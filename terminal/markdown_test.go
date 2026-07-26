package terminal

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderAssistantMessageRendersMarkdown(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	message := Message{Content: "# Result\n\nUse **care** and `go test`."}

	rendered := model.renderAssistantMessage(&message, 60)
	plain := ansi.Strip(rendered)
	if !containsAll(plain, "Result", "care", "go test") {
		t.Fatalf("rendered markdown = %q", rendered)
	}
	for _, marker := range []string{"# Result", "**care**", "`go test`"} {
		if strings.Contains(plain, marker) {
			t.Fatalf("rendered markdown retained source marker %q: %q", marker, plain)
		}
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("rendered markdown has no ANSI styling: %q", rendered)
	}
	if strings.HasPrefix(rendered, "\n") || strings.HasSuffix(rendered, "\n") {
		t.Fatalf("rendered markdown has outer blank line: %q", rendered)
	}
}

func TestRenderAssistantMessageCachesEmptyOutput(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	message := Message{}

	if got := model.renderAssistantMessage(&message, 20); got != "" {
		t.Fatalf("empty render = %q, want empty", got)
	}
	if message.WidthBucket != 20 || message.renderedContent != "" || message.renderCount != 1 {
		t.Fatalf("empty cache metadata = bucket %d, source %q, renders %d", message.WidthBucket, message.renderedContent, message.renderCount)
	}
	if got := model.renderAssistantMessage(&message, 20); got != "" {
		t.Fatalf("cached empty render = %q, want empty", got)
	}
	if message.renderCount != 1 {
		t.Fatalf("cached empty output rendered %d times, want 1", message.renderCount)
	}
}

func TestRenderAssistantMessageWrapsToWidth(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	message := Message{Content: "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda"}

	rendered := ansi.Strip(model.renderAssistantMessage(&message, 24))
	if !strings.Contains(rendered, "\n") {
		t.Fatalf("narrow render did not wrap: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := lipgloss.Width(line); width > 24 {
			t.Fatalf("rendered line width = %d, want <= 24: %q", width, line)
		}
	}
}

func TestRenderAssistantMessageCachesByWidthBucket(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	message := Message{Content: "A paragraph that can be wrapped at different widths."}

	first := model.renderAssistantMessage(&message, 50)
	if message.WidthBucket != 48 || message.renderCount != 1 {
		t.Fatalf("cache metadata = bucket %d, renders %d; want 48, 1", message.WidthBucket, message.renderCount)
	}
	if got := model.renderAssistantMessage(&message, 51); got != first || message.renderCount != 1 {
		t.Fatalf("same-bucket render = %q after %d renders, want cached %q after 1", got, message.renderCount, first)
	}

	message.Content = "Changed content in the same width bucket."
	if got := model.renderAssistantMessage(&message, 51); !strings.Contains(ansi.Strip(got), "Changed content") {
		t.Fatalf("same-width content mutation rendered %q", got)
	}
	if message.renderCount != 2 {
		t.Fatalf("content mutation render count = %d, want 2", message.renderCount)
	}

	if got := model.renderAssistantMessage(&message, 56); got == "" {
		t.Fatal("new-bucket render is empty")
	}
	if message.WidthBucket != 56 || message.renderCount != 3 {
		t.Fatalf("cache metadata = bucket %d, renders %d; want 56, 3", message.WidthBucket, message.renderCount)
	}
}

func TestAddMessageRecordsTimestamp(t *testing.T) {
	model := NewMaestroModel(&MaestroConfig{}, NewNoOpBackend("owner", "repo"))
	model.addMessage("assistant", "done")
	if model.messages[len(model.messages)-1].Timestamp.IsZero() {
		t.Fatal("message timestamp is zero")
	}
}
