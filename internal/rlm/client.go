package rlm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/sourcegraph/conc/pool"
)

// ModelTier defines the capability level for sub-agent calls.
type ModelTier int

const (
	// TierFast uses cheap, fast models (Haiku, Flash) for high-volume work
	TierFast ModelTier = iota
	// TierSmart uses balanced models (Sonnet) for moderate complexity
	TierSmart
	// TierBest uses frontier models (Opus, Claude Code) for synthesis
	TierBest
)

func (t ModelTier) String() string {
	switch t {
	case TierFast:
		return "fast"
	case TierSmart:
		return "smart"
	case TierBest:
		return "best"
	default:
		return "unknown"
	}
}

// Pricing per 1K tokens (approximate, USD)
var tierPricing = map[ModelTier]struct{ input, output float64 }{
	TierFast:  {0.00025, 0.00125},  // Haiku-level
	TierSmart: {0.003, 0.015},      // Sonnet-level
	TierBest:  {0.015, 0.075},      // Opus-level
}

// TieredSubClient implements rlm.SubLLMClient with model tier routing.
type TieredSubClient struct {
	models        map[ModelTier]core.LLM
	defaultTier   ModelTier
	maxConcurrent int
	timeout       time.Duration

	// Tracking (protected by mu)
	mu          sync.Mutex
	totalPrompt int
	totalCompl  int
	callsByTier map[ModelTier]int
	costUSD     float64
}

// TieredSubClientConfig configures the tiered sub-client.
type TieredSubClientConfig struct {
	FastModel    core.LLM // For TierFast (optional)
	SmartModel   core.LLM // For TierSmart (required)
	BestModel    core.LLM // For TierBest (optional, defaults to SmartModel)
	DefaultTier  ModelTier
	MaxConcurrent int
	Timeout      time.Duration
}

// NewTieredSubClient creates a sub-client with model tier support.
func NewTieredSubClient(config TieredSubClientConfig) (*TieredSubClient, error) {
	if config.SmartModel == nil {
		return nil, fmt.Errorf("SmartModel is required")
	}

	models := make(map[ModelTier]core.LLM)
	models[TierSmart] = config.SmartModel

	// FastModel defaults to SmartModel if not provided
	if config.FastModel != nil {
		models[TierFast] = config.FastModel
	} else {
		models[TierFast] = config.SmartModel
	}

	// BestModel defaults to SmartModel if not provided
	if config.BestModel != nil {
		models[TierBest] = config.BestModel
	} else {
		models[TierBest] = config.SmartModel
	}

	maxConcurrent := config.MaxConcurrent
	if maxConcurrent == 0 {
		maxConcurrent = 10
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	// Default to TierSmart if not specified (TierFast is 0, so we need explicit check)
	defaultTier := config.DefaultTier
	if defaultTier == TierFast && config.FastModel == nil {
		// If TierFast was not explicitly configured (just zero value), use TierSmart
		defaultTier = TierSmart
	}

	return &TieredSubClient{
		models:        models,
		defaultTier:   defaultTier,
		maxConcurrent: maxConcurrent,
		timeout:       timeout,
		callsByTier:   make(map[ModelTier]int),
	}, nil
}

// Query implements rlm.SubLLMClient using the default tier.
func (c *TieredSubClient) Query(ctx context.Context, prompt string) (rlm.QueryResponse, error) {
	return c.QueryWithTier(ctx, prompt, c.defaultTier)
}

// QueryWithTier makes a query using a specific model tier.
func (c *TieredSubClient) QueryWithTier(ctx context.Context, prompt string, tier ModelTier) (rlm.QueryResponse, error) {
	actualTier := tier
	model, ok := c.models[tier]
	if !ok {
		model = c.models[TierSmart] // Fallback
		actualTier = TierSmart      // Record actual tier used for cost accounting
	}

	// Apply timeout
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := model.Generate(ctx, prompt)
	if err != nil {
		return rlm.QueryResponse{}, fmt.Errorf("tier %s query failed: %w", tier, err)
	}

	var promptTokens, completionTokens int
	if resp.Usage != nil {
		promptTokens = resp.Usage.PromptTokens
		completionTokens = resp.Usage.CompletionTokens
	}

	// Track usage with the actual tier used (for correct cost accounting)
	c.recordUsage(actualTier, promptTokens, completionTokens)

	return rlm.QueryResponse{
		Response:         resp.Content,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
	}, nil
}

// QueryBatched implements rlm.SubLLMClient with concurrent queries.
func (c *TieredSubClient) QueryBatched(ctx context.Context, prompts []string) ([]rlm.QueryResponse, error) {
	return c.QueryBatchedWithTier(ctx, prompts, c.defaultTier)
}

// QueryBatchedWithTier makes concurrent queries using a specific tier.
func (c *TieredSubClient) QueryBatchedWithTier(ctx context.Context, prompts []string, tier ModelTier) ([]rlm.QueryResponse, error) {
	if len(prompts) == 0 {
		return nil, nil
	}

	results := make([]rlm.QueryResponse, len(prompts))
	p := pool.New().WithMaxGoroutines(c.maxConcurrent).WithErrors().WithContext(ctx)

	for i, prompt := range prompts {
		i, prompt := i, prompt
		p.Go(func(ctx context.Context) error {
			resp, err := c.QueryWithTier(ctx, prompt, tier)
			if err != nil {
				results[i] = rlm.QueryResponse{Response: fmt.Sprintf("Error: %v", err)}
				return err
			}
			results[i] = resp
			return nil
		})
	}

	return results, p.Wait()
}

func (c *TieredSubClient) recordUsage(tier ModelTier, prompt, completion int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalPrompt += prompt
	c.totalCompl += completion
	c.callsByTier[tier]++

	// Calculate cost
	pricing := tierPricing[tier]
	cost := (float64(prompt) * pricing.input / 1000) +
		(float64(completion) * pricing.output / 1000)
	c.costUSD += cost
}

// TotalTokens returns total tokens used across all calls.
func (c *TieredSubClient) TotalTokens() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalPrompt + c.totalCompl
}

// TotalCostUSD returns estimated cost in USD.
func (c *TieredSubClient) TotalCostUSD() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.costUSD
}

// Stats returns usage statistics.
func (c *TieredSubClient) Stats() SubClientStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	tierCalls := make(map[ModelTier]int)
	for k, v := range c.callsByTier {
		tierCalls[k] = v
	}

	return SubClientStats{
		TotalPromptTokens:     c.totalPrompt,
		TotalCompletionTokens: c.totalCompl,
		CallsByTier:           tierCalls,
		TotalCostUSD:          c.costUSD,
	}
}

// Reset clears usage tracking.
func (c *TieredSubClient) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalPrompt = 0
	c.totalCompl = 0
	c.callsByTier = make(map[ModelTier]int)
	c.costUSD = 0
}

// SubClientStats contains usage statistics.
type SubClientStats struct {
	TotalPromptTokens     int
	TotalCompletionTokens int
	CallsByTier           map[ModelTier]int
	TotalCostUSD          float64
}
