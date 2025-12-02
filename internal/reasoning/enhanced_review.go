// Package reasoning provides LLM-based reasoning capabilities for code review.
package reasoning

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
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
)

// Global module cache to prevent redundant module creation.
var (
	moduleCache    = sync.Map{}
	moduleCacheMux = sync.RWMutex{}
)

// ModuleCacheEntry holds cached module instances.
type ModuleCacheEntry struct {
	BasePredict       *modules.Predict
	ParallelProcessor *modules.Parallel
	ConsensusModule   *modules.MultiChainComparison
	CreatedAt         time.Time
}

// EnhancedCodeReviewProcessor implements optimized reasoning for code review.
type EnhancedCodeReviewProcessor struct {
	parallelProcessor *modules.Parallel
	refinementModule  *modules.Refine
	consensusModule   *modules.MultiChainComparison
	metrics           types.MetricsCollector
	logger            *logging.Logger
}

// hashSignature creates a unique hash for a signature based on its structure.
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

// getParallelWorkers returns the number of parallel workers to use for chunk processing.
func getParallelWorkers() int {
	if envWorkers := os.Getenv("MAESTRO_PARALLEL_WORKERS"); envWorkers != "" {
		if workers, err := strconv.Atoi(envWorkers); err == nil && workers > 0 {
			return workers
		}
	}
	// Default to 120 workers for I/O-bound LLM API calls.
	return 120
}

// getOrCreateModules retrieves cached modules or creates new ones if not found.
func getOrCreateModules(signature core.Signature) *ModuleCacheEntry {
	logger := logging.GetLogger()
	signatureHash := hashSignature(signature)
	maxWorkers := getParallelWorkers()
	llm := core.GetDefaultLLM()

	logger.Info(context.Background(), "🔧 getOrCreateModules called, llm=%v, maxWorkers=%d", llm != nil, maxWorkers)

	// Try to get from cache first
	if cached, ok := moduleCache.Load(signatureHash); ok {
		if entry, ok := cached.(*ModuleCacheEntry); ok {
			// Check if cache is valid: not expired AND has LLM configured
			// We check LLM field directly since there's no GetLLM method
			cachedLLM := entry.BasePredict.LLM
			logger.Info(context.Background(), "🔧 Found cached entry, cachedLLM=%v, age=%v", cachedLLM != nil, time.Since(entry.CreatedAt))
			if time.Since(entry.CreatedAt) < 24*time.Hour && cachedLLM != nil {
				return entry
			}
			// Invalidate cache if expired or LLM not set
			logger.Info(context.Background(), "🔧 Invalidating cache: expired=%v, llmNil=%v", time.Since(entry.CreatedAt) >= 24*time.Hour, cachedLLM == nil)
			moduleCache.Delete(signatureHash)
		}
	}

	// Create new modules with cache protection
	moduleCacheMux.Lock()
	defer moduleCacheMux.Unlock()

	// Double-check pattern - also verify LLM is set
	if cached, ok := moduleCache.Load(signatureHash); ok {
		if entry, ok := cached.(*ModuleCacheEntry); ok {
			if entry.BasePredict.LLM != nil {
				return entry
			}
			moduleCache.Delete(signatureHash)
		}
	}

	// Create new module instances with LLM
	logger.Info(context.Background(), "🔧 Creating NEW modules with llm=%v", llm != nil)
	basePredict := modules.NewPredict(signature).WithName("CodeReviewPredict")
	basePredict.SetLLM(llm)
	logger.Info(context.Background(), "🔧 After SetLLM, basePredict.LLM=%v", basePredict.LLM != nil)

	parallelProcessor := modules.NewParallel(
		basePredict,
		modules.WithMaxWorkers(maxWorkers),
		modules.WithReturnFailures(true),
	).WithName("ParallelCodeReview")
	parallelProcessor.SetLLM(llm)

	consensusModule := modules.NewMultiChainComparison(signature, 3, 0.7).WithName("ConsensusReview")
	consensusModule.SetLLM(llm)

	entry := &ModuleCacheEntry{
		BasePredict:       basePredict,
		ParallelProcessor: parallelProcessor,
		ConsensusModule:   consensusModule,
		CreatedAt:         time.Now(),
	}

	moduleCache.Store(signatureHash, entry)
	logger.Info(context.Background(), "🔧 Stored new entry in cache")
	return entry
}

// NewEnhancedCodeReviewProcessor creates an optimized processor using cached DSPy modules.
func NewEnhancedCodeReviewProcessor(metrics types.MetricsCollector, logger *logging.Logger) *EnhancedCodeReviewProcessor {
	signature := createCodeReviewSignature()
	cachedModules := getOrCreateModules(signature)

	refinementConfig := modules.RefineConfig{
		N:         3,
		Threshold: 0.6,
		RewardFn:  codeReviewQualityReward,
	}
	refinementModule := modules.NewRefine(cachedModules.BasePredict, refinementConfig).WithName("CodeReviewRefiner")
	refinementModule.SetLLM(core.GetDefaultLLM())

	return &EnhancedCodeReviewProcessor{
		parallelProcessor: cachedModules.ParallelProcessor,
		refinementModule:  refinementModule,
		consensusModule:   cachedModules.ConsensusModule,
		metrics:           metrics,
		logger:            logger,
	}
}

// createCodeReviewSignature creates the signature for code review.
// Includes core fields only (no package_name/imports for A/B testing).
func createCodeReviewSignature() core.Signature {
	return core.NewSignature(
		[]core.InputField{
			// Core required fields
			{Field: core.Field{Name: "file_content", Description: "The source code to review"}},
			{Field: core.Field{Name: "changes", Description: "The specific changes made to the code"}},
			{Field: core.Field{Name: "guidelines", Description: "Coding guidelines and standards"}},
			{Field: core.Field{Name: "repo_context", Description: "Repository context and patterns"}},
			{Field: core.Field{Name: "file_path", Description: "Path of the file being reviewed"}},
			{Field: core.Field{Name: "file_type_context", Description: "File type specific review context and focus areas"}},
			{Field: core.Field{Name: "leading_context", Description: "Code lines before the chunk for context"}},
			{Field: core.Field{Name: "trailing_context", Description: "Code lines after the chunk for context"}},
		},
		[]core.OutputField{
			{Field: core.NewField("rationale")},
			{Field: core.NewField("overall_assessment")},
			{Field: core.NewField("confidence_score")},
		},
	).WithInstruction(codeReviewInstruction)
}

// codeReviewInstruction contains the detailed instructions for the code review LLM.
const codeReviewInstruction = `
You are an expert code reviewer. Your goal is to provide HIGH-VALUE, ACTIONABLE feedback that developers will find genuinely useful. AVOID generic comments that waste time.

CONTEXT AVAILABLE:
- file_path: Path of the file being reviewed
- file_content: The source code chunk to review
- changes: The specific diff/changes made to the code
- guidelines: Coding guidelines and standards to apply
- leading_context: Code lines before this chunk for context
- trailing_context: Code lines after this chunk for context

OUTPUT REQUIREMENTS:
- rationale: Provide evidence-based analysis. End with a JSON array of issues in this format:
  [{"type": "bug|security|performance|style", "severity": "critical|high|medium|low", "line": <line_number>, "description": "<issue description>", "suggestion": "<how to fix>"}]
  Return [] if no issues found.
- overall_assessment: Summarize actual findings (not theoretical concerns)
- confidence_score: 0.9+ for concrete issues, <0.7 for speculative concerns

IMPORTANT: Better to return NO issues than generic, unhelpful comments. Quality over quantity.
`

// codeReviewQualityReward evaluates the quality of a code review result.
func codeReviewQualityReward(inputs map[string]interface{}, outputs map[string]interface{}) float64 {
	qualityScore := 0.3

	rationale, hasRationale := outputs["rationale"].(string)
	if hasRationale && rationale != "" {
		qualityScore += 0.3

		if strings.Contains(rationale, "[") && strings.Contains(rationale, "]") {
			qualityScore += 0.3
		}

		if strings.Contains(rationale, "security") || strings.Contains(rationale, "performance") {
			qualityScore += 0.2
		}
	}

	// Cap at 1.0
	if qualityScore > 1.0 {
		qualityScore = 1.0
	}

	return qualityScore
}

// Process performs enhanced code review processing.
func (p *EnhancedCodeReviewProcessor) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	startTime := util.GetCurrentTimeMs()

	// Extract review metadata from task
	metadata, err := extractReviewMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	p.logger.Debug(ctx, "Starting enhanced review for %s", metadata.FilePath)

	// Prepare inputs for review
	inputs := p.prepareReviewInputs(metadata, taskContext)

	// Process with parallel processor
	result, err := p.parallelProcessor.Process(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("parallel processing failed: %w", err)
	}

	// Parse results
	issues, err := p.parseReviewResults(result, metadata)
	if err != nil {
		p.logger.Warn(ctx, "Failed to parse review results: %v", err)
		issues = []types.ReviewIssue{}
	}

	processingTime := util.GetCurrentTimeMs() - startTime

	enhancedResult := &types.EnhancedReviewResult{
		Issues:         issues,
		OverallQuality: determineOverallQuality(issues),
		ReasoningChain: "Enhanced reasoning with parallel processing",
		Confidence:     calculateConfidence(issues),
		ProcessingTime: processingTime,
		FilePath:       metadata.FilePath,
	}

	p.logger.Debug(ctx, "Enhanced review completed: %d issues found in %.2fms",
		len(issues), processingTime)

	return enhancedResult, nil
}

// ReviewMetadata holds metadata for the review task.
type ReviewMetadata struct {
	FilePath    string
	FileContent string
	Changes     string
	Guidelines  string
	RepoContext string
}

// extractReviewMetadata extracts review metadata from task metadata.
func extractReviewMetadata(metadata map[string]interface{}) (*ReviewMetadata, error) {
	rm := &ReviewMetadata{}

	if filePath, ok := metadata["file_path"].(string); ok {
		rm.FilePath = filePath
	} else {
		return nil, fmt.Errorf("missing file_path in metadata")
	}

	if content, ok := metadata["file_content"].(string); ok {
		rm.FileContent = content
	}

	if changes, ok := metadata["changes"].(string); ok {
		rm.Changes = changes
	}

	if guidelines, ok := metadata["guidelines"].(string); ok {
		rm.Guidelines = guidelines
	}

	if repoContext, ok := metadata["repo_context"].(string); ok {
		rm.RepoContext = repoContext
	}

	return rm, nil
}

// prepareReviewInputs prepares inputs for the review processor.
func (p *EnhancedCodeReviewProcessor) prepareReviewInputs(metadata *ReviewMetadata, taskContext map[string]interface{}) map[string]interface{} {
	inputs := map[string]interface{}{
		"file_content": metadata.FileContent,
		"changes":      metadata.Changes,
		"guidelines":   metadata.Guidelines,
		"repo_context": metadata.RepoContext,
		"file_path":    metadata.FilePath,
	}

	// Add any additional context from taskContext
	for key, value := range taskContext {
		if _, exists := inputs[key]; !exists {
			inputs[key] = value
		}
	}

	return inputs
}

// parseReviewResults parses the review results into ReviewIssue slice.
func (p *EnhancedCodeReviewProcessor) parseReviewResults(result map[string]interface{}, metadata *ReviewMetadata) ([]types.ReviewIssue, error) {
	var issues []types.ReviewIssue

	rationale, ok := result["rationale"].(string)
	if !ok {
		return issues, nil
	}

	// Try to extract JSON array from rationale
	jsonStart := strings.Index(rationale, "[")
	jsonEnd := strings.LastIndex(rationale, "]")

	if jsonStart >= 0 && jsonEnd > jsonStart {
		jsonStr := rationale[jsonStart : jsonEnd+1]

		var rawIssues []map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &rawIssues); err == nil {
			for _, raw := range rawIssues {
				issue := parseRawIssue(raw, metadata.FilePath)
				if issue != nil {
					issues = append(issues, *issue)
				}
			}
		}
	}

	return issues, nil
}

// parseRawIssue parses a raw issue map into a ReviewIssue.
func parseRawIssue(raw map[string]interface{}, defaultFilePath string) *types.ReviewIssue {
	issue := &types.ReviewIssue{
		FilePath: defaultFilePath,
	}

	if filePath, ok := raw["file_path"].(string); ok && filePath != "" {
		issue.FilePath = filePath
	}

	// Handle both "line_number" and "line" keys
	if lineNumber, ok := raw["line_number"].(float64); ok {
		issue.LineRange = types.LineRange{Start: int(lineNumber), End: int(lineNumber)}
	} else if line, ok := raw["line"].(float64); ok {
		issue.LineRange = types.LineRange{Start: int(line), End: int(line)}
	}

	// Handle both "category" and "type" keys
	if category, ok := raw["category"].(string); ok {
		issue.Category = category
	} else if issueType, ok := raw["type"].(string); ok {
		issue.Category = issueType
	}

	if severity, ok := raw["severity"].(string); ok {
		issue.Severity = severity
	}

	if description, ok := raw["description"].(string); ok {
		issue.Description = description
	}

	if reasoning, ok := raw["reasoning"].(string); ok {
		issue.Reasoning = reasoning
	}

	if suggestion, ok := raw["suggestion"].(string); ok {
		issue.Suggestion = suggestion
	}

	if confidence, ok := raw["confidence"].(float64); ok {
		issue.Confidence = confidence
	} else {
		issue.Confidence = 0.7 // Default confidence
	}

	return issue
}

// determineOverallQuality determines the overall quality based on issues.
func determineOverallQuality(issues []types.ReviewIssue) string {
	if len(issues) == 0 {
		return "good"
	}

	hasHighSeverity := false
	hasMediumSeverity := false

	for _, issue := range issues {
		switch strings.ToLower(issue.Severity) {
		case "critical", "high", "error":
			hasHighSeverity = true
		case "medium", "warning":
			hasMediumSeverity = true
		}
	}

	if hasHighSeverity {
		return "needs_attention"
	}
	if hasMediumSeverity {
		return "acceptable"
	}
	return "good"
}

// calculateConfidence calculates the average confidence of issues.
func calculateConfidence(issues []types.ReviewIssue) float64 {
	if len(issues) == 0 {
		return 0.9 // High confidence when no issues found
	}

	total := 0.0
	for _, issue := range issues {
		total += issue.Confidence
	}

	return total / float64(len(issues))
}

// ProcessMultipleChunks processes multiple chunks in parallel using modules.Parallel.
// This is the high-performance batch processing method that should be used for code reviews.
func (p *EnhancedCodeReviewProcessor) ProcessMultipleChunks(ctx context.Context, chunks []map[string]interface{}, taskContext map[string]interface{}) ([]*types.EnhancedReviewResult, error) {
	if len(chunks) == 0 {
		return []*types.EnhancedReviewResult{}, nil
	}

	startTime := util.GetCurrentTimeMs()
	p.logger.Info(ctx, "🚀 Processing %d chunks with parallel module (workers: %d)", len(chunks), getParallelWorkers())

	// Prepare batch inputs for parallel processing
	batchInputs := make([]map[string]interface{}, len(chunks))
	for i, chunk := range chunks {
		inputs := make(map[string]interface{})

		// Copy chunk data
		for k, v := range chunk {
			inputs[k] = v
		}

		// Add task context (guidelines, repo patterns, etc.)
		for k, v := range taskContext {
			if _, exists := inputs[k]; !exists {
				inputs[k] = v
			}
		}

		// Add file type context
		if filePath, ok := chunk["file_path"].(string); ok {
			inputs["file_type_context"] = getFileTypeContext(filePath)
		}

		batchInputs[i] = inputs
	}

	// Process all chunks in parallel using modules.Parallel
	p.logger.Info(ctx, "🔍 Calling parallelProcessor.Process with %d batch inputs", len(batchInputs))
	result, err := p.parallelProcessor.Process(ctx, map[string]interface{}{
		"batch_inputs": batchInputs,
	})
	if err != nil {
		p.logger.Error(ctx, "❌ Parallel processing error: %v", err)
		return nil, fmt.Errorf("parallel processing failed: %w", err)
	}

	p.logger.Info(ctx, "🔍 Parallel processing returned, result keys: %v", getMapKeys(result))

	// Check for failures
	if failures, ok := result["failures"].([]map[string]interface{}); ok && len(failures) > 0 {
		p.logger.Warn(ctx, "⚠️ Got %d failures from parallel processing", len(failures))
		for i, f := range failures {
			if i < 3 { // Log first 3 failures
				p.logger.Warn(ctx, "  Failure %d: %v", i, f)
			}
		}
	}

	// Extract results
	rawResults, ok := result["results"].([]map[string]interface{})
	if !ok {
		// Try alternate format
		if resultsList, ok := result["results"].([]interface{}); ok {
			rawResults = make([]map[string]interface{}, len(resultsList))
			for i, r := range resultsList {
				if m, ok := r.(map[string]interface{}); ok {
					rawResults[i] = m
				}
			}
		}
	}

	// Parse results into EnhancedReviewResult
	results := make([]*types.EnhancedReviewResult, len(chunks))
	for i, chunk := range chunks {
		filePath, _ := chunk["file_path"].(string)

		var rawResult map[string]interface{}
		if i < len(rawResults) && rawResults[i] != nil {
			rawResult = rawResults[i]
		}

		if rawResult == nil {
			// Empty result for this chunk
			results[i] = &types.EnhancedReviewResult{
				Issues:         []types.ReviewIssue{},
				OverallQuality: "good",
				Confidence:     0.9,
				FilePath:       filePath,
			}
			continue
		}

		// Parse issues from result
		issues := p.parseIssuesFromResult(rawResult, filePath)

		results[i] = &types.EnhancedReviewResult{
			Issues:         issues,
			OverallQuality: determineOverallQuality(issues),
			Confidence:     calculateConfidence(issues),
			FilePath:       filePath,
		}
	}

	processingTime := util.GetCurrentTimeMs() - startTime
	totalIssues := 0
	for _, r := range results {
		totalIssues += len(r.Issues)
	}

	p.logger.Info(ctx, "✅ Parallel processing completed: %d chunks, %d issues found in %.2fms",
		len(chunks), totalIssues, processingTime)

	return results, nil
}

// parseIssuesFromResult parses issues from a single result map.
func (p *EnhancedCodeReviewProcessor) parseIssuesFromResult(result map[string]interface{}, filePath string) []types.ReviewIssue {
	var issues []types.ReviewIssue

	rationale, ok := result["rationale"].(string)
	if !ok {
		return issues
	}

	// Try to extract JSON array from rationale
	jsonStart := strings.Index(rationale, "[")
	jsonEnd := strings.LastIndex(rationale, "]")

	if jsonStart >= 0 && jsonEnd > jsonStart {
		jsonStr := rationale[jsonStart : jsonEnd+1]

		var rawIssues []map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &rawIssues); err == nil {
			for _, raw := range rawIssues {
				issue := parseRawIssue(raw, filePath)
				if issue != nil {
					issues = append(issues, *issue)
				}
			}
		}
	}

	return issues
}

// getMapKeys returns the keys of a map for logging.
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// getFileTypeContext returns context hints based on file type.
func getFileTypeContext(filePath string) string {
	if strings.Contains(filePath, "_test.go") || strings.Contains(filePath, "/test/") {
		return "TEST_FILE: Focus on test quality, coverage, edge cases, and maintainability."
	}
	if strings.Contains(filePath, "main.go") || strings.Contains(filePath, "/cmd/") {
		return "MAIN_FILE: Focus on error handling, configuration validation, and startup logic."
	}
	if strings.Contains(filePath, "config") || strings.Contains(filePath, "settings") {
		return "CONFIG_FILE: Focus on security (no hardcoded secrets), validation, and proper defaults."
	}
	if strings.Contains(filePath, "/api/") || strings.Contains(filePath, "/handler/") {
		return "API_FILE: Focus on input validation, authentication, authorization, and error responses."
	}
	return "PRODUCTION_FILE: Focus on correctness, security, error handling, and maintainability."
}
