package terminal

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
}

// NewInputModel creates a new input model.
func NewInputModel(theme *Theme, onCommand InputCommandHandler, onQuestion InputQuestionHandler) *InputModel {
	ta := textarea.New()
	ta.Placeholder = "Type a command (/help) or ask a question..."
	ta.CharLimit = 4000
	ta.SetWidth(80)
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.Focus()

	// Style the textarea
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle().
		Foreground(theme.TextPrimary).
		Background(theme.Surface)
	ta.BlurredStyle.Base = lipgloss.NewStyle().
		Foreground(theme.TextSecondary).
		Background(theme.Surface)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().
		Foreground(theme.TextMuted)
	ta.BlurredStyle.Placeholder = lipgloss.NewStyle().
		Foreground(theme.TextMuted)

	return &InputModel{
		textarea:     ta,
		theme:        theme,
		styles:       theme.CreateStyles(),
		onCommand:    onCommand,
		onQuestion:   onQuestion,
		history:      []string{},
		historyIndex: -1,
		focused:      true,
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
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			// Get the input value
			value := strings.TrimSpace(m.textarea.Value())
			if value == "" {
				return m, nil
			}

			// Add to history
			m.addToHistory(value)

			// Clear input
			m.textarea.Reset()

			// Parse and handle
			if strings.HasPrefix(value, "/") {
				return m, m.parseCommand(value)
			}
			if m.onQuestion != nil {
				return m, m.onQuestion(value)
			}

		case "up":
			// Navigate history up
			if m.textarea.Value() == "" || m.historyIndex >= 0 {
				if m.historyIndex < len(m.history)-1 {
					m.historyIndex++
					m.textarea.SetValue(m.history[len(m.history)-1-m.historyIndex])
				}
				return m, nil
			}

		case "down":
			// Navigate history down
			if m.historyIndex > 0 {
				m.historyIndex--
				m.textarea.SetValue(m.history[len(m.history)-1-m.historyIndex])
			} else if m.historyIndex == 0 {
				m.historyIndex = -1
				m.textarea.Reset()
			}
			return m, nil

		case "ctrl+u":
			// Clear line
			m.textarea.Reset()
			m.historyIndex = -1
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
	return m, cmd
}

// View renders the input.
func (m *InputModel) View() string {
	// Prompt prefix
	prompt := lipgloss.NewStyle().
		Foreground(m.theme.Accent).
		Bold(true).
		Render("> ")

	// Input area
	input := m.textarea.View()

	// Container style
	container := lipgloss.NewStyle().
		Width(m.width).
		Padding(0, 1).
		Background(m.theme.Surface)

	return container.Render(prompt + input)
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
