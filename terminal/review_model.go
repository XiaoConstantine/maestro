package terminal

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
	Path     string
	Comments []ReviewComment
	Expanded bool
	StartIdx int
}

// FilterMode represents the current filter state.
type FilterMode int

const (
	FilterNone FilterMode = iota
	FilterCritical
	FilterWarning
	FilterSuggestion
)

// ReviewModel is the main TUI model for displaying review comments (Crush-style).
type ReviewModel struct {
	comments       []ReviewComment
	fileGroups     []FileGroup
	filteredGroups []FileGroup

	selectedIdx      int // Global index in flattened list
	selectedFileIdx  int // Currently selected file group index
	selectedInFile   int // Index within current file group
	filterMode       FilterMode

	listViewport   viewport.Model
	detailViewport viewport.Model

	width  int
	height int
	ready  bool

	theme  *Theme
	styles *ReviewStyles

	showDetail  bool
	confirmPost bool

	onPost func([]ReviewComment) error
	onQuit func()
}

// ReviewStyles holds styles specific to review display.
type ReviewStyles struct {
	// Section headers
	SectionTitle lipgloss.Style
	SectionLine  lipgloss.Style

	// List items
	FileItem         lipgloss.Style
	FileActive       lipgloss.Style
	CommentItem      lipgloss.Style
	CommentActive    lipgloss.Style
	CommentHighlight lipgloss.Style

	// Severity badges
	SeverityCritical   lipgloss.Style
	SeverityWarning    lipgloss.Style
	SeveritySuggestion lipgloss.Style

	// Detail view
	DetailTitle    lipgloss.Style
	DetailContent  lipgloss.Style
	CodeBlock      lipgloss.Style
	DiffAdded      lipgloss.Style
	DiffRemoved    lipgloss.Style
	Suggestion     lipgloss.Style

	// Status elements
	StatusMuted lipgloss.Style
	StatusInfo  lipgloss.Style
}

// NewReviewModel creates a new review TUI model with Crush-style layout.
func NewReviewModel(comments []ReviewComment, theme *Theme) *ReviewModel {
	styles := createCrushReviewStyles(theme)

	listVP := viewport.New()
	listVP.SetWidth(60)
	listVP.SetHeight(20)
	listVP.KeyMap = viewport.KeyMap{}

	detailVP := viewport.New()
	detailVP.SetWidth(60)
	detailVP.SetHeight(20)
	detailVP.KeyMap = viewport.KeyMap{}

	m := &ReviewModel{
		comments:       comments,
		fileGroups:     groupCommentsByFile(comments),
		selectedIdx:    0,
		filterMode:     FilterNone,
		listViewport:   listVP,
		detailViewport: detailVP,
		theme:          theme,
		styles:         styles,
		showDetail:     false,
	}

	m.filteredGroups = m.fileGroups
	return m
}

func createCrushReviewStyles(theme *Theme) *ReviewStyles {
	return &ReviewStyles{
		// Section headers - Crush style with subtle line
		SectionTitle: lipgloss.NewStyle().
			Foreground(theme.TextMuted).
			Bold(false),

		SectionLine: lipgloss.NewStyle().
			Foreground(theme.Border),

		// File items - clean, minimal
		FileItem: lipgloss.NewStyle().
			Foreground(theme.TextSecondary),

		FileActive: lipgloss.NewStyle().
			Foreground(theme.TextPrimary).
			Bold(true),

		// Comment items
		CommentItem: lipgloss.NewStyle().
			Foreground(theme.TextMuted),

		CommentActive: lipgloss.NewStyle().
			Foreground(theme.TextPrimary),

		CommentHighlight: lipgloss.NewStyle().
			Background(lipgloss.Color("#3A3C55")).
			Foreground(theme.TextPrimary).
			Bold(true),

		// Severity - simple colored dots
		SeverityCritical: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B")),

		SeverityWarning: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFD93D")),

		SeveritySuggestion: lipgloss.NewStyle().
			Foreground(theme.StatusHighlight),

		// Detail view
		DetailTitle: lipgloss.NewStyle().
			Foreground(theme.TextPrimary).
			Bold(true),

		DetailContent: lipgloss.NewStyle().
			Foreground(theme.TextSecondary),

		CodeBlock: lipgloss.NewStyle().
			Foreground(theme.Code).
			Background(theme.Surface).
			Padding(0, 1),

		DiffAdded: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7FE9DE")),

		DiffRemoved: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B")),

		Suggestion: lipgloss.NewStyle().
			Foreground(theme.StatusHighlight).
			Italic(true),

		StatusMuted: lipgloss.NewStyle().
			Foreground(theme.TextMuted),

		StatusInfo: lipgloss.NewStyle().
			Foreground(theme.TextSecondary),
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
	return func() tea.Msg { return tea.RequestWindowSize() }
}

// Update handles messages for the review model.
func (m *ReviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.updateViewportSizes()

	case tea.KeyPressMsg:
		if m.confirmPost {
			return m.handleConfirmPost(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			if m.onQuit != nil {
				m.onQuit()
			}
			return m, tea.Quit

		case "j", "down":
			m.moveDown()

		case "k", "up":
			m.moveUp()

		case "enter", "l", "right":
			m.showDetail = true

		case "h", "left", "esc":
			if m.showDetail {
				m.showDetail = false
			}

		case "tab", " ":
			m.toggleCurrentFileGroup()

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
			m.selectedIdx = 0

		case "G":
			m.selectedIdx = m.getTotalComments() - 1
			if m.selectedIdx < 0 {
				m.selectedIdx = 0
			}

		case "ctrl+d":
			for i := 0; i < 10; i++ {
				m.moveDown()
			}

		case "ctrl+u":
			for i := 0; i < 10; i++ {
				m.moveUp()
			}
		}
	}

	return m, nil
}

func (m *ReviewModel) handleConfirmPost(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

	contentHeight := m.height - 6 // Header + filter + status

	if m.showDetail {
		// Split view: 40% list, 60% detail
		listWidth := m.width * 2 / 5
		detailWidth := m.width - listWidth - 2

		m.listViewport.SetWidth(listWidth)
		m.listViewport.SetHeight(contentHeight)

		m.detailViewport.SetWidth(detailWidth)
		m.detailViewport.SetHeight(contentHeight)
	} else {
		// Full width list
		m.listViewport.SetWidth(m.width - 4)
		m.listViewport.SetHeight(contentHeight)
	}
}

func (m *ReviewModel) moveDown() {
	total := m.getTotalVisibleItems()
	if m.selectedIdx < total-1 {
		m.selectedIdx++
	}
	m.updateFileIndexFromSelected()
}

func (m *ReviewModel) moveUp() {
	if m.selectedIdx > 0 {
		m.selectedIdx--
	}
	m.updateFileIndexFromSelected()
}

func (m *ReviewModel) updateFileIndexFromSelected() {
	idx := 0
	for i, g := range m.filteredGroups {
		if !g.Expanded {
			if idx == m.selectedIdx {
				m.selectedFileIdx = i
				m.selectedInFile = -1
				return
			}
			idx++
		} else {
			for j := range g.Comments {
				if idx == m.selectedIdx {
					m.selectedFileIdx = i
					m.selectedInFile = j
					return
				}
				idx++
			}
		}
	}
}

func (m *ReviewModel) toggleCurrentFileGroup() {
	if m.selectedFileIdx >= 0 && m.selectedFileIdx < len(m.filteredGroups) {
		m.filteredGroups[m.selectedFileIdx].Expanded = !m.filteredGroups[m.selectedFileIdx].Expanded
		m.recalculateSelectedIdx()
	}
}

func (m *ReviewModel) recalculateSelectedIdx() {
	idx := 0
	for i, g := range m.filteredGroups {
		if i == m.selectedFileIdx {
			if !g.Expanded || m.selectedInFile < 0 {
				m.selectedIdx = idx
			} else {
				m.selectedIdx = idx + m.selectedInFile
			}
			return
		}
		if g.Expanded {
			idx += len(g.Comments)
		} else {
			idx++
		}
	}
}

func (m *ReviewModel) getTotalComments() int {
	total := 0
	for _, g := range m.filteredGroups {
		total += len(g.Comments)
	}
	return total
}

func (m *ReviewModel) getTotalVisibleItems() int {
	total := 0
	for _, g := range m.filteredGroups {
		if g.Expanded {
			total += len(g.Comments)
		} else {
			total++
		}
	}
	return total
}

func (m *ReviewModel) setFilter(mode FilterMode) {
	m.filterMode = mode
	m.applyFilter()
	m.selectedIdx = 0
}

func (m *ReviewModel) applyFilter() {
	if m.filterMode == FilterNone {
		m.filteredGroups = m.fileGroups
		return
	}

	var severity string
	switch m.filterMode {
	case FilterCritical:
		severity = "critical"
	case FilterWarning:
		severity = "warning"
	case FilterSuggestion:
		severity = "suggestion"
	}

	var filtered []FileGroup
	for _, g := range m.fileGroups {
		var comments []ReviewComment
		for _, c := range g.Comments {
			if c.Severity == severity {
				comments = append(comments, c)
			}
		}
		if len(comments) > 0 {
			filtered = append(filtered, FileGroup{
				Path:     g.Path,
				Comments: comments,
				Expanded: true,
			})
		}
	}

	// Recalculate start indices
	idx := 0
	for i := range filtered {
		filtered[i].StartIdx = idx
		idx += len(filtered[i].Comments)
	}

	m.filteredGroups = filtered
}

func (m *ReviewModel) getFilteredComments() []ReviewComment {
	var result []ReviewComment
	for _, group := range m.filteredGroups {
		result = append(result, group.Comments...)
	}
	return result
}

func (m *ReviewModel) getSelectedComment() *ReviewComment {
	idx := 0
	for _, g := range m.filteredGroups {
		if !g.Expanded {
			idx++
			continue
		}
		for i := range g.Comments {
			if idx == m.selectedIdx {
				return &g.Comments[i]
			}
			idx++
		}
	}
	return nil
}

// View renders the review TUI.
func (m *ReviewModel) View() tea.View {
	var view tea.View
	view.AltScreen = true
	view.SetContent(m.ViewString())
	return view
}

// ViewString returns the view content as a string (for embedding in other views).
func (m *ReviewModel) ViewString() string {
	if !m.ready {
		return "Loading..."
	}

	if m.confirmPost {
		return m.renderConfirmPost()
	}

	var parts []string

	// Header with title and counts
	parts = append(parts, m.renderHeader())
	parts = append(parts, "")

	// Filter tabs
	parts = append(parts, m.renderFilterTabs())
	parts = append(parts, "")

	// Main content
	if m.showDetail {
		// Split view
		listContent := m.renderList()
		detailContent := m.renderDetail()

		listWidth := m.width * 2 / 5
		detailWidth := m.width - listWidth - 3

		listPane := lipgloss.NewStyle().
			Width(listWidth).
			Height(m.height - 6).
			Render(listContent)

		separator := lipgloss.NewStyle().
			Foreground(m.theme.Border).
			Render("│")

		detailPane := lipgloss.NewStyle().
			Width(detailWidth).
			Height(m.height - 6).
			PaddingLeft(1).
			Render(detailContent)

		content := lipgloss.JoinHorizontal(lipgloss.Top, listPane, separator, detailPane)
		parts = append(parts, content)
	} else {
		// Full list view
		parts = append(parts, m.renderList())
	}

	// Status bar
	parts = append(parts, "")
	parts = append(parts, m.renderStatusBar())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *ReviewModel) renderHeader() string {
	// Title with icon
	title := lipgloss.NewStyle().
		Foreground(m.theme.LogoPrimary).
		Bold(true).
		Render("Review Comments")

	// Counts
	counts := m.getCommentCounts()
	countStr := fmt.Sprintf("%d comments", counts["total"])

	if counts["critical"] > 0 {
		countStr += fmt.Sprintf(" • %s %d critical",
			m.styles.SeverityCritical.Render("●"),
			counts["critical"])
	}
	if counts["warning"] > 0 {
		countStr += fmt.Sprintf(" • %s %d warning",
			m.styles.SeverityWarning.Render("●"),
			counts["warning"])
	}
	if counts["suggestion"] > 0 {
		countStr += fmt.Sprintf(" • %s %d suggestion",
			m.styles.SeveritySuggestion.Render("●"),
			counts["suggestion"])
	}

	countStyled := m.styles.StatusMuted.Render(countStr)

	// Section line
	titleWidth := lipgloss.Width(title) + lipgloss.Width(countStyled) + 4
	lineWidth := m.width - titleWidth
	if lineWidth < 0 {
		lineWidth = 0
	}
	line := m.styles.SectionLine.Render(strings.Repeat("─", lineWidth))

	return lipgloss.JoinHorizontal(lipgloss.Center, title, " ", line, " ", countStyled)
}

func (m *ReviewModel) renderFilterTabs() string {
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
		label := fmt.Sprintf("[%s] %s", f.key, f.label)
		if m.filterMode == f.mode {
			// Active tab
			parts = append(parts, lipgloss.NewStyle().
				Foreground(m.theme.TextPrimary).
				Bold(true).
				Render(label))
		} else {
			// Inactive tab
			parts = append(parts, m.styles.StatusMuted.Render(label))
		}
	}

	return strings.Join(parts, "  ")
}

func (m *ReviewModel) renderList() string {
	var lines []string
	currentIdx := 0
	contentWidth := m.listViewport.Width() - 2

	for fileIdx, group := range m.filteredGroups {
		isFileSelected := fileIdx == m.selectedFileIdx && m.selectedInFile < 0

		// Expand/collapse arrow
		var arrow string
		if group.Expanded {
			arrow = "▼ "
		} else {
			arrow = "▶ "
		}

		// File header - Crush style section
		fileName := truncatePath(group.Path, contentWidth-15)
		countStr := fmt.Sprintf("(%d)", len(group.Comments))

		// Style based on whether file header is selected
		var fileHeader string
		if isFileSelected {
			fileHeader = lipgloss.NewStyle().
				Background(lipgloss.Color("#3A3C55")).
				Foreground(m.theme.TextPrimary).
				Bold(true).
				Render(arrow + fileName + " " + countStr)
		} else {
			fileHeader = lipgloss.NewStyle().
				Foreground(m.theme.TextMuted).
				Render(arrow + fileName + " " + countStr)
		}

		// Add separator line
		lineWidth := contentWidth - lipgloss.Width(fileHeader) - 1
		if lineWidth > 0 {
			fileHeader = fileHeader + " " + m.styles.SectionLine.Render(strings.Repeat("─", lineWidth))
		}

		lines = append(lines, fileHeader)

		if !group.Expanded {
			currentIdx++
			lines = append(lines, "") // Space between files
			continue
		}

		// Comments under this file
		for i, comment := range group.Comments {
			isSelected := currentIdx == m.selectedIdx
			line := m.renderCommentLine(comment, isSelected, contentWidth)
			lines = append(lines, "  "+line) // Indent under file
			currentIdx++

			// Add spacing between comments (except last)
			if i < len(group.Comments)-1 {
				lines = append(lines, "")
			}
		}

		lines = append(lines, "") // Space between files
	}

	if len(lines) == 0 {
		return m.styles.StatusMuted.Render("No comments match the current filter")
	}

	return strings.Join(lines, "\n")
}

func (m *ReviewModel) renderCommentLine(comment ReviewComment, selected bool, maxWidth int) string {
	// Severity icon
	icon := m.getSeverityIcon(comment.Severity)

	// Line number
	lineNum := m.styles.StatusMuted.Render(fmt.Sprintf("L%d", comment.LineNumber))

	// Category badge (if exists)
	var category string
	if comment.Category != "" {
		category = lipgloss.NewStyle().
			Foreground(m.theme.TextMuted).
			Background(m.theme.Surface).
			Padding(0, 1).
			Render(comment.Category)
	}

	// Content preview
	contentWidth := maxWidth - 20 // Account for icon, line num, category
	if category != "" {
		contentWidth -= lipgloss.Width(category) + 1
	}

	preview := ansi.Truncate(comment.Content, contentWidth, "…")

	// Style based on selection
	if selected {
		preview = m.styles.CommentHighlight.Render(preview)
	} else {
		preview = m.styles.StatusInfo.Render(preview)
	}

	// Combine parts
	parts := []string{icon, lineNum}
	if category != "" {
		parts = append(parts, category)
	}
	parts = append(parts, preview)

	line := strings.Join(parts, " ")

	// Add selection indicator with highlight background
	if selected {
		indicator := lipgloss.NewStyle().
			Foreground(m.theme.Accent).
			Bold(true).
			Render("▸ ")
		line = indicator + m.styles.CommentHighlight.Render(line)
	} else {
		line = "  " + line
	}

	return line
}

func (m *ReviewModel) renderDetail() string {
	comment := m.getSelectedComment()
	if comment == nil {
		return m.styles.StatusMuted.Render("Select a comment to view details")
	}

	var lines []string
	detailWidth := m.detailViewport.Width() - 2

	// File and line
	header := fmt.Sprintf("%s:%d", truncatePath(comment.FilePath, detailWidth-10), comment.LineNumber)
	lines = append(lines, m.styles.DetailTitle.Render(header))
	lines = append(lines, "")

	// Severity and category
	severityLine := m.getSeverityIcon(comment.Severity) + " " +
		lipgloss.NewStyle().Foreground(m.theme.TextSecondary).Render(comment.Severity)
	if comment.Category != "" {
		severityLine += " • " + m.styles.StatusMuted.Render(comment.Category)
	}
	lines = append(lines, severityLine)
	lines = append(lines, "")

	// Content
	lines = append(lines, m.styles.StatusMuted.Render("Issue"))
	contentLines := wrapText(comment.Content, detailWidth)
	for _, cl := range contentLines {
		lines = append(lines, m.styles.DetailContent.Render(cl))
	}
	lines = append(lines, "")

	// Code block if present
	if comment.CodeBlock != "" {
		lines = append(lines, m.styles.StatusMuted.Render("Code"))
		codeLines := strings.Split(comment.CodeBlock, "\n")
		for _, cl := range codeLines {
			lines = append(lines, m.styles.CodeBlock.Render(cl))
		}
		lines = append(lines, "")
	}

	// Diff if present
	if comment.DiffBlock != "" {
		lines = append(lines, m.styles.StatusMuted.Render("Changes"))
		diffLines := strings.Split(comment.DiffBlock, "\n")
		for _, dl := range diffLines {
			style := m.styles.StatusMuted
			if strings.HasPrefix(dl, "+") {
				style = m.styles.DiffAdded
			} else if strings.HasPrefix(dl, "-") {
				style = m.styles.DiffRemoved
			}
			lines = append(lines, style.Render(dl))
		}
		lines = append(lines, "")
	}

	// Suggestion if present
	if comment.Suggestion != "" {
		lines = append(lines, m.styles.StatusMuted.Render("Suggestion"))
		suggestionLines := wrapText(comment.Suggestion, detailWidth-2)
		for _, sl := range suggestionLines {
			lines = append(lines, m.styles.Suggestion.Render("  "+sl))
		}
	}

	return strings.Join(lines, "\n")
}

func (m *ReviewModel) renderStatusBar() string {
	// Crush-style hints at bottom
	hints := []string{
		"j/k navigate",
		"enter view details",
		"tab focus input",
		"esc close",
	}

	hintStr := strings.Join(hints, " • ")
	hintStyled := m.styles.StatusMuted.Render(hintStr)

	// Center the hints
	padding := (m.width - lipgloss.Width(hintStyled)) / 2
	if padding < 0 {
		padding = 0
	}

	return strings.Repeat(" ", padding) + hintStyled
}

func (m *ReviewModel) renderConfirmPost() string {
	counts := m.getCommentCounts()

	var lines []string
	lines = append(lines, m.styles.DetailTitle.Render("Post comments to GitHub?"))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  Total: %d comments", counts["total"]))

	if counts["critical"] > 0 {
		lines = append(lines, fmt.Sprintf("  %s Critical: %d",
			m.styles.SeverityCritical.Render("●"), counts["critical"]))
	}
	if counts["warning"] > 0 {
		lines = append(lines, fmt.Sprintf("  %s Warning: %d",
			m.styles.SeverityWarning.Render("●"), counts["warning"]))
	}
	if counts["suggestion"] > 0 {
		lines = append(lines, fmt.Sprintf("  %s Suggestion: %d",
			m.styles.SeveritySuggestion.Render("●"), counts["suggestion"]))
	}

	lines = append(lines, "")
	lines = append(lines, m.styles.StatusInfo.Render("[Y]es  [N]o"))

	content := strings.Join(lines, "\n")

	// Center in screen
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Border).
		Padding(1, 3).
		Render(content)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
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
		return m.styles.StatusMuted.Render("○")
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

// SetOnPost sets the callback for posting comments.
func (m *ReviewModel) SetOnPost(fn func([]ReviewComment) error) {
	m.onPost = fn
}

// SetOnQuit sets the callback for quit.
func (m *ReviewModel) SetOnQuit(fn func()) {
	m.onQuit = fn
}

// SetSize sets the dimensions and marks the model as ready.
func (m *ReviewModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.ready = true
	m.updateViewportSizes()
}

func truncatePath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	// Show last part of path
	parts := strings.Split(path, "/")
	if len(parts) <= 2 {
		if maxLen <= 3 {
			return path[:maxLen]
		}
		return path[:maxLen-3] + "..."
	}
	// Try to fit last 2 parts
	short := parts[len(parts)-2] + "/" + parts[len(parts)-1]
	if len(short) <= maxLen {
		return "…/" + short
	}
	if maxLen <= 3 {
		return short[:maxLen]
	}
	return short[:maxLen-1] + "…"
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

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/tty: %w", err)
	}
	defer tty.Close()

	p := tea.NewProgram(
		model,
		tea.WithInput(tty),
		tea.WithOutput(tty),
	)
	_, err = p.Run()
	return err
}

// ReviewTickMsg is sent periodically to update the review display.
type ReviewTickMsg time.Time
