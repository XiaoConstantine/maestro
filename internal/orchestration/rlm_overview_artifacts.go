package orchestration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

const (
	RLMOverviewOptimizedProgramArtifactVersion = 1
	RLMOverviewArtifactsEnvVar                 = "MAESTRO_RLM_OVERVIEW_ARTIFACTS"
	rlmOverviewArtifactDirName                 = "rlm_artifacts"
	rlmOverviewOptimizedProgramFileName        = "overview_optimized_program.json"
	rlmOverviewArtifactMetadataVersionKey      = "maestro_artifact_version"
	rlmOverviewArtifactMetadataSignatureKey    = "agent_signature"
	rlmOverviewArtifactMetadataRouteKey        = "route"
	rlmOverviewArtifactRoute                   = "ask.rlm_overview"
)

type rlmOverviewRuntimeArtifactsAgent struct {
	agent     *agentrlm.Agent
	agentType string
}

var _ optimize.OptimizableAgent = (*rlmOverviewRuntimeArtifactsAgent)(nil)

func DefaultRLMOverviewOptimizedProgramPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for RLM overview artifacts: %w", err)
	}
	return filepath.Join(homeDir, ".maestro", rlmOverviewArtifactDirName, rlmOverviewOptimizedProgramFileName), nil
}

func ResolveRLMOverviewOptimizedProgramPath(path string) (string, error) {
	path = strings.TrimSpace(os.ExpandEnv(path))
	if path == "" {
		path = strings.TrimSpace(os.Getenv(RLMOverviewArtifactsEnvVar))
	}
	if path == "" {
		return DefaultRLMOverviewOptimizedProgramPath()
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for RLM overview artifacts: %w", err)
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Clean(path), nil
}

func AnnotateRLMOverviewOptimizedProgram(program *optimize.OptimizedAgentProgram, metadata map[string]interface{}) error {
	if program == nil {
		return fmt.Errorf("RLM overview optimized program is nil")
	}
	if program.Metadata == nil {
		program.Metadata = make(map[string]interface{})
	}
	for key, value := range metadata {
		if strings.TrimSpace(key) == "" {
			continue
		}
		program.Metadata[key] = value
	}
	program.Metadata[rlmOverviewArtifactMetadataVersionKey] = RLMOverviewOptimizedProgramArtifactVersion
	program.Metadata[rlmOverviewArtifactMetadataSignatureKey] = RLMOverviewBenchmarkAgentSignature
	program.Metadata[rlmOverviewArtifactMetadataRouteKey] = rlmOverviewArtifactRoute
	if strings.TrimSpace(program.AgentType) == "" {
		program.AgentType = RLMOverviewBenchmarkAgentSignature
	}
	return ValidateRLMOverviewOptimizedProgram(program)
}

func ValidateRLMOverviewOptimizedProgram(program *optimize.OptimizedAgentProgram) error {
	if program == nil {
		return fmt.Errorf("RLM overview optimized program is nil")
	}
	if err := program.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(program.AgentType) != "" && program.AgentType != RLMOverviewBenchmarkAgentSignature {
		return fmt.Errorf("RLM overview optimized program agent_type %q does not match %q", program.AgentType, RLMOverviewBenchmarkAgentSignature)
	}
	metadata := program.Metadata
	if metadata == nil {
		return fmt.Errorf("RLM overview optimized program missing metadata")
	}
	if got := strings.TrimSpace(stringValue(metadata[rlmOverviewArtifactMetadataSignatureKey])); got != RLMOverviewBenchmarkAgentSignature {
		return fmt.Errorf("RLM overview optimized program agent_signature %q does not match %q", got, RLMOverviewBenchmarkAgentSignature)
	}
	if got := intMetadataValue(metadata[rlmOverviewArtifactMetadataVersionKey]); got != RLMOverviewOptimizedProgramArtifactVersion {
		return fmt.Errorf("unsupported RLM overview optimized program artifact version %d", got)
	}
	if got := strings.TrimSpace(stringValue(metadata[rlmOverviewArtifactMetadataRouteKey])); got != "" && got != rlmOverviewArtifactRoute {
		return fmt.Errorf("RLM overview optimized program route %q does not match %q", got, rlmOverviewArtifactRoute)
	}
	return nil
}

func LoadRLMOverviewOptimizedProgram(path string) (*optimize.OptimizedAgentProgram, string, error) {
	resolvedPath, err := ResolveRLMOverviewOptimizedProgramPath(path)
	if err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(resolvedPath); err != nil {
		if os.IsNotExist(err) {
			return nil, resolvedPath, nil
		}
		return nil, resolvedPath, err
	}
	program, err := optimize.ReadOptimizedAgentProgram(resolvedPath)
	if err != nil {
		return nil, resolvedPath, err
	}
	if err := ValidateRLMOverviewOptimizedProgram(program); err != nil {
		return nil, resolvedPath, err
	}
	return program, resolvedPath, nil
}

func WriteRLMOverviewOptimizedProgram(path string, program *optimize.OptimizedAgentProgram) error {
	resolvedPath, err := ResolveRLMOverviewOptimizedProgramPath(path)
	if err != nil {
		return err
	}
	if err := ValidateRLMOverviewOptimizedProgram(program); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return fmt.Errorf("create RLM overview artifact directory: %w", err)
	}
	return optimize.WriteOptimizedAgentProgram(resolvedPath, program)
}

func ApplyRLMOverviewOptimizedProgram(agent optimize.OptimizableAgent, program *optimize.OptimizedAgentProgram) error {
	if err := ValidateRLMOverviewOptimizedProgram(program); err != nil {
		return err
	}
	return optimize.ApplyOptimizedAgentProgram(agent, program)
}

func applyRLMOverviewOptimizedProgramToModule(module *modrlm.RLM, program *optimize.OptimizedAgentProgram) error {
	if module == nil {
		return fmt.Errorf("RLM overview module is nil")
	}
	return ApplyRLMOverviewOptimizedProgram(&rlmOverviewRuntimeArtifactsAgent{
		agent: agentrlm.NewAgent(RLMOverviewBenchmarkAgentSignature, module),
	}, program)
}

func (s *MaestroService) loadAndApplyRLMOverviewOptimizedProgram(ctx context.Context, module *modrlm.RLM) (string, bool) {
	path := ""
	if s != nil && s.config != nil {
		path = s.config.RLMOverviewArtifactsPath
	}
	program, resolvedPath, err := LoadRLMOverviewOptimizedProgram(path)
	if err != nil {
		if s != nil && s.logger != nil {
			s.logger.Warn(ctx, "Skipping RLM overview optimized program path=%q: %v", resolvedPath, err)
		}
		return resolvedPath, false
	}
	if program == nil {
		return resolvedPath, false
	}
	if err := applyRLMOverviewOptimizedProgramToModule(module, program); err != nil {
		if s != nil && s.logger != nil {
			s.logger.Warn(ctx, "Failed to apply RLM overview optimized program path=%q: %v", resolvedPath, err)
		}
		return resolvedPath, false
	}
	return resolvedPath, true
}

func (a *rlmOverviewRuntimeArtifactsAgent) Execute(context.Context, map[string]interface{}) (map[string]interface{}, error) {
	return nil, fmt.Errorf("RLM overview runtime artifact agent does not execute")
}

func (a *rlmOverviewRuntimeArtifactsAgent) GetCapabilities() []core.Tool {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.GetCapabilities()
}

func (a *rlmOverviewRuntimeArtifactsAgent) GetMemory() agents.Memory {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.GetMemory()
}

func (a *rlmOverviewRuntimeArtifactsAgent) GetArtifacts() optimize.AgentArtifacts {
	if a == nil || a.agent == nil {
		return optimize.AgentArtifacts{}
	}
	return a.agent.GetArtifacts()
}

func (a *rlmOverviewRuntimeArtifactsAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	if a == nil || a.agent == nil {
		return fmt.Errorf("RLM overview runtime artifact agent is nil")
	}
	return a.agent.SetArtifacts(artifacts)
}

func (a *rlmOverviewRuntimeArtifactsAgent) Clone() (optimize.OptimizableAgent, error) {
	if a == nil || a.agent == nil {
		return nil, fmt.Errorf("RLM overview runtime artifact agent is nil")
	}
	cloned, err := a.agent.Clone()
	if err != nil {
		return nil, err
	}
	rlmAgent, ok := cloned.(*agentrlm.Agent)
	if !ok {
		return nil, fmt.Errorf("RLM overview runtime artifact clone produced %T", cloned)
	}
	return &rlmOverviewRuntimeArtifactsAgent{agent: rlmAgent, agentType: a.agentType}, nil
}

func (a *rlmOverviewRuntimeArtifactsAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	if a == nil || a.agent == nil {
		return fmt.Errorf("RLM overview runtime artifact agent is nil")
	}
	return a.agent.UpdateArtifacts(update)
}

func (a *rlmOverviewRuntimeArtifactsAgent) OptimizationAgentType() string {
	if a != nil && strings.TrimSpace(a.agentType) != "" {
		return a.agentType
	}
	return RLMOverviewBenchmarkAgentSignature
}

func (a *rlmOverviewRuntimeArtifactsAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.ListOptimizationTargets()
}

func rlmOverviewProgramMetadata(modelID, suitePath string, trainingCount, validationCount int) map[string]interface{} {
	return map[string]interface{}{
		"created_at":                time.Now().UTC().Format(time.RFC3339),
		"model_id":                  modelID,
		"suite_path":                suitePath,
		"training_example_count":    trainingCount,
		"validation_example_count":  validationCount,
		"optimized_program_schema":  "dspy-go.optimized-agent-program",
		"optimized_program_version": 1,
	}
}

func NewRLMOverviewOptimizedProgramMetadata(modelID, suitePath string, trainingCount, validationCount int) map[string]interface{} {
	return rlmOverviewProgramMetadata(modelID, suitePath, trainingCount, validationCount)
}

func intMetadataValue(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case jsonNumber:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}

type jsonNumber interface {
	Int64() (int64, error)
}
