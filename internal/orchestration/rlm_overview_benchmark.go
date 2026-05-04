package orchestration

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
)

type RLMOverviewBenchmarkCase struct {
	ID              string              `json:"id,omitempty"`
	RepoPath        string              `json:"repo_path"`
	Owner           string              `json:"owner,omitempty"`
	Repo            string              `json:"repo,omitempty"`
	Question        string              `json:"question"`
	GoldAnswer      string              `json:"gold_answer,omitempty"`
	ExpectedFacts   []string            `json:"expected_facts"`
	ForbiddenFacts  []string            `json:"forbidden_facts,omitempty"`
	ExpectedSources []string            `json:"expected_sources,omitempty"`
	FactAliases     map[string][]string `json:"fact_aliases,omitempty"`
	SourceAliases   map[string][]string `json:"source_aliases,omitempty"`
	Protected       bool                `json:"protected,omitempty"`
	Tags            []string            `json:"tags,omitempty"`
	Notes           string              `json:"notes,omitempty"`
}

type RLMOverviewBenchmarkSuite struct {
	Cases []RLMOverviewBenchmarkCase `json:"cases"`
}

type RLMOverviewEvaluatorConfig struct {
	ForbiddenFactPenalty  float64
	FactRecallWeight      float64
	FactPrecisionWeight   float64
	SourceCoverageWeight  float64
	SourceRecallWeight    float64
	SourcePrecisionWeight float64
	SchemaValidityWeight  float64
	TersenessWeight       float64
	MinAnswerWords        int
	MaxAnswerWords        int
}

type RLMOverviewEvaluation struct {
	Score                   float64
	ExactGroundingScore     float64
	SemanticQualityScore    float64
	FactRecall              float64
	FactPrecision           float64
	SourceCoverage          float64
	SourceRecall            float64
	SourcePrecision         float64
	SemanticFactRecall      float64
	SemanticSourceCoverage  float64
	SemanticSourceRecall    float64
	SemanticSourcePrecision float64
	ManifestSourceCoverage  float64
	EvidenceCoverage        RLMOverviewEvidenceCoverage
	RepoEvidenceCoverage    RLMOverviewEvidenceCoverage
	SchemaValid             bool
	Terseness               float64
	AnswerWords             int
	MinAnswerWords          int
	MatchedFacts            []string
	MissingFacts            []string
	SemanticMatchedFacts    []string
	SemanticMissingFacts    []string
	ForbiddenHits           []string
	CitedSources            []string
	UnexpectedSources       []string
	MatchedSources          []string
	MissingSources          []string
	SemanticMatchedSources  []string
	SemanticMissingSources  []string
	ManifestMatchedSources  []string
	Diagnostics             map[string]interface{}
}

type RLMOverviewEvidenceCoverage struct {
	FactCoverage   float64  `json:"fact_coverage"`
	SourceCoverage float64  `json:"source_coverage"`
	MatchedFacts   []string `json:"matched_facts,omitempty"`
	MissingFacts   []string `json:"missing_facts,omitempty"`
	MatchedSources []string `json:"matched_sources,omitempty"`
	MissingSources []string `json:"missing_sources,omitempty"`
}

const rlmOverviewEvaluationRubric = `RLM overview optimization uses a deterministic, repo-grounded rubric.

Inputs:
- answer: the final overview answer returned to the user
- sources: repo-relative files or package metadata that grounded the overview
- manifest context: the compact context actually provided to the RLM/direct answerer
- gold case: a frozen question with expected factual substrings, optional aliases, optional expected sources, optional forbidden substrings, and a protected-case marker

Score:
- exact_grounding_score is the legacy deterministic score based on canonical exact fact/source matching
- semantic_quality_score uses the same rubric but allows configured fact/source aliases for semantically equivalent answers
- evidence_coverage reports whether expected facts/sources were actually present in the compact manifest context
- repo_evidence_coverage reports whether expected facts/sources can be found by a broader repository scan, which helps diagnose current-manifest-vs-richer-manifest gaps
- fact_recall rewards concrete expected facts that appear in the answer
- fact_precision penalizes answers that include explicitly forbidden or hallucinated facts
- source_recall rewards expected source paths that the model actually cites in the answer
- source_precision penalizes cited source paths that are not expected for the case
- manifest_source_coverage reports expected source paths that were available in the manifest, but this is diagnostic only by default
- schema_valid rewards outputs that parsed into the requested JSON/typed shape
- terseness rewards direct answers that stay under the configured word budget without collapsing into one-word replies
- forbidden_facts subtract a fixed penalty for hallucinated or cross-repository facts

Protected cases are not scored differently here. Optimization and replay commands must use the protected marker to enforce zero-regression gates before accepting an artifact.`

func DefaultRLMOverviewEvaluatorConfig() RLMOverviewEvaluatorConfig {
	return RLMOverviewEvaluatorConfig{
		ForbiddenFactPenalty:  0.25,
		FactRecallWeight:      0.35,
		FactPrecisionWeight:   0.10,
		SourceRecallWeight:    0.20,
		SourcePrecisionWeight: 0.15,
		SchemaValidityWeight:  0.10,
		TersenessWeight:       0.10,
		MinAnswerWords:        8,
		MaxAnswerWords:        220,
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
				"fact_aliases":     cloneStringSliceMap(benchmarkCase.FactAliases),
				"source_aliases":   cloneStringSliceMap(benchmarkCase.SourceAliases),
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
	return evaluateRLMOverviewAnswerWithSchema(benchmarkCase, answer, sources, true, cfg)
}

func evaluateRLMOverviewAnswerWithSchema(benchmarkCase RLMOverviewBenchmarkCase, answer string, sources []string, schemaValid bool, cfg RLMOverviewEvaluatorConfig) RLMOverviewEvaluation {
	return evaluateRLMOverviewAnswerWithEvidence(benchmarkCase, answer, sources, "", schemaValid, cfg)
}

func evaluateRLMOverviewAnswerWithEvidence(benchmarkCase RLMOverviewBenchmarkCase, answer string, sources []string, manifestContext string, schemaValid bool, cfg RLMOverviewEvaluatorConfig) RLMOverviewEvaluation {
	cfg = normalizeRLMOverviewEvaluatorConfig(cfg)
	answer = strings.TrimSpace(answer)

	matchedFacts, missingFacts := qaMatchedFacts(answer, benchmarkCase.ExpectedFacts)
	semanticMatchedFacts, semanticMissingFacts := rlmOverviewMatchedFactsWithAliases(answer, benchmarkCase.ExpectedFacts, benchmarkCase.FactAliases)
	forbiddenHits := qaMatchedFactsOnly(answer, benchmarkCase.ForbiddenFacts)
	matchedSources, missingSources := rlmOverviewMatchedSources(answer, sources, benchmarkCase.ExpectedSources)
	semanticMatchedSources, semanticMissingSources := rlmOverviewMatchedSourcesWithAliases(answer, sources, benchmarkCase.ExpectedSources, benchmarkCase.SourceAliases)
	answerMatchedSources, _ := rlmOverviewMatchedSources(answer, nil, benchmarkCase.ExpectedSources)
	semanticAnswerMatchedSources, _ := rlmOverviewMatchedSourcesWithAliases(answer, nil, benchmarkCase.ExpectedSources, benchmarkCase.SourceAliases)
	manifestMatchedSources, _ := rlmOverviewMatchedSources("", sources, benchmarkCase.ExpectedSources)
	citedSources := extractOverviewSourceRefs(answer)
	semanticUnexpectedSources := unexpectedOverviewSourcesWithAliases(citedSources, benchmarkCase.ExpectedSources, benchmarkCase.SourceAliases)
	expectedFacts := nonEmptyStrings(benchmarkCase.ExpectedFacts)
	expectedSources := nonEmptyStrings(benchmarkCase.ExpectedSources)

	factRecall := 1.0
	if len(expectedFacts) > 0 {
		factRecall = float64(len(matchedFacts)) / float64(len(expectedFacts))
	}
	semanticFactRecall := 1.0
	if len(expectedFacts) > 0 {
		semanticFactRecall = float64(len(semanticMatchedFacts)) / float64(len(expectedFacts))
	}
	factPrecision := 1.0
	if len(matchedFacts)+len(forbiddenHits) > 0 {
		factPrecision = float64(len(matchedFacts)) / float64(len(matchedFacts)+len(forbiddenHits))
	}

	sourceCoverage := 1.0
	if len(expectedSources) > 0 {
		sourceCoverage = float64(len(matchedSources)) / float64(len(expectedSources))
	}
	semanticSourceCoverage := 1.0
	if len(expectedSources) > 0 {
		semanticSourceCoverage = float64(len(semanticMatchedSources)) / float64(len(expectedSources))
	}
	sourceRecall := 1.0
	if len(expectedSources) > 0 {
		sourceRecall = float64(len(answerMatchedSources)) / float64(len(expectedSources))
	}
	semanticSourceRecall := 1.0
	if len(expectedSources) > 0 {
		semanticSourceRecall = float64(len(semanticAnswerMatchedSources)) / float64(len(expectedSources))
	}
	manifestSourceCoverage := 1.0
	if len(expectedSources) > 0 {
		manifestSourceCoverage = float64(len(manifestMatchedSources)) / float64(len(expectedSources))
	}
	sourcePrecision := overviewSourcePrecision(citedSources, expectedSources)
	semanticSourcePrecision := overviewSourcePrecisionWithAliases(citedSources, expectedSources, benchmarkCase.SourceAliases)

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

	schemaScore := 0.0
	if schemaValid {
		schemaScore = 1.0
	}

	score := scoreRLMOverviewQuality(cfg, factRecall, factPrecision, sourceCoverage, sourceRecall, sourcePrecision, schemaScore, terseness)
	score -= float64(len(forbiddenHits)) * cfg.ForbiddenFactPenalty
	score = clampOverviewScore(score)
	semanticScore := scoreRLMOverviewQuality(cfg, semanticFactRecall, factPrecision, semanticSourceCoverage, semanticSourceRecall, semanticSourcePrecision, schemaScore, terseness)
	semanticScore -= float64(len(forbiddenHits)) * cfg.ForbiddenFactPenalty
	semanticScore = clampOverviewScore(semanticScore)
	evidenceCoverage := rlmOverviewEvidenceCoverage(benchmarkCase, manifestContext, sources)
	repoEvidenceCoverage := rlmOverviewRepoEvidenceCoverage(benchmarkCase)

	return RLMOverviewEvaluation{
		Score:                   score,
		ExactGroundingScore:     score,
		SemanticQualityScore:    semanticScore,
		FactRecall:              factRecall,
		FactPrecision:           factPrecision,
		SourceCoverage:          sourceCoverage,
		SourceRecall:            sourceRecall,
		SourcePrecision:         sourcePrecision,
		SemanticFactRecall:      semanticFactRecall,
		SemanticSourceCoverage:  semanticSourceCoverage,
		SemanticSourceRecall:    semanticSourceRecall,
		SemanticSourcePrecision: semanticSourcePrecision,
		ManifestSourceCoverage:  manifestSourceCoverage,
		EvidenceCoverage:        evidenceCoverage,
		RepoEvidenceCoverage:    repoEvidenceCoverage,
		SchemaValid:             schemaValid,
		Terseness:               terseness,
		AnswerWords:             answerWords,
		MinAnswerWords:          cfg.MinAnswerWords,
		MatchedFacts:            matchedFacts,
		MissingFacts:            missingFacts,
		SemanticMatchedFacts:    semanticMatchedFacts,
		SemanticMissingFacts:    semanticMissingFacts,
		ForbiddenHits:           forbiddenHits,
		CitedSources:            citedSources,
		UnexpectedSources:       semanticUnexpectedSources,
		MatchedSources:          matchedSources,
		MissingSources:          missingSources,
		SemanticMatchedSources:  semanticMatchedSources,
		SemanticMissingSources:  semanticMissingSources,
		ManifestMatchedSources:  manifestMatchedSources,
		Diagnostics: map[string]interface{}{
			"answer":           answer,
			"question":         benchmarkCase.Question,
			"repo_path":        benchmarkCase.RepoPath,
			"gold_answer":      benchmarkCase.GoldAnswer,
			"expected_facts":   append([]string(nil), benchmarkCase.ExpectedFacts...),
			"forbidden_facts":  append([]string(nil), benchmarkCase.ForbiddenFacts...),
			"expected_sources": append([]string(nil), benchmarkCase.ExpectedSources...),
			"fact_aliases":     cloneStringSliceMap(benchmarkCase.FactAliases),
			"source_aliases":   cloneStringSliceMap(benchmarkCase.SourceAliases),
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
	benchmarkCase.FactAliases = stringSliceMapFromAgentOutput(ex.Outputs["fact_aliases"])
	benchmarkCase.SourceAliases = stringSliceMapFromAgentOutput(ex.Outputs["source_aliases"])
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
	if cfg.FactRecallWeight <= 0 &&
		cfg.FactPrecisionWeight <= 0 &&
		cfg.SourceCoverageWeight <= 0 &&
		cfg.SourceRecallWeight <= 0 &&
		cfg.SourcePrecisionWeight <= 0 &&
		cfg.SchemaValidityWeight <= 0 &&
		cfg.TersenessWeight <= 0 {
		cfg.FactRecallWeight = defaults.FactRecallWeight
		cfg.FactPrecisionWeight = defaults.FactPrecisionWeight
		cfg.SourceCoverageWeight = defaults.SourceCoverageWeight
		cfg.SourceRecallWeight = defaults.SourceRecallWeight
		cfg.SourcePrecisionWeight = defaults.SourcePrecisionWeight
		cfg.SchemaValidityWeight = defaults.SchemaValidityWeight
		cfg.TersenessWeight = defaults.TersenessWeight
	} else {
		cfg.FactRecallWeight = clampOverviewWeight(cfg.FactRecallWeight)
		cfg.FactPrecisionWeight = clampOverviewWeight(cfg.FactPrecisionWeight)
		cfg.SourceCoverageWeight = clampOverviewWeight(cfg.SourceCoverageWeight)
		cfg.SourceRecallWeight = clampOverviewWeight(cfg.SourceRecallWeight)
		cfg.SourcePrecisionWeight = clampOverviewWeight(cfg.SourcePrecisionWeight)
		cfg.SchemaValidityWeight = clampOverviewWeight(cfg.SchemaValidityWeight)
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

func scoreRLMOverviewQuality(cfg RLMOverviewEvaluatorConfig, factRecall, factPrecision, sourceCoverage, sourceRecall, sourcePrecision, schemaScore, terseness float64) float64 {
	weightTotal := cfg.FactRecallWeight +
		cfg.FactPrecisionWeight +
		cfg.SourceCoverageWeight +
		cfg.SourceRecallWeight +
		cfg.SourcePrecisionWeight +
		cfg.SchemaValidityWeight +
		cfg.TersenessWeight
	if weightTotal <= 0 {
		return 0
	}
	return clampOverviewScore((factRecall*cfg.FactRecallWeight +
		factPrecision*cfg.FactPrecisionWeight +
		sourceCoverage*cfg.SourceCoverageWeight +
		sourceRecall*cfg.SourceRecallWeight +
		sourcePrecision*cfg.SourcePrecisionWeight +
		schemaScore*cfg.SchemaValidityWeight +
		terseness*cfg.TersenessWeight) / weightTotal)
}

func extractOverviewSourceRefs(answer string) []string {
	fields := strings.Fields(answer)
	seen := make(map[string]bool, len(fields))
	refs := make([]string, 0)
	for _, field := range fields {
		candidate := strings.Trim(field, "`'\".,;:()[]{}<>")
		candidate = filepath.ToSlash(strings.TrimSpace(candidate))
		if !looksLikeOverviewSourceRef(candidate) {
			continue
		}
		normalized := normalizeOverviewSourceText(candidate)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		refs = append(refs, candidate)
	}
	return refs
}

func looksLikeOverviewSourceRef(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "/") {
		return true
	}
	for _, exact := range []string{"go.mod", "go.sum", "makefile", "dockerfile", "readme", "readme.md"} {
		if lower == exact {
			return true
		}
	}
	for _, suffix := range []string{".go", ".md", ".json", ".yaml", ".yml", ".toml", ".sh", ".txt"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func overviewSourcePrecision(citedSources []string, expectedSources []string) float64 {
	return overviewSourcePrecisionWithAliases(citedSources, expectedSources, nil)
}

func overviewSourcePrecisionWithAliases(citedSources []string, expectedSources []string, aliases map[string][]string) float64 {
	expected := nonEmptyStrings(expectedSources)
	cited := nonEmptyStrings(citedSources)
	if len(cited) == 0 {
		if len(expected) == 0 {
			return 1.0
		}
		return 0
	}
	if len(expected) == 0 {
		return 0
	}

	matches := 0
	for _, citedSource := range cited {
		if overviewSourceMatchesAnyWithAliases(citedSource, expected, aliases) {
			matches++
		}
	}
	return float64(matches) / float64(len(cited))
}

func unexpectedOverviewSources(citedSources []string, expectedSources []string) []string {
	return unexpectedOverviewSourcesWithAliases(citedSources, expectedSources, nil)
}

func unexpectedOverviewSourcesWithAliases(citedSources []string, expectedSources []string, aliases map[string][]string) []string {
	expected := nonEmptyStrings(expectedSources)
	if len(citedSources) == 0 || len(expected) == 0 {
		return nil
	}
	unexpected := make([]string, 0)
	for _, citedSource := range citedSources {
		if !overviewSourceMatchesAnyWithAliases(citedSource, expected, aliases) {
			unexpected = append(unexpected, citedSource)
		}
	}
	return unexpected
}

func overviewSourceMatchesAny(source string, expectedSources []string) bool {
	return overviewSourceMatchesAnyWithAliases(source, expectedSources, nil)
}

func overviewSourceMatchesAnyWithAliases(source string, expectedSources []string, aliases map[string][]string) bool {
	normalizedSource := normalizeOverviewSourceText(source)
	if normalizedSource == "" {
		return false
	}
	for _, expected := range expectedSources {
		for _, candidate := range overviewExpectedSourceCandidates(expected, aliases) {
			if overviewSourceMatchesExpected(normalizedSource, candidate) {
				return true
			}
		}
	}
	return false
}

func clampOverviewWeight(weight float64) float64 {
	if weight < 0 {
		return 0
	}
	return weight
}

func rlmOverviewMatchedSources(answer string, sources []string, expectedSources []string) ([]string, []string) {
	return rlmOverviewMatchedSourcesWithAliases(answer, sources, expectedSources, nil)
}

func rlmOverviewMatchedSourcesWithAliases(answer string, sources []string, expectedSources []string, aliases map[string][]string) ([]string, []string) {
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
		if overviewAnswerOrSourcesContainSource(normalizedAnswer, normalizedSources, expected, aliases) {
			matched = append(matched, expected)
			continue
		}
		missing = append(missing, expected)
	}
	return matched, missing
}

func overviewAnswerOrSourcesContainSource(normalizedAnswer string, normalizedSources []string, expected string, aliases map[string][]string) bool {
	for _, candidate := range overviewExpectedSourceCandidates(expected, aliases) {
		normalizedCandidate := normalizeOverviewSourceText(candidate)
		if normalizedCandidate == "" {
			continue
		}
		if strings.Contains(normalizedAnswer, normalizedCandidate) || overviewSourcesContain(normalizedSources, normalizedCandidate) {
			return true
		}
	}
	return false
}

func overviewSourcesContain(sources []string, expected string) bool {
	for _, source := range sources {
		if overviewSourceMatchesExpected(source, expected) {
			return true
		}
	}
	return false
}

func overviewExpectedSourceCandidates(expected string, aliases map[string][]string) []string {
	expected = strings.TrimSpace(expected)
	candidates := make([]string, 0, 1+len(aliases[expected]))
	if expected != "" {
		candidates = append(candidates, expected)
	}
	for _, alias := range aliasesForRLMOverviewKey(expected, aliases) {
		if strings.TrimSpace(alias) != "" {
			candidates = append(candidates, alias)
		}
	}
	return candidates
}

func overviewSourceMatchesExpected(source, expected string) bool {
	normalizedSource := normalizeOverviewSourceText(source)
	normalizedExpected := normalizeOverviewSourceText(expected)
	if normalizedSource == "" || normalizedExpected == "" {
		return false
	}
	if normalizedSource == normalizedExpected {
		return true
	}
	if strings.HasPrefix(normalizedSource, normalizedExpected+"/") {
		return true
	}
	return false
}

func normalizeOverviewSourceText(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`'\".,;:()[]{}<>")
	value = filepath.ToSlash(value)
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	value = strings.TrimRight(value, "/")
	if idx := strings.IndexAny(value, "?#"); idx >= 0 {
		value = value[:idx]
	}
	if idx := strings.LastIndex(value, ":"); idx > 0 && allDigits(value[idx+1:]) {
		value = value[:idx]
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.ContainsAny(value, " \n\t") {
		return value
	}
	cleaned := filepath.ToSlash(filepath.Clean(value))
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func rlmOverviewMatchedFactsWithAliases(answer string, facts []string, aliases map[string][]string) ([]string, []string) {
	if len(facts) == 0 {
		return nil, nil
	}
	lowerAnswer := strings.ToLower(answer)
	matched := make([]string, 0, len(facts))
	missing := make([]string, 0, len(facts))
	for _, fact := range facts {
		fact = strings.TrimSpace(fact)
		if fact == "" {
			continue
		}
		if rlmOverviewTextContainsFactCandidate(lowerAnswer, fact) {
			matched = append(matched, fact)
			continue
		}
		aliasMatched := false
		for _, alias := range aliasesForRLMOverviewKey(fact, aliases) {
			if rlmOverviewTextContainsFactCandidate(lowerAnswer, alias) {
				aliasMatched = true
				break
			}
		}
		if aliasMatched {
			matched = append(matched, fact)
			continue
		}
		missing = append(missing, fact)
	}
	return matched, missing
}

func rlmOverviewTextContainsFactCandidate(lowerText, candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	return strings.Contains(lowerText, strings.ToLower(candidate))
}

func rlmOverviewEvidenceCoverage(benchmarkCase RLMOverviewBenchmarkCase, manifestContext string, sources []string) RLMOverviewEvidenceCoverage {
	evidenceText := strings.TrimSpace(manifestContext)
	if len(sources) > 0 {
		evidenceText = strings.TrimSpace(evidenceText + "\n" + strings.Join(sources, "\n"))
	}
	matchedFacts, missingFacts := rlmOverviewMatchedFactsWithAliases(evidenceText, benchmarkCase.ExpectedFacts, benchmarkCase.FactAliases)
	matchedSources, missingSources := rlmOverviewMatchedSourcesWithAliases(evidenceText, sources, benchmarkCase.ExpectedSources, benchmarkCase.SourceAliases)
	return rlmOverviewCoverageFromMatches(benchmarkCase, matchedFacts, missingFacts, matchedSources, missingSources)
}

func rlmOverviewRepoEvidenceCoverage(benchmarkCase RLMOverviewBenchmarkCase) RLMOverviewEvidenceCoverage {
	repoPath := strings.TrimSpace(benchmarkCase.RepoPath)
	if repoPath == "" {
		return rlmOverviewCoverageFromMatches(benchmarkCase, nil, nonEmptyStrings(benchmarkCase.ExpectedFacts), nil, nonEmptyStrings(benchmarkCase.ExpectedSources))
	}
	info, err := os.Stat(repoPath)
	if err != nil || !info.IsDir() {
		return rlmOverviewCoverageFromMatches(benchmarkCase, nil, nonEmptyStrings(benchmarkCase.ExpectedFacts), nil, nonEmptyStrings(benchmarkCase.ExpectedSources))
	}
	repoEvidence := rlmOverviewRepoEvidenceText(repoPath)

	matchedFacts := make([]string, 0, len(benchmarkCase.ExpectedFacts))
	missingFacts := make([]string, 0, len(benchmarkCase.ExpectedFacts))
	for _, fact := range nonEmptyStrings(benchmarkCase.ExpectedFacts) {
		if rlmOverviewRepoContainsAnyCandidate(repoPath, repoEvidence, fact, benchmarkCase.FactAliases) {
			matchedFacts = append(matchedFacts, fact)
		} else {
			missingFacts = append(missingFacts, fact)
		}
	}

	matchedSources := make([]string, 0, len(benchmarkCase.ExpectedSources))
	missingSources := make([]string, 0, len(benchmarkCase.ExpectedSources))
	for _, source := range nonEmptyStrings(benchmarkCase.ExpectedSources) {
		if rlmOverviewRepoContainsAnyCandidate(repoPath, repoEvidence, source, benchmarkCase.SourceAliases) {
			matchedSources = append(matchedSources, source)
		} else {
			missingSources = append(missingSources, source)
		}
	}
	return rlmOverviewCoverageFromMatches(benchmarkCase, matchedFacts, missingFacts, matchedSources, missingSources)
}

func rlmOverviewCoverageFromMatches(benchmarkCase RLMOverviewBenchmarkCase, matchedFacts, missingFacts, matchedSources, missingSources []string) RLMOverviewEvidenceCoverage {
	coverage := RLMOverviewEvidenceCoverage{
		MatchedFacts:   append([]string(nil), matchedFacts...),
		MissingFacts:   append([]string(nil), missingFacts...),
		MatchedSources: append([]string(nil), matchedSources...),
		MissingSources: append([]string(nil), missingSources...),
		FactCoverage:   1,
		SourceCoverage: 1,
	}
	expectedFacts := nonEmptyStrings(benchmarkCase.ExpectedFacts)
	if len(expectedFacts) > 0 {
		coverage.FactCoverage = float64(len(matchedFacts)) / float64(len(expectedFacts))
	}
	expectedSources := nonEmptyStrings(benchmarkCase.ExpectedSources)
	if len(expectedSources) > 0 {
		coverage.SourceCoverage = float64(len(matchedSources)) / float64(len(expectedSources))
	}
	return coverage
}

func rlmOverviewRepoContainsAnyCandidate(repoPath, repoEvidence, expected string, aliases map[string][]string) bool {
	for _, candidate := range append([]string{expected}, aliasesForRLMOverviewKey(expected, aliases)...) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if rlmOverviewRepoPathExists(repoPath, candidate) {
			return true
		}
		if strings.Contains(repoEvidence, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func rlmOverviewRepoPathExists(repoPath, candidate string) bool {
	candidate = normalizeOverviewSourceText(candidate)
	if candidate == "" || strings.ContainsAny(candidate, " \n\t") {
		return false
	}
	if _, err := os.Stat(filepath.Join(repoPath, filepath.FromSlash(candidate))); err == nil {
		return true
	}
	return false
}

func rlmOverviewRepoEvidenceText(repoPath string) string {
	const maxTotalBytes = 5 << 20
	var builder strings.Builder
	_ = filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || builder.Len() >= maxTotalBytes {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".hg", ".svn", "node_modules", "vendor", "benchmark_results", "tmp", ".tmp", "scratch":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		rel, relErr := filepath.Rel(repoPath, path)
		if relErr != nil || !rlmOverviewEvidenceFile(name) {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > 1<<20 {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		remaining := maxTotalBytes - builder.Len()
		if remaining <= 0 {
			return nil
		}
		text := filepath.ToSlash(rel) + "\n" + string(data) + "\n"
		if len(text) > remaining {
			text = text[:remaining]
		}
		builder.WriteString(strings.ToLower(text))
		return nil
	})
	return builder.String()
}

func rlmOverviewEvidenceFile(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "go.mod", "go.sum", "makefile", "dockerfile", "readme", "license":
		return true
	}
	for _, suffix := range []string{".go", ".md", ".txt", ".json", ".toml", ".yaml", ".yml", ".sh"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func aliasesForRLMOverviewKey(key string, aliases map[string][]string) []string {
	if len(aliases) == 0 {
		return nil
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if values, ok := aliases[key]; ok {
		return append([]string(nil), values...)
	}
	normalizedKey := normalizeOverviewSourceText(key)
	for aliasKey, values := range aliases {
		if normalizeOverviewSourceText(aliasKey) == normalizedKey {
			return append([]string(nil), values...)
		}
	}
	return nil
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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

func stringSliceMapFromAgentOutput(value interface{}) map[string][]string {
	switch typed := value.(type) {
	case map[string][]string:
		return cloneStringSliceMap(typed)
	case map[string]interface{}:
		result := make(map[string][]string, len(typed))
		for key, raw := range typed {
			values := stringsFromAgentOutput(raw)
			if len(values) > 0 {
				result[strings.TrimSpace(key)] = values
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	default:
		return nil
	}
}

func cloneStringSliceMap(src map[string][]string) map[string][]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string][]string, len(src))
	for key, values := range src {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		copied := make([]string, 0, len(values))
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				copied = append(copied, value)
			}
		}
		if len(copied) > 0 {
			dst[key] = copied
		}
	}
	if len(dst) == 0 {
		return nil
	}
	return dst
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
