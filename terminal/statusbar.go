package terminal

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// View renders the status bar.
func (sb *StatusBarModel) View() string {
	if sb.width <= 0 {
		return ""
	}

	// Build status bar segments

	// Left side: Mode and branch
	leftSegments := []string{}

	// Mode indicator
	modeStyle := sb.getModeStyle()
	modeIndicator := modeStyle.Render(fmt.Sprintf(" %s ", sb.mode))
	leftSegments = append(leftSegments, modeIndicator)

	// Git branch
	if sb.branch != "" {
		branchIndicator := sb.styles.StatusValue.Render(fmt.Sprintf(" ⎇ %s ", sb.branch))
		leftSegments = append(leftSegments, branchIndicator)
	}

	// Center: Tool status or message
	centerContent := ""
	if len(sb.tools) > 0 {
		// Show running tools
		runningTools := sb.getRunningTools()
		if len(runningTools) > 0 {
			tool := runningTools[0] // Show first running tool
			centerContent = sb.renderToolStatus(tool)
		}
	} else if sb.message != "" {
		centerContent = sb.styles.StatusText.Render(sb.message)
	}

	// Right side: Help hints
	rightSegments := []string{}
	if sb.showHelp {
		helpHints := sb.getHelpHints()
		rightSegments = append(rightSegments, sb.styles.StatusKey.Render(helpHints))
	}

	// Calculate spacing
	left := strings.Join(leftSegments, " ")
	right := strings.Join(rightSegments, " ")

	leftLen := lipgloss.Width(left)
	rightLen := lipgloss.Width(right)
	centerLen := lipgloss.Width(centerContent)

	availableSpace := sb.width - leftLen - rightLen - 4 // Account for spacing

	// Build the final status bar
	var statusLine string

	if availableSpace >= centerLen {
		// Center the middle content
		leftPadding := (availableSpace - centerLen) / 2
		rightPadding := availableSpace - centerLen - leftPadding

		statusLine = fmt.Sprintf("%s%s%s%s%s",
			left,
			strings.Repeat(" ", leftPadding),
			centerContent,
			strings.Repeat(" ", rightPadding),
			right,
		)
	} else {
		// Just space evenly
		spacing := max(1, (sb.width-leftLen-centerLen-rightLen)/3)
		statusLine = fmt.Sprintf("%s%s%s%s%s",
			left,
			strings.Repeat(" ", spacing),
			centerContent,
			strings.Repeat(" ", spacing),
			right,
		)
	}

	// Ensure it fits the width
	if lipgloss.Width(statusLine) > sb.width {
		statusLine = statusLine[:sb.width-3] + "..."
	} else if lipgloss.Width(statusLine) < sb.width {
		statusLine += strings.Repeat(" ", sb.width-lipgloss.Width(statusLine))
	}

	return sb.styles.StatusBar.
		Width(sb.width).
		Render(statusLine)
}

// getModeStyle returns the appropriate style for the current mode.
func (sb *StatusBarModel) getModeStyle() lipgloss.Style {
	baseStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)

	switch sb.mode {
	case "NORMAL":
		return baseStyle.
			Background(sb.theme.Accent).
			Foreground(sb.theme.Background)
	case "INSERT":
		return baseStyle.
			Background(lipgloss.Color("#7FE9DE")).
			Foreground(sb.theme.Background)
	case "VISUAL":
		return baseStyle.
			Background(lipgloss.Color("#B185F7")).
			Foreground(sb.theme.Background)
	case "COMMAND":
		return baseStyle.
			Background(lipgloss.Color("#FFA500")).
			Foreground(sb.theme.Background)
	default:
		return baseStyle.
			Background(sb.theme.TextMuted).
			Foreground(sb.theme.Background)
	}
}

// renderToolStatus renders a tool status with progress.
func (sb *StatusBarModel) renderToolStatus(tool ToolStatus) string {
	// Icon based on status
	icon := "◉"
	if !tool.IsRunning {
		icon = "✓"
	}

	// Progress bar
	progressBar := ""
	if tool.Progress > 0 {
		progressBar = sb.renderProgressBar(tool.Progress)
	}

	// Elapsed time
	elapsed := ""
	if !tool.StartTime.IsZero() {
		duration := time.Since(tool.StartTime)
		elapsed = fmt.Sprintf("[%s]", formatDuration(duration))
	}

	// Build status string
	parts := []string{
		sb.styles.StatusValue.Render(icon),
		sb.styles.StatusKey.Render(tool.Name),
	}

	if tool.Message != "" {
		parts = append(parts, sb.styles.StatusText.Render(tool.Message))
	}

	if progressBar != "" {
		parts = append(parts, progressBar)
	}

	if elapsed != "" {
		parts = append(parts, sb.styles.StatusText.Render(elapsed))
	}

	return strings.Join(parts, " ")
}

// renderProgressBar renders a progress bar.
func (sb *StatusBarModel) renderProgressBar(percent int) string {
	width := 20
	filled := (percent * width) / 100

	fillChar := "█"
	emptyChar := "░"

	// Apply styles
	filledPart := sb.styles.ProgressFill.Render(strings.Repeat(fillChar, filled))
	emptyPart := sb.styles.StatusText.Render(strings.Repeat(emptyChar, width-filled))

	return fmt.Sprintf("%s%s %d%%", filledPart, emptyPart, percent)
}

// getHelpHints returns contextual help hints.
func (sb *StatusBarModel) getHelpHints() string {
	hints := []string{}

	switch sb.mode {
	case "NORMAL":
		hints = []string{
			"Tab: Switch Panel",
			"/: Search",
			"?: Help",
			"Ctrl+C: Exit",
		}
	case "INSERT":
		hints = []string{
			"Esc: Normal Mode",
			"Ctrl+C: Cancel",
		}
	case "VISUAL":
		hints = []string{
			"y: Yank",
			"d: Delete",
			"Esc: Normal Mode",
		}
	case "COMMAND":
		hints = []string{
			"Enter: Execute",
			"Esc: Cancel",
		}
	}

	// Return first few hints that fit
	result := []string{}
	totalWidth := 0
	maxWidth := 60 // Maximum width for hints

	for _, hint := range hints {
		hintWidth := len(hint) + 4 // Account for spacing
		if totalWidth+hintWidth <= maxWidth {
			result = append(result, hint)
			totalWidth += hintWidth
		} else {
			break
		}
	}

	return strings.Join(result, "  ")
}

// getRunningTools returns list of currently running tools.
func (sb *StatusBarModel) getRunningTools() []ToolStatus {
	running := []ToolStatus{}
	for _, tool := range sb.tools {
		if tool.IsRunning {
			running = append(running, tool)
		}
	}
	return running
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

// Helper functions

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	} else if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
