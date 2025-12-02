package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/github"
	"github.com/XiaoConstantine/maestro/internal/review"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
	"github.com/XiaoConstantine/maestro/terminal"
	"github.com/briandowns/spinner"
	gh "github.com/google/go-github/v68/github"
	"github.com/logrusorgru/aurora"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	_ "github.com/mattn/go-sqlite3"
)

// cleanupRegistry tracks resources that need cleanup on shutdown.
var cleanupRegistry = struct {
	sync.Mutex
	funcs []func()
}{}

// registerCleanup adds a cleanup function to be called on shutdown.
func registerCleanup(fn func()) {
	cleanupRegistry.Lock()
	defer cleanupRegistry.Unlock()
	cleanupRegistry.funcs = append(cleanupRegistry.funcs, fn)
}

// runCleanup executes all registered cleanup functions.
func runCleanup() {
	cleanupRegistry.Lock()
	defer cleanupRegistry.Unlock()
	for _, fn := range cleanupRegistry.funcs {
		fn()
	}
	cleanupRegistry.funcs = nil
}

// setupSignalHandler sets up graceful shutdown on SIGINT/SIGTERM.
func setupSignalHandler() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger := logging.GetLogger()
		logger.Info(context.Background(), "Received signal %v, shutting down...", sig)

		// Run all cleanup functions
		runCleanup()

		os.Exit(0)
	}()
}

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

	// Set up signal handler for graceful shutdown
	setupSignalHandler()

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
				provider, name, modelCfg := util.ParseModelString(modelStr)
				if provider != "" {
					cfg.modelProvider = provider
				}
				if name != "" {
					cfg.modelName = name
				}
				if modelCfg != "" {
					cfg.modelConfig = modelCfg
				}
			}

			interactive, _ := cmd.Flags().GetBool("interactive")

			// Use TUI v2 for interactive mode (default when no PR specified)
			if interactive || cmd.Flags().NFlag() == 0 || cfg.prNumber == 0 {
				return runModernUI(cfg)
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
	modelCfg := &util.ModelConfig{
		ModelProvider: cfg.modelProvider,
		ModelName:     cfg.modelName,
		ModelConfig:   cfg.modelConfig,
		APIKey:        cfg.apiKey,
	}
	err = util.ValidateModelConfig(modelCfg)
	if err != nil {
		logger.Error(ctx, "Model config is incorrect: %v", err)
		os.Exit(1)
	}
	cfg.apiKey = modelCfg.APIKey // Update with resolved API key
	err = console.WithSpinner(ctx, "Verifying permissions...", func() error {
		return github.VerifyTokenPermissions(ctx, cfg.githubToken, cfg.owner, cfg.repo)
	})
	if err != nil {
		logger.Error(ctx, "Token permission verification failed: %v", err)
		os.Exit(1)
	}

	if cfg.verifyOnly {
		os.Exit(0)
	}
	llms.EnsureFactory()

	modelID := util.ConstructModelID(modelCfg)
	err = core.ConfigureDefaultLLM(cfg.apiKey, modelID)

	if err != nil {
		logger.Error(ctx, "Failed to configure LLM: %v", err)
	}
	// Use unified embedding model for both code and guidelines
	// Configure teacher LLM with a Gemini generation model that supports embeddings
	if err := core.ConfigureTeacherLLM(cfg.apiKey, core.ModelGoogleGeminiPro); err != nil {
		return fmt.Errorf("failed to configure teacher LLM: %w", err)
	}
	githubTools := github.NewTools(cfg.githubToken, cfg.owner, cfg.repo)

	// Initialize MCP bash helper for GitHub operations
	var mcpHelper *github.MCPBashHelper
	if helper, err := github.NewMCPBashHelper(); err != nil {
		logger.Warn(ctx, "Failed to initialize MCP bash helper: %v", err)
		logger.Info(ctx, "Falling back to GitHub API for PR operations")
		mcpHelper = nil
	} else {
		mcpHelper = helper
		// Register for cleanup on signal (Ctrl+C) and defer for normal exit
		registerCleanup(func() {
			logger.Debug(context.Background(), "Signal handler: cleaning up MCP bash helper")
			mcpHelper.Close()
		})
		defer mcpHelper.Close()
		logger.Debug(ctx, "MCP bash helper initialized successfully")
	}

	dbPath, err := util.CreateStoragePath(ctx, cfg.owner, cfg.repo)
	// Check if we already have an index for this commit
	if err != nil {
		panic(err)
	}
	agent, err := review.NewPRReviewAgent(ctx, githubTools, dbPath, &types.AgentConfig{
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
func runFullPRReview(ctx context.Context, prNumber int, cfg *config, console types.ConsoleInterface, agent types.ReviewAgent, mcpHelper *github.MCPBashHelper) error {
	logger := logging.GetLogger()

	githubTools := github.NewTools(cfg.githubToken, cfg.owner, cfg.repo)

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

	var changes *types.PRChanges

	// Use MCP if available, otherwise fall back to GitHub API
	if mcpHelper != nil {
		logger.Debug(ctx, "Using MCP bash helper to fetch PR changes")
		// Wait for clone to complete (up to 5 minutes for large repos)
		clonePath := agent.WaitForClone(ctx, 5*time.Minute)
		if clonePath == "" {
			logger.Warn(ctx, "Clone not available, falling back to GitHub API")
			changes, err = githubTools.GetPullRequestChanges(ctx, prNumber)
		} else {
			changes, err = github.GetPullRequestChangesWithMCP(ctx, cfg.owner, cfg.repo, prNumber, mcpHelper, clonePath)
		}
	} else {
		logger.Debug(ctx, "Using GitHub API to fetch PR changes")
		changes, err = githubTools.GetPullRequestChanges(ctx, prNumber)
	}

	if err != nil {
		logger.Error(ctx, "Failed to get PR changes: %v", err)
		return fmt.Errorf("failed to get PR changes: %w", err)
	}
	tasks := make([]types.PRReviewTask, 0, len(changes.Files))
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

		tasks = append(tasks, types.PRReviewTask{
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
				util.Pluralize("file", len(tasks)))).Bold(),
			aurora.Blue("...").String(),
		)
	} else {
		console.Printf("⚡ Starting code review for %d %s...\n",
			len(tasks),
			util.Pluralize("file", len(tasks)))
	}
	// Starting code review
	// Note: Don't stop the agent here in interactive mode - it's managed at the session level
	comments, err := agent.ReviewPRWithChanges(ctx, prNumber, tasks, console, changes)
	if err != nil {
		logger.Error(ctx, "Failed to review PR: %v", err)
		return fmt.Errorf("failed to review PR: %w", err)
	}
	if len(comments) != 0 {
		// Check if interactive TUI mode is enabled
		useInteractiveTUI := os.Getenv("MAESTRO_INTERACTIVE_TUI") == "true"

		if useInteractiveTUI && console.IsInteractive() {
			// Use the new lazygit-style TUI for reviewing comments
			onPost := func(selectedComments []types.PRReviewComment) error {
				logger.Info(ctx, "Posting %d review comments to GitHub", len(selectedComments))
				return githubTools.CreateReviewComments(ctx, prNumber, selectedComments)
			}
			if err := console.ShowCommentsInteractive(comments, onPost); err != nil {
				logger.Error(ctx, "Interactive TUI error: %v", err)
				// Fall back to standard preview
				_, _ = githubTools.PreviewReview(ctx, console, prNumber, comments, agent.Metrics(ctx))
			}
		} else {
			// Standard preview flow
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
	}
	console.ReviewComplete()
	return nil
}

// TUIBackendAdapter adapts ReviewAgent to terminal.MaestroBackend.
type TUIBackendAdapter struct {
	agent       types.ReviewAgent
	githubTools types.GitHubInterface
	owner       string
	repo        string
}

// NewTUIBackendAdapter creates a new backend adapter.
func NewTUIBackendAdapter(agent types.ReviewAgent, githubTools types.GitHubInterface, owner, repo string) *TUIBackendAdapter {
	return &TUIBackendAdapter{
		agent:       agent,
		githubTools: githubTools,
		owner:       owner,
		repo:        repo,
	}
}

// ReviewPR implements terminal.MaestroBackend.
func (a *TUIBackendAdapter) ReviewPR(ctx context.Context, prNumber int, onProgress func(status string)) ([]terminal.ReviewComment, error) {
	if a.agent == nil {
		return nil, fmt.Errorf("agent not initialized")
	}

	// Create a progress console adapter
	progressConsole := &tuiProgressConsole{onProgress: onProgress}

	// Fetch PR changes (like runFullPRReview does)
	if onProgress != nil {
		onProgress("Fetching PR changes...")
	}
	changes, err := a.githubTools.GetPullRequestChanges(ctx, prNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR changes: %w", err)
	}

	// Create review tasks from the changes
	tasks := make([]types.PRReviewTask, 0, len(changes.Files))
	for _, file := range changes.Files {
		tasks = append(tasks, types.PRReviewTask{
			FilePath:    file.FilePath,
			FileContent: file.FileContent,
			Changes:     file.Patch,
		})
	}

	if onProgress != nil {
		onProgress(fmt.Sprintf("Reviewing %d files...", len(tasks)))
	}

	// Call ReviewPRWithChanges with tasks and preloaded changes
	comments, err := a.agent.ReviewPRWithChanges(ctx, prNumber, tasks, progressConsole, changes)
	if err != nil {
		return nil, err
	}

	result := make([]terminal.ReviewComment, 0, len(comments))
	for _, c := range comments {
		result = append(result, terminal.ReviewComment{
			FilePath:   c.FilePath,
			LineNumber: c.LineNumber,
			Content:    c.Content,
			Severity:   c.Severity,
			Suggestion: c.Suggestion,
			Category:   c.Category,
		})
	}

	return result, nil
}

// AskQuestion implements terminal.MaestroBackend.
func (a *TUIBackendAdapter) AskQuestion(ctx context.Context, question string) (string, error) {
	return "Question answering coming soon. Use /review <PR#> to review a pull request.", nil
}

// GetRepoInfo implements terminal.MaestroBackend.
func (a *TUIBackendAdapter) GetRepoInfo() terminal.RepoInfo {
	return terminal.RepoInfo{
		Owner:  a.owner,
		Repo:   a.repo,
		Branch: "main",
	}
}

// IsReady implements terminal.MaestroBackend.
func (a *TUIBackendAdapter) IsReady() bool {
	return a.agent != nil
}

// tuiProgressConsole adapts progress callbacks to ConsoleInterface.
type tuiProgressConsole struct {
	onProgress func(status string)
}

func (c *tuiProgressConsole) StartSpinner(message string) {
	if c.onProgress != nil {
		c.onProgress(message)
	}
}
func (c *tuiProgressConsole) StopSpinner() {}
func (c *tuiProgressConsole) WithSpinner(ctx context.Context, message string, fn func() error) error {
	return fn()
}
func (c *tuiProgressConsole) ShowComments(comments []types.PRReviewComment, metric types.MetricsCollector) {}
func (c *tuiProgressConsole) ShowCommentsInteractive(comments []types.PRReviewComment, onPost func([]types.PRReviewComment) error) error {
	return nil
}
func (c *tuiProgressConsole) ShowSummary(comments []types.PRReviewComment, metric types.MetricsCollector) {}
func (c *tuiProgressConsole) StartReview(pr *gh.PullRequest) {
	if c.onProgress != nil && pr != nil {
		c.onProgress(fmt.Sprintf("Starting review: %s", pr.GetTitle()))
	}
}
func (c *tuiProgressConsole) ReviewingFile(file string, current, total int) {
	if c.onProgress != nil {
		c.onProgress(fmt.Sprintf("Reviewing %s (%d/%d)", file, current, total))
	}
}
func (c *tuiProgressConsole) ConfirmReviewPost(commentCount int) (bool, error) { return false, nil }
func (c *tuiProgressConsole) ReviewComplete() {
	if c.onProgress != nil {
		c.onProgress("Review complete")
	}
}
func (c *tuiProgressConsole) UpdateSpinnerText(text string) {
	if c.onProgress != nil {
		c.onProgress(text)
	}
}
func (c *tuiProgressConsole) ShowReviewMetrics(metrics types.MetricsCollector, comments []types.PRReviewComment) {
}
func (c *tuiProgressConsole) CollectAllFeedback(comments []types.PRReviewComment, metric types.MetricsCollector) error {
	return nil
}
func (c *tuiProgressConsole) Confirm(opts types.PromptOptions) (bool, error) { return false, nil }
func (c *tuiProgressConsole) FileError(filepath string, err error) {
	if c.onProgress != nil {
		c.onProgress(fmt.Sprintf("Error in %s: %v", filepath, err))
	}
}
func (c *tuiProgressConsole) Printf(format string, a ...interface{})    {}
func (c *tuiProgressConsole) Println(a ...interface{})                  {}
func (c *tuiProgressConsole) PrintHeader(text string)                   {}
func (c *tuiProgressConsole) NoIssuesFound(file string, chunkNumber, totalChunks int) {}
func (c *tuiProgressConsole) SeverityIcon(severity string) string { return "" }
func (c *tuiProgressConsole) Color() bool                          { return false }
func (c *tuiProgressConsole) Spinner() *spinner.Spinner            { return nil }
func (c *tuiProgressConsole) IsInteractive() bool                  { return false }

func runModernUI(cfg *config) error {
	ctx := core.WithExecutionState(context.Background())

	// Simple validation - just check if GitHub token is provided
	if cfg.githubToken == "" {
		fmt.Fprintln(os.Stderr, "GitHub token required via --github-token or MAESTRO_GITHUB_TOKEN")
		return fmt.Errorf("GitHub token required")
	}

	// Configure logger to write to file only (not console) to avoid corrupting TUI
	logLevel := logging.INFO
	if cfg.verbose {
		logLevel = logging.DEBUG
	}
	fileOutput, _ := logging.NewFileOutput(
		filepath.Join(".", "dspy.log"),
		logging.WithRotation(100*1024*1024, 5),
		logging.WithJSONFormat(true),
	)
	logger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  []logging.Output{fileOutput}, // File only, no console output
	})
	logging.SetLogger(logger)

	// Validate model config and setup LLM
	modelCfg := &util.ModelConfig{
		ModelProvider: cfg.modelProvider,
		ModelName:     cfg.modelName,
		ModelConfig:   cfg.modelConfig,
		APIKey:        cfg.apiKey,
	}
	if err := util.ValidateModelConfig(modelCfg); err != nil {
		return fmt.Errorf("model config is incorrect: %w", err)
	}
	cfg.apiKey = modelCfg.APIKey // Update with resolved API key
	llms.EnsureFactory()
	modelID := util.ConstructModelID(modelCfg)
	if err := core.ConfigureDefaultLLM(cfg.apiKey, modelID); err != nil {
		return fmt.Errorf("failed to configure LLM: %w", err)
	}

	// Initialize GitHub tools and agent for real functionality
	githubTools := github.NewTools(cfg.githubToken, cfg.owner, cfg.repo)
	dbPath, err := util.CreateStoragePath(ctx, cfg.owner, cfg.repo)
	if err != nil {
		return fmt.Errorf("failed to create storage path: %w", err)
	}

	// Create agent to enable real reviews
	agent, err := review.NewPRReviewAgent(ctx, githubTools, dbPath, &types.AgentConfig{
		IndexWorkers:  cfg.indexWorkers,
		ReviewWorkers: cfg.reviewWorkers,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize agent: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		agent.Stop(shutdownCtx)
	}()

	// Create TUI config
	tuiConfig := &terminal.MaestroConfig{
		Owner:         cfg.owner,
		Repo:          cfg.repo,
		GitHubToken:   cfg.githubToken,
		Verbose:       cfg.verbose,
		IndexWorkers:  cfg.indexWorkers,
		ReviewWorkers: cfg.reviewWorkers,
	}

	// Create real backend adapter wrapping the agent
	backend := NewTUIBackendAdapter(agent, githubTools, cfg.owner, cfg.repo)

	// Start the unified Maestro TUI
	return terminal.RunMaestro(tuiConfig, backend)
}

