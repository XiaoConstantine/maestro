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

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/ace"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	maestroace "github.com/XiaoConstantine/maestro/internal/ace"
	maestroauth "github.com/XiaoConstantine/maestro/internal/auth"
	"github.com/XiaoConstantine/maestro/internal/github"
	"github.com/XiaoConstantine/maestro/internal/orchestration"
	"github.com/XiaoConstantine/maestro/internal/review"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
	"github.com/XiaoConstantine/maestro/terminal"
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

func resolveCLIStoragePath(ctx context.Context, cfg *config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config is required")
	}
	if strings.TrimSpace(cfg.memoryPath) == "" {
		return util.CreateStoragePath(ctx, cfg.owner, cfg.repo)
	}

	path := strings.TrimSpace(cfg.memoryPath)
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to expand storage path %q: %w", path, err)
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	}

	cleaned := filepath.Clean(path)
	if strings.HasSuffix(path, string(os.PathSeparator)) || filepath.Ext(cleaned) == "" {
		if err := os.MkdirAll(cleaned, 0755); err != nil {
			return "", fmt.Errorf("failed to create storage directory %q: %w", cleaned, err)
		}
		dbName := fmt.Sprintf("%s_%s.db", cfg.owner, cfg.repo)
		return filepath.Join(cleaned, dbName), nil
	}

	if err := os.MkdirAll(filepath.Dir(cleaned), 0755); err != nil {
		return "", fmt.Errorf("failed to create storage parent for %q: %w", cleaned, err)
	}
	return cleaned, nil
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
	apiKey                 string
	githubToken            string
	owner                  string
	memoryPath             string
	repo                   string
	prNumber               int
	verbose                bool
	verifyOnly             bool
	allowCodingBash        bool
	modelProvider          string
	modelName              string
	modelConfig            string // For additional model-specific configuration
	qaArtifacts            string
	qaSkillStore           string
	qaSkillDomain          string
	reviewArtifacts        string
	reviewSkillStore       string
	reviewSkillDomain      string
	rlmOverviewSkillStore  string
	rlmOverviewSkillDomain string

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
	rootCmd.PersistentFlags().StringVar(&cfg.githubToken, "github-token", "", "Github token (defaults to MAESTRO_GITHUB_TOKEN)")
	rootCmd.PersistentFlags().StringVar(&cfg.owner, "owner", "", "Repository owner")
	rootCmd.PersistentFlags().StringVar(&cfg.repo, "repo", "", "Repository")
	rootCmd.PersistentFlags().StringVar(&cfg.memoryPath, "path", "~/.maestro/", "Path for sqlite table")
	rootCmd.PersistentFlags().IntVar(&cfg.prNumber, "pr", 0, "Pull request number")
	rootCmd.PersistentFlags().BoolVar(&cfg.verbose, "verbose", false, "Enable verbose logging")
	rootCmd.PersistentFlags().BoolVar(&cfg.verifyOnly, "verify-only", false, "Only verify token permissions")
	rootCmd.PersistentFlags().BoolVar(&cfg.allowCodingBash, "allow-coding-bash", false, "Allow the coding agent to run unrestricted shell commands in the workspace")

	rootCmd.PersistentFlags().BoolP("interactive", "i", false, "Run in interactive mode")

	rootCmd.PersistentFlags().StringP("model", "m", "", `Full model specification (e.g. "ollama:mistral:q4", "llamacpp:", "anthropic:claude-3")`)
	rootCmd.PersistentFlags().StringVar(&cfg.modelProvider, "provider", DefaultModelProvider, "Model provider (llamacpp, ollama, anthropic, google, openai, openai-codex)")
	rootCmd.PersistentFlags().StringVar(&cfg.modelName, "model-name", DefaultModelName, "Specific model name")
	rootCmd.PersistentFlags().StringVar(&cfg.modelConfig, "model-config", "", "Additional model configuration")
	rootCmd.PersistentFlags().StringVar(&cfg.qaArtifacts, "qa-artifacts", os.Getenv("MAESTRO_QA_ARTIFACTS"), "Optional path to GEPA-tuned QA artifacts JSON")
	rootCmd.PersistentFlags().StringVar(&cfg.qaSkillStore, "qa-skill-store", os.Getenv("MAESTRO_QA_SKILL_STORE"), "Optional path to the persisted QA skill store JSON")
	rootCmd.PersistentFlags().StringVar(&cfg.qaSkillDomain, "qa-skill-domain", os.Getenv("MAESTRO_QA_SKILL_DOMAIN"), "Optional persisted QA skill domain (defaults to maestro:qa)")
	rootCmd.PersistentFlags().StringVar(&cfg.reviewArtifacts, "review-artifacts", os.Getenv("MAESTRO_REVIEW_ARTIFACTS"), "Optional path to GEPA-tuned review artifacts JSON")
	rootCmd.PersistentFlags().StringVar(&cfg.reviewSkillStore, "review-skill-store", os.Getenv("MAESTRO_REVIEW_SKILL_STORE"), "Optional path to the persisted review skill store JSON")
	rootCmd.PersistentFlags().StringVar(&cfg.reviewSkillDomain, "review-skill-domain", os.Getenv("MAESTRO_REVIEW_SKILL_DOMAIN"), "Optional persisted review skill domain (defaults to maestro:review:go)")
	rootCmd.PersistentFlags().StringVar(&cfg.rlmOverviewSkillStore, "rlm-overview-skill-store", os.Getenv("MAESTRO_RLM_OVERVIEW_SKILL_STORE"), "Optional path to the persisted RLM overview skill store JSON")
	rootCmd.PersistentFlags().StringVar(&cfg.rlmOverviewSkillDomain, "rlm-overview-skill-domain", os.Getenv("MAESTRO_RLM_OVERVIEW_SKILL_DOMAIN"), "Optional persisted RLM overview skill domain (defaults to maestro:ask:rlm-overview)")

	rootCmd.PersistentFlags().IntVar(&cfg.indexWorkers, "index-workers", runtime.NumCPU(), "Number of concurrent workers for repository indexing")

	// Default to 120 workers for I/O-bound LLM API calls.
	// LLM calls are network-bound, not CPU-bound, so higher concurrency
	// improves throughput by overlapping HTTP requests.
	rootCmd.PersistentFlags().IntVar(&cfg.reviewWorkers, "review-workers", 120, "Number of concurrent workers for parallel review")

	rootCmd.AddCommand(
		&cobra.Command{
			Use:   "login openai",
			Short: "Connect a ChatGPT Plus/Pro subscription",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if !strings.EqualFold(args[0], "openai") {
					return fmt.Errorf("unsupported login provider %q", args[0])
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
				defer cancel()
				return maestroauth.LoginOpenAI(ctx, cmd.OutOrStdout())
			},
		},
		&cobra.Command{
			Use:   "logout openai",
			Short: "Remove a stored ChatGPT subscription credential",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if !strings.EqualFold(args[0], "openai") {
					return fmt.Errorf("unsupported logout provider %q", args[0])
				}
				if err := maestroauth.LogoutOpenAI(); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "OpenAI subscription disconnected.")
				return nil
			},
		},
	)

	// Mark required flags for repository operations; auth commands do not need GitHub access.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Name() == "login" || cmd.Name() == "logout" {
			return nil
		}
		if cfg.githubToken == "" {
			cfg.githubToken = strings.TrimSpace(os.Getenv("MAESTRO_GITHUB_TOKEN"))
		}
		if cfg.githubToken == "" {
			return fmt.Errorf("GitHub token required via --github-token or MAESTRO_GITHUB_TOKEN")
		}
		return nil
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
	defaultLLM, err := util.LoadLLMFromModelConfig(ctx, modelCfg, modelID)
	if err == nil {
		core.GlobalConfig.DefaultLLM = defaultLLM
	}

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

	dbPath, err := resolveCLIStoragePath(ctx, cfg)
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
		IndexWorkers:         cfg.indexWorkers,
		ReviewWorkers:        cfg.reviewWorkers,
		ReviewArtifactsPath:  cfg.reviewArtifacts,
		ReviewSkillStorePath: cfg.reviewSkillStore,
		ReviewSkillDomain:    cfg.reviewSkillDomain,
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
			validComments, err := githubTools.FilterReviewComments(ctx, prNumber, comments)
			if err != nil {
				logger.Error(ctx, "Failed to filter interactive review comments: %v", err)
				return fmt.Errorf("failed to filter interactive review comments: %w", err)
			}

			// Use the new lazygit-style TUI for reviewing comments
			onPost := func(selectedComments []types.PRReviewComment) error {
				logger.Info(ctx, "Posting %d review comments to GitHub", len(selectedComments))
				return githubTools.CreateReviewComments(ctx, prNumber, selectedComments)
			}
			if err := console.ShowCommentsInteractive(validComments, onPost); err != nil {
				logger.Error(ctx, "Interactive TUI error: %v", err)
				// Fall back to standard preview
				_, _, _ = githubTools.PreviewReview(ctx, console, prNumber, comments, agent.Metrics(ctx))
			}
		} else {
			// Standard preview flow
			validComments, shouldPost, err := githubTools.PreviewReview(ctx, console, prNumber, comments, agent.Metrics(ctx))
			if err != nil {
				logger.Error(ctx, "Failed to preview review: %v", err)
				return fmt.Errorf("failed to preview review: %w", err)
			}

			console.ShowReviewMetrics(agent.Metrics(ctx), validComments)

			if shouldPost {
				logger.Info(ctx, "Posting review comments to GitHub")
				err = githubTools.CreateReviewComments(ctx, prNumber, validComments)
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
	service *orchestration.MaestroService
	owner   string
	repo    string
}

func shortenWorkspacePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func NewTUIServiceAdapter(service *orchestration.MaestroService, owner, repo string) *TUIServiceAdapter {
	return &TUIServiceAdapter{
		service: service,
		owner:   owner,
		repo:    repo,
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

func (a *TUIServiceAdapter) RunCodingTask(ctx context.Context, prompt string, onEvent func(terminal.CodingEvent)) (string, error) {
	var sink agents.EventSink
	if onEvent != nil {
		sink = agents.EventSinkFunc(func(_ context.Context, event agents.ExecutionEvent) {
			if mapped, ok := mapCodingEvent(event); ok {
				onEvent(mapped)
			}
		})
	}
	response, err := a.service.ProcessRequest(ctx, orchestration.Request{
		Type:      orchestration.RequestCoding,
		Prompt:    prompt,
		EventSink: sink,
	})
	if err != nil {
		return "", err
	}
	return response.Answer, nil
}

func (a *TUIServiceAdapter) CancelCodingTask() bool {
	return a.service != nil && a.service.CancelCodingRun()
}

func codingPath(arguments map[string]any) string {
	if arguments == nil {
		return ""
	}
	if path, ok := arguments["path"].(string); ok {
		return path
	}
	return ""
}

func codingToolDetail(message *agents.Message) string {
	if message == nil || message.ToolResult == nil {
		return ""
	}
	contentText := strings.TrimSpace(contentBlocksText(message.ToolResult.DisplayContent))
	if contentText == "" {
		contentText = strings.TrimSpace(contentBlocksText(message.ToolResult.Content))
	}
	if path, ok := message.ToolResult.Details["path"].(string); ok && strings.TrimSpace(path) != "" {
		if contentText != "" {
			return fmt.Sprintf("%s — %s", path, contentText)
		}
		return path
	}
	return contentText
}

func contentBlocksText(blocks []core.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(block.String()); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func mapCodingEvent(event agents.ExecutionEvent) (terminal.CodingEvent, bool) {
	switch payload := event.Payload.(type) {
	case agents.RunStartedEvent:
		return terminal.CodingEvent{Kind: "run", Status: "started", Detail: payload.Task}, true
	case agents.TurnStartedEvent:
		return terminal.CodingEvent{Kind: "turn", Status: "started", Detail: fmt.Sprintf("Turn %d/%d", payload.Turn, payload.MaxTurns)}, true
	case agents.ToolExecutionStartedEvent:
		detail := fmt.Sprintf("Running %s", payload.Call.Name)
		if path := codingPath(payload.Call.Arguments); path != "" {
			detail = fmt.Sprintf("Running %s %s", payload.Call.Name, path)
		}
		return terminal.CodingEvent{Kind: "tool", Tool: payload.Call.Name, Status: "started", Detail: detail}, true
	case agents.ToolCallFinishedEvent:
		detail := codingToolDetail(payload.Result)
		if detail == "" {
			detail = fmt.Sprintf("%s %s", payload.Call.Name, payload.Status)
		}
		return terminal.CodingEvent{Kind: "tool", Tool: payload.Call.Name, Status: string(payload.Status), Detail: detail}, true
	case agents.RunFinishedEvent:
		return terminal.CodingEvent{Kind: "run", Status: string(payload.Status), Detail: payload.Diagnostic}, true
	default:
		return terminal.CodingEvent{}, false
	}
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
	if domain, ok := response.Metadata["qa_skill_domain"].(string); ok && strings.TrimSpace(domain) != "" {
		version, _ := response.Metadata["qa_skill_version"].(int)
		if version > 0 {
			result += fmt.Sprintf("\n\nQA Skill: %s v%d", domain, version)
		} else {
			result += fmt.Sprintf("\n\nQA Skill: %s (base prompt only)", domain)
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

func (a *TUIServiceAdapter) GetWorkspace() string {
	if a == nil || a.service == nil {
		return ""
	}
	return a.service.WorkspaceRoot()
}

func (a *TUIServiceAdapter) GetModelInfo() string {
	if a == nil || a.service == nil {
		return ""
	}
	return a.service.CodingModelInfo()
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
	defaultLLM, err := util.LoadLLMFromModelConfig(ctx, modelCfg, modelID)
	if err != nil {
		return fmt.Errorf("failed to configure LLM: %w", err)
	}
	core.GlobalConfig.DefaultLLM = defaultLLM

	// Initialize GitHub tools
	githubTools := github.NewTools(cfg.githubToken, cfg.owner, cfg.repo)
	dbPath, err := resolveCLIStoragePath(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create storage path: %w", err)
	}

	workspaceRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}

	// Create MaestroService (singleton for this session)
	service, err := orchestration.NewMaestroService(ctx, &orchestration.ServiceConfig{
		MemoryType:                orchestration.MemoryInMemory,
		MemoryPath:                dbPath,
		QAArtifactsPath:           cfg.qaArtifacts,
		QASkillStorePath:          cfg.qaSkillStore,
		QASkillDomain:             cfg.qaSkillDomain,
		RLMOverviewSkillStorePath: cfg.rlmOverviewSkillStore,
		RLMOverviewSkillDomain:    cfg.rlmOverviewSkillDomain,
		Owner:                     cfg.owner,
		Repo:                      cfg.repo,
		GitHubToken:               cfg.githubToken,
		IndexWorkers:              cfg.indexWorkers,
		ReviewWorkers:             cfg.reviewWorkers,
		WorkspaceRoot:             workspaceRoot,
		AllowCodingBash:           cfg.allowCodingBash,
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
		IndexWorkers:         cfg.indexWorkers,
		ReviewWorkers:        cfg.reviewWorkers,
		ReviewArtifactsPath:  cfg.reviewArtifacts,
		ReviewSkillStorePath: cfg.reviewSkillStore,
		ReviewSkillDomain:    cfg.reviewSkillDomain,
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
	backend := NewTUIServiceAdapter(service, cfg.owner, cfg.repo)

	// Start the unified Maestro TUI
	return terminal.RunMaestro(tuiConfig, backend)
}
