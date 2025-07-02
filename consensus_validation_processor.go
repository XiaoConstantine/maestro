package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
)

// ConsensusValidationProcessor implements multi-chain comparison for robust validation
type ConsensusValidationProcessor struct {
	validator *modules.MultiChainComparison
	metrics   MetricsCollector
	logger    *logging.Logger
}

// ConsensusValidationResult represents the output of consensus validation
type ConsensusValidationResult struct {
	ValidatedIssues   []ReviewIssue     `json:"validated_issues"`
	RejectedIssues    []ReviewIssue     `json:"rejected_issues"`
	ConsensusScore    float64           `json:"consensus_score"`
	ValidationReasons map[string]string `json:"validation_reasons"`
	ProcessingTime    float64           `json:"processing_time_ms"`
	ValidatorResults  []ValidatorResult `json:"validator_results"`
}

// ValidatorResult represents the result from a single validator
type ValidatorResult struct {
	ValidatorType string  `json:"validator_type"`
	Decision      string  `json:"decision"` // "accept", "reject", "uncertain"
	Confidence    float64 `json:"confidence"`
	Reasoning     string  `json:"reasoning"`
}

// IssueValidationInput represents input for validating a single issue
type IssueValidationInput struct {
	Issue       ReviewIssue `json:"issue"`
	CodeContext string      `json:"code_context"`
	Guidelines  string      `json:"guidelines"`
	RepoContext string      `json:"repo_context"`
}

// NewConsensusValidationProcessor creates a new consensus validation processor
func NewConsensusValidationProcessor(metrics MetricsCollector, logger *logging.Logger) *ConsensusValidationProcessor {
	// Create multi-chain comparison signature for consensus validation
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "issue_description", Description: "The code issue to validate"}},
			{Field: core.Field{Name: "code_context", Description: "Surrounding code context"}},
			{Field: core.Field{Name: "file_path", Description: "Path of the file containing the issue"}},
			{Field: core.Field{Name: "guidelines", Description: "Coding guidelines and standards"}},
			{Field: core.Field{Name: "repo_patterns", Description: "Repository patterns and context"}},
		},
		[]core.OutputField{
			{Field: core.NewField("consensus_decision")},
			{Field: core.NewField("consensus_confidence")},
			{Field: core.NewField("validation_reasoning")},
			{Field: core.NewField("validator_perspectives")},
		},
	).WithInstruction(`
You are performing consensus validation of a code review issue using multiple perspectives.

VALIDATION PERSPECTIVES:
1. CONTEXT VALIDATOR: Does this issue make sense in the surrounding code context?
   - Check if the issue is relevant to the actual code
   - Verify the issue location is accurate
   - Ensure the issue isn't a false positive due to missing context

2. RULE COMPLIANCE VALIDATOR: Does this issue violate established coding standards?
   - Check against language-specific best practices
   - Verify security guidelines compliance
   - Assess performance impact guidelines
   - Review maintainability standards

3. PRACTICAL IMPACT VALIDATOR: Is this issue actionable and worthwhile?
   - Assess real-world impact of the issue
   - Determine if the suggested fix is practical
   - Evaluate cost-benefit of addressing the issue
   - Consider team priorities and constraints

CONSENSUS DECISION PROCESS:
- If 2+ validators agree on "accept": Final decision = accept
- If 2+ validators agree on "reject": Final decision = reject  
- If validators disagree significantly: Final decision = uncertain
- Weight decisions by confidence scores

OUTPUT FORMAT:
- consensus_decision: "accept" | "reject" | "uncertain"
- consensus_confidence: Weighted confidence score (0-1)
- validation_reasoning: Clear explanation of the consensus decision
- validator_perspectives: JSON array with each validator's decision, confidence, and reasoning

Be thorough but practical. Focus on filtering out false positives while preserving legitimate issues.
`)

	// Create multi-chain comparison with 3 validation perspectives
	// Temperature 0.2 for consistent validation decisions
	validator := modules.NewMultiChainComparison(signature, 3, 0.2).WithName("IssueConsensusValidator")

	return &ConsensusValidationProcessor{
		validator: validator,
		metrics:   metrics,
		logger:    logger,
	}
}

// Process performs consensus validation on review issues
func (p *ConsensusValidationProcessor) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	// Check if consensus validation is enabled
	if !isConsensusValidationEnabled() {
		p.logger.Info(ctx, "Consensus validation disabled, using single validation")
		return p.fallbackToSingleValidation(ctx, task, taskContext)
	}

	startTime := getCurrentTimeMs()

	// Extract validation input from task
	validationInput, err := p.extractValidationInput(task, taskContext)
	if err != nil {
		return nil, fmt.Errorf("failed to extract validation input: %w", err)
	}

	p.logger.Debug(ctx, "Starting consensus validation for %d issues", len(validationInput))

	// Process each issue through consensus validation
	var validatedIssues []ReviewIssue
	var rejectedIssues []ReviewIssue
	var validatorResults []ValidatorResult
	validationReasons := make(map[string]string)

	for _, input := range validationInput {
		result, err := p.validateSingleIssue(ctx, input)
		if err != nil {
			p.logger.Warn(ctx, "Failed to validate issue %s: %v", input.Issue.Description, err)
			// On error, accept the issue to be conservative
			validatedIssues = append(validatedIssues, input.Issue)
			continue
		}

		// Store validation reasoning
		issueKey := fmt.Sprintf("%s:%d", input.Issue.FilePath, input.Issue.LineRange.Start)
		validationReasons[issueKey] = result.ValidationReasoning

		// Append validator results
		validatorResults = append(validatorResults, result.ValidatorResults...)

		// Make decision based on consensus
		if result.ConsensusDecision == "accept" && result.ConsensusConfidence > 0.6 {
			validatedIssues = append(validatedIssues, input.Issue)
		} else if result.ConsensusDecision == "reject" && result.ConsensusConfidence > 0.6 {
			rejectedIssues = append(rejectedIssues, input.Issue)
		} else {
			// Uncertain or low confidence - be conservative and accept
			validatedIssues = append(validatedIssues, input.Issue)
		}
	}

	processingTime := getCurrentTimeMs() - startTime

	// Calculate overall consensus score
	consensusScore := p.calculateOverallConsensusScore(validatorResults)

	result := &ConsensusValidationResult{
		ValidatedIssues:   validatedIssues,
		RejectedIssues:    rejectedIssues,
		ConsensusScore:    consensusScore,
		ValidationReasons: validationReasons,
		ProcessingTime:    processingTime,
		ValidatorResults:  validatorResults,
	}

	// Track metrics
	p.trackValidationMetrics(ctx, result)

	p.logger.Debug(ctx, "Consensus validation completed: %d validated, %d rejected, %.2f consensus score",
		len(validatedIssues), len(rejectedIssues), consensusScore)

	return result, nil
}

// validateSingleIssue performs consensus validation on a single issue
func (p *ConsensusValidationProcessor) validateSingleIssue(ctx context.Context, input IssueValidationInput) (*SingleValidationResult, error) {
	// Prepare inputs for multi-chain comparison
	inputs := map[string]interface{}{
		"issue_description": input.Issue.Description,
		"code_context":      input.CodeContext,
		"file_path":         input.Issue.FilePath,
		"guidelines":        input.Guidelines,
		"repo_patterns":     input.RepoContext,
	}

	// Execute multi-chain comparison
	result, err := p.validator.Process(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("validator processing failed: %w", err)
	}

	// Parse the validation result
	return p.parseValidationResult(result)
}

// SingleValidationResult represents the result of validating one issue
type SingleValidationResult struct {
	ConsensusDecision   string            `json:"consensus_decision"`
	ConsensusConfidence float64           `json:"consensus_confidence"`
	ValidationReasoning string            `json:"validation_reasoning"`
	ValidatorResults    []ValidatorResult `json:"validator_results"`
}

// parseValidationResult parses the output from the multi-chain validator
func (p *ConsensusValidationProcessor) parseValidationResult(result map[string]interface{}) (*SingleValidationResult, error) {
	// Extract consensus decision
	decision, _ := result["consensus_decision"].(string)
	if decision == "" {
		decision = "accept" // Default to accepting if unclear
	}

	// Extract confidence score
	confidence := 0.7 // Default confidence
	if confStr, ok := result["consensus_confidence"].(string); ok {
		if parsed, err := parseFloat(confStr); err == nil {
			confidence = parsed
		}
	}

	// Extract reasoning
	reasoning, _ := result["validation_reasoning"].(string)

	// Parse individual validator perspectives
	validatorResults := p.parseValidatorPerspectives(result["validator_perspectives"])

	return &SingleValidationResult{
		ConsensusDecision:   decision,
		ConsensusConfidence: confidence,
		ValidationReasoning: reasoning,
		ValidatorResults:    validatorResults,
	}, nil
}

// parseValidatorPerspectives parses individual validator results from JSON
func (p *ConsensusValidationProcessor) parseValidatorPerspectives(perspectivesData interface{}) []ValidatorResult {
	var results []ValidatorResult

	// Try to parse as a JSON string first for robustness
	if perspectivesStr, ok := perspectivesData.(string); ok {
		if err := json.Unmarshal([]byte(perspectivesStr), &results); err == nil && len(results) > 0 {
			return results
		}
	}

	// Fallback if JSON parsing fails or no results are parsed
	return []ValidatorResult{
		{ValidatorType: "context", Decision: "accept", Confidence: 0.7, Reasoning: "Default context validation (fallback)"},
		{ValidatorType: "compliance", Decision: "accept", Confidence: 0.7, Reasoning: "Default compliance validation (fallback)"},
		{ValidatorType: "impact", Decision: "accept", Confidence: 0.7, Reasoning: "Default impact validation (fallback)"},
	}
}

// extractValidationInput extracts validation inputs from task and context
func (p *ConsensusValidationProcessor) extractValidationInput(task agents.Task, taskContext map[string]interface{}) ([]IssueValidationInput, error) {
	// Extract issues from task metadata
	var issues []ReviewIssue

	// Try to get issues from enhanced review result
	if enhancedResult, ok := task.Metadata["enhanced_result"].(*EnhancedReviewResult); ok {
		issues = enhancedResult.Issues
	} else if reviewHandoff, ok := task.Metadata["handoff"].(*ReviewHandoff); ok {
		// Extract from legacy handoff format
		for _, validatedIssue := range reviewHandoff.ValidatedIssues {
			issues = append(issues, ReviewIssue{
				FilePath:    validatedIssue.FilePath,
				LineRange:   validatedIssue.LineRange,
				Category:    validatedIssue.Category,
				Severity:    validatedIssue.Severity,
				Description: validatedIssue.Context,
				Suggestion:  validatedIssue.Suggestion,
				Confidence:  0.8,
			})
		}
	} else {
		return nil, fmt.Errorf("no issues found in task metadata")
	}

	// Extract context information
	codeContext := getStringFromContext(taskContext, "code_context", "")
	guidelines := getStringFromContext(taskContext, "guidelines", "Follow standard coding practices")
	repoContext := getStringFromContext(taskContext, "repository_context", "")

	// Create validation inputs
	var inputs []IssueValidationInput
	for _, issue := range issues {
		inputs = append(inputs, IssueValidationInput{
			Issue:       issue,
			CodeContext: codeContext,
			Guidelines:  guidelines,
			RepoContext: repoContext,
		})
	}

	return inputs, nil
}

// calculateOverallConsensusScore calculates the overall consensus quality
func (p *ConsensusValidationProcessor) calculateOverallConsensusScore(results []ValidatorResult) float64 {
	if len(results) == 0 {
		return 0.5
	}

	totalScore := 0.0
	for _, result := range results {
		totalScore += result.Confidence
	}

	return totalScore / float64(len(results))
}

// fallbackToSingleValidation provides fallback when consensus validation is disabled
func (p *ConsensusValidationProcessor) fallbackToSingleValidation(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	p.logger.Info(ctx, "Using single validation fallback")

	// Extract issues and return them as-is with basic validation
	validationInput, err := p.extractValidationInput(task, taskContext)
	if err != nil {
		return nil, err
	}

	var validatedIssues []ReviewIssue
	for _, input := range validationInput {
		validatedIssues = append(validatedIssues, input.Issue)
	}

	return &ConsensusValidationResult{
		ValidatedIssues: validatedIssues,
		RejectedIssues:  []ReviewIssue{},
		ConsensusScore:  0.8, // Default score for single validation
		ProcessingTime:  0.0,
	}, nil
}

// trackValidationMetrics records metrics for validation processing
func (p *ConsensusValidationProcessor) trackValidationMetrics(ctx context.Context, result *ConsensusValidationResult) {
	if p.metrics != nil {
		// Basic metrics tracking - extend MetricsCollector interface as needed
		// p.metrics.TrackProcessingTime(ctx, "consensus_validation", result.ProcessingTime)
		// p.metrics.TrackValidationResults(ctx, len(result.ValidatedIssues), len(result.RejectedIssues))
		// p.metrics.TrackConsensusScore(ctx, result.ConsensusScore)
	}
}

// Helper functions

func isConsensusValidationEnabled() bool {
	return getEnvBool("MAESTRO_CONSENSUS_VALIDATION", true)
}
