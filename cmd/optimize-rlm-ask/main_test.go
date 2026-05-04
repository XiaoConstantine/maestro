package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
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

func TestEvaluateOptimizationAcceptanceClassifiesRejectedAndSmoke(t *testing.T) {
	checkpoint := optimizationCheckpoint{
		BaselineValidation:   0.8,
		ReplayValidation:     0.8,
		BestSearch:           0.9,
		ValidationDelta:      0,
		SearchToReplayGap:    0.1,
		ArtifactApplySuccess: true,
		ProtectedGate:        &orchestration.RLMOverviewProtectedGateReport{Passed: true},
	}

	rejected := evaluateOptimizationAcceptance(runOptimizationRequest{
		baseline:             &orchestration.RLMOverviewBenchmarkBaseline{Scores: map[string]orchestration.RLMOverviewBenchmarkBaselineCaseScore{"case": {Score: 1}}},
		maxSearchToReplayGap: 0.03,
	}, checkpoint)
	if rejected.Passed || rejected.Decision != "rejected" {
		t.Fatalf("rejected gate = %#v, want rejected", rejected)
	}
	if !strings.Contains(strings.Join(rejected.Reasons, "\n"), "validation_delta") {
		t.Fatalf("rejected reasons = %#v, want validation_delta", rejected.Reasons)
	}

	smoke := evaluateOptimizationAcceptance(runOptimizationRequest{
		baseline:             &orchestration.RLMOverviewBenchmarkBaseline{Scores: map[string]orchestration.RLMOverviewBenchmarkBaselineCaseScore{"case": {Score: 1}}},
		allowNoImprovement:   true,
		maxSearchToReplayGap: 0.2,
	}, checkpoint)
	if !smoke.Passed || smoke.Decision != "smoke_tested" {
		t.Fatalf("smoke gate = %#v, want smoke_tested", smoke)
	}
}

func TestWriteBenchmarkMarkdownIncludesDecisionMetricsAndDirectComparison(t *testing.T) {
	report := &orchestration.RLMOverviewBenchmarkRunReport{
		AverageScore:      0.9,
		PassedExamples:    1,
		CompletedExamples: 1,
		TokenUsage:        map[string]int64{"total_tokens": 100},
		CostUSD:           0.02,
		LatencyMS:         orchestration.RLMOverviewLatencySummary{Average: 100},
		AverageQuality: orchestration.RLMOverviewQualitySummary{
			ExactGroundingScore:    0.9,
			SemanticQualityScore:   0.95,
			FactRecall:             1,
			FactPrecision:          1,
			SourceRecall:           1,
			SourcePrecision:        1,
			EvidenceFactCoverage:   1,
			EvidenceSourceCoverage: 1,
			RepoFactCoverage:       1,
			RepoSourceCoverage:     1,
			SchemaValidRate:        1,
			Terseness:              1,
		},
		RLMMetrics: orchestration.RLMOverviewTraceMetricsSummary{
			RootPromptMaxTokens:          180,
			RootPromptMeanTokens:         120,
			FullContextQuerySuccessCount: 0,
			SliceQueryRatio:              1,
			SubcallUsefulRatio:           1,
			QueryActionCount:             1,
			QueryActionSuccessCount:      1,
			QueryModeCounts:              map[string]int{"query_raw": 1},
			FinalAnswerRate:              1,
			TerminationCause:             "final_answer",
		},
		AcceptanceGate: &orchestration.RLMOverviewAcceptanceGateReport{Passed: true, Decision: "accepted"},
		FailureClasses: map[string]int{"semantic_match": 1},
		Ablations: &orchestration.RLMOverviewAblationSummary{
			ExactGroundingAverage:         0.9,
			SemanticQualityAverage:        0.95,
			SemanticQualityDelta:          0.05,
			SemanticRescuedCases:          1,
			CurrentManifestFactCoverage:   0.8,
			RicherManifestFactCoverage:    1.0,
			ManifestFactCoverageDelta:     0.2,
			CurrentManifestSourceCoverage: 0.7,
			RicherManifestSourceCoverage:  1.0,
			ManifestSourceCoverageDelta:   0.3,
			ContextMissingCases:           1,
		},
		BaselineComparison: &orchestration.RLMOverviewBaselineComparisonReport{
			RLMAverageScore:            0.9,
			DirectAverageScore:         0.7,
			QualityDelta:               0.2,
			RLMExactGroundingScore:     0.9,
			DirectExactGroundingScore:  0.7,
			ExactGroundingDelta:        0.2,
			RLMSemanticQualityScore:    0.95,
			DirectSemanticQualityScore: 0.75,
			SemanticQualityDelta:       0.2,
			RLMTokens:                  100,
			DirectTokens:               120,
			TokenDelta:                 -20,
			RLMCostPerCorrect:          0.02,
			DirectCostPerCorrect:       0.03,
		},
	}
	path := filepath.Join(t.TempDir(), "benchmark.md")
	if err := writeBenchmarkMarkdown(path, core.ModelID("test:model"), nil, report); err != nil {
		t.Fatalf("writeBenchmarkMarkdown() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(data)
	for _, want := range []string{"Decision: accepted", "semantic_quality_score", "Failure Classification", "semantic_match", "query_raw", "Ablations", "current_vs_richer_manifest", "Direct Baseline", "average_score"} {
		if !strings.Contains(content, want) {
			t.Fatalf("markdown missing %q:\n%s", want, content)
		}
	}
}

func TestWriteOptimizationMarkdownIncludesGEPAValidationFields(t *testing.T) {
	checkpoint := optimizationCheckpoint{
		Decision:             "accepted",
		ArtifactApplySuccess: true,
		ArtifactWritten:      true,
		BaselineValidation:   0.7,
		BestSearch:           0.9,
		BestValidation:       0.85,
		ReplayValidation:     0.82,
		ValidationDelta:      0.12,
		SearchToReplayGap:    0.08,
		ProtectedDelta:       0.02,
		MetricCallCount:      42,
		CandidateCount:       7,
		AcceptanceGate:       &optimizationAcceptanceGateReport{Passed: true, Decision: "accepted"},
	}
	path := filepath.Join(t.TempDir(), "gepa.md")
	if err := writeOptimizationMarkdown(path, runOptimizationRequest{modelID: core.ModelID("test:model")}, checkpoint); err != nil {
		t.Fatalf("writeOptimizationMarkdown() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(data)
	for _, want := range []string{"Decision: accepted", "baseline_validation", "best_search", "replay_validation", "Artifact apply success"} {
		if !strings.Contains(content, want) {
			t.Fatalf("markdown missing %q:\n%s", want, content)
		}
	}
}
