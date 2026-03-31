package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
)

const (
	reviewArtifactsEnvVar    = "MAESTRO_REVIEW_ARTIFACTS"
	reviewSkillStoreEnvVar   = "MAESTRO_REVIEW_SKILL_STORE"
	reviewSkillDomainEnvVar  = "MAESTRO_REVIEW_SKILL_DOMAIN"
	reviewDefaultSkillDomain = "maestro:review:go"
	reviewPersistedSkillFile = "skills.json"
)

const DefaultReviewSkillDomain = reviewDefaultSkillDomain

const defaultReviewOptimizationSeedSkillPack = `Focus on concrete Go code review findings in the changed code.

Prioritize:
- correctness bugs, nil misuse, bounds mistakes, data races, deadlocks, goroutine leaks
- error handling mistakes, resource leaks, cleanup bugs, and context misuse
- API contract mismatches and behavior regressions introduced by the patch
- performance issues only when they are directly caused by the changed lines

Rules:
- Report only specific, line-grounded findings that the author can act on.
- Prefer changed-line issues over broad style advice.
- Return [] when there is no concrete issue worth raising.`

type reviewArtifactsEnvelope struct {
	BestArtifacts optimize.AgentArtifacts `json:"best_artifacts"`
}

func defaultReviewArtifacts() optimize.AgentArtifacts {
	return optimize.AgentArtifacts{
		Text: map[optimize.ArtifactKey]string{
			optimize.ArtifactSkillPack: "",
		},
		Int:  map[string]int{},
		Bool: map[string]bool{},
	}
}

func EnsureReviewOptimizationSeedArtifacts(artifacts optimize.AgentArtifacts) optimize.AgentArtifacts {
	seeded := mergeReviewArtifactsWithDefaults(artifacts.Clone())
	if strings.TrimSpace(seeded.Text[optimize.ArtifactSkillPack]) == "" {
		seeded.Text[optimize.ArtifactSkillPack] = defaultReviewOptimizationSeedSkillPack
	}
	return seeded
}

func mergeReviewArtifactsWithDefaults(artifacts optimize.AgentArtifacts) optimize.AgentArtifacts {
	merged := defaultReviewArtifacts()
	for key, value := range artifacts.Text {
		if strings.TrimSpace(value) == "" {
			continue
		}
		merged.Text[key] = value
	}
	for key, value := range artifacts.Int {
		if value <= 0 {
			continue
		}
		merged.Int[key] = value
	}
	for key, value := range artifacts.Bool {
		merged.Bool[key] = value
	}
	return merged
}

func reviewArtifactsEmpty(artifacts optimize.AgentArtifacts) bool {
	return len(artifacts.Text) == 0 && len(artifacts.Int) == 0 && len(artifacts.Bool) == 0
}

func resolveReviewArtifactsPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(reviewArtifactsEnvVar))
	}
	if path == "" {
		return "", nil
	}
	return expandReviewPath(path)
}

func decodeReviewArtifacts(data []byte) (optimize.AgentArtifacts, error) {
	var direct optimize.AgentArtifacts
	if err := json.Unmarshal(data, &direct); err == nil && !reviewArtifactsEmpty(direct) {
		return direct, nil
	}

	var envelope reviewArtifactsEnvelope
	if err := json.Unmarshal(data, &envelope); err == nil && !reviewArtifactsEmpty(envelope.BestArtifacts) {
		return envelope.BestArtifacts, nil
	}

	return optimize.AgentArtifacts{}, fmt.Errorf("unsupported review artifact payload")
}

func LoadConfiguredReviewArtifacts(path string) (optimize.AgentArtifacts, error) {
	resolvedPath, err := resolveReviewArtifactsPath(path)
	if err != nil {
		return optimize.AgentArtifacts{}, err
	}
	if resolvedPath == "" {
		return defaultReviewArtifacts(), nil
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return optimize.AgentArtifacts{}, fmt.Errorf("read review artifacts %q: %w", resolvedPath, err)
	}

	artifacts, err := decodeReviewArtifacts(data)
	if err != nil {
		return optimize.AgentArtifacts{}, fmt.Errorf("decode review artifacts %q: %w", resolvedPath, err)
	}
	return mergeReviewArtifactsWithDefaults(artifacts), nil
}

func ResolveReviewSkillDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = strings.TrimSpace(os.Getenv(reviewSkillDomainEnvVar))
	}
	if domain == "" {
		return DefaultReviewSkillDomain
	}
	return domain
}

func ResolveReviewSkillStorePath(path, memoryPath string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(reviewSkillStoreEnvVar))
	}
	if path == "" {
		stateDir, err := resolveReviewStateDir(memoryPath)
		if err != nil {
			return "", err
		}
		path = filepath.Join(stateDir, reviewPersistedSkillFile)
	}
	return expandReviewPath(path)
}

func expandReviewPath(path string) (string, error) {
	path = strings.TrimSpace(os.ExpandEnv(path))
	if path == "" {
		return "", nil
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Clean(path), nil
}

func resolveReviewStateDir(memoryPath string) (string, error) {
	memoryPath = strings.TrimSpace(memoryPath)
	if memoryPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for review state: %w", err)
		}
		return filepath.Join(homeDir, ".maestro"), nil
	}
	expanded, err := expandReviewPath(memoryPath)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(memoryPath, string(os.PathSeparator)) || filepath.Ext(expanded) == "" {
		return expanded, nil
	}
	return filepath.Dir(expanded), nil
}

func loadRuntimeReviewArtifacts(ctx context.Context, memoryPath string, cfg *AgentConfig) (optimize.AgentArtifacts, *skills.Skill, string, string, error) {
	if cfg == nil {
		cfg = defaultAgentConfig()
	}
	artifacts, err := LoadConfiguredReviewArtifacts(cfg.ReviewArtifactsPath)
	if err != nil {
		return optimize.AgentArtifacts{}, nil, "", "", err
	}

	storePath, err := ResolveReviewSkillStorePath(cfg.ReviewSkillStorePath, memoryPath)
	if err != nil {
		return optimize.AgentArtifacts{}, nil, "", "", err
	}
	domain := ResolveReviewSkillDomain(cfg.ReviewSkillDomain)

	store := skills.NewFileStore(storePath)
	bestSkill, err := store.Best(ctx, domain)
	if err != nil {
		return optimize.AgentArtifacts{}, nil, "", "", fmt.Errorf("load review skill domain %q: %w", domain, err)
	}

	return artifacts, bestSkill, storePath, domain, nil
}

func materializeReviewInstructionOverlay(artifacts optimize.AgentArtifacts, skill *skills.Skill) string {
	if skill != nil && strings.TrimSpace(skill.Content) != "" {
		return strings.TrimSpace(skill.Content)
	}
	return strings.TrimSpace(artifacts.Text[optimize.ArtifactSkillPack])
}
