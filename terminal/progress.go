package terminal

import (
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ProgressState represents the current progress state.
type ProgressState int

const (
	ProgressIdle ProgressState = iota
	ProgressWorking
	ProgressComplete
	ProgressError
)

// ProgressModel displays agent progress with Crush-style animation.
type ProgressModel struct {
	width    int
	theme    *Theme
	state    ProgressState
	message  string
	detail   string
	elapsed  time.Duration
	start    time.Time
	frame    int
	id       int
	visible  bool
	spinning bool
}

// ProgressTickMsg triggers animation updates.
type ProgressTickMsg struct{ id int }

var progressID int64

func nextProgressID() int {
	return int(atomic.AddInt64(&progressID, 1))
}

// NewProgressModel creates a new progress display.
func NewProgressModel(theme *Theme) *ProgressModel {
	return &ProgressModel{
		theme: theme,
		id:    nextProgressID(),
	}
}

// Init initializes the progress model.
func (p *ProgressModel) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (p *ProgressModel) Update(msg tea.Msg) (*ProgressModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width

	case ProgressTickMsg:
		if msg.id != p.id {
			return p, nil
		}
		if p.spinning {
			p.frame++
			p.elapsed = time.Since(p.start)
			return p, p.tick()
		}
	}
	return p, nil
}

// View renders the progress display.
func (p *ProgressModel) View() string {
	if !p.visible || p.state == ProgressIdle {
		return ""
	}

	var content string

	switch p.state {
	case ProgressWorking:
		content = p.renderWorking()
	case ProgressComplete:
		content = p.renderComplete()
	case ProgressError:
		content = p.renderError()
	}

	// Container style with padding
	container := lipgloss.NewStyle().
		Width(p.width).
		Padding(0, 2)

	return container.Render(content)
}

// renderWorking renders the working state with animation.
func (p *ProgressModel) renderWorking() string {
	// Crush-style animated spinner using cycling characters
	spinnerChars := []rune("⣾⣽⣻⢿⡿⣟⣯⣷")
	spinnerFrame := spinnerChars[p.frame%len(spinnerChars)]

	// Coral/orange color for spinner (matching logo)
	spinnerStyle := lipgloss.NewStyle().
		Foreground(p.theme.StatusHighlight)

	// Message style
	msgStyle := lipgloss.NewStyle().
		Foreground(p.theme.TextSecondary)

	// Elapsed time
	elapsedStr := formatElapsed(p.elapsed)
	elapsedStyle := lipgloss.NewStyle().
		Foreground(p.theme.TextMuted)

	parts := []string{
		spinnerStyle.Render(string(spinnerFrame)),
		" ",
		msgStyle.Render(p.message),
	}

	if p.elapsed >= time.Second {
		parts = append(parts, " ", elapsedStyle.Render("["+elapsedStr+"]"))
	}

	line := strings.Join(parts, "")

	// Add detail on second line if present
	if p.detail != "" {
		detailStyle := lipgloss.NewStyle().
			Foreground(p.theme.TextMuted).
			PaddingLeft(2)
		line = lipgloss.JoinVertical(lipgloss.Left,
			line,
			detailStyle.Render(p.detail),
		)
	}

	return line
}

// renderComplete renders the complete state.
func (p *ProgressModel) renderComplete() string {
	icon := lipgloss.NewStyle().
		Foreground(p.theme.StatusHighlight).
		Render("✓")

	msg := lipgloss.NewStyle().
		Foreground(p.theme.TextSecondary).
		Render(p.message)

	return icon + " " + msg
}

// renderError renders the error state.
func (p *ProgressModel) renderError() string {
	icon := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B6B")).
		Render("✗")

	msg := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B6B")).
		Render(p.message)

	return icon + " " + msg
}

// tick returns a command to trigger the next animation frame.
func (p *ProgressModel) tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return ProgressTickMsg{id: p.id}
	})
}

// Public methods for controlling progress

// Start begins showing progress with the given message.
func (p *ProgressModel) Start(message string) tea.Cmd {
	p.state = ProgressWorking
	p.message = message
	p.detail = ""
	p.start = time.Now()
	p.elapsed = 0
	p.frame = 0
	p.visible = true
	p.spinning = true
	return p.tick()
}

// SetDetail updates the detail message shown below the main message.
func (p *ProgressModel) SetDetail(detail string) {
	p.detail = detail
}

// SetMessage updates the main progress message.
func (p *ProgressModel) SetMessage(message string) {
	p.message = message
}

// Complete marks the progress as complete.
func (p *ProgressModel) Complete(message string) {
	p.state = ProgressComplete
	p.message = message
	p.detail = ""
	p.spinning = false
}

// Error marks the progress as failed.
func (p *ProgressModel) Error(message string) {
	p.state = ProgressError
	p.message = message
	p.detail = ""
	p.spinning = false
}

// Hide hides the progress display.
func (p *ProgressModel) Hide() {
	p.visible = false
	p.spinning = false
	p.state = ProgressIdle
}

// IsVisible returns true if progress is currently visible.
func (p *ProgressModel) IsVisible() bool {
	return p.visible
}

// SetWidth sets the width for rendering.
func (p *ProgressModel) SetWidth(width int) {
	p.width = width
}

// Working placeholder messages (Crush-style).
var workingMessages = []string{
	"Working...",
	"Thinking...",
	"Processing...",
	"Analyzing...",
}

// GetRandomWorkingMessage returns a random working placeholder.
func GetRandomWorkingMessage() string {
	return workingMessages[rand.Intn(len(workingMessages))]
}

// formatElapsed formats duration for display.
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return "0s"
	} else if d < time.Minute {
		return d.Truncate(time.Second).String()
	} else if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s > 0 {
			return (time.Duration(m)*time.Minute + time.Duration(s)*time.Second).String()
		}
		return (time.Duration(m) * time.Minute).String()
	}
	return d.Truncate(time.Minute).String()
}
