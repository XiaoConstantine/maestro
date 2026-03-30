package review

import "testing"

func TestPostProcessReviewCommentsMergesOverlappingDuplicates(t *testing.T) {
	comments := []PRReviewComment{
		{
			FilePath:   "pkg/agents/agent.go",
			LineNumber: 42,
			EndLine:    44,
			Content:    "This nil check is missing before dereferencing the session pointer.",
			Severity:   "medium",
			Confidence: 0.76,
			Suggestion: "Guard the pointer before accessing its fields.",
			Category:   "bug",
		},
		{
			FilePath:   "pkg/agents/agent.go",
			LineNumber: 43,
			EndLine:    45,
			Content:    "The session pointer can still be nil here, which may panic when fields are read.",
			Severity:   "high",
			Confidence: 0.91,
			Suggestion: "Add an early nil guard before dereferencing session state.",
			Category:   "bug",
		},
	}

	got := postProcessReviewComments(comments)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].LineNumber != 42 || got[0].EndLine != 45 {
		t.Fatalf("merged range = %d-%d, want 42-45", got[0].LineNumber, got[0].EndLine)
	}
	if got[0].Severity != "high" {
		t.Fatalf("merged severity = %q, want high", got[0].Severity)
	}
	if got[0].Confidence != 0.91 {
		t.Fatalf("merged confidence = %v, want 0.91", got[0].Confidence)
	}
}

func TestPostProcessReviewCommentsKeepsDistinctOverlappingIssues(t *testing.T) {
	comments := []PRReviewComment{
		{
			FilePath:   "pkg/agents/agent.go",
			LineNumber: 42,
			EndLine:    42,
			Content:    "This nil check is missing before dereferencing the session pointer.",
			Severity:   "high",
			Confidence: 0.88,
			Suggestion: "Guard the pointer before accessing its fields.",
			Category:   "bug",
		},
		{
			FilePath:   "pkg/agents/agent.go",
			LineNumber: 42,
			EndLine:    42,
			Content:    "The variable name is vague and makes the branch harder to read.",
			Severity:   "low",
			Confidence: 0.81,
			Suggestion: "Rename the variable to reflect the guarded session state.",
			Category:   "style",
		},
	}

	got := postProcessReviewComments(comments)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
}

func TestPostProcessReviewCommentsRanksBySeverityThenConfidence(t *testing.T) {
	comments := []PRReviewComment{
		{
			FilePath:   "pkg/style.go",
			LineNumber: 18,
			EndLine:    18,
			Content:    "Rename this helper for clarity.",
			Severity:   "low",
			Confidence: 0.95,
			Suggestion: "Use a more descriptive helper name.",
			Category:   "style",
		},
		{
			FilePath:   "pkg/security.go",
			LineNumber: 9,
			EndLine:    9,
			Content:    "User input reaches shell execution without validation.",
			Severity:   "critical",
			Confidence: 0.10,
			Suggestion: "Validate or escape the user-controlled value before execution.",
			Category:   "security",
		},
		{
			FilePath:   "pkg/bug.go",
			LineNumber: 33,
			EndLine:    33,
			Content:    "This branch returns the wrong error type.",
			Severity:   "high",
			Confidence: 0.99,
			Suggestion: "Return the original wrapped error instead.",
			Category:   "bug",
		},
	}

	got := postProcessReviewComments(comments)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got[0].FilePath != "pkg/bug.go" {
		t.Fatalf("got[0] file = %q, want pkg/bug.go", got[0].FilePath)
	}
	if got[1].FilePath != "pkg/security.go" {
		t.Fatalf("got[1] file = %q, want pkg/security.go", got[1].FilePath)
	}
	if got[2].FilePath != "pkg/style.go" {
		t.Fatalf("got[2] file = %q, want pkg/style.go", got[2].FilePath)
	}
}

func TestPostProcessReviewCommentsDefaultsUnknownConfidenceToNeutral(t *testing.T) {
	got := postProcessReviewComments([]PRReviewComment{{
		FilePath:   "pkg/example.go",
		LineNumber: 10,
		EndLine:    10,
		Content:    "This branch should return the original error.",
		Severity:   "high",
		Confidence: 0,
		Category:   "bug",
	}})

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Confidence != 0.5 {
		t.Fatalf("confidence = %v, want 0.5", got[0].Confidence)
	}
}

func TestPostProcessReviewCommentsDoesNotMergeUnknownLineComments(t *testing.T) {
	comments := []PRReviewComment{
		{
			FilePath:   "pkg/example.go",
			LineNumber: 0,
			EndLine:    0,
			Content:    "This helper name is unclear.",
			Severity:   "low",
			Confidence: 0.8,
			Category:   "style",
		},
		{
			FilePath:   "pkg/example.go",
			LineNumber: 0,
			EndLine:    0,
			Content:    "This helper name is unclear.",
			Severity:   "low",
			Confidence: 0.8,
			Category:   "style",
		},
	}

	got := postProcessReviewComments(comments)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
}

func TestPostProcessReviewCommentsKeepsDifferentFilesSeparate(t *testing.T) {
	comments := []PRReviewComment{
		{
			FilePath:   "pkg/one.go",
			LineNumber: 12,
			EndLine:    12,
			Content:    "The error return is ignored.",
			Severity:   "high",
			Confidence: 0.9,
			Suggestion: "Check and handle the returned error.",
			Category:   "bug",
		},
		{
			FilePath:   "pkg/two.go",
			LineNumber: 12,
			EndLine:    12,
			Content:    "The error return is ignored.",
			Severity:   "high",
			Confidence: 0.9,
			Suggestion: "Check and handle the returned error.",
			Category:   "bug",
		},
	}

	got := postProcessReviewComments(comments)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
}

func TestPostProcessReviewCommentsPreservesTransitiveBoundary(t *testing.T) {
	comments := []PRReviewComment{
		{
			FilePath:   "pkg/agents/agent.go",
			LineNumber: 40,
			EndLine:    42,
			Content:    "The session pointer can still be nil before this dereference.",
			Severity:   "high",
			Confidence: 0.91,
			Suggestion: "Add a nil guard before dereferencing the session.",
			Category:   "bug",
		},
		{
			FilePath:   "pkg/agents/agent.go",
			LineNumber: 41,
			EndLine:    43,
			Content:    "This path still dereferences a nil session pointer.",
			Severity:   "high",
			Confidence: 0.88,
			Suggestion: "Guard the session pointer before accessing its fields.",
			Category:   "bug",
		},
		{
			FilePath:   "pkg/agents/agent.go",
			LineNumber: 43,
			EndLine:    44,
			Content:    "This branch returns the wrong wrapped error.",
			Severity:   "high",
			Confidence: 0.95,
			Suggestion: "Return the original wrapped error instead of the sentinel.",
			Category:   "bug",
		},
	}

	got := postProcessReviewComments(comments)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
}
