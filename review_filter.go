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

// PotentialIssue represents a detected but unvalidated code issue.
type PotentialIssue struct {
	FilePath   string
	LineNumber int
	RuleID     string            // Reference to the rule that detected this
	Confidence float64           // Initial confidence score
	Content    string            // Detected problematic code
	Context    map[string]string // Surrounding code context
	Suggestion string            // Initial suggested fix
	Category   string
	Metadata   map[string]interface{}
}

// ReviewFilter validates potential issues and generates final review comments.
type ReviewFilter struct {
	metrics       MetricsCollector
	contextWindow int
	logger        *logging.Logger
}

type ReviewFilterMetadata struct {
	FilePath    string
	FileContent string
	Changes     string
	Issues      []PotentialIssue
	LineRange   LineRange
}

func NewReviewFilter(metrics MetricsCollector, contextWindow int, logger *logging.Logger) *ReviewFilter {
	return &ReviewFilter{
		metrics:       metrics,
		contextWindow: contextWindow,
		logger:        logger,
	}
}

// ReviewFilter implementation.
func (rf *ReviewFilter) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	metadata, err := extractReviewFilterMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "issues"}},
			{Field: core.Field{Name: "code_context"}},
			{Field: core.Field{Name: "full_file"}},
		},
		[]core.OutputField{
			{Field: core.NewField("validated_issues")},
		},
	).WithInstruction(`Validate each detected issue by performing these checks:
    1. Analyze the full context around each issue
    2. Verify the rules actually apply in this context
    3. Check for false positives and edge cases
    4. Ensure suggestions are appropriate and actionable
    5. Consider the practical impact of each issue

    For each issue, provide:
    1. is_valid: true/false - whether this is a genuine issue
    2. confidence: 0.0-1.0 - how confident we are in this assessment
    3. comment: {
        file_path: string,
        line_number: number,
        content: string,
        category: string,
        suggestion: string
    }

    Only mark issues as valid when you're very confident (>80%).
    Focus on precision - reject any issues that aren't clearly problematic.`)
	// Extract code context for each issue
	codeContexts := make(map[string]string)
	for _, issue := range metadata.Issues {
		contextText, err := rf.extractContext(metadata.FileContent, issue.LineNumber)
		if err != nil {
			rf.logger.Warn(ctx, "Failed to extract context for line %d: %v", issue.LineNumber, err)
			continue
		}
		codeContexts[fmt.Sprintf("line_%d", issue.LineNumber)] = contextText
	}

	predict := modules.NewPredict(signature)
	result, err := predict.Process(ctx, map[string]interface{}{
		"issues":       metadata.Issues,
		"code_context": codeContexts,
		"full_file":    metadata.FileContent,
	})
	if err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	validatedComments, err := rf.parseValidationResult(result)
	if err != nil {
		return nil, err
	}

	// Track validation metrics
	for _, comment := range validatedComments {
		rf.metrics.TrackValidationResult(ctx, comment.Category, true)
	}

	return validatedComments, nil
}

// ReviewFilter implementation.
func (rf *ReviewFilter) extractContext(content string, lineNumber int) (string, error) {
	lines := strings.Split(content, "\n")
	if lineNumber < 1 || lineNumber > len(lines) {
		return "", fmt.Errorf("line number out of range")
	}

	start := max(1, lineNumber-rf.contextWindow)
	end := min(len(lines), lineNumber+rf.contextWindow)

	return strings.Join(lines[start-1:end], "\n"), nil
}

func (rf *ReviewFilter) parseValidationResult(result interface{}) ([]*PRReviewComment, error) {
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid result type: %T", result)
	}

	validatedResults, ok := resultMap["validated_issues"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("missing or invalid validated_issues field")
	}

	var validatedComments []*PRReviewComment

	for _, validatedResult := range validatedResults {
		resultData, ok := validatedResult.(map[string]interface{})
		if !ok {
			continue
		}

		// Check validity and confidence
		isValid, ok := resultData["is_valid"].(bool)
		if !ok || !isValid {
			continue
		}

		confidence, ok := resultData["confidence"].(float64)
		if !ok || confidence < 0.8 { // We require high confidence
			continue
		}

		commentData, ok := resultData["comment"].(map[string]interface{})
		if !ok {
			continue
		}

		// Create validated comment
		comment := &PRReviewComment{
			FilePath:   commentData["file_path"].(string),
			LineNumber: int(commentData["line_number"].(float64)),
			Content:    commentData["content"].(string),
			Severity:   deriveSeverityFromConfidence(confidence),
			Category:   commentData["category"].(string),
			Suggestion: commentData["suggestion"].(string),
			Timestamp:  time.Now(),
		}

		validatedComments = append(validatedComments, comment)
	}

	return validatedComments, nil
}

// Helper function to derive severity based on confidence.
func deriveSeverityFromConfidence(confidence float64) string {
	switch {
	case confidence >= 0.9:
		return "critical"
	case confidence >= 0.8:
		return "warning"
	default:
		return "suggestion"
	}
}

// Add to review_stages.go.
func extractReviewFilterMetadata(metadata map[string]interface{}) (*ReviewFilterMetadata, error) {
	rfm := &ReviewFilterMetadata{}

	// Extract file information
	if filePath, ok := metadata["file_path"].(string); ok {
		rfm.FilePath = filePath
	} else {
		return nil, fmt.Errorf("missing or invalid file_path")
	}

	if content, ok := metadata["file_content"].(string); ok {
		rfm.FileContent = content
	} else {
		return nil, fmt.Errorf("missing or invalid file_content")
	}

	if changes, ok := metadata["changes"].(string); ok {
		rfm.Changes = changes
	}

	// Extract potential issues
	if issues, ok := metadata["issues"].([]PotentialIssue); ok {
		rfm.Issues = issues
	} else {
		return nil, fmt.Errorf("missing or invalid issues")
	}

	// Extract line range if present
	if rangeData, ok := metadata["line_range"].(map[string]interface{}); ok {
		startLine, startOk := rangeData["start"].(int)
		endLine, endOk := rangeData["end"].(int)
		if !startOk || !endOk {
			return nil, fmt.Errorf("invalid line range format")
		}
		rfm.LineRange = LineRange{
			Start: startLine,
			End:   endLine,
			File:  rfm.FilePath,
		}
	}

	return rfm, nil
}
