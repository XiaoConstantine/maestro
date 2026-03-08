// Package rlm provides RLM (Recursive Language Model) integration for Maestro.
package rlm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	dspyrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

// BudgetConfig configures token budget management.
type BudgetConfig struct {
	// MaxSteps limits root-level LLM decision points (0 = unlimited).
	MaxSteps int

	// MaxTokens limits total tokens across root + sub calls (0 = unlimited).
	MaxTokens int

	// MaxBudgetUSD sets the maximum spending limit per session (e.g., 1.00 for $1)
	MaxBudgetUSD float64

	// WarnThreshold triggers a warning when this percentage is reached (default: 0.8 for 80%)
	WarnThreshold float64

	// OnWarning is called when the warning threshold is reached
	OnWarning func(spent, budget float64)

	// OnLimit is called when the budget limit is reached
	OnLimit func(spent, budget float64)

	// TrackByAgent enables per-agent cost breakdown
	TrackByAgent bool
}

// DefaultBudgetConfig returns sensible defaults for budget management.
func DefaultBudgetConfig() BudgetConfig {
	return BudgetConfig{
		MaxSteps:      15,
		MaxTokens:     50000,
		MaxBudgetUSD:  1.00,
		WarnThreshold: 0.8,
		TrackByAgent:  true,
	}
}

// BudgetManager tracks and enforces token budgets across RLM sessions.
type BudgetManager struct {
	config BudgetConfig

	mu            sync.RWMutex
	stepsUsed     int
	tokensUsed    int
	totalSpent    float64
	agentCosts    map[string]float64
	tokensByAgent map[string]TokenUsage
	warningIssued bool
	startTime     time.Time
	lastUpdate    time.Time
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// BudgetStatus represents the current budget state.
type BudgetStatus struct {
	StepsUsed       int
	TokensUsed      int
	RemainingSteps  int
	RemainingTokens int
	TotalSpent      float64
	RemainingBudget float64
	PercentUsed     float64
	AtWarning       bool
	AtLimit         bool
	AgentBreakdown  map[string]AgentCostBreakdown
	StartTime       time.Time
	LastUpdate      time.Time
}

// AgentCostBreakdown shows cost and usage for a specific agent.
type AgentCostBreakdown struct {
	CostUSD          float64
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	PercentOfTotal   float64
}

// BudgetError indicates a budget-related error.
type BudgetError struct {
	Type    BudgetErrorType
	Spent   float64
	Budget  float64
	Message string
}

func (e *BudgetError) Error() string {
	return e.Message
}

// BudgetErrorType categorizes budget errors.
type BudgetErrorType int

const (
	// BudgetExceeded means the budget limit was reached
	BudgetExceeded BudgetErrorType = iota
	// BudgetWarning means the warning threshold was reached
	BudgetWarning
	// BudgetStepsExceeded means step budget was exhausted.
	BudgetStepsExceeded
	// BudgetTokensExceeded means token budget was exhausted.
	BudgetTokensExceeded
)

// NewBudgetManager creates a new budget manager with the given configuration.
func NewBudgetManager(config BudgetConfig) *BudgetManager {
	if config.WarnThreshold == 0 {
		config.WarnThreshold = 0.8
	}
	return &BudgetManager{
		config:        config,
		agentCosts:    make(map[string]float64),
		tokensByAgent: make(map[string]TokenUsage),
		startTime:     time.Now(),
		lastUpdate:    time.Now(),
	}
}

// RecordUsage records token usage and cost for an agent.
// Returns an error if the budget limit is exceeded.
func (m *BudgetManager) RecordUsage(agentName string, promptTokens, completionTokens int, costUSD float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.totalSpent += costUSD
	m.tokensUsed += promptTokens + completionTokens
	m.lastUpdate = time.Now()

	if m.config.TrackByAgent {
		m.agentCosts[agentName] += costUSD
		usage := m.tokensByAgent[agentName]
		usage.PromptTokens += promptTokens
		usage.CompletionTokens += completionTokens
		usage.TotalTokens += promptTokens + completionTokens
		m.tokensByAgent[agentName] = usage
	}

	// Check warning threshold
	if !m.warningIssued && m.config.MaxBudgetUSD > 0 {
		percentUsed := m.totalSpent / m.config.MaxBudgetUSD
		if percentUsed >= m.config.WarnThreshold {
			m.warningIssued = true
			if m.config.OnWarning != nil {
				m.config.OnWarning(m.totalSpent, m.config.MaxBudgetUSD)
			}
		}
	}

	// Check limit
	if m.config.MaxBudgetUSD > 0 && m.totalSpent >= m.config.MaxBudgetUSD {
		if m.config.OnLimit != nil {
			m.config.OnLimit(m.totalSpent, m.config.MaxBudgetUSD)
		}
		return &BudgetError{
			Type:    BudgetExceeded,
			Spent:   m.totalSpent,
			Budget:  m.config.MaxBudgetUSD,
			Message: fmt.Sprintf("budget limit exceeded: spent $%.4f of $%.4f budget", m.totalSpent, m.config.MaxBudgetUSD),
		}
	}

	// Token limit is best-effort: we account usage, then surface an over-limit error.
	if m.config.MaxTokens > 0 && m.tokensUsed >= m.config.MaxTokens {
		return &BudgetError{
			Type:    BudgetTokensExceeded,
			Message: fmt.Sprintf("token budget exhausted: used %d of %d tokens", m.tokensUsed, m.config.MaxTokens),
		}
	}

	return nil
}

// Step consumes one root-level decision step.
func (m *BudgetManager) Step() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config.MaxSteps > 0 && m.stepsUsed >= m.config.MaxSteps {
		return &BudgetError{
			Type:    BudgetStepsExceeded,
			Message: fmt.Sprintf("step budget exhausted: used %d of %d steps", m.stepsUsed, m.config.MaxSteps),
		}
	}

	m.stepsUsed++
	m.lastUpdate = time.Now()
	return nil
}

// Tokens records token usage against the global token budget.
// Token budget is best-effort, so this method records usage first and then
// returns an error if the limit has been crossed.
func (m *BudgetManager) Tokens(n int) error {
	if n <= 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.tokensUsed += n
	m.lastUpdate = time.Now()

	if m.config.MaxTokens > 0 && m.tokensUsed >= m.config.MaxTokens {
		return &BudgetError{
			Type:    BudgetTokensExceeded,
			Message: fmt.Sprintf("token budget exhausted: used %d of %d tokens", m.tokensUsed, m.config.MaxTokens),
		}
	}

	return nil
}

// CheckBudget checks if there is remaining budget for a query.
// Returns nil if OK, or a BudgetError if at limit.
func (m *BudgetManager) CheckBudget() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config.MaxTokens > 0 && m.tokensUsed >= m.config.MaxTokens {
		return &BudgetError{
			Type:    BudgetTokensExceeded,
			Message: fmt.Sprintf("token budget exhausted: used %d of %d tokens", m.tokensUsed, m.config.MaxTokens),
		}
	}

	if m.config.MaxBudgetUSD > 0 && m.totalSpent >= m.config.MaxBudgetUSD {
		return &BudgetError{
			Type:    BudgetExceeded,
			Spent:   m.totalSpent,
			Budget:  m.config.MaxBudgetUSD,
			Message: fmt.Sprintf("budget exhausted: spent $%.4f of $%.4f", m.totalSpent, m.config.MaxBudgetUSD),
		}
	}
	return nil
}

// RemainingSteps returns the number of remaining step budget units, or -1 for unlimited.
func (m *BudgetManager) RemainingSteps() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config.MaxSteps <= 0 {
		return -1
	}
	remaining := m.config.MaxSteps - m.stepsUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// RemainingTokens returns the number of remaining token budget units, or -1 for unlimited.
func (m *BudgetManager) RemainingTokens() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config.MaxTokens <= 0 {
		return -1
	}
	remaining := m.config.MaxTokens - m.tokensUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// StepsUsed returns the number of consumed budget steps.
func (m *BudgetManager) StepsUsed() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stepsUsed
}

// TokensUsed returns the number of consumed budget tokens.
func (m *BudgetManager) TokensUsed() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tokensUsed
}

// EstimateCost estimates the cost of a query based on estimated tokens.
func (m *BudgetManager) EstimateCost(estimatedPromptTokens, estimatedCompletionTokens int, inputPricePerK, outputPricePerK float64) float64 {
	return (float64(estimatedPromptTokens) * inputPricePerK / 1000) +
		(float64(estimatedCompletionTokens) * outputPricePerK / 1000)
}

// WouldExceedBudget checks if an estimated cost would exceed the budget.
func (m *BudgetManager) WouldExceedBudget(estimatedCost float64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config.MaxBudgetUSD <= 0 {
		return false
	}
	return (m.totalSpent + estimatedCost) >= m.config.MaxBudgetUSD
}

// RemainingBudget returns the remaining budget in USD.
func (m *BudgetManager) RemainingBudget() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config.MaxBudgetUSD <= 0 {
		return -1 // Unlimited
	}
	remaining := m.config.MaxBudgetUSD - m.totalSpent
	if remaining < 0 {
		return 0
	}
	return remaining
}

// TotalSpent returns the total amount spent so far.
func (m *BudgetManager) TotalSpent() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalSpent
}

// Status returns a complete budget status snapshot.
func (m *BudgetManager) Status() BudgetStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := BudgetStatus{
		StepsUsed:       m.stepsUsed,
		TokensUsed:      m.tokensUsed,
		RemainingSteps:  -1,
		RemainingTokens: -1,
		TotalSpent:      m.totalSpent,
		RemainingBudget: m.config.MaxBudgetUSD - m.totalSpent,
		StartTime:       m.startTime,
		LastUpdate:      m.lastUpdate,
	}

	if m.config.MaxSteps > 0 {
		status.RemainingSteps = m.config.MaxSteps - m.stepsUsed
		if status.RemainingSteps < 0 {
			status.RemainingSteps = 0
		}
	}

	if m.config.MaxTokens > 0 {
		status.RemainingTokens = m.config.MaxTokens - m.tokensUsed
		if status.RemainingTokens < 0 {
			status.RemainingTokens = 0
		}
	}

	if m.config.MaxBudgetUSD > 0 {
		status.PercentUsed = (m.totalSpent / m.config.MaxBudgetUSD) * 100
		status.AtWarning = status.PercentUsed >= (m.config.WarnThreshold * 100)
		status.AtLimit = m.totalSpent >= m.config.MaxBudgetUSD
	}

	if status.RemainingBudget < 0 {
		status.RemainingBudget = 0
	}

	// Build agent breakdown
	if m.config.TrackByAgent {
		status.AgentBreakdown = make(map[string]AgentCostBreakdown)
		for agent, cost := range m.agentCosts {
			usage := m.tokensByAgent[agent]
			percentOfTotal := 0.0
			if m.totalSpent > 0 {
				percentOfTotal = (cost / m.totalSpent) * 100
			}
			status.AgentBreakdown[agent] = AgentCostBreakdown{
				CostUSD:          cost,
				PromptTokens:     usage.PromptTokens,
				CompletionTokens: usage.CompletionTokens,
				TotalTokens:      usage.TotalTokens,
				PercentOfTotal:   percentOfTotal,
			}
		}
	}

	return status
}

// Reset clears all budget tracking state.
func (m *BudgetManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.totalSpent = 0
	m.stepsUsed = 0
	m.tokensUsed = 0
	m.agentCosts = make(map[string]float64)
	m.tokensByAgent = make(map[string]TokenUsage)
	m.warningIssued = false
	m.startTime = time.Now()
	m.lastUpdate = time.Now()
}

// SetBudget updates the maximum budget.
func (m *BudgetManager) SetBudget(maxBudgetUSD float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config.MaxBudgetUSD = maxBudgetUSD
	// Reset warning state if budget increased
	if m.config.MaxBudgetUSD > 0 && (m.totalSpent/m.config.MaxBudgetUSD) < m.config.WarnThreshold {
		m.warningIssued = false
	}
}

// BudgetAwareSubClient wraps a SubAgent with budget enforcement.
type BudgetAwareSubClient struct {
	delegate SubAgent
	budget   *BudgetManager
}

// NewBudgetAwareSubClient creates a SubAgent that enforces budget limits.
func NewBudgetAwareSubClient(delegate SubAgent, budget *BudgetManager) *BudgetAwareSubClient {
	return &BudgetAwareSubClient{
		delegate: delegate,
		budget:   budget,
	}
}

// Query implements rlm.SubLLMClient with budget checking.
func (c *BudgetAwareSubClient) Query(ctx context.Context, prompt string) (dspyrlm.QueryResponse, error) {
	// Check budget before query
	if err := c.budget.CheckBudget(); err != nil {
		return dspyrlm.QueryResponse{}, err
	}

	// Import the response type from the delegate
	resp, err := c.delegate.Query(ctx, prompt)
	if err != nil {
		return dspyrlm.QueryResponse{}, err
	}

	// Calculate cost from pricing
	inputPrice, outputPrice := c.delegate.TokenPricing()
	cost := c.budget.EstimateCost(resp.PromptTokens, resp.CompletionTokens, inputPrice, outputPrice)

	// Record usage (may return budget exceeded error)
	if budgetErr := c.budget.RecordUsage(c.delegate.Name(), resp.PromptTokens, resp.CompletionTokens, cost); budgetErr != nil {
		// Return the response but also the budget error
		return dspyrlm.QueryResponse{
			Response:         resp.Response,
			PromptTokens:     resp.PromptTokens,
			CompletionTokens: resp.CompletionTokens,
		}, budgetErr
	}

	return dspyrlm.QueryResponse{
		Response:         resp.Response,
		PromptTokens:     resp.PromptTokens,
		CompletionTokens: resp.CompletionTokens,
	}, nil
}

// QueryBatched implements rlm.SubLLMClient with budget checking.
func (c *BudgetAwareSubClient) QueryBatched(ctx context.Context, prompts []string) ([]dspyrlm.QueryResponse, error) {
	results := make([]dspyrlm.QueryResponse, 0, len(prompts))
	inputPrice, outputPrice := c.delegate.TokenPricing()

	for _, prompt := range prompts {
		// Enforce budget per item to prevent large batches from overrunning limits.
		if err := c.budget.CheckBudget(); err != nil {
			return results, err
		}

		resp, err := c.delegate.Query(ctx, prompt)
		if err != nil {
			results = append(results, dspyrlm.QueryResponse{
				Response: fmt.Sprintf("Error: %v", err),
			})
			continue
		}

		cost := c.budget.EstimateCost(resp.PromptTokens, resp.CompletionTokens, inputPrice, outputPrice)
		if budgetErr := c.budget.RecordUsage(c.delegate.Name(), resp.PromptTokens, resp.CompletionTokens, cost); budgetErr != nil {
			results = append(results, dspyrlm.QueryResponse{
				Response:         resp.Response,
				PromptTokens:     resp.PromptTokens,
				CompletionTokens: resp.CompletionTokens,
			})
			return results, budgetErr
		}

		results = append(results, dspyrlm.QueryResponse{
			Response:         resp.Response,
			PromptTokens:     resp.PromptTokens,
			CompletionTokens: resp.CompletionTokens,
		})
	}

	return results, nil
}

// Name implements SubAgent.
func (c *BudgetAwareSubClient) Name() string {
	return c.delegate.Name()
}

// Capabilities implements SubAgent.
func (c *BudgetAwareSubClient) Capabilities() []Capability {
	return c.delegate.Capabilities()
}

// TokenPricing implements SubAgent.
func (c *BudgetAwareSubClient) TokenPricing() (input float64, output float64) {
	return c.delegate.TokenPricing()
}

// Stats implements SubAgent.
func (c *BudgetAwareSubClient) Stats() AgentStats {
	return c.delegate.Stats()
}

// Reset clears state on the wrapped sub-agent when supported.
func (c *BudgetAwareSubClient) Reset() {
	if resetter, ok := c.delegate.(interface{ Reset() }); ok {
		resetter.Reset()
	}
}

// BudgetStatus returns the current budget status.
func (c *BudgetAwareSubClient) BudgetStatus() BudgetStatus {
	return c.budget.Status()
}

// BudgetAwareLLM wraps a root core.LLM with budget enforcement.
type BudgetAwareLLM struct {
	delegate    core.LLM
	budget      *BudgetManager
	name        string
	inputPrice  float64
	outputPrice float64
}

// NewBudgetAwareLLM creates a root LLM wrapper that enforces budget limits.
func NewBudgetAwareLLM(delegate core.LLM, budget *BudgetManager) *BudgetAwareLLM {
	inputPrice, outputPrice := inferLLMPricing(delegate)
	name := fmt.Sprintf("root-%s-%s", delegate.ProviderName(), delegate.ModelID())
	return &BudgetAwareLLM{
		delegate:    delegate,
		budget:      budget,
		name:        name,
		inputPrice:  inputPrice,
		outputPrice: outputPrice,
	}
}

// Generate implements core.LLM with step + token + cost budget checks.
func (l *BudgetAwareLLM) Generate(ctx context.Context, prompt string, options ...core.GenerateOption) (*core.LLMResponse, error) {
	if err := l.budget.CheckBudget(); err != nil {
		return nil, err
	}
	if err := l.budget.Step(); err != nil {
		return nil, err
	}

	resp, err := l.delegate.Generate(ctx, prompt, options...)
	if err != nil {
		return nil, err
	}

	promptTokens, completionTokens := usageFromResponse(resp)
	cost := l.budget.EstimateCost(promptTokens, completionTokens, l.inputPrice, l.outputPrice)
	if budgetErr := l.budget.RecordUsage(l.name, promptTokens, completionTokens, cost); budgetErr != nil {
		return resp, budgetErr
	}
	return resp, nil
}

// GenerateWithContent implements core.LLM with budget checks.
func (l *BudgetAwareLLM) GenerateWithContent(ctx context.Context, content []core.ContentBlock, options ...core.GenerateOption) (*core.LLMResponse, error) {
	if err := l.budget.CheckBudget(); err != nil {
		return nil, err
	}
	if err := l.budget.Step(); err != nil {
		return nil, err
	}

	resp, err := l.delegate.GenerateWithContent(ctx, content, options...)
	if err != nil {
		return nil, err
	}

	promptTokens, completionTokens := usageFromResponse(resp)
	cost := l.budget.EstimateCost(promptTokens, completionTokens, l.inputPrice, l.outputPrice)
	if budgetErr := l.budget.RecordUsage(l.name, promptTokens, completionTokens, cost); budgetErr != nil {
		return resp, budgetErr
	}
	return resp, nil
}

// GenerateWithJSON forwards directly to the wrapped LLM.
func (l *BudgetAwareLLM) GenerateWithJSON(ctx context.Context, prompt string, options ...core.GenerateOption) (map[string]interface{}, error) {
	return l.delegate.GenerateWithJSON(ctx, prompt, options...)
}

// GenerateWithFunctions forwards directly to the wrapped LLM.
func (l *BudgetAwareLLM) GenerateWithFunctions(ctx context.Context, prompt string, functions []map[string]interface{}, options ...core.GenerateOption) (map[string]interface{}, error) {
	return l.delegate.GenerateWithFunctions(ctx, prompt, functions, options...)
}

// CreateEmbedding forwards directly to the wrapped LLM.
func (l *BudgetAwareLLM) CreateEmbedding(ctx context.Context, input string, options ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	return l.delegate.CreateEmbedding(ctx, input, options...)
}

// CreateEmbeddings forwards directly to the wrapped LLM.
func (l *BudgetAwareLLM) CreateEmbeddings(ctx context.Context, inputs []string, options ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	return l.delegate.CreateEmbeddings(ctx, inputs, options...)
}

// StreamGenerate forwards directly to the wrapped LLM.
func (l *BudgetAwareLLM) StreamGenerate(ctx context.Context, prompt string, options ...core.GenerateOption) (*core.StreamResponse, error) {
	return l.delegate.StreamGenerate(ctx, prompt, options...)
}

// StreamGenerateWithContent forwards directly to the wrapped LLM.
func (l *BudgetAwareLLM) StreamGenerateWithContent(ctx context.Context, content []core.ContentBlock, options ...core.GenerateOption) (*core.StreamResponse, error) {
	return l.delegate.StreamGenerateWithContent(ctx, content, options...)
}

// ProviderName forwards directly to the wrapped LLM.
func (l *BudgetAwareLLM) ProviderName() string {
	return l.delegate.ProviderName()
}

// ModelID forwards directly to the wrapped LLM.
func (l *BudgetAwareLLM) ModelID() string {
	return l.delegate.ModelID()
}

// Capabilities forwards directly to the wrapped LLM.
func (l *BudgetAwareLLM) Capabilities() []core.Capability {
	return l.delegate.Capabilities()
}

// Reset clears state on the wrapped root LLM when supported.
func (l *BudgetAwareLLM) Reset() {
	if resetter, ok := l.delegate.(interface{ Reset() }); ok {
		resetter.Reset()
	}
}

func usageFromResponse(resp *core.LLMResponse) (promptTokens, completionTokens int) {
	if resp == nil || resp.Usage == nil {
		return 0, 0
	}
	return resp.Usage.PromptTokens, resp.Usage.CompletionTokens
}

func inferLLMPricing(llm core.LLM) (input, output float64) {
	modelID := strings.ToLower(llm.ModelID())
	provider := strings.ToLower(llm.ProviderName())

	switch provider {
	case "openai", "codex":
		if pricing, ok := openAIPricing[modelID]; ok {
			return pricing.input, pricing.output
		}
	case "anthropic":
		if pricing, ok := anthropicPricing[modelID]; ok {
			return pricing.input, pricing.output
		}
	case "claude-code":
		return 0.003, 0.015
	}

	if strings.Contains(modelID, "haiku") || strings.Contains(modelID, "flash") || strings.Contains(modelID, "mini") {
		return 0.001, 0.005
	}
	if strings.Contains(modelID, "opus") || strings.Contains(modelID, "o3") || strings.Contains(modelID, "gpt-5") {
		return 0.015, 0.075
	}
	return 0.003, 0.015
}
