package orchestration

import (
	"strings"
	"testing"
)

func TestAnalyzeQAQueryCodeFocused(t *testing.T) {
	analysis := analyzeQAQuery("Where is NewAgent implemented and how does the function work?")
	if analysis.PrimaryType != qaQueryTypeCode {
		t.Fatalf("PrimaryType = %q, want %q", analysis.PrimaryType, qaQueryTypeCode)
	}
	if len(analysis.RequiredTools) == 0 || analysis.RequiredTools[0] != "search_content" {
		t.Fatalf("RequiredTools = %#v, want search_content first", analysis.RequiredTools)
	}
	if analysis.MaxIterations <= 0 {
		t.Fatalf("MaxIterations = %d, want > 0", analysis.MaxIterations)
	}
}

func TestBuildNativeQATaskIncludesAmbiguityGuidance(t *testing.T) {
	task := buildNativeQATask("How does it work?", "XiaoConstantine", "dspy-go")
	if !strings.Contains(task, "state your assumptions explicitly") {
		t.Fatalf("task missing ambiguity guidance: %s", task)
	}
}

func TestBuildNativeQATaskIncludesGuidelineGuidance(t *testing.T) {
	task := buildNativeQATask("What are the best practices for session persistence?", "XiaoConstantine", "dspy-go")
	if !strings.Contains(task, "Inspect README, docs, examples, tests") {
		t.Fatalf("task missing guideline guidance: %s", task)
	}
}
