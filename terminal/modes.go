package terminal

// MaestroMode represents the active top-level interaction state.
type MaestroMode int

const (
	// ModeInput is the default coding and command state.
	ModeInput MaestroMode = 0
	// ModeSessionPicker selects a persisted coding session. Its historical value
	// is retained for compatibility with any persisted or external state.
	ModeSessionPicker MaestroMode = 3
)

// InputFocus identifies the active region within the primary surface.
type InputFocus int

const (
	// FocusInput means the composer has focus.
	FocusInput InputFocus = iota
	// FocusReviewList means inline review results have focus.
	FocusReviewList
	// FocusToolActivity means the current run's tool transcript has focus.
	FocusToolActivity
)

// String returns a human-readable mode name.
func (m MaestroMode) String() string {
	switch m {
	case ModeInput:
		return "INPUT"
	case ModeSessionPicker:
		return "SESSION"
	default:
		return "UNKNOWN"
	}
}
