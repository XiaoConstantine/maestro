package main

import (
	"context"
	"fmt"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/workflows"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
)

type ReviewChainResult struct {
	DetectedIssues    []PotentialIssue   // Issues found during rule checking
	ValidationResults []ValidationResult // Results from validation steps
	ValidationPassed  bool               // Overall validation status
	Category          string             // Review category
}

// From review_chain.go.
type ReviewChainOutput struct {
	// Core review findings
	DetectedIssues []PotentialIssue `json:"detected_issues"`

	// Validation results from each step
	ContextValidation struct {
		Valid           bool    `json:"valid"`
		Confidence      float64 `json:"confidence"`
		EnhancedContext string  `json:"enhanced_context"`
	} `json:"context_validation"`

	RuleCompliance struct {
		Compliant         bool   `json:"compliant"`
		RefinedSuggestion string `json:"refined_suggestion"`
	} `json:"rule_compliance"`

	PracticalImpact struct {
		IsActionable    bool   `json:"is_actionable"`
		FinalSuggestion string `json:"final_suggestion"`
		Severity        string `json:"severity"`
	} `json:"practical_impact"`

	ReviewMetadata struct {
		FilePath  string    `json:"file_path"`
		LineRange LineRange `json:"line_range"`
		Category  string    `json:"category"`
		ThreadID  *int64    `json:"thread_id,omitempty"`
		InReplyTo *int64    `json:"in_reply_to,omitempty"`
	} `json:"review_metadata"`
}

// ValidationResult represents the outcome of each validation step.
type ValidationResult struct {
	Step       string  // Name of the validation step
	Passed     bool    // Whether validation passed
	Confidence float64 // Confidence score
	Details    string  // Additional validation details
}

type ReviewChainProcessor struct {
	workflow *workflows.ChainWorkflow
	metrics  MetricsCollector
	logger   *logging.Logger
}

func NewReviewChainProcessor(ctx context.Context, metrics MetricsCollector, logger *logging.Logger) *ReviewChainProcessor {
	// Create the chain workflow
	workflow := workflows.NewChainWorkflow(agents.NewInMemoryStore())

	processor := &ReviewChainProcessor{
		metrics: metrics,
		logger:  logger,
	}
	ruleSignature := core.NewSignature(
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

		potential_issues:
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

		Important format requirements:
		1. Return in XML format, DO NOT wrap response in JSON or MARKDOWN.
		2. Start with the exact prefix 'potential_issues:' followed by the XML content on new lines.
		3. Ensure proper indentation and structure in the XML.
		4. Use proper XML escaping for special characters in all text fields (e.g., 'content', 'suggestion', 'before', 'after'):
			- Replace every '&' with '&amp;', every '<' with '&lt;', and every '>' with '&gt;'.
			- For example, if the content is "if (a < b && b > c)", it should become "if (a &lt; b &amp;&amp; b &gt; c)".
		5. Ensure line numbers are valid integers.
		6. Keep confidence scores between 0.0 and 1.0.
		7. Use only the standard category values listed above.
		8. Provide specific, actionable suggestions.
		9. Include relevant context before and after the issue:
			- The 'before' field must contain the exact lines of code from 'file_content' immediately preceding the line with the issue.
			- The 'after' field must contain the exact lines of code from 'file_content' immediately following the line with the issue.
		10. Apply the escaping rules from point 4 to all text content in the XML to ensure well-formedness.

		Focus on finding:
		1. Error handling issues
		2. Code style violations
		3. Performance concerns
		4. Security vulnerabilities
		5. Documentation gaps
		Only report issues with high confidence (>0.7) and clear impact.`)

	// Step 1: Rule Checking
	ruleCheckStep := &workflows.Step{
		ID:     "rule_checking",
		Module: modules.NewPredict(ruleSignature),
	}

	contextValidationStep := &workflows.Step{
		ID: "context_validation",
		Module: modules.NewPredict(core.NewSignature(
			[]core.InputField{
				{Field: core.Field{Name: "potential_issues"}},
				{Field: core.Field{Name: "file_content"}},
				{Field: core.Field{Name: "line_range"}},
			},
			[]core.OutputField{
				{Field: core.NewField("potential_issues")},
				{Field: core.NewField("context_valid")},
				{Field: core.NewField("confidence")},
				{Field: core.NewField("enhanced_context")},
			},
		).WithInstruction(`Validate if the potential issue makes sense in its code context.
        Consider:
        1. The surrounding code's logical flow
        2. Whether the flagged issue is actually problematic in this context
        3. If the code pattern truly violates best practices
        
        Provide:
        - context_valid: boolean indicating if issue is valid in context
        - confidence: float between 0-1 indicating confidence level
        - enhanced_context: any additional context that helps understand the issue`)),
	}

	// Define rule compliance validation step
	ruleComplianceStep := &workflows.Step{
		ID: "rule_compliance",
		Module: modules.NewPredict(core.NewSignature(
			[]core.InputField{
				{Field: core.Field{Name: "potential_issues"}},
				{Field: core.Field{Name: "enhanced_context"}},
				{Field: core.Field{Name: "context_valid"}},
			},
			[]core.OutputField{
				{Field: core.NewField("potential_issues")},
				{Field: core.NewField("rule_compliant")},
				{Field: core.NewField("refined_suggestion")},
			},
		).WithInstruction(`Verify if the issue strictly complies with the review rules.
        Consider:
        1. Does the issue match the rule's criteria exactly?
        2. Are there any edge cases or exceptions that should be considered?
        3. Can the suggestion be improved based on the specific context?
        
        Only mark as compliant if the issue is a clear violation of the rule.`)),
	}

	// Define practical impact validation step
	practicalImpactStep := &workflows.Step{
		ID: "practical_impact",
		Module: modules.NewPredict(core.NewSignature(
			[]core.InputField{
				{Field: core.Field{Name: "potential_issues"}},
				{Field: core.Field{Name: "rule_compliant"}},
				{Field: core.Field{Name: "refined_suggestion"}},
			},
			[]core.OutputField{
				{Field: core.NewField("is_actionable")},
				{Field: core.NewField("final_suggestion")},
				{Field: core.NewField("severity")},
			},
		).WithInstruction(`Evaluate the practical impact and actionability of the issue.
        Consider:
        1. Will fixing this issue meaningfully improve the code?
        2. Is the suggestion clear and actionable?
        3. What is the appropriate severity level?
        
        Focus on providing practical value to developers.`)),
	}

	addStepWithErrorHandling := func(step *workflows.Step, stepName string) error {
		if err := workflow.AddStep(step); err != nil {
			// Log the specific step that failed
			logger.Error(ctx, "Failed to add %s step to workflow: %v", stepName, err)
			return fmt.Errorf("failed to add %s step: %w", stepName, err)
		}
		logger.Debug(ctx, "Successfully added %s step to workflow", stepName)
		return nil
	}
	steps := []struct {
		step *workflows.Step
		name string
	}{
		{ruleCheckStep, "rule checking"},
		{contextValidationStep, "context validation"},
		{ruleComplianceStep, "rule compliance"},
		{practicalImpactStep, "practical impact"},
	}
	// Add each step and handle any errors
	for _, s := range steps {
		if err := addStepWithErrorHandling(s.step, s.name); err != nil {
			// Return a partially initialized processor with error logging
			// This allows caller to handle the error appropriately
			logger.Error(ctx, "Review chain processor initialization failed: %v", err)
			return processor
		}
	}
	processor.workflow = workflow

	logger.Debug(ctx, "Successfully initialized review chain processor with all steps")
	return processor

}

func (p *ReviewChainProcessor) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()
	metadata, err := extractChainMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	// Execute the workflow
	workflowResult, err := p.workflow.Execute(ctx, map[string]interface{}{
		"file_content":  metadata.FileContent,
		"changes":       metadata.Changes,
		"guidelines":    metadata.Guidelines,
		"line_range":    metadata.LineRange,
		"repo_patterns": metadata.ReviewPatterns,
	})

	logger.Debug(ctx, "LLM response for rule checking: %+v", workflowResult)
	if err != nil {
		return nil, fmt.Errorf("chain workflow failed: %w", err)
	}
	// First parse the workflow results into our internal structure
	chainOutput := &ReviewChainOutput{}
	if err := p.parseWorkflowResults(workflowResult, chainOutput); err != nil {
		return nil, fmt.Errorf("failed to parse workflow results: %w", err)
	}

	handoff := &ReviewHandoff{
		ChainOutput:     *chainOutput,
		ValidatedIssues: make([]ValidatedIssue, 0),
	}
	// Process each detected issue that passed validation
	for _, detectedIssue := range chainOutput.DetectedIssues {
		// Only include issues that passed all validation steps
		if chainOutput.ContextValidation.Valid &&
			chainOutput.RuleCompliance.Compliant &&
			chainOutput.PracticalImpact.IsActionable {

			validatedIssue := ValidatedIssue{
				// Use the specific information from the detected issue
				FilePath: detectedIssue.FilePath,
				LineRange: LineRange{
					Start: detectedIssue.LineNumber,
					End:   detectedIssue.LineNumber,
					File:  detectedIssue.FilePath,
				},
				Category: detectedIssue.Category,
				// Combine the original issue content with enhanced context
				Context: fmt.Sprintf("%s\n\nOriginal Issue: %s",
					chainOutput.ContextValidation.EnhancedContext,
					detectedIssue.Content,
				),
				// Use the refined suggestion if available, otherwise fall back to original
				Suggestion: chainOutput.RuleCompliance.RefinedSuggestion,
				// Use the severity from practical impact assessment
				Severity:   chainOutput.PracticalImpact.Severity,
				Confidence: chainOutput.ContextValidation.Confidence,
			}

			validatedIssue.ValidationDetails.ContextValid = chainOutput.ContextValidation.Valid
			validatedIssue.ValidationDetails.RuleCompliant = chainOutput.RuleCompliance.Compliant
			validatedIssue.ValidationDetails.IsActionable = chainOutput.PracticalImpact.IsActionable

			handoff.ValidatedIssues = append(handoff.ValidatedIssues, validatedIssue)
		}
	}

	nextTaskType := determineNextTaskType(&handoff.ChainOutput)

	logger.Debug(ctx, "Created handoff with %d validated issues", len(handoff.ValidatedIssues))
	// Format for the analyzer to determine next steps
	return map[string]interface{}{
		"task_type":   nextTaskType,
		"processor":   nextTaskType,
		"description": "Process validated review findings",
		"metadata": map[string]interface{}{
			"handoff":   handoff,
			"file_path": chainOutput.ReviewMetadata.FilePath,
			"category":  chainOutput.ReviewMetadata.Category,
		},
	}, nil
}

func extractChainMetadata(metadata map[string]interface{}) (*RuleCheckerMetadata, error) {
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
	if category, ok := metadata["category"].(string); ok {
		rcm.Category = category
	}

	// If no category provided, set default
	if rcm.Category == "" {
		rcm.Category = "code-style"
	}

	return rcm, nil
}

func determineNextTaskType(output *ReviewChainOutput) string {
	// Determine whether this should route to code_review or comment_response
	if output.ReviewMetadata.InReplyTo != nil {
		return "comment_response"
	}
	return "code_review"
}

func (p *ReviewChainProcessor) parseWorkflowResults(workflowResult map[string]interface{}, output *ReviewChainOutput) error {
	// A detailed parser that handles each workflow step's output
	// and populates our structured output type

	// Example for practical impact step:
	if impactResult, ok := workflowResult["practical_impact"].(map[string]interface{}); ok {
		output.PracticalImpact.IsActionable = impactResult["is_actionable"].(bool)
		output.PracticalImpact.FinalSuggestion = impactResult["final_suggestion"].(string)
		output.PracticalImpact.Severity = impactResult["severity"].(string)
	}

	return nil
}
