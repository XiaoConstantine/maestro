package terminal

import "time"

// Message is a conversation entry rendered by the primary TUI.
type Message struct {
	Role      string
	Content   string
	Timestamp time.Time
}
