package terminal

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CommentListModel is a scrollable list of review comments.
type CommentListModel struct {
	comments     []ReviewComment
	fileGroups   []FileGroup
	selected     int
	cursor       int
	scrollOffset int
	width        int
	height       int
	focused      bool
	viewport     viewport.Model
	styles       *ReviewStyles
	theme        *Theme

	onSelect func(comment ReviewComment)
}

// NewCommentList creates a new comment list model.
func NewCommentList(comments []ReviewComment, theme *Theme) *CommentListModel {
	styles := createReviewStyles(theme)

	cl := &CommentListModel{
		comments:   comments,
		fileGroups: groupCommentsByFile(comments),
		selected:   0,
		cursor:     0,
		theme:      theme,
		styles:     styles,
		viewport:   viewport.New(80, 20),
	}

	cl.updateContent()
	return cl
}

// Init initializes the comment list.
func (cl *CommentListModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the comment list.
func (cl *CommentListModel) Update(msg tea.Msg) (*CommentListModel, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		cl.width = msg.Width
		cl.height = msg.Height
		cl.viewport.Width = cl.width
		cl.viewport.Height = cl.height
		cl.updateContent()

	case tea.KeyMsg:
		if !cl.focused {
			return cl, nil
		}

		switch msg.String() {
		case "j", "down":
			cl.moveDown()
		case "k", "up":
			cl.moveUp()
		case "enter":
			if cl.onSelect != nil && cl.cursor < len(cl.comments) {
				cl.onSelect(cl.comments[cl.cursor])
			}
		case "g":
			cl.cursor = 0
			cl.updateScroll()
		case "G":
			cl.cursor = len(cl.comments) - 1
			cl.updateScroll()
		case "ctrl+d":
			cl.pageDown()
		case "ctrl+u":
			cl.pageUp()
		}

		cl.updateContent()
	}

	cl.viewport, cmd = cl.viewport.Update(msg)
	return cl, cmd
}

// View renders the comment list.
func (cl *CommentListModel) View() string {
	return cl.viewport.View()
}

func (cl *CommentListModel) moveDown() {
	if cl.cursor < len(cl.comments)-1 {
		cl.cursor++
		cl.updateScroll()
	}
}

func (cl *CommentListModel) moveUp() {
	if cl.cursor > 0 {
		cl.cursor--
		cl.updateScroll()
	}
}

func (cl *CommentListModel) pageDown() {
	cl.cursor = min(cl.cursor+10, len(cl.comments)-1)
	cl.updateScroll()
}

func (cl *CommentListModel) pageUp() {
	cl.cursor = max(cl.cursor-10, 0)
	cl.updateScroll()
}

func (cl *CommentListModel) updateScroll() {
	if cl.cursor < cl.scrollOffset {
		cl.scrollOffset = cl.cursor
	} else if cl.cursor >= cl.scrollOffset+cl.height {
		cl.scrollOffset = cl.cursor - cl.height + 1
	}
}

func (cl *CommentListModel) updateContent() {
	var lines []string
	currentFile := ""

	for i, comment := range cl.comments {
		if comment.FilePath != currentFile {
			if currentFile != "" {
				lines = append(lines, "")
			}
			currentFile = comment.FilePath

			fileStyle := cl.styles.FileHeader
			fileName := truncateString(comment.FilePath, cl.width-4)
			lines = append(lines, fileStyle.Render(fmt.Sprintf("📄 %s", fileName)))
		}

		icon := cl.getSeverityIcon(comment.Severity)
		lineInfo := cl.styles.LineNumber.Render(fmt.Sprintf("L%-4d", comment.LineNumber))
		category := cl.styles.CategoryBadge.Render(fmt.Sprintf("[%s]", comment.Category))

		preview := truncateString(comment.Content, cl.width-30)

		style := cl.styles.CommentItem
		if i == cl.cursor {
			style = cl.styles.CommentActive
		}

		line := style.Render(fmt.Sprintf("  %s %s %s %s", icon, lineInfo, category, preview))
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(cl.theme.TextMuted).
			Italic(true).
			Render("  No comments to display"))
	}

	cl.viewport.SetContent(strings.Join(lines, "\n"))
}

func (cl *CommentListModel) getSeverityIcon(severity string) string {
	switch severity {
	case "critical":
		return cl.styles.SeverityCritical.Render("●")
	case "warning":
		return cl.styles.SeverityWarning.Render("●")
	case "suggestion":
		return cl.styles.SeveritySuggestion.Render("●")
	default:
		return "○"
	}
}

// SetFocused sets the focus state.
func (cl *CommentListModel) SetFocused(focused bool) {
	cl.focused = focused
}

// SetOnSelect sets the selection callback.
func (cl *CommentListModel) SetOnSelect(fn func(comment ReviewComment)) {
	cl.onSelect = fn
}

// GetSelectedComment returns the currently selected comment.
func (cl *CommentListModel) GetSelectedComment() *ReviewComment {
	if cl.cursor >= 0 && cl.cursor < len(cl.comments) {
		return &cl.comments[cl.cursor]
	}
	return nil
}

// SetComments updates the comment list.
func (cl *CommentListModel) SetComments(comments []ReviewComment) {
	cl.comments = comments
	cl.fileGroups = groupCommentsByFile(comments)
	cl.cursor = 0
	cl.scrollOffset = 0
	cl.updateContent()
}

// FilterBySeverity filters comments by severity.
func (cl *CommentListModel) FilterBySeverity(severity string) {
	if severity == "" {
		cl.updateContent()
		return
	}

	var filtered []ReviewComment
	for _, c := range cl.comments {
		if c.Severity == severity {
			filtered = append(filtered, c)
		}
	}
	cl.comments = filtered
	cl.fileGroups = groupCommentsByFile(filtered)
	cl.cursor = 0
	cl.scrollOffset = 0
	cl.updateContent()
}

// GetCommentCount returns the number of comments.
func (cl *CommentListModel) GetCommentCount() int {
	return len(cl.comments)
}

// GetSeverityCounts returns counts by severity.
func (cl *CommentListModel) GetSeverityCounts() map[string]int {
	counts := map[string]int{"critical": 0, "warning": 0, "suggestion": 0}
	for _, c := range cl.comments {
		counts[c.Severity]++
	}
	return counts
}
