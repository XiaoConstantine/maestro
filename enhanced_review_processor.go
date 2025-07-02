package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
)

// Global module cache to prevent redundant module creation
var (
	moduleCache    = sync.Map{}
	moduleCacheMux = sync.RWMutex{}
)

// ModuleCacheEntry holds cached module instances
type ModuleCacheEntry struct {
	BasePredict *modules.Predict
	//	RefinementModule  *modules.Refine
	ParallelProcessor *modules.Parallel
	ConsensusModule   *modules.MultiChainComparison
	CreatedAt         time.Time
}

// EnhancedCodeReviewProcessor implements optimized reasoning for code review with consensus and refinement
type EnhancedCodeReviewProcessor struct {
	parallelProcessor *modules.Parallel
	refinementModule  *modules.Refine
	consensusModule   *modules.MultiChainComparison
	metrics           MetricsCollector
	logger            *logging.Logger
}

// ReviewIssue represents a code issue identified through reasoning
type ReviewIssue struct {
	FilePath    string    `json:"file_path"`
	LineRange   LineRange `json:"line_range"`
	Category    string    `json:"category"`
	Severity    string    `json:"severity"`
	Description string    `json:"description"`
	Reasoning   string    `json:"reasoning"`
	Suggestion  string    `json:"suggestion"`
	Confidence  float64   `json:"confidence"`
	CodeExample string    `json:"code_example,omitempty"`
}

// EnhancedReviewResult contains the output of enhanced reasoning
type EnhancedReviewResult struct {
	Issues         []ReviewIssue `json:"issues"`
	OverallQuality string        `json:"overall_quality"`
	ReasoningChain string        `json:"reasoning_chain"`
	Confidence     float64       `json:"confidence"`
	ProcessingTime float64       `json:"processing_time_ms"`
}

// hashSignature creates a unique hash for a signature based on its structure
func hashSignature(sig core.Signature) string {
	hasher := md5.New()

	// Hash input fields
	for _, input := range sig.Inputs {
		hasher.Write([]byte(input.Name + ":" + input.Description))
	}

	// Hash output fields
	for _, output := range sig.Outputs {
		hasher.Write([]byte(output.Name + ":" + output.Description))
	}

	// Hash instruction
	hasher.Write([]byte(sig.Instruction))

	return hex.EncodeToString(hasher.Sum(nil))
}

// getOrCreateModules retrieves cached modules or creates new ones if not found
func getOrCreateModules(signature core.Signature) *ModuleCacheEntry {
	signatureHash := hashSignature(signature)

	// Try to get from cache first
	if cached, ok := moduleCache.Load(signatureHash); ok {
		if entry, ok := cached.(*ModuleCacheEntry); ok {
			// Check if cache entry is not too old (optional: cache expiration)
			if time.Since(entry.CreatedAt) < 24*time.Hour {
				// Cache HIT - reusing existing modules
				return entry
			}
			// Remove stale entry
			moduleCache.Delete(signatureHash)
		}
	}

	// Create new modules with cache protection
	moduleCacheMux.Lock()
	defer moduleCacheMux.Unlock()

	// Double-check pattern: another goroutine might have created it
	if cached, ok := moduleCache.Load(signatureHash); ok {
		if entry, ok := cached.(*ModuleCacheEntry); ok {
			return entry
		}
	}

	// Create new module instances with descriptive names for better tracing
	basePredict := modules.NewPredict(signature).WithName("CodeReviewPredict")

	// Skip creating refinement module here - will be created fresh in constructor

	// Create parallel processor for concurrent chunk processing (use basePredict to avoid recursion)
	parallelProcessor := modules.NewParallel(
		basePredict,
		modules.WithMaxWorkers(4),
		modules.WithReturnFailures(true),
	).WithName("ParallelCodeReview")

	// Create consensus module for critical reviews
	consensusModule := modules.NewMultiChainComparison(signature, 3, 0.7).WithName("ConsensusReview")

	// Cache the new entry
	entry := &ModuleCacheEntry{
		BasePredict:       basePredict,
		ParallelProcessor: parallelProcessor,
		ConsensusModule:   consensusModule,
		CreatedAt:         time.Now(),
	}

	moduleCache.Store(signatureHash, entry)
	return entry
}

// NewEnhancedCodeReviewProcessor creates an optimized processor using cached DSPy modules
func NewEnhancedCodeReviewProcessor(metrics MetricsCollector, logger *logging.Logger) *EnhancedCodeReviewProcessor {
	// Create signature for direct code review (using Predict for fast, deterministic analysis)
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "file_content", Description: "The source code to review"}},
			{Field: core.Field{Name: "changes", Description: "The specific changes made to the code"}},
			{Field: core.Field{Name: "guidelines", Description: "Coding guidelines and standards"}},
			{Field: core.Field{Name: "repo_context", Description: "Repository context and patterns"}},
			{Field: core.Field{Name: "file_path", Description: "Path of the file being reviewed"}},
			{Field: core.Field{Name: "file_type_context", Description: "File type specific review context and focus areas"}},
		},
		[]core.OutputField{
			{Field: core.NewField("rationale")},
			{Field: core.NewField("overall_assessment")},
			{Field: core.NewField("confidence_score")},
		},
	).WithInstruction(`
You are an expert code reviewer. Your goal is to provide HIGH-VALUE, ACTIONABLE feedback that developers will find genuinely useful. AVOID generic comments that waste time.

CRITICAL INSTRUCTIONS:
1. ONLY flag issues you can identify with SPECIFIC evidence in the code
2. PROVIDE EXACT LINE NUMBERS and CODE EXAMPLES for every issue
3. For test files: Focus on test quality, not production performance optimizations
4. For production code: Focus on real bugs, security vulnerabilities, and maintainability
5. If you cannot find specific, actionable issues, return an empty array []

ANALYSIS FRAMEWORK:
1. UNDERSTANDING: Analyze what this code does and its context
2. CHANGE ANALYSIS: Examine specific changes and their impact
3. EVIDENCE-BASED ISSUE IDENTIFICATION: Only flag issues with concrete evidence
4. SEVERITY ASSESSMENT: Evaluate actual impact (not theoretical)
5. SPECIFIC SOLUTIONS: Provide exact code changes or clear action items

REVIEW RULES (with EVIDENCE requirements):

SECURITY CHECKS (must provide exact vulnerable code):
- SQL injection: Show specific query with unescaped user input
- Hardcoded secrets: Point to actual secrets (not test data)
- Auth bypass: Identify missing authentication on sensitive operations
- Path traversal: Show user-controlled file paths without validation
- XSS: Identify unescaped user input in web output

PERFORMANCE ANALYSIS (must show specific inefficiencies):
- Algorithm complexity: Show O(n²) loops that could be O(n)
- Memory issues: Point to specific allocation patterns
- Blocking operations: Identify synchronous calls that should be async
- Database inefficiency: Show N+1 queries or missing indexes
- Resource leaks: Point to unclosed files/connections

BUG PREVENTION (must identify specific risks):
- Null dereference: Show where nil checks are missing
- Bounds violations: Point to unsafe array/slice access
- Race conditions: Identify shared state without proper synchronization
- Error handling: Show where errors are ignored or mishandled

MAINTAINABILITY (must show specific examples):
- Complex functions: Count actual lines and show complexity
- Duplicate code: Point to multiple identical code blocks
- Naming issues: Show confusing variable/function names
- Missing docs: Only for public APIs that need explanation

FILE TYPE SPECIFIC RULES:
- TEST FILES: Focus on test completeness, edge cases, and maintainability
- PRODUCTION FILES: Focus on correctness, security, and performance
- CONFIGURATION FILES: Focus on security and correctness

OUTPUT REQUIREMENTS:
- rationale: Provide evidence-based analysis. End with JSON array of issues or [] if none found
- overall_assessment: Summarize actual findings (not theoretical concerns)
- confidence_score: 0.9+ for concrete issues, <0.7 for speculative concerns

IMPORTANT: Better to return NO issues than generic, unhelpful comments. Quality over quantity.
`)

	// Get or create cached modules for this signature
	cachedModules := getOrCreateModules(signature)

	// Using cached DSPy modules

	// Create fresh refinement module to avoid recursion
	refinementConfig := modules.RefineConfig{
		N:         3,
		Threshold: 0.6,
		RewardFn:  codeReviewQualityReward,
	}
	refinementModule := modules.NewRefine(cachedModules.BasePredict, refinementConfig).WithName("CodeReviewRefiner")

	return &EnhancedCodeReviewProcessor{
		parallelProcessor: cachedModules.ParallelProcessor,
		refinementModule:  refinementModule,
		consensusModule:   cachedModules.ConsensusModule,
		metrics:           metrics,
		logger:            logger,
	}
}

// codeReviewQualityReward evaluates the quality of a code review result with generous scoring
func codeReviewQualityReward(inputs map[string]interface{}, outputs map[string]interface{}) float64 {
	// Lower base score, more room for rewards
	qualityScore := 0.3
	scoreBreakdown := []string{"base: 0.3"}

	// Get file path for logging context
	_ = "unknown" // filePath for future logging use

	// Check if rationale contains structured JSON issues (from outputs)
	rationale, hasRationale := outputs["rationale"].(string)
	if hasRationale && rationale != "" {
		// More generous content reward
		qualityScore += 0.3
		scoreBreakdown = append(scoreBreakdown, fmt.Sprintf("content: +0.3 (%d chars)", len(rationale)))

		// Higher JSON format reward
		hasOpenBracket := strings.Contains(rationale, "[")
		hasCloseBracket := strings.Contains(rationale, "]")
		if hasOpenBracket && hasCloseBracket {
			qualityScore += 0.3
			scoreBreakdown = append(scoreBreakdown, "json_format: +0.3")
		} else {
			scoreBreakdown = append(scoreBreakdown, fmt.Sprintf("json_format: +0.0 (missing brackets: open=%t, close=%t)", hasOpenBracket, hasCloseBracket))
		}

		// Better category reward
		hasSecurity := strings.Contains(rationale, "security")
		hasPerformance := strings.Contains(rationale, "performance")
		if hasSecurity || hasPerformance {
			qualityScore += 0.2
			scoreBreakdown = append(scoreBreakdown, fmt.Sprintf("categories: +0.2 (security=%t, performance=%t)", hasSecurity, hasPerformance))
		} else {
			scoreBreakdown = append(scoreBreakdown, "categories: +0.0 (no security/performance)")
		}

		// Additional reward for maintainability/bugs categories
		hasMaintainability := strings.Contains(rationale, "maintainability")
		hasBugs := strings.Contains(rationale, "bugs")
		if hasMaintainability || hasBugs {
			qualityScore += 0.1
			scoreBreakdown = append(scoreBreakdown, fmt.Sprintf("additional: +0.1 (maintainability=%t, bugs=%t)", hasMaintainability, hasBugs))
		} else {
			scoreBreakdown = append(scoreBreakdown, "additional: +0.0 (no maintainability/bugs)")
		}
	} else {
		scoreBreakdown = append(scoreBreakdown, "content: +0.0 (empty rationale)")
	}

	// Check confidence score (from outputs) with lower threshold
	if confidenceStr, ok := outputs["confidence_score"].(string); ok {
		if confidence, err := parseFloat(confidenceStr); err == nil && confidence > 0.5 {
			qualityScore += 0.1
			scoreBreakdown = append(scoreBreakdown, fmt.Sprintf("confidence: +0.1 (%.2f)", confidence))
		} else {
			scoreBreakdown = append(scoreBreakdown, fmt.Sprintf("confidence: +0.0 (%.2f or parse error)", confidence))
		}
	} else {
		scoreBreakdown = append(scoreBreakdown, "confidence: +0.0 (missing)")
	}

	// Cap at 1.0 but allow higher intermediate scores
	finalScore := qualityScore
	if qualityScore > 1.0 {
		finalScore = 1.0
		scoreBreakdown = append(scoreBreakdown, "capped to 1.0")
	}

	// Note: Reward calculation complete

	return finalScore
}

// GetModuleCacheStats returns statistics about the module cache
func GetModuleCacheStats() map[string]interface{} {
	stats := make(map[string]interface{})

	cacheSize := 0
	oldestEntry := time.Now()
	newestEntry := time.Time{}

	moduleCache.Range(func(key, value interface{}) bool {
		cacheSize++
		if entry, ok := value.(*ModuleCacheEntry); ok {
			if entry.CreatedAt.Before(oldestEntry) {
				oldestEntry = entry.CreatedAt
			}
			if entry.CreatedAt.After(newestEntry) {
				newestEntry = entry.CreatedAt
			}
		}
		return true
	})

	stats["cache_size"] = cacheSize
	stats["oldest_entry_age_minutes"] = time.Since(oldestEntry).Minutes()
	stats["newest_entry_age_minutes"] = time.Since(newestEntry).Minutes()

	return stats
}

// ClearModuleCache clears all cached modules (useful for testing or memory management)
func ClearModuleCache() {
	moduleCache.Range(func(key, value interface{}) bool {
		moduleCache.Delete(key)
		return true
	})
}

// getFileTypeContext analyzes the file path to provide context-specific review guidelines
func (p *EnhancedCodeReviewProcessor) getFileTypeContext(filePath string) string {
	if strings.Contains(filePath, "_test.go") || strings.Contains(filePath, "/test/") {
		return "TEST_FILE: Focus on test quality, coverage, edge cases, and maintainability. Do NOT suggest performance optimizations like caching in tests unless they're actually needed for test execution."
	}
	if strings.Contains(filePath, "main.go") || strings.Contains(filePath, "/cmd/") {
		return "MAIN_FILE: Focus on error handling, configuration validation, and startup logic correctness."
	}
	if strings.Contains(filePath, "config") || strings.Contains(filePath, "settings") {
		return "CONFIG_FILE: Focus on security (no hardcoded secrets), validation, and proper defaults."
	}
	if strings.Contains(filePath, "/api/") || strings.Contains(filePath, "/handler/") {
		return "API_FILE: Focus on input validation, authentication, authorization, and error responses."
	}
	if strings.Contains(filePath, "/db/") || strings.Contains(filePath, "database") {
		return "DATABASE_FILE: Focus on SQL injection prevention, connection management, and transaction handling."
	}
	return "PRODUCTION_FILE: Focus on correctness, security vulnerabilities, error handling, and maintainability."
}

// Process performs optimized code review using Parallel, Refine, and consensus modules
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

	// Extract context data with defaults and add file-type specific context
	guidelines := getStringFromContext(taskContext, "guidelines", "Follow Go best practices and code review standards")
	repoContext := getStringFromContext(taskContext, "repository_context", "No specific repository context available")
	fileTypeContext := p.getFileTypeContext(filePath)

	// Starting optimized code review

	// Prepare inputs for reasoning module
	inputs := map[string]interface{}{
		"file_content":     fileContent,
		"changes":          changes,
		"guidelines":       guidelines,
		"repo_context":     repoContext,
		"file_path":        filePath,
		"file_type_context": fileTypeContext,
	}

	// Use optimized parallel processing with refinement
	result, err := p.refinementModule.Process(ctx, inputs)
	if err != nil {
		p.logger.Error(ctx, "Optimized reasoning with refinement failed for %s: %v", filePath, err)
		// Fallback to legacy processing
		return p.fallbackToLegacy(ctx, task, taskContext)
	}

	// Raw LLM response received

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

// ProcessMultipleChunks processes multiple code chunks in parallel with consensus for critical issues
func (p *EnhancedCodeReviewProcessor) ProcessMultipleChunks(ctx context.Context, tasks []agents.Task, taskContext map[string]interface{}) ([]interface{}, error) {
	if !isEnhancedProcessingEnabled() {
		p.logger.Info(ctx, "Enhanced processing disabled, falling back to legacy processing")
		// Process sequentially with legacy processor
		results := make([]interface{}, len(tasks))
		for i, task := range tasks {
			result, err := p.fallbackToLegacy(ctx, task, taskContext)
			if err != nil {
				return nil, fmt.Errorf("legacy processing failed for task %d: %w", i, err)
			}
			results[i] = result
		}
		return results, nil
	}

	// Processing chunks in parallel

	// Prepare inputs for all tasks
	inputsBatch := make([]map[string]interface{}, len(tasks))
	for i, task := range tasks {
		fileContent, _ := task.Metadata["file_content"].(string)
		changes, _ := task.Metadata["changes"].(string)
		filePath, _ := task.Metadata["file_path"].(string)

		guidelines := getStringFromContext(taskContext, "guidelines", "Follow Go best practices and code review standards")
		repoContext := getStringFromContext(taskContext, "repository_context", "No specific repository context available")
		fileTypeContext := p.getFileTypeContext(filePath)

		inputsBatch[i] = map[string]interface{}{
			"file_content":     fileContent,
			"changes":          changes,
			"guidelines":       guidelines,
			"repo_context":     repoContext,
			"file_path":        filePath,
			"file_type_context": fileTypeContext,
		}
	}

	startTime := getCurrentTimeMs()

	// Process all chunks in parallel using batch_inputs format
	batchInput := map[string]interface{}{
		"batch_inputs": inputsBatch,
	}

	result, err := p.parallelProcessor.Process(ctx, batchInput)
	if err != nil {
		p.logger.Error(ctx, "Parallel processing failed: %v", err)
		return nil, fmt.Errorf("parallel processing failed: %w", err)
	}

	// Extract results from parallel processing output
	var results []map[string]interface{}
	if resultsInterface, ok := result["results"]; ok {
		if resultSlice, ok := resultsInterface.([]map[string]interface{}); ok {
			results = resultSlice
		} else if resultSliceInterface, ok := resultsInterface.([]interface{}); ok {
			// Convert []interface{} to []map[string]interface{}
			results = make([]map[string]interface{}, len(resultSliceInterface))
			for i, r := range resultSliceInterface {
				if rm, ok := r.(map[string]interface{}); ok {
					results[i] = rm
				}
			}
		}
	}

	// Convert raw results to EnhancedReviewResult format
	enhancedResults := make([]interface{}, len(results))
	for i, resultMap := range results {
		filePath := ""
		if i < len(tasks) {
			filePath, _ = tasks[i].Metadata["file_path"].(string)
		}

		enhancedResult, err := p.parseReasoningResult(ctx, resultMap, filePath, startTime)
		if err != nil {
			p.logger.Error(ctx, "Failed to parse result for chunk %d: %v", i, err)
			continue
		}
		enhancedResults[i] = enhancedResult

		// Track metrics
		p.trackEnhancedMetrics(ctx, enhancedResult, filePath)
	}

	processingTime := getCurrentTimeMs() - startTime
	p.logger.Info(ctx, "Parallel processing completed for %d chunks in %.2f ms", len(tasks), processingTime)

	return enhancedResults, nil
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

	// Extract rationale which contains reasoning + JSON array
	rationale, _ := result["rationale"].(string)
	// Rationale extracted

	// Extract JSON array from the end of rationale
	issues := p.extractIssuesFromRationale(rationale, filePath)

	// Issues parsed from response

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

	// Try to parse as proper JSON first
	var rawIssues []map[string]interface{}
	if err := json.Unmarshal([]byte(issuesJSON), &rawIssues); err != nil {
		// If JSON parsing fails, try to extract JSON from the text
		return p.parseIssuesFromText(issuesJSON, filePath), nil
	}

	issues := []ReviewIssue{}
	for _, rawIssue := range rawIssues {
		issue := p.convertMapToIssue(rawIssue, filePath)
		if issue != nil && p.isValidIssue(issue) {
			issues = append(issues, *issue)
		}
	}

	return issues, nil
}

// isValidIssue checks if an issue provides meaningful, actionable feedback
func (p *EnhancedCodeReviewProcessor) isValidIssue(issue *ReviewIssue) bool {
	if issue == nil {
		return false
	}

	// Filter out generic descriptions that provide no value
	genericDescriptions := []string{
		"potential security concern identified during review",
		"potential performance issue identified",
		"please review for security best practices",
		"consider optimizing for better performance",
		"review the code for potential issues",
		"this section may have issues",
		"consider reviewing this code",
	}

	descLower := strings.ToLower(issue.Description)
	for _, generic := range genericDescriptions {
		if strings.Contains(descLower, generic) {
			return false
		}
	}

	// Filter out generic suggestions
	genericSuggestions := []string{
		"please review for security best practices",
		"consider optimizing for better performance",
		"review the implementation",
		"consider refactoring",
		"add proper error handling",
	}

	suggLower := strings.ToLower(issue.Suggestion)
	for _, generic := range genericSuggestions {
		if suggLower == generic {
			return false
		}
	}

	// Require minimum confidence for generic categories
	if (issue.Category == "security" || issue.Category == "performance") && issue.Confidence < 0.8 {
		return false
	}

	// Require specific description (not too short)
	if len(issue.Description) < 20 {
		return false
	}

	return true
}

// convertMapToIssue converts a map to a ReviewIssue struct
func (p *EnhancedCodeReviewProcessor) convertMapToIssue(rawIssue map[string]interface{}, filePath string) *ReviewIssue {
	// Extract required fields with defaults
	category, _ := rawIssue["category"].(string)
	if category == "" {
		category = "maintainability"
	}

	severity, _ := rawIssue["severity"].(string)
	if severity == "" {
		severity = "medium"
	}

	description, _ := rawIssue["description"].(string)
	if description == "" {
		return nil // Skip issues without description
	}

	suggestion, _ := rawIssue["suggestion"].(string)
	codeExample, _ := rawIssue["code_example"].(string)

	// Parse line range
	lineStart := 1
	lineEnd := 1
	if lineStartRaw, ok := rawIssue["line_start"]; ok {
		if ls, ok := lineStartRaw.(float64); ok {
			lineStart = int(ls)
		}
	}
	if lineEndRaw, ok := rawIssue["line_end"]; ok {
		if le, ok := lineEndRaw.(float64); ok {
			lineEnd = int(le)
		}
	}

	// Parse confidence
	confidence := 0.8
	if confRaw, ok := rawIssue["confidence"]; ok {
		if conf, ok := confRaw.(float64); ok {
			confidence = conf
		}
	}

	return &ReviewIssue{
		FilePath:    filePath,
		LineRange:   LineRange{Start: lineStart, End: lineEnd},
		Category:    category,
		Severity:    severity,
		Description: description,
		Suggestion:  suggestion,
		Confidence:  confidence,
		CodeExample: codeExample,
	}
}

// extractIssuesFromRationale extracts JSON array from rationale text
func (p *EnhancedCodeReviewProcessor) extractIssuesFromRationale(rationale, filePath string) []ReviewIssue {
	if rationale == "" {
		return []ReviewIssue{}
	}

	// Look for JSON array at the end of rationale
	// Find the last occurrence of '[' which should start the JSON array
	lastBracket := strings.LastIndex(rationale, "[")
	if lastBracket == -1 {
		// No JSON array found, try text parsing
		return p.parseIssuesFromText(rationale, filePath)
	}

	// Extract potential JSON from last bracket to end
	jsonCandidate := rationale[lastBracket:]

	// Try to parse as JSON
	issues, err := p.parseIssuesFromJSON(jsonCandidate, filePath)
	if err != nil {
		// Fallback to text parsing
		return p.parseIssuesFromText(rationale, filePath)
	}

	return issues
}

// parseIssuesFromText fallback parsing when JSON parsing fails
func (p *EnhancedCodeReviewProcessor) parseIssuesFromText(issuesText, filePath string) []ReviewIssue {
	issues := []ReviewIssue{}

	// Try to extract JSON-like patterns from the text
	lines := strings.Split(issuesText, "\n")
	for _, line := range lines {
		if strings.Contains(line, "category") && strings.Contains(line, "description") {
			issue := p.parseIssueFromLine(line, filePath)
			if issue != nil && p.isValidIssue(issue) {
				issues = append(issues, *issue)
			}
			}
	}

	// REMOVED: Generic fallback comments - they provide no value
	// Instead, return empty slice if no specific issues found
	// This forces the LLM to be more specific or return no issues

	return issues
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
