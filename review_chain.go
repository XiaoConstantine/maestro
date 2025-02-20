package main

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		// Enhanced thread context with detailed conversation tracking
		ThreadContext struct {
			// Participant information
			OriginalAuthor string   `json:"original_author"`
			LastResponder  string   `json:"last_responder,omitempty"`
			Participants   []string `json:"participants"`

			// Timing and status
			CreatedAt        time.Time         `json:"created_at"`
			LastUpdate       time.Time         `json:"last_update"`
			Status           ThreadStatus      `json:"status"`
			ResolutionStatus ResolutionOutcome `json:"resolution_status"`

			// Conversation history and context
			ConversationFlow []PRReviewComment `json:"conversation_flow"`
			RelatedChanges   []ReviewChunk     `json:"related_changes"`

			// Conversation metrics and analysis
			InteractionCount   int `json:"interaction_count"`
			ResolutionAttempts []struct {
				Timestamp time.Time `json:"timestamp"`
				Proposal  string    `json:"proposal"`
				Outcome   string    `json:"outcome"`
			} `json:"resolution_attempts"`

			// Context tracking
			ContextualMetrics struct {
				CategoryFrequency map[string]int `json:"category_frequency"`
				TopicEvolution    []string       `json:"topic_evolution"`
				RelatedThreads    []int64        `json:"related_threads"`
			} `json:"contextual_metrics"`

			// Quality metrics
			QualityIndicators struct {
				ResponseLatency    []time.Duration `json:"response_latency"`
				ResolutionProgress float64         `json:"resolution_progress"`
				EffectivenesScore  float64         `json:"effectiveness_score"`
			} `json:"quality_indicators"`
		} `json:"thread_context"`
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
	- potential_issues: same issues from input
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
			Provide:
			- potential_issues: same issues from input
			- rule_compliant: whether the issue match rule's criteria exactly 
			- refined_suggestion: Can the suggestion be improved based on the specific context
        
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
			Provide:
			- is_actionable: boolean indicating if the issue requires action
			- final_suggestion: string with the final suggestion
			- severity: string indicating severity level (low, medium, high)`)),
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

	// Get thread information
	if threadID, exists := metadata["thread_id"]; exists {
		switch v := threadID.(type) {
		case int64:
			rcm.ThreadID = &v
		case float64:
			val := int64(v)
			rcm.ThreadID = &val
		}
	}
	if history, ok := metadata["thread_history"].([]PRReviewComment); ok {
		rcm.ThreadHistory = history

		// Initialize conversation context when we have history
		if len(history) > 0 {
			rcm.ConversationContext.OriginalAuthor = history[0].Author
			rcm.ConversationContext.LastUpdate = history[len(history)-1].Timestamp

			// Determine conversation status
			if isThreadResolved(history) {
				rcm.ConversationContext.Status = ThreadResolved
			} else if isThreadStale(history) {
				rcm.ConversationContext.Status = ThreadStale
			} else {
				rcm.ConversationContext.Status = ThreadInProgress
			}

			// Collect previous responses
			for _, comment := range history {
				rcm.ConversationContext.PreviousResponses = append(
					rcm.ConversationContext.PreviousResponses,
					comment.Content)
			}
		}
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
	// Parse rule checking results - handles the initial issue detection
	if ruleResult, ok := workflowResult["rule_checking"].(map[string]interface{}); ok {
		if issues, ok := ruleResult["potential_issues"].([]interface{}); ok {
			for _, issue := range issues {
				if issueMap, ok := issue.(map[string]interface{}); ok {
					// Create a new potential issue with safe type assertions
					potentialIssue := PotentialIssue{
						FilePath:   safeGetString(issueMap, "file_path"),
						LineNumber: safeGetInt(issueMap, "line_number"),
						RuleID:     safeGetString(issueMap, "rule_id"),
						Content:    safeGetString(issueMap, "content"),
						Category:   safeGetString(issueMap, "category"),
						Suggestion: safeGetString(issueMap, "suggestion"),
						Confidence: safeGetFloat(issueMap, "confidence"),
					}

					// Handle the context map separately since it's nested
					if contextMap, ok := issueMap["context"].(map[string]interface{}); ok {
						potentialIssue.Context = map[string]string{
							"before": safeGetString(contextMap, "before"),
							"after":  safeGetString(contextMap, "after"),
						}
					}

					// Handle metadata map
					if metadataMap, ok := issueMap["metadata"].(map[string]interface{}); ok {
						potentialIssue.Metadata = metadataMap
					}

					output.DetectedIssues = append(output.DetectedIssues, potentialIssue)
				}
			}
		}
	}

	// Parse context validation results - handles contextual understanding
	if contextResult, ok := workflowResult["context_validation"].(map[string]interface{}); ok {
		output.ContextValidation.Valid = safeGetBool(contextResult, "context_valid")
		output.ContextValidation.Confidence = safeGetFloat(contextResult, "confidence")
		output.ContextValidation.EnhancedContext = safeGetString(contextResult, "enhanced_context")
	}

	// Parse rule compliance results - validates against established rules
	if ruleCompResult, ok := workflowResult["rule_compliance"].(map[string]interface{}); ok {
		output.RuleCompliance.Compliant = safeGetBool(ruleCompResult, "rule_compliant")
		output.RuleCompliance.RefinedSuggestion = safeGetString(ruleCompResult, "refined_suggestion")
	}

	// Parse practical impact results - determines actionability and severity
	if impactResult, ok := workflowResult["practical_impact"].(map[string]interface{}); ok {
		output.PracticalImpact.IsActionable = safeGetBool(impactResult, "is_actionable")
		output.PracticalImpact.FinalSuggestion = safeGetString(impactResult, "final_suggestion")
		output.PracticalImpact.Severity = safeGetString(impactResult, "severity")
	}

	// Parse review metadata - captures file and thread information
	if metadata, ok := workflowResult["metadata"].(map[string]interface{}); ok {
		output.ReviewMetadata.FilePath = safeGetString(metadata, "file_path")
		output.ReviewMetadata.Category = safeGetString(metadata, "category")

		// Handle LineRange structure
		if lineRange, ok := metadata["line_range"].(map[string]interface{}); ok {
			output.ReviewMetadata.LineRange = LineRange{
				Start: safeGetInt(lineRange, "start"),
				End:   safeGetInt(lineRange, "end"),
				File:  safeGetString(lineRange, "file"),
			}
		}

		// Handle optional thread tracking fields
		if threadID, ok := metadata["thread_id"].(float64); ok {
			id := int64(threadID)
			output.ReviewMetadata.ThreadID = &id
		}
		if replyTo, ok := metadata["in_reply_to"].(float64); ok {
			reply := int64(replyTo)
			output.ReviewMetadata.InReplyTo = &reply
		}
	}

	return nil
}

func isThreadResolved(history []PRReviewComment) bool {
	if len(history) == 0 {
		return false
	}

	// Check if the last comment indicates resolution
	lastComment := history[len(history)-1]
	return lastComment.Resolved ||
		strings.Contains(strings.ToLower(lastComment.Content), "resolved") ||
		strings.Contains(strings.ToLower(lastComment.Content), "fixed")
}

func isThreadStale(history []PRReviewComment) bool {
	if len(history) == 0 {
		return false
	}

	lastComment := history[len(history)-1]
	// Consider a thread stale if no activity for 7 days
	return time.Since(lastComment.Timestamp) > 7*24*time.Hour
}
