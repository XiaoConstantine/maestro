package terminal

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Mode represents the current input mode (Vim-style).
type Mode int

const (
	ModeNormal Mode = iota
	ModeInsert
	ModeVisual
	ModeCommand
	ModeSearch
)

// KeyBinding represents a key binding configuration.
type KeyBinding struct {
	Key         string
	Description string
	Action      ActionType
	Mode        Mode
}

// ActionType represents the type of action to perform.
type ActionType int

const (
	ActionNone ActionType = iota
	ActionQuit
	ActionSwitchPane
	ActionToggleFileTree
	ActionToggleTodoList
	ActionOpenFile
	ActionSearch
	ActionCommand
	ActionHelp
	ActionNavigateUp
	ActionNavigateDown
	ActionNavigateLeft
	ActionNavigateRight
	ActionPageUp
	ActionPageDown
	ActionGoToTop
	ActionGoToBottom
	ActionEnterInsertMode
	ActionEnterNormalMode
	ActionEnterVisualMode
	ActionEnterCommandMode
	ActionExecuteCommand
	ActionCancelCommand
	ActionYank
	ActionPaste
	ActionDelete
	ActionUndo
	ActionRedo
	ActionSave
	ActionReload
	ActionToggleTodo
	ActionMarkTodoComplete
	ActionResetTodo
	ActionExpandCollapse
	ActionSelectItem
)

// KeyHandler manages keyboard input and mode switching.
type KeyHandler struct {
	mode          Mode
	bindings      map[Mode][]KeyBinding
	commandBuffer string
	searchBuffer  string
	visualStart   int
	visualEnd     int
	lastAction    ActionType
	repeatCount   int
	leaderKey     string
	leaderPressed bool
}

// NewKeyHandler creates a new key handler with default Vim bindings.
func NewKeyHandler() *KeyHandler {
	kh := &KeyHandler{
		mode:      ModeNormal,
		bindings:  make(map[Mode][]KeyBinding),
		leaderKey: " ", // Space as leader key
	}

	kh.setupDefaultBindings()
	return kh
}

// setupDefaultBindings configures the default Vim-style key bindings.
func (kh *KeyHandler) setupDefaultBindings() {
	// Normal mode bindings
	kh.bindings[ModeNormal] = []KeyBinding{
		// Navigation
		{Key: "h", Description: "Move left", Action: ActionNavigateLeft},
		{Key: "j", Description: "Move down", Action: ActionNavigateDown},
		{Key: "k", Description: "Move up", Action: ActionNavigateUp},
		{Key: "l", Description: "Move right", Action: ActionNavigateRight},
		{Key: "left", Description: "Move left", Action: ActionNavigateLeft},
		{Key: "down", Description: "Move down", Action: ActionNavigateDown},
		{Key: "up", Description: "Move up", Action: ActionNavigateUp},
		{Key: "right", Description: "Move right", Action: ActionNavigateRight},

		// Quick navigation
		{Key: "g", Description: "Go to top", Action: ActionGoToTop},
		{Key: "G", Description: "Go to bottom", Action: ActionGoToBottom},
		{Key: "ctrl+u", Description: "Page up", Action: ActionPageUp},
		{Key: "ctrl+d", Description: "Page down", Action: ActionPageDown},

		// Mode switching
		{Key: "i", Description: "Insert mode", Action: ActionEnterInsertMode},
		{Key: "v", Description: "Visual mode", Action: ActionEnterVisualMode},
		{Key: ":", Description: "Command mode", Action: ActionEnterCommandMode},
		{Key: "/", Description: "Search", Action: ActionSearch},

		// File operations
		{Key: "enter", Description: "Open/Select", Action: ActionSelectItem},
		{Key: "o", Description: "Open file", Action: ActionOpenFile},
		{Key: "space", Description: "Expand/Collapse", Action: ActionExpandCollapse},

		// Pane management
		{Key: "tab", Description: "Next pane", Action: ActionSwitchPane},
		{Key: "ctrl+b", Description: "Toggle file tree", Action: ActionToggleFileTree},
		{Key: "ctrl+t", Description: "Toggle TODO list", Action: ActionToggleTodoList},

		// TODO operations
		{Key: "x", Description: "Mark TODO complete", Action: ActionMarkTodoComplete},
		{Key: "r", Description: "Reset TODO", Action: ActionResetTodo},
		{Key: "t", Description: "Toggle TODO status", Action: ActionToggleTodo},

		// Edit operations
		{Key: "y", Description: "Yank", Action: ActionYank},
		{Key: "p", Description: "Paste", Action: ActionPaste},
		{Key: "d", Description: "Delete", Action: ActionDelete},
		{Key: "u", Description: "Undo", Action: ActionUndo},
		{Key: "ctrl+r", Description: "Redo", Action: ActionRedo},

		// System
		{Key: "ctrl+s", Description: "Save", Action: ActionSave},
		{Key: "ctrl+l", Description: "Reload", Action: ActionReload},
		{Key: "?", Description: "Help", Action: ActionHelp},
		{Key: "ctrl+c", Description: "Quit", Action: ActionQuit},
		{Key: "q", Description: "Quit", Action: ActionQuit},
	}

	// Insert mode bindings
	kh.bindings[ModeInsert] = []KeyBinding{
		{Key: "esc", Description: "Normal mode", Action: ActionEnterNormalMode},
		{Key: "ctrl+c", Description: "Cancel", Action: ActionCancelCommand},
	}

	// Visual mode bindings
	kh.bindings[ModeVisual] = []KeyBinding{
		{Key: "esc", Description: "Normal mode", Action: ActionEnterNormalMode},
		{Key: "h", Description: "Extend left", Action: ActionNavigateLeft},
		{Key: "j", Description: "Extend down", Action: ActionNavigateDown},
		{Key: "k", Description: "Extend up", Action: ActionNavigateUp},
		{Key: "l", Description: "Extend right", Action: ActionNavigateRight},
		{Key: "y", Description: "Yank selection", Action: ActionYank},
		{Key: "d", Description: "Delete selection", Action: ActionDelete},
		{Key: "c", Description: "Change selection", Action: ActionEnterInsertMode},
	}

	// Command mode bindings
	kh.bindings[ModeCommand] = []KeyBinding{
		{Key: "esc", Description: "Cancel", Action: ActionCancelCommand},
		{Key: "enter", Description: "Execute", Action: ActionExecuteCommand},
	}

	// Search mode bindings
	kh.bindings[ModeSearch] = []KeyBinding{
		{Key: "esc", Description: "Cancel", Action: ActionCancelCommand},
		{Key: "enter", Description: "Search", Action: ActionExecuteCommand},
	}
}

// HandleKey processes a key event and returns the corresponding action.
func (kh *KeyHandler) HandleKey(msg tea.KeyPressMsg) (ActionType, string) {
	key := msg.String()

	// Handle leader key combinations
	if kh.leaderPressed {
		kh.leaderPressed = false
		leaderCombo := kh.leaderKey + key
		if action := kh.findAction(leaderCombo, kh.mode); action != ActionNone {
			return action, ""
		}
	}

	// Check if this is the leader key
	if key == kh.leaderKey && kh.mode == ModeNormal {
		kh.leaderPressed = true
		return ActionNone, ""
	}

	// Handle numeric repeat counts in normal mode
	if kh.mode == ModeNormal && isNumeric(key) && key != "0" {
		if kh.repeatCount == 0 {
			kh.repeatCount = int(key[0] - '0')
		} else {
			kh.repeatCount = kh.repeatCount*10 + int(key[0]-'0')
		}
		return ActionNone, fmt.Sprintf("%d", kh.repeatCount)
	}

	// Buffer input in command/search modes
	if kh.mode == ModeCommand || kh.mode == ModeSearch {
		if key == "backspace" {
			if kh.mode == ModeCommand && len(kh.commandBuffer) > 0 {
				kh.commandBuffer = kh.commandBuffer[:len(kh.commandBuffer)-1]
			} else if kh.mode == ModeSearch && len(kh.searchBuffer) > 0 {
				kh.searchBuffer = kh.searchBuffer[:len(kh.searchBuffer)-1]
			}
			return ActionNone, kh.getBuffer()
		} else if key != "esc" && key != "enter" && len(key) == 1 {
			if kh.mode == ModeCommand {
				kh.commandBuffer += key
			} else {
				kh.searchBuffer += key
			}
			return ActionNone, kh.getBuffer()
		}
	}

	// Find and execute the action
	action := kh.findAction(key, kh.mode)

	// Apply repeat count if applicable
	repeatCount := max(1, kh.repeatCount)
	kh.repeatCount = 0

	// Handle mode transitions
	switch action {
	case ActionEnterNormalMode:
		kh.mode = ModeNormal
		kh.clearBuffers()
	case ActionEnterInsertMode:
		kh.mode = ModeInsert
	case ActionEnterVisualMode:
		kh.mode = ModeVisual
	case ActionEnterCommandMode:
		kh.mode = ModeCommand
		kh.commandBuffer = ""
	case ActionSearch:
		kh.mode = ModeSearch
		kh.searchBuffer = ""
	case ActionExecuteCommand:
		buffer := kh.getBuffer()
		kh.mode = ModeNormal
		kh.clearBuffers()
		return action, buffer
	case ActionCancelCommand:
		kh.mode = ModeNormal
		kh.clearBuffers()
	}

	// Store last action for repeat (.)
	if action != ActionNone && kh.mode == ModeNormal {
		kh.lastAction = action
	}

	// Return action with repeat count encoded
	if repeatCount > 1 {
		return action, fmt.Sprintf("%d", repeatCount)
	}

	return action, ""
}

// findAction finds the action for a given key in the current mode.
func (kh *KeyHandler) findAction(key string, mode Mode) ActionType {
	if bindings, ok := kh.bindings[mode]; ok {
		for _, binding := range bindings {
			if binding.Key == key {
				return binding.Action
			}
		}
	}
	return ActionNone
}

// GetMode returns the current mode.
func (kh *KeyHandler) GetMode() Mode {
	return kh.mode
}

// GetModeString returns the mode as a string for display.
func (kh *KeyHandler) GetModeString() string {
	switch kh.mode {
	case ModeNormal:
		return "NORMAL"
	case ModeInsert:
		return "INSERT"
	case ModeVisual:
		return "VISUAL"
	case ModeCommand:
		return "COMMAND"
	case ModeSearch:
		return "SEARCH"
	default:
		return "UNKNOWN"
	}
}

// getBuffer returns the current input buffer.
func (kh *KeyHandler) getBuffer() string {
	switch kh.mode {
	case ModeCommand:
		return kh.commandBuffer
	case ModeSearch:
		return kh.searchBuffer
	default:
		return ""
	}
}

// clearBuffers clears all input buffers.
func (kh *KeyHandler) clearBuffers() {
	kh.commandBuffer = ""
	kh.searchBuffer = ""
	kh.visualStart = 0
	kh.visualEnd = 0
}

// GetHelpText returns help text for the current mode.
func (kh *KeyHandler) GetHelpText() string {
	var help strings.Builder

	help.WriteString(fmt.Sprintf("Mode: %s\n\n", kh.GetModeString()))
	help.WriteString("Key Bindings:\n")

	if bindings, ok := kh.bindings[kh.mode]; ok {
		for _, binding := range bindings {
			help.WriteString(fmt.Sprintf("  %-12s %s\n", binding.Key, binding.Description))
		}
	}

	return help.String()
}

// SetLeaderKey sets the leader key for combinations.
func (kh *KeyHandler) SetLeaderKey(key string) {
	kh.leaderKey = key
}

// AddBinding adds a custom key binding.
func (kh *KeyHandler) AddBinding(mode Mode, key string, description string, action ActionType) {
	binding := KeyBinding{
		Key:         key,
		Description: description,
		Action:      action,
		Mode:        mode,
	}

	kh.bindings[mode] = append(kh.bindings[mode], binding)
}

// RemoveBinding removes a key binding.
func (kh *KeyHandler) RemoveBinding(mode Mode, key string) {
	if bindings, ok := kh.bindings[mode]; ok {
		filtered := []KeyBinding{}
		for _, binding := range bindings {
			if binding.Key != key {
				filtered = append(filtered, binding)
			}
		}
		kh.bindings[mode] = filtered
	}
}

// Helper functions

func isNumeric(s string) bool {
	return len(s) == 1 && s[0] >= '0' && s[0] <= '9'
}
