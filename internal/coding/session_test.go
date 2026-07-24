package coding

import (
	"context"
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
