package terminal

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ToolStatus represents the status of a running tool.
type ToolStatus struct {
	Name      string
	Status    string
	Progress  int
	Message   string
	StartTime time.Time
	IsRunning bool
}

// StatusBarModel represents the bottom status bar.
type StatusBarModel struct {
	width    int
	height   int
	mode     string // Current mode (NORMAL, INSERT, etc.)
	branch   string // Git branch
	tools    []ToolStatus
	message  string // Status message
	showHelp bool
	styles   *ComponentStyles
	theme    *Theme
}

// NewStatusBar creates a new status bar model.
func NewStatusBar(theme *Theme) *StatusBarModel {
	return &StatusBarModel{
		height:   1,
		mode:     "NORMAL",
		branch:   "main",
		tools:    []ToolStatus{},
		styles:   theme.CreateComponentStyles(),
		theme:    theme,
		showHelp: true,
	}
}

// Init initializes the status bar.
func (sb *StatusBarModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the status bar.
func (sb *StatusBarModel) Update(msg tea.Msg) (StatusBarModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		sb.width = msg.Width
	}

	return *sb, nil
}

// View renders the status bar with Crush-style keyboard hints.
func (sb *StatusBarModel) View() string {
	if sb.width <= 0 {
		return ""
	}

	// Always show Crush-style keyboard hints at the bottom
	return sb.renderDefaultBar()
}

// renderDefaultBar renders the Crush-style help hints bar.
func (sb *StatusBarModel) renderDefaultBar() string {
	// Crush-style: show keyboard shortcuts at bottom
	// Like: "ctrl+p commands • ctrl+l models • ctrl+j newline • ctrl+c quit • ctrl+g more"

	hints := sb.getCrushStyleHints()
	hintStyle := lipgloss.NewStyle().
		Foreground(sb.theme.TextMuted)

	separator := lipgloss.NewStyle().
		Foreground(sb.theme.TextMuted).
		Render(" • ")

	var parts []string
	for _, hint := range hints {
		parts = append(parts, hintStyle.Render(hint))
	}

	content := strings.Join(parts, separator)

	// Center the content
	contentWidth := lipgloss.Width(content)
	leftPad := max(0, (sb.width-contentWidth)/2)

	return strings.Repeat(" ", leftPad) + content
}

// getCrushStyleHints returns keyboard shortcut hints like Crush.
func (sb *StatusBarModel) getCrushStyleHints() []string {
	return []string{
		"ctrl+p commands",
		"/help for help",
		"ctrl+c quit",
	}
}

// Public methods for external updates

// SetMode sets the current mode.
func (sb *StatusBarModel) SetMode(mode string) {
	sb.mode = mode
}

// SetBranch sets the current git branch.
func (sb *StatusBarModel) SetBranch(branch string) {
	sb.branch = branch
}

// SetMessage sets a status message.
func (sb *StatusBarModel) SetMessage(message string) {
	sb.message = message
}

// AddTool adds a tool status.
func (sb *StatusBarModel) AddTool(name string) string {
	id := fmt.Sprintf("tool-%d", len(sb.tools))
	tool := ToolStatus{
		Name:      name,
		Status:    "Starting",
		Progress:  0,
		StartTime: time.Now(),
		IsRunning: true,
	}
	sb.tools = append(sb.tools, tool)
	return id
}

// UpdateTool updates a tool's status.
func (sb *StatusBarModel) UpdateTool(name string, progress int, message string) {
	for i := range sb.tools {
		if sb.tools[i].Name == name {
			sb.tools[i].Progress = progress
			sb.tools[i].Message = message
			if progress >= 100 {
				sb.tools[i].IsRunning = false
				sb.tools[i].Status = "Completed"
			}
			break
		}
	}
}

// RemoveTool removes a tool from tracking.
func (sb *StatusBarModel) RemoveTool(name string) {
	filtered := []ToolStatus{}
	for _, tool := range sb.tools {
		if tool.Name != name {
			filtered = append(filtered, tool)
		}
	}
	sb.tools = filtered
}

// ClearTools clears all tool statuses.
func (sb *StatusBarModel) ClearTools() {
	sb.tools = []ToolStatus{}
}
