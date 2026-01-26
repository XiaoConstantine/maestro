// Package rlm provides RLM (Recursive Language Model) integration for Maestro.
package rlm

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// BudgetConfig configures token budget management.
type BudgetConfig struct {
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
		MaxBudgetUSD:  1.00,
		WarnThreshold: 0.8,
		TrackByAgent:  true,
	}
}

// BudgetManager tracks and enforces token budgets across RLM sessions.
type BudgetManager struct {
	config BudgetConfig

	mu          sync.RWMutex
	totalSpent  float64
	agentCosts  map[string]float64
	tokensByAgent map[string]TokenUsage
	warningIssued bool
	startTime   time.Time
	lastUpdate  time.Time
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// BudgetStatus represents the current budget state.
type BudgetStatus struct {
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
)

// NewBudgetManager creates a new budget manager with the given configuration.
func NewBudgetManager(config BudgetConfig) *BudgetManager {
	if config.WarnThreshold == 0 {
		config.WarnThreshold = 0.8
	}
	return &BudgetManager{
		config:        config,
		agentCosts:   make(map[string]float64),
		tokensByAgent: make(map[string]TokenUsage),
		startTime:    time.Now(),
		lastUpdate:   time.Now(),
	}
}

// RecordUsage records token usage and cost for an agent.
// Returns an error if the budget limit is exceeded.
func (m *BudgetManager) RecordUsage(agentName string, promptTokens, completionTokens int, costUSD float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.totalSpent += costUSD
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

	return nil
}

// CheckBudget checks if there is remaining budget for a query.
// Returns nil if OK, or a BudgetError if at limit.
func (m *BudgetManager) CheckBudget() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

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
		TotalSpent:      m.totalSpent,
		RemainingBudget: m.config.MaxBudgetUSD - m.totalSpent,
		StartTime:       m.startTime,
		LastUpdate:      m.lastUpdate,
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
func (c *BudgetAwareSubClient) Query(ctx context.Context, prompt string) (QueryResponse, error) {
	// Check budget before query
	if err := c.budget.CheckBudget(); err != nil {
		return QueryResponse{}, err
	}

	// Import the response type from the delegate
	resp, err := c.delegate.Query(ctx, prompt)
	if err != nil {
		return QueryResponse{}, err
	}

	// Calculate cost from pricing
	inputPrice, outputPrice := c.delegate.TokenPricing()
	cost := c.budget.EstimateCost(resp.PromptTokens, resp.CompletionTokens, inputPrice, outputPrice)

	// Record usage (may return budget exceeded error)
	if budgetErr := c.budget.RecordUsage(c.delegate.Name(), resp.PromptTokens, resp.CompletionTokens, cost); budgetErr != nil {
		// Return the response but also the budget error
		return QueryResponse{
			Response:         resp.Response,
			PromptTokens:     resp.PromptTokens,
			CompletionTokens: resp.CompletionTokens,
		}, budgetErr
	}

	return QueryResponse{
		Response:         resp.Response,
		PromptTokens:     resp.PromptTokens,
		CompletionTokens: resp.CompletionTokens,
	}, nil
}

// QueryBatched implements rlm.SubLLMClient with budget checking.
func (c *BudgetAwareSubClient) QueryBatched(ctx context.Context, prompts []string) ([]QueryResponse, error) {
	if err := c.budget.CheckBudget(); err != nil {
		return nil, err
	}

	responses, err := c.delegate.QueryBatched(ctx, prompts)
	if err != nil {
		return nil, err
	}

	// Convert and record usage for each response
	results := make([]QueryResponse, len(responses))
	inputPrice, outputPrice := c.delegate.TokenPricing()

	for i, resp := range responses {
		cost := c.budget.EstimateCost(resp.PromptTokens, resp.CompletionTokens, inputPrice, outputPrice)
		_ = c.budget.RecordUsage(c.delegate.Name(), resp.PromptTokens, resp.CompletionTokens, cost)
		results[i] = QueryResponse{
			Response:         resp.Response,
			PromptTokens:     resp.PromptTokens,
			CompletionTokens: resp.CompletionTokens,
		}
	}

	// Check if we exceeded budget during batch
	if budgetErr := c.budget.CheckBudget(); budgetErr != nil {
		return results, budgetErr
	}

	return results, nil
}

// Name implements SubAgent.
func (c *BudgetAwareSubClient) Name() string {
	return c.delegate.Name() + "-budgeted"
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

// BudgetStatus returns the current budget status.
func (c *BudgetAwareSubClient) BudgetStatus() BudgetStatus {
	return c.budget.Status()
}

// QueryResponse wraps the response from a query (local copy to avoid import cycle).
type QueryResponse struct {
	Response         string
	PromptTokens     int
	CompletionTokens int
}
