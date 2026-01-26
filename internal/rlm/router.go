// Package rlm provides RLM (Recursive Language Model) integration for Maestro.
package rlm

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

// QueryIntent represents the classified intent of a query.
type QueryIntent int

const (
	// IntentAnalysis for reasoning, analysis, and explanation tasks
	IntentAnalysis QueryIntent = iota
	// IntentCodeGeneration for code writing and modification tasks
	IntentCodeGeneration
	// IntentSimple for simple, direct questions
	IntentSimple
	// IntentComplex for multi-step, complex reasoning tasks
	IntentComplex
	// IntentSearch for code search and exploration tasks
	IntentSearch
	// IntentUnknown when intent cannot be determined
	IntentUnknown
)

func (i QueryIntent) String() string {
	switch i {
	case IntentAnalysis:
		return "analysis"
	case IntentCodeGeneration:
		return "code_generation"
	case IntentSimple:
		return "simple"
	case IntentComplex:
		return "complex"
	case IntentSearch:
		return "search"
	default:
		return "unknown"
	}
}

// RouterConfig configures the query router.
type RouterConfig struct {
	// DefaultAgent is used when no specific routing rule matches
	DefaultAgent string

	// AnalysisAgents are preferred for analysis queries (e.g., Claude)
	AnalysisAgents []string

	// CodeGenAgents are preferred for code generation (e.g., Codex, GPT)
	CodeGenAgents []string

	// FastAgents are used for simple queries (cost optimization)
	FastAgents []string

	// BestAgents are used for complex/critical queries
	BestAgents []string

	// EnableMetrics enables routing metrics collection
	EnableMetrics bool

	// FallbackOnError uses fallback agent if primary fails
	FallbackOnError bool

	// CustomRules allows custom routing rules
	CustomRules []RoutingRule
}

// RoutingRule defines a custom routing rule.
type RoutingRule struct {
	Name       string
	Pattern    *regexp.Regexp
	Keywords   []string
	TargetAgent string
	Priority   int
}

// DefaultRouterConfig returns a configuration with sensible defaults.
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		DefaultAgent:    "anthropic-claude-sonnet",
		AnalysisAgents:  []string{"anthropic-claude-sonnet", "anthropic-claude-opus"},
		CodeGenAgents:   []string{"openai-gpt-4o", "openai-codex", "anthropic-claude-sonnet"},
		FastAgents:      []string{"anthropic-claude-haiku", "openai-gpt-4o-mini"},
		BestAgents:      []string{"anthropic-claude-opus", "openai-o3"},
		EnableMetrics:   true,
		FallbackOnError: true,
	}
}

// QueryRouter routes queries to optimal SubAgents based on intent classification.
type QueryRouter struct {
	config   RouterConfig
	registry *SubAgentRegistry

	mu       sync.RWMutex
	metrics  RouterMetrics
	history  []RoutingDecision
}

// RouterMetrics tracks routing statistics.
type RouterMetrics struct {
	TotalRouted       int
	ByIntent          map[QueryIntent]int
	ByAgent           map[string]int
	FallbackCount     int
	AverageLatencyMS  float64
	SuccessRate       float64
	totalLatencyMS    float64
	successCount      int
}

// RoutingDecision records a routing decision for analysis.
type RoutingDecision struct {
	Query       string
	Intent      QueryIntent
	SelectedAgent string
	FallbackUsed bool
	LatencyMS   float64
	Success     bool
	Timestamp   time.Time
}

// NewQueryRouter creates a new query router.
func NewQueryRouter(registry *SubAgentRegistry, config RouterConfig) *QueryRouter {
	if config.DefaultAgent == "" {
		config = DefaultRouterConfig()
	}
	return &QueryRouter{
		config:   config,
		registry: registry,
		metrics: RouterMetrics{
			ByIntent: make(map[QueryIntent]int),
			ByAgent:  make(map[string]int),
		},
	}
}

// Route selects the optimal agent for a query and executes it.
func (r *QueryRouter) Route(ctx context.Context, query string) (rlm.QueryResponse, error) {
	start := time.Now()

	// Classify intent
	intent := r.ClassifyIntent(query)

	// Select agent
	agentName := r.selectAgent(intent)
	agent, err := r.registry.Get(agentName)
	if err != nil {
		// Try fallback
		if r.config.FallbackOnError {
			agent, err = r.registry.GetDefault()
			if err != nil {
				return rlm.QueryResponse{}, fmt.Errorf("no agent available: %w", err)
			}
			agentName = agent.Name()
		} else {
			return rlm.QueryResponse{}, fmt.Errorf("agent %s not found: %w", agentName, err)
		}
	}

	// Execute query
	resp, err := agent.Query(ctx, query)
	latency := float64(time.Since(start).Milliseconds())

	// Record metrics
	r.recordDecision(RoutingDecision{
		Query:         truncateQuery(query, 100),
		Intent:        intent,
		SelectedAgent: agentName,
		FallbackUsed:  agentName != r.selectAgent(intent),
		LatencyMS:     latency,
		Success:       err == nil,
		Timestamp:     start,
	})

	return resp, err
}

// RouteWithTier routes with an explicit tier preference.
func (r *QueryRouter) RouteWithTier(ctx context.Context, query string, tier ModelTier) (rlm.QueryResponse, error) {
	start := time.Now()

	var agents []string
	switch tier {
	case TierFast:
		agents = r.config.FastAgents
	case TierSmart:
		agents = r.config.AnalysisAgents
	case TierBest:
		agents = r.config.BestAgents
	default:
		agents = []string{r.config.DefaultAgent}
	}

	// Find first available agent
	var agent SubAgent
	var agentName string
	for _, name := range agents {
		a, err := r.registry.Get(name)
		if err == nil {
			agent = a
			agentName = name
			break
		}
	}

	if agent == nil {
		var err error
		agent, err = r.registry.GetDefault()
		if err != nil {
			return rlm.QueryResponse{}, fmt.Errorf("no agent available for tier %s", tier)
		}
		agentName = agent.Name()
	}

	resp, err := agent.Query(ctx, query)
	latency := float64(time.Since(start).Milliseconds())

	r.recordDecision(RoutingDecision{
		Query:         truncateQuery(query, 100),
		Intent:        IntentUnknown,
		SelectedAgent: agentName,
		LatencyMS:     latency,
		Success:       err == nil,
		Timestamp:     start,
	})

	return resp, err
}

// Query implements rlm.SubLLMClient for use as a sub-client.
func (r *QueryRouter) Query(ctx context.Context, prompt string) (rlm.QueryResponse, error) {
	return r.Route(ctx, prompt)
}

// QueryBatched implements rlm.SubLLMClient for batched queries.
func (r *QueryRouter) QueryBatched(ctx context.Context, prompts []string) ([]rlm.QueryResponse, error) {
	results := make([]rlm.QueryResponse, len(prompts))

	// Route each query independently
	for i, prompt := range prompts {
		resp, err := r.Route(ctx, prompt)
		if err != nil {
			results[i] = rlm.QueryResponse{Response: fmt.Sprintf("Error: %v", err)}
			continue
		}
		results[i] = resp
	}

	return results, nil
}

// ClassifyIntent determines the intent of a query.
func (r *QueryRouter) ClassifyIntent(query string) QueryIntent {
	query = strings.ToLower(query)

	// Check custom rules first
	for _, rule := range r.config.CustomRules {
		if rule.Pattern != nil && rule.Pattern.MatchString(query) {
			return classifyFromRule(rule)
		}
		for _, kw := range rule.Keywords {
			if strings.Contains(query, kw) {
				return classifyFromRule(rule)
			}
		}
	}

	// Simple queries (short, direct questions)
	if len(query) < 50 && isSimpleQuery(query) {
		return IntentSimple
	}

	// Code generation patterns
	codeGenPatterns := []string{
		"write", "generate", "create", "implement", "add",
		"function", "method", "class", "type", "struct",
		"fix the", "refactor", "modify", "change",
		"```", "code", "snippet",
	}
	for _, p := range codeGenPatterns {
		if strings.Contains(query, p) {
			return IntentCodeGeneration
		}
	}

	// Search patterns
	searchPatterns := []string{
		"find", "search", "where is", "locate",
		"which file", "what files", "look for",
	}
	for _, p := range searchPatterns {
		if strings.Contains(query, p) {
			return IntentSearch
		}
	}

	// Complex patterns
	complexPatterns := []string{
		"explain the entire", "analyze all", "comprehensive",
		"architecture", "design", "review the",
		"compare and contrast", "deep dive",
	}
	for _, p := range complexPatterns {
		if strings.Contains(query, p) {
			return IntentComplex
		}
	}

	// Analysis patterns (default for longer queries)
	analysisPatterns := []string{
		"explain", "why", "how does", "what is",
		"analyze", "understand", "describe",
		"reason", "logic", "behavior",
	}
	for _, p := range analysisPatterns {
		if strings.Contains(query, p) {
			return IntentAnalysis
		}
	}

	// Default based on length
	if len(query) > 200 {
		return IntentComplex
	}
	if len(query) > 50 {
		return IntentAnalysis
	}

	return IntentSimple
}

// selectAgent chooses the best agent for an intent.
func (r *QueryRouter) selectAgent(intent QueryIntent) string {
	var candidates []string

	switch intent {
	case IntentAnalysis:
		candidates = r.config.AnalysisAgents
	case IntentCodeGeneration:
		candidates = r.config.CodeGenAgents
	case IntentSimple:
		candidates = r.config.FastAgents
	case IntentComplex:
		candidates = r.config.BestAgents
	case IntentSearch:
		candidates = r.config.FastAgents // Search is usually quick
	default:
		candidates = []string{r.config.DefaultAgent}
	}

	// Find first available agent
	for _, name := range candidates {
		if r.registry.HasAgent(name) {
			return name
		}
	}

	// Fallback to default
	return r.config.DefaultAgent
}

func (r *QueryRouter) recordDecision(decision RoutingDecision) {
	if !r.config.EnableMetrics {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.metrics.TotalRouted++
	r.metrics.ByIntent[decision.Intent]++
	r.metrics.ByAgent[decision.SelectedAgent]++
	r.metrics.totalLatencyMS += decision.LatencyMS

	if decision.Success {
		r.metrics.successCount++
	}
	if decision.FallbackUsed {
		r.metrics.FallbackCount++
	}

	r.metrics.AverageLatencyMS = r.metrics.totalLatencyMS / float64(r.metrics.TotalRouted)
	r.metrics.SuccessRate = float64(r.metrics.successCount) / float64(r.metrics.TotalRouted)

	// Keep recent history (last 100 decisions)
	r.history = append(r.history, decision)
	if len(r.history) > 100 {
		r.history = r.history[1:]
	}
}

// Metrics returns current routing metrics.
func (r *QueryRouter) Metrics() RouterMetrics {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Deep copy maps
	metrics := r.metrics
	metrics.ByIntent = make(map[QueryIntent]int)
	metrics.ByAgent = make(map[string]int)
	for k, v := range r.metrics.ByIntent {
		metrics.ByIntent[k] = v
	}
	for k, v := range r.metrics.ByAgent {
		metrics.ByAgent[k] = v
	}

	return metrics
}

// History returns recent routing decisions.
func (r *QueryRouter) History(limit int) []RoutingDecision {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if limit <= 0 || limit > len(r.history) {
		limit = len(r.history)
	}

	result := make([]RoutingDecision, limit)
	copy(result, r.history[len(r.history)-limit:])
	return result
}

// ResetMetrics clears all metrics.
func (r *QueryRouter) ResetMetrics() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.metrics = RouterMetrics{
		ByIntent: make(map[QueryIntent]int),
		ByAgent:  make(map[string]int),
	}
	r.history = nil
}

// AddRule adds a custom routing rule.
func (r *QueryRouter) AddRule(rule RoutingRule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config.CustomRules = append(r.config.CustomRules, rule)
}

// Helper functions

func isSimpleQuery(query string) bool {
	simplePatterns := []string{
		"what is", "how to", "where",
		"yes or no", "true or false",
		"name of", "list",
	}
	for _, p := range simplePatterns {
		if strings.HasPrefix(query, p) {
			return true
		}
	}
	return false
}

func classifyFromRule(rule RoutingRule) QueryIntent {
	// Map rule target to intent (simplified mapping)
	target := strings.ToLower(rule.TargetAgent)
	if strings.Contains(target, "opus") || strings.Contains(target, "o3") {
		return IntentComplex
	}
	if strings.Contains(target, "haiku") || strings.Contains(target, "mini") {
		return IntentSimple
	}
	if strings.Contains(target, "codex") || strings.Contains(target, "gpt-4o") {
		return IntentCodeGeneration
	}
	return IntentAnalysis
}

func truncateQuery(query string, maxLen int) string {
	if len(query) <= maxLen {
		return query
	}
	return query[:maxLen-3] + "..."
}

// RouterSubClient adapts QueryRouter to implement SubAgent interface.
type RouterSubClient struct {
	router *QueryRouter
}

// NewRouterSubClient creates a SubAgent that routes queries intelligently.
func NewRouterSubClient(router *QueryRouter) *RouterSubClient {
	return &RouterSubClient{router: router}
}

// Query implements rlm.SubLLMClient.
func (c *RouterSubClient) Query(ctx context.Context, prompt string) (rlm.QueryResponse, error) {
	return c.router.Route(ctx, prompt)
}

// QueryBatched implements rlm.SubLLMClient.
func (c *RouterSubClient) QueryBatched(ctx context.Context, prompts []string) ([]rlm.QueryResponse, error) {
	return c.router.QueryBatched(ctx, prompts)
}

// Name implements SubAgent.
func (c *RouterSubClient) Name() string {
	return "query-router"
}

// Capabilities implements SubAgent.
func (c *RouterSubClient) Capabilities() []Capability {
	return []Capability{
		CapabilityCodeAnalysis,
		CapabilityCodeGeneration,
	}
}

// TokenPricing implements SubAgent.
// Returns average pricing across registered agents.
func (c *RouterSubClient) TokenPricing() (input float64, output float64) {
	// Use Sonnet pricing as representative average
	return 0.003, 0.015
}

// Stats implements SubAgent.
func (c *RouterSubClient) Stats() AgentStats {
	metrics := c.router.Metrics()
	return AgentStats{
		TotalQueries: metrics.TotalRouted,
	}
}
