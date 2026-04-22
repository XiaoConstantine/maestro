package review

import "testing"

func TestParseReviewVerificationOutputParsesWrappedObject(t *testing.T) {
	raw := "Verifier result:\n```json\n{\"decisions\":[{\"id\":\"c1\",\"decision\":\"drop\",\"reason\":\"duplicate\"}]}\n```"

	got, err := parseReviewVerificationOutput(raw)
	if err != nil {
		t.Fatalf("parseReviewVerificationOutput returned error: %v", err)
	}
	if len(got.Decisions) != 1 {
		t.Fatalf("len(got.Decisions) = %d, want 1", len(got.Decisions))
	}
	if got.Decisions[0].ID != "c1" || got.Decisions[0].Decision != "drop" {
		t.Fatalf("got decision = %+v, want c1/drop", got.Decisions[0])
	}
}

func TestParseReviewVerificationOutputParsesDecisionArray(t *testing.T) {
	raw := "[{\"id\":\"c1\",\"decision\":\"keep\"},{\"id\":\"c2\",\"decision\":\"drop\"}]"

	got, err := parseReviewVerificationOutput(raw)
	if err != nil {
		t.Fatalf("parseReviewVerificationOutput returned error: %v", err)
	}
	if len(got.Decisions) != 2 {
		t.Fatalf("len(got.Decisions) = %d, want 2", len(got.Decisions))
	}
	if got.Decisions[1].ID != "c2" || got.Decisions[1].Decision != "drop" {
		t.Fatalf("got second decision = %+v, want c2/drop", got.Decisions[1])
	}
}

func TestParseReviewVerificationOutputRejectsEmptyDecisions(t *testing.T) {
	raw := "{\"decisions\":[]}"

	_, err := parseReviewVerificationOutput(raw)
	if err == nil {
		t.Fatal("parseReviewVerificationOutput returned nil error, want failure")
	}
}

func TestParseReviewVerificationOutputHandlesBracesInsideStrings(t *testing.T) {
	raw := "{\"decisions\":[{\"id\":\"c1\",\"decision\":\"drop\",\"reason_code\":\"content_check\",\"reason\":\"reason with { braces } inside string\"}]}"

	got, err := parseReviewVerificationOutput(raw)
	if err != nil {
		t.Fatalf("parseReviewVerificationOutput returned error: %v", err)
	}
	if len(got.Decisions) != 1 {
		t.Fatalf("len(got.Decisions) = %d, want 1", len(got.Decisions))
	}
	if got.Decisions[0].Reason != "reason with { braces } inside string" {
		t.Fatalf("got reason = %q, want braces preserved", got.Decisions[0].Reason)
	}
	if got.Decisions[0].ReasonCode != "content_check" {
		t.Fatalf("got reason_code = %q, want content_check", got.Decisions[0].ReasonCode)
	}
}

func TestSelectReviewVerificationCandidatesUsesLowPriorityTail(t *testing.T) {
	candidates := []reviewVerificationCandidate{
		{ID: "c1"},
		{ID: "c2"},
		{ID: "c3"},
		{ID: "c4"},
		{ID: "c5"},
		{ID: "c6"},
		{ID: "c7"},
		{ID: "c8"},
	}

	got := selectReviewVerificationCandidates(candidates)
	if len(got) != reviewVerificationMaxComments {
		t.Fatalf("len(got) = %d, want %d", len(got), reviewVerificationMaxComments)
	}
	if got[0].ID != "c3" || got[len(got)-1].ID != "c8" {
		t.Fatalf("got ids = %q..%q, want c3..c8", got[0].ID, got[len(got)-1].ID)
	}
}

func TestReviewChangedFilesNormalizesLeadingDotSlash(t *testing.T) {
	tasks := []PRReviewTask{
		{FilePath: "./internal/review/verifier.go"},
		{FilePath: "internal/review/verifier.go"},
		{FilePath: "./internal/review/review.go"},
	}

	got := reviewChangedFiles(tasks)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0] != "internal/review/review.go" || got[1] != "internal/review/verifier.go" {
		t.Fatalf("got = %#v, want normalized deduped paths", got)
	}
}

func TestApplyReviewVerificationDecisionsKeepsUndecidedComments(t *testing.T) {
	comments := []PRReviewComment{
		{FilePath: "pkg/a.go", LineNumber: 10, Content: "drop me"},
		{FilePath: "pkg/b.go", LineNumber: 20, Content: "keep me"},
		{FilePath: "pkg/c.go", LineNumber: 30, Content: "undecided stays"},
	}
	allCandidates := []reviewVerificationCandidate{
		{ID: "c1"},
		{ID: "c2"},
		{ID: "c3"},
	}
	output := reviewVerificationOutput{
		Decisions: []reviewVerificationDecision{
			{ID: "c1", Decision: "drop", ReasonCode: "content_check", Reason: "code contradicts the finding"},
		},
	}

	got, report := applyReviewVerificationDecisions(comments, allCandidates, output)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].FilePath != "pkg/b.go" {
		t.Fatalf("got[0].FilePath = %q, want pkg/b.go", got[0].FilePath)
	}
	if got[1].FilePath != "pkg/c.go" {
		t.Fatalf("got[1].FilePath = %q, want pkg/c.go", got[1].FilePath)
	}
	if report.DroppedCount != 1 || report.KeptCount != 2 {
		t.Fatalf("report counts = %+v, want dropped=1 kept=2", report)
	}
	if report.DropReasons["content_check"] != 1 {
		t.Fatalf("report.DropReasons = %#v, want content_check=1", report.DropReasons)
	}
	if len(report.Rejections) != 1 || report.Rejections[0].Reason != "code contradicts the finding" {
		t.Fatalf("report.Rejections = %#v, want one recorded rejection", report.Rejections)
	}
}

func TestNormalizeReviewVerificationReasonCodeInfersCategories(t *testing.T) {
	tests := map[string]struct {
		reasonCode string
		reason     string
		want       string
	}{
		"explicit":         {reasonCode: "duplicate", want: "duplicate"},
		"wrong line":       {reason: "points at the wrong line in the file", want: "line_mismatch"},
		"outside changed":  {reason: "this is outside changed lines", want: "hunk_mismatch"},
		"subjective":       {reason: "too subjective and not actionable", want: "subjective"},
		"unsupported":      {reason: "repository content does not support this claim", want: "content_check"},
		"default fallback": {reason: "needs more thought", want: "other"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := normalizeReviewVerificationReasonCode(tt.reasonCode, tt.reason); got != tt.want {
				t.Fatalf("normalizeReviewVerificationReasonCode(%q, %q) = %q, want %q", tt.reasonCode, tt.reason, got, tt.want)
			}
		})
	}
}
