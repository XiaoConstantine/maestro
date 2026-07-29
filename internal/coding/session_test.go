package coding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

func TestSessionPromptRunsWorkspaceToolsAndEmitsLifecycle(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("# fixture\n"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	llm := &scriptedLLM{results: []map[string]any{
		{"function_call": map[string]any{"name": "read", "arguments": map[string]any{"path": "README.md"}}},
		{"function_call": map[string]any{"name": "Finish", "arguments": map[string]any{"answer": "README inspected"}}},
	}}
	session, err := NewSession(Config{LLM: llm, Workspace: workspace, SessionID: "coding-test"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	var events []agents.ExecutionEvent
	result, err := session.Prompt(context.Background(), "Inspect README.md", agents.EventSinkFunc(func(_ context.Context, event agents.ExecutionEvent) {
		events = append(events, event)
	}))
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if got := result.Output["final_answer"]; got != "README inspected" {
		t.Fatalf("final answer = %#v, want README inspected", got)
	}
	if result.Trace == nil || result.Trace.Status != agents.TraceStatusSuccess {
		t.Fatalf("trace = %#v, want successful trace", result.Trace)
	}
	if len(result.Trace.Steps) == 0 || result.Trace.Steps[0].Tool != "read" {
		t.Fatalf("trace steps = %#v, want read tool", result.Trace.Steps)
	}
	if len(events) == 0 {
		t.Fatal("events = nil, want typed lifecycle events")
	}
	if _, ok := events[0].Payload.(agents.RunStartedEvent); !ok {
		t.Fatalf("first event = %T, want RunStartedEvent", events[0].Payload)
	}
	if _, ok := events[len(events)-1].Payload.(agents.RunFinishedEvent); !ok {
		t.Fatalf("last event = %T, want RunFinishedEvent", events[len(events)-1].Payload)
	}
}

func TestSessionRunsInjectedExecutor(t *testing.T) {
	executor := &recordingExecutor{}
	var factoryWorkspace string
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	session, err := NewSessionWithExecutor(root+string(os.PathSeparator)+".", func(workspace string) (Executor, error) {
		factoryWorkspace = workspace
		return executor, nil
	})
	if err != nil {
		t.Fatalf("NewSessionWithExecutor() error = %v", err)
	}
	if factoryWorkspace != canonicalRoot {
		t.Fatalf("factory workspace = %q, want canonical root %q", factoryWorkspace, canonicalRoot)
	}
	if session.Workspace() != canonicalRoot {
		t.Fatalf("session workspace = %q, want canonical root %q", session.Workspace(), canonicalRoot)
	}

	eventCount := 0
	result, err := session.Prompt(context.Background(), "delegate this task", agents.EventSinkFunc(func(context.Context, agents.ExecutionEvent) {
		eventCount++
	}))
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if got := executor.input["task"]; got != "delegate this task" {
		t.Fatalf("executor task = %#v, want delegate this task", got)
	}
	if got := result.Output["final_answer"]; got != "injected executor" {
		t.Fatalf("final answer = %#v, want injected executor", got)
	}
	if eventCount != 1 {
		t.Fatalf("event count = %d, want 1", eventCount)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !executor.closed {
		t.Fatal("executor was not closed")
	}
}

func TestZeroValueSessionCloseIsSafe(t *testing.T) {
	var session Session
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestSessionCloseIsTerminal(t *testing.T) {
	session, err := NewSession(Config{LLM: &scriptedLLM{}, Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := session.Prompt(context.Background(), "after close", nil); err != ErrSessionClosed {
		t.Fatalf("Prompt() error = %v, want %v", err, ErrSessionClosed)
	}
}

func TestSessionCloseContinuesExecutorCleanupAfterTimeout(t *testing.T) {
	executor := &blockingExecutor{
		started: make(chan struct{}),
		release: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	session, err := NewSessionWithExecutor(t.TempDir(), func(string) (Executor, error) {
		return executor, nil
	})
	if err != nil {
		t.Fatalf("NewSessionWithExecutor() error = %v", err)
	}

	promptDone := make(chan error, 1)
	go func() {
		_, promptErr := session.Prompt(context.Background(), "block", nil)
		promptDone <- promptErr
	}()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := session.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v, want deadline exceeded", err)
	}
	select {
	case <-executor.closed:
		t.Fatal("executor closed before active run finished")
	default:
	}
	close(executor.release)
	select {
	case <-executor.closed:
	case <-time.After(time.Second):
		t.Fatal("executor was not closed after active run finished")
	}
	if err := <-promptDone; err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
}

func TestSessionRequiresExplicitBashOptIn(t *testing.T) {
	tests := []struct {
		name      string
		allowBash bool
		wantError bool
	}{
		{name: "disabled by default", wantError: true},
		{name: "explicitly enabled", allowBash: true, wantError: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &scriptedLLM{results: []map[string]any{
				{"function_call": map[string]any{"name": "bash", "arguments": map[string]any{"command": "printf ok"}}},
				{"function_call": map[string]any{"name": "Finish", "arguments": map[string]any{"answer": "done"}}},
			}}
			session, err := NewSession(Config{LLM: llm, Workspace: t.TempDir(), AllowBash: tt.allowBash})
			if err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}
			result, err := session.Prompt(context.Background(), "run shell", nil)
			if err != nil {
				t.Fatalf("Prompt() error = %v", err)
			}
			if result.Trace == nil || len(result.Trace.Steps) == 0 {
				t.Fatalf("trace = %#v, want bash step", result.Trace)
			}
			gotError := result.Trace.Steps[0].Error != ""
			if gotError != tt.wantError {
				t.Fatalf("bash step error = %q, wantError=%v", result.Trace.Steps[0].Error, tt.wantError)
			}
		})
	}
}

func TestSessionRejectsOverlappingRunsAndCancels(t *testing.T) {
	llm := &scriptedLLM{block: true, started: make(chan struct{})}
	session, err := NewSession(Config{LLM: llm, Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := session.Prompt(context.Background(), "wait", nil)
		done <- err
	}()
	select {
	case <-llm.started:
	case <-time.After(time.Second):
		t.Fatal("model did not start")
	}

	if _, err := session.Prompt(context.Background(), "overlap", nil); err != ErrRunActive {
		t.Fatalf("overlapping Prompt() error = %v, want %v", err, ErrRunActive)
	}
	if !session.Cancel() {
		t.Fatal("Cancel() = false, want true")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled Prompt() error = nil")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled prompt did not return")
	}
}

type blockingExecutor struct {
	started chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (e *blockingExecutor) ExecuteWithTrace(
	context.Context,
	map[string]any,
	agents.EventSink,
) (agents.AgentExecutionResult, error) {
	close(e.started)
	<-e.release
	return agents.AgentExecutionResult{}, nil
}

func (e *blockingExecutor) Close(context.Context) error {
	close(e.closed)
	return nil
}

var _ Executor = (*blockingExecutor)(nil)

type recordingExecutor struct {
	input  map[string]any
	closed bool
}

func (e *recordingExecutor) ExecuteWithTrace(
	ctx context.Context,
	input map[string]any,
	sink agents.EventSink,
) (agents.AgentExecutionResult, error) {
	e.input = input
	if sink != nil {
		sink.EmitEvent(ctx, agents.ExecutionEvent{})
	}
	return agents.AgentExecutionResult{Output: map[string]any{"final_answer": "injected executor"}}, nil
}

func (e *recordingExecutor) Close(context.Context) error {
	e.closed = true
	return nil
}

var _ Executor = (*recordingExecutor)(nil)

type scriptedLLM struct {
	mu        sync.Mutex
	results   []map[string]any
	index     int
	block     bool
	started   chan struct{}
	startOnce sync.Once
}

func (m *scriptedLLM) Generate(context.Context, string, ...core.GenerateOption) (*core.LLMResponse, error) {
	return nil, fmt.Errorf("unexpected Generate call")
}
func (m *scriptedLLM) GenerateWithJSON(context.Context, string, ...core.GenerateOption) (map[string]any, error) {
	return nil, fmt.Errorf("unexpected GenerateWithJSON call")
}
func (m *scriptedLLM) GenerateWithFunctions(ctx context.Context, _ string, _ []map[string]any, _ ...core.GenerateOption) (map[string]any, error) {
	if m.block {
		m.startOnce.Do(func() {
			if m.started != nil {
				close(m.started)
			}
		})
		<-ctx.Done()
		return nil, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.index >= len(m.results) {
		return nil, fmt.Errorf("no scripted result")
	}
	result := m.results[m.index]
	m.index++
	return result, nil
}
func (m *scriptedLLM) CreateEmbedding(context.Context, string, ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	return nil, fmt.Errorf("unexpected CreateEmbedding call")
}
func (m *scriptedLLM) CreateEmbeddings(context.Context, []string, ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	return nil, fmt.Errorf("unexpected CreateEmbeddings call")
}
func (m *scriptedLLM) StreamGenerate(context.Context, string, ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("unexpected StreamGenerate call")
}
func (m *scriptedLLM) GenerateWithContent(context.Context, []core.ContentBlock, ...core.GenerateOption) (*core.LLMResponse, error) {
	return nil, fmt.Errorf("unexpected GenerateWithContent call")
}
func (m *scriptedLLM) StreamGenerateWithContent(context.Context, []core.ContentBlock, ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("unexpected StreamGenerateWithContent call")
}
func (m *scriptedLLM) ProviderName() string { return "scripted" }
func (m *scriptedLLM) ModelID() string      { return "scripted-model" }
func (m *scriptedLLM) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityCompletion, core.CapabilityToolCalling}
}
