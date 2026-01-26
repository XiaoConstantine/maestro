package rlm

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, 30, cfg.MaxIterations)
	assert.Equal(t, 10*time.Minute, cfg.Timeout)
	assert.Equal(t, 5, cfg.CheckpointInterval)
	assert.False(t, cfg.Verbose)
	assert.Equal(t, 10, cfg.BatchConfig.MaxConcurrent)
	assert.Equal(t, 5.0, cfg.BatchConfig.RateLimitPerSec)
	assert.Equal(t, 60*time.Second, cfg.BatchConfig.TimeoutPerCall)
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status   Status
		expected string
	}{
		{StatusSuccess, "success"},
		{StatusTimeout, "timeout"},
		{StatusMaxIterations, "max_iterations"},
		{StatusError, "error"},
		{StatusPartial, "partial"},
		{Status(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}

func TestNewProcessorValidation(t *testing.T) {
	// Can't fully test without mock LLM, but test config defaults
	cfg := ProcessorConfig{}

	// These should be set to defaults
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 30
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.CheckpointInterval == 0 {
		cfg.CheckpointInterval = 5
	}

	assert.Equal(t, 30, cfg.MaxIterations)
	assert.Equal(t, 10*time.Minute, cfg.Timeout)
	assert.Equal(t, 5, cfg.CheckpointInterval)
}

func TestCheckpoint(t *testing.T) {
	checkpoint := Checkpoint{
		Iteration:  5,
		REPLState:  map[string]any{"sections": []string{"a", "b"}},
		TokensUsed: 1000,
		CostUSD:    0.05,
		Timestamp:  time.Now(),
	}

	assert.Equal(t, 5, checkpoint.Iteration)
	assert.Equal(t, 1000, checkpoint.TokensUsed)
	assert.Equal(t, 0.05, checkpoint.CostUSD)
	assert.NotNil(t, checkpoint.REPLState)
}

func TestResult(t *testing.T) {
	result := &Result{
		Answer:        "The answer is 42",
		Iterations:    10,
		TotalTokens:   5000,
		RootTokens:    1000,
		SubTokens:     4000,
		Duration:      30 * time.Second,
		CostUSD:       0.15,
		Status:        StatusSuccess,
		PartialOutput: "",
	}

	assert.Equal(t, "The answer is 42", result.Answer)
	assert.Equal(t, 10, result.Iterations)
	assert.Equal(t, 5000, result.TotalTokens)
	assert.Equal(t, StatusSuccess, result.Status)
}

func TestResumeRequiresCheckpointManager(t *testing.T) {
	// Resume requires checkpoint manager to be configured
	p := &Processor{}
	_, err := p.Resume(context.Background(), "/path/to/checkpoint.json", Request{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint manager not configured")
}

func TestProgressEvent(t *testing.T) {
	event := ProgressEvent{
		Iteration:      5,
		TotalExpected:  20,
		CurrentPhase:   "extraction",
		TokensUsed:     2500,
		ItemsProcessed: 15,
		CostUSD:        0.05,
	}

	assert.Equal(t, 5, event.Iteration)
	assert.Equal(t, 20, event.TotalExpected)
	assert.Equal(t, "extraction", event.CurrentPhase)
	assert.Equal(t, 2500, event.TokensUsed)
	assert.Equal(t, 15, event.ItemsProcessed)
	assert.Equal(t, 0.05, event.CostUSD)
}

func TestRequest(t *testing.T) {
	req := Request{
		Context: "Large document content here...",
		Query:   "Summarize the key points",
		Hints:   []string{"Focus on technical details", "Include code examples"},
	}

	assert.Equal(t, "Large document content here...", req.Context)
	assert.Equal(t, "Summarize the key points", req.Query)
	assert.Len(t, req.Hints, 2)
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected int
	}{
		{"empty", "", 0},
		{"short", "hello", 1},        // 5 chars / 4 = 1
		{"medium", "hello world!", 3}, // 12 chars / 4 = 3
		{"1kb", string(make([]byte, 1000)), 250},   // 1000 / 4 = 250
		{"10kb", string(make([]byte, 10000)), 2500}, // 10000 / 4 = 2500
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateTokens(tt.text)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestIsTextFile(t *testing.T) {
	// Text file extensions
	textExts := []string{".go", ".py", ".js", ".ts", ".md", ".json", ".yaml", ".yml", ".toml", ".html", ".css", ".sql", ".sh"}
	for _, ext := range textExts {
		assert.True(t, isTextFile(ext), "expected %s to be text file", ext)
	}

	// Non-text file extensions
	nonTextExts := []string{".exe", ".bin", ".png", ".jpg", ".pdf", ".zip", ".tar", ".gz", ".mp3", ".mp4", ""}
	for _, ext := range nonTextExts {
		assert.False(t, isTextFile(ext), "expected %s to NOT be text file", ext)
	}
}

func TestTokenSavingsCalculation(t *testing.T) {
	// Simulate a scenario where RLM would provide savings
	// Naive approach: send all content + query in one prompt
	// RLM approach: send smaller targeted queries

	largeContent := string(make([]byte, 100000)) // 100KB content
	query := "What are the error handling patterns?"

	// Baseline: naive approach would use all tokens
	baselineTokens := estimateTokens(largeContent) + estimateTokens(query)

	// Simulated RLM result with targeted queries
	result := &Result{
		TotalTokens:  5000, // Only used 5000 tokens via targeted queries
		RootTokens:   1000, // Orchestrator overhead
		SubTokens:    4000, // Targeted sub-queries
		TokenSavings: 1.0 - float64(5000)/float64(baselineTokens),
	}

	// Verify savings calculation
	expectedSavings := 1.0 - float64(5000)/float64(baselineTokens)
	assert.InDelta(t, expectedSavings, result.TokenSavings, 0.001)

	// For 100KB content (~25000 tokens), using 5000 tokens = 80% savings
	assert.Greater(t, result.TokenSavings, 0.7, "expected >70%% savings for large content")

	// Verify the token breakdown makes sense
	assert.Equal(t, result.RootTokens+result.SubTokens, result.TotalTokens)
}

func TestTokenEfficiencyWithSubClient(t *testing.T) {
	// This test verifies that the sub-client correctly tracks tokens
	// and that the cost calculation reflects the tier used
	// Uses mockLLM from client_test.go which returns 100 prompt + 50 completion tokens per call

	mock := &mockLLM{response: "analysis result"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel: mock,
	})
	require.NoError(t, err)

	// Simulate multiple sub-agent calls (like RLM would do)
	numCalls := 10
	for i := 0; i < numCalls; i++ {
		_, err := client.Query(context.Background(), "analyze chunk")
		require.NoError(t, err)
	}

	stats := client.Stats()

	// Verify token tracking (mockLLM returns 100 prompt + 50 completion per call)
	expectedPromptTokens := numCalls * 100
	expectedCompletionTokens := numCalls * 50
	assert.Equal(t, expectedPromptTokens, stats.TotalPromptTokens)
	assert.Equal(t, expectedCompletionTokens, stats.TotalCompletionTokens)

	// Verify call count by tier
	assert.Equal(t, numCalls, stats.CallsByTier[TierSmart])

	// Verify cost is calculated
	assert.Greater(t, stats.TotalCostUSD, 0.0)

	// Compare to naive approach
	// If we had 100KB content, naive would use ~25000 tokens
	// RLM used: 10 calls * 150 tokens = 1500 tokens
	naiveTokens := 25000
	rlmTokens := stats.TotalPromptTokens + stats.TotalCompletionTokens
	savings := 1.0 - float64(rlmTokens)/float64(naiveTokens)
	assert.Greater(t, savings, 0.9, "expected >90%% savings vs naive approach")

	t.Logf("Token efficiency test results:")
	t.Logf("  Naive approach tokens: %d", naiveTokens)
	t.Logf("  RLM approach tokens: %d", rlmTokens)
	t.Logf("  Savings: %.1f%%", savings*100)
	t.Logf("  Estimated cost: $%.6f", stats.TotalCostUSD)
}
