package rlm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLLM implements core.LLM for testing.
type mockLLM struct {
	response         string
	callCount        int
	promptTokens     int
	completionTokens int
	errFn            func(prompt string) error
	provider         string
	model            string
}

// newMockLLMWithUsage creates a mockLLM with custom token usage values.
func newMockLLMWithUsage(response string, promptTokens, completionTokens int) *mockLLM {
	return &mockLLM{
		response:         response,
		promptTokens:     promptTokens,
		completionTokens: completionTokens,
	}
}

func (m *mockLLM) Generate(ctx context.Context, prompt string, opts ...core.GenerateOption) (*core.LLMResponse, error) {
	if m.errFn != nil {
		if err := m.errFn(prompt); err != nil {
			return nil, err
		}
	}
	m.callCount++
	promptToks := m.promptTokens
	if promptToks == 0 {
		promptToks = 100
	}
	compToks := m.completionTokens
	if compToks == 0 {
		compToks = 50
	}
	return &core.LLMResponse{
		Content: m.response,
		Usage: &core.TokenInfo{
			PromptTokens:     promptToks,
			CompletionTokens: compToks,
			TotalTokens:      promptToks + compToks,
		},
	}, nil
}

func (m *mockLLM) GenerateWithJSON(ctx context.Context, prompt string, opts ...core.GenerateOption) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLLM) GenerateWithFunctions(ctx context.Context, prompt string, functions []map[string]interface{}, opts ...core.GenerateOption) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLLM) GenerateWithContent(ctx context.Context, content []core.ContentBlock, opts ...core.GenerateOption) (*core.LLMResponse, error) {
	return m.Generate(ctx, "", opts...)
}

func (m *mockLLM) StreamGenerate(ctx context.Context, prompt string, opts ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLLM) StreamGenerateWithContent(ctx context.Context, content []core.ContentBlock, opts ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLLM) CreateEmbedding(ctx context.Context, input string, opts ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLLM) CreateEmbeddings(ctx context.Context, inputs []string, opts ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLLM) ProviderName() string {
	if m.provider != "" {
		return m.provider
	}
	return "mock"
}

func (m *mockLLM) ModelID() string {
	if m.model != "" {
		return m.model
	}
	return "mock-model"
}
func (m *mockLLM) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityCompletion}
}

func TestModelTierString(t *testing.T) {
	tests := []struct {
		tier     ModelTier
		expected string
	}{
		{TierFast, "fast"},
		{TierSmart, "smart"},
		{TierBest, "best"},
		{ModelTier(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.tier.String())
		})
	}
}

func TestNewTieredSubClient(t *testing.T) {
	smartModel := &mockLLM{response: "smart response"}

	// Test with only required SmartModel
	client, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel: smartModel,
	})
	require.NoError(t, err)
	assert.NotNil(t, client)

	// Test missing SmartModel
	_, err = NewTieredSubClient(TieredSubClientConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SmartModel is required")
}

func TestTieredSubClientDefaults(t *testing.T) {
	smartModel := &mockLLM{response: "smart"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel: smartModel,
	})
	require.NoError(t, err)

	// FastModel should default to SmartModel
	assert.Same(t, smartModel, client.models[TierFast])
	// BestModel should default to SmartModel
	assert.Same(t, smartModel, client.models[TierBest])
	// Default concurrency
	assert.Equal(t, 10, client.maxConcurrent)
	// Default timeout
	assert.Equal(t, 60*time.Second, client.timeout)
}

func TestTieredSubClientWithAllModels(t *testing.T) {
	fastModel := &mockLLM{response: "fast"}
	smartModel := &mockLLM{response: "smart"}
	bestModel := &mockLLM{response: "best"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		FastModel:     fastModel,
		SmartModel:    smartModel,
		BestModel:     bestModel,
		DefaultTier:   TierSmart,
		MaxConcurrent: 5,
		Timeout:       30 * time.Second,
	})
	require.NoError(t, err)

	assert.Same(t, fastModel, client.models[TierFast])
	assert.Same(t, smartModel, client.models[TierSmart])
	assert.Same(t, bestModel, client.models[TierBest])
	assert.Equal(t, TierSmart, client.defaultTier)
	assert.Equal(t, 5, client.maxConcurrent)
	assert.Equal(t, 30*time.Second, client.timeout)
}

func TestTieredSubClientQuery(t *testing.T) {
	smartModel := &mockLLM{response: "smart response"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel:  smartModel,
		DefaultTier: TierSmart,
	})
	require.NoError(t, err)

	resp, err := client.Query(context.Background(), "test prompt")
	require.NoError(t, err)

	assert.Equal(t, "smart response", resp.Response)
	assert.Equal(t, 100, resp.PromptTokens)
	assert.Equal(t, 50, resp.CompletionTokens)
}

func TestTieredSubClientQueryWithTier(t *testing.T) {
	fastModel := &mockLLM{response: "fast response"}
	smartModel := &mockLLM{response: "smart response"}
	bestModel := &mockLLM{response: "best response"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		FastModel:  fastModel,
		SmartModel: smartModel,
		BestModel:  bestModel,
	})
	require.NoError(t, err)

	// Query with TierFast
	resp, err := client.QueryWithTier(context.Background(), "test", TierFast)
	require.NoError(t, err)
	assert.Equal(t, "fast response", resp.Response)

	// Query with TierSmart
	resp, err = client.QueryWithTier(context.Background(), "test", TierSmart)
	require.NoError(t, err)
	assert.Equal(t, "smart response", resp.Response)

	// Query with TierBest
	resp, err = client.QueryWithTier(context.Background(), "test", TierBest)
	require.NoError(t, err)
	assert.Equal(t, "best response", resp.Response)
}

func TestTieredSubClientQueryBatched(t *testing.T) {
	smartModel := &mockLLM{response: "batched response"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel:    smartModel,
		MaxConcurrent: 3,
	})
	require.NoError(t, err)

	prompts := []string{"prompt1", "prompt2", "prompt3"}
	results, err := client.QueryBatched(context.Background(), prompts)
	require.NoError(t, err)

	assert.Len(t, results, 3)
	for _, r := range results {
		assert.Equal(t, "batched response", r.Response)
	}
}

func TestTieredSubClientQueryBatchedEmpty(t *testing.T) {
	smartModel := &mockLLM{response: "response"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel: smartModel,
	})
	require.NoError(t, err)

	results, err := client.QueryBatched(context.Background(), []string{})
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestTieredSubClientQueryBatched_ResilientOnErrors(t *testing.T) {
	smartModel := &mockLLM{
		response: "ok",
		errFn: func(prompt string) error {
			if prompt == "bad" {
				return errors.New("boom")
			}
			return nil
		},
	}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel:    smartModel,
		MaxConcurrent: 3,
	})
	require.NoError(t, err)

	results, err := client.QueryBatched(context.Background(), []string{"a", "bad", "c"})
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, "ok", results[0].Response)
	assert.Contains(t, results[1].Response, "Error:")
	assert.Equal(t, "ok", results[2].Response)
}

func TestTieredSubClientTokenTracking(t *testing.T) {
	smartModel := &mockLLM{response: "response"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel: smartModel,
	})
	require.NoError(t, err)

	// Initial state
	assert.Equal(t, 0, client.TotalTokens())
	assert.Equal(t, 0.0, client.TotalCostUSD())

	// Make a query
	_, err = client.Query(context.Background(), "test")
	require.NoError(t, err)

	// Should have tracked tokens
	assert.Equal(t, 150, client.TotalTokens()) // 100 prompt + 50 completion

	// Make another query
	_, err = client.Query(context.Background(), "test2")
	require.NoError(t, err)

	assert.Equal(t, 300, client.TotalTokens())
}

func TestTieredSubClientStats(t *testing.T) {
	fastModel := &mockLLM{response: "fast"}
	smartModel := &mockLLM{response: "smart"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		FastModel:  fastModel,
		SmartModel: smartModel,
	})
	require.NoError(t, err)

	// Make queries with different tiers
	_, _ = client.QueryWithTier(context.Background(), "test", TierFast)
	_, _ = client.QueryWithTier(context.Background(), "test", TierFast)
	_, _ = client.QueryWithTier(context.Background(), "test", TierSmart)

	stats := client.Stats()
	assert.Equal(t, 300, stats.TotalPromptTokens)     // 3 * 100
	assert.Equal(t, 150, stats.TotalCompletionTokens) // 3 * 50
	assert.Equal(t, 2, stats.CallsByTier[TierFast])
	assert.Equal(t, 1, stats.CallsByTier[TierSmart])
	assert.Greater(t, stats.TotalCostUSD, 0.0)
}

func TestTieredSubClientReset(t *testing.T) {
	smartModel := &mockLLM{response: "response"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel: smartModel,
	})
	require.NoError(t, err)

	// Make some queries
	_, _ = client.Query(context.Background(), "test1")
	_, _ = client.Query(context.Background(), "test2")

	assert.Greater(t, client.TotalTokens(), 0)

	// Reset
	client.Reset()

	assert.Equal(t, 0, client.TotalTokens())
	assert.Equal(t, 0.0, client.TotalCostUSD())
	stats := client.Stats()
	assert.Empty(t, stats.CallsByTier)
}

func TestTieredSubClientCostCalculation(t *testing.T) {
	smartModel := &mockLLM{response: "response"}

	client, err := NewTieredSubClient(TieredSubClientConfig{
		SmartModel: smartModel,
	})
	require.NoError(t, err)

	// Make queries with different tiers to verify cost calculation
	_, _ = client.QueryWithTier(context.Background(), "test", TierFast)
	fastCost := client.TotalCostUSD()

	client.Reset()

	_, _ = client.QueryWithTier(context.Background(), "test", TierSmart)
	smartCost := client.TotalCostUSD()

	// Smart tier should cost more than fast tier
	assert.Greater(t, smartCost, fastCost)
}
