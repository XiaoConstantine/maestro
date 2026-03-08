package rlm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	dspyrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

// BenchmarkMode defines the execution mode for benchmarking.
type BenchmarkMode string

const (
	ModeDirect BenchmarkMode = "direct" // Direct context stuffing
	ModeRLM    BenchmarkMode = "rlm"    // Recursive Language Model
	ModeAB     BenchmarkMode = "ab"     // Run both and compare
)

const (
	defaultContextWindowTokens = 200000
	minRLMFillRatio            = 0.10
)

var defaultBenchmarkOutputDir = filepath.Join(os.TempDir(), "maestro-benchmark-results")

// BenchmarkConfig configures benchmark execution.
type BenchmarkConfig struct {
	Mode           BenchmarkMode
	Iterations     int           // Number of iterations per test case
	WarmupRuns     int           // Warmup runs before measurement
	Timeout        time.Duration // Timeout per test case
	OutputDir      string        // Directory for reports
	CollectQuality bool          // Whether to collect quality metrics
	// ContextWindowTokens is used to compute prompt fill ratio per call.
	// Defaults to 200k if unset.
	ContextWindowTokens int
}

// DefaultBenchmarkConfig returns sensible defaults.
func DefaultBenchmarkConfig() BenchmarkConfig {
	return BenchmarkConfig{
		Mode:                ModeAB,
		Iterations:          3,
		WarmupRuns:          1,
		Timeout:             5 * time.Minute,
		OutputDir:           defaultBenchmarkOutputDir,
		CollectQuality:      true,
		ContextWindowTokens: defaultContextWindowTokens,
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
	Mode                  BenchmarkMode `json:"mode"`
	Iteration             int           `json:"iteration"`
	StartTime             time.Time     `json:"start_time"`
	Duration              time.Duration `json:"duration"`
	PromptTokens          int           `json:"prompt_tokens"`
	CompletionTokens      int           `json:"completion_tokens"`
	TotalTokens           int           `json:"total_tokens"`
	RawPromptTokens       int           `json:"raw_prompt_tokens,omitempty"`
	RawCompletionTokens   int           `json:"raw_completion_tokens,omitempty"`
	RawTotalTokens        int           `json:"raw_total_tokens,omitempty"`
	PromptOverhead        int           `json:"prompt_overhead,omitempty"`
	CompletionOverhead    int           `json:"completion_overhead,omitempty"`
	CostUSD               float64       `json:"cost_usd"`
	Response              string        `json:"response"`
	Error                 string        `json:"error,omitempty"`
	QualityScore          float64       `json:"quality_score,omitempty"` // 0-1 scale
	MaxPromptTokens       int           `json:"max_prompt_tokens,omitempty"`
	PeakPromptFillRatio   float64       `json:"peak_prompt_fill_ratio,omitempty"`
	PromptTokensSeries    []int         `json:"prompt_tokens_series,omitempty"`
	PromptFillRatioSeries []float64     `json:"prompt_fill_ratio_series,omitempty"`
}

// BenchTestResult aggregates results for a single test case.
type BenchTestResult struct {
	TestCase    TestCase    `json:"test_case"`
	DirectRuns  []RunResult `json:"direct_runs,omitempty"`
	RLMRuns     []RunResult `json:"rlm_runs,omitempty"`
	DirectStats RunStats    `json:"direct_stats,omitempty"`
	RLMStats    RunStats    `json:"rlm_stats,omitempty"`
	Comparison  Comparison  `json:"comparison,omitempty"`
	GeneratedAt time.Time   `json:"generated_at"`
}

// RunStats provides statistical summary of runs.
type RunStats struct {
	TotalRuns              int      `json:"total_runs"`
	SuccessfulRuns         int      `json:"successful_runs"`
	FailedRuns             int      `json:"failed_runs,omitempty"`
	Errors                 []string `json:"errors,omitempty"`
	AvgDuration            float64  `json:"avg_duration_ms"`
	MinDuration            float64  `json:"min_duration_ms"`
	MaxDuration            float64  `json:"max_duration_ms"`
	AvgPromptTokens        float64  `json:"avg_prompt_tokens"`
	AvgCompTokens          float64  `json:"avg_completion_tokens"`
	AvgTotalTokens         float64  `json:"avg_total_tokens"`
	TotalCostUSD           float64  `json:"total_cost_usd"`
	AvgQualityScore        float64  `json:"avg_quality_score,omitempty"`
	AvgMaxPromptTokens     float64  `json:"avg_max_prompt_tokens,omitempty"`
	AvgPeakPromptFillRatio float64  `json:"avg_peak_prompt_fill_ratio,omitempty"`
}

// Comparison shows the efficiency comparison between modes.
type Comparison struct {
	TokenSavingsPercent            float64 `json:"token_savings_percent"`             // Positive = RLM uses fewer tokens
	CostSavingsPercent             float64 `json:"cost_savings_percent"`              // Positive = RLM costs less
	LatencyDiffPercent             float64 `json:"latency_diff_percent"`              // Positive = RLM is slower
	QualityDiffPercent             float64 `json:"quality_diff_percent"`              // Positive = RLM has better quality
	PromptPressureReductionPercent float64 `json:"prompt_pressure_reduction_percent"` // Positive = RLM has lower peak prompt pressure
	EfficiencyRatio                float64 `json:"efficiency_ratio"`                  // Tokens saved per quality point
	Recommendation                 string  `json:"recommendation"`                    // Which mode to use
}

// BenchmarkReport is the full benchmark report.
type BenchmarkReport struct {
	Config           BenchmarkConfig   `json:"config"`
	BenchTestResults []BenchTestResult `json:"test_results"`
	Summary          ReportSummary     `json:"summary"`
	GeneratedAt      time.Time         `json:"generated_at"`
	DurationTotal    time.Duration     `json:"duration_total"`
}

// ReportSummary provides high-level benchmark summary.
type ReportSummary struct {
	TotalTestCases             int     `json:"total_test_cases"`
	PassedTestCases            int     `json:"passed_test_cases"`
	AvgTokenSavings            float64 `json:"avg_token_savings_percent"`
	AvgCostSavings             float64 `json:"avg_cost_savings_percent"`
	AvgLatencyOverhead         float64 `json:"avg_latency_overhead_percent"`
	AvgQualityDiff             float64 `json:"avg_quality_diff_percent"`
	AvgPromptPressureReduction float64 `json:"avg_prompt_pressure_reduction_percent"`
	RecommendedMode            string  `json:"recommended_mode"`
	RecommendationReason       string  `json:"recommendation_reason"`
}

// Benchmarker runs benchmarks comparing direct vs RLM approaches.
type Benchmarker struct {
	config    BenchmarkConfig
	processor *Processor
	directLLM core.LLM
	mu        sync.Mutex
	results   []BenchTestResult

	calibrationMu                   sync.Mutex
	directPromptOverheadPerCall     int
	directCompletionOverheadPerCall int
	directOverheadCalibrated        bool
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
	prompt := fmt.Sprintf("Context:\n%s\n\nQuery: %s", tc.Context, tc.Query)
	promptOverheadPerCall, completionOverheadPerCall := b.calibratedDirectOverhead(ctx)
	warmupRuns := b.effectiveWarmupRuns()

	// Warmup runs (not recorded)
	for i := 0; i < warmupRuns; i++ {
		b.resetDirectState()
		_, _ = b.directLLM.Generate(ctx, prompt)
	}

	// Measured runs
	for i := 0; i < b.config.Iterations; i++ {
		b.resetDirectState()

		run := RunResult{
			Mode:      ModeDirect,
			Iteration: i + 1,
			StartTime: time.Now(),
		}

		tracker := newCallPressureTracker(b.config.ContextWindowTokens)
		meteredLLM := &meteredLLM{LLM: b.directLLM, tracker: tracker}

		timeoutCtx, cancel := context.WithTimeout(ctx, b.config.Timeout)
		resp, err := meteredLLM.Generate(timeoutCtx, prompt)
		cancel()

		run.Duration = time.Since(run.StartTime)

		if err != nil {
			run.Error = err.Error()
		} else {
			run.Response = resp.Content
			rawPrompt := 0
			rawCompletion := 0
			if resp.Usage != nil {
				rawPrompt = resp.Usage.PromptTokens
				rawCompletion = resp.Usage.CompletionTokens
			}
			if rawPrompt <= 0 {
				rawPrompt = estimateTokens(prompt)
			}
			if rawCompletion <= 0 {
				rawCompletion = estimateTokens(resp.Content)
			}

			adjPrompt, adjCompletion, promptOverhead, completionOverhead := applyPerCallOverhead(
				rawPrompt,
				rawCompletion,
				1,
				promptOverheadPerCall,
				completionOverheadPerCall,
			)
			run.RawPromptTokens = rawPrompt
			run.RawCompletionTokens = rawCompletion
			run.RawTotalTokens = rawPrompt + rawCompletion
			run.PromptOverhead = promptOverhead
			run.CompletionOverhead = completionOverhead
			run.PromptTokens = adjPrompt
			run.CompletionTokens = adjCompletion
			run.TotalTokens = run.PromptTokens + run.CompletionTokens

			// Estimate cost (using default pricing)
			run.CostUSD = estimateDirectCost(run.PromptTokens, run.CompletionTokens)
			_, _, promptSeries, _, _, _ := tracker.Snapshot()
			adjustedSeries, adjustedFill, adjustedMaxPrompt, adjustedPeakFill := adjustPromptPressureSeries(
				promptSeries,
				b.config.ContextWindowTokens,
				promptOverheadPerCall,
			)
			run.MaxPromptTokens = adjustedMaxPrompt
			run.PeakPromptFillRatio = adjustedPeakFill
			run.PromptTokensSeries = adjustedSeries
			run.PromptFillRatioSeries = adjustedFill

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
	if b.processor == nil {
		return nil, fmt.Errorf("rlm processor is not configured")
	}
	if b.directLLM == nil {
		return nil, fmt.Errorf("direct LLM is not configured for instrumentation")
	}
	fillRatio := contextFillRatio(tc.Context, b.config.ContextWindowTokens)
	if fillRatio < minRLMFillRatio {
		for i := 0; i < b.config.Iterations; i++ {
			runs = append(runs, RunResult{
				Mode:      ModeRLM,
				Iteration: i + 1,
				StartTime: time.Now(),
				Error: fmt.Sprintf(
					"skipped: context fill ratio %.2f below RLM threshold %.2f",
					fillRatio,
					minRLMFillRatio,
				),
			})
		}
		return runs, nil
	}

	req := Request{
		Context: tc.Context,
		Query:   tc.Query,
	}
	runConfig := b.processor.config
	runConfig.MaxIterations = benchmarkMaxIterations(estimateTokens(tc.Context), runConfig.MaxIterations)
	promptOverheadPerCall, completionOverheadPerCall := b.calibratedDirectOverhead(ctx)
	warmupRuns := b.effectiveWarmupRuns()

	// Warmup runs
	for i := 0; i < warmupRuns; i++ {
		b.resetProcessorState()
		b.resetDirectState()

		warmupProcessor, err := NewProcessorWithLLM(b.directLLM, b.processor.subClient, runConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create warmup processor: %w", err)
		}
		_, _ = warmupProcessor.Process(ctx, req)
	}

	// Measured runs
	for i := 0; i < b.config.Iterations; i++ {
		b.resetProcessorState()
		b.resetDirectState()

		run := RunResult{
			Mode:      ModeRLM,
			Iteration: i + 1,
			StartTime: time.Now(),
		}
		tracker := newCallPressureTracker(b.config.ContextWindowTokens)
		meteredRoot := &meteredLLM{LLM: b.directLLM, tracker: tracker}
		meteredSub := &meteredSubClient{inner: b.processor.subClient, tracker: tracker}
		instrumentedProcessor, procErr := NewProcessorWithLLM(meteredRoot, meteredSub, runConfig)
		if procErr != nil {
			return nil, fmt.Errorf("failed to create instrumented processor: %w", procErr)
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, b.config.Timeout)
		resp, err := instrumentedProcessor.Process(timeoutCtx, req)
		cancel()

		run.Duration = time.Since(run.StartTime)

		if err != nil {
			run.Error = err.Error()
		} else {
			run.Response = resp.Answer
			rawPrompt := resp.PromptTokens
			rawCompletion := resp.CompletionTokens

			_, _, promptSeries, _, trackedPrompt, trackedCompletion := tracker.Snapshot()
			if rawPrompt <= 0 {
				rawPrompt = trackedPrompt
			}
			if rawCompletion <= 0 {
				rawCompletion = trackedCompletion
			}

			callCount := len(promptSeries)
			adjPrompt, adjCompletion, promptOverhead, completionOverhead := applyPerCallOverhead(
				rawPrompt,
				rawCompletion,
				callCount,
				promptOverheadPerCall,
				completionOverheadPerCall,
			)
			run.RawPromptTokens = rawPrompt
			run.RawCompletionTokens = rawCompletion
			run.RawTotalTokens = rawPrompt + rawCompletion
			run.PromptOverhead = promptOverhead
			run.CompletionOverhead = completionOverhead
			run.PromptTokens = adjPrompt
			run.CompletionTokens = adjCompletion
			run.TotalTokens = run.PromptTokens + run.CompletionTokens
			run.CostUSD = resp.CostUSD
			if run.TotalTokens == 0 {
				lower := strings.ToLower(strings.TrimSpace(run.Response))
				if lower == "" || strings.HasPrefix(lower, "error:") || strings.Contains(lower, "failed") {
					run.Error = "rlm returned zero-token output; likely upstream query failure"
				}
			}

			adjustedSeries, adjustedFill, adjustedMaxPrompt, adjustedPeakFill := adjustPromptPressureSeries(
				promptSeries,
				b.config.ContextWindowTokens,
				promptOverheadPerCall,
			)
			run.MaxPromptTokens = adjustedMaxPrompt
			run.PeakPromptFillRatio = adjustedPeakFill
			run.PromptTokensSeries = adjustedSeries
			run.PromptFillRatioSeries = adjustedFill

			if b.config.CollectQuality && tc.Expected != "" {
				run.QualityScore = scoreQuality(run.Response, tc.Expected)
			}
		}

		runs = append(runs, run)
		if i == 0 && run.Error != "" {
			for skipped := i + 1; skipped < b.config.Iterations; skipped++ {
				runs = append(runs, RunResult{
					Mode:      ModeRLM,
					Iteration: skipped + 1,
					StartTime: time.Now(),
					Error:     fmt.Sprintf("skipped after iteration 1 failure: %s", run.Error),
				})
			}
			break
		}
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
		Config:           b.config,
		BenchTestResults: b.results,
		GeneratedAt:      time.Now(),
	}

	// Calculate summary
	report.Summary = calculateSummary(b.results)

	return report
}

// SaveReport saves the benchmark report to disk.
func (b *Benchmarker) SaveReport(report *BenchmarkReport) error {
	outputDir := strings.TrimSpace(b.config.OutputDir)
	if outputDir == "" {
		outputDir = defaultBenchmarkOutputDir
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fallbackDir := filepath.Join(os.TempDir(), "maestro-benchmark-results")
		if fallbackErr := os.MkdirAll(fallbackDir, 0755); fallbackErr != nil {
			return fmt.Errorf("failed to create output directory %q (also tried %q): %w", b.config.OutputDir, fallbackDir, err)
		}
		fmt.Printf("Warning: could not write to %s, falling back to %s\n", b.config.OutputDir, fallbackDir)
		outputDir = fallbackDir
	}
	b.config.OutputDir = outputDir
	if report != nil {
		report.Config.OutputDir = outputDir
	}

	// Save JSON report
	jsonPath := filepath.Join(outputDir, fmt.Sprintf("benchmark_%s.json", time.Now().Format("20060102_150405")))
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write JSON report: %w", err)
	}

	// Save markdown report
	mdPath := filepath.Join(outputDir, fmt.Sprintf("benchmark_%s.md", time.Now().Format("20060102_150405")))
	mdContent := generateMarkdownReport(report)
	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		return fmt.Errorf("failed to write markdown report: %w", err)
	}

	return nil
}

// Helper functions

type callPressureTracker struct {
	mu              sync.Mutex
	contextWindow   int
	maxPromptTokens int
	peakFillRatio   float64
	promptSeries    []int
	fillSeries      []float64
	totalPrompt     int
	totalCompletion int
}

func newCallPressureTracker(contextWindow int) *callPressureTracker {
	if contextWindow <= 0 {
		contextWindow = defaultContextWindowTokens
	}
	return &callPressureTracker{
		contextWindow: contextWindow,
		promptSeries:  make([]int, 0, 16),
		fillSeries:    make([]float64, 0, 16),
	}
}

func (t *callPressureTracker) Record(promptTokens, completionTokens int) {
	if promptTokens < 0 {
		promptTokens = 0
	}
	if completionTokens < 0 {
		completionTokens = 0
	}
	fill := 0.0
	if t.contextWindow > 0 {
		fill = float64(promptTokens) / float64(t.contextWindow)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.promptSeries = append(t.promptSeries, promptTokens)
	t.fillSeries = append(t.fillSeries, fill)
	t.totalPrompt += promptTokens
	t.totalCompletion += completionTokens
	if promptTokens > t.maxPromptTokens {
		t.maxPromptTokens = promptTokens
	}
	if fill > t.peakFillRatio {
		t.peakFillRatio = fill
	}
}

func (t *callPressureTracker) Snapshot() (maxPrompt int, peakFill float64, promptSeries []int, fillSeries []float64, totalPrompt int, totalCompletion int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	promptSeries = make([]int, len(t.promptSeries))
	copy(promptSeries, t.promptSeries)
	fillSeries = make([]float64, len(t.fillSeries))
	copy(fillSeries, t.fillSeries)
	return t.maxPromptTokens, t.peakFillRatio, promptSeries, fillSeries, t.totalPrompt, t.totalCompletion
}

type meteredLLM struct {
	core.LLM
	tracker *callPressureTracker
}

func (m *meteredLLM) Generate(ctx context.Context, prompt string, options ...core.GenerateOption) (*core.LLMResponse, error) {
	resp, err := m.LLM.Generate(ctx, prompt, options...)
	if err != nil {
		return resp, err
	}

	promptTokens := 0
	completionTokens := 0
	if resp != nil && resp.Usage != nil {
		promptTokens = resp.Usage.PromptTokens
		completionTokens = resp.Usage.CompletionTokens
	}
	if promptTokens <= 0 {
		promptTokens = estimateTokens(prompt)
	}
	if completionTokens <= 0 && resp != nil {
		completionTokens = estimateTokens(resp.Content)
	}
	if m.tracker != nil {
		m.tracker.Record(promptTokens, completionTokens)
	}
	return resp, nil
}

type meteredSubClient struct {
	inner   dspyrlm.SubLLMClient
	tracker *callPressureTracker
}

func (m *meteredSubClient) Query(ctx context.Context, prompt string) (dspyrlm.QueryResponse, error) {
	resp, err := m.inner.Query(ctx, prompt)
	if err != nil {
		return resp, err
	}
	promptTokens := resp.PromptTokens
	completionTokens := resp.CompletionTokens
	if promptTokens <= 0 {
		promptTokens = estimateTokens(prompt)
	}
	if completionTokens <= 0 {
		completionTokens = estimateTokens(resp.Response)
	}
	if m.tracker != nil {
		m.tracker.Record(promptTokens, completionTokens)
	}
	return resp, nil
}

func (m *meteredSubClient) QueryBatched(ctx context.Context, prompts []string) ([]dspyrlm.QueryResponse, error) {
	responses, err := m.inner.QueryBatched(ctx, prompts)
	for i, prompt := range prompts {
		if i >= len(responses) {
			break
		}
		resp := responses[i]
		promptTokens := resp.PromptTokens
		completionTokens := resp.CompletionTokens
		if promptTokens <= 0 {
			promptTokens = estimateTokens(prompt)
		}
		if completionTokens <= 0 {
			completionTokens = estimateTokens(resp.Response)
		}
		if m.tracker != nil {
			m.tracker.Record(promptTokens, completionTokens)
		}
	}
	if err == nil {
		for _, resp := range responses {
			trimmed := strings.TrimSpace(resp.Response)
			if strings.HasPrefix(strings.ToLower(trimmed), "error:") {
				return responses, fmt.Errorf("%s", trimmed)
			}
		}
	}
	return responses, err
}

func (b *Benchmarker) resetDirectState() {
	if b.directLLM == nil {
		return
	}
	_ = resetIfSupported(b.directLLM)
}

func (b *Benchmarker) resetProcessorState() {
	if b.processor == nil {
		return
	}
	b.processor.ResetState()
}

func (b *Benchmarker) calibrateDirectOverhead(ctx context.Context) (promptOverheadPerCall, completionOverheadPerCall int) {
	if b.directLLM == nil {
		return 0, 0
	}
	if _, ok := b.directLLM.(stateResetter); !ok {
		return 0, 0
	}

	// Calibrate from a tiny stateless call to approximate fixed provider overhead.
	b.resetDirectState()
	defer b.resetDirectState()

	calibrationTimeout := b.config.Timeout
	if calibrationTimeout <= 0 || calibrationTimeout > 30*time.Second {
		calibrationTimeout = 30 * time.Second
	}
	calibrationCtx, cancel := context.WithTimeout(ctx, calibrationTimeout)
	defer cancel()

	resp, err := b.directLLM.Generate(calibrationCtx, "Reply with exactly: OK")
	if err != nil || resp == nil || resp.Usage == nil {
		return 0, 0
	}

	promptOverheadPerCall = resp.Usage.PromptTokens
	completionOverheadPerCall = resp.Usage.CompletionTokens
	if promptOverheadPerCall < 0 {
		promptOverheadPerCall = 0
	}
	if completionOverheadPerCall < 0 {
		completionOverheadPerCall = 0
	}
	return promptOverheadPerCall, completionOverheadPerCall
}

func (b *Benchmarker) calibratedDirectOverhead(ctx context.Context) (promptOverheadPerCall, completionOverheadPerCall int) {
	b.calibrationMu.Lock()
	defer b.calibrationMu.Unlock()

	if b.directOverheadCalibrated {
		return b.directPromptOverheadPerCall, b.directCompletionOverheadPerCall
	}

	promptOverheadPerCall, completionOverheadPerCall = b.calibrateDirectOverhead(ctx)
	b.directPromptOverheadPerCall = promptOverheadPerCall
	b.directCompletionOverheadPerCall = completionOverheadPerCall
	b.directOverheadCalibrated = true
	return promptOverheadPerCall, completionOverheadPerCall
}

func (b *Benchmarker) effectiveWarmupRuns() int {
	if b.config.WarmupRuns <= 0 {
		return 0
	}
	if b.directLLM != nil && strings.EqualFold(b.directLLM.ProviderName(), "claude-code") {
		return 0
	}
	return b.config.WarmupRuns
}

func benchmarkMaxIterations(contextTokens, configured int) int {
	if configured <= 0 {
		configured = 30
	}

	switch {
	case contextTokens < 100000 && configured > 8:
		return 8
	case contextTokens < 200000 && configured > 12:
		return 12
	default:
		return configured
	}
}

func applyPerCallOverhead(rawPrompt, rawCompletion, callCount, promptOverheadPerCall, completionOverheadPerCall int) (adjustedPrompt, adjustedCompletion, promptOverheadTotal, completionOverheadTotal int) {
	if callCount <= 0 {
		callCount = 1
	}

	promptOverheadTotal = promptOverheadPerCall * callCount
	completionOverheadTotal = completionOverheadPerCall * callCount
	adjustedPrompt = rawPrompt - promptOverheadTotal
	adjustedCompletion = rawCompletion - completionOverheadTotal
	if adjustedPrompt < 0 {
		adjustedPrompt = 0
	}
	if adjustedCompletion < 0 {
		adjustedCompletion = 0
	}
	return adjustedPrompt, adjustedCompletion, promptOverheadTotal, completionOverheadTotal
}

func adjustPromptPressureSeries(promptSeries []int, contextWindowTokens, promptOverheadPerCall int) (adjustedPromptSeries []int, adjustedFillSeries []float64, maxPromptTokens int, peakFillRatio float64) {
	if len(promptSeries) == 0 {
		return []int{}, []float64{}, 0, 0
	}
	if contextWindowTokens <= 0 {
		contextWindowTokens = defaultContextWindowTokens
	}

	adjustedPromptSeries = make([]int, len(promptSeries))
	adjustedFillSeries = make([]float64, len(promptSeries))
	for i, promptTokens := range promptSeries {
		adjusted := promptTokens - promptOverheadPerCall
		if adjusted < 0 {
			adjusted = 0
		}
		adjustedPromptSeries[i] = adjusted
		if adjusted > maxPromptTokens {
			maxPromptTokens = adjusted
		}

		fillRatio := float64(adjusted) / float64(contextWindowTokens)
		adjustedFillSeries[i] = fillRatio
		if fillRatio > peakFillRatio {
			peakFillRatio = fillRatio
		}
	}

	return adjustedPromptSeries, adjustedFillSeries, maxPromptTokens, peakFillRatio
}

func calculateStats(runs []RunResult) RunStats {
	stats := RunStats{TotalRuns: len(runs)}
	if len(runs) == 0 {
		return stats
	}

	var totalDuration, totalPrompt, totalComp, totalTokens, totalCost, totalQuality float64
	var totalMaxPrompt, totalPeakFill float64
	var minDuration, maxDuration float64 = -1, 0

	seen := make(map[string]bool)
	for _, run := range runs {
		if run.Error != "" {
			stats.FailedRuns++
			if !seen[run.Error] {
				seen[run.Error] = true
				stats.Errors = append(stats.Errors, run.Error)
			}
			continue
		}
		stats.SuccessfulRuns++
		durationMs := float64(run.Duration.Milliseconds())
		totalDuration += durationMs
		totalPrompt += float64(run.PromptTokens)
		totalComp += float64(run.CompletionTokens)
		totalTokens += float64(run.TotalTokens)
		totalCost += run.CostUSD
		totalQuality += run.QualityScore
		totalMaxPrompt += float64(run.MaxPromptTokens)
		totalPeakFill += run.PeakPromptFillRatio

		if minDuration < 0 || durationMs < minDuration {
			minDuration = durationMs
		}
		if durationMs > maxDuration {
			maxDuration = durationMs
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
		stats.AvgMaxPromptTokens = totalMaxPrompt / n
		stats.AvgPeakPromptFillRatio = totalPeakFill / n
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
	if direct.AvgPeakPromptFillRatio > 0 {
		comp.PromptPressureReductionPercent = ((direct.AvgPeakPromptFillRatio - rlm.AvgPeakPromptFillRatio) / direct.AvgPeakPromptFillRatio) * 100
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

	var allTokenSavings, allCostSavings, allLatency, allQuality, allPromptPressure float64
	var allComparableTests int
	var eligibleWeightedTokenSavings, eligibleContextWeight float64
	var eligibleCostSavings, eligibleLatency, eligibleQuality, eligiblePromptPressure float64
	var eligibleComparableTests int

	for _, r := range results {
		if r.DirectStats.SuccessfulRuns > 0 && r.RLMStats.SuccessfulRuns > 0 {
			summary.PassedTestCases++
			allTokenSavings += r.Comparison.TokenSavingsPercent
			allCostSavings += r.Comparison.CostSavingsPercent
			allLatency += r.Comparison.LatencyDiffPercent
			allQuality += r.Comparison.QualityDiffPercent
			allPromptPressure += r.Comparison.PromptPressureReductionPercent
			allComparableTests++

			contextTokens := estimateTokens(r.TestCase.Context)
			if contextTokens <= 0 {
				contextTokens = 1
			}
			fillRatio := float64(contextTokens) / float64(defaultContextWindowTokens)
			if fillRatio >= minRLMFillRatio {
				weight := float64(contextTokens)
				eligibleWeightedTokenSavings += r.Comparison.TokenSavingsPercent * weight
				eligibleContextWeight += weight
				eligibleCostSavings += r.Comparison.CostSavingsPercent
				eligibleLatency += r.Comparison.LatencyDiffPercent
				eligibleQuality += r.Comparison.QualityDiffPercent
				eligiblePromptPressure += r.Comparison.PromptPressureReductionPercent
				eligibleComparableTests++
			}
		}
	}

	if eligibleComparableTests > 0 && eligibleContextWeight > 0 {
		n := float64(eligibleComparableTests)
		summary.AvgTokenSavings = eligibleWeightedTokenSavings / eligibleContextWeight
		summary.AvgCostSavings = eligibleCostSavings / n
		summary.AvgLatencyOverhead = eligibleLatency / n
		summary.AvgQualityDiff = eligibleQuality / n
		summary.AvgPromptPressureReduction = eligiblePromptPressure / n
	} else if allComparableTests > 0 {
		n := float64(allComparableTests)
		summary.AvgTokenSavings = allTokenSavings / n
		summary.AvgCostSavings = allCostSavings / n
		summary.AvgLatencyOverhead = allLatency / n
		summary.AvgQualityDiff = allQuality / n
		summary.AvgPromptPressureReduction = allPromptPressure / n
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

func contextFillRatio(context string, contextWindowTokens int) float64 {
	window := contextWindowTokens
	if window <= 0 {
		window = defaultContextWindowTokens
	}
	if window <= 0 {
		return 0
	}
	return float64(estimateTokens(context)) / float64(window)
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
	md += fmt.Sprintf("| Avg Prompt Pressure Reduction | %.1f%% |\n", report.Summary.AvgPromptPressureReduction)
	md += fmt.Sprintf("| **Recommended Mode** | **%s** |\n", report.Summary.RecommendedMode)
	md += fmt.Sprintf("| Reason | %s |\n\n", report.Summary.RecommendationReason)

	// Individual results
	md += "## Test Results\n\n"
	for _, result := range report.BenchTestResults {
		md += fmt.Sprintf("### %s\n\n", result.TestCase.Name)
		md += fmt.Sprintf("%s\n\n", result.TestCase.Description)

		// Report errors for any mode with failures
		for _, stats := range []struct {
			label string
			s     RunStats
		}{{"Direct", result.DirectStats}, {"RLM", result.RLMStats}} {
			if stats.s.FailedRuns > 0 {
				md += fmt.Sprintf("**%s mode**: %d/%d runs failed\n\n", stats.label, stats.s.FailedRuns, stats.s.TotalRuns)
				for _, e := range stats.s.Errors {
					md += fmt.Sprintf("- `%s`\n", e)
				}
				md += "\n"
			}
		}

		if result.DirectStats.SuccessfulRuns > 0 && result.RLMStats.SuccessfulRuns > 0 {
			md += "| Metric | Direct | RLM | Diff |\n"
			md += "|--------|--------|-----|------|\n"
			md += fmt.Sprintf("| Avg Tokens | %.0f | %.0f | %.1f%% |\n",
				result.DirectStats.AvgTotalTokens, result.RLMStats.AvgTotalTokens, result.Comparison.TokenSavingsPercent)
			md += fmt.Sprintf("| Avg Duration (ms) | %.0f | %.0f | %.1f%% |\n",
				result.DirectStats.AvgDuration, result.RLMStats.AvgDuration, result.Comparison.LatencyDiffPercent)
			md += fmt.Sprintf("| Avg Max Prompt Tokens | %.0f | %.0f | %.1f%% |\n",
				result.DirectStats.AvgMaxPromptTokens, result.RLMStats.AvgMaxPromptTokens, result.Comparison.PromptPressureReductionPercent)
			md += fmt.Sprintf("| Avg Peak Fill Ratio | %.3f | %.3f | %.1f%% |\n",
				result.DirectStats.AvgPeakPromptFillRatio, result.RLMStats.AvgPeakPromptFillRatio, result.Comparison.PromptPressureReductionPercent)
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
