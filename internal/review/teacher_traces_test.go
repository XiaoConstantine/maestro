package review

import (
	"strings"
	"testing"
)

func TestSelectTopTeacherDemos_PrefersMatchedHighScoringTraces(t *testing.T) {
	traces := []ReviewTeacherTrace{
		{
			CaseID:  "low",
			Score:   0.40,
			Matched: true,
			TeacherComments: []PRReviewComment{{
				LineNumber: 10,
				Content:    "low score comment",
			}},
		},
		{
			CaseID:  "high",
			Score:   0.90,
			Matched: true,
			FilePath: "src/runtime/high.go",
			InputDiff: "@@ -1,1 +1,2 @@\n+value := ptr\n",
			TeacherComments: []PRReviewComment{{
				LineNumber: 42,
				Content:    "add a nil check before dereferencing ptr",
				Suggestion: "guard ptr before use",
			}},
		},
		{
			CaseID:          "error",
			Score:           1.0,
			Matched:         true,
			EvaluationError: "boom",
			TeacherComments: []PRReviewComment{{
				LineNumber: 50,
				Content:    "should be ignored",
			}},
		},
	}

	demos := SelectTopTeacherDemos(traces, 1)
	if !strings.Contains(demos, "src/runtime/high.go") {
		t.Fatalf("demos = %q, want highest-scoring matched trace", demos)
	}
	if strings.Contains(demos, "low score comment") {
		t.Fatalf("demos = %q, want lower-scoring trace excluded when limit=1", demos)
	}
	if strings.Contains(demos, "should be ignored") {
		t.Fatalf("demos = %q, want traces with execution errors excluded", demos)
	}
}
