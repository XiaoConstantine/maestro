package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

func TestMapCodingEventMapsToolLifecycle(t *testing.T) {
	at := time.Now()
	event, ok := mapCodingEvent(agents.ExecutionEvent{Timestamp: at, Payload: agents.ToolExecutionStartedEvent{
		RunID: "run-1", Turn: 2, ToolIndex: 3,
		Call: core.ToolCall{Name: "edit", Arguments: map[string]any{"path": "main.go"}},
	}})
	if !ok {
		t.Fatal("mapCodingEvent() ok = false")
	}
	if event.Kind != "tool" || event.Tool != "edit" || event.Status != "started" || event.Detail != "Running edit main.go" ||
		event.RunID != "run-1" || event.Turn != 2 || event.ToolIndex != 3 || !event.At.Equal(at) {
		t.Fatalf("event = %#v, want started edit tool with path detail", event)
	}
}

func TestMapCodingEventMapsToolResultDetails(t *testing.T) {
	event, ok := mapCodingEvent(agents.ExecutionEvent{Payload: agents.ToolCallFinishedEvent{
		Call:    core.ToolCall{Name: "write"},
		Status:  agents.OperationStatusCompleted,
		Outcome: agents.ToolCallOutcomeExecuted,
		Result: &agents.Message{ToolResult: &agents.MessageToolResult{
			Content:        []core.ContentBlock{core.NewTextBlock("model-visible output")},
			DisplayContent: []core.ContentBlock{core.NewTextBlock("wrote 12 bytes to who_are_you.txt")},
			Details:        map[string]any{"path": "who_are_you.txt"},
		}},
	}})
	if !ok {
		t.Fatal("mapCodingEvent() ok = false")
	}
	if event.Detail != "who_are_you.txt — wrote 12 bytes to who_are_you.txt" || event.Outcome != "executed" {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapCodingEventFallsBackToModelContentWhenDisplayContentMissing(t *testing.T) {
	event, ok := mapCodingEvent(agents.ExecutionEvent{Payload: agents.ToolCallFinishedEvent{
		Call:   core.ToolCall{Name: "read"},
		Status: agents.OperationStatusCompleted,
		Result: &agents.Message{ToolResult: &agents.MessageToolResult{
			Content: []core.ContentBlock{core.NewTextBlock("fallback model-visible output")},
			Details: map[string]any{"path": "README.md"},
		}},
	}})
	if !ok {
		t.Fatal("mapCodingEvent() ok = false")
	}
	if event.Detail != "README.md — fallback model-visible output" {
		t.Fatalf("event.Detail = %q", event.Detail)
	}
}

func TestMapCodingEventPreservesExplicitlyEmptyDisplayContent(t *testing.T) {
	event, ok := mapCodingEvent(agents.ExecutionEvent{Payload: agents.ToolCallFinishedEvent{
		Call:   core.ToolCall{Name: "read"},
		Status: agents.OperationStatusCompleted,
		Result: &agents.Message{ToolResult: &agents.MessageToolResult{
			Content:        []core.ContentBlock{core.NewTextBlock("model-only secret")},
			DisplayContent: []core.ContentBlock{},
			Details:        map[string]any{"path": "secret.txt"},
		}},
	}})
	if !ok {
		t.Fatal("mapCodingEvent() ok = false")
	}
	if event.Detail != "secret.txt" {
		t.Fatalf("event.Detail = %q, want only path when display content is intentionally empty", event.Detail)
	}
}

func TestMapCodingEventPreservesRunSummary(t *testing.T) {
	at := time.Now()
	event, ok := mapCodingEvent(agents.ExecutionEvent{Timestamp: at, Payload: agents.RunFinishedEvent{
		RunID: "run-1", Status: agents.RunStatusCompleted, Turns: 3, ToolCalls: 5,
	}})
	if !ok {
		t.Fatal("mapCodingEvent() ok = false")
	}
	if event.Kind != "run" || event.Status != "completed" || event.RunID != "run-1" ||
		event.Turn != 3 || event.ToolCalls != 5 || !event.At.Equal(at) {
		t.Fatalf("event = %#v, want complete run summary", event)
	}
}

func TestMapCodingEventIgnoresMessageAdded(t *testing.T) {
	if _, ok := mapCodingEvent(agents.ExecutionEvent{Payload: agents.MessageAddedEvent{}}); ok {
		t.Fatal("mapCodingEvent() ok = true, want false")
	}
}

func TestResolveCLIStoragePath_DirectoryPathUsesRepoDBName(t *testing.T) {
	cfg := &config{
		owner:      "XiaoConstantine",
		repo:       "maestro",
		memoryPath: filepath.Join(t.TempDir(), "state") + string(filepath.Separator),
	}

	got, err := resolveCLIStoragePath(context.Background(), cfg)
	if err != nil {
		t.Fatalf("resolveCLIStoragePath() error = %v", err)
	}

	want := filepath.Join(cfg.memoryPath, "XiaoConstantine_maestro.db")
	if got != want {
		t.Fatalf("resolveCLIStoragePath() = %q, want %q", got, want)
	}
}

func TestResolveCLIStoragePath_FilePathPreserved(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom.db")
	cfg := &config{
		owner:      "XiaoConstantine",
		repo:       "maestro",
		memoryPath: want,
	}

	got, err := resolveCLIStoragePath(context.Background(), cfg)
	if err != nil {
		t.Fatalf("resolveCLIStoragePath() error = %v", err)
	}
	if got != want {
		t.Fatalf("resolveCLIStoragePath() = %q, want %q", got, want)
	}
}
