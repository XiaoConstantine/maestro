package main

import (
	"context"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/google/go-github/v68/github"
)

// BusinessReport represents a comprehensive snapshot of our review system's metrics.
type BusinessReport struct {
	GeneratedAt       time.Time
	CategoryMetrics   map[string]CategoryMetrics
	WeeklyActiveUsers int
	TotalReviews      int
}

// CategoryMetrics holds the core metrics for a specific review category.
type CategoryMetrics struct {
	Precision     float64   // Ratio of valid comments to total comments
	OutdatedRate  float64   // Ratio of modified lines to total flagged lines
	TotalComments int       // Total number of comments in this category
	LastUpdated   time.Time // When this category was last updated
}

// BusinessMetrics tracks the practical impact of code reviews.
type BusinessMetrics struct {
	logger *logging.Logger
	mu     sync.RWMutex

	// Category-level metrics
	categoryMetrics map[string]*CategoryStats
	activeSessions  map[int]*ReviewSession   // Maps PR number to session
	activeThreads   map[int64]*ThreadMetrics // Maps thread ID to metrics
	// Overall system metrics
	weeklyActiveUsers int
	totalReviews      int
}

// ReviewSession represents a single code review interaction.
type ReviewSession struct {
	PRNumber   int
	StartTime  time.Time
	Comments   []*PRReviewComment
	Categories map[string]int // Tracks comments per category
}

type ThreadMetrics struct {
	CreatedAt  time.Time
	ResolvedAt time.Time
	Category   string
	IsResolved bool
	ModifiedAt time.Time // When the associated code was last modified
}

// CategoryStats tracks statistics for each review category.
type CategoryStats struct {
	TotalComments int
	ValidComments int // Comments deemed technically correct
	OutdatedLines int // Lines modified after being flagged
	TotalLines    int // Total lines flagged
	LastUpdated   time.Time
}

type ReviewMetricsCollector interface {
	// Session tracking
	StartReviewSession(ctx context.Context, prNumber int)
	TrackNewThread(ctx context.Context, threadID int64, comment PRReviewComment)

	// Comment impact tracking
	TrackCommentModification(ctx context.Context, comment *github.PullRequestComment)
	TrackHistoricalComment(ctx context.Context, comment PRReviewComment)

	// Resolution tracking
	TrackCommentResolution(ctx context.Context, threadID int64, category string)

	// Reporting
	GetOutdatedRate(category string) float64
	GetPrecision(category string) float64
	GenerateReport(ctx context.Context) *BusinessReport
}

func (cs *CategoryStats) GetOutdatedRate() float64 {
	if cs.TotalLines == 0 {
		return 0
	}
	return float64(cs.OutdatedLines) / float64(cs.TotalLines)
}

func NewBusinessMetrics(logger *logging.Logger) *BusinessMetrics {
	return &BusinessMetrics{
		logger:            logger,
		categoryMetrics:   make(map[string]*CategoryStats),
		activeSessions:    make(map[int]*ReviewSession),
		activeThreads:     make(map[int64]*ThreadMetrics),
		weeklyActiveUsers: 0,
		totalReviews:      0,
	}
}

func (bm *BusinessMetrics) StartReviewSession(ctx context.Context, prNumber int) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	session := &ReviewSession{
		PRNumber:   prNumber,
		StartTime:  time.Now(),
		Categories: make(map[string]int),
	}

	bm.activeSessions[prNumber] = session
	bm.logger.Debug(ctx, "Started new review session for PR #%d", prNumber)
}

func (bm *BusinessMetrics) TrackNewThread(ctx context.Context, threadID int64, comment PRReviewComment) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	thread := &ThreadMetrics{
		CreatedAt: time.Now(),
		Category:  comment.Category,
	}

	bm.activeThreads[threadID] = thread

	// Update category stats
	if stats, exists := bm.categoryMetrics[comment.Category]; exists {
		stats.TotalComments++
		stats.LastUpdated = time.Now()
	} else {
		bm.categoryMetrics[comment.Category] = &CategoryStats{
			TotalComments: 1,
			LastUpdated:   time.Now(),
		}
	}

	bm.logger.Debug(ctx, "Tracked new thread %d in category %s", threadID, comment.Category)
}

func (bm *BusinessMetrics) TrackHistoricalComment(ctx context.Context, comment PRReviewComment) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	// Update category stats for historical tracking
	stats, exists := bm.categoryMetrics[comment.Category]
	if !exists {
		stats = &CategoryStats{}
		bm.categoryMetrics[comment.Category] = stats
	}

	stats.TotalComments++

	// If the comment has already led to code changes, track that
	if comment.Resolved {
		stats.OutdatedLines++
		stats.TotalLines++
	} else {
		stats.TotalLines++
	}

	stats.LastUpdated = time.Now()
	bm.logger.Debug(ctx, "Tracked historical comment in category %s", comment.Category)
}

func (bm *BusinessMetrics) TrackReviewStart(ctx context.Context, category string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	stats, exists := bm.categoryMetrics[category]
	if !exists {
		stats = &CategoryStats{}
		bm.categoryMetrics[category] = stats
	}

	stats.LastUpdated = time.Now()
	bm.logger.Debug(ctx, "Started review tracking for category %s", category)
}

// TrackReviewComment records a new review comment and its initial metrics.
func (bm *BusinessMetrics) TrackReviewComment(ctx context.Context, comment PRReviewComment, isValid bool) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	stats, exists := bm.categoryMetrics[comment.Category]
	if !exists {
		stats = &CategoryStats{}
		bm.categoryMetrics[comment.Category] = stats
	}

	stats.TotalComments++
	if isValid {
		stats.ValidComments++
	}

	// Track the lines being reviewed
	stats.TotalLines++
	stats.LastUpdated = time.Now()

	bm.logger.Debug(ctx, "Tracked new review comment in category %s (valid: %v)",
		comment.Category, isValid)
}

func (bm *BusinessMetrics) TrackCommentResolution(ctx context.Context, threadID int64, resolution ResolutionOutcome) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	thread, exists := bm.activeThreads[threadID]
	if !exists {
		bm.logger.Warn(ctx, "Attempted to track resolution for unknown thread %d", threadID)
		return
	}

	thread.IsResolved = resolution == ResolutionAccepted
	thread.ResolvedAt = time.Now()

	// Update category stats
	if stats, exists := bm.categoryMetrics[thread.Category]; exists {
		if thread.IsResolved {
			stats.OutdatedLines++
		}
		stats.LastUpdated = time.Now()
	}

	bm.logger.Debug(ctx, "Tracked resolution for thread %d with outcome %s",
		threadID, resolution)
}

// TrackLineModification records when a reviewed line is modified.
func (bm *BusinessMetrics) TrackLineModification(ctx context.Context, category string, lineNumber int) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if stats, exists := bm.categoryMetrics[category]; exists {
		stats.OutdatedLines++
		stats.LastUpdated = time.Now()

		bm.logger.Debug(ctx, "Tracked line modification in category %s (line: %d)",
			category, lineNumber)
	}
}

// GetPrecision calculates the precision rate for a category.
func (bm *BusinessMetrics) GetPrecision(category string) float64 {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if stats, exists := bm.categoryMetrics[category]; exists && stats.TotalComments > 0 {
		return float64(stats.ValidComments) / float64(stats.TotalComments)
	}
	return 0
}

// GetOutdatedRate calculates the outdated rate for a category.
func (bm *BusinessMetrics) GetOutdatedRate(category string) float64 {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if stats, exists := bm.categoryMetrics[category]; exists && stats.TotalLines > 0 {
		return float64(stats.OutdatedLines) / float64(stats.TotalLines)
	}
	return 0
}

// GenerateReport creates a comprehensive metrics report.
func (bm *BusinessMetrics) GenerateReport(ctx context.Context) *BusinessReport {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	report := &BusinessReport{
		GeneratedAt:       time.Now(),
		CategoryMetrics:   make(map[string]CategoryMetrics),
		WeeklyActiveUsers: bm.weeklyActiveUsers,
		TotalReviews:      bm.totalReviews,
	}

	for category, stats := range bm.categoryMetrics {
		precision := float64(stats.ValidComments) / float64(stats.TotalComments)
		outdatedRate := float64(stats.OutdatedLines) / float64(stats.TotalLines)

		report.CategoryMetrics[category] = CategoryMetrics{
			Precision:     precision,
			OutdatedRate:  outdatedRate,
			TotalComments: stats.TotalComments,
			LastUpdated:   stats.LastUpdated,
		}
	}

	return report
}

func (bm *BusinessMetrics) GetCategoryMetrics(category string) *CategoryStats {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	// If we have stats for this category, return them
	if stats, exists := bm.categoryMetrics[category]; exists {
		return stats
	}

	// Return empty stats if category not found
	return &CategoryStats{}
}

// GetOverallOutdatedRate calculates the weighted average of outdated rates across all categories.
func (bm *BusinessMetrics) GetOverallOutdatedRate() float64 {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	var totalOutdated, totalLines float64
	for _, stats := range bm.categoryMetrics {
		totalOutdated += float64(stats.OutdatedLines)
		totalLines += float64(stats.TotalLines)
	}

	if totalLines == 0 {
		return 0
	}

	return totalOutdated / totalLines
}

// GetWeeklyActiveUsers returns the count of unique users in the past week

func (bm *BusinessMetrics) GetWeeklyActiveUsers() int {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return bm.weeklyActiveUsers
}

// GetReviewResponseRate calculates the percentage of comments that receive responses.
func (bm *BusinessMetrics) GetReviewResponseRate() float64 {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	var totalThreads, respondedThreads int
	for _, thread := range bm.activeThreads {
		totalThreads++
		// A thread is considered "responded to" if it's resolved or has been modified
		if thread.IsResolved || !thread.ModifiedAt.IsZero() {
			respondedThreads++
		}
	}

	if totalThreads == 0 {
		return 0
	}

	return float64(respondedThreads) / float64(totalThreads) * 100
}

// IncrementWeeklyActiveUsers increases the weekly active user count.
func (bm *BusinessMetrics) IncrementWeeklyActiveUsers() {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.weeklyActiveUsers++
}

// ResetWeeklyStats resets weekly counters - should be called by a timer or scheduler.
func (bm *BusinessMetrics) ResetWeeklyStats() {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.weeklyActiveUsers = 0
}
