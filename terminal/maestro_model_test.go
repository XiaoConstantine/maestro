package terminal

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

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
