package review

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

type fakeReviewBenchmarkAgent struct {
	comments             []PRReviewComment
	rawCandidates        int
	preVerificationCount int
	skippedAfterFilter   int
	totalChunks          int
}

func (a *fakeReviewBenchmarkAgent) Execute(context.Context, map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{
		"comments":               a.comments,
		"raw_candidates":         a.rawCandidates,
		"pre_verification_count": a.preVerificationCount,
		"skipped_after_filter":   a.skippedAfterFilter,
		"total_chunks":           a.totalChunks,
	}, nil
}

func (a *fakeReviewBenchmarkAgent) GetCapabilities() []core.Tool { return nil }

func (a *fakeReviewBenchmarkAgent) GetMemory() agents.Memory { return nil }

func (a *fakeReviewBenchmarkAgent) GetArtifacts() optimize.AgentArtifacts {
	return optimize.AgentArtifacts{}
}

func (a *fakeReviewBenchmarkAgent) SetArtifacts(optimize.AgentArtifacts) error { return nil }

func (a *fakeReviewBenchmarkAgent) Clone() (optimize.OptimizableAgent, error) {
	return &fakeReviewBenchmarkAgent{
		comments:             append([]PRReviewComment(nil), a.comments...),
		rawCandidates:        a.rawCandidates,
		preVerificationCount: a.preVerificationCount,
		skippedAfterFilter:   a.skippedAfterFilter,
		totalChunks:          a.totalChunks,
	}, nil
}

func TestReviewBenchmarkEvaluatorScoresMatchingFinding(t *testing.T) {
	evaluator := NewReviewBenchmarkEvaluator(DefaultReviewBenchmarkEvaluatorConfig())
	agent := &fakeReviewBenchmarkAgent{
		comments: []PRReviewComment{{
			FilePath:   "src/runtime/foo.go",
			LineNumber: 11,
			Content:    "Add a nil check before dereferencing the value.",
			Severity:   "high",
			Category:   "bug",
		}},
	}

	example := ReviewBenchmarkExamples([]ReviewBenchmarkCase{{
		ID:               "c1",
		FilePath:         "src/runtime/foo.go",
		FileContent:      "package runtime\nfunc f(){}\n",
		Diff:             "@@ -10,1 +10,2 @@\n+value := ptr\n+_ = value\n",
		Line:             11,
		ReviewerComment:  "This needs a nil check before dereferencing.",
		Label:            ReviewBenchmarkAccepted,
		ExpectedKeywords: []string{"nil", "check", "dereferencing"},
	}})[0]

	result, err := evaluator.Evaluate(context.Background(), agent, example)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Score <= 0.3 {
		t.Fatalf("score = %.4f, want strong positive score", result.Score)
	}
}

func TestReviewBenchmarkEvaluatorWeightsAcceptedCases(t *testing.T) {
	evaluator := NewReviewBenchmarkEvaluator(ReviewBenchmarkEvaluatorConfig{
		LineSlack:            reviewBenchmarkDefaultLineSlack,
		FalsePositivePenalty: 0.30,
		DuplicatePenalty:     0.20,
		OffHunkPenalty:       0.20,
		NegativeCasePenalty:  0.35,
		AcceptedCaseWeight:   2.0,
	})
	agent := &fakeReviewBenchmarkAgent{
		comments: []PRReviewComment{
			{
				FilePath:   "src/runtime/foo.go",
				LineNumber: 11,
				Content:    "Add a nil check before dereferencing the value.",
				Severity:   "high",
				Category:   "bug",
			},
			{
				FilePath:   "src/runtime/foo.go",
				LineNumber: 11,
				Content:    "Rename this helper while you are here.",
				Severity:   "low",
				Category:   "style",
			},
		},
	}

	example := ReviewBenchmarkExamples([]ReviewBenchmarkCase{{
		ID:               "weighted-accepted",
		FilePath:         "src/runtime/foo.go",
		FileContent:      "package runtime\nfunc f(){}\n",
		Diff:             "@@ -10,1 +10,2 @@\n+value := ptr\n+_ = value\n",
		Line:             11,
		ReviewerComment:  "This needs a nil check before dereferencing.",
		Label:            ReviewBenchmarkAccepted,
		ExpectedKeywords: []string{"nil", "check", "dereferencing"},
	}})[0]

	result, err := evaluator.Evaluate(context.Background(), agent, example)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	rawScore, ok := result.SideInfo.Diagnostics["raw_score"].(float64)
	if !ok {
		t.Fatalf("raw_score diagnostic missing: %#v", result.SideInfo.Diagnostics)
	}
	if diff := rawScore - 0.375; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("raw_score = %.6f, want 0.375", rawScore)
	}
	if diff := result.Score - 0.75; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("weighted score = %.6f, want 0.75", result.Score)
	}
	if got := result.SideInfo.Diagnostics["case_weight"]; got != 2.0 {
		t.Fatalf("case_weight = %#v, want 2.0", got)
	}
}

func TestReviewBenchmarkEvaluatorAppliesMatchedScoreFloor(t *testing.T) {
	evaluator := NewReviewBenchmarkEvaluator(ReviewBenchmarkEvaluatorConfig{
		LineSlack:            reviewBenchmarkDefaultLineSlack,
		FalsePositivePenalty: 0.30,
		DuplicatePenalty:     0.20,
		OffHunkPenalty:       0.20,
		NegativeCasePenalty:  0.35,
		AcceptedCaseWeight:   1.0,
		MatchedScoreFloor:    0.10,
	})
	agent := &fakeReviewBenchmarkAgent{
		comments: []PRReviewComment{
			{
				FilePath:   "src/runtime/foo.go",
				LineNumber: 11,
				Content:    "Add a nil check before dereferencing the value.",
				Severity:   "high",
				Category:   "bug",
			},
			{
				FilePath:   "src/runtime/foo.go",
				LineNumber: 11,
				Content:    "Rename this helper while you are here.",
				Severity:   "low",
				Category:   "style",
			},
			{
				FilePath:   "src/runtime/foo.go",
				LineNumber: 11,
				Content:    "Consider adding another test as well.",
				Severity:   "low",
				Category:   "style",
			},
			{
				FilePath:   "src/runtime/foo.go",
				LineNumber: 11,
				Content:    "Document this behavior more clearly.",
				Severity:   "low",
				Category:   "style",
			},
		},
	}

	example := ReviewBenchmarkExamples([]ReviewBenchmarkCase{{
		ID:               "matched-floor",
		FilePath:         "src/runtime/foo.go",
		FileContent:      "package runtime\nfunc f(){}\n",
		Diff:             "@@ -10,1 +10,2 @@\n+value := ptr\n+_ = value\n",
		Line:             11,
		ReviewerComment:  "This needs a nil check before dereferencing.",
		Label:            ReviewBenchmarkAccepted,
		ExpectedKeywords: []string{"nil", "check", "dereferencing"},
	}})[0]

	result, err := evaluator.Evaluate(context.Background(), agent, example)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	rawScore, ok := result.SideInfo.Diagnostics["raw_score"].(float64)
	if !ok {
		t.Fatalf("raw_score diagnostic missing: %#v", result.SideInfo.Diagnostics)
	}
	if diff := rawScore - 0.10; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("raw_score = %.6f, want floor 0.10", rawScore)
	}
	if diff := result.Score - 0.10; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("weighted score = %.6f, want 0.10", result.Score)
	}
}

func TestReviewBenchmarkMatch_AllowsEquivalentPhraseForUseTSetenv(t *testing.T) {
	benchmarkCase := ReviewBenchmarkCase{
		FilePath:        "src/crypto/internal/cryptotest/fetchmodule.go",
		Line:            35,
		ReviewerComment: "Use t.Setenv.",
	}
	comment := PRReviewComment{
		FilePath:   benchmarkCase.FilePath,
		LineNumber: 33,
		Content:    "The helper permanently mutates process-wide GO environment variables (`GOMODCACHE` and `GOFLAGS`) when the default cache is unavailable, which can leak into later tests and accumulate duplicate `-modcacherw` flags on repeated calls.",
		Suggestion: "Prefer setting the environment only for the invoked `go` command (if supported by `testenv.Command`) or restore the original environment after the download completes.",
	}
	hunks := []ChangeHunk{{StartLine: 30, EndLine: 40}}
	keywords := reviewBenchmarkKeywords(nil, benchmarkCase.ReviewerComment)

	if !reviewBenchmarkMatch(benchmarkCase, keywords, hunks, comment, reviewBenchmarkDefaultLineSlack) {
		t.Fatalf("reviewBenchmarkMatch() = false, want equivalent-phrase environment leak match")
	}
}

func TestReviewBenchmarkMatch_DoesNotMatchUnrelatedSameLineComment(t *testing.T) {
	benchmarkCase := ReviewBenchmarkCase{
		FilePath:        "src/crypto/internal/cryptotest/fetchmodule.go",
		Line:            35,
		ReviewerComment: "Use t.Setenv.",
	}
	comment := PRReviewComment{
		FilePath:   benchmarkCase.FilePath,
		LineNumber: 34,
		Content:    "Rename this helper so the intent is easier to follow.",
		Suggestion: "The current name is too vague.",
	}
	hunks := []ChangeHunk{{StartLine: 30, EndLine: 40}}
	keywords := reviewBenchmarkKeywords(nil, benchmarkCase.ReviewerComment)

	if reviewBenchmarkMatch(benchmarkCase, keywords, hunks, comment, reviewBenchmarkDefaultLineSlack) {
		t.Fatalf("reviewBenchmarkMatch() = true, want unrelated style comment to stay unmatched")
	}
}

func TestReviewBenchmarkEvaluatorPenalizesNegativeCaseFindings(t *testing.T) {
	evaluator := NewReviewBenchmarkEvaluator(DefaultReviewBenchmarkEvaluatorConfig())
	agent := &fakeReviewBenchmarkAgent{
		comments: []PRReviewComment{{
			FilePath:   "src/runtime/foo.go",
			LineNumber: 12,
			Content:    "Generic style suggestion.",
			Severity:   "low",
			Category:   "style",
		}},
	}

	example := ReviewBenchmarkExamples([]ReviewBenchmarkCase{{
		ID:          "c2",
		FilePath:    "src/runtime/foo.go",
		FileContent: "package runtime\nfunc f(){}\n",
		Diff:        "@@ -10,1 +10,1 @@\n+value := ptr\n",
		Label:       ReviewBenchmarkNegative,
	}})[0]

	result, err := evaluator.Evaluate(context.Background(), agent, example)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Score >= 1 {
		t.Fatalf("score = %.4f, want penalty for false positive", result.Score)
	}
}

func TestReviewBenchmarkSkillOverlay_AppendsFewShotDemos(t *testing.T) {
	overlay := reviewBenchmarkSkillOverlay(optimize.AgentArtifacts{
		Text: map[optimize.ArtifactKey]string{
			optimize.ArtifactSkillPack: "Prefer changed-hunk grounded findings.",
			ArtifactFewShotDemos:       "Example 1\n- line 10: add a nil check",
		},
	})

	if !strings.Contains(overlay, "Prefer changed-hunk grounded findings.") {
		t.Fatalf("overlay = %q, want skill pack content", overlay)
	}
	if !strings.Contains(overlay, "## Examples of good reviews:") {
		t.Fatalf("overlay = %q, want examples section", overlay)
	}
	if !strings.Contains(overlay, "Example 1") {
		t.Fatalf("overlay = %q, want demo content", overlay)
	}
}

func TestReviewBenchmarkSkillOverlay_UsesDemosWhenSkillPackEmpty(t *testing.T) {
	overlay := reviewBenchmarkSkillOverlay(optimize.AgentArtifacts{
		Text: map[optimize.ArtifactKey]string{
			ArtifactFewShotDemos: "Example 1\n- line 10: add a nil check",
		},
	})

	if overlay != "## Examples of good reviews:\nExample 1\n- line 10: add a nil check" {
		t.Fatalf("overlay = %q, want demos-only overlay", overlay)
	}
}

func TestReviewBenchmarkExamples_FilterAcceptedAndNegativeCases(t *testing.T) {
	examples := ReviewBenchmarkExamples([]ReviewBenchmarkCase{
		{
			ID:    "accepted",
			Label: ReviewBenchmarkAccepted,
			Diff:  "@@ -10,1 +10,1 @@\n+value := ptr\n",
		},
		{ID: "negative", Label: ReviewBenchmarkNegative},
		{ID: "discussion", Label: ReviewBenchmarkDiscussion},
	})
	if len(examples) != 2 {
		t.Fatalf("len(examples) = %d, want 2", len(examples))
	}
}

func TestReviewBenchmarkExamples_SkipsAcceptedCommentOnlyCases(t *testing.T) {
	examples := ReviewBenchmarkExamples([]ReviewBenchmarkCase{
		{
			ID:    "accepted-comment-only",
			Label: ReviewBenchmarkAccepted,
			Diff:  "@@ -10,1 +10,2 @@\n+// document the behavior\n+// more docs\n",
		},
		{
			ID:    "accepted-code",
			Label: ReviewBenchmarkAccepted,
			Diff:  "@@ -10,1 +10,1 @@\n+value := ptr\n",
		},
		{
			ID:    "negative-comment-only",
			Label: ReviewBenchmarkNegative,
			Diff:  "@@ -10,1 +10,2 @@\n+// document the behavior\n+// more docs\n",
		},
	})

	if len(examples) != 2 {
		t.Fatalf("len(examples) = %d, want 2", len(examples))
	}
	if examples[0].ID != "accepted-code" {
		t.Fatalf("examples[0].ID = %q, want accepted code case", examples[0].ID)
	}
	if examples[1].ID != "negative-comment-only" {
		t.Fatalf("examples[1].ID = %q, want negative case preserved", examples[1].ID)
	}
}

func TestReviewBenchmarkExamples_SkipsAcceptedSpeculativeReviewComments(t *testing.T) {
	examples := ReviewBenchmarkExamples([]ReviewBenchmarkCase{
		{
			ID:              "accepted-softened",
			Label:           ReviewBenchmarkAccepted,
			Diff:            "@@ -10,1 +10,1 @@\n+value := ptr\n",
			ReviewerComment: "It's not necessarily a problem for this CL, but I wonder if this should match the Unix version for consistency.",
		},
		{
			ID:              "accepted-actionable",
			Label:           ReviewBenchmarkAccepted,
			Diff:            "@@ -10,1 +10,1 @@\n+value := ptr\n",
			ReviewerComment: "Add a nil check before dereferencing ptr.",
		},
		{
			ID:              "negative-softened",
			Label:           ReviewBenchmarkNegative,
			Diff:            "@@ -10,1 +10,1 @@\n+value := ptr\n",
			ReviewerComment: "I wonder if this should match the Unix version for consistency.",
		},
	})

	if len(examples) != 2 {
		t.Fatalf("len(examples) = %d, want 2", len(examples))
	}
	if examples[0].ID != "accepted-actionable" {
		t.Fatalf("examples[0].ID = %q, want accepted actionable case", examples[0].ID)
	}
	if examples[1].ID != "negative-softened" {
		t.Fatalf("examples[1].ID = %q, want negative case preserved", examples[1].ID)
	}
}

func TestReviewBenchmarkAcceptedReviewerCommentDisposition(t *testing.T) {
	tests := []struct {
		name    string
		message string
		wantOK  bool
		wantWhy string
	}{
		{
			name:    "doc comment request",
			message: "This function needs a doc comment.",
			wantOK:  false,
			wantWhy: "doc_comment_request",
		},
		{
			name:    "minor wording tweak",
			message: "Minor: perhaps \"as in this CaseInsensitive example\". Saying below seems pedantic when it immediately follows.",
			wantOK:  false,
			wantWhy: "wording_or_clarity_tweak",
		},
		{
			name:    "positive statement wording suggestion",
			message: "I think it's clearer to write this as a positive statement, rather than an \"unless\". Something like If a signal causes the returned context to be canceled, calling [context.Cause] will return an error describing the signal.",
			wantOK:  false,
			wantWhy: "wording_or_clarity_tweak",
		},
		{
			name:    "nit clearer loop structure",
			message: "Nit: Seems slightly clearer to me to write len := 0; for ; n != 0; n >>= 1 { len++ }; return len because it cleanly separates looping logic from body of loop.",
			wantOK:  false,
			wantWhy: "wording_or_clarity_tweak",
		},
		{
			name:    "design preference discussion",
			message: "The proposal says ScanColumn(dest any, index int) error. Was there any discussion about swapping the argument order?",
			wantOK:  false,
			wantWhy: "design_preference_or_api_discussion",
		},
		{
			name:    "long discussion reply",
			message: "I disagree with you about pointless. The standard says these things because older, bad implementations made these mistakes, and they want to make sure that no matter what else happens, those specific mistakes will not be repeated. This is the reason we write any tests at all. It is just that these tests are run-time tests, not separate go test tests. In that sense they are like the PCTs. I may or may not agree with each instance, but I can see that its rationale from a certain point of view.",
			wantOK:  false,
			wantWhy: "long_discussion_reply",
		},
		{
			name:    "actionable local directive stays accepted",
			message: "Use t.Setenv.",
			wantOK:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotOK, gotWhy := reviewBenchmarkAcceptedReviewerCommentDisposition(tc.message)
			if gotOK != tc.wantOK || gotWhy != tc.wantWhy {
				t.Fatalf("reviewBenchmarkAcceptedReviewerCommentDisposition(%q) = (%v, %q), want (%v, %q)", tc.message, gotOK, gotWhy, tc.wantOK, tc.wantWhy)
			}
		})
	}
}

func TestReviewBenchmarkExamples_SkipsAcceptedUnfindableReviewerComments(t *testing.T) {
	examples := ReviewBenchmarkExamples([]ReviewBenchmarkCase{
		{
			ID:              "accepted-doc-comment",
			Label:           ReviewBenchmarkAccepted,
			Diff:            "@@ -10,1 +10,1 @@\n+func Run() {}\n",
			ReviewerComment: "This function needs a doc comment.",
		},
		{
			ID:              "accepted-preference",
			Label:           ReviewBenchmarkAccepted,
			Diff:            "@@ -10,1 +10,1 @@\n+value := ptr\n",
			ReviewerComment: "I would prefer to address this in a slightly more general way.",
		},
		{
			ID:              "accepted-actionable",
			Label:           ReviewBenchmarkAccepted,
			Diff:            "@@ -10,1 +10,1 @@\n+value := ptr\n",
			ReviewerComment: "Use t.Setenv.",
		},
		{
			ID:              "negative-doc-comment",
			Label:           ReviewBenchmarkNegative,
			Diff:            "@@ -10,1 +10,1 @@\n+func Run() {}\n",
			ReviewerComment: "This function needs a doc comment.",
		},
	})

	if len(examples) != 2 {
		t.Fatalf("len(examples) = %d, want 2", len(examples))
	}
	if examples[0].ID != "accepted-actionable" {
		t.Fatalf("examples[0].ID = %q, want accepted actionable case", examples[0].ID)
	}
	if examples[1].ID != "negative-doc-comment" {
		t.Fatalf("examples[1].ID = %q, want negative case preserved", examples[1].ID)
	}
}

func TestLoadReviewBenchmarkSuite_DecodesFlatArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suite.json")
	payload := []ReviewBenchmarkCase{
		{
			ID:       "flat-case",
			FilePath: "src/runtime/foo.go",
			Diff:     "@@ -1,1 +1,1 @@\n+value := ptr\n",
			Label:    ReviewBenchmarkNegative,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cases, err := LoadReviewBenchmarkSuite(path)
	if err != nil {
		t.Fatalf("LoadReviewBenchmarkSuite() error = %v", err)
	}
	if len(cases) != 1 || cases[0].ID != "flat-case" {
		t.Fatalf("cases = %#v, want decoded flat-array suite", cases)
	}
}

func TestReviewBenchmarkKeywords_FallbackIncludesGoTerms(t *testing.T) {
	keywords := reviewBenchmarkKeywords(nil, "Fix nil err fmt check")
	for _, want := range []string{"nil", "err", "fmt"} {
		found := false
		for _, keyword := range keywords {
			if keyword == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("keywords = %#v, want %q to be preserved", keywords, want)
		}
	}
}

func TestReviewBenchmarkKeywords_FallbackDropsGenericFunctionWords(t *testing.T) {
	keywords := reviewBenchmarkKeywords(nil, "add /files to the URL.")
	for _, unwanted := range []string{"add", "the"} {
		for _, keyword := range keywords {
			if keyword == unwanted {
				t.Fatalf("keywords = %#v, do not want generic token %q", keywords, unwanted)
			}
		}
	}
	for _, want := range []string{"files", "url"} {
		found := false
		for _, keyword := range keywords {
			if keyword == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("keywords = %#v, want %q to remain", keywords, want)
		}
	}
}

func TestReviewBenchmarkEvaluator_ExposesPipelineDiagnostics(t *testing.T) {
	evaluator := NewReviewBenchmarkEvaluator(DefaultReviewBenchmarkEvaluatorConfig())
	agent := &fakeReviewBenchmarkAgent{
		rawCandidates:        4,
		preVerificationCount: 3,
		skippedAfterFilter:   2,
		totalChunks:          5,
	}

	example := ReviewBenchmarkExamples([]ReviewBenchmarkCase{{
		ID:          "c3",
		FilePath:    "src/runtime/foo.go",
		FileContent: "package runtime\nfunc f(){}\n",
		Diff:        "@@ -10,1 +10,1 @@\n+value := ptr\n",
		Label:       ReviewBenchmarkNegative,
	}})[0]

	result, err := evaluator.Evaluate(context.Background(), agent, example)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got := result.SideInfo.Diagnostics["raw_candidates"]; got != 4 {
		t.Fatalf("raw_candidates = %#v, want 4", got)
	}
	if got := result.SideInfo.Diagnostics["pre_verification_count"]; got != 3 {
		t.Fatalf("pre_verification_count = %#v, want 3", got)
	}
	if got := result.SideInfo.Diagnostics["skipped_after_filter"]; got != 2 {
		t.Fatalf("skipped_after_filter = %#v, want 2", got)
	}
	if got := result.SideInfo.Diagnostics["total_chunks"]; got != 5 {
		t.Fatalf("total_chunks = %#v, want 5", got)
	}
}
