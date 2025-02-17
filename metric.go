package main

import (
	"context"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/google/go-github/v68/github"
)

type MetricsCollector interface {
	TrackReviewStart(ctx context.Context, category string)
	TrackNewThread(ctx context.Context, threadID int64, comment PRReviewComment)
	TrackCommentResolution(ctx context.Context, threadID int64, resolution ResolutionOutcome)
	TrackReviewComment(ctx context.Context, comment PRReviewComment, isValid bool)
	TrackHistoricalComment(ctx context.Context, comment PRReviewComment)
	TrackUserFeedback(ctx context.Context, threadID int64, helpful bool, reason string)
	GetOutdatedRate(category string) float64
	GetPrecision(category string) float64
	GenerateReport(ctx context.Context) *BusinessReport
	GetCategoryMetrics(category string) *CategoryStats
	GetOverallOutdatedRate() float64
	GetWeeklyActiveUsers() int
	GetReviewResponseRate() float64
	StartOptimizationCycle(ctx context.Context)
	StartReviewSession(ctx context.Context, prNumber int)
	StartThreadTracking(ctx context.Context, comment PRReviewComment)
	TrackValidationResult(ctx context.Context, category string, validated bool)
	TrackDetectionResults(ctx context.Context, issueCount int)
}

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

	detectionStats *DetectionStats
	// Category-level metrics
	categoryMetrics map[string]*CategoryStats
	activeSessions  map[int]*ReviewSession   // Maps PR number to session
	activeThreads   map[int64]*ThreadMetrics // Maps thread ID to metrics
	// Overall system metrics
	weeklyActiveUsers int
	totalReviews      int
	userFeedback      map[string]*FeedbackStats
}

type FeedbackStats struct {
	Helpful    int
	Unhelpful  int
	Reasons    map[string]int // Track feedback reasons
	LastRating time.Time
}

// ReviewSession represents a single code review interaction.
type ReviewSession struct {
	PRNumber   int
	StartTime  time.Time
	Comments   []*PRReviewComment
	Categories map[string]int // Tracks comments per category
}

type ThreadMetrics struct {
	CreatedAt   time.Time
	ResolvedAt  time.Time
	Category    string
	IsResolved  bool
	ModifiedAt  time.Time // When the associated code was last modified
	LastComment *PRReviewComment
}

// CategoryStats tracks statistics for each review category.
type CategoryStats struct {
	TotalComments  int
	ValidComments  int // Comments deemed technically correct
	OutdatedLines  int // Lines modified after being flagged
	TotalLines     int // Total lines flagged
	LastUpdated    time.Time
	TotalValidated int     // Total number of issues that went through validation
	ValidIssues    int     // Number of issues that passed validation
	ValidationRate float64 // Ratio of valid issues to total validated
}

// TrendAnalysis represents the performance trends for a review category.
type TrendAnalysis struct {
	Category     string
	OutdatedRate float64
	Precision    float64
	Impact       float64 // Composite score of effectiveness
	Status       string  // high_performing, low_impact, or needs_improvement
	LastUpdated  time.Time
}

// CommentIdentifier provides mapping between comments and their categories.
type CommentIdentifier struct {
	ID       string
	Category string
	ThreadID int64
}

type DetectionStats struct {
	TotalIssuesDetected int
	TotalDetectionRuns  int
	LastUpdated         time.Time
	DetectionLatency    time.Duration  // Track how long detection takes
	IssuesPerFile       map[string]int // Track issues found per file
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

func NewBusinessMetrics(logger *logging.Logger) MetricsCollector {
	return &BusinessMetrics{
		logger:            logger,
		categoryMetrics:   make(map[string]*CategoryStats),
		activeSessions:    make(map[int]*ReviewSession),
		activeThreads:     make(map[int64]*ThreadMetrics),
		weeklyActiveUsers: 0,
		totalReviews:      0,
		detectionStats: &DetectionStats{
			IssuesPerFile: make(map[string]int),
		},
		userFeedback: make(map[string]*FeedbackStats),
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

func (bm *BusinessMetrics) TrackUserFeedback(ctx context.Context, threadID int64, helpful bool, reason string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	commentKey := strconv.FormatInt(threadID, 10)

	stats, exists := bm.userFeedback[commentKey]
	if !exists {
		stats = &FeedbackStats{
			Reasons: make(map[string]int),
		}
		bm.userFeedback[commentKey] = stats
	}

	if helpful {
		stats.Helpful++
	} else {
		stats.Unhelpful++
	}
	stats.Reasons[reason]++
	stats.LastRating = time.Now()

	// Update category metrics
	if category, exists := bm.findCommentCategory(threadID); exists {
		if stats, ok := bm.categoryMetrics[category]; ok {
			if helpful {
				stats.ValidComments++
			}
		}
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

func (bm *BusinessMetrics) AnalyzeOutdatedRateTrends(ctx context.Context) map[string]*TrendAnalysis {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	trends := make(map[string]*TrendAnalysis)

	for category, stats := range bm.categoryMetrics {
		outdatedRate := stats.GetOutdatedRate()
		precision := float64(stats.ValidComments) / float64(stats.TotalComments)

		trend := &TrendAnalysis{
			Category:     category,
			OutdatedRate: outdatedRate,
			Precision:    precision,
			Impact:       calculateImpactScore(outdatedRate, precision),
			LastUpdated:  stats.LastUpdated,
		}

		// Apply the paper's criteria
		if outdatedRate > 0.25 && precision > 0.65 {
			trend.Status = "high_performing"
		} else if outdatedRate < 0.15 && precision > 0.65 {
			trend.Status = "low_impact"
		} else if precision < 0.50 {
			trend.Status = "needs_improvement"
		}

		trends[category] = trend
	}

	return trends
}

func (bm *BusinessMetrics) StartOptimizationCycle(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				trends := bm.AnalyzeOutdatedRateTrends(ctx)

				// Generate optimization report
				report := bm.GenerateReport(ctx)

				// Process optimization results using the report data
				for category, metrics := range report.CategoryMetrics {
					if _, exists := trends[category]; exists {
						bm.logger.Info(ctx, "Category %s metrics - Outdated Rate: %.2f%%, Precision: %.2f%%",
							category,
							metrics.OutdatedRate*100,
							metrics.Precision*100)

						// Take action based on metrics
						if metrics.OutdatedRate < 0.15 && metrics.Precision > 0.65 {
							bm.logger.Info(ctx, "Category %s marked for review - low impact despite high precision",
								category)
						}
					}
				}

				// Store optimization results
				if err := bm.storeOptimizationResults(ctx, trends, report); err != nil {
					bm.logger.Error(ctx, "Failed to store optimization results: %v", err)
				}
			}
		}
	}()
}

// findCommentCategory retrieves the category for a given threadID.
func (bm *BusinessMetrics) findCommentCategory(threadID int64) (string, bool) {
	if thread, exists := bm.activeThreads[threadID]; exists {
		return thread.Category, true
	}

	// Check active sessions if not found in active threads
	for _, session := range bm.activeSessions {
		for _, comment := range session.Comments {
			if comment.ThreadID != nil && *comment.ThreadID == threadID {
				return comment.Category, true
			}
		}
	}

	return "", false
}

// Helper method to create new thread metrics.
func (bm *BusinessMetrics) StartThreadTracking(ctx context.Context, comment PRReviewComment) {
	if comment.ThreadID == nil {
		bm.logger.Warn(ctx, "Comment has no ThreadID, skipping thread metrics")
		return
	}

	metrics := &ThreadMetrics{
		CreatedAt:   time.Now(),
		Category:    comment.Category,
		IsResolved:  false,
		LastComment: &comment,
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.activeThreads[*comment.ThreadID] = metrics
}

func (bm *BusinessMetrics) storeOptimizationResults(ctx context.Context, trends map[string]*TrendAnalysis, report *BusinessReport) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	// Update category metrics with trend analysis
	for category, trend := range trends {
		if stats, exists := bm.categoryMetrics[category]; exists {
			stats.LastUpdated = time.Now()

			// Find all threads in this category
			var categoryThreads []int64
			for threadID, thread := range bm.activeThreads {
				if thread.Category == category {
					categoryThreads = append(categoryThreads, threadID)
				}
			}

			// Log optimization insights with thread information
			bm.logger.Info(ctx, "Category %s optimization: status=%s, impact=%.2f, active_threads=%d",
				category, trend.Status, trend.Impact, len(categoryThreads))
		}
	}

	return nil
}

func (bm *BusinessMetrics) TrackDetectionResults(ctx context.Context, issueCount int) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bm.detectionStats.TotalIssuesDetected += issueCount
	bm.detectionStats.TotalDetectionRuns++
	bm.detectionStats.LastUpdated = time.Now()

	bm.logger.Debug(ctx, "Tracked detection results: %d issues found", issueCount)
}

func (bm *BusinessMetrics) TrackValidationResult(ctx context.Context, category string, validated bool) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	stats, exists := bm.categoryMetrics[category]
	if !exists {
		stats = &CategoryStats{}
		bm.categoryMetrics[category] = stats
	}

	stats.TotalValidated++
	if validated {
		stats.ValidIssues++

		stats.ValidationRate = float64(stats.ValidIssues) / float64(stats.TotalValidated)
	}
	stats.LastUpdated = time.Now()

	bm.logger.Debug(ctx, "Tracked validation result for category %s: validated=%v, rate=%.2f%%",
		category, validated, stats.ValidationRate*100)
}

// calculateImpactScore computes a composite score based on outdated rate and precision.
func calculateImpactScore(outdatedRate, precision float64) float64 {
	// Weight factors for each component
	const (
		outdatedWeight  = 0.6 // Emphasize actual code changes
		precisionWeight = 0.4 // Balance with technical accuracy
	)

	// Normalize outdated rate to handle varying scales
	normalizedOutdated := math.Min(outdatedRate, 1.0)

	// Calculate weighted score
	impact := (normalizedOutdated * outdatedWeight) + (precision * precisionWeight)

	// Return rounded score between 0 and 1
	return math.Round(impact*100) / 100
}
