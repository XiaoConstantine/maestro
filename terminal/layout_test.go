package terminal

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestPlanContentLayoutBreakpoints(t *testing.T) {
	tests := []struct {
		width        int
		requested    bool
		wantRail     bool
		wantRailSize int
	}{
		{width: 60, requested: true, wantRail: false},
		{width: 80, requested: true, wantRail: true, wantRailSize: 24},
		{width: 119, requested: true, wantRail: true, wantRailSize: 24},
		{width: 120, requested: true, wantRail: true, wantRailSize: 32},
		{width: 160, requested: true, wantRail: true, wantRailSize: 40},
		{width: 120, requested: false, wantRail: false},
	}
	for _, tc := range tests {
		plan := planContentLayout(tc.width, 20, tc.requested)
		if plan.showRail != tc.wantRail || plan.railWidth != tc.wantRailSize {
			t.Fatalf("planContentLayout(%d, requested=%v) = %#v", tc.width, tc.requested, plan)
		}
		if plan.conversationWidth+plan.railWidth != tc.width {
			t.Fatalf("content widths = %d + %d, want %d", plan.conversationWidth, plan.railWidth, tc.width)
		}
	}
}

func TestComposerHeightGrowsWithinCap(t *testing.T) {
	value := strings.Repeat("line\n", 20)
	if got := composerHeightForPane(20, value); got != 6 {
		t.Fatalf("composer height = %d, want 30%% cap 6", got)
	}
	if got := composerHeightForPane(60, value); got != 10 {
		t.Fatalf("composer height = %d, want absolute cap 10", got)
	}
}

func TestContextRailTogglePreservesContent(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.width = 120
	model.toolActivity.Apply(CodingEvent{Kind: "run", Status: "started"})
	model.railVisible = true

	_, _ = model.Update(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	if model.railVisible || !model.toolActivity.HasEntries() {
		t.Fatalf("hidden rail state = visible:%v entries:%v", model.railVisible, model.toolActivity.HasEntries())
	}
	_, _ = model.Update(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	if !model.railVisible {
		t.Fatal("rail did not reopen")
	}
}

func TestRailRenderDoesNotSnapManualScroll(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.width = 120
	model.height = 20
	model.railVisible = true
	for i := 0; i < 20; i++ {
		model.toolActivity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "completed", Detail: fmt.Sprintf("file-%d", i), ToolIndex: i})
	}
	model.setInputFocus(FocusToolActivity)
	_ = model.renderInputMode()
	model.railViewport.SetYOffset(2)
	_ = model.renderInputMode()
	if got := model.railViewport.YOffset(); got != 2 {
		t.Fatalf("rail offset = %d, want manually selected 2", got)
	}
}

func TestRailFollowsNewToolsAndRevealsSelectionOnFocus(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.width = 120
	model.height = 18
	model.railVisible = true
	for i := 0; i < 16; i++ {
		model.toolActivity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "completed", Detail: fmt.Sprintf("file-%d", i), ToolIndex: i})
	}
	_ = model.renderInputMode()
	if !model.railViewport.AtBottom() {
		t.Fatal("unfocused rail did not follow new activity")
	}
	model.railViewport.GotoTop()
	model.railFollowTail = false
	model.setInputFocus(FocusToolActivity)
	line := model.toolActivityRailStartLine + model.toolActivity.SelectedLine(model.railViewport.Width())
	if line < model.railViewport.YOffset() || line >= model.railViewport.YOffset()+model.railViewport.Height() {
		t.Fatalf("focused selected line %d outside rail [%d,%d)", line, model.railViewport.YOffset(), model.railViewport.YOffset()+model.railViewport.Height())
	}
}

func TestLiveEventsPreserveManualFocusedRailScroll(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.width = 120
	model.height = 18
	model.railVisible = true
	model.toolActivity.Apply(CodingEvent{Kind: "run", RunID: "run-1", Status: "started"})
	for i := 0; i < 20; i++ {
		model.toolActivity.Apply(CodingEvent{Kind: "tool", RunID: "run-1", Tool: "read", Status: "completed", Detail: fmt.Sprintf("file-%d", i), ToolIndex: i})
	}
	_ = model.renderInputMode()
	model.setInputFocus(FocusToolActivity)
	model.railViewport.GotoBottom()
	_, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	manualOffset := model.railViewport.YOffset()
	if model.railFollowTail {
		t.Fatal("manual rail scroll did not disable follow")
	}

	_, _ = model.Update(CodingEventMsg{Event: CodingEvent{Kind: "tool", RunID: "run-1", Tool: "read", Status: "completed", Detail: "new", ToolIndex: 21}})
	_ = model.renderInputMode()
	if got := model.railViewport.YOffset(); got != manualOffset {
		t.Fatalf("event rail offset = %d, want manual %d", got, manualOffset)
	}
	_, _ = model.Update(CodingResultMsg{Content: "final"})
	_ = model.renderInputMode()
	if got := model.railViewport.YOffset(); got != manualOffset {
		t.Fatalf("result rail offset = %d, want manual %d", got, manualOffset)
	}
}

func TestLayoutReflowPreservesConversationTail(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.width = 120
	model.height = 18
	for i := 0; i < 20; i++ {
		model.messages = append(model.messages, Message{Role: "user", Content: fmt.Sprintf("long conversation message %d", i)})
	}
	_ = model.renderInputMode()
	model.viewport.GotoBottom()
	model.toolActivity.Apply(CodingEvent{Kind: "run", Status: "started", Detail: "task"})
	model.railVisible = true
	_ = model.renderInputMode()
	if !model.viewport.AtBottom() {
		t.Fatalf("conversation offset = %d, want tail after rail reflow", model.viewport.YOffset())
	}
}

func TestReviewRenderingUsesConversationWidthWithRail(t *testing.T) {
	for _, width := range []int{80, 120} {
		model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
		model.width = width
		model.height = 24
		model.railVisible = true
		model.reviewResults = []ReviewComment{{FilePath: "very/long/path/to/main.go", Content: strings.Repeat("finding ", 20)}}
		model.showReviewDetail = true
		model.initReviewFileExpanded()
		_ = model.renderInputMode()
		for _, line := range strings.Split(model.renderInlineReview(), "\n") {
			if got := lipgloss.Width(line); got > model.viewport.Width() {
				t.Fatalf("width %d review line = %d, conversation = %d", width, got, model.viewport.Width())
			}
		}
	}
}

func TestActualToggleAndResizePreserveConversationTail(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	_, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 18})
	for i := 0; i < 20; i++ {
		model.messages = append(model.messages, Message{Role: "user", Content: fmt.Sprintf("message %d with wrapping content", i)})
	}
	model.toolActivity.Apply(CodingEvent{Kind: "run", Status: "started"})
	model.railVisible = true
	_ = model.renderInputMode()
	model.viewport.GotoBottom()

	_, _ = model.Update(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	_ = model.renderInputMode()
	if !model.viewport.AtBottom() {
		t.Fatal("Ctrl+\\ rail transition lost conversation tail")
	}

	model.railVisible = true
	_ = model.renderInputMode()
	model.viewport.GotoBottom()
	_, _ = model.Update(tea.WindowSizeMsg{Width: 70, Height: 12})
	_ = model.renderInputMode()
	if !model.viewport.AtBottom() {
		t.Fatal("WindowSizeMsg transition lost conversation tail")
	}
}

func TestFocusedSelectionSurvivesRailToInlineTransition(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.width = 120
	model.height = 18
	model.messages = []Message{{Role: "user", Content: "task"}}
	model.toolActivityAnchor = 1
	model.railVisible = true
	for i := 0; i < 12; i++ {
		model.toolActivity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "completed", Detail: fmt.Sprintf("file-%d", i), ToolIndex: i})
	}
	model.setInputFocus(FocusToolActivity)
	_ = model.renderInputMode()

	model.railVisible = false
	_ = model.renderInputMode()
	line := model.toolActivityStartLine + model.toolActivity.SelectedLine(model.viewport.Width())
	if line < model.viewport.YOffset() || line >= model.viewport.YOffset()+model.viewport.Height() {
		t.Fatalf("inline selected line %d outside viewport [%d,%d)", line, model.viewport.YOffset(), model.viewport.YOffset()+model.viewport.Height())
	}
}

func TestContextRailPersistsAcrossResponsiveBreakpoint(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.width = 120
	model.height = 30
	model.railVisible = true
	model.toolActivity.Apply(CodingEvent{Kind: "run", Status: "started", Detail: "task"})

	wide := model.renderInputMode()
	if !model.contextRailActive() || !strings.Contains(model.railViewport.View(), "ACTIVITY") {
		t.Fatalf("wide layout omitted active rail: %q", wide)
	}
	for _, line := range strings.Split(wide, "\n") {
		if width := lipgloss.Width(line); width > 120 {
			t.Fatalf("wide layout line width = %d, want <= 120", width)
		}
	}

	model.width = 70
	_ = model.renderInputMode()
	if model.contextRailActive() || !model.railVisible {
		t.Fatalf("narrow layout active=%v requested=%v, want hidden but remembered", model.contextRailActive(), model.railVisible)
	}
	model.width = 120
	_ = model.renderInputMode()
	if !model.contextRailActive() {
		t.Fatal("rail did not return after widening")
	}
}
