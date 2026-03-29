package orchestration

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/native"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

func TestBuildNativeSearchToolsSchemas(t *testing.T) {
	tools := buildNativeSearchTools(t.TempDir(), logging.GetLogger())

	required := map[string][]string{
		"search_files":    {"pattern"},
		"search_content":  {"query"},
		"read_file":       {"file_path"},
		"semantic_search": {"query"},
	}

	for _, tool := range tools {
		requiredArgs, ok := required[tool.Name()]
		if !ok {
			continue
		}

		schema := tool.InputSchema()
		if schema.Type != "object" {
			t.Fatalf("%s schema type = %q, want object", tool.Name(), schema.Type)
		}
		for _, arg := range requiredArgs {
			param, ok := schema.Properties[arg]
			if !ok {
				t.Fatalf("%s missing schema property %q", tool.Name(), arg)
			}
			if !param.Required {
				t.Fatalf("%s schema property %q should be required", tool.Name(), arg)
			}
		}
	}
}

func TestExtractSourcesFromNativeTrace(t *testing.T) {
	trace := &native.Trace{
		Completed: true,
		Steps: []native.TraceStep{
			{
				ToolName: "search_files",
				ObservationDetails: map[string]any{
					"files": []string{"README.md", "internal/orchestration/pool.go"},
				},
			},
			{
				ToolName: "search_content",
				ObservationDetails: map[string]any{
					"results": []map[string]any{
						{"file_path": "internal/orchestration/pool.go", "line_number": 12},
						{"file_path": "internal/orchestration/native_qa.go", "line_number": 44},
					},
				},
			},
			{
				ToolName: "read_file",
				Arguments: map[string]any{
					"path": "internal/orchestration/native_qa.go",
				},
			},
		},
	}

	got := extractSourcesFromNativeTrace(trace)
	want := []string{
		"README.md",
		"internal/orchestration/pool.go",
		"internal/orchestration/native_qa.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractSourcesFromNativeTrace() = %#v, want %#v", got, want)
	}
}

func TestNativeSearchToolsExecutionAnnotations(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Maestro\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "pkg"), 0755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "pkg", "foo.go"), []byte("package pkg\n\nfunc Foo() {}\n"), 0644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}

	tools := buildNativeSearchTools(repoDir, logging.GetLogger())
	toolByName := map[string]core.Tool{}
	for _, tool := range tools {
		toolByName[tool.Name()] = tool
	}

	ctx := context.Background()

	searchFilesResult, err := toolByName["search_files"].Execute(ctx, map[string]interface{}{
		"pattern": "README",
	})
	if err != nil {
		t.Fatalf("search_files execute: %v", err)
	}
	searchFilesDetails, _ := searchFilesResult.Annotations[core.ToolResultDetailsAnnotation].(map[string]any)
	searchFilesList, _ := searchFilesDetails["files"].([]string)
	if !reflect.DeepEqual(searchFilesList, []string{"README.md"}) {
		t.Fatalf("search_files details = %#v, want README.md", searchFilesDetails)
	}

	searchContentResult, err := toolByName["search_content"].Execute(ctx, map[string]interface{}{
		"query": "Foo",
	})
	if err != nil {
		t.Fatalf("search_content execute: %v", err)
	}
	searchContentDetails, _ := searchContentResult.Annotations[core.ToolResultDetailsAnnotation].(map[string]any)
	searchContentFiles, _ := searchContentDetails["files"].([]string)
	if !reflect.DeepEqual(searchContentFiles, []string{"pkg/foo.go"}) {
		t.Fatalf("search_content files = %#v, want pkg/foo.go", searchContentDetails)
	}

	readFileResult, err := toolByName["read_file"].Execute(ctx, map[string]interface{}{
		"file_path": "pkg/foo.go",
	})
	if err != nil {
		t.Fatalf("read_file execute: %v", err)
	}
	readFileDetails, _ := readFileResult.Annotations[core.ToolResultDetailsAnnotation].(map[string]any)
	if readFileDetails["file_path"] != "pkg/foo.go" {
		t.Fatalf("read_file details = %#v, want file_path pkg/foo.go", readFileDetails)
	}
}
