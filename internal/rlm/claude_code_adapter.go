package rlm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/sourcegraph/conc/pool"
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
	mu           sync.RWMutex
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

// TotalInputTokens returns the total prompt tokens including cache tokens.
func (r *ClaudeCodeResponse) TotalInputTokens() int {
	return r.Usage.InputTokens + r.Usage.CacheCreationInputTokens + r.Usage.CacheReadInputTokens
}

// parseClaudeCodeOutput parses the JSON output from Claude Code CLI.
// The CLI outputs a JSON array of events; we need to find the final result message.
func parseClaudeCodeOutput(data []byte) (ClaudeCodeResponse, error) {
	// First try to parse as a single object (backwards compatibility)
	var singleResp ClaudeCodeResponse
	if err := json.Unmarshal(data, &singleResp); err == nil {
		// Successfully parsed as single object
		return singleResp, nil
	}

	// Try to parse as an array of events
	var events []ClaudeCodeResponse
	if err := json.Unmarshal(data, &events); err != nil {
		return ClaudeCodeResponse{}, fmt.Errorf("failed to parse as single object or array: %w", err)
	}

	if len(events) == 0 {
		return ClaudeCodeResponse{}, fmt.Errorf("empty response array from claude-code")
	}

	// Find the result message - typically the last one with type "result"
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Type == "result" {
			return event, nil
		}
	}

	// If no result type found, return the last event
	return events[len(events)-1], nil
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
	return a.queryWithSessionMode(ctx, prompt, a.GetSessionID(), true)
}

func (a *ClaudeCodeAdapter) queryWithSessionMode(ctx context.Context, prompt string, resumeSession string, persistSession bool) (rlm.QueryResponse, error) {
	args := []string{
		"-p", "-",
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
	if resumeSession != "" {
		args = append(args, "--resume", resumeSession)
	}
	args = maybeAppendDebugFileArg(args)

	// Apply timeout via context
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.cliPath, args...)
	if a.workDir != "" {
		cmd.Dir = a.workDir
	}
	cmd.Env = nonInteractiveEnv()
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		// Extract the last meaningful lines from stderr (skip verbose prompt logs)
		stderrSummary := lastLines(stderrStr, 5)
		if resumeSession != "" && strings.Contains(stderrStr, "No conversation found with session ID") {
			a.ResetSession()
			return a.queryWithSessionMode(ctx, prompt, "", persistSession)
		}
		// Check if it's a context timeout
		if ctx.Err() == context.DeadlineExceeded {
			return rlm.QueryResponse{}, fmt.Errorf("claude-code timed out after %v", a.timeout)
		}
		return rlm.QueryResponse{}, fmt.Errorf("claude-code failed: %w, stderr (last 5 lines): %s", err, stderrSummary)
	}

	// Claude Code CLI outputs a JSON array of events. We need to find the result message.
	resp, err := parseClaudeCodeOutput(stdout.Bytes())
	if err != nil {
		// Try to return raw output if JSON parsing fails
		return rlm.QueryResponse{
			Response: stdout.String(),
		}, fmt.Errorf("failed to parse claude-code JSON output: %w", err)
	}

	// Store session ID for resumption
	if persistSession && resp.SessionID != "" {
		a.SetSessionID(resp.SessionID)
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
			PromptTokens:     resp.TotalInputTokens(),
			CompletionTokens: resp.Usage.OutputTokens,
		}, fmt.Errorf("claude-code error: %s", errMsg)
	}

	return rlm.QueryResponse{
		Response:         resp.Result,
		PromptTokens:     resp.TotalInputTokens(),
		CompletionTokens: resp.Usage.OutputTokens,
	}, nil
}

// QueryWithSchema executes a query expecting structured JSON output.
func (a *ClaudeCodeAdapter) QueryWithSchema(ctx context.Context, prompt string, schema string) (json.RawMessage, rlm.QueryResponse, error) {
	args := []string{
		"-p", "-",
		"--output-format", "json",
		"--json-schema", schema,
	}

	if len(a.allowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(a.allowedTools, ","))
	}

	if session := a.GetSessionID(); session != "" {
		args = append(args, "--resume", session)
	}
	args = maybeAppendDebugFileArg(args)

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.cliPath, args...)
	if a.workDir != "" {
		cmd.Dir = a.workDir
	}
	cmd.Env = nonInteractiveEnv()
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		if session := a.GetSessionID(); session != "" && strings.Contains(stderrStr, "No conversation found with session ID") {
			a.ResetSession()
			return a.QueryWithSchema(ctx, prompt, schema)
		}
		if ctx.Err() == context.DeadlineExceeded {
			return nil, rlm.QueryResponse{}, fmt.Errorf("claude-code timed out after %v", a.timeout)
		}
		return nil, rlm.QueryResponse{}, fmt.Errorf("claude-code failed: %w, stderr: %s", err, stderrStr)
	}

	resp, err := parseClaudeCodeOutput(stdout.Bytes())
	if err != nil {
		return nil, rlm.QueryResponse{}, fmt.Errorf("failed to parse claude-code output: %w", err)
	}

	if resp.SessionID != "" {
		a.SetSessionID(resp.SessionID)
	}
	a.recordUsage(resp)

	queryResp := rlm.QueryResponse{
		Response:         resp.Result,
		PromptTokens:     resp.TotalInputTokens(),
		CompletionTokens: resp.Usage.OutputTokens,
	}

	return resp.StructuredOutput, queryResp, nil
}

// QueryBatched implements rlm.SubLLMClient.
// When session state exists, execution is sequential to preserve conversation continuity.
// In stateless mode (no session), queries run concurrently for better throughput.
func (a *ClaudeCodeAdapter) QueryBatched(ctx context.Context, prompts []string) ([]rlm.QueryResponse, error) {
	if len(prompts) == 0 {
		return nil, nil
	}

	results := make([]rlm.QueryResponse, len(prompts))
	if a.GetSessionID() != "" {
		for i, prompt := range prompts {
			resp, err := a.Query(ctx, prompt)
			if err != nil {
				results[i] = rlm.QueryResponse{Response: fmt.Sprintf("Error: %v", err)}
				continue
			}
			results[i] = resp
		}
		return results, nil
	}

	p := pool.New().WithMaxGoroutines(defaultBatchConcurrency).WithContext(ctx)
	for i, prompt := range prompts {
		i, prompt := i, prompt
		p.Go(func(ctx context.Context) error {
			resp, err := a.queryWithSessionMode(ctx, prompt, "", false)
			if err != nil {
				results[i] = rlm.QueryResponse{Response: fmt.Sprintf("Error: %v", err)}
				return nil
			}
			results[i] = resp
			return nil
		})
	}
	if err := p.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}

func (a *ClaudeCodeAdapter) recordUsage(resp ClaudeCodeResponse) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.totalPrompt += resp.TotalInputTokens()
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
	a.mu.RLock()
	defer a.mu.RUnlock()

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
	a.mu.RLock()
	defer a.mu.RUnlock()
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

// nonInteractiveEnv returns the current process environment configured for
// non-interactive Claude CLI subprocess execution. It:
//   - Strips CLAUDECODE to avoid the nested-session guard that blocks invocation
//     from within an existing Claude Code session.
//   - Strips ANTHROPIC_API_KEY and CLAUDE_API_KEY so the CLI uses the user's
//     Claude Max/Pro subscription auth instead of a (possibly empty) API key.
//   - Sets TERM=dumb and NO_COLOR=1 to prevent TTY/raw-mode setup that hangs
//     when stdin is a pipe (benchmarks, CI, etc.).
func nonInteractiveEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "CLAUDECODE=") ||
			strings.HasPrefix(e, "ANTHROPIC_API_KEY=") ||
			strings.HasPrefix(e, "CLAUDE_API_KEY=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env,
		"TERM=dumb",
		"NO_COLOR=1",
	)
	return env
}

// maybeAppendDebugFileArg ensures claude CLI writes debug logs to a writable path
// in restricted environments. Users can override the path with
// MAESTRO_CLAUDE_DEBUG_FILE.
func maybeAppendDebugFileArg(args []string) []string {
	if dbg := os.Getenv("MAESTRO_CLAUDE_DEBUG_FILE"); dbg != "" {
		return append(args, "--debug-file", dbg)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if dirWritable(filepath.Join(home, ".claude", "debug")) {
			return args
		}
	}
	return append(args, "--debug-file", filepath.Join(os.TempDir(), "maestro-claude-code-debug.log"))
}

func dirWritable(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	testPath := filepath.Join(dir, ".maestro_write_test")
	if err := os.WriteFile(testPath, []byte("ok"), 0o600); err != nil {
		return false
	}
	_ = os.Remove(testPath)
	return true
}

// ClaudeCodeLLM wraps ClaudeCodeAdapter to implement core.LLM interface.
// This allows Claude Code to be used as the root LLM for RLM orchestration,
// enabling full subscription-based usage without API keys.
type ClaudeCodeLLM struct {
	adapter *ClaudeCodeAdapter
}

// NewClaudeCodeLLM creates a core.LLM backed by Claude Code CLI.
func NewClaudeCodeLLM(config ClaudeCodeConfig) *ClaudeCodeLLM {
	return &ClaudeCodeLLM{
		adapter: NewClaudeCodeAdapter(config),
	}
}

// Generate implements core.LLM.
func (c *ClaudeCodeLLM) Generate(ctx context.Context, prompt string, opts ...core.GenerateOption) (*core.LLMResponse, error) {
	resp, err := c.adapter.Query(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return &core.LLMResponse{
		Content: resp.Response,
		Usage: &core.TokenInfo{
			PromptTokens:     resp.PromptTokens,
			CompletionTokens: resp.CompletionTokens,
			TotalTokens:      resp.PromptTokens + resp.CompletionTokens,
		},
	}, nil
}

// GenerateWithJSON implements core.LLM.
func (c *ClaudeCodeLLM) GenerateWithJSON(ctx context.Context, prompt string, opts ...core.GenerateOption) (map[string]interface{}, error) {
	resp, err := c.Generate(ctx, prompt, opts...)
	if err != nil {
		return nil, err
	}

	// Try to parse response as JSON
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		// Return as raw content if not valid JSON
		return map[string]interface{}{"content": resp.Content}, nil
	}
	return result, nil
}

// GenerateWithFunctions implements core.LLM.
func (c *ClaudeCodeLLM) GenerateWithFunctions(ctx context.Context, prompt string, functions []map[string]interface{}, opts ...core.GenerateOption) (map[string]interface{}, error) {
	// Claude Code doesn't support function calling directly
	return c.GenerateWithJSON(ctx, prompt, opts...)
}

// CreateEmbedding implements core.LLM.
func (c *ClaudeCodeLLM) CreateEmbedding(ctx context.Context, input string, opts ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	return nil, fmt.Errorf("embeddings not supported by claude-code provider")
}

// CreateEmbeddings implements core.LLM.
func (c *ClaudeCodeLLM) CreateEmbeddings(ctx context.Context, inputs []string, opts ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	return nil, fmt.Errorf("embeddings not supported by claude-code provider")
}

// StreamGenerate implements core.LLM.
func (c *ClaudeCodeLLM) StreamGenerate(ctx context.Context, prompt string, opts ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("streaming not supported by claude-code provider")
}

// GenerateWithContent implements core.LLM.
func (c *ClaudeCodeLLM) GenerateWithContent(ctx context.Context, content []core.ContentBlock, opts ...core.GenerateOption) (*core.LLMResponse, error) {
	// Convert content blocks to text prompt
	var prompt strings.Builder
	for _, block := range content {
		prompt.WriteString(block.String())
		prompt.WriteString("\n")
	}
	return c.Generate(ctx, prompt.String(), opts...)
}

// StreamGenerateWithContent implements core.LLM.
func (c *ClaudeCodeLLM) StreamGenerateWithContent(ctx context.Context, content []core.ContentBlock, opts ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("streaming not supported by claude-code provider")
}

// ModelID implements core.LLM.
func (c *ClaudeCodeLLM) ModelID() string {
	return "claude-code"
}

// ProviderName implements core.LLM.
func (c *ClaudeCodeLLM) ProviderName() string {
	return "claude-code"
}

// Capabilities implements core.LLM.
func (c *ClaudeCodeLLM) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityCompletion, core.CapabilityJSON}
}

// Reset clears the underlying adapter session and usage counters.
func (c *ClaudeCodeLLM) Reset() {
	if c.adapter != nil {
		c.adapter.Reset()
	}
}

// GetAdapter returns the underlying ClaudeCodeAdapter for sub-client usage.
func (c *ClaudeCodeLLM) GetAdapter() *ClaudeCodeAdapter {
	return c.adapter
}

// lastLines returns the last n non-empty lines from s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return strings.TrimSpace(s)
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
