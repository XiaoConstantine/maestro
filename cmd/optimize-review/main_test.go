package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/maestro/internal/review"
)

func TestResolveOptimizationModels_DefaultsTeacherToStudent(t *testing.T) {
	student, teacher, err := resolveOptimizationModels("ollama:qwen3:32b", "", "google", "gemini-3.0-pro", "", "", "", "")
	if err != nil {
		t.Fatalf("resolveOptimizationModels() error = %v", err)
	}

	if student.ID != core.ModelID("ollama:qwen3:32b") {
		t.Fatalf("student.ID = %q, want %q", student.ID, "ollama:qwen3:32b")
	}
	if teacher.ID != student.ID {
		t.Fatalf("teacher.ID = %q, want %q", teacher.ID, student.ID)
	}
	if teacher.Config.ModelProvider != student.Config.ModelProvider || teacher.Config.ModelName != student.Config.ModelName || teacher.Config.ModelConfig != student.Config.ModelConfig {
		t.Fatalf("teacher config = %#v, want same effective config as student %#v", teacher.Config, student.Config)
	}
}

func TestResolveOptimizationModels_UsesTeacherOverride(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-google-key")

	student, teacher, err := resolveOptimizationModels("ollama:qwen3:32b", "google:gemini-3-pro-preview", "google", "gemini-3.0-pro", "", "", "", "")
	if err != nil {
		t.Fatalf("resolveOptimizationModels() error = %v", err)
	}

	if student.ID != core.ModelID("ollama:qwen3:32b") {
		t.Fatalf("student.ID = %q, want %q", student.ID, "ollama:qwen3:32b")
	}
	if teacher.ID != core.ModelID("gemini-3-pro-preview") {
		t.Fatalf("teacher.ID = %q, want %q", teacher.ID, "gemini-3-pro-preview")
	}
	if teacher.Config.ModelProvider != "google" || teacher.Config.ModelName != "gemini-3-pro-preview" {
		t.Fatalf("teacher config = %#v, want google/gemini-3-pro-preview", teacher.Config)
	}
}

func TestResolveOptimizationModels_RejectsInvalidTeacherSpec(t *testing.T) {
	if _, _, err := resolveOptimizationModels("ollama:qwen3:32b", "bad:spec:with:too:many:parts", "google", "gemini-3.0-pro", "", "", "", ""); err == nil {
		t.Fatalf("resolveOptimizationModels() error = nil, want invalid teacher model specification")
	}
}

func TestResolveOptimizationModels_UsesSeparateBaseURLs(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-google-key")

	student, teacher, err := resolveOptimizationModels(
		"llamacpp:qwen3.5-9b",
		"google:gemini-3-pro-preview",
		"google",
		"gemini-3.0-pro",
		"",
		"",
		"http://127.0.0.1:8081",
		"",
	)
	if err != nil {
		t.Fatalf("resolveOptimizationModels() error = %v", err)
	}

	if student.Config.BaseURL != "http://127.0.0.1:8081" {
		t.Fatalf("student.Config.BaseURL = %q, want %q", student.Config.BaseURL, "http://127.0.0.1:8081")
	}
	if teacher.Config.BaseURL != "" {
		t.Fatalf("teacher.Config.BaseURL = %q, want empty for external teacher", teacher.Config.BaseURL)
	}
}

func TestLoadReviewSuites_SplitsEachSuiteIndependently(t *testing.T) {
	dir := t.TempDir()
	firstSuite := filepath.Join(dir, "rsc.json")
	secondSuite := filepath.Join(dir, "iant.json")

	writeReviewSuiteFile(t, firstSuite, benchmarkCases("rsc"))
	writeReviewSuiteFile(t, secondSuite, benchmarkCases("iant"))

	suites, training, validation, err := loadReviewSuites([]string{firstSuite, secondSuite}, 0.25, 0)
	if err != nil {
		t.Fatalf("loadReviewSuites() error = %v", err)
	}
	if len(suites) != 2 {
		t.Fatalf("len(suites) = %d, want 2", len(suites))
	}
	if len(training) != 6 {
		t.Fatalf("len(training) = %d, want 6", len(training))
	}
	if len(validation) != 2 {
		t.Fatalf("len(validation) = %d, want 2", len(validation))
	}
	for _, suite := range suites {
		if len(suite.TrainingExamples) != 3 {
			t.Fatalf("suite %q training count = %d, want 3", suite.Path, len(suite.TrainingExamples))
		}
		if len(suite.ValidationExamples) != 1 {
			t.Fatalf("suite %q validation count = %d, want 1", suite.Path, len(suite.ValidationExamples))
		}
	}
}

func TestSplitAgentExamples_StratifesValidationAcrossLabels(t *testing.T) {
	examples := review.ReviewBenchmarkExamples(benchmarkCases("mix"))
	training, validation, err := splitAgentExamples(examples, 0.5)
	if err != nil {
		t.Fatalf("splitAgentExamples() error = %v", err)
	}
	if len(training) != 2 || len(validation) != 2 {
		t.Fatalf("training=%d validation=%d, want 2/2", len(training), len(validation))
	}
	labelCounts := make(map[string]int)
	for _, example := range validation {
		labelCounts[example.Outputs["label"].(string)]++
	}
	if labelCounts[string(review.ReviewBenchmarkAccepted)] != 1 {
		t.Fatalf("accepted validation count = %d, want 1", labelCounts[string(review.ReviewBenchmarkAccepted)])
	}
	if labelCounts[string(review.ReviewBenchmarkNegative)] != 1 {
		t.Fatalf("negative validation count = %d, want 1", labelCounts[string(review.ReviewBenchmarkNegative)])
	}
}

func TestSplitAgentExamples_ReservesOneValidationExamplePerLabelWhenPossible(t *testing.T) {
	cases := []review.ReviewBenchmarkCase{
		{ID: "accepted-1", Label: review.ReviewBenchmarkAccepted, FilePath: "src/runtime/a.go", Diff: "@@ -1,1 +1,1 @@\n+value := ptr\n"},
		{ID: "accepted-2", Label: review.ReviewBenchmarkAccepted, FilePath: "src/runtime/b.go", Diff: "@@ -1,1 +1,1 @@\n+value := ptr\n"},
		{ID: "negative-1", Label: review.ReviewBenchmarkNegative, FilePath: "src/runtime/c.go", Diff: "@@ -1,1 +1,1 @@\n+value := ptr\n"},
		{ID: "negative-2", Label: review.ReviewBenchmarkNegative, FilePath: "src/runtime/d.go", Diff: "@@ -1,1 +1,1 @@\n+value := ptr\n"},
		{ID: "negative-3", Label: review.ReviewBenchmarkNegative, FilePath: "src/runtime/e.go", Diff: "@@ -1,1 +1,1 @@\n+value := ptr\n"},
		{ID: "negative-4", Label: review.ReviewBenchmarkNegative, FilePath: "src/runtime/f.go", Diff: "@@ -1,1 +1,1 @@\n+value := ptr\n"},
		{ID: "negative-5", Label: review.ReviewBenchmarkNegative, FilePath: "src/runtime/g.go", Diff: "@@ -1,1 +1,1 @@\n+value := ptr\n"},
		{ID: "negative-6", Label: review.ReviewBenchmarkNegative, FilePath: "src/runtime/h.go", Diff: "@@ -1,1 +1,1 @@\n+value := ptr\n"},
	}

	examples := review.ReviewBenchmarkExamples(cases)
	_, validation, err := splitAgentExamples(examples, 0.25)
	if err != nil {
		t.Fatalf("splitAgentExamples() error = %v", err)
	}
	if len(validation) != 2 {
		t.Fatalf("len(validation) = %d, want 2", len(validation))
	}

	labelCounts := make(map[string]int)
	for _, example := range validation {
		labelCounts[example.Outputs["label"].(string)]++
	}
	if labelCounts[string(review.ReviewBenchmarkAccepted)] != 1 {
		t.Fatalf("accepted validation count = %d, want 1", labelCounts[string(review.ReviewBenchmarkAccepted)])
	}
	if labelCounts[string(review.ReviewBenchmarkNegative)] != 1 {
		t.Fatalf("negative validation count = %d, want 1", labelCounts[string(review.ReviewBenchmarkNegative)])
	}
}

func TestSuitesImproved_RequiresEverySuiteToImprove(t *testing.T) {
	if suitesImproved(map[string]suiteMetrics{
		"rsc":  {BaselineValidation: 0.4, BestValidation: 0.6},
		"iant": {BaselineValidation: 0.5, BestValidation: 0.5},
	}) {
		t.Fatalf("expected a non-improving suite to fail publication gating")
	}
}

func TestValidationCaseReportFromEvalResult_ExtractsDiagnostics(t *testing.T) {
	example := review.ReviewBenchmarkExamples([]review.ReviewBenchmarkCase{{
		ID:       "accepted-1",
		Label:    review.ReviewBenchmarkAccepted,
		FilePath: "src/runtime/a.go",
		Line:     42,
	}})[0]
	result := &optimize.EvalResult{
		Score: 0.625,
		SideInfo: &optimize.SideInfo{
			LatencyMS: 18.5,
			Diagnostics: map[string]interface{}{
				"raw_score":              0.25,
				"case_weight":            2.5,
				"comment_count":          2,
				"raw_candidates":         4,
				"pre_verification_count": 3,
				"skipped_after_filter":   1,
				"filter_drop_reasons": map[string]interface{}{
					"before_first_hunk": 1.0,
				},
				"filter_rejections": []map[string]interface{}{
					{
						"file_path":   "src/runtime/a.go",
						"line_number": 9.0,
						"reason_code": "before_first_hunk",
						"content":     "wrong line",
					},
				},
				"total_chunks":         6,
				"selected_chunks":      3,
				"matched":              true,
				"matched_comment":      "needs nil check",
				"verification_enabled": true,
				"verification_dropped": 2,
				"verification_drop_reasons": map[string]interface{}{
					"content_check": 2.0,
				},
				"verification_rejections": []map[string]interface{}{
					{
						"id":          "c2",
						"reason_code": "content_check",
						"reason":      "code contradicts the finding",
					},
				},
			},
		},
	}

	report := validationCaseReportFromEvalResult(example, result)
	if report.ID != "accepted-1" {
		t.Fatalf("ID = %q, want accepted-1", report.ID)
	}
	if report.Label != string(review.ReviewBenchmarkAccepted) {
		t.Fatalf("Label = %q, want %q", report.Label, review.ReviewBenchmarkAccepted)
	}
	if report.FilePath != "src/runtime/a.go" || report.Line != 42 {
		t.Fatalf("case location = %s:%d, want src/runtime/a.go:42", report.FilePath, report.Line)
	}
	if report.Score != 0.625 || report.LatencyMS != 18.5 {
		t.Fatalf("score/latency = %.3f/%.1f, want 0.625/18.5", report.Score, report.LatencyMS)
	}
	if report.RawScore != 0.25 || report.CaseWeight != 2.5 {
		t.Fatalf("raw score/case weight = %.3f/%.1f, want 0.25/2.5", report.RawScore, report.CaseWeight)
	}
	if report.CommentCount != 2 || report.RawCandidates != 4 || report.PreVerificationCount != 3 || report.SkippedAfterFilter != 1 || report.TotalChunks != 6 || report.SelectedChunks != 3 {
		t.Fatalf("report = %#v, want diagnostics to be copied", report)
	}
	if report.FilterDropReasons["before_first_hunk"] != 1 {
		t.Fatalf("FilterDropReasons = %#v, want before_first_hunk=1", report.FilterDropReasons)
	}
	if len(report.FilterRejections) != 1 || report.FilterRejections[0].ReasonCode != "before_first_hunk" {
		t.Fatalf("FilterRejections = %#v, want one before_first_hunk rejection", report.FilterRejections)
	}
	if !report.Matched || report.MatchedComment != "needs nil check" {
		t.Fatalf("matched report = %#v, want matched comment extracted", report)
	}
	if !report.VerificationEnabled || report.VerificationDropped != 2 {
		t.Fatalf("verification report = %#v, want verification diagnostics copied", report)
	}
	if report.VerificationDropReasons["content_check"] != 2 {
		t.Fatalf("VerificationDropReasons = %#v, want content_check=2", report.VerificationDropReasons)
	}
	if len(report.VerificationRejections) != 1 || report.VerificationRejections[0].ReasonCode != "content_check" {
		t.Fatalf("VerificationRejections = %#v, want one content_check rejection", report.VerificationRejections)
	}
}

func writeReviewSuiteFile(t *testing.T, path string, cases []review.ReviewBenchmarkCase) {
	t.Helper()
	data, err := json.Marshal(struct {
		Cases []review.ReviewBenchmarkCase `json:"cases"`
	}{Cases: cases})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func benchmarkCases(prefix string) []review.ReviewBenchmarkCase {
	return []review.ReviewBenchmarkCase{
		{ID: prefix + "-accepted-1", Label: review.ReviewBenchmarkAccepted, FilePath: "src/runtime/a.go", Diff: "@@ -1,1 +1,2 @@\n+value := ptr\n", ReviewerComment: "add nil check"},
		{ID: prefix + "-accepted-2", Label: review.ReviewBenchmarkAccepted, FilePath: "src/runtime/b.go", Diff: "@@ -1,1 +1,2 @@\n+value := ptr\n", ReviewerComment: "rename helper"},
		{ID: prefix + "-negative-1", Label: review.ReviewBenchmarkNegative, FilePath: "src/runtime/c.go", Diff: "@@ -1,1 +1,1 @@\n+value := ptr\n"},
		{ID: prefix + "-negative-2", Label: review.ReviewBenchmarkNegative, FilePath: "src/runtime/d.go", Diff: "@@ -1,1 +1,1 @@\n+value := ptr\n"},
	}
}
