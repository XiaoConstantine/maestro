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

// Extend ThreadContext to include more detailed tracking.
type ThreadContext struct {
	// Existing fields
	OriginalConcern    string
	ConversationFlow   []PRReviewComment
	RelatedChanges     []ReviewChunk
	ResolutionAttempts []ResolutionAttempt
	LastInteraction    time.Time

	// New fields for enhanced context
	Status          ThreadStatus
	Participants    []string
	RelatedThreads  []int64        // IDs of related discussion threads
	CategoryMetrics map[string]int // Track frequency of issue categories
}

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
	Changes   []ReviewChunk // Use ReviewChunk instead of CodeChange
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
	previousContext string
	metrics         MetricsCollector
}

func (p *CommentResponseProcessor) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()
	// Ensure we have valid thread context
	if _, exists := context["thread_context"]; !exists {
		logger.Debug(ctx, "No thread context found in request")
		context["thread_context"] = []PRReviewComment{}
	}

	threadContext, exists := context["thread_context"].([]PRReviewComment)
	if exists && len(threadContext) > 0 {
		// Build conversation history
		var conversationHistory strings.Builder
		for _, comment := range threadContext {
			conversationHistory.WriteString(fmt.Sprintf("Author: %s\n", comment.Author))
			conversationHistory.WriteString(fmt.Sprintf("Message: %s\n", comment.Content))
			if comment.Suggestion != "" {
				conversationHistory.WriteString(fmt.Sprintf("Suggestion: %s\n", comment.Suggestion))
			}
			conversationHistory.WriteString("---\n")
		}
		p.previousContext = conversationHistory.String()

		logger.Debug(ctx, "Built conversation history: %s", p.previousContext)
	}
	// Define our signature with clear input and output expectations
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

	// Create predict module for generating responses
	predict := modules.NewPredict(signature)

	logger.Info(ctx, "Raw task Meta: %v", task.Metadata)

	// Extract metadata from the task
	metadata, err := extractResponseMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	// Log the context we're working with
	logger.Debug(ctx, "Processing response for comment thread with %d messages",
		len(metadata.ThreadContext))

	logger.Info(ctx, "Comment Processing response with line number: %v", metadata.LineRange)
	// Process the response
	result, err := predict.Process(ctx, map[string]interface{}{
		"original_comment": metadata.OriginalComment,
		"thread_context":   metadata.ThreadContext,
		//"file_content":     escapeXMLContent(metadata.FileContent),
		"file_content":   metadata.FileContent,
		"review_history": p.previousContext,
		"line_range":     metadata.LineRange,
		"file_path":      metadata.FilePath,
	})
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	// Parse and validate the response
	response, err := parseResponseResult(result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if metadata.ThreadID != nil {
		resolution := mapResponseStatusToResolution(response.ResolutionStatus)
		p.metrics.TrackCommentResolution(ctx, *metadata.ThreadID, resolution)
	}
	comment := PRReviewComment{
		FilePath:   metadata.FilePath,
		LineNumber: metadata.LineRange.Start,
		Content:    response.Response,
		Severity:   deriveSeverity(response.ResolutionStatus),
		Category:   "code-style",
		InReplyTo:  metadata.InReplyTo,
		ThreadID:   metadata.ThreadID,
		Resolved:   response.ResolutionStatus == "resolved",
		Timestamp:  time.Now(),
	}
	if len(response.ActionItems) > 0 {
		comment.Suggestion = formatActionItems(response.ActionItems)
	}

	return comment, nil
}

// CommentThread represents a series of related comments.
type CommentThread struct {
	Comments   []string
	Timestamps []time.Time
	Authors    []string
}

// ReviewAction represents an action taken in the review.
type ReviewAction struct {
	Type      string // comment, suggestion, resolution
	Content   string
	Timestamp time.Time
	Author    string
}

type ResponseResult struct {
	Response         string
	ResolutionStatus string
	ActionItems      []string
}

// Extract metadata from the task context.
func extractResponseMetadata(metadata map[string]interface{}) (*ResponseMetadata, error) {
	rm := &ResponseMetadata{}

	// Get original comment details
	if comment, ok := metadata["original_comment"].(string); ok {
		rm.OriginalComment = comment
	} else {
		return nil, fmt.Errorf("missing or invalid original comment")
	}

	// Get thread context
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

		rm.LineRange = LineRange{
			Start: startLine,
			End:   endLine,
			File:  rm.FilePath,
		}

		if !rm.LineRange.IsValid() {
			return nil, fmt.Errorf("invalid line range: %v", rm.LineRange)
		}
	} else {
		// For backward compatibility, check for single line number
		if line, ok := metadata["line_number"].(int); ok {
			rm.LineRange = LineRange{
				Start: line,
				End:   line,
				File:  rm.FilePath,
			}
		} else {
			return nil, fmt.Errorf("missing or invalid line range information")
		}
	}

	if replyTo, ok := metadata["in_reply_to"].(*int64); ok {
		rm.InReplyTo = replyTo
	}

	// Get category
	if category, ok := metadata["category"].(string); ok {
		rm.Category = category
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

	// Extract response content
	if respContent, ok := resultMap["response"].(string); ok {
		response.Response = respContent
	} else {
		return nil, fmt.Errorf("missing or invalid response content, got %T", resultMap["response"])
	}

	// Extract resolution status
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

	// Extract action items
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

	// Ensure response maintains professional tone
	if containsUnprofessionalContent(response.Response) {
		return fmt.Errorf("response contains unprofessional content")
	}

	// Validate action items if status indicates work needed
	if response.ResolutionStatus == "needs_work" && len(response.ActionItems) == 0 {
		return fmt.Errorf("status indicates work needed but no action items provided")
	}

	return nil
}

func containsUnprofessionalContent(content string) bool {
	// Check for unprofessional language or tone
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

// Helper function to format action items into a suggestion.
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
