package terminal

import (
	"github.com/charmbracelet/lipgloss"
)

// Theme defines the minimal color scheme for Claude Code-inspired design.
type Theme struct {
	// Background colors - deep, muted tones
	Background lipgloss.Color // Deep dark background
	Surface    lipgloss.Color // Slightly lighter for subtle separation

	// Text colors - clean hierarchy
	TextPrimary   lipgloss.Color // Main text - clean white
	TextSecondary lipgloss.Color // Secondary text - muted gray
	TextMuted     lipgloss.Color // Hints and metadata

	// Functional colors - used sparingly
	Accent lipgloss.Color // Minimal blue accent
	Border lipgloss.Color // Subtle borders
	Cursor lipgloss.Color // Terminal cursor

	// Syntax colors - for code only
	Code    lipgloss.Color // Code text
	Comment lipgloss.Color // Code comments
	String  lipgloss.Color // String literals
	Keyword lipgloss.Color // Keywords
}

// ClaudeCodeTheme returns the minimal Claude Code-inspired theme.
func ClaudeCodeTheme() *Theme {
	return &Theme{
		// Backgrounds - very subtle, no contrast
		Background: lipgloss.Color("#0f0f11"), // Deep muted dark
		Surface:    lipgloss.Color("#1a1a1c"), // Barely lighter

		// Text - clean hierarchy
		TextPrimary:   lipgloss.Color("#fafafa"), // Clean off-white
		TextSecondary: lipgloss.Color("#a1a1aa"), // Muted gray
		TextMuted:     lipgloss.Color("#71717a"), // Very muted

		// Functional - minimal use
		Accent: lipgloss.Color("#3b82f6"), // Subtle blue, used rarely
		Border: lipgloss.Color("#27272a"), // Very subtle borders
		Cursor: lipgloss.Color("#fafafa"), // Same as primary text

		// Code syntax - subtle differentiation
		Code:    lipgloss.Color("#e4e4e7"), // Slightly different from text
		Comment: lipgloss.Color("#71717a"), // Same as muted text
		String:  lipgloss.Color("#84cc16"), // Subtle green
		Keyword: lipgloss.Color("#8b5cf6"), // Subtle purple
	}
}

// CreateStyles creates the base styles for the terminal UI.
func (t *Theme) CreateStyles() *Styles {
	return &Styles{
		// Main container
		Container: lipgloss.NewStyle().
			Background(t.Background).
			Foreground(t.TextPrimary),

		// Header - minimal
		Header: lipgloss.NewStyle().
			Foreground(t.TextSecondary).
			PaddingLeft(1).
			PaddingRight(1),

		// Conversation area - no decoration
		Conversation: lipgloss.NewStyle().
			Background(t.Background).
			Foreground(t.TextPrimary).
			Padding(0, 1),

		// User message - minimal prefix
		UserMessage: lipgloss.NewStyle().
			Foreground(t.TextPrimary),

		// Assistant message - minimal prefix
		AssistantMessage: lipgloss.NewStyle().
			Foreground(t.TextPrimary),

		// Role indicators - very subtle
		UserPrefix: lipgloss.NewStyle().
			Foreground(t.TextSecondary).
			Bold(false),

		AssistantPrefix: lipgloss.NewStyle().
			Foreground(t.TextSecondary).
			Bold(false),

		// Input area - terminal style
		InputContainer: lipgloss.NewStyle().
			Background(t.Background).
			Foreground(t.TextPrimary).
			Padding(0, 1),

		InputPrompt: lipgloss.NewStyle().
			Foreground(t.TextSecondary),

		InputText: lipgloss.NewStyle().
			Foreground(t.TextPrimary),

		// Code blocks - subtle highlighting
		CodeBlock: lipgloss.NewStyle().
			Foreground(t.Code).
			Background(t.Surface).
			Padding(1, 2),

		// Status and metadata - very subtle
		Status: lipgloss.NewStyle().
			Foreground(t.TextMuted),

		// File references - subtle accent
		FileRef: lipgloss.NewStyle().
			Foreground(t.Accent).
			Bold(false),

		// Commands - subtle styling
		Command: lipgloss.NewStyle().
			Foreground(t.Accent).
			Bold(false),
	}
}

// Styles holds all the styled components.
type Styles struct {
	Container        lipgloss.Style
	Header           lipgloss.Style
	Conversation     lipgloss.Style
	UserMessage      lipgloss.Style
	AssistantMessage lipgloss.Style
	UserPrefix       lipgloss.Style
	AssistantPrefix  lipgloss.Style
	InputContainer   lipgloss.Style
	InputPrompt      lipgloss.Style
	InputText        lipgloss.Style
	CodeBlock        lipgloss.Style
	Status           lipgloss.Style
	FileRef          lipgloss.Style
	Command          lipgloss.Style
}
