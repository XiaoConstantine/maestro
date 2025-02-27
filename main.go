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
	"github.com/logrusorgru/aurora"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"

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

func showConversationBox(c ConsoleInterface) {
	width, _, err := term.GetSize(0)
	if err != nil {
		width = 80 // Default if we can't get terminal width
	}

	// Create a box that takes most of the terminal width
	boxWidth := width - 4

	// Box drawing characters
	topBorder := "╭" + strings.Repeat("─", boxWidth) + "╮"
	bottomBorder := "╰" + strings.Repeat("─", boxWidth) + "╯"
	sideBorder := "│"

	// Display the box
	if c.Color() {
		c.Println(aurora.Cyan(topBorder).String())
		c.Println(aurora.Cyan(sideBorder).String() +
			strings.Repeat(" ", boxWidth) +
			aurora.Cyan(sideBorder).String())
		c.Println(aurora.Cyan(bottomBorder).String())
	} else {
		c.Println(topBorder)
		c.Println(sideBorder + strings.Repeat(" ", boxWidth) + sideBorder)
		c.Println(bottomBorder)
	}

	if c.Color() {
		prompt := aurora.Cyan("maestro> ").Bold().String()
		c.Printf("%s", prompt)
	} else {
		c.Printf("maestro> ")
	}
}

func main() {
	cfg := &config{}
	sqlite_vec.Auto()
	// Create root command
	rootCmd := &cobra.Command{
		Use:   "Maestro",
		Short: "Maestro - Code assistant",
		Long: `Maestro is an AI-powered code assistant that helps you review PR
and impl changes through interactive learning sessions.

Try the new conversational interface with slash commands:
  maestro -i  (or --interactive)

Available slash commands in conversation mode:
  /help                   - Show help for available commands
  /review <PR-NUMBER>     - Review a specific pull request
  /ask <QUESTION>         - Ask a question about the repository
  /compact                - Compact conversation history
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
			if interactive || cmd.Flags().NFlag() == 0 {
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
	ctx := core.WithExecutionState(context.Background())
	output := logging.NewConsoleOutput(true, logging.WithColor(true))
	logLevel := logging.INFO
	if cfg.verbose {
		logLevel = logging.DEBUG

	}
	logger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  []logging.Output{output},
	})
	logging.SetLogger(logger)

	console := NewConsole(os.Stdout, logger, nil)
	err := validateModelConfig(cfg)
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

	githubTools := NewGitHubTools(cfg.githubToken, cfg.owner, cfg.repo)

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

	logger.Debug(ctx, "Fetching changes for PR #%d", cfg.prNumber)
	// Before calling changes, err := githubTools.GetPullRequestChanges()
	if console.Color() {
		console.Printf("%s %s %s\n",
			aurora.Blue("↳").Bold(), // Arrow indicator for fetching
			aurora.White("Fetching changes for PR").Bold(),
			aurora.Cyan(fmt.Sprintf("#%d", cfg.prNumber)).Bold(),
		)
	} else {
		console.Printf("↳ Fetching changes for PR #%d\n", cfg.prNumber)
	}
	pr, _, _ := githubTools.Client().PullRequests.Get(ctx, cfg.owner, cfg.repo, cfg.prNumber)
	console.StartReview(pr)

	changes, err := githubTools.GetPullRequestChanges(ctx, cfg.prNumber)
	if err != nil {
		logger.Error(ctx, "Failed to get PR changes: %v", err)
		os.Exit(1)
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
		// Log file being processed
		logger.Debug(ctx, "Processing file: %s (+%d/-%d lines)",
			file.FilePath,
			file.Additions,
			file.Deletions,
		)

		tasks = append(tasks, PRReviewTask{
			FilePath:    file.FilePath,
			FileContent: file.FileContent,
			Changes:     file.Patch,
		})
	}
	if err != nil {
		logger.Error(ctx, "Failed to get PR changes: %v", err)
		os.Exit(1)
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
	logger.Debug(ctx, "Starting code review for %d files", len(tasks))
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		agent.Stop(shutdownCtx)
	}()
	comments, err := agent.ReviewPR(ctx, cfg.prNumber, tasks, console)
	if err != nil {
		logger.Error(ctx, "Failed to review PR: %v", err)
		os.Exit(1)
	}
	if len(comments) != 0 {

		shouldPost, err := githubTools.PreviewReview(ctx, console, cfg.prNumber, comments, agent.Metrics(ctx))
		if err != nil {
			logger.Error(ctx, "Failed to preview review: %v", err)
			os.Exit(1)
		}

		console.ShowReviewMetrics(agent.Metrics(ctx), comments)

		if shouldPost {
			logger.Info(ctx, "Posting review comments to GitHub")
			err = githubTools.CreateReviewComments(ctx, cfg.prNumber, comments)
			if err != nil {
				logger.Error(ctx, "Failed to post review comments: %v", err)
				os.Exit(1)
			}
		}
	}
	console.ReviewComplete()
	return nil
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
	ShowHelpMessage(console)
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

	var customizeConcurrency bool
	concurrencyPrompt := &survey.Confirm{
		Message: fmt.Sprintf("Customize concurrency settings? (currently using %d workers)", runtime.NumCPU()),
		Default: false,
	}
	if err := survey.AskOne(concurrencyPrompt, &customizeConcurrency); err != nil {
		return fmt.Errorf("failed to get concurrency preference: %w", err)
	}

	if customizeConcurrency {
		workersPrompt := &survey.Input{
			Message: fmt.Sprintf("Enter number of concurrent workers for indexing (current: %d):", cfg.indexWorkers),
			Default: strconv.Itoa(cfg.indexWorkers),
		}
		var workersStr string
		if err := survey.AskOne(workersPrompt, &workersStr); err != nil {
			return fmt.Errorf("failed to get worker count: %w", err)
		}
		if workers, err := strconv.Atoi(workersStr); err == nil && workers > 0 {
			cfg.indexWorkers = workers
		}

		workersPrompt = &survey.Input{
			Message: fmt.Sprintf("Enter number of concurrent workers for review (current: %d):", cfg.reviewWorkers),
			Default: strconv.Itoa(cfg.reviewWorkers),
		}
		if err := survey.AskOne(workersPrompt, &workersStr); err != nil {
			return fmt.Errorf("failed to get worker count: %w", err)
		}
		if workers, err := strconv.Atoi(workersStr); err == nil && workers > 0 {
			cfg.reviewWorkers = workers
		}
	}

	// Optional settings
	if cfg.modelProvider == DefaultModelProvider && cfg.modelName == DefaultModelName {

		var useCustomModel bool
		modelPrompt := &survey.Confirm{
			Message: "Default to models with llamacpp, Would you like to use a custom model?",
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

	logger.Debug(ctx, "model config provider: %s, model name: %s, model config: %v", cfg.modelProvider, cfg.modelName, cfg.modelConfig)
	modelID := constructModelID(cfg)

	if err := core.ConfigureDefaultLLM(cfg.apiKey, modelID); err != nil {
		return fmt.Errorf("failed to configure LLM: %w", err)
	}

	console.Println("Type /help to see available commands, or ask a question directly.")
	showConversationBox(console)

	// Prompt for action
	actionPrompt := &survey.Select{
		Message: "What would you like to do?",
		Options: []string{
			"Review a Pull Request",
			"Ask questions about the repository",
			"Exit",
		},
	}

	for {
		var action string
		if err := survey.AskOne(actionPrompt, &action); err != nil {
			return fmt.Errorf("failed to get action: %w", err)
		}

		switch action {
		case "Review a Pull Request":
			var prNumber string
			prPrompt := &survey.Input{
				Message: "Enter PR number:",
			}
			if err := survey.AskOne(prPrompt, &prNumber); err != nil {
				return fmt.Errorf("failed to get PR number: %w", err)
			}
			var err error
			cfg.prNumber, err = strconv.Atoi(prNumber)
			if err != nil {
				return fmt.Errorf("invalid PR number: %w", err)
			}
			if err := runCLI(cfg); err != nil {
				fmt.Printf("Error reviewing PR: %v\n", err)
			}

		case "Ask questions about the repository":
			// Initialize necessary components for repository Q&A
			if err := initializeAndAskQuestions(ctx, cfg, console); err != nil {
				fmt.Printf("Error during Q&A session: %v\n", err)
			}

		case "Exit":
			return nil
		}
	}
}

func initializeAndAskQuestions(ctx context.Context, cfg *config, console ConsoleInterface) error {
	if cfg.githubToken == "" {
		return fmt.Errorf("GitHub token is required")
	}
	// Initialize GitHub tools and other necessary components
	githubTools := NewGitHubTools(cfg.githubToken, cfg.owner, cfg.repo)
	if githubTools == nil || githubTools.Client() == nil {
		return fmt.Errorf("failed to initialize GitHub client")
	}

	dbPath, err := CreateStoragePath(ctx, cfg.owner, cfg.repo)
	if err != nil {
		return fmt.Errorf("failed to create storage path: %w", err)
	}
	agent, err := NewPRReviewAgent(ctx, githubTools, dbPath, &AgentConfig{
		IndexWorkers:  cfg.indexWorkers,
		ReviewWorkers: cfg.reviewWorkers,
	})
	logging.GetLogger().Info(ctx, "agent: %v", agent)
	if err != nil {
		return fmt.Errorf("Failed to initiliaze agent due to : %v", err)
	}

	qaProcessor, _ := agent.Orchestrator(ctx).GetProcessor("repo_qa")
	// Interactive question loop
	for {
		var question string
		questionPrompt := &survey.Input{
			Message: "Ask a question about the repository (or type 'exit' to return to main menu):",
		}
		if err := survey.AskOne(questionPrompt, &question); err != nil {
			return fmt.Errorf("failed to get question: %w", err)
		}

		if strings.ToLower(question) == "exit" {
			return nil
		}

		// Process the question
		result, err := qaProcessor.Process(ctx, agents.Task{
			ID: "qa",
			Metadata: map[string]interface{}{
				"question": question,
			},
		}, nil)

		if err != nil {
			fmt.Printf("Error processing question: %v\n", err)
			continue
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
	}
}

func ShowHelpMessage(c ConsoleInterface) {
	c.PrintHeader("Available Commands")

	commands := []struct {
		Command     string
		Description string
	}{
		{"/help", "Show this help message"},
		{"/exit", "Exit the application"},
		{"/quit", "Exit the application"},
		{"/compact", "Compact the conversation history"},
		{"/review <PR-NUMBER>", "Review a specific pull request"},
		{"/ask <QUESTION>", "Ask a specific question about the repository"},
	}

	for _, cmd := range commands {
		if c.Color() {
			c.Printf("%s %s\n",
				aurora.Cyan(cmd.Command).Bold().String(),
				cmd.Description)
		} else {
			c.Printf("%s - %s\n", cmd.Command, cmd.Description)
		}
	}

	c.Println("\nYou can also type a question directly to ask about the repository.")
}
