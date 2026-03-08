// Package rlm provides RLM (Recursive Language Model) integration for Maestro.
// RLM enables large-context processing by keeping state in REPL variables
// and using targeted sub-agent queries instead of verbalized conversation context.
package rlm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

// ProcessorConfig configures the RLM processor behavior.
type ProcessorConfig struct {
	// MaxIterations bounds the RLM loop iterations (default: 30)
	MaxIterations int

	// Timeout for the entire RLM task (default: 10 minutes)
	Timeout time.Duration

	// TraceDir for RLM trace logs (default: ~/.maestro/rlm_traces/)
	TraceDir string

	// CheckpointInterval: checkpoint every N iterations (default: 5)
	CheckpointInterval int

	// Verbose enables detailed logging
	Verbose bool

	// ModelTier selects the model tier: "fast", "smart", or "best"
	ModelTier string

	// Provider specifies the LLM provider: "anthropic", "openai", "codex", "claude-code" (default: "anthropic")
	Provider string

	// Model specifies the model to use (provider-specific)
	Model string

	// APIKey for the provider (if not set, uses environment variables)
	APIKey string

	// WorkDir is the working directory for claude-code provider
	WorkDir string

	// OnProgress callback for progress updates
	OnProgress func(ProgressEvent)

	// BatchConfig for parallel sub-agent calls
	BatchConfig BatchConfig

	// BudgetConfig for token budget management (Phase 6)
	BudgetConfig *BudgetConfig

	// CheckpointConfig for checkpoint/resume functionality (Phase 6)
	CheckpointConfig *CheckpointConfig

	// EnableRouting enables cross-agent query routing (Phase 6)
	EnableRouting bool

	// RouterConfig for cross-agent orchestration (Phase 6)
	RouterConfig *RouterConfig

	// ContextIndexConfig for persistent context caching (Phase 6)
	ContextIndexConfig *ContextIndexConfig
}

// BatchConfig controls parallel execution of sub-agent calls.
type BatchConfig struct {
	MaxConcurrent   int
	RateLimitPerSec float64
	TimeoutPerCall  time.Duration
}

// ProgressEvent reports RLM execution progress.
type ProgressEvent struct {
	Iteration      int
	TotalExpected  int
	CurrentPhase   string
	TokensUsed     int
	ItemsProcessed int
	CostUSD        float64
}

// Result contains the RLM execution outcome.
type Result struct {
	Answer           string
	Iterations       int
	TotalTokens      int
	PromptTokens     int
	CompletionTokens int
	RootTokens       int
	SubTokens        int
	TokenSavings     float64 // Estimated savings vs. naive approach (0.0-1.0)
	Duration         time.Duration
	CostUSD          float64
	Checkpoints      []Checkpoint
	PartialOutput    string // Available if interrupted
	Status           Status
}

// Status indicates the RLM execution outcome.
type Status int

const (
	StatusSuccess Status = iota
	StatusTimeout
	StatusMaxIterations
	StatusError
	StatusPartial
)

func (s Status) String() string {
	switch s {
	case StatusSuccess:
		return "success"
	case StatusTimeout:
		return "timeout"
	case StatusMaxIterations:
		return "max_iterations"
	case StatusError:
		return "error"
	case StatusPartial:
		return "partial"
	default:
		return "unknown"
	}
}

// Checkpoint captures RLM state for resume capability.
type Checkpoint struct {
	Iteration  int
	REPLState  map[string]any
	TokensUsed int
	CostUSD    float64
	Timestamp  time.Time
}

// Processor orchestrates RLM execution in Maestro.
type Processor struct {
	config    ProcessorConfig
	rootLLM   core.LLM
	rlmModule *rlm.RLM
	subClient rlm.SubLLMClient // Interface to support TieredSubClient, ClaudeCodeAdapter, etc.

	// Phase 6 components
	budget       *BudgetManager
	checkpoint   *CheckpointManager
	router       *QueryRouter
	contextIndex *ContextIndex
}

// DefaultConfig returns sensible defaults for RLM processing.
func DefaultConfig() ProcessorConfig {
	// Default trace directory: ~/.maestro/rlm_traces/
	homeDir, _ := os.UserHomeDir()
	defaultTraceDir := filepath.Join(homeDir, ".maestro", "rlm_traces")

	return ProcessorConfig{
		MaxIterations:      30,
		Timeout:            10 * time.Minute,
		TraceDir:           defaultTraceDir,
		CheckpointInterval: 5,
		Verbose:            false,
		BatchConfig: BatchConfig{
			MaxConcurrent:   10,
			RateLimitPerSec: 5.0,
			TimeoutPerCall:  60 * time.Second,
		},
	}
}

// NewProcessor creates an RLM processor using the default LLM from the global registry.
// This is a convenience method for CLI usage that sets up everything internally.
// If Provider is specified in config, it creates a provider-specific SubAgent.
func NewProcessor(config ProcessorConfig) (*Processor, error) {
	// Map model tier to default tier enum
	defaultTier := TierSmart
	switch config.ModelTier {
	case "fast":
		defaultTier = TierFast
	case "best":
		defaultTier = TierBest
	}

	provider := strings.ToLower(config.Provider)

	// Handle claude-code provider - uses CLI subprocess for both orchestration and sub-queries.
	// This enables using Claude Max/Pro subscription without API keys.
	if provider == "claude-code" || provider == "cc" {
		claudeCodeLLM := NewClaudeCodeLLM(ClaudeCodeConfig{
			WorkDir: config.WorkDir,
		})

		// Use the same Claude Code instance for both root LLM and sub-queries
		// This ensures all calls go through the subscription
		return NewProcessorWithLLM(claudeCodeLLM, claudeCodeLLM.GetAdapter(), config)
	}

	// If provider is explicitly specified, create a tiered sub-client for that provider
	if provider != "" && provider != "llamacpp" && provider != "llamacpp:" && provider != "ollama" {
		subClient, err := NewTieredSubClientFromConfig(ProviderConfig{
			Provider: provider,
			Model:    config.Model,
			APIKey:   config.APIKey,
			WorkDir:  config.WorkDir,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create %s sub-client: %w", provider, err)
		}

		// Get root LLM from provider config as well
		rootAgent, err := NewSubAgentFromConfig(ProviderConfig{
			Provider: provider,
			Model:    config.Model,
			APIKey:   config.APIKey,
			WorkDir:  config.WorkDir,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create root LLM for %s: %w", provider, err)
		}

		// Extract the underlying LLM from the adapter
		adapter, ok := rootAgent.(*LLMSubAgentAdapter)
		if !ok {
			return nil, fmt.Errorf("unexpected SubAgent type for root LLM (got %T)", rootAgent)
		}

		return NewProcessorWithLLM(adapter.llm, subClient, config)
	}

	// Fall back to default LLM from core registry
	defaultLLM := core.GetDefaultLLM()
	if defaultLLM == nil {
		return nil, fmt.Errorf("no default LLM configured - call core.ConfigureDefaultLLM first or specify --provider")
	}

	// Create tiered sub-client using the same LLM for all tiers initially
	// In production, different tiers would use different models
	subClient, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel:    defaultLLM,
		DefaultTier:   defaultTier,
		MaxConcurrent: config.BatchConfig.MaxConcurrent,
		Timeout:       config.BatchConfig.TimeoutPerCall,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create sub-client: %w", err)
	}

	return NewProcessorWithLLM(defaultLLM, subClient, config)
}

// NewProcessorWithLLM creates an RLM processor with explicit LLM configuration.
// The rootLLM is used for the RLM orchestration loop.
// The subClient handles sub-agent queries (can be TieredSubClient, ClaudeCodeAdapter, etc.)
func NewProcessorWithLLM(rootLLM core.LLM, subClient rlm.SubLLMClient, config ProcessorConfig) (*Processor, error) {
	if config.MaxIterations == 0 {
		config.MaxIterations = 30
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Minute
	}
	if config.CheckpointInterval == 0 {
		config.CheckpointInterval = 5
	}
	if config.TraceDir == "" {
		// Apply default trace directory
		homeDir, _ := os.UserHomeDir()
		config.TraceDir = filepath.Join(homeDir, ".maestro", "rlm_traces")
	}

	processor := &Processor{
		config:    config,
		rootLLM:   rootLLM,
		subClient: subClient,
	}
	effectiveRootLLM := rootLLM
	effectiveSubClient := subClient
	effectiveSubAgent := toSubAgent(subClient)

	// Initialize Phase 6 components

	// Budget management
	if config.BudgetConfig != nil {
		processor.budget = NewBudgetManager(*config.BudgetConfig)
		effectiveRootLLM = NewBudgetAwareLLM(rootLLM, processor.budget)
		if effectiveSubAgent == nil {
			return nil, fmt.Errorf("budget management requires a SubAgent-compatible sub-client (got %T)", subClient)
		}
		budgeted := NewBudgetAwareSubClient(effectiveSubAgent, processor.budget)
		effectiveSubClient = budgeted
		effectiveSubAgent = budgeted
	}

	// Checkpoint management
	if config.CheckpointConfig != nil {
		var err error
		processor.checkpoint, err = NewCheckpointManager(*config.CheckpointConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize checkpoint manager: %w", err)
		}
	} else if config.CheckpointInterval > 0 {
		// Use default checkpoint config if interval is set
		var err error
		processor.checkpoint, err = NewCheckpointManager(CheckpointConfig{
			Interval: config.CheckpointInterval,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize checkpoint manager: %w", err)
		}
	}

	// Context index
	if config.ContextIndexConfig != nil {
		var err error
		processor.contextIndex, err = NewContextIndex(*config.ContextIndexConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize context index: %w", err)
		}
	}

	// Optional cross-agent routing: route sub-queries to best available backend.
	if config.EnableRouting {
		if effectiveSubAgent == nil {
			return nil, fmt.Errorf("query routing requires a SubAgent-compatible sub-client (got %T)", effectiveSubClient)
		}

		registry := NewSubAgentRegistry()
		if err := registerSubAgentIfMissing(registry, effectiveSubAgent); err != nil {
			return nil, fmt.Errorf("failed to register base sub-agent for routing: %w", err)
		}
		for _, candidate := range GlobalRegistry().All() {
			candidateForRouter := candidate
			if processor.budget != nil {
				candidateForRouter = NewBudgetAwareSubClient(candidate, processor.budget)
			}
			if err := registerSubAgentIfMissing(registry, candidateForRouter); err != nil {
				return nil, fmt.Errorf("failed to register global sub-agent %q: %w", candidate.Name(), err)
			}
		}

		routerCfg := buildRouterConfig(config.RouterConfig, effectiveSubAgent.Name())
		processor.router = NewQueryRouter(registry, routerCfg)
		effectiveSubClient = NewRouterSubClient(processor.router)
	}

	processor.subClient = effectiveSubClient
	processor.rootLLM = effectiveRootLLM

	// Build RLM options
	opts := []rlm.Option{
		rlm.WithMaxIterations(config.MaxIterations),
		rlm.WithTimeout(config.Timeout),
		rlm.WithTraceDir(config.TraceDir),
		rlm.WithHistoryCompression(3, 500),
	}

	if config.Verbose {
		opts = append(opts, rlm.WithVerbose(true))
	}

	if config.OnProgress != nil || processor.checkpoint != nil {
		opts = append(opts, rlm.WithProgressHandler(func(progress rlm.IterationProgress) {
			totalPromptTokens, totalCompletionTokens, totalTokens, costUSD := processor.currentUsageSnapshot()
			if processor.checkpoint != nil {
				processor.checkpoint.UpdateState(
					progress.CurrentIteration,
					nil, // REPL variable serialization is not exposed by dspy-go today.
					TokenUsage{
						PromptTokens:     totalPromptTokens,
						CompletionTokens: totalCompletionTokens,
						TotalTokens:      totalTokens,
					},
					costUSD,
				)
				if processor.checkpoint.ShouldCheckpoint(progress.CurrentIteration) {
					_ = processor.checkpoint.Save()
				}
			}

			if config.OnProgress != nil {
				config.OnProgress(ProgressEvent{
					Iteration:      progress.CurrentIteration,
					TotalExpected:  progress.MaxIterations,
					CurrentPhase:   "iteration",
					TokensUsed:     totalTokens,
					ItemsProcessed: progress.CurrentIteration,
					CostUSD:        costUSD,
				})
			}
		}))
	}

	// Create RLM module with our tiered sub-client for Query calls
	// New() takes (rootLLM, subLLMClient, opts...) - subClient handles sub-queries
	processor.rlmModule = rlm.New(effectiveRootLLM, effectiveSubClient, opts...)

	return processor, nil
}

// ResetState clears per-run mutable state so benchmarks do not leak sessions,
// token counters, or budget usage across warmups and measured iterations.
func (p *Processor) ResetState() {
	if p == nil {
		return
	}

	if p.budget != nil {
		p.budget.Reset()
	}

	_ = resetIfSupported(p.rootLLM)

	if !resetIfSupported(p.subClient) {
		if routerClient, ok := p.subClient.(*RouterSubClient); ok && routerClient.router != nil && routerClient.router.registry != nil {
			for _, agent := range routerClient.router.registry.All() {
				_ = resetIfSupported(agent)
			}
		}
	}
}

// Request defines an RLM processing request.
type Request struct {
	// Context is the large context payload (code, documents, etc.)
	Context string

	// Query is the user's question or task
	Query string

	// Hints provide optional guidance for the RLM
	// TODO: Wire Hints into the RLM system prompt (PR 6 or later)
	Hints []string

	// ContentPath is the path to content to load (file or directory)
	ContentPath string

	// OnProgress callback for status updates
	OnProgress func(status string)
}

// Process executes the RLM loop for the given request.
func (p *Processor) Process(ctx context.Context, req Request) (*Result, error) {
	start := time.Now()

	// Load content from path if provided
	content := req.Context
	query := req.Query
	if content == "" && req.ContentPath != "" {
		if req.OnProgress != nil {
			req.OnProgress("Loading content...")
		}
		loaded, err := loadContent(req.ContentPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load content from %s: %w", req.ContentPath, err)
		}
		content = loaded
	}

	// Calculate baseline token estimate (naive approach would use all context)
	baselineTokens := estimateTokens(content) + estimateTokens(query)

	// Apply timeout
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	if req.OnProgress != nil {
		req.OnProgress("Processing with RLM...")
	}

	if p.budget != nil {
		if remaining := p.budget.RemainingSteps(); remaining >= 0 && remaining < 2 {
			return &Result{
					Duration: time.Since(start),
					Status:   StatusError,
				}, &BudgetError{
					Type:    BudgetStepsExceeded,
					Message: fmt.Sprintf("budget exhausted: remaining root steps %d below minimum required 2", remaining),
				}
		}
		if err := p.budget.CheckBudget(); err != nil {
			return &Result{
				Duration: time.Since(start),
				Status:   StatusError,
			}, err
		}
	}

	// Use Complete() instead of Process() to get full iteration and token data
	completionResult, err := p.rlmModule.Complete(ctx, content, query)

	duration := time.Since(start)

	// Build result
	result := &Result{
		Duration: duration,
		Status:   StatusSuccess,
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Status = StatusTimeout
		} else {
			result.Status = StatusError
		}
		// Try to extract partial output if available from completion result
		if completionResult != nil {
			result.PartialOutput = completionResult.Response
			result.Iterations = completionResult.Iterations
		}
		return result, err
	}

	// Extract results from CompletionResult
	result.Answer = completionResult.Response
	result.Iterations = completionResult.Iterations

	// Use TokenTracker as the canonical source for root/sub separation.
	tracker := p.rlmModule.GetTokenTracker()
	rootUsage := tracker.GetRootUsage()
	subUsage := tracker.GetSubUsage()
	subRLMUsage := tracker.GetSubRLMUsage()
	totalUsage := tracker.GetTotalUsage()

	result.RootTokens = rootUsage.TotalTokens
	result.SubTokens = subUsage.TotalTokens + subRLMUsage.TotalTokens
	result.PromptTokens = totalUsage.PromptTokens
	result.CompletionTokens = totalUsage.CompletionTokens
	result.TotalTokens = totalUsage.TotalTokens

	// Cost is tracked by budget manager when enabled, otherwise via sub-client stats.
	_, _, _, totalCostUSD := p.currentUsageSnapshot()
	result.CostUSD = totalCostUSD

	// Calculate token savings vs naive approach
	if baselineTokens > 0 && result.TotalTokens < baselineTokens {
		result.TokenSavings = 1.0 - float64(result.TotalTokens)/float64(baselineTokens)
	}

	return result, nil
}

// ProcessWithCheckpoints executes RLM with periodic checkpointing.
func (p *Processor) ProcessWithCheckpoints(ctx context.Context, req Request) (*Result, error) {
	if p.checkpoint == nil {
		// No checkpoint manager configured, use regular process
		return p.Process(ctx, req)
	}

	// Initialize checkpoint state
	p.checkpoint.SetQuery(req.Query)
	if req.ContentPath != "" {
		info, err := os.Stat(req.ContentPath)
		if err == nil {
			p.checkpoint.SetContextRef(ContextReference{
				Path:         req.ContentPath,
				ContentHash:  HashContent(req.Context),
				SizeBytes:    info.Size(),
				LastModified: info.ModTime(),
			})
		}
	}

	// Process with checkpointing
	result, err := p.Process(ctx, req)

	// Save final checkpoint
	if p.checkpoint != nil {
		if err != nil {
			p.checkpoint.MarkFailed(err)
		} else {
			p.checkpoint.MarkCompleted()
		}
		if saveErr := p.checkpoint.Save(); saveErr != nil && p.config.Verbose {
			fmt.Fprintf(os.Stderr, "Warning: failed to save final checkpoint: %v\n", saveErr)
		}
	}

	return result, err
}

// Resume continues an RLM execution from a checkpoint.
func (p *Processor) Resume(ctx context.Context, checkpointPath string, req Request) (*Result, error) {
	if p.checkpoint == nil {
		return nil, fmt.Errorf("checkpoint manager not configured")
	}

	// Load checkpoint
	state, err := p.checkpoint.Load(checkpointPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint: %w", err)
	}

	// Check if resumable
	resumable, warning := IsResumable(state)
	if !resumable {
		return nil, fmt.Errorf("checkpoint not resumable: %s", warning)
	}
	if warning != "" && p.config.Verbose {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
	}

	// Use original query if not provided
	if req.Query == "" {
		req.Query = state.Query
	}

	// Continue processing from checkpoint state
	// Note: The RLM module itself doesn't support state restoration yet,
	// so we start fresh but can use the partial results as hints
	if len(state.PartialResults) > 0 && len(req.Hints) == 0 {
		// Convert partial results to hints
		for _, pr := range state.PartialResults {
			if str, ok := pr.Value.(string); ok {
				req.Hints = append(req.Hints, fmt.Sprintf("Previous finding (%s): %s", pr.Key, str))
			}
		}
	}

	return p.ProcessWithCheckpoints(ctx, req)
}

// ResumeLatest resumes from the most recent checkpoint for a session.
func (p *Processor) ResumeLatest(ctx context.Context, sessionID string, req Request) (*Result, error) {
	if p.checkpoint == nil {
		return nil, fmt.Errorf("checkpoint manager not configured")
	}

	state, err := p.checkpoint.LoadLatest(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to load latest checkpoint: %w", err)
	}

	// Use the checkpoint path
	checkpoints, err := p.checkpoint.ListCheckpoints(sessionID)
	if err != nil || len(checkpoints) == 0 {
		return nil, fmt.Errorf("no checkpoints found for session %s", sessionID)
	}

	// Set session ID
	p.checkpoint.SetSessionID(state.SessionID)

	return p.Resume(ctx, checkpoints[0].Path, req)
}

// ListCheckpoints returns available checkpoints.
func (p *Processor) ListCheckpoints(sessionID string) ([]CheckpointInfo, error) {
	if p.checkpoint == nil {
		return nil, fmt.Errorf("checkpoint manager not configured")
	}
	return p.checkpoint.ListCheckpoints(sessionID)
}

// BudgetStatus returns the current budget status.
func (p *Processor) BudgetStatus() *BudgetStatus {
	if p.budget == nil {
		return nil
	}
	status := p.budget.Status()
	return &status
}

// SetBudget updates the budget limit.
func (p *Processor) SetBudget(maxBudgetUSD float64) {
	if p.budget != nil {
		p.budget.SetBudget(maxBudgetUSD)
	}
}

// ContextIndexStats returns context index statistics.
func (p *Processor) ContextIndexStats() *IndexStats {
	if p.contextIndex == nil {
		return nil
	}
	stats := p.contextIndex.Stats()
	return &stats
}

// IndexPath indexes a file or directory for faster processing.
func (p *Processor) IndexPath(path string) (int, error) {
	if p.contextIndex == nil {
		return 0, fmt.Errorf("context index not configured")
	}

	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}

	if info.IsDir() {
		return p.contextIndex.IndexDirectory(path, nil)
	}

	entries, err := p.contextIndex.IndexFile(path)
	return len(entries), err
}

type stateResetter interface {
	Reset()
}

func resetIfSupported(target any) bool {
	if resetter, ok := target.(stateResetter); ok {
		resetter.Reset()
		return true
	}
	return false
}

func toSubAgent(client rlm.SubLLMClient) SubAgent {
	if agent, ok := client.(SubAgent); ok {
		return agent
	}
	if tiered, ok := client.(*TieredSubClient); ok {
		return NewTieredSubClientAdapter(tiered, "tiered-subclient")
	}
	return nil
}

func registerSubAgentIfMissing(registry *SubAgentRegistry, agent SubAgent) error {
	if registry.HasAgent(agent.Name()) {
		return nil
	}
	return registry.Register(agent)
}

func buildRouterConfig(userConfig *RouterConfig, fallbackDefaultAgent string) RouterConfig {
	cfg := DefaultRouterConfig()
	if userConfig != nil {
		cfg = *userConfig
	}

	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = fallbackDefaultAgent
	}
	if len(cfg.AnalysisAgents) == 0 {
		cfg.AnalysisAgents = []string{cfg.DefaultAgent}
	}
	if len(cfg.CodeGenAgents) == 0 {
		cfg.CodeGenAgents = []string{cfg.DefaultAgent}
	}
	if len(cfg.FastAgents) == 0 {
		cfg.FastAgents = []string{cfg.DefaultAgent}
	}
	if len(cfg.BestAgents) == 0 {
		cfg.BestAgents = []string{cfg.DefaultAgent}
	}
	if cfg.BatchMaxConcurrent <= 0 {
		cfg.BatchMaxConcurrent = defaultBatchConcurrency
	}

	return cfg
}

// currentUsageSnapshot returns cumulative usage currently visible to the processor:
// total prompt tokens, total completion tokens, total tokens, and cost.
func (p *Processor) currentUsageSnapshot() (promptTokens, completionTokens, totalTokens int, costUSD float64) {
	if p.rlmModule != nil {
		usage := p.rlmModule.GetTokenTracker().GetTotalUsage()
		promptTokens = usage.PromptTokens
		completionTokens = usage.CompletionTokens
		totalTokens = usage.TotalTokens
	}

	// Budget manager is the canonical cost source when enabled (includes root+sub).
	if p.budget != nil {
		return promptTokens, completionTokens, totalTokens, p.budget.TotalSpent()
	}

	// Without budget manager, cost comes from sub-agent statistics.
	if p.router != nil && p.router.registry != nil {
		stats := p.router.registry.AggregateStats()
		return promptTokens, completionTokens, totalTokens, stats.TotalCostUSD
	}

	if agent, ok := p.subClient.(SubAgent); ok {
		stats := agent.Stats()
		return promptTokens, completionTokens, totalTokens, stats.TotalCostUSD
	}
	return promptTokens, completionTokens, totalTokens, 0
}

// loadContent loads content from a file or directory path.
func loadContent(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	if info.IsDir() {
		return loadDirectory(path)
	}
	return loadFile(path)
}

// loadFile loads content from a single file.
func loadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// loadDirectory recursively loads content from a directory.
// Content loading limits to prevent runaway memory usage
const (
	maxContentBytes = 50 * 1024 * 1024 // 50MB max total content
	maxFiles        = 1000             // Max files to load
	maxFileSize     = 1 * 1024 * 1024  // 1MB max per file
)

// textExtensions lists file extensions considered as text files for content loading.
var textExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".java": true, ".c": true, ".cpp": true, ".h": true, ".hpp": true,
	".rs": true, ".rb": true, ".php": true, ".swift": true, ".kt": true,
	".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".xml": true, ".html": true, ".css": true, ".scss": true,
	".sql": true, ".sh": true, ".bash": true, ".zsh": true,
	".mod": true, ".sum": true, ".lock": true,
}

// loadDirectoryState tracks accumulated size during directory traversal.
type loadDirectoryState struct {
	totalBytes int
	fileCount  int
}

func loadDirectory(dir string) (string, error) {
	state := &loadDirectoryState{}
	return loadDirectoryWithState(dir, state)
}

func loadDirectoryWithState(dir string, state *loadDirectoryState) (string, error) {
	var content strings.Builder
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		// Check limits
		if state.totalBytes >= maxContentBytes {
			break // Stop if we've reached max bytes
		}
		if state.fileCount >= maxFiles {
			break // Stop if we've reached max files
		}

		path := filepath.Join(dir, entry.Name())

		// Skip hidden files and common non-content directories
		if strings.HasPrefix(entry.Name(), ".") ||
			entry.Name() == "node_modules" ||
			entry.Name() == "vendor" ||
			entry.Name() == "__pycache__" ||
			entry.Name() == "dist" ||
			entry.Name() == "build" ||
			entry.Name() == ".git" {
			continue
		}

		if entry.IsDir() {
			subContent, err := loadDirectoryWithState(path, state)
			if err != nil {
				continue // Skip directories we can't read
			}
			content.WriteString(subContent)
		} else {
			// Only load text files (simple extension check)
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if isTextFile(ext) {
				// Check file size before reading
				info, err := entry.Info()
				if err != nil || info.Size() > maxFileSize {
					continue // Skip large files
				}

				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}

				// Check if adding this file would exceed limits
				fileContent := fmt.Sprintf("=== %s ===\n%s\n\n", path, string(data))
				if state.totalBytes+len(fileContent) > maxContentBytes {
					continue // Skip if would exceed total limit
				}

				content.WriteString(fileContent)
				state.totalBytes += len(fileContent)
				state.fileCount++
			}
		}
	}

	return content.String(), nil
}

// isTextFile checks if the file extension indicates a text file.
func isTextFile(ext string) bool {
	return textExtensions[ext]
}

// estimateTokens provides a rough token count estimate (4 chars per token).
func estimateTokens(text string) int {
	return len(text) / 4
}
