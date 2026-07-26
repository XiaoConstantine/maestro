package terminal

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestSessionPickerModeRetainsHistoricalValue(t *testing.T) {
	if ModeSessionPicker != MaestroMode(3) {
		t.Fatalf("ModeSessionPicker = %d, want historical value 3", ModeSessionPicker)
	}
}

func TestReviewCommandDispatchesThroughBackend(t *testing.T) {
	backend := &reviewRecordingBackend{NoOpBackend: NewNoOpBackend("owner", "repo")}
	model := NewMaestroModel(&MaestroConfig{}, backend)
	cmd := model.handleCommand("review", []string{"42"})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("cmdReview() message = %T, want tea.BatchMsg", cmd())
	}
	var result ReviewResultMsg
	for _, child := range batch {
		if child == nil {
			continue
		}
		if message, ok := child().(ReviewResultMsg); ok {
			result = message
		}
	}
	if backend.prNumber != 42 {
		t.Fatalf("ReviewPR() prNumber = %d, want 42", backend.prNumber)
	}
	if result.PRNumber != 42 || len(result.Comments) != 1 || result.Comments[0].FilePath != "main.go" {
		t.Fatalf("ReviewResultMsg = %#v", result)
	}
}

type reviewRecordingBackend struct {
	*NoOpBackend
	prNumber int
}

func (*reviewRecordingBackend) IsReady() bool { return true }

func (b *reviewRecordingBackend) ReviewPR(_ context.Context, prNumber int, onProgress func(string)) ([]ReviewComment, error) {
	b.prNumber = prNumber
	if onProgress != nil {
		onProgress("reviewing")
	}
	return []ReviewComment{{FilePath: "main.go", LineNumber: 7, Content: "finding"}}, nil
}

func TestHandleQuestionRejectsSecondActiveCodingRun(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	first, accepted := model.handleQuestion("first task")
	if !accepted {
		t.Fatal("first handleQuestion() accepted = false")
	}
	if first == nil {
		t.Fatal("first handleQuestion() command = nil")
	}
	second, accepted := model.handleQuestion("second task")
	if accepted {
		t.Fatal("second handleQuestion() accepted = true")
	}
	if second == nil {
		t.Fatal("second handleQuestion() command = nil")
	}
	msg := second()
	if _, ok := msg.(ErrorMsg); !ok {
		t.Fatalf("second handleQuestion() message = %T, want ErrorMsg", msg)
	}
	if !model.codingRunActive {
		t.Fatal("second prompt cleared active-run state")
	}
}

func TestInputModelKeepsRejectedActiveRunPromptForEditing(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.codingRunActive = true
	model.inputModel.SetValue("second task")

	updated, cmd := model.inputModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model.inputModel = updated
	if cmd == nil {
		t.Fatal("Update() command = nil, want rejection message")
	}
	if got := model.inputModel.Value(); got != "second task" {
		t.Fatalf("input value = %q, want rejected prompt preserved", got)
	}
	if history := model.inputModel.GetHistory(); len(history) != 0 {
		t.Fatalf("history = %#v, want rejected prompt omitted", history)
	}
}

func TestEscapeBeforeCodingCommandStartsCancelsReservedContext(t *testing.T) {
	backend := &cancelAwareBackend{NoOpBackend: NewNoOpBackend("owner", "repo")}
	model := NewMaestroModel(&MaestroConfig{}, backend)
	cmd, accepted := model.handleQuestion("task")
	if !accepted {
		t.Fatal("handleQuestion() accepted = false")
	}

	if _, updateCmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); updateCmd != nil {
		_ = updateCmd
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("handleQuestion() message = %T, want tea.BatchMsg", cmd())
	}
	for _, child := range batch {
		if child != nil {
			_ = child()
		}
	}
	if !backend.sawCanceledContext {
		t.Fatal("backend did not receive the pre-start canceled context")
	}
}

type cancelAwareBackend struct {
	*NoOpBackend
	sawCanceledContext bool
}

func (b *cancelAwareBackend) IsReady() bool { return true }

func (b *cancelAwareBackend) RunCodingTask(ctx context.Context, _ string, _ func(CodingEvent)) (string, error) {
	b.sawCanceledContext = ctx.Err() != nil
	return "", ctx.Err()
}

func TestRenderInfoSectionFallsBackWithoutOptionalWorkspaceInterfaces(t *testing.T) {
	backend := &cancelAwareBackend{NoOpBackend: NewNoOpBackend("owner", "repo")}
	model := NewMaestroModel(&MaestroConfig{}, backend)
	info := model.renderInfoSection()
	if info == "" {
		t.Fatal("renderInfoSection() = empty, want fallback content")
	}
	if !containsAll(info, "Maestro coding agent") {
		t.Fatalf("renderInfoSection() = %q, want fallback model label", info)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}

func TestRenderInfoSectionFitsNarrowAndWidePanes(t *testing.T) {
	backend := &cancelAwareBackend{NoOpBackend: NewNoOpBackend("owner", "repo")}
	model := NewMaestroModel(&MaestroConfig{}, backend)
	for _, width := range []int{8, 24, 100, 121} {
		model.width = width
		for _, line := range strings.Split(model.renderInfoSection(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("renderInfoSection() line width = %d, want <= %d", got, width)
			}
		}
	}
}

func TestPlanInputModeLayoutDropsChromeOnShortPane(t *testing.T) {
	layout := planInputModeLayout(
		7,
		1, // compact logo
		4, // info section
		0, // no progress
		3, // single-line input with padding
		1, // status bar
	)

	if layout.showInfo {
		t.Fatalf("expected info section to be hidden on short pane")
	}
	if layout.showLogo {
		t.Fatalf("expected logo to be hidden on short pane")
	}
	if !layout.showStatus {
		t.Fatalf("expected status bar to remain visible when layout fits")
	}
	if layout.conversationHeight != 3 {
		t.Fatalf("conversationHeight = %d, want 3", layout.conversationHeight)
	}
}

func TestPlanInputModeLayoutDropsProgressBeforeStatus(t *testing.T) {
	layout := planInputModeLayout(
		8,
		0,
		0,
		2, // progress section
		3,
		1,
	)

	if layout.showProgress {
		t.Fatalf("expected progress section to be hidden before status")
	}
	if !layout.showStatus {
		t.Fatalf("expected status bar to remain visible")
	}
	if layout.conversationHeight < 3 {
		t.Fatalf("conversationHeight = %d, want at least 3", layout.conversationHeight)
	}
}

func TestWindowSizeUpdatesCommandPalette(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	_, _ = model.Update(tea.WindowSizeMsg{Width: 72, Height: 24})

	if model.commandPalette.width != 68 {
		t.Fatalf("command palette width = %d, want 68", model.commandPalette.width)
	}
	if model.commandPalette.height != 15 {
		t.Fatalf("command palette height = %d, want 15", model.commandPalette.height)
	}
}

func TestConfigureStatusBarUsesRunContext(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.codingRunActive = true
	model.configureStatusBar()

	if model.statusBar.mode != "RUNNING" {
		t.Fatalf("status mode = %q, want RUNNING", model.statusBar.mode)
	}
	if got := strings.Join(model.statusBar.hints, " "); !strings.Contains(got, "esc cancel") {
		t.Fatalf("status hints = %q, want cancellation hint", got)
	}
}

func TestCommandPaletteOverlayPreservesStyledContent(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.width = 30
	background := "\x1b[31mbackground\x1b[0m\nsecond line\nthird line"
	overlay := "\x1b[36mcommands\x1b[0m"

	view := model.overlayCommandPalette(background, overlay)
	if !containsAll(view, "commands", "background") {
		t.Fatalf("overlay view = %q, want foreground and background", view)
	}
}

func TestHiddenSuggestionsDoNotInterceptSubmit(t *testing.T) {
	var command string
	input := NewInputModel(ClaudeCodeTheme(), func(cmd string, _ []string) tea.Cmd {
		command = cmd
		return nil
	}, nil)
	input.SetSuggestionLimit(0)
	input.SetValue("/help")
	input.updateSuggestions()

	updated, _ := input.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command != "help" {
		t.Fatalf("submitted command = %q, want help", command)
	}
	if got := updated.Value(); got != "" {
		t.Fatalf("input value = %q, want cleared after submit", got)
	}
}

func TestInputControlJInsertsNewline(t *testing.T) {
	input := NewInputModel(ClaudeCodeTheme(), nil, nil)
	input.SetValue("first")
	updated, _ := input.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if got := updated.Value(); got != "first\n" {
		t.Fatalf("input value = %q, want newline", got)
	}
}

func TestSuggestionLimitForPane(t *testing.T) {
	tests := []struct {
		height int
		want   int
	}{
		{height: 10, want: 0},
		{height: 12, want: 2},
		{height: 18, want: 4},
		{height: 24, want: 6},
	}
	for _, tc := range tests {
		if got := suggestionLimitForPane(tc.height); got != tc.want {
			t.Fatalf("suggestionLimitForPane(%d) = %d, want %d", tc.height, got, tc.want)
		}
	}
}

func TestInputTextareaHeightForPane(t *testing.T) {
	tests := []struct {
		height int
		want   int
	}{
		{height: 7, want: 1},
		{height: 10, want: 2},
		{height: 20, want: 3},
	}

	for _, tc := range tests {
		if got := inputTextareaHeightForPane(tc.height); got != tc.want {
			t.Fatalf("inputTextareaHeightForPane(%d) = %d, want %d", tc.height, got, tc.want)
		}
	}
}
