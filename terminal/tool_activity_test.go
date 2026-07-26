package terminal

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestToolActivityRetainsLifecycleAndRunSummary(t *testing.T) {
	activity := NewToolActivityModel(ClaudeCodeTheme())
	start := time.Now()
	activity.Apply(CodingEvent{Kind: "run", Status: "started", Detail: "fix tests", MaxTurns: 8, At: start})
	activity.Apply(CodingEvent{Kind: "turn", Status: "started", Turn: 1, MaxTurns: 8, At: start})
	activity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "started", Detail: "Running read main.go", Turn: 1, ToolIndex: 0, At: start})
	activity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "completed", Detail: "main.go\npackage main", Turn: 1, ToolIndex: 0, At: start.Add(125 * time.Millisecond)})
	activity.Apply(CodingEvent{Kind: "run", Status: "completed", Turn: 1, ToolCalls: 1, At: start.Add(time.Second)})

	view := ansi.Strip(activity.View(100, false))
	if !containsAll(view, "run started", "turn 1/8", "read", "125ms", "run completed · 1 turns · 1 tools") {
		t.Fatalf("activity view = %q", view)
	}
	if activity.IsRunning() {
		t.Fatal("activity remains running after terminal event")
	}
	if len(activity.entries) != 4 {
		t.Fatalf("entries = %d, want started run, turn, tool, finished run", len(activity.entries))
	}
}

func TestToolActivityIgnoresStaleRunEvents(t *testing.T) {
	activity := NewToolActivityModel(ClaudeCodeTheme())
	activity.Apply(CodingEvent{Kind: "run", RunID: "new", Status: "started"})
	activity.Apply(CodingEvent{Kind: "tool", RunID: "old", Tool: "write", Status: "completed", Turn: 1, ToolIndex: 0})
	if activity.HasTools() {
		t.Fatalf("stale event created tool entries: %#v", activity.entries)
	}
}

func TestStaleCodingEventDoesNotMutateOuterUI(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.toolActivity.Apply(CodingEvent{Kind: "run", RunID: "current", Status: "started"})
	model.toolActivityAnchor = 1
	_ = model.progressModel.Start("current progress")

	_, _ = model.Update(CodingEventMsg{Event: CodingEvent{Kind: "run", RunID: "stale", Status: "completed"}})
	if !model.progressModel.IsVisible() || model.progressModel.message != "current progress" {
		t.Fatalf("stale event mutated progress: visible=%v message=%q", model.progressModel.IsVisible(), model.progressModel.message)
	}
	if model.toolActivityAnchor != 1 || !model.toolActivity.IsRunning() {
		t.Fatalf("stale event mutated activity: anchor=%d running=%v", model.toolActivityAnchor, model.toolActivity.IsRunning())
	}
}

func TestToolActivitySkipsFinishOutcome(t *testing.T) {
	activity := NewToolActivityModel(ClaudeCodeTheme())
	activity.Apply(CodingEvent{Kind: "tool", Tool: "Finish", Status: "completed", Outcome: "finish", Turn: 1, ToolIndex: 0})
	if activity.HasTools() || activity.HasEntries() {
		t.Fatalf("finish outcome created activity entries: %#v", activity.entries)
	}
}

func TestToolActivityDistinguishesTerminalRunStatuses(t *testing.T) {
	activity := NewToolActivityModel(ClaudeCodeTheme())
	for _, status := range []string{"completed", "stopped", "canceled", "failed"} {
		view := ansi.Strip(activity.renderRun(ToolActivityEntry{Kind: "run", Status: status}, 80))
		if !strings.Contains(view, "run "+status) {
			t.Fatalf("status %q rendered as %q", status, view)
		}
	}
	if got := ansi.Strip(activity.renderRun(ToolActivityEntry{Kind: "run", Status: "stopped"}, 80)); strings.HasPrefix(got, "✗") {
		t.Fatalf("stopped run rendered as failure: %q", got)
	}
}

func TestToolActivityPreservesSelectionWhileFocused(t *testing.T) {
	activity := NewToolActivityModel(ClaudeCodeTheme())
	activity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "started", Turn: 1, ToolIndex: 0})
	activity.SetFocused(true)
	activity.Apply(CodingEvent{Kind: "tool", Tool: "edit", Status: "started", Turn: 1, ToolIndex: 1})
	if activity.selected != 0 {
		t.Fatalf("selection = %d, want pinned first tool", activity.selected)
	}
}

func TestToolActivityExpandsSelectedDetail(t *testing.T) {
	activity := NewToolActivityModel(ClaudeCodeTheme())
	activity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "completed", Detail: "main.go\npackage main", Turn: 1, ToolIndex: 0})

	collapsed := ansi.Strip(activity.View(80, true))
	if strings.Contains(collapsed, "package main") {
		t.Fatalf("collapsed activity exposed detail: %q", collapsed)
	}
	activity.ToggleSelected()
	expanded := ansi.Strip(activity.View(80, true))
	if !strings.Contains(expanded, "package main") {
		t.Fatalf("expanded activity omitted detail: %q", expanded)
	}
}

func TestToolActivityRendersBeforeFinalResponse(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.messages = []Message{
		{Role: "user", Content: "fix tests"},
		{Role: "assistant", Content: "finished response"},
	}
	model.toolActivityAnchor = 1
	model.toolActivity.Apply(CodingEvent{Kind: "run", Status: "started", Detail: "fix tests"})
	model.toolActivity.Apply(CodingEvent{Kind: "run", Status: "completed", Turn: 1, ToolCalls: 0})
	model.renderMessages()

	view := ansi.Strip(model.viewport.View())
	activityIndex := strings.Index(view, "run completed")
	responseIndex := strings.Index(view, "finished response")
	if activityIndex < 0 || responseIndex < 0 || activityIndex > responseIndex {
		t.Fatalf("conversation order = %q, want activity before final response", view)
	}
}

func TestEnsureToolSelectionVisibleScrollsLongTranscript(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.viewport.SetHeight(4)
	model.messages = []Message{{Role: "user", Content: "task"}}
	model.toolActivityAnchor = 1
	for i := 0; i < 12; i++ {
		model.toolActivity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "completed", Detail: fmt.Sprintf("file-%d", i), Turn: 1, ToolIndex: i})
	}
	model.inputFocus = FocusToolActivity
	model.toolActivity.SetFocused(true)
	model.renderMessages()
	model.ensureToolSelectionVisible()
	if model.viewport.YOffset() == 0 {
		t.Fatalf("long selected tool did not scroll into view: selected=%d line=%d start=%d view=%q", model.toolActivity.selected, model.toolActivity.SelectedLine(model.viewport.Width()), model.toolActivityStartLine, ansi.Strip(model.toolActivity.View(model.viewport.Width(), true)))
	}
}

func TestFocusedToolSelectionStaysVisibleAsEventsArrive(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.viewport.SetHeight(4)
	model.messages = []Message{{Role: "user", Content: "task"}}
	model.toolActivityAnchor = 1
	model.toolActivity.Apply(CodingEvent{Kind: "run", RunID: "run-1", Status: "started"})
	for i := 0; i < 8; i++ {
		model.toolActivity.Apply(CodingEvent{Kind: "tool", RunID: "run-1", Tool: "read", Status: "completed", Detail: fmt.Sprintf("file-%d", i), Turn: 1, ToolIndex: i})
	}
	model.toolActivity.selected = 0
	model.setInputFocus(FocusToolActivity)
	model.renderMessages()
	model.viewport.GotoBottom()

	_, _ = model.Update(CodingEventMsg{Event: CodingEvent{Kind: "tool", RunID: "run-1", Tool: "read", Status: "completed", Detail: "new-file", Turn: 1, ToolIndex: 9}})
	selectedLine := model.toolActivityStartLine + model.toolActivity.SelectedLine(model.viewport.Width())
	if selectedLine < model.viewport.YOffset() || selectedLine >= model.viewport.YOffset()+model.viewport.Height() {
		t.Fatalf("selected line %d outside viewport [%d,%d)", selectedLine, model.viewport.YOffset(), model.viewport.YOffset()+model.viewport.Height())
	}
	if model.toolActivity.selected != 0 {
		t.Fatalf("selection moved to %d, want pinned 0", model.toolActivity.selected)
	}
}

func TestFocusedToolStaysVisibleAfterFinalResult(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.viewport.SetHeight(4)
	model.messages = []Message{{Role: "user", Content: "task"}}
	model.toolActivityAnchor = 1
	for i := 0; i < 8; i++ {
		model.toolActivity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "completed", Detail: fmt.Sprintf("file-%d", i), Turn: 1, ToolIndex: i})
	}
	model.toolActivity.selected = 0
	model.setInputFocus(FocusToolActivity)
	model.renderMessages()
	model.viewport.GotoBottom()

	_, _ = model.Update(CodingResultMsg{Content: strings.Repeat("long final response ", 30)})
	selectedLine := model.toolActivityStartLine + model.toolActivity.SelectedLine(model.viewport.Width())
	if selectedLine < model.viewport.YOffset() || selectedLine >= model.viewport.YOffset()+model.viewport.Height() {
		t.Fatalf("selected line %d outside viewport [%d,%d) after final result", selectedLine, model.viewport.YOffset(), model.viewport.YOffset()+model.viewport.Height())
	}
}

func TestFocusTransitionsStaySynchronized(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.toolActivity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "completed"})
	model.setInputFocus(FocusToolActivity)

	_, _ = model.Update(SessionPickerMsg{Sessions: []SessionInfo{{Name: "default"}}})
	if model.inputFocus != FocusInput || model.toolActivity.focused || model.inputModel.IsFocused() {
		t.Fatalf("session picker focus state = input:%v tool:%v composer:%v", model.inputFocus, model.toolActivity.focused, model.inputModel.IsFocused())
	}

	model.mode = ModeInput
	model.setInputFocus(FocusToolActivity)
	_, _ = model.Update(InsertCommandMsg{Command: "/review "})
	if model.inputFocus != FocusInput || model.toolActivity.focused || !model.inputModel.IsFocused() {
		t.Fatalf("insert command focus state = input:%v tool:%v composer:%v", model.inputFocus, model.toolActivity.focused, model.inputModel.IsFocused())
	}

	model.setInputFocus(FocusToolActivity)
	_ = model.handleCommand("clear", nil)
	if model.inputFocus != FocusInput || model.toolActivity.focused || !model.inputModel.IsFocused() {
		t.Fatalf("clear focus state = input:%v tool:%v composer:%v", model.inputFocus, model.toolActivity.focused, model.inputModel.IsFocused())
	}
}

func TestToolActivityFocusCyclesThroughAvailableRegions(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.toolActivity.Apply(CodingEvent{Kind: "tool", Tool: "read", Status: "completed", Detail: "README.md"})
	model.reviewResults = []ReviewComment{{FilePath: "main.go", Content: "finding"}}

	model.changeFocus()
	if model.inputFocus != FocusToolActivity {
		t.Fatalf("first focus = %v, want tools", model.inputFocus)
	}
	model.changeFocus()
	if model.inputFocus != FocusReviewList {
		t.Fatalf("second focus = %v, want review", model.inputFocus)
	}
	model.changeFocus()
	if model.inputFocus != FocusInput {
		t.Fatalf("third focus = %v, want input", model.inputFocus)
	}
}
