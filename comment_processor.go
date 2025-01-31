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

type CommentResponseProcessor struct {
	previousContext string
}

func (p *CommentResponseProcessor) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()

	// Define our signature with clear input and output expectations
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "original_comment"}},
			{Field: core.Field{Name: "thread_context"}},
			{Field: core.Field{Name: "file_content"}},
			{Field: core.Field{Name: "review_history"}},
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
action_items: List of specific tasks or clarifications needed, if any`)

	// Create predict module for generating responses
	predict := modules.NewPredict(signature)

	// Extract metadata from the task
	metadata, err := extractResponseMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	// Log the context we're working with
	logger.Debug(ctx, "Processing response for comment thread with %d messages",
		len(metadata.ThreadContext))

	// Process the response
	result, err := predict.Process(ctx, map[string]interface{}{
		"original_comment": metadata.OriginalComment,
		"thread_context":   metadata.ThreadContext,
		"file_content":     metadata.FileContent,
		"review_history":   metadata.ReviewHistory,
	})
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	// Parse and validate the response
	response, err := parseResponseResult(result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	logger.Debug(ctx, "Generated response with status: %s", response.ResolutionStatus)

	return response, nil
}

// ResponseMetadata contains all the context needed for processing a response.
type ResponseMetadata struct {
	OriginalComment string
	ThreadContext   []CommentThread
	FileContent     string
	ReviewHistory   []ReviewAction
	FilePath        string
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

// ResponseResult represents the structured output of our response generation.
type ResponseResult struct {
	Response         string
	ResolutionStatus string
	ActionItems      []string
}

func extractResponseMetadata(metadata map[string]interface{}) (*ResponseMetadata, error) {
	rm := &ResponseMetadata{}

	// Extract original comment
	if comment, ok := metadata["original_comment"].(string); ok {
		rm.OriginalComment = comment
	} else {
		return nil, fmt.Errorf("missing or invalid original comment")
	}

	// Extract thread context
	if threadCtx, ok := metadata["thread_context"].([]CommentThread); ok {
		rm.ThreadContext = threadCtx
	} else {
		// Initialize empty thread context if none provided
		rm.ThreadContext = []CommentThread{}
	}

	// Extract file content
	if content, ok := metadata["file_content"].(string); ok {
		rm.FileContent = content
	}

	// Extract review history
	if history, ok := metadata["review_history"].([]ReviewAction); ok {
		rm.ReviewHistory = history
	} else {
		rm.ReviewHistory = []ReviewAction{}
	}

	return rm, nil
}

func parseResponseResult(result interface{}) (*ResponseResult, error) {
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid result type: %T", result)
	}

	response := &ResponseResult{}

	// Extract response content
	if respContent, ok := resultMap["response"].(string); ok {
		response.Response = respContent
	} else {
		return nil, fmt.Errorf("missing response content")
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
