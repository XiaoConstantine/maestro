package orchestration

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	maestrocoding "github.com/XiaoConstantine/maestro/internal/coding"
)

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
