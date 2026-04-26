package main

import (
	"strings"
	"testing"

	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
)

func TestReviewRLMIntMutationPlansBoundRuntimeKnobs(t *testing.T) {
	plans := rlmIntMutationPlans(4, 32000)

	if got := plans[agentrlm.ArtifactMaxIterations]; got.Min != 1 || got.Max != 4 || got.Step != 1 {
		t.Fatalf("max iterations plan = %#v, want min=1 max=4 step=1", got)
	}
	if got := plans[agentrlm.ArtifactMaxTokens]; got.Min != 6400 || got.Max != 32000 || got.Step != 6400 {
		t.Fatalf("max tokens plan = %#v, want min=6400 max=32000 step=6400", got)
	}
}

func TestReviewRLMArtifactMetadataRecordsSuitePaths(t *testing.T) {
	metadata := reviewRLMArtifactMetadata("anthropic:claude-sonnet-4-6", []string{"a.json", "b.json"}, 12, 4, 0.75)

	if metadata["model_id"] != "anthropic:claude-sonnet-4-6" {
		t.Fatalf("model_id = %#v", metadata["model_id"])
	}
	if got := metadata["suite_paths"].(string); !strings.Contains(got, "a.json") || !strings.Contains(got, "b.json") {
		t.Fatalf("suite_paths = %q, want both paths", got)
	}
	if metadata["training_example_count"] != 12 || metadata["validation_example_count"] != 4 {
		t.Fatalf("example counts = %#v/%#v", metadata["training_example_count"], metadata["validation_example_count"])
	}
}

func TestReviewRLMValidationOutcome(t *testing.T) {
	if got := validationOutcome(0.01); got != "improved" {
		t.Fatalf("validationOutcome(improved) = %q", got)
	}
	if got := validationOutcome(0); got != "no_change" {
		t.Fatalf("validationOutcome(no_change) = %q", got)
	}
	if got := validationOutcome(-0.01); got != "regressed" {
		t.Fatalf("validationOutcome(regressed) = %q", got)
	}
	if !shouldWriteValidationArtifact("improved", false) {
		t.Fatalf("improved artifact should be written")
	}
	if shouldWriteValidationArtifact("regressed", true) {
		t.Fatalf("regressed artifact should not be written even with allow-no-improvement")
	}
}
