package terminal

import "time"

// Message is a conversation entry rendered by the primary TUI.
type Message struct {
	Role            string
	Content         string
	Rendered        string
	WidthBucket     int
	renderedContent string
	renderCount     uint64
	Timestamp       time.Time
}
