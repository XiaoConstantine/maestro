package orchestration

import (
	"context"

	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
)

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
