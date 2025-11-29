package terminal

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Theme defines the minimal color scheme for Claude Code-inspired design.
type Theme struct {
	// Background colors - deep, muted tones
	Background color.Color // Deep dark background
	Surface    color.Color // Slightly lighter for subtle separation

	// Text colors - clean hierarchy
	TextPrimary   color.Color // Main text - clean white
	TextSecondary color.Color // Secondary text - muted gray
	TextMuted     color.Color // Hints and metadata

	// Functional colors - used sparingly
	Accent    color.Color // Primary accent (cyan)
	Secondary color.Color // Secondary accent (purple/magenta)
	Border    color.Color // Subtle borders
	Cursor    color.Color // Terminal cursor

	// Logo colors - Crush-style purple/magenta gradient
	LogoPrimary   color.Color // Purple (Charple) - left side of gradient
	LogoSecondary color.Color // Magenta (Dolly) - right side of gradient
	LogoField     color.Color // Diagonal field lines color

	// Status bar colors
	StatusHighlight color.Color // Mint green for highlighted status bar

	// Syntax colors - for code only
	Code    color.Color // Code text
	Comment color.Color // Code comments
	String  color.Color // String literals
	Keyword color.Color // Keywords
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
		Accent:    lipgloss.Color("#00D9FF"), // Cyan accent for active elements
		Secondary: lipgloss.Color("#B185F7"), // Purple/magenta secondary accent
		Border:    lipgloss.Color("#3A3C55"), // Subtle purple borders
		Cursor:    lipgloss.Color("#00D9FF"), // Cyan cursor

		// Logo colors - Maestro coral/salmon orange
		LogoPrimary:   lipgloss.Color("#E8985A"), // Coral/salmon orange
		LogoSecondary: lipgloss.Color("#E8985A"), // Same color (solid, not gradient)
		LogoField:     lipgloss.Color("#E8985A"), // Coral for diagonal lines

		// Status bar colors
		StatusHighlight: lipgloss.Color("#00FFB2"), // Julep - mint green

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
