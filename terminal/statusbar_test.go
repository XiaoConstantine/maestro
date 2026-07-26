package terminal

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestStatusBarFitsAvailableWidth(t *testing.T) {
	bar := NewStatusBar(ClaudeCodeTheme())
	bar.width = 32
	bar.SetMode("RUNNING")
	bar.SetMessage("Coding in the active workspace")
	bar.SetHints("esc cancel", "ctrl+p commands", "ctrl+c quit")

	view := bar.View()
	if width := lipgloss.Width(view); width > bar.width {
		t.Fatalf("status bar width = %d, want <= %d", width, bar.width)
	}
	if !strings.Contains(view, "RUNNING") {
		t.Fatalf("status bar = %q, want mode", view)
	}
	if !strings.Contains(view, "esc cancel") {
		t.Fatalf("status bar = %q, want primary run action", view)
	}
}

func TestStatusBarPrioritizesFirstContextHint(t *testing.T) {
	bar := NewStatusBar(ClaudeCodeTheme())
	bar.width = 80
	bar.SetMode("RUNNING")
	bar.SetHints("esc cancel", "ctrl+p commands", "ctrl+c quit")

	view := bar.View()
	if !strings.Contains(view, "esc cancel") {
		t.Fatalf("status bar = %q, want primary hint", view)
	}
}
