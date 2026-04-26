package orchestration

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

type rlmOverviewArtifactTestAgent struct {
	artifacts optimize.AgentArtifacts
}

func newRLMOverviewArtifactTestAgent(outerPrompt string) *rlmOverviewArtifactTestAgent {
	return &rlmOverviewArtifactTestAgent{
		artifacts: optimize.AgentArtifacts{
			Text: map[optimize.ArtifactKey]string{
				optimize.ArtifactRLMOuterPrompt:     outerPrompt,
				optimize.ArtifactRLMIterationPrompt: "iteration",
			},
			Int: map[string]int{"rlm_max_iterations": 3},
		},
	}
}

func (a *rlmOverviewArtifactTestAgent) Execute(context.Context, map[string]interface{}) (map[string]interface{}, error) {
	return nil, nil
}

func (a *rlmOverviewArtifactTestAgent) GetCapabilities() []core.Tool { return nil }

func (a *rlmOverviewArtifactTestAgent) GetMemory() agents.Memory { return nil }

func (a *rlmOverviewArtifactTestAgent) GetArtifacts() optimize.AgentArtifacts {
	return a.artifacts.Clone()
}

func (a *rlmOverviewArtifactTestAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	a.artifacts = artifacts.Clone()
	return nil
}

func (a *rlmOverviewArtifactTestAgent) Clone() (optimize.OptimizableAgent, error) {
	return &rlmOverviewArtifactTestAgent{artifacts: a.GetArtifacts()}, nil
}

func (a *rlmOverviewArtifactTestAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	next, err := update(a.GetArtifacts())
	if err != nil {
		return err
	}
	a.artifacts = next.Clone()
	return nil
}

func (a *rlmOverviewArtifactTestAgent) OptimizationAgentType() string {
	return RLMOverviewBenchmarkAgentSignature
}

func (a *rlmOverviewArtifactTestAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
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
			IntKey: "rlm_max_iterations",
		},
	}
}

func TestRLMOverviewOptimizedProgramRoundTripAndApply(t *testing.T) {
	source := newRLMOverviewArtifactTestAgent("optimized outer")
	program, err := optimize.ExportOptimizedAgentProgram(source)
	if err != nil {
		t.Fatalf("ExportOptimizedAgentProgram() error = %v", err)
	}
	if err := AnnotateRLMOverviewOptimizedProgram(program, rlmOverviewProgramMetadata("test-model", "suite.json", 3, 1)); err != nil {
		t.Fatalf("AnnotateRLMOverviewOptimizedProgram() error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "overview-program.json")
	if err := WriteRLMOverviewOptimizedProgram(path, program); err != nil {
		t.Fatalf("WriteRLMOverviewOptimizedProgram() error = %v", err)
	}
	loaded, resolvedPath, err := LoadRLMOverviewOptimizedProgram(path)
	if err != nil {
		t.Fatalf("LoadRLMOverviewOptimizedProgram() error = %v", err)
	}
	if resolvedPath != path {
		t.Fatalf("resolvedPath = %q, want %q", resolvedPath, path)
	}

	target := newRLMOverviewArtifactTestAgent("baseline outer")
	if err := ApplyRLMOverviewOptimizedProgram(target, loaded); err != nil {
		t.Fatalf("ApplyRLMOverviewOptimizedProgram() error = %v", err)
	}
	if got := target.GetArtifacts().Text[optimize.ArtifactRLMOuterPrompt]; got != "optimized outer" {
		t.Fatalf("outer prompt = %q, want optimized outer", got)
	}
}

func TestLoadRLMOverviewOptimizedProgramMissingFileFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	program, resolvedPath, err := LoadRLMOverviewOptimizedProgram(path)
	if err != nil {
		t.Fatalf("LoadRLMOverviewOptimizedProgram() error = %v", err)
	}
	if program != nil {
		t.Fatalf("program = %#v, want nil", program)
	}
	if resolvedPath != path {
		t.Fatalf("resolvedPath = %q, want %q", resolvedPath, path)
	}
}

func TestValidateRLMOverviewOptimizedProgramRejectsMismatchedMetadata(t *testing.T) {
	source := newRLMOverviewArtifactTestAgent("outer")
	program, err := optimize.ExportOptimizedAgentProgram(source)
	if err != nil {
		t.Fatalf("ExportOptimizedAgentProgram() error = %v", err)
	}
	if err := AnnotateRLMOverviewOptimizedProgram(program, nil); err != nil {
		t.Fatalf("AnnotateRLMOverviewOptimizedProgram() error = %v", err)
	}
	program.Metadata[rlmOverviewArtifactMetadataSignatureKey] = "other-agent"

	if err := ValidateRLMOverviewOptimizedProgram(program); err == nil {
		t.Fatalf("ValidateRLMOverviewOptimizedProgram() error = nil, want signature mismatch")
	}
}

func TestApplyRLMOverviewOptimizedProgramSkipsUnknownTargets(t *testing.T) {
	source := newRLMOverviewArtifactTestAgent("optimized outer")
	program, err := optimize.ExportOptimizedAgentProgram(source)
	if err != nil {
		t.Fatalf("ExportOptimizedAgentProgram() error = %v", err)
	}
	if err := AnnotateRLMOverviewOptimizedProgram(program, rlmOverviewProgramMetadata("test-model", "suite.json", 3, 1)); err != nil {
		t.Fatalf("AnnotateRLMOverviewOptimizedProgram() error = %v", err)
	}
	program.TargetOrder = append(program.TargetOrder, "root.rlm.future_target")
	program.Text["root.rlm.future_target"] = "future artifact value"

	target := newRLMOverviewArtifactTestAgent("baseline outer")
	if err := ApplyRLMOverviewOptimizedProgram(target, program); err != nil {
		t.Fatalf("ApplyRLMOverviewOptimizedProgram() error = %v", err)
	}
	if got := target.GetArtifacts().Text[optimize.ArtifactRLMOuterPrompt]; got != "optimized outer" {
		t.Fatalf("outer prompt = %q, want optimized outer", got)
	}
}
