// Package review - Task processors for code review workflow
package review

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
	"github.com/XiaoConstantine/maestro/internal/types"
	"golang.org/x/exp/maps"
)

// CodeReviewProcessor handles code review task processing.
type CodeReviewProcessor struct {
	metrics MetricsCollector
}

// Process implements agents.TaskProcessor for code review.
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

// CommentResponseProcessor handles comment response task processing.
type CommentResponseProcessor struct {
	metrics MetricsCollector
}

// Process implements agents.TaskProcessor for comment responses.
func (p *CommentResponseProcessor) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()

	// Check for handoff from ReviewChainProcessor
	handoffRaw, hasHandoff := task.Metadata["handoff"]
	if hasHandoff {
		handoff, ok := handoffRaw.(*ReviewHandoff)
		if !ok {
			return nil, fmt.Errorf("invalid handoff type: %T", handoffRaw)
		}
		return p.processFromHandoff(ctx, task, handoff)
	}

	logger.Warn(ctx, "No ReviewHandoff provided for comment_response task; falling back to standalone processing.")
	return p.processStandalone(ctx, task, context)
}

func (p *CommentResponseProcessor) processFromHandoff(ctx context.Context, task agents.Task, handoff *ReviewHandoff) (interface{}, error) {
	logger := logging.GetLogger()

	if len(handoff.ValidatedIssues) == 0 && handoff.ThreadStatus != ThreadOpen {
		logger.Debug(ctx, "No validated issues in handoff and thread not open, returning nil comment")
		return nil, nil
	}

	metadata, err := extractResponseMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	var issue *ValidatedIssue
	if len(handoff.ValidatedIssues) > 0 {
		issue = &handoff.ValidatedIssues[0]
	}

	resolutionStatus := string(handoff.ThreadStatus)
	responseContent := metadata.OriginalComment
	var actionItems []string
	if issue != nil {
		responseContent = issue.Context
		actionItems = extractActionItems(issue.Suggestion)
		if issue.ValidationDetails.IsActionable {
			resolutionStatus = "needs_work"
		} else if issue.Confidence > 0.9 && issue.ValidationDetails.RuleCompliant {
			resolutionStatus = "resolved"
		}
	}

	response := ResponseResult{
		Response:         responseContent,
		ResolutionStatus: resolutionStatus,
		ActionItems:      actionItems,
	}

	comment := p.createCommentFromResponse(ctx, metadata, &response, issue)
	logger.Debug(ctx, "Generated comment from handoff: %+v", comment)
	return comment, nil
}

func (p *CommentResponseProcessor) processStandalone(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()

	if _, exists := context["thread_context"]; !exists {
		logger.Debug(ctx, "No thread context found in request")
		context["thread_context"] = []PRReviewComment{}
	}

	var previousContext string
	if threadContext, exists := context["thread_context"].([]PRReviewComment); exists && len(threadContext) > 0 {
		var conversationHistory strings.Builder
		for _, comment := range threadContext {
			conversationHistory.WriteString(fmt.Sprintf("Author: %s\n", comment.Author))
			conversationHistory.WriteString(fmt.Sprintf("Message: %s\n", comment.Content))
			if comment.Suggestion != "" {
				conversationHistory.WriteString(fmt.Sprintf("Suggestion: %s\n", comment.Suggestion))
			}
			conversationHistory.WriteString("---\n")
		}
		previousContext = conversationHistory.String()
	}

	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "original_comment"}},
			{Field: core.Field{Name: "thread_context"}},
			{Field: core.Field{Name: "file_content"}},
			{Field: core.Field{Name: "review_history"}},
			{Field: core.Field{Name: "line_range"}},
		},
		[]core.OutputField{
			{Field: core.NewField("response")},
			{Field: core.NewField("resolution_status")},
			{Field: core.NewField("action_items")},
		},
	).WithInstruction(`Analyze the review comment thread and generate an appropriate response.`)

	predict := modules.NewPredict(signature)
	metadata, err := extractResponseMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	result, err := predict.Process(ctx, map[string]interface{}{
		"original_comment": metadata.OriginalComment,
		"thread_context":   metadata.ThreadContext,
		"file_content":     metadata.FileContent,
		"review_history":   previousContext,
		"line_range":       metadata.LineRange,
	})
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	response, err := parseResponseResult(result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return p.createCommentFromResponse(ctx, metadata, response, nil), nil
}

func (p *CommentResponseProcessor) createCommentFromResponse(ctx context.Context, metadata *ResponseMetadata, response *ResponseResult, issue *ValidatedIssue) PRReviewComment {
	var severity string
	if issue != nil && issue.Severity != "" {
		severity = issue.Severity
	} else {
		severity = deriveSeverity(response.ResolutionStatus)
	}

	comment := PRReviewComment{
		FilePath:   metadata.FilePath,
		LineNumber: metadata.LineRange.Start,
		Content:    response.Response,
		Severity:   severity,
		Category:   metadata.Category,
		InReplyTo:  metadata.InReplyTo,
		ThreadID:   metadata.ThreadID,
		Resolved:   response.ResolutionStatus == "resolved",
		Timestamp:  time.Now(),
	}
	if len(response.ActionItems) > 0 {
		comment.Suggestion = formatActionItemsList(response.ActionItems)
	}

	if metadata.ThreadID != nil {
		resolution := mapResponseStatusToResolution(response.ResolutionStatus)
		p.metrics.TrackCommentResolution(ctx, *metadata.ThreadID, resolution)
	}

	return comment
}

// ResponseResult holds parsed response data.
type ResponseResult struct {
	Response         string
	ResolutionStatus string
	ActionItems      []string
}

// ResponseMetadata holds metadata for response processing.
type ResponseMetadata struct {
	OriginalComment string
	ThreadContext   []PRReviewComment
	FileContent     string
	FilePath        string
	LineRange       types.LineRange
	ThreadID        *int64
	InReplyTo       *int64
	Category        string
	ReviewHistory   []types.ReviewChunk
}

// Helper functions

func formatCommentContent(issue ValidatedIssue) string {
	var content strings.Builder
	content.WriteString(issue.Context)

	if issue.Confidence > 0.9 {
		content.WriteString("\n\nThis issue was validated with high confidence based on:")
		content.WriteString("\n• Context analysis")
		content.WriteString("\n• Rule compliance verification")
		content.WriteString("\n• Impact assessment")
	}

	return content.String()
}

func formatSuggestion(issue ValidatedIssue) string {
	var suggestion strings.Builder
	suggestion.WriteString("Recommended fix:\n")
	suggestion.WriteString(issue.Suggestion)

	if issue.ValidationDetails.RuleCompliant {
		suggestion.WriteString("\n\nThis suggestion follows established patterns and best practices.")
	}

	return suggestion.String()
}

func calculateValidationScore(handoff *ReviewHandoff) float64 {
	if len(handoff.ValidatedIssues) == 0 {
		return 0.0
	}

	var totalScore float64
	for _, issue := range handoff.ValidatedIssues {
		issueScore := issue.Confidence
		if issue.ValidationDetails.ContextValid {
			issueScore *= 0.4
		}
		if issue.ValidationDetails.RuleCompliant {
			issueScore *= 0.3
		}
		if issue.ValidationDetails.IsActionable {
			issueScore *= 0.3
		}
		totalScore += issueScore
	}

	averageScore := totalScore / float64(len(handoff.ValidatedIssues))
	return math.Round(averageScore*100) / 100
}

func parseReviewComments(ctx context.Context, filePath string, commentsStr string, metric MetricsCollector) ([]PRReviewComment, error) {
	var comments []PRReviewComment

	sections := strings.Split(commentsStr, "\n-")
	for _, section := range sections {
		if strings.TrimSpace(section) == "" {
			continue
		}

		comment := PRReviewComment{FilePath: filePath}
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
				value = strings.TrimPrefix(value, "L")
				value = strings.Split(value, "-")[0]
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
		if comments, ok := v["comments"].([]PRReviewComment); ok {
			return comments, nil
		}

		if metadata, ok := v["metadata"].(map[string]interface{}); ok {
			logger.Debug(ctx, "Found metadata with keys: %v", maps.Keys(metadata))
			if handoffRaw, ok := metadata["handoff"]; ok {
				logger.Debug(ctx, "Found handoff of type: %T", handoffRaw)
				handoff, ok := handoffRaw.(*ReviewHandoff)
				if !ok {
					return nil, fmt.Errorf("invalid handoff type: %T", handoffRaw)
				}

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
	return "suggestion"
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
	return "code-style"
}

func isValidComment(comment PRReviewComment) bool {
	if comment.LineNumber <= 0 ||
		comment.Suggestion == "" ||
		comment.Content == "" {
		return false
	}

	if len(strings.TrimSpace(comment.Content)) < 10 {
		return false
	}

	if len(strings.TrimSpace(comment.Suggestion)) < 10 {
		return false
	}

	return true
}

func extractResponseMetadata(metadata map[string]interface{}) (*ResponseMetadata, error) {
	rm := &ResponseMetadata{}

	if comment, ok := metadata["original_comment"].(string); ok && comment != "" {
		rm.OriginalComment = comment
	} else {
		return nil, fmt.Errorf("missing or invalid original comment")
	}

	if thread, ok := metadata["thread_context"].([]PRReviewComment); ok {
		rm.ThreadContext = thread
	}

	if content, ok := metadata["file_content"].(string); ok {
		rm.FileContent = content
	}

	if path, ok := metadata["file_path"].(string); ok {
		rm.FilePath = path
	}

	if threadID, exists := metadata["thread_id"]; exists {
		switch v := threadID.(type) {
		case int64:
			rm.ThreadID = &v
		case float64:
			val := int64(v)
			rm.ThreadID = &val
		}
	}
	if rangeData, ok := metadata["line_range"].(map[string]interface{}); ok {
		startLine, startOk := rangeData["start"].(int)
		endLine, endOk := rangeData["end"].(int)
		if !startOk || !endOk {
			return nil, fmt.Errorf("invalid line range format: start and end must be integers")
		}
		rm.LineRange = types.LineRange{Start: startLine, End: endLine, File: rm.FilePath}
	} else if line, ok := metadata["line_number"].(int); ok {
		rm.LineRange = types.LineRange{Start: line, End: line, File: rm.FilePath}
	} else {
		return nil, fmt.Errorf("missing or invalid line range information")
	}

	if replyTo, ok := metadata["in_reply_to"].(*int64); ok {
		rm.InReplyTo = replyTo
	}

	if category, ok := metadata["category"].(string); ok {
		rm.Category = category
	} else {
		rm.Category = "code-style"
	}

	if rm.LineRange.Start == 0 {
		return nil, fmt.Errorf("failed to extract valid line number from metadata: %+v", metadata)
	}

	return rm, nil
}

func parseResponseResult(result interface{}) (*ResponseResult, error) {
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid result type: %T, expected map[string]interface{}", result)
	}

	response := &ResponseResult{}

	if respContent, ok := resultMap["response"].(string); ok {
		response.Response = respContent
	} else {
		return nil, fmt.Errorf("missing or invalid response content, got %T", resultMap["response"])
	}

	if status, ok := resultMap["resolution_status"].(string); ok {
		status = strings.ToLower(status)
		validStatuses := map[string]bool{
			"resolved":            true,
			"needs_work":          true,
			"needs_clarification": true,
			"acknowledged":        true,
		}
		if validStatuses[status] {
			response.ResolutionStatus = status
		} else {
			response.ResolutionStatus = "acknowledged"
		}
	}

	if items, ok := resultMap["action_items"].([]interface{}); ok {
		response.ActionItems = make([]string, 0, len(items))
		for _, item := range items {
			if strItem, ok := item.(string); ok {
				response.ActionItems = append(response.ActionItems, strItem)
			}
		}
	}

	return response, nil
}

func deriveSeverity(status string) string {
	switch status {
	case "needs_work":
		return "warning"
	case "needs_clarification":
		return "suggestion"
	case "resolved":
		return "suggestion"
	default:
		return "suggestion"
	}
}

func formatActionItemsList(items []string) string {
	var sb strings.Builder
	sb.WriteString("Suggested actions:\n")
	for _, item := range items {
		sb.WriteString(fmt.Sprintf("- %s\n", item))
	}
	return sb.String()
}

func mapResponseStatusToResolution(status string) ResolutionOutcome {
	switch strings.ToLower(status) {
	case "resolved":
		return ResolutionAccepted
	case "needs_work":
		return ResolutionNeedsWork
	case "needs_clarification":
		return ResolutionInconclusive
	case "acknowledged":
		return ResolutionInProgress
	default:
		return ResolutionInProgress
	}
}

func extractActionItems(suggestion string) []string {
	lines := strings.Split(suggestion, "\n")
	var items []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			items = append(items, strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "))
		}
	}
	return items
}

// Resolution outcome constants (aliased from types for convenience).
const (
	ResolutionAccepted     = types.ResolutionAccepted
	ResolutionRejected     = types.ResolutionRejected
	ResolutionNeedsWork    = types.ResolutionNeedsWork
	ResolutionInProgress   = types.ResolutionInProgress
	ResolutionInconclusive = types.ResolutionInconclusive
)
