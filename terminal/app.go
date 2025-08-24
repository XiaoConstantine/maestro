package terminal

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// Message represents a conversation message.
type Message struct {
	Role      string // "user" or "assistant"
	Content   string
	Timestamp time.Time
}

// Model represents the terminal application state.
type Model struct {
	// Core components
	viewport viewport.Model
	input    textarea.Model

	// State
	messages []Message
	width    int
	height   int
	ready    bool

	// Theme and styling
	theme    *Theme
	styles   *Styles
	renderer *glamour.TermRenderer

	// Input state
	inputFocused bool
	history      []string
	historyIndex int
}

// New creates a new terminal model.
func New() *Model {
	theme := ClaudeCodeTheme()
	styles := theme.CreateStyles()

	// Configure input area - minimal terminal style
	ta := textarea.New()
	ta.Placeholder = ""
	ta.CharLimit = 4000
	ta.SetWidth(80)
	ta.SetHeight(1) // Single line like terminal
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = styles.InputText
	ta.BlurredStyle.Base = styles.InputText
	ta.Focus()

	// Configure viewport for conversation
	vp := viewport.New(80, 20)

	// Configure minimal markdown renderer
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"), // Use dark theme but minimal
		glamour.WithWordWrap(80),
	)

	m := &Model{
		viewport:     vp,
		input:        ta,
		messages:     []Message{},
		theme:        theme,
		styles:       styles,
		renderer:     renderer,
		inputFocused: true,
		history:      []string{},
	}

	// Add welcome message
	m.addMessage("assistant", "Claude Code\n\nI can help you with code review, analysis, and development tasks. What would you like to work on?")

	return m
}

// Init initializes the model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		tea.EnterAltScreen,
	)
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true

		// Update component sizes
		m.viewport.Width = m.width - 2
		m.viewport.Height = m.height - 4 // Reserve space for input and borders
		m.input.SetWidth(m.width - 4)    // Account for prompt

		// Re-render messages with new width
		m.renderMessages()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "enter":
			if m.input.Value() != "" {
				content := strings.TrimSpace(m.input.Value())
				m.sendMessage(content)
				m.input.Reset()

				// Simulate assistant response
				cmds = append(cmds, m.simulateResponse(content))
			}

		case "ctrl+l":
			// Clear screen like terminal
			m.messages = []Message{}
			m.addMessage("assistant", "Claude Code\n\nScreen cleared. How can I help you?")

		case "up":
			if m.input.Value() == "" && len(m.history) > 0 {
				// Navigate history
				if m.historyIndex > 0 {
					m.historyIndex--
					m.input.SetValue(m.history[m.historyIndex])
				}
				return m, nil
			}
			fallthrough

		case "down":
			if m.input.Value() == "" && m.historyIndex < len(m.history)-1 {
				// Navigate history
				m.historyIndex++
				m.input.SetValue(m.history[m.historyIndex])
				return m, nil
			}
			fallthrough

		default:
			// Pass to input
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
		}

	default:
		// Update components
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)

		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// View renders the terminal UI.
func (m *Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Build the layout - clean and minimal
	var sections []string

	// Header - minimal, just like Claude Code
	header := m.styles.Header.Render("Claude Code")
	sections = append(sections, header)

	// Conversation area
	conversation := m.viewport.View()
	sections = append(sections, conversation)

	// Input area with terminal prompt
	inputLine := m.renderInput()
	sections = append(sections, inputLine)

	// Join with minimal spacing
	content := strings.Join(sections, "\n")

	// Apply container style
	return m.styles.Container.
		Width(m.width).
		Height(m.height).
		Render(content)
}

// renderInput creates the terminal-style input line.
func (m *Model) renderInput() string {
	prompt := m.styles.InputPrompt.Render("> ")
	input := m.input.View()

	return m.styles.InputContainer.Render(prompt + input)
}

// renderMessages renders all messages to the viewport.
func (m *Model) renderMessages() {
	var content strings.Builder

	for i, msg := range m.messages {
		rendered := m.renderMessage(msg)
		content.WriteString(rendered)

		// Add spacing between messages
		if i < len(m.messages)-1 {
			content.WriteString("\n\n")
		}
	}

	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

// renderMessage renders a single message with minimal styling.
func (m *Model) renderMessage(msg Message) string {
	var content string

	switch msg.Role {
	case "user":
		// Simple "user:" prefix like terminal
		prefix := m.styles.UserPrefix.Render("user: ")
		text := m.styles.UserMessage.Render(msg.Content)
		content = prefix + text

	case "assistant":
		// Simple "assistant:" prefix
		prefix := m.styles.AssistantPrefix.Render("assistant: ")

		// Use glamour for markdown but keep it minimal
		if m.renderer != nil {
			rendered, err := m.renderer.Render(msg.Content)
			if err == nil {
				text := strings.TrimSpace(rendered)
				content = prefix + text
			} else {
				text := m.styles.AssistantMessage.Render(msg.Content)
				content = prefix + text
			}
		} else {
			text := m.styles.AssistantMessage.Render(msg.Content)
			content = prefix + text
		}
	}

	return content
}

// addMessage adds a message to the conversation.
func (m *Model) addMessage(role, content string) {
	msg := Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	}

	m.messages = append(m.messages, msg)
	m.renderMessages()
}

// sendMessage sends a user message.
func (m *Model) sendMessage(content string) {
	// Add to history
	m.history = append(m.history, content)
	m.historyIndex = len(m.history)

	// Add user message
	m.addMessage("user", content)
}

// simulateResponse simulates an assistant response.
func (m *Model) simulateResponse(userInput string) tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		// Generate contextual response based on input
		response := m.generateResponse(userInput)
		m.addMessage("assistant", response)
		return nil
	})
}

// generateResponse creates a contextual response.
func (m *Model) generateResponse(input string) string {
	input = strings.ToLower(input)

	switch {
	case strings.Contains(input, "help"):
		return "I'm here to help with code review, development tasks, and technical questions. You can:\n\n• Ask me to review code\n• Get help with debugging\n• Discuss architecture decisions\n• Learn about best practices\n\nWhat specific area would you like help with?"

	case strings.Contains(input, "review") || strings.Contains(input, "code"):
		return "I'd be happy to help review your code! Please share:\n\n• The code you'd like reviewed\n• Specific concerns or areas to focus on\n• Context about what the code should do\n\nYou can paste code directly or reference files with @filename."

	case strings.Contains(input, "debug"):
		return "Let's debug this together. Please provide:\n\n• The error message or unexpected behavior\n• Relevant code sections\n• Steps to reproduce the issue\n• Your environment details\n\nWhat specific problem are you encountering?"

	case strings.Contains(input, "hello") || strings.Contains(input, "hi"):
		return "Hello! I'm Claude, ready to help with your code and development tasks. What are you working on today?"

	default:
		return fmt.Sprintf("I understand you're asking about: \"%s\"\n\nCould you provide more details about what you'd like me to help with? I'm here to assist with code review, debugging, architecture decisions, and other development tasks.", input)
	}
}
