package terminal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// FileNode represents a node in the file tree.
type FileNode struct {
	Name       string
	Path       string
	IsDir      bool
	IsExpanded bool
	Level      int
	Children   []*FileNode
	Parent     *FileNode
}

// FileTreeModel represents the file explorer component.
type FileTreeModel struct {
	root         *FileNode
	items        []*FileNode
	selected     int
	cursor       int
	scrollOffset int
	width        int
	height       int
	focused      bool
	styles       *ComponentStyles
	theme        *Theme
	showHidden   bool
}

// NewFileTree creates a new file tree model.
func NewFileTree(rootPath string, theme *Theme) (*FileTreeModel, error) {
	root := &FileNode{
		Name:       filepath.Base(rootPath),
		Path:       rootPath,
		IsDir:      true,
		IsExpanded: true,
		Level:      0,
	}

	// Load initial directory
	if err := loadDirectory(root, false); err != nil {
		return nil, err
	}

	ft := &FileTreeModel{
		root:       root,
		selected:   0,
		cursor:     0,
		theme:      theme,
		styles:     theme.CreateComponentStyles(),
		showHidden: false,
		items:      []*FileNode{},
	}

	ft.updateItems()
	return ft, nil
}

// Init initializes the file tree.
func (ft *FileTreeModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the file tree.
func (ft *FileTreeModel) Update(msg tea.Msg) (FileTreeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		ft.width = msg.Width / 3   // File tree takes 1/3 of width
		ft.height = msg.Height - 3 // Account for borders and status

	case tea.KeyPressMsg:
		if ft.focused {
			switch msg.String() {
			case "j", "down":
				ft.moveDown()
			case "k", "up":
				ft.moveUp()
			case "l", "right", "enter":
				ft.toggleExpand()
			case "h", "left":
				ft.collapseOrMoveToParent()
			case "g":
				ft.cursor = 0
				ft.scrollOffset = 0
			case "G":
				ft.cursor = len(ft.items) - 1
				ft.updateScroll()
			case ".":
				ft.showHidden = !ft.showHidden
				ft.updateItems()
			case "/":
				// Start search mode
				return *ft, nil
			}
		}
	}

	return *ft, nil
}

// View renders the file tree.
func (ft *FileTreeModel) View() string {
	if ft.height <= 0 || ft.width <= 0 {
		return ""
	}

	var lines []string
	chars := GetBoxDrawingChars()

	// Render visible items
	visibleStart := ft.scrollOffset
	visibleEnd := min(visibleStart+ft.height, len(ft.items))

	for i := visibleStart; i < visibleEnd; i++ {
		node := ft.items[i]
		line := ft.renderNode(node, i == ft.cursor, chars)
		lines = append(lines, line)
	}

	// Pad remaining space
	for len(lines) < ft.height {
		lines = append(lines, strings.Repeat(" ", ft.width))
	}

	content := strings.Join(lines, "\n")

	// Apply container style
	containerStyle := ft.styles.FileTree
	if ft.focused {
		containerStyle = containerStyle.BorderForeground(ft.theme.Accent)
	}

	return containerStyle.
		Width(ft.width).
		Height(ft.height).
		Render(content)
}

// renderNode renders a single node in the tree.
func (ft *FileTreeModel) renderNode(node *FileNode, selected bool, chars BoxDrawingChars) string {
	var prefix strings.Builder

	// Build tree structure
	for i := 0; i < node.Level; i++ {
		if i == node.Level-1 {
			prefix.WriteString(chars.TeeRight + chars.Horizontal)
		} else {
			prefix.WriteString(chars.Vertical + "  ")
		}
	}

	// Icon
	icon := "  "
	if node.IsDir {
		if node.IsExpanded {
			icon = "▼ "
		} else {
			icon = "▶ "
		}
	}

	// Name
	name := node.Name
	if node.IsDir {
		name += "/"
	}

	// Apply styles
	var style lipgloss.Style
	if selected {
		style = ft.styles.FileSelected
	} else if node.IsDir {
		style = ft.styles.FileItem.Foreground(ft.theme.Accent)
	} else {
		style = ft.styles.FileItem
	}

	line := fmt.Sprintf("%s%s%s", prefix.String(), icon, name)

	// Truncate if too long
	if len(line) > ft.width-2 {
		line = line[:ft.width-5] + "..."
	}

	// Pad to width
	line = line + strings.Repeat(" ", max(0, ft.width-2-len(line)))

	return style.Render(line)
}

// Navigation methods.
func (ft *FileTreeModel) moveDown() {
	if ft.cursor < len(ft.items)-1 {
		ft.cursor++
		ft.updateScroll()
	}
}

func (ft *FileTreeModel) moveUp() {
	if ft.cursor > 0 {
		ft.cursor--
		ft.updateScroll()
	}
}

func (ft *FileTreeModel) toggleExpand() {
	if ft.cursor >= 0 && ft.cursor < len(ft.items) {
		node := ft.items[ft.cursor]
		if node.IsDir {
			node.IsExpanded = !node.IsExpanded
			if node.IsExpanded && len(node.Children) == 0 {
				_ = loadDirectory(node, ft.showHidden) // Ignore error for UI operations
			}
			ft.updateItems()
		}
	}
}

func (ft *FileTreeModel) collapseOrMoveToParent() {
	if ft.cursor >= 0 && ft.cursor < len(ft.items) {
		node := ft.items[ft.cursor]
		if node.IsDir && node.IsExpanded {
			node.IsExpanded = false
			ft.updateItems()
		} else if node.Parent != nil {
			// Move to parent
			for i, item := range ft.items {
				if item == node.Parent {
					ft.cursor = i
					ft.updateScroll()
					break
				}
			}
		}
	}
}

func (ft *FileTreeModel) updateScroll() {
	// Ensure cursor is visible
	if ft.cursor < ft.scrollOffset {
		ft.scrollOffset = ft.cursor
	} else if ft.cursor >= ft.scrollOffset+ft.height {
		ft.scrollOffset = ft.cursor - ft.height + 1
	}
}

// updateItems rebuilds the flat list of visible items.
func (ft *FileTreeModel) updateItems() {
	ft.items = []*FileNode{}
	ft.addNodeToItems(ft.root)

	// Ensure cursor is valid
	if ft.cursor >= len(ft.items) {
		ft.cursor = max(0, len(ft.items)-1)
	}
}

func (ft *FileTreeModel) addNodeToItems(node *FileNode) {
	ft.items = append(ft.items, node)

	if node.IsDir && node.IsExpanded {
		for _, child := range node.Children {
			ft.addNodeToItems(child)
		}
	}
}

// GetSelectedPath returns the path of the selected item.
func (ft *FileTreeModel) GetSelectedPath() string {
	if ft.cursor >= 0 && ft.cursor < len(ft.items) {
		node := ft.items[ft.cursor]
		return node.Path
	}
	return ""
}

// SetFocused sets the focus state.
func (ft *FileTreeModel) SetFocused(focused bool) {
	ft.focused = focused
}

// Helper functions

func loadDirectory(node *FileNode, showHidden bool) error {
	entries, err := filepath.Glob(filepath.Join(node.Path, "*"))
	if err != nil {
		return err
	}

	// Also get hidden files if requested
	if showHidden {
		if hidden, err := filepath.Glob(filepath.Join(node.Path, ".*")); err == nil {
			entries = append(entries, hidden...)
		}
	}

	node.Children = []*FileNode{}

	for _, entry := range entries {
		name := filepath.Base(entry)
		if name == "." || name == ".." {
			continue
		}

		// Skip hidden files unless requested
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}

		stat, err := os.Stat(entry)
		if err != nil {
			continue
		}

		child := &FileNode{
			Name:   name,
			Path:   entry,
			IsDir:  stat.IsDir(),
			Level:  node.Level + 1,
			Parent: node,
		}

		node.Children = append(node.Children, child)
	}

	// Sort: directories first, then alphabetically
	sort.Slice(node.Children, func(i, j int) bool {
		if node.Children[i].IsDir != node.Children[j].IsDir {
			return node.Children[i].IsDir
		}
		return strings.ToLower(node.Children[i].Name) < strings.ToLower(node.Children[j].Name)
	})

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
