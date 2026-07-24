package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

func TestMapCodingEventMapsToolLifecycle(t *testing.T) {
	event, ok := mapCodingEvent(agents.ExecutionEvent{Payload: agents.ToolExecutionStartedEvent{
		Call: core.ToolCall{Name: "edit"},
	}})
	if !ok {
		t.Fatal("mapCodingEvent() ok = false")
	}
	if event.Kind != "tool" || event.Tool != "edit" || event.Status != "started" {
		t.Fatalf("event = %#v, want started edit tool", event)
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
