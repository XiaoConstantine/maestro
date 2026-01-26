package rlm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

// ClaudeCodeAdapter implements SubAgent using Claude Code CLI.
// It invokes `claude -p` with JSON output for programmatic integration.
// By default, it runs in stateless mode with no tools enabled.
type ClaudeCodeAdapter struct {
	cliPath      string   // Path to claude CLI (default: "claude")
	workDir      string   // Working directory for Claude Code
	sessionID    string   // For session resumption
	allowedTools []string // Tools to allow (empty + !enableTools = no tools)
	enableTools  bool     // Whether to enable tools
	timeout      time.Duration

	// Usage tracking
	mu           sync.Mutex
	totalPrompt  int
	totalCompl   int
	totalCost    float64
	totalQueries int
}

// ClaudeCodeConfig configures the Claude Code adapter.
type ClaudeCodeConfig struct {
	CLIPath      string        // Path to claude CLI (default: "claude")
	WorkDir      string        // Working directory
	AllowedTools []string      // Restrict to specific tools (empty = stateless/no tools)
	EnableTools  bool          // Set to true to enable default tools (Read, Grep, etc.)
	Timeout      time.Duration // Timeout per query (default: 5 minutes)
	SessionID    string        // Resume existing session
}

// ClaudeCodeResponse matches the JSON output from `claude -p --output-format json`.
type ClaudeCodeResponse struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Result    string `json:"result"`
	IsError   bool   `json:"is_error"`
	NumTurns  int    `json:"num_turns"`

	DurationMS    int     `json:"duration_ms"`
	DurationAPIMS int     `json:"duration_api_ms"`
	TotalCostUSD  float64 `json:"total_cost_usd"`

	Usage struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`

	ModelUsage map[string]struct {
		InputTokens   int     `json:"inputTokens"`
		OutputTokens  int     `json:"outputTokens"`
		CostUSD       float64 `json:"costUSD"`
		ContextWindow int     `json:"contextWindow"`
	} `json:"modelUsage"`

	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`
	Errors           []string        `json:"errors,omitempty"`
}

// NewClaudeCodeAdapter creates a SubAgent backed by Claude Code CLI.
// By default, it runs stateless with no tools. Set EnableTools=true or
// provide AllowedTools to enable specific tools.
func NewClaudeCodeAdapter(config ClaudeCodeConfig) *ClaudeCodeAdapter {
	cliPath := config.CLIPath
	if cliPath == "" {
		cliPath = "claude"
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	// If EnableTools is true but AllowedTools is empty, use default read-only tools
	allowedTools := config.AllowedTools
	enableTools := config.EnableTools
	if enableTools && len(allowedTools) == 0 {
		allowedTools = []string{"Read", "Grep", "Glob"}
	}

	return &ClaudeCodeAdapter{
		cliPath:      cliPath,
		workDir:      config.WorkDir,
		sessionID:    config.SessionID,
		allowedTools: allowedTools,
		enableTools:  enableTools,
		timeout:      timeout,
	}
}

// Query implements rlm.SubLLMClient.
func (a *ClaudeCodeAdapter) Query(ctx context.Context, prompt string) (rlm.QueryResponse, error) {
	args := []string{
		"-p", prompt,
		"--output-format", "json",
	}

	// Handle tool permissions
	if len(a.allowedTools) > 0 {
		// Explicitly allow only these tools
		args = append(args, "--allowedTools", strings.Join(a.allowedTools, ","))
	} else if !a.enableTools {
		// Stateless mode: disable all tools by allowing none
		// This makes Claude Code operate in pure LLM mode without tool access
		args = append(args, "--allowedTools", "")
	}
	// If enableTools is true but allowedTools is empty, use Claude Code defaults

	// Resume session if available
	if a.sessionID != "" {
		args = append(args, "--resume", a.sessionID)
	}

	// Apply timeout via context
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.cliPath, args...)
	if a.workDir != "" {
		cmd.Dir = a.workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Check if it's a context timeout
		if ctx.Err() == context.DeadlineExceeded {
			return rlm.QueryResponse{}, fmt.Errorf("claude-code timed out after %v", a.timeout)
		}
		return rlm.QueryResponse{}, fmt.Errorf("claude-code failed: %w, stderr: %s", err, stderr.String())
	}

	var resp ClaudeCodeResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		// Try to return raw output if JSON parsing fails
		return rlm.QueryResponse{
			Response: stdout.String(),
		}, fmt.Errorf("failed to parse claude-code JSON output: %w", err)
	}

	// Store session ID for resumption
	if resp.SessionID != "" {
		a.sessionID = resp.SessionID
	}

	// Track usage
	a.recordUsage(resp)

	// Handle errors
	if resp.IsError || resp.Subtype == "error_during_execution" {
		errMsg := resp.Result
		if len(resp.Errors) > 0 {
			errMsg = fmt.Sprintf("%s: %v", resp.Result, resp.Errors)
		}
		return rlm.QueryResponse{
			Response:         errMsg,
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
		}, fmt.Errorf("claude-code error: %s", errMsg)
	}

	return rlm.QueryResponse{
		Response:         resp.Result,
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
	}, nil
}

// QueryWithSchema executes a query expecting structured JSON output.
func (a *ClaudeCodeAdapter) QueryWithSchema(ctx context.Context, prompt string, schema string) (json.RawMessage, rlm.QueryResponse, error) {
	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--json-schema", schema,
	}

	if len(a.allowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(a.allowedTools, ","))
	}

	if a.sessionID != "" {
		args = append(args, "--resume", a.sessionID)
	}

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.cliPath, args...)
	if a.workDir != "" {
		cmd.Dir = a.workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, rlm.QueryResponse{}, fmt.Errorf("claude-code timed out after %v", a.timeout)
		}
		return nil, rlm.QueryResponse{}, fmt.Errorf("claude-code failed: %w, stderr: %s", err, stderr.String())
	}

	var resp ClaudeCodeResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, rlm.QueryResponse{}, fmt.Errorf("failed to parse claude-code output: %w", err)
	}

	a.sessionID = resp.SessionID
	a.recordUsage(resp)

	queryResp := rlm.QueryResponse{
		Response:         resp.Result,
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
	}

	return resp.StructuredOutput, queryResp, nil
}

// QueryBatched implements rlm.SubLLMClient.
// Note: Claude Code queries are executed sequentially to maintain session context.
func (a *ClaudeCodeAdapter) QueryBatched(ctx context.Context, prompts []string) ([]rlm.QueryResponse, error) {
	results := make([]rlm.QueryResponse, len(prompts))
	for i, prompt := range prompts {
		resp, err := a.Query(ctx, prompt)
		if err != nil {
			results[i] = rlm.QueryResponse{Response: fmt.Sprintf("Error: %v", err)}
			// Continue with remaining prompts
			continue
		}
		results[i] = resp
	}
	return results, nil
}

func (a *ClaudeCodeAdapter) recordUsage(resp ClaudeCodeResponse) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.totalPrompt += resp.Usage.InputTokens
	a.totalCompl += resp.Usage.OutputTokens
	a.totalCost += resp.TotalCostUSD
	a.totalQueries++
}

// Name implements SubAgent.
func (a *ClaudeCodeAdapter) Name() string {
	return "claude-code"
}

// Capabilities implements SubAgent.
// Claude Code has full capabilities including file operations and shell execution.
func (a *ClaudeCodeAdapter) Capabilities() []Capability {
	return []Capability{
		CapabilityCodeAnalysis,
		CapabilityCodeGeneration,
		CapabilityFileRead,
		CapabilityFileWrite,
		CapabilityWebSearch,
		CapabilityShellExecution,
	}
}

// TokenPricing implements SubAgent.
// Returns Sonnet pricing as Claude Code uses Sonnet by default.
func (a *ClaudeCodeAdapter) TokenPricing() (input float64, output float64) {
	return 0.003, 0.015 // Sonnet pricing
}

// Stats implements SubAgent.
func (a *ClaudeCodeAdapter) Stats() AgentStats {
	a.mu.Lock()
	defer a.mu.Unlock()

	return AgentStats{
		TotalPromptTokens:     a.totalPrompt,
		TotalCompletionTokens: a.totalCompl,
		TotalQueries:          a.totalQueries,
		TotalCostUSD:          a.totalCost,
		CallsByTier:           map[ModelTier]int{TierBest: a.totalQueries},
	}
}

// ResetSession clears the session ID to start fresh.
func (a *ClaudeCodeAdapter) ResetSession() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionID = ""
}

// GetSessionID returns the current session ID.
func (a *ClaudeCodeAdapter) GetSessionID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessionID
}

// SetSessionID sets the session ID for resumption.
func (a *ClaudeCodeAdapter) SetSessionID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionID = id
}

// Reset clears all usage tracking and session state.
func (a *ClaudeCodeAdapter) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.sessionID = ""
	a.totalPrompt = 0
	a.totalCompl = 0
	a.totalCost = 0
	a.totalQueries = 0
}

// IsAvailable checks if the Claude Code CLI is available.
func (a *ClaudeCodeAdapter) IsAvailable() bool {
	cmd := exec.Command(a.cliPath, "--version")
	return cmd.Run() == nil
}
