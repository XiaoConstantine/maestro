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

	// OnProgress callback for progress updates
	// TODO: Wire OnProgress to RLM iteration callbacks (PR 7)
	OnProgress func(ProgressEvent)

	// BatchConfig for parallel sub-agent calls
	// TODO: Wire BatchConfig to sub-client creation (PR 7)
	BatchConfig BatchConfig
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
	rlmModule *rlm.RLM
	subClient *TieredSubClient
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
func NewProcessor(config ProcessorConfig) (*Processor, error) {
	// Get the default LLM from core registry
	defaultLLM := core.GetDefaultLLM()
	if defaultLLM == nil {
		return nil, fmt.Errorf("no default LLM configured - call core.ConfigureDefaultLLM first")
	}

	// Map model tier to default tier enum
	defaultTier := TierSmart
	switch config.ModelTier {
	case "fast":
		defaultTier = TierFast
	case "best":
		defaultTier = TierBest
	}

	// Create tiered sub-client using the same LLM for all tiers initially
	// In production, different tiers would use different models
	subClient, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel:  defaultLLM,
		DefaultTier: defaultTier,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create sub-client: %w", err)
	}

	return NewProcessorWithLLM(defaultLLM, subClient, config)
}

// NewProcessorWithLLM creates an RLM processor with explicit LLM configuration.
// The rootLLM is used for the RLM orchestration loop.
// The subClient handles sub-agent queries with model tiering.
func NewProcessorWithLLM(rootLLM core.LLM, subClient *TieredSubClient, config ProcessorConfig) (*Processor, error) {
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

	// Build RLM options
	opts := []rlm.Option{
		rlm.WithMaxIterations(config.MaxIterations),
		rlm.WithTimeout(config.Timeout),
		rlm.WithTraceDir(config.TraceDir),
	}

	if config.Verbose {
		opts = append(opts, rlm.WithVerbose(true))
	}

	// Create RLM module with our tiered sub-client for Query calls
	// New() takes (rootLLM, subLLMClient, opts...) - subClient handles sub-queries
	rlmModule := rlm.New(rootLLM, subClient, opts...)

	return &Processor{
		config:    config,
		rlmModule: rlmModule,
		subClient: subClient,
	}, nil
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

	// Get root token usage from completion result
	result.RootTokens = completionResult.Usage.TotalTokens
	rootPromptTokens := completionResult.Usage.PromptTokens
	rootCompletionTokens := completionResult.Usage.CompletionTokens

	// Get sub-client token usage
	stats := p.subClient.Stats()
	result.SubTokens = stats.TotalPromptTokens + stats.TotalCompletionTokens

	// Combine root + sub tokens
	result.PromptTokens = rootPromptTokens + stats.TotalPromptTokens
	result.CompletionTokens = rootCompletionTokens + stats.TotalCompletionTokens
	result.CostUSD = stats.TotalCostUSD
	result.TotalTokens = result.RootTokens + result.SubTokens

	// Calculate token savings vs naive approach
	if baselineTokens > 0 && result.TotalTokens < baselineTokens {
		result.TokenSavings = 1.0 - float64(result.TotalTokens)/float64(baselineTokens)
	}

	return result, nil
}

// ProcessWithCheckpoints executes RLM with periodic checkpointing.
func (p *Processor) ProcessWithCheckpoints(ctx context.Context, req Request) (*Result, error) {
	// For now, delegate to Process. Checkpointing will be added in a future PR.
	return p.Process(ctx, req)
}

// Resume continues an RLM execution from a checkpoint.
func (p *Processor) Resume(ctx context.Context, checkpoint Checkpoint, req Request) (*Result, error) {
	// Checkpoint resume will be implemented in PR 6
	return nil, fmt.Errorf("checkpoint resume not yet implemented")
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
