package rlm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

// BenchmarkCLIConfig holds CLI configuration for benchmarks.
type BenchmarkCLIConfig struct {
	Mode       string   // "direct", "rlm", or "ab"
	Iterations int      // Number of iterations
	WarmupRuns int      // Warmup runs
	OutputDir  string   // Output directory for reports
	Tags       []string // Tags to filter tests
	ExcludeTag []string // Tags to exclude
	Verbose    bool     // Verbose output
	TestDir    string   // Directory containing test case files
}

// DefaultBenchmarkCLIConfig returns default CLI config.
func DefaultBenchmarkCLIConfig() BenchmarkCLIConfig {
	return BenchmarkCLIConfig{
		Mode:       "ab",
		Iterations: 3,
		WarmupRuns: 1,
		OutputDir:  "./benchmark_results",
		Verbose:    false,
	}
}

// BenchmarkRunner handles running benchmarks from CLI.
type BenchmarkRunner struct {
	config     BenchmarkCLIConfig
	processor  *Processor
	directLLM  core.LLM
	testCases  []TestCase
}

// NewBenchmarkRunner creates a CLI benchmark runner.
func NewBenchmarkRunner(config BenchmarkCLIConfig, processor *Processor, directLLM core.LLM) *BenchmarkRunner {
	return &BenchmarkRunner{
		config:    config,
		processor: processor,
		directLLM: directLLM,
	}
}

// LoadTestCases loads test cases from a directory or uses built-in tests.
func (r *BenchmarkRunner) LoadTestCases() error {
	if r.config.TestDir != "" {
		return r.loadTestCasesFromDir(r.config.TestDir)
	}

	// Use built-in test suites
	r.testCases = append(r.testCases, SmallCodebaseTests()...)
	r.testCases = append(r.testCases, MediumCodebaseTests()...)
	r.testCases = append(r.testCases, LargeCodebaseTests()...)

	// Filter by tags
	r.testCases = FilterTestsByTags(r.testCases, r.config.Tags, r.config.ExcludeTag)

	// Sort by size for progressive testing
	r.testCases = SortTestsBySize(r.testCases)

	return nil
}

func (r *BenchmarkRunner) loadTestCasesFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read test directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		// Parse test case file format:
		// ---CONTEXT---
		// ...context content...
		// ---QUERY---
		// ...query content...
		// ---EXPECTED---
		// ...expected output (optional)...

		tc := parseTestCaseFile(entry.Name(), string(content))
		if tc != nil {
			r.testCases = append(r.testCases, *tc)
		}
	}

	return nil
}

func parseTestCaseFile(filename, content string) *TestCase {
	tc := &TestCase{
		ID:   strings.TrimSuffix(filename, ".txt"),
		Name: strings.TrimSuffix(filename, ".txt"),
	}

	parts := strings.Split(content, "---")
	var currentSection string

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		switch {
		case trimmed == "CONTEXT":
			currentSection = "context"
		case trimmed == "QUERY":
			currentSection = "query"
		case trimmed == "EXPECTED":
			currentSection = "expected"
		case trimmed == "DESCRIPTION":
			currentSection = "description"
		case trimmed == "TAGS":
			currentSection = "tags"
		case currentSection == "context":
			tc.Context = strings.TrimSpace(part)
		case currentSection == "query":
			tc.Query = strings.TrimSpace(part)
		case currentSection == "expected":
			tc.Expected = strings.TrimSpace(part)
		case currentSection == "description":
			tc.Description = strings.TrimSpace(part)
		case currentSection == "tags":
			tc.Tags = strings.Fields(strings.TrimSpace(part))
		}
	}

	if tc.Context == "" || tc.Query == "" {
		return nil
	}

	return tc
}

// Run executes the benchmark suite.
func (r *BenchmarkRunner) Run(ctx context.Context) (*BenchmarkReport, error) {
	if err := r.LoadTestCases(); err != nil {
		return nil, fmt.Errorf("failed to load test cases: %w", err)
	}

	if len(r.testCases) == 0 {
		return nil, fmt.Errorf("no test cases found")
	}

	mode := parseBenchmarkMode(r.config.Mode)

	benchConfig := BenchmarkConfig{
		Mode:           mode,
		Iterations:     r.config.Iterations,
		WarmupRuns:     r.config.WarmupRuns,
		Timeout:        5 * time.Minute,
		OutputDir:      r.config.OutputDir,
		CollectQuality: true,
	}

	benchmarker := NewBenchmarker(benchConfig, r.processor, r.directLLM)

	if r.config.Verbose {
		fmt.Printf("Running %d test cases in %s mode\n", len(r.testCases), mode)
		fmt.Printf("Iterations: %d, Warmup: %d\n", r.config.Iterations, r.config.WarmupRuns)
		fmt.Println()
	}

	startTime := time.Now()

	for i, tc := range r.testCases {
		if r.config.Verbose {
			fmt.Printf("[%d/%d] Running: %s\n", i+1, len(r.testCases), tc.Name)
		}

		result, err := benchmarker.RunTestCase(ctx, tc)
		if err != nil {
			if r.config.Verbose {
				fmt.Printf("  ERROR: %v\n", err)
			}
			continue
		}

		if r.config.Verbose {
			printBenchTestResult(result, mode)
		}
	}

	report := benchmarker.GenerateReport()
	report.DurationTotal = time.Since(startTime)

	if r.config.Verbose {
		printSummary(report)
	}

	// Save report
	if err := benchmarker.SaveReport(report); err != nil {
		return report, fmt.Errorf("failed to save report: %w", err)
	}

	if r.config.Verbose {
		fmt.Printf("\nReport saved to: %s\n", r.config.OutputDir)
	}

	return report, nil
}

func parseBenchmarkMode(mode string) BenchmarkMode {
	switch strings.ToLower(mode) {
	case "direct":
		return ModeDirect
	case "rlm":
		return ModeRLM
	default:
		return ModeAB
	}
}

func printBenchTestResult(result *BenchTestResult, mode BenchmarkMode) {
	switch mode {
	case ModeDirect:
		fmt.Printf("  Direct: %.0f tokens, %.0fms, $%.4f\n",
			result.DirectStats.AvgTotalTokens,
			result.DirectStats.AvgDuration,
			result.DirectStats.TotalCostUSD)

	case ModeRLM:
		fmt.Printf("  RLM: %.0f tokens, %.0fms, $%.4f\n",
			result.RLMStats.AvgTotalTokens,
			result.RLMStats.AvgDuration,
			result.RLMStats.TotalCostUSD)

	case ModeAB:
		fmt.Printf("  Direct: %.0f tokens | RLM: %.0f tokens | Savings: %.1f%%\n",
			result.DirectStats.AvgTotalTokens,
			result.RLMStats.AvgTotalTokens,
			result.Comparison.TokenSavingsPercent)
	}
}

func printSummary(report *BenchmarkReport) {
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("              BENCHMARK SUMMARY            ")
	fmt.Println("═══════════════════════════════════════════")
	fmt.Printf("Total Test Cases:     %d\n", report.Summary.TotalTestCases)
	fmt.Printf("Passed Test Cases:    %d\n", report.Summary.PassedTestCases)
	fmt.Printf("Total Duration:       %v\n", report.DurationTotal.Round(time.Second))
	fmt.Println()
	fmt.Printf("Avg Token Savings:    %.1f%%\n", report.Summary.AvgTokenSavings)
	fmt.Printf("Avg Cost Savings:     %.1f%%\n", report.Summary.AvgCostSavings)
	fmt.Printf("Avg Latency Overhead: %.1f%%\n", report.Summary.AvgLatencyOverhead)
	fmt.Printf("Avg Quality Diff:     %.1f%%\n", report.Summary.AvgQualityDiff)
	fmt.Println()
	fmt.Printf("Recommended Mode:     %s\n", report.Summary.RecommendedMode)
	fmt.Printf("Reason:               %s\n", report.Summary.RecommendationReason)
	fmt.Println("═══════════════════════════════════════════")
}

// CreateSampleTestCase creates a sample test case for the current directory.
func CreateSampleTestCase(dir, name string) error {
	// Gather context from directory
	context, err := gatherDirectoryContext(dir)
	if err != nil {
		return fmt.Errorf("failed to gather context: %w", err)
	}

	tc := TestCase{
		ID:          name,
		Name:        name,
		Description: fmt.Sprintf("Test case for %s", dir),
		Context:     context,
		Query:       "Analyze this codebase and identify potential improvements.",
		Tags:        []string{estimateSizeTag(len(context)), "analysis"},
	}

	content := formatTestCaseFile(tc)

	outPath := filepath.Join(dir, name+".txt")
	return os.WriteFile(outPath, []byte(content), 0644)
}

func gatherDirectoryContext(dir string) (string, error) {
	var builder strings.Builder
	maxSize := 100000 // ~100KB limit

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Skip hidden, vendor, node_modules
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		// Only include code files
		ext := strings.ToLower(filepath.Ext(path))
		codeExts := map[string]bool{
			".go": true, ".py": true, ".js": true, ".ts": true,
			".java": true, ".c": true, ".cpp": true, ".h": true,
			".rs": true, ".rb": true, ".php": true,
		}
		if !codeExts[ext] {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(dir, path)
		builder.WriteString(fmt.Sprintf("=== %s ===\n", relPath))
		builder.Write(content)
		builder.WriteString("\n\n")

		if builder.Len() > maxSize {
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil && err != filepath.SkipAll {
		return "", err
	}

	return builder.String(), nil
}

func estimateSizeTag(contextLen int) string {
	// Rough estimate: 4 chars per token
	tokens := contextLen / 4

	switch {
	case tokens < 10000:
		return "small"
	case tokens < 50000:
		return "medium"
	default:
		return "large"
	}
}

func formatTestCaseFile(tc TestCase) string {
	var builder strings.Builder

	builder.WriteString("---DESCRIPTION---\n")
	builder.WriteString(tc.Description)
	builder.WriteString("\n\n---TAGS---\n")
	builder.WriteString(strings.Join(tc.Tags, " "))
	builder.WriteString("\n\n---CONTEXT---\n")
	builder.WriteString(tc.Context)
	builder.WriteString("\n\n---QUERY---\n")
	builder.WriteString(tc.Query)

	if tc.Expected != "" {
		builder.WriteString("\n\n---EXPECTED---\n")
		builder.WriteString(tc.Expected)
	}

	return builder.String()
}

// GenerateEfficiencyReport generates a detailed efficiency analysis.
func GenerateEfficiencyReport(report *BenchmarkReport) string {
	var builder strings.Builder

	builder.WriteString("# RLM Efficiency Analysis Report\n\n")
	builder.WriteString(fmt.Sprintf("Generated: %s\n\n", report.GeneratedAt.Format(time.RFC3339)))

	// Executive Summary
	builder.WriteString("## Executive Summary\n\n")
	if report.Summary.AvgTokenSavings > 0 {
		builder.WriteString(fmt.Sprintf("RLM achieves **%.1f%% token savings** compared to direct context stuffing, ",
			report.Summary.AvgTokenSavings))
		builder.WriteString(fmt.Sprintf("with **%.1f%% cost reduction** ", report.Summary.AvgCostSavings))

		if report.Summary.AvgLatencyOverhead > 0 {
			builder.WriteString(fmt.Sprintf("at a **%.1f%% latency overhead**.\n\n", report.Summary.AvgLatencyOverhead))
		} else {
			builder.WriteString("with no latency overhead.\n\n")
		}
	} else {
		builder.WriteString("RLM did not demonstrate token savings in this benchmark run.\n\n")
	}

	// Breakeven Analysis
	builder.WriteString("## Breakeven Analysis\n\n")
	if report.Summary.AvgTokenSavings > 0 && report.Summary.AvgLatencyOverhead > 0 {
		// Calculate when RLM becomes worthwhile
		// If RLM saves X% tokens but adds Y% latency, it's worthwhile when:
		// Token cost savings > Latency cost
		savingsRatio := report.Summary.AvgTokenSavings / report.Summary.AvgLatencyOverhead
		builder.WriteString(fmt.Sprintf("- Efficiency Ratio: %.2f (token savings per latency overhead unit)\n", savingsRatio))

		if savingsRatio > 1 {
			builder.WriteString("- **Recommendation**: Use RLM - token savings exceed latency costs\n")
		} else {
			builder.WriteString("- **Recommendation**: Use Direct mode for latency-sensitive workloads\n")
		}
	}
	builder.WriteString("\n")

	// Per-Size Analysis
	builder.WriteString("## Analysis by Codebase Size\n\n")

	sizeGroups := groupResultsBySize(report.BenchTestResults)
	for _, size := range []string{"small", "medium", "large"} {
		results := sizeGroups[size]
		if len(results) == 0 {
			continue
		}

		builder.WriteString(fmt.Sprintf("### %s Codebases\n\n", strings.Title(size)))

		var avgSavings, avgLatency float64
		for _, r := range results {
			avgSavings += r.Comparison.TokenSavingsPercent
			avgLatency += r.Comparison.LatencyDiffPercent
		}
		avgSavings /= float64(len(results))
		avgLatency /= float64(len(results))

		builder.WriteString(fmt.Sprintf("- Test Cases: %d\n", len(results)))
		builder.WriteString(fmt.Sprintf("- Avg Token Savings: %.1f%%\n", avgSavings))
		builder.WriteString(fmt.Sprintf("- Avg Latency Overhead: %.1f%%\n", avgLatency))

		switch size {
		case "small":
			if avgSavings < 10 {
				builder.WriteString("- **Verdict**: Direct mode preferred for small codebases\n")
			} else {
				builder.WriteString("- **Verdict**: RLM provides modest benefits\n")
			}
		case "medium":
			builder.WriteString("- **Verdict**: RLM recommended for moderate savings\n")
		case "large":
			builder.WriteString("- **Verdict**: RLM strongly recommended for large contexts\n")
		}
		builder.WriteString("\n")
	}

	// Recommendations
	builder.WriteString("## Recommendations\n\n")
	builder.WriteString("Based on this benchmark:\n\n")

	builder.WriteString(fmt.Sprintf("1. **Primary Mode**: %s\n", report.Summary.RecommendedMode))
	builder.WriteString(fmt.Sprintf("   - %s\n\n", report.Summary.RecommendationReason))

	if report.Summary.AvgTokenSavings > 30 {
		builder.WriteString("2. **Token Budget**: Consider using RLM's token budget feature to control costs\n\n")
	}

	if report.Summary.AvgLatencyOverhead > 50 {
		builder.WriteString("3. **Latency**: For time-sensitive operations, consider direct mode or reducing RLM iterations\n\n")
	}

	return builder.String()
}

func groupResultsBySize(results []BenchTestResult) map[string][]BenchTestResult {
	groups := make(map[string][]BenchTestResult)

	for _, r := range results {
		size := "unknown"
		for _, tag := range r.TestCase.Tags {
			if tag == "small" || tag == "medium" || tag == "large" {
				size = tag
				break
			}
		}
		groups[size] = append(groups[size], r)
	}

	return groups
}
