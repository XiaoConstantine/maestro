package orchestration

import (
	"context"
	"fmt"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
)

const (
	rlmOverviewSkillStoreEnvVar   = "MAESTRO_RLM_OVERVIEW_SKILL_STORE"
	rlmOverviewSkillDomainEnvVar  = "MAESTRO_RLM_OVERVIEW_SKILL_DOMAIN"
	rlmOverviewDefaultSkillDomain = "maestro:ask:rlm-overview"
)

const DefaultRLMOverviewSkillDomain = rlmOverviewDefaultSkillDomain

const rlmOverviewBaseQueryTemplate = `Answer the user's repository overview question using only the provided manifest.

Question: %s

Return strict JSON with this schema and no markdown fences:
{
  "answer": "direct answer to the overview question",
  "needs_verification": [
    {
      "package": "repo-relative package or directory path",
      "reason": "why a scoped code-level verification would improve the answer"
    }
  ]
}

Rules:
- Use the manifest only. Do not invent files or responsibilities that are not present.
- Keep the answer concise but useful.
- Only populate needs_verification when a specific package needs a follow-up code-level check.
- If no follow-up is needed, return an empty array.
- Verification requests must be scoped to a package or directory, not the whole repository.`

func resolveRLMOverviewSkillStorePath(path, memoryPath, fallbackPath string) (string, error) {
	return resolveSkillStorePath(path, rlmOverviewSkillStoreEnvVar, memoryPath, fallbackPath)
}

func resolveRLMOverviewSkillDomain(domain string) string {
	return resolveSkillDomain(domain, rlmOverviewSkillDomainEnvVar, rlmOverviewDefaultSkillDomain)
}

func buildRLMOverviewQuery(question string) string {
	return fmt.Sprintf(rlmOverviewBaseQueryTemplate, strings.TrimSpace(question))
}

func buildRLMOverviewQueryWithOverlay(question, overlay string) string {
	base := buildRLMOverviewQuery(question)
	overlay = strings.TrimSpace(overlay)
	if overlay == "" {
		return base
	}
	return base + "\n\nOPTIMIZATION GUIDANCE:\n" + overlay
}

func loadBestRLMOverviewSkill(ctx context.Context, store skills.Store, domain string) (*skills.Skill, error) {
	return loadBestSkill(ctx, store, domain)
}
