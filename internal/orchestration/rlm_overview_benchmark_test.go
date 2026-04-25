package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
)

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
