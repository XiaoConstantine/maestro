package main

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// RuleChecker handles the initial detection of potential code issues.
type RuleChecker struct {
	metrics *BusinessMetrics
	rules   map[string]ReviewRule
	logger  *logging.Logger
}

// ReviewFilter validates potential issues and generates final review comments.
type ReviewFilter struct {
	metrics       *BusinessMetrics
	contextWindow int
	logger        *logging.Logger
}

func NewRuleChecker(metrics *BusinessMetrics, logger *logging.Logger) *RuleChecker {
	return &RuleChecker{
		metrics: metrics,
		rules:   make(map[string]ReviewRule),
		logger:  logger,
	}
}

func NewReviewFilter(metrics *BusinessMetrics, contextWindow int, logger *logging.Logger) *ReviewFilter {
	return &ReviewFilter{
		metrics:       metrics,
		contextWindow: contextWindow,
		logger:        logger,
	}
}

// RuleChecker implementation.
func (rc *RuleChecker) DetectIssues(ctx context.Context, task PRReviewTask) ([]PotentialIssue, error) {
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "file_content"}},
			{Field: core.Field{Name: "changes"}},
			{Field: core.Field{Name: "guidelines"}},
		},
		[]core.OutputField{
			{Field: core.NewField("issues")},
		},
	).WithInstruction(`Analyze the code for potential issues with high recall.
    Consider all possible violations of the provided guidelines.
    For each potential issue provide:
    - Exact location (line number)
    - Relevant guideline/rule
    - Initial confidence score (0.0-1.0)
    - Brief explanation of the potential violation
    - Suggested fix if applicable

    Focus on finding all possible issues - validation will happen in the next stage.`)

	// Create predict module for detection
	predict := modules.NewPredict(signature)

	rc.logger.Debug(ctx, "Starting issue detection for file: %s", task.FilePath)

	result, err := predict.Process(ctx, map[string]interface{}{
		"file_content": task.FileContent,
		"changes":      task.Changes,
		"guidelines":   rc.rules,
	})
	if err != nil {
		return nil, fmt.Errorf("detection failed: %w", err)
	}

	// Parse detection results
	issues, err := rc.parseDetectionResult(result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse detection results: %w", err)
	}

	// Track metrics for detection stage
	rc.metrics.TrackDetectionResults(ctx, len(issues))

	return issues, nil
}

func (rc *RuleChecker) parseDetectionResult(result interface{}) ([]PotentialIssue, error) {
	// Implementation of result parsing logic
	// Convert LLM output into PotentialIssue structs
	return nil, nil
}

// ReviewFilter implementation.
func (rf *ReviewFilter) ValidateIssue(ctx context.Context, issue PotentialIssue, fileContent string) (*PRReviewComment, error) {
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "issue"}},
			{Field: core.Field{Name: "code_context"}},
			{Field: core.Field{Name: "full_file"}},
		},
		[]core.OutputField{
			{Field: core.NewField("is_valid")},
			{Field: core.NewField("confidence")},
			{Field: core.NewField("comment")},
		},
	).WithInstruction(`Validate the detected issue by performing these checks:
    1. Analyze the full context around the issue
    2. Verify the rule actually applies in this context
    3. Check for false positives and edge cases
    4. Ensure the suggestion is appropriate and actionable
    5. Consider the practical impact of the issue

    Only validate issues where you have high confidence.
    Focus on precision - reject any issues that aren't clearly problematic.`)

	// Extract context around the issue
	context, err := rf.extractContext(fileContent, issue.LineNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to extract context: %w", err)
	}

	predict := modules.NewPredict(signature)
	result, err := predict.Process(ctx, map[string]interface{}{
		"issue":        issue,
		"code_context": context,
		"full_file":    fileContent,
	})
	if err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	comment, valid, err := rf.parseValidationResult(result, issue)
	if err != nil {
		return nil, err
	}

	if !valid {
		rf.logger.Debug(ctx, "Issue rejected during validation: %v", issue)
		return nil, nil
	}

	// Track validation metrics
	rf.metrics.TrackValidationResult(ctx, issue.Category, true)

	return comment, nil
}

func (rf *ReviewFilter) extractContext(content string, lineNumber int) (string, error) {
	lines := strings.Split(content, "\n")
	if lineNumber < 1 || lineNumber > len(lines) {
		return "", fmt.Errorf("line number out of range")
	}

	start := max(1, lineNumber-rf.contextWindow)
	end := min(len(lines), lineNumber+rf.contextWindow)

	return strings.Join(lines[start-1:end], "\n"), nil
}

// Add to review_stages.go.
func (rf *ReviewFilter) parseValidationResult(result interface{}, issue PotentialIssue) (*PRReviewComment, bool, error) {
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("invalid result type: %T", result)
	}

	// Check if the issue is valid
	isValid, ok := resultMap["is_valid"].(bool)
	if !ok {
		return nil, false, fmt.Errorf("missing or invalid is_valid field")
	}

	// If not valid, return early
	if !isValid {
		return nil, false, nil
	}

	// Get the confidence score
	confidence, ok := resultMap["confidence"].(float64)
	if !ok || confidence < 0.8 { // We require high confidence
		return nil, false, nil
	}

	// Parse the comment content
	commentContent, ok := resultMap["comment"].(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("invalid comment format")
	}

	// Create the review comment
	comment := &PRReviewComment{
		FilePath:   issue.FilePath,
		LineNumber: issue.LineNumber,
		Content:    commentContent["content"].(string),
		Severity:   deriveSeverityFromConfidence(confidence),
		Category:   issue.Category,
		Suggestion: commentContent["suggestion"].(string),
		Timestamp:  time.Now(),
	}

	rf.logger.Debug(context.Background(), "Parsed validation result: valid=%v, confidence=%.2f",
		isValid, confidence)

	return comment, true, nil
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
