package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReviewCasesFromFileSupportsSingleAndArray(t *testing.T) {
	dir := t.TempDir()
	single := filepath.Join(dir, "single.json")
	array := filepath.Join(dir, "array.json")

	caseA := ReviewBenchmarkCase{
		ID:              "single",
		FilePath:        "foo.go",
		FileContent:     "package foo\n",
		Diff:            "@@ -1 +1 @@\n+panic(\"x\")\n",
		ReviewerComment: "this needs a guard",
		Label:           ReviewBenchmarkAccepted,
	}
	caseB := ReviewBenchmarkCase{
		ID:          "neg",
		FilePath:    "bar.go",
		FileContent: "package bar\n",
		Diff:        "@@ -1 +1 @@\n+return nil\n",
		Label:       ReviewBenchmarkNegative,
	}
	data, _ := json.Marshal(caseA)
	if err := os.WriteFile(single, data, 0o644); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal([]ReviewBenchmarkCase{caseB})
	if err := os.WriteFile(array, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cases, err := loadReviewCasesFromFile(single)
	if err != nil {
		t.Fatalf("loadReviewCasesFromFile(single) error = %v", err)
	}
	if len(cases) != 1 || cases[0].ID != "single" {
		t.Fatalf("unexpected single cases: %#v", cases)
	}

	cases, err = loadReviewCasesFromFile(array)
	if err != nil {
		t.Fatalf("loadReviewCasesFromFile(array) error = %v", err)
	}
	if len(cases) != 1 || cases[0].ID != "neg" {
		t.Fatalf("unexpected array cases: %#v", cases)
	}
}

func TestLoadInboxReviewCasesCountsEligibleExamples(t *testing.T) {
	dir := t.TempDir()
	payload := []ReviewBenchmarkCase{
		{
			ID:              "accepted",
			FilePath:        "foo.go",
			FileContent:     "package foo\n",
			Diff:            "@@ -1 +1 @@\n+panic(\"x\")\n",
			ReviewerComment: "this needs a nil check",
			Label:           ReviewBenchmarkAccepted,
		},
		{
			ID:          "negative",
			FilePath:    "bar.go",
			FileContent: "package bar\n",
			Diff:        "@@ -1 +1 @@\n+return nil\n",
			Label:       ReviewBenchmarkNegative,
		},
		{
			ID:              "discussion",
			FilePath:        "baz.go",
			FileContent:     "package baz\n",
			Diff:            "@@ -1 +1 @@\n+return nil\n",
			ReviewerComment: "maybe rethink this",
			Label:           ReviewBenchmarkDiscussion,
		},
	}
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cases, files, err := loadInboxReviewCases(dir)
	if err != nil {
		t.Fatalf("loadInboxReviewCases() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	if got := len(ReviewBenchmarkExamples(cases)); got != 2 {
		t.Fatalf("eligible examples = %d, want 2", got)
	}
}

func TestReplayRegressedBeyondTolerance(t *testing.T) {
	tests := []struct {
		name      string
		baseline  float64
		replay    float64
		tolerance float64
		want      bool
	}{
		{
			name:      "within tolerance does not regress",
			baseline:  0.7818548387096774,
			replay:    0.7725806451612903,
			tolerance: 0.015,
			want:      false,
		},
		{
			name:      "beyond tolerance regresses",
			baseline:  0.7818548387096774,
			replay:    0.7725806451612903,
			tolerance: 0.005,
			want:      true,
		},
		{
			name:      "negative tolerance clamps to zero",
			baseline:  0.5,
			replay:    0.49,
			tolerance: -1,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replayRegressedBeyondTolerance(tt.baseline, tt.replay, tt.tolerance)
			if got != tt.want {
				t.Fatalf("replayRegressedBeyondTolerance() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProtectedSuitesRegressed(t *testing.T) {
	tests := []struct {
		name      string
		baseline  map[string]float64
		replay    map[string]float64
		tolerance float64
		want      bool
	}{
		{
			name: "within protected tolerance does not regress",
			baseline: map[string]float64{
				"mdempsky": 0.7239130434782608,
			},
			replay: map[string]float64{
				"mdempsky": 0.6847826086956522,
			},
			tolerance: 0.04,
			want:      false,
		},
		{
			name: "beyond protected tolerance regresses",
			baseline: map[string]float64{
				"mdempsky": 0.7239130434782608,
			},
			replay: map[string]float64{
				"mdempsky": 0.6847826086956522,
			},
			tolerance: 0.02,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := protectedSuitesRegressed(tt.baseline, tt.replay, tt.tolerance)
			if got != tt.want {
				t.Fatalf("protectedSuitesRegressed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSearchSuitePathsForConfig(t *testing.T) {
	cfg := EvolveReviewConfig{
		SuitePaths: []string{"full-a", "full-b"},
	}
	if got := searchSuitePathsForConfig(cfg); len(got) != 2 || got[0] != "full-a" || got[1] != "full-b" {
		t.Fatalf("searchSuitePathsForConfig() fallback = %#v", got)
	}

	cfg.SearchSuitePaths = []string{"search-a"}
	if got := searchSuitePathsForConfig(cfg); len(got) != 1 || got[0] != "search-a" {
		t.Fatalf("searchSuitePathsForConfig() explicit = %#v", got)
	}

	cfg = EvolveReviewConfig{
		SearchSuitePaths: []string{"search-only"},
	}
	if got := replaySuitePathsForConfig(cfg); len(got) != 1 || got[0] != "search-only" {
		t.Fatalf("replaySuitePathsForConfig() fallback = %#v", got)
	}
}

func TestSearchCaseCapForConfig(t *testing.T) {
	cfg := EvolveReviewConfig{
		MaxCasesPerRun:         64,
		MaxSearchCasesPerSuite: 12,
	}
	if got := searchCaseCapForConfig(cfg); got != 12 {
		t.Fatalf("searchCaseCapForConfig() = %d, want 12", got)
	}

	cfg.MaxSearchCasesPerSuite = 0
	if got := searchCaseCapForConfig(cfg); got != 64 {
		t.Fatalf("searchCaseCapForConfig() fallback = %d, want 64", got)
	}
}
