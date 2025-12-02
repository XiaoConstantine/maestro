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
	signatureHash := hashSignature(signature)
	maxWorkers := getParallelWorkers()

	// Try to get from cache first
	if cached, ok := moduleCache.Load(signatureHash); ok {
		if entry, ok := cached.(*ModuleCacheEntry); ok {
			if time.Since(entry.CreatedAt) < 24*time.Hour {
				return entry
			}
			moduleCache.Delete(signatureHash)
		}
	}

	// Create new modules with cache protection
	moduleCacheMux.Lock()
	defer moduleCacheMux.Unlock()

	// Double-check pattern
	if cached, ok := moduleCache.Load(signatureHash); ok {
		if entry, ok := cached.(*ModuleCacheEntry); ok {
			return entry
		}
	}

	// Create new module instances
	basePredict := modules.NewPredict(signature).WithName("CodeReviewPredict")

	parallelProcessor := modules.NewParallel(
		basePredict,
		modules.WithMaxWorkers(maxWorkers),
		modules.WithReturnFailures(true),
	).WithName("ParallelCodeReview")

	consensusModule := modules.NewMultiChainComparison(signature, 3, 0.7).WithName("ConsensusReview")

	entry := &ModuleCacheEntry{
		BasePredict:       basePredict,
		ParallelProcessor: parallelProcessor,
		ConsensusModule:   consensusModule,
		CreatedAt:         time.Now(),
	}

	moduleCache.Store(signatureHash, entry)
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

	return &EnhancedCodeReviewProcessor{
		parallelProcessor: cachedModules.ParallelProcessor,
		refinementModule:  refinementModule,
		consensusModule:   cachedModules.ConsensusModule,
		metrics:           metrics,
		logger:            logger,
	}
}

// createCodeReviewSignature creates the signature for code review.
func createCodeReviewSignature() core.Signature {
	return core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "file_content", Description: "The source code to review"}},
			{Field: core.Field{Name: "changes", Description: "The specific changes made to the code"}},
			{Field: core.Field{Name: "guidelines", Description: "Coding guidelines and standards"}},
			{Field: core.Field{Name: "repo_context", Description: "Repository context and patterns"}},
			{Field: core.Field{Name: "file_path", Description: "Path of the file being reviewed"}},
			{Field: core.Field{Name: "file_type_context", Description: "File type specific review context and focus areas"}},
			{Field: core.Field{Name: "package_name", Description: "Go package declaration for this file"}},
			{Field: core.Field{Name: "imports", Description: "Import statements showing external dependencies"}},
			{Field: core.Field{Name: "type_definitions", Description: "Struct, interface and custom type definitions from the file"}},
			{Field: core.Field{Name: "interfaces", Description: "Interface definitions showing contracts and expected behavior"}},
			{Field: core.Field{Name: "function_signatures", Description: "Function signatures showing available functions and their parameters"}},
			{Field: core.Field{Name: "method_signatures", Description: "Method signatures showing receiver methods"}},
			{Field: core.Field{Name: "leading_context", Description: "Code lines before the chunk for context (15+ lines)"}},
			{Field: core.Field{Name: "trailing_context", Description: "Code lines after the chunk for context (15+ lines)"}},
			{Field: core.Field{Name: "called_functions", Description: "Functions called within this code chunk"}},
			{Field: core.Field{Name: "used_types", Description: "Types and structs referenced in this chunk"}},
			{Field: core.Field{Name: "semantic_purpose", Description: "High-level description of what this code chunk does"}},
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
You are an expert code reviewer with comprehensive context about the codebase. Your goal is to provide HIGH-VALUE, ACTIONABLE feedback that developers will find genuinely useful. AVOID generic comments that waste time.

ENHANCED CONTEXT AVAILABLE:
- package_name: The Go package this code belongs to
- imports: All import dependencies showing what external packages are used
- type_definitions: Custom types, structs, and interfaces defined in this file
- function_signatures: Available functions with their parameters and return types
- leading_context: 15+ lines of code before this chunk for full context
- trailing_context: 15+ lines of code after this chunk for continuity
- called_functions: Functions called within this specific chunk
- used_types: Types and data structures referenced in this chunk
- semantic_purpose: High-level description of what this code accomplishes

OUTPUT REQUIREMENTS:
- rationale: Provide evidence-based analysis. End with JSON array of issues or [] if none found
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

	if lineNumber, ok := raw["line_number"].(float64); ok {
		issue.LineRange = types.LineRange{Start: int(lineNumber), End: int(lineNumber)}
	}

	if category, ok := raw["category"].(string); ok {
		issue.Category = category
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
