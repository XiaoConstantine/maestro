package orchestration

import (
	"context"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
)

func (s *MaestroService) loadRLMOverviewSkill(ctx context.Context) (*skills.Skill, error) {
	return loadBestRLMOverviewSkill(ctx, s.rlmOverviewSkillStore, s.rlmOverviewSkillDomain)
}
