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

RLM evidence-gathering rule:
- When this prompt is running inside the RLM REPL, the manifest is loaded as the ` + "`context`" + ` variable and ` + "`context_info`" + ` is only a short preview.
- Do not answer from ` + "`context_info`" + ` or top-level guesses alone.
- Before the final answer, run at least one evidence-gathering action that inspects the loaded manifest, such as:
  - ` + "`fmt.Println(GetContext(1, LineCount()))`" + ` for compact manifests
  - ` + "`hits := FindRelevant(\"<question terms>\", 8); fmt.Println(strings.Join(hits, \"\\n---\\n\"))`" + `
  - ` + "`answer := QueryWith(strings.Join(hits, \"\\n---\\n\"), \"answer the question as strict JSON\"); FINAL(answer)`" + `
- If you assign findings to variables, print them or store them in ` + "`answer`" + `, ` + "`summary`" + `, ` + "`findings`" + `, or ` + "`result`" + ` so the next step can see them.
- If the full manifest is already inline in the prompt, answer directly from that manifest text.
- If a FOCUSED MANIFEST EVIDENCE section is present, treat it as already-inspected manifest evidence; answer from it directly unless it is clearly insufficient.

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
- Include concrete repo-relative paths from the manifest in the answer when they are relevant.
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
	base := buildRLMOverviewQueryWithFocusedEvidence(question, "")
	overlay = strings.TrimSpace(overlay)
	if overlay == "" {
		return base
	}
	return base + "\n\nOPTIMIZATION GUIDANCE:\n" + overlay
}

func buildRLMOverviewQueryWithFocusedEvidence(question, evidence string) string {
	base := buildRLMOverviewQuery(question)
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return base
	}
	return base + "\n\nFOCUSED MANIFEST EVIDENCE:\n" + evidence + `

Focused evidence instructions:
- The focused evidence was built from the repository manifest and a shallow repo path/symbol index before this RLM loop.
- It satisfies the required evidence-inspection step for overview answers.
- Prefer Action: final with strict JSON in the Answer field when the focused evidence answers the question.
- Mention the relevant candidate paths and symbols exactly as shown.
- Do not dump broad GetContext ranges unless the focused evidence is missing necessary facts.`
}

func buildRLMOverviewQueryWithFocusedEvidenceAndOverlay(question, evidence, overlay string) string {
	base := buildRLMOverviewQueryWithFocusedEvidence(question, evidence)
	overlay = strings.TrimSpace(overlay)
	if overlay == "" {
		return base
	}
	return base + "\n\nOPTIMIZATION GUIDANCE:\n" + overlay
}

func loadBestRLMOverviewSkill(ctx context.Context, store skills.Store, domain string) (*skills.Skill, error) {
	return loadBestSkill(ctx, store, domain)
}
