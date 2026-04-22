package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
)

func TestLoadConfiguredQAArtifacts_Defaults(t *testing.T) {
	t.Setenv(qaArtifactsEnvVar, "")

	artifacts, err := loadConfiguredQAArtifacts("")
	if err != nil {
		t.Fatalf("loadConfiguredQAArtifacts() error = %v", err)
	}
	if artifacts.Text[optimize.ArtifactSkillPack] != qaNativeSystemPrompt {
		t.Fatalf("skill pack = %q, want default prompt", artifacts.Text[optimize.ArtifactSkillPack])
	}
	if artifacts.Int["max_turns"] != qaNativeDefaultMaxTurns {
		t.Fatalf("max_turns = %d, want %d", artifacts.Int["max_turns"], qaNativeDefaultMaxTurns)
	}
}

func TestLoadConfiguredQAArtifacts_DirectFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qa_artifacts.json")
	payload := optimize.AgentArtifacts{
		Text: map[optimize.ArtifactKey]string{
			optimize.ArtifactSkillPack:  "Use highly targeted repository reads.",
			optimize.ArtifactToolPolicy: "Prefer semantic_search before broad content scans.",
		},
		Int: map[string]int{
			"max_turns": 7,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	artifacts, err := loadConfiguredQAArtifacts(path)
	if err != nil {
		t.Fatalf("loadConfiguredQAArtifacts() error = %v", err)
	}
	if artifacts.Text[optimize.ArtifactSkillPack] != payload.Text[optimize.ArtifactSkillPack] {
		t.Fatalf("skill pack = %q, want %q", artifacts.Text[optimize.ArtifactSkillPack], payload.Text[optimize.ArtifactSkillPack])
	}
	if artifacts.Text[optimize.ArtifactToolPolicy] != payload.Text[optimize.ArtifactToolPolicy] {
		t.Fatalf("tool policy = %q, want %q", artifacts.Text[optimize.ArtifactToolPolicy], payload.Text[optimize.ArtifactToolPolicy])
	}
	if artifacts.Int["max_turns"] != 7 {
		t.Fatalf("max_turns = %d, want 7", artifacts.Int["max_turns"])
	}
}

func TestLoadConfiguredQAArtifacts_CheckpointEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qa_checkpoint.json")
	payload := struct {
		BestArtifacts optimize.AgentArtifacts `json:"best_artifacts"`
	}{
		BestArtifacts: optimize.AgentArtifacts{
			Text: map[optimize.ArtifactKey]string{
				optimize.ArtifactSkillPack: "Summarize with exact package evidence.",
			},
			Int: map[string]int{
				"max_turns": 5,
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	artifacts, err := loadConfiguredQAArtifacts(path)
	if err != nil {
		t.Fatalf("loadConfiguredQAArtifacts() error = %v", err)
	}
	if artifacts.Text[optimize.ArtifactSkillPack] != payload.BestArtifacts.Text[optimize.ArtifactSkillPack] {
		t.Fatalf("skill pack = %q, want %q", artifacts.Text[optimize.ArtifactSkillPack], payload.BestArtifacts.Text[optimize.ArtifactSkillPack])
	}
	if artifacts.Int["max_turns"] != 5 {
		t.Fatalf("max_turns = %d, want 5", artifacts.Int["max_turns"])
	}
}

func TestLoadConfiguredQAArtifacts_OptimizedProgram(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qa_program.json")
	program := &optimize.OptimizedAgentProgram{
		Schema:    "dspy-go.optimized-agent-program",
		Version:   1,
		AgentType: qaBenchmarkOptimizationAgentType,
		TargetOrder: []string{
			"root.skill_pack",
			"root.tool_policy",
			"root.max_turns",
		},
		Text: map[string]string{
			"root.skill_pack":  "Prefer concise package-level answers with exact files.",
			"root.tool_policy": "Read README before broad searches.",
		},
		Int: map[string]int{
			"root.max_turns": 6,
		},
	}
	if err := optimize.WriteOptimizedAgentProgram(path, program); err != nil {
		t.Fatalf("WriteOptimizedAgentProgram() error = %v", err)
	}

	artifacts, err := loadConfiguredQAArtifacts(path)
	if err != nil {
		t.Fatalf("loadConfiguredQAArtifacts() error = %v", err)
	}

	wantPrompt := composeQABenchmarkSystemPrompt(qaNativeSystemPrompt, program.Text["root.skill_pack"])
	if artifacts.Text[optimize.ArtifactSkillPack] != wantPrompt {
		t.Fatalf("skill pack = %q, want %q", artifacts.Text[optimize.ArtifactSkillPack], wantPrompt)
	}
	if artifacts.Text[optimize.ArtifactToolPolicy] != program.Text["root.tool_policy"] {
		t.Fatalf("tool policy = %q, want %q", artifacts.Text[optimize.ArtifactToolPolicy], program.Text["root.tool_policy"])
	}
	if artifacts.Int["max_turns"] != 6 {
		t.Fatalf("max_turns = %d, want 6", artifacts.Int["max_turns"])
	}
}

func TestResolveQASkillStorePath_DefaultFromMemoryDBPath(t *testing.T) {
	t.Setenv(qaSkillStoreEnvVar, "")

	stateDir := t.TempDir()
	dbPath := filepath.Join(stateDir, "maestro.db")

	resolved, err := resolveQASkillStorePath("", dbPath)
	if err != nil {
		t.Fatalf("resolveQASkillStorePath() error = %v", err)
	}
	want := filepath.Join(stateDir, defaultPersistedSkillStoreFile)
	if resolved != want {
		t.Fatalf("resolved = %q, want %q", resolved, want)
	}
}

func TestResolveQASkillDomain_Defaults(t *testing.T) {
	t.Setenv(qaSkillDomainEnvVar, "")
	if got := resolveQASkillDomain(""); got != qaDefaultSkillDomain {
		t.Fatalf("resolveQASkillDomain() = %q, want %q", got, qaDefaultSkillDomain)
	}
}

func TestBuildNativeQAConfig_SetsPersistedSkillFields(t *testing.T) {
	store := skills.NewMemoryStore()
	cfg := buildNativeQAConfig(defaultQAArtifacts(), nil, "session-1", nil, store, "maestro:qa")
	if cfg.SkillStore != store {
		t.Fatalf("SkillStore not propagated")
	}
	if cfg.SkillDomain != "maestro:qa" {
		t.Fatalf("SkillDomain = %q, want maestro:qa", cfg.SkillDomain)
	}
}
