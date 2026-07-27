package terminal

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/text/unicode/norm"
)

// MaestroModel is the root TUI model that manages all modes.
type MaestroModel struct {
	// Current mode
	mode MaestroMode

	// Sub-models
	inputModel                *InputModel
	progressModel             *ProgressModel
	toolActivity              *ToolActivityModel
	toolActivityAnchor        int
	toolActivityStartLine     int
	toolActivityRailStartLine int

	// Shared components
	statusBar      *StatusBarModel
	commandPalette *CommandPaletteModel

	// Theming
	theme  *Theme
	styles *Styles

	// Conversation state
	messages                []Message
	viewport                viewport.Model
	railViewport            viewport.Model
	railVisible             bool
	railActive              bool
	railFollowTail          bool
	restoreConversationTail bool

	// Inline review results (Crush-style - results shown in conversation)
	reviewResults []ReviewComment
	reviewModel   *ReviewModel
	inputFocus    InputFocus

	// Picker state
	sessionPickerSessions  []SessionInfo
	sessionPickerIdx       int
	modelPickerModels      []ModelOption
	modelPickerIdx         int
	pickerRequestID        uint64
	pickerLoadID           uint64
	pickerFilter           string
	modelSelectionPending  bool
	sessionMutationPending bool
	specialistRuns         int

	// Dimensions
	width  int
	height int
	ready  bool

	// Backend
	backend MaestroBackend
	config  *MaestroConfig
	ctx     context.Context
	now     func() time.Time

	// Program reference for sending async updates
	program *tea.Program

	codingRunActive bool
	codingCancel    context.CancelFunc
}

// ProgressMsg is sent to update the UI with progress information.
type ProgressMsg struct {
	Status string
	Detail string
}

type CodingResultMsg struct {
	Content string
	Error   error
}

type CodingEventMsg struct {
	Event CodingEvent
}

type ModelPickerMsg struct {
	RequestID uint64
	Models    []ModelOption
	Error     error
}

type ModelSelectedMsg struct {
	RequestID uint64
	ID        string
	Error     error
}

type SpecialistResultMsg struct {
	Content string
	Error   error
}

type ReviewFailedMsg struct{ Error error }

type SessionMutationMsg struct {
	RequestID uint64
	Name      string
	Created   bool
	Error     error
}

// NewMaestroModel creates a new root TUI model.
func NewMaestroModel(cfg *MaestroConfig, backend MaestroBackend) *MaestroModel {
	theme := ResolveTheme(cfg != nil && cfg.HighContrast)
	styles := theme.CreateStyles()

	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(20)
	vp.KeyMap = viewport.KeyMap{}
	rail := viewport.New()
	rail.SetWidth(24)
	rail.SetHeight(20)
	rail.KeyMap = viewport.KeyMap{}

	m := &MaestroModel{
		mode:           ModeInput,
		statusBar:      NewStatusBar(theme),
		commandPalette: NewCommandPalette(theme),
		progressModel:  NewProgressModel(theme),
		toolActivity:   NewToolActivityModel(theme),
		theme:          theme,
		styles:         styles,
		messages:       []Message{},
		viewport:       vp,
		railViewport:   rail,
		railFollowTail: true,
		backend:        backend,
		config:         cfg,
		ctx:            context.Background(),
		now:            time.Now,
	}

	// Create input model and hide optional capabilities the backend does not expose.
	m.inputModel = NewInputModel(theme, m.handleCommand, m.handleQuestion)
	reduceMotion := cfg != nil && cfg.ReduceMotion
	if value := strings.TrimSpace(os.Getenv("MAESTRO_REDUCE_MOTION")); value != "" && value != "0" {
		reduceMotion = true
	}
	m.progressModel.SetReducedMotion(reduceMotion)
	if _, ok := backend.(modelSelectionProvider); !ok {
		m.inputModel.removeCommand("model")
		m.commandPalette.removeCommand("model")
	}

	// Set up status bar. Workspace and model details are rendered in the header.
	m.statusBar.SetMode("")
	m.statusBar.SetMessage("Ready")

	// Add welcome message
	m.addMessage("assistant", "Welcome to Maestro! Enter a coding task, or use /ask for read-only repository questions.")

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

// Update translates Bubble Tea messages into application actions, dispatches
// deterministic state changes, and returns commands that execute described effects.
func (m *MaestroModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	action := actionFromMessage(msg)
	return m, executeEffects(m.Dispatch(action))
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

	// Overlay the active picker without adding it to conversation history.
	if m.commandPalette.IsVisible() {
		content = m.overlayCommandPalette(content, m.commandPalette.View())
	} else if m.mode == ModeSessionPicker {
		content = m.overlayCommandPalette(content, m.renderSessionPicker())
	} else if m.mode == ModeModelPicker {
		content = m.overlayCommandPalette(content, m.renderModelPicker())
	}

	view.SetContent(content)
	return view
}

// renderInputMode renders the default input mode view with Crush-style layout.
func (m *MaestroModel) renderInputMode() string {
	if m.inputModel != nil {
		m.inputModel.SetSize(m.width, composerHeightForPane(m.height, m.inputModel.Value()))
		m.inputModel.SetSuggestionLimit(suggestionLimitForPane(m.height))
	}

	// Determine if we should show full logo or compact
	showFullLogo := m.height > 25 && m.width > 70

	var logoSection string
	if showFullLogo {
		logoSection = MaestroLogo(m.width, m.theme)
	} else {
		logoSection = MaestroLogoSmall(m.width, m.theme)
	}

	// Info section (path + model info) - like Crush
	infoSection := m.renderInfoSection()

	// Progress section (between conversation and input)
	m.progressModel.SetWidth(m.width)
	progressSection := m.progressModel.View()

	// Input area with modern styling - like Crush's "> Ready for instructions"
	inputBox := m.inputModel.View()

	// Status bar
	m.configureStatusBar()
	statusView := m.statusBar.View()

	layout := planInputModeLayout(
		m.height,
		sectionHeight(logoSection),
		sectionHeight(infoSection),
		sectionHeight(progressSection),
		sectionHeight(inputBox),
		sectionHeight(statusView),
	)

	contentPlan := planContentLayout(m.width, layout.conversationHeight, m.railVisible && m.hasRailContent())
	conversationWidth := max(1, contentPlan.conversationWidth-4)
	layoutChanged := m.viewport.Width() != conversationWidth ||
		m.viewport.Height() != layout.conversationHeight ||
		m.railActive != contentPlan.showRail
	followTail := m.restoreConversationTail || m.viewport.AtBottom()
	m.restoreConversationTail = false
	m.viewport.SetWidth(conversationWidth)
	m.viewport.SetHeight(layout.conversationHeight)
	m.renderMessages()
	if contentPlan.showRail {
		m.updateRailViewport(contentPlan.railWidth, layout.conversationHeight)
	}
	railTransition := m.railActive != contentPlan.showRail
	if layoutChanged && m.inputFocus == FocusToolActivity &&
		(!contentPlan.showRail || railTransition || m.railFollowTail) {
		m.ensureToolSelectionVisible()
	} else if layoutChanged && m.inputFocus != FocusToolActivity && followTail {
		m.viewport.GotoBottom()
	}
	m.railActive = contentPlan.showRail

	conversationBox := lipgloss.NewStyle().
		Width(contentPlan.conversationWidth).
		Height(layout.conversationHeight).
		Padding(0, 2).
		Render(m.viewport.View())
	conversationRow := conversationBox
	if contentPlan.showRail {
		conversationRow = lipgloss.JoinHorizontal(
			lipgloss.Top,
			conversationBox,
			m.renderContextRail(contentPlan.railWidth, layout.conversationHeight),
		)
	}

	// Combine all sections
	sections := make([]string, 0, 5)
	if layout.showLogo {
		sections = append(sections, logoSection)
	}
	if layout.showInfo {
		sections = append(sections, infoSection)
	}
	sections = append(sections, conversationRow)

	// Add progress section if visible
	if layout.showProgress && progressSection != "" {
		sections = append(sections, progressSection)
	}

	sections = append(sections, inputBox)
	if layout.showStatus && statusView != "" {
		sections = append(sections, statusView)
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderInfoSection renders the authoritative workspace and active model.
func (m *MaestroModel) renderInfoSection() string {
	workspace := ""
	if provider, ok := m.backend.(workspaceInfoProvider); ok {
		workspace = strings.TrimSpace(provider.GetWorkspace())
	}
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	if home, _ := os.UserHomeDir(); home != "" && strings.HasPrefix(workspace, home) {
		workspace = "~" + workspace[len(home):]
	}

	modelLabel := "Maestro coding agent"
	if provider, ok := m.backend.(modelInfoProvider); ok {
		if info := strings.TrimSpace(provider.GetModelInfo()); info != "" {
			modelLabel = info
		}
	}

	labelStyle := lipgloss.NewStyle().Foreground(m.theme.TextMuted)
	valueStyle := lipgloss.NewStyle().Foreground(m.theme.TextPrimary)
	lineWidth := m.width
	if lineWidth <= 0 {
		lineWidth = 80
	}
	renderLine := func(name, value string, width int) string {
		prefix := "  " + labelStyle.Render(name)
		if lipgloss.Width(prefix) >= width {
			return ansi.Truncate(prefix, width, "")
		}
		remaining := width - lipgloss.Width(prefix)
		return prefix + valueStyle.Render(ansi.Truncate(value, remaining, "…"))
	}

	session := "default"
	if m.backend != nil {
		if current := strings.TrimSpace(m.backend.GetCurrentSession()); current != "" {
			session = current
		}
	}
	if m.width >= 100 {
		columnWidth := max(1, (m.width-8)/3)
		workspaceLine := renderLine("workspace ", workspace, columnWidth)
		modelLine := renderLine("model ", modelLabel, columnWidth)
		sessionLine := renderLine("session ", session, columnWidth)
		return workspaceLine + "    " + modelLine + "    " + sessionLine
	}
	workspaceLine := renderLine("workspace ", workspace, lineWidth)
	modelLine := renderLine("model     ", modelLabel, lineWidth)
	sessionLine := renderLine("session   ", session, lineWidth)
	return lipgloss.JoinVertical(lipgloss.Left, workspaceLine, modelLine, sessionLine)
}

func (m *MaestroModel) hasRailContent() bool {
	return m.toolActivity.HasEntries() || len(m.reviewResults) > 0
}

func (m *MaestroModel) contextRailActive() bool {
	return planContentLayout(m.width, max(1, m.viewport.Height()), m.railVisible && m.hasRailContent()).showRail
}

func (m *MaestroModel) updateRailViewport(width, height int) {
	followTail := m.railFollowTail
	if m.inputFocus != FocusToolActivity {
		followTail = followTail || m.railViewport.AtBottom()
	}
	innerWidth := max(1, width-2)
	sections := make([]string, 0, 4)
	if len(m.reviewResults) > 0 {
		counts := m.getReviewCounts()
		sections = append(sections,
			lipgloss.NewStyle().Foreground(m.theme.Secondary).Bold(true).Render("REVIEW"),
			lipgloss.NewStyle().Foreground(m.theme.TextSecondary).Render(
				fmt.Sprintf("%d findings · %d critical · %d high", counts["total"], counts["critical"], counts["high"]),
			),
		)
	}
	if m.toolActivity.HasEntries() {
		if len(sections) > 0 {
			sections = append(sections, "")
		}
		sections = append(sections, lipgloss.NewStyle().Foreground(m.theme.Accent).Bold(true).Render("ACTIVITY"))
		m.toolActivityRailStartLine = len(sections)
		sections = append(sections, m.toolActivity.View(innerWidth, m.inputFocus == FocusToolActivity))
	}
	m.railViewport.SetWidth(innerWidth)
	m.railViewport.SetHeight(max(1, height))
	m.railViewport.SetContent(strings.Join(sections, "\n"))
	if followTail && m.inputFocus != FocusToolActivity {
		m.railViewport.GotoBottom()
		m.railFollowTail = m.railViewport.AtBottom()
	}
}

func (m *MaestroModel) renderContextRail(width, height int) string {
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(m.theme.Border).
		PaddingLeft(1).
		Render(m.railViewport.View())
}

func (m *MaestroModel) configureStatusBar() {
	switch {
	case m.mode == ModeModelPicker:
		m.statusBar.SetMode("MODELS")
		m.statusBar.SetHints("type filter", "↑↓ navigate", "enter select", "esc close")
	case m.mode == ModeSessionPicker:
		m.statusBar.SetMode("SESSIONS")
		m.statusBar.SetHints("type filter", "↑↓ navigate", "enter switch", "esc close")
	case m.inputFocus == FocusToolActivity && m.toolActivity.HasTools():
		m.statusBar.SetMode("TOOLS")
		if m.codingRunActive {
			m.statusBar.SetHints("↑↓ navigate", "enter expand", "tab next", "esc cancel", "ctrl+\\ rail")
		} else {
			m.statusBar.SetHints("↑↓ navigate", "enter expand", "tab next", "esc input", "ctrl+\\ rail")
		}
	case m.codingRunActive:
		m.statusBar.SetMode("RUNNING")
		m.statusBar.SetHints("esc cancel", "ctrl+p commands", "ctrl+c quit")
	case len(m.reviewResults) > 0 && m.inputFocus == FocusReviewList:
		m.statusBar.SetMode("REVIEW")
		m.statusBar.SetHints("↑↓ navigate", "enter detail", "tab input", "esc close", "ctrl+\\ rail")
	case m.progressModel.IsVisible():
		m.statusBar.SetMode("WORKING")
		m.statusBar.SetHints("ctrl+p commands", "ctrl+c quit")
	default:
		m.statusBar.SetMode("")
		_, canSwitchModel := m.backend.(modelSelectionProvider)
		hints := []string{"ctrl+p commands"}
		if canSwitchModel {
			hints = append(hints, "ctrl+m models")
		}
		if m.hasRailContent() {
			hints = append(hints, "ctrl+\\ rail")
		}
		hints = append(hints, "/help", "ctrl+c quit")
		m.statusBar.SetHints(hints...)
	}
}

type inputModeLayout struct {
	showLogo           bool
	showInfo           bool
	showProgress       bool
	showStatus         bool
	conversationHeight int
}

func planInputModeLayout(totalHeight, logoHeight, infoHeight, progressHeight, inputHeight, statusHeight int) inputModeLayout {
	layout := inputModeLayout{
		showLogo:     logoHeight > 0,
		showInfo:     infoHeight > 0,
		showProgress: progressHeight > 0,
		showStatus:   statusHeight > 0,
	}

	reserved := inputHeight
	if layout.showStatus {
		reserved += statusHeight
	}
	if layout.showProgress {
		reserved += progressHeight
	}
	if layout.showInfo {
		reserved += infoHeight
	}
	if layout.showLogo {
		reserved += logoHeight
	}

	targetConversationHeight := minConversationHeightForPane(totalHeight)
	for totalHeight-reserved < targetConversationHeight {
		switch {
		case layout.showInfo:
			layout.showInfo = false
			reserved -= infoHeight
		case layout.showLogo:
			layout.showLogo = false
			reserved -= logoHeight
		case layout.showProgress:
			layout.showProgress = false
			reserved -= progressHeight
		case layout.showStatus:
			layout.showStatus = false
			reserved -= statusHeight
		default:
			layout.conversationHeight = max(1, totalHeight-reserved)
			return layout
		}
	}

	layout.conversationHeight = max(1, totalHeight-reserved)
	return layout
}

func suggestionLimitForPane(height int) int {
	switch {
	case height < 12:
		return 0
	case height < 16:
		return 2
	case height < 22:
		return 4
	default:
		return 6
	}
}

func inputTextareaHeightForPane(totalHeight int) int {
	switch {
	case totalHeight <= 8:
		return 1
	case totalHeight <= 12:
		return 2
	default:
		return 3
	}
}

func minConversationHeightForPane(totalHeight int) int {
	return max(1, min(3, totalHeight-4))
}

func sectionHeight(section string) int {
	if section == "" {
		return 0
	}
	return lipgloss.Height(section)
}

// handleCommand processes a slash command.
func (m *MaestroModel) handleCommand(cmd string, args []string) tea.Cmd {
	m.addMessage("user", "/"+cmd+" "+strings.Join(args, " "))

	// Handle multi-word commands from command palette (e.g., "session list")
	// by splitting into base command and prepending extra parts to args
	cmdParts := strings.Fields(cmd)
	if len(cmdParts) > 1 {
		cmd = cmdParts[0]
		args = append(cmdParts[1:], args...)
	}

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
		m.toolActivity = NewToolActivityModel(m.theme)
		m.toolActivityAnchor = 0
		m.railVisible = false
		m.setInputFocus(FocusInput)
		m.addMessage("assistant", "Conversation cleared.")
		return nil
	case "model":
		return m.cmdModelList()
	case "session":
		if len(args) == 0 {
			return func() tea.Msg {
				return ErrorMsg{Error: fmt.Errorf("usage: /session <new|switch|list> [name]")}
			}
		}
		switch args[0] {
		case "new":
			requestID, err := m.reserveSessionMutation()
			if err != nil {
				return func() tea.Msg { return ErrorMsg{Error: err} }
			}
			name := ""
			if len(args) >= 2 {
				name = args[1]
			}
			return m.cmdSessionNew(name, requestID)
		case "switch":
			if len(args) < 2 {
				return func() tea.Msg {
					return ErrorMsg{Error: fmt.Errorf("usage: /session switch <name>")}
				}
			}
			requestID, err := m.reserveSessionMutation()
			if err != nil {
				return func() tea.Msg { return ErrorMsg{Error: err} }
			}
			return m.cmdSessionSwitch(args[1], requestID)
		case "list":
			return m.cmdSessionList()
		default:
			return func() tea.Msg {
				return ErrorMsg{Error: fmt.Errorf("unknown session subcommand: %s", args[0])}
			}
		}
	default:
		return func() tea.Msg {
			return ErrorMsg{Error: fmt.Errorf("unknown command: /%s", cmd)}
		}
	}
}

// handleQuestion processes natural language as a workspace coding task.
func (m *MaestroModel) handleQuestion(question string) (tea.Cmd, bool) {
	if m.modelSelectionPending || m.sessionMutationPending {
		return func() tea.Msg {
			return ErrorMsg{Error: fmt.Errorf("wait for the pending model or session change before starting a run")}
		}, false
	}
	if m.codingRunActive {
		return func() tea.Msg {
			return ErrorMsg{Error: fmt.Errorf("a coding run is already active; press Esc to cancel it")}
		}, false
	}
	m.addMessage("user", question)
	m.codingRunActive = true
	m.statusBar.SetMessage("Coding in the active workspace")
	runCtx, cancel := context.WithCancel(m.ctx)
	m.codingCancel = cancel

	startCmd := m.progressModel.Start("Starting coding agent...")
	prog := m.program

	taskCmd := func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			return CodingResultMsg{Error: fmt.Errorf("backend not ready; configure the agent")}
		}
		response, err := m.backend.RunCodingTask(runCtx, question, func(event CodingEvent) {
			if prog != nil {
				prog.Send(CodingEventMsg{Event: event})
			}
		})
		return CodingResultMsg{Content: response, Error: err}
	}

	return tea.Batch(startCmd, taskCmd), true
}

// Command implementations

func (m *MaestroModel) cmdHelp() tea.Cmd {
	return func() tea.Msg {
		currentSession := "unknown"
		modelCommand := ""
		modelShortcut := ""
		if _, ok := m.backend.(modelSelectionProvider); ok {
			modelCommand = "  /model                 Choose the model for the next coding run\n"
			modelShortcut = "  Ctrl+M                 Open model picker\n"
		}
		if m.backend != nil {
			currentSession = m.backend.GetCurrentSession()
		}

		help := fmt.Sprintf(`Available commands:
  /help                  Show this help message
  /review <PR#>          Review a pull request
  /ask <question>        Ask a question about the repository
  /claude <prompt>       Send prompt to Claude CLI subagent
  /gemini <prompt>       Send prompt to Gemini CLI subagent
  /gemini search <q>     Search the web with Gemini
%s  /session new [name]    Create a new session (auto-generates name if omitted)
  /session switch <name> Switch to an existing session
  /session list          List all available sessions
  /clear                 Clear the conversation
  /exit, /quit           Exit Maestro

Natural-language input runs the coding agent with ls/read/write/edit (bash is opt-in).
Current session: %s

Keyboard shortcuts:
  Ctrl+P                 Open command palette
%s  Enter                  Run the current prompt
  Ctrl+J                 Insert a newline in the prompt
  Up/Down                Navigate suggestions or prompt history
  Tab                    Complete commands or cycle available transcript regions
  Esc                    Cancel an active coding run or close the current overlay
  Ctrl+C                 Exit`, modelCommand, currentSession, modelShortcut)

		return ResponseMsg{Content: help}
	}
}

// ReviewResultMsg contains review results to display inline.
type ReviewResultMsg struct {
	PRNumber int
	Comments []ReviewComment
}

func (m *MaestroModel) cmdReview(prArg string) tea.Cmd {
	if m.sessionMutationPending || m.modelSelectionPending {
		return func() tea.Msg { return ErrorMsg{Error: fmt.Errorf("wait for the pending model or session change")} }
	}
	var prNumber int
	if _, err := fmt.Sscanf(prArg, "%d", &prNumber); err != nil {
		return func() tea.Msg {
			return ErrorMsg{Error: fmt.Errorf("invalid PR number: %s", prArg)}
		}
	}

	m.specialistRuns++
	// Clear previous review results when starting a new review
	m.reviewResults = nil
	m.reviewModel = nil
	m.setInputFocus(FocusInput)
	m.renderMessages()

	// Start progress display
	startCmd := m.progressModel.Start(fmt.Sprintf("Reviewing PR #%d...", prNumber))

	// Capture program reference for sending progress updates
	prog := m.program

	reviewCmd := func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			if prog != nil {
				prog.Send(ProgressMsg{Status: ""}) // Clear progress
			}
			return ReviewFailedMsg{Error: fmt.Errorf("backend not ready")}
		}

		// Create progress callback that sends updates to the TUI
		onProgress := func(status string) {
			if prog != nil {
				prog.Send(ProgressMsg{Status: status})
			}
		}

		comments, err := m.backend.ReviewPR(m.ctx, prNumber, onProgress)
		if err != nil {
			if prog != nil {
				prog.Send(ProgressMsg{Status: ""}) // Clear progress
			}
			return ReviewFailedMsg{Error: err}
		}

		// Clear progress on completion
		if prog != nil {
			prog.Send(ProgressMsg{Status: ""})
		}

		if len(comments) == 0 {
			return SpecialistResultMsg{Content: fmt.Sprintf("✓ No issues found in PR #%d", prNumber)}
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
	if m.sessionMutationPending || m.modelSelectionPending {
		return func() tea.Msg { return ErrorMsg{Error: fmt.Errorf("wait for the pending model or session change")} }
	}
	m.specialistRuns++
	// Start progress display
	startCmd := m.progressModel.Start("Thinking...")

	// Capture program reference
	prog := m.program

	askCmd := func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			if prog != nil {
				prog.Send(ProgressMsg{Status: ""})
			}
			return SpecialistResultMsg{Content: "Backend not ready. Please configure the agent."}
		}

		response, err := m.backend.AskQuestion(m.ctx, question)
		if prog != nil {
			prog.Send(ProgressMsg{Status: ""})
		}
		return SpecialistResultMsg{Content: response, Error: err}
	}

	return tea.Batch(startCmd, askCmd)
}

func (m *MaestroModel) cmdClaude(prompt string) tea.Cmd {
	if m.sessionMutationPending || m.modelSelectionPending {
		return func() tea.Msg { return ErrorMsg{Error: fmt.Errorf("wait for the pending model or session change")} }
	}
	m.specialistRuns++
	startCmd := m.progressModel.Start("Asking Claude...")
	prog := m.program

	claudeCmd := func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			if prog != nil {
				prog.Send(ProgressMsg{Status: ""})
			}
			return SpecialistResultMsg{Content: "Backend not ready."}
		}

		response, err := m.backend.Claude(m.ctx, prompt)
		if prog != nil {
			prog.Send(ProgressMsg{Status: ""})
		}
		return SpecialistResultMsg{Content: response, Error: err}
	}

	return tea.Batch(startCmd, claudeCmd)
}

func (m *MaestroModel) cmdGemini(prompt string, taskType string) tea.Cmd {
	if m.sessionMutationPending || m.modelSelectionPending {
		return func() tea.Msg { return ErrorMsg{Error: fmt.Errorf("wait for the pending model or session change")} }
	}
	m.specialistRuns++
	msg := "Asking Gemini..."
	if taskType == "search" {
		msg = "Searching with Gemini..."
	}
	startCmd := m.progressModel.Start(msg)
	prog := m.program

	geminiCmd := func() tea.Msg {
		if m.backend == nil || !m.backend.IsReady() {
			if prog != nil {
				prog.Send(ProgressMsg{Status: ""})
			}
			return SpecialistResultMsg{Content: "Backend not ready."}
		}

		response, err := m.backend.Gemini(m.ctx, prompt, taskType)
		if prog != nil {
			prog.Send(ProgressMsg{Status: ""})
		}
		return SpecialistResultMsg{Content: response, Error: err}
	}

	return tea.Batch(startCmd, geminiCmd)
}

func (m *MaestroModel) cmdSessionNew(name string, requestID uint64) tea.Cmd {
	startCmd := m.progressModel.Start("Creating session...")

	createCmd := func() tea.Msg {
		if m.backend == nil {
			return SessionMutationMsg{RequestID: requestID, Error: fmt.Errorf("backend not ready")}
		}

		err := m.backend.CreateSession(m.ctx, name)
		if err != nil {
			return SessionMutationMsg{RequestID: requestID, Error: err}
		}
		return SessionMutationMsg{RequestID: requestID, Name: m.backend.GetCurrentSession(), Created: true}
	}

	return tea.Batch(startCmd, createCmd)
}

func (m *MaestroModel) cmdSessionSwitch(name string, requestID uint64) tea.Cmd {
	startCmd := m.progressModel.Start("Switching session...")

	switchCmd := func() tea.Msg {
		if m.backend == nil {
			return SessionMutationMsg{RequestID: requestID, Name: name, Error: fmt.Errorf("backend not ready")}
		}

		err := m.backend.SwitchSession(m.ctx, name)
		return SessionMutationMsg{RequestID: requestID, Name: name, Error: err}
	}

	return tea.Batch(startCmd, switchCmd)
}

// SessionPickerMsg is sent when sessions are loaded for the picker.
type SessionPickerMsg struct {
	RequestID uint64
	Sessions  []SessionInfo
	Error     error
}

func (m *MaestroModel) cmdSessionList() tea.Cmd {
	m.pickerLoadID++
	requestID := m.pickerLoadID

	return func() tea.Msg {
		if m.backend == nil {
			return SessionPickerMsg{RequestID: requestID, Error: fmt.Errorf("backend not ready")}
		}

		sessions, err := m.backend.ListSessions(m.ctx)
		return SessionPickerMsg{RequestID: requestID, Sessions: sessions, Error: err}
	}
}

func (m *MaestroModel) cmdModelList() tea.Cmd {
	m.pickerLoadID++
	requestID := m.pickerLoadID
	return func() tea.Msg {
		provider, ok := m.backend.(modelSelectionProvider)
		if !ok {
			return ModelPickerMsg{RequestID: requestID, Error: fmt.Errorf("backend does not support model switching")}
		}
		models, err := provider.ListModels(m.ctx)
		return ModelPickerMsg{RequestID: requestID, Models: models, Error: err}
	}
}

func (m *MaestroModel) cmdModelSelect(id string, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		provider, ok := m.backend.(modelSelectionProvider)
		if !ok {
			return ModelSelectedMsg{RequestID: requestID, ID: id, Error: fmt.Errorf("backend does not support model switching")}
		}
		err := provider.SetModel(m.ctx, id)
		return ModelSelectedMsg{RequestID: requestID, ID: id, Error: err}
	}
}

func (m *MaestroModel) nextPickerRequestID() uint64 {
	m.pickerRequestID++
	return m.pickerRequestID
}

func (m *MaestroModel) reserveSessionMutation() (uint64, error) {
	if m.codingRunActive || m.sessionMutationPending || m.modelSelectionPending || m.specialistRuns > 0 {
		return 0, fmt.Errorf("session changes require an idle coding session")
	}
	m.sessionMutationPending = true
	return m.nextPickerRequestID(), nil
}

func (m *MaestroModel) resetSessionUI() {
	m.messages = nil
	m.toolActivity = NewToolActivityModel(m.theme)
	m.toolActivityAnchor = 0
	m.reviewResults = nil
	m.reviewModel = nil
	m.railVisible = false
	m.setInputFocus(FocusInput)
}

// addMessage adds a message to the conversation.
func (m *MaestroModel) addMessage(role, content string) {
	m.messages = append(m.messages, Message{
		Role:      role,
		Content:   content,
		Timestamp: m.now(),
	})
	m.renderMessages()
	m.viewport.GotoBottom()
}

// renderMessages renders messages to the viewport.
func (m *MaestroModel) renderMessages() {
	var sb strings.Builder

	for i := range m.messages {
		msg := &m.messages[i]
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
			icon := lipgloss.NewStyle().
				Foreground(m.theme.Accent).
				Render("◉ ")
			contentWidth := max(1, m.viewport.Width()-lipgloss.Width(icon))
			content := m.renderAssistantMessage(msg, contentWidth)
			sb.WriteString(icon + content)

		case "system":
			// System messages: warning style
			icon := lipgloss.NewStyle().
				Foreground(m.theme.LogoPrimary).
				Bold(true).
				Render("⚠ ")
			content := lipgloss.NewStyle().
				Foreground(m.theme.LogoPrimary).
				Render(msg.Content)
			sb.WriteString(icon + content)
		}

		sb.WriteString("\n\n")
		if m.toolActivity.HasEntries() && !m.contextRailActive() && m.toolActivityAnchor == i+1 {
			m.toolActivityStartLine = lipgloss.Height(sb.String()) - 1
			sb.WriteString(m.toolActivity.View(
				max(1, m.viewport.Width()),
				m.inputFocus == FocusToolActivity,
			))
			sb.WriteString("\n\n")
		}
	}

	if m.toolActivity.HasEntries() && !m.contextRailActive() && (m.toolActivityAnchor <= 0 || m.toolActivityAnchor > len(m.messages)) {
		m.toolActivityStartLine = lipgloss.Height(sb.String()) - 1
		sb.WriteString(m.toolActivity.View(
			max(1, m.viewport.Width()),
			m.inputFocus == FocusToolActivity,
		))
		sb.WriteString("\n\n")
	}

	if m.reviewModel != nil {
		m.reviewModel.SetSize(max(1, m.viewport.Width()), max(6, m.viewport.Height()))
		sb.WriteString(m.reviewModel.ViewString())
	}

	m.viewport.SetContent(sb.String())
}

func (m *MaestroModel) updatePickerFilter(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "backspace", "ctrl+h":
		runes := []rune(m.pickerFilter)
		if len(runes) > 0 {
			m.pickerFilter = string(runes[:len(runes)-1])
		}
		return true
	case "ctrl+u":
		m.pickerFilter = ""
		return true
	}
	key := tea.Key(msg)
	if key.Text == "" || key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta|tea.ModSuper|tea.ModHyper) != 0 {
		return false
	}
	m.pickerFilter += key.Text
	return true
}

func pickerFuzzyMatch(query, text string) bool {
	query = strings.ToLower(norm.NFC.String(strings.TrimSpace(query)))
	if query == "" {
		return true
	}
	textRunes := []rune(strings.ToLower(norm.NFC.String(text)))
	position := 0
	for _, wanted := range query {
		found := false
		for position < len(textRunes) {
			if textRunes[position] == wanted {
				found = true
				position++
				break
			}
			position++
		}
		if !found {
			return false
		}
	}
	return true
}

func (m *MaestroModel) filteredSessions() []SessionInfo {
	filtered := make([]SessionInfo, 0, len(m.sessionPickerSessions))
	for _, session := range m.sessionPickerSessions {
		if pickerFuzzyMatch(m.pickerFilter, session.Name+" "+session.CreatedAt) {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

func (m *MaestroModel) filteredModels() []ModelOption {
	filtered := make([]ModelOption, 0, len(m.modelPickerModels))
	for _, option := range m.modelPickerModels {
		if pickerFuzzyMatch(m.pickerFilter, option.ID+" "+option.Description) {
			filtered = append(filtered, option)
		}
	}
	return filtered
}

func (m *MaestroModel) sessionPickerCapacity() int {
	if m.height <= 0 {
		return len(m.sessionPickerSessions)
	}
	return max(0, m.height-7)
}

func (m *MaestroModel) modelPickerCapacity() int {
	if m.height <= 0 {
		return len(m.modelPickerModels)
	}
	return max(0, (m.height-7)/2)
}

// renderSessionPicker renders the interactive session selector.
func (m *MaestroModel) renderSessionPicker() string {
	var sb strings.Builder

	// Header
	header := lipgloss.NewStyle().
		Foreground(m.theme.Accent).
		Bold(true).
		Render("Select a session")
	hint := lipgloss.NewStyle().
		Foreground(m.theme.TextMuted).
		Render(" (type to filter, ↑/↓ navigate, enter select, esc cancel)")
	sb.WriteString(header + hint + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("Filter: ") + m.pickerFilter + "\n")

	// Session list. Four rows are reserved for panel border/padding and three
	// for title, filter, and an overflow/empty-state footer.
	filtered := m.filteredSessions()
	start, end := pickerWindow(len(filtered), m.sessionPickerIdx, m.sessionPickerCapacity())
	for i := start; i < end; i++ {
		session := filtered[i]
		isSelected := i == m.sessionPickerIdx

		// Selection indicator
		indicator := "  "
		if isSelected {
			indicator = lipgloss.NewStyle().
				Foreground(m.theme.Accent).
				Bold(true).
				Render("> ")
		}

		// Current session marker
		currentMarker := ""
		if session.IsCurrent {
			currentMarker = lipgloss.NewStyle().
				Foreground(m.theme.StatusHighlight).
				Render(" (current)")
		}

		// Session name
		nameStyle := lipgloss.NewStyle().Foreground(m.theme.TextSecondary)
		if isSelected {
			nameStyle = lipgloss.NewStyle().
				Foreground(m.theme.TextPrimary).
				Bold(true)
		}
		name := nameStyle.Render(session.Name)

		// Created date
		dateStyle := lipgloss.NewStyle().Foreground(m.theme.TextMuted)
		date := dateStyle.Render(fmt.Sprintf(" (%s)", session.CreatedAt))

		sb.WriteString(indicator + name + currentMarker + date + "\n")
	}
	if len(filtered) == 0 {
		sb.WriteString(lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("No matching sessions"))
	} else if end == 0 {
		sb.WriteString(lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("Increase terminal height"))
	} else if start > 0 || end < len(filtered) {
		sb.WriteString(lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render(
			fmt.Sprintf("%d–%d of %d", start+1, end, len(filtered)),
		))
	}

	return m.renderPickerPanel(sb.String())
}

func (m *MaestroModel) renderModelPicker() string {
	var lines []string
	title := lipgloss.NewStyle().Foreground(m.theme.Accent).Bold(true).Render("Select model")
	hint := lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render(" (type to filter, ↑/↓ navigate, enter select, esc cancel)")
	lines = append(lines, title+hint, lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("Filter: ")+m.pickerFilter)
	filtered := m.filteredModels()
	start, end := pickerWindow(len(filtered), m.modelPickerIdx, m.modelPickerCapacity())
	for i := start; i < end; i++ {
		option := filtered[i]
		prefix := "  "
		if i == m.modelPickerIdx {
			prefix = lipgloss.NewStyle().Foreground(m.theme.Accent).Bold(true).Render("> ")
		}
		label := option.ID
		if option.Current {
			label += " (current)"
		}
		lines = append(lines, prefix+label)
		if option.Description != "" {
			lines = append(lines, "    "+lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render(option.Description))
		}
	}
	if len(filtered) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("No matching models"))
	} else if end == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render("Increase terminal height"))
	} else if start > 0 || end < len(filtered) {
		lines = append(lines, lipgloss.NewStyle().Foreground(m.theme.TextMuted).Render(
			fmt.Sprintf("%d–%d of %d", start+1, end, len(filtered)),
		))
	}
	return m.renderPickerPanel(strings.Join(lines, "\n"))
}

func pickerWindow(total, selected, limit int) (int, int) {
	if total <= 0 || limit <= 0 {
		return 0, 0
	}
	limit = min(limit, total)
	start := max(0, selected-limit+1)
	return start, min(total, start+limit)
}

func (m *MaestroModel) renderPickerPanel(content string) string {
	width := max(1, min(72, m.width-4))
	lines := strings.Split(content, "\n")
	for i := range lines {
		// Width includes the two-cell padding and border frame in Lip Gloss v2.
		lines[i] = ansi.Truncate(lines[i], max(1, width-6), "…")
	}
	return lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Border).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
}

// getReviewCounts returns counts of review comments by severity.
func (m *MaestroModel) getReviewCounts() map[string]int {
	counts := map[string]int{"total": 0, "critical": 0, "high": 0, "medium": 0, "low": 0}
	for _, c := range m.reviewResults {
		counts["total"]++
		switch normalizedReviewSeverity(c.Severity) {
		case "critical":
			counts["critical"]++
		case "high":
			counts["high"]++
		case "medium":
			counts["medium"]++
		case "low":
			counts["low"]++
		}
	}
	return counts
}

// changeFocus cycles through the composer and available transcript regions.
func (m *MaestroModel) changeFocus() {
	order := []InputFocus{FocusInput}
	if m.toolActivity.HasTools() {
		order = append(order, FocusToolActivity)
	}
	if len(m.reviewResults) > 0 {
		order = append(order, FocusReviewList)
	}
	current := 0
	for i, focus := range order {
		if focus == m.inputFocus {
			current = i
			break
		}
	}
	m.setInputFocus(order[(current+1)%len(order)])
}

func (m *MaestroModel) setInputFocus(focus InputFocus) {
	m.inputFocus = focus
	m.toolActivity.SetFocused(focus == FocusToolActivity)
	if m.reviewModel != nil {
		m.reviewModel.SetFocused(focus == FocusReviewList)
	}
	if focus == FocusToolActivity {
		m.railFollowTail = false
		m.ensureToolSelectionVisible()
	}
	if focus == FocusInput {
		m.inputModel.Focus()
	} else {
		m.inputModel.Blur()
	}
}

func (m *MaestroModel) ensureToolSelectionVisible() {
	if m.contextRailActive() {
		line := m.toolActivityRailStartLine + m.toolActivity.SelectedLine(max(1, m.railViewport.Width()))
		m.railViewport.EnsureVisible(line, 0, 0)
		return
	}
	line := m.toolActivityStartLine + m.toolActivity.SelectedLine(max(1, m.viewport.Width()))
	m.viewport.EnsureVisible(line, 0, 0)
}

// overlayCommandPalette composes the palette without slicing ANSI escape sequences.
func (m *MaestroModel) overlayCommandPalette(background, overlay string) string {
	backgroundHeight := sectionHeight(background)
	overlayHeight := sectionHeight(overlay)
	overlayWidth := lipgloss.Width(overlay)
	startX := max(0, (m.width-overlayWidth)/2)
	startY := max(0, (backgroundHeight-overlayHeight)/2)

	canvas := lipgloss.NewCanvas(max(1, m.width), max(1, backgroundHeight))
	layers := lipgloss.NewCompositor(
		lipgloss.NewLayer(background),
		lipgloss.NewLayer(overlay).X(startX).Y(startY).Z(1),
	)
	canvas.Compose(layers)
	return canvas.Render()
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
