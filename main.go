package main

import (
	"context"
	"fmt"
	"os"
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
}

const (
	DefaultModelProvider = "llamacpp:"
	DefaultModelName     = ""
)

func main() {
	cfg := &config{}
	sqlite_vec.Auto()
	// Create root command
	rootCmd := &cobra.Command{
		Use:   "Maestro",
		Short: "Maestro - Code assistant",
		Long: `Maestro is an AI-powered code assistant that helps you review PR
and impl changes through interactive learning sessions.`,
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

	latestSHA, err := githubTools.GetLatestCommitSHA(ctx, "main")
	if err != nil {
		return fmt.Errorf("failed to get latest commit SHA: %w", err)
	}

	dbPath, needFullIndex, err := CreateStoragePath(ctx, cfg.owner, cfg.repo, latestSHA)
	// Check if we already have an index for this commit
	if err != nil {
		panic(err)
	}
	console.printf("\nNeed full index: %v", needFullIndex)
	agent, err := NewPRReviewAgent(ctx, githubTools, dbPath, needFullIndex)
	if err != nil {
		panic(err)
	}

	logger.Info(ctx, "Fetching changes for PR #%d", cfg.prNumber)
	pr, _, _ := githubTools.client.PullRequests.Get(ctx, cfg.owner, cfg.repo, cfg.prNumber)
	console.StartReview(pr)

	changes, err := githubTools.GetPullRequestChanges(ctx, cfg.prNumber)
	if err != nil {
		logger.Error(ctx, "Failed to get PR changes: %v", err)
		os.Exit(1)
	}
	tasks := make([]PRReviewTask, 0, len(changes.Files))
	for _, file := range changes.Files {
		// Log file being processed
		logger.Info(ctx, "Processing file: %s (+%d/-%d lines)",
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

	logger.Info(ctx, "Starting code review for %d files", len(tasks))
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
		shouldPost, err := githubTools.PreviewReview(ctx, console, cfg.prNumber, comments)
		if err != nil {
			logger.Error(ctx, "Failed to preview review: %v", err)
			os.Exit(1)
		}

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
	output := logging.NewConsoleOutput(true, logging.WithColor(true))
	logLevel := logging.INFO
	logger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  []logging.Output{output},
	})
	logging.SetLogger(logger)

	console := NewConsole(os.Stdout, logger, nil)
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

	// Optional settings
	if cfg.modelProvider == DefaultModelProvider && cfg.modelName == DefaultModelName {
		var useCustomModel bool
		modelPrompt := &survey.Confirm{
			Message: "Would you like to use a custom model?",
			Default: false,
		}
		if err := survey.AskOne(modelPrompt, &useCustomModel); err != nil {
			return fmt.Errorf("failed to get model preference: %w", err)
		}

		if useCustomModel {
			modelStrPrompt := &survey.Input{
				Message: "Enter model specification (e.g., ollama:mistral:q4, anthropic:claude-3):",
				Default: DefaultModelProvider,
			}
			var modelStr string
			if err := survey.AskOne(modelStrPrompt, &modelStr); err != nil {
				return fmt.Errorf("failed to get model specification: %w", err)
			}
			provider, name, config := parseModelString(modelStr)
			cfg.modelProvider = provider
			cfg.modelName = name
			cfg.modelConfig = config
		}
	}

	modelID := constructModelID(cfg)

	if err := core.ConfigureDefaultLLM(cfg.apiKey, modelID); err != nil {
		return fmt.Errorf("failed to configure LLM: %w", err)
	}

	verbosePrompt := &survey.Confirm{
		Message: "Enable verbose logging?",
		Default: false,
	}
	if err := survey.AskOne(verbosePrompt, &cfg.verbose); err != nil {
		return fmt.Errorf("failed to get verbose preference: %w", err)
	}

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

func initializeAndAskQuestions(ctx context.Context, cfg *config, console *Console) error {
	if cfg.githubToken == "" {
		return fmt.Errorf("GitHub token is required")
	}
	// Initialize GitHub tools and other necessary components
	githubTools := NewGitHubTools(cfg.githubToken, cfg.owner, cfg.repo)
	if githubTools == nil || githubTools.client == nil {
		return fmt.Errorf("failed to initialize GitHub client")
	}
	latestSHA, err := githubTools.GetLatestCommitSHA(ctx, "main")
	if err != nil {
		return fmt.Errorf("failed to get latest commit SHA: %w", err)
	}

	dbPath, needFullIndex, err := CreateStoragePath(ctx, cfg.owner, cfg.repo, latestSHA)
	if err != nil {
		return fmt.Errorf("failed to create storage path: %w", err)
	}
	agent, err := NewPRReviewAgent(ctx, githubTools, dbPath, needFullIndex)
	if err != nil {
		return fmt.Errorf("Failed to initiliaze agent due to : %v", err)
	}

	qaProcessor, _ := agent.GetOrchestrator().GetProcessor("repo_qa")
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
			console.println("\n" + strings.Repeat("─", 80))

			// Format and print the main answer using structured sections
			formattedAnswer := formatStructuredAnswer(response.Answer)
			console.println(formattedAnswer)

			// Print source files in a tree-like structure if available
			if len(response.SourceFiles) > 0 {
				if console.color {
					console.println("\n" + aurora.Blue("Source Files:").String())
				} else {
					console.println("\nSource Files:")
				}

				// Group files by directory for better organization
				filesByDir := groupFilesByDirectory(response.SourceFiles)
				printFileTree(console, filesByDir)
			}

			// Print final separator
			console.println("\n" + strings.Repeat("─", 80) + "\n")
		}
	}
}
