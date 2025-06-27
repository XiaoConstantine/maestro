package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// EnhancedCodeReviewProcessor implements chain-of-thought reasoning for code review
type EnhancedCodeReviewProcessor struct {
	reasoningModule *modules.ChainOfThought
	metrics         MetricsCollector
	logger          *logging.Logger
}

// ReviewIssue represents a code issue identified through reasoning
type ReviewIssue struct {
	FilePath     string  `json:"file_path"`
	LineRange    LineRange `json:"line_range"`
	Category     string  `json:"category"`
	Severity     string  `json:"severity"`
	Description  string  `json:"description"`
	Reasoning    string  `json:"reasoning"`
	Suggestion   string  `json:"suggestion"`
	Confidence   float64 `json:"confidence"`
	CodeExample  string  `json:"code_example,omitempty"`
}

// EnhancedReviewResult contains the output of enhanced reasoning
type EnhancedReviewResult struct {
	Issues          []ReviewIssue `json:"issues"`
	OverallQuality  string        `json:"overall_quality"`
	ReasoningChain  string        `json:"reasoning_chain"`
	Confidence      float64       `json:"confidence"`
	ProcessingTime  float64       `json:"processing_time_ms"`
}

// NewEnhancedCodeReviewProcessor creates a new enhanced processor with chain-of-thought reasoning
func NewEnhancedCodeReviewProcessor(metrics MetricsCollector, logger *logging.Logger) *EnhancedCodeReviewProcessor {
	// Create signature for code review reasoning
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "file_content", Description: "The source code to review"}},
			{Field: core.Field{Name: "changes", Description: "The specific changes made to the code"}},
			{Field: core.Field{Name: "guidelines", Description: "Coding guidelines and standards"}},
			{Field: core.Field{Name: "repo_context", Description: "Repository context and patterns"}},
			{Field: core.Field{Name: "file_path", Description: "Path of the file being reviewed"}},
		},
		[]core.OutputField{
			{Field: core.Field{Name: "reasoning_steps", Description: "Step by step reasoning process"}},
			{Field: core.Field{Name: "issues_found", Description: "JSON array of identified issues"}},
			{Field: core.Field{Name: "overall_assessment", Description: "Overall code quality assessment"}},
			{Field: core.Field{Name: "confidence_score", Description: "Confidence in the analysis (0-1)"}},
		},
	).WithInstruction(`
You are an expert code reviewer. Think step by step about this code review:

REASONING PROCESS:
1. UNDERSTANDING: What does this code do? What are the key components and logic flows?
2. CHANGE ANALYSIS: What specific changes were made? Why might these changes have been made?
3. ISSUE IDENTIFICATION: What potential issues do you see? Consider:
   - Security vulnerabilities (injection, authentication, authorization)
   - Performance problems (inefficient algorithms, memory leaks)
   - Maintainability issues (code complexity, readability)
   - Bug risks (edge cases, error handling, race conditions)
   - Style violations (naming, formatting, patterns)
4. SEVERITY ASSESSMENT: How critical is each issue? What are the potential consequences?
5. SOLUTION FORMULATION: What specific, actionable improvements would you suggest?
6. CONFIDENCE EVALUATION: How confident are you in each assessment?

OUTPUT FORMAT:
- reasoning_steps: Detailed step-by-step thinking process
- issues_found: JSON array with format: [{"category": "security|performance|maintainability|bugs|style", "severity": "critical|high|medium|low", "line_start": 10, "line_end": 15, "description": "Clear issue description", "suggestion": "Specific improvement suggestion", "confidence": 0.95, "code_example": "optional better code"}]
- overall_assessment: Summary of code quality and main concerns
- confidence_score: Overall confidence in the entire analysis (0-1)

Focus on actionable, specific feedback that helps developers improve their code.
Be thorough but practical - prioritize issues that matter most.
`)

	return &EnhancedCodeReviewProcessor{
		reasoningModule: modules.NewChainOfThought(signature),
		metrics:         metrics,
		logger:          logger,
	}
}

// Process performs enhanced code review with chain-of-thought reasoning
func (p *EnhancedCodeReviewProcessor) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	// Check if enhanced processing is enabled
	if !isEnhancedProcessingEnabled() {
		p.logger.Info(ctx, "Enhanced processing disabled, falling back to legacy processor")
		return p.fallbackToLegacy(ctx, task, taskContext)
	}

	startTime := getCurrentTimeMs()
	
	// Extract task data
	fileContent, ok := task.Metadata["file_content"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid file_content in task metadata")
	}
	
	changes, ok := task.Metadata["changes"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid changes in task metadata")
	}
	
	filePath, ok := task.Metadata["file_path"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid file_path in task metadata")
	}

	// Extract context data with defaults
	guidelines := getStringFromContext(taskContext, "guidelines", "Follow Go best practices and code review standards")
	repoContext := getStringFromContext(taskContext, "repository_context", "No specific repository context available")

	p.logger.Debug(ctx, "Starting enhanced code review for %s with chain-of-thought reasoning", filePath)

	// Prepare inputs for reasoning module
	inputs := map[string]interface{}{
		"file_content": fileContent,
		"changes":      changes,
		"guidelines":   guidelines,
		"repo_context": repoContext,
		"file_path":    filePath,
	}

	// Use chain of thought reasoning
	result, err := p.reasoningModule.Process(ctx, inputs)
	if err != nil {
		p.logger.Error(ctx, "Chain-of-thought reasoning failed for %s: %v", filePath, err)
		// Fallback to legacy processing
		return p.fallbackToLegacy(ctx, task, taskContext)
	}

	// Parse and format results
	enhancedResult, err := p.parseReasoningResult(ctx, result, filePath, startTime)
	if err != nil {
		p.logger.Error(ctx, "Failed to parse reasoning result for %s: %v", filePath, err)
		return p.fallbackToLegacy(ctx, task, taskContext)
	}

	// Track metrics for enhanced processing
	p.trackEnhancedMetrics(ctx, enhancedResult, filePath)

	p.logger.Info(ctx, "Enhanced code review completed for %s: found %d issues with %.2f confidence", 
		filePath, len(enhancedResult.Issues), enhancedResult.Confidence)

	return enhancedResult, nil
}

// parseReasoningResult converts the reasoning module output into structured results
func (p *EnhancedCodeReviewProcessor) parseReasoningResult(ctx context.Context, result map[string]interface{}, filePath string, startTime float64) (*EnhancedReviewResult, error) {
	// Extract reasoning chain
	reasoningSteps, _ := result["reasoning_steps"].(string)
	
	// Extract overall assessment
	overallAssessment, _ := result["overall_assessment"].(string)
	
	// Extract confidence score
	confidenceScore := 0.8 // default
	if conf, ok := result["confidence_score"].(string); ok {
		if parsed, err := parseFloat(conf); err == nil {
			confidenceScore = parsed
		}
	}

	// Parse issues from JSON string
	issuesJSON, _ := result["issues_found"].(string)
	issues, err := p.parseIssuesFromJSON(issuesJSON, filePath)
	if err != nil {
		p.logger.Warn(ctx, "Failed to parse issues JSON: %v, using fallback parsing", err)
		issues = p.parseIssuesFromText(result, filePath)
	}

	processingTime := getCurrentTimeMs() - startTime

	return &EnhancedReviewResult{
		Issues:         issues,
		OverallQuality: overallAssessment,
		ReasoningChain: reasoningSteps,
		Confidence:     confidenceScore,
		ProcessingTime: processingTime,
	}, nil
}

// parseIssuesFromJSON attempts to parse issues from JSON format
func (p *EnhancedCodeReviewProcessor) parseIssuesFromJSON(issuesJSON, filePath string) ([]ReviewIssue, error) {
	if issuesJSON == "" {
		return []ReviewIssue{}, nil
	}

	// Simple JSON parsing - in production, use a proper JSON parser
	issues := []ReviewIssue{}
	
	// For now, implement basic parsing logic
	// This should be replaced with proper JSON unmarshaling
	lines := strings.Split(issuesJSON, "\n")
	for _, line := range lines {
		if strings.Contains(line, "category") && strings.Contains(line, "description") {
			issue := p.parseIssueFromLine(line, filePath)
			if issue != nil {
				issues = append(issues, *issue)
			}
		}
	}

	return issues, nil
}

// parseIssueFromLine parses a single issue from a text line (fallback method)
func (p *EnhancedCodeReviewProcessor) parseIssueFromLine(line, filePath string) *ReviewIssue {
	// Simplified parsing - extract key information
	category := extractBetween(line, `"category":`, `"`, `"`)
	if category == "" {
		category = "maintainability"
	}

	severity := extractBetween(line, `"severity":`, `"`, `"`)
	if severity == "" {
		severity = "medium"
	}

	description := extractBetween(line, `"description":`, `"`, `"`)
	if description == "" {
		return nil
	}

	suggestion := extractBetween(line, `"suggestion":`, `"`, `"`)

	return &ReviewIssue{
		FilePath:    filePath,
		LineRange:   LineRange{Start: 1, End: 1}, // Default range
		Category:    category,
		Severity:    severity,
		Description: description,
		Suggestion:  suggestion,
		Confidence:  0.8,
	}
}

// parseIssuesFromText fallback parsing when JSON parsing fails
func (p *EnhancedCodeReviewProcessor) parseIssuesFromText(result map[string]interface{}, filePath string) []ReviewIssue {
	// Extract any textual issue descriptions and convert to structured format
	issues := []ReviewIssue{}
	
	if reasoningSteps, ok := result["reasoning_steps"].(string); ok {
		// Look for common issue patterns in the reasoning
		if strings.Contains(strings.ToLower(reasoningSteps), "security") {
			issues = append(issues, ReviewIssue{
				FilePath:    filePath,
				LineRange:   LineRange{Start: 1, End: 1},
				Category:    "security",
				Severity:    "medium",
				Description: "Potential security concern identified during review",
				Suggestion:  "Please review for security best practices",
				Confidence:  0.6,
			})
		}
		
		if strings.Contains(strings.ToLower(reasoningSteps), "performance") {
			issues = append(issues, ReviewIssue{
				FilePath:    filePath,
				LineRange:   LineRange{Start: 1, End: 1},
				Category:    "performance",
				Severity:    "medium",
				Description: "Potential performance issue identified",
				Suggestion:  "Consider optimizing for better performance",
				Confidence:  0.6,
			})
		}
	}

	return issues
}

// fallbackToLegacy falls back to the original processor when enhanced processing fails
func (p *EnhancedCodeReviewProcessor) fallbackToLegacy(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	p.logger.Info(ctx, "Falling back to legacy processor")
	
	// Create legacy processor and delegate
	legacyProcessor := &CodeReviewProcessor{metrics: p.metrics}
	return legacyProcessor.Process(ctx, task, taskContext)
}

// trackEnhancedMetrics records metrics for enhanced processing
func (p *EnhancedCodeReviewProcessor) trackEnhancedMetrics(ctx context.Context, result *EnhancedReviewResult, filePath string) {
	if p.metrics != nil {
		// Basic metrics tracking - extend MetricsCollector interface as needed
		// p.metrics.TrackProcessingTime(ctx, "enhanced_review", result.ProcessingTime)
		
		// Track issue counts by category
		categoryCount := make(map[string]int)
		for _, issue := range result.Issues {
			categoryCount[issue.Category]++
		}
		
		// for category, count := range categoryCount {
		//     p.metrics.TrackIssueCount(ctx, category, count)
		// }
		
		// p.metrics.TrackConfidenceScore(ctx, result.Confidence)
		
		// Use existing metrics method
		for _, issue := range result.Issues {
			comment := PRReviewComment{
				FilePath:   issue.FilePath,
				LineNumber: issue.LineRange.Start,
				Content:    issue.Description,
				Category:   issue.Category,
				Severity:   issue.Severity,
				Suggestion: issue.Suggestion,
			}
			p.metrics.TrackReviewComment(ctx, comment, true) // true for enhanced
		}
	}
}

// Helper functions

func isEnhancedProcessingEnabled() bool {
	return getEnvBool("MAESTRO_ENHANCED_REASONING", true)
}

func getStringFromContext(context map[string]interface{}, key, defaultValue string) string {
	if value, ok := context[key].(string); ok {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1"
	}
	return defaultValue
}

func getCurrentTimeMs() float64 {
	return float64(time.Now().UnixNano()) / 1e6
}

func parseFloat(s string) (float64, error) {
	// Simple float parsing
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return f, nil
	}
	return 0.0, fmt.Errorf("invalid float: %s", s)
}

func extractBetween(text, start, end1, end2 string) string {
	startIdx := strings.Index(text, start)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(start)
	
	// Try first end delimiter
	endIdx := strings.Index(text[startIdx:], end1)
	if endIdx == -1 && end2 != "" {
		// Try second end delimiter
		endIdx = strings.Index(text[startIdx:], end2)
	}
	
	if endIdx == -1 {
		return ""
	}
	
	return strings.TrimSpace(text[startIdx : startIdx+endIdx])
}