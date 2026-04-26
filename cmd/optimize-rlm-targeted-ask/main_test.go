package main

import (
	"path/filepath"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/maestro/internal/orchestration"
)

func TestSplitAgentExamplesStratifiesTargetedAskByRepo(t *testing.T) {
	examples := make([]optimize.AgentExample, 0, targetedAskMinimumGEPAExamples)
	for i := 0; i < 8; i++ {
		examples = append(examples, optimize.AgentExample{
			ID:     "maestro-case-" + string(rune('a'+i)),
			Inputs: map[string]interface{}{"owner": "XiaoConstantine", "repo": "maestro"},
		})
	}
	for i := 0; i < 8; i++ {
		examples = append(examples, optimize.AgentExample{
			ID:     "dspy-case-" + string(rune('a'+i)),
			Inputs: map[string]interface{}{"owner": "XiaoConstantine", "repo": "dspy-go"},
		})
	}

	train, validation, err := splitAgentExamples(examples, 0.25)
	if err != nil {
		t.Fatalf("splitAgentExamples() error = %v", err)
	}
	if len(train) != 12 || len(validation) != 4 {
		t.Fatalf("len(train), len(validation) = %d, %d; want 12, 4", len(train), len(validation))
	}
	counts := map[string]int{}
	for _, example := range validation {
		counts[example.Inputs["repo"].(string)]++
	}
	if counts["maestro"] != 2 || counts["dspy-go"] != 2 {
		t.Fatalf("validation repo counts = %#v, want 2 maestro and 2 dspy-go", counts)
	}
}

func TestSplitAgentExamplesKeepsCommittedTargetedSuiteValidationMixed(t *testing.T) {
	cases, err := orchestration.LoadRLMOverviewBenchmarkSuite(filepath.Join("..", "..", "benchmarks", "rlm_overview_suite.json"))
	if err != nil {
		t.Fatalf("LoadRLMOverviewBenchmarkSuite() error = %v", err)
	}

	_, validation, err := splitAgentExamples(orchestration.RLMOverviewBenchmarkExamples(cases), 0.25)
	if err != nil {
		t.Fatalf("splitAgentExamples() error = %v", err)
	}
	counts := map[string]int{}
	for _, example := range validation {
		counts[example.Inputs["repo"].(string)]++
	}
	if counts["maestro"] != 4 || counts["dspy-go"] != 4 {
		t.Fatalf("committed-suite validation repo counts = %#v, want 4 maestro and 4 dspy-go", counts)
	}
}

func TestRLMTargetedAskIntMutationPlansBoundRuntimeKnobs(t *testing.T) {
	plans := rlmIntMutationPlans(5, 42000)

	if got := plans[agentrlm.ArtifactMaxIterations]; got.Min != 1 || got.Max != 5 || got.Step != 1 {
		t.Fatalf("max iterations plan = %#v, want min=1 max=5 step=1", got)
	}
	if got := plans[agentrlm.ArtifactMaxTokens]; got.Min != 8400 || got.Max != 42000 || got.Step != 8400 {
		t.Fatalf("max tokens plan = %#v, want min=8400 max=42000 step=8400", got)
	}
}

func TestRLMTargetedAskValidationOutcome(t *testing.T) {
	if got := validationOutcome(0.01); got != "improved" {
		t.Fatalf("validationOutcome(improved) = %q", got)
	}
	if got := validationOutcome(0); got != "no_change" {
		t.Fatalf("validationOutcome(no_change) = %q", got)
	}
	if got := validationOutcome(-0.01); got != "regressed" {
		t.Fatalf("validationOutcome(regressed) = %q", got)
	}
	if !shouldWriteValidationArtifact("improved", false) {
		t.Fatalf("improved artifact should be written")
	}
	if shouldWriteValidationArtifact("regressed", true) {
		t.Fatalf("regressed artifact should not be written even with allow-no-improvement")
	}
}
