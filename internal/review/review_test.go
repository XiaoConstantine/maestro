package review

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	maestrotypes "github.com/XiaoConstantine/maestro/internal/types"
)

func TestReviewPRWithChangesRejectsOverlappingRun(t *testing.T) {
	agent := &PRReviewAgent{}
	agent.runMu.Lock()
	defer agent.runMu.Unlock()

	_, err := agent.ReviewPRWithChanges(context.Background(), 42, nil, nil, nil)
	if !errors.Is(err, ErrReviewActive) {
		t.Fatalf("ReviewPRWithChanges() error = %v, want ErrReviewActive", err)
	}
}

func TestReviewPRWithChangesRejectsAfterShutdown(t *testing.T) {
	agent := &PRReviewAgent{shuttingDown: true}
	_, err := agent.ReviewPRWithChanges(context.Background(), 42, nil, nil, nil)
	if !errors.Is(err, ErrReviewClosed) {
		t.Fatalf("ReviewPRWithChanges() error = %v, want ErrReviewClosed", err)
	}
}

func TestClosePreservesCloneWhileReviewIsActive(t *testing.T) {
	clone := t.TempDir()
	agent := &PRReviewAgent{clonedRepoPath: clone, runDone: make(chan struct{}), stopper: NewStopper()}
	if err := agent.Close(); err == nil {
		t.Fatal("Close() error = nil, want active-review error")
	}
	if _, err := os.Stat(clone); err != nil {
		t.Fatalf("clone was removed while review active: %v", err)
	}
}

func TestStopThenCloseCleansCloneAfterReviewQuiesces(t *testing.T) {
	clone := t.TempDir()
	runCtx, runCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	agent := &PRReviewAgent{
		clonedRepoPath: clone,
		runCancel:      runCancel,
		runDone:        done,
		stopper:        NewStopper(),
	}

	stopReturned := make(chan struct{})
	go func() {
		agent.Stop(context.Background())
		close(stopReturned)
	}()
	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("Stop() did not cancel the active review")
	}

	agent.finishReviewRun(done, runCancel)
	select {
	case <-stopReturned:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not observe review completion")
	}
	if err := agent.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Fatalf("clone stat error = %v, want removed", err)
	}
}

func TestPartitionReviewCommentsByChanges_PreservesFileLevelComments(t *testing.T) {
	comments := []PRReviewComment{
		{FilePath: "pkg/agents/agent.go", LineNumber: 0, Content: "general architecture feedback"},
		{FilePath: "pkg/agents/agent.go", LineNumber: 12, Content: "line comment"},
		{FilePath: "pkg/agents/agent.go", LineNumber: 99, Content: "outside hunk"},
	}
	changes := &PRChanges{
		Files: []PRFileChange{{
			FilePath: "pkg/agents/agent.go",
			Hunks: []ChangeHunk{
				{StartLine: 10, EndLine: 20},
			},
		}},
	}

	valid, skipped := partitionReviewCommentsByChanges(comments, changes)
	if len(valid) != 2 {
		t.Fatalf("len(valid) = %d, want 2", len(valid))
	}
	if valid[0].Content != "general architecture feedback" {
		t.Fatalf("valid[0] = %q, want file-level comment preserved", valid[0].Content)
	}
	if len(skipped) != 1 || skipped[0].Content != "outside hunk" {
		t.Fatalf("skipped = %#v, want only unchanged-line comment filtered", skipped)
	}
}

func TestPartitionReviewCommentsByChangesDetailed_RecordsMissReason(t *testing.T) {
	comments := []PRReviewComment{
		{FilePath: "pkg/agents/agent.go", LineNumber: 4, Content: "too early"},
	}
	changes := &PRChanges{
		Files: []PRFileChange{{
			FilePath: "pkg/agents/agent.go",
			Hunks: []ChangeHunk{
				{StartLine: 10, EndLine: 20},
			},
		}},
	}

	valid, skipped, rejected := partitionReviewCommentsByChangesDetailed(comments, changes)
	if len(valid) != 0 || len(skipped) != 1 {
		t.Fatalf("valid/skipped = %d/%d, want 0/1", len(valid), len(skipped))
	}
	if len(rejected) != 1 || rejected[0].ReasonCode != "before_first_hunk" {
		t.Fatalf("rejected = %#v, want one before_first_hunk rejection", rejected)
	}
}

func TestPartitionReviewCommentsByChangesDetailed_AllowsSlackNearHunkBoundary(t *testing.T) {
	comments := []PRReviewComment{
		{FilePath: "pkg/agents/agent.go", LineNumber: 24, Content: "nearby context line"},
	}
	changes := &PRChanges{
		Files: []PRFileChange{{
			FilePath: "pkg/agents/agent.go",
			Hunks: []ChangeHunk{
				{StartLine: 10, EndLine: 20},
			},
		}},
	}

	valid, skipped, rejected := partitionReviewCommentsByChangesDetailed(comments, changes)
	if len(valid) != 1 || len(skipped) != 0 || len(rejected) != 0 {
		t.Fatalf("expected slack to keep comment, got valid=%d skipped=%d rejected=%d", len(valid), len(skipped), len(rejected))
	}
}

func TestSelectChunksForChangedHunks_IncludesAdjacentContext(t *testing.T) {
	chunks := []ReviewChunk{
		{StartLine: 1, EndLine: 50},
		{StartLine: 51, EndLine: 100},
		{StartLine: 101, EndLine: 150},
		{StartLine: 151, EndLine: 200},
		{StartLine: 201, EndLine: 250},
	}
	hunks := []ChangeHunk{{StartLine: 120, EndLine: 125}}

	selected := selectChunksForChangedHunks(chunks, hunks, 1)
	if len(selected) != 3 {
		t.Fatalf("len(selected) = %d, want 3", len(selected))
	}
	if selected[0].StartLine != 51 || selected[1].StartLine != 101 || selected[2].StartLine != 151 {
		t.Fatalf("selected = %#v, want overlap chunk plus one adjacent on each side", selected)
	}
}

func TestShiftReviewIssuesToFileLines_AlignsChunkRelativeLinesWithDiffHunks(t *testing.T) {
	issues := []maestrotypes.ReviewIssue{{
		FilePath:  "pkg/agents/agent.go",
		LineRange: maestrotypes.LineRange{Start: 5, End: 6},
	}}
	changes := &PRChanges{
		Files: []PRFileChange{{
			FilePath: "pkg/agents/agent.go",
			Hunks: []ChangeHunk{
				{StartLine: 54, EndLine: 60},
			},
		}},
	}

	shiftReviewIssuesToFileLines(issues, 50)
	valid, skipped := partitionReviewCommentsByChanges([]PRReviewComment{
		commentFromReviewIssue(issues[0]),
	}, changes)
	if len(valid) != 1 {
		t.Fatalf("len(valid) = %d, want shifted comment to survive filtering", len(valid))
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %#v, want no shifted comments filtered", skipped)
	}
	if got := valid[0].LineNumber; got != 54 {
		t.Fatalf("line = %d, want 54 after offsetting from chunk-relative line 5", got)
	}
}

func TestFilterChunkBoundaryIssues_DropsBoundarySyntaxArtifact(t *testing.T) {
	issues := []maestrotypes.ReviewIssue{{
		LineRange:   maestrotypes.LineRange{Start: 1, End: 1},
		Description: "Extraneous closing brace at the start of the snippet causes a syntax error.",
		Suggestion:  "Remove the duplicate brace.",
	}}

	got := filterChunkBoundaryIssues(issues, "}\nreturn nil\n}\nfunc next() {}")
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0 after dropping boundary syntax artifact", len(got))
	}
}

func TestFilterChunkBoundaryIssues_KeepsNonBoundaryIssue(t *testing.T) {
	issues := []maestrotypes.ReviewIssue{{
		LineRange:   maestrotypes.LineRange{Start: 5, End: 5},
		Description: "The nil error path is unchecked before dereferencing resp.",
		Suggestion:  "Guard resp before accessing its fields.",
	}}

	got := filterChunkBoundaryIssues(issues, "line1\nline2\nline3\nline4\nline5\nline6\nline7")
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 non-boundary issue kept", len(got))
	}
}

func TestFilterChunkBoundaryIssues_KeepsSyntaxIssueAwayFromBoundary(t *testing.T) {
	issues := []maestrotypes.ReviewIssue{{
		LineRange:   maestrotypes.LineRange{Start: 5, End: 5},
		Description: "This syntax error leaves the composite literal malformed.",
		Suggestion:  "Add the missing field value.",
	}}

	got := filterChunkBoundaryIssues(issues, "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9")
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 non-boundary syntax issue kept", len(got))
	}
}

func TestReviewStateDirFromDBPath_UsesDBParentDirectory(t *testing.T) {
	got := reviewStateDirFromDBPath("/Users/xiao/.maestro/XiaoConstantine_maestro.db")
	if got != "/Users/xiao/.maestro" {
		t.Fatalf("reviewStateDirFromDBPath() = %q, want %q", got, "/Users/xiao/.maestro")
	}
}

func TestReviewSgrepHome_NestsUnderStateDir(t *testing.T) {
	got := reviewSgrepHome("/Users/xiao/.maestro")
	if got != "/Users/xiao/.maestro/sgrep" {
		t.Fatalf("reviewSgrepHome() = %q, want %q", got, "/Users/xiao/.maestro/sgrep")
	}
}

func TestFilterLowSignalAdvisoryIssues_DropsGenericNamingAdvice(t *testing.T) {
	issues := []maestrotypes.ReviewIssue{{
		FilePath:    "internal/github/client.go",
		Category:    "style",
		Severity:    "medium",
		Description: "The interface name is too generic and not idiomatic Go.",
		Suggestion:  "Consider renaming the interface to a more descriptive name.",
		LineRange:   maestrotypes.LineRange{Start: 23, End: 23},
		Confidence:  0.8,
		CodeExample: "",
	}}

	got := filterLowSignalAdvisoryIssues(issues)
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0 after dropping generic advisory issue", len(got))
	}
}

func TestFilterLowSignalAdvisoryIssues_KeepsConcreteBugRisk(t *testing.T) {
	issues := []maestrotypes.ReviewIssue{{
		FilePath:    "internal/github/client.go",
		Category:    "bug",
		Severity:    "high",
		Description: "Potential nil pointer dereference if ref.Object is nil after GetRef succeeds.",
		Suggestion:  "Add a nil check before accessing ref.Object.GetSHA().",
		LineRange:   maestrotypes.LineRange{Start: 365, End: 365},
		Confidence:  0.9,
		CodeExample: "",
	}}

	got := filterLowSignalAdvisoryIssues(issues)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 concrete bug kept", len(got))
	}
}

func TestBuildReviewGuidelinesText_NarrowsAndDeduplicatesGuidelines(t *testing.T) {
	guidelines := []*maestrotypes.Content{
		{Text: "Verify interface compliance with a compile-time assertion near the type definition."},
		{Text: "Verify interface compliance with a compile-time assertion near the type definition."},
		{Text: "Error strings should not be capitalized and should avoid the phrase failed to when wrapping errors."},
	}

	got := buildReviewGuidelinesText(guidelines)
	if want := "Use Go best practices and project guidelines only to confirm concrete issues"; !strings.Contains(got, want) {
		t.Fatalf("guidelines text missing narrowing prefix %q", want)
	}
	if strings.Count(got, "Verify interface compliance") != 1 {
		t.Fatalf("guidelines text did not deduplicate repeated excerpts: %q", got)
	}
}

func TestFilterLowSignalAdvisoryIssues_DropsCompileTimeInterfaceAdvice(t *testing.T) {
	issues := []maestrotypes.ReviewIssue{{
		FilePath:    "internal/rlm/router.go",
		Category:    "style",
		Severity:    "low",
		Description: "Add a compile-time interface check to ensure RouterSubClient implements the SubAgent interface.",
		Suggestion:  "Add `var _ SubAgent = (*RouterSubClient)(nil)` near the type definition.",
		LineRange:   maestrotypes.LineRange{Start: 520, End: 520},
		Confidence:  0.8,
	}}

	got := filterLowSignalAdvisoryIssues(issues)
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0 after dropping compile-time interface advice", len(got))
	}
}

func TestFilterLowSignalAdvisoryIssues_DropsErrorStringStyleAdvice(t *testing.T) {
	issues := []maestrotypes.ReviewIssue{{
		FilePath:    "internal/github/client.go",
		Category:    "style",
		Severity:    "low",
		Description: "Error message uses 'failed to' prefix, violating Go error string style guidance.",
		Suggestion:  "Change the error text to avoid the failed to prefix.",
		LineRange:   maestrotypes.LineRange{Start: 126, End: 126},
		Confidence:  0.8,
	}}

	got := filterLowSignalAdvisoryIssues(issues)
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0 after dropping error-string style advice", len(got))
	}
}
