package orchestration

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
)

func TestResolveRLMOverviewSkillStorePath_UsesFallback(t *testing.T) {
	t.Setenv(rlmOverviewSkillStoreEnvVar, "")

	fallback := filepath.Join(t.TempDir(), "skills.json")
	resolved, err := resolveRLMOverviewSkillStorePath("", filepath.Join(t.TempDir(), "maestro.db"), fallback)
	if err != nil {
		t.Fatalf("resolveRLMOverviewSkillStorePath() error = %v", err)
	}
	if resolved != fallback {
		t.Fatalf("resolved = %q, want %q", resolved, fallback)
	}
}

func TestResolveRLMOverviewSkillDomain_Defaults(t *testing.T) {
	t.Setenv(rlmOverviewSkillDomainEnvVar, "")
	if got := resolveRLMOverviewSkillDomain(""); got != rlmOverviewDefaultSkillDomain {
		t.Fatalf("resolveRLMOverviewSkillDomain() = %q, want %q", got, rlmOverviewDefaultSkillDomain)
	}
}

func TestBuildRLMOverviewQueryWithOverlay(t *testing.T) {
	question := "What are the main packages?"
	overlay := "Prefer exact package-to-responsibility mappings."

	query := buildRLMOverviewQueryWithOverlay(question, overlay)
	base := buildRLMOverviewQuery(question)
	if strings.Count(query, base) != 1 {
		t.Fatalf("query contains base prompt %d times, want 1", strings.Count(query, base))
	}
	if !strings.Contains(query, "OPTIMIZATION GUIDANCE:") {
		t.Fatalf("query missing optimization guidance marker: %q", query)
	}
	if !strings.Contains(query, overlay) {
		t.Fatalf("query missing overlay text: %q", query)
	}
}

func TestBuildRLMOverviewQueryWithEmptyOverlay_UsesBasePrompt(t *testing.T) {
	question := "What are the main packages?"

	query := buildRLMOverviewQueryWithOverlay(question, "")
	base := buildRLMOverviewQuery(question)
	if query != base {
		t.Fatalf("query = %q, want base prompt %q", query, base)
	}
}

func TestLoadBestRLMOverviewSkill_BestVersion(t *testing.T) {
	store := skills.NewMemoryStore()
	if err := store.Save(context.Background(), skills.Skill{
		Name:    "rlm-overview-v1",
		Domain:  rlmOverviewDefaultSkillDomain,
		Content: "Prefer package summaries.",
		Version: 1,
	}); err != nil {
		t.Fatalf("save v1 skill: %v", err)
	}
	if err := store.Save(context.Background(), skills.Skill{
		Name:    "rlm-overview-v2",
		Domain:  rlmOverviewDefaultSkillDomain,
		Content: "Prefer exact package summaries with verification hooks.",
		Version: 2,
	}); err != nil {
		t.Fatalf("save v2 skill: %v", err)
	}

	skill, err := loadBestRLMOverviewSkill(context.Background(), store, rlmOverviewDefaultSkillDomain)
	if err != nil {
		t.Fatalf("loadBestRLMOverviewSkill() error = %v", err)
	}
	if skill == nil {
		t.Fatalf("loadBestRLMOverviewSkill() = nil, want best skill")
	}
	if skill.Version != 2 {
		t.Fatalf("skill version = %d, want 2", skill.Version)
	}
}
