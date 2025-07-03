package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// ResultAggregator consolidates chunk-based review results into file-level results.
type ResultAggregator struct {
	deduplicationThreshold float64
	logger                 *logging.Logger
}

// FileReviewResult represents aggregated review results for a single file.
type FileReviewResult struct {
	FilePath       string        `json:"file_path"`
	Issues         []ReviewIssue `json:"issues"`
	OverallQuality string        `json:"overall_quality"`
	Confidence     float64       `json:"confidence"`
	ChunkCount     int           `json:"chunk_count"`
	ProcessingTime float64       `json:"processing_time_ms"`
	ReasoningChain string        `json:"reasoning_chain"`
}

// NewResultAggregator creates a new ResultAggregator with the specified deduplication threshold.
func NewResultAggregator(deduplicationThreshold float64, logger *logging.Logger) *ResultAggregator {
	return &ResultAggregator{
		deduplicationThreshold: deduplicationThreshold,
		logger:                 logger,
	}
}

// AggregateByFile consolidates chunk-based review results into file-level results.
func (ra *ResultAggregator) AggregateByFile(ctx context.Context, chunkResults []interface{}) (map[string]*FileReviewResult, error) {
	startTime := time.Now()

	// Group results by file path
	fileGroups := make(map[string][]*EnhancedReviewResult)

	for i, result := range chunkResults {
		enhancedResult, ok := result.(*EnhancedReviewResult)
		if !ok {
			ra.logger.Warn(ctx, "Invalid result type at index %d, skipping", i)
			continue
		}

		// Use file path from enhanced result metadata (set during processing)
		filePath := enhancedResult.FilePath
		if filePath == "" {
			// Fallback: extract from first issue if file path not set
			filePath = "unknown"
			if len(enhancedResult.Issues) > 0 {
				filePath = enhancedResult.Issues[0].FilePath
			}
		}

		fileGroups[filePath] = append(fileGroups[filePath], enhancedResult)
	}

	// Aggregate results for each file
	aggregatedResults := make(map[string]*FileReviewResult)

	for filePath, results := range fileGroups {
		fileResult, err := ra.aggregateFileResults(ctx, filePath, results)
		if err != nil {
			ra.logger.Error(ctx, "Failed to aggregate results for file %s: %v", filePath, err)
			continue
		}

		aggregatedResults[filePath] = fileResult
	}

	processingTime := time.Since(startTime).Seconds() * 1000
	ra.logger.Info(ctx, "Aggregated %d chunk results into %d file results in %.2f ms",
		len(chunkResults), len(aggregatedResults), processingTime)

	return aggregatedResults, nil
}

// aggregateFileResults combines multiple chunk results for a single file.
func (ra *ResultAggregator) aggregateFileResults(ctx context.Context, filePath string, results []*EnhancedReviewResult) (*FileReviewResult, error) {
	if len(results) == 0 {
		return nil, fmt.Errorf("no results to aggregate for file %s", filePath)
	}

	// Collect all issues from chunks
	var allIssues []ReviewIssue
	var allAssessments []string
	var allReasoningChains []string
	var totalProcessingTime float64
	var totalConfidence float64

	for _, result := range results {
		allIssues = append(allIssues, result.Issues...)
		if result.OverallQuality != "" {
			allAssessments = append(allAssessments, result.OverallQuality)
		}
		if result.ReasoningChain != "" {
			allReasoningChains = append(allReasoningChains, result.ReasoningChain)
		}
		totalProcessingTime += result.ProcessingTime
		totalConfidence += result.Confidence
	}

	// Deduplicate issues
	deduplicatedIssues := ra.deduplicateIssues(ctx, allIssues)

	// Merge overall assessments
	overallQuality := ra.mergeOverallAssessments(allAssessments)

	// Merge reasoning chains
	reasoningChain := ra.mergeReasoningChains(allReasoningChains)

	// Calculate average confidence
	avgConfidence := totalConfidence / float64(len(results))

	fileResult := &FileReviewResult{
		FilePath:       filePath,
		Issues:         deduplicatedIssues,
		OverallQuality: overallQuality,
		Confidence:     avgConfidence,
		ChunkCount:     len(results),
		ProcessingTime: totalProcessingTime,
		ReasoningChain: reasoningChain,
	}

	ra.logger.Debug(ctx, "Aggregated %d chunks into %d unique issues for file %s",
		len(results), len(deduplicatedIssues), filePath)

	return fileResult, nil
}

// deduplicateIssues removes duplicate issues using similarity detection.
func (ra *ResultAggregator) deduplicateIssues(ctx context.Context, issues []ReviewIssue) []ReviewIssue {
	if len(issues) <= 1 {
		return issues
	}

	// Sort issues by confidence (descending) to prefer higher confidence issues
	sort.Slice(issues, func(i, j int) bool {
		// First sort by severity (critical > high > medium > low)
		severityOrder := map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1}
		severityI := severityOrder[strings.ToLower(issues[i].Severity)]
		severityJ := severityOrder[strings.ToLower(issues[j].Severity)]

		if severityI != severityJ {
			return severityI > severityJ
		}

		// Then by confidence
		return issues[i].Confidence > issues[j].Confidence
	})

	var deduplicatedIssues []ReviewIssue

	for _, issue := range issues {
		isDuplicate := false

		// Check against existing deduplicated issues
		for _, existing := range deduplicatedIssues {
			if ra.issuesSimilar(issue, existing) {
				isDuplicate = true
				ra.logger.Debug(ctx, "Removed duplicate issue: %s (similar to existing)",
					truncateString(issue.Description, 50))
				break
			}
		}

		if !isDuplicate {
			deduplicatedIssues = append(deduplicatedIssues, issue)
		}
	}

	originalCount := len(issues)
	deduplicatedCount := len(deduplicatedIssues)
	if originalCount > deduplicatedCount {
		ra.logger.Info(ctx, "Deduplication removed %d duplicate issues (%d -> %d)",
			originalCount-deduplicatedCount, originalCount, deduplicatedCount)
	}

	return deduplicatedIssues
}

// issuesSimilar determines if two issues are similar enough to be considered duplicates.
func (ra *ResultAggregator) issuesSimilar(issue1, issue2 ReviewIssue) bool {
	// Same category and similar line ranges
	if issue1.Category != issue2.Category {
		return false
	}

	// Check line range overlap
	if ra.lineRangesOverlap(issue1.LineRange, issue2.LineRange) {
		// Check description similarity
		similarity := ra.stringSimilarity(issue1.Description, issue2.Description)
		return similarity >= ra.deduplicationThreshold
	}

	// Check for very similar descriptions even if line ranges don't overlap
	similarity := ra.stringSimilarity(issue1.Description, issue2.Description)
	return similarity >= 0.9 // Higher threshold for non-overlapping ranges
}

// lineRangesOverlap checks if two line ranges overlap.
func (ra *ResultAggregator) lineRangesOverlap(range1, range2 LineRange) bool {
	return range1.Start <= range2.End && range2.Start <= range1.End
}

// stringSimilarity calculates similarity between two strings using a simple approach.
func (ra *ResultAggregator) stringSimilarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}

	// Convert to lowercase for comparison
	s1 = strings.ToLower(s1)
	s2 = strings.ToLower(s2)

	// Use Levenshtein distance for similarity
	distance := ra.levenshteinDistance(s1, s2)
	maxLen := len(s1)
	if len(s2) > maxLen {
		maxLen = len(s2)
	}

	if maxLen == 0 {
		return 1.0
	}

	return 1.0 - (float64(distance) / float64(maxLen))
}

// levenshteinDistance calculates the Levenshtein distance between two strings.
func (ra *ResultAggregator) levenshteinDistance(s1, s2 string) int {
	if len(s1) == 0 {
		return len(s2)
	}
	if len(s2) == 0 {
		return len(s1)
	}

	// Create a matrix to store distances
	matrix := make([][]int, len(s1)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(s2)+1)
	}

	// Initialize first row and column
	for i := 0; i <= len(s1); i++ {
		matrix[i][0] = i
	}
	for j := 0; j <= len(s2); j++ {
		matrix[0][j] = j
	}

	// Fill the matrix
	for i := 1; i <= len(s1); i++ {
		for j := 1; j <= len(s2); j++ {
			cost := 0
			if s1[i-1] != s2[j-1] {
				cost = 1
			}

			matrix[i][j] = min3(
				matrix[i-1][j]+1,      // deletion
				matrix[i][j-1]+1,      // insertion
				matrix[i-1][j-1]+cost, // substitution
			)
		}
	}

	return matrix[len(s1)][len(s2)]
}

// mergeOverallAssessments combines multiple overall quality assessments.
func (ra *ResultAggregator) mergeOverallAssessments(assessments []string) string {
	if len(assessments) == 0 {
		return "No overall assessment available"
	}

	if len(assessments) == 1 {
		return assessments[0]
	}

	// Count quality indicators
	qualityIndicators := make(map[string]int)
	for _, assessment := range assessments {
		lower := strings.ToLower(assessment)
		if strings.Contains(lower, "excellent") || strings.Contains(lower, "outstanding") {
			qualityIndicators["excellent"]++
		} else if strings.Contains(lower, "good") || strings.Contains(lower, "well") {
			qualityIndicators["good"]++
		} else if strings.Contains(lower, "fair") || strings.Contains(lower, "acceptable") {
			qualityIndicators["fair"]++
		} else if strings.Contains(lower, "poor") || strings.Contains(lower, "needs improvement") {
			qualityIndicators["poor"]++
		}
	}

	// Determine overall quality based on majority
	maxCount := 0
	overallQuality := "fair"

	for quality, count := range qualityIndicators {
		if count > maxCount {
			maxCount = count
			overallQuality = quality
		}
	}

	// Create summary
	return fmt.Sprintf("Overall code quality: %s (based on %d chunk assessments)",
		overallQuality, len(assessments))
}

// mergeReasoningChains combines multiple reasoning chains into a coherent summary.
func (ra *ResultAggregator) mergeReasoningChains(chains []string) string {
	if len(chains) == 0 {
		return ""
	}

	if len(chains) == 1 {
		return chains[0]
	}

	// Combine unique reasoning steps
	uniqueSteps := make(map[string]bool)
	var mergedSteps []string

	for i, chain := range chains {
		if chain == "" {
			continue
		}

		// Add chunk context
		chunkStep := fmt.Sprintf("Chunk %d analysis: %s", i+1, truncateString(chain, 200))

		// Only add if not duplicate
		if !uniqueSteps[chunkStep] {
			uniqueSteps[chunkStep] = true
			mergedSteps = append(mergedSteps, chunkStep)
		}
	}

	return strings.Join(mergedSteps, "\n\n")
}

// ConvertFileResultsToInterface converts FileReviewResult map to []interface{} for backward compatibility.
func ConvertFileResultsToInterface(fileResults map[string]*FileReviewResult) []interface{} {
	results := make([]interface{}, 0, len(fileResults))

	for _, fileResult := range fileResults {
		// Convert FileReviewResult to EnhancedReviewResult for compatibility
		enhancedResult := &EnhancedReviewResult{
			Issues:         fileResult.Issues,
			OverallQuality: fileResult.OverallQuality,
			ReasoningChain: fileResult.ReasoningChain,
			Confidence:     fileResult.Confidence,
			ProcessingTime: fileResult.ProcessingTime,
		}

		results = append(results, enhancedResult)
	}

	return results
}

// Helper functions

func min3(a, b, c int) int {
	if a < b && a < c {
		return a
	} else if b < c {
		return b
	}
	return c
}

// Note: truncateString is defined in rag.go to avoid duplication
