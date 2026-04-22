package subagent

import (
	"context"
	"maps"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

type stubTaskProcessor struct {
	response string
	seenTask agents.Task
	seenCtx  map[string]interface{}
}

func (p *stubTaskProcessor) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	p.seenTask = task
	p.seenCtx = maps.Clone(taskContext)
	return map[string]interface{}{
		"response":  p.response,
		"completed": true,
	}, nil
}

func TestProcessorToolUsesDerivedSessionAndRepoContext(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessionevent.db"))
	if err != nil {
		t.Fatalf("NewSQLiteSessionStore() error = %v", err)
	}
	defer store.Close()

	manager, err := NewSessionManager(
		filepath.Join(t.TempDir(), "sessions"),
		logging.GetLogger(),
		WithSessionEventStore(store),
	)
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}

	processor := &stubTaskProcessor{response: "delegated answer"}
	var seenSessionDir string
	tool, err := newProcessorTool(processorToolConfig{
		name:            "claude",
		description:     "Delegated claude worker.",
		parentSessionID: "parent-session",
		staticInput: map[string]any{
			"repo_path": "/tmp/repo",
			"owner":     "XiaoConstantine",
			"repo":      "maestro",
		},
		logger:         logging.GetLogger(),
		sessionManager: manager,
		factory: func(sessionDir string) (agents.TaskProcessor, error) {
			seenSessionDir = sessionDir
			return processor, nil
		},
	})
	if err != nil {
		t.Fatalf("newProcessorTool() error = %v", err)
	}

	result, err := tool.Execute(context.Background(), map[string]any{
		"prompt":    "Review the auth flow",
		"task_type": "review",
	})
	if err != nil {
		t.Fatalf("tool.Execute() error = %v", err)
	}

	session, err := manager.GetSession("parent-session/claude")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if seenSessionDir != session.Dir {
		t.Fatalf("factory saw session dir %q, want %q", seenSessionDir, session.Dir)
	}
	if processor.seenTask.Metadata["prompt"] != "Review the auth flow" {
		t.Fatalf("prompt = %v, want prompt preserved", processor.seenTask.Metadata["prompt"])
	}
	if processor.seenTask.Metadata["type"] != "review" {
		t.Fatalf("task type = %v, want review", processor.seenTask.Metadata["type"])
	}
	if !reflect.DeepEqual(processor.seenCtx, map[string]interface{}{
		"repo_path": "/tmp/repo",
		"owner":     "XiaoConstantine",
		"repo":      "maestro",
	}) {
		t.Fatalf("task context = %#v", processor.seenCtx)
	}

	modelText, _ := result.Metadata[core.ToolResultModelTextMeta].(string)
	if modelText != "delegated answer" {
		t.Fatalf("model text = %q, want delegated answer", modelText)
	}
	details, _ := result.Annotations[core.ToolResultDetailsAnnotation].(map[string]any)
	if details["subagent_name"] != "claude" {
		t.Fatalf("details = %#v, want subagent_name claude", details)
	}
}
