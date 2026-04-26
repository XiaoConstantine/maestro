package main

import (
	"path/filepath"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/maestro/internal/orchestration"
)

func TestSplitAgentExamplesStratifiesByRepo(t *testing.T) {
	examples := make([]optimize.AgentExample, 0, 16)
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

	_, validationAgain, err := splitAgentExamples(examples, 0.25)
	if err != nil {
		t.Fatalf("second splitAgentExamples() error = %v", err)
	}
	for i := range validation {
		if validation[i].ID != validationAgain[i].ID {
			t.Fatalf("validation split is not deterministic: run1[%d]=%q run2[%d]=%q", i, validation[i].ID, i, validationAgain[i].ID)
		}
	}
}

func TestSplitAgentExamplesKeepsCommittedSuiteValidationMixed(t *testing.T) {
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

func TestSplitAgentExamplesRejectsTinyOptimizationSuites(t *testing.T) {
	examples := make([]optimize.AgentExample, 0, rlmOverviewMinimumGEPAExamples-1)
	for i := 0; i < rlmOverviewMinimumGEPAExamples-1; i++ {
		examples = append(examples, optimize.AgentExample{ID: "case"})
	}

	if _, _, err := splitAgentExamples(examples, 0.25); err == nil {
		t.Fatalf("splitAgentExamples() error = nil, want minimum-suite-size error")
	}
}

func TestValidateRunModeRejectsOptimizeAndReplay(t *testing.T) {
	if err := validateRunMode(true, true); err == nil {
		t.Fatalf("validateRunMode(true, true) error = nil, want mutually exclusive error")
	}
	if err := validateRunMode(true, false); err != nil {
		t.Fatalf("validateRunMode(true, false) error = %v", err)
	}
	if err := validateRunMode(false, true); err != nil {
		t.Fatalf("validateRunMode(false, true) error = %v", err)
	}
}

func TestRLMOverviewIntMutationPlansBoundRuntimeKnobs(t *testing.T) {
	plans := rlmOverviewIntMutationPlans(5, 50000)

	if got := plans[agentrlm.ArtifactMaxIterations]; got.Min != 1 || got.Max != 5 || got.Step != 1 {
		t.Fatalf("max iterations plan = %#v, want min=1 max=5 step=1", got)
	}
	if got := plans[agentrlm.ArtifactMaxTokens]; got.Min != 10000 || got.Max != 50000 || got.Step != 10000 {
		t.Fatalf("max tokens plan = %#v, want min=10000 max=50000 step=10000", got)
	}
}

func TestTokenLedgerAggregatesEvaluationTokens(t *testing.T) {
	ledger := newTokenLedger()
	ledger.record(map[string]int64{"total_tokens": 10, "prompt_tokens": 7})
	ledger.record(map[string]int64{"total_tokens": 5, "completion_tokens": 3})

	got := ledger.snapshot()
	if got["total_tokens"] != 15 || got["prompt_tokens"] != 7 || got["completion_tokens"] != 3 {
		t.Fatalf("snapshot = %#v, want aggregated token usage", got)
	}
}
