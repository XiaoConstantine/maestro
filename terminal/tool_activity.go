package terminal

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ToolActivityEntry is one durable coding-run lifecycle entry.
type ToolActivityEntry struct {
	Kind       string
	Tool       string
	Status     string
	Outcome    string
	Detail     string
	Turn       int
	MaxTurns   int
	ToolIndex  int
	ToolCalls  int
	StartedAt  time.Time
	FinishedAt time.Time
	Expanded   bool
}

// ToolActivityModel retains the current run's lifecycle evidence after it ends.
type ToolActivityModel struct {
	entries      []ToolActivityEntry
	selected     int
	currentRunID string
	running      bool
	focused      bool
	theme        *Theme
}

func NewToolActivityModel(theme *Theme) *ToolActivityModel {
	return &ToolActivityModel{theme: theme}
}

func (m *ToolActivityModel) Apply(event CodingEvent) bool {
	if event.Kind == "run" && event.Status == "started" {
		if m.running && event.RunID != "" && m.currentRunID != "" && event.RunID != m.currentRunID {
			return false
		}
		m.currentRunID = event.RunID
	} else if event.RunID != "" && m.currentRunID != "" && event.RunID != m.currentRunID {
		return false
	}
	switch event.Kind {
	case "run":
		if event.Status == "started" {
			m.entries = []ToolActivityEntry{{
				Kind: "run", Status: event.Status, Detail: event.Detail,
				MaxTurns: event.MaxTurns, StartedAt: event.At,
			}}
			m.selected = 0
			m.running = true
			return true
		}
		m.entries = append(m.entries, ToolActivityEntry{
			Kind: "run", Status: event.Status, Detail: event.Detail,
			Turn: event.Turn, ToolCalls: event.ToolCalls, FinishedAt: event.At,
		})
		m.running = false
	case "turn":
		m.entries = append(m.entries, ToolActivityEntry{
			Kind: "turn", Status: event.Status, Detail: event.Detail,
			Turn: event.Turn, MaxTurns: event.MaxTurns, StartedAt: event.At,
		})
	case "tool":
		if event.Outcome == "finish" {
			return true
		}
		if event.Status == "started" {
			m.entries = append(m.entries, ToolActivityEntry{
				Kind: "tool", Tool: event.Tool, Status: event.Status, Detail: event.Detail,
				Turn: event.Turn, ToolIndex: event.ToolIndex, StartedAt: event.At,
			})
			if !m.focused {
				m.selected = m.expandableCount() - 1
			}
			return true
		}
		if entry := m.findTool(event.Turn, event.ToolIndex); entry != nil {
			entry.Status = event.Status
			entry.Outcome = event.Outcome
			entry.Detail = event.Detail
			entry.FinishedAt = event.At
			return true
		}
		m.entries = append(m.entries, ToolActivityEntry{
			Kind: "tool", Tool: event.Tool, Status: event.Status, Outcome: event.Outcome, Detail: event.Detail,
			Turn: event.Turn, ToolIndex: event.ToolIndex, FinishedAt: event.At,
		})
		if !m.focused {
			m.selected = m.expandableCount() - 1
		}
	}
	return true
}

func (m *ToolActivityModel) findTool(turn, index int) *ToolActivityEntry {
	for i := len(m.entries) - 1; i >= 0; i-- {
		entry := &m.entries[i]
		if entry.Kind == "tool" && entry.Turn == turn && entry.ToolIndex == index {
			return entry
		}
	}
	return nil
}

func (m *ToolActivityModel) HasEntries() bool        { return len(m.entries) > 0 }
func (m *ToolActivityModel) HasTools() bool          { return m.expandableCount() > 0 }
func (m *ToolActivityModel) IsRunning() bool         { return m.running }
func (m *ToolActivityModel) SetFocused(focused bool) { m.focused = focused }

func (m *ToolActivityModel) expandableCount() int {
	count := 0
	for _, entry := range m.entries {
		if entry.Kind == "tool" {
			count++
		}
	}
	return count
}

func (m *ToolActivityModel) Move(delta int) {
	count := m.expandableCount()
	if count == 0 {
		m.selected = 0
		return
	}
	m.selected = (m.selected + delta + count) % count
}

func (m *ToolActivityModel) ToggleSelected() {
	index := 0
	for i := range m.entries {
		if m.entries[i].Kind != "tool" {
			continue
		}
		if index == m.selected {
			m.entries[i].Expanded = !m.entries[i].Expanded
			return
		}
		index++
	}
}

func (m *ToolActivityModel) View(width int, focused bool) string {
	if len(m.entries) == 0 {
		return ""
	}
	width = max(1, width)
	var lines []string
	toolIndex := 0
	for _, entry := range m.entries {
		switch entry.Kind {
		case "run":
			lines = append(lines, m.renderRun(entry, width))
		case "turn":
			lines = append(lines, ansi.Truncate(
				lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render(
					fmt.Sprintf("— turn %d/%d —", entry.Turn, entry.MaxTurns),
				), width, "…"))
		case "tool":
			selected := focused && toolIndex == m.selected
			lines = append(lines, m.renderTool(entry, width, selected)...)
			toolIndex++
		}
	}
	return strings.Join(lines, "\n")
}

func (m *ToolActivityModel) SelectedLine(width int) int {
	if !m.HasTools() {
		return 0
	}
	for index, line := range strings.Split(ansi.Strip(m.View(width, true)), "\n") {
		if strings.HasPrefix(line, "› ") {
			return index
		}
	}
	return 0
}

func (m *ToolActivityModel) renderRun(entry ToolActivityEntry, width int) string {
	if entry.Status == "started" {
		text := "run started"
		if entry.Detail != "" {
			text += " — " + firstLine(entry.Detail)
		}
		return ansi.Truncate(lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render(text), width, "…")
	}
	icon := "✓"
	color := m.theme.StatusHighlight
	switch entry.Status {
	case "completed", "succeeded":
	case "stopped":
		icon = "◇"
		color = m.theme.Secondary
	case "canceled":
		icon = "■"
		color = m.theme.TextMuted
	default:
		icon = "✗"
		color = lipgloss.Color("#FF6B6B")
	}
	text := fmt.Sprintf("%s run %s · %d turns · %d tools", icon, entry.Status, entry.Turn, entry.ToolCalls)
	if entry.Detail != "" {
		text += " — " + firstLine(entry.Detail)
	}
	return ansi.Truncate(lipgloss.NewStyle().Foreground(color).Render(text), width, "…")
}

func (m *ToolActivityModel) renderTool(entry ToolActivityEntry, width int, selected bool) []string {
	icon := "▶"
	color := m.theme.TextSecondary
	if entry.Status != "started" {
		icon = "✓"
		color = m.theme.StatusHighlight
		switch entry.Outcome {
		case "blocked":
			icon = "!"
			color = m.theme.Secondary
		case "rejected":
			icon = "✗"
			color = lipgloss.Color("#FF6B6B")
		default:
			if entry.Status != "completed" && entry.Status != "succeeded" {
				icon = "✗"
				color = lipgloss.Color("#FF6B6B")
			}
		}
	}
	prefix := "  "
	if selected {
		prefix = "› "
	}
	elapsed := ""
	if !entry.StartedAt.IsZero() && !entry.FinishedAt.IsZero() {
		elapsed = "  " + entry.FinishedAt.Sub(entry.StartedAt).Round(time.Millisecond).String()
	}
	line := fmt.Sprintf("%s%s %-7s %s%s", prefix, icon, entry.Tool, firstLine(entry.Detail), elapsed)
	lines := []string{ansi.Truncate(lipgloss.NewStyle().Foreground(color).Render(line), width, "…")}
	if entry.Expanded && entry.Detail != "" {
		for _, detailLine := range strings.Split(entry.Detail, "\n") {
			lines = append(lines, ansi.Truncate(
				lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("    "+detailLine),
				width, "…"))
			if len(lines) == 9 {
				break
			}
		}
	}
	return lines
}

func firstLine(value string) string {
	if line, _, ok := strings.Cut(strings.TrimSpace(value), "\n"); ok {
		return line
	}
	return strings.TrimSpace(value)
}
