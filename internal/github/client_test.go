package github

import (
	"strings"
	"testing"

	"github.com/XiaoConstantine/maestro/internal/types"
)

func TestPartitionReviewCommentsByChanges(t *testing.T) {
	comments := []types.PRReviewComment{
		{FilePath: "pkg/agents/agent.go", LineNumber: 12, Content: "valid"},
		{FilePath: "pkg/agents/agent.go", LineNumber: 40, Content: "unchanged"},
		{FilePath: "pkg/other/file.go", LineNumber: 3, Content: "missing-file"},
		{FilePath: "pkg/agents/agent.go", LineNumber: 0, Content: "file-level"},
	}
	changes := &types.PRChanges{
		Files: []types.PRFileChange{
			{
				FilePath: "pkg/agents/agent.go",
				Hunks: []types.ChangeHunk{
					{StartLine: 10, EndLine: 20},
				},
			},
		},
	}

	valid, skipped := partitionReviewCommentsByChanges(comments, changes)
	if len(valid) != 2 {
		t.Fatalf("expected 2 valid comments, got %d", len(valid))
	}
	if valid[0].Content != "valid" {
		t.Fatalf("expected valid comment to be preserved, got %q", valid[0].Content)
	}
	if valid[1].Content != "file-level" {
		t.Fatalf("expected file-level comment to be preserved, got %q", valid[1].Content)
	}
	if len(skipped) != 2 {
		t.Fatalf("expected 2 skipped comments, got %d", len(skipped))
	}
}

func TestPartitionReviewCommentsByChangesWithNilChanges(t *testing.T) {
	comments := []types.PRReviewComment{
		{FilePath: "pkg/agents/agent.go", LineNumber: 12, Content: "comment"},
	}

	valid, skipped := partitionReviewCommentsByChanges(comments, nil)
	if len(valid) != 0 {
		t.Fatalf("expected no valid comments without changes, got %d", len(valid))
	}
	if len(skipped) != 1 {
		t.Fatalf("expected comment to be skipped without changes, got %d", len(skipped))
	}
}

func TestPartitionReviewCommentsByChangesAnchorsOnStartLine(t *testing.T) {
	comments := []types.PRReviewComment{
		{FilePath: "pkg/agents/agent.go", LineNumber: 12, EndLine: 30, Content: "range-comment"},
	}
	changes := &types.PRChanges{
		Files: []types.PRFileChange{
			{
				FilePath: "pkg/agents/agent.go",
				Hunks: []types.ChangeHunk{
					{StartLine: 10, EndLine: 20},
				},
			},
		},
	}

	valid, skipped := partitionReviewCommentsByChanges(comments, changes)
	if len(valid) != 1 || len(skipped) != 0 {
		t.Fatalf("expected comment anchor line to control filtering, got valid=%d skipped=%d", len(valid), len(skipped))
	}
}

func TestPartitionReviewCommentsByChangesSkipsGapBetweenHunks(t *testing.T) {
	comments := []types.PRReviewComment{
		{FilePath: "pkg/agents/agent.go", LineNumber: 15, Content: "first-hunk"},
		{FilePath: "pkg/agents/agent.go", LineNumber: 30, Content: "gap"},
		{FilePath: "pkg/agents/agent.go", LineNumber: 45, Content: "second-hunk"},
	}
	changes := &types.PRChanges{
		Files: []types.PRFileChange{
			{
				FilePath: "pkg/agents/agent.go",
				Hunks: []types.ChangeHunk{
					{StartLine: 10, EndLine: 20},
					{StartLine: 40, EndLine: 50},
				},
			},
		},
	}

	valid, skipped := partitionReviewCommentsByChanges(comments, changes)
	if len(valid) != 2 {
		t.Fatalf("expected two valid comments across separate hunks, got %d", len(valid))
	}
	if len(skipped) != 1 || skipped[0].Content != "gap" {
		t.Fatalf("expected the gap comment to be skipped, got %#v", skipped)
	}
}

func TestPartitionReviewCommentsByChanges_AllowsSlackNearHunkBoundary(t *testing.T) {
	comments := []types.PRReviewComment{
		{FilePath: "pkg/agents/agent.go", LineNumber: 24, Content: "nearby-context"},
	}
	changes := &types.PRChanges{
		Files: []types.PRFileChange{
			{
				FilePath: "pkg/agents/agent.go",
				Hunks: []types.ChangeHunk{
					{StartLine: 10, EndLine: 20},
				},
			},
		},
	}

	valid, skipped := partitionReviewCommentsByChanges(comments, changes)
	if len(valid) != 1 || len(skipped) != 0 {
		t.Fatalf("expected slack to keep nearby context comment, got valid=%d skipped=%d", len(valid), len(skipped))
	}
}

func TestComposeReviewBody_IncludesGeneralComments(t *testing.T) {
	body := composeReviewBody([]string{"first general comment", "second general comment"})
	if body == "Code Review Comments" {
		t.Fatalf("expected general comments to be appended")
	}
	if !strings.Contains(body, "first general comment") || !strings.Contains(body, "second general comment") {
		t.Fatalf("body = %q, want both general comments", body)
	}
}

func TestParseHunksTracksFullNewFileRangeAcrossContextLines(t *testing.T) {
	patch := strings.Join([]string{
		"@@ -250,6 +250,6 @@",
		" context1",
		"-oldA",
		"+newA",
		" context2",
		" context3",
		"-oldB",
		"+newB",
	}, "\n")

	hunks, err := ParseHunks(patch, "src/crypto/x509/x509_test.go")
	if err != nil {
		t.Fatalf("ParseHunks() error = %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	if hunks[0].StartLine != 250 || hunks[0].EndLine != 254 {
		t.Fatalf("expected hunk range 250-254, got %d-%d", hunks[0].StartLine, hunks[0].EndLine)
	}
}
