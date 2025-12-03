package terminal

// MaestroMode represents the current UI mode.
type MaestroMode int

const (
	// ModeInput is the default mode for command/question input.
	ModeInput MaestroMode = iota
	// ModeReview is the PR review TUI mode with 3-pane layout.
	ModeReview
	// ModeDashboard is the full IDE-like dashboard mode.
	ModeDashboard
	// ModeSessionPicker is for selecting sessions with arrow keys.
	ModeSessionPicker
)

// InputFocus represents which area has keyboard focus in input mode.
type InputFocus int

const (
	// FocusInput means the input area has focus.
	FocusInput InputFocus = iota
	// FocusReviewList means the review results list has focus.
	FocusReviewList
)

// String returns a human-readable name for the mode.
func (m MaestroMode) String() string {
	switch m {
	case ModeInput:
		return "INPUT"
	case ModeReview:
		return "REVIEW"
	case ModeDashboard:
		return "DASHBOARD"
	case ModeSessionPicker:
		return "SESSION"
	default:
		return "UNKNOWN"
	}
}

// ModeTransition represents a transition between modes.
type ModeTransition struct {
	From MaestroMode
	To   MaestroMode
	Data interface{} // Optional data to pass to the new mode
}

// ReviewModeData contains data passed when entering review mode.
type ReviewModeData struct {
	PRNumber int
	Comments []ReviewComment
	OnPost   func([]ReviewComment) error
}

// DashboardModeData contains data passed when entering dashboard mode.
type DashboardModeData struct {
	RootPath string
}
