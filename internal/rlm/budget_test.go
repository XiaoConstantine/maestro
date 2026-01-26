package rlm

import (
	"context"
	"sync"
	"testing"
	"time"

	dspyrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

func TestBudgetManager_Basic(t *testing.T) {
	config := BudgetConfig{
		MaxBudgetUSD:  1.0,
		WarnThreshold: 0.8,
		TrackByAgent:  true,
	}
	bm := NewBudgetManager(config)

	// Initial state
	if spent := bm.TotalSpent(); spent != 0 {
		t.Errorf("expected 0 spent, got %f", spent)
	}
	if remaining := bm.RemainingBudget(); remaining != 1.0 {
		t.Errorf("expected 1.0 remaining, got %f", remaining)
	}

	// Record usage
	err := bm.RecordUsage("agent1", 1000, 500, 0.10)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if spent := bm.TotalSpent(); spent != 0.10 {
		t.Errorf("expected 0.10 spent, got %f", spent)
	}
	if remaining := bm.RemainingBudget(); remaining != 0.90 {
		t.Errorf("expected 0.90 remaining, got %f", remaining)
	}
}

func TestBudgetManager_Warning(t *testing.T) {
	warningCalled := false
	config := BudgetConfig{
		MaxBudgetUSD:  1.0,
		WarnThreshold: 0.5, // 50% threshold for easier testing
		TrackByAgent:  true,
		OnWarning: func(spent, budget float64) {
			warningCalled = true
		},
	}
	bm := NewBudgetManager(config)

	// Below threshold
	err := bm.RecordUsage("agent1", 1000, 500, 0.40)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if warningCalled {
		t.Error("warning called too early")
	}

	// At threshold
	err = bm.RecordUsage("agent1", 1000, 500, 0.15)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !warningCalled {
		t.Error("warning not called at threshold")
	}
}

func TestBudgetManager_Limit(t *testing.T) {
	limitCalled := false
	config := BudgetConfig{
		MaxBudgetUSD:  1.0,
		WarnThreshold: 0.8,
		TrackByAgent:  true,
		OnLimit: func(spent, budget float64) {
			limitCalled = true
		},
	}
	bm := NewBudgetManager(config)

	// Below limit
	err := bm.RecordUsage("agent1", 1000, 500, 0.90)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// At limit
	err = bm.RecordUsage("agent1", 1000, 500, 0.15)
	if err == nil {
		t.Error("expected budget exceeded error")
	}
	if !limitCalled {
		t.Error("limit callback not called")
	}

	// Check error type
	budgetErr, ok := err.(*BudgetError)
	if !ok {
		t.Errorf("expected BudgetError, got %T", err)
	}
	if budgetErr.Type != BudgetExceeded {
		t.Errorf("expected BudgetExceeded, got %v", budgetErr.Type)
	}
}

func TestBudgetManager_CheckBudget(t *testing.T) {
	config := BudgetConfig{
		MaxBudgetUSD:  0.50,
		WarnThreshold: 0.8,
	}
	bm := NewBudgetManager(config)

	// Budget available
	err := bm.CheckBudget()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Exhaust budget
	_ = bm.RecordUsage("agent1", 1000, 500, 0.50)

	// Budget exhausted
	err = bm.CheckBudget()
	if err == nil {
		t.Error("expected error when budget exhausted")
	}
}

func TestBudgetManager_WouldExceedBudget(t *testing.T) {
	config := BudgetConfig{
		MaxBudgetUSD:  1.0,
		WarnThreshold: 0.8,
	}
	bm := NewBudgetManager(config)

	_ = bm.RecordUsage("agent1", 1000, 500, 0.80)

	// Would not exceed
	if bm.WouldExceedBudget(0.10) {
		t.Error("0.10 should not exceed budget")
	}

	// Would exceed
	if !bm.WouldExceedBudget(0.30) {
		t.Error("0.30 should exceed budget")
	}
}

func TestBudgetManager_AgentBreakdown(t *testing.T) {
	config := BudgetConfig{
		MaxBudgetUSD:  10.0,
		WarnThreshold: 0.8,
		TrackByAgent:  true,
	}
	bm := NewBudgetManager(config)

	_ = bm.RecordUsage("claude", 1000, 500, 0.30)
	_ = bm.RecordUsage("gpt-4", 2000, 1000, 0.50)
	_ = bm.RecordUsage("claude", 500, 250, 0.20)

	status := bm.Status()

	if len(status.AgentBreakdown) != 2 {
		t.Errorf("expected 2 agents, got %d", len(status.AgentBreakdown))
	}

	claude := status.AgentBreakdown["claude"]
	if claude.CostUSD != 0.50 {
		t.Errorf("expected claude cost 0.50, got %f", claude.CostUSD)
	}
	if claude.PromptTokens != 1500 {
		t.Errorf("expected claude prompt tokens 1500, got %d", claude.PromptTokens)
	}

	gpt := status.AgentBreakdown["gpt-4"]
	if gpt.CostUSD != 0.50 {
		t.Errorf("expected gpt-4 cost 0.50, got %f", gpt.CostUSD)
	}
}

func TestBudgetManager_Status(t *testing.T) {
	config := BudgetConfig{
		MaxBudgetUSD:  1.0,
		WarnThreshold: 0.8,
		TrackByAgent:  true,
	}
	bm := NewBudgetManager(config)

	_ = bm.RecordUsage("agent1", 1000, 500, 0.85)

	status := bm.Status()

	if status.TotalSpent != 0.85 {
		t.Errorf("expected 0.85 spent, got %f", status.TotalSpent)
	}
	// Use approximate comparison for floating point
	if status.RemainingBudget < 0.149 || status.RemainingBudget > 0.151 {
		t.Errorf("expected ~0.15 remaining, got %f", status.RemainingBudget)
	}
	if status.PercentUsed != 85.0 {
		t.Errorf("expected 85%% used, got %f", status.PercentUsed)
	}
	if !status.AtWarning {
		t.Error("expected at warning")
	}
	if status.AtLimit {
		t.Error("should not be at limit")
	}
}

func TestBudgetManager_Reset(t *testing.T) {
	config := BudgetConfig{
		MaxBudgetUSD:  1.0,
		WarnThreshold: 0.8,
		TrackByAgent:  true,
	}
	bm := NewBudgetManager(config)

	_ = bm.RecordUsage("agent1", 1000, 500, 0.85)
	bm.Reset()

	if spent := bm.TotalSpent(); spent != 0 {
		t.Errorf("expected 0 after reset, got %f", spent)
	}

	status := bm.Status()
	if len(status.AgentBreakdown) != 0 {
		t.Error("expected empty agent breakdown after reset")
	}
}

func TestBudgetManager_Concurrent(t *testing.T) {
	config := BudgetConfig{
		MaxBudgetUSD:  100.0,
		WarnThreshold: 0.8,
		TrackByAgent:  true,
	}
	bm := NewBudgetManager(config)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = bm.RecordUsage("agent1", 100, 50, 0.01)
		}(i)
	}
	wg.Wait()

	// Use approximate comparison for floating point due to concurrent access
	spent := bm.TotalSpent()
	if spent < 0.999 || spent > 1.001 {
		t.Errorf("expected ~1.0 spent, got %f", spent)
	}
}

func TestBudgetManager_UnlimitedBudget(t *testing.T) {
	config := BudgetConfig{
		MaxBudgetUSD:  0, // No limit
		WarnThreshold: 0.8,
		TrackByAgent:  true,
	}
	bm := NewBudgetManager(config)

	// Should never return error
	for i := 0; i < 100; i++ {
		err := bm.RecordUsage("agent1", 10000, 5000, 10.0)
		if err != nil {
			t.Errorf("unexpected error with unlimited budget: %v", err)
		}
	}

	if remaining := bm.RemainingBudget(); remaining != -1 {
		t.Errorf("expected -1 for unlimited, got %f", remaining)
	}
}

// budgetMockSubAgent implements SubAgent for budget testing.
type budgetMockSubAgent struct {
	name         string
	inputPrice   float64
	outputPrice  float64
	queryFunc    func(ctx context.Context, prompt string) (dspyrlm.QueryResponse, error)
}

func (m *budgetMockSubAgent) Query(ctx context.Context, prompt string) (dspyrlm.QueryResponse, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, prompt)
	}
	return dspyrlm.QueryResponse{
		Response:         "mock response",
		PromptTokens:     100,
		CompletionTokens: 50,
	}, nil
}

func (m *budgetMockSubAgent) QueryBatched(ctx context.Context, prompts []string) ([]dspyrlm.QueryResponse, error) {
	results := make([]dspyrlm.QueryResponse, len(prompts))
	for i := range prompts {
		results[i] = dspyrlm.QueryResponse{
			Response:         "mock response",
			PromptTokens:     100,
			CompletionTokens: 50,
		}
	}
	return results, nil
}

func (m *budgetMockSubAgent) Name() string { return m.name }

func (m *budgetMockSubAgent) Capabilities() []Capability {
	return []Capability{CapabilityCodeAnalysis}
}

func (m *budgetMockSubAgent) TokenPricing() (float64, float64) {
	return m.inputPrice, m.outputPrice
}

func (m *budgetMockSubAgent) Stats() AgentStats {
	return AgentStats{}
}

func TestBudgetAwareSubClient(t *testing.T) {
	config := BudgetConfig{
		MaxBudgetUSD:  0.01, // Very small budget
		WarnThreshold: 0.8,
		TrackByAgent:  true,
	}
	bm := NewBudgetManager(config)

	mock := &budgetMockSubAgent{
		name:        "test-agent",
		inputPrice:  0.003,
		outputPrice: 0.015,
	}

	client := NewBudgetAwareSubClient(mock, bm)

	// First query should succeed
	resp, err := client.Query(context.Background(), "test prompt")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if resp.Response != "mock response" {
		t.Errorf("unexpected response: %s", resp.Response)
	}

	// Budget should be tracked
	status := client.BudgetStatus()
	if status.TotalSpent == 0 {
		t.Error("expected non-zero spent")
	}

	// Multiple queries should eventually hit limit
	for i := 0; i < 100; i++ {
		_, err = client.Query(context.Background(), "test prompt")
		if err != nil {
			// Expected budget exceeded
			budgetErr, ok := err.(*BudgetError)
			if ok && budgetErr.Type == BudgetExceeded {
				return // Test passed
			}
		}
	}
	t.Error("expected budget to be exceeded")
}

func TestEstimateCost(t *testing.T) {
	config := DefaultBudgetConfig()
	bm := NewBudgetManager(config)

	// Estimate cost for 1000 prompt tokens and 500 completion tokens
	// with Sonnet pricing ($0.003/1K input, $0.015/1K output)
	cost := bm.EstimateCost(1000, 500, 0.003, 0.015)
	// Expected: (1000 * 0.003 / 1000) + (500 * 0.015 / 1000) = 0.003 + 0.0075 = 0.0105
	expected := 0.0105
	// Use delta comparison for floating point
	delta := cost - expected
	if delta < -0.0001 || delta > 0.0001 {
		t.Errorf("expected cost ~%f, got %f", expected, cost)
	}
}

func TestBudgetManager_SetBudget(t *testing.T) {
	config := BudgetConfig{
		MaxBudgetUSD:  1.0,
		WarnThreshold: 0.8,
	}
	bm := NewBudgetManager(config)

	// Spend 0.85 (triggers warning)
	_ = bm.RecordUsage("agent1", 1000, 500, 0.85)

	status := bm.Status()
	if !status.AtWarning {
		t.Error("expected at warning")
	}

	// Increase budget - warning should reset
	bm.SetBudget(2.0)
	status = bm.Status()
	if status.AtWarning {
		t.Error("warning should be reset after budget increase")
	}
	if status.RemainingBudget != 1.15 {
		t.Errorf("expected 1.15 remaining, got %f", status.RemainingBudget)
	}
}

func TestBudgetStatus_Timestamps(t *testing.T) {
	config := DefaultBudgetConfig()
	bm := NewBudgetManager(config)

	status1 := bm.Status()
	if status1.StartTime.IsZero() {
		t.Error("start time should not be zero")
	}

	time.Sleep(10 * time.Millisecond)
	_ = bm.RecordUsage("agent1", 100, 50, 0.01)

	status2 := bm.Status()
	if !status2.LastUpdate.After(status1.LastUpdate) {
		t.Error("last update should be after initial")
	}
}
