package main

import (
	"context"
	"fmt"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
)

// RuleChecker handles the initial detection of potential code issues.
type RuleChecker struct {
	metrics MetricsCollector
	rules   map[string]ReviewRule
	logger  *logging.Logger
}

type RuleCheckerMetadata struct {
	FilePath       string
	FileContent    string
	Changes        string
	Guidelines     []*Content
	ReviewPatterns []*Content
	LineRange      LineRange
	ChunkNumber    int
	TotalChunks    int
}

func NewRuleChecker(metrics MetricsCollector, logger *logging.Logger) *RuleChecker {
	return &RuleChecker{
		metrics: metrics,
		rules:   make(map[string]ReviewRule),
		logger:  logger,
	}
}

// RuleChecker implementation.
func (rc *RuleChecker) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	metadata, err := extractRuleCheckerMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "file_content"}},
			{Field: core.Field{Name: "changes"}},
			{Field: core.Field{Name: "guidelines"}},
			{Field: core.Field{Name: "repo_patterns"}},
		},
		[]core.OutputField{
			{Field: core.NewField("potential_issues")},
		},
	).WithInstruction(`Analyze the code for potential issues with high recall.
    For each potential issue, provide:
    - File path and line number
    - Rule ID that detected the issue
    - Initial confidence score (0.0-1.0)
    - Problematic code snippet
    - Surrounding context
    - Preliminary suggestion
    - Category (error-handling, code-style, etc.)
    
    Focus on finding all possible issues - validation will happen in the next stage.
    Include issues even with lower confidence scores, as they will be filtered later.`)

	predict := modules.NewPredict(signature)

	rc.logger.Debug(ctx, "Starting issue detection for file: %s", metadata.FilePath)

	result, err := predict.Process(ctx, map[string]interface{}{
		"file_content":  metadata.FileContent,
		"changes":       metadata.Changes,
		"guidelines":    metadata.Guidelines,
		"repo_patterns": metadata.ReviewPatterns,
	})
	if err != nil {
		return nil, fmt.Errorf("detection failed: %w", err)
	}

	issues, err := rc.parseDetectionResult(result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse detection results: %w", err)
	}

	rc.metrics.TrackDetectionResults(ctx, len(issues))

	return issues, nil
}

func (rc *RuleChecker) parseDetectionResult(result interface{}) ([]PotentialIssue, error) {
	// First, we need to ensure the result is in the expected format
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid result type: expected map[string]interface{}, got %T", result)
	}

	// The LLM output should contain a "potential_issues" field as defined in our signature
	issuesRaw, exists := resultMap["potential_issues"]
	if !exists {
		return nil, fmt.Errorf("missing potential_issues field in result")
	}

	// The issues should be provided as an array
	issuesArray, ok := issuesRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("potential_issues must be an array, got %T", issuesRaw)
	}

	var potentialIssues []PotentialIssue

	// Process each detected issue from the LLM output
	for i, issueRaw := range issuesArray {
		issueMap, ok := issueRaw.(map[string]interface{})
		if !ok {
			rc.logger.Warn(context.Background(), "Skipping invalid issue format at index %d", i)
			continue
		}

		// Extract required fields with type validation
		filePath := getStringOrEmpty(issueMap["file_path"])
		lineNum := getIntOrZero(issueMap["line_number"])
		ruleID := getStringOrEmpty(issueMap["rule_id"])
		content := getStringOrEmpty(issueMap["content"])
		suggestion := getStringOrEmpty(issueMap["suggestion"])
		category := getStringOrEmpty(issueMap["category"])

		// Extract confidence score with validation
		confidence := 0.0
		if conf, ok := issueMap["confidence"].(float64); ok {
			confidence = conf
		}

		// Extract context information
		contextMap := make(map[string]string)
		if ctx, ok := issueMap["context"].(map[string]interface{}); ok {
			for key, value := range ctx {
				if strValue, ok := value.(string); ok {
					contextMap[key] = strValue
				}
			}
		}

		// Extract any additional metadata
		metadata := make(map[string]interface{})
		if meta, ok := issueMap["metadata"].(map[string]interface{}); ok {
			metadata = meta
		}

		// Validate required fields
		if filePath == "" || lineNum == 0 || content == "" {
			rc.logger.Warn(context.Background(),
				"Skipping issue with missing required fields at index %d", i)
			continue
		}

		// Normalize the category if provided, otherwise use a default
		if category == "" {
			category = "code-style" // Default category
		}

		// Create the PotentialIssue with all extracted information
		issue := PotentialIssue{
			FilePath:   filePath,
			LineNumber: lineNum,
			RuleID:     ruleID,
			Content:    content,
			Confidence: confidence,
			Context:    contextMap,
			Suggestion: suggestion,
			Category:   category,
			Metadata:   metadata,
		}

		// Ensure the confidence score is within valid range
		if issue.Confidence < 0 || issue.Confidence > 1 {
			issue.Confidence = 0.5 // Set a default confidence if invalid
		}

		potentialIssues = append(potentialIssues, issue)
	}

	// Log the parsing results for debugging
	rc.logger.Debug(context.Background(),
		"Parsed %d potential issues from detection results",
		len(potentialIssues))

	// Return an empty slice rather than nil if no issues were found
	if potentialIssues == nil {
		potentialIssues = make([]PotentialIssue, 0)
	}

	return potentialIssues, nil
}

// Add to review_stages.go.
func extractRuleCheckerMetadata(metadata map[string]interface{}) (*RuleCheckerMetadata, error) {
	rcm := &RuleCheckerMetadata{}

	// Extract file information
	if filePath, ok := metadata["file_path"].(string); ok {
		rcm.FilePath = filePath
	} else {
		return nil, fmt.Errorf("missing or invalid file_path")
	}

	if content, ok := metadata["file_content"].(string); ok {
		rcm.FileContent = content
	} else {
		return nil, fmt.Errorf("missing or invalid file_content")
	}

	if changes, ok := metadata["changes"].(string); ok {
		rcm.Changes = changes
	}

	// Extract guidelines and patterns
	if guidelines, ok := metadata["guidelines"].([]*Content); ok {
		rcm.Guidelines = guidelines
	}
	if patterns, ok := metadata["repo_patterns"].([]*Content); ok {
		rcm.ReviewPatterns = patterns
	}

	// Extract line range information
	if rangeData, ok := metadata["line_range"].(map[string]interface{}); ok {
		startLine, startOk := rangeData["start"].(int)
		endLine, endOk := rangeData["end"].(int)
		if !startOk || !endOk {
			return nil, fmt.Errorf("invalid line range format")
		}
		rcm.LineRange = LineRange{
			Start: startLine,
			End:   endLine,
			File:  rcm.FilePath,
		}
	}

	// Extract chunk information
	if chunkNum, ok := metadata["chunk_number"].(int); ok {
		rcm.ChunkNumber = chunkNum
	}
	if totalChunks, ok := metadata["total_chunks"].(int); ok {
		rcm.TotalChunks = totalChunks
	}

	return rcm, nil
}
