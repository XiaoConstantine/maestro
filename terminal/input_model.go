package terminal

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// InputCommandHandler is called when a slash command is entered.
type InputCommandHandler func(cmd string, args []string) tea.Cmd

// InputQuestionHandler is called when a natural language question is entered.
type InputQuestionHandler func(question string) (tea.Cmd, bool)

// InputModel handles text input and command parsing.
type InputModel struct {
	textarea textarea.Model
	theme    *Theme
	styles   *Styles

	// Handlers
	onCommand  InputCommandHandler
	onQuestion InputQuestionHandler

	// History
	history      []string
	historyIndex int

	// Dimensions
	width  int
	height int

	// State
	focused bool

	// Command autocomplete
	showSuggestions       bool
	suggestions           []Command
	selectedSuggestion    int
	maxVisibleSuggestions int
	allCommands           []Command // All available commands for autocomplete
}

// NewInputModel creates a new input model.
func NewInputModel(theme *Theme, onCommand InputCommandHandler, onQuestion InputQuestionHandler) *InputModel {
	ta := textarea.New()
	ta.Placeholder = "Describe a coding task…"
	ta.CharLimit = 4000
	ta.SetWidth(80)
	ta.SetHeight(3) // Allow multi-line input like Crush
	ta.ShowLineNumbers = false
	ta.Focus()
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j", "new line"),
	)

	// Style the textarea with v2 API
	ta.SetStyles(textarea.Styles{
		Focused: textarea.StyleState{
			Base:        lipgloss.NewStyle().Foreground(theme.TextPrimary),
			Text:        lipgloss.NewStyle().Foreground(theme.TextPrimary),
			CursorLine:  lipgloss.NewStyle(),
			Placeholder: lipgloss.NewStyle().Foreground(theme.TextMuted),
			Prompt:      lipgloss.NewStyle().Foreground(theme.TextMuted),
		},
		Blurred: textarea.StyleState{
			Base:        lipgloss.NewStyle().Foreground(theme.TextSecondary),
			Text:        lipgloss.NewStyle().Foreground(theme.TextSecondary),
			CursorLine:  lipgloss.NewStyle(),
			Placeholder: lipgloss.NewStyle().Foreground(theme.TextMuted),
			Prompt:      lipgloss.NewStyle().Foreground(theme.TextMuted),
		},
	})

	m := &InputModel{
		textarea:              ta,
		theme:                 theme,
		styles:                theme.CreateStyles(),
		onCommand:             onCommand,
		onQuestion:            onQuestion,
		history:               []string{},
		historyIndex:          -1,
		focused:               true,
		maxVisibleSuggestions: 6,
		allCommands:           builtinCommands(),
	}

	// Set Crush-style prompt function
	ta.SetPromptFunc(4, m.promptFunc)

	return m
}

// Init initializes the input model.
func (m *InputModel) Init() tea.Cmd {
	return textarea.Blink
}

// Update handles messages.
func (m *InputModel) Update(msg tea.Msg) (*InputModel, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "tab":
			// Autocomplete command if showing suggestions.
			if m.suggestionsActive() {
				selected := m.suggestions[m.selectedSuggestion]
				m.textarea.SetValue("/" + selected.Name + " ")
				m.textarea.MoveToEnd()
				m.showSuggestions = false
				m.suggestions = nil
				m.selectedSuggestion = 0
				return m, nil
			}

		case "enter":
			// If visible suggestions are showing, select the current one.
			if m.suggestionsActive() {
				selected := m.suggestions[m.selectedSuggestion]
				m.textarea.SetValue("/" + selected.Name + " ")
				m.textarea.MoveToEnd()
				m.showSuggestions = false
				m.suggestions = nil
				m.selectedSuggestion = 0
				return m, nil
			}

			// Get the input value
			value := strings.TrimSpace(m.textarea.Value())
			if value == "" {
				return m, nil
			}

			var questionCmd tea.Cmd
			if !strings.HasPrefix(value, "/") && m.onQuestion != nil {
				var accepted bool
				questionCmd, accepted = m.onQuestion(value)
				if !accepted {
					return m, questionCmd
				}
			}

			// Add accepted input to history, then clear the editor.
			m.addToHistory(value)
			m.textarea.Reset()
			m.showSuggestions = false
			m.suggestions = nil
			m.selectedSuggestion = 0

			if strings.HasPrefix(value, "/") {
				return m, m.parseCommand(value)
			}
			return m, questionCmd

		case "up":
			// Navigate suggestions if showing.
			if m.suggestionsActive() {
				m.selectedSuggestion--
				if m.selectedSuggestion < 0 {
					m.selectedSuggestion = len(m.suggestions) - 1
				}
				return m, nil
			}
			// Navigate history up
			if m.textarea.Value() == "" || m.historyIndex >= 0 {
				if m.historyIndex < len(m.history)-1 {
					m.historyIndex++
					m.textarea.SetValue(m.history[len(m.history)-1-m.historyIndex])
				}
				return m, nil
			}

		case "down":
			// Navigate suggestions if showing.
			if m.suggestionsActive() {
				m.selectedSuggestion++
				if m.selectedSuggestion >= len(m.suggestions) {
					m.selectedSuggestion = 0
				}
				return m, nil
			}
			// Navigate history down
			if m.historyIndex > 0 {
				m.historyIndex--
				m.textarea.SetValue(m.history[len(m.history)-1-m.historyIndex])
			} else if m.historyIndex == 0 {
				m.historyIndex = -1
				m.textarea.Reset()
			}
			return m, nil

		case "esc":
			// Close suggestions
			if m.showSuggestions {
				m.showSuggestions = false
				m.suggestions = nil
				m.selectedSuggestion = 0
				return m, nil
			}

		case "ctrl+u":
			// Clear line
			m.textarea.Reset()
			m.historyIndex = -1
			m.showSuggestions = false
			return m, nil

		case "ctrl+w":
			// Delete word
			value := m.textarea.Value()
			if value != "" {
				// Find last space and truncate
				lastSpace := strings.LastIndex(strings.TrimRight(value, " "), " ")
				if lastSpace > 0 {
					m.textarea.SetValue(value[:lastSpace+1])
				} else {
					m.textarea.Reset()
				}
			}
			return m, nil
		}
	}

	// Update textarea
	m.textarea, cmd = m.textarea.Update(msg)

	// Update command suggestions based on input
	m.updateSuggestions()

	return m, cmd
}

func (m *InputModel) suggestionsActive() bool {
	return m.maxVisibleSuggestions > 0 && m.showSuggestions && len(m.suggestions) > 0
}

func (m *InputModel) HasActiveSuggestions() bool {
	return m.suggestionsActive()
}

// updateSuggestions updates command suggestions based on current input.
func (m *InputModel) updateSuggestions() {
	value := m.textarea.Value()

	// Only show suggestions if typing a command
	if !strings.HasPrefix(value, "/") {
		m.showSuggestions = false
		m.suggestions = nil
		m.selectedSuggestion = 0
		return
	}

	// Get the partial command (without leading /)
	partial := strings.TrimPrefix(value, "/")

	// Handle empty partial (just typed "/")
	if partial == "" {
		m.showSuggestions = true
		m.suggestions = m.allCommands
		m.selectedSuggestion = 0
		return
	}

	// Get just the command part (first word)
	fields := strings.Fields(partial)
	if len(fields) == 0 {
		m.showSuggestions = true
		m.suggestions = m.allCommands
		return
	}
	partial = strings.ToLower(fields[0])

	// If there's a space after the command, don't show suggestions
	if strings.Contains(value, " ") {
		m.showSuggestions = false
		m.suggestions = nil
		return
	}

	// Filter matching commands
	var matches []Command
	for _, cmd := range m.allCommands {
		if strings.HasPrefix(strings.ToLower(cmd.Name), partial) {
			matches = append(matches, cmd)
		}
	}

	if len(matches) > 0 {
		m.showSuggestions = true
		m.suggestions = matches
		// Reset selection if it's out of bounds
		if m.selectedSuggestion >= len(matches) {
			m.selectedSuggestion = 0
		}
	} else {
		m.showSuggestions = false
		m.suggestions = nil
	}
}

// View renders the input with Crush-style appearance.
func (m *InputModel) View() string {
	container := lipgloss.NewStyle().
		Width(max(1, m.width)).
		Padding(0, 1)

	muted := lipgloss.NewStyle().Foreground(m.theme.TextMuted)
	headerParts := make([]string, 0, 2)
	if m.width >= 48 {
		headerParts = append(headerParts, muted.Render("enter run  •  ctrl+j newline  •  / commands"))
	}
	if lines := strings.Count(m.textarea.Value(), "\n") + 1; lines > 1 {
		headerParts = append(headerParts, muted.Render(
			fmt.Sprintf("%d lines · %d chars", lines, utf8.RuneCountInString(m.textarea.Value())),
		))
	}

	parts := make([]string, 0, 3)
	if len(headerParts) > 0 {
		header := ansi.Truncate(strings.Join(headerParts, "  "), max(1, m.width-4), "…")
		parts = append(parts, header)
	}
	if m.showSuggestions && len(m.suggestions) > 0 && m.maxVisibleSuggestions > 0 {
		parts = append(parts, m.renderSuggestions())
	}
	parts = append(parts, m.textarea.View())
	return container.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// renderSuggestions renders the command autocomplete suggestions.
func (m *InputModel) renderSuggestions() string {
	var lines []string

	// Style for suggestions
	normalStyle := lipgloss.NewStyle().
		Foreground(m.theme.TextSecondary).
		PaddingLeft(2)

	selectedStyle := lipgloss.NewStyle().
		Foreground(m.theme.TextPrimary).
		Background(m.theme.Surface).
		PaddingLeft(2)

	descStyle := lipgloss.NewStyle().
		Foreground(m.theme.TextMuted)

	limit := max(1, m.maxVisibleSuggestions)
	start := max(0, m.selectedSuggestion-limit+1)
	end := min(len(m.suggestions), start+limit)
	for i := start; i < end; i++ {
		cmd := m.suggestions[i]
		style := normalStyle
		if i == m.selectedSuggestion {
			style = selectedStyle
		}

		line := style.Render("/" + cmd.Name + " ")
		line += descStyle.Render(cmd.Description)
		lines = append(lines, ansi.Truncate(line, max(1, m.width-4), "…"))
	}
	if hidden := len(m.suggestions) - (end - start); hidden > 0 {
		lines = append(lines, descStyle.Render(fmt.Sprintf("  … %d more", hidden)))
	}

	// Add hint at bottom
	hintStyle := lipgloss.NewStyle().
		Foreground(m.theme.TextMuted).
		PaddingLeft(2)
	lines = append(lines, ansi.Truncate(
		hintStyle.Render("↑↓ navigate • tab/enter select • esc cancel"),
		max(1, m.width-4),
		"…",
	))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// promptFunc returns the Crush-style prompt for each line.
func (m *InputModel) promptFunc(info textarea.PromptInfo) string {
	// First line gets "> " prompt
	if info.LineNumber == 0 {
		return lipgloss.NewStyle().
			Foreground(m.theme.TextPrimary).
			Render("  > ")
	}
	// Continuation lines get "::" dots like Crush
	if info.Focused {
		return lipgloss.NewStyle().
			Foreground(m.theme.StatusHighlight).
			Render("::: ")
	}
	return lipgloss.NewStyle().
		Foreground(m.theme.TextMuted).
		Render("::: ")
}

// SetSize sets the input dimensions.
func (m *InputModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.textarea.SetWidth(max(1, width-4)) // Account for prompt and padding
	m.textarea.SetHeight(max(1, height))
}

// SetSuggestionLimit caps autocomplete rows so the editor remains visible.
func (m *InputModel) SetSuggestionLimit(limit int) {
	m.maxVisibleSuggestions = max(0, limit)
}

// Focus sets focus on the input.
func (m *InputModel) Focus() tea.Cmd {
	m.focused = true
	return m.textarea.Focus()
}

// Blur removes focus from the input.
func (m *InputModel) Blur() {
	m.focused = false
	m.textarea.Blur()
}

// IsFocused returns whether the input is focused.
func (m *InputModel) IsFocused() bool {
	return m.focused
}

// Value returns the current input value.
func (m *InputModel) Value() string {
	return m.textarea.Value()
}

// SetValue sets the input value.
func (m *InputModel) SetValue(value string) {
	m.textarea.SetValue(value)
}

// parseCommand parses a slash command and calls the handler.
func (m *InputModel) parseCommand(input string) tea.Cmd {
	// Remove leading slash
	input = strings.TrimPrefix(input, "/")

	// Split into command and args
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	if m.onCommand != nil {
		return m.onCommand(cmd, args)
	}
	return nil
}

// addToHistory adds an entry to command history.
func (m *InputModel) addToHistory(value string) {
	// Don't add duplicates of the last entry
	if len(m.history) > 0 && m.history[len(m.history)-1] == value {
		return
	}

	m.history = append(m.history, value)

	// Limit history size
	if len(m.history) > 100 {
		m.history = m.history[1:]
	}

	// Reset history navigation
	m.historyIndex = -1
}

func (m *InputModel) removeCommand(name string) {
	commands := m.allCommands[:0]
	for _, command := range m.allCommands {
		if command.Name != name {
			commands = append(commands, command)
		}
	}
	m.allCommands = commands
	m.updateSuggestions()
}

// GetHistory returns the command history.
func (m *InputModel) GetHistory() []string {
	return m.history
}
