package rlm

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/sourcegraph/conc/pool"
)

// TieredSubClientAdapter wraps TieredSubClient to implement SubAgent.
// TieredSubClient already implements rlm.SubLLMClient, so we just add
// the orchestration methods.
type TieredSubClientAdapter struct {
	*TieredSubClient // Embed for SubLLMClient methods
	name             string
	capabilities     []Capability
}

// NewTieredSubClientAdapter creates a SubAgent adapter for TieredSubClient.
func NewTieredSubClientAdapter(client *TieredSubClient, name string) *TieredSubClientAdapter {
	if name == "" {
		name = "tiered-anthropic"
	}
	return &TieredSubClientAdapter{
		TieredSubClient: client,
		name:            name,
		capabilities: []Capability{
			CapabilityCodeAnalysis,
			CapabilityCodeGeneration,
		},
	}
}

// Name implements SubAgent.
func (a *TieredSubClientAdapter) Name() string {
	return a.name
}

// Capabilities implements SubAgent.
func (a *TieredSubClientAdapter) Capabilities() []Capability {
	return a.capabilities
}

// TokenPricing implements SubAgent (returns TierSmart pricing as default).
func (a *TieredSubClientAdapter) TokenPricing() (input float64, output float64) {
	pricing := tierPricing[TierSmart]
	return pricing.input, pricing.output
}

// Stats implements SubAgent.
func (a *TieredSubClientAdapter) Stats() AgentStats {
	stats := a.TieredSubClient.Stats()

	// Sum up calls across all tiers to get total queries
	totalQueries := 0
	for _, count := range stats.CallsByTier {
		totalQueries += count
	}

	return AgentStats{
		TotalPromptTokens:     stats.TotalPromptTokens,
		TotalCompletionTokens: stats.TotalCompletionTokens,
		TotalQueries:          totalQueries,
		TotalCostUSD:          stats.TotalCostUSD,
		CallsByTier:           stats.CallsByTier,
	}
}

// LLMSubAgentAdapter wraps any core.LLM to implement SubAgent.
// This is a generic adapter for single-model backends.
type LLMSubAgentAdapter struct {
	llm          core.LLM
	name         string
	capabilities []Capability
	inputPrice   float64 // per 1K tokens
	outputPrice  float64 // per 1K tokens

	mu           sync.Mutex
	totalPrompt  int
	totalCompl   int
	totalQueries int
	totalCost    float64
}

// LLMSubAgentConfig configures the LLM adapter.
type LLMSubAgentConfig struct {
	Name         string
	InputPrice   float64 // per 1K tokens
	OutputPrice  float64 // per 1K tokens
	Capabilities []Capability
}

// NewLLMSubAgentAdapter creates a SubAgent from any core.LLM.
func NewLLMSubAgentAdapter(llm core.LLM, config LLMSubAgentConfig) *LLMSubAgentAdapter {
	caps := config.Capabilities
	if len(caps) == 0 {
		caps = []Capability{CapabilityCodeAnalysis, CapabilityCodeGeneration}
	}
	return &LLMSubAgentAdapter{
		llm:          llm,
		name:         config.Name,
		inputPrice:   config.InputPrice,
		outputPrice:  config.OutputPrice,
		capabilities: caps,
	}
}

// Query implements rlm.SubLLMClient.
func (a *LLMSubAgentAdapter) Query(ctx context.Context, prompt string) (rlm.QueryResponse, error) {
	resp, err := a.llm.Generate(ctx, prompt)
	if err != nil {
		return rlm.QueryResponse{}, err
	}

	var promptTokens, completionTokens int
	if resp.Usage != nil {
		promptTokens = resp.Usage.PromptTokens
		completionTokens = resp.Usage.CompletionTokens
	}

	a.recordUsage(promptTokens, completionTokens)

	return rlm.QueryResponse{
		Response:         resp.Content,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
	}, nil
}

// QueryBatched implements rlm.SubLLMClient.
func (a *LLMSubAgentAdapter) QueryBatched(ctx context.Context, prompts []string) ([]rlm.QueryResponse, error) {
	if len(prompts) == 0 {
		return nil, nil
	}

	results := make([]rlm.QueryResponse, len(prompts))
	p := pool.New().WithMaxGoroutines(defaultBatchConcurrency).WithContext(ctx)

	for i, prompt := range prompts {
		i, prompt := i, prompt
		p.Go(func(ctx context.Context) error {
			resp, err := a.Query(ctx, prompt)
			if err != nil {
				results[i] = rlm.QueryResponse{Response: fmt.Sprintf("Error: %v", err)}
				return nil
			}
			results[i] = resp
			return nil
		})
	}

	p.Wait()
	return results, nil
}

func (a *LLMSubAgentAdapter) recordUsage(prompt, completion int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.totalPrompt += prompt
	a.totalCompl += completion
	a.totalQueries++

	cost := (float64(prompt) * a.inputPrice / 1000) +
		(float64(completion) * a.outputPrice / 1000)
	a.totalCost += cost
}

// Name implements SubAgent.
func (a *LLMSubAgentAdapter) Name() string {
	return a.name
}

// Capabilities implements SubAgent.
func (a *LLMSubAgentAdapter) Capabilities() []Capability {
	return a.capabilities
}

// TokenPricing implements SubAgent.
func (a *LLMSubAgentAdapter) TokenPricing() (input float64, output float64) {
	return a.inputPrice, a.outputPrice
}

// Stats implements SubAgent.
func (a *LLMSubAgentAdapter) Stats() AgentStats {
	a.mu.Lock()
	defer a.mu.Unlock()

	return AgentStats{
		TotalPromptTokens:     a.totalPrompt,
		TotalCompletionTokens: a.totalCompl,
		TotalQueries:          a.totalQueries,
		TotalCostUSD:          a.totalCost,
	}
}

// OpenAI model pricing (per 1K tokens)
var openAIPricing = map[string]struct{ input, output float64 }{
	"gpt-4o":           {0.005, 0.015},
	"gpt-4o-mini":      {0.00015, 0.0006},
	"gpt-4-turbo":      {0.01, 0.03},
	"gpt-4":            {0.03, 0.06},
	"o1":               {0.015, 0.06},
	"o1-mini":          {0.003, 0.012},
	"o3":               {0.02, 0.08},
	"o3-mini":          {0.001, 0.004},
	"gpt-5":            {0.01, 0.04},
	"gpt-5-mini":       {0.002, 0.008},
	"gpt-5.2-codex":    {0.015, 0.06},
	"gpt-5.2-instant":  {0.0005, 0.002},
	"gpt-5.2-thinking": {0.01, 0.04},
}

// NewOpenAISubAgent creates a SubAgent using OpenAI models.
func NewOpenAISubAgent(modelID string, apiKey string) (*LLMSubAgentAdapter, error) {
	llm, err := llms.NewOpenAI(core.ModelID(modelID), apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI LLM: %w", err)
	}

	pricing, ok := openAIPricing[modelID]
	if !ok {
		pricing = struct{ input, output float64 }{0.01, 0.03}
	}

	return NewLLMSubAgentAdapter(llm, LLMSubAgentConfig{
		Name:        "openai-" + modelID,
		InputPrice:  pricing.input,
		OutputPrice: pricing.output,
	}), nil
}

// Anthropic model pricing (per 1K tokens)
var anthropicPricing = map[string]struct{ input, output float64 }{
	"claude-3-haiku-20240307":    {0.00025, 0.00125},
	"claude-3-5-haiku-20241022":  {0.001, 0.005},
	"claude-3-5-sonnet-20241022": {0.003, 0.015},
	"claude-sonnet-4-5-20250929": {0.003, 0.015},
	"claude-3-opus-20240229":     {0.015, 0.075},
	"claude-opus-4-5-20251101":   {0.015, 0.075},
}

// NewAnthropicSubAgent creates a SubAgent using Anthropic models.
func NewAnthropicSubAgent(modelID string, apiKey string) (*LLMSubAgentAdapter, error) {
	llm, err := llms.NewAnthropicLLM(apiKey, anthropic.Model(modelID))
	if err != nil {
		return nil, fmt.Errorf("failed to create Anthropic LLM: %w", err)
	}

	pricing, ok := anthropicPricing[modelID]
	if !ok {
		pricing = struct{ input, output float64 }{0.003, 0.015}
	}

	return NewLLMSubAgentAdapter(llm, LLMSubAgentConfig{
		Name:        "anthropic-" + modelID,
		InputPrice:  pricing.input,
		OutputPrice: pricing.output,
	}), nil
}

// Gemini model pricing (per 1K tokens)
// NOTE: Values are approximate defaults and may vary by region/tier.
var geminiPricing = map[string]struct{ input, output float64 }{
	"gemini-2.5-flash":      {0.00125, 0.005},
	"gemini-2.5-pro":        {0.0035, 0.0105},
	"gemini-2.5-flash-lite": {0.00075, 0.003},
	"gemini-2.0-flash":      {0.00075, 0.003},
}

// NewGoogleSubAgent creates a SubAgent using Google Gemini models.
func NewGoogleSubAgent(modelID string, apiKey string) (*LLMSubAgentAdapter, error) {
	llm, err := llms.NewGeminiLLM(apiKey, core.ModelID(modelID))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini LLM: %w", err)
	}

	pricing, ok := geminiPricing[modelID]
	if !ok {
		pricing = struct{ input, output float64 }{0.00125, 0.005}
	}

	return NewLLMSubAgentAdapter(llm, LLMSubAgentConfig{
		Name:        "google-" + modelID,
		InputPrice:  pricing.input,
		OutputPrice: pricing.output,
	}), nil
}

// ProviderConfig contains configuration for creating a SubAgent.
type ProviderConfig struct {
	Provider string // "anthropic", "openai", "google", "codex", "claude-code", etc.
	Model    string // Model name/ID
	APIKey   string // API key for the provider
	WorkDir  string // Working directory (for claude-code provider)
}

// DefaultAnthropicModel is the default model for Anthropic provider.
const DefaultAnthropicModel = "claude-sonnet-4-5-20250929"

// DefaultOpenAIModel is the default model for OpenAI provider.
const DefaultOpenAIModel = "gpt-4o"

// DefaultGoogleModel is the default model for Google provider.
const DefaultGoogleModel = "gemini-2.5-flash"

// NewSubAgentFromConfig creates a SubAgent based on provider configuration.
// This is the main factory function for creating SubAgents from CLI flags.
func NewSubAgentFromConfig(config ProviderConfig) (SubAgent, error) {
	provider := strings.ToLower(config.Provider)
	switch provider {
	case "anthropic":
		model := config.Model
		if model == "" {
			model = DefaultAnthropicModel
		}
		return NewAnthropicSubAgent(model, config.APIKey)

	case "openai", "codex":
		model := config.Model
		if model == "" {
			model = DefaultOpenAIModel
		}
		return NewOpenAISubAgent(model, config.APIKey)

	case "google", "gemini":
		model := config.Model
		if model == "" {
			model = DefaultGoogleModel
		}
		return NewGoogleSubAgent(model, config.APIKey)

	case "claude-code", "cc":
		// Claude Code uses CLI, no API key needed (uses local auth)
		return NewClaudeCodeAdapter(ClaudeCodeConfig{
			WorkDir: config.WorkDir,
		}), nil

	default:
		return nil, fmt.Errorf("unsupported provider: %s (supported: anthropic, openai, google, codex, claude-code)", config.Provider)
	}
}

// NewTieredSubClientFromConfig creates a TieredSubClient based on provider configuration.
// It uses the specified provider for the smart tier, with appropriate model selection.
func NewTieredSubClientFromConfig(config ProviderConfig) (*TieredSubClient, error) {
	var smartLLM, fastLLM, bestLLM core.LLM
	var err error

	provider := strings.ToLower(config.Provider)
	switch provider {
	case "anthropic":
		// Default to Sonnet for smart tier
		smartModel := config.Model
		if smartModel == "" {
			smartModel = "claude-sonnet-4-5-20250929"
		}
		smartLLM, err = llms.NewAnthropicLLM(config.APIKey, anthropic.Model(smartModel))
		if err != nil {
			return nil, fmt.Errorf("failed to create Anthropic smart LLM: %w", err)
		}

		// Use Haiku for fast tier
		fastLLM, err = llms.NewAnthropicLLM(config.APIKey, anthropic.Model("claude-3-5-haiku-20241022"))
		if err != nil {
			// Fall back to smart model if Haiku unavailable
			fastLLM = smartLLM
		}

		// Use Opus for best tier (or same as smart if not specified)
		bestLLM, err = llms.NewAnthropicLLM(config.APIKey, anthropic.Model("claude-opus-4-5-20251101"))
		if err != nil {
			bestLLM = smartLLM
		}

	case "openai", "codex":
		// Default to GPT-4o for smart tier
		smartModel := config.Model
		if smartModel == "" {
			smartModel = "gpt-4o"
		}
		smartLLM, err = llms.NewOpenAI(core.ModelID(smartModel), config.APIKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create OpenAI smart LLM: %w", err)
		}

		// Use GPT-4o-mini for fast tier
		fastLLM, err = llms.NewOpenAI(core.ModelID("gpt-4o-mini"), config.APIKey)
		if err != nil {
			fastLLM = smartLLM
		}

		// Use o3 or specified model for best tier
		bestModel := "o3"
		if config.Model != "" && (config.Model == "o3" || config.Model == "gpt-5" || config.Model == "gpt-5.2-codex") {
			bestModel = config.Model
		}
		bestLLM, err = llms.NewOpenAI(core.ModelID(bestModel), config.APIKey)
		if err != nil {
			bestLLM = smartLLM
		}

	case "google", "gemini":
		// Default to Gemini Flash for smart tier.
		smartModel := config.Model
		if smartModel == "" {
			smartModel = DefaultGoogleModel
		}
		smartLLM, err = llms.NewGeminiLLM(config.APIKey, core.ModelID(smartModel))
		if err != nil {
			return nil, fmt.Errorf("failed to create Gemini smart LLM: %w", err)
		}

		// Use Flash Lite for fast tier; fall back to smart model.
		fastLLM, err = llms.NewGeminiLLM(config.APIKey, core.ModelID("gemini-2.5-flash-lite"))
		if err != nil {
			fastLLM = smartLLM
		}

		// Use Pro for best tier; fall back to smart model.
		bestLLM, err = llms.NewGeminiLLM(config.APIKey, core.ModelID("gemini-2.5-pro"))
		if err != nil {
			bestLLM = smartLLM
		}

	default:
		return nil, fmt.Errorf("unsupported provider for tiered client: %s", config.Provider)
	}

	return NewTieredSubClient(TieredSubClientConfig{
		SmartModel:  smartLLM,
		FastModel:   fastLLM,
		BestModel:   bestLLM,
		DefaultTier: TierSmart,
	})
}
