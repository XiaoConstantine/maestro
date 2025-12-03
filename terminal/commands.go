package terminal

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Command represents a command that can be executed.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	Category    string
	Handler     CommandHandler
	Args        []CommandArg
}

// CommandArg represents a command argument.
type CommandArg struct {
	Name        string
	Description string
	Required    bool
	Default     string
}

// CommandHandler is a function that executes a command.
type CommandHandler func(args map[string]string) (string, error)

// CommandPaletteModel manages the command palette.
type CommandPaletteModel struct {
	commands     []Command
	filteredCmds []Command
	selected     int
	input        textinput.Model
	visible      bool
	width        int
	height       int
	maxHeight    int
	styles       *ComponentStyles
	theme        *Theme
	history      []string
	historyIdx   int
}

// NewCommandPalette creates a new command palette.
func NewCommandPalette(theme *Theme) *CommandPaletteModel {
	ti := textinput.New()
	ti.Placeholder = "Type a command or search..."
	ti.CharLimit = 256
	ti.SetWidth(60)

	cp := &CommandPaletteModel{
		commands:   []Command{},
		input:      ti,
		visible:    false,
		maxHeight:  15,
		styles:     theme.CreateComponentStyles(),
		theme:      theme,
		history:    []string{},
		historyIdx: -1,
	}

	cp.registerDefaultCommands()
	return cp
}

// registerDefaultCommands registers the default set of commands.
func (cp *CommandPaletteModel) registerDefaultCommands() {
	// Maestro core/help/system
	cp.RegisterCommand(Command{
		Name:        "help",
		Aliases:     []string{"/help", "h", "?"},
		Description: "Show available commands",
		Category:    "Help",
	})
	cp.RegisterCommand(Command{
		Name:        "exit",
		Aliases:     []string{"/exit", "quit", "/quit"},
		Description: "Exit the application",
		Category:    "System",
	})

	// PR review and questions
	cp.RegisterCommand(Command{
		Name:        "review",
		Aliases:     []string{"/review"},
		Description: "Review a specific pull request",
		Category:    "GitHub",
		Args: []CommandArg{
			{Name: "pr", Description: "PR number", Required: true},
		},
	})
	cp.RegisterCommand(Command{
		Name:        "ask",
		Aliases:     []string{"/ask"},
		Description: "Ask a question about the repository",
		Category:    "Maestro",
		Args: []CommandArg{
			{Name: "question", Description: "Question text", Required: true},
		},
	})

	// Subagents
	cp.RegisterCommand(Command{
		Name:        "claude",
		Aliases:     []string{"/claude"},
		Description: "Send prompt to Claude Sonnet 4.5",
		Category:    "Subagent",
		Args: []CommandArg{
			{Name: "prompt", Description: "Prompt text", Required: true},
		},
	})
	cp.RegisterCommand(Command{
		Name:        "gemini",
		Aliases:     []string{"/gemini"},
		Description: "Send prompt to Gemini 3 Pro",
		Category:    "Subagent",
		Args: []CommandArg{
			{Name: "prompt", Description: "Prompt text", Required: true},
		},
	})
	// Tools management as multi-word commands (to match `tools setup|status`)
	cp.RegisterCommand(Command{
		Name:        "tools setup",
		Aliases:     []string{"/tools setup"},
		Description: "Setup CLI tools",
		Category:    "Tools",
	})
	cp.RegisterCommand(Command{
		Name:        "tools status",
		Aliases:     []string{"/tools status"},
		Description: "Show CLI tool status",
		Category:    "Tools",
	})

	// Sessions and coordination
	cp.RegisterCommand(Command{
		Name:        "sessions",
		Aliases:     []string{"/sessions"},
		Description: "Manage Claude sessions",
		Category:    "Sessions",
	})
	cp.RegisterCommand(Command{
		Name:        "dashboard",
		Aliases:     []string{"/dashboard"},
		Description: "Show sessions dashboard",
		Category:    "Sessions",
	})
	cp.RegisterCommand(Command{
		Name:        "coordinate",
		Aliases:     []string{"/coordinate"},
		Description: "Coordinate multi-session tasks",
		Category:    "Sessions",
		Args:        []CommandArg{{Name: "task", Description: "Task description", Required: true}},
	})
	cp.RegisterCommand(Command{
		Name:        "switch",
		Aliases:     []string{"/switch"},
		Description: "Switch between sessions",
		Category:    "Sessions",
		Args:        []CommandArg{{Name: "session", Description: "Name or ID", Required: true}},
	})
	cp.RegisterCommand(Command{
		Name:        "enter",
		Aliases:     []string{"/enter"},
		Description: "Enter interactive mode with a session",
		Category:    "Sessions",
	})
	cp.RegisterCommand(Command{
		Name:        "list",
		Aliases:     []string{"/list"},
		Description: "List available sessions",
		Category:    "Sessions",
	})
}

// Init initializes the command palette.
func (cp *CommandPaletteModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the command palette.
func (cp *CommandPaletteModel) Update(msg tea.Msg) (CommandPaletteModel, tea.Cmd) {
	var cmd tea.Cmd

	if !cp.visible {
		return *cp, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		cp.width = min(80, msg.Width-10)
		cp.height = min(cp.maxHeight, msg.Height/2)
		cp.input.SetWidth(cp.width - 4)

	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			cp.Hide()
			return *cp, nil

		case "enter":
			if cp.selected >= 0 && cp.selected < len(cp.filteredCmds) {
				cmd := cp.filteredCmds[cp.selected]
				cp.addToHistory(cp.input.Value())
				cp.Hide()
				// If command has required args, insert into input for user to complete
				if len(cmd.Args) > 0 && cmd.Args[0].Required {
					return *cp, func() tea.Msg {
						return InsertCommandMsg{Command: "/" + cmd.Name + " "}
					}
				}
				// Otherwise execute directly
				return *cp, cp.executeCommand(cmd)
			}
			// Try to parse and execute typed command
			if cp.input.Value() != "" {
				cp.addToHistory(cp.input.Value())
				if cmd := cp.parseAndExecute(cp.input.Value()); cmd != nil {
					cp.Hide()
					return *cp, cmd
				}
			}

		case "up", "ctrl+p":
			if cp.selected > 0 {
				cp.selected--
			}

		case "down", "ctrl+n":
			if cp.selected < len(cp.filteredCmds)-1 {
				cp.selected++
			}

		case "ctrl+u":
			// Clear input
			cp.input.SetValue("")
			cp.filterCommands("")

		case "tab":
			// Auto-complete
			if cp.selected >= 0 && cp.selected < len(cp.filteredCmds) {
				cp.input.SetValue(cp.filteredCmds[cp.selected].Name)
				cp.filterCommands(cp.input.Value())
			}

		default:
			// Update input
			cp.input, cmd = cp.input.Update(msg)
			cp.filterCommands(cp.input.Value())
			cp.selected = 0
		}
	}

	return *cp, cmd
}

// View renders the command palette.
func (cp *CommandPaletteModel) View() string {
	if !cp.visible {
		return ""
	}

	var content strings.Builder

	// Input field
	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(cp.theme.Accent).
		Padding(0, 1)

	prompt := lipgloss.NewStyle().Foreground(cp.theme.TextSecondary).Render(":") + " "
	inputLine := prompt + cp.input.View()
	content.WriteString(inputStyle.Render(inputLine))
	content.WriteString("\n")

	// Command list
	if len(cp.filteredCmds) > 0 {
		listStyle := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderTop(false).
			BorderForeground(cp.theme.Border).
			Padding(0, 1).
			Width(cp.width)

		var items []string
		maxItems := min(10, len(cp.filteredCmds))

		for i := 0; i < maxItems; i++ {
			cmd := cp.filteredCmds[i]
			item := cp.renderCommandItem(cmd, i == cp.selected)
			items = append(items, item)
		}

		if len(cp.filteredCmds) > maxItems {
			more := lipgloss.NewStyle().Foreground(cp.theme.TextMuted).Render(
				fmt.Sprintf("... and %d more", len(cp.filteredCmds)-maxItems),
			)
			items = append(items, more)
		}

		content.WriteString(listStyle.Render(strings.Join(items, "\n")))
	} else if cp.input.Value() != "" {
		// No matches
		noMatch := lipgloss.NewStyle().
			Foreground(cp.theme.TextMuted).
			Italic(true).
			Padding(1).
			Render("No matching commands")
		content.WriteString(noMatch)
	}

	// Center the palette
	return lipgloss.Place(
		cp.width+4,
		cp.height,
		lipgloss.Center,
		lipgloss.Center,
		content.String(),
	)
}

// renderCommandItem renders a single command item.
func (cp *CommandPaletteModel) renderCommandItem(cmd Command, selected bool) string {
	var style lipgloss.Style

	if selected {
		style = lipgloss.NewStyle().
			Background(lipgloss.Color("#3A3C55")).
			Foreground(cp.theme.Accent).
			Bold(true)
	} else {
		style = lipgloss.NewStyle().
			Foreground(cp.theme.TextPrimary)
	}

	// Format: [category] name - description
	category := lipgloss.NewStyle().Foreground(cp.theme.Accent).Render(fmt.Sprintf("[%s]", cmd.Category))
	name := style.Render(cmd.Name)

	// Add aliases if present
	aliases := ""
	if len(cmd.Aliases) > 0 {
		aliasStr := strings.Join(cmd.Aliases, ", ")
		aliases = lipgloss.NewStyle().Foreground(cp.theme.TextMuted).Render(fmt.Sprintf(" (%s)", aliasStr))
	}

	desc := lipgloss.NewStyle().Foreground(cp.theme.TextSecondary).Render(" - " + cmd.Description)

	line := fmt.Sprintf("%s %s%s%s", category, name, aliases, desc)

	// Truncate if too long
	maxWidth := cp.width - 4
	if maxWidth < 3 {
		maxWidth = 3
	}
	if len(line) > maxWidth {
		if maxWidth >= 4 {
			line = line[:maxWidth-3] + "..."
		} else {
			line = "..."
		}
	}

	return line
}

// filterCommands filters commands based on input.
func (cp *CommandPaletteModel) filterCommands(input string) {
	if input == "" {
		cp.filteredCmds = cp.commands
		return
	}

	input = strings.ToLower(input)
	cp.filteredCmds = []Command{}

	// Exact matches first
	for _, cmd := range cp.commands {
		if strings.ToLower(cmd.Name) == input {
			cp.filteredCmds = append(cp.filteredCmds, cmd)
		}
	}

	// Alias matches
	for _, cmd := range cp.commands {
		for _, alias := range cmd.Aliases {
			if strings.ToLower(alias) == input {
				if !cp.containsCommand(cp.filteredCmds, cmd) {
					cp.filteredCmds = append(cp.filteredCmds, cmd)
				}
			}
		}
	}

	// Prefix matches
	for _, cmd := range cp.commands {
		if strings.HasPrefix(strings.ToLower(cmd.Name), input) {
			if !cp.containsCommand(cp.filteredCmds, cmd) {
				cp.filteredCmds = append(cp.filteredCmds, cmd)
			}
		}
	}

	// Fuzzy matches
	for _, cmd := range cp.commands {
		if cp.fuzzyMatch(input, strings.ToLower(cmd.Name)) ||
			cp.fuzzyMatch(input, strings.ToLower(cmd.Description)) {
			if !cp.containsCommand(cp.filteredCmds, cmd) {
				cp.filteredCmds = append(cp.filteredCmds, cmd)
			}
		}
	}

	// Sort by relevance (already sorted by match type above)
	if len(cp.filteredCmds) > 1 {
		// Additional sorting by category and name
		sort.Slice(cp.filteredCmds[1:], func(i, j int) bool {
			if cp.filteredCmds[i+1].Category != cp.filteredCmds[j+1].Category {
				return cp.filteredCmds[i+1].Category < cp.filteredCmds[j+1].Category
			}
			return cp.filteredCmds[i+1].Name < cp.filteredCmds[j+1].Name
		})
	}
}

// fuzzyMatch performs a simple fuzzy match.
func (cp *CommandPaletteModel) fuzzyMatch(pattern, text string) bool {
	patternIdx := 0
	for _, ch := range text {
		if patternIdx < len(pattern) && byte(ch) == pattern[patternIdx] {
			patternIdx++
		}
	}
	return patternIdx == len(pattern)
}

// containsCommand checks if a command is in the list.
func (cp *CommandPaletteModel) containsCommand(cmds []Command, cmd Command) bool {
	for _, c := range cmds {
		if c.Name == cmd.Name {
			return true
		}
	}
	return false
}

// parseAndExecute parses and executes a typed command.
func (cp *CommandPaletteModel) parseAndExecute(input string) tea.Cmd {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	// Try to match multi-word command names or aliases by progressively joining parts
	var matched *Command
	var consumed int
	for i := len(parts); i >= 1; i-- {
		candidate := strings.Join(parts[:i], " ")
		for idx := range cp.commands {
			c := &cp.commands[idx]
			if strings.EqualFold(c.Name, candidate) {
				matched = c
				consumed = i
				break
			}
			for _, alias := range c.Aliases {
				if strings.EqualFold(alias, candidate) {
					matched = c
					consumed = i
					break
				}
			}
			if matched != nil {
				break
			}
		}
		if matched != nil {
			break
		}
	}

	if matched == nil {
		return nil
	}

	// Remaining parts are arguments
	args := parts[consumed:]

	// Map positional args into the command's declared Args
	argMap := make(map[string]string)
	for i, arg := range matched.Args {
		if i < len(args) {
			argMap[arg.Name] = args[i]
		} else if arg.Default != "" {
			argMap[arg.Name] = arg.Default
		} else if arg.Required {
			return nil
		}
	}

	return cp.executeCommandWithArgs(*matched, argMap)
}

// executeCommand executes a command by sending it to handleCommand.
func (cp *CommandPaletteModel) executeCommand(cmd Command) tea.Cmd {
	return func() tea.Msg {
		return ExecuteCommandMsg{Command: cmd.Name, Args: []string{}}
	}
}

// executeCommandWithArgs executes a command with arguments.
func (cp *CommandPaletteModel) executeCommandWithArgs(cmd Command, args map[string]string) tea.Cmd {
	// Convert args map to slice for handleCommand
	var argSlice []string
	for _, arg := range cmd.Args {
		if val, ok := args[arg.Name]; ok {
			argSlice = append(argSlice, val)
		}
	}
	return func() tea.Msg {
		return ExecuteCommandMsg{Command: cmd.Name, Args: argSlice}
	}
}

// addToHistory adds a command to history.
func (cp *CommandPaletteModel) addToHistory(cmd string) {
	cp.history = append(cp.history, cmd)
	if len(cp.history) > 100 {
		cp.history = cp.history[1:]
	}
	cp.historyIdx = len(cp.history)
}

// Public methods

// Show shows the command palette.
func (cp *CommandPaletteModel) Show() {
	cp.visible = true
	cp.input.SetValue("")
	cp.input.Focus()
	cp.filterCommands("")
	cp.selected = 0
	// Ensure sensible defaults before WindowSize is delivered
	if cp.width <= 0 {
		cp.width = cp.input.Width() + 4
	}
	if cp.height <= 0 {
		cp.height = cp.maxHeight
	}
}

// Hide hides the command palette.
func (cp *CommandPaletteModel) Hide() {
	cp.visible = false
	cp.input.Blur()
	cp.input.SetValue("")
}

// IsVisible returns whether the palette is visible.
func (cp *CommandPaletteModel) IsVisible() bool {
	return cp.visible
}

// RegisterCommand registers a new command.
func (cp *CommandPaletteModel) RegisterCommand(cmd Command) {
	cp.commands = append(cp.commands, cmd)
}

// SetHandler sets the handler for a command.
func (cp *CommandPaletteModel) SetHandler(name string, handler CommandHandler) {
	for i := range cp.commands {
		if cp.commands[i].Name == name {
			cp.commands[i].Handler = handler
			break
		}
	}
}

// CommandResultMsg is sent when a command completes.
type CommandResultMsg struct {
	Result string
	Error  error
}

// ExecuteCommandMsg is sent when a command should be executed via handleCommand.
type ExecuteCommandMsg struct {
	Command string
	Args    []string
}

// InsertCommandMsg is sent when a command should be inserted into the input field.
type InsertCommandMsg struct {
	Command string
}
