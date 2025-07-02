package main

import (
	"context"
	"fmt"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// EnhancedProcessorRegistry manages the enhanced processors
type EnhancedProcessorRegistry struct {
	enhancedReviewProcessor      *EnhancedCodeReviewProcessor
	consensusValidationProcessor *ConsensusValidationProcessor
	commentRefinementProcessor   *CommentRefinementProcessor
	features                     *EnhancedFeatures
	metrics                      MetricsCollector
	logger                       *logging.Logger
}

// NewEnhancedProcessorRegistry creates a new registry for enhanced processors
func NewEnhancedProcessorRegistry(metrics MetricsCollector, logger *logging.Logger) *EnhancedProcessorRegistry {
	features := GetGlobalFeatures()

	return &EnhancedProcessorRegistry{
		enhancedReviewProcessor:      NewEnhancedCodeReviewProcessor(metrics, logger),
		consensusValidationProcessor: NewConsensusValidationProcessor(metrics, logger),
		commentRefinementProcessor:   NewCommentRefinementProcessor(metrics, logger),
		features:                     features,
		metrics:                      metrics,
		logger:                       logger,
	}
}

// ProcessCodeReview processes a code review task using enhanced capabilities
func (r *EnhancedProcessorRegistry) ProcessCodeReview(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	if !r.features.IsEnhancedProcessingEnabled() {
		r.logger.Info(ctx, "Enhanced processing disabled, using legacy processor")
		return r.fallbackToLegacyReview(ctx, task, taskContext)
	}

	r.logger.Debug(ctx, "Starting enhanced code review processing")
	startTime := time.Now()

	// Phase 1: Enhanced Reasoning
	var reviewResult interface{}
	var err error

	if r.features.AdvancedReasoning {
		// Using enhanced reasoning processor
		reviewResult, err = r.enhancedReviewProcessor.Process(ctx, task, taskContext)
		if err != nil {
			r.logger.Warn(ctx, "Enhanced reasoning failed, falling back: %v", err)
			reviewResult, err = r.fallbackToLegacyReview(ctx, task, taskContext)
			if err != nil {
				return nil, fmt.Errorf("both enhanced and legacy processing failed: %w", err)
			}
		}
		GetGlobalMetrics().TrackFeatureUsage(r.features, "advanced_reasoning")
	} else {
		reviewResult, err = r.fallbackToLegacyReview(ctx, task, taskContext)
		if err != nil {
			return nil, err
		}
	}

	// Phase 2: Consensus Validation
	if r.features.ConsensusValidation {
		// Applying consensus validation

		// Prepare validation task with review results
		validationTask := r.createValidationTask(task, reviewResult)
		validationResult, err := r.consensusValidationProcessor.Process(ctx, validationTask, taskContext)
		if err != nil {
			r.logger.Warn(ctx, "Consensus validation failed, using original results: %v", err)
		} else {
			// Update task with validation results
			task.Metadata["validation_result"] = validationResult
			GetGlobalMetrics().TrackFeatureUsage(r.features, "consensus_validation")
		}
	}

	// Phase 3: Comment Refinement
	if r.features.CommentRefinement {
		// Applying comment refinement

		// Prepare refinement task
		refinementTask := r.createRefinementTask(task, reviewResult)
		refinementResult, err := r.commentRefinementProcessor.Process(ctx, refinementTask, taskContext)
		if err != nil {
			r.logger.Warn(ctx, "Comment refinement failed, using original comments: %v", err)
		} else {
			// Convert refinement result to final format
			finalResult := r.convertToFinalResult(refinementResult)
			r.trackProcessingMetrics(ctx, startTime, "enhanced")
			GetGlobalMetrics().TrackFeatureUsage(r.features, "comment_refinement")
			return finalResult, nil
		}
	}

	// Convert review result to final format if no refinement was applied
	finalResult := r.convertToFinalResult(reviewResult)
	r.trackProcessingMetrics(ctx, startTime, "enhanced")
	return finalResult, nil
}

// createValidationTask creates a task for consensus validation
func (r *EnhancedProcessorRegistry) createValidationTask(originalTask agents.Task, reviewResult interface{}) agents.Task {
	validationTask := agents.Task{
		ID:           originalTask.ID + "_validation",
		Type:         "consensus_validation",
		Metadata:     make(map[string]interface{}),
		Dependencies: []string{originalTask.ID},
		Priority:     originalTask.Priority,
	}

	// Copy original metadata
	for k, v := range originalTask.Metadata {
		validationTask.Metadata[k] = v
	}

	// Add review result for validation
	if enhancedResult, ok := reviewResult.(*EnhancedReviewResult); ok {
		validationTask.Metadata["enhanced_result"] = enhancedResult
	} else {
		validationTask.Metadata["review_result"] = reviewResult
	}

	return validationTask
}

// createRefinementTask creates a task for comment refinement
func (r *EnhancedProcessorRegistry) createRefinementTask(originalTask agents.Task, reviewResult interface{}) agents.Task {
	refinementTask := agents.Task{
		ID:           originalTask.ID + "_refinement",
		Type:         "comment_refinement",
		Metadata:     make(map[string]interface{}),
		Dependencies: []string{originalTask.ID},
		Priority:     originalTask.Priority,
	}

	// Copy original metadata
	for k, v := range originalTask.Metadata {
		refinementTask.Metadata[k] = v
	}

	// Add results from previous stages
	if enhancedResult, ok := reviewResult.(*EnhancedReviewResult); ok {
		refinementTask.Metadata["enhanced_result"] = enhancedResult
	} else {
		refinementTask.Metadata["review_result"] = reviewResult
	}

	return refinementTask
}

// convertToFinalResult converts various result types to the expected format
func (r *EnhancedProcessorRegistry) convertToFinalResult(result interface{}) interface{} {
	switch res := result.(type) {
	case *CommentRefinementResult:
		// Convert refined comments to PR review comments
		var comments []PRReviewComment
		for _, refined := range res.RefinedComments {
			comment := PRReviewComment{
				FilePath:   refined.FilePath,
				LineNumber: refined.LineNumber,
				Content:    refined.RefinedComment,
				Category:   refined.Category,
				Severity:   refined.Severity,
				Suggestion: refined.Suggestion,
			}
			comments = append(comments, comment)
		}

		return map[string]interface{}{
			"comments":        comments,
			"processing_type": "enhanced_refined",
			"quality_score":   res.AverageQuality,
			"processing_time": res.ProcessingTime,
			"total_attempts":  res.TotalAttempts,
		}

	case *EnhancedReviewResult:
		// Convert enhanced review result to PR review comments
		var comments []PRReviewComment
		for _, issue := range res.Issues {
			comment := PRReviewComment{
				FilePath:   issue.FilePath,
				LineNumber: issue.LineRange.Start,
				Content:    issue.Description,
				Category:   issue.Category,
				Severity:   issue.Severity,
				Suggestion: issue.Suggestion,
			}
			comments = append(comments, comment)
		}

		return map[string]interface{}{
			"comments":        comments,
			"processing_type": "enhanced_reasoning",
			"confidence":      res.Confidence,
			"reasoning_chain": res.ReasoningChain,
			"processing_time": res.ProcessingTime,
		}

	case *ConsensusValidationResult:
		// Convert validation result to PR review comments
		var comments []PRReviewComment
		for _, issue := range res.ValidatedIssues {
			comment := PRReviewComment{
				FilePath:   issue.FilePath,
				LineNumber: issue.LineRange.Start,
				Content:    issue.Description,
				Category:   issue.Category,
				Severity:   issue.Severity,
				Suggestion: issue.Suggestion,
			}
			comments = append(comments, comment)
		}

		return map[string]interface{}{
			"comments":        comments,
			"processing_type": "consensus_validated",
			"consensus_score": res.ConsensusScore,
			"rejected_count":  len(res.RejectedIssues),
			"processing_time": res.ProcessingTime,
		}

	default:
		// Return as-is for unknown types
		return result
	}
}

// fallbackToLegacyReview falls back to the original review processor
func (r *EnhancedProcessorRegistry) fallbackToLegacyReview(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	// Using legacy review processor

	// Use the original CodeReviewProcessor
	legacyProcessor := &CodeReviewProcessor{metrics: r.metrics}
	result, err := legacyProcessor.Process(ctx, task, taskContext)

	if err == nil {
		GetGlobalMetrics().TrackFeatureUsage(r.features, "legacy_fallback")
	}

	return result, err
}

// trackProcessingMetrics tracks metrics for the enhanced processing
func (r *EnhancedProcessorRegistry) trackProcessingMetrics(ctx context.Context, startTime time.Time, processingType string) {
	if r.metrics != nil {
		processingDuration := time.Since(startTime).Milliseconds()
		// Basic metrics tracking - extend MetricsCollector interface as needed
		// r.metrics.TrackProcessingTime(ctx, processingType, float64(processingDuration))
		_ = processingDuration // Use the variable to avoid unused error
	}
}

// EnhancedTaskProcessor wraps the existing task processing with enhanced capabilities
type EnhancedTaskProcessor struct {
	registry *EnhancedProcessorRegistry
	logger   *logging.Logger
}

// NewEnhancedTaskProcessor creates a new enhanced task processor
func NewEnhancedTaskProcessor(metrics MetricsCollector, logger *logging.Logger) *EnhancedTaskProcessor {
	return &EnhancedTaskProcessor{
		registry: NewEnhancedProcessorRegistry(metrics, logger),
		logger:   logger,
	}
}

// Process handles task processing with enhanced capabilities
func (p *EnhancedTaskProcessor) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	// Initialize enhanced features if not already done
	if globalFeatures == nil {
		InitializeEnhancedFeatures()
	}

	// Log feature status
	if isDebugLoggingEnabled() {
		// Processing task with enhanced features
	}

	// Try Phase 2 processing first if available
	if IsPhase2Available() {
		phase2 := GetGlobalPhase2Integration()
		if phase2 != nil {
			capabilities := phase2.GetProcessingCapabilities()

			// Use Phase 2 declarative workflows for code review
			if task.Type == "code_review" && capabilities.DeclarativeWorkflows {
				// Using Phase 2 declarative workflow processing
				result, err := phase2.ProcessWithPhase2Features(ctx, task, taskContext)
				if err == nil {
					return result, nil
				}
				p.logger.Warn(ctx, "Phase 2 processing failed, falling back: %v", err)
			}

			// Enhanced memory for conversation context
			if capabilities.AdvancedMemory {
				if conversationID, exists := task.Metadata["conversation_id"].(string); exists {
					// Store the interaction
					speaker := "user"
					if task.Type == "comment_response" {
						speaker = "assistant"
					}
					content := fmt.Sprintf("Processing %s task", task.Type)
					phase2.StoreConversationMessage(ctx, conversationID, speaker, content, UserMessage)
				}
			}
		}
	}

	// Route to appropriate processor based on task type (Phase 1/fallback)
	switch task.Type {
	case "code_review":
		return p.registry.ProcessCodeReview(ctx, task, taskContext)

	case "comment_response":
		// Enhanced with Phase 2 memory if available
		if IsPhase2Available() {
			if result, err := p.processWithPhase2Memory(ctx, task, taskContext); err == nil {
				return result, nil
			}
		}
		return p.fallbackToLegacy(ctx, task, taskContext, "comment_response")

	case "repo_qa":
		// Enhanced with Phase 2 memory if available
		if IsPhase2Available() {
			if result, err := p.processWithPhase2Memory(ctx, task, taskContext); err == nil {
				return result, nil
			}
		}
		return p.fallbackToLegacy(ctx, task, taskContext, "repo_qa")

	default:
		return p.fallbackToLegacy(ctx, task, taskContext, "unknown")
	}
}

// fallbackToLegacy provides fallback to original processors
func (p *EnhancedTaskProcessor) fallbackToLegacy(ctx context.Context, task agents.Task, taskContext map[string]interface{}, reason string) (interface{}, error) {
	// Falling back to legacy processor

	// Use the original task processors based on type
	switch task.Type {
	case "code_review":
		processor := &CodeReviewProcessor{metrics: p.registry.metrics}
		return processor.Process(ctx, task, taskContext)

	case "comment_response":
		processor := &CommentResponseProcessor{metrics: p.registry.metrics}
		return processor.Process(ctx, task, taskContext)

	case "repo_qa":
		processor := &RepoQAProcessor{}
		return processor.Process(ctx, task, taskContext)

	default:
		return nil, fmt.Errorf("unknown task type: %s", task.Type)
	}
}

// processWithPhase2Memory processes tasks with Phase 2 enhanced memory capabilities
func (p *EnhancedTaskProcessor) processWithPhase2Memory(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	phase2 := GetGlobalPhase2Integration()
	if phase2 == nil {
		return nil, fmt.Errorf("Phase 2 integration not available")
	}

	// Build enhanced context using advanced memory
	if conversationID, exists := task.Metadata["conversation_id"].(string); exists {
		currentMessage := fmt.Sprintf("Processing %s", task.Type)
		if content, exists := task.Metadata["content"].(string); exists {
			currentMessage = content
		}

		contextResult, err := phase2.BuildEnhancedContext(ctx, conversationID, currentMessage)
		if err == nil && contextResult != nil {
			// Add enhanced context to task context
			taskContext["enhanced_context"] = contextResult.Context
			taskContext["context_sources"] = contextResult.Sources
			taskContext["context_confidence"] = contextResult.Confidence
		}
	}

	// Process with enhanced context using Phase 1 processors but with Phase 2 context
	switch task.Type {
	case "comment_response":
		processor := &CommentResponseProcessor{metrics: p.registry.metrics}
		result, err := processor.Process(ctx, task, taskContext)

		// Store the result in memory for learning
		if err == nil && phase2 != nil {
			speaker := "assistant"
			content := fmt.Sprintf("Generated response for %s", task.Type)
			if conversationID, exists := task.Metadata["conversation_id"].(string); exists {
				phase2.StoreConversationMessage(ctx, conversationID, speaker, content, AssistantMessage)
			}
		}

		return result, err

	case "repo_qa":
		processor := &RepoQAProcessor{}
		result, err := processor.Process(ctx, task, taskContext)

		// Store the Q&A interaction
		if err == nil && phase2 != nil {
			if conversationID, exists := task.Metadata["conversation_id"].(string); exists {
				question := "Repository question"
				if q, exists := task.Metadata["question"].(string); exists {
					question = q
				}
				phase2.StoreConversationMessage(ctx, conversationID, "user", question, UserMessage)

				answer := "Repository answer"
				if resultMap, ok := result.(map[string]interface{}); ok {
					if ans, exists := resultMap["answer"].(string); exists {
						answer = ans
					}
				}
				phase2.StoreConversationMessage(ctx, conversationID, "assistant", answer, AssistantMessage)
			}
		}

		return result, err

	default:
		return nil, fmt.Errorf("unsupported task type for Phase 2 memory processing: %s", task.Type)
	}
}

// Enhanced Orchestrator Integration
// Note: This section will be implemented when the base orchestrator interface is available

/*
// EnhancedFlexibleOrchestrator extends the original orchestrator with enhanced capabilities
type EnhancedFlexibleOrchestrator struct {
	originalOrchestrator interface{} // Will be *FlexibleOrchestrator when available
	enhancedProcessor    *EnhancedTaskProcessor
	features             *EnhancedFeatures
	logger               *logging.Logger
}
*/

// Integration Helper Functions

// ReplaceTaskProcessor replaces the task processor in existing orchestrator
func ReplaceTaskProcessor(orchestrator interface{}, enhancedProcessor *EnhancedTaskProcessor) error {
	// This would be implemented based on the actual orchestrator structure
	// For now, return success to indicate the pattern
	return nil
}

// WrapExistingProcessor wraps an existing processor with enhanced capabilities
func WrapExistingProcessor(existingProcessor interface{}, features *EnhancedFeatures) interface{} {
	// This would wrap existing processors to add enhanced capabilities
	// Implementation depends on the specific processor interface
	return existingProcessor
}

