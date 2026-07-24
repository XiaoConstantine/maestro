package orchestration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	maestrocoding "github.com/XiaoConstantine/maestro/internal/coding"
	"github.com/XiaoConstantine/maestro/internal/types"
)

func TestBuildCodingSystemPromptIncludesIdentityWorkspaceAndEvidenceRules(t *testing.T) {
	llm := &capturingCodingLLM{capabilities: []core.Capability{core.CapabilityCompletion, core.CapabilityToolCalling}}
	prompt := buildCodingSystemPrompt(llm, "/workspace/project", "XiaoConstantine", "maestro")
	for _, want := range []string{
		"Maestro's workspace coding agent",
		"Provider/model: capture / capture-model",
		"Workspace: /workspace/project",
		"Repository: XiaoConstantine/maestro",
		"After any write or edit, verify the mutation",
		"Do not claim a file was created, updated, or fixed unless the trace includes that mutation and a post-mutation verification step.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("buildCodingSystemPrompt() missing %q in %q", want, prompt)
		}
	}
}

func TestCodingSessionIDUsesReservedCollisionResistantNamespace(t *testing.T) {
	first := codingSessionID("alpha")
	second := codingSessionID("alpha-coding")
	if !strings.HasPrefix(first, codingSessionNamespace) {
		t.Fatalf("codingSessionID(alpha) = %q, want reserved prefix", first)
	}
	if first == "alpha" || first == "alpha-coding" {
		t.Fatalf("codingSessionID(alpha) = %q, collides with user-derived name", first)
	}
	if first == second {
		t.Fatalf("coding session IDs collide: %q", first)
	}
}

func TestCodingSessionReplacementInvalidatesClosedCacheOnConstructionFailure(t *testing.T) {
	llm := &capturingBenchmarkLLM{capabilities: []core.Capability{core.CapabilityCompletion, core.CapabilityToolCalling}}
	workspace := t.TempDir()
	oldSession, err := maestrocoding.NewSession(maestrocoding.Config{LLM: llm, Workspace: workspace})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	service := &MaestroService{
		config:          &ServiceConfig{},
		currentSession:  "alpha",
		codingSession:   oldSession,
		codingWorkspace: workspace,
		codingSessionID: codingSessionID("alpha"),
	}

	previousLLM := core.GlobalConfig.DefaultLLM
	t.Cleanup(func() { core.GlobalConfig.DefaultLLM = previousLLM })
	core.GlobalConfig.DefaultLLM = nil
	if _, err := service.codingSessionFor(context.Background(), t.TempDir()); err == nil {
		t.Fatal("codingSessionFor() error = nil with no default LLM")
	}
	core.GlobalConfig.DefaultLLM = llm

	replacement, err := service.codingSessionFor(context.Background(), workspace)
	if err != nil {
		t.Fatalf("codingSessionFor() retry error = %v", err)
	}
	if replacement == oldSession {
		t.Fatal("codingSessionFor() reused terminally closed session")
	}
}

func TestRepositoryWorkspacePrefersConfiguredRootOverReviewClone(t *testing.T) {
	service := &MaestroService{config: &ServiceConfig{WorkspaceRoot: "/authoritative/workspace"}, pool: &AgentPool{reviewAgent: &workspacePathReviewAgent{path: "/tmp/hidden-clone"}}}
	if got := service.repositoryWorkspace(context.Background()); got != "/authoritative/workspace" {
		t.Fatalf("repositoryWorkspace() = %q, want authoritative workspace", got)
	}
}

func TestCodingSessionAdmissionRejectsWorkAfterShutdownStarts(t *testing.T) {
	service := &MaestroService{
		config:         &ServiceConfig{},
		currentSession: "alpha",
		pool:           &AgentPool{},
		logger:         logging.GetLogger(),
	}
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if _, err := service.codingSessionFor(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("codingSessionFor() error = %v, want shutdown rejection", err)
	}
}

func TestShutdownStopsCodingBeforeClosingWorkspaceOwner(t *testing.T) {
	llm := newBlockingCodingLLM(false)
	session, err := maestrocoding.NewSession(maestrocoding.Config{LLM: llm, Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	var closedBeforeStop atomic.Bool
	reviewAgent := &shutdownOrderReviewAgent{
		budgetAwareReviewAgent: &budgetAwareReviewAgent{},
		onClose: func() {
			if !llm.exited.Load() {
				closedBeforeStop.Store(true)
			}
		},
	}
	service := &MaestroService{
		pool:          &AgentPool{reviewAgent: reviewAgent},
		codingSession: session,
		logger:        logging.GetLogger(),
	}
	done := make(chan error, 1)
	go func() {
		_, err := session.Prompt(context.Background(), "block", nil)
		done <- err
	}()
	<-llm.started

	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if closedBeforeStop.Load() {
		t.Fatal("review workspace owner closed before coding model exited")
	}
	<-done
}

func TestShutdownTimeoutSkipsWorkspaceOwnerClose(t *testing.T) {
	llm := newBlockingCodingLLM(true)
	session, err := maestrocoding.NewSession(maestrocoding.Config{LLM: llm, Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	var reviewClosed atomic.Bool
	service := &MaestroService{
		pool: &AgentPool{reviewAgent: &shutdownOrderReviewAgent{
			budgetAwareReviewAgent: &budgetAwareReviewAgent{},
			onClose:                func() { reviewClosed.Store(true) },
		}},
		codingSession: session,
		logger:        logging.GetLogger(),
	}
	done := make(chan error, 1)
	go func() {
		_, err := session.Prompt(context.Background(), "resist cancellation", nil)
		done <- err
	}()
	<-llm.started

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := service.Shutdown(ctx); err == nil {
		t.Fatal("Shutdown() error = nil, want timeout")
	}
	if reviewClosed.Load() {
		t.Fatal("review workspace owner closed after coding close timed out")
	}
	close(llm.release)
	<-done
}

type blockingCodingLLM struct {
	*capturingBenchmarkLLM
	started      chan struct{}
	release      chan struct{}
	ignoreCancel bool
	exited       atomic.Bool
}

func newBlockingCodingLLM(ignoreCancel bool) *blockingCodingLLM {
	return &blockingCodingLLM{
		capturingBenchmarkLLM: &capturingBenchmarkLLM{capabilities: []core.Capability{core.CapabilityCompletion, core.CapabilityToolCalling}},
		started:               make(chan struct{}),
		release:               make(chan struct{}),
		ignoreCancel:          ignoreCancel,
	}
}

func (l *blockingCodingLLM) GenerateWithFunctions(ctx context.Context, _ string, _ []map[string]interface{}, _ ...core.GenerateOption) (map[string]interface{}, error) {
	close(l.started)
	if l.ignoreCancel {
		<-l.release
	} else {
		<-ctx.Done()
	}
	l.exited.Store(true)
	return nil, ctx.Err()
}

type workspacePathReviewAgent struct{ path string }

func (a *workspacePathReviewAgent) ReviewPR(context.Context, int, []types.PRReviewTask, types.ConsoleInterface) ([]types.PRReviewComment, error) {
	return nil, nil
}
func (a *workspacePathReviewAgent) ReviewPRWithChanges(context.Context, int, []types.PRReviewTask, types.ConsoleInterface, *types.PRChanges) ([]types.PRReviewComment, error) {
	return nil, nil
}
func (*workspacePathReviewAgent) Stop(context.Context)                               {}
func (*workspacePathReviewAgent) Metrics(context.Context) types.MetricsCollector     { return nil }
func (a *workspacePathReviewAgent) ClonedRepoPath() string                           { return a.path }
func (*workspacePathReviewAgent) WaitForClone(context.Context, time.Duration) string { return "" }
func (*workspacePathReviewAgent) GetIndexingStatus() *types.IndexingStatus           { return nil }
func (*workspacePathReviewAgent) Close() error                                       { return nil }

type capturingCodingLLM struct {
	mu           sync.Mutex
	results      []map[string]any
	index        int
	prompts      []string
	capabilities []core.Capability
}

func (m *capturingCodingLLM) Generate(context.Context, string, ...core.GenerateOption) (*core.LLMResponse, error) {
	return nil, fmt.Errorf("unexpected Generate call")
}
func (m *capturingCodingLLM) GenerateWithJSON(context.Context, string, ...core.GenerateOption) (map[string]any, error) {
	return nil, fmt.Errorf("unexpected GenerateWithJSON call")
}
func (m *capturingCodingLLM) GenerateWithFunctions(_ context.Context, prompt string, _ []map[string]any, _ ...core.GenerateOption) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prompts = append(m.prompts, prompt)
	if m.index >= len(m.results) {
		return nil, fmt.Errorf("no scripted result")
	}
	result := m.results[m.index]
	m.index++
	return result, nil
}
func (m *capturingCodingLLM) CreateEmbedding(context.Context, string, ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	return nil, fmt.Errorf("unexpected CreateEmbedding call")
}
func (m *capturingCodingLLM) CreateEmbeddings(context.Context, []string, ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	return nil, fmt.Errorf("unexpected CreateEmbeddings call")
}
func (m *capturingCodingLLM) StreamGenerate(context.Context, string, ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("unexpected StreamGenerate call")
}
func (m *capturingCodingLLM) GenerateWithContent(context.Context, []core.ContentBlock, ...core.GenerateOption) (*core.LLMResponse, error) {
	return nil, fmt.Errorf("unexpected GenerateWithContent call")
}
func (m *capturingCodingLLM) StreamGenerateWithContent(context.Context, []core.ContentBlock, ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("unexpected StreamGenerateWithContent call")
}
func (*capturingCodingLLM) ProviderName() string { return "capture" }
func (*capturingCodingLLM) ModelID() string      { return "capture-model" }
func (m *capturingCodingLLM) Capabilities() []core.Capability {
	if len(m.capabilities) == 0 {
		return []core.Capability{core.CapabilityCompletion, core.CapabilityToolCalling}
	}
	return m.capabilities
}

type shutdownOrderReviewAgent struct {
	*budgetAwareReviewAgent
	onClose func()
}

func (a *shutdownOrderReviewAgent) Close() error {
	if a.onClose != nil {
		a.onClose()
	}
	return nil
}

func TestHandleCodingWritesToConfiguredWorkspaceAndCarriesInstructions(t *testing.T) {
	workspace := t.TempDir()
	hiddenClone := t.TempDir()
	llm := &capturingCodingLLM{results: []map[string]any{
		{"function_call": map[string]any{"name": "write", "arguments": map[string]any{"path": "who_are_you.txt", "content": "visible workspace\n"}}},
		{"function_call": map[string]any{"name": "read", "arguments": map[string]any{"path": "who_are_you.txt"}}},
		{"function_call": map[string]any{"name": "Finish", "arguments": map[string]any{"answer": "Created and verified who_are_you.txt in the workspace"}}},
	}}
	previousLLM := core.GlobalConfig.DefaultLLM
	core.GlobalConfig.DefaultLLM = llm
	defer func() { core.GlobalConfig.DefaultLLM = previousLLM }()

	session, err := maestrocoding.NewSession(maestrocoding.Config{
		LLM: llm, Workspace: workspace, SystemPrompt: buildCodingSystemPrompt(llm, workspace, "XiaoConstantine", "maestro"),
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	service := &MaestroService{
		config:          &ServiceConfig{WorkspaceRoot: workspace, Owner: "XiaoConstantine", Repo: "maestro"},
		pool:            &AgentPool{reviewAgent: &workspacePathReviewAgent{path: hiddenClone}},
		codingSession:   session,
		codingWorkspace: workspace,
		codingSessionID: codingSessionID(""),
	}
	response, err := service.ProcessRequest(context.Background(), Request{Type: RequestCoding, Prompt: "Create who_are_you.txt and verify it"})
	if err != nil {
		t.Fatalf("ProcessRequest() error = %v", err)
	}
	if got := response.Metadata["workspace"]; got != workspace {
		t.Fatalf("workspace metadata = %v, want %q", got, workspace)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "who_are_you.txt"))
	if err != nil {
		t.Fatalf("ReadFile(workspace) error = %v", err)
	}
	if string(data) != "visible workspace\n" {
		t.Fatalf("workspace file = %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(hiddenClone, "who_are_you.txt")); !os.IsNotExist(err) {
		t.Fatalf("hidden clone stat error = %v, want file absent", err)
	}
	trace, _ := response.Metadata["trace"].(*agents.ExecutionTrace)
	if trace == nil || len(trace.Steps) < 2 || trace.Steps[0].Tool != "write" || trace.Steps[1].Tool != "read" {
		t.Fatalf("trace steps = %#v, want write then read verification", trace)
	}
	if len(llm.prompts) == 0 {
		t.Fatal("LLM prompts = nil, want coding prompt")
	}
	prompt := llm.prompts[0]
	for _, want := range []string{workspace, "Provider/model: capture / capture-model", "After any write or edit, verify the mutation"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("first coding prompt missing %q in %q", want, prompt)
		}
	}
}

func TestCodingAnswerSurfacesStoppedRunDiagnostic(t *testing.T) {
	answer, err := codingAnswer(agents.AgentExecutionResult{
		Output: map[string]any{"completed": false, "error": "max turns reached without completion"},
		Trace: &agents.ExecutionTrace{
			Status:           agents.TraceStatusPartial,
			TerminationCause: "max_turns",
		},
	})
	if err != nil {
		t.Fatalf("codingAnswer() error = %v", err)
	}
	if !strings.Contains(answer, "max turns reached") {
		t.Fatalf("codingAnswer() = %q, want max-turn diagnostic", answer)
	}
}

func TestCodingAnswerSurfacesPartialTraceWithoutOutputError(t *testing.T) {
	answer, err := codingAnswer(agents.AgentExecutionResult{Trace: &agents.ExecutionTrace{
		Status:           agents.TraceStatusPartial,
		TerminationCause: "stopped",
	}})
	if err != nil {
		t.Fatalf("codingAnswer() error = %v", err)
	}
	if answer != "Coding run stopped: stopped" {
		t.Fatalf("codingAnswer() = %q, want visible stop diagnostic", answer)
	}
}
