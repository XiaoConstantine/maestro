// Package workflow provides declarative workflow patterns for code review.
package workflow

import (
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
)

// ExtractConfidenceScore extracts confidence score from result.
func ExtractConfidenceScore(result interface{}) float64 {
	if resultMap, ok := result.(map[string]interface{}); ok {
		if confidence, exists := resultMap["confidence"]; exists {
			if score, ok := confidence.(float64); ok {
				return score
			}
		}
	}
	return 0.5 // Default confidence
}

// EstimateCodeComplexity estimates code complexity from content.
func EstimateCodeComplexity(content string) float64 {
	// Handle empty content
	if content == "" {
		return 0.0
	}

	// Simple complexity estimation based on various factors
	lines := len(strings.Split(content, "\n"))
	complexity := 0.0

	// Add complexity for control structures
	complexity += float64(strings.Count(content, "if ")) * 0.1
	complexity += float64(strings.Count(content, "for ")) * 0.15
	complexity += float64(strings.Count(content, "switch ")) * 0.2
	complexity += float64(strings.Count(content, "func ")) * 0.1

	// Normalize by line count
	if lines > 0 {
		complexity = complexity / float64(lines) * 100
	}

	// Cap at 1.0
	if complexity > 1.0 {
		complexity = 1.0
	}

	return complexity
}

// AnalyzeChangeScope analyzes the scope of changes.
func AnalyzeChangeScope(changes string) string {
	// Handle empty changes
	if changes == "" {
		return "none"
	}

	lineCount := len(strings.Split(changes, "\n"))

	if lineCount < 10 {
		return "small"
	} else if lineCount < 50 {
		return "medium"
	} else if lineCount < 200 {
		return "large"
	}
	return "massive"
}

// CalculateOverallQuality calculates overall quality based on result metrics.
func CalculateOverallQuality(resultMap map[string]interface{}) float64 {
	// Calculate overall quality based on various metrics
	quality := 0.5 // Base quality

	if confidence, exists := resultMap["confidence"]; exists {
		if score, ok := confidence.(float64); ok {
			quality += score * 0.3
		}
	}

	if issues, exists := resultMap["issues"]; exists {
		if issueList, ok := issues.([]interface{}); ok {
			// Fewer issues = higher quality
			issueCount := len(issueList)
			if issueCount == 0 {
				quality += 0.2
			} else if issueCount < 3 {
				quality += 0.1
			}
		}
	}

	// Cap at 1.0
	if quality > 1.0 {
		quality = 1.0
	}

	return quality
}

// ExtractIntent extracts intent from content.
func ExtractIntent(content string) string {
	// Simple intent extraction
	if strings.Contains(content, "review") {
		return "code_review"
	} else if strings.Contains(content, "question") {
		return "question"
	} else if strings.Contains(content, "help") {
		return "assistance"
	}
	return "general"
}

// ExtractCodeReferences extracts code references from content.
func ExtractCodeReferences(content string) []CodeReference {
	var refs []CodeReference

	if strings.Contains(content, ".go") {
		refs = append(refs, CodeReference{
			FilePath: "extracted.go",
			Content:  "extracted code",
			Context:  "code discussion",
		})
	}

	return refs
}

// ExtractTaskComplexity extracts task complexity from task metadata.
func ExtractTaskComplexity(task agents.Task) float64 {
	// Simple complexity estimation based on task metadata
	complexity := 0.5 // Base complexity

	if fileContent, exists := task.Metadata["file_content"].(string); exists {
		// Increase complexity based on content size and structure
		lines := len(strings.Split(fileContent, "\n"))
		complexity += float64(lines) / 1000 // Add complexity for large files

		if strings.Contains(fileContent, "interface") || strings.Contains(fileContent, "struct") {
			complexity += 0.2
		}
	}

	return minFloat64(complexity, 1.0)
}

// minFloat64 returns the minimum of two float64 values.
func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// ExtractQualityScore extracts quality score from result.
func ExtractQualityScore(result map[string]interface{}) float64 {
	if result == nil {
		return 0.0
	}

	if quality, exists := result["quality_score"].(float64); exists {
		return quality
	}

	return 0.5 // Default quality
}
