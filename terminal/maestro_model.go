package terminal

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// MaestroModel is the root TUI model that manages all modes.
type MaestroModel struct {
	// Current mode
	mode MaestroMode

	// Sub-models
	inputModel  *InputModel
	reviewModel *ReviewModel

	// Shared components
	statusBar      *StatusBarModel
	commandPalette *CommandPaletteModel
	keyHandler     *KeyHandler

	// Theming
	theme  *Theme
	styles *Styles

	// Conversation state
	messages []Message
	viewport viewport.Model

	// Dimensions
	width  int
	height int
	ready  bool

	// Backend
	backend MaestroBackend
	config  *MaestroConfig
	ctx     context.Context

	// Program reference for sending async updates
	program *tea.Program
}

// ProgressMsg is sent to update the UI with progress information.
type ProgressMsg struct {
	Status string
	Detail string
}

// NewMaestroModel creates a new root TUI model.
func NewMaestroModel(cfg *MaestroConfig, backend MaestroBackend) *MaestroModel {
	theme := ClaudeCodeTheme()
	styles := theme.CreateStyles()

	m := &MaestroModel{
		mode:           ModeInput,
		statusBar:      NewStatusBar(theme),
		commandPalette: NewCommandPalette(theme),
		keyHandler:     NewKeyHandler(),
		theme:          theme,
		styles:         styles,
		messages:       []Message{},
		viewport:       viewport.New(80, 20),
		backend:        backend,
		config:         cfg,
		ctx:            context.Background(),
	}

	// Create input model
	m.inputModel = NewInputModel(theme, m.handleCommand, m.handleQuestion)

	// Set up status bar
	m.statusBar.SetMode("INPUT")
	if cfg != nil {
		m.statusBar.SetMessage(fmt.Sprintf("Repository: %s/%s", cfg.Owner, cfg.Repo))
	}

	// Register command handlers
	m.registerCommands()

	// Add welcome message
	m.addMessage("assistant", "Welcome to Maestro! Type /help for commands or ask questions directly.")

	return m
}

// Init initializes the model.
func (m *MaestroModel) Init() tea.Cmd {
	return tea.Batch(
		tea.EnterAltScreen,
		tea.WindowSize(),
		m.inputModel.Init(),
	)
}

// Update handles messages.
func (m *MaestroModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true

		// Update component sizes
		m.updateLayout()

		// Forward to sub-models
		if m.inputModel != nil {
			m.inputModel.SetSize(m.width, 3)
		}
		if m.statusBar != nil {
			newSB, cmd := m.statusBar.Update(msg)
			m.statusBar = &newSB
			cmds = append(cmds, cmd)
		}

	case tea.KeyMsg:
		// Global key handling
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "ctrl+p":
			// Toggle command palette
			if m.commandPalette.IsVisible() {
				m.commandPalette.Hide()
			} else {
				m.commandPalette.Show()
			}
			return m, nil

		case "esc":
			// Handle escape based on current state
			if m.commandPalette.IsVisible() {
				m.commandPalette.Hide()
				return m, nil
			}
			if m.mode == ModeReview {
				m.mode = ModeInput
				m.statusBar.SetMode("INPUT")
				return m, nil
			}
		}

		// Route to command palette if visible
		if m.commandPalette.IsVisible() {
			newCP, cmd := m.commandPalette.Update(msg)
			m.commandPalette = &newCP
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

		// Route to current mode
		switch m.mode {
		case ModeInput:
			newInput, cmd := m.inputModel.Update(msg)
			m.inputModel = newInput
			cmds = append(cmds, cmd)

		case ModeReview:
			if m.reviewModel != nil {
				newReview, cmd := m.reviewModel.Update(msg)
				m.reviewModel = newReview.(*ReviewModel)
				cmds = append(cmds, cmd)
			}
		}

	case ModeTransition:
		m.handleModeTransition(msg)

	case CommandResultMsg:
		// Handle command execution result
		if msg.Error != nil {
			m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
		} else if msg.Result != "" {
			m.addMessage("assistant", msg.Result)
		}

	case ResponseMsg:
		// Handle async response from backend
		m.addMessage("assistant", msg.Content)
		m.statusBar.SetMessage("")

	case ErrorMsg:
		m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
		m.statusBar.SetMessage("")

	case ProgressMsg:
		// Update status bar with progress and add detail to conversation
		m.statusBar.SetMessage(msg.Status)
		if msg.Detail != "" {
			m.addMessage("system", msg.Detail)
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the UI.
func (m *MaestroModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var content string

	switch m.mode {
	case ModeInput:
		content = m.renderInputMode()
	case ModeReview:
		if m.reviewModel != nil {
			content = m.reviewModel.View()
		} else {
			content = "Review mode not initialized"
		}
	case ModeDashboard:
		content = "Dashboard mode (not implemented yet)"
	}

	// Overlay command palette if visible
	if m.commandPalette.IsVisible() {
		content = m.overlayCommandPalette(content, m.commandPalette.View())
	}

	return content
}

// renderInputMode renders the default input mode view.
func (m *MaestroModel) renderInputMode() string {
	// Calculate heights
	statusHeight := 1
	inputHeight := 3
	headerHeight := 3
	conversationHeight := m.height - statusHeight - inputHeight - headerHeight

	// Modern header with gradient-style border
	headerStyle := lipgloss.NewStyle().
		Foreground(m.theme.Accent).
		Bold(true).
		Padding(0, 1)

	repoInfo := ""
	if m.config != nil && m.config.Owner != "" {
		repoInfo = lipgloss.NewStyle().
			Foreground(m.theme.TextMuted).
			Render(fmt.Sprintf("  %s/%s", m.config.Owner, m.config.Repo))
	}

	headerContent := headerStyle.Render("◉ Maestro") + repoInfo

	headerBox := lipgloss.NewStyle().
		Width(m.width).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(m.theme.Border).
		Padding(0, 1).
		Render(headerContent)

	// Conversation viewport with subtle background
	m.viewport.Width = m.width - 4
	m.viewport.Height = max(3, conversationHeight)
	m.renderMessages()

	conversationBox := lipgloss.NewStyle().
		Width(m.width).
		Height(conversationHeight).
		Padding(0, 1).
		Render(m.viewport.View())

	// Input area with modern styling
	inputBox := lipgloss.NewStyle().
		Width(m.width).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(m.theme.Border).
		Padding(0, 0).
		Render(m.inputModel.View())

	// Status bar
	statusView := m.statusBar.View()

	// Combine all sections
	return lipgloss.JoinVertical(lipgloss.Left,
		headerBox,
		conversationBox,
		inputBox,
		statusView,
	)
}

// updateLayout updates component sizes based on terminal dimensions.
func (m *MaestroModel) updateLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	// Update viewport
	statusHeight := 1
	inputHeight := 3
	headerHeight := 1
	conversationHeight := m.height - statusHeight - inputHeight - headerHeight - 1

	m.viewport.Width = m.width - 2
	m.viewport.Height = max(5, conversationHeight)
}

// handleModeTransition handles mode transitions.
func (m *MaestroModel) handleModeTransition(transition ModeTransition) {
	m.mode = transition.To
	m.statusBar.SetMode(transition.To.String())

	switch transition.To {
	case ModeReview:
		if data, ok := transition.Data.(ReviewModeData); ok {
			m.reviewModel = NewReviewModel(data.Comments, m.theme)
			m.reviewModel.SetOnPost(data.OnPost)
			// Initialize the review model with current window size
			m.reviewModel.SetSize(m.width, m.height)
		}

	case ModeInput:
		// Clean up review model when returning to input
		m.reviewModel = nil
	}
}

// handleCommand processes a slash command.
func (m *MaestroModel) handleCommand(cmd string, args []string) tea.Cmd {
	m.addMessage("user", "/"+cmd+" "+strings.Join(args, " "))

	switch cmd {
	case "help":
		return m.cmdHelp()
	case "review":
		if len(args) == 0 {
			return func() tea.Msg {
				return ErrorMsg{Error: fmt.Errorf("usage: /review <PR-number>")}
			}
		}
		return m.cmdReview(args[0])
	case "ask":
		if len(args) == 0 {
			return func() tea.Msg {
				return ErrorMsg{Error: fmt.Errorf("usage: /ask <question>")}
			}
		}
		return m.cmdAsk(strings.Join(args, " "))
	case "exit", "quit":
		return tea.Quit
	case "clear":
		m.messages = []Message{}
		m.addMessage("assistant", "Conversation cleared.")
		return nil
	default:
		return func() tea.Msg {
			return ErrorMsg{Error: fmt.Errorf("unknown command: /%s", cmd)}
		}
	}
}

// handleQuestion processes a natural language question.
func (m *MaestroModel) handleQuestion(question string) tea.Cmd {
	m.addMessage("user", question)
	m.statusBar.SetMessage("Thinking...")

	return func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			return ResponseMsg{Content: "Backend not ready. Please configure the agent."}
		}

		response, err := m.backend.AskQuestion(m.ctx, question)
		if err != nil {
			return ErrorMsg{Error: err}
		}
		return ResponseMsg{Content: response}
	}
}

// Command implementations

func (m *MaestroModel) cmdHelp() tea.Cmd {
	help := `Available commands:
  /help              Show this help message
  /review <PR#>      Review a pull request
  /ask <question>    Ask a question about the repository
  /clear             Clear the conversation
  /exit, /quit       Exit Maestro

Keyboard shortcuts:
  Ctrl+P             Open command palette
  Ctrl+C             Exit
  Up/Down            Scroll conversation
  Enter              Submit input`

	return func() tea.Msg {
		return ResponseMsg{Content: help}
	}
}

func (m *MaestroModel) cmdReview(prArg string) tea.Cmd {
	var prNumber int
	if _, err := fmt.Sscanf(prArg, "%d", &prNumber); err != nil {
		return func() tea.Msg {
			return ErrorMsg{Error: fmt.Errorf("invalid PR number: %s", prArg)}
		}
	}

	m.statusBar.SetMessage(fmt.Sprintf("Reviewing PR #%d...", prNumber))

	// Capture program reference for sending progress updates
	prog := m.program

	return func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			return ErrorMsg{Error: fmt.Errorf("backend not ready")}
		}

		// Create progress callback that sends updates to the TUI
		onProgress := func(status string) {
			if prog != nil {
				prog.Send(ProgressMsg{Status: status})
			}
		}

		comments, err := m.backend.ReviewPR(m.ctx, prNumber, onProgress)
		if err != nil {
			return ErrorMsg{Error: err}
		}

		if len(comments) == 0 {
			return ResponseMsg{Content: fmt.Sprintf("No issues found in PR #%d", prNumber)}
		}

		// Transition to review mode
		return ModeTransition{
			From: ModeInput,
			To:   ModeReview,
			Data: ReviewModeData{
				PRNumber: prNumber,
				Comments: comments,
			},
		}
	}
}

func (m *MaestroModel) cmdAsk(question string) tea.Cmd {
	m.statusBar.SetMessage("Thinking...")

	return func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			return ResponseMsg{Content: "Backend not ready. Please configure the agent."}
		}

		response, err := m.backend.AskQuestion(m.ctx, question)
		if err != nil {
			return ErrorMsg{Error: err}
		}
		return ResponseMsg{Content: response}
	}
}

// registerCommands sets up command palette commands.
func (m *MaestroModel) registerCommands() {
	// Commands are already registered in NewCommandPalette
	// Add handlers here
	m.commandPalette.SetHandler("help", func(args map[string]string) (string, error) {
		return "Use /help in the input for command list", nil
	})
}

// addMessage adds a message to the conversation.
func (m *MaestroModel) addMessage(role, content string) {
	m.messages = append(m.messages, Message{
		Role:    role,
		Content: content,
	})
	m.renderMessages()
	m.viewport.GotoBottom()
}

// renderMessages renders messages to the viewport.
func (m *MaestroModel) renderMessages() {
	var sb strings.Builder

	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			// User messages: cyan accent with "you >" prefix
			prefix := lipgloss.NewStyle().
				Foreground(m.theme.TextMuted).
				Render("you ")
			arrow := lipgloss.NewStyle().
				Foreground(m.theme.Accent).
				Bold(true).
				Render("> ")
			content := lipgloss.NewStyle().
				Foreground(m.theme.TextPrimary).
				Render(msg.Content)
			sb.WriteString(prefix + arrow + content)

		case "assistant":
			// Assistant messages: clean with subtle icon
			icon := lipgloss.NewStyle().
				Foreground(m.theme.Accent).
				Render("◉ ")
			content := lipgloss.NewStyle().
				Foreground(m.theme.TextPrimary).
				Render(msg.Content)
			sb.WriteString(icon + content)

		case "system":
			// System messages: warning style
			icon := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FF6B6B")).
				Bold(true).
				Render("⚠ ")
			content := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FF6B6B")).
				Render(msg.Content)
			sb.WriteString(icon + content)
		}

		sb.WriteString("\n\n")
	}

	m.viewport.SetContent(sb.String())
}

// overlayCommandPalette overlays the command palette on the background.
func (m *MaestroModel) overlayCommandPalette(background, overlay string) string {
	bgLines := strings.Split(background, "\n")
	overlayLines := strings.Split(overlay, "\n")

	// Center vertically
	startY := (len(bgLines) - len(overlayLines)) / 2
	if startY < 0 {
		startY = 0
	}

	// Center horizontally
	overlayWidth := 0
	for _, line := range overlayLines {
		if w := lipgloss.Width(line); w > overlayWidth {
			overlayWidth = w
		}
	}
	startX := (m.width - overlayWidth) / 2
	if startX < 0 {
		startX = 0
	}

	// Overlay
	for i, overlayLine := range overlayLines {
		if startY+i < len(bgLines) {
			bgLine := bgLines[startY+i]
			// Simple replacement - could be improved with proper width handling
			padded := strings.Repeat(" ", startX) + overlayLine
			if len(padded) < len(bgLine) {
				padded += bgLine[len(padded):]
			}
			bgLines[startY+i] = padded
		}
	}

	return strings.Join(bgLines, "\n")
}

// ResponseMsg is sent when an async operation completes successfully.
type ResponseMsg struct {
	Content string
}

// ErrorMsg is sent when an async operation fails.
type ErrorMsg struct {
	Error error
}

// RunMaestro starts the Maestro TUI.
func RunMaestro(cfg *MaestroConfig, backend MaestroBackend) error {
	m := NewMaestroModel(cfg, backend)

	// Open /dev/tty directly to bypass any terminal state issues
	// from previous terminal operations (banner printing, etc.)
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/tty: %w", err)
	}
	defer tty.Close()

	// Force lipgloss to use ANSI256 colors since we're using a custom TTY
	lipgloss.SetColorProfile(termenv.ANSI256)

	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithMouseCellMotion(),
	)

	// Set program reference so model can send async updates
	m.program = p

	// Ensure terminal is restored on panic
	defer func() {
		if r := recover(); r != nil {
			// Restore terminal state by writing escape sequences
			tty.WriteString("\033[?1049l") // Exit alternate screen
			tty.WriteString("\033[?25h")   // Show cursor
			tty.WriteString("\033[0m")     // Reset colors
			panic(r)                       // Re-panic after cleanup
		}
	}()

	_, err = p.Run()
	return err
}
