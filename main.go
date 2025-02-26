package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/logrusorgru/aurora"
	"github.com/spf13/cobra"
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

//	func handleAPIKeySetup(cfg *config, choice *ModelChoice) error {
//		// First try to get the API key from environment variables
//		key, err := checkProviderAPIKey(choice.Provider, "")
//		if err == nil {
//			// Found a valid key in environment variables
//			cfg.apiKey = key
//			return nil
//		}
//
//		// If no environment variable is found, prompt the user
//		var apiKey string
//		apiKeyPrompt := &survey.Password{
//			Message: fmt.Sprintf("Enter API key for %s:", choice.DisplayName),
//			Help: fmt.Sprintf(`Required for %s models.
//
// You can also set this via environment variables:
//
//   - Anthropic: ANTHROPIC_API_KEY or CLAUDE_API_KEY
//
//   - Google: GOOGLE_API_KEY or GEMINI_API_KEY`, choice.Provider),
//     }
//
//     if err := survey.AskOne(apiKeyPrompt, &apiKey); err != nil {
//     return fmt.Errorf("failed to get API key: %w", err)
//     }
//
//     // Basic validation of the provided key
//     if strings.TrimSpace(apiKey) == "" {
//     return fmt.Errorf("API key cannot be empty")
//     }
//
//     cfg.apiKey = apiKey
//     return nil
//     }
//
// setupLogging configures debug logging to a file when verbose mode is enabled.
func setupLogging(verbose bool) (func(), logging.Output) {
	if !verbose {
		return func() {}, nil // No-op cleanup function
	}

	// Set DEBUG environment variable for other components
	os.Setenv("DEBUG", "1")

	// Expand the ~ in the log path
	logDir := "~/.maestro/logs"
	if strings.HasPrefix(logDir, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			logDir = filepath.Join(home, logDir[2:])
		}
	}

	// Create the directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create log directory: %v\n", err)
		return func() {}, nil
	}

	logPath := filepath.Join(logDir, "maestro-debug.log")
	f, err := tea.LogToFile(logPath, "debug")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set up logging: %v\n", err)
		return func() {}, nil
	}
	fileOutput, err := logging.NewFileOutput(
		logPath,
		logging.WithRotation(50*1024*1024, 5), // 50MB max size, keep 5 files
		logging.WithJSONFormat(false),         // Use plain text for readability
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create application log output: %v\n", err)
		// Still return the Bubble Tea cleanup even if file output fails
		return func() {
			if f != nil {
				f.Close()
			}
		}, nil
	}
	fmt.Fprintf(os.Stderr, "Debug logging enabled to %s\n", logPath)

	// Return a cleanup function that closes the file
	return func() {
		if f != nil {
			f.Close()
		}
	}, fileOutput
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
	ctx := core.WithExecutionState(context.Background())
	consoleOutput := logging.NewConsoleOutput(true, logging.WithColor(true))

	logLevel := logging.INFO
	if cfg.verbose {
		logLevel = logging.DEBUG
	}
	cleanup, fileOutput := setupLogging(cfg.verbose)
	defer cleanup()

	var outputs []logging.Output
	outputs = append(outputs, consoleOutput)
	if fileOutput != nil {
		outputs = append(outputs, fileOutput)
	}
	logger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  outputs,
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
	ctx := core.WithExecutionState(context.Background())
	consoleOutput := logging.NewConsoleOutput(true, logging.WithColor(true))
	logLevel := logging.INFO
	if cfg.verbose {
		logLevel = logging.DEBUG
	}
	var outputs []logging.Output
	outputs = append(outputs, consoleOutput)
	cleanup, fileOutput := setupLogging(cfg.verbose)
	defer cleanup()
	if fileOutput != nil {
		outputs = append(outputs, fileOutput)
	}
	logger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  outputs,
	})
	logging.SetLogger(logger)
	console := NewConsole(os.Stdout, logger, nil)
	llms.EnsureFactory()
	modelID := constructModelID(cfg)
	err := core.ConfigureDefaultLLM(cfg.apiKey, modelID)
	if err != nil {
		logger.Fatalf(ctx, "Failed to config llm: %v", err)
	}

	// Create and run the Bubble Tea program
	p := tea.NewProgram(initialModel(cfg, logger, console), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running Bubble Tea program: %w", err)
	}

	return nil
}
func runQA(ctx context.Context, cfg *config, console ConsoleInterface, question string) error {
	_ = core.WithExecutionState(context.Background())
	consoleOutput := logging.NewConsoleOutput(true, logging.WithColor(true))
	logLevel := logging.INFO
	if cfg.verbose {
		logLevel = logging.DEBUG
	}
	var outputs []logging.Output
	outputs = append(outputs, consoleOutput)
	cleanup, fileOutput := setupLogging(cfg.verbose)
	defer cleanup()
	if fileOutput != nil {
		outputs = append(outputs, fileOutput)
	}
	logger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  outputs,
	})
	logging.SetLogger(logger)
	llms.EnsureFactory()
	modelID := constructModelID(cfg)
	err := core.ConfigureDefaultLLM(cfg.apiKey, modelID)
	if err != nil {
		logger.Fatalf(ctx, "Failed to configure LLM: %v", err)
	}

	if cfg.githubToken == "" {
		return fmt.Errorf("GitHub token is required")
	}

	logger.Info(ctx, "config: %v", cfg)
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
	if err != nil {
		return fmt.Errorf("Failed to initialize agent due to: %v", err)
	}

	qaProcessor, _ := agent.Orchestrator(ctx).GetProcessor("repo_qa")

	// Process the question
	result, err := qaProcessor.Process(ctx, agents.Task{
		ID: "qa",
		Metadata: map[string]interface{}{
			"question": question,
		},
	}, nil)

	if err != nil {
		return fmt.Errorf("Error processing question: %v", err)
	}

	// Format response
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
