package terminal

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CommentDetailModel displays detailed view of a single comment.
type CommentDetailModel struct {
	comment  *ReviewComment
	viewport viewport.Model
	width    int
	height   int
	focused  bool
	styles   *ReviewStyles
	theme    *Theme
}

// NewCommentDetail creates a new comment detail model.
func NewCommentDetail(theme *Theme) *CommentDetailModel {
	styles := createReviewStyles(theme)

	return &CommentDetailModel{
		viewport: viewport.New(80, 20),
		theme:    theme,
		styles:   styles,
	}
}

// Init initializes the comment detail.
func (cd *CommentDetailModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the comment detail.
func (cd *CommentDetailModel) Update(msg tea.Msg) (*CommentDetailModel, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		cd.width = msg.Width
		cd.height = msg.Height
		cd.viewport.Width = cd.width
		cd.viewport.Height = cd.height
		cd.updateContent()

	case tea.KeyMsg:
		if !cd.focused {
			return cd, nil
		}

		switch msg.String() {
		case "j", "down":
			cd.viewport.LineDown(1)
		case "k", "up":
			cd.viewport.LineUp(1)
		case "ctrl+d":
			cd.viewport.HalfViewDown()
		case "ctrl+u":
			cd.viewport.HalfViewUp()
		case "g":
			cd.viewport.GotoTop()
		case "G":
			cd.viewport.GotoBottom()
		}
	}

	cd.viewport, cmd = cd.viewport.Update(msg)
	return cd, cmd
}

// View renders the comment detail.
func (cd *CommentDetailModel) View() string {
	if cd.comment == nil {
		return cd.renderEmpty()
	}
	return cd.viewport.View()
}

func (cd *CommentDetailModel) renderEmpty() string {
	emptyStyle := lipgloss.NewStyle().
		Foreground(cd.theme.TextMuted).
		Italic(true)

	content := emptyStyle.Render("Select a comment to view details\n\nUse j/k to navigate, Enter to select")

	return lipgloss.Place(cd.width, cd.height, lipgloss.Center, lipgloss.Center, content)
}

func (cd *CommentDetailModel) updateContent() {
	if cd.comment == nil {
		return
	}

	var sections []string

	sections = append(sections, cd.renderHeader())
	sections = append(sections, "")

	sections = append(sections, cd.renderMetadata())
	sections = append(sections, "")

	sections = append(sections, cd.renderContent())
	sections = append(sections, "")

	if cd.comment.CodeBlock != "" {
		sections = append(sections, cd.renderCodeBlock())
		sections = append(sections, "")
	}

	if cd.comment.DiffBlock != "" {
		sections = append(sections, cd.renderDiffBlock())
		sections = append(sections, "")
	}

	if cd.comment.Suggestion != "" {
		sections = append(sections, cd.renderSuggestion())
	}

	cd.viewport.SetContent(strings.Join(sections, "\n"))
}

func (cd *CommentDetailModel) renderHeader() string {
	icon := cd.getSeverityIcon(cd.comment.Severity)
	severityLabel := cd.getSeverityLabel(cd.comment.Severity)

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(cd.theme.TextPrimary)

	return headerStyle.Render(fmt.Sprintf("%s %s", icon, severityLabel))
}

func (cd *CommentDetailModel) renderMetadata() string {
	var parts []string

	fileStyle := lipgloss.NewStyle().
		Foreground(cd.theme.Accent)
	parts = append(parts, fileStyle.Render(fmt.Sprintf("📄 %s", cd.comment.FilePath)))

	lineStyle := lipgloss.NewStyle().
		Foreground(cd.theme.TextSecondary)
	parts = append(parts, lineStyle.Render(fmt.Sprintf("Line: %d", cd.comment.LineNumber)))

	categoryStyle := cd.styles.CategoryBadge
	parts = append(parts, categoryStyle.Render(cd.comment.Category))

	return strings.Join(parts, "  │  ")
}

func (cd *CommentDetailModel) renderContent() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(cd.theme.Accent).
		Bold(true)

	contentStyle := lipgloss.NewStyle().
		Foreground(cd.theme.TextPrimary).
		PaddingLeft(2)

	lines := []string{
		titleStyle.Render("Issue:"),
		"",
	}

	wrappedContent := wrapText(cd.comment.Content, cd.width-4)
	for _, line := range wrappedContent {
		lines = append(lines, contentStyle.Render(line))
	}

	return strings.Join(lines, "\n")
}

func (cd *CommentDetailModel) renderCodeBlock() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(cd.theme.Accent).
		Bold(true)

	lines := []string{
		titleStyle.Render("Code Context:"),
		"",
	}

	codeLines := strings.Split(cd.comment.CodeBlock, "\n")
	for i, line := range codeLines {
		lineNum := lipgloss.NewStyle().
			Foreground(cd.theme.TextMuted).
			Width(4).
			Align(lipgloss.Right).
			Render(fmt.Sprintf("%d", i+1))

		codeLine := cd.styles.CodeBlock.Render(line)
		lines = append(lines, fmt.Sprintf("%s │ %s", lineNum, codeLine))
	}

	return strings.Join(lines, "\n")
}

func (cd *CommentDetailModel) renderDiffBlock() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(cd.theme.Accent).
		Bold(true)

	lines := []string{
		titleStyle.Render("Changes:"),
		"",
	}

	diffLines := strings.Split(cd.comment.DiffBlock, "\n")
	for _, line := range diffLines {
		var styledLine string
		if strings.HasPrefix(line, "+") {
			styledLine = cd.styles.DiffAdded.Render(line)
		} else if strings.HasPrefix(line, "-") {
			styledLine = cd.styles.DiffRemoved.Render(line)
		} else if strings.HasPrefix(line, "@@") {
			styledLine = lipgloss.NewStyle().
				Foreground(cd.theme.Accent).
				Render(line)
		} else {
			styledLine = cd.styles.DiffContext.Render(line)
		}
		lines = append(lines, "  "+styledLine)
	}

	return strings.Join(lines, "\n")
}

func (cd *CommentDetailModel) renderSuggestion() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7FE9DE")).
		Bold(true)

	suggestionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7FE9DE")).
		Italic(true).
		PaddingLeft(2)

	lines := []string{
		titleStyle.Render("💡 Suggestion:"),
		"",
	}

	wrappedSuggestion := wrapText(cd.comment.Suggestion, cd.width-6)
	for _, line := range wrappedSuggestion {
		lines = append(lines, suggestionStyle.Render(line))
	}

	return strings.Join(lines, "\n")
}

func (cd *CommentDetailModel) getSeverityIcon(severity string) string {
	switch severity {
	case "critical":
		return cd.styles.SeverityCritical.Render("●")
	case "warning":
		return cd.styles.SeverityWarning.Render("●")
	case "suggestion":
		return cd.styles.SeveritySuggestion.Render("●")
	default:
		return "○"
	}
}

func (cd *CommentDetailModel) getSeverityLabel(severity string) string {
	switch severity {
	case "critical":
		return cd.styles.SeverityCritical.Render("CRITICAL")
	case "warning":
		return cd.styles.SeverityWarning.Render("WARNING")
	case "suggestion":
		return cd.styles.SeveritySuggestion.Render("SUGGESTION")
	default:
		return severity
	}
}

// SetComment sets the comment to display.
func (cd *CommentDetailModel) SetComment(comment *ReviewComment) {
	cd.comment = comment
	cd.viewport.GotoTop()
	cd.updateContent()
}

// SetFocused sets the focus state.
func (cd *CommentDetailModel) SetFocused(focused bool) {
	cd.focused = focused
}

// SetSize sets the component size.
func (cd *CommentDetailModel) SetSize(width, height int) {
	cd.width = width
	cd.height = height
	cd.viewport.Width = width
	cd.viewport.Height = height
	cd.updateContent()
}

// HasComment returns whether a comment is set.
func (cd *CommentDetailModel) HasComment() bool {
	return cd.comment != nil
}

// Clear clears the current comment.
func (cd *CommentDetailModel) Clear() {
	cd.comment = nil
}
