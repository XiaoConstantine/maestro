package orchestration

import (
	"context"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
)

func (s *MaestroService) recordExecutionTraceUsage(ctx context.Context, agent string, trace *agents.ExecutionTrace) {
	if s == nil || s.budgetManager == nil || trace == nil {
		return
	}
	delta := maestrobudget.UsageDeltaFromExecutionTrace(trace)
	if delta.Empty() {
		return
	}
	if err := s.budgetManager.RecordUsageDelta(agent, delta); err != nil && s.logger != nil {
		s.logger.Warn(ctx, "Failed to record execution-trace budget usage for %s: %v", agent, err)
	}
}

func (s *MaestroService) recordRLMTraceUsage(ctx context.Context, agent string, trace *modrlm.RLMTrace) {
	if s == nil || s.budgetManager == nil || trace == nil {
		return
	}
	delta := maestrobudget.UsageDeltaFromRLMTrace(trace)
	if delta.Empty() {
		return
	}
	if err := s.budgetManager.RecordUsageDelta(agent, delta); err != nil && s.logger != nil {
		s.logger.Warn(ctx, "Failed to record RLM budget usage for %s: %v", agent, err)
	}
}
