package terminal

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TodoStatus represents the status of a TODO item.
type TodoStatus int

const (
	TodoPending TodoStatus = iota
	TodoInProgress
	TodoCompleted
)

// TodoItem represents a single TODO entry.
type TodoItem struct {
	ID         string
	Content    string
	ActiveForm string // Alternative display text when in progress
	Status     TodoStatus
	Progress   int // Progress percentage for in-progress items
}

// TodoListModel represents the TODO panel component.
type TodoListModel struct {
	items        []TodoItem
	selected     int
	scrollOffset int
	width        int
	height       int
	focused      bool
	styles       *ComponentStyles
	theme        *Theme
	title        string
}

// NewTodoList creates a new TODO list model.
func NewTodoList(theme *Theme) *TodoListModel {
	return &TodoListModel{
		items:    []TodoItem{},
		selected: 0,
		theme:    theme,
		styles:   theme.CreateComponentStyles(),
		title:    "TODOs",
	}
}

// Init initializes the TODO list.
func (tl *TodoListModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the TODO list.
func (tl *TodoListModel) Update(msg tea.Msg) (TodoListModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		tl.width = msg.Width / 3   // TODO panel takes 1/3 of width
		tl.height = msg.Height - 3 // Account for borders and status

	case tea.KeyMsg:
		if tl.focused {
			switch msg.String() {
			case "j", "down":
				tl.moveDown()
			case "k", "up":
				tl.moveUp()
			case "g":
				tl.selected = 0
				tl.scrollOffset = 0
			case "G":
				tl.selected = len(tl.items) - 1
				tl.updateScroll()
			case "space", "enter":
				tl.toggleStatus()
			case "x":
				tl.markCompleted()
			case "r":
				tl.resetStatus()
			}
		}
	}

	return *tl, nil
}

// View renders the TODO list.
func (tl *TodoListModel) View() string {
	if tl.height <= 0 || tl.width <= 0 {
		return ""
	}

	var lines []string

	// Title bar - more subtle for agent thinking
	title := tl.styles.ToolHeader.Render("TODOs")
	lines = append(lines, title)

	// Calculate visible area (accounting for title)
	contentHeight := tl.height - 1

	// Render visible items
	visibleStart := tl.scrollOffset
	visibleEnd := min(visibleStart+contentHeight, len(tl.items))

	if len(tl.items) == 0 {
		emptyMsg := tl.styles.TodoItem.Foreground(tl.theme.TextMuted).
			Render("• Waiting for agent thoughts...")
		lines = append(lines, emptyMsg)
	} else {
		for i := visibleStart; i < visibleEnd; i++ {
			line := tl.renderTodoItem(tl.items[i], i == tl.selected)
			lines = append(lines, line)
		}
	}

	// Pad remaining space
	for len(lines) < tl.height {
		lines = append(lines, strings.Repeat(" ", tl.width-4))
	}

	content := strings.Join(lines, "\n")

	// Apply container style
	containerStyle := tl.styles.TodoPanel
	if tl.focused {
		containerStyle = containerStyle.BorderForeground(tl.theme.Accent)
	}

	return containerStyle.
		Width(tl.width).
		Height(tl.height).
		Render(content)
}

// renderTodoItem renders a single TODO item.
func (tl *TodoListModel) renderTodoItem(item TodoItem, selected bool) string {
	var checkbox string
	var style lipgloss.Style
	var content string

	switch item.Status {
	case TodoPending:
		checkbox = "☐"
		style = tl.styles.TodoPending
		content = item.Content

	case TodoInProgress:
		checkbox = "◉"
		style = tl.styles.TodoActive
		// Use ActiveForm if available, otherwise use Content
		if item.ActiveForm != "" {
			content = item.ActiveForm
		} else {
			content = item.Content
		}
		// Add progress indicator if available
		if item.Progress > 0 {
			progressBar := tl.renderMiniProgress(item.Progress)
			content = fmt.Sprintf("%s %s", content, progressBar)
		}

	case TodoCompleted:
		checkbox = "✓"
		style = tl.styles.TodoCompleted
		content = item.Content
	}

	// Apply selection highlight
	if selected {
		style = style.Background(lipgloss.Color("#3A3C55"))
	}

	// Format the line
	checkboxStyled := tl.styles.TodoCheckbox.Render(checkbox)
	line := fmt.Sprintf(" %s %s", checkboxStyled, content)

	// Truncate if too long
	maxWidth := tl.width - 6
	if len(line) > maxWidth {
		line = line[:maxWidth-3] + "..."
	}

	// Pad to width
	line = line + strings.Repeat(" ", max(0, maxWidth-len(line)))

	return style.Render(line)
}

// renderMiniProgress renders a small progress bar.
func (tl *TodoListModel) renderMiniProgress(percent int) string {
	width := 10
	filled := (percent * width) / 100

	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("[%s] %d%%", bar, percent)
}

// Navigation methods.
func (tl *TodoListModel) moveDown() {
	if tl.selected < len(tl.items)-1 {
		tl.selected++
		tl.updateScroll()
	}
}

func (tl *TodoListModel) moveUp() {
	if tl.selected > 0 {
		tl.selected--
		tl.updateScroll()
	}
}

func (tl *TodoListModel) updateScroll() {
	contentHeight := tl.height - 2 // Account for title

	if tl.selected < tl.scrollOffset {
		tl.scrollOffset = tl.selected
	} else if tl.selected >= tl.scrollOffset+contentHeight {
		tl.scrollOffset = tl.selected - contentHeight + 1
	}
}

// Status management methods.
func (tl *TodoListModel) toggleStatus() {
	if tl.selected >= 0 && tl.selected < len(tl.items) {
		item := &tl.items[tl.selected]
		switch item.Status {
		case TodoPending:
			item.Status = TodoInProgress
			item.Progress = 0
		case TodoInProgress:
			item.Status = TodoCompleted
			item.Progress = 100
		case TodoCompleted:
			item.Status = TodoPending
			item.Progress = 0
		}
	}
}

func (tl *TodoListModel) markCompleted() {
	if tl.selected >= 0 && tl.selected < len(tl.items) {
		tl.items[tl.selected].Status = TodoCompleted
		tl.items[tl.selected].Progress = 100
	}
}

func (tl *TodoListModel) resetStatus() {
	if tl.selected >= 0 && tl.selected < len(tl.items) {
		tl.items[tl.selected].Status = TodoPending
		tl.items[tl.selected].Progress = 0
	}
}

// Public methods for external updates

// AddTodo adds a new TODO item.
func (tl *TodoListModel) AddTodo(content string, activeForm string) {
	item := TodoItem{
		ID:         fmt.Sprintf("todo-%d", len(tl.items)+1),
		Content:    content,
		ActiveForm: activeForm,
		Status:     TodoPending,
		Progress:   0,
	}
	tl.items = append(tl.items, item)
}

// UpdateTodo updates an existing TODO item.
func (tl *TodoListModel) UpdateTodo(id string, status TodoStatus, progress int) {
	for i := range tl.items {
		if tl.items[i].ID == id {
			tl.items[i].Status = status
			tl.items[i].Progress = progress
			break
		}
	}
}

// SetTodos replaces the entire TODO list.
func (tl *TodoListModel) SetTodos(items []TodoItem) {
	tl.items = items
	if tl.selected >= len(tl.items) {
		tl.selected = max(0, len(tl.items)-1)
	}
}

// GetSelectedTodo returns the currently selected TODO item.
func (tl *TodoListModel) GetSelectedTodo() *TodoItem {
	if tl.selected >= 0 && tl.selected < len(tl.items) {
		return &tl.items[tl.selected]
	}
	return nil
}

// SetFocused sets the focus state.
func (tl *TodoListModel) SetFocused(focused bool) {
	tl.focused = focused
}

// GetItemCount returns the number of TODO items.
func (tl *TodoListModel) GetItemCount() int {
	return len(tl.items)
}

// GetStatusCounts returns counts for each status.
func (tl *TodoListModel) GetStatusCounts() (pending, inProgress, completed int) {
	for _, item := range tl.items {
		switch item.Status {
		case TodoPending:
			pending++
		case TodoInProgress:
			inProgress++
		case TodoCompleted:
			completed++
		}
	}
	return
}
