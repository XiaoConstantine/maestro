package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/c-bata/go-prompt"
	"github.com/logrusorgru/aurora"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"path/filepath"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	
	"github.com/XiaoConstantine/maestro/terminal"

	_ "github.com/mattn/go-sqlite3"
)

type config struct {
	apiKey        string
	githubToken   string
	owner         string
	memoryPath    string
	repo          string
	prNumber      int
	verbose       bool
	verifyOnly    bool
	modelProvider string
	modelName     string
	modelConfig   string // For additional model-specific configuration

	indexWorkers  int // Number of concurrent workers for indexing
	reviewWorkers int // Number of concurrent workers for review
}

const (
	DefaultModelProvider = "llamacpp:"
	DefaultModelName     = "llamacpp:"
)

type ModelChoice struct {
	ID          core.ModelID
	Provider    string
	DisplayName string
	Description string
}

func commandCompleter(d prompt.Document) []prompt.Suggest {
	// Commands with their descriptions - matching Claude Code style
	commandSuggestions := []prompt.Suggest{
		{Text: "/help", Description: "Show available commands"},
		{Text: "/exit", Description: "Exit the application"},
		{Text: "/quit", Description: "Exit the application"},
		{Text: "/review", Description: "Review a specific pull request"},
		{Text: "/ask", Description: "Ask a specific question about the repository"},
		{Text: "/claude", Description: "Interact with Claude Code CLI"},
		{Text: "/gemini", Description: "Interact with Gemini CLI"},
		{Text: "/tools", Description: "Manage CLI tools (setup, status)"},
		{Text: "/sessions", Description: "Manage Claude sessions"},
		{Text: "/dashboard", Description: "Show Claude sessions dashboard"},
		{Text: "/coordinate", Description: "Coordinate multi-session tasks"},
		{Text: "/switch", Description: "Switch between Claude sessions"},
		{Text: "/enter", Description: "Enter interactive mode with current session"},
		{Text: "/list", Description: "List all available sessions"},
	}

	text := d.TextBeforeCursor()

	// Always show suggestions when starting with "/" - like Claude Code
	if strings.HasPrefix(text, "/") {
		// Check if we're typing a command or its arguments
		parts := strings.Fields(text)

		if len(parts) == 1 {
			// We're still typing the command itself - filter and return matches
			return prompt.FilterHasPrefix(commandSuggestions, text, true)
		} else if len(parts) > 1 {
			// We're typing arguments for a command
			command := parts[0]

			// Add argument-specific completions
			switch command {
			case "/review":
				// You could potentially fetch recent PR numbers here
				return []prompt.Suggest{
					{Text: parts[0] + " 123", Description: "Recent PR #123"},
					{Text: parts[0] + " 456", Description: "Recent PR #456"},
				}
			case "/ask":
				// You could suggest common questions or topics
				return []prompt.Suggest{
					{Text: parts[0] + " How does the codebase handle errors?", Description: "Error handling patterns"},
					{Text: parts[0] + " What's the project structure?", Description: "Code organization"},
				}
			}
		}
		return prompt.FilterHasPrefix(commandSuggestions, text, true)
	}
	return []prompt.Suggest{}
}

func getModelChoices() []ModelChoice {
	// Let's first create a description map for our providers
	providerDescriptions := map[string]string{
		"anthropic": "Cloud-based models with strong performance across various tasks",
		"google":    "Google's latest language models with diverse capabilities",
		"ollama":    "Run any supported model locally through Ollama - includes Llama, Mistral, and more",
		"llamacpp":  "Run models locally using LLaMA.cpp backend - supports various model formats",
	}

	// Create model-specific descriptions
	modelDescriptions := map[core.ModelID]string{
		core.ModelAnthropicHaiku:            "Fastest Claude model, optimized for quick tasks and real-time interactions",
		core.ModelAnthropicSonnet:           "Balanced performance model suitable for most tasks",
		core.ModelAnthropicOpus:             "Most powerful Claude model for complex reasoning and analysis",
		core.ModelGoogleGeminiFlash:         "Fast Google model focused on quick response times",
		core.ModelGoogleGeminiPro:           "Advanced Google model for sophisticated tasks",
		core.ModelGoogleGeminiFlashThinking: "Enhanced reasoning capabilities with rapid response times",
	}

	var choices []ModelChoice

	// Define the order in which we want to present providers
	providerOrder := []string{"llamacpp", "ollama", "anthropic", "google"}

	titleCaser := cases.Title(language.English)
	// Iterate through providers in our desired order
	for _, provider := range providerOrder {
		models, exists := core.ProviderModels[provider]
		if !exists {
			continue
		}

		// Handle local providers (ollama and llamacpp) specially
		if provider == "ollama" || provider == "llamacpp" {
			// Create a single entry for each local provider
			choices = append(choices, ModelChoice{
				ID:          core.ModelID(provider + ":"), // Creates "ollama:" or "llamacpp:"
				Provider:    provider,
				DisplayName: titleCaser.String(provider), // Capitalizes first letter
				Description: providerDescriptions[provider],
			})
			continue
		}

		// For cloud providers, add each specific model
		for _, modelID := range models {
			displayName := formatDisplayName(string(modelID))
			description := modelDescriptions[modelID]

			choices = append(choices, ModelChoice{
				ID:          modelID,
				Provider:    provider,
				DisplayName: displayName,
				Description: description,
			})
		}
	}

	return choices
}

func formatDisplayName(modelID string) string {
	titleCaser := cases.Title(language.English)

	// Remove provider prefixes if present
	name := strings.TrimPrefix(modelID, "anthropic:")
	name = strings.TrimPrefix(name, "google:")

	// Convert hyphens to spaces and title case each word
	words := strings.Split(name, "-")
	for i, word := range words {
		// Special handling for version numbers and abbreviations
		if !strings.ContainsAny(word, "0123456789") {
			words[i] = titleCaser.String(word)
		}
	}

	return strings.Join(words, " ")
}

func findSelectedChoice(selectedOption string, choices []ModelChoice, options []string) *ModelChoice {
	// Skip empty selections
	if selectedOption == "" {
		return nil
	}

	// Skip section headers
	if strings.HasPrefix(selectedOption, "===") {
		return nil
	}

	// Find the index of the selected option in our display list
	optionIndex := -1
	for i, opt := range options {
		if opt == selectedOption {
			optionIndex = i
			break
		}
	}

	if optionIndex == -1 {
		return nil
	}

	// Count non-header options to find the corresponding choice
	modelIndex := 0
	for _, opt := range options[:optionIndex] {
		if !strings.HasPrefix(opt, "===") {
			modelIndex++
		}
	}

	// Ensure we don't exceed our choices slice bounds
	if modelIndex >= len(choices) {
		return nil
	}

	return &choices[modelIndex]
}

func handleAPIKeySetup(cfg *config, choice *ModelChoice) error {
	// First try to get the API key from environment variables
	key, err := checkProviderAPIKey(choice.Provider, "")
	if err == nil {
		// Found a valid key in environment variables
		cfg.apiKey = key
		return nil
	}

	// If no environment variable is found, prompt the user
	var apiKey string
	apiKeyPrompt := &survey.Password{
		Message: fmt.Sprintf("Enter API key for %s:", choice.DisplayName),
		Help: fmt.Sprintf(`Required for %s models. 
You can also set this via environment variables:
- Anthropic: ANTHROPIC_API_KEY or CLAUDE_API_KEY
- Google: GOOGLE_API_KEY or GEMINI_API_KEY`, choice.Provider),
	}

	if err := survey.AskOne(apiKeyPrompt, &apiKey); err != nil {
		return fmt.Errorf("failed to get API key: %w", err)
	}

	// Basic validation of the provided key
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("API key cannot be empty")
	}

	cfg.apiKey = apiKey
	return nil
}

// Create an interactive command system using Cobra.
func createInteractiveCommands(cfg *config, console ConsoleInterface, agent ReviewAgent, mcpHelper *MCPBashHelper) *cobra.Command {
	ctx := context.Background()

	// Initialize CLI tool manager
	cliManager := NewCLIToolManager(logging.GetLogger(), console)

	// Initialize Claude session management
	claudeSessionManager := NewClaudeSessionManager(logging.GetLogger(), console)
	claudeCoordinator := NewClaudeCoordinator(claudeSessionManager, logging.GetLogger(), console)
	claudeDashboard := NewClaudeDashboard(claudeSessionManager, claudeCoordinator, console)
	claudeSessionSwitcher := NewSessionSwitcher(claudeSessionManager, claudeDashboard, console)

	// Create a root command for interactive mode
	rootCmd := &cobra.Command{
		Use:   "maestro",
		Short: "Maestro interactive shell",
		Run: func(cmd *cobra.Command, args []string) {
			// Default behavior when no subcommand is specified
			if len(args) > 0 {
				// Ask the agent directly for general input
				input := strings.Join(args, " ")
				if err := askQuestionWithAgent(ctx, cfg, console, agent, input); err != nil {
					console.Printf("Error: %v\n", err)
				}
			}
		},
	}

	// Help command
	helpCmd := &cobra.Command{
		Use:   "help",
		Short: "Show available commands",
		Run: func(cmd *cobra.Command, args []string) {
			showHelpMessage(console)
		},
	}
	rootCmd.AddCommand(helpCmd)

	// Exit command
	exitCmd := &cobra.Command{
		Use:   "exit",
		Short: "Exit the application",
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(0)
		},
	}
	rootCmd.AddCommand(exitCmd)

	// Quit command (alias for exit)
	quitCmd := &cobra.Command{
		Use:   "quit",
		Short: "Exit the application",
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(0)
		},
	}
	rootCmd.AddCommand(quitCmd)

	// Review command
	reviewCmd := &cobra.Command{
		Use:   "review [PR-NUMBER]",
		Short: "Review a specific pull request",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			prNumber, err := strconv.Atoi(args[0])
			if err != nil {
				console.Println("Invalid PR number. Please use a number.")
				return
			}

			// Run full PR review process
			if err := runFullPRReview(ctx, prNumber, cfg, console, agent, mcpHelper); err != nil {
				console.Printf("Error: %v\n", err)
			}
		},
	}
	rootCmd.AddCommand(reviewCmd)

	// Ask command
	askCmd := &cobra.Command{
		Use:   "ask [QUESTION]",
		Short: "Ask a specific question about the repository",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			question := strings.Join(args, " ")
			if err := askQuestionWithAgent(ctx, cfg, console, agent, question); err != nil {
				console.Printf("Error: %v\n", err)
			}
		},
	}
	rootCmd.AddCommand(askCmd)

	// Claude CLI command
	claudeCmd := &cobra.Command{
		Use:   "claude [ARGS...]",
		Short: "Interact with Claude Code CLI",
		Run: func(cmd *cobra.Command, args []string) {
			if err := cliManager.ExecuteTool(ctx, ClaudeCode, args); err != nil {
				console.Printf("Error: %v\n", err)
			}
		},
	}
	rootCmd.AddCommand(claudeCmd)

	// Gemini CLI command
	geminiCmd := &cobra.Command{
		Use:   "gemini [ARGS...]",
		Short: "Interact with Gemini CLI",
		Run: func(cmd *cobra.Command, args []string) {
			if err := cliManager.ExecuteTool(ctx, GeminiCLI, args); err != nil {
				console.Printf("Error: %v\n", err)
			}
		},
	}
	rootCmd.AddCommand(geminiCmd)

	// Tools management command
	toolsCmd := &cobra.Command{
		Use:   "tools [setup|status]",
		Short: "Manage CLI tools (setup, status)",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				// Default to showing status
				if err := cliManager.CheckToolStatus(ctx); err != nil {
					console.Printf("Error: %v\n", err)
				}
				return
			}

			switch args[0] {
			case "setup":
				if err := cliManager.InteractiveSetup(ctx); err != nil {
					console.Printf("Error: %v\n", err)
				}
			case "status":
				if err := cliManager.CheckToolStatus(ctx); err != nil {
					console.Printf("Error: %v\n", err)
				}
			default:
				console.Printf("Unknown tools command: %s. Use 'setup' or 'status'\n", args[0])
			}
		},
	}
	rootCmd.AddCommand(toolsCmd)

	// Claude session management commands
	sessionsCmd := &cobra.Command{
		Use:   "sessions [action] [args...]",
		Short: "Manage Claude sessions",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				// Default to showing overview
				claudeDashboard.ShowOverview()
				return
			}

			command := strings.Join(args, " ")
			if err := claudeDashboard.HandleCommand(ctx, command); err != nil {
				console.Printf("Error: %v\n", err)
			}
		},
	}
	rootCmd.AddCommand(sessionsCmd)

	// Dashboard command
	dashboardCmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Show Claude sessions dashboard",
		Run: func(cmd *cobra.Command, args []string) {
			claudeDashboard.ShowOverview()
		},
	}
	rootCmd.AddCommand(dashboardCmd)

	// Coordinate command
	coordinateCmd := &cobra.Command{
		Use:   "coordinate [TASK_DESCRIPTION...]",
		Short: "Coordinate multi-session tasks",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				console.Printf("Please provide a task description to coordinate\n")
				return
			}

			description := strings.Join(args, " ")
			if err := claudeCoordinator.CoordinateMultiSessionTask(ctx, description); err != nil {
				console.Printf("Error: %v\n", err)
			}
		},
	}
	rootCmd.AddCommand(coordinateCmd)

	// Session switching commands
	switchCmd := &cobra.Command{
		Use:   "switch [SESSION_NAME_OR_ID]",
		Short: "Switch between Claude sessions",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				claudeSessionSwitcher.ListAvailableSessions()
				return
			}

			identifier := args[0]
			if err := claudeSessionSwitcher.SwitchToSession(identifier); err != nil {
				console.Printf("Error: %v\n", err)
			}
		},
	}
	rootCmd.AddCommand(switchCmd)

	// Enter interactive session mode
	enterCmd := &cobra.Command{
		Use:   "enter [SESSION_NAME_OR_ID]",
		Short: "Enter interactive mode with a Claude session",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				// Switch to session first, then enter
				if err := claudeSessionSwitcher.QuickSwitch(ctx, args[0]); err != nil {
					console.Printf("Error: %v\n", err)
				}
			} else {
				// Enter with current session
				if err := claudeSessionSwitcher.EnterInteractiveMode(ctx); err != nil {
					console.Printf("Error: %v\n", err)
				}
			}
		},
	}
	rootCmd.AddCommand(enterCmd)

	// List sessions command
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all available Claude sessions",
		Run: func(cmd *cobra.Command, args []string) {
			claudeSessionSwitcher.ListAvailableSessions()
		},
	}
	rootCmd.AddCommand(listCmd)

	return rootCmd
}

// askQuestionWithAgent uses the pre-initialized agent to ask questions.
func askQuestionWithAgent(ctx context.Context, cfg *config, console ConsoleInterface, agent ReviewAgent, question string) error {
	qaProcessor, _ := agent.Orchestrator(ctx).GetProcessor("repo_qa")

	result, err := qaProcessor.Process(ctx, agents.Task{
		ID: "qa",
		Metadata: map[string]interface{}{
			"question": question,
		},
	}, nil)

	if err != nil {
		return fmt.Errorf("error processing question: %w", err)
	}

	if response, ok := result.(*QAResponse); ok {
		// Print a separator line for visual clarity
		console.Println("\n" + strings.Repeat("─", 80))

		// Format and print the main answer using structured sections
		formattedAnswer := formatStructuredAnswer(response.Answer)
		console.Println(formattedAnswer)

		// Print source files in a tree-like structure if available
		if len(response.SourceFiles) > 0 {
			if console.Color() {
				console.Println("\n" + aurora.Blue("Source Files:").String())
			} else {
				console.Println("\nSource Files:")
			}

			// Group files by directory for better organization
			filesByDir := groupFilesByDirectory(response.SourceFiles)
			printFileTree(console, filesByDir)
		}

		// Print final separator
		console.Println("\n" + strings.Repeat("─", 80) + "\n")
	}

	return nil
}

func printMaestroBanner() {
	// Get a colored output that works with your terminal
	au := aurora.NewAurora(true)

	width, _, err := term.GetSize(0) // Increased to accommodate our ASCII art

	if err != nil {
		width = 60 // Fallback
	}
	frameWidth := width - 2

	// Define box drawing characters
	topBorderStr := "╭" + strings.Repeat("─", frameWidth) + "╮"
	bottomBorderStr := "╰" + strings.Repeat("─", frameWidth) + "╯"
	sideStr := "│"

	// Welcome message with padding
	welcomeMsg := "✨ Welcome to Maestro - Your AI Code Assistant! ✨"

	// Print the top border with coral color
	fmt.Println(au.Index(209, topBorderStr))

	// Center the welcome message
	msgPadding := max(0, (frameWidth-len(welcomeMsg))/2)

	paddedWelcome := strings.Repeat(" ", msgPadding) + welcomeMsg

	if len(paddedWelcome) > frameWidth {
		paddedWelcome = paddedWelcome[:frameWidth]
	} else {
		// Add right padding to fill the frame
		rightPadding := frameWidth - len(paddedWelcome)
		paddedWelcome += strings.Repeat(" ", rightPadding)
	}
	fmt.Printf("%s %s %s\n",
		au.Index(209, sideStr),
		paddedWelcome,
		au.Index(209, sideStr))
	fmt.Printf("%s%s%s\n",
		au.Index(209, sideStr),
		strings.Repeat(" ", frameWidth),
		au.Index(209, sideStr))

	fmt.Println(au.Index(209, bottomBorderStr))

	// The thick ASCII art for MAESTRO using block characters for a layered effect
	maestroThick := []string{
		"███╗   ███╗ █████╗ ███████╗███████╗████████╗██████╗  ██████╗ ",
		"████╗ ████║██╔══██╗██╔════╝██╔════╝╚══██╔══╝██╔══██╗██╔═══██╗",
		"██╔████╔██║███████║█████╗  ███████╗   ██║   ██████╔╝██║   ██║",
		"██║╚██╔╝██║██╔══██║██╔══╝  ╚════██║   ██║   ██╔══██╗██║   ██║",
		"██║ ╚═╝ ██║██║  ██║███████╗███████║   ██║   ██║  ██║╚██████╔╝",
		"╚═╝     ╚═╝╚═╝  ╚═╝╚══════╝╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝ ",
	}
	for _, line := range maestroThick {
		// Calculate centering based on terminal width, not frame width
		padding := max(0, (width-len(line))/2)
		paddedLine := strings.Repeat(" ", padding) + line

		// Print the line without side borders
		fmt.Printf("%s\n", au.Index(209, paddedLine))
	}
}

func main() {
	cfg := &config{}
	sqlite_vec.Auto()

	// Initialize enhanced DSPy-Go features
	InitializeEnhancedFeatures()
	// Create root command
	rootCmd := &cobra.Command{
		Use:   "Maestro",
		Short: "Maestro - AI Code Assistant",
		Long: `Maestro is an AI-powered code assistant with Claude Code-inspired interface
that helps you review PRs and analyze code through interactive sessions.

Interactive mode with clean terminal interface:
  maestro -i  (or --interactive)

Available slash commands in interactive mode:
  /help                   - Show help for available commands
  /review <PR-NUMBER>     - Review a specific pull request
  /ask <QUESTION>         - Ask a question about the repository
  /exit or /quit          - Exit the application`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("model") {
				modelStr, _ := cmd.Flags().GetString("model")
				provider, name, config := parseModelString(modelStr)
				if provider != "" {
					cfg.modelProvider = provider
				}
				if name != "" {
					cfg.modelName = name
				}
				if config != "" {
					cfg.modelConfig = config
				}
			}

			interactive, _ := cmd.Flags().GetBool("interactive")
			modernUI, _ := cmd.Flags().GetBool("ui")
			
			// Handle modern UI flag
			if modernUI {
				return runModernUI(cfg)
			}
			
			// Force interactive mode if no PR number is specified, even when other flags are set
			if interactive || cmd.Flags().NFlag() == 0 || cfg.prNumber == 0 {
				return runInteractiveMode(cfg)
			}
			return runCLI(cfg)
		},
	}

	// Add flags
	rootCmd.PersistentFlags().StringVar(&cfg.apiKey, "api-key", "", "API Key for vendors")
	rootCmd.PersistentFlags().StringVar(&cfg.githubToken, "github-token", os.Getenv("MAESTRO_GITHUB_TOKEN"), "Github token")
	rootCmd.PersistentFlags().StringVar(&cfg.owner, "owner", "", "Repository owner")
	rootCmd.PersistentFlags().StringVar(&cfg.repo, "repo", "", "Repository")
	rootCmd.PersistentFlags().StringVar(&cfg.memoryPath, "path", "~/.maestro/", "Path for sqlite table")
	rootCmd.PersistentFlags().IntVar(&cfg.prNumber, "pr", 0, "Pull request number")
	rootCmd.PersistentFlags().BoolVar(&cfg.verbose, "verbose", false, "Enable verbose logging")
	rootCmd.PersistentFlags().BoolVar(&cfg.verifyOnly, "verify-only", false, "Only verify token permissions")

	rootCmd.PersistentFlags().BoolP("interactive", "i", false, "Run in interactive mode")
	rootCmd.PersistentFlags().Bool("ui", false, "Use modern terminal UI (experimental)")

	rootCmd.PersistentFlags().StringP("model", "m", "", `Full model specification (e.g. "ollama:mistral:q4", "llamacpp:", "anthropic:claude-3")`)
	rootCmd.PersistentFlags().StringVar(&cfg.modelProvider, "provider", DefaultModelProvider, "Model provider (llamacpp, ollama, anthropic)")
	rootCmd.PersistentFlags().StringVar(&cfg.modelName, "model-name", DefaultModelName, "Specific model name")
	rootCmd.PersistentFlags().StringVar(&cfg.modelConfig, "model-config", "", "Additional model configuration")

	rootCmd.PersistentFlags().IntVar(&cfg.indexWorkers, "index-workers", runtime.NumCPU(), "Number of concurrent workers for repository indexing")

	rootCmd.PersistentFlags().IntVar(&cfg.reviewWorkers, "review-workers", runtime.NumCPU(), "Number of concurrent workers for parallel review")

	// Mark required flags
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if cfg.githubToken == "" {
			fmt.Fprintln(os.Stderr, "GitHub token required via --github-token or MAESTRO_GITHUB_TOKEN")
			os.Exit(1)
		}
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runCLI(cfg *config) error {
	printMaestroBanner()
	return runCLIWithoutBanner(cfg)
}

// runCLIWithoutBanner contains the core CLI logic without printing the banner.
func runCLIWithoutBanner(cfg *config) error {
	ctx := core.WithExecutionState(context.Background())
	output := logging.NewConsoleOutput(true, logging.WithColor(true))
	logLevel := logging.INFO

	fileOutput, _ := logging.NewFileOutput(
		filepath.Join(".", "dspy.log"),
		logging.WithRotation(100*1024*1024, 5), // 10MB max size, keep 5 files
		logging.WithJSONFormat(true),           // Use JSON format
	)
	var err error
	if cfg.verbose {
		logLevel = logging.DEBUG

	}
	logger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  []logging.Output{output, fileOutput},
	})
	logging.SetLogger(logger)

	console := NewConsole(os.Stdout, logger, nil)
	err = validateModelConfig(cfg)
	if err != nil {
		logger.Error(ctx, "Model config is incorrect: %v", err)
		os.Exit(1)
	}
	err = console.WithSpinner(ctx, "Verifying permissions...", func() error {
		return VerifyTokenPermissions(ctx, cfg.githubToken, cfg.owner, cfg.repo)
	})
	if err != nil {
		logger.Error(ctx, "Token permission verification failed: %v", err)
		os.Exit(1)
	}

	if cfg.verifyOnly {
		os.Exit(0)
	}
	llms.EnsureFactory()

	modelID := constructModelID(cfg)
	err = core.ConfigureDefaultLLM(cfg.apiKey, modelID)

	if err != nil {
		logger.Error(ctx, "Failed to configure LLM: %v", err)
	}
	// Use unified embedding model for both code and guidelines
	// Configure teacher LLM with a Gemini generation model that supports embeddings
	if err := core.ConfigureTeacherLLM(cfg.apiKey, core.ModelGoogleGeminiPro); err != nil {
		return fmt.Errorf("failed to configure teacher LLM: %w", err)
	}
	githubTools := NewGitHubTools(cfg.githubToken, cfg.owner, cfg.repo)

	// Initialize MCP bash helper for GitHub operations
	var mcpHelper *MCPBashHelper
	if helper, err := NewMCPBashHelper(); err != nil {
		logger.Warn(ctx, "Failed to initialize MCP bash helper: %v", err)
		logger.Info(ctx, "Falling back to GitHub API for PR operations")
		mcpHelper = nil
	} else {
		mcpHelper = helper
		// Ensure cleanup on exit
		defer mcpHelper.Close()
		logger.Debug(ctx, "MCP bash helper initialized successfully")
	}

	dbPath, err := CreateStoragePath(ctx, cfg.owner, cfg.repo)
	// Check if we already have an index for this commit
	if err != nil {
		panic(err)
	}
	agent, err := NewPRReviewAgent(ctx, githubTools, dbPath, &AgentConfig{
		IndexWorkers:  cfg.indexWorkers,
		ReviewWorkers: cfg.reviewWorkers,
	})
	if err != nil {
		panic(err)
	}

	// Validate PR number
	if cfg.prNumber <= 0 {
		logger.Error(ctx, "Invalid PR number: %d. Please specify a valid PR number with --pr flag", cfg.prNumber)
		return fmt.Errorf("invalid PR number %d", cfg.prNumber)
	}

	return runFullPRReview(ctx, cfg.prNumber, cfg, console, agent, mcpHelper)
}

// runFullPRReview executes the complete PR review process.
func runFullPRReview(ctx context.Context, prNumber int, cfg *config, console ConsoleInterface, agent ReviewAgent, mcpHelper *MCPBashHelper) error {
	logger := logging.GetLogger()

	githubTools := NewGitHubTools(cfg.githubToken, cfg.owner, cfg.repo)

	// Fetching PR changes
	if console.Color() {
		console.Printf("%s %s %s\n",
			aurora.Blue("↳").Bold(), // Arrow indicator for fetching
			aurora.White("Fetching changes for PR").Bold(),
			aurora.Cyan(fmt.Sprintf("#%d", prNumber)).Bold(),
		)
	} else {
		console.Printf("↳ Fetching changes for PR #%d\n", prNumber)
	}
	pr, _, err := githubTools.Client().PullRequests.Get(ctx, cfg.owner, cfg.repo, prNumber)
	if err != nil {
		logger.Error(ctx, "Failed to get PR #%d: %v", prNumber, err)
		return fmt.Errorf("PR #%d not found: %w", prNumber, err)
	}
	console.StartReview(pr)

	var changes *PRChanges

	// Use MCP if available, otherwise fall back to GitHub API
	if mcpHelper != nil {
		logger.Debug(ctx, "Using MCP bash helper to fetch PR changes")
		changes, err = GetPullRequestChangesWithMCP(ctx, cfg.owner, cfg.repo, prNumber, mcpHelper)
	} else {
		logger.Debug(ctx, "Using GitHub API to fetch PR changes")
		changes, err = githubTools.GetPullRequestChanges(ctx, prNumber)
	}

	if err != nil {
		logger.Error(ctx, "Failed to get PR changes: %v", err)
		return fmt.Errorf("failed to get PR changes: %w", err)
	}
	tasks := make([]PRReviewTask, 0, len(changes.Files))
	for _, file := range changes.Files {

		if console.Color() {
			console.Printf("\n%s Processing file: %s %s\n",
				aurora.Blue("→").Bold(),
				aurora.Cyan(file.FilePath).Bold(),
				aurora.Gray(12, fmt.Sprintf("(+%d/-%d lines)", file.Additions, file.Deletions)),
			)
		} else {
			console.Printf("\n→ Processing file: %s (+%d/-%d lines)\n",
				file.FilePath, file.Additions, file.Deletions)
		}
		// File being processed

		tasks = append(tasks, PRReviewTask{
			FilePath:    file.FilePath,
			FileContent: file.FileContent,
			Changes:     file.Patch,
		})
	}

	if console.Color() {
		console.Printf("%s %s %s\n",
			aurora.Green("⚡").Bold(),
			aurora.White(fmt.Sprintf("Starting code review for %d %s",
				len(tasks),
				pluralize("file", len(tasks)))).Bold(),
			aurora.Blue("...").String(),
		)
	} else {
		console.Printf("⚡ Starting code review for %d %s...\n",
			len(tasks),
			pluralize("file", len(tasks)))
	}
	// Starting code review
	// Note: Don't stop the agent here in interactive mode - it's managed at the session level
	comments, err := agent.ReviewPRWithChanges(ctx, prNumber, tasks, console, changes)
	if err != nil {
		logger.Error(ctx, "Failed to review PR: %v", err)
		return fmt.Errorf("failed to review PR: %w", err)
	}
	if len(comments) != 0 {

		shouldPost, err := githubTools.PreviewReview(ctx, console, prNumber, comments, agent.Metrics(ctx))
		if err != nil {
			logger.Error(ctx, "Failed to preview review: %v", err)
			return fmt.Errorf("failed to preview review: %w", err)
		}

		console.ShowReviewMetrics(agent.Metrics(ctx), comments)

		if shouldPost {
			logger.Info(ctx, "Posting review comments to GitHub")
			err = githubTools.CreateReviewComments(ctx, prNumber, comments)
			if err != nil {
				logger.Error(ctx, "Failed to post review comments: %v", err)
				return fmt.Errorf("failed to post review comments: %w", err)
			}
		}
	}
	console.ReviewComplete()
	return nil
}

// getVectorDimensions returns the configured vector dimensions.
func getVectorDimensions() int {
	if dimensions := os.Getenv("MAESTRO_VECTOR_DIMENSIONS"); dimensions != "" {
		if dims, err := strconv.Atoi(dimensions); err == nil && dims > 0 && dims <= 2048 {
			return dims
		}
	}
	return 768 // Default for text-embedding-004
}

func runInteractiveMode(cfg *config) error {
	printMaestroBanner()
	ctx := core.WithExecutionState(context.Background())
	output := logging.NewConsoleOutput(true, logging.WithColor(true))
	logLevel := logging.INFO
	verbosePrompt := &survey.Confirm{
		Message: "Enable verbose logging?",
		Default: false,
	}
	if err := survey.AskOne(verbosePrompt, &cfg.verbose); err != nil {
		return fmt.Errorf("failed to get verbose preference: %w", err)
	}
	if cfg.verbose {
		logLevel = logging.DEBUG
	}
	logger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  []logging.Output{output},
	})
	logging.SetLogger(logger)
	console := NewConsole(os.Stdout, logger, nil)
	showHelpMessage(console)
	llms.EnsureFactory()

	if cfg.owner == "" {
		ownerPrompt := &survey.Input{
			Message: "Enter repository owner:",
		}
		if err := survey.AskOne(ownerPrompt, &cfg.owner); err != nil {
			return fmt.Errorf("failed to get owner: %w", err)
		}
	}

	// Only prompt for repo if not provided
	if cfg.repo == "" {
		repoPrompt := &survey.Input{
			Message: "Enter repository name:",
		}
		if err := survey.AskOne(repoPrompt, &cfg.repo); err != nil {
			return fmt.Errorf("failed to get repo: %w", err)
		}
	}

	// Skip concurrency customization for cleaner startup like Claude Code
	// Use sensible defaults silently

	// Optional settings
	if cfg.modelProvider == DefaultModelProvider && cfg.modelName == DefaultModelName {

		var useCustomModel bool
		modelPrompt := &survey.Confirm{
			Message: "Use custom model?",
			Default: false,
		}
		if err := survey.AskOne(modelPrompt, &useCustomModel); err != nil {
			return fmt.Errorf("failed to get model preference: %w", err)
		}

		if useCustomModel {
			titleCaser := cases.Title(language.English)
			choices := getModelChoices()

			// Create formatted options for the survey
			var options []string
			var sections = map[string]bool{} // Track where we need section headers
			currentProvider := ""
			for _, choice := range choices {
				// Add section header if we're switching providers
				if choice.Provider != currentProvider {
					currentProvider = choice.Provider
					if !sections[currentProvider] {
						sections[currentProvider] = true
						options = append(options, fmt.Sprintf("=== %s Models ===",
							titleCaser.String(currentProvider)))
					}
				}

				option := fmt.Sprintf("%-30s - %s", choice.DisplayName, choice.Description)
				options = append(options, option)
			}
			var selectedOption string
			selectPrompt := &survey.Select{
				Message: "Choose a model:",
				Options: options,
				Default: options[1], // Skip the first section header
				Help: `Use ↑↓ to navigate, Enter to select

Local Models (Ollama, LLaMA.cpp):
- Run locally on your machine
- No API keys required
- Support various model formats

Cloud Models (Anthropic, Google):
- Require API keys
- Hosted solutions with consistent performance
- Regular updates and improvements`,
			}
			if err := survey.AskOne(selectPrompt, &selectedOption, survey.WithPageSize(15)); err != nil {
				return fmt.Errorf("failed to get model selection: %w", err)
			}

			// Find the selected model and update configuration
			if selectedChoice := findSelectedChoice(selectedOption, choices, options); selectedChoice != nil {
				console.Printf("Select choice: %s", selectedChoice)
				// Update configuration based on selection
				cfg.modelProvider = selectedChoice.Provider
				cfg.modelName = string(selectedChoice.ID)

				if cfg.modelProvider == "ollama" {
					// Prompt user for specific Ollama model
					var ollamaModel string
					modelPrompt := &survey.Input{
						Message: "Enter Ollama model (e.g., mistral:q4, llama2:13b):",
						Help: `Format: modelname[:tag]
Examples:
- mistral:q4    (Mistral with 4-bit quantization)
- llama2:13b    (LLaMA 2 13B model)
- codellama     (default CodeLLaMA model)`,
					}

					if err := survey.AskOne(modelPrompt, &ollamaModel); err != nil {
						return fmt.Errorf("failed to get Ollama model: %w", err)
					}

					// Parse the Ollama model string
					if strings.Contains(ollamaModel, ":") {
						// If user provided model:tag format
						parts := strings.SplitN(ollamaModel, ":", 2)
						cfg.modelName = parts[0]
						cfg.modelConfig = parts[1]
					} else {
						// If user provided just the model name
						cfg.modelName = ollamaModel
					}
				}

				// Handle API key requirements for cloud providers
				if selectedChoice.Provider != "ollama" &&
					selectedChoice.Provider != "llamacpp" &&
					cfg.apiKey == "" {
					if err := handleAPIKeySetup(cfg, selectedChoice); err != nil {
						console.Printf("got error verify key: %v", err)

						return err
					}
				}
			}
		}
	}

	// Model configuration set
	modelID := constructModelID(cfg)

	if err := core.ConfigureDefaultLLM(cfg.apiKey, modelID); err != nil {
		return fmt.Errorf("failed to configure LLM: %w", err)
	}
	// Use unified embedding model for both code and guidelines
	// Configure teacher LLM with a Gemini generation model that supports embeddings
	if err := core.ConfigureTeacherLLM(cfg.apiKey, core.ModelGoogleGeminiPro); err != nil {
		return fmt.Errorf("failed to configure teacher LLM: %w", err)
	}

	// Initialize GitHub tools and agent immediately for background indexing
	githubTools := NewGitHubTools(cfg.githubToken, cfg.owner, cfg.repo)
	dbPath, err := CreateStoragePath(ctx, cfg.owner, cfg.repo)
	if err != nil {
		return fmt.Errorf("failed to create storage path: %w", err)
	}

	// Create agent immediately to start background indexing
	agent, err := NewPRReviewAgent(ctx, githubTools, dbPath, &AgentConfig{
		IndexWorkers:  cfg.indexWorkers,
		ReviewWorkers: cfg.reviewWorkers,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize agent: %w", err)
	}
	// Ensure agent cleanup on exit
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		agent.Stop(shutdownCtx)
	}()

	console.Println("Type /help or ask questions directly.")

	// Initialize persistent MCP bash helper
	var mcpHelper *MCPBashHelper
	if helper, err := NewMCPBashHelper(); err != nil {
		console.Printf("Warning: Failed to initialize MCP bash helper: %v\n", err)
		console.Println("GitHub operations will use fallback methods.")
		mcpHelper = nil
	} else {
		mcpHelper = helper
		// Ensure cleanup on exit
		defer mcpHelper.Close()
	}

	// Create interactive commands with the pre-initialized agent and MCP helper
	interactiveCmd := createInteractiveCommands(cfg, console, agent, mcpHelper)
	ctrlCPressed := false
	ctrlCTimer := time.NewTimer(0)
	ctrlCTimer.Stop() // Initialize in stopped state

	// Configure go-prompt options with Claude Code-inspired theme
	options := []prompt.Option{
		prompt.OptionTitle("Maestro AI Code Assistant"),
		prompt.OptionPrefix("> "), // Clean terminal-style prompt like Claude Code

		// Claude Code theme colors - minimal and clean
		prompt.OptionPrefixTextColor(prompt.White),

		// Minimal suggestion styling - dark background with subtle text
		prompt.OptionSuggestionBGColor(prompt.Black),
		prompt.OptionSuggestionTextColor(prompt.DarkGray),
		prompt.OptionDescriptionBGColor(prompt.Black),
		prompt.OptionDescriptionTextColor(prompt.LightGray),

		// Selected item styling - subtle highlight
		prompt.OptionSelectedSuggestionBGColor(prompt.DarkGray),
		prompt.OptionSelectedSuggestionTextColor(prompt.White),
		prompt.OptionSelectedDescriptionBGColor(prompt.DarkGray),
		prompt.OptionSelectedDescriptionTextColor(prompt.White),

		// Format and layout - make suggestions selectable like Claude Code
		prompt.OptionMaxSuggestion(10),                // Show more suggestions like Claude Code
		prompt.OptionCompletionWordSeparator(" \t\n"), // Standard word separators
		prompt.OptionAddKeyBind(
			// Handle Ctrl+C
			prompt.KeyBind{
				Key: prompt.ControlC,
				Fn: func(buf *prompt.Buffer) {
					if ctrlCPressed {
						// Second press - exit the program
						fmt.Println("\nExiting...")
						os.Exit(0)
					} else {
						// First press - show warning
						ctrlCPressed = true
						fmt.Println("\nPress Ctrl+C again to exit.")

						// Reset the flag after 3 seconds
						ctrlCTimer.Reset(3 * time.Second)
						go func() {
							<-ctrlCTimer.C
							ctrlCPressed = false
						}()
					}
				},
			},
		),
		// Enable arrow key navigation in suggestions
		prompt.OptionHistory([]string{}), // Initialize history

		// Input behavior - essential for proper suggestion navigation
		prompt.OptionInputTextColor(prompt.DefaultColor),
		prompt.OptionCompletionOnDown(), // Enable down arrow to enter completion mode
	}

	console.Println("")
	// Start the interactive prompt with auto-completion
	p := prompt.New(
		// Executor function runs when the user presses Enter
		func(input string) {

			ctrlCPressed = false
			// Exit condition
			if input == "exit" || input == "quit" || input == "/exit" || input == "/quit" {
				os.Exit(0)
			}

			// Handle command syntax
			if strings.HasPrefix(input, "/") {
				// Remove the leading slash for Cobra
				args := strings.TrimPrefix(input, "/")
				// Split into arguments
				argList := strings.Fields(args)

				// Set os.Args temporarily and execute the command
				oldArgs := os.Args
				os.Args = append([]string{"maestro"}, argList...)
				interactiveCmd.SetArgs(argList)
				err := interactiveCmd.Execute()
				os.Args = oldArgs

				if err != nil {
					console.Printf("Error: %v\n", err)
				}
			} else if input != "" {
				// Fallback to asking the agent for natural input
				if err := askQuestionWithAgent(ctx, cfg, console, agent, input); err != nil {
					console.Printf("Error: %v\n", err)
				}
			}
		},
		commandCompleter,
		options...,
	)
	p.Run()
	return nil
}

func runModernUI(cfg *config) error {
	printMaestroBanner()
	
	// Simple validation - just check if GitHub token is provided
	if cfg.githubToken == "" {
		fmt.Fprintln(os.Stderr, "GitHub token required via --github-token or MAESTRO_GITHUB_TOKEN")
		return fmt.Errorf("GitHub token required")
	}
	
	// Start the modern terminal UI
	return terminal.RunModernUI()
}

func showHelpMessage(c ConsoleInterface) {
	// Clean, minimal help like Claude Code
	commands := []struct {
		Command     string
		Description string
	}{
		{"/help", "Show available commands"},
		{"/ask", "Ask about the repository"},
		{"/review <PR>", "Review a pull request"},
		{"/sessions", "Manage sessions"},
		{"/tools", "Manage CLI tools"},
		{"/exit", "Exit"},
	}

	for _, cmd := range commands {
		if c.Color() {
			c.Printf("%-12s %s\n",
				aurora.Blue(cmd.Command).String(),
				aurora.Gray(12, cmd.Description).String())
		} else {
			c.Printf("%-12s %s\n", cmd.Command, cmd.Description)
		}
	}

	if c.Color() {
		c.Printf("\n%s\n", aurora.Gray(12, "Type a question or use tab completion with /").String())
	} else {
		c.Printf("\nType a question or use tab completion with /\n")
	}
}
