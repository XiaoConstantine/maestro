package comment

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
)

// RefinementProcessor implements iterative comment improvement using Refine module.
type RefinementProcessor struct {
	refiner *modules.Refine
	metrics types.MetricsCollector
	logger  *logging.Logger
}

// NewRefinementProcessor creates a new comment refinement processor.
func NewRefinementProcessor(metrics types.MetricsCollector, logger *logging.Logger) *RefinementProcessor {
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
		return EvaluateCommentQuality(comment)
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

	return &RefinementProcessor{
		refiner: refiner,
		metrics: metrics,
		logger:  logger,
	}
}

// Process performs comment refinement on validated issues.
func (p *RefinementProcessor) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	// Check if comment refinement is enabled
	if !IsCommentRefinementEnabled() {
		p.logger.Info(ctx, "Comment refinement disabled, using basic comment generation")
		return p.fallbackToBasicComments(ctx, task, taskContext)
	}

	startTime := util.GetCurrentTimeMs()

	// Extract validated issues for comment generation
	issues, err := p.extractValidatedIssues(task, taskContext)
	if err != nil {
		return nil, fmt.Errorf("failed to extract validated issues: %w", err)
	}

	p.logger.Debug(ctx, "Starting comment refinement for %d issues", len(issues))

	// Refine comments for each issue
	var refinedComments []types.RefinedComment
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

	processingTime := util.GetCurrentTimeMs() - startTime

	// Calculate average quality
	averageQuality := p.calculateAverageQuality(refinedComments)

	result := &types.CommentRefinementResult{
		RefinedComments: refinedComments,
		ProcessingTime:  processingTime,
		AverageQuality:  averageQuality,
		TotalAttempts:   totalAttempts,
	}

	// Track metrics
	p.trackRefinementMetrics(ctx, result)

	p.logger.Debug(ctx, "Comment refinement completed: %d comments with %.2f average quality",
		len(refinedComments), averageQuality)

	return result, nil
}

// refineCommentForIssue refines a comment for a single issue.
func (p *RefinementProcessor) refineCommentForIssue(ctx context.Context, issue types.ReviewIssue, taskContext map[string]interface{}) (*types.RefinedComment, int, error) {
	// Prepare inputs for refinement
	inputs := map[string]interface{}{
		"issue_description": issue.Description,
		"code_context":      util.GetStringFromContext(taskContext, "code_context", ""),
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

// parseRefinementResult parses the output from the refine module.
func (p *RefinementProcessor) parseRefinementResult(result map[string]interface{}, issue types.ReviewIssue) (*types.RefinedComment, int) {
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

	return &types.RefinedComment{
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

// parseQualityMetrics parses quality metrics from the result.
func (p *RefinementProcessor) parseQualityMetrics(metricsData interface{}) types.CommentQualityMetrics {
	// Default metrics
	metrics := types.CommentQualityMetrics{
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

// extractValidatedIssues extracts validated issues from task data.
func (p *RefinementProcessor) extractValidatedIssues(task agents.Task, taskContext map[string]interface{}) ([]types.ReviewIssue, error) {
	var issues []types.ReviewIssue

	// Try to get issues from validation result
	if validationResult, ok := task.Metadata["validation_result"].(*types.ConsensusValidationResult); ok {
		issues = validationResult.ValidatedIssues
	} else if enhancedResult, ok := task.Metadata["enhanced_result"].(*types.EnhancedReviewResult); ok {
		// Get from enhanced review result
		issues = enhancedResult.Issues
	} else if reviewHandoff, ok := task.Metadata["handoff"].(*types.ReviewHandoff); ok {
		// Extract from legacy handoff format
		for _, validatedIssue := range reviewHandoff.ValidatedIssues {
			issues = append(issues, types.ReviewIssue{
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

// generateBasicComment creates a fallback comment for an issue.
func (p *RefinementProcessor) generateBasicComment(issue types.ReviewIssue) types.RefinedComment {
	comment := fmt.Sprintf("%s\n\n%s", issue.Description, issue.Suggestion)

	return types.RefinedComment{
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

// calculateAverageQuality calculates the average quality score.
func (p *RefinementProcessor) calculateAverageQuality(comments []types.RefinedComment) float64 {
	if len(comments) == 0 {
		return 0.0
	}

	total := 0.0
	for _, comment := range comments {
		total += comment.QualityScore
	}

	return total / float64(len(comments))
}

// fallbackToBasicComments provides fallback when refinement is disabled.
func (p *RefinementProcessor) fallbackToBasicComments(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	p.logger.Info(ctx, "Using basic comment generation fallback")

	issues, err := p.extractValidatedIssues(task, taskContext)
	if err != nil {
		return nil, err
	}

	var comments []types.RefinedComment
	for _, issue := range issues {
		comments = append(comments, p.generateBasicComment(issue))
	}

	return &types.CommentRefinementResult{
		RefinedComments: comments,
		ProcessingTime:  0.0,
		AverageQuality:  0.6,
		TotalAttempts:   0,
	}, nil
}

// trackRefinementMetrics records metrics for refinement processing.
func (p *RefinementProcessor) trackRefinementMetrics(ctx context.Context, result *types.CommentRefinementResult) {
	_ = p.metrics // Metrics tracking not yet implemented
}

// Helper functions for comment quality evaluation

// EvaluateCommentQuality evaluates the quality of a comment.
func EvaluateCommentQuality(comment string) float64 {
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

// IsCommentRefinementEnabled checks if comment refinement is enabled.
func IsCommentRefinementEnabled() bool {
	return util.GetEnvBool("MAESTRO_COMMENT_REFINEMENT", true)
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}
