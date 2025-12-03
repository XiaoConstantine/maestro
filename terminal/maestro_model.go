package terminal

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// MaestroModel is the root TUI model that manages all modes.
type MaestroModel struct {
	// Current mode
	mode MaestroMode

	// Sub-models
	inputModel    *InputModel
	reviewModel   *ReviewModel
	progressModel *ProgressModel

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

	// Inline review results (Crush-style - results shown in conversation)
	reviewResults      []ReviewComment
	selectedReviewIdx  int
	showReviewDetail   bool
	inputFocus         InputFocus
	reviewFileExpanded map[string]bool // Track expanded/collapsed file groups
	selectedFileIdx    int             // Currently selected file group index
	selectedCommentIdx int             // Index within current file group (-1 if file header selected)

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

	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(20)
	vp.KeyMap = viewport.KeyMap{}

	m := &MaestroModel{
		mode:           ModeInput,
		statusBar:      NewStatusBar(theme),
		commandPalette: NewCommandPalette(theme),
		keyHandler:     NewKeyHandler(),
		progressModel:  NewProgressModel(theme),
		theme:          theme,
		styles:         styles,
		messages:       []Message{},
		viewport:       vp,
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
	// In v2, alt screen is set via View() return value
	return tea.Batch(
		func() tea.Msg { return tea.RequestWindowSize() },
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

	case tea.MouseWheelMsg:
		// Handle mouse wheel scrolling in the viewport
		switch msg.Button {
		case tea.MouseWheelUp:
			m.viewport.ScrollUp(3)
		case tea.MouseWheelDown:
			m.viewport.ScrollDown(3)
		}

	case tea.KeyPressMsg:
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
			// Close review detail view
			if m.showReviewDetail {
				m.showReviewDetail = false
				m.renderMessages()
				return m, nil
			}
			// Clear review results
			if len(m.reviewResults) > 0 {
				m.reviewResults = nil
				m.selectedReviewIdx = 0
				m.inputFocus = FocusInput
				m.inputModel.Focus()
				m.renderMessages()
				return m, nil
			}

		case "tab":
			// Cycle focus between review list and input (Crush-style)
			if len(m.reviewResults) > 0 && !m.commandPalette.IsVisible() {
				m.changeFocus()
				m.renderMessages()
				return m, nil
			}
		}

		// Handle review navigation when results are visible AND review pane is focused
		if len(m.reviewResults) > 0 && m.inputFocus == FocusReviewList && !m.commandPalette.IsVisible() {
			switch msg.String() {
			case "j", "down":
				m.moveReviewDown()
				m.renderMessages()
				return m, nil
			case "k", "up":
				m.moveReviewUp()
				m.renderMessages()
				return m, nil
			case " ":
				m.toggleCurrentFileExpand()
				m.renderMessages()
				return m, nil
			case "enter", "l", "right":
				m.showReviewDetail = !m.showReviewDetail
				m.renderMessages()
				return m, nil
			case "h", "left":
				if m.showReviewDetail {
					m.showReviewDetail = false
					m.renderMessages()
				}
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

		// Route to input model
		newInput, cmd := m.inputModel.Update(msg)
		m.inputModel = newInput
		cmds = append(cmds, cmd)

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
		m.progressModel.Hide() // Ensure progress is hidden when response arrives

	case ReviewResultMsg:
		// Store review results for inline display (Crush-style)
		m.reviewResults = msg.Comments
		m.selectedReviewIdx = 0
		m.selectedFileIdx = 0
		m.selectedCommentIdx = 0
		m.showReviewDetail = false
		m.reviewFileExpanded = make(map[string]bool) // Reset expanded state
		m.initReviewFileExpanded()
		// Focus on review list when results arrive
		m.inputFocus = FocusReviewList
		m.inputModel.Blur()
		// Add a summary message
		counts := m.getReviewCounts()
		summary := fmt.Sprintf("Review complete: %d comments", counts["total"])
		if counts["critical"] > 0 {
			summary += fmt.Sprintf(" (%d critical)", counts["critical"])
		}
		m.addMessage("assistant", summary)

	case ErrorMsg:
		m.addMessage("system", fmt.Sprintf("Error: %v", msg.Error))
		m.statusBar.SetMessage("")

	case ProgressMsg:
		// Update progress display with status
		if msg.Status != "" {
			// Start or update progress
			if !m.progressModel.IsVisible() {
				cmd := m.progressModel.Start(msg.Status)
				cmds = append(cmds, cmd)
			} else {
				m.progressModel.SetMessage(msg.Status)
			}
			if msg.Detail != "" {
				m.progressModel.SetDetail(msg.Detail)
			}
		} else {
			// Empty status means hide progress
			m.progressModel.Hide()
		}

	case ProgressTickMsg:
		// Forward to progress model
		newProgress, cmd := m.progressModel.Update(msg)
		m.progressModel = newProgress
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// View renders the UI.
func (m *MaestroModel) View() tea.View {
	var view tea.View
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion // Enable mouse wheel scrolling

	if !m.ready {
		view.SetContent("Initializing...")
		return view
	}

	// Always render input mode - review results are shown inline (Crush-style)
	content := m.renderInputMode()

	// Overlay command palette if visible
	if m.commandPalette.IsVisible() {
		content = m.overlayCommandPalette(content, m.commandPalette.View())
	}

	view.SetContent(content)
	return view
}

// renderInputMode renders the default input mode view with Crush-style layout.
func (m *MaestroModel) renderInputMode() string {
	// Determine if we should show full logo or compact
	showFullLogo := m.height > 25 && m.width > 70

	var logoSection string
	var logoHeight int

	if showFullLogo {
		logoSection = MaestroLogo(m.width, m.theme)
		logoHeight = lipgloss.Height(logoSection)
	} else {
		logoSection = MaestroLogoSmall(m.width, m.theme)
		logoHeight = 1
	}

	// Calculate remaining heights
	statusHeight := 1
	inputHeight := 3
	infoHeight := 4 // For path, model info, etc.
	progressHeight := 0
	if m.progressModel.IsVisible() {
		progressHeight = 2 // Progress section height when visible
	}
	conversationHeight := m.height - statusHeight - inputHeight - logoHeight - infoHeight - progressHeight - 2

	// Info section (path + model info) - like Crush
	infoSection := m.renderInfoSection()

	// Conversation viewport
	m.viewport.SetWidth(m.width - 4)
	m.viewport.SetHeight(max(3, conversationHeight))
	m.renderMessages()

	conversationBox := lipgloss.NewStyle().
		Width(m.width).
		Height(max(3, conversationHeight)).
		Padding(0, 2).
		Render(m.viewport.View())

	// Progress section (between conversation and input)
	m.progressModel.SetWidth(m.width)
	progressSection := m.progressModel.View()

	// Input area with modern styling - like Crush's "> Ready for instructions"
	inputBox := m.inputModel.View()

	// Status bar
	statusView := m.statusBar.View()

	// Combine all sections
	sections := []string{
		logoSection,
		infoSection,
		conversationBox,
	}

	// Add progress section if visible
	if progressSection != "" {
		sections = append(sections, progressSection)
	}

	sections = append(sections, inputBox, statusView)

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderInfoSection renders the info area below the logo (path, model, etc.)
func (m *MaestroModel) renderInfoSection() string {
	pathStyle := lipgloss.NewStyle().
		Foreground(m.theme.TextMuted).
		PaddingLeft(2)

	modelIconStyle := lipgloss.NewStyle().
		Foreground(m.theme.TextMuted)

	modelNameStyle := lipgloss.NewStyle().
		Foreground(m.theme.TextPrimary)

	// Working directory / repo path
	var pathLine string
	if m.config != nil && m.config.Owner != "" {
		pathLine = pathStyle.Render(fmt.Sprintf("~/%s/%s", m.config.Owner, m.config.Repo))
	} else {
		cwd, _ := os.Getwd()
		if cwd != "" {
			// Shorten home directory
			home, _ := os.UserHomeDir()
			if home != "" && strings.HasPrefix(cwd, home) {
				cwd = "~" + cwd[len(home):]
			}
			pathLine = pathStyle.Render(cwd)
		}
	}

	// Model info line
	modelLine := lipgloss.NewStyle().PaddingLeft(2).Render(
		modelIconStyle.Render("◇ ") + modelNameStyle.Render("AI Code Review Agent"),
	)

	return lipgloss.JoinVertical(lipgloss.Left,
		pathLine,
		"",
		modelLine,
		"",
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

	m.viewport.SetWidth(m.width - 2)
	m.viewport.SetHeight(max(5, conversationHeight))
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
	case "claude":
		if len(args) == 0 {
			return func() tea.Msg {
				return ErrorMsg{Error: fmt.Errorf("usage: /claude <prompt>")}
			}
		}
		return m.cmdClaude(strings.Join(args, " "))
	case "gemini":
		if len(args) == 0 {
			return func() tea.Msg {
				return ErrorMsg{Error: fmt.Errorf("usage: /gemini <prompt> or /gemini search <query>")}
			}
		}
		// Check if first arg is a task type
		taskType := ""
		prompt := strings.Join(args, " ")
		if args[0] == "search" && len(args) > 1 {
			taskType = "search"
			prompt = strings.Join(args[1:], " ")
		}
		return m.cmdGemini(prompt, taskType)
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

	// Start progress display
	startCmd := m.progressModel.Start("Thinking...")

	// Capture program reference
	prog := m.program

	askCmd := func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			prog.Send(ProgressMsg{Status: ""}) // Clear progress
			return ResponseMsg{Content: "Backend not ready. Please configure the agent."}
		}

		response, err := m.backend.AskQuestion(m.ctx, question)
		prog.Send(ProgressMsg{Status: ""}) // Clear progress
		if err != nil {
			return ErrorMsg{Error: err}
		}
		return ResponseMsg{Content: response}
	}

	return tea.Batch(startCmd, askCmd)
}

// Command implementations

func (m *MaestroModel) cmdHelp() tea.Cmd {
	help := `Available commands:
  /help              Show this help message
  /review <PR#>      Review a pull request
  /ask <question>    Ask a question about the repository
  /claude <prompt>   Send prompt to Claude CLI subagent
  /gemini <prompt>   Send prompt to Gemini CLI subagent
  /gemini search <q> Search the web with Gemini
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

// ReviewResultMsg contains review results to display inline.
type ReviewResultMsg struct {
	PRNumber int
	Comments []ReviewComment
}

func (m *MaestroModel) cmdReview(prArg string) tea.Cmd {
	var prNumber int
	if _, err := fmt.Sscanf(prArg, "%d", &prNumber); err != nil {
		return func() tea.Msg {
			return ErrorMsg{Error: fmt.Errorf("invalid PR number: %s", prArg)}
		}
	}

	// Clear previous review results when starting a new review
	m.reviewResults = nil
	m.selectedReviewIdx = 0
	m.selectedFileIdx = 0
	m.selectedCommentIdx = 0
	m.reviewFileExpanded = nil
	m.showReviewDetail = false
	m.inputFocus = FocusInput
	m.renderMessages()

	// Start progress display
	startCmd := m.progressModel.Start(fmt.Sprintf("Reviewing PR #%d...", prNumber))

	// Capture program reference for sending progress updates
	prog := m.program

	reviewCmd := func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			prog.Send(ProgressMsg{Status: ""}) // Clear progress
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
			prog.Send(ProgressMsg{Status: ""}) // Clear progress
			return ErrorMsg{Error: err}
		}

		// Clear progress on completion
		prog.Send(ProgressMsg{Status: ""})

		if len(comments) == 0 {
			return ResponseMsg{Content: fmt.Sprintf("✓ No issues found in PR #%d", prNumber)}
		}

		// Return review results to display inline (Crush-style)
		return ReviewResultMsg{
			PRNumber: prNumber,
			Comments: comments,
		}
	}

	return tea.Batch(startCmd, reviewCmd)
}

func (m *MaestroModel) cmdAsk(question string) tea.Cmd {
	// Start progress display
	startCmd := m.progressModel.Start("Thinking...")

	// Capture program reference
	prog := m.program

	askCmd := func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			prog.Send(ProgressMsg{Status: ""}) // Clear progress
			return ResponseMsg{Content: "Backend not ready. Please configure the agent."}
		}

		response, err := m.backend.AskQuestion(m.ctx, question)
		prog.Send(ProgressMsg{Status: ""}) // Clear progress
		if err != nil {
			return ErrorMsg{Error: err}
		}
		return ResponseMsg{Content: response}
	}

	return tea.Batch(startCmd, askCmd)
}

func (m *MaestroModel) cmdClaude(prompt string) tea.Cmd {
	startCmd := m.progressModel.Start("Asking Claude...")
	prog := m.program

	claudeCmd := func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			prog.Send(ProgressMsg{Status: ""})
			return ResponseMsg{Content: "Backend not ready."}
		}

		response, err := m.backend.Claude(m.ctx, prompt)
		prog.Send(ProgressMsg{Status: ""})
		if err != nil {
			return ErrorMsg{Error: err}
		}
		return ResponseMsg{Content: response}
	}

	return tea.Batch(startCmd, claudeCmd)
}

func (m *MaestroModel) cmdGemini(prompt string, taskType string) tea.Cmd {
	msg := "Asking Gemini..."
	if taskType == "search" {
		msg = "Searching with Gemini..."
	}
	startCmd := m.progressModel.Start(msg)
	prog := m.program

	geminiCmd := func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			prog.Send(ProgressMsg{Status: ""})
			return ResponseMsg{Content: "Backend not ready."}
		}

		response, err := m.backend.Gemini(m.ctx, prompt, taskType)
		prog.Send(ProgressMsg{Status: ""})
		if err != nil {
			return ErrorMsg{Error: err}
		}
		return ResponseMsg{Content: response}
	}

	return tea.Batch(startCmd, geminiCmd)
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

	// Add inline review results (Crush-style)
	if len(m.reviewResults) > 0 {
		sb.WriteString(m.renderInlineReview())
	}

	m.viewport.SetContent(sb.String())
}

// renderInlineReview renders review comments inline in the conversation.
func (m *MaestroModel) renderInlineReview() string {
	var sb strings.Builder
	contentWidth := m.width - 6

	// Show focus indicator header when review list is focused
	if m.inputFocus == FocusReviewList {
		focusIndicator := lipgloss.NewStyle().
			Foreground(m.theme.Accent).
			Bold(true).
			Render("▌")
		title := lipgloss.NewStyle().
			Foreground(m.theme.TextPrimary).
			Bold(true).
			Render(" Review Comments")
		sb.WriteString(focusIndicator + title + "\n\n")
	}

	// Group by file
	fileGroups := groupCommentsByFile(m.reviewResults)

	// Render in two columns if showing detail, otherwise full width list
	if m.showReviewDetail && m.getSelectedReviewComment() != nil {
		// Split layout: 40% list, 60% detail
		listWidth := contentWidth * 2 / 5
		detailWidth := contentWidth - listWidth - 3

		listContent := m.renderReviewList(listWidth, fileGroups)
		detailContent := m.renderReviewDetail(detailWidth)

		// Join horizontally
		listLines := strings.Split(listContent, "\n")
		detailLines := strings.Split(detailContent, "\n")

		maxLines := max(len(listLines), len(detailLines))
		separator := lipgloss.NewStyle().Foreground(m.theme.Border).Render("│")

		for i := 0; i < maxLines; i++ {
			listLine := ""
			detailLine := ""
			if i < len(listLines) {
				listLine = listLines[i]
			}
			if i < len(detailLines) {
				detailLine = detailLines[i]
			}

			// Pad list line to width
			listLine = padRight(listLine, listWidth)
			sb.WriteString(listLine + " " + separator + " " + detailLine + "\n")
		}
	} else {
		// Full width list
		sb.WriteString(m.renderReviewList(contentWidth, fileGroups))
	}

	// Navigation hint with focus indicator
	sb.WriteString("\n")
	var hint string
	if m.inputFocus == FocusReviewList {
		hint = lipgloss.NewStyle().Foreground(m.theme.TextMuted).
			Render("j/k navigate • space expand/collapse • enter view details • tab focus input • esc close")
	} else {
		hint = lipgloss.NewStyle().Foreground(m.theme.TextMuted).
			Render("tab focus review list")
	}
	sb.WriteString(hint)

	return sb.String()
}

// renderReviewList renders the list of review comments.
func (m *MaestroModel) renderReviewList(width int, fileGroups []FileGroup) string {
	m.initReviewFileExpanded()
	var lines []string
	currentIdx := 0
	isFocused := m.inputFocus == FocusReviewList

	for fileIdx, group := range fileGroups {
		isExpanded := m.isFileExpanded(group.Path)
		isFileSelected := isFocused && fileIdx == m.selectedFileIdx && m.selectedCommentIdx < 0

		// Expand/collapse arrow (using simpler characters for compatibility)
		var arrow string
		if isExpanded {
			arrow = "[-] "
		} else {
			arrow = "[+] "
		}

		// File header with separator line
		fileName := truncatePath(group.Path, width-15)
		countStr := fmt.Sprintf("(%d)", len(group.Comments))

		var header string
		if isFileSelected {
			header = lipgloss.NewStyle().
				Background(lipgloss.Color("#3A3C55")).
				Foreground(m.theme.TextPrimary).
				Bold(true).
				Render(arrow + fileName + " " + countStr)
		} else {
			header = lipgloss.NewStyle().Foreground(m.theme.TextMuted).
				Render(arrow + fileName + " " + countStr)
		}

		lineWidth := width - lipgloss.Width(header) - 1
		if lineWidth > 0 {
			header = header + " " + lipgloss.NewStyle().Foreground(m.theme.Border).
				Render(strings.Repeat("─", lineWidth))
		}
		lines = append(lines, header)

		if !isExpanded {
			currentIdx++
			lines = append(lines, "")
			continue
		}

		// Comments
		for _, comment := range group.Comments {
			isSelected := isFocused && currentIdx == m.selectedReviewIdx
			line := m.renderCommentLine(comment, isSelected, width-2)
			lines = append(lines, "  "+line)
			currentIdx++
		}
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

// renderCommentLine renders a single comment line.
func (m *MaestroModel) renderCommentLine(comment ReviewComment, selected bool, maxWidth int) string {
	// Severity icon
	icon := m.getSeverityIcon(comment.Severity)

	// Line number
	lineNum := lipgloss.NewStyle().Foreground(m.theme.TextMuted).
		Render(fmt.Sprintf("L%d", comment.LineNumber))

	// Category
	category := ""
	if comment.Category != "" {
		category = lipgloss.NewStyle().
			Foreground(m.theme.TextMuted).
			Background(m.theme.Surface).
			Padding(0, 1).
			Render(comment.Category)
	}

	// Content preview
	previewWidth := maxWidth - 20
	if category != "" {
		previewWidth -= lipgloss.Width(category) + 1
	}
	if previewWidth < 5 {
		previewWidth = 5
	}

	preview := comment.Content
	if len(preview) > previewWidth && previewWidth > 1 {
		preview = preview[:previewWidth-1] + "…"
	}

	// Build line parts
	parts := []string{icon, lineNum}
	if category != "" {
		parts = append(parts, category)
	}

	// Style content based on selection
	if selected {
		// Highlight style for selected comment
		highlightStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#3A3C55")).
			Foreground(m.theme.TextPrimary).
			Bold(true)

		preview = highlightStyle.Render(preview)
		parts = append(parts, preview)
		line := strings.Join(parts, " ")

		// Selection indicator with accent color
		indicator := lipgloss.NewStyle().
			Foreground(m.theme.Accent).
			Bold(true).
			Render("▸ ")
		return indicator + highlightStyle.Render(line)
	}

	preview = lipgloss.NewStyle().Foreground(m.theme.TextSecondary).Render(preview)
	parts = append(parts, preview)
	line := strings.Join(parts, " ")
	return "  " + line
}

// getSelectedReviewComment returns the currently selected review comment.
func (m *MaestroModel) getSelectedReviewComment() *ReviewComment {
	fileGroups := groupCommentsByFile(m.reviewResults)
	if m.selectedFileIdx < 0 || m.selectedFileIdx >= len(fileGroups) {
		return nil
	}
	group := fileGroups[m.selectedFileIdx]
	if m.selectedCommentIdx < 0 || m.selectedCommentIdx >= len(group.Comments) {
		return nil
	}
	return &group.Comments[m.selectedCommentIdx]
}

// renderReviewDetail renders the detail view for the selected comment.
func (m *MaestroModel) renderReviewDetail(width int) string {
	comment := m.getSelectedReviewComment()
	if comment == nil {
		return lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("Select a comment to view details")
	}
	var lines []string

	// Header
	header := fmt.Sprintf("%s:%d", truncatePath(comment.FilePath, width-10), comment.LineNumber)
	lines = append(lines, lipgloss.NewStyle().Foreground(m.theme.TextPrimary).Bold(true).Render(header))
	lines = append(lines, "")

	// Severity
	severityLine := m.getSeverityIcon(comment.Severity) + " " +
		lipgloss.NewStyle().Foreground(m.theme.TextSecondary).Render(comment.Severity)
	if comment.Category != "" {
		severityLine += " • " + lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render(comment.Category)
	}
	lines = append(lines, severityLine)
	lines = append(lines, "")

	// Issue
	lines = append(lines, lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("Issue"))
	issueLines := wrapTextSimple(comment.Content, width)
	for _, l := range issueLines {
		lines = append(lines, lipgloss.NewStyle().Foreground(m.theme.TextSecondary).Render(l))
	}

	// Suggestion
	if comment.Suggestion != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("Suggestion"))
		suggLines := wrapTextSimple(comment.Suggestion, width)
		for _, l := range suggLines {
			lines = append(lines, lipgloss.NewStyle().Foreground(m.theme.StatusHighlight).Render(l))
		}
	}

	return strings.Join(lines, "\n")
}

// getSeverityIcon returns a colored icon for the severity level.
func (m *MaestroModel) getSeverityIcon(severity string) string {
	switch severity {
	case "critical":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render("●")
	case "warning":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD93D")).Render("●")
	case "suggestion":
		return lipgloss.NewStyle().Foreground(m.theme.StatusHighlight).Render("●")
	default:
		return lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("○")
	}
}

// getReviewCounts returns counts of review comments by severity.
func (m *MaestroModel) getReviewCounts() map[string]int {
	counts := map[string]int{"total": 0, "critical": 0, "warning": 0, "suggestion": 0}
	for _, c := range m.reviewResults {
		counts["total"]++
		switch c.Severity {
		case "critical":
			counts["critical"]++
		case "warning":
			counts["warning"]++
		case "suggestion":
			counts["suggestion"]++
		}
	}
	return counts
}

// changeFocus cycles focus between review list and input (Crush-style).
func (m *MaestroModel) changeFocus() {
	switch m.inputFocus {
	case FocusReviewList:
		m.inputFocus = FocusInput
		m.inputModel.Focus()
	case FocusInput:
		m.inputFocus = FocusReviewList
		m.inputModel.Blur()
	}
}

// initReviewFileExpanded initializes the expanded state for all file groups.
func (m *MaestroModel) initReviewFileExpanded() {
	if m.reviewFileExpanded == nil {
		m.reviewFileExpanded = make(map[string]bool)
	}
	fileGroups := groupCommentsByFile(m.reviewResults)
	for _, g := range fileGroups {
		if _, exists := m.reviewFileExpanded[g.Path]; !exists {
			m.reviewFileExpanded[g.Path] = true // Default expanded
		}
	}
}

// isFileExpanded returns whether a file group is expanded.
func (m *MaestroModel) isFileExpanded(path string) bool {
	if m.reviewFileExpanded == nil {
		return true
	}
	expanded, exists := m.reviewFileExpanded[path]
	return !exists || expanded
}

// toggleCurrentFileExpand toggles the expand/collapse state of the current file group.
func (m *MaestroModel) toggleCurrentFileExpand() {
	m.initReviewFileExpanded()
	fileGroups := groupCommentsByFile(m.reviewResults)
	if m.selectedFileIdx >= 0 && m.selectedFileIdx < len(fileGroups) {
		path := fileGroups[m.selectedFileIdx].Path
		m.reviewFileExpanded[path] = !m.isFileExpanded(path)
	}
}

// getTotalVisibleReviewItems returns the total number of visible items in review list.
func (m *MaestroModel) getTotalVisibleReviewItems() int {
	m.initReviewFileExpanded()
	fileGroups := groupCommentsByFile(m.reviewResults)
	total := 0
	for _, g := range fileGroups {
		if m.isFileExpanded(g.Path) {
			total += len(g.Comments)
		} else {
			total++ // Collapsed file header counts as one item
		}
	}
	return total
}

// moveReviewDown moves selection down in the review list.
func (m *MaestroModel) moveReviewDown() {
	total := m.getTotalVisibleReviewItems()
	if m.selectedReviewIdx < total-1 {
		m.selectedReviewIdx++
	}
	m.updateFileIndexFromReviewIdx()
}

// moveReviewUp moves selection up in the review list.
func (m *MaestroModel) moveReviewUp() {
	if m.selectedReviewIdx > 0 {
		m.selectedReviewIdx--
	}
	m.updateFileIndexFromReviewIdx()
}

// updateFileIndexFromReviewIdx updates selectedFileIdx and selectedCommentIdx from selectedReviewIdx.
func (m *MaestroModel) updateFileIndexFromReviewIdx() {
	m.initReviewFileExpanded()
	fileGroups := groupCommentsByFile(m.reviewResults)
	idx := 0
	for i, g := range fileGroups {
		if !m.isFileExpanded(g.Path) {
			if idx == m.selectedReviewIdx {
				m.selectedFileIdx = i
				m.selectedCommentIdx = -1
				return
			}
			idx++
		} else {
			for j := range g.Comments {
				if idx == m.selectedReviewIdx {
					m.selectedFileIdx = i
					m.selectedCommentIdx = j
					return
				}
				idx++
			}
		}
	}
}

func padRight(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func wrapTextSimple(text string, width int) []string {
	if width <= 0 || len(text) <= width {
		return []string{text}
	}

	var lines []string
	words := strings.Fields(text)
	var line strings.Builder

	for _, word := range words {
		if line.Len()+len(word)+1 > width {
			if line.Len() > 0 {
				lines = append(lines, line.String())
				line.Reset()
			}
		}
		if line.Len() > 0 {
			line.WriteString(" ")
		}
		line.WriteString(word)
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
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

	// In v2, color profile is handled automatically by the terminal

	p := tea.NewProgram(
		m,
		tea.WithInput(tty),
		tea.WithOutput(tty),
	)

	// Set program reference so model can send async updates
	m.program = p

	// Ensure terminal is restored on panic
	defer func() {
		if r := recover(); r != nil {
			// Restore terminal state by writing escape sequences
			_, _ = tty.WriteString("\033[?1049l") // Exit alternate screen
			_, _ = tty.WriteString("\033[?25h")   // Show cursor
			_, _ = tty.WriteString("\033[0m")     // Reset colors
			panic(r)                              // Re-panic after cleanup
		}
	}()

	_, err = p.Run()
	return err
}
