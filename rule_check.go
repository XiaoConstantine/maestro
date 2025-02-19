package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/errors"
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

	Category string
}

// RuleCheckResult represents a structured issue found during analysis.
type RuleCheckResult struct {
	FilePath   string  `json:"file_path"`
	LineNumber int     `json:"line_number"`
	RuleID     string  `json:"rule_id"`
	Confidence float64 `json:"confidence"`
	Content    string  `json:"content"`
	Context    struct {
		Before string `json:"before"`
		After  string `json:"after"`
	} `json:"context"`
	Suggestion string                 `json:"suggestion"`
	Category   string                 `json:"category"`
	Metadata   map[string]interface{} `json:"metadata"`
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
		For each potential issue found, provide the information in the following XML format:
		<potential_issues>
		<issue>
		<file_path>string</file_path>
		<line_number>integer</line_number>
		<rule_id>string</rule_id>
		<confidence>float between 0.0-1.0</confidence>
		<content>string describing the problematic code</content>
		<context>
		<before>lines before the issue</before>
		<after>lines after the issue</after>
		</context>
		<suggestion>specific steps to fix the issue</suggestion>
		<category>one of: error-handling, code-style, performance, security, documentation</category>
		<metadata></metadata>
		</issue>
		<!-- Additional issues as needed --
		</potential_issues>

		Important format requirements
		1. Use proper XML escaping for special characters in code conten
		2. Ensure line numbers are valid integer
		3. Keep confidence scores between 0.0 and 1.
		4. Use standard category value
		5. Provide specific, actionable suggestion
		6. Include relevant context before/after the issue
		7. Before inserting it into the XML, replace every '<' with '&lt; and every '>' with '&gt;', every & with &amp.

		Focus on finding
		1. Error handling issue
		2. Code style violation
		3. Performance concern
		4. Security vulnerabilitie
		5. Documentation gaps
		Only report issues with high confidence (>0.7) and clear impact.`)
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

	// Parse the XML response into a structured format
	xmlContent, ok := result["potential_issues"].(string)
	if !ok {
		return nil, errors.New(errors.InvalidResponse, "missing potential_issues in response")
	}

	// Create a struct to unmarshal the XML
	var xmlResults struct {
		Issues []struct {
			FilePath   string  `xml:"file_path"`
			LineNumber int     `xml:"line_number"`
			RuleID     string  `xml:"rule_id"`
			Confidence float64 `xml:"confidence"`
			Content    string  `xml:"content"`
			Context    struct {
				Before string `xml:"before"`
				After  string `xml:"after"`
			} `xml:"context"`
			Suggestion string `xml:"suggestion"`
			Category   string `xml:"category"`
		} `xml:"issue"`
	}

	if err := xml.Unmarshal([]byte(xmlContent), &xmlResults); err != nil {
		return nil, errors.WithFields(
			errors.Wrap(err, errors.InvalidResponse, "failed to parse detection results"),
			errors.Fields{
				"xml_content": xmlContent,
			})
	}

	// Convert XML results to the format expected by orchestrator
	issues := make([]RuleCheckResult, len(xmlResults.Issues))
	for i, issue := range xmlResults.Issues {
		issues[i] = RuleCheckResult{
			FilePath:   issue.FilePath,
			LineNumber: issue.LineNumber,
			RuleID:     issue.RuleID,
			Confidence: issue.Confidence,
			Content:    issue.Content,
			Context: struct {
				Before string `json:"before"`
				After  string `json:"after"`
			}{
				Before: issue.Context.Before,
				After:  issue.Context.After,
			},
			Suggestion: issue.Suggestion,
			Category:   issue.Category,
			Metadata:   make(map[string]interface{}),
		}
	}

	// Return the results in a format the orchestrator expects
	return map[string]interface{}{
		"potential_issues": issues, // This will be properly serialized as an array
		"metadata": map[string]interface{}{
			"issue_count": len(issues),
			"task_id":     task.ID,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		},
	}, nil
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
