package rlm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

// BenchmarkMode defines the execution mode for benchmarking.
type BenchmarkMode string

const (
	ModeDirect BenchmarkMode = "direct" // Direct context stuffing
	ModeRLM    BenchmarkMode = "rlm"    // Recursive Language Model
	ModeAB     BenchmarkMode = "ab"     // Run both and compare
)

// BenchmarkConfig configures benchmark execution.
type BenchmarkConfig struct {
	Mode           BenchmarkMode
	Iterations     int           // Number of iterations per test case
	WarmupRuns     int           // Warmup runs before measurement
	Timeout        time.Duration // Timeout per test case
	OutputDir      string        // Directory for reports
	CollectQuality bool          // Whether to collect quality metrics
}

// DefaultBenchmarkConfig returns sensible defaults.
func DefaultBenchmarkConfig() BenchmarkConfig {
	return BenchmarkConfig{
		Mode:           ModeAB,
		Iterations:     3,
		WarmupRuns:     1,
		Timeout:        5 * time.Minute,
		OutputDir:      "./benchmark_results",
		CollectQuality: true,
	}
}

// TestCase represents a single benchmark test case.
type TestCase struct {
	ID          string            // Unique identifier
	Name        string            // Human-readable name
	Description string            // What this test measures
	Context     string            // The context/codebase content
	Query       string            // The query to process
	Expected    string            // Expected output (for quality scoring)
	Tags        []string          // Tags for filtering (e.g., "small", "large", "complex")
	Metadata    map[string]string // Additional metadata
}

// RunResult captures the result of a single benchmark run.
type RunResult struct {
	Mode             BenchmarkMode `json:"mode"`
	Iteration        int           `json:"iteration"`
	StartTime        time.Time     `json:"start_time"`
	Duration         time.Duration `json:"duration"`
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
	TotalTokens      int           `json:"total_tokens"`
	CostUSD          float64       `json:"cost_usd"`
	Response         string        `json:"response"`
	Error            string        `json:"error,omitempty"`
	QualityScore     float64       `json:"quality_score,omitempty"` // 0-1 scale
}

// BenchTestResult aggregates results for a single test case.
type BenchTestResult struct {
	TestCase     TestCase    `json:"test_case"`
	DirectRuns   []RunResult `json:"direct_runs,omitempty"`
	RLMRuns      []RunResult `json:"rlm_runs,omitempty"`
	DirectStats  RunStats    `json:"direct_stats,omitempty"`
	RLMStats     RunStats    `json:"rlm_stats,omitempty"`
	Comparison   Comparison  `json:"comparison,omitempty"`
	GeneratedAt  time.Time   `json:"generated_at"`
}

// RunStats provides statistical summary of runs.
type RunStats struct {
	TotalRuns        int     `json:"total_runs"`
	SuccessfulRuns   int     `json:"successful_runs"`
	AvgDuration      float64 `json:"avg_duration_ms"`
	MinDuration      float64 `json:"min_duration_ms"`
	MaxDuration      float64 `json:"max_duration_ms"`
	AvgPromptTokens  float64 `json:"avg_prompt_tokens"`
	AvgCompTokens    float64 `json:"avg_completion_tokens"`
	AvgTotalTokens   float64 `json:"avg_total_tokens"`
	TotalCostUSD     float64 `json:"total_cost_usd"`
	AvgQualityScore  float64 `json:"avg_quality_score,omitempty"`
}

// Comparison shows the efficiency comparison between modes.
type Comparison struct {
	TokenSavingsPercent   float64 `json:"token_savings_percent"`   // Positive = RLM uses fewer tokens
	CostSavingsPercent    float64 `json:"cost_savings_percent"`    // Positive = RLM costs less
	LatencyDiffPercent    float64 `json:"latency_diff_percent"`    // Positive = RLM is slower
	QualityDiffPercent    float64 `json:"quality_diff_percent"`    // Positive = RLM has better quality
	EfficiencyRatio       float64 `json:"efficiency_ratio"`        // Tokens saved per quality point
	Recommendation        string  `json:"recommendation"`          // Which mode to use
}

// BenchmarkReport is the full benchmark report.
type BenchmarkReport struct {
	Config        BenchmarkConfig       `json:"config"`
	BenchTestResults   []BenchTestResult          `json:"test_results"`
	Summary       ReportSummary         `json:"summary"`
	GeneratedAt   time.Time             `json:"generated_at"`
	DurationTotal time.Duration         `json:"duration_total"`
}

// ReportSummary provides high-level benchmark summary.
type ReportSummary struct {
	TotalTestCases       int     `json:"total_test_cases"`
	PassedTestCases      int     `json:"passed_test_cases"`
	AvgTokenSavings      float64 `json:"avg_token_savings_percent"`
	AvgCostSavings       float64 `json:"avg_cost_savings_percent"`
	AvgLatencyOverhead   float64 `json:"avg_latency_overhead_percent"`
	AvgQualityDiff       float64 `json:"avg_quality_diff_percent"`
	RecommendedMode      string  `json:"recommended_mode"`
	RecommendationReason string  `json:"recommendation_reason"`
}

// Benchmarker runs benchmarks comparing direct vs RLM approaches.
type Benchmarker struct {
	config    BenchmarkConfig
	processor *Processor
	directLLM core.LLM
	mu        sync.Mutex
	results   []BenchTestResult
}

// NewBenchmarker creates a new benchmarker.
func NewBenchmarker(config BenchmarkConfig, processor *Processor, directLLM core.LLM) *Benchmarker {
	return &Benchmarker{
		config:    config,
		processor: processor,
		directLLM: directLLM,
		results:   make([]BenchTestResult, 0),
	}
}

// RunTestCase executes a single test case.
func (b *Benchmarker) RunTestCase(ctx context.Context, tc TestCase) (*BenchTestResult, error) {
	result := &BenchTestResult{
		TestCase:    tc,
		GeneratedAt: time.Now(),
	}

	switch b.config.Mode {
	case ModeDirect:
		runs, err := b.runDirect(ctx, tc)
		if err != nil {
			return nil, err
		}
		result.DirectRuns = runs
		result.DirectStats = calculateStats(runs)

	case ModeRLM:
		runs, err := b.runRLM(ctx, tc)
		if err != nil {
			return nil, err
		}
		result.RLMRuns = runs
		result.RLMStats = calculateStats(runs)

	case ModeAB:
		// Run both modes
		directRuns, err := b.runDirect(ctx, tc)
		if err != nil {
			return nil, fmt.Errorf("direct mode failed: %w", err)
		}
		result.DirectRuns = directRuns
		result.DirectStats = calculateStats(directRuns)

		rlmRuns, err := b.runRLM(ctx, tc)
		if err != nil {
			return nil, fmt.Errorf("RLM mode failed: %w", err)
		}
		result.RLMRuns = rlmRuns
		result.RLMStats = calculateStats(rlmRuns)

		// Calculate comparison
		result.Comparison = calculateComparison(result.DirectStats, result.RLMStats)
	}

	b.mu.Lock()
	b.results = append(b.results, *result)
	b.mu.Unlock()

	return result, nil
}

// runDirect executes test case in direct mode (full context stuffing).
func (b *Benchmarker) runDirect(ctx context.Context, tc TestCase) ([]RunResult, error) {
	runs := make([]RunResult, 0, b.config.Iterations)

	// Warmup runs (not recorded)
	for i := 0; i < b.config.WarmupRuns; i++ {
		prompt := fmt.Sprintf("Context:\n%s\n\nQuery: %s", tc.Context, tc.Query)
		_, _ = b.directLLM.Generate(ctx, prompt)
	}

	// Measured runs
	for i := 0; i < b.config.Iterations; i++ {
		run := RunResult{
			Mode:      ModeDirect,
			Iteration: i + 1,
			StartTime: time.Now(),
		}

		prompt := fmt.Sprintf("Context:\n%s\n\nQuery: %s", tc.Context, tc.Query)

		timeoutCtx, cancel := context.WithTimeout(ctx, b.config.Timeout)
		resp, err := b.directLLM.Generate(timeoutCtx, prompt)
		cancel()

		run.Duration = time.Since(run.StartTime)

		if err != nil {
			run.Error = err.Error()
		} else {
			run.Response = resp.Content
			if resp.Usage != nil {
				run.PromptTokens = resp.Usage.PromptTokens
				run.CompletionTokens = resp.Usage.CompletionTokens
				run.TotalTokens = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
			}
			// Estimate cost (using default pricing)
			run.CostUSD = estimateDirectCost(run.PromptTokens, run.CompletionTokens)

			// Quality scoring if expected output provided
			if b.config.CollectQuality && tc.Expected != "" {
				run.QualityScore = scoreQuality(run.Response, tc.Expected)
			}
		}

		runs = append(runs, run)
	}

	return runs, nil
}

// runRLM executes test case in RLM mode.
func (b *Benchmarker) runRLM(ctx context.Context, tc TestCase) ([]RunResult, error) {
	runs := make([]RunResult, 0, b.config.Iterations)

	req := Request{
		Context: tc.Context,
		Query:   tc.Query,
	}

	// Warmup runs
	for i := 0; i < b.config.WarmupRuns; i++ {
		_, _ = b.processor.Process(ctx, req)
	}

	// Measured runs
	for i := 0; i < b.config.Iterations; i++ {
		run := RunResult{
			Mode:      ModeRLM,
			Iteration: i + 1,
			StartTime: time.Now(),
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, b.config.Timeout)
		resp, err := b.processor.Process(timeoutCtx, req)
		cancel()

		run.Duration = time.Since(run.StartTime)

		if err != nil {
			run.Error = err.Error()
		} else {
			run.Response = resp.Answer
			run.PromptTokens = resp.PromptTokens
			run.CompletionTokens = resp.CompletionTokens
			run.TotalTokens = resp.TotalTokens
			run.CostUSD = resp.CostUSD

			if b.config.CollectQuality && tc.Expected != "" {
				run.QualityScore = scoreQuality(run.Response, tc.Expected)
			}
		}

		runs = append(runs, run)
	}

	return runs, nil
}

// RunSuite executes a suite of test cases.
func (b *Benchmarker) RunSuite(ctx context.Context, testCases []TestCase) (*BenchmarkReport, error) {
	startTime := time.Now()

	for _, tc := range testCases {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		_, err := b.RunTestCase(ctx, tc)
		if err != nil {
			// Log error but continue with other tests
			fmt.Printf("Test case %s failed: %v\n", tc.ID, err)
		}
	}

	report := b.GenerateReport()
	report.DurationTotal = time.Since(startTime)

	return report, nil
}

// GenerateReport creates a benchmark report from collected results.
func (b *Benchmarker) GenerateReport() *BenchmarkReport {
	b.mu.Lock()
	defer b.mu.Unlock()

	report := &BenchmarkReport{
		Config:      b.config,
		BenchTestResults: b.results,
		GeneratedAt: time.Now(),
	}

	// Calculate summary
	report.Summary = calculateSummary(b.results)

	return report
}

// SaveReport saves the benchmark report to disk.
func (b *Benchmarker) SaveReport(report *BenchmarkReport) error {
	if err := os.MkdirAll(b.config.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Save JSON report
	jsonPath := filepath.Join(b.config.OutputDir, fmt.Sprintf("benchmark_%s.json", time.Now().Format("20060102_150405")))
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write JSON report: %w", err)
	}

	// Save markdown report
	mdPath := filepath.Join(b.config.OutputDir, fmt.Sprintf("benchmark_%s.md", time.Now().Format("20060102_150405")))
	mdContent := generateMarkdownReport(report)
	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		return fmt.Errorf("failed to write markdown report: %w", err)
	}

	return nil
}

// Helper functions

func calculateStats(runs []RunResult) RunStats {
	stats := RunStats{TotalRuns: len(runs)}
	if len(runs) == 0 {
		return stats
	}

	var totalDuration, totalPrompt, totalComp, totalTokens, totalCost, totalQuality float64
	var minDuration, maxDuration float64 = -1, 0

	for _, run := range runs {
		if run.Error == "" {
			stats.SuccessfulRuns++
			durationMs := float64(run.Duration.Milliseconds())
			totalDuration += durationMs
			totalPrompt += float64(run.PromptTokens)
			totalComp += float64(run.CompletionTokens)
			totalTokens += float64(run.TotalTokens)
			totalCost += run.CostUSD
			totalQuality += run.QualityScore

			if minDuration < 0 || durationMs < minDuration {
				minDuration = durationMs
			}
			if durationMs > maxDuration {
				maxDuration = durationMs
			}
		}
	}

	if stats.SuccessfulRuns > 0 {
		n := float64(stats.SuccessfulRuns)
		stats.AvgDuration = totalDuration / n
		stats.MinDuration = minDuration
		stats.MaxDuration = maxDuration
		stats.AvgPromptTokens = totalPrompt / n
		stats.AvgCompTokens = totalComp / n
		stats.AvgTotalTokens = totalTokens / n
		stats.TotalCostUSD = totalCost
		stats.AvgQualityScore = totalQuality / n
	}

	return stats
}

func calculateComparison(direct, rlm RunStats) Comparison {
	comp := Comparison{}

	if direct.AvgTotalTokens > 0 {
		comp.TokenSavingsPercent = ((direct.AvgTotalTokens - rlm.AvgTotalTokens) / direct.AvgTotalTokens) * 100
	}

	if direct.TotalCostUSD > 0 {
		comp.CostSavingsPercent = ((direct.TotalCostUSD - rlm.TotalCostUSD) / direct.TotalCostUSD) * 100
	}

	if direct.AvgDuration > 0 {
		comp.LatencyDiffPercent = ((rlm.AvgDuration - direct.AvgDuration) / direct.AvgDuration) * 100
	}

	if direct.AvgQualityScore > 0 {
		comp.QualityDiffPercent = ((rlm.AvgQualityScore - direct.AvgQualityScore) / direct.AvgQualityScore) * 100
	}

	// Efficiency ratio: token savings per quality point maintained
	if rlm.AvgQualityScore > 0 {
		comp.EfficiencyRatio = comp.TokenSavingsPercent / (rlm.AvgQualityScore * 100)
	}

	// Generate recommendation
	if comp.TokenSavingsPercent > 20 && comp.QualityDiffPercent >= -5 {
		comp.Recommendation = "RLM recommended - significant token savings with acceptable quality"
	} else if comp.TokenSavingsPercent > 0 && comp.QualityDiffPercent > 5 {
		comp.Recommendation = "RLM recommended - better quality with token savings"
	} else if comp.TokenSavingsPercent < 0 {
		comp.Recommendation = "Direct mode recommended - RLM uses more tokens"
	} else if comp.QualityDiffPercent < -10 {
		comp.Recommendation = "Direct mode recommended - quality degradation too high"
	} else {
		comp.Recommendation = "Either mode acceptable - marginal differences"
	}

	return comp
}

func calculateSummary(results []BenchTestResult) ReportSummary {
	summary := ReportSummary{TotalTestCases: len(results)}

	var totalTokenSavings, totalCostSavings, totalLatency, totalQuality float64
	var comparableTests int

	for _, r := range results {
		if r.DirectStats.SuccessfulRuns > 0 && r.RLMStats.SuccessfulRuns > 0 {
			summary.PassedTestCases++
			totalTokenSavings += r.Comparison.TokenSavingsPercent
			totalCostSavings += r.Comparison.CostSavingsPercent
			totalLatency += r.Comparison.LatencyDiffPercent
			totalQuality += r.Comparison.QualityDiffPercent
			comparableTests++
		}
	}

	if comparableTests > 0 {
		n := float64(comparableTests)
		summary.AvgTokenSavings = totalTokenSavings / n
		summary.AvgCostSavings = totalCostSavings / n
		summary.AvgLatencyOverhead = totalLatency / n
		summary.AvgQualityDiff = totalQuality / n
	}

	// Overall recommendation
	if summary.AvgTokenSavings > 30 && summary.AvgQualityDiff >= -5 {
		summary.RecommendedMode = "RLM"
		summary.RecommendationReason = fmt.Sprintf("%.1f%% token savings with %.1f%% quality difference",
			summary.AvgTokenSavings, summary.AvgQualityDiff)
	} else if summary.AvgTokenSavings > 10 {
		summary.RecommendedMode = "RLM for large contexts"
		summary.RecommendationReason = "Moderate savings, use RLM when context exceeds model limits"
	} else {
		summary.RecommendedMode = "Direct"
		summary.RecommendationReason = "Insufficient token savings to justify RLM overhead"
	}

	return summary
}

func estimateDirectCost(promptTokens, completionTokens int) float64 {
	// Default to Sonnet pricing
	inputPrice := 0.003  // per 1K
	outputPrice := 0.015 // per 1K
	return (float64(promptTokens)*inputPrice + float64(completionTokens)*outputPrice) / 1000
}

func scoreQuality(response, expected string) float64 {
	// Simple similarity scoring - in production, use semantic similarity
	if response == "" {
		return 0
	}
	if response == expected {
		return 1.0
	}

	// Basic overlap scoring
	responseWords := tokenize(response)
	expectedWords := tokenize(expected)

	if len(expectedWords) == 0 {
		return 0.5 // No expected output, assume moderate quality
	}

	matches := 0
	expectedSet := make(map[string]bool)
	for _, w := range expectedWords {
		expectedSet[w] = true
	}
	for _, w := range responseWords {
		if expectedSet[w] {
			matches++
		}
	}

	precision := float64(matches) / float64(len(responseWords))
	recall := float64(matches) / float64(len(expectedWords))

	if precision+recall == 0 {
		return 0
	}

	// F1 score
	return 2 * (precision * recall) / (precision + recall)
}

func tokenize(s string) []string {
	// Simple word tokenization
	var words []string
	var current []rune
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '.' || r == ',' {
			if len(current) > 0 {
				words = append(words, string(current))
				current = nil
			}
		} else {
			current = append(current, r)
		}
	}
	if len(current) > 0 {
		words = append(words, string(current))
	}
	return words
}

func generateMarkdownReport(report *BenchmarkReport) string {
	var md string

	md += "# RLM Benchmark Report\n\n"
	md += fmt.Sprintf("Generated: %s\n\n", report.GeneratedAt.Format(time.RFC3339))

	// Summary
	md += "## Summary\n\n"
	md += fmt.Sprintf("| Metric | Value |\n")
	md += fmt.Sprintf("|--------|-------|\n")
	md += fmt.Sprintf("| Total Test Cases | %d |\n", report.Summary.TotalTestCases)
	md += fmt.Sprintf("| Passed Test Cases | %d |\n", report.Summary.PassedTestCases)
	md += fmt.Sprintf("| Avg Token Savings | %.1f%% |\n", report.Summary.AvgTokenSavings)
	md += fmt.Sprintf("| Avg Cost Savings | %.1f%% |\n", report.Summary.AvgCostSavings)
	md += fmt.Sprintf("| Avg Latency Overhead | %.1f%% |\n", report.Summary.AvgLatencyOverhead)
	md += fmt.Sprintf("| Avg Quality Diff | %.1f%% |\n", report.Summary.AvgQualityDiff)
	md += fmt.Sprintf("| **Recommended Mode** | **%s** |\n", report.Summary.RecommendedMode)
	md += fmt.Sprintf("| Reason | %s |\n\n", report.Summary.RecommendationReason)

	// Individual results
	md += "## Test Results\n\n"
	for _, result := range report.BenchTestResults {
		md += fmt.Sprintf("### %s\n\n", result.TestCase.Name)
		md += fmt.Sprintf("%s\n\n", result.TestCase.Description)

		if result.DirectStats.TotalRuns > 0 && result.RLMStats.TotalRuns > 0 {
			md += "| Metric | Direct | RLM | Diff |\n"
			md += "|--------|--------|-----|------|\n"
			md += fmt.Sprintf("| Avg Tokens | %.0f | %.0f | %.1f%% |\n",
				result.DirectStats.AvgTotalTokens, result.RLMStats.AvgTotalTokens, result.Comparison.TokenSavingsPercent)
			md += fmt.Sprintf("| Avg Duration (ms) | %.0f | %.0f | %.1f%% |\n",
				result.DirectStats.AvgDuration, result.RLMStats.AvgDuration, result.Comparison.LatencyDiffPercent)
			md += fmt.Sprintf("| Total Cost | $%.4f | $%.4f | %.1f%% |\n",
				result.DirectStats.TotalCostUSD, result.RLMStats.TotalCostUSD, result.Comparison.CostSavingsPercent)
			md += fmt.Sprintf("| Quality Score | %.2f | %.2f | %.1f%% |\n",
				result.DirectStats.AvgQualityScore, result.RLMStats.AvgQualityScore, result.Comparison.QualityDiffPercent)
			md += fmt.Sprintf("\n**Recommendation**: %s\n\n", result.Comparison.Recommendation)
		}
	}

	return md
}

// Predefined test suites

// SmallCodebaseTests returns test cases for small codebases (<10K tokens).
func SmallCodebaseTests() []TestCase {
	return []TestCase{
		{
			ID:          "small-function-analysis",
			Name:        "Small Function Analysis",
			Description: "Analyze a simple function implementation",
			Tags:        []string{"small", "analysis"},
		},
		{
			ID:          "small-bug-fix",
			Name:        "Small Bug Fix",
			Description: "Identify and fix a bug in a small module",
			Tags:        []string{"small", "bugfix"},
		},
	}
}

// MediumCodebaseTests returns test cases for medium codebases (10K-50K tokens).
func MediumCodebaseTests() []TestCase {
	return []TestCase{
		{
			ID:          "medium-refactor",
			Name:        "Medium Refactoring Task",
			Description: "Refactor a module with multiple files",
			Tags:        []string{"medium", "refactor"},
		},
		{
			ID:          "medium-feature",
			Name:        "Medium Feature Addition",
			Description: "Add a feature spanning multiple components",
			Tags:        []string{"medium", "feature"},
		},
	}
}

// LargeCodebaseTests returns test cases for large codebases (50K+ tokens).
func LargeCodebaseTests() []TestCase {
	return []TestCase{
		{
			ID:          "large-architecture",
			Name:        "Large Architecture Review",
			Description: "Review architecture of a complex system",
			Tags:        []string{"large", "architecture"},
		},
		{
			ID:          "large-cross-cutting",
			Name:        "Large Cross-Cutting Change",
			Description: "Implement a change affecting many modules",
			Tags:        []string{"large", "cross-cutting"},
		},
	}
}

// FilterTestsByTags filters test cases by tags.
func FilterTestsByTags(tests []TestCase, includeTags, excludeTags []string) []TestCase {
	if len(includeTags) == 0 && len(excludeTags) == 0 {
		return tests
	}

	includeSet := make(map[string]bool)
	for _, t := range includeTags {
		includeSet[t] = true
	}
	excludeSet := make(map[string]bool)
	for _, t := range excludeTags {
		excludeSet[t] = true
	}

	var filtered []TestCase
	for _, tc := range tests {
		include := len(includeTags) == 0
		for _, tag := range tc.Tags {
			if includeSet[tag] {
				include = true
			}
			if excludeSet[tag] {
				include = false
				break
			}
		}
		if include {
			filtered = append(filtered, tc)
		}
	}
	return filtered
}

// SortTestsBySize sorts test cases by estimated size (based on tags).
func SortTestsBySize(tests []TestCase) []TestCase {
	sizeOrder := map[string]int{"small": 1, "medium": 2, "large": 3}
	sorted := make([]TestCase, len(tests))
	copy(sorted, tests)

	sort.Slice(sorted, func(i, j int) bool {
		var sizeI, sizeJ int
		for _, tag := range sorted[i].Tags {
			if s, ok := sizeOrder[tag]; ok {
				sizeI = s
				break
			}
		}
		for _, tag := range sorted[j].Tags {
			if s, ok := sizeOrder[tag]; ok {
				sizeJ = s
				break
			}
		}
		return sizeI < sizeJ
	})

	return sorted
}
