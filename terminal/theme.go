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
		// Backgrounds - navy/purple tones matching the screenshot
		Background: lipgloss.Color("#1F2041"), // Deep navy background
		Surface:    lipgloss.Color("#2B2D42"), // Slightly lighter purple-navy

		// Text - clean hierarchy
		TextPrimary:   lipgloss.Color("#E8E9F3"), // Clean off-white
		TextSecondary: lipgloss.Color("#A9A9B8"), // Muted gray-purple
		TextMuted:     lipgloss.Color("#6B6B7E"), // Very muted purple-gray

		// Functional - used for accents and highlights
		Accent: lipgloss.Color("#00D9FF"), // Cyan accent for active elements
		Border: lipgloss.Color("#3A3C55"), // Subtle purple borders
		Cursor: lipgloss.Color("#00D9FF"), // Cyan cursor

		// Code syntax - purple/cyan palette
		Code:    lipgloss.Color("#D4D4E5"), // Light purple-gray for code
		Comment: lipgloss.Color("#6B6B7E"), // Same as muted text
		String:  lipgloss.Color("#7FE9DE"), // Cyan-green for strings
		Keyword: lipgloss.Color("#B185F7"), // Light purple for keywords
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
