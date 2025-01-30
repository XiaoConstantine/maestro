package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/briandowns/spinner"
	"github.com/spf13/cobra"
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
			return runCLI(cfg)
		},
	}

	// Add flags
	rootCmd.PersistentFlags().StringVar(&cfg.apiKey, "api-key", "", "API Key for vendors")
	rootCmd.PersistentFlags().StringVar(&cfg.githubToken, "github-token", os.Getenv("GITHUB_TOKEN"), "Github token")
	rootCmd.PersistentFlags().StringVar(&cfg.owner, "owner", "", "Repository owner")
	rootCmd.PersistentFlags().StringVar(&cfg.repo, "repo", "", "Repository")
	rootCmd.PersistentFlags().StringVar(&cfg.memoryPath, "path", "./memory", "Path for sqlite table")
	rootCmd.PersistentFlags().IntVar(&cfg.prNumber, "pr", 0, "Pull request number")
	rootCmd.PersistentFlags().BoolVar(&cfg.verbose, "verbose", false, "Enable verbose logging")
	rootCmd.PersistentFlags().BoolVar(&cfg.verifyOnly, "verify-only", false, "Only verify token permissions")

	rootCmd.PersistentFlags().StringP("model", "m", "", `Full model specification (e.g. "ollama:mistral:q4", "llamacpp:", "anthropic:claude-3")`)
	rootCmd.PersistentFlags().StringVar(&cfg.modelProvider, "provider", DefaultModelProvider, "Model provider (llamacpp, ollama, anthropic)")
	rootCmd.PersistentFlags().StringVar(&cfg.modelName, "model-name", DefaultModelName, "Specific model name")
	rootCmd.PersistentFlags().StringVar(&cfg.modelConfig, "model-config", "", "Additional model configuration")
	// Mark required flags
	if err := rootCmd.MarkPersistentFlagRequired("github-token"); err != nil {
		// Use fmt.Fprintf to write to stderr since we don't have a logger configured yet
		fmt.Fprintf(os.Stderr, "Failed to mark github-token flag as required: %v\n", err)
		os.Exit(1)
	}
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runCLI(cfg *config) error {
	err := validateModelConfig(cfg)
	if err != nil {
		os.Exit(1)
	}
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

	console := NewConsole(os.Stdout, logger)

	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Prefix = "Processing "
	err = s.Color("cyan")
	if err != nil {
		logger.Error(ctx, "Failed to start spinner properly")
	}
	err = VerifyTokenPermissions(ctx, cfg.githubToken, cfg.owner, cfg.repo)
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
	agent, err := NewPRReviewAgent()
	if err != nil {
		panic(err)
	}

	githubTools := NewGitHubTools(cfg.githubToken, cfg.owner, cfg.repo)
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

	s.Start()
	comments, err := agent.ReviewPR(ctx, tasks, console)
	s.Stop()
	if err != nil {
		logger.Error(ctx, "Failed to review PR: %v", err)
		os.Exit(1)
	}
	console.ShowSummary(comments)

	logger.Info(ctx, "Posting review comments to GitHub")
	// err = githubTools.CreateReviewComments(ctx, *prNumber, comments)
	// if err != nil {
	// 	logger.Error(ctx, "Failed to post review comments: %v", err)
	// 	os.Exit(1)
	// }

	console.ReviewComplete()
	return nil
}

func parseModelString(modelStr string) (provider, name, config string) {
	parts := strings.Split(modelStr, ":")
	switch len(parts) {
	case 1:
		return parts[0], "", ""
	case 2:
		return parts[0], parts[1], ""
	case 3:
		return parts[0], parts[1], parts[2]
	default:
		return "", "", ""
	}
}

func constructModelID(cfg *config) core.ModelID {
	var parts []string
	parts = append(parts, cfg.modelProvider)

	if cfg.modelName != "" {
		parts = append(parts, cfg.modelName)
	}

	if cfg.modelConfig != "" {
		parts = append(parts, cfg.modelConfig)
	}

	return core.ModelID(strings.Join(parts, ":"))
}

func validateModelConfig(cfg *config) error {
	// Validate provider
	switch cfg.modelProvider {
	case "llamacpp:", "ollama", "anthropic", "google":
		// Valid providers
	default:
		return fmt.Errorf("unsupported model provider: %s", cfg.modelProvider)
	}

	// Validate provider-specific configurations
	switch cfg.modelProvider {
	case "anthropic", "google":
		if cfg.apiKey == "" {
			return fmt.Errorf("API key required for Anthropic models")
		}
	case "ollama":
		if cfg.modelName == "" {
			return fmt.Errorf("model name required for Ollama models")
		}
	}

	return nil
}
