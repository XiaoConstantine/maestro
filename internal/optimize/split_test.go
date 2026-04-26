package optimizeutil

import (
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
)

func TestSplitAgentExamplesStratifiesByRepo(t *testing.T) {
	examples := make([]optimize.AgentExample, 0, 16)
	for i := 0; i < 8; i++ {
		examples = append(examples, optimize.AgentExample{
			ID:     "maestro-case-" + string(rune('a'+i)),
			Inputs: map[string]interface{}{"owner": "XiaoConstantine", "repo": "maestro"},
		})
	}
	for i := 0; i < 8; i++ {
		examples = append(examples, optimize.AgentExample{
			ID:     "dspy-case-" + string(rune('a'+i)),
			Inputs: map[string]interface{}{"owner": "XiaoConstantine", "repo": "dspy-go"},
		})
	}

	train, validation, err := SplitAgentExamples(examples, 0.25, 16)
	if err != nil {
		t.Fatalf("SplitAgentExamples() error = %v", err)
	}
	if len(train) != 12 || len(validation) != 4 {
		t.Fatalf("len(train), len(validation) = %d, %d; want 12, 4", len(train), len(validation))
	}
	counts := map[string]int{}
	for _, example := range validation {
		counts[example.Inputs["repo"].(string)]++
	}
	if counts["maestro"] != 2 || counts["dspy-go"] != 2 {
		t.Fatalf("validation repo counts = %#v, want 2 maestro and 2 dspy-go", counts)
	}

	_, validationAgain, err := SplitAgentExamples(examples, 0.25, 16)
	if err != nil {
		t.Fatalf("second SplitAgentExamples() error = %v", err)
	}
	for i := range validation {
		if validation[i].ID != validationAgain[i].ID {
			t.Fatalf("validation split is not deterministic: run1[%d]=%q run2[%d]=%q", i, validation[i].ID, i, validationAgain[i].ID)
		}
	}
}

func TestSplitAgentExamplesUsesOverviewMetadataFallback(t *testing.T) {
	examples := make([]optimize.AgentExample, 0, 16)
	for i := 0; i < 8; i++ {
		examples = append(examples, optimize.AgentExample{
			ID: "maestro-metadata-" + string(rune('a'+i)),
			Metadata: map[string]interface{}{
				"rlm_overview_case": map[string]interface{}{"owner": "XiaoConstantine", "repo": "maestro"},
			},
		})
	}
	for i := 0; i < 8; i++ {
		examples = append(examples, optimize.AgentExample{
			ID: "dspy-metadata-" + string(rune('a'+i)),
			Metadata: map[string]interface{}{
				"rlm_overview_case": struct {
					Owner string `json:"owner"`
					Repo  string `json:"repo"`
				}{Owner: "XiaoConstantine", Repo: "dspy-go"},
			},
		})
	}

	_, validation, err := SplitAgentExamples(examples, 0.25, 16)
	if err != nil {
		t.Fatalf("SplitAgentExamples() error = %v", err)
	}
	counts := map[string]int{}
	for _, example := range validation {
		_, repo := ownerRepoFromOverviewMetadata(example.Metadata["rlm_overview_case"])
		counts[repo]++
	}
	if counts["maestro"] != 2 || counts["dspy-go"] != 2 {
		t.Fatalf("metadata validation repo counts = %#v, want 2 maestro and 2 dspy-go", counts)
	}
}

func TestSplitAgentExamplesRejectsTinySuites(t *testing.T) {
	examples := make([]optimize.AgentExample, 0, 15)
	for i := 0; i < 15; i++ {
		examples = append(examples, optimize.AgentExample{ID: "case-" + string(rune('a'+i))})
	}

	if _, _, err := SplitAgentExamples(examples, 0.25, 16); err == nil {
		t.Fatalf("SplitAgentExamples() error = nil, want minimum-suite-size error")
	}
}

func TestValidateUnitThreshold(t *testing.T) {
	if err := ValidateUnitThreshold("pass-threshold", 0.7); err != nil {
		t.Fatalf("ValidateUnitThreshold(valid) error = %v", err)
	}
	if err := ValidateUnitThreshold("pass-threshold", 0); err == nil {
		t.Fatalf("ValidateUnitThreshold(0) error = nil, want error")
	}
	if err := ValidateUnitThreshold("pass-threshold", 1.01); err == nil {
		t.Fatalf("ValidateUnitThreshold(1.01) error = nil, want error")
	}
}
