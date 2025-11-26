package terminal

import (
	"github.com/charmbracelet/lipgloss"
)

// ComponentStyles holds styled components for the modern UI.
type ComponentStyles struct {
	// File Explorer styles
	FileTree     lipgloss.Style
	FileItem     lipgloss.Style
	FileSelected lipgloss.Style
	FolderIcon   lipgloss.Style
	FileIcon     lipgloss.Style
	TreeBranch   lipgloss.Style

	// TODO Panel styles
	TodoPanel     lipgloss.Style
	TodoItem      lipgloss.Style
	TodoPending   lipgloss.Style
	TodoActive    lipgloss.Style
	TodoCompleted lipgloss.Style
	TodoCheckbox  lipgloss.Style

	// Status Bar styles
	StatusBar    lipgloss.Style
	StatusText   lipgloss.Style
	StatusKey    lipgloss.Style
	StatusValue  lipgloss.Style
	ProgressBar  lipgloss.Style
	ProgressFill lipgloss.Style

	// Split Pane styles
	PaneContainer lipgloss.Style
	PaneBorder    lipgloss.Style
	PaneActive    lipgloss.Style
	PaneInactive  lipgloss.Style

	// Tool Output styles
	ToolHeader    lipgloss.Style
	ToolOutput    lipgloss.Style
	ToolThinking  lipgloss.Style
	ToolStreaming lipgloss.Style
}

// CreateComponentStyles creates styles for all UI components.
func (t *Theme) CreateComponentStyles() *ComponentStyles {
	return &ComponentStyles{
		// File Explorer styles - cleaner, more minimal
		FileTree: lipgloss.NewStyle().
			Background(t.Surface).
			Foreground(t.TextPrimary).
			Padding(0, 1),

		FileItem: lipgloss.NewStyle().
			Foreground(t.TextSecondary).
			PaddingLeft(1),

		FileSelected: lipgloss.NewStyle().
			Background(lipgloss.Color("#3A3C55")).
			Foreground(t.Accent).
			Bold(true),

		FolderIcon: lipgloss.NewStyle().
			Foreground(t.Accent).
			SetString("▶ "),

		FileIcon: lipgloss.NewStyle().
			Foreground(t.TextMuted).
			SetString("  "),

		TreeBranch: lipgloss.NewStyle().
			Foreground(t.Border),

		// TODO Panel styles - cleaner, more minimal
		TodoPanel: lipgloss.NewStyle().
			Background(t.Surface).
			Foreground(t.TextPrimary).
			Padding(0, 1),

		TodoItem: lipgloss.NewStyle().
			Foreground(t.TextSecondary).
			PaddingLeft(1),

		TodoPending: lipgloss.NewStyle().
			Foreground(t.TextMuted),

		TodoActive: lipgloss.NewStyle().
			Foreground(t.Accent).
			Bold(true),

		TodoCompleted: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7FE9DE")).
			Strikethrough(true),

		TodoCheckbox: lipgloss.NewStyle().
			Foreground(t.Accent),

		// Status Bar styles
		StatusBar: lipgloss.NewStyle().
			Background(t.Background).
			Foreground(t.TextSecondary).
			Height(1).
			Padding(0, 1),

		StatusText: lipgloss.NewStyle().
			Foreground(t.TextMuted),

		StatusKey: lipgloss.NewStyle().
			Foreground(t.TextSecondary).
			Bold(true),

		StatusValue: lipgloss.NewStyle().
			Foreground(t.Accent),

		ProgressBar: lipgloss.NewStyle().
			Foreground(t.Border).
			Background(t.Background).
			Width(20),

		ProgressFill: lipgloss.NewStyle().
			Foreground(t.Accent).
			Background(t.Accent),

		// Split Pane styles
		PaneContainer: lipgloss.NewStyle().
			Background(t.Background),

		PaneBorder: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(t.Border),

		PaneActive: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(t.Accent),

		PaneInactive: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(t.Border),

		// Tool Output styles
		ToolHeader: lipgloss.NewStyle().
			Foreground(t.Accent).
			Bold(true).
			PaddingLeft(2),

		ToolOutput: lipgloss.NewStyle().
			Foreground(t.TextPrimary).
			PaddingLeft(4),

		ToolThinking: lipgloss.NewStyle().
			Foreground(t.TextMuted).
			Italic(true).
			PaddingLeft(4),

		ToolStreaming: lipgloss.NewStyle().
			Foreground(t.TextSecondary).
			PaddingLeft(4),
	}
}

// BoxDrawingChars contains Unicode box drawing characters for the tree view.
type BoxDrawingChars struct {
	Vertical   string
	Horizontal string
	UpRight    string
	UpLeft     string
	DownRight  string
	DownLeft   string
	Cross      string
	TeeRight   string
	TeeLeft    string
	TeeUp      string
	TeeDown    string
}

// GetBoxDrawingChars returns the box drawing characters for tree rendering.
func GetBoxDrawingChars() BoxDrawingChars {
	return BoxDrawingChars{
		Vertical:   "│",
		Horizontal: "─",
		UpRight:    "└",
		UpLeft:     "┘",
		DownRight:  "┌",
		DownLeft:   "┐",
		Cross:      "┼",
		TeeRight:   "├",
		TeeLeft:    "┤",
		TeeUp:      "┴",
		TeeDown:    "┬",
	}
}
