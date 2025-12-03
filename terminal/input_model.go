package terminal

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// InputCommandHandler is called when a slash command is entered.
type InputCommandHandler func(cmd string, args []string) tea.Cmd

// InputQuestionHandler is called when a natural language question is entered.
type InputQuestionHandler func(question string) tea.Cmd

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
	showSuggestions    bool
	suggestions        []Command
	selectedSuggestion int
	allCommands        []Command // All available commands for autocomplete
}

// NewInputModel creates a new input model.
func NewInputModel(theme *Theme, onCommand InputCommandHandler, onQuestion InputQuestionHandler) *InputModel {
	ta := textarea.New()
	ta.Placeholder = "Ready for instructions"
	ta.CharLimit = 4000
	ta.SetWidth(80)
	ta.SetHeight(3) // Allow multi-line input like Crush
	ta.ShowLineNumbers = false
	ta.Focus()

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
		textarea:     ta,
		theme:        theme,
		styles:       theme.CreateStyles(),
		onCommand:    onCommand,
		onQuestion:   onQuestion,
		history:      []string{},
		historyIndex: -1,
		focused:      true,
		allCommands:  getBuiltinCommands(),
	}

	// Set Crush-style prompt function
	ta.SetPromptFunc(4, m.promptFunc)

	return m
}

// getBuiltinCommands returns the list of builtin commands for autocomplete.
func getBuiltinCommands() []Command {
	return []Command{
		{Name: "help", Description: "Show available commands", Category: "Help"},
		{Name: "review", Description: "Review a pull request", Category: "GitHub"},
		{Name: "ask", Description: "Ask a question about the repository", Category: "Maestro"},
		{Name: "claude", Description: "Send prompt to Claude Sonnet 4.5", Category: "Subagent"},
		{Name: "gemini", Description: "Send prompt to Gemini 3 Pro", Category: "Subagent"},
		{Name: "clear", Description: "Clear the conversation", Category: "System"},
		{Name: "exit", Description: "Exit Maestro", Category: "System"},
		{Name: "quit", Description: "Exit Maestro", Category: "System"},
	}
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
			// Autocomplete command if showing suggestions
			if m.showSuggestions && len(m.suggestions) > 0 {
				selected := m.suggestions[m.selectedSuggestion]
				m.textarea.SetValue("/" + selected.Name + " ")
				m.textarea.MoveToEnd()
				m.showSuggestions = false
				m.suggestions = nil
				m.selectedSuggestion = 0
				return m, nil
			}

		case "enter":
			// If showing suggestions, select the current one
			if m.showSuggestions && len(m.suggestions) > 0 {
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

			// Add to history
			m.addToHistory(value)

			// Clear input and suggestions
			m.textarea.Reset()
			m.showSuggestions = false
			m.suggestions = nil
			m.selectedSuggestion = 0

			// Parse and handle
			if strings.HasPrefix(value, "/") {
				return m, m.parseCommand(value)
			}
			if m.onQuestion != nil {
				return m, m.onQuestion(value)
			}

		case "up":
			// Navigate suggestions if showing
			if m.showSuggestions && len(m.suggestions) > 0 {
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
			// Navigate suggestions if showing
			if m.showSuggestions && len(m.suggestions) > 0 {
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
	// Container style - minimal padding like Crush
	container := lipgloss.NewStyle().
		Width(m.width).
		Padding(1)

	inputView := m.textarea.View()

	// Render suggestions if showing
	if m.showSuggestions && len(m.suggestions) > 0 {
		suggestionsView := m.renderSuggestions()
		return container.Render(lipgloss.JoinVertical(lipgloss.Left, suggestionsView, inputView))
	}

	return container.Render(inputView)
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

	for i, cmd := range m.suggestions {
		style := normalStyle
		if i == m.selectedSuggestion {
			style = selectedStyle
		}

		line := style.Render("/" + cmd.Name + " ")
		line += descStyle.Render(cmd.Description)
		lines = append(lines, line)
	}

	// Add hint at bottom
	hintStyle := lipgloss.NewStyle().
		Foreground(m.theme.TextMuted).
		PaddingLeft(2)
	lines = append(lines, hintStyle.Render("↑↓ navigate • tab/enter select • esc cancel"))

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
	m.textarea.SetWidth(width - 4) // Account for prompt and padding
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

// GetHistory returns the command history.
func (m *InputModel) GetHistory() []string {
	return m.history
}
