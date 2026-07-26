package terminal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

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

func TestEmbeddedAndStandaloneReviewShareRendering(t *testing.T) {
	comments := []ReviewComment{{FilePath: "main.go", LineNumber: 7, Severity: "critical", Content: "finding"}}
	standalone := NewReviewModel(comments, ClaudeCodeTheme())
	embedded := NewEmbeddedReviewModel(comments, ClaudeCodeTheme())
	standalone.SetSize(80, 20)
	embedded.SetSize(80, 20)
	embedded.SetFocused(true)

	if standalone.renderList() != embedded.renderList() || standalone.renderFilterTabs() != embedded.renderFilterTabs() {
		t.Fatal("embedded and standalone hosts do not share review rendering")
	}
	updated, cmd := embedded.Update(tea.KeyPressMsg{Code: 'q'})
	if cmd != nil || updated.(*ReviewModel) != embedded {
		t.Fatal("embedded q attempted to quit host")
	}
}

func TestMaestroRoutesReviewKeysThroughSharedComponent(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	_, _ = model.Update(ReviewResultMsg{Comments: []ReviewComment{
		{FilePath: "critical.go", Severity: "critical", Content: "critical"},
		{FilePath: "low.go", Severity: "low", Content: "low"},
	}})
	_, _ = model.Update(tea.KeyPressMsg{Code: '1'})
	if model.reviewModel.filterMode != FilterCritical || model.reviewModel.getTotalComments() != 1 {
		t.Fatalf("embedded filter = %v with %d comments", model.reviewModel.filterMode, model.reviewModel.getTotalComments())
	}
}

func TestReviewSeverityUsesShapeAndColor(t *testing.T) {
	model := NewReviewModel(nil, ClaudeCodeTheme())
	critical := ansi.Strip(model.getSeverityIcon("critical"))
	high := ansi.Strip(model.getSeverityIcon("high"))
	medium := ansi.Strip(model.getSeverityIcon("medium"))
	low := ansi.Strip(model.getSeverityIcon("low"))
	seen := map[string]bool{critical: true, high: true, medium: true, low: true}
	if len(seen) != 4 || !strings.Contains(critical, "✖") {
		t.Fatalf("severity shapes = critical:%q high:%q medium:%q low:%q", critical, high, medium, low)
	}
}

func TestReviewNarrowWidthsAndConfirmationDoNotPanic(t *testing.T) {
	comments := []ReviewComment{{FilePath: "very/long/path/to/main.go", Severity: "critical", Content: "finding"}}
	for width := 1; width <= 20; width++ {
		model := NewReviewModel(comments, ClaudeCodeTheme())
		model.SetSize(width, 5)
		for _, confirming := range []bool{false, true} {
			model.confirmPost = confirming
			for _, line := range strings.Split(model.ViewString(), "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Fatalf("width %d confirming=%v line width=%d: %q", width, confirming, got, line)
				}
			}
		}
	}
}

func TestReviewCollapseThenGoBottomUsesVisibleItems(t *testing.T) {
	model := NewReviewModel([]ReviewComment{
		{FilePath: "a.go", Content: "first"},
		{FilePath: "b.go", Content: "second"},
	}, ClaudeCodeTheme())
	model.toggleCurrentFileGroup()
	_, _ = model.Update(tea.KeyPressMsg{Code: 'G'})
	if model.selectedIdx != model.getTotalVisibleItems()-1 {
		t.Fatalf("selected index = %d, visible items = %d", model.selectedIdx, model.getTotalVisibleItems())
	}
	file, comment := model.selectedPosition()
	if file != 1 || comment != 0 {
		t.Fatalf("bottom position = file:%d comment:%d, want second file comment", file, comment)
	}
}

func TestEmbeddedReviewReflectsHostFocus(t *testing.T) {
	model := NewEmbeddedReviewModel([]ReviewComment{{FilePath: "a.go", Content: "finding"}}, ClaudeCodeTheme())
	model.SetSize(60, 15)
	model.SetFocused(false)
	if view := ansi.Strip(model.ViewString()); strings.Contains(view, "▸") || !strings.Contains(view, "tab focus review") {
		t.Fatalf("unfocused embedded view = %q", view)
	}
	model.SetFocused(true)
	if view := ansi.Strip(model.ViewString()); !strings.Contains(view, "▸") {
		t.Fatalf("focused embedded view = %q", view)
	}
	model.toggleCurrentFileGroup()
	focusedCollapsed := model.renderList()
	model.SetFocused(false)
	if unfocusedCollapsed := model.renderList(); unfocusedCollapsed == focusedCollapsed {
		t.Fatal("collapsed file highlight did not change with host focus")
	}
}

func TestReviewSelectionUsesOneFlattenedIndex(t *testing.T) {
	model := NewReviewModel([]ReviewComment{
		{FilePath: "a.go", Content: "first"},
		{FilePath: "b.go", Content: "second"},
	}, ClaudeCodeTheme())
	model.selectedIdx = 1
	model.toggleCurrentFileGroup()
	file, comment := model.selectedPosition()
	if file != 1 || comment != -1 || model.selectedIdx != 1 || model.filteredGroups[1].Expanded {
		t.Fatalf("collapsed selection = file:%d comment:%d index:%d expanded:%v", file, comment, model.selectedIdx, model.filteredGroups[1].Expanded)
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
