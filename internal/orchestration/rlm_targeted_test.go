package orchestration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

func TestBuildRLMTargetedAskContextUsesExplicitFilesAndSearchTerms(t *testing.T) {
	repoDir := t.TempDir()
	writeTargetedAskTestFile(t, repoDir, "internal/orchestration/service.go", "package orchestration\n\nfunc handleAsk() string { return \"native\" }\n")
	writeTargetedAskTestFile(t, repoDir, "README.md", "# Maestro\n")

	manifest, err := buildRLMTargetedAskContext(context.Background(), repoDir, "How does internal/orchestration/service.go handleAsk route requests?", 20000)
	if err != nil {
		t.Fatalf("buildRLMTargetedAskContext() error = %v", err)
	}
	if !targetedContainsString(manifest.Sources, "internal/orchestration/service.go") {
		t.Fatalf("sources = %#v, want explicit service.go", manifest.Sources)
	}
	if !targetedContainsString(manifest.Sources, "README.md") && manifest.Context == "" {
		t.Fatalf("manifest context is empty")
	}
	if got := manifest.Context; got == "" || !targetedContainsAll(got, "File: internal/orchestration/service.go", "handleAsk") {
		t.Fatalf("context = %q, want explicit file content", got)
	}
}

func TestParseRLMTargetedAskOutputAndSanitizeSources(t *testing.T) {
	raw := `{"answer":"handleAsk routes overview questions through RLM.","sources":["internal/orchestration/service.go","missing.go"]}`
	parsed, err := parseRLMTargetedAskOutput(raw)
	if err != nil {
		t.Fatalf("parseRLMTargetedAskOutput() error = %v", err)
	}
	if parsed.Answer == "" {
		t.Fatalf("Answer empty")
	}
	sources := sanitizeRLMTargetedAskSources(parsed.Sources, []string{"internal/orchestration/service.go"})
	if len(sources) != 1 || sources[0] != "internal/orchestration/service.go" {
		t.Fatalf("sources = %#v, want only allowed source", sources)
	}
}

func TestForcedAskStrategyAllowsTargetedAsk(t *testing.T) {
	t.Setenv(askStrategyEnvVar, "rlm-targeted")
	if got := forcedAskStrategy(); got != "rlm-targeted" {
		t.Fatalf("forcedAskStrategy() = %q, want rlm-targeted", got)
	}
}

func TestRLMTargetedAskOptimizedProgramRoundTripAndApply(t *testing.T) {
	source := newRLMTargetedAskArtifactTestAgent("optimized targeted outer")
	program, err := optimize.ExportOptimizedAgentProgram(source)
	if err != nil {
		t.Fatalf("ExportOptimizedAgentProgram() error = %v", err)
	}
	program.AgentType = "rlm"
	if err := AnnotateRLMTargetedAskOptimizedProgram(program, map[string]interface{}{"model_id": "test-model"}); err != nil {
		t.Fatalf("AnnotateRLMTargetedAskOptimizedProgram() error = %v", err)
	}
	if program.AgentType != RLMTargetedAskAgentSignature {
		t.Fatalf("AgentType = %q, want %q", program.AgentType, RLMTargetedAskAgentSignature)
	}

	path := filepath.Join(t.TempDir(), "targeted-program.json")
	if err := WriteRLMTargetedAskOptimizedProgram(path, program); err != nil {
		t.Fatalf("WriteRLMTargetedAskOptimizedProgram() error = %v", err)
	}
	loaded, resolvedPath, err := LoadRLMTargetedAskOptimizedProgram(path)
	if err != nil {
		t.Fatalf("LoadRLMTargetedAskOptimizedProgram() error = %v", err)
	}
	if resolvedPath != path {
		t.Fatalf("resolvedPath = %q, want %q", resolvedPath, path)
	}

	target := newRLMTargetedAskArtifactTestAgent("baseline targeted outer")
	if err := ApplyRLMTargetedAskOptimizedProgram(target, loaded); err != nil {
		t.Fatalf("ApplyRLMTargetedAskOptimizedProgram() error = %v", err)
	}
	if got := target.GetArtifacts().Text[optimize.ArtifactRLMOuterPrompt]; got != "optimized targeted outer" {
		t.Fatalf("outer prompt = %q, want optimized targeted outer", got)
	}
}

func TestValidateRLMTargetedAskOptimizedProgramRejectsOverviewArtifact(t *testing.T) {
	program := &optimize.OptimizedAgentProgram{
		Schema:      "dspy-go.optimized-agent-program",
		Version:     1,
		AgentType:   RLMOverviewBenchmarkAgentSignature,
		TargetOrder: []string{"root.rlm.outer"},
		Text:        map[string]string{"root.rlm.outer": "outer"},
		Metadata: map[string]interface{}{
			rlmOverviewArtifactMetadataVersionKey:   RLMOverviewOptimizedProgramArtifactVersion,
			rlmOverviewArtifactMetadataSignatureKey: RLMOverviewBenchmarkAgentSignature,
			rlmOverviewArtifactMetadataRouteKey:     rlmOverviewArtifactRoute,
		},
	}
	if err := ValidateRLMTargetedAskOptimizedProgram(program); err == nil {
		t.Fatalf("ValidateRLMTargetedAskOptimizedProgram() error = nil, want mismatched route/signature")
	}
}

type rlmTargetedAskArtifactTestAgent struct {
	artifacts optimize.AgentArtifacts
}

func newRLMTargetedAskArtifactTestAgent(outerPrompt string) *rlmTargetedAskArtifactTestAgent {
	return &rlmTargetedAskArtifactTestAgent{
		artifacts: optimize.AgentArtifacts{
			Text: map[optimize.ArtifactKey]string{
				optimize.ArtifactRLMOuterPrompt:     outerPrompt,
				optimize.ArtifactRLMIterationPrompt: "iteration",
			},
			Int: map[string]int{agentrlm.ArtifactMaxIterations: 3},
		},
	}
}

func (a *rlmTargetedAskArtifactTestAgent) Execute(context.Context, map[string]interface{}) (map[string]interface{}, error) {
	return nil, nil
}

func (a *rlmTargetedAskArtifactTestAgent) GetCapabilities() []core.Tool { return nil }

func (a *rlmTargetedAskArtifactTestAgent) GetMemory() agents.Memory { return nil }

func (a *rlmTargetedAskArtifactTestAgent) GetArtifacts() optimize.AgentArtifacts {
	return a.artifacts.Clone()
}

func (a *rlmTargetedAskArtifactTestAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	a.artifacts = artifacts.Clone()
	return nil
}

func (a *rlmTargetedAskArtifactTestAgent) Clone() (optimize.OptimizableAgent, error) {
	return &rlmTargetedAskArtifactTestAgent{artifacts: a.GetArtifacts()}, nil
}

func (a *rlmTargetedAskArtifactTestAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	next, err := update(a.GetArtifacts())
	if err != nil {
		return err
	}
	a.artifacts = next.Clone()
	return nil
}

func (a *rlmTargetedAskArtifactTestAgent) OptimizationAgentType() string {
	return RLMTargetedAskAgentSignature
}

func (a *rlmTargetedAskArtifactTestAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	return []optimize.OptimizationTargetDescriptor{
		{
			ID:          "root.rlm.outer",
			Kind:        optimize.OptimizationTargetText,
			ArtifactKey: optimize.ArtifactRLMOuterPrompt,
		},
		{
			ID:          "root.rlm.iteration",
			Kind:        optimize.OptimizationTargetText,
			ArtifactKey: optimize.ArtifactRLMIterationPrompt,
		},
		{
			ID:     "root.rlm.max_iterations",
			Kind:   optimize.OptimizationTargetInt,
			IntKey: agentrlm.ArtifactMaxIterations,
		},
	}
}

func writeTargetedAskTestFile(t *testing.T, repoDir, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(repoDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func targetedContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func targetedContainsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}
