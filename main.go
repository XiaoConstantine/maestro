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

	"github.com/XiaoConstantine/dspy-go/pkg/agents/ace"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	maestroace "github.com/XiaoConstantine/maestro/internal/ace"
	"github.com/XiaoConstantine/maestro/internal/agent"
	"github.com/XiaoConstantine/maestro/internal/github"
	"github.com/XiaoConstantine/maestro/internal/orchestration"
	"github.com/XiaoConstantine/maestro/internal/review"
	"github.com/XiaoConstantine/maestro/internal/search"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
	"github.com/XiaoConstantine/maestro/terminal"
	"github.com/briandowns/spinner"
	gh "github.com/google/go-github/v68/github"
	"github.com/logrusorgru/aurora"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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

	// Default to 120 workers for I/O-bound LLM API calls.
	// LLM calls are network-bound, not CPU-bound, so higher concurrency
	// improves throughput by overlapping HTTP requests.
	rootCmd.PersistentFlags().IntVar(&cfg.reviewWorkers, "review-workers", 120, "Number of concurrent workers for parallel review")

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
	if err != nil {
		logger.Error(ctx, "Failed to create storage path: %v", err)
		return fmt.Errorf("failed to create storage path: %w", err)
	}

	// Initialize ACE (Agentic Context Engineering) for self-improving reviews
	aceConfig := maestroace.LoadConfigFromEnv()
	aceBasePath := filepath.Dir(dbPath)
	aceManager, err := maestroace.NewMaestroACEManager(aceBasePath, aceConfig, logger)
	if err != nil {
		logger.Warn(ctx, "Failed to initialize ACE manager: %v", err)
	} else if aceManager.IsEnabled() {
		logger.Info(ctx, "ACE enabled for self-improving reviews")
		// Register ACE cleanup
		registerCleanup(func() {
			aceManager.Close()
		})
	}

	// Get ACE review manager for trajectory recording
	var aceReviewManager *ace.Manager
	if aceManager != nil && aceManager.IsEnabled() {
		aceReviewManager, _ = aceManager.GetReviewManager(ctx)
	}

	agent, err := review.NewPRReviewAgentWithACE(ctx, githubTools, dbPath, &types.AgentConfig{
		IndexWorkers:  cfg.indexWorkers,
		ReviewWorkers: cfg.reviewWorkers,
	}, aceReviewManager)
	if err != nil {
		logger.Error(ctx, "Failed to initialize review agent: %v", err)
		return fmt.Errorf("failed to initialize review agent: %w", err)
	}
	// Register for cleanup on signal (Ctrl+C)
	registerCleanup(func() {
		logger.Debug(context.Background(), "Signal handler: cleaning up review agent")
		agent.Close()
	})
	defer func() {
		if err := agent.Close(); err != nil {
			logger.Warn(ctx, "Error closing review agent: %v", err)
		}
	}()

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
// It uses the UnifiedReActAgent which can iteratively search and read files to answer questions.
func (a *TUIBackendAdapter) AskQuestion(ctx context.Context, question string) (string, error) {
	if a.agent == nil {
		return "", fmt.Errorf("agent not initialized")
	}

	logger := logging.GetLogger()
	logger.Info(ctx, "Processing question with ReAct agent: %s", question)

	// Get the cloned repo path
	repoPath := a.agent.ClonedRepoPath()
	if repoPath == "" {
		return "Repository is still being cloned. Please wait a moment and try again.", nil
	}

	// Create a SimpleSearchTool for the cloned repo
	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	// Create a UnifiedReActAgent that can iteratively explore the codebase
	reactAgent, err := agent.NewUnifiedReActAgent("qa-agent", searchTool, logger)
	if err != nil {
		logger.Error(ctx, "Failed to create ReAct agent: %v", err)
		return "", fmt.Errorf("failed to initialize question answering: %w", err)
	}

	// Create search request with the question
	searchRequest := &search.SearchRequest{
		Query:         question,
		Context:       fmt.Sprintf("Repository: %s/%s. Answer the user's question by exploring the codebase. For overview questions, start by reading README.md.", a.owner, a.repo),
		MaxResults:    10,
		RequiredDepth: 3,
	}

	// Execute the search using the ReAct agent
	response, err := reactAgent.ExecuteSearch(ctx, searchRequest)
	if err != nil {
		logger.Error(ctx, "ReAct agent search failed: %v", err)
		return "", fmt.Errorf("failed to answer question: %w", err)
	}

	// Format the response
	var result strings.Builder

	// Extract the answer from the ReAct agent results
	// The agent's answer is stored in a result with FilePath like "phase-X-output"
	var answer string
	var sourceFiles []string
	seenFiles := make(map[string]bool)

	for _, r := range response.Results {
		if r.SearchResult == nil {
			continue
		}
		// Phase outputs contain the agent's synthesized answer
		if strings.HasPrefix(r.FilePath, "phase-") || strings.HasPrefix(r.FilePath, "react-") {
			if r.Line != "" && len(r.Line) > len(answer) {
				answer = r.Line
			}
		} else if r.FilePath != "" {
			// Track actual source files explored
			if !seenFiles[r.FilePath] {
				seenFiles[r.FilePath] = true
				sourceFiles = append(sourceFiles, r.FilePath)
			}
		}
	}

	// Use the agent's answer, fall back to synthesis
	if answer != "" {
		result.WriteString(answer)
	} else if response.Synthesis != "" {
		result.WriteString(response.Synthesis)
	} else {
		return fmt.Sprintf("I couldn't find relevant information about \"%s\" in this repository.", question), nil
	}

	// Add source files if available
	if len(sourceFiles) > 0 {
		result.WriteString("\n\n📁 Sources explored:\n")
		for _, f := range sourceFiles {
			result.WriteString(fmt.Sprintf("  • %s\n", f))
		}
	}

	// Add confidence indicator
	if response.Confidence < 0.5 {
		result.WriteString("\n⚠️  Low confidence answer - results may be incomplete")
	}

	logger.Info(ctx, "ReAct agent completed in %v with confidence %.2f", response.Duration, response.Confidence)
	return result.String(), nil
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

// Claude implements terminal.MaestroBackend (not supported in legacy adapter).
func (a *TUIBackendAdapter) Claude(ctx context.Context, prompt string) (string, error) {
	return "", fmt.Errorf("claude CLI not available in legacy mode - use TUI service adapter")
}

// Gemini implements terminal.MaestroBackend (not supported in legacy adapter).
func (a *TUIBackendAdapter) Gemini(ctx context.Context, prompt string, taskType string) (string, error) {
	return "", fmt.Errorf("gemini CLI not available in legacy mode - use TUI service adapter")
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

// TUIServiceAdapter wraps MaestroService for terminal.MaestroBackend.
type TUIServiceAdapter struct {
	service     *orchestration.MaestroService
	githubTools types.GitHubInterface
	owner       string
	repo        string
}

func NewTUIServiceAdapter(service *orchestration.MaestroService, githubTools types.GitHubInterface, owner, repo string) *TUIServiceAdapter {
	return &TUIServiceAdapter{
		service:     service,
		githubTools: githubTools,
		owner:       owner,
		repo:        repo,
	}
}

func (a *TUIServiceAdapter) ReviewPR(ctx context.Context, prNumber int, onProgress func(status string)) ([]terminal.ReviewComment, error) {
	response, err := a.service.ProcessRequest(ctx, orchestration.Request{
		Type:       orchestration.RequestReview,
		PRNumber:   prNumber,
		OnProgress: onProgress,
	})
	if err != nil {
		return nil, err
	}

	result := make([]terminal.ReviewComment, 0, len(response.Comments))
	for _, c := range response.Comments {
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

func (a *TUIServiceAdapter) AskQuestion(ctx context.Context, question string) (string, error) {
	response, err := a.service.ProcessRequest(ctx, orchestration.Request{
		Type:     orchestration.RequestAsk,
		Question: question,
	})
	if err != nil {
		return "", err
	}

	result := response.Answer
	if sources, ok := response.Metadata["sources"].([]string); ok && len(sources) > 0 {
		result += "\n\nSources:\n"
		for _, s := range sources {
			result += fmt.Sprintf("  - %s\n", s)
		}
	}
	return result, nil
}

func (a *TUIServiceAdapter) GetRepoInfo() terminal.RepoInfo {
	return terminal.RepoInfo{
		Owner:  a.owner,
		Repo:   a.repo,
		Branch: "main",
	}
}

func (a *TUIServiceAdapter) IsReady() bool {
	return a.service != nil && a.service.IsReady()
}

func (a *TUIServiceAdapter) Claude(ctx context.Context, prompt string) (string, error) {
	response, err := a.service.ProcessRequest(ctx, orchestration.Request{
		Type:   orchestration.RequestClaude,
		Prompt: prompt,
	})
	if err != nil {
		return "", err
	}
	return response.Answer, nil
}

func (a *TUIServiceAdapter) Gemini(ctx context.Context, prompt string, taskType string) (string, error) {
	response, err := a.service.ProcessRequest(ctx, orchestration.Request{
		Type:     orchestration.RequestGemini,
		Prompt:   prompt,
		TaskType: taskType,
	})
	if err != nil {
		return "", err
	}
	return response.Answer, nil
}

func (a *TUIServiceAdapter) CreateSession(ctx context.Context, name string) error {
	return a.service.CreateSession(ctx, name)
}

func (a *TUIServiceAdapter) SwitchSession(ctx context.Context, name string) error {
	return a.service.SwitchSession(ctx, name)
}

func (a *TUIServiceAdapter) ListSessions(ctx context.Context) ([]terminal.SessionInfo, error) {
	sessions, err := a.service.ListSessions(ctx)
	if err != nil {
		return nil, err
	}

	currentSession := a.service.GetCurrentSession()
	result := make([]terminal.SessionInfo, len(sessions))
	for i, s := range sessions {
		result[i] = terminal.SessionInfo{
			Name:      s.ID,
			CreatedAt: s.CreatedAt.Format("2006-01-02 15:04:05"),
			IsCurrent: s.ID == currentSession,
		}
	}
	return result, nil
}

func (a *TUIServiceAdapter) GetCurrentSession() string {
	return a.service.GetCurrentSession()
}

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

	// Initialize GitHub tools
	githubTools := github.NewTools(cfg.githubToken, cfg.owner, cfg.repo)
	dbPath, err := util.CreateStoragePath(ctx, cfg.owner, cfg.repo)
	if err != nil {
		return fmt.Errorf("failed to create storage path: %w", err)
	}

	// Create MaestroService (singleton for this session)
	service, err := orchestration.NewMaestroService(ctx, &orchestration.ServiceConfig{
		MemoryType:    orchestration.MemoryInMemory,
		MemoryPath:    dbPath,
		Owner:         cfg.owner,
		Repo:          cfg.repo,
		GitHubToken:   cfg.githubToken,
		IndexWorkers:  cfg.indexWorkers,
		ReviewWorkers: cfg.reviewWorkers,
	}, githubTools)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Shutdown(shutdownCtx)
	}()

	// Create review agent with ACE integration and register with service
	var aceReviewManager *ace.Manager
	if aceManager := service.GetACEManager(); aceManager != nil && aceManager.IsEnabled() {
		aceReviewManager, _ = aceManager.GetReviewManager(ctx)
	}

	reviewAgent, err := review.NewPRReviewAgentWithACE(ctx, githubTools, dbPath, &types.AgentConfig{
		IndexWorkers:  cfg.indexWorkers,
		ReviewWorkers: cfg.reviewWorkers,
	}, aceReviewManager)
	if err != nil {
		return fmt.Errorf("failed to initialize review agent: %w", err)
	}
	service.SetReviewAgent(reviewAgent)

	// Create TUI config
	tuiConfig := &terminal.MaestroConfig{
		Owner:         cfg.owner,
		Repo:          cfg.repo,
		GitHubToken:   cfg.githubToken,
		Verbose:       cfg.verbose,
		IndexWorkers:  cfg.indexWorkers,
		ReviewWorkers: cfg.reviewWorkers,
	}

	// Create service adapter for TUI
	backend := NewTUIServiceAdapter(service, githubTools, cfg.owner, cfg.repo)

	// Start the unified Maestro TUI
	return terminal.RunMaestro(tuiConfig, backend)
}

