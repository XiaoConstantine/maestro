package terminal

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestNoColorThemeAndMarkdownContainNoANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	if _, ok := model.theme.Accent.(lipgloss.NoColor); !ok {
		t.Fatalf("accent color type = %T, want lipgloss.NoColor", model.theme.Accent)
	}
	message := Message{Role: "assistant", Content: "# Heading\n\n`code` and **bold**"}
	rendered := model.renderAssistantMessage(&message, 60)
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("NO_COLOR markdown contains ANSI escapes: %q", rendered)
	}
	if !strings.Contains(rendered, "**bold**") {
		t.Fatalf("NO_COLOR markdown lost textual emphasis: %q", rendered)
	}
}

func TestHighContrastThemeCanBeSelectedFromEnvironment(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("MAESTRO_THEME", "high-contrast")
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	if model.theme.TextMuted == ClaudeCodeTheme().TextMuted || model.theme.Comment != model.theme.TextMuted {
		t.Fatal("high-contrast theme retained a default low-contrast semantic color")
	}
	styles := maestroMarkdownStyles(model.theme)
	if styles.CodeBlock.Chroma == nil || styles.CodeBlock.Chroma.Comment.Color == nil ||
		*styles.CodeBlock.Chroma.Comment.Color != *markdownColor(model.theme.Comment) {
		t.Fatal("Markdown code comments do not use the selected theme")
	}
}

func TestReducedMotionUsesStaticProgressIndicator(t *testing.T) {
	progress := NewProgressModel(ClaudeCodeTheme())
	progress.SetReducedMotion(true)
	progress.message = "Testing"
	progress.state = ProgressWorking
	first := progress.renderWorking()
	progress.frame = 7
	second := progress.renderWorking()
	if first != second || !strings.Contains(first, "[working]") {
		t.Fatalf("reduced-motion progress changed frames: %q != %q", first, second)
	}
}
