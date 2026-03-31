package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/native"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
)

const (
	qaArtifactsEnvVar                    = "MAESTRO_QA_ARTIFACTS"
	qaSkillStoreEnvVar                   = "MAESTRO_QA_SKILL_STORE"
	qaSkillDomainEnvVar                  = "MAESTRO_QA_SKILL_DOMAIN"
	qaDefaultSkillDomain                 = "maestro:qa"
	qaNativeDefaultMaxTurns              = 12
	qaNativeDefaultMaxTokens             = 2048
	qaNativeDefaultTemperature           = 0.1
	qaNativeDefaultSessionRecallLimit    = 4
	qaNativeDefaultSessionRecallMaxChars = 1800
	qaNativeDefaultNoCallResponses       = 4
)

const DefaultQASkillDomain = qaDefaultSkillDomain

type qaArtifactsEnvelope struct {
	BestArtifacts optimize.AgentArtifacts `json:"best_artifacts"`
}

func defaultQAArtifacts() optimize.AgentArtifacts {
	return optimize.AgentArtifacts{
		Text: map[optimize.ArtifactKey]string{
			optimize.ArtifactSkillPack: qaNativeSystemPrompt,
		},
		Int: map[string]int{
			"max_turns": qaNativeDefaultMaxTurns,
		},
		Bool: map[string]bool{},
	}
}

func loadConfiguredQAArtifacts(path string) (optimize.AgentArtifacts, error) {
	resolvedPath, err := resolveQAArtifactsPath(path)
	if err != nil {
		return optimize.AgentArtifacts{}, err
	}
	if resolvedPath == "" {
		return defaultQAArtifacts(), nil
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return optimize.AgentArtifacts{}, fmt.Errorf("read QA artifacts %q: %w", resolvedPath, err)
	}

	artifacts, err := decodeQAArtifacts(data)
	if err != nil {
		return optimize.AgentArtifacts{}, fmt.Errorf("decode QA artifacts %q: %w", resolvedPath, err)
	}

	return mergeQAArtifactsWithDefaults(artifacts), nil
}

func LoadConfiguredQAArtifacts(path string) (optimize.AgentArtifacts, error) {
	return loadConfiguredQAArtifacts(path)
}

func resolveQAArtifactsPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(qaArtifactsEnvVar))
	}
	if path == "" {
		return "", nil
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for QA artifacts: %w", err)
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Clean(path), nil
}

func resolveQASkillDomain(domain string) string {
	return resolveSkillDomain(domain, qaSkillDomainEnvVar, qaDefaultSkillDomain)
}

func resolveQASkillStorePath(path, memoryPath string) (string, error) {
	return resolveSkillStorePath(path, qaSkillStoreEnvVar, memoryPath, "")
}

func decodeQAArtifacts(data []byte) (optimize.AgentArtifacts, error) {
	var direct optimize.AgentArtifacts
	if err := json.Unmarshal(data, &direct); err == nil && !qaArtifactsEmpty(direct) {
		return direct, nil
	}

	var envelope qaArtifactsEnvelope
	if err := json.Unmarshal(data, &envelope); err == nil && !qaArtifactsEmpty(envelope.BestArtifacts) {
		return envelope.BestArtifacts, nil
	}

	return optimize.AgentArtifacts{}, fmt.Errorf("unsupported QA artifact payload")
}

func qaArtifactsEmpty(artifacts optimize.AgentArtifacts) bool {
	return len(artifacts.Text) == 0 && len(artifacts.Int) == 0 && len(artifacts.Bool) == 0
}

func mergeQAArtifactsWithDefaults(artifacts optimize.AgentArtifacts) optimize.AgentArtifacts {
	merged := defaultQAArtifacts()

	for key, value := range artifacts.Text {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if merged.Text == nil {
			merged.Text = make(map[optimize.ArtifactKey]string)
		}
		merged.Text[key] = value
	}
	for key, value := range artifacts.Int {
		if value <= 0 {
			continue
		}
		if merged.Int == nil {
			merged.Int = make(map[string]int)
		}
		merged.Int[key] = value
	}
	for key, value := range artifacts.Bool {
		if merged.Bool == nil {
			merged.Bool = make(map[string]bool)
		}
		merged.Bool[key] = value
	}

	return merged
}

func buildNativeQAConfig(artifacts optimize.AgentArtifacts, memory agents.Memory, sessionID string, sessionStore sessionevent.SessionEventStore, skillStore skills.Store, skillDomain string) native.Config {
	cfg := native.Config{
		MaxTurns:                      qaNativeDefaultMaxTurns,
		MaxTokens:                     qaNativeDefaultMaxTokens,
		Temperature:                   qaNativeDefaultTemperature,
		SystemPrompt:                  qaNativeSystemPrompt,
		Memory:                        memory,
		SessionID:                     sessionID,
		SessionEventStore:             sessionStore,
		SessionRecallLimit:            qaNativeDefaultSessionRecallLimit,
		SessionRecallMaxChars:         qaNativeDefaultSessionRecallMaxChars,
		MaxConsecutiveNoCallResponses: qaNativeDefaultNoCallResponses,
		SkillStore:                    skillStore,
		SkillDomain:                   strings.TrimSpace(skillDomain),
	}
	applyQAArtifactsToNativeConfig(&cfg, artifacts)
	return cfg
}

func applyQAArtifactsToNativeConfig(cfg *native.Config, artifacts optimize.AgentArtifacts) {
	if cfg == nil {
		return
	}
	if prompt := strings.TrimSpace(artifacts.Text[optimize.ArtifactSkillPack]); prompt != "" {
		cfg.SystemPrompt = prompt
	}
	if policy, ok := artifacts.Text[optimize.ArtifactToolPolicy]; ok {
		cfg.ToolPolicy = strings.TrimSpace(policy)
	}
	if maxTurns, ok := artifacts.Int["max_turns"]; ok && maxTurns > 0 {
		cfg.MaxTurns = maxTurns
	}
}
