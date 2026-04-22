package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
)

func TestLoadConfiguredReviewArtifacts_DefaultsWhenEmpty(t *testing.T) {
	artifacts, err := LoadConfiguredReviewArtifacts("")
	if err != nil {
		t.Fatalf("LoadConfiguredReviewArtifacts() error = %v", err)
	}
	if got := artifacts.Text[optimize.ArtifactSkillPack]; got != "" {
		t.Fatalf("skill pack = %q, want empty overlay", got)
	}
}

func TestLoadConfiguredReviewArtifacts_Envelope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifacts.json")
	if err := os.WriteFile(path, []byte(`{"best_artifacts":{"text":{"skill_pack":"Prefer changed-hunk grounded findings."}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	artifacts, err := LoadConfiguredReviewArtifacts(path)
	if err != nil {
		t.Fatalf("LoadConfiguredReviewArtifacts() error = %v", err)
	}
	if got := artifacts.Text[optimize.ArtifactSkillPack]; got != "Prefer changed-hunk grounded findings." {
		t.Fatalf("skill pack = %q, want persisted overlay", got)
	}
}

func TestLoadConfiguredReviewArtifacts_OptimizedProgram(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review_program.json")
	program := &optimize.OptimizedAgentProgram{
		Schema:    "dspy-go.optimized-agent-program",
		Version:   1,
		AgentType: reviewBenchmarkOptimizationAgentType,
		TargetOrder: []string{
			"root.skill_pack",
			"root.few_shot_demos",
		},
		Text: map[string]string{
			"root.skill_pack":     "Prefer changed-hunk grounded findings.",
			"root.few_shot_demos": "1. Flag nil dereferences.\n2. Flag context leaks.",
		},
	}
	if err := optimize.WriteOptimizedAgentProgram(path, program); err != nil {
		t.Fatalf("WriteOptimizedAgentProgram() error = %v", err)
	}

	artifacts, err := LoadConfiguredReviewArtifacts(path)
	if err != nil {
		t.Fatalf("LoadConfiguredReviewArtifacts() error = %v", err)
	}
	if got := artifacts.Text[optimize.ArtifactSkillPack]; got != program.Text["root.skill_pack"] {
		t.Fatalf("skill pack = %q, want %q", got, program.Text["root.skill_pack"])
	}
	if got := artifacts.Text[ArtifactFewShotDemos]; got != program.Text["root.few_shot_demos"] {
		t.Fatalf("few_shot_demos = %q, want %q", got, program.Text["root.few_shot_demos"])
	}
}

func TestMaterializeReviewInstructionOverlay_PrefersPersistedSkill(t *testing.T) {
	overlay := materializeReviewInstructionOverlay(
		optimize.AgentArtifacts{Text: map[optimize.ArtifactKey]string{optimize.ArtifactSkillPack: "Base overlay."}},
		&skills.Skill{Content: "Published overlay."},
	)
	want := "Published overlay."
	if overlay != want {
		t.Fatalf("overlay = %q, want %q", overlay, want)
	}
}

func TestLoadRuntimeReviewArtifacts_ResolvesPersistedSkill(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "skills.json")
	store := skills.NewFileStore(storePath)
	if err := store.Save(context.Background(), skills.Skill{
		Name:    "review-gepa",
		Domain:  DefaultReviewSkillDomain,
		Content: "Published review overlay.",
		Version: 1,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	artifacts, skill, resolvedStore, domain, err := loadRuntimeReviewArtifacts(context.Background(), dir, &AgentConfig{
		ReviewSkillStorePath: storePath,
	})
	if err != nil {
		t.Fatalf("loadRuntimeReviewArtifacts() error = %v", err)
	}
	if resolvedStore != storePath {
		t.Fatalf("resolvedStore = %q, want %q", resolvedStore, storePath)
	}
	if domain != DefaultReviewSkillDomain {
		t.Fatalf("domain = %q, want %q", domain, DefaultReviewSkillDomain)
	}
	if skill == nil || skill.Content != "Published review overlay." {
		t.Fatalf("skill = %#v, want persisted review overlay", skill)
	}
	if got := artifacts.Text[optimize.ArtifactSkillPack]; got != "" {
		t.Fatalf("base skill pack = %q, want empty default", got)
	}
}

func TestEnsureReviewOptimizationSeedArtifacts_DefaultsSkillPack(t *testing.T) {
	artifacts := EnsureReviewOptimizationSeedArtifacts(optimize.AgentArtifacts{})
	if got := artifacts.Text[optimize.ArtifactSkillPack]; got != defaultReviewOptimizationSeedSkillPack {
		t.Fatalf("skill pack = %q, want default optimization seed", got)
	}
}

func TestEnsureReviewOptimizationSeedArtifacts_PreservesExplicitSkillPack(t *testing.T) {
	artifacts := EnsureReviewOptimizationSeedArtifacts(optimize.AgentArtifacts{
		Text: map[optimize.ArtifactKey]string{
			optimize.ArtifactSkillPack: "Use the published review playbook.",
		},
	})
	if got := artifacts.Text[optimize.ArtifactSkillPack]; got != "Use the published review playbook." {
		t.Fatalf("skill pack = %q, want explicit seed preserved", got)
	}
}
