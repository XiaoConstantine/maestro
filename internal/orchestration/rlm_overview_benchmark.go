package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
)

type RLMOverviewBenchmarkCase struct {
	ID              string   `json:"id,omitempty"`
	RepoPath        string   `json:"repo_path"`
	Owner           string   `json:"owner,omitempty"`
	Repo            string   `json:"repo,omitempty"`
	Question        string   `json:"question"`
	GoldAnswer      string   `json:"gold_answer,omitempty"`
	ExpectedFacts   []string `json:"expected_facts"`
	ForbiddenFacts  []string `json:"forbidden_facts,omitempty"`
	ExpectedSources []string `json:"expected_sources,omitempty"`
	Protected       bool     `json:"protected,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Notes           string   `json:"notes,omitempty"`
}

type RLMOverviewBenchmarkSuite struct {
	Cases []RLMOverviewBenchmarkCase `json:"cases"`
}

type RLMOverviewEvaluatorConfig struct {
	ForbiddenFactPenalty float64
	FactRecallWeight     float64
	SourceCoverageWeight float64
	TersenessWeight      float64
	MinAnswerWords       int
	MaxAnswerWords       int
}

type RLMOverviewEvaluation struct {
	Score          float64
	FactRecall     float64
	SourceCoverage float64
	Terseness      float64
	AnswerWords    int
	MinAnswerWords int
	MatchedFacts   []string
	MissingFacts   []string
	ForbiddenHits  []string
	MatchedSources []string
	MissingSources []string
	Diagnostics    map[string]interface{}
}

const rlmOverviewEvaluationRubric = `RLM overview optimization uses a deterministic, repo-grounded rubric.

Inputs:
- answer: the final overview answer returned to the user
- sources: repo-relative files or package metadata that grounded the overview
- gold case: a frozen question with expected factual substrings, optional expected sources, optional forbidden substrings, and a protected-case marker

Score:
- fact_recall rewards concrete expected facts that appear in the answer
- source_coverage rewards expected source paths that appear either in returned sources or in the answer
- terseness rewards direct answers that stay under the configured word budget without collapsing into one-word replies
- forbidden_facts subtract a fixed penalty for hallucinated or cross-repository facts

Protected cases are not scored differently here. Optimization and replay commands must use the protected marker to enforce zero-regression gates before accepting an artifact.`

func DefaultRLMOverviewEvaluatorConfig() RLMOverviewEvaluatorConfig {
	return RLMOverviewEvaluatorConfig{
		ForbiddenFactPenalty: 0.25,
		FactRecallWeight:     0.70,
		SourceCoverageWeight: 0.20,
		TersenessWeight:      0.10,
		MinAnswerWords:       8,
		MaxAnswerWords:       220,
	}
}

func RLMOverviewEvaluationRubric() string {
	return rlmOverviewEvaluationRubric
}

func LoadRLMOverviewBenchmarkSuite(path string) ([]RLMOverviewBenchmarkCase, error) {
	resolvedPath, err := expandBenchmarkPath(path, "")
	if err != nil {
		return nil, fmt.Errorf("resolve RLM overview benchmark suite path %q: %w", path, err)
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("read RLM overview benchmark suite %q: %w", resolvedPath, err)
	}

	var suite RLMOverviewBenchmarkSuite
	if err := json.Unmarshal(data, &suite); err == nil && len(suite.Cases) > 0 {
		return normalizeRLMOverviewBenchmarkSuitePaths(filepath.Dir(resolvedPath), suite.Cases)
	}

	var cases []RLMOverviewBenchmarkCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("decode RLM overview benchmark suite %q: %w", resolvedPath, err)
	}
	return normalizeRLMOverviewBenchmarkSuitePaths(filepath.Dir(resolvedPath), cases)
}

func normalizeRLMOverviewBenchmarkSuitePaths(baseDir string, cases []RLMOverviewBenchmarkCase) ([]RLMOverviewBenchmarkCase, error) {
	normalized := make([]RLMOverviewBenchmarkCase, 0, len(cases))
	for _, benchmarkCase := range cases {
		if benchmarkCase.RepoPath != "" {
			resolvedPath, err := expandBenchmarkPath(benchmarkCase.RepoPath, baseDir)
			if err != nil {
				return nil, fmt.Errorf("resolve repo_path for RLM overview benchmark case %q: %w", benchmarkCase.ID, err)
			}
			benchmarkCase.RepoPath = resolvedPath
		}
		normalized = append(normalized, benchmarkCase)
	}
	return normalized, nil
}

func RLMOverviewBenchmarkExamples(cases []RLMOverviewBenchmarkCase) []optimize.AgentExample {
	examples := make([]optimize.AgentExample, 0, len(cases))
	for i, benchmarkCase := range cases {
		id := strings.TrimSpace(benchmarkCase.ID)
		if id == "" {
			id = fmt.Sprintf("rlm-overview-case-%d", i+1)
		}
		examples = append(examples, optimize.AgentExample{
			ID: id,
			Inputs: map[string]interface{}{
				"repo_path": benchmarkCase.RepoPath,
				"owner":     benchmarkCase.Owner,
				"repo":      benchmarkCase.Repo,
				"question":  benchmarkCase.Question,
			},
			Outputs: map[string]interface{}{
				"gold_answer":      benchmarkCase.GoldAnswer,
				"expected_facts":   append([]string(nil), benchmarkCase.ExpectedFacts...),
				"forbidden_facts":  append([]string(nil), benchmarkCase.ForbiddenFacts...),
				"expected_sources": append([]string(nil), benchmarkCase.ExpectedSources...),
			},
			Metadata: map[string]interface{}{
				"rlm_overview_case": benchmarkCase,
				"protected":         benchmarkCase.Protected,
				"tags":              append([]string(nil), benchmarkCase.Tags...),
			},
		})
	}
	return examples
}

func EvaluateRLMOverviewAnswer(benchmarkCase RLMOverviewBenchmarkCase, answer string, sources []string, cfg RLMOverviewEvaluatorConfig) RLMOverviewEvaluation {
	cfg = normalizeRLMOverviewEvaluatorConfig(cfg)
	answer = strings.TrimSpace(answer)

	matchedFacts, missingFacts := qaMatchedFacts(answer, benchmarkCase.ExpectedFacts)
	forbiddenHits := qaMatchedFactsOnly(answer, benchmarkCase.ForbiddenFacts)
	matchedSources, missingSources := rlmOverviewMatchedSources(answer, sources, benchmarkCase.ExpectedSources)
	expectedFacts := nonEmptyStrings(benchmarkCase.ExpectedFacts)
	expectedSources := nonEmptyStrings(benchmarkCase.ExpectedSources)

	factRecall := 1.0
	if len(expectedFacts) > 0 {
		factRecall = float64(len(matchedFacts)) / float64(len(expectedFacts))
	}

	sourceCoverage := 1.0
	if len(expectedSources) > 0 {
		sourceCoverage = float64(len(matchedSources)) / float64(len(expectedSources))
	}

	answerWords := countOverviewWords(answer)
	terseness := 0.0
	if answerWords > 0 {
		terseness = 1.0
		if cfg.MinAnswerWords > 0 && answerWords < cfg.MinAnswerWords {
			terseness *= float64(answerWords) / float64(cfg.MinAnswerWords)
		}
		if cfg.MaxAnswerWords > 0 && answerWords > cfg.MaxAnswerWords {
			terseness *= float64(cfg.MaxAnswerWords) / float64(answerWords)
		}
	}
	terseness = clampOverviewScore(terseness)

	weightTotal := cfg.FactRecallWeight + cfg.SourceCoverageWeight + cfg.TersenessWeight
	score := 0.0
	if weightTotal > 0 {
		score = (factRecall*cfg.FactRecallWeight + sourceCoverage*cfg.SourceCoverageWeight + terseness*cfg.TersenessWeight) / weightTotal
	}
	score -= float64(len(forbiddenHits)) * cfg.ForbiddenFactPenalty
	score = clampOverviewScore(score)

	return RLMOverviewEvaluation{
		Score:          score,
		FactRecall:     factRecall,
		SourceCoverage: sourceCoverage,
		Terseness:      terseness,
		AnswerWords:    answerWords,
		MinAnswerWords: cfg.MinAnswerWords,
		MatchedFacts:   matchedFacts,
		MissingFacts:   missingFacts,
		ForbiddenHits:  forbiddenHits,
		MatchedSources: matchedSources,
		MissingSources: missingSources,
		Diagnostics: map[string]interface{}{
			"answer":           answer,
			"question":         benchmarkCase.Question,
			"repo_path":        benchmarkCase.RepoPath,
			"gold_answer":      benchmarkCase.GoldAnswer,
			"expected_facts":   append([]string(nil), benchmarkCase.ExpectedFacts...),
			"forbidden_facts":  append([]string(nil), benchmarkCase.ForbiddenFacts...),
			"expected_sources": append([]string(nil), benchmarkCase.ExpectedSources...),
			"protected":        benchmarkCase.Protected,
			"tags":             append([]string(nil), benchmarkCase.Tags...),
		},
	}
}

func rlmOverviewCaseFromExample(ex optimize.AgentExample) (RLMOverviewBenchmarkCase, error) {
	if raw, ok := ex.Metadata["rlm_overview_case"]; ok {
		if benchmarkCase, ok := raw.(RLMOverviewBenchmarkCase); ok {
			return benchmarkCase, nil
		}
		if benchmarkCase, err := decodeRLMOverviewBenchmarkCase(raw); err == nil {
			return benchmarkCase, nil
		}
	}

	benchmarkCase := RLMOverviewBenchmarkCase{
		ID:         ex.ID,
		RepoPath:   strings.TrimSpace(stringValue(ex.Inputs["repo_path"])),
		Owner:      strings.TrimSpace(stringValue(ex.Inputs["owner"])),
		Repo:       strings.TrimSpace(stringValue(ex.Inputs["repo"])),
		Question:   strings.TrimSpace(stringValue(ex.Inputs["question"])),
		GoldAnswer: strings.TrimSpace(stringValue(ex.Outputs["gold_answer"])),
	}
	if benchmarkCase.RepoPath == "" || benchmarkCase.Question == "" {
		return RLMOverviewBenchmarkCase{}, fmt.Errorf("RLM overview benchmark example %q missing repo_path or question", ex.ID)
	}
	benchmarkCase.ExpectedFacts = stringsFromAgentOutput(ex.Outputs["expected_facts"])
	benchmarkCase.ForbiddenFacts = stringsFromAgentOutput(ex.Outputs["forbidden_facts"])
	benchmarkCase.ExpectedSources = stringsFromAgentOutput(ex.Outputs["expected_sources"])
	if protected, ok := ex.Metadata["protected"].(bool); ok {
		benchmarkCase.Protected = protected
	}
	benchmarkCase.Tags = stringsFromAgentOutput(ex.Metadata["tags"])
	return benchmarkCase, nil
}

func decodeRLMOverviewBenchmarkCase(raw interface{}) (RLMOverviewBenchmarkCase, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return RLMOverviewBenchmarkCase{}, err
	}
	var benchmarkCase RLMOverviewBenchmarkCase
	if err := json.Unmarshal(data, &benchmarkCase); err != nil {
		return RLMOverviewBenchmarkCase{}, err
	}
	return benchmarkCase, nil
}

func normalizeRLMOverviewEvaluatorConfig(cfg RLMOverviewEvaluatorConfig) RLMOverviewEvaluatorConfig {
	defaults := DefaultRLMOverviewEvaluatorConfig()
	if cfg.ForbiddenFactPenalty <= 0 {
		cfg.ForbiddenFactPenalty = defaults.ForbiddenFactPenalty
	}
	if cfg.FactRecallWeight <= 0 && cfg.SourceCoverageWeight <= 0 && cfg.TersenessWeight <= 0 {
		cfg.FactRecallWeight = defaults.FactRecallWeight
		cfg.SourceCoverageWeight = defaults.SourceCoverageWeight
		cfg.TersenessWeight = defaults.TersenessWeight
	} else {
		cfg.FactRecallWeight = clampOverviewWeight(cfg.FactRecallWeight)
		cfg.SourceCoverageWeight = clampOverviewWeight(cfg.SourceCoverageWeight)
		cfg.TersenessWeight = clampOverviewWeight(cfg.TersenessWeight)
	}
	if cfg.MinAnswerWords <= 0 {
		cfg.MinAnswerWords = defaults.MinAnswerWords
	}
	if cfg.MaxAnswerWords <= 0 {
		cfg.MaxAnswerWords = defaults.MaxAnswerWords
	}
	return cfg
}

func clampOverviewWeight(weight float64) float64 {
	if weight < 0 {
		return 0
	}
	return weight
}

func rlmOverviewMatchedSources(answer string, sources []string, expectedSources []string) ([]string, []string) {
	if len(expectedSources) == 0 {
		return nil, nil
	}
	normalizedAnswer := normalizeOverviewSourceText(answer)
	normalizedSources := make([]string, 0, len(sources))
	for _, source := range sources {
		source = normalizeOverviewSourceText(source)
		if source != "" {
			normalizedSources = append(normalizedSources, source)
		}
	}

	matched := make([]string, 0, len(expectedSources))
	missing := make([]string, 0, len(expectedSources))
	for _, expected := range expectedSources {
		expected = strings.TrimSpace(expected)
		if expected == "" {
			continue
		}
		normalizedExpected := normalizeOverviewSourceText(expected)
		if strings.Contains(normalizedAnswer, normalizedExpected) || overviewSourcesContain(normalizedSources, normalizedExpected) {
			matched = append(matched, expected)
			continue
		}
		missing = append(missing, expected)
	}
	return matched, missing
}

func overviewSourcesContain(sources []string, expected string) bool {
	for _, source := range sources {
		if source == expected || strings.Contains(source, expected) {
			return true
		}
	}
	return false
}

func normalizeOverviewSourceText(value string) string {
	return strings.ToLower(filepath.ToSlash(strings.TrimSpace(value)))
}

func stringsFromAgentOutput(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []interface{}:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if value := strings.TrimSpace(stringValue(item)); value != "" {
				result = append(result, value)
			}
		}
		return result
	default:
		return nil
	}
}

func nonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func countOverviewWords(value string) int {
	return len(strings.Fields(value))
}

func clampOverviewScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}
