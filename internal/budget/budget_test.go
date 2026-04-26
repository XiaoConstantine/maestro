package budget

import (
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

func TestRecordUsageDeltaWeightsCacheReadTokens(t *testing.T) {
	manager := NewBudgetManager(DefaultConfig())
	err := manager.RecordUsageDelta("rlm", UsageDelta{
		PromptTokens:             1000,
		CompletionTokens:         100,
		CacheReadInputTokens:     800,
		CacheCreationInputTokens: 50,
		CostUSD:                  0.02,
	})
	if err != nil {
		t.Fatalf("RecordUsageDelta() error = %v", err)
	}

	status := manager.Status()
	if status.TotalTokens != 1100 {
		t.Fatalf("TotalTokens = %d, want 1100", status.TotalTokens)
	}
	if status.CacheReadInputTokens != 800 {
		t.Fatalf("CacheReadInputTokens = %d, want 800", status.CacheReadInputTokens)
	}
	if status.WeightedPromptTokens != 280 {
		t.Fatalf("WeightedPromptTokens = %.1f, want 280", status.WeightedPromptTokens)
	}
	if status.WeightedTotalTokens != 380 {
		t.Fatalf("WeightedTotalTokens = %.1f, want 380", status.WeightedTotalTokens)
	}
	if got := status.ByAgent["rlm"].WeightedTotalTokens; got != 380 {
		t.Fatalf("agent WeightedTotalTokens = %.1f, want 380", got)
	}
}

func TestRecordTokenTrackerDeltaAvoidsCumulativeDoubleCount(t *testing.T) {
	manager := NewBudgetManager(DefaultConfig())
	tracker := modrlm.NewTokenTracker()
	tracker.AddRootUsage(100, 50)
	tracker.AddSubCall(modrlm.LLMCall{PromptTokens: 20, CompletionTokens: 10})

	snapshot, err := manager.RecordTokenTrackerDelta("gepa", tracker, TokenTrackerSnapshot{}, CacheTokenUsage{
		CacheReadInputTokens: 60,
	})
	if err != nil {
		t.Fatalf("RecordTokenTrackerDelta(first) error = %v", err)
	}
	if snapshot.PromptTokens != 120 || snapshot.CompletionTokens != 60 || snapshot.TotalTokens != 180 {
		t.Fatalf("snapshot = %+v, want 120/60/180", snapshot)
	}

	tracker.AddRootUsage(10, 5)
	snapshot, err = manager.RecordTokenTrackerDelta("gepa", tracker, snapshot, CacheTokenUsage{})
	if err != nil {
		t.Fatalf("RecordTokenTrackerDelta(second) error = %v", err)
	}
	if snapshot.PromptTokens != 130 || snapshot.CompletionTokens != 65 || snapshot.TotalTokens != 195 {
		t.Fatalf("second snapshot = %+v, want 130/65/195", snapshot)
	}

	status := manager.Status()
	if status.PromptTokens != 130 || status.CompletionTokens != 65 || status.TotalTokens != 195 {
		t.Fatalf("status tokens = %d/%d/%d, want 130/65/195", status.PromptTokens, status.CompletionTokens, status.TotalTokens)
	}
	if status.WeightedTotalTokens != 141 {
		t.Fatalf("WeightedTotalTokens = %.1f, want 141", status.WeightedTotalTokens)
	}
}

func TestUsageDeltaFromExecutionTraceReadsCacheTokens(t *testing.T) {
	trace := &agents.ExecutionTrace{
		TokenUsage: map[string]int64{
			"input_tokens":                1000,
			"output_tokens":               100,
			"cache_read_input_tokens":     800,
			"cache_creation_input_tokens": 25,
			"total_tokens":                1100,
		},
		ContextMetadata: map[string]interface{}{"cost_usd": "0.04"},
	}

	delta := UsageDeltaFromExecutionTrace(trace)
	if delta.PromptTokens != 1000 || delta.CompletionTokens != 100 || delta.TotalTokens != 1100 {
		t.Fatalf("delta tokens = %+v, want prompt=1000 completion=100 total=1100", delta)
	}
	if delta.CacheReadInputTokens != 800 || delta.CacheCreationInputTokens != 25 {
		t.Fatalf("cache tokens = %d/%d, want 800/25", delta.CacheReadInputTokens, delta.CacheCreationInputTokens)
	}
	if delta.CostUSD != 0.04 {
		t.Fatalf("CostUSD = %.2f, want 0.04", delta.CostUSD)
	}
}

func TestUsageDeltaFromRLMTraceMarksCacheWeightUnavailable(t *testing.T) {
	delta := UsageDeltaFromRLMTrace(&modrlm.RLMTrace{
		Usage: core.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 25,
			TotalTokens:      125,
		},
	})
	if !delta.CacheTokenWeightUnavailable {
		t.Fatal("CacheTokenWeightUnavailable = false, want true")
	}

	manager := NewBudgetManager(DefaultConfig())
	if err := manager.RecordUsageDelta("ask.rlm_overview", delta); err != nil {
		t.Fatalf("RecordUsageDelta() error = %v", err)
	}
	status := manager.Status()
	if !status.CacheTokenWeightUnavailable {
		t.Fatal("status CacheTokenWeightUnavailable = false, want true")
	}
	if !status.ByAgent["ask.rlm_overview"].CacheTokenWeightUnavailable {
		t.Fatal("agent CacheTokenWeightUnavailable = false, want true")
	}
}
