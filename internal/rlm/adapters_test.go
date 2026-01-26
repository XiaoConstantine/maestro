package rlm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLLMSubAgentAdapter(t *testing.T) {
	t.Run("Query returns response with usage tracking", func(t *testing.T) {
		llm := &mockLLM{response: "test response"}
		adapter := NewLLMSubAgentAdapter(llm, LLMSubAgentConfig{
			Name:        "test-agent",
			InputPrice:  0.01,
			OutputPrice: 0.03,
		})

		ctx := context.Background()
		resp, err := adapter.Query(ctx, "test prompt")
		require.NoError(t, err)

		assert.Equal(t, "test response", resp.Response)

		// Check stats were recorded
		stats := adapter.Stats()
		assert.Equal(t, 1, stats.TotalQueries)
		assert.Greater(t, stats.TotalPromptTokens, 0)
	})

	t.Run("QueryBatched processes multiple prompts", func(t *testing.T) {
		llm := &mockLLM{response: "batched response"}
		adapter := NewLLMSubAgentAdapter(llm, LLMSubAgentConfig{
			Name: "batch-agent",
		})

		ctx := context.Background()
		prompts := []string{"prompt1", "prompt2", "prompt3"}
		results, err := adapter.QueryBatched(ctx, prompts)
		require.NoError(t, err)

		assert.Len(t, results, 3)

		stats := adapter.Stats()
		assert.Equal(t, 3, stats.TotalQueries)
	})

	t.Run("Name returns configured name", func(t *testing.T) {
		adapter := NewLLMSubAgentAdapter(&mockLLM{}, LLMSubAgentConfig{
			Name: "my-agent",
		})

		assert.Equal(t, "my-agent", adapter.Name())
	})

	t.Run("TokenPricing returns configured prices", func(t *testing.T) {
		adapter := NewLLMSubAgentAdapter(&mockLLM{}, LLMSubAgentConfig{
			InputPrice:  0.005,
			OutputPrice: 0.015,
		})

		input, output := adapter.TokenPricing()
		assert.Equal(t, 0.005, input)
		assert.Equal(t, 0.015, output)
	})

	t.Run("Default capabilities include code analysis", func(t *testing.T) {
		adapter := NewLLMSubAgentAdapter(&mockLLM{}, LLMSubAgentConfig{})

		caps := adapter.Capabilities()
		hasCodeAnalysis := false
		for _, cap := range caps {
			if cap == CapabilityCodeAnalysis {
				hasCodeAnalysis = true
				break
			}
		}
		assert.True(t, hasCodeAnalysis, "Expected CapabilityCodeAnalysis in default capabilities")
	})
}

func TestTieredSubClientAdapter(t *testing.T) {
	t.Run("Wraps TieredSubClient correctly", func(t *testing.T) {
		smartLLM := &mockLLM{response: "smart response"}
		client, err := NewTieredSubClient(TieredSubClientConfig{
			SmartModel: smartLLM,
		})
		require.NoError(t, err)

		adapter := NewTieredSubClientAdapter(client, "test-tiered")

		assert.Equal(t, "test-tiered", adapter.Name())

		// Test Query through adapter
		ctx := context.Background()
		resp, err := adapter.Query(ctx, "test")
		require.NoError(t, err)
		assert.Equal(t, "smart response", resp.Response)
	})

	t.Run("Default name is tiered-anthropic", func(t *testing.T) {
		client, _ := NewTieredSubClient(TieredSubClientConfig{
			SmartModel: &mockLLM{},
		})
		adapter := NewTieredSubClientAdapter(client, "")

		assert.Equal(t, "tiered-anthropic", adapter.Name())
	})

	t.Run("Stats aggregates from TieredSubClient", func(t *testing.T) {
		client, _ := NewTieredSubClient(TieredSubClientConfig{
			SmartModel: &mockLLM{response: "stats test"},
		})
		adapter := NewTieredSubClientAdapter(client, "stats-test")

		// Make some queries to generate stats
		ctx := context.Background()
		_, _ = adapter.Query(ctx, "test1")
		_, _ = adapter.Query(ctx, "test2")

		stats := adapter.Stats()
		assert.Greater(t, stats.TotalQueries, 0)
	})

	t.Run("Capabilities returns expected values", func(t *testing.T) {
		client, _ := NewTieredSubClient(TieredSubClientConfig{
			SmartModel: &mockLLM{},
		})
		adapter := NewTieredSubClientAdapter(client, "test")

		caps := adapter.Capabilities()
		assert.Contains(t, caps, CapabilityCodeAnalysis)
		assert.Contains(t, caps, CapabilityCodeGeneration)
	})

	t.Run("TokenPricing returns smart tier pricing", func(t *testing.T) {
		client, _ := NewTieredSubClient(TieredSubClientConfig{
			SmartModel: &mockLLM{},
		})
		adapter := NewTieredSubClientAdapter(client, "test")

		input, output := adapter.TokenPricing()
		// Should match TierSmart pricing
		assert.Equal(t, tierPricing[TierSmart].input, input)
		assert.Equal(t, tierPricing[TierSmart].output, output)
	})
}

func TestProviderConfig(t *testing.T) {
	t.Run("NewSubAgentFromConfig rejects unsupported provider", func(t *testing.T) {
		_, err := NewSubAgentFromConfig(ProviderConfig{
			Provider: "unsupported",
			APIKey:   "test-key",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported provider")
	})

	t.Run("NewTieredSubClientFromConfig rejects unsupported provider", func(t *testing.T) {
		_, err := NewTieredSubClientFromConfig(ProviderConfig{
			Provider: "unsupported",
			APIKey:   "test-key",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported provider")
	})

	// Note: Testing actual provider creation requires valid API keys,
	// so we skip those in unit tests. Integration tests should cover them.
}

func TestOpenAIPricing(t *testing.T) {
	// Verify pricing map has expected models
	expectedModels := []string{"gpt-4o", "gpt-4o-mini", "o3", "o3-mini", "gpt-5.2-codex"}

	for _, model := range expectedModels {
		_, ok := openAIPricing[model]
		assert.True(t, ok, "Expected pricing for model %q", model)
	}

	// Verify GPT-4o pricing values
	gpt4o := openAIPricing["gpt-4o"]
	assert.Equal(t, 0.005, gpt4o.input, "GPT-4o input price")
	assert.Equal(t, 0.015, gpt4o.output, "GPT-4o output price")

	// Verify mini pricing is cheaper
	gpt4oMini := openAIPricing["gpt-4o-mini"]
	assert.Less(t, gpt4oMini.input, gpt4o.input, "Mini should be cheaper than full model")
	assert.Less(t, gpt4oMini.output, gpt4o.output, "Mini should be cheaper than full model")
}

func TestAnthropicPricing(t *testing.T) {
	// Verify pricing map has expected models
	expectedModels := []string{
		"claude-3-5-sonnet-20241022",
		"claude-sonnet-4-5-20250929",
		"claude-opus-4-5-20251101",
		"claude-3-5-haiku-20241022",
	}

	for _, model := range expectedModels {
		_, ok := anthropicPricing[model]
		assert.True(t, ok, "Expected pricing for model %q", model)
	}

	// Verify Haiku is cheaper than Sonnet
	haiku := anthropicPricing["claude-3-5-haiku-20241022"]
	sonnet := anthropicPricing["claude-3-5-sonnet-20241022"]
	assert.Less(t, haiku.input, sonnet.input, "Haiku should be cheaper than Sonnet")

	// Verify Opus is more expensive than Sonnet
	opus := anthropicPricing["claude-opus-4-5-20251101"]
	assert.Greater(t, opus.input, sonnet.input, "Opus should be more expensive than Sonnet")
}

func TestSubAgentInterfaceImplementation(t *testing.T) {
	// Verify LLMSubAgentAdapter implements SubAgent
	var _ SubAgent = (*LLMSubAgentAdapter)(nil)

	// Verify TieredSubClientAdapter implements SubAgent
	var _ SubAgent = (*TieredSubClientAdapter)(nil)
}

func TestDefaultModels(t *testing.T) {
	assert.Equal(t, "claude-sonnet-4-5-20250929", DefaultAnthropicModel)
	assert.Equal(t, "gpt-4o", DefaultOpenAIModel)
}
