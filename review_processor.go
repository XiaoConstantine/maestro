package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
)

type CodeReviewProcessor struct{}

func (p *CodeReviewProcessor) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()
	metadata, err := extractReviewMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("task %s: %w", task.ID, err)
	}

	instruction := buildReviewInstruction(metadata.Guidelines)
	// Create signature for code review
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "file_content"}},
			{Field: core.Field{Name: "changes"}},
			{Field: core.Field{Name: "guidelines"}},    // Added for best practices
			{Field: core.Field{Name: "repo_patterns"}}, // Added for consistency
		},
		[]core.OutputField{
			{Field: core.NewField("comments")},
			{Field: core.NewField("summary")},
		},
	).WithInstruction(instruction)
	// 	).WithInstruction(`Review the code changes and provide specific, actionable feedback.
	// Consider both best practices from guidelines and consistency with existing patterns.
	// For each issue found, output in following format:
	//
	// comments:
	//   file: [filename]
	//   line: [specific line number where the issue occurs]
	//   severity: [must be one of: critical, warning, suggestion]
	//   category: [must be one of: error-handling, code-style, performance, security, documentation]
	//   content: [clear explanation of the issue and why it matters]
	//   suggestion: [specific code example or clear steps to fix the issue]
	//
	// Review for these specific issues:
	// 1. Error Handling
	//    - Missing error checks or ignored errors
	//    - Inconsistent error handling patterns
	//    - Silent failures
	// 2. Code Quality
	//    - Function complexity and length
	//    - Code duplication
	//    - Unclear logic or control flow
	// 3. Documentation
	//    - Missing documentation for exported items
	//    - Unclear or incomplete comments
	// 4. Performance
	//    - Inefficient patterns
	//    - Resource leaks
	//    - Unnecessary allocations
	// 5. Best Practices
	//    - Go idioms and conventions
	//    - Package organization
	//    - Clear naming conventions`)
	//
	// Create predict module for review
	predict := modules.NewPredict(signature)

	if metadata.FileContent == "" && metadata.Changes == "" {
		return nil, fmt.Errorf("both file content and changes cannot be empty for file %s", metadata.FilePath)
	}
	logger.Debug(ctx, "Extracted metadata for task %s: file_path=%s, content_length=%d",
		task.ID, metadata.FilePath, len(metadata.FileContent))
	// Process the review
	result, err := predict.Process(ctx, map[string]interface{}{
		"file_content":  metadata.FileContent,
		"changes":       metadata.Changes,
		"guidelines":    metadata.Guidelines,
		"repo_patterns": metadata.ReviewPatterns,
	})
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	// Parse and format comments
	comments, err := extractComments(result, metadata.FilePath)

	if err != nil {
		return nil, fmt.Errorf("failed to parse comments for task %s: %w", task.ID, err)
	}

	logger.Debug(ctx, "Successfully processed review for task %s with %d comments",
		task.ID, len(comments))

	return comments, nil
}

// Helper functions.
func parseReviewComments(filePath string, commentsStr string) ([]PRReviewComment, error) {
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
		}
	}

	return comments, nil
}

func extractComments(result interface{}, filePath string) ([]PRReviewComment, error) {
	if comments, ok := result.([]PRReviewComment); ok {
		return comments, nil
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid task result type: %T", result)
	}
	commentsRaw, exists := resultMap["comments"]
	if !exists {
		return nil, fmt.Errorf("prediction result missing 'comments' field")
	}

	commentsStr, ok := commentsRaw.(string)
	if !ok {
		return nil, fmt.Errorf("comments must be string, got %T", commentsRaw)
	}

	return parseReviewComments(filePath, commentsStr)
}

func extractReviewMetadata(metadata map[string]interface{}) (*ReviewMetadata, error) {
	rm := &ReviewMetadata{}

	// Extract category (always required)
	categoryRaw, exists := metadata["category"]
	if !exists {
		return nil, fmt.Errorf("missing required field 'category' in metadata")
	}
	category, ok := categoryRaw.(string)
	if !ok {
		return nil, fmt.Errorf("field 'category' must be string, got %T", categoryRaw)
	}
	rm.Category = category

	filePathRaw, exists := metadata["file_path"]
	if !exists {
		return nil, fmt.Errorf("missing required field 'file_path' for file review")
	}
	filePath, ok := filePathRaw.(string)
	if !ok {
		return nil, fmt.Errorf("field 'file_path' must be string, got %T", filePathRaw)
	}
	rm.FilePath = filePath

	// Extract changes (required for file reviews)
	changesRaw, exists := metadata["changes"]
	if !exists {
		return nil, fmt.Errorf("missing required field 'changes' for file review")
	}
	changes, ok := changesRaw.(string)
	if !ok {
		return nil, fmt.Errorf("field 'changes' must be string, got %T", changesRaw)
	}
	rm.Changes = changes

	if fileContent, ok := metadata["file_content"]; ok {
		if str, ok := fileContent.(string); ok {
			rm.FileContent = str
		}
	}
	if start, ok := getIntFromMetadata(metadata, "chunk_start"); ok {
		rm.LineRange.Start = start
	}

	if end, ok := getIntFromMetadata(metadata, "chunk_end"); ok {
		rm.LineRange.End = end
	}

	if chunkNum, ok := getIntFromMetadata(metadata, "chunk_number"); ok {
		rm.ChunkNumber = chunkNum
	}
	if totalChunks, ok := getIntFromMetadata(metadata, "total_chunks"); ok {
		rm.TotalChunks = totalChunks
	}

	if rangeData, ok := metadata["line_range"].(map[string]interface{}); ok {
		startLine, startOk := getIntFromMetadata(rangeData, "start")
		endLine, endOk := getIntFromMetadata(rangeData, "end")
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
	}
	// Validate chunk information
	if rm.LineRange.Start == 0 || rm.LineRange.End == 0 {
		return nil, fmt.Errorf("missing or invalid chunk line range")
	}
	if rm.ChunkNumber == 0 || rm.TotalChunks == 0 {
		return nil, fmt.Errorf("missing or invalid chunk numbering")
	}
	if patternsRaw, exists := metadata["repo_patterns"]; exists {
		if patterns, ok := patternsRaw.([]*Content); ok {
			rm.ReviewPatterns = patterns
		} else {
			// Log a warning but don't fail - patterns are optional
			logging.GetLogger().Warn(context.Background(),
				"Invalid repo_patterns type: %T, expected []*Content", patternsRaw)
		}
	}

	// Extract guidelines for best practices checking
	if guidelinesRaw, exists := metadata["guidelines"]; exists {
		if guidelines, ok := guidelinesRaw.([]*Content); ok {
			rm.Guidelines = guidelines
		} else {
			// Log a warning but don't fail - guidelines are optional
			logging.GetLogger().Warn(context.Background(),
				"Invalid guidelines type: %T, expected []*Content", guidelinesRaw)
		}
	}

	return rm, nil
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

func getIntFromMetadata(metadata map[string]interface{}, key string) (int, bool) {
	if val, exists := metadata[key]; exists {
		switch v := val.(type) {
		case int:
			return v, true
		case float64:
			return int(v), true
		case string:
			if num, err := strconv.Atoi(v); err == nil {
				return num, true
			}
		case json.Number:
			if i, err := v.Int64(); err == nil {
				return int(i), true
			}
		}
	}
	return 0, false
}

func buildReviewInstruction(guidelines []*Content) string {
	var builder strings.Builder

	builder.WriteString(`Review the code changes following these specific guidelines:

For each potential issue, provide:
- Precise location (file and line number)
- Clear explanation of the issue
- Specific suggestion for improvement
- Reference to the relevant guideline

Focus on these key aspects based on the matched guidelines:
`)

	// Add specific guidance from each relevant guideline
	for _, guideline := range guidelines {
		builder.WriteString(fmt.Sprintf("\n• %s:\n", guideline.Metadata["category"]))
		builder.WriteString(extractKeyPoints(guideline.Text))
	}

	return builder.String()
}

// extractKeyPoints analyzes guideline text and extracts the most important points
// for code review. It looks for specific patterns that indicate key requirements,
// common issues, and best practices.
func extractKeyPoints(content string) string {
	// Split content into sections based on common headers
	sections := strings.Split(content, "\n")
	var keyPoints strings.Builder

	inRelevantSection := false
	for _, line := range sections {
		// Look for sections that typically contain key review points
		if strings.Contains(line, "Review Criteria:") ||
			strings.Contains(line, "Best Practice Guidelines:") {
			inRelevantSection = true
			continue
		}

		// Stop extracting when we hit examples or other sections
		if strings.Contains(line, "Examples:") ||
			strings.Contains(line, "Implementation:") {
			inRelevantSection = false
		}

		// Collect bullet points and key statements
		if inRelevantSection && strings.TrimSpace(line) != "" {
			// Clean up the line and add indentation for readability
			cleanLine := strings.TrimPrefix(strings.TrimSpace(line), "-")
			cleanLine = strings.TrimPrefix(cleanLine, "•")
			cleanLine = strings.TrimSpace(cleanLine)

			if cleanLine != "" {
				keyPoints.WriteString("  • " + cleanLine + "\n")
			}
		}
	}

	return keyPoints.String()
}
