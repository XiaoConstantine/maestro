package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
)

type ThreadStatus string

const (
	ThreadOpen       ThreadStatus = "open"
	ThreadResolved   ThreadStatus = "resolved"
	ThreadInProgress ThreadStatus = "in_progress"
	ThreadStale      ThreadStatus = "stale"
)

type ResolutionEffectiveness string

const (
	HighEffectiveness    ResolutionEffectiveness = "high"
	MediumEffectiveness  ResolutionEffectiveness = "medium"
	LowEffectiveness     ResolutionEffectiveness = "low"
	UnknownEffectiveness ResolutionEffectiveness = "unknown"
)

type ResolutionAttempt struct {
	Proposal  string
	Outcome   ResolutionOutcome
	Timestamp time.Time
	Feedback  string
	Changes   []ReviewChunk
}

type ResolutionOutcome string

const (
	ResolutionAccepted     ResolutionOutcome = "accepted"
	ResolutionRejected     ResolutionOutcome = "rejected"
	ResolutionNeedsWork    ResolutionOutcome = "needs_work"
	ResolutionInProgress   ResolutionOutcome = "in_progress"
	ResolutionInconclusive ResolutionOutcome = "inconclusive"
)

type CommentResponseProcessor struct {
	metrics MetricsCollector
}

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

	logger.Warn(ctx, "No ReviewHandoff provided for comment_response task; falling back to standalone processing. This may indicate a misconfiguration as review_chain should always run first.")
	// Fallback to standalone processing
	return p.processStandalone(ctx, task, context)
}

func (p *CommentResponseProcessor) processFromHandoff(ctx context.Context, task agents.Task, handoff *ReviewHandoff) (interface{}, error) {
	logger := logging.GetLogger()

	// Handle case with no validated issues but thread status
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
		issue = &handoff.ValidatedIssues[0] // Single-issue assumption
	}

	// Construct response
	resolutionStatus := string(handoff.ThreadStatus)
	responseContent := metadata.OriginalComment
	var actionItems []string
	if issue != nil {
		responseContent = issue.Context // Includes thread history if present
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

	// Ensure thread_context is set
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
		logger.Debug(ctx, "Built conversation history: %s", previousContext)
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
	).WithInstruction(`Analyze the review comment thread and generate an appropriate response.
    
Consider the following aspects:
1. The original review comment and its concerns
2. Any subsequent discussion in the thread
3. The current state of the code
4. Whether the feedback has been addressed

Provide response in the following format:
response: Clear and professional response addressing the feedback
resolution_status: [resolved|needs_work|needs_clarification|acknowledged]
action_items: List of specific tasks or clarifications needed, if any

NOTE: 
For needs_clarification responses, focus on asking specific questions about what needs to be clarified.
For needs_work responses, always provide specific action items
`)

	predict := modules.NewPredict(signature)
	metadata, err := extractResponseMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	logger.Debug(ctx, "Processing response for comment thread with %d messages", len(metadata.ThreadContext))
	logger.Info(ctx, "Comment Processing response with line number: %v", metadata.LineRange)

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
		comment.Suggestion = formatActionItems(response.ActionItems)
	}

	if metadata.ThreadID != nil {
		resolution := mapResponseStatusToResolution(response.ResolutionStatus)
		p.metrics.TrackCommentResolution(ctx, *metadata.ThreadID, resolution)
	}

	return comment
}

type ResponseResult struct {
	Response         string
	ResolutionStatus string
	ActionItems      []string
}

func extractResponseMetadata(metadata map[string]interface{}) (*ResponseMetadata, error) {
	rm := &ResponseMetadata{}

	// Get original comment details
	if comment, ok := metadata["original_comment"].(string); ok && comment != "" {
		rm.OriginalComment = comment
	} else {
		return nil, fmt.Errorf("missing or invalid original comment")
	}

	// Get thread context (default empty slice handled upstream)
	if thread, ok := metadata["thread_context"].([]PRReviewComment); ok {
		rm.ThreadContext = thread
	}

	// Get file content
	if content, ok := metadata["file_content"].(string); ok {
		rm.FileContent = content
	}

	// Get file path
	if path, ok := metadata["file_path"].(string); ok {
		rm.FilePath = path
	}

	// Get thread identifiers
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
		rm.LineRange = LineRange{Start: startLine, End: endLine, File: rm.FilePath}
	} else if line, ok := metadata["line_number"].(int); ok {
		rm.LineRange = LineRange{Start: line, End: line, File: rm.FilePath}
	} else {
		return nil, fmt.Errorf("missing or invalid line range information")
	}

	if replyTo, ok := metadata["in_reply_to"].(*int64); ok {
		rm.InReplyTo = replyTo
	}

	if category, ok := metadata["category"].(string); ok {
		rm.Category = category
	} else {
		rm.Category = "code-style" // Default if not provided
	}

	if rm.LineRange.Start == 0 {
		return nil, fmt.Errorf("failed to extract valid line number from metadata: %+v", metadata)
	}

	return rm, nil
}

type ResponseMetadata struct {
	OriginalComment string
	ThreadContext   []PRReviewComment
	FileContent     string
	FilePath        string
	LineRange       LineRange
	ThreadID        *int64
	InReplyTo       *int64
	Category        string
	ReviewHistory   []ReviewChunk
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

	return response, validateResponse(response)
}

func validateResponse(response *ResponseResult) error {
	if response.Response == "" {
		return fmt.Errorf("empty response content")
	}

	if containsUnprofessionalContent(response.Response) {
		return fmt.Errorf("response contains unprofessional content")
	}

	if response.ResolutionStatus == "needs_work" && len(response.ActionItems) == 0 {
		return fmt.Errorf("status indicates work needed but no action items provided")
	}

	return nil
}

func containsUnprofessionalContent(content string) bool {
	unprofessionalTerms := []string{
		"stupid",
		"lazy",
		"horrible",
		"terrible",
		"awful",
	}
	lowered := strings.ToLower(content)
	for _, term := range unprofessionalTerms {
		if strings.Contains(lowered, term) {
			return true
		}
	}
	return false
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

func formatActionItems(items []string) string {
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
