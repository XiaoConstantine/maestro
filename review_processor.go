package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"golang.org/x/exp/maps"
)

type CodeReviewProcessor struct {
	metrics MetricsCollector
}

func (p *CodeReviewProcessor) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	// Extract the handoff from metadata
	handoffRaw, exists := task.Metadata["handoff"]
	if !exists {
		return nil, fmt.Errorf("missing review handoff in metadata")
	}

	handoff, ok := handoffRaw.(*ReviewHandoff)
	if !ok {
		return nil, fmt.Errorf("invalid handoff type: %T", handoffRaw)
	}

	var comments []PRReviewComment

	// Convert validated issues into well-formatted comments
	for _, issue := range handoff.ValidatedIssues {
		// Create base comment
		comment := PRReviewComment{
			FilePath:   issue.FilePath,
			LineNumber: issue.LineRange.Start,
			Category:   issue.Category,
			Severity:   issue.Severity,
			Content:    formatCommentContent(issue),
			Suggestion: formatSuggestion(issue),
		}

		// Track metrics for the comment
		p.metrics.TrackReviewComment(ctx, comment, true)
		comments = append(comments, comment)
	}

	// Return the formatted comments
	return map[string]interface{}{
		"comments": comments,
		"metadata": map[string]interface{}{
			"validation_score": calculateValidationScore(handoff),
			"issue_count":      len(comments),
			"file_path":        handoff.ChainOutput.ReviewMetadata.FilePath,
		},
	}, nil
}

// Helper function to format comment content with enhanced context.
func formatCommentContent(issue ValidatedIssue) string {
	var content strings.Builder

	// Start with the core issue description
	content.WriteString(issue.Context)

	// Add validation context if confidence is high
	if issue.Confidence > 0.9 {
		content.WriteString("\n\nThis issue was validated with high confidence based on:")
		content.WriteString("\n• Context analysis")
		content.WriteString("\n• Rule compliance verification")
		content.WriteString("\n• Impact assessment")
	}

	return content.String()
}

// Helper function to format actionable suggestions.
func formatSuggestion(issue ValidatedIssue) string {
	var suggestion strings.Builder

	suggestion.WriteString("Recommended fix:\n")
	suggestion.WriteString(issue.Suggestion)

	// Add any additional context from rule compliance
	if issue.ValidationDetails.RuleCompliant {
		suggestion.WriteString("\n\nThis suggestion follows established patterns and best practices.")
	}

	return suggestion.String()
}

// // Helper functions.
func parseReviewComments(ctx context.Context, filePath string, commentsStr string, metric MetricsCollector) ([]PRReviewComment, error) {
	var comments []PRReviewComment

	// Parse the YAML-like format from the LLM response
	sections := strings.Split(commentsStr, "\n-")
	for _, section := range sections {
		if strings.TrimSpace(section) == "" {
			continue
		}

		// Extract comment fields
		comment := PRReviewComment{FilePath: filePath}

		// Parse each field
		lines := strings.Split(section, "\n")
		for _, line := range lines {
			parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
			if len(parts) != 2 {
				continue
			}

			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "line":
				value = strings.TrimPrefix(value, "L") // Handle "L123" format
				value = strings.Split(value, "-")[0]   // Hand
				if lineNum, err := strconv.Atoi(value); err == nil && lineNum > 0 {
					comment.LineNumber = lineNum
				}
			case "severity":
				comment.Severity = validateSeverity(value)
			case "content":
				comment.Content = value
			case "suggestion":
				comment.Suggestion = value
			case "category":
				comment.Category = validateCategory(value)
			}
		}

		if isValidComment(comment) {
			comments = append(comments, comment)
			metric.TrackReviewComment(ctx, comment, true)
		} else {
			metric.TrackReviewComment(ctx, comment, true)
		}
	}

	return comments, nil
}

func extractComments(ctx context.Context, result interface{}, filePath string, metric MetricsCollector) ([]PRReviewComment, error) {
	logger := logging.GetLogger()
	logger.Debug(ctx, "Extracting comments from result type: %T", result)
	switch v := result.(type) {
	case map[string]interface{}:
		logger.Debug(ctx, "Processing map result with keys: %v", maps.Keys(v))
		// First check if this is a direct comment result
		if comments, ok := v["comments"].([]PRReviewComment); ok {
			return comments, nil
		}

		// Check for handoff in metadata
		if metadata, ok := v["metadata"].(map[string]interface{}); ok {

			logger.Debug(ctx, "Found metadata with keys: %v", maps.Keys(metadata))
			if handoffRaw, ok := metadata["handoff"]; ok {

				logger.Debug(ctx, "Found handoff of type: %T", handoffRaw)

				handoff, ok := handoffRaw.(*ReviewHandoff)
				logger.Debug(ctx, "Handoff: %v", handoff)
				if !ok {
					return nil, fmt.Errorf("invalid handoff type: %T", handoffRaw)
				}

				// Convert validated issues into comments
				var comments []PRReviewComment
				for _, issue := range handoff.ValidatedIssues {
					comment := PRReviewComment{
						FilePath:   issue.FilePath,
						LineNumber: issue.LineRange.Start,
						Content:    issue.Context,
						Severity:   issue.Severity,
						Category:   issue.Category,
						Suggestion: issue.Suggestion,
					}

					metric.TrackReviewComment(ctx, comment, true)
					comments = append(comments, comment)
				}
				return comments, nil
			}
		}

		// Legacy format handling for string-based comments
		commentsRaw, exists := v["comments"]
		if !exists {
			return nil, fmt.Errorf("missing comments in result")
		}

		commentsStr, ok := commentsRaw.(string)
		if !ok {
			return nil, fmt.Errorf("invalid comments format: %T", commentsRaw)
		}

		return parseReviewComments(ctx, filePath, commentsStr, metric)

	default:
		return nil, fmt.Errorf("unexpected result type: %T", result)
	}
}
func validateSeverity(severity string) string {
	validSeverities := map[string]bool{
		"critical":   true,
		"warning":    true,
		"suggestion": true,
	}

	severity = strings.ToLower(strings.TrimSpace(severity))
	if validSeverities[severity] {
		return severity
	}
	return "suggestion" // Default severity
}

func validateCategory(category string) string {
	validCategories := map[string]bool{
		"error-handling":   true,
		"code-style":       true,
		"performance":      true,
		"security":         true,
		"documentation":    true,
		"comment-response": true,
	}

	category = strings.ToLower(strings.TrimSpace(category))
	if validCategories[category] {
		return category
	}
	return "code-style" // Default category
}

func isValidComment(comment PRReviewComment) bool {
	// A comment is considered actionable if it has:
	// 1. A specific location (line number)
	// 2. A clear suggestion for improvement
	// 3. Non-empty content explaining the issue
	if comment.LineNumber <= 0 ||
		comment.Suggestion == "" ||
		comment.Content == "" {
		return false
	}

	// Check that the content provides meaningful explanation
	if len(strings.TrimSpace(comment.Content)) < 10 {
		return false // Too short to be meaningful
	}

	// Check that the suggestion is specific enough
	if len(strings.TrimSpace(comment.Suggestion)) < 10 {
		return false // Too short to be actionable
	}

	return true
}

// A helper function to calculate an overall validation score from the review handoff.
// This provides a numerical measure of how confident we are in our review findings.
func calculateValidationScore(handoff *ReviewHandoff) float64 {
	if len(handoff.ValidatedIssues) == 0 {
		return 0.0
	}

	// We'll calculate a weighted average considering multiple factors:
	// - Individual issue confidence scores
	// - Validation completeness
	// - Number of validated issues

	var totalScore float64
	for _, issue := range handoff.ValidatedIssues {
		// Start with the base confidence score
		issueScore := issue.Confidence

		// Weight the score based on validation completeness
		if issue.ValidationDetails.ContextValid {
			issueScore *= 0.4 // Context validation carries 40% weight
		}
		if issue.ValidationDetails.RuleCompliant {
			issueScore *= 0.3 // Rule compliance carries 30% weight
		}
		if issue.ValidationDetails.IsActionable {
			issueScore *= 0.3 // Actionability carries 30% weight
		}

		totalScore += issueScore
	}

	// Calculate the average score across all issues
	// This gives us a final score between 0.0 and 1.0
	averageScore := totalScore / float64(len(handoff.ValidatedIssues))

	return math.Round(averageScore*100) / 100 // Round to 2 decimal places
}
