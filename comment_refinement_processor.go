package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
)

// CommentRefinementProcessor implements iterative comment improvement using Refine module
type CommentRefinementProcessor struct {
	refiner *modules.Refine
	metrics MetricsCollector
	logger  *logging.Logger
}

// RefinedComment represents a refined review comment
type RefinedComment struct {
	FilePath        string  `json:"file_path"`
	LineNumber      int     `json:"line_number"`
	OriginalComment string  `json:"original_comment"`
	RefinedComment  string  `json:"refined_comment"`
	QualityScore    float64 `json:"quality_score"`
	Improvement     string  `json:"improvement"`
	RefineAttempts  int     `json:"refine_attempts"`
	Category        string  `json:"category"`
	Severity        string  `json:"severity"`
	Suggestion      string  `json:"suggestion"`
}

// CommentRefinementResult represents the output of comment refinement
type CommentRefinementResult struct {
	RefinedComments []RefinedComment `json:"refined_comments"`
	ProcessingTime  float64          `json:"processing_time_ms"`
	AverageQuality  float64          `json:"average_quality_score"`
	TotalAttempts   int              `json:"total_refine_attempts"`
}

// CommentQualityMetrics tracks comment quality aspects
type CommentQualityMetrics struct {
	Clarity         float64 `json:"clarity"`
	Actionability   float64 `json:"actionability"`
	Professionalism float64 `json:"professionalism"`
	Specificity     float64 `json:"specificity"`
	Helpfulness     float64 `json:"helpfulness"`
	Overall         float64 `json:"overall"`
}

// NewCommentRefinementProcessor creates a new comment refinement processor
func NewCommentRefinementProcessor(metrics MetricsCollector, logger *logging.Logger) *CommentRefinementProcessor {
	// Create comment generation and refinement signature
	commentSig := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "issue_description", Description: "The code issue to comment on"}},
			{Field: core.Field{Name: "code_context", Description: "Surrounding code context"}},
			{Field: core.Field{Name: "severity", Description: "Issue severity level"}},
			{Field: core.Field{Name: "category", Description: "Issue category"}},
			{Field: core.Field{Name: "suggestion", Description: "Improvement suggestion"}},
			{Field: core.Field{Name: "file_path", Description: "Path of the file"}},
			{Field: core.Field{Name: "previous_attempt", Description: "Previous comment attempt (if any)"}},
		},
		[]core.OutputField{
			{Field: core.NewField("comment")},
			{Field: core.NewField("quality_score")},
			{Field: core.NewField("quality_metrics")},
			{Field: core.NewField("improvement_notes")},
		},
	).WithInstruction(`
Generate a high-quality code review comment that follows these principles:

COMMENT QUALITY CRITERIA:
1. CLARITY: Clear, concise, and easy to understand
   - Use simple, direct language
   - Avoid jargon unless necessary
   - Structure thoughts logically

2. ACTIONABILITY: Provides specific, actionable guidance
   - Give concrete steps to fix the issue
   - Include code examples when helpful
   - Suggest alternative approaches

3. PROFESSIONALISM: Maintains a respectful, constructive tone
   - Focus on the code, not the person
   - Use positive, collaborative language
   - Avoid harsh criticism

4. SPECIFICITY: Addresses the exact issue with precision
   - Reference specific lines or functions
   - Explain why the issue matters
   - Connect to broader impacts

5. HELPFULNESS: Genuinely assists developer improvement
   - Explain the reasoning behind suggestions
   - Share relevant best practices
   - Point to helpful resources when applicable

FORMAT GUIDELINES:
- Start with a clear problem statement
- Explain why it's an issue
- Provide specific solution(s)
- Include code example if beneficial
- End with a helpful note if appropriate

EXAMPLE QUALITY COMMENT:
"The current implementation has a potential memory leak on line 45. When processData() returns early due to an error, the allocated buffer isn't freed. Consider using a defer statement or moving the allocation after the error check:

Better approach:
if err := validateInput(data); err != nil {
    return err
}
buffer := make([]byte, size) // Allocate after validation
defer releaseBuffer(buffer)

This ensures proper cleanup and follows Go's idiomatic error handling patterns."

OUTPUT:
- comment: The refined comment text
- quality_score: Overall quality score (0-1)
- quality_metrics: JSON with scores for clarity, actionability, professionalism, specificity, helpfulness
- improvement_notes: What was improved from previous attempt (if any)
`)

	// Create reward function for comment quality evaluation
	rewardFn := func(inputs map[string]interface{}, outputs map[string]interface{}) float64 {
		comment, ok := outputs["comment"].(string)
		if !ok {
			return 0.0
		}
		return evaluateCommentQuality(comment)
	}

	// Create Refine module with quality-focused configuration
	refineConfig := modules.RefineConfig{
		N:         3,        // Up to 3 refinement attempts
		RewardFn:  rewardFn, // Quality evaluation function
		Threshold: 0.8,      // Target quality threshold
	}

	// Create base prediction module
	baseModule := modules.NewChainOfThought(commentSig).WithName("CommentReasoningChain")

	// Create refine module
	refiner := modules.NewRefine(baseModule, refineConfig).WithName("CommentRefiner")

	return &CommentRefinementProcessor{
		refiner: refiner,
		metrics: metrics,
		logger:  logger,
	}
}

// Process performs comment refinement on validated issues
func (p *CommentRefinementProcessor) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	// Check if comment refinement is enabled
	if !isCommentRefinementEnabled() {
		p.logger.Info(ctx, "Comment refinement disabled, using basic comment generation")
		return p.fallbackToBasicComments(ctx, task, taskContext)
	}

	startTime := getCurrentTimeMs()

	// Extract validated issues for comment generation
	issues, err := p.extractValidatedIssues(task, taskContext)
	if err != nil {
		return nil, fmt.Errorf("failed to extract validated issues: %w", err)
	}

	p.logger.Debug(ctx, "Starting comment refinement for %d issues", len(issues))

	// Refine comments for each issue
	var refinedComments []RefinedComment
	totalAttempts := 0

	for _, issue := range issues {
		refined, attempts, err := p.refineCommentForIssue(ctx, issue, taskContext)
		if err != nil {
			p.logger.Warn(ctx, "Failed to refine comment for issue %s: %v", issue.Description, err)
			// Fallback to basic comment
			basic := p.generateBasicComment(issue)
			refinedComments = append(refinedComments, basic)
			continue
		}

		refinedComments = append(refinedComments, *refined)
		totalAttempts += attempts
	}

	processingTime := getCurrentTimeMs() - startTime

	// Calculate average quality
	averageQuality := p.calculateAverageQuality(refinedComments)

	result := &CommentRefinementResult{
		RefinedComments: refinedComments,
		ProcessingTime:  processingTime,
		AverageQuality:  averageQuality,
		TotalAttempts:   totalAttempts,
	}

	// Track metrics
	p.trackRefinementMetrics(ctx, result)

	p.logger.Info(ctx, "Comment refinement completed: %d comments with %.2f average quality",
		len(refinedComments), averageQuality)

	return result, nil
}

// refineCommentForIssue refines a comment for a single issue
func (p *CommentRefinementProcessor) refineCommentForIssue(ctx context.Context, issue ReviewIssue, taskContext map[string]interface{}) (*RefinedComment, int, error) {
	// Prepare inputs for refinement
	inputs := map[string]interface{}{
		"issue_description": issue.Description,
		"code_context":      getStringFromContext(taskContext, "code_context", ""),
		"severity":          issue.Severity,
		"category":          issue.Category,
		"suggestion":        issue.Suggestion,
		"file_path":         issue.FilePath,
		"previous_attempt":  "", // No previous attempt for initial generation
	}

	// Use refine module to iteratively improve the comment
	result, err := p.refiner.Process(ctx, inputs)
	if err != nil {
		return nil, 0, fmt.Errorf("refine processing failed: %w", err)
	}

	// Parse refinement result
	refined, attempts := p.parseRefinementResult(result, issue)

	return refined, attempts, nil
}

// parseRefinementResult parses the output from the refine module
func (p *CommentRefinementProcessor) parseRefinementResult(result map[string]interface{}, issue ReviewIssue) (*RefinedComment, int) {
	// Extract refined comment
	comment, _ := result["comment"].(string)
	if comment == "" {
		comment = p.generateBasicComment(issue).RefinedComment
	}

	// Extract quality score
	qualityScore := 0.8 // Default score
	if scoreStr, ok := result["quality_score"].(string); ok {
		if parsed, err := parseFloat(scoreStr); err == nil {
			qualityScore = parsed
		}
	}

	// Extract improvement notes
	improvement, _ := result["improvement_notes"].(string)

	// Extract quality metrics
	_ = p.parseQualityMetrics(result["quality_metrics"])

	// Estimate number of attempts (would be tracked by Refine module in practice)
	attempts := estimateRefineAttempts(qualityScore)

	return &RefinedComment{
		FilePath:        issue.FilePath,
		LineNumber:      issue.LineRange.Start,
		OriginalComment: issue.Description,
		RefinedComment:  comment,
		QualityScore:    qualityScore,
		Improvement:     improvement,
		RefineAttempts:  attempts,
		Category:        issue.Category,
		Severity:        issue.Severity,
		Suggestion:      issue.Suggestion,
	}, attempts
}

// parseQualityMetrics parses quality metrics from the result
func (p *CommentRefinementProcessor) parseQualityMetrics(metricsData interface{}) CommentQualityMetrics {
	// Default metrics
	metrics := CommentQualityMetrics{
		Clarity:         0.8,
		Actionability:   0.8,
		Professionalism: 0.8,
		Specificity:     0.8,
		Helpfulness:     0.8,
		Overall:         0.8,
	}

	// Try to parse from string (JSON format)
	if metricsStr, ok := metricsData.(string); ok {
		// Simple parsing - look for metric patterns
		if strings.Contains(strings.ToLower(metricsStr), "clarity") {
			metrics.Clarity = extractMetricScore(metricsStr, "clarity")
		}
		if strings.Contains(strings.ToLower(metricsStr), "actionability") {
			metrics.Actionability = extractMetricScore(metricsStr, "actionability")
		}
		// ... parse other metrics similarly
	}

	// Calculate overall as average
	metrics.Overall = (metrics.Clarity + metrics.Actionability +
		metrics.Professionalism + metrics.Specificity +
		metrics.Helpfulness) / 5.0

	return metrics
}

// extractValidatedIssues extracts validated issues from task data
func (p *CommentRefinementProcessor) extractValidatedIssues(task agents.Task, taskContext map[string]interface{}) ([]ReviewIssue, error) {
	var issues []ReviewIssue

	// Try to get issues from validation result
	if validationResult, ok := task.Metadata["validation_result"].(*ConsensusValidationResult); ok {
		issues = validationResult.ValidatedIssues
	} else if enhancedResult, ok := task.Metadata["enhanced_result"].(*EnhancedReviewResult); ok {
		// Get from enhanced review result
		issues = enhancedResult.Issues
	} else if reviewHandoff, ok := task.Metadata["handoff"].(*ReviewHandoff); ok {
		// Extract from legacy handoff format
		for _, validatedIssue := range reviewHandoff.ValidatedIssues {
			issues = append(issues, ReviewIssue{
				FilePath:    validatedIssue.FilePath,
				LineRange:   validatedIssue.LineRange,
				Category:    validatedIssue.Category,
				Severity:    validatedIssue.Severity,
				Description: validatedIssue.Context,
				Suggestion:  validatedIssue.Suggestion,
				Confidence:  0.8,
			})
		}
	} else {
		return nil, fmt.Errorf("no validated issues found in task metadata")
	}

	return issues, nil
}

// generateBasicComment creates a fallback comment for an issue
func (p *CommentRefinementProcessor) generateBasicComment(issue ReviewIssue) RefinedComment {
	comment := fmt.Sprintf("%s\n\n%s", issue.Description, issue.Suggestion)

	return RefinedComment{
		FilePath:        issue.FilePath,
		LineNumber:      issue.LineRange.Start,
		OriginalComment: issue.Description,
		RefinedComment:  comment,
		QualityScore:    0.6, // Lower quality for basic generation
		Improvement:     "Generated using fallback method",
		RefineAttempts:  0,
		Category:        issue.Category,
		Severity:        issue.Severity,
		Suggestion:      issue.Suggestion,
	}
}

// calculateAverageQuality calculates the average quality score
func (p *CommentRefinementProcessor) calculateAverageQuality(comments []RefinedComment) float64 {
	if len(comments) == 0 {
		return 0.0
	}

	total := 0.0
	for _, comment := range comments {
		total += comment.QualityScore
	}

	return total / float64(len(comments))
}

// fallbackToBasicComments provides fallback when refinement is disabled
func (p *CommentRefinementProcessor) fallbackToBasicComments(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	p.logger.Info(ctx, "Using basic comment generation fallback")

	issues, err := p.extractValidatedIssues(task, taskContext)
	if err != nil {
		return nil, err
	}

	var comments []RefinedComment
	for _, issue := range issues {
		comments = append(comments, p.generateBasicComment(issue))
	}

	return &CommentRefinementResult{
		RefinedComments: comments,
		ProcessingTime:  0.0,
		AverageQuality:  0.6,
		TotalAttempts:   0,
	}, nil
}

// trackRefinementMetrics records metrics for refinement processing
func (p *CommentRefinementProcessor) trackRefinementMetrics(ctx context.Context, result *CommentRefinementResult) {
	if p.metrics != nil {
		// Basic metrics tracking - extend MetricsCollector interface as needed
		// p.metrics.TrackProcessingTime(ctx, "comment_refinement", result.ProcessingTime)
		// p.metrics.TrackCommentQuality(ctx, result.AverageQuality)
		// p.metrics.TrackRefinementAttempts(ctx, result.TotalAttempts)
	}
}

// Helper functions for comment quality evaluation

func evaluateCommentQuality(comment string) float64 {
	score := 0.0

	// Check for clarity (reasonable length and structure)
	if len(comment) > 50 && len(comment) < 500 {
		score += 0.2
	}

	// Check for actionable suggestions
	actionableWords := []string{"suggest", "recommend", "consider", "try", "use", "change"}
	for _, word := range actionableWords {
		if strings.Contains(strings.ToLower(comment), word) {
			score += 0.15
			break
		}
	}

	// Check for code examples
	if strings.Contains(comment, "```") || strings.Contains(comment, "`") {
		score += 0.2
	}

	// Check for professional tone (avoid negative words)
	negativeWords := []string{"wrong", "bad", "terrible", "awful", "stupid"}
	hasNegative := false
	for _, word := range negativeWords {
		if strings.Contains(strings.ToLower(comment), word) {
			hasNegative = true
			break
		}
	}
	if !hasNegative {
		score += 0.2
	}

	// Check for explanation/reasoning
	reasoningWords := []string{"because", "since", "due to", "this causes", "this leads"}
	for _, phrase := range reasoningWords {
		if strings.Contains(strings.ToLower(comment), phrase) {
			score += 0.15
			break
		}
	}

	// Check for specificity (mentions lines, functions, variables)
	specificityWords := []string{"line", "function", "method", "variable", "on line"}
	for _, word := range specificityWords {
		if strings.Contains(strings.ToLower(comment), word) {
			score += 0.1
			break
		}
	}

	return score
}

func extractMetricScore(text, metric string) float64 {
	// Simple extraction - look for patterns like "clarity: 0.8"
	pattern := fmt.Sprintf("%s:", strings.ToLower(metric))
	index := strings.Index(strings.ToLower(text), pattern)
	if index == -1 {
		return 0.8 // Default score
	}

	// Extract number after the pattern
	start := index + len(pattern)
	end := start
	for end < len(text) && (text[end] >= '0' && text[end] <= '9' || text[end] == '.') {
		end++
	}

	if end > start {
		if score, err := strconv.ParseFloat(text[start:end], 64); err == nil {
			return score
		}
	}

	return 0.8 // Default if parsing fails
}

func estimateRefineAttempts(qualityScore float64) int {
	// Estimate attempts based on quality achieved
	if qualityScore >= 0.9 {
		return 3 // High quality likely took multiple attempts
	} else if qualityScore >= 0.8 {
		return 2
	} else {
		return 1
	}
}

func isCommentRefinementEnabled() bool {
	return getEnvBool("MAESTRO_COMMENT_REFINEMENT", true)
}
