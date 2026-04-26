package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

type scriptedRLMOverviewBenchmarkAgent struct {
	responses map[string]scriptedRLMOverviewResponse
	artifacts optimize.AgentArtifacts
	lastTrace *agents.ExecutionTrace
}

type scriptedRLMOverviewResponse struct {
	answer  string
	sources []string
	trace   *agents.ExecutionTrace
	err     error
}

func (a *scriptedRLMOverviewBenchmarkAgent) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	caseID := strings.TrimSpace(stringValue(input["case_id"]))
	response, ok := a.responses[caseID]
	if !ok {
		return nil, fmt.Errorf("missing scripted response for %q", caseID)
	}
	a.lastTrace = response.trace
	if response.err != nil {
		return nil, response.err
	}
	return map[string]interface{}{
		"answer":     response.answer,
		"raw_answer": response.answer,
		"sources":    append([]string(nil), response.sources...),
	}, nil
}

func (a *scriptedRLMOverviewBenchmarkAgent) GetCapabilities() []core.Tool { return nil }

func (a *scriptedRLMOverviewBenchmarkAgent) GetMemory() agents.Memory { return nil }

func (a *scriptedRLMOverviewBenchmarkAgent) GetArtifacts() optimize.AgentArtifacts {
	return a.artifacts.Clone()
}

func (a *scriptedRLMOverviewBenchmarkAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	a.artifacts = artifacts.Clone()
	return nil
}

func (a *scriptedRLMOverviewBenchmarkAgent) Clone() (optimize.OptimizableAgent, error) {
	return &scriptedRLMOverviewBenchmarkAgent{
		responses: a.responses,
		artifacts: a.artifacts.Clone(),
	}, nil
}

func (a *scriptedRLMOverviewBenchmarkAgent) LastExecutionTrace() *agents.ExecutionTrace {
	if a.lastTrace == nil {
		return nil
	}
	return a.lastTrace.Clone()
}

func TestRLMOverviewEvaluationRubricDefinesMetric(t *testing.T) {
	rubric := RLMOverviewEvaluationRubric()
	for _, want := range []string{"fact_recall", "source_coverage", "terseness", "forbidden_facts", "Protected cases"} {
		if !strings.Contains(rubric, want) {
			t.Fatalf("rubric missing %q:\n%s", want, rubric)
		}
	}
}

func TestEvaluateRLMOverviewAnswerScoresFactsSourcesAndTerseness(t *testing.T) {
	benchmarkCase := RLMOverviewBenchmarkCase{
		ID:              "overview",
		RepoPath:        "/tmp/repo",
		Question:        "How is the repository organized?",
		ExpectedFacts:   []string{"internal/orchestration", "internal/review"},
		ExpectedSources: []string{"README.md", "go.mod"},
		Protected:       true,
	}

	eval := EvaluateRLMOverviewAnswer(
		benchmarkCase,
		"The repo centers on internal/orchestration and internal/review, with entry points described in README.md.",
		[]string{"go.mod"},
		DefaultRLMOverviewEvaluatorConfig(),
	)

	if eval.Score != 1.0 {
		t.Fatalf("Score = %v, want 1.0", eval.Score)
	}
	if eval.FactRecall != 1.0 {
		t.Fatalf("FactRecall = %v, want 1.0", eval.FactRecall)
	}
	if eval.SourceCoverage != 1.0 {
		t.Fatalf("SourceCoverage = %v, want 1.0", eval.SourceCoverage)
	}
	if eval.Terseness != 1.0 {
		t.Fatalf("Terseness = %v, want 1.0", eval.Terseness)
	}
	if got := eval.Diagnostics["protected"]; got != true {
		t.Fatalf("protected diagnostic = %#v, want true", got)
	}
}

func TestEvaluateRLMOverviewAnswerMatchesFactsCaseInsensitively(t *testing.T) {
	benchmarkCase := RLMOverviewBenchmarkCase{
		ID:            "case-insensitive",
		RepoPath:      "/tmp/repo",
		Question:      "What does this repo expose?",
		ExpectedFacts: []string{"GO CLI"},
	}

	eval := EvaluateRLMOverviewAnswer(
		benchmarkCase,
		"Go CLI support lives in the terminal package for repository workflows.",
		nil,
		DefaultRLMOverviewEvaluatorConfig(),
	)

	if eval.FactRecall != 1.0 {
		t.Fatalf("FactRecall = %v, want case-insensitive match", eval.FactRecall)
	}
}

func TestEvaluateRLMOverviewAnswerPenalizesForbiddenFacts(t *testing.T) {
	benchmarkCase := RLMOverviewBenchmarkCase{
		ID:             "boundary",
		RepoPath:       "/tmp/repo",
		Question:       "What does this repo do?",
		ExpectedFacts:  []string{"internal/orchestration"},
		ForbiddenFacts: []string{"React"},
	}

	eval := EvaluateRLMOverviewAnswer(
		benchmarkCase,
		"The repo centers on internal/orchestration and a React frontend.",
		nil,
		DefaultRLMOverviewEvaluatorConfig(),
	)

	if eval.Score >= 1.0 {
		t.Fatalf("Score = %v, want forbidden fact penalty", eval.Score)
	}
	if len(eval.ForbiddenHits) != 1 || eval.ForbiddenHits[0] != "React" {
		t.Fatalf("ForbiddenHits = %#v, want React", eval.ForbiddenHits)
	}
}

func TestEvaluateRLMOverviewAnswerPenalizesTooShortAnswers(t *testing.T) {
	benchmarkCase := RLMOverviewBenchmarkCase{
		ID:       "too-short",
		RepoPath: "/tmp/repo",
		Question: "How is the repository organized?",
	}

	eval := EvaluateRLMOverviewAnswer(
		benchmarkCase,
		"Yes.",
		nil,
		RLMOverviewEvaluatorConfig{
			TersenessWeight:      1,
			ForbiddenFactPenalty: 0.25,
			MinAnswerWords:       4,
			MaxAnswerWords:       20,
		},
	)

	if eval.Terseness >= 1.0 {
		t.Fatalf("Terseness = %v, want penalty for short answer", eval.Terseness)
	}
	if eval.Score != eval.Terseness {
		t.Fatalf("Score = %v, want terseness-only score %v", eval.Score, eval.Terseness)
	}
}

func TestEvaluateRLMOverviewAnswerDoesNotReverseMatchSources(t *testing.T) {
	benchmarkCase := RLMOverviewBenchmarkCase{
		ID:              "source-reverse",
		RepoPath:        "/tmp/repo",
		Question:        "Which source grounds this answer?",
		ExpectedSources: []string{"go.mod"},
	}

	eval := EvaluateRLMOverviewAnswer(
		benchmarkCase,
		"This answer is grounded by repository metadata.",
		[]string{"go"},
		RLMOverviewEvaluatorConfig{
			SourceCoverageWeight: 1,
			ForbiddenFactPenalty: 0.25,
			MinAnswerWords:       4,
			MaxAnswerWords:       20,
		},
	)

	if eval.SourceCoverage != 0 {
		t.Fatalf("SourceCoverage = %v, want no reverse match from short source", eval.SourceCoverage)
	}
	if len(eval.MissingSources) != 1 || eval.MissingSources[0] != "go.mod" {
		t.Fatalf("MissingSources = %#v, want go.mod", eval.MissingSources)
	}
}

func TestEvaluateRLMOverviewAnswerAllowsExplicitWeightDisable(t *testing.T) {
	benchmarkCase := RLMOverviewBenchmarkCase{
		ID:              "source-only",
		RepoPath:        "/tmp/repo",
		Question:        "How is the repository organized?",
		ExpectedFacts:   []string{"missing fact"},
		ExpectedSources: []string{"README.md"},
	}

	eval := EvaluateRLMOverviewAnswer(
		benchmarkCase,
		"A concise overview grounded by README.md.",
		nil,
		RLMOverviewEvaluatorConfig{
			FactRecallWeight:     0,
			SourceCoverageWeight: 1,
			TersenessWeight:      0,
			ForbiddenFactPenalty: 0.25,
			MaxAnswerWords:       20,
		},
	)

	if eval.Score != 1.0 {
		t.Fatalf("Score = %v, want source-only weight to ignore missing fact", eval.Score)
	}
}

func TestEvaluateRLMOverviewAnswerHandlesForbiddenOnlyCases(t *testing.T) {
	benchmarkCase := RLMOverviewBenchmarkCase{
		ID:             "negative",
		RepoPath:       "/tmp/repo",
		Question:       "Where is the frontend app?",
		ForbiddenFacts: []string{"src/App.tsx"},
	}

	clean := EvaluateRLMOverviewAnswer(
		benchmarkCase,
		"I do not see a frontend app in the repository manifest.",
		nil,
		DefaultRLMOverviewEvaluatorConfig(),
	)
	if clean.Score != 1.0 {
		t.Fatalf("clean Score = %v, want 1.0", clean.Score)
	}

	hallucinated := EvaluateRLMOverviewAnswer(
		benchmarkCase,
		"The frontend app is in src/App.tsx.",
		nil,
		DefaultRLMOverviewEvaluatorConfig(),
	)
	if hallucinated.Score >= clean.Score {
		t.Fatalf("hallucinated Score = %v, want below clean score %v", hallucinated.Score, clean.Score)
	}
}

func TestRLMOverviewBenchmarkExamplesRoundTrip(t *testing.T) {
	example := RLMOverviewBenchmarkExamples([]RLMOverviewBenchmarkCase{{
		ID:              "roundtrip",
		RepoPath:        "/tmp/repo",
		Owner:           "XiaoConstantine",
		Repo:            "maestro",
		Question:        "What does this repo do?",
		GoldAnswer:      "Maestro coordinates repository QA and review workflows.",
		ExpectedFacts:   []string{"internal/orchestration"},
		ForbiddenFacts:  []string{"Django"},
		ExpectedSources: []string{"README.md"},
		Protected:       true,
		Tags:            []string{"overview"},
	}})[0]

	data, err := json.Marshal(example)
	if err != nil {
		t.Fatalf("json.Marshal(example) error = %v", err)
	}

	var decoded optimize.AgentExample
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(example) error = %v", err)
	}

	benchmarkCase, err := rlmOverviewCaseFromExample(decoded)
	if err != nil {
		t.Fatalf("rlmOverviewCaseFromExample() error = %v", err)
	}
	if benchmarkCase.GoldAnswer == "" {
		t.Fatalf("GoldAnswer was not preserved")
	}
	if !benchmarkCase.Protected {
		t.Fatalf("Protected = false, want true")
	}
	if len(benchmarkCase.ExpectedSources) != 1 || benchmarkCase.ExpectedSources[0] != "README.md" {
		t.Fatalf("ExpectedSources = %#v, want README.md", benchmarkCase.ExpectedSources)
	}
}

func TestLoadRLMOverviewBenchmarkSuiteResolvesRelativeRepoPaths(t *testing.T) {
	suiteDir := filepath.Join(t.TempDir(), "benchmarks")
	if err := os.MkdirAll(suiteDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	repoDir := filepath.Join(suiteDir, "..", "fixture-repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(repoDir) error = %v", err)
	}

	suitePath := filepath.Join(suiteDir, "rlm_overview_suite.json")
	data := []byte(`{"cases":[{"id":"c1","repo_path":"../fixture-repo","owner":"XiaoConstantine","repo":"maestro","question":"How is the repository organized?","gold_answer":"The repo is organized around internal/orchestration.","expected_facts":["internal/orchestration"],"expected_sources":["README.md"],"protected":true}]}`)
	if err := os.WriteFile(suitePath, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cases, err := LoadRLMOverviewBenchmarkSuite(suitePath)
	if err != nil {
		t.Fatalf("LoadRLMOverviewBenchmarkSuite() error = %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("len(cases) = %d, want 1", len(cases))
	}
	if got, want := cases[0].RepoPath, filepath.Clean(repoDir); got != want {
		t.Fatalf("RepoPath = %q, want %q", got, want)
	}
	if !cases[0].Protected {
		t.Fatalf("Protected = false, want true")
	}
}

func TestLoadCommittedRLMOverviewBenchmarkSuite(t *testing.T) {
	cases, err := LoadRLMOverviewBenchmarkSuite("../../benchmarks/rlm_overview_suite.json")
	if err != nil {
		t.Fatalf("LoadRLMOverviewBenchmarkSuite() error = %v", err)
	}
	if len(cases) != 32 {
		t.Fatalf("len(cases) = %d, want frozen 32-case bootstrap suite", len(cases))
	}

	protectedCount := 0
	for _, benchmarkCase := range cases {
		if benchmarkCase.Protected {
			protectedCount++
		}
		if benchmarkCase.GoldAnswer == "" {
			t.Fatalf("case %q missing gold_answer", benchmarkCase.ID)
		}
		if len(benchmarkCase.ExpectedFacts) == 0 && len(benchmarkCase.ForbiddenFacts) == 0 {
			t.Fatalf("case %q missing expected_facts and forbidden_facts", benchmarkCase.ID)
		}
	}
	if protectedCount == 0 {
		t.Fatalf("protectedCount = 0, want protected qualitative subset")
	}
}

func TestRLMOverviewBenchmarkEvaluatorUsesExplicitSourcesAndTraceTokens(t *testing.T) {
	benchmarkCase := RLMOverviewBenchmarkCase{
		ID:              "overview",
		RepoPath:        "/tmp/repo",
		Question:        "How is this repo organized?",
		ExpectedFacts:   []string{"internal/orchestration"},
		ExpectedSources: []string{"README.md"},
	}
	agent := &scriptedRLMOverviewBenchmarkAgent{
		responses: map[string]scriptedRLMOverviewResponse{
			"overview": {
				answer:  "The repository overview is grounded in internal/orchestration and the surrounding command flow.",
				sources: []string{"README.md"},
				trace: &agents.ExecutionTrace{
					TokenUsage: map[string]int64{"total_tokens": 42},
				},
			},
		},
	}
	evaluator := NewRLMOverviewBenchmarkEvaluator(DefaultRLMOverviewEvaluatorConfig())

	result, err := evaluator.Evaluate(context.Background(), agent, RLMOverviewBenchmarkExamples([]RLMOverviewBenchmarkCase{benchmarkCase})[0])
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Score != 1.0 {
		t.Fatalf("Score = %v, want 1.0", result.Score)
	}
	if got := result.SideInfo.Tokens["total_tokens"]; got != 42 {
		t.Fatalf("total_tokens = %d, want 42", got)
	}
	if got := result.SideInfo.Scores["source_coverage"]; got != 1.0 {
		t.Fatalf("source_coverage = %v, want 1.0", got)
	}
	if got := result.SideInfo.Diagnostics["sources"]; got == nil {
		t.Fatalf("sources diagnostic missing")
	}
}

func TestRunRLMOverviewBenchmarkOrdersConcurrentResults(t *testing.T) {
	cases := []RLMOverviewBenchmarkCase{
		{
			ID:            "case-a",
			RepoPath:      "/tmp/repo",
			Question:      "What is A?",
			ExpectedFacts: []string{"alpha"},
		},
		{
			ID:            "case-b",
			RepoPath:      "/tmp/repo",
			Question:      "What is B?",
			ExpectedFacts: []string{"beta"},
		},
		{
			ID:            "case-c",
			RepoPath:      "/tmp/repo",
			Question:      "What is C?",
			ExpectedFacts: []string{"gamma"},
		},
	}
	agent := &scriptedRLMOverviewBenchmarkAgent{
		responses: map[string]scriptedRLMOverviewResponse{
			"case-a": {
				answer: "alpha appears in a sufficiently detailed answer for the runner.",
				trace:  &agents.ExecutionTrace{TokenUsage: map[string]int64{"total_tokens": 10}},
			},
			"case-b": {
				answer: "beta appears in a sufficiently detailed answer for the runner.",
				trace:  &agents.ExecutionTrace{TokenUsage: map[string]int64{"total_tokens": 20}},
			},
			"case-c": {
				answer: "gamma appears in a sufficiently detailed answer for the runner.",
				trace:  &agents.ExecutionTrace{TokenUsage: map[string]int64{"total_tokens": 30}},
			},
		},
	}

	report, err := RunRLMOverviewBenchmark(context.Background(), agent, cases, RLMOverviewBenchmarkRunConfig{Workers: 2})
	if err != nil {
		t.Fatalf("RunRLMOverviewBenchmark() error = %v", err)
	}
	if len(report.Results) != 3 {
		t.Fatalf("len(Results) = %d, want 3", len(report.Results))
	}
	for i, wantID := range []string{"case-a", "case-b", "case-c"} {
		if report.Results[i].CaseID != wantID {
			t.Fatalf("Results[%d].CaseID = %q, want %q", i, report.Results[i].CaseID, wantID)
		}
	}
	if got := report.TokenUsage["total_tokens"]; got != 60 {
		t.Fatalf("total_tokens = %d, want 60", got)
	}
	if report.AverageScore != 1.0 {
		t.Fatalf("AverageScore = %v, want 1.0", report.AverageScore)
	}
}

func TestRunRLMOverviewBenchmarkProtectedGateUsesVersionedBaseline(t *testing.T) {
	cases := []RLMOverviewBenchmarkCase{
		{
			ID:            "protected",
			RepoPath:      "/tmp/repo",
			Question:      "What is protected?",
			ExpectedFacts: []string{"important"},
			Protected:     true,
		},
		{
			ID:            "unprotected",
			RepoPath:      "/tmp/repo",
			Question:      "What is unprotected?",
			ExpectedFacts: []string{"ordinary"},
		},
	}
	agent := &scriptedRLMOverviewBenchmarkAgent{
		responses: map[string]scriptedRLMOverviewResponse{
			"protected": {
				answer: "This answer is detailed enough but does not include the key term.",
			},
			"unprotected": {
				answer: "ordinary appears in a sufficiently detailed unprotected answer.",
			},
		},
	}
	baseline := &RLMOverviewBenchmarkBaseline{
		Version:        RLMOverviewBenchmarkBaselineVersion,
		AgentSignature: RLMOverviewBenchmarkAgentSignature,
		Scores: map[string]RLMOverviewBenchmarkBaselineCaseScore{
			"protected": {
				Score:          1.0,
				FactRecall:     1.0,
				SourceCoverage: 1.0,
				Terseness:      1.0,
			},
			"unprotected": {
				Score: 1.0,
			},
		},
	}

	report, err := RunRLMOverviewBenchmark(context.Background(), agent, cases, RLMOverviewBenchmarkRunConfig{
		Workers:  2,
		Baseline: baseline,
	})
	if err != nil {
		t.Fatalf("RunRLMOverviewBenchmark() error = %v", err)
	}
	if report.ProtectedGate == nil {
		t.Fatalf("ProtectedGate = nil, want gate report")
	}
	if report.ProtectedGate.Passed {
		t.Fatalf("ProtectedGate.Passed = true, want strict protected regression failure")
	}
	if len(report.ProtectedGate.Regressions) != 1 || report.ProtectedGate.Regressions[0].CaseID != "protected" {
		t.Fatalf("Regressions = %#v, want protected regression", report.ProtectedGate.Regressions)
	}
	if len(report.ProtectedGate.Regressions[0].RegressedDims) == 0 {
		t.Fatalf("RegressedDims missing")
	}
}

func TestRunRLMOverviewBenchmarkProtectedGateFailsOnMissingBaseline(t *testing.T) {
	cases := []RLMOverviewBenchmarkCase{
		{
			ID:            "protected-missing",
			RepoPath:      "/tmp/repo",
			Question:      "What is protected?",
			ExpectedFacts: []string{"important"},
			Protected:     true,
		},
	}
	agent := &scriptedRLMOverviewBenchmarkAgent{
		responses: map[string]scriptedRLMOverviewResponse{
			"protected-missing": {
				answer: "important appears in a sufficiently detailed protected answer.",
			},
		},
	}
	baseline := &RLMOverviewBenchmarkBaseline{
		Version:        RLMOverviewBenchmarkBaselineVersion,
		AgentSignature: RLMOverviewBenchmarkAgentSignature,
		Scores: map[string]RLMOverviewBenchmarkBaselineCaseScore{
			"other-protected": {Score: 1.0},
		},
	}

	report, err := RunRLMOverviewBenchmark(context.Background(), agent, cases, RLMOverviewBenchmarkRunConfig{
		Baseline: baseline,
	})
	if err != nil {
		t.Fatalf("RunRLMOverviewBenchmark() error = %v", err)
	}
	if report.ProtectedGate == nil {
		t.Fatalf("ProtectedGate = nil, want gate report")
	}
	if report.ProtectedGate.Passed {
		t.Fatalf("ProtectedGate.Passed = true, want missing-baseline failure")
	}
	if len(report.ProtectedGate.MissingBaseline) != 1 || report.ProtectedGate.MissingBaseline[0] != "protected-missing" {
		t.Fatalf("MissingBaseline = %#v, want protected-missing", report.ProtectedGate.MissingBaseline)
	}
}

func TestRunRLMOverviewBenchmarkRetriesEvaluationErrors(t *testing.T) {
	cases := []RLMOverviewBenchmarkCase{{
		ID:            "flaky",
		RepoPath:      "/tmp/repo",
		Question:      "What failed?",
		ExpectedFacts: []string{"anything"},
	}}
	agent := &scriptedRLMOverviewBenchmarkAgent{
		responses: map[string]scriptedRLMOverviewResponse{
			"flaky": {err: fmt.Errorf("temporary failure")},
		},
	}

	report, err := RunRLMOverviewBenchmark(context.Background(), agent, cases, RLMOverviewBenchmarkRunConfig{MaxAttempts: 2})
	if err != nil {
		t.Fatalf("RunRLMOverviewBenchmark() error = %v", err)
	}
	if report.Results[0].Attempts != 2 {
		t.Fatalf("Attempts = %d, want 2", report.Results[0].Attempts)
	}
	if report.EvaluationErrors != 1 {
		t.Fatalf("EvaluationErrors = %d, want 1 final failed case", report.EvaluationErrors)
	}
	if got := report.Results[0].Diagnostics["error"]; got != "temporary failure" {
		t.Fatalf("Diagnostics[error] = %#v, want temporary failure", got)
	}
}

func TestRLMOverviewBenchmarkBaselineRoundTripAndSignatureValidation(t *testing.T) {
	report := &RLMOverviewBenchmarkRunReport{
		AgentSignature: RLMOverviewBenchmarkAgentSignature,
		Results: []RLMOverviewBenchmarkCaseReport{
			{
				CaseID:         "case-a",
				Score:          0.75,
				FactRecall:     0.5,
				SourceCoverage: 1.0,
				Terseness:      1.0,
				ForbiddenHits:  []string{"bad"},
			},
		},
	}
	baseline, err := NewRLMOverviewBenchmarkBaseline(report)
	if err != nil {
		t.Fatalf("NewRLMOverviewBenchmarkBaseline() error = %v", err)
	}
	if baseline.Version != RLMOverviewBenchmarkBaselineVersion {
		t.Fatalf("Version = %d, want %d", baseline.Version, RLMOverviewBenchmarkBaselineVersion)
	}
	if err := baseline.Validate("different-agent"); err == nil {
		t.Fatalf("Validate() with mismatched signature returned nil error")
	}

	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := WriteRLMOverviewBenchmarkBaseline(path, baseline); err != nil {
		t.Fatalf("WriteRLMOverviewBenchmarkBaseline() error = %v", err)
	}
	loaded, err := LoadRLMOverviewBenchmarkBaseline(path)
	if err != nil {
		t.Fatalf("LoadRLMOverviewBenchmarkBaseline() error = %v", err)
	}
	if got := loaded.Scores["case-a"].ForbiddenHits[0]; got != "bad" {
		t.Fatalf("ForbiddenHits[0] = %q, want bad", got)
	}
}

func TestNewRLMOverviewBenchmarkBaselineRefusesErroredCases(t *testing.T) {
	report := &RLMOverviewBenchmarkRunReport{
		AgentSignature: RLMOverviewBenchmarkAgentSignature,
		Results: []RLMOverviewBenchmarkCaseReport{
			{
				CaseID: "case-a",
				Score:  0,
				Error:  "temporary failure",
			},
		},
	}

	if _, err := NewRLMOverviewBenchmarkBaseline(report); err == nil {
		t.Fatalf("NewRLMOverviewBenchmarkBaseline() error = nil, want errored-case refusal")
	}
}
