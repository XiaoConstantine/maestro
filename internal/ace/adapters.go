package ace

import (
	"context"
	"fmt"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/ace"
	"github.com/XiaoConstantine/maestro/internal/types"
)

// BusinessMetricsAdapter extracts insights from Maestro's BusinessMetrics.
// This bridges the existing metrics collection with ACE's learning system.
type BusinessMetricsAdapter struct {
	metrics types.MetricsCollector
}

// NewBusinessMetricsAdapter creates an adapter for BusinessMetrics.
func NewBusinessMetricsAdapter(metrics types.MetricsCollector) *BusinessMetricsAdapter {
	return &BusinessMetricsAdapter{
		metrics: metrics,
	}
}

// Extract implements ace.Adapter by converting BusinessMetrics trends to InsightCandidates.
func (a *BusinessMetricsAdapter) Extract(ctx context.Context) ([]ace.InsightCandidate, error) {
	if a.metrics == nil {
		return nil, nil
	}

	var insights []ace.InsightCandidate

	// Get the business report for analysis
	report := a.metrics.GenerateReport(ctx)
	if report == nil {
		return nil, nil
	}

	// Convert category performance to strategies and mistakes
	for category, metrics := range report.CategoryMetrics {
		// High precision categories become strategies
		if metrics.Precision > 0.7 && metrics.OutdatedRate > 0.2 {
			insights = append(insights, ace.InsightCandidate{
				Content:    fmt.Sprintf("Focus on %s reviews - high precision (%.0f%%) and good impact (%.0f%% outdated rate)", category, metrics.Precision*100, metrics.OutdatedRate*100),
				Category:   "strategies",
				Confidence: metrics.Precision,
				Source:     "business_metrics",
			})
		}

		// Low impact despite high precision becomes a pattern to avoid
		if metrics.Precision > 0.6 && metrics.OutdatedRate < 0.15 {
			insights = append(insights, ace.InsightCandidate{
				Content:    fmt.Sprintf("Reduce emphasis on %s comments - low developer adoption (%.0f%% outdated rate) despite accuracy", category, metrics.OutdatedRate*100),
				Category:   "patterns",
				Confidence: 0.7,
				Source:     "business_metrics",
			})
		}

		// Low precision categories become mistakes to learn from
		if metrics.Precision < 0.5 && metrics.TotalComments > 5 {
			insights = append(insights, ace.InsightCandidate{
				Content:    fmt.Sprintf("Improve %s detection - current precision (%.0f%%) indicates too many false positives", category, metrics.Precision*100),
				Category:   "mistakes",
				Confidence: 1.0 - metrics.Precision,
				Source:     "business_metrics",
			})
		}
	}

	return insights, nil
}

// FeedbackBridge connects user feedback to ACE outcomes.
// It tracks which comments received positive/negative feedback
// and correlates them with ACE trajectories.
type FeedbackBridge struct {
	// Maps comment ThreadID to trajectory recorder
	trajectoryMap map[int64]*ace.TrajectoryRecorder
	aceManager    *ace.Manager
	mu            sync.RWMutex
}

// NewFeedbackBridge creates a bridge between user feedback and ACE.
func NewFeedbackBridge(aceManager *ace.Manager) *FeedbackBridge {
	return &FeedbackBridge{
		trajectoryMap: make(map[int64]*ace.TrajectoryRecorder),
		aceManager:    aceManager,
	}
}

// RegisterTrajectory associates a trajectory with a comment thread.
func (fb *FeedbackBridge) RegisterTrajectory(threadID int64, recorder *ace.TrajectoryRecorder) {
	if recorder == nil {
		return
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	fb.trajectoryMap[threadID] = recorder
}

// ProcessFeedback handles user feedback and updates ACE accordingly.
func (fb *FeedbackBridge) ProcessFeedback(ctx context.Context, threadID int64, helpful bool, reason string) {
	fb.mu.Lock()
	recorder, exists := fb.trajectoryMap[threadID]
	if exists {
		delete(fb.trajectoryMap, threadID)
	}
	fb.mu.Unlock()

	if !exists || recorder == nil || fb.aceManager == nil {
		return
	}

	// Convert feedback to ACE outcome
	outcome := ace.OutcomeSuccess
	if !helpful {
		outcome = ace.OutcomeFailure
	}

	// Record the reason as a step before ending
	recorder.RecordStep("feedback", "", reason, nil, map[string]any{
		"helpful":   helpful,
		"thread_id": threadID,
	}, nil)

	fb.aceManager.EndTrajectory(ctx, recorder, outcome)
}

// CategoryPerformanceAdapter extracts insights from category-level trends.
type CategoryPerformanceAdapter struct {
	getCategoryStats func(category string) *types.CategoryStats
	categories       []string
}

// NewCategoryPerformanceAdapter creates an adapter for category performance.
func NewCategoryPerformanceAdapter(getCategoryStats func(category string) *types.CategoryStats, categories []string) *CategoryPerformanceAdapter {
	return &CategoryPerformanceAdapter{
		getCategoryStats: getCategoryStats,
		categories:       categories,
	}
}

// Extract implements ace.Adapter.
func (a *CategoryPerformanceAdapter) Extract(ctx context.Context) ([]ace.InsightCandidate, error) {
	if a.getCategoryStats == nil {
		return nil, nil
	}

	var insights []ace.InsightCandidate

	for _, category := range a.categories {
		stats := a.getCategoryStats(category)
		if stats == nil || stats.TotalComments < 3 {
			continue
		}

		// Calculate validation rate
		validationRate := float64(stats.ValidComments) / float64(stats.TotalComments)

		// High validation rate strategies
		if validationRate > 0.8 && stats.TotalComments >= 5 {
			insights = append(insights, ace.InsightCandidate{
				Content:    fmt.Sprintf("Pattern: %s issues have %.0f%% validation rate - continue this detection approach", category, validationRate*100),
				Category:   "strategies",
				Confidence: validationRate,
				Source:     "category_performance",
			})
		}

		// Track improvement needs
		if validationRate < 0.4 && stats.TotalComments >= 5 {
			insights = append(insights, ace.InsightCandidate{
				Content:    fmt.Sprintf("Mistake: %s detection has only %.0f%% validation - review detection criteria", category, validationRate*100),
				Category:   "mistakes",
				Confidence: 1.0 - validationRate,
				Source:     "category_performance",
			})
		}
	}

	return insights, nil
}

// SearchQualityAdapter extracts insights from search result quality.
type SearchQualityAdapter struct {
	successPatterns map[string]int // Query pattern -> success count
	failurePatterns map[string]int // Query pattern -> failure count
	mu              sync.RWMutex
}

// NewSearchQualityAdapter creates an adapter for search quality patterns.
func NewSearchQualityAdapter() *SearchQualityAdapter {
	return &SearchQualityAdapter{
		successPatterns: make(map[string]int),
		failurePatterns: make(map[string]int),
	}
}

// RecordSuccess records a successful search pattern.
func (a *SearchQualityAdapter) RecordSuccess(pattern string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.successPatterns[pattern]++
}

// RecordFailure records a failed search pattern.
func (a *SearchQualityAdapter) RecordFailure(pattern string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failurePatterns[pattern]++
}

// Extract implements ace.Adapter.
func (a *SearchQualityAdapter) Extract(ctx context.Context) ([]ace.InsightCandidate, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var insights []ace.InsightCandidate

	// Extract successful patterns
	for pattern, count := range a.successPatterns {
		if count >= 3 {
			failCount := a.failurePatterns[pattern]
			total := count + failCount
			successRate := float64(count) / float64(total)

			if successRate > 0.7 {
				insights = append(insights, ace.InsightCandidate{
					Content:    fmt.Sprintf("Effective search pattern: %s (%.0f%% success rate)", pattern, successRate*100),
					Category:   "strategies",
					Confidence: successRate,
					Source:     "search_quality",
				})
			}
		}
	}

	// Extract failure patterns
	for pattern, count := range a.failurePatterns {
		if count >= 3 {
			successCount := a.successPatterns[pattern]
			total := count + successCount
			failureRate := float64(count) / float64(total)

			if failureRate > 0.6 {
				insights = append(insights, ace.InsightCandidate{
					Content:    fmt.Sprintf("Avoid search pattern: %s (%.0f%% failure rate)", pattern, failureRate*100),
					Category:   "mistakes",
					Confidence: failureRate,
					Source:     "search_quality",
				})
			}
		}
	}

	return insights, nil
}
