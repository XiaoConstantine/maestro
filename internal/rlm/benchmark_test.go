package rlm

import (
	"context"
	"testing"
	"time"
)

func TestCalculateStats(t *testing.T) {
	runs := []RunResult{
		{
			Duration:         100 * time.Millisecond,
			PromptTokens:     1000,
			CompletionTokens: 200,
			TotalTokens:      1200,
			CostUSD:          0.01,
			QualityScore:     0.8,
		},
		{
			Duration:         150 * time.Millisecond,
			PromptTokens:     1100,
			CompletionTokens: 250,
			TotalTokens:      1350,
			CostUSD:          0.012,
			QualityScore:     0.85,
		},
		{
			Duration:         120 * time.Millisecond,
			PromptTokens:     1050,
			CompletionTokens: 220,
			TotalTokens:      1270,
			CostUSD:          0.011,
			QualityScore:     0.82,
		},
	}

	stats := calculateStats(runs)

	if stats.TotalRuns != 3 {
		t.Errorf("expected 3 total runs, got %d", stats.TotalRuns)
	}
	if stats.SuccessfulRuns != 3 {
		t.Errorf("expected 3 successful runs, got %d", stats.SuccessfulRuns)
	}
	if stats.MinDuration != 100 {
		t.Errorf("expected min duration 100ms, got %.0fms", stats.MinDuration)
	}
	if stats.MaxDuration != 150 {
		t.Errorf("expected max duration 150ms, got %.0fms", stats.MaxDuration)
	}
	// Average tokens: (1200 + 1350 + 1270) / 3 = 1273.33
	expectedAvgTokens := 1273.33
	if stats.AvgTotalTokens < expectedAvgTokens-1 || stats.AvgTotalTokens > expectedAvgTokens+1 {
		t.Errorf("expected avg total tokens ~%.2f, got %.2f", expectedAvgTokens, stats.AvgTotalTokens)
	}
}

func TestCalculateStatsWithErrors(t *testing.T) {
	runs := []RunResult{
		{
			Duration:     100 * time.Millisecond,
			PromptTokens: 1000,
			TotalTokens:  1200,
		},
		{
			Error: "timeout",
		},
		{
			Duration:     120 * time.Millisecond,
			PromptTokens: 1050,
			TotalTokens:  1270,
		},
	}

	stats := calculateStats(runs)

	if stats.TotalRuns != 3 {
		t.Errorf("expected 3 total runs, got %d", stats.TotalRuns)
	}
	if stats.SuccessfulRuns != 2 {
		t.Errorf("expected 2 successful runs, got %d", stats.SuccessfulRuns)
	}
}

func TestCalculateStatsEmpty(t *testing.T) {
	stats := calculateStats([]RunResult{})

	if stats.TotalRuns != 0 {
		t.Errorf("expected 0 total runs, got %d", stats.TotalRuns)
	}
	if stats.AvgTotalTokens != 0 {
		t.Errorf("expected 0 avg tokens, got %.2f", stats.AvgTotalTokens)
	}
}

func TestCalculateComparison(t *testing.T) {
	direct := RunStats{
		SuccessfulRuns:  3,
		AvgTotalTokens:  10000,
		AvgDuration:     1000,
		TotalCostUSD:    0.10,
		AvgQualityScore: 0.9,
	}

	rlm := RunStats{
		SuccessfulRuns:  3,
		AvgTotalTokens:  4000, // 60% savings
		AvgDuration:     1500, // 50% slower
		TotalCostUSD:    0.04, // 60% savings
		AvgQualityScore: 0.88, // 2.2% lower quality
	}

	comp := calculateComparison(direct, rlm)

	// Token savings: (10000 - 4000) / 10000 = 60%
	if comp.TokenSavingsPercent < 59 || comp.TokenSavingsPercent > 61 {
		t.Errorf("expected ~60%% token savings, got %.1f%%", comp.TokenSavingsPercent)
	}

	// Cost savings: (0.10 - 0.04) / 0.10 = 60%
	if comp.CostSavingsPercent < 59 || comp.CostSavingsPercent > 61 {
		t.Errorf("expected ~60%% cost savings, got %.1f%%", comp.CostSavingsPercent)
	}

	// Latency: (1500 - 1000) / 1000 = 50% overhead
	if comp.LatencyDiffPercent < 49 || comp.LatencyDiffPercent > 51 {
		t.Errorf("expected ~50%% latency overhead, got %.1f%%", comp.LatencyDiffPercent)
	}

	// Should recommend RLM due to high savings with acceptable quality
	if comp.Recommendation == "" {
		t.Error("expected a recommendation")
	}
}

func TestCalculateComparisonNoSavings(t *testing.T) {
	direct := RunStats{
		SuccessfulRuns:  3,
		AvgTotalTokens:  5000,
		TotalCostUSD:    0.05,
		AvgQualityScore: 0.9,
	}

	rlm := RunStats{
		SuccessfulRuns:  3,
		AvgTotalTokens:  6000, // Uses more tokens
		TotalCostUSD:    0.06,
		AvgQualityScore: 0.85,
	}

	comp := calculateComparison(direct, rlm)

	if comp.TokenSavingsPercent >= 0 {
		t.Errorf("expected negative token savings, got %.1f%%", comp.TokenSavingsPercent)
	}

	// Should recommend direct mode
	if comp.Recommendation == "" {
		t.Error("expected a recommendation")
	}
}

func TestScoreQuality(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected string
		minScore float64
		maxScore float64
	}{
		{
			name:     "exact match",
			response: "The answer is 42",
			expected: "The answer is 42",
			minScore: 0.99,
			maxScore: 1.0,
		},
		{
			name:     "partial match",
			response: "The answer is definitely 42 according to the data",
			expected: "The answer is 42",
			minScore: 0.4,
			maxScore: 0.8,
		},
		{
			name:     "no match",
			response: "I don't know",
			expected: "The answer is 42",
			minScore: 0,
			maxScore: 0.3,
		},
		{
			name:     "empty response",
			response: "",
			expected: "The answer is 42",
			minScore: 0,
			maxScore: 0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scoreQuality(tt.response, tt.expected)
			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("expected score in [%.2f, %.2f], got %.2f", tt.minScore, tt.maxScore, score)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected int // expected word count
	}{
		{"hello world", 2},
		{"hello, world!", 2},
		{"one\ntwo\tthree", 3},
		{"", 0},
		{"single", 1},
	}

	for _, tt := range tests {
		words := tokenize(tt.input)
		if len(words) != tt.expected {
			t.Errorf("tokenize(%q) = %d words, expected %d", tt.input, len(words), tt.expected)
		}
	}
}

func TestFilterTestsByTags(t *testing.T) {
	tests := []TestCase{
		{ID: "1", Tags: []string{"small", "analysis"}},
		{ID: "2", Tags: []string{"medium", "refactor"}},
		{ID: "3", Tags: []string{"large", "analysis"}},
		{ID: "4", Tags: []string{"small", "bugfix"}},
	}

	t.Run("include small", func(t *testing.T) {
		filtered := FilterTestsByTags(tests, []string{"small"}, nil)
		if len(filtered) != 2 {
			t.Errorf("expected 2 tests, got %d", len(filtered))
		}
	})

	t.Run("exclude large", func(t *testing.T) {
		filtered := FilterTestsByTags(tests, nil, []string{"large"})
		if len(filtered) != 3 {
			t.Errorf("expected 3 tests, got %d", len(filtered))
		}
	})

	t.Run("include analysis exclude large", func(t *testing.T) {
		filtered := FilterTestsByTags(tests, []string{"analysis"}, []string{"large"})
		if len(filtered) != 1 {
			t.Errorf("expected 1 test, got %d", len(filtered))
		}
		if filtered[0].ID != "1" {
			t.Errorf("expected test 1, got %s", filtered[0].ID)
		}
	})

	t.Run("no filters", func(t *testing.T) {
		filtered := FilterTestsByTags(tests, nil, nil)
		if len(filtered) != 4 {
			t.Errorf("expected 4 tests, got %d", len(filtered))
		}
	})
}

func TestSortTestsBySize(t *testing.T) {
	tests := []TestCase{
		{ID: "1", Tags: []string{"large"}},
		{ID: "2", Tags: []string{"small"}},
		{ID: "3", Tags: []string{"medium"}},
		{ID: "4", Tags: []string{"small"}},
	}

	sorted := SortTestsBySize(tests)

	expectedOrder := []string{"2", "4", "3", "1"}
	for i, tc := range sorted {
		if tc.ID != expectedOrder[i] {
			t.Errorf("position %d: expected %s, got %s", i, expectedOrder[i], tc.ID)
		}
	}
}

func TestDefaultBenchmarkConfig(t *testing.T) {
	config := DefaultBenchmarkConfig()

	if config.Mode != ModeAB {
		t.Errorf("expected default mode AB, got %s", config.Mode)
	}
	if config.Iterations != 3 {
		t.Errorf("expected 3 iterations, got %d", config.Iterations)
	}
	if config.WarmupRuns != 1 {
		t.Errorf("expected 1 warmup run, got %d", config.WarmupRuns)
	}
	if config.Timeout != 5*time.Minute {
		t.Errorf("expected 5 minute timeout, got %v", config.Timeout)
	}
}

func TestPredefinedTestSuites(t *testing.T) {
	small := SmallCodebaseTests()
	if len(small) == 0 {
		t.Error("SmallCodebaseTests should return non-empty slice")
	}
	for _, tc := range small {
		hasSmallTag := false
		for _, tag := range tc.Tags {
			if tag == "small" {
				hasSmallTag = true
				break
			}
		}
		if !hasSmallTag {
			t.Errorf("test %s missing 'small' tag", tc.ID)
		}
	}

	medium := MediumCodebaseTests()
	if len(medium) == 0 {
		t.Error("MediumCodebaseTests should return non-empty slice")
	}

	large := LargeCodebaseTests()
	if len(large) == 0 {
		t.Error("LargeCodebaseTests should return non-empty slice")
	}
}

func TestCalculateSummary(t *testing.T) {
	results := []BenchTestResult{
		{
			DirectStats: RunStats{SuccessfulRuns: 3, AvgTotalTokens: 10000, TotalCostUSD: 0.10, AvgQualityScore: 0.9},
			RLMStats:    RunStats{SuccessfulRuns: 3, AvgTotalTokens: 4000, TotalCostUSD: 0.04, AvgQualityScore: 0.88},
			Comparison:  Comparison{TokenSavingsPercent: 60, CostSavingsPercent: 60, QualityDiffPercent: -2.2},
		},
		{
			DirectStats: RunStats{SuccessfulRuns: 3, AvgTotalTokens: 8000, TotalCostUSD: 0.08, AvgQualityScore: 0.85},
			RLMStats:    RunStats{SuccessfulRuns: 3, AvgTotalTokens: 3500, TotalCostUSD: 0.035, AvgQualityScore: 0.84},
			Comparison:  Comparison{TokenSavingsPercent: 56.25, CostSavingsPercent: 56.25, QualityDiffPercent: -1.2},
		},
	}

	summary := calculateSummary(results)

	if summary.TotalTestCases != 2 {
		t.Errorf("expected 2 total test cases, got %d", summary.TotalTestCases)
	}
	if summary.PassedTestCases != 2 {
		t.Errorf("expected 2 passed test cases, got %d", summary.PassedTestCases)
	}

	// Average token savings: (60 + 56.25) / 2 = 58.125
	expectedSavings := 58.125
	if summary.AvgTokenSavings < expectedSavings-1 || summary.AvgTokenSavings > expectedSavings+1 {
		t.Errorf("expected ~%.2f%% avg token savings, got %.2f%%", expectedSavings, summary.AvgTokenSavings)
	}

	// Should recommend RLM with high savings
	if summary.RecommendedMode != "RLM" {
		t.Errorf("expected RLM recommendation, got %s", summary.RecommendedMode)
	}
}

func TestBenchmarkerCreation(t *testing.T) {
	config := DefaultBenchmarkConfig()
	benchmarker := NewBenchmarker(config, nil, nil)

	if benchmarker == nil {
		t.Fatal("NewBenchmarker returned nil")
	}
	if benchmarker.config.Mode != ModeAB {
		t.Errorf("expected mode AB, got %s", benchmarker.config.Mode)
	}
}

func TestGenerateMarkdownReport(t *testing.T) {
	report := &BenchmarkReport{
		Config:      DefaultBenchmarkConfig(),
		GeneratedAt: time.Now(),
		Summary: ReportSummary{
			TotalTestCases:  2,
			PassedTestCases: 2,
			AvgTokenSavings: 58.0,
			AvgCostSavings:  58.0,
			RecommendedMode: "RLM",
		},
		BenchTestResults: []BenchTestResult{
			{
				TestCase:    TestCase{Name: "Test 1", Description: "A test"},
				DirectStats: RunStats{SuccessfulRuns: 3, AvgTotalTokens: 10000},
				RLMStats:    RunStats{SuccessfulRuns: 3, AvgTotalTokens: 4000},
				Comparison:  Comparison{TokenSavingsPercent: 60},
			},
		},
	}

	md := generateMarkdownReport(report)

	if md == "" {
		t.Error("expected non-empty markdown")
	}
	if !contains(md, "# RLM Benchmark Report") {
		t.Error("missing report title")
	}
	if !contains(md, "## Summary") {
		t.Error("missing summary section")
	}
	if !contains(md, "Test 1") {
		t.Error("missing test result")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestEstimateDirectCost(t *testing.T) {
	cost := estimateDirectCost(1000, 500)

	// (1000 * 0.003 + 500 * 0.015) / 1000 = (3 + 7.5) / 1000 = 0.0105
	expected := 0.0105
	if cost < expected-0.001 || cost > expected+0.001 {
		t.Errorf("expected cost ~$%.4f, got $%.4f", expected, cost)
	}
}

func TestBenchmarkerRunDirect(t *testing.T) {
	// Use existing mockLLM from client_test.go
	mock := newMockLLMWithUsage("Test response", 1000, 200)

	config := BenchmarkConfig{
		Mode:       ModeDirect,
		Iterations: 2,
		WarmupRuns: 0,
		Timeout:    1 * time.Minute,
	}

	benchmarker := NewBenchmarker(config, nil, mock)

	tc := TestCase{
		ID:      "test-1",
		Name:    "Test Case",
		Context: "some context",
		Query:   "some query",
	}

	ctx := context.Background()
	result, err := benchmarker.RunTestCase(ctx, tc)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.DirectRuns) != 2 {
		t.Errorf("expected 2 direct runs, got %d", len(result.DirectRuns))
	}
	if result.DirectStats.SuccessfulRuns != 2 {
		t.Errorf("expected 2 successful runs, got %d", result.DirectStats.SuccessfulRuns)
	}
}
