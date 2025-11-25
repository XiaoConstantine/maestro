package terminal

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ReviewActionType represents actions specific to the review TUI.
type ReviewActionType int

const (
	ReviewActionNone ReviewActionType = iota
	ReviewActionNavigateUp
	ReviewActionNavigateDown
	ReviewActionNavigateLeft
	ReviewActionNavigateRight
	ReviewActionExpand
	ReviewActionCollapse
	ReviewActionToggleExpand
	ReviewActionCycleFocus
	ReviewActionFilterAll
	ReviewActionFilterCritical
	ReviewActionFilterWarning
	ReviewActionFilterSuggestion
	ReviewActionCycleFilter
	ReviewActionSearch
	ReviewActionPost
	ReviewActionSkip
	ReviewActionResolve
	ReviewActionGoToTop
	ReviewActionGoToBottom
	ReviewActionPageUp
	ReviewActionPageDown
	ReviewActionShowHelp
	ReviewActionShowMetrics
	ReviewActionQuit
)

// ReviewKeyBinding represents a key binding for review actions.
type ReviewKeyBinding struct {
	Key         string
	Description string
	Action      ReviewActionType
	ShowInHelp  bool
}

// ReviewKeyHandler manages keyboard input for the review TUI.
type ReviewKeyHandler struct {
	bindings    []ReviewKeyBinding
	searchMode  bool
	searchQuery string
}

// NewReviewKeyHandler creates a new review key handler with default bindings.
func NewReviewKeyHandler() *ReviewKeyHandler {
	handler := &ReviewKeyHandler{
		bindings: getDefaultReviewBindings(),
	}
	return handler
}

func getDefaultReviewBindings() []ReviewKeyBinding {
	return []ReviewKeyBinding{
		{Key: "j", Description: "Move down", Action: ReviewActionNavigateDown, ShowInHelp: true},
		{Key: "down", Description: "Move down", Action: ReviewActionNavigateDown, ShowInHelp: false},
		{Key: "k", Description: "Move up", Action: ReviewActionNavigateUp, ShowInHelp: true},
		{Key: "up", Description: "Move up", Action: ReviewActionNavigateUp, ShowInHelp: false},
		{Key: "h", Description: "Move left / Collapse", Action: ReviewActionNavigateLeft, ShowInHelp: true},
		{Key: "left", Description: "Move left / Collapse", Action: ReviewActionNavigateLeft, ShowInHelp: false},
		{Key: "l", Description: "Move right / Expand", Action: ReviewActionNavigateRight, ShowInHelp: true},
		{Key: "right", Description: "Move right / Expand", Action: ReviewActionNavigateRight, ShowInHelp: false},

		{Key: "enter", Description: "Expand / Select", Action: ReviewActionExpand, ShowInHelp: true},
		{Key: "esc", Description: "Collapse / Back", Action: ReviewActionCollapse, ShowInHelp: true},
		{Key: " ", Description: "Toggle file expand", Action: ReviewActionToggleExpand, ShowInHelp: true},
		{Key: "tab", Description: "Cycle focus", Action: ReviewActionCycleFocus, ShowInHelp: true},

		{Key: "0", Description: "Show all comments", Action: ReviewActionFilterAll, ShowInHelp: true},
		{Key: "1", Description: "Filter critical", Action: ReviewActionFilterCritical, ShowInHelp: true},
		{Key: "2", Description: "Filter warnings", Action: ReviewActionFilterWarning, ShowInHelp: true},
		{Key: "3", Description: "Filter suggestions", Action: ReviewActionFilterSuggestion, ShowInHelp: true},
		{Key: "f", Description: "Cycle filter", Action: ReviewActionCycleFilter, ShowInHelp: true},
		{Key: "/", Description: "Search comments", Action: ReviewActionSearch, ShowInHelp: true},

		{Key: "p", Description: "Post comments to GitHub", Action: ReviewActionPost, ShowInHelp: true},
		{Key: "s", Description: "Skip current comment", Action: ReviewActionSkip, ShowInHelp: true},
		{Key: "r", Description: "Mark as resolved", Action: ReviewActionResolve, ShowInHelp: true},

		{Key: "g", Description: "Go to top", Action: ReviewActionGoToTop, ShowInHelp: true},
		{Key: "G", Description: "Go to bottom", Action: ReviewActionGoToBottom, ShowInHelp: true},
		{Key: "ctrl+d", Description: "Page down", Action: ReviewActionPageDown, ShowInHelp: true},
		{Key: "ctrl+u", Description: "Page up", Action: ReviewActionPageUp, ShowInHelp: true},

		{Key: "?", Description: "Toggle help", Action: ReviewActionShowHelp, ShowInHelp: true},
		{Key: "m", Description: "Toggle metrics", Action: ReviewActionShowMetrics, ShowInHelp: true},
		{Key: "q", Description: "Quit", Action: ReviewActionQuit, ShowInHelp: true},
		{Key: "ctrl+c", Description: "Quit", Action: ReviewActionQuit, ShowInHelp: false},
	}
}

// HandleKey processes a key event and returns the corresponding action.
func (h *ReviewKeyHandler) HandleKey(msg tea.KeyMsg) ReviewActionType {
	key := msg.String()

	if h.searchMode {
		switch key {
		case "esc":
			h.searchMode = false
			h.searchQuery = ""
			return ReviewActionCollapse
		case "enter":
			h.searchMode = false
			return ReviewActionSearch
		case "backspace":
			if len(h.searchQuery) > 0 {
				h.searchQuery = h.searchQuery[:len(h.searchQuery)-1]
			}
			return ReviewActionNone
		default:
			if len(key) == 1 {
				h.searchQuery += key
			}
			return ReviewActionNone
		}
	}

	for _, binding := range h.bindings {
		if binding.Key == key {
			if binding.Action == ReviewActionSearch {
				h.searchMode = true
				h.searchQuery = ""
			}
			return binding.Action
		}
	}

	return ReviewActionNone
}

// IsSearchMode returns whether search mode is active.
func (h *ReviewKeyHandler) IsSearchMode() bool {
	return h.searchMode
}

// GetSearchQuery returns the current search query.
func (h *ReviewKeyHandler) GetSearchQuery() string {
	return h.searchQuery
}

// ClearSearch clears the search state.
func (h *ReviewKeyHandler) ClearSearch() {
	h.searchMode = false
	h.searchQuery = ""
}

// GetHelpBindings returns bindings that should be shown in help.
func (h *ReviewKeyHandler) GetHelpBindings() []ReviewKeyBinding {
	var helpBindings []ReviewKeyBinding
	for _, b := range h.bindings {
		if b.ShowInHelp {
			helpBindings = append(helpBindings, b)
		}
	}
	return helpBindings
}

// GetHelpText returns formatted help text.
func (h *ReviewKeyHandler) GetHelpText() string {
	var lines []string
	lines = append(lines, "Review TUI Keyboard Shortcuts")
	lines = append(lines, "")
	lines = append(lines, "Navigation:")

	sections := map[string][]ReviewKeyBinding{
		"Navigation": {},
		"Filtering":  {},
		"Actions":    {},
		"Other":      {},
	}

	for _, b := range h.GetHelpBindings() {
		switch b.Action {
		case ReviewActionNavigateUp, ReviewActionNavigateDown, ReviewActionNavigateLeft, ReviewActionNavigateRight,
			ReviewActionGoToTop, ReviewActionGoToBottom, ReviewActionPageUp, ReviewActionPageDown,
			ReviewActionCycleFocus:
			sections["Navigation"] = append(sections["Navigation"], b)
		case ReviewActionFilterAll, ReviewActionFilterCritical, ReviewActionFilterWarning, ReviewActionFilterSuggestion,
			ReviewActionCycleFilter, ReviewActionSearch:
			sections["Filtering"] = append(sections["Filtering"], b)
		case ReviewActionExpand, ReviewActionCollapse, ReviewActionToggleExpand, ReviewActionPost, ReviewActionSkip, ReviewActionResolve:
			sections["Actions"] = append(sections["Actions"], b)
		default:
			sections["Other"] = append(sections["Other"], b)
		}
	}

	for _, section := range []string{"Navigation", "Filtering", "Actions", "Other"} {
		if len(sections[section]) > 0 {
			lines = append(lines, "")
			lines = append(lines, section+":")
			for _, b := range sections[section] {
				lines = append(lines, fmt.Sprintf("  %-12s %s", b.Key, b.Description))
			}
		}
	}

	return strings.Join(lines, "\n")
}

// AddBinding adds a custom key binding.
func (h *ReviewKeyHandler) AddBinding(key, description string, action ReviewActionType) {
	h.bindings = append(h.bindings, ReviewKeyBinding{
		Key:         key,
		Description: description,
		Action:      action,
		ShowInHelp:  true,
	})
}

// RemoveBinding removes a key binding.
func (h *ReviewKeyHandler) RemoveBinding(key string) {
	var filtered []ReviewKeyBinding
	for _, b := range h.bindings {
		if b.Key != key {
			filtered = append(filtered, b)
		}
	}
	h.bindings = filtered
}

// GetActionDescription returns the description for an action.
func GetActionDescription(action ReviewActionType) string {
	descriptions := map[ReviewActionType]string{
		ReviewActionNavigateUp:       "Move cursor up",
		ReviewActionNavigateDown:     "Move cursor down",
		ReviewActionNavigateLeft:     "Collapse or move left",
		ReviewActionNavigateRight:    "Expand or move right",
		ReviewActionExpand:           "Expand selected item",
		ReviewActionCollapse:         "Collapse or go back",
		ReviewActionToggleExpand:     "Toggle file expansion",
		ReviewActionCycleFocus:       "Cycle between panes",
		ReviewActionFilterAll:        "Show all comments",
		ReviewActionFilterCritical:   "Show only critical comments",
		ReviewActionFilterWarning:    "Show only warnings",
		ReviewActionFilterSuggestion: "Show only suggestions",
		ReviewActionCycleFilter:      "Cycle through filters",
		ReviewActionSearch:           "Search in comments",
		ReviewActionPost:             "Post comments to GitHub",
		ReviewActionSkip:             "Skip current comment",
		ReviewActionResolve:          "Mark comment as resolved",
		ReviewActionGoToTop:          "Go to first item",
		ReviewActionGoToBottom:       "Go to last item",
		ReviewActionPageUp:           "Scroll up one page",
		ReviewActionPageDown:         "Scroll down one page",
		ReviewActionShowHelp:         "Toggle help panel",
		ReviewActionShowMetrics:      "Toggle metrics view",
		ReviewActionQuit:             "Exit the review TUI",
	}

	if desc, ok := descriptions[action]; ok {
		return desc
	}
	return "Unknown action"
}
