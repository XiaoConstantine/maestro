package orchestration

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
	"github.com/XiaoConstantine/maestro/internal/search"
)

func TestShouldFallbackToLegacyQA(t *testing.T) {
	if !shouldFallbackToLegacyQA(fmt.Errorf("LLMGenerationFailed [model=gemini-3-pro-preview statusCode=400 ]")) {
		t.Fatalf("expected Gemini 3 400 error to trigger fallback")
	}
	if shouldFallbackToLegacyQA(fmt.Errorf("statusCode=500 model=gemini-3-pro-preview")) {
		t.Fatalf("unexpected fallback for non-400 Gemini error")
	}
	if shouldFallbackToLegacyQA(fmt.Errorf("statusCode=400 model=gpt-4.1")) {
		t.Fatalf("unexpected fallback for non-Gemini model")
	}
}

func TestExtractAnswerAndSources(t *testing.T) {
	response := &search.SearchResponse{
		Results: []*search.EnhancedSearchResult{
			{
				SearchResult: &search.SearchResult{
					FilePath: "phase-1",
					Line:     "short answer",
				},
			},
			{
				SearchResult: &search.SearchResult{
					FilePath: "phase-2",
					Line:     "longer synthesized answer",
				},
			},
			{
				SearchResult: &search.SearchResult{
					FilePath: "pkg/core/agent.go",
				},
			},
			{
				SearchResult: &search.SearchResult{
					FilePath: "pkg/core/agent.go",
				},
			},
			{
				SearchResult: &search.SearchResult{
					FilePath: "pkg/modules/react.go",
				},
			},
		},
	}

	answer, sources := extractAnswerAndSources(response)
	if answer != "longer synthesized answer" {
		t.Fatalf("answer = %q, want longest synthesized answer", answer)
	}
	wantSources := []string{"pkg/core/agent.go", "pkg/modules/react.go"}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Fatalf("sources = %#v, want %#v", sources, wantSources)
	}
}

func TestBestPersistedSkillVersion(t *testing.T) {
	store := skills.NewMemoryStore()
	if err := store.Save(context.Background(), skills.Skill{
		Name:    "qa-v1",
		Domain:  "maestro:qa",
		Content: "Prefer narrow repo reads.",
		Version: 1,
	}); err != nil {
		t.Fatalf("save v1 skill: %v", err)
	}
	if err := store.Save(context.Background(), skills.Skill{
		Name:    "qa-v2",
		Domain:  "maestro:qa",
		Content: "Prefer exact symbol lookups first.",
		Version: 2,
	}); err != nil {
		t.Fatalf("save v2 skill: %v", err)
	}

	version, err := bestPersistedSkillVersion(context.Background(), store, "maestro:qa")
	if err != nil {
		t.Fatalf("bestPersistedSkillVersion() error = %v", err)
	}
	if version != 2 {
		t.Fatalf("bestPersistedSkillVersion() = %d, want 2", version)
	}

	emptyVersion, err := bestPersistedSkillVersion(context.Background(), store, "maestro:missing")
	if err != nil {
		t.Fatalf("bestPersistedSkillVersion() missing error = %v", err)
	}
	if emptyVersion != 0 {
		t.Fatalf("bestPersistedSkillVersion() missing = %d, want 0", emptyVersion)
	}
}
