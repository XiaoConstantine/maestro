package rlm

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dspyrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

func TestQueryIntent_String(t *testing.T) {
	tests := []struct {
		intent   QueryIntent
		expected string
	}{
		{IntentAnalysis, "analysis"},
		{IntentCodeGeneration, "code_generation"},
		{IntentSimple, "simple"},
		{IntentComplex, "complex"},
		{IntentSearch, "search"},
		{IntentUnknown, "unknown"},
	}

	for _, tt := range tests {
		if got := tt.intent.String(); got != tt.expected {
			t.Errorf("%v.String() = %s, want %s", tt.intent, got, tt.expected)
		}
	}
}

func TestQueryRouter_ClassifyIntent(t *testing.T) {
	registry := NewSubAgentRegistry()
	router := NewQueryRouter(registry, DefaultRouterConfig())

	tests := []struct {
		query    string
		expected QueryIntent
	}{
		// Simple queries
		{"what is a pointer?", IntentSimple},
		{"how to use fmt.Println", IntentSimple},

		// Code generation
		{"write a function to sort a list", IntentCodeGeneration},
		{"generate a HTTP handler for /users", IntentCodeGeneration},
		{"create a struct for User data", IntentCodeGeneration},
		{"implement the interface", IntentCodeGeneration},
		{"fix the bug in this code", IntentCodeGeneration},
		{"refactor this function", IntentCodeGeneration},

		// Search
		{"find all error handling", IntentSearch},
		{"search for the entry point", IntentSearch},
		{"which file contains the Config", IntentSearch},
		{"locate the database connection", IntentSearch},

		// Complex
		{"explain the entire architecture of this system", IntentComplex},
		{"analyze all dependencies and their impacts", IntentComplex},
		{"design a comprehensive solution for this problem", IntentComplex},

		// Analysis (default for longer explanatory queries)
		{"explain how the HTTP router works", IntentAnalysis},
		{"why does this return an error", IntentAnalysis},
		{"how does the cache invalidation work", IntentAnalysis},
		{"describe the authentication flow", IntentAnalysis},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := router.ClassifyIntent(tt.query)
			if got != tt.expected {
				t.Errorf("ClassifyIntent(%q) = %v, want %v", tt.query, got, tt.expected)
			}
		})
	}
}

func TestQueryRouter_CustomRules(t *testing.T) {
	registry := NewSubAgentRegistry()
	config := DefaultRouterConfig()
	config.CustomRules = []RoutingRule{
		{
			Name:        "security-queries",
			Pattern:     regexp.MustCompile(`(?i)security|vulnerability|exploit`),
			TargetAgent: "claude-opus",
			Priority:    1,
		},
		{
			Name:        "test-queries",
			Keywords:    []string{"test", "unit test", "integration test"},
			TargetAgent: "gpt-4o-mini",
			Priority:    2,
		},
	}

	router := NewQueryRouter(registry, config)

	// Custom rule should take precedence
	intent := router.ClassifyIntent("check for security vulnerabilities")
	if intent != IntentComplex { // "opus" in target triggers complex
		t.Errorf("expected complex (security), got %v", intent)
	}

	intent = router.ClassifyIntent("write unit test for handler")
	if intent != IntentSimple { // "mini" in target triggers simple
		t.Errorf("expected simple (test), got %v", intent)
	}
}

func TestQueryRouter_Route(t *testing.T) {
	registry := NewSubAgentRegistry()

	// Register mock agents
	mockClaude := &mockSubAgent{
		name:        "anthropic-claude-sonnet",
		inputPrice:  0.003,
		outputPrice: 0.015,
	}
	mockGPT := &mockSubAgent{
		name:        "openai-gpt-4o",
		inputPrice:  0.005,
		outputPrice: 0.015,
	}

	registry.Register(mockClaude)
	registry.Register(mockGPT)

	config := DefaultRouterConfig()
	config.EnableMetrics = true
	router := NewQueryRouter(registry, config)

	ctx := context.Background()
	resp, err := router.Route(ctx, "explain this code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Response != "mock response" {
		t.Errorf("unexpected response: %s", resp.Response)
	}

	// Check metrics
	metrics := router.Metrics()
	if metrics.TotalRouted != 1 {
		t.Errorf("expected 1 routed, got %d", metrics.TotalRouted)
	}
}

func TestQueryRouter_RouteWithTier(t *testing.T) {
	registry := NewSubAgentRegistry()

	mockFast := &mockSubAgent{
		name:        "anthropic-claude-haiku",
		inputPrice:  0.00025,
		outputPrice: 0.00125,
	}
	mockSmart := &mockSubAgent{
		name:        "anthropic-claude-sonnet",
		inputPrice:  0.003,
		outputPrice: 0.015,
	}

	registry.Register(mockFast)
	registry.Register(mockSmart)

	config := DefaultRouterConfig()
	config.FastAgents = []string{"anthropic-claude-haiku"}
	config.AnalysisAgents = []string{"anthropic-claude-sonnet"}
	router := NewQueryRouter(registry, config)

	ctx := context.Background()

	// Route with fast tier
	_, err := router.RouteWithTier(ctx, "simple question", TierFast)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Route with smart tier
	_, err = router.RouteWithTier(ctx, "complex question", TierSmart)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQueryRouter_Fallback(t *testing.T) {
	registry := NewSubAgentRegistry()

	mockDefault := &mockSubAgent{
		name:        "default-agent",
		inputPrice:  0.003,
		outputPrice: 0.015,
	}
	registry.Register(mockDefault)

	config := DefaultRouterConfig()
	config.DefaultAgent = "default-agent"
	config.AnalysisAgents = []string{"nonexistent-agent"} // Won't find this
	config.FallbackOnError = true
	router := NewQueryRouter(registry, config)

	ctx := context.Background()
	resp, err := router.Route(ctx, "explain this")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Response != "mock response" {
		t.Errorf("unexpected response: %s", resp.Response)
	}
}

func TestQueryRouter_NoFallback(t *testing.T) {
	registry := NewSubAgentRegistry()

	config := DefaultRouterConfig()
	config.DefaultAgent = "nonexistent"
	config.AnalysisAgents = []string{"also-nonexistent"}
	config.FallbackOnError = false
	router := NewQueryRouter(registry, config)

	ctx := context.Background()
	_, err := router.Route(ctx, "explain this")
	if err == nil {
		t.Error("expected error when no agent available")
	}
}

func TestQueryRouter_Metrics(t *testing.T) {
	registry := NewSubAgentRegistry()

	mock := &mockSubAgent{
		name:        "test-agent",
		inputPrice:  0.003,
		outputPrice: 0.015,
	}
	registry.Register(mock)

	config := DefaultRouterConfig()
	config.DefaultAgent = "test-agent"
	config.EnableMetrics = true
	router := NewQueryRouter(registry, config)

	ctx := context.Background()

	// Make several queries
	for i := 0; i < 5; i++ {
		router.Route(ctx, "explain this")
	}
	for i := 0; i < 3; i++ {
		router.Route(ctx, "write a function")
	}

	metrics := router.Metrics()

	if metrics.TotalRouted != 8 {
		t.Errorf("expected 8 routed, got %d", metrics.TotalRouted)
	}
	if metrics.ByIntent[IntentAnalysis] != 5 {
		t.Errorf("expected 5 analysis, got %d", metrics.ByIntent[IntentAnalysis])
	}
	if metrics.ByIntent[IntentCodeGeneration] != 3 {
		t.Errorf("expected 3 code_generation, got %d", metrics.ByIntent[IntentCodeGeneration])
	}
	if metrics.SuccessRate != 1.0 {
		t.Errorf("expected 100%% success rate, got %f", metrics.SuccessRate)
	}
}

func TestQueryRouter_History(t *testing.T) {
	registry := NewSubAgentRegistry()

	mock := &mockSubAgent{
		name:        "test-agent",
		inputPrice:  0.003,
		outputPrice: 0.015,
	}
	registry.Register(mock)

	config := DefaultRouterConfig()
	config.DefaultAgent = "test-agent"
	config.EnableMetrics = true
	router := NewQueryRouter(registry, config)

	ctx := context.Background()

	router.Route(ctx, "query 1")
	router.Route(ctx, "query 2")
	router.Route(ctx, "query 3")

	history := router.History(2)

	if len(history) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(history))
	}

	// Should be most recent
	if history[1].Query != "query 3" {
		t.Errorf("unexpected query in history: %s", history[1].Query)
	}
}

func TestQueryRouter_ResetMetrics(t *testing.T) {
	registry := NewSubAgentRegistry()

	mock := &mockSubAgent{
		name:        "test-agent",
		inputPrice:  0.003,
		outputPrice: 0.015,
	}
	registry.Register(mock)

	config := DefaultRouterConfig()
	config.DefaultAgent = "test-agent"
	config.EnableMetrics = true
	router := NewQueryRouter(registry, config)

	ctx := context.Background()
	router.Route(ctx, "test query")

	metrics := router.Metrics()
	if metrics.TotalRouted != 1 {
		t.Errorf("expected 1 routed, got %d", metrics.TotalRouted)
	}

	router.ResetMetrics()

	metrics = router.Metrics()
	if metrics.TotalRouted != 0 {
		t.Errorf("expected 0 after reset, got %d", metrics.TotalRouted)
	}

	history := router.History(10)
	if len(history) != 0 {
		t.Errorf("expected 0 history after reset, got %d", len(history))
	}
}

func TestQueryRouter_AddRule(t *testing.T) {
	registry := NewSubAgentRegistry()

	mock := &mockSubAgent{
		name:        "special-agent",
		inputPrice:  0.003,
		outputPrice: 0.015,
	}
	registry.Register(mock)

	config := DefaultRouterConfig()
	router := NewQueryRouter(registry, config)

	// Add custom rule
	router.AddRule(RoutingRule{
		Name:        "special",
		Keywords:    []string{"special"},
		TargetAgent: "special-agent",
	})

	// Verify rule is applied
	intent := router.ClassifyIntent("handle special request")
	// Keywords trigger rule; agent name doesn't contain opus/mini/codex
	// so it defaults to analysis
	if intent != IntentAnalysis {
		t.Errorf("expected analysis for custom rule, got %v", intent)
	}
}

func TestQueryRouter_QueryBatched(t *testing.T) {
	registry := NewSubAgentRegistry()

	mock := &mockSubAgent{
		name:        "test-agent",
		inputPrice:  0.003,
		outputPrice: 0.015,
	}
	registry.Register(mock)

	config := DefaultRouterConfig()
	config.DefaultAgent = "test-agent"
	router := NewQueryRouter(registry, config)

	ctx := context.Background()
	prompts := []string{"query 1", "query 2", "query 3"}

	results, err := router.QueryBatched(ctx, prompts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	for i, result := range results {
		if result.Response != "mock response" {
			t.Errorf("result %d: unexpected response: %s", i, result.Response)
		}
	}
}

func TestQueryRouter_QueryBatched_ResilientAndParallel(t *testing.T) {
	registry := NewSubAgentRegistry()

	var active int32
	var maxActive int32
	mock := &mockSubAgent{
		name:        "test-agent",
		inputPrice:  0.003,
		outputPrice: 0.015,
		queryFunc: func(ctx context.Context, prompt string) (dspyrlm.QueryResponse, error) {
			current := atomic.AddInt32(&active, 1)
			for {
				seen := atomic.LoadInt32(&maxActive)
				if current <= seen {
					break
				}
				if atomic.CompareAndSwapInt32(&maxActive, seen, current) {
					break
				}
			}
			defer atomic.AddInt32(&active, -1)

			time.Sleep(40 * time.Millisecond)
			if prompt == "bad" {
				return dspyrlm.QueryResponse{}, fmt.Errorf("boom")
			}
			return dspyrlm.QueryResponse{Response: "ok:" + prompt}, nil
		},
	}
	registry.Register(mock)

	config := DefaultRouterConfig()
	config.DefaultAgent = "test-agent"
	config.BatchMaxConcurrent = 4
	router := NewQueryRouter(registry, config)

	results, err := router.QueryBatched(context.Background(), []string{"a", "bad", "c", "d"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	if results[0].Response != "ok:a" || results[2].Response != "ok:c" || results[3].Response != "ok:d" {
		t.Fatalf("unexpected success responses: %#v", results)
	}
	if !strings.HasPrefix(results[1].Response, "Error:") {
		t.Fatalf("expected error response at index 1, got %q", results[1].Response)
	}
	if atomic.LoadInt32(&maxActive) <= 1 {
		t.Fatalf("expected concurrent execution, maxActive=%d", atomic.LoadInt32(&maxActive))
	}
}

func TestRouterSubClient(t *testing.T) {
	registry := NewSubAgentRegistry()

	mock := &mockSubAgent{
		name:        "test-agent",
		inputPrice:  0.003,
		outputPrice: 0.015,
	}
	registry.Register(mock)

	config := DefaultRouterConfig()
	config.DefaultAgent = "test-agent"
	router := NewQueryRouter(registry, config)

	// Wrap as SubClient
	client := NewRouterSubClient(router)

	// Verify SubAgent interface
	if client.Name() != "query-router" {
		t.Errorf("unexpected name: %s", client.Name())
	}

	caps := client.Capabilities()
	if len(caps) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(caps))
	}

	inputPrice, outputPrice := client.TokenPricing()
	if inputPrice != 0.003 || outputPrice != 0.015 {
		t.Errorf("unexpected pricing: %f, %f", inputPrice, outputPrice)
	}

	// Test query
	ctx := context.Background()
	resp, err := client.Query(ctx, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Response != "mock response" {
		t.Errorf("unexpected response: %s", resp.Response)
	}

	// Test batched
	results, err := client.QueryBatched(ctx, []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	stats := client.Stats()
	if stats.TotalQueries != 3 { // 1 + 2 batched
		t.Errorf("expected 3 queries, got %d", stats.TotalQueries)
	}
}

func TestTruncateQuery(t *testing.T) {
	tests := []struct {
		query    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is a longer query", 10, "this is..."},
	}

	for _, tt := range tests {
		got := truncateQuery(tt.query, tt.maxLen)
		if got != tt.expected {
			t.Errorf("truncateQuery(%q, %d) = %q, want %q", tt.query, tt.maxLen, got, tt.expected)
		}
	}
}

func TestQueryRouter_IntentByLength(t *testing.T) {
	registry := NewSubAgentRegistry()
	router := NewQueryRouter(registry, DefaultRouterConfig())

	// Very long query without specific keywords should be complex
	longQuery := "Please provide a detailed explanation of how the system works including all components, their interactions, dependencies, error handling, and potential improvements that could be made to the architecture"
	intent := router.ClassifyIntent(longQuery)
	if intent != IntentComplex {
		t.Errorf("long query should be complex, got %v", intent)
	}

	// Medium length query should be analysis
	mediumQuery := "Can you tell me about the authentication flow in this application"
	intent = router.ClassifyIntent(mediumQuery)
	if intent != IntentAnalysis {
		t.Errorf("medium query should be analysis, got %v", intent)
	}
}

// Helper to verify SubLLMClient interface compliance
func TestRouterImplementsSubLLMClient(t *testing.T) {
	registry := NewSubAgentRegistry()
	router := NewQueryRouter(registry, DefaultRouterConfig())

	// Verify interface compliance
	var _ dspyrlm.SubLLMClient = router
}
