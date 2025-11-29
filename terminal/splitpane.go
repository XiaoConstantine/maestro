package terminal

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// PanePosition represents the position of a pane.
type PanePosition int

const (
	PaneLeft PanePosition = iota
	PaneRight
	PaneTop
	PaneBottom
	PaneCenter
)

// FocusedPane represents which pane is currently focused.
type FocusedPane int

const (
	FocusFileTree FocusedPane = iota
	FocusTodoList
	FocusMainContent
)

// SplitPaneModel manages the layout of multiple panes.
type SplitPaneModel struct {
	width        int
	height       int
	focused      FocusedPane
	splitRatio   float64 // Ratio for vertical split (0.0 to 1.0)
	showFileTree bool
	showTodoList bool
	styles       *ComponentStyles
	theme        *Theme
}

// NewSplitPane creates a new split pane layout manager.
func NewSplitPane(theme *Theme) *SplitPaneModel {
	return &SplitPaneModel{
		focused:      FocusMainContent,
		splitRatio:   0.25, // Smaller side panels for better proportions
		showFileTree: true,
		showTodoList: true,
		styles:       theme.CreateComponentStyles(),
		theme:        theme,
	}
}

// Init initializes the split pane.
func (sp *SplitPaneModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the split pane.
func (sp *SplitPaneModel) Update(msg tea.Msg) (SplitPaneModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		sp.width = msg.Width
		sp.height = msg.Height

	case tea.KeyPressMsg:
		switch msg.String() {
		case "tab":
			// Cycle through panes
			sp.cycleFocus(true)
		case "shift+tab":
			// Cycle backward
			sp.cycleFocus(false)
		case "ctrl+b":
			// Toggle file tree
			sp.showFileTree = !sp.showFileTree
			sp.adjustLayout()
		case "ctrl+t":
			// Toggle TODO list
			sp.showTodoList = !sp.showTodoList
			sp.adjustLayout()
		case "ctrl+w":
			// Window navigation commands
			return *sp, nil
		}
	}

	return *sp, nil
}

// View renders the split pane layout.
func (sp *SplitPaneModel) View() string {
	// This method doesn't render content directly,
	// but provides layout information to the main app
	return ""
}

// GetLayout returns the dimensions for each pane.
func (sp *SplitPaneModel) GetLayout() (fileTreeWidth, todoListWidth, mainWidth, contentHeight int) {
	totalWidth := sp.width
	contentHeight = sp.height - 1 // Reserve 1 line for status bar

	if sp.showFileTree && sp.showTodoList {
		// Three-pane layout
		sideWidth := int(float64(totalWidth) * sp.splitRatio)
		fileTreeWidth = sideWidth
		todoListWidth = sideWidth
		mainWidth = totalWidth - (fileTreeWidth + todoListWidth)
	} else if sp.showFileTree && !sp.showTodoList {
		// Two-pane: file tree and main
		fileTreeWidth = int(float64(totalWidth) * sp.splitRatio)
		todoListWidth = 0
		mainWidth = totalWidth - fileTreeWidth
	} else if !sp.showFileTree && sp.showTodoList {
		// Two-pane: main and TODO list
		fileTreeWidth = 0
		todoListWidth = int(float64(totalWidth) * sp.splitRatio)
		mainWidth = totalWidth - todoListWidth
	} else {
		// Single pane: main only
		fileTreeWidth = 0
		todoListWidth = 0
		mainWidth = totalWidth
	}

	// Ensure minimum widths
	minPaneWidth := 20
	if fileTreeWidth > 0 && fileTreeWidth < minPaneWidth {
		fileTreeWidth = minPaneWidth
	}
	if todoListWidth > 0 && todoListWidth < minPaneWidth {
		todoListWidth = minPaneWidth
	}
	if mainWidth < minPaneWidth*2 {
		mainWidth = minPaneWidth * 2
	}

	return
}

// RenderLayout combines multiple pane views into a single layout.
func (sp *SplitPaneModel) RenderLayout(fileTreeView, todoListView, mainView, statusBarView string) string {
	fileTreeWidth, todoListWidth, mainWidth, contentHeight := sp.GetLayout()

	// Split views into lines
	fileTreeLines := strings.Split(fileTreeView, "\n")
	todoListLines := strings.Split(todoListView, "\n")
	mainLines := strings.Split(mainView, "\n")

	// Ensure all have the same number of lines
	for len(fileTreeLines) < contentHeight {
		fileTreeLines = append(fileTreeLines, strings.Repeat(" ", fileTreeWidth))
	}
	for len(todoListLines) < contentHeight {
		todoListLines = append(todoListLines, strings.Repeat(" ", todoListWidth))
	}
	for len(mainLines) < contentHeight {
		mainLines = append(mainLines, strings.Repeat(" ", mainWidth))
	}

	// Combine lines horizontally
	var combinedLines []string
	for i := 0; i < contentHeight; i++ {
		var line strings.Builder

		// Add file tree if visible
		if sp.showFileTree && i < len(fileTreeLines) {
			line.WriteString(fileTreeLines[i])
		}

		// Add main content
		if i < len(mainLines) {
			line.WriteString(mainLines[i])
		}

		// Add TODO list if visible
		if sp.showTodoList && i < len(todoListLines) {
			line.WriteString(todoListLines[i])
		}

		combinedLines = append(combinedLines, line.String())
	}

	// Add status bar at the bottom
	combinedLines = append(combinedLines, statusBarView)

	return strings.Join(combinedLines, "\n")
}

// cycleFocus cycles through the focused pane.
func (sp *SplitPaneModel) cycleFocus(forward bool) {
	panes := sp.getVisiblePanes()
	if len(panes) <= 1 {
		return
	}

	// Find current focus in visible panes
	currentIdx := -1
	for i, pane := range panes {
		if pane == sp.focused {
			currentIdx = i
			break
		}
	}

	// Cycle to next/previous
	if forward {
		currentIdx = (currentIdx + 1) % len(panes)
	} else {
		currentIdx = (currentIdx - 1 + len(panes)) % len(panes)
	}

	sp.focused = panes[currentIdx]
}

// getVisiblePanes returns list of visible panes.
func (sp *SplitPaneModel) getVisiblePanes() []FocusedPane {
	panes := []FocusedPane{}

	if sp.showFileTree {
		panes = append(panes, FocusFileTree)
	}
	panes = append(panes, FocusMainContent)
	if sp.showTodoList {
		panes = append(panes, FocusTodoList)
	}

	return panes
}

// adjustLayout adjusts the split ratio when panes are toggled.
func (sp *SplitPaneModel) adjustLayout() {
	if sp.showFileTree && sp.showTodoList {
		// Three panes - smaller side panels
		sp.splitRatio = 0.25
	} else if sp.showFileTree || sp.showTodoList {
		// Two panes
		sp.splitRatio = 0.30
	}
	// Single pane doesn't need ratio adjustment
}

// GetFocusedPane returns the currently focused pane.
func (sp *SplitPaneModel) GetFocusedPane() FocusedPane {
	return sp.focused
}

// SetFocusedPane sets the focused pane.
func (sp *SplitPaneModel) SetFocusedPane(pane FocusedPane) {
	sp.focused = pane
}

// IsFileTreeVisible returns whether the file tree is visible.
func (sp *SplitPaneModel) IsFileTreeVisible() bool {
	return sp.showFileTree
}

// IsTodoListVisible returns whether the TODO list is visible.
func (sp *SplitPaneModel) IsTodoListVisible() bool {
	return sp.showTodoList
}

// SetSplitRatio sets the split ratio for side panes.
func (sp *SplitPaneModel) SetSplitRatio(ratio float64) {
	if ratio > 0.1 && ratio < 0.5 {
		sp.splitRatio = ratio
	}
}

// ResizePanes adjusts pane sizes with keyboard shortcuts.
func (sp *SplitPaneModel) ResizePanes(increase bool) {
	delta := 0.05
	if !increase {
		delta = -delta
	}

	newRatio := sp.splitRatio + delta
	sp.SetSplitRatio(newRatio)
}
