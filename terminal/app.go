package terminal

import (
	"fmt"
	"os"
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

// Model represents the modern terminal application state.
type Model struct {
	// Core UI components
	fileTree       *FileTreeModel
	todoList       *TodoListModel
	commandPalette *CommandPaletteModel
	splitPane      *SplitPaneModel
	statusBar      *StatusBarModel
	keyHandler     *KeyHandler

	// Legacy components for main content area
	viewport viewport.Model
	input    textarea.Model

	// State
	messages []Message
	width    int
	height   int
	ready    bool
	modernUI bool // Whether to use modern UI layout

	// Theme and styling
	theme      *Theme
	styles     *Styles
	compStyles *ComponentStyles
	renderer   *glamour.TermRenderer

	// Input state
	inputFocused bool
	history      []string
	historyIndex int
}

// New creates a new terminal model.
func New() *Model {
	return NewWithOptions(false)
}

// NewModern creates a new terminal model with modern UI enabled.
func NewModern() *Model {
	return NewWithOptions(true)
}

// NewWithOptions creates a new terminal model with specified options.
func NewWithOptions(modernUI bool) *Model {
	theme := ClaudeCodeTheme()
	styles := theme.CreateStyles()
	compStyles := theme.CreateComponentStyles()

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
		compStyles:   compStyles,
		renderer:     renderer,
		inputFocused: true,
		history:      []string{},
		modernUI:     modernUI,
	}

	// Initialize modern UI components if enabled
	if modernUI {
		// Get current working directory for file tree
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "." // Fallback
		}

		// Initialize components
		m.fileTree, _ = NewFileTree(cwd, theme)
		m.todoList = NewTodoList(theme)
		m.commandPalette = NewCommandPalette(theme)
		m.splitPane = NewSplitPane(theme)
		m.statusBar = NewStatusBar(theme)
		m.keyHandler = NewKeyHandler()

		// Set up initial layout
		m.splitPane.SetFocusedPane(FocusMainContent)
		m.statusBar.SetMode(m.keyHandler.GetModeString())
		m.statusBar.SetMessage("Modern Maestro UI - Press ? for help")

		// Add initial agent thinking items
		m.addAgentThought("Explore project structure and understand what dspy-go is")
		m.addAgentThought("Read README, go.mod, and other key documentation files")
		m.addAgentThought("Use Oracle for comprehensive codebase review and improvement …")

		// Don't focus input initially in modern mode
		ta.Blur()
	} else {
		// Focus input in legacy mode
		ta.Focus()
		// Add welcome message
		m.addMessage("assistant", "Claude Code\n\nI can help you with code review, analysis, and development tasks. What would you like to work on?")
	}

	return m
}

// Init initializes the model.
func (m *Model) Init() tea.Cmd {
	var cmds []tea.Cmd

	cmds = append(cmds, tea.EnterAltScreen)

	if m.modernUI {
		// Initialize modern components
		if m.fileTree != nil {
			cmds = append(cmds, m.fileTree.Init())
		}
		if m.todoList != nil {
			cmds = append(cmds, m.todoList.Init())
		}
		if m.commandPalette != nil {
			cmds = append(cmds, m.commandPalette.Init())
		}
		if m.splitPane != nil {
			cmds = append(cmds, m.splitPane.Init())
		}
		if m.statusBar != nil {
			cmds = append(cmds, m.statusBar.Init())
		}
	} else {
		cmds = append(cmds, textarea.Blink)
	}

	return tea.Batch(cmds...)
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	// Handle modern UI differently
	if m.modernUI {
		return m.updateModernUI(msg)
	}

	// Legacy UI handling
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

// updateModernUI handles updates for the modern UI layout.
func (m *Model) updateModernUI(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true

		// Update status and palette widths
		if m.statusBar != nil {
			updatedStatusBar, _ := m.statusBar.Update(msg)
			*m.statusBar = updatedStatusBar
		}
		if m.commandPalette != nil {
			updatedCommandPalette, _ := m.commandPalette.Update(msg)
			*m.commandPalette = updatedCommandPalette
		}

		// Compute Amp-style layout sizing
		statusHeight := 1
		bottomHeight := m.height / 3
		if bottomHeight < 6 {
			bottomHeight = 6
		} else if bottomHeight > 12 {
			bottomHeight = 12
		}
		topHeight := m.height - statusHeight - bottomHeight
		if topHeight < 3 {
			topHeight = 3
		}
		leftWidth := int(float64(m.width) * 0.65)
		if leftWidth < 30 {
			leftWidth = m.width - 30
		}
		if leftWidth > m.width-20 {
			leftWidth = m.width - 20
		}

		// Update main content area sizing
		m.viewport.Width = m.width - 2
		m.viewport.Height = topHeight - 2
		m.input.SetWidth(max(10, leftWidth-4))

		// Re-render messages with new width
		m.renderMessages()

	case tea.KeyMsg:
		// Command palette takes priority
		if m.commandPalette != nil && m.commandPalette.IsVisible() {
			updatedCommandPalette, cmd := m.commandPalette.Update(msg)
			*m.commandPalette = updatedCommandPalette
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

		// Process key through vim-style handler
		action, data := m.keyHandler.HandleKey(msg)

		// Update status bar with current mode
		if m.statusBar != nil {
			m.statusBar.SetMode(m.keyHandler.GetModeString())
		}

		// Handle the action
		cmd := m.handleAction(action, data)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

		// If we're in insert mode, pass keys to the input field and handle Enter
		if m.keyHandler != nil && m.keyHandler.GetMode() == ModeInsert {
			switch msg.String() {
			case "enter":
				if strings.TrimSpace(m.input.Value()) != "" {
					content := strings.TrimSpace(m.input.Value())
					m.sendMessage(content)
					m.input.Reset()
					// Simulate assistant response
					cmds = append(cmds, m.simulateResponse(content))
				}
			default:
				var icmd tea.Cmd
				m.input, icmd = m.input.Update(msg)
				if icmd != nil {
					cmds = append(cmds, icmd)
				}
			}
		}

	case CommandResultMsg:
		// Handle command palette results
		if msg.Error != nil {
			m.statusBar.SetMessage(fmt.Sprintf("Error: %v", msg.Error))
		} else {
			m.statusBar.SetMessage(msg.Result)
		}

	default:
		// Update components that need continuous updates
		if m.statusBar != nil {
			updatedStatusBar, cmd := m.statusBar.Update(msg)
			*m.statusBar = updatedStatusBar
			cmds = append(cmds, cmd)
		}
	}

	// Update focused state for input based on pane focus (Amp layout uses main input)
	m.inputFocused = (m.keyHandler != nil && m.keyHandler.GetMode() == ModeInsert)

	return m, tea.Batch(cmds...)
}

// handleAction processes vim-style actions.
func (m *Model) handleAction(action ActionType, data string) tea.Cmd {
	switch action {
	case ActionQuit:
		return tea.Quit

	case ActionSwitchPane:
		if m.splitPane != nil {
			updatedSplitPane, _ := m.splitPane.Update(tea.KeyMsg{Type: tea.KeyTab})
			*m.splitPane = updatedSplitPane
		}

	case ActionToggleFileTree:
		// Toggle file tree visibility
		m.statusBar.SetMessage("File tree toggle not implemented yet")

	case ActionToggleTodoList:
		// Toggle TODO list visibility
		m.statusBar.SetMessage("TODO list toggle not implemented yet")

	case ActionEnterCommandMode:
		if m.commandPalette != nil {
			m.commandPalette.Show()
		}

	case ActionSearch:
		if m.commandPalette != nil {
			m.commandPalette.Show()
		}

	case ActionEnterInsertMode:
		m.input.Focus()

	case ActionEnterNormalMode:
		m.input.Blur()

	case ActionHelp:
		if m.keyHandler != nil {
			helpText := m.keyHandler.GetHelpText()
			m.addMessage("system", helpText)
		}

	case ActionNavigateUp, ActionNavigateDown, ActionNavigateLeft, ActionNavigateRight:
		// Navigation in Amp layout is handled per-component; keep placeholders for future
		// (No-op for now)

	case ActionExecuteCommand:
		// Handle command execution result
		m.statusBar.SetMessage(fmt.Sprintf("Command executed: %s", data))

	default:
		// Handle other actions or pass to default handler
		if action != ActionNone {
			m.statusBar.SetMessage(fmt.Sprintf("Action: %d", int(action)))
		}
	}

	return nil
}

// View renders the terminal UI.
func (m *Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	if m.modernUI {
		return m.viewModernUI()
	}

	// Legacy UI rendering
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

// viewModernUI renders the modern split-pane UI.
func (m *Model) viewModernUI() string {
	// Amp-style layout:
	// - Top: Agent output (header + viewport)
	// - Bottom-left: Input
	// - Bottom-right: Agent plan / TODOs
	// - Status bar at bottom

	width := m.width
	height := m.height
	if width <= 0 || height <= 0 {
		return ""
	}

	statusBarView := ""
	if m.statusBar != nil {
		statusBarView = m.statusBar.View()
	}
	statusHeight := 1

	// Compute areas
	bottomHeight := height / 3
	if bottomHeight < 6 {
		bottomHeight = 6
	} else if bottomHeight > 12 {
		bottomHeight = 12
	}
	topHeight := height - statusHeight - bottomHeight
	if topHeight < 3 {
		topHeight = 3
	}

	leftWidth := int(float64(width) * 0.65)
	if leftWidth < 30 {
		leftWidth = width - 30
	}
	if leftWidth > width-20 {
		leftWidth = width - 20
	}
	rightWidth := max(20, width-leftWidth)

	// Render top (header + conversation)
	var topSections []string
	header := m.styles.Header.Render("◉ Agent Output")
	topSections = append(topSections, header)
	topSections = append(topSections, m.viewport.View())
	topContent := strings.Join(topSections, "\n")
	topLines := strings.Split(topContent, "\n")
	// Fit to topHeight
	if len(topLines) > topHeight {
		topLines = topLines[:topHeight]
	} else if len(topLines) < topHeight {
		for len(topLines) < topHeight {
			topLines = append(topLines, strings.Repeat(" ", max(0, width-2)))
		}
	}

	// Render bottom-left input (single prominent line, rest empty)
	leftLines := make([]string, 0, bottomHeight)
	inputLine := m.renderInput()
	// Clamp/pad to leftWidth
	if lipgloss.Width(inputLine) > leftWidth {
		inputLine = inputLine[:max(0, leftWidth-3)] + "..."
	} else if lipgloss.Width(inputLine) < leftWidth {
		inputLine += strings.Repeat(" ", leftWidth-lipgloss.Width(inputLine))
	}
	leftLines = append(leftLines, inputLine)
	for len(leftLines) < bottomHeight {
		leftLines = append(leftLines, strings.Repeat(" ", leftWidth))
	}

	// Render bottom-right TODOs (compact agent plan)
	rightLines := make([]string, 0, bottomHeight)
	title := m.compStyles.ToolHeader.Render("Agent Plan")
	if lipgloss.Width(title) > rightWidth {
		title = title[:max(0, rightWidth-3)] + "..."
	} else if lipgloss.Width(title) < rightWidth {
		title += strings.Repeat(" ", rightWidth-lipgloss.Width(title))
	}
	rightLines = append(rightLines, title)
	// Items
	if m.todoList != nil && len(m.todoList.items) > 0 {
		maxItems := bottomHeight - 1
		for i := 0; i < min(maxItems, len(m.todoList.items)); i++ {
			item := m.todoList.items[i]
			checkbox := "☐"
			style := m.compStyles.TodoPending
			content := item.Content
			switch item.Status {
			case TodoInProgress:
				checkbox = "◉"
				style = m.compStyles.TodoActive
				if item.ActiveForm != "" {
					content = item.ActiveForm
				}
				if item.Progress > 0 {
					content = fmt.Sprintf("%s [%d%%]", content, item.Progress)
				}
			case TodoCompleted:
				checkbox = "✓"
				style = m.compStyles.TodoCompleted
			}
			line := fmt.Sprintf(" %s %s", m.compStyles.TodoCheckbox.Render(checkbox), content)
			line = style.Render(line)
			if lipgloss.Width(line) > rightWidth {
				line = line[:max(0, rightWidth-3)] + "..."
			} else if lipgloss.Width(line) < rightWidth {
				line += strings.Repeat(" ", rightWidth-lipgloss.Width(line))
			}
			rightLines = append(rightLines, line)
		}
	} else {
		empty := m.compStyles.TodoItem.Foreground(m.theme.TextMuted).Render("• Waiting for agent thoughts...")
		if lipgloss.Width(empty) > rightWidth {
			empty = empty[:max(0, rightWidth-3)] + "..."
		} else if lipgloss.Width(empty) < rightWidth {
			empty += strings.Repeat(" ", rightWidth-lipgloss.Width(empty))
		}
		rightLines = append(rightLines, empty)
	}
	for len(rightLines) < bottomHeight {
		rightLines = append(rightLines, strings.Repeat(" ", rightWidth))
	}

	// Combine bottom area line by line
	bottomLines := make([]string, 0, bottomHeight)
	for i := 0; i < bottomHeight; i++ {
		bottomLines = append(bottomLines, leftLines[i]+rightLines[i])
	}

	// Join all sections and append status bar
	layoutLines := append(topLines, bottomLines...)
	layout := strings.Join(layoutLines, "\n")
	layout = layout + "\n" + statusBarView

	// Render command palette overlay if visible
	if m.commandPalette != nil && m.commandPalette.IsVisible() {
		commandPaletteView := m.commandPalette.View()
		return m.overlayCommandPalette(layout, commandPaletteView)
	}

	return layout
}

// overlayCommandPalette overlays the command palette on top of the main UI.
func (m *Model) overlayCommandPalette(background, overlay string) string {
	backgroundLines := strings.Split(background, "\n")
	overlayLines := strings.Split(overlay, "\n")

	// Center the overlay
	startY := (len(backgroundLines) - len(overlayLines)) / 2
	if startY < 0 {
		startY = 0
	}

	// Apply overlay
	for i, overlayLine := range overlayLines {
		if startY+i < len(backgroundLines) {
			// Simple overlay - replace the background line
			backgroundLines[startY+i] = overlayLine
		}
	}

	return strings.Join(backgroundLines, "\n")
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

// addAgentThought adds an agent thinking item to the TODO panel.
func (m *Model) addAgentThought(thought string) {
	if m.modernUI && m.todoList != nil {
		m.todoList.AddTodo(thought, "")
	}
}
