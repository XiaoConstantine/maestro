package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	internalgithub "github.com/XiaoConstantine/maestro/internal/github"
	maestrotypes "github.com/XiaoConstantine/maestro/internal/types"
)

type ReviewBenchmarkLabel string

const (
	ReviewBenchmarkAccepted   ReviewBenchmarkLabel = "accepted"
	ReviewBenchmarkDiscussion ReviewBenchmarkLabel = "discussion"
	ReviewBenchmarkAmbiguous  ReviewBenchmarkLabel = "ambiguous"
	ReviewBenchmarkNegative   ReviewBenchmarkLabel = "negative"

	reviewBenchmarkDefaultLineSlack = maestrotypes.DefaultCommentHunkLineSlack
)

type ReviewBenchmarkCase struct {
	ID               string               `json:"id,omitempty"`
	Project          string               `json:"project"`
	ChangeNumber     int                  `json:"change_number"`
	Subject          string               `json:"subject"`
	ReviewerEmail    string               `json:"reviewer_email,omitempty"`
	PatchSet         int                  `json:"patch_set"`
	NextPatchSet     int                  `json:"next_patch_set,omitempty"`
	BeforeCommit     string               `json:"before_commit,omitempty"`
	AfterCommit      string               `json:"after_commit,omitempty"`
	FilePath         string               `json:"file_path"`
	FileContent      string               `json:"file_content"`
	Diff             string               `json:"diff"`
	Line             int                  `json:"line,omitempty"`
	ReviewerComment  string               `json:"reviewer_comment,omitempty"`
	Label            ReviewBenchmarkLabel `json:"label"`
	PatchExcerpt     string               `json:"patch_excerpt,omitempty"`
	ExpectedKeywords []string             `json:"expected_keywords,omitempty"`
}

type ReviewBenchmarkSuite struct {
	Cases []ReviewBenchmarkCase `json:"cases"`
}

type ReviewBenchmarkEvaluatorConfig struct {
	LineSlack            int
	FalsePositivePenalty float64
	DuplicatePenalty     float64
	OffHunkPenalty       float64
	NegativeCasePenalty  float64
	AcceptedCaseWeight   float64
	MatchedScoreFloor    float64
}

type reviewBenchmarkEvaluator struct {
	cfg ReviewBenchmarkEvaluatorConfig
}

type ReviewBenchmarkEvaluation struct {
	Comments    []PRReviewComment
	Score       float64
	RawScore    float64
	Diagnostics map[string]interface{}
}

func DefaultReviewBenchmarkEvaluatorConfig() ReviewBenchmarkEvaluatorConfig {
	return ReviewBenchmarkEvaluatorConfig{
		LineSlack:            reviewBenchmarkDefaultLineSlack,
		FalsePositivePenalty: 0.30,
		DuplicatePenalty:     0.20,
		OffHunkPenalty:       0.20,
		NegativeCasePenalty:  0.35,
		AcceptedCaseWeight:   2.5,
		MatchedScoreFloor:    0.10,
	}
}

func NewReviewBenchmarkEvaluator(cfg ReviewBenchmarkEvaluatorConfig) optimize.AgentEvaluator {
	return &reviewBenchmarkEvaluator{cfg: normalizeReviewBenchmarkEvaluatorConfig(cfg)}
}

func LoadReviewBenchmarkSuite(path string) ([]ReviewBenchmarkCase, error) {
	resolvedPath, err := expandReviewPath(path)
	if err != nil {
		return nil, fmt.Errorf("resolve review benchmark suite path %q: %w", path, err)
	}
	if strings.TrimSpace(resolvedPath) == "" {
		return nil, fmt.Errorf("benchmark suite path is required")
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("read review benchmark suite %q: %w", resolvedPath, err)
	}

	var suite ReviewBenchmarkSuite
	if err := json.Unmarshal(data, &suite); err == nil && len(suite.Cases) > 0 {
		return suite.Cases, nil
	}

	var cases []ReviewBenchmarkCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("decode review benchmark suite %q: %w", resolvedPath, err)
	}
	return cases, nil
}

func ReviewBenchmarkExamples(cases []ReviewBenchmarkCase) []optimize.AgentExample {
	examples := make([]optimize.AgentExample, 0, len(cases))
	for i, benchmarkCase := range cases {
		if benchmarkCase.Label != ReviewBenchmarkAccepted && benchmarkCase.Label != ReviewBenchmarkNegative {
			continue
		}
		if benchmarkCase.Label == ReviewBenchmarkAccepted {
			if !reviewBenchmarkAcceptedCaseHasCodeSignal(benchmarkCase) {
				continue
			}
			if !reviewBenchmarkAcceptedCaseHasActionableReviewerComment(benchmarkCase) {
				continue
			}
		}
		id := strings.TrimSpace(benchmarkCase.ID)
		if id == "" {
			id = fmt.Sprintf("review-case-%d", i+1)
		}
		examples = append(examples, optimize.AgentExample{
			ID: id,
			Inputs: map[string]interface{}{
				"benchmark_case": benchmarkCase,
			},
			Outputs: map[string]interface{}{
				"label":             string(benchmarkCase.Label),
				"expected_keywords": append([]string(nil), benchmarkCase.ExpectedKeywords...),
			},
			Metadata: map[string]interface{}{
				"review_case": benchmarkCase,
			},
		})
	}
	return examples
}

func reviewBenchmarkAcceptedCaseHasCodeSignal(benchmarkCase ReviewBenchmarkCase) bool {
	if strings.TrimSpace(benchmarkCase.Diff) == "" {
		return true
	}

	for _, line := range strings.Split(benchmarkCase.Diff, "\n") {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			continue
		}
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "+"))
		if trimmed == "" {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "//"),
			strings.HasPrefix(trimmed, "/*"),
			strings.HasPrefix(trimmed, "*"),
			strings.HasPrefix(trimmed, "*/"):
			continue
		default:
			return true
		}
	}

	return false
}

func reviewBenchmarkAcceptedCaseHasActionableReviewerComment(benchmarkCase ReviewBenchmarkCase) bool {
	ok, _ := reviewBenchmarkAcceptedReviewerCommentDisposition(benchmarkCase.ReviewerComment)
	return ok
}

func reviewBenchmarkAcceptedReviewerCommentDisposition(message string) (bool, string) {
	comment := normalizeReviewBenchmarkText(message)
	if comment == "" {
		return true, ""
	}

	for _, marker := range []string{
		"not necessarily a problem",
		"i wonder if",
		"for consistency",
	} {
		if strings.Contains(comment, marker) {
			return false, "speculative_reviewer_comment"
		}
	}

	for _, marker := range []string{
		"doc comment",
		"godoc",
	} {
		if strings.Contains(comment, marker) {
			return false, "doc_comment_request"
		}
	}

	if strings.HasPrefix(comment, "minor ") || strings.HasPrefix(comment, "minor perhaps ") {
		return false, "wording_or_clarity_tweak"
	}
	for _, marker := range []string{
		"clearer to write this as a positive statement",
		"rather than an unless",
		"for clarity",
		"hard time parsing",
		"much nicer to read",
		"line up in the output",
		"seems pedantic",
		"seems slightly clearer to me to write",
		"slightly clearer to me to write",
		"cleanly separates looping logic from body of loop",
		"wording",
	} {
		if strings.Contains(comment, marker) {
			return false, "wording_or_clarity_tweak"
		}
	}

	for _, marker := range []string{
		"i would prefer to address this",
		"prefer to address this in a slightly more general way",
		"i d rather be explicit",
		"was there any discussion about",
		"proposal says",
	} {
		if strings.Contains(comment, marker) {
			return false, "design_preference_or_api_discussion"
		}
	}

	if len(comment) >= 240 {
		for _, marker := range []string{
			"i disagree with you",
			"i agree that it s fine",
			"i personally find",
			"point of view",
			"i may or may not agree",
		} {
			if strings.Contains(comment, marker) {
				return false, "long_discussion_reply"
			}
		}
	}

	return true, ""
}

func ReviewBenchmarkCaseFromAgentExample(ex optimize.AgentExample) (ReviewBenchmarkCase, error) {
	if raw, ok := ex.Metadata["review_case"]; ok {
		if benchmarkCase, ok := raw.(ReviewBenchmarkCase); ok {
			return benchmarkCase, nil
		}
	}
	if raw, ok := ex.Inputs["benchmark_case"]; ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return ReviewBenchmarkCase{}, err
		}
		var benchmarkCase ReviewBenchmarkCase
		if err := json.Unmarshal(data, &benchmarkCase); err != nil {
			return ReviewBenchmarkCase{}, err
		}
		return benchmarkCase, nil
	}
	return ReviewBenchmarkCase{}, fmt.Errorf("review benchmark example %q missing benchmark_case", ex.ID)
}

func reviewBenchmarkCaseFromExample(ex optimize.AgentExample) (ReviewBenchmarkCase, error) {
	return ReviewBenchmarkCaseFromAgentExample(ex)
}

func (e *reviewBenchmarkEvaluator) Evaluate(ctx context.Context, agent optimize.OptimizableAgent, ex optimize.AgentExample) (*optimize.EvalResult, error) {
	benchmarkCase, err := reviewBenchmarkCaseFromExample(ex)
	if err != nil {
		return nil, err
	}

	startedAt := time.Now()
	result, execErr := agent.Execute(ctx, map[string]interface{}{
		"benchmark_case": benchmarkCase,
	})
	latencyMS := float64(time.Since(startedAt)) / float64(time.Millisecond)

	hunks, err := internalgithub.ParseHunks(benchmarkCase.Diff, benchmarkCase.FilePath)
	if err != nil {
		return nil, fmt.Errorf("parse benchmark hunks for %q: %w", ex.ID, err)
	}
	evaluation := e.evaluateExecutedResult(benchmarkCase, hunks, result)
	rawScore, score := evaluation.RawScore, evaluation.Score
	scores, diagnostics := evaluation.scores(), evaluation.diagnosticsWithCopy()
	diagnostics["raw_score"] = rawScore
	diagnostics["case_weight"] = e.caseWeight(benchmarkCase)
	diagnostics["weighted_score"] = score
	if result != nil {
		if rawCandidates, ok := reviewBenchmarkIntResult(result, "raw_candidates"); ok {
			diagnostics["raw_candidates"] = rawCandidates
		}
		if preVerificationCount, ok := reviewBenchmarkIntResult(result, "pre_verification_count"); ok {
			diagnostics["pre_verification_count"] = preVerificationCount
		}
		if skippedAfterFilter, ok := reviewBenchmarkIntResult(result, "skipped_after_filter"); ok {
			diagnostics["skipped_after_filter"] = skippedAfterFilter
		}
		if totalChunks, ok := reviewBenchmarkIntResult(result, "total_chunks"); ok {
			diagnostics["total_chunks"] = totalChunks
		}
		if selectedChunks, ok := reviewBenchmarkIntResult(result, "selected_chunks"); ok {
			diagnostics["selected_chunks"] = selectedChunks
		}
		for _, key := range []string{"filter_drop_reasons", "filter_rejections", "verification_enabled", "verification_dropped", "verification_drop_reasons", "verification_rejections"} {
			if value, ok := result[key]; ok && value != nil {
				diagnostics[key] = value
			}
		}
	}
	if execErr != nil {
		diagnostics["evaluation_error"] = execErr.Error()
	}

	return &optimize.EvalResult{
		Score: score,
		SideInfo: &optimize.SideInfo{
			LatencyMS:   latencyMS,
			Scores:      scores,
			Diagnostics: diagnostics,
		},
	}, nil
}

func EvaluateReviewBenchmarkResult(benchmarkCase ReviewBenchmarkCase, result map[string]interface{}, cfg ReviewBenchmarkEvaluatorConfig) (*ReviewBenchmarkEvaluation, error) {
	hunks, err := internalgithub.ParseHunks(benchmarkCase.Diff, benchmarkCase.FilePath)
	if err != nil {
		return nil, fmt.Errorf("parse benchmark hunks for %q: %w", benchmarkCase.FilePath, err)
	}
	evaluator := &reviewBenchmarkEvaluator{cfg: normalizeReviewBenchmarkEvaluatorConfig(cfg)}
	evaluation := evaluator.evaluateExecutedResult(benchmarkCase, hunks, result)
	return &ReviewBenchmarkEvaluation{
		Comments:    append([]PRReviewComment(nil), evaluation.Comments...),
		Score:       evaluation.Score,
		RawScore:    evaluation.RawScore,
		Diagnostics: evaluation.diagnosticsWithCopy(),
	}, nil
}

type reviewBenchmarkExecutionEvaluation struct {
	Comments    []PRReviewComment
	Score       float64
	RawScore    float64
	metrics     map[string]float64
	Diagnostics map[string]interface{}
}

func (e *reviewBenchmarkExecutionEvaluation) scores() map[string]float64 {
	if e == nil || len(e.metrics) == 0 {
		return nil
	}
	scores := make(map[string]float64, len(e.metrics))
	for key, value := range e.metrics {
		scores[key] = value
	}
	return scores
}

func (e *reviewBenchmarkExecutionEvaluation) diagnosticsWithCopy() map[string]interface{} {
	if e == nil || len(e.Diagnostics) == 0 {
		return map[string]interface{}{}
	}
	diagnostics := make(map[string]interface{}, len(e.Diagnostics))
	for key, value := range e.Diagnostics {
		diagnostics[key] = value
	}
	return diagnostics
}

func (e *reviewBenchmarkEvaluator) evaluateExecutedResult(benchmarkCase ReviewBenchmarkCase, hunks []ChangeHunk, result map[string]interface{}) *reviewBenchmarkExecutionEvaluation {
	comments := reviewBenchmarkCommentsFromResult(result)
	rawScore, score, scores, diagnostics := e.scoreCase(benchmarkCase, hunks, comments)
	return &reviewBenchmarkExecutionEvaluation{
		Comments:    comments,
		Score:       score,
		RawScore:    rawScore,
		metrics:     scores,
		Diagnostics: diagnostics,
	}
}

func (e *reviewBenchmarkEvaluator) scoreCase(benchmarkCase ReviewBenchmarkCase, hunks []ChangeHunk, comments []PRReviewComment) (float64, float64, map[string]float64, map[string]interface{}) {
	normalizedKeywords := reviewBenchmarkKeywords(benchmarkCase.ExpectedKeywords, benchmarkCase.ReviewerComment)

	offHunkCount := 0
	duplicateCount := 0
	seenFingerprints := make(map[string]int)
	matched := false
	matchedComment := ""

	for _, comment := range comments {
		fingerprint := strings.Join([]string{
			comment.FilePath,
			fmt.Sprintf("%d", comment.LineNumber),
			normalizeReviewBenchmarkText(comment.Content),
		}, "|")
		seenFingerprints[fingerprint]++
		if seenFingerprints[fingerprint] > 1 {
			duplicateCount++
		}
		if comment.LineNumber > 0 && len(hunks) > 0 && !maestrotypes.CommentInChangedHunks(comment, hunks, e.cfg.LineSlack) {
			offHunkCount++
		}
		if !matched && reviewBenchmarkMatch(benchmarkCase, normalizedKeywords, hunks, comment, e.cfg.LineSlack) {
			matched = true
			matchedComment = comment.Content
		}
	}

	precision := 0.0
	recall := 0.0
	falsePositiveCount := len(comments)

	switch benchmarkCase.Label {
	case ReviewBenchmarkNegative:
		rawScore := 1.0 - float64(len(comments))*e.cfg.NegativeCasePenalty
		if rawScore < 0 {
			rawScore = 0
		}
		precision = 1.0
		f1 := 1.0
		if len(comments) > 0 {
			precision = 0
			f1 = 0
		}
		weightedScore := e.weightScore(benchmarkCase, rawScore)
		return rawScore, weightedScore, map[string]float64{
				"raw_score":      rawScore,
				"weighted_score": weightedScore,
				"precision":      precision,
				"recall":         1,
				"f1":             f1,
			}, map[string]interface{}{
				"label":             benchmarkCase.Label,
				"comment_count":     len(comments),
				"false_positives":   len(comments),
				"matched_comment":   "",
				"expected_keywords": normalizedKeywords,
			}
	default:
		if matched {
			precision = 1.0 / float64(maxInt(1, len(comments)))
			recall = 1.0
			falsePositiveCount = len(comments) - 1
		}
		rawScore := 0.65*precision + 0.35*recall
		rawScore -= float64(falsePositiveCount) * e.cfg.FalsePositivePenalty
		rawScore -= float64(duplicateCount) * e.cfg.DuplicatePenalty
		rawScore -= float64(offHunkCount) * e.cfg.OffHunkPenalty
		if rawScore < 0 {
			rawScore = 0
		}
		if matched && rawScore < e.cfg.MatchedScoreFloor {
			rawScore = e.cfg.MatchedScoreFloor
		}
		if rawScore > 1 {
			rawScore = 1
		}
		f1 := 0.0
		if precision+recall > 0 {
			f1 = (2 * precision * recall) / (precision + recall)
		}
		weightedScore := e.weightScore(benchmarkCase, rawScore)
		return rawScore, weightedScore, map[string]float64{
				"raw_score":      rawScore,
				"weighted_score": weightedScore,
				"precision":      precision,
				"recall":         recall,
				"f1":             f1,
			}, map[string]interface{}{
				"label":             benchmarkCase.Label,
				"comment_count":     len(comments),
				"false_positives":   falsePositiveCount,
				"duplicate_count":   duplicateCount,
				"off_hunk_count":    offHunkCount,
				"matched":           matched,
				"matched_comment":   matchedComment,
				"expected_keywords": normalizedKeywords,
			}
	}
}

func (e *reviewBenchmarkEvaluator) caseWeight(benchmarkCase ReviewBenchmarkCase) float64 {
	if benchmarkCase.Label == ReviewBenchmarkAccepted && e.cfg.AcceptedCaseWeight > 0 {
		return e.cfg.AcceptedCaseWeight
	}
	return 1.0
}

func (e *reviewBenchmarkEvaluator) weightScore(benchmarkCase ReviewBenchmarkCase, rawScore float64) float64 {
	weightedScore := rawScore * e.caseWeight(benchmarkCase)
	if weightedScore < 0 {
		return 0
	}
	if weightedScore > 1 {
		return 1
	}
	return weightedScore
}

func reviewBenchmarkCommentsFromResult(result map[string]interface{}) []PRReviewComment {
	if result == nil {
		return nil
	}
	if typed, ok := result["comments"].([]PRReviewComment); ok {
		return typed
	}
	if typed, ok := result["comments"].([]interface{}); ok {
		data, err := json.Marshal(typed)
		if err == nil {
			var comments []PRReviewComment
			if err := json.Unmarshal(data, &comments); err == nil {
				return comments
			}
		}
	}
	return nil
}

func normalizeReviewBenchmarkEvaluatorConfig(cfg ReviewBenchmarkEvaluatorConfig) ReviewBenchmarkEvaluatorConfig {
	defaults := DefaultReviewBenchmarkEvaluatorConfig()
	if cfg.LineSlack <= 0 {
		cfg = defaults
	}
	if cfg.AcceptedCaseWeight <= 0 {
		cfg.AcceptedCaseWeight = defaults.AcceptedCaseWeight
	}
	if cfg.MatchedScoreFloor < 0 {
		cfg.MatchedScoreFloor = defaults.MatchedScoreFloor
	}
	return cfg
}

func reviewBenchmarkIntResult(result map[string]interface{}, key string) (int, bool) {
	if result == nil {
		return 0, false
	}
	switch value := result[key].(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}

func reviewBenchmarkKeywords(keywords []string, fallback string) []string {
	normalized := make([]string, 0, len(keywords))
	seen := make(map[string]struct{})
	appendKeyword := func(value string) {
		value = normalizeReviewBenchmarkText(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}

	for _, keyword := range keywords {
		appendKeyword(keyword)
	}
	if len(normalized) > 0 {
		return normalized
	}

	for _, token := range strings.Fields(strings.ToLower(fallback)) {
		token = strings.Trim(token, ".,:;!?`()[]{}<>\"'")
		if len(token) < 3 || reviewBenchmarkStopWord(token) {
			continue
		}
		appendKeyword(token)
		if len(normalized) >= 5 {
			break
		}
	}
	sort.Strings(normalized)
	return normalized
}

func reviewBenchmarkMatch(benchmarkCase ReviewBenchmarkCase, keywords []string, hunks []ChangeHunk, comment PRReviewComment, lineSlack int) bool {
	if strings.TrimSpace(comment.FilePath) != strings.TrimSpace(benchmarkCase.FilePath) {
		return false
	}
	if benchmarkCase.Line > 0 {
		if comment.LineNumber <= 0 || absInt(comment.LineNumber-benchmarkCase.Line) > lineSlack {
			return false
		}
	}
	if len(hunks) > 0 && comment.LineNumber > 0 && !maestrotypes.CommentInChangedHunks(comment, hunks, lineSlack) {
		return false
	}
	commentText := normalizeReviewBenchmarkText(comment.Content + " " + comment.Suggestion)
	if commentText == "" {
		return false
	}
	for _, keyword := range keywords {
		if strings.Contains(commentText, keyword) {
			return true
		}
	}
	for _, phrase := range reviewBenchmarkEquivalentPhrases(benchmarkCase.ReviewerComment) {
		if strings.Contains(commentText, phrase) {
			return true
		}
	}
	reviewerComment := normalizeReviewBenchmarkText(benchmarkCase.ReviewerComment)
	return reviewerComment != "" && strings.Contains(reviewerComment, commentText)
}

func reviewBenchmarkEquivalentPhrases(reviewerComment string) []string {
	switch normalizeReviewBenchmarkText(reviewerComment) {
	case "use t setenv", "use setenv":
		return []string{
			"environment variable",
			"environment variables",
			"process wide state",
			"restore the original environment",
			"restore the environment",
			"later tests",
			"scope the change to this test",
		}
	default:
		return nil
	}
}

func normalizeReviewBenchmarkText(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func reviewBenchmarkStopWord(token string) bool {
	switch token {
	case "add", "all", "and", "any", "are", "but", "can", "code", "for", "from", "get", "have", "here", "how", "into", "its", "just", "let", "line", "lines", "may", "need", "needs", "new", "not", "now", "old", "one", "out", "see", "set", "should", "that", "the", "then", "there", "this", "too", "use", "was", "way", "what", "when", "why", "with", "would", "yet", "your", "could":
		return true
	default:
		return false
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
