package orchestration

import (
	"context"
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
	"github.com/XiaoConstantine/dspy-go/pkg/core"
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

type qaRuntimeArtifactsAgent struct {
	artifacts optimize.AgentArtifacts
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
		artifacts, err = decodeQAArtifactsProgram(resolvedPath)
		if err != nil {
			return optimize.AgentArtifacts{}, fmt.Errorf("decode QA artifacts %q: %w", resolvedPath, err)
		}
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
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &header); err == nil && strings.TrimSpace(header.Schema) == "dspy-go.optimized-agent-program" {
		return optimize.AgentArtifacts{}, fmt.Errorf("optimized agent program payload")
	}

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

func decodeQAArtifactsProgram(path string) (optimize.AgentArtifacts, error) {
	program, err := optimize.ReadOptimizedAgentProgram(path)
	if err != nil {
		return optimize.AgentArtifacts{}, err
	}

	switch strings.TrimSpace(program.AgentType) {
	case "", "native":
		agent := &qaRuntimeArtifactsAgent{artifacts: defaultQAArtifacts()}
		if err := optimize.ApplyOptimizedAgentProgram(agent, program); err != nil {
			return optimize.AgentArtifacts{}, err
		}
		return agent.GetArtifacts(), nil
	case qaBenchmarkOptimizationAgentType:
		agent := NewQABenchmarkAgent(nil, nil, defaultQABenchmarkArtifacts())
		if err := optimize.ApplyOptimizedAgentProgram(agent, program); err != nil {
			return optimize.AgentArtifacts{}, err
		}
		return runtimeQAArtifactsFromBenchmark(agent.GetArtifacts()), nil
	default:
		return optimize.AgentArtifacts{}, fmt.Errorf("unsupported QA optimized program agent_type %q", program.AgentType)
	}
}

func runtimeQAArtifactsFromBenchmark(artifacts optimize.AgentArtifacts) optimize.AgentArtifacts {
	runtimeArtifacts := defaultQAArtifacts()

	if overlay := strings.TrimSpace(artifacts.Text[optimize.ArtifactSkillPack]); overlay != "" {
		runtimeArtifacts.Text[optimize.ArtifactSkillPack] = composeQABenchmarkSystemPrompt(qaNativeSystemPrompt, overlay)
	}
	if policy := strings.TrimSpace(artifacts.Text[optimize.ArtifactToolPolicy]); policy != "" {
		runtimeArtifacts.Text[optimize.ArtifactToolPolicy] = policy
	}
	if maxTurns, ok := artifacts.Int["max_turns"]; ok && maxTurns > 0 {
		runtimeArtifacts.Int["max_turns"] = maxTurns
	}

	return runtimeArtifacts
}

func qaRuntimeOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	return []optimize.OptimizationTargetDescriptor{
		{
			ID:          "root.system",
			Kind:        optimize.OptimizationTargetText,
			Description: "Primary QA system prompt and persisted skill guidance.",
			ArtifactKey: optimize.ArtifactSkillPack,
		},
		{
			ID:          "root.tool_policy",
			Kind:        optimize.OptimizationTargetText,
			Description: "QA tool-use policy and guardrails.",
			ArtifactKey: optimize.ArtifactToolPolicy,
		},
		{
			ID:          "root.max_turns",
			Kind:        optimize.OptimizationTargetInt,
			Description: "Maximum repository-tool turns for one answer.",
			IntKey:      "max_turns",
		},
	}
}

func (a *qaRuntimeArtifactsAgent) Execute(context.Context, map[string]interface{}) (map[string]interface{}, error) {
	return nil, fmt.Errorf("qa runtime artifacts agent does not execute tasks")
}

func (a *qaRuntimeArtifactsAgent) GetCapabilities() []core.Tool { return nil }

func (a *qaRuntimeArtifactsAgent) GetMemory() agents.Memory { return nil }

func (a *qaRuntimeArtifactsAgent) GetArtifacts() optimize.AgentArtifacts {
	if a == nil {
		return optimize.AgentArtifacts{}
	}
	return a.artifacts.Clone()
}

func (a *qaRuntimeArtifactsAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	if a == nil {
		return fmt.Errorf("qa runtime artifacts agent is nil")
	}
	a.artifacts = mergeQAArtifactsWithDefaults(artifacts)
	return nil
}

func (a *qaRuntimeArtifactsAgent) Clone() (optimize.OptimizableAgent, error) {
	if a == nil {
		return nil, fmt.Errorf("qa runtime artifacts agent is nil")
	}
	return &qaRuntimeArtifactsAgent{artifacts: a.GetArtifacts()}, nil
}

func (a *qaRuntimeArtifactsAgent) OptimizationAgentType() string { return "native" }

func (a *qaRuntimeArtifactsAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	return qaRuntimeOptimizationTargets()
}

func (a *qaRuntimeArtifactsAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	if a == nil {
		return fmt.Errorf("qa runtime artifacts agent is nil")
	}
	if update == nil {
		return fmt.Errorf("qa runtime artifact update function is nil")
	}
	next, err := update(a.artifacts.Clone())
	if err != nil {
		return err
	}
	a.artifacts = mergeQAArtifactsWithDefaults(next)
	return nil
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
