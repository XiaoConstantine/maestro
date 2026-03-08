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
	"github.com/XiaoConstantine/maestro/internal/github"
	"github.com/XiaoConstantine/maestro/internal/orchestration"
	"github.com/XiaoConstantine/maestro/internal/review"
	"github.com/XiaoConstantine/maestro/internal/rlm"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
	"github.com/XiaoConstantine/maestro/terminal"
	"github.com/anthropics/anthropic-sdk-go"
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

	// RLM-specific provider configuration
	rlmProvider string // "anthropic", "openai" for RLM processing
	rlmModel    string // Model name for RLM (e.g., "gpt-4o", "claude-sonnet-4-5")
}

const (
	DefaultModelProvider = "llamacpp:"
	DefaultModelName     = "llamacpp:"

	// RLM-specific defaults.
	DefaultRLMProvider = "anthropic"
	DefaultRLMModel    = "" // Use provider default
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

	// RLM provider flags - these control which LLM provider is used for RLM processing
	rootCmd.PersistentFlags().StringVar(&cfg.rlmProvider, "rlm-provider", DefaultRLMProvider, "LLM provider for RLM processing (anthropic, openai, codex, claude-code)")
	rootCmd.PersistentFlags().StringVar(&cfg.rlmModel, "rlm-model", DefaultRLMModel, "Model name for RLM (e.g., gpt-4o, gpt-4o-mini, o3, o3-mini, claude-sonnet-4-5)")

	// Mark required flags
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		// Skip github token check for benchmark command
		if cmd.Name() == "benchmark" {
			return
		}
		if cfg.githubToken == "" {
			fmt.Fprintln(os.Stderr, "GitHub token required via --github-token or MAESTRO_GITHUB_TOKEN")
			os.Exit(1)
		}
	}

	// Add benchmark subcommand
	benchmarkCmd := createBenchmarkCmd(cfg)
	rootCmd.AddCommand(benchmarkCmd)

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
	githubTools, err := github.NewToolsWithError(cfg.githubToken, cfg.owner, cfg.repo)
	if err != nil {
		return fmt.Errorf("github client is not initialized: %w", err)
	}

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

	githubTools, err := github.NewToolsWithError(cfg.githubToken, cfg.owner, cfg.repo)
	if err != nil {
		return fmt.Errorf("github client is not initialized: %w", err)
	}

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

	return formatAskResponse(response.Answer, response.Metadata), nil
}

// formatAskResponse formats an ask response with sources and confidence.
func formatAskResponse(answer string, metadata map[string]interface{}) string {
	var result strings.Builder
	result.WriteString(answer)

	// Add source files if available
	if sources, ok := metadata["sources"].([]string); ok && len(sources) > 0 {
		result.WriteString("\n\n📁 Sources explored:\n")
		for _, s := range sources {
			result.WriteString(fmt.Sprintf("  • %s\n", s))
		}
	}

	// Add confidence indicator
	if confidence, ok := metadata["confidence"].(float64); ok {
		if confidence < 0.5 {
			result.WriteString(fmt.Sprintf("\n⚠️  Confidence: %.0f%% - results may be incomplete", confidence*100))
		} else {
			result.WriteString(fmt.Sprintf("\n✓ Confidence: %.0f%%", confidence*100))
		}
	}

	return result.String()
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

func (a *TUIServiceAdapter) AskWithRLM(ctx context.Context, question string, opts terminal.RLMOptions) (string, error) {
	response, err := a.service.ProcessRequest(ctx, orchestration.Request{
		Type:     orchestration.RequestRLM,
		Question: question,
		Context: map[string]interface{}{
			"content_path":   opts.ContentPath,
			"max_iterations": opts.MaxIterations,
			"model_tier":     opts.ModelTier,
		},
		OnProgress: opts.OnProgress,
	})
	if err != nil {
		return "", err
	}

	// Format response with statistics
	result := response.Answer
	if response.Metadata != nil {
		result += "\n\n📊 RLM Statistics:\n"

		if iterations, ok := response.Metadata["iterations"].(int); ok {
			result += fmt.Sprintf("  • Iterations: %d\n", iterations)
		}

		// Token breakdown
		totalTokens, _ := response.Metadata["total_tokens"].(int)
		rootTokens, _ := response.Metadata["root_tokens"].(int)
		subTokens, _ := response.Metadata["sub_tokens"].(int)
		promptTokens, _ := response.Metadata["prompt_tokens"].(int)
		completionTokens, _ := response.Metadata["completion_tokens"].(int)

		if totalTokens > 0 {
			result += fmt.Sprintf("  • Total tokens: %d (prompt: %d, completion: %d)\n",
				totalTokens, promptTokens, completionTokens)
			result += fmt.Sprintf("  • Token breakdown: root=%d, sub-agents=%d\n", rootTokens, subTokens)
		}

		// Token savings
		if savings, ok := response.Metadata["token_savings"].(float64); ok && savings > 0 {
			result += fmt.Sprintf("  • Token savings vs naive: %.1f%%\n", savings*100)
		}

		// Cost
		if cost, ok := response.Metadata["cost_usd"].(float64); ok && cost > 0 {
			result += fmt.Sprintf("  • Estimated cost: $%.4f\n", cost)
		}

		// Duration
		if durationMs, ok := response.Metadata["duration_ms"].(int64); ok {
			result += fmt.Sprintf("  • Duration: %.1fs\n", float64(durationMs)/1000)
		}

		// Status
		if status, ok := response.Metadata["status"].(string); ok && status != "success" {
			result += fmt.Sprintf("  • Status: %s\n", status)
		}
	}
	return result, nil
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
	githubTools, err := github.NewToolsWithError(cfg.githubToken, cfg.owner, cfg.repo)
	if err != nil {
		return fmt.Errorf("github client is not initialized: %w", err)
	}
	dbPath, err := util.CreateStoragePath(ctx, cfg.owner, cfg.repo)
	if err != nil {
		return fmt.Errorf("failed to create storage path: %w", err)
	}

	// Resolve RLM API key based on RLM provider (not main provider)
	// This prevents using an incompatible key when providers differ
	rlmProvider := strings.ToLower(cfg.rlmProvider)
	mainProvider := strings.ToLower(cfg.modelProvider)
	var rlmAPIKey string

	switch rlmProvider {
	case "openai", "codex":
		// For OpenAI/Codex RLM, prefer OAuth subscription token, then API key.
		rlmAPIKey = util.FirstNonEmpty(
			os.Getenv("OPENAI_OAUTH_TOKEN"),
			os.Getenv("OPENAI_API_KEY"),
		)
		if rlmAPIKey == "" && (mainProvider == "openai" || mainProvider == "codex") {
			rlmAPIKey = cfg.apiKey
		}
	case "anthropic":
		// For Anthropic RLM, prefer Anthropic env vars, fall back to main key only if main provider is also Anthropic
		rlmAPIKey = util.FirstNonEmpty(
			os.Getenv("ANTHROPIC_OAUTH_TOKEN"),
			os.Getenv("ANTHROPIC_API_KEY"),
			os.Getenv("CLAUDE_API_KEY"),
		)
		if rlmAPIKey == "" && mainProvider == "anthropic" {
			rlmAPIKey = cfg.apiKey
		}
	case "claude-code", "cc":
		// Claude Code uses CLI auth, no API key needed
		rlmAPIKey = ""
	default:
		// For other providers, use main API key if providers match
		if rlmProvider == mainProvider {
			rlmAPIKey = cfg.apiKey
		}
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
		RLMProvider:   cfg.rlmProvider,
		RLMModel:      cfg.rlmModel,
		RLMAPIKey:     rlmAPIKey,
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

// createBenchmarkCmd creates the benchmark subcommand for RLM efficiency testing.
func createBenchmarkCmd(cfg *config) *cobra.Command {
	var (
		benchMode   string
		iterations  int
		warmupRuns  int
		outputDir   string
		testDir     string
		tags        []string
		excludeTags []string
		verbose     bool
		contextFile string
		query       string
	)

	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Run RLM efficiency benchmarks",
		Long: `Run benchmarks comparing Direct vs RLM approaches for context processing.

Modes:
  direct  - Run only direct context stuffing
  rlm     - Run only RLM recursive processing
  ab      - Run both and compare (default)

Examples:
  # Run A/B comparison with default test cases
  maestro benchmark --rlm-provider anthropic
  maestro benchmark --rlm-provider google --rlm-model gemini-2.5-flash

  # Benchmark a specific file/directory
  maestro benchmark --context ./src --query "Explain the architecture"

  # Run with custom iterations
  maestro benchmark --iterations 5 --warmup 2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBenchmark(cfg, benchMode, iterations, warmupRuns, outputDir, testDir, tags, excludeTags, verbose, contextFile, query)
		},
	}

	cmd.Flags().StringVar(&benchMode, "mode", "ab", "Benchmark mode: direct, rlm, or ab (both)")
	cmd.Flags().IntVar(&iterations, "iterations", 3, "Number of iterations per test")
	cmd.Flags().IntVar(&warmupRuns, "warmup", 1, "Number of warmup runs before measurement")
	cmd.Flags().StringVar(&outputDir, "output", filepath.Join(os.TempDir(), "maestro-benchmark-results"), "Output directory for reports")
	cmd.Flags().StringVar(&testDir, "test-dir", "", "Directory containing test case files")
	cmd.Flags().StringSliceVar(&tags, "tags", nil, "Filter tests by tags (e.g., small,medium)")
	cmd.Flags().StringSliceVar(&excludeTags, "exclude-tags", nil, "Exclude tests by tags")
	cmd.Flags().BoolVar(&verbose, "verbose", true, "Verbose output")
	cmd.Flags().StringVar(&contextFile, "context", "", "File or directory to use as context")
	cmd.Flags().StringVar(&query, "query", "", "Query to run against the context")

	return cmd
}

func runBenchmark(cfg *config, mode string, iterations, warmupRuns int, outputDir, testDir string, tags, excludeTags []string, verbose bool, contextFile, query string) error {
	ctx := context.Background()

	// Configure dspy-go logger so RLM/Predict internals are captured
	logLevel := logging.INFO
	if verbose {
		logLevel = logging.DEBUG
	}
	benchLogPath := filepath.Join(os.TempDir(), "maestro-benchmark-dspy.log")
	fileOutput, _ := logging.NewFileOutput(
		benchLogPath,
		logging.WithRotation(100*1024*1024, 5),
		logging.WithJSONFormat(true),
	)
	benchLogger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  []logging.Output{fileOutput},
	})
	logging.SetLogger(benchLogger)

	benchmarkTraceDir := filepath.Join(os.TempDir(), "maestro-rlm-traces")
	if err := os.MkdirAll(benchmarkTraceDir, 0o755); err != nil && verbose {
		fmt.Printf("Warning: failed to create benchmark trace directory %q: %v\n", benchmarkTraceDir, err)
	}

	fmt.Printf("dspy-go log: %s\n", benchLogPath)
	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("         MAESTRO RLM BENCHMARK             ")
	fmt.Println("═══════════════════════════════════════════")
	fmt.Printf("Provider: %s\n", cfg.rlmProvider)
	fmt.Printf("Model: %s\n", cfg.rlmModel)
	fmt.Printf("Mode: %s\n", mode)
	fmt.Println()

	// Resolve API key based on RLM provider
	rlmProvider := strings.ToLower(cfg.rlmProvider)
	var rlmAPIKey string
	switch rlmProvider {
	case "openai", "codex":
		rlmAPIKey = util.FirstNonEmpty(
			os.Getenv("OPENAI_OAUTH_TOKEN"),
			os.Getenv("OPENAI_API_KEY"),
		)
	case "google", "gemini":
		rlmAPIKey = util.FirstNonEmpty(
			os.Getenv("GEMINI_API_KEY"),
			os.Getenv("GOOGLE_API_KEY"),
		)
	case "anthropic":
		rlmAPIKey = util.FirstNonEmpty(
			os.Getenv("ANTHROPIC_API_KEY"),
			os.Getenv("CLAUDE_API_KEY"),
		)
	case "claude-code", "cc":
		// Claude Code doesn't need API key
	default:
		rlmAPIKey = cfg.apiKey
	}

	if rlmAPIKey == "" && rlmProvider != "claude-code" && rlmProvider != "cc" {
		return fmt.Errorf("API key required for provider %s (set via environment variable)", rlmProvider)
	}

	// Create the direct LLM and RLM processor based on provider
	var directLLM core.LLM
	var processor *rlm.Processor
	var err error

	switch rlmProvider {
	case "claude-code", "cc":
		// Use Claude Code CLI for both direct and RLM modes
		// This uses your Claude Max/Pro subscription
		claudeCodeLLM := rlm.NewClaudeCodeLLM(rlm.ClaudeCodeConfig{})
		directLLM = claudeCodeLLM

		processorConfig := rlm.ProcessorConfig{
			Provider: "claude-code",
			Verbose:  verbose,
			TraceDir: benchmarkTraceDir,
		}
		processor, err = rlm.NewProcessor(processorConfig)
		if err != nil {
			return fmt.Errorf("failed to create processor: %w", err)
		}

	case "anthropic":
		model := cfg.rlmModel
		if model == "" {
			model = "claude-sonnet-4-5-20250929"
		}
		directLLM, err = llms.NewAnthropicLLM(rlmAPIKey, anthropic.Model(model))
		if err != nil {
			return fmt.Errorf("failed to create direct LLM: %w", err)
		}

		subClient, err := rlm.NewTieredSubClientFromConfig(rlm.ProviderConfig{
			Provider: rlmProvider,
			Model:    cfg.rlmModel,
			APIKey:   rlmAPIKey,
		})
		if err != nil {
			return fmt.Errorf("failed to create tiered client: %w", err)
		}

		processorConfig := rlm.ProcessorConfig{Verbose: verbose, TraceDir: benchmarkTraceDir}
		processor, err = rlm.NewProcessorWithLLM(directLLM, subClient, processorConfig)
		if err != nil {
			return fmt.Errorf("failed to create processor: %w", err)
		}

	case "openai", "codex":
		model := cfg.rlmModel
		if model == "" {
			model = "gpt-4o"
		}
		directLLM, err = llms.NewOpenAI(core.ModelID(model), rlmAPIKey)
		if err != nil {
			return fmt.Errorf("failed to create direct LLM: %w", err)
		}

		subClient, err := rlm.NewTieredSubClientFromConfig(rlm.ProviderConfig{
			Provider: rlmProvider,
			Model:    cfg.rlmModel,
			APIKey:   rlmAPIKey,
		})
		if err != nil {
			return fmt.Errorf("failed to create tiered client: %w", err)
		}

		processorConfig := rlm.ProcessorConfig{Verbose: verbose, TraceDir: benchmarkTraceDir}
		processor, err = rlm.NewProcessorWithLLM(directLLM, subClient, processorConfig)
		if err != nil {
			return fmt.Errorf("failed to create processor: %w", err)
		}

	case "google", "gemini":
		model := cfg.rlmModel
		if model == "" {
			model = "gemini-2.5-flash"
		}
		directLLM, err = llms.NewGeminiLLM(rlmAPIKey, core.ModelID(model))
		if err != nil {
			return fmt.Errorf("failed to create direct Gemini LLM: %w", err)
		}

		subClient, err := rlm.NewTieredSubClientFromConfig(rlm.ProviderConfig{
			Provider: rlmProvider,
			Model:    cfg.rlmModel,
			APIKey:   rlmAPIKey,
		})
		if err != nil {
			return fmt.Errorf("failed to create tiered client: %w", err)
		}

		processorConfig := rlm.ProcessorConfig{Verbose: verbose, TraceDir: benchmarkTraceDir}
		processor, err = rlm.NewProcessorWithLLM(directLLM, subClient, processorConfig)
		if err != nil {
			return fmt.Errorf("failed to create processor: %w", err)
		}

	default:
		return fmt.Errorf("unsupported provider for benchmark: %s (supported: anthropic, openai, google, claude-code)", rlmProvider)
	}

	// If context and query provided, run single benchmark
	if contextFile != "" && query != "" {
		return runSingleBenchmark(ctx, processor, directLLM, mode, contextFile, query, iterations, warmupRuns, verbose)
	}

	// Otherwise run the full suite
	cliConfig := rlm.BenchmarkCLIConfig{
		Mode:       mode,
		Iterations: iterations,
		WarmupRuns: warmupRuns,
		OutputDir:  outputDir,
		TestDir:    testDir,
		Tags:       tags,
		ExcludeTag: excludeTags,
		Verbose:    verbose,
	}

	runner := rlm.NewBenchmarkRunner(cliConfig, processor, directLLM)
	report, err := runner.Run(ctx)
	if err != nil {
		return fmt.Errorf("benchmark failed: %w", err)
	}

	// Print efficiency report
	fmt.Println()
	fmt.Println(rlm.GenerateEfficiencyReport(report))

	return nil
}

func runSingleBenchmark(ctx context.Context, processor *rlm.Processor, directLLM core.LLM, mode, contextFile, query string, iterations, warmupRuns int, verbose bool) error {
	// Load context from file or directory
	var contextContent string
	info, err := os.Stat(contextFile)
	if err != nil {
		return fmt.Errorf("failed to stat context file: %w", err)
	}

	if info.IsDir() {
		// Gather context from directory
		var builder strings.Builder
		err = filepath.Walk(contextFile, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			// Only include code files
			ext := strings.ToLower(filepath.Ext(path))
			codeExts := map[string]bool{
				".go": true, ".py": true, ".js": true, ".ts": true,
				".java": true, ".c": true, ".cpp": true, ".h": true,
				".rs": true, ".rb": true, ".php": true, ".md": true,
			}
			if !codeExts[ext] {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			relPath, _ := filepath.Rel(contextFile, path)
			builder.WriteString(fmt.Sprintf("=== %s ===\n", relPath))
			builder.Write(content)
			builder.WriteString("\n\n")
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to gather context: %w", err)
		}
		contextContent = builder.String()
	} else {
		content, err := os.ReadFile(contextFile)
		if err != nil {
			return fmt.Errorf("failed to read context file: %w", err)
		}
		contextContent = string(content)
	}

	fmt.Printf("Context size: %d bytes (~%d tokens)\n", len(contextContent), len(contextContent)/4)
	fmt.Printf("Query: %s\n\n", query)

	tc := rlm.TestCase{
		ID:      "single-benchmark",
		Name:    "Single Benchmark",
		Context: contextContent,
		Query:   query,
	}

	config := rlm.BenchmarkConfig{
		Mode:           rlm.BenchmarkMode(mode),
		Iterations:     iterations,
		WarmupRuns:     warmupRuns,
		Timeout:        10 * time.Minute,
		CollectQuality: false,
	}

	benchmarker := rlm.NewBenchmarker(config, processor, directLLM)
	result, err := benchmarker.RunTestCase(ctx, tc)
	if err != nil {
		return fmt.Errorf("benchmark failed: %w", err)
	}

	// Print results
	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("                 RESULTS                   ")
	fmt.Println("═══════════════════════════════════════════")

	if len(result.DirectRuns) > 0 {
		printModeResults("DIRECT MODE", result.DirectStats)
	}

	if len(result.RLMRuns) > 0 {
		printModeResults("RLM MODE", result.RLMStats)
	}

	if len(result.DirectRuns) > 0 && len(result.RLMRuns) > 0 {
		fmt.Println("\n  COMPARISON:")
		fmt.Printf("  Token Savings:    %.1f%%\n", result.Comparison.TokenSavingsPercent)
		fmt.Printf("  Cost Savings:     %.1f%%\n", result.Comparison.CostSavingsPercent)
		fmt.Printf("  Latency Overhead: %.1f%%\n", result.Comparison.LatencyDiffPercent)
		fmt.Printf("  Prompt Pressure Reduction: %.1f%%\n", result.Comparison.PromptPressureReductionPercent)
		fmt.Printf("\n  %s\n", result.Comparison.Recommendation)
	}

	return nil
}

func printModeResults(label string, stats rlm.RunStats) {
	fmt.Printf("\n%s (%d/%d succeeded):\n", label, stats.SuccessfulRuns, stats.TotalRuns)
	if stats.SuccessfulRuns == 0 {
		fmt.Println("  ALL RUNS FAILED")
		for _, e := range stats.Errors {
			fmt.Printf("  Error: %s\n", e)
		}
		return
	}
	fmt.Printf("  Avg Tokens:   %.0f\n", stats.AvgTotalTokens)
	fmt.Printf("  Avg Duration: %.0fms\n", stats.AvgDuration)
	fmt.Printf("  Avg Max Prompt: %.0f tokens (fill %.3f)\n",
		stats.AvgMaxPromptTokens, stats.AvgPeakPromptFillRatio)
	fmt.Printf("  Total Cost:   $%.4f\n", stats.TotalCostUSD)
	if stats.FailedRuns > 0 {
		fmt.Printf("  Failed Runs:  %d\n", stats.FailedRuns)
		for _, e := range stats.Errors {
			fmt.Printf("    Error: %s\n", e)
		}
	}
}
