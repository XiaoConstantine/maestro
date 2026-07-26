package terminal

// builtinCommands is the canonical command registry for autocomplete and the
// command palette. It returns a fresh slice because palette handlers are
// attached at runtime.
func builtinCommands() []Command {
	return []Command{
		{
			Name:        "help",
			Aliases:     []string{"/help", "h", "?"},
			Description: "Show available commands",
			Category:    "Help",
		},
		{
			Name:        "review",
			Aliases:     []string{"/review"},
			Description: "Review a pull request",
			Category:    "GitHub",
			Args:        []CommandArg{{Name: "pr", Description: "PR number", Required: true}},
		},
		{
			Name:        "ask",
			Aliases:     []string{"/ask"},
			Description: "Ask a read-only repository question",
			Category:    "Maestro",
			Args:        []CommandArg{{Name: "question", Description: "Question text", Required: true}},
		},
		{
			Name:        "claude",
			Aliases:     []string{"/claude"},
			Description: "Send a prompt to the Claude subagent",
			Category:    "Subagent",
			Args:        []CommandArg{{Name: "prompt", Description: "Prompt text", Required: true}},
		},
		{
			Name:        "gemini",
			Aliases:     []string{"/gemini"},
			Description: "Send a prompt to the Gemini subagent",
			Category:    "Subagent",
			Args:        []CommandArg{{Name: "prompt", Description: "Prompt text", Required: true}},
		},
		{
			Name:        "model",
			Aliases:     []string{"/model"},
			Description: "Choose the model for the next run",
			Category:    "Session",
		},
		{
			Name:        "session new",
			Aliases:     []string{"/session new"},
			Description: "Create a session",
			Category:    "Session",
			Args:        []CommandArg{{Name: "name", Description: "Optional session name"}},
		},
		{
			Name:        "session switch",
			Aliases:     []string{"/session switch"},
			Description: "Switch sessions",
			Category:    "Session",
			Args:        []CommandArg{{Name: "name", Description: "Session name", Required: true}},
		},
		{
			Name:        "session list",
			Aliases:     []string{"/session list"},
			Description: "List sessions",
			Category:    "Session",
		},
		{
			Name:        "clear",
			Aliases:     []string{"/clear"},
			Description: "Clear the conversation",
			Category:    "System",
		},
		{
			Name:        "exit",
			Aliases:     []string{"/exit", "quit", "/quit"},
			Description: "Exit Maestro",
			Category:    "System",
		},
	}
}
