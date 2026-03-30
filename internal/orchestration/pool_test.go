package orchestration

import (
	"fmt"
	"reflect"
	"testing"

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
