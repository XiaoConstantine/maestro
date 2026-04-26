package optimize

import (
	"context"
	"fmt"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	dspyoptimize "github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
)

type BudgetAccountingEvaluator struct {
	Base      dspyoptimize.AgentEvaluator
	Manager   *maestrobudget.BudgetManager
	AgentName string
}

func NewBudgetAccountingEvaluator(base dspyoptimize.AgentEvaluator, manager *maestrobudget.BudgetManager, agentName string) *BudgetAccountingEvaluator {
	return &BudgetAccountingEvaluator{
		Base:      base,
		Manager:   manager,
		AgentName: strings.TrimSpace(agentName),
	}
}

func (e *BudgetAccountingEvaluator) Evaluate(ctx context.Context, agent dspyoptimize.OptimizableAgent, ex dspyoptimize.AgentExample) (*dspyoptimize.EvalResult, error) {
	if e == nil || e.Base == nil {
		return nil, fmt.Errorf("budget accounting evaluator missing base evaluator")
	}
	result, err := e.Base.Evaluate(ctx, agent, ex)
	e.record(agent, result)
	return result, err
}

func (e *BudgetAccountingEvaluator) record(agent dspyoptimize.OptimizableAgent, result *dspyoptimize.EvalResult) {
	if e == nil || e.Manager == nil {
		return
	}
	name := e.AgentName
	if name == "" {
		name = "rlm.optimization"
	}

	delta := maestrobudget.UsageDelta{}
	if result != nil && result.SideInfo != nil && len(result.SideInfo.Tokens) > 0 {
		delta = maestrobudget.UsageDeltaFromTokenMap(result.SideInfo.Tokens, result.SideInfo.Diagnostics)
		if delta.CostUSD == 0 {
			delta.CostUSD = result.SideInfo.Cost
		}
	} else if traceProvider, ok := agent.(interface{ LastExecutionTrace() *agents.ExecutionTrace }); ok {
		delta = maestrobudget.UsageDeltaFromExecutionTrace(traceProvider.LastExecutionTrace())
	}
	if delta.Empty() {
		return
	}
	_ = e.Manager.RecordUsageDelta(name, delta)
}
