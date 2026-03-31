package terminal

import "testing"

func TestReviewModelSeverityBucketsAndFilters(t *testing.T) {
	model := NewReviewModel([]ReviewComment{
		{FilePath: "a.go", Severity: "critical"},
		{FilePath: "b.go", Severity: "high"},
		{FilePath: "c.go", Severity: "medium"},
		{FilePath: "d.go", Severity: "low"},
		{FilePath: "e.go", Severity: "warning"},
		{FilePath: "f.go", Severity: "suggestion"},
	}, ClaudeCodeTheme())

	counts := model.getCommentCounts()
	if counts["critical"] != 1 {
		t.Fatalf("critical count = %d, want 1", counts["critical"])
	}
	if counts["high"] != 1 {
		t.Fatalf("high count = %d, want 1", counts["high"])
	}
	if counts["medium"] != 2 {
		t.Fatalf("medium count = %d, want 2", counts["medium"])
	}
	if counts["low"] != 2 {
		t.Fatalf("low count = %d, want 2", counts["low"])
	}

	model.setFilter(FilterHigh)
	if got := model.getTotalComments(); got != 1 {
		t.Fatalf("high filter total = %d, want 1", got)
	}

	model.setFilter(FilterMedium)
	if got := model.getTotalComments(); got != 2 {
		t.Fatalf("medium filter total = %d, want 2", got)
	}

	model.setFilter(FilterLow)
	if got := model.getTotalComments(); got != 2 {
		t.Fatalf("low filter total = %d, want 2", got)
	}
}

func TestMaestroModelReviewCountsSeverityBuckets(t *testing.T) {
	model := &MaestroModel{
		theme: ClaudeCodeTheme(),
		reviewResults: []ReviewComment{
			{Severity: "critical"},
			{Severity: "high"},
			{Severity: "medium"},
			{Severity: "low"},
			{Severity: "warning"},
			{Severity: "suggestion"},
		},
	}

	counts := model.getReviewCounts()
	if counts["critical"] != 1 {
		t.Fatalf("critical count = %d, want 1", counts["critical"])
	}
	if counts["high"] != 1 {
		t.Fatalf("high count = %d, want 1", counts["high"])
	}
	if counts["medium"] != 2 {
		t.Fatalf("medium count = %d, want 2", counts["medium"])
	}
	if counts["low"] != 2 {
		t.Fatalf("low count = %d, want 2", counts["low"])
	}
}
