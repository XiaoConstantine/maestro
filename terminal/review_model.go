package terminal

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ReviewComment represents a single review comment for the TUI.
type ReviewComment struct {
	FilePath   string
	LineNumber int
	Content    string
	Severity   string
	Suggestion string
	Category   string
	CodeBlock  string
	DiffBlock  string
}

// FileGroup groups comments by file path.
type FileGroup struct {
	Path      string
	Comments  []ReviewComment
	Expanded  bool
	StartIdx  int
}

// FilterMode represents the current filter state.
type FilterMode int

const (
	FilterNone FilterMode = iota
	FilterCritical
	FilterWarning
	FilterSuggestion
	FilterByFile
	FilterByCategory
)

// ReviewPane represents which pane is focused.
type ReviewPane int

const (
	PaneFileList ReviewPane = iota
	PaneCommentList
	PaneCommentDetail
)

// ReviewModel is the main TUI model for displaying review comments.
type ReviewModel struct {
	comments       []ReviewComment
	fileGroups     []FileGroup
	filteredGroups []FileGroup

	selectedFileIdx    int
	selectedCommentIdx int
	expandedCommentIdx int

	focusedPane ReviewPane
	filterMode  FilterMode

	fileListViewport    viewport.Model
	commentListViewport viewport.Model
	detailViewport      viewport.Model

	width  int
	height int
	ready  bool

	theme      *Theme
	styles     *ReviewStyles
	compStyles *ComponentStyles

	showHelp    bool
	showMetrics bool
	confirmPost bool

	onPost func([]ReviewComment) error
	onQuit func()
}

// ReviewStyles holds styles specific to review display.
type ReviewStyles struct {
	FileHeader       lipgloss.Style
	FileHeaderActive lipgloss.Style
	CommentItem      lipgloss.Style
	CommentActive    lipgloss.Style
	CommentExpanded  lipgloss.Style

	SeverityCritical lipgloss.Style
	SeverityWarning  lipgloss.Style
	SeveritySuggestion lipgloss.Style

	CodeBlock    lipgloss.Style
	DiffAdded    lipgloss.Style
	DiffRemoved  lipgloss.Style
	DiffContext  lipgloss.Style

	CategoryBadge lipgloss.Style
	LineNumber    lipgloss.Style
	Suggestion    lipgloss.Style

	FilterBar     lipgloss.Style
	FilterActive  lipgloss.Style
	SearchInput   lipgloss.Style

	HelpKey   lipgloss.Style
	HelpDesc  lipgloss.Style
	HelpPanel lipgloss.Style

	PaneBorder       lipgloss.Style
	PaneBorderActive lipgloss.Style
}

// NewReviewModel creates a new review TUI model.
func NewReviewModel(comments []ReviewComment, theme *Theme) *ReviewModel {
	styles := createReviewStyles(theme)
	compStyles := theme.CreateComponentStyles()

	m := &ReviewModel{
		comments:           comments,
		fileGroups:         groupCommentsByFile(comments),
		selectedFileIdx:    0,
		selectedCommentIdx: 0,
		expandedCommentIdx: -1,
		focusedPane:        PaneCommentList,
		filterMode:         FilterNone,
		theme:              theme,
		styles:             styles,
		compStyles:         compStyles,
		showHelp:           false,
		showMetrics:        false,
	}

	m.filteredGroups = m.fileGroups
	m.fileListViewport = viewport.New(30, 20)
	m.commentListViewport = viewport.New(50, 20)
	m.detailViewport = viewport.New(50, 20)

	return m
}

func createReviewStyles(theme *Theme) *ReviewStyles {
	return &ReviewStyles{
		FileHeader: lipgloss.NewStyle().
			Foreground(theme.Accent).
			Bold(true).
			PaddingLeft(1),

		FileHeaderActive: lipgloss.NewStyle().
			Foreground(theme.TextPrimary).
			Background(lipgloss.Color("#3A3C55")).
			Bold(true).
			PaddingLeft(1),

		CommentItem: lipgloss.NewStyle().
			Foreground(theme.TextSecondary).
			PaddingLeft(2),

		CommentActive: lipgloss.NewStyle().
			Foreground(theme.TextPrimary).
			Background(lipgloss.Color("#3A3C55")).
			PaddingLeft(2),

		CommentExpanded: lipgloss.NewStyle().
			Foreground(theme.TextPrimary).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.Accent).
			Padding(1),

		SeverityCritical: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B")).
			Bold(true),

		SeverityWarning: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFE066")).
			Bold(true),

		SeveritySuggestion: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4ECDC4")).
			Bold(true),

		CodeBlock: lipgloss.NewStyle().
			Foreground(theme.Code).
			Background(theme.Surface).
			Padding(0, 1),

		DiffAdded: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7FE9DE")),

		DiffRemoved: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B")),

		DiffContext: lipgloss.NewStyle().
			Foreground(theme.TextMuted),

		CategoryBadge: lipgloss.NewStyle().
			Foreground(theme.TextPrimary).
			Background(theme.Border).
			Padding(0, 1),

		LineNumber: lipgloss.NewStyle().
			Foreground(theme.TextMuted),

		Suggestion: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7FE9DE")).
			Italic(true).
			PaddingLeft(2),

		FilterBar: lipgloss.NewStyle().
			Foreground(theme.TextSecondary).
			Background(theme.Surface).
			Padding(0, 1),

		FilterActive: lipgloss.NewStyle().
			Foreground(theme.Accent).
			Background(theme.Surface).
			Bold(true).
			Padding(0, 1),

		SearchInput: lipgloss.NewStyle().
			Foreground(theme.TextPrimary).
			Background(theme.Surface).
			Padding(0, 1),

		HelpKey: lipgloss.NewStyle().
			Foreground(theme.Accent).
			Bold(true),

		HelpDesc: lipgloss.NewStyle().
			Foreground(theme.TextSecondary),

		HelpPanel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.Border).
			Padding(1, 2),

		PaneBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.Border),

		PaneBorderActive: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.Accent),
	}
}

func groupCommentsByFile(comments []ReviewComment) []FileGroup {
	fileMap := make(map[string]*FileGroup)
	var order []string

	for _, c := range comments {
		if fg, exists := fileMap[c.FilePath]; exists {
			fg.Comments = append(fg.Comments, c)
		} else {
			order = append(order, c.FilePath)
			fileMap[c.FilePath] = &FileGroup{
				Path:     c.FilePath,
				Comments: []ReviewComment{c},
				Expanded: true,
			}
		}
	}

	groups := make([]FileGroup, 0, len(order))
	idx := 0
	for _, path := range order {
		fg := fileMap[path]
		fg.StartIdx = idx
		idx += len(fg.Comments)
		groups = append(groups, *fg)
	}

	return groups
}

// Init initializes the review model.
func (m *ReviewModel) Init() tea.Cmd {
	return tea.EnterAltScreen
}

// Update handles messages for the review model.
func (m *ReviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.updateViewportSizes()

	case tea.KeyMsg:
		if m.showHelp {
			if msg.String() == "?" || msg.String() == "esc" || msg.String() == "q" {
				m.showHelp = false
			}
			return m, nil
		}

		if m.confirmPost {
			return m.handleConfirmPost(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			if m.onQuit != nil {
				m.onQuit()
			}
			return m, tea.Quit

		case "?":
			m.showHelp = !m.showHelp

		case "m":
			m.showMetrics = !m.showMetrics

		case "j", "down":
			m.moveDown()

		case "k", "up":
			m.moveUp()

		case "enter", "l", "right":
			m.expandSelected()

		case "h", "left", "esc":
			if m.expandedCommentIdx >= 0 {
				m.expandedCommentIdx = -1
			} else if m.focusedPane == PaneCommentDetail {
				m.focusedPane = PaneCommentList
			}

		case "tab":
			m.cycleFocus()

		case "f":
			m.cycleFilter()

		case "1":
			m.setFilter(FilterCritical)

		case "2":
			m.setFilter(FilterWarning)

		case "3":
			m.setFilter(FilterSuggestion)

		case "0":
			m.setFilter(FilterNone)

		case "p":
			m.confirmPost = true

		case "g":
			m.goToTop()

		case "G":
			m.goToBottom()

		case "ctrl+d":
			m.pageDown()

		case "ctrl+u":
			m.pageUp()

		case " ":
			m.toggleFileExpanded()
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *ReviewModel) handleConfirmPost(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.confirmPost = false
		if m.onPost != nil {
			if err := m.onPost(m.getFilteredComments()); err != nil {
				return m, nil
			}
		}
		return m, tea.Quit
	case "n", "N", "esc":
		m.confirmPost = false
	}
	return m, nil
}

func (m *ReviewModel) updateViewportSizes() {
	if m.width == 0 || m.height == 0 {
		return
	}

	fileListWidth := m.width / 4
	commentListWidth := m.width / 2
	detailWidth := m.width - fileListWidth - commentListWidth - 4

	contentHeight := m.height - 4

	m.fileListViewport.Width = fileListWidth
	m.fileListViewport.Height = contentHeight

	m.commentListViewport.Width = commentListWidth
	m.commentListViewport.Height = contentHeight

	m.detailViewport.Width = detailWidth
	m.detailViewport.Height = contentHeight
}

func (m *ReviewModel) moveDown() {
	switch m.focusedPane {
	case PaneFileList:
		if m.selectedFileIdx < len(m.filteredGroups)-1 {
			m.selectedFileIdx++
			m.selectedCommentIdx = 0
		}
	case PaneCommentList:
		group := m.getCurrentGroup()
		if group != nil && m.selectedCommentIdx < len(group.Comments)-1 {
			m.selectedCommentIdx++
		} else if m.selectedFileIdx < len(m.filteredGroups)-1 {
			m.selectedFileIdx++
			m.selectedCommentIdx = 0
		}
	}
}

func (m *ReviewModel) moveUp() {
	switch m.focusedPane {
	case PaneFileList:
		if m.selectedFileIdx > 0 {
			m.selectedFileIdx--
			m.selectedCommentIdx = 0
		}
	case PaneCommentList:
		if m.selectedCommentIdx > 0 {
			m.selectedCommentIdx--
		} else if m.selectedFileIdx > 0 {
			m.selectedFileIdx--
			group := m.getCurrentGroup()
			if group != nil {
				m.selectedCommentIdx = len(group.Comments) - 1
			}
		}
	}
}

func (m *ReviewModel) expandSelected() {
	if m.focusedPane == PaneCommentList {
		group := m.getCurrentGroup()
		if group != nil && m.selectedCommentIdx < len(group.Comments) {
			globalIdx := group.StartIdx + m.selectedCommentIdx
			if m.expandedCommentIdx == globalIdx {
				m.expandedCommentIdx = -1
			} else {
				m.expandedCommentIdx = globalIdx
				m.focusedPane = PaneCommentDetail
			}
		}
	}
}

func (m *ReviewModel) cycleFocus() {
	switch m.focusedPane {
	case PaneFileList:
		m.focusedPane = PaneCommentList
	case PaneCommentList:
		if m.expandedCommentIdx >= 0 {
			m.focusedPane = PaneCommentDetail
		} else {
			m.focusedPane = PaneFileList
		}
	case PaneCommentDetail:
		m.focusedPane = PaneFileList
	}
}

func (m *ReviewModel) cycleFilter() {
	switch m.filterMode {
	case FilterNone:
		m.setFilter(FilterCritical)
	case FilterCritical:
		m.setFilter(FilterWarning)
	case FilterWarning:
		m.setFilter(FilterSuggestion)
	case FilterSuggestion:
		m.setFilter(FilterNone)
	default:
		m.setFilter(FilterNone)
	}
}

func (m *ReviewModel) setFilter(mode FilterMode) {
	m.filterMode = mode
	m.applyFilter()
	m.selectedFileIdx = 0
	m.selectedCommentIdx = 0
}

func (m *ReviewModel) applyFilter() {
	if m.filterMode == FilterNone {
		m.filteredGroups = m.fileGroups
		return
	}

	var severityFilter string
	switch m.filterMode {
	case FilterCritical:
		severityFilter = "critical"
	case FilterWarning:
		severityFilter = "warning"
	case FilterSuggestion:
		severityFilter = "suggestion"
	}

	filtered := make([]FileGroup, 0)
	idx := 0
	for _, group := range m.fileGroups {
		var matchedComments []ReviewComment
		for _, c := range group.Comments {
			if c.Severity == severityFilter {
				matchedComments = append(matchedComments, c)
			}
		}
		if len(matchedComments) > 0 {
			filtered = append(filtered, FileGroup{
				Path:     group.Path,
				Comments: matchedComments,
				Expanded: group.Expanded,
				StartIdx: idx,
			})
			idx += len(matchedComments)
		}
	}
	m.filteredGroups = filtered
}

func (m *ReviewModel) toggleFileExpanded() {
	if m.selectedFileIdx < len(m.filteredGroups) {
		m.filteredGroups[m.selectedFileIdx].Expanded = !m.filteredGroups[m.selectedFileIdx].Expanded
	}
}

func (m *ReviewModel) goToTop() {
	m.selectedFileIdx = 0
	m.selectedCommentIdx = 0
}

func (m *ReviewModel) goToBottom() {
	if len(m.filteredGroups) > 0 {
		m.selectedFileIdx = len(m.filteredGroups) - 1
		group := m.getCurrentGroup()
		if group != nil {
			m.selectedCommentIdx = len(group.Comments) - 1
		}
	}
}

func (m *ReviewModel) pageDown() {
	for i := 0; i < 10; i++ {
		m.moveDown()
	}
}

func (m *ReviewModel) pageUp() {
	for i := 0; i < 10; i++ {
		m.moveUp()
	}
}

func (m *ReviewModel) getCurrentGroup() *FileGroup {
	if m.selectedFileIdx >= 0 && m.selectedFileIdx < len(m.filteredGroups) {
		return &m.filteredGroups[m.selectedFileIdx]
	}
	return nil
}

func (m *ReviewModel) getFilteredComments() []ReviewComment {
	var result []ReviewComment
	for _, group := range m.filteredGroups {
		result = append(result, group.Comments...)
	}
	return result
}

// View renders the review TUI.
func (m *ReviewModel) View() string {
	if !m.ready {
		return "Loading..."
	}

	if m.showHelp {
		return m.renderHelp()
	}

	if m.confirmPost {
		return m.renderConfirmPost()
	}

	var layout strings.Builder

	header := m.renderHeader()
	layout.WriteString(header)
	layout.WriteString("\n")

	filterBar := m.renderFilterBar()
	layout.WriteString(filterBar)
	layout.WriteString("\n")

	contentHeight := m.height - 4
	fileList := m.renderFileList(contentHeight)
	commentList := m.renderCommentList(contentHeight)
	detailView := m.renderDetailView(contentHeight)

	fileListWidth := m.width / 4
	commentListWidth := m.width / 2
	detailWidth := m.width - fileListWidth - commentListWidth - 4

	fileListPane := m.wrapPane(fileList, fileListWidth, contentHeight, m.focusedPane == PaneFileList)
	commentListPane := m.wrapPane(commentList, commentListWidth, contentHeight, m.focusedPane == PaneCommentList)
	detailPane := m.wrapPane(detailView, detailWidth, contentHeight, m.focusedPane == PaneCommentDetail)

	contentRow := lipgloss.JoinHorizontal(lipgloss.Top, fileListPane, commentListPane, detailPane)
	layout.WriteString(contentRow)
	layout.WriteString("\n")

	statusBar := m.renderStatusBar()
	layout.WriteString(statusBar)

	return layout.String()
}

func (m *ReviewModel) wrapPane(content string, width, height int, active bool) string {
	style := m.styles.PaneBorder
	if active {
		style = m.styles.PaneBorderActive
	}
	return style.Width(width).Height(height).Render(content)
}

func (m *ReviewModel) renderHeader() string {
	title := lipgloss.NewStyle().
		Foreground(m.theme.Accent).
		Bold(true).
		Render("◉ Review Comments")

	counts := m.getCommentCounts()
	stats := fmt.Sprintf("Total: %d | Critical: %d | Warning: %d | Suggestion: %d",
		counts["total"], counts["critical"], counts["warning"], counts["suggestion"])

	statsStyled := lipgloss.NewStyle().
		Foreground(m.theme.TextMuted).
		Render(stats)

	padding := m.width - lipgloss.Width(title) - lipgloss.Width(statsStyled) - 2
	if padding < 0 {
		padding = 1
	}

	return title + strings.Repeat(" ", padding) + statsStyled
}

func (m *ReviewModel) renderFilterBar() string {
	filters := []struct {
		mode  FilterMode
		label string
		key   string
	}{
		{FilterNone, "All", "0"},
		{FilterCritical, "Critical", "1"},
		{FilterWarning, "Warning", "2"},
		{FilterSuggestion, "Suggestion", "3"},
	}

	var parts []string
	for _, f := range filters {
		style := m.styles.FilterBar
		if m.filterMode == f.mode {
			style = m.styles.FilterActive
		}
		parts = append(parts, style.Render(fmt.Sprintf("[%s] %s", f.key, f.label)))
	}

	return strings.Join(parts, " ")
}

func (m *ReviewModel) renderFileList(height int) string {
	var lines []string

	for i, group := range m.filteredGroups {
		icon := "▼"
		if !group.Expanded {
			icon = "▶"
		}

		style := m.styles.FileHeader
		if i == m.selectedFileIdx && m.focusedPane == PaneFileList {
			style = m.styles.FileHeaderActive
		}

		fileName := truncateString(group.Path, m.width/4-6)
		line := style.Render(fmt.Sprintf("%s %s (%d)", icon, fileName, len(group.Comments)))
		lines = append(lines, line)
	}

	for len(lines) < height {
		lines = append(lines, "")
	}

	return strings.Join(lines[:min(height, len(lines))], "\n")
}

func (m *ReviewModel) renderCommentList(height int) string {
	var lines []string

	for groupIdx, group := range m.filteredGroups {
		if !group.Expanded {
			continue
		}

		for commentIdx, comment := range group.Comments {
			icon := m.getSeverityIcon(comment.Severity)
			isSelected := groupIdx == m.selectedFileIdx &&
				commentIdx == m.selectedCommentIdx &&
				m.focusedPane == PaneCommentList

			style := m.styles.CommentItem
			if isSelected {
				style = m.styles.CommentActive
			}

			lineInfo := m.styles.LineNumber.Render(fmt.Sprintf("L%d", comment.LineNumber))
			category := m.styles.CategoryBadge.Render(comment.Category)

			preview := truncateString(comment.Content, m.width/2-20)
			line := style.Render(fmt.Sprintf("%s %s %s %s", icon, lineInfo, category, preview))
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(m.theme.TextMuted).
			Render("No comments match the current filter"))
	}

	for len(lines) < height {
		lines = append(lines, "")
	}

	return strings.Join(lines[:min(height, len(lines))], "\n")
}

func (m *ReviewModel) renderDetailView(height int) string {
	if m.expandedCommentIdx < 0 {
		return lipgloss.NewStyle().
			Foreground(m.theme.TextMuted).
			Render("Select a comment and press Enter to view details")
	}

	comment := m.getCommentByGlobalIdx(m.expandedCommentIdx)
	if comment == nil {
		return ""
	}

	var lines []string

	headerLine := fmt.Sprintf("%s %s:%d",
		m.getSeverityIcon(comment.Severity),
		comment.FilePath,
		comment.LineNumber)
	lines = append(lines, m.styles.FileHeader.Render(headerLine))
	lines = append(lines, "")

	lines = append(lines, m.styles.CategoryBadge.Render(comment.Category))
	lines = append(lines, "")

	contentLines := wrapText(comment.Content, m.width/4-4)
	lines = append(lines, contentLines...)
	lines = append(lines, "")

	if comment.CodeBlock != "" {
		lines = append(lines, m.styles.HelpKey.Render("Code:"))
		codeLines := strings.Split(comment.CodeBlock, "\n")
		for _, cl := range codeLines {
			lines = append(lines, m.styles.CodeBlock.Render(cl))
		}
		lines = append(lines, "")
	}

	if comment.DiffBlock != "" {
		lines = append(lines, m.styles.HelpKey.Render("Diff:"))
		diffLines := strings.Split(comment.DiffBlock, "\n")
		for _, dl := range diffLines {
			style := m.styles.DiffContext
			if strings.HasPrefix(dl, "+") {
				style = m.styles.DiffAdded
			} else if strings.HasPrefix(dl, "-") {
				style = m.styles.DiffRemoved
			}
			lines = append(lines, style.Render(dl))
		}
		lines = append(lines, "")
	}

	if comment.Suggestion != "" {
		lines = append(lines, m.styles.HelpKey.Render("Suggestion:"))
		suggestionLines := wrapText(comment.Suggestion, m.width/4-6)
		for _, sl := range suggestionLines {
			lines = append(lines, m.styles.Suggestion.Render(sl))
		}
	}

	for len(lines) < height {
		lines = append(lines, "")
	}

	return strings.Join(lines[:min(height, len(lines))], "\n")
}

func (m *ReviewModel) renderStatusBar() string {
	mode := "REVIEW"
	if m.filterMode != FilterNone {
		mode = "FILTER"
	}

	modeStyle := lipgloss.NewStyle().
		Background(m.theme.Accent).
		Foreground(m.theme.Background).
		Bold(true).
		Padding(0, 1).
		Render(mode)

	hints := "j/k: Navigate  Enter: Expand  f: Filter  p: Post  ?: Help  q: Quit"
	hintsStyled := lipgloss.NewStyle().
		Foreground(m.theme.TextMuted).
		Render(hints)

	position := ""
	if len(m.filteredGroups) > 0 {
		position = fmt.Sprintf("File %d/%d", m.selectedFileIdx+1, len(m.filteredGroups))
	}
	positionStyled := lipgloss.NewStyle().
		Foreground(m.theme.TextSecondary).
		Render(position)

	leftWidth := lipgloss.Width(modeStyle)
	rightWidth := lipgloss.Width(positionStyled)
	centerWidth := lipgloss.Width(hintsStyled)
	padding := m.width - leftWidth - rightWidth - centerWidth - 4
	if padding < 0 {
		padding = 1
	}

	leftPad := padding / 2
	rightPad := padding - leftPad

	return modeStyle + strings.Repeat(" ", leftPad) + hintsStyled + strings.Repeat(" ", rightPad) + positionStyled
}

func (m *ReviewModel) renderHelp() string {
	helpItems := []struct {
		key  string
		desc string
	}{
		{"j/k, ↑/↓", "Navigate up/down"},
		{"Enter, l", "Expand/select comment"},
		{"h, Esc", "Collapse/go back"},
		{"Tab", "Cycle between panes"},
		{"Space", "Toggle file expand/collapse"},
		{"f", "Cycle through filters"},
		{"0-3", "Quick filter (All/Critical/Warning/Suggestion)"},
		{"g/G", "Go to top/bottom"},
		{"Ctrl+d/u", "Page down/up"},
		{"p", "Post comments to GitHub"},
		{"m", "Toggle metrics view"},
		{"?", "Toggle this help"},
		{"q", "Quit"},
	}

	var lines []string
	lines = append(lines, m.styles.FileHeader.Render("Keyboard Shortcuts"))
	lines = append(lines, "")

	for _, item := range helpItems {
		key := m.styles.HelpKey.Render(fmt.Sprintf("%-12s", item.key))
		desc := m.styles.HelpDesc.Render(item.desc)
		lines = append(lines, "  "+key+"  "+desc)
	}

	content := strings.Join(lines, "\n")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		m.styles.HelpPanel.Render(content))
}

func (m *ReviewModel) renderConfirmPost() string {
	counts := m.getCommentCounts()
	content := fmt.Sprintf("Post %d comments to GitHub?\n\n"+
		"  Critical: %d\n"+
		"  Warning: %d\n"+
		"  Suggestion: %d\n\n"+
		"[Y]es  [N]o",
		counts["total"], counts["critical"], counts["warning"], counts["suggestion"])

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		m.styles.HelpPanel.Render(content))
}

func (m *ReviewModel) getSeverityIcon(severity string) string {
	switch severity {
	case "critical":
		return m.styles.SeverityCritical.Render("●")
	case "warning":
		return m.styles.SeverityWarning.Render("●")
	case "suggestion":
		return m.styles.SeveritySuggestion.Render("●")
	default:
		return "○"
	}
}

func (m *ReviewModel) getCommentCounts() map[string]int {
	counts := map[string]int{"total": 0, "critical": 0, "warning": 0, "suggestion": 0}

	for _, c := range m.comments {
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

func (m *ReviewModel) getCommentByGlobalIdx(idx int) *ReviewComment {
	current := 0
	for _, group := range m.filteredGroups {
		if idx < current+len(group.Comments) {
			return &group.Comments[idx-current]
		}
		current += len(group.Comments)
	}
	return nil
}

// SetOnPost sets the callback for posting comments.
func (m *ReviewModel) SetOnPost(fn func([]ReviewComment) error) {
	m.onPost = fn
}

// SetOnQuit sets the callback for quit.
func (m *ReviewModel) SetOnQuit(fn func()) {
	m.onQuit = fn
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{}
	}

	var lines []string
	var currentLine strings.Builder

	for _, word := range words {
		if currentLine.Len()+len(word)+1 > width {
			if currentLine.Len() > 0 {
				lines = append(lines, currentLine.String())
				currentLine.Reset()
			}
		}
		if currentLine.Len() > 0 {
			currentLine.WriteString(" ")
		}
		currentLine.WriteString(word)
	}

	if currentLine.Len() > 0 {
		lines = append(lines, currentLine.String())
	}

	return lines
}

// RunReviewTUI launches the interactive review TUI.
func RunReviewTUI(comments []ReviewComment, onPost func([]ReviewComment) error) error {
	theme := ClaudeCodeTheme()
	model := NewReviewModel(comments, theme)
	model.SetOnPost(onPost)

	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// ReviewTickMsg is sent periodically to update the review display.
type ReviewTickMsg time.Time
