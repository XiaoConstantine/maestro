package review

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
)

func TestParseReviewRLMIssuesObjectPayload(t *testing.T) {
	raw := "```json\n" +
		"{\n" +
		`  "issues": [` + "\n" +
		"    {\n" +
		`      "line": 42,` + "\n" +
		`      "category": "correctness",` + "\n" +
		`      "severity": "high",` + "\n" +
		`      "description": "The new branch dereferences cfg before checking for nil.",` + "\n" +
		`      "suggestion": "Check cfg before using it.",` + "\n" +
		`      "confidence": 0.91` + "\n" +
		"    }\n" +
		"  ]\n" +
		"}\n" +
		"```"

	issues, err := parseReviewRLMIssues(raw, "internal/review/review.go")
	if err != nil {
		t.Fatalf("parseReviewRLMIssues() error = %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("len(issues) = %d, want 1", len(issues))
	}
	issue := issues[0]
	if issue.FilePath != "internal/review/review.go" {
		t.Fatalf("FilePath = %q, want default file", issue.FilePath)
	}
	if issue.LineRange.Start != 42 || issue.LineRange.End != 42 {
		t.Fatalf("LineRange = %+v, want 42-42", issue.LineRange)
	}
	if issue.Category != "bug" || issue.Severity != "high" {
		t.Fatalf("category/severity = %q/%q, want bug/high", issue.Category, issue.Severity)
	}
	if issue.Confidence != 0.91 {
		t.Fatalf("Confidence = %.2f, want 0.91", issue.Confidence)
	}
}

func TestReviewRLMProcessorMarksChunkErrorsUnknown(t *testing.T) {
	processor := &reviewRLMProcessor{}
	results, err := processor.ProcessMultipleChunks(context.Background(), []map[string]interface{}{
		{"file_path": "internal/review/review.go"},
	}, nil)
	if err != nil {
		t.Fatalf("ProcessMultipleChunks() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].OverallQuality != "unknown" || results[0].ReasoningChain != "rlm_chunk_error" {
		t.Fatalf("failure marker = %q/%q, want unknown/rlm_chunk_error", results[0].OverallQuality, results[0].ReasoningChain)
	}
}

func TestPRReviewAgentSetBudgetManagerPropagatesToRLMProcessor(t *testing.T) {
	processor := &reviewRLMProcessor{}
	agent := &PRReviewAgent{reviewProcessor: processor}
	manager := maestrobudget.NewBudgetManager(maestrobudget.DefaultConfig())

	agent.SetBudgetManager(manager)

	if processor.budgetManager != manager {
		t.Fatal("budget manager was not propagated to review RLM processor")
	}
}

func TestReviewRLMOptimizedProgramRoundTripAndApply(t *testing.T) {
	agent := &reviewRLMArtifactTestAgent{
		artifacts: optimize.AgentArtifacts{
			Text: map[optimize.ArtifactKey]string{
				optimize.ArtifactRLMOuterPrompt:     "optimized review outer",
				optimize.ArtifactRLMIterationPrompt: "review iteration",
			},
			Int: map[string]int{agentrlm.ArtifactMaxIterations: 3},
		},
	}
	program, err := optimize.ExportOptimizedAgentProgram(agent)
	if err != nil {
		t.Fatalf("ExportOptimizedAgentProgram() error = %v", err)
	}
	program.AgentType = "rlm"
	if err := annotateReviewRLMOptimizedProgram(program, map[string]interface{}{"model_id": "test-model"}); err != nil {
		t.Fatalf("annotateReviewRLMOptimizedProgram() error = %v", err)
	}
	if program.AgentType != reviewRLMAgentSignature {
		t.Fatalf("AgentType = %q, want %q", program.AgentType, reviewRLMAgentSignature)
	}

	path := filepath.Join(t.TempDir(), "review-rlm-program.json")
	if err := optimize.WriteOptimizedAgentProgram(path, program); err != nil {
		t.Fatalf("WriteOptimizedAgentProgram() error = %v", err)
	}
	loaded, resolvedPath, err := loadReviewRLMOptimizedProgram(path)
	if err != nil {
		t.Fatalf("loadReviewRLMOptimizedProgram() error = %v", err)
	}
	if resolvedPath != path {
		t.Fatalf("resolvedPath = %q, want %q", resolvedPath, path)
	}

	target := &reviewRLMArtifactTestAgent{
		artifacts: optimize.AgentArtifacts{
			Text: map[optimize.ArtifactKey]string{optimize.ArtifactRLMOuterPrompt: "baseline"},
			Int:  map[string]int{},
		},
	}
	if err := applyReviewRLMOptimizedProgram(target, loaded); err != nil {
		t.Fatalf("applyReviewRLMOptimizedProgram() error = %v", err)
	}
	if got := target.GetArtifacts().Text[optimize.ArtifactRLMOuterPrompt]; got != "optimized review outer" {
		t.Fatalf("outer prompt = %q, want optimized review outer", got)
	}
}

func TestValidateReviewRLMOptimizedProgramRejectsWrongSignature(t *testing.T) {
	program := &optimize.OptimizedAgentProgram{
		Schema:      "dspy-go.optimized-agent-program",
		Version:     1,
		AgentType:   reviewRLMAgentSignature,
		TargetOrder: []string{"root.rlm.outer"},
		Text:        map[string]string{"root.rlm.outer": "outer"},
		Metadata: map[string]interface{}{
			reviewRLMArtifactMetadataVersionKey:   reviewRLMOptimizedProgramVersion,
			reviewRLMArtifactMetadataSignatureKey: "other-agent",
			reviewRLMArtifactMetadataRouteKey:     reviewRLMArtifactRoute,
		},
	}
	if err := validateReviewRLMOptimizedProgram(program); err == nil {
		t.Fatalf("validateReviewRLMOptimizedProgram() error = nil, want signature mismatch")
	}
}

type reviewRLMArtifactTestAgent struct {
	artifacts optimize.AgentArtifacts
}

func (a *reviewRLMArtifactTestAgent) Execute(context.Context, map[string]interface{}) (map[string]interface{}, error) {
	return nil, nil
}

func (a *reviewRLMArtifactTestAgent) GetCapabilities() []core.Tool { return nil }

func (a *reviewRLMArtifactTestAgent) GetMemory() agents.Memory { return nil }

func (a *reviewRLMArtifactTestAgent) GetArtifacts() optimize.AgentArtifacts {
	return a.artifacts.Clone()
}

func (a *reviewRLMArtifactTestAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	a.artifacts = artifacts.Clone()
	return nil
}

func (a *reviewRLMArtifactTestAgent) Clone() (optimize.OptimizableAgent, error) {
	return &reviewRLMArtifactTestAgent{artifacts: a.GetArtifacts()}, nil
}

func (a *reviewRLMArtifactTestAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	next, err := update(a.GetArtifacts())
	if err != nil {
		return err
	}
	a.artifacts = next.Clone()
	return nil
}

func (a *reviewRLMArtifactTestAgent) OptimizationAgentType() string {
	return reviewRLMAgentSignature
}

func (a *reviewRLMArtifactTestAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
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
