package optimize

import (
	"context"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	dspyoptimize "github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
)

func TestBudgetAccountingEvaluatorRecordsSideInfoTokens(t *testing.T) {
	manager := maestrobudget.NewBudgetManager(maestrobudget.DefaultConfig())
	evaluator := NewBudgetAccountingEvaluator(
		fakeBudgetEvaluator{tokens: map[string]int64{
			"prompt_tokens":           100,
			"completion_tokens":       20,
			"total_tokens":            120,
			"cache_read_input_tokens": 50,
		}},
		manager,
		"ask.rlm_overview",
	)

	if _, err := evaluator.Evaluate(context.Background(), fakeBudgetAgent{}, dspyoptimize.AgentExample{}); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	status := manager.Status()
	if status.TotalTokens != 120 {
		t.Fatalf("TotalTokens = %d, want 120", status.TotalTokens)
	}
	if status.WeightedTotalTokens != 75 {
		t.Fatalf("WeightedTotalTokens = %.1f, want 75", status.WeightedTotalTokens)
	}
	if got := status.ByAgent["ask.rlm_overview"].CacheReadInputTokens; got != 50 {
		t.Fatalf("agent cache read tokens = %d, want 50", got)
	}
}

type fakeBudgetEvaluator struct {
	tokens map[string]int64
}

func (e fakeBudgetEvaluator) Evaluate(context.Context, dspyoptimize.OptimizableAgent, dspyoptimize.AgentExample) (*dspyoptimize.EvalResult, error) {
	return &dspyoptimize.EvalResult{
		Score: 1,
		SideInfo: &dspyoptimize.SideInfo{
			Tokens: e.tokens,
		},
	}, nil
}

type fakeBudgetAgent struct{}

func (a fakeBudgetAgent) Execute(context.Context, map[string]interface{}) (map[string]interface{}, error) {
	return nil, nil
}

func (a fakeBudgetAgent) GetCapabilities() []core.Tool { return nil }

func (a fakeBudgetAgent) GetMemory() agents.Memory { return nil }

func (a fakeBudgetAgent) GetArtifacts() dspyoptimize.AgentArtifacts {
	return dspyoptimize.AgentArtifacts{}
}

func (a fakeBudgetAgent) SetArtifacts(dspyoptimize.AgentArtifacts) error { return nil }

func (a fakeBudgetAgent) Clone() (dspyoptimize.OptimizableAgent, error) { return a, nil }
