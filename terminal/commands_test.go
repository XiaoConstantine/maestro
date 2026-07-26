package terminal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestBuiltinCommandsSharedByComposerAndPalette(t *testing.T) {
	input := NewInputModel(ClaudeCodeTheme(), nil, nil)
	palette := NewCommandPalette(ClaudeCodeTheme())
	if len(input.allCommands) != len(palette.commands) {
		t.Fatalf("composer commands = %d, palette commands = %d", len(input.allCommands), len(palette.commands))
	}
	for i := range input.allCommands {
		if input.allCommands[i].Name != palette.commands[i].Name {
			t.Fatalf("command %d = %q in composer, %q in palette", i, input.allCommands[i].Name, palette.commands[i].Name)
		}
	}
}

func TestCommandItemTruncatesByCellWidth(t *testing.T) {
	palette := NewCommandPalette(ClaudeCodeTheme())
	palette.width = 20

	item := palette.renderCommandItem(Command{
		Name:        "review",
		Aliases:     []string{"/review"},
		Description: "Review a pull request with a long description",
		Category:    "GitHub",
	}, true)
	if width := lipgloss.Width(item); width > palette.width-4 {
		t.Fatalf("command item width = %d, want <= %d", width, palette.width-4)
	}
}

func TestCommandPaletteKeepsSelectionInVisibleWindow(t *testing.T) {
	palette := NewCommandPalette(ClaudeCodeTheme())
	palette.visible = true
	palette.width = 60
	palette.height = 8
	palette.filterCommands("")
	palette.selected = len(palette.filteredCmds) - 1

	view := palette.View()
	selected := palette.filteredCmds[palette.selected].Name
	if !strings.Contains(view, selected) {
		t.Fatalf("palette view does not contain selected command %q", selected)
	}
}

func TestCommandPaletteTabCompletionClampsSelection(t *testing.T) {
	palette := NewCommandPalette(ClaudeCodeTheme())
	palette.Show()
	palette.selected = len(palette.filteredCmds) - 1
	selectedName := palette.filteredCmds[palette.selected].Name

	updated, _ := palette.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if updated.selected != 0 || len(updated.filteredCmds) != 1 {
		t.Fatalf("selection = %d with %d matches, want 0 with 1", updated.selected, len(updated.filteredCmds))
	}
	if !strings.Contains(updated.View(), selectedName) {
		t.Fatalf("palette view does not contain completed command %q", selectedName)
	}
}

func TestCommandPaletteAcceptsTinyWindowSize(t *testing.T) {
	palette := NewCommandPalette(ClaudeCodeTheme())
	updated, _ := palette.Update(tea.WindowSizeMsg{Width: 8, Height: 4})
	if updated.width != 4 || updated.height != 1 {
		t.Fatalf("palette size = %dx%d, want 4x1", updated.width, updated.height)
	}
}
