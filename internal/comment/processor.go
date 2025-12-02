// Package comment provides comment processing and refinement functionality.
package comment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
	"github.com/XiaoConstantine/maestro/internal/types"
)

// ResponseProcessor processes comment responses for review threads.
type ResponseProcessor struct {
	metrics types.MetricsCollector
}

// NewResponseProcessor creates a new comment response processor.
func NewResponseProcessor(metrics types.MetricsCollector) *ResponseProcessor {
	return &ResponseProcessor{
		metrics: metrics,
	}
}

// Process processes a comment response task.
func (p *ResponseProcessor) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()

	// Check for handoff from ReviewChainProcessor
	handoffRaw, hasHandoff := task.Metadata["handoff"]
	if hasHandoff {
		handoff, ok := handoffRaw.(*types.ReviewHandoff)
		if !ok {
			return nil, fmt.Errorf("invalid handoff type: %T", handoffRaw)
		}
		return p.processFromHandoff(ctx, task, handoff)
	}

	logger.Warn(ctx, "No ReviewHandoff provided for comment_response task; falling back to standalone processing. This may indicate a misconfiguration as review_chain should always run first.")
	// Fallback to standalone processing
	return p.processStandalone(ctx, task, context)
}

func (p *ResponseProcessor) processFromHandoff(ctx context.Context, task agents.Task, handoff *types.ReviewHandoff) (interface{}, error) {
	logger := logging.GetLogger()

	// Handle case with no validated issues but thread status
	if len(handoff.ValidatedIssues) == 0 && handoff.ThreadStatus != types.ThreadOpen {
		logger.Debug(ctx, "No validated issues in handoff and thread not open, returning nil comment")
		return nil, nil
	}

	metadata, err := ExtractResponseMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	var issue *types.ValidatedIssue
	if len(handoff.ValidatedIssues) > 0 {
		issue = &handoff.ValidatedIssues[0] // Single-issue assumption
	}

	// Construct response
	resolutionStatus := string(handoff.ThreadStatus)
	responseContent := metadata.OriginalComment
	var actionItems []string
	if issue != nil {
		responseContent = issue.Context // Includes thread history if present
		actionItems = ExtractActionItems(issue.Suggestion)
		if issue.ValidationDetails.IsActionable {
			resolutionStatus = "needs_work"
		} else if issue.Confidence > 0.9 && issue.ValidationDetails.RuleCompliant {
			resolutionStatus = "resolved"
		}
	}

	response := types.ResponseResult{
		Response:         responseContent,
		ResolutionStatus: resolutionStatus,
		ActionItems:      actionItems,
	}

	comment := p.createCommentFromResponse(ctx, metadata, &response, issue)
	logger.Debug(ctx, "Generated comment from handoff: %+v", comment)
	return comment, nil
}

func (p *ResponseProcessor) processStandalone(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()

	// Ensure thread_context is set
	if _, exists := context["thread_context"]; !exists {
		logger.Debug(ctx, "No thread context found in request")
		context["thread_context"] = []types.PRReviewComment{}
	}

	var previousContext string
	if threadContext, exists := context["thread_context"].([]types.PRReviewComment); exists && len(threadContext) > 0 {
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
	metadata, err := ExtractResponseMetadata(task.Metadata)
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

	response, err := ParseResponseResult(result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return p.createCommentFromResponse(ctx, metadata, response, nil), nil
}

func (p *ResponseProcessor) createCommentFromResponse(ctx context.Context, metadata *types.ResponseMetadata, response *types.ResponseResult, issue *types.ValidatedIssue) types.PRReviewComment {
	var severity string
	if issue != nil && issue.Severity != "" {
		severity = issue.Severity
	} else {
		severity = DeriveSeverity(response.ResolutionStatus)
	}

	comment := types.PRReviewComment{
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
		comment.Suggestion = FormatActionItems(response.ActionItems)
	}

	if metadata.ThreadID != nil {
		resolution := MapResponseStatusToResolution(response.ResolutionStatus)
		p.metrics.TrackCommentResolution(ctx, *metadata.ThreadID, resolution)
	}

	return comment
}
