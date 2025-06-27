package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// DeclarativeReviewChain implements advanced workflow patterns for code review
type DeclarativeReviewChain struct {
	workflow *DeclarativeWorkflow
	metrics  *FeatureUsageMetrics
	logger   logging.Logger
}

// WorkflowStage represents a stage in the declarative workflow
type WorkflowStage struct {
	Name        string
	Processor   agents.TaskProcessor
	Timeout     time.Duration
	RetryConfig *RetryConfig
	Priority    int
	Condition   func(context.Context, interface{}) bool
}

// RetryConfig defines retry behavior for workflow stages
type RetryConfig struct {
	MaxAttempts     int
	BackoffStrategy BackoffStrategy
	RetryableErrors []string
}

type BackoffStrategy int

const (
	ConstantBackoff BackoffStrategy = iota
	LinearBackoff
	ExponentialBackoff
)

// ParallelStage represents parallel execution of multiple processors
type ParallelStage struct {
	Name      string
	Steps     []WorkflowStep
	Strategy  ParallelStrategy
	Timeout   time.Duration
	MinSuccess int // Minimum number of steps that must succeed
}

type ParallelStrategy int

const (
	AllMustSucceed ParallelStrategy = iota
	MajorityMustSucceed
	AtLeastOneMustSucceed
	BestEffortStrategy
)

// WorkflowStep represents a single step in a parallel stage
type WorkflowStep struct {
	Name      string
	Processor agents.TaskProcessor
	Priority  int
	Timeout   time.Duration
	Retry     *RetryConfig
}

// ConditionalStage represents conditional execution based on previous results
type ConditionalStage struct {
	Name      string
	Condition func(context.Context, interface{}) bool
	ThenStage WorkflowStage
	ElseStage *WorkflowStage // Optional
}

// NewDeclarativeReviewChain creates a sophisticated workflow for code review
func NewDeclarativeReviewChain(ctx context.Context) *DeclarativeReviewChain {
	logger := logging.GetLogger()
	
	// Create specialized processors for different stages
	enhancedReviewProcessor := NewEnhancedCodeReviewProcessor(nil, logger)
	consensusValidator := NewConsensusValidationProcessor(nil, logger)
	commentRefiner := NewCommentRefinementProcessor(nil, logger)
	
	// Build declarative workflow using builder pattern
	workflow := NewWorkflowBuilder().
		// Stage 1: Initial Analysis with Chain of Thought
		Stage(WorkflowStage{
			Name:      "enhanced_analysis",
			Processor: enhancedReviewProcessor,
			Timeout:   30 * time.Second,
			RetryConfig: &RetryConfig{
				MaxAttempts:     3,
				BackoffStrategy: ExponentialBackoff,
				RetryableErrors: []string{"timeout", "rate_limit", "temporary_failure"},
			},
			Priority: 1,
		}).
		
		// Stage 2: Parallel Validation from Multiple Perspectives
		Parallel(ParallelStage{
			Name: "multi_perspective_validation",
			Steps: []WorkflowStep{
				{
					Name:      "context_validation",
					Processor: consensusValidator,
					Priority:  1,
					Timeout:   20 * time.Second,
					Retry: &RetryConfig{
						MaxAttempts:     2,
						BackoffStrategy: LinearBackoff,
					},
				},
				{
					Name:      "rule_compliance_check",
					Processor: consensusValidator,
					Priority:  2,
					Timeout:   25 * time.Second,
					Retry: &RetryConfig{
						MaxAttempts:     2,
						BackoffStrategy: LinearBackoff,
					},
				},
				{
					Name:      "practical_impact_assessment",
					Processor: consensusValidator,
					Priority:  3,
					Timeout:   30 * time.Second,
					Retry: &RetryConfig{
						MaxAttempts:     1,
						BackoffStrategy: ConstantBackoff,
					},
				},
			},
			Strategy:   MajorityMustSucceed,
			Timeout:    60 * time.Second,
			MinSuccess: 2, // At least 2 out of 3 validations must succeed
		}).
		
		// Stage 3: Conditional Refinement Based on Confidence
		Conditional(ConditionalStage{
			Name: "confidence_based_refinement",
			Condition: func(ctx context.Context, result interface{}) bool {
				return extractConfidenceScore(result) < 0.8
			},
			ThenStage: WorkflowStage{
				Name:      "refine_analysis",
				Processor: commentRefiner,
				Timeout:   45 * time.Second,
				RetryConfig: &RetryConfig{
					MaxAttempts:     2,
					BackoffStrategy: ExponentialBackoff,
				},
				Priority: 1,
			},
		}).
		
		// Stage 4: Final Comment Generation and Quality Check
		Stage(WorkflowStage{
			Name:      "final_comment_generation",
			Processor: commentRefiner,
			Timeout:   30 * time.Second,
			RetryConfig: &RetryConfig{
				MaxAttempts:     3,
				BackoffStrategy: LinearBackoff,
			},
			Priority: 1,
		}).
		
		// Configure global workflow settings
		WithErrorHandling(ErrorHandlingConfig{
			Strategy:        FallbackToLegacy,
			MaxRetries:      3,
			CircuitBreaker:  true,
			DeadlineTimeout: 5 * time.Minute,
		}).
		WithMetrics(MetricsConfig{
			Enabled:           true,
			DetailedTiming:    true,
			StageBreakdown:    true,
			ResourceTracking:  true,
		}).
		WithTracing(TracingConfig{
			Enabled:     true,
			SampleRate:  1.0,
			TraceStages: true,
		}).
		Build()

	return &DeclarativeReviewChain{
		workflow: workflow,
		metrics:  GetGlobalMetrics(),
		logger:   *logger,
	}
}

// Process executes the declarative workflow for a code review task
func (d *DeclarativeReviewChain) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	d.logger.Info(ctx, "🔄 Starting declarative workflow for %s (type: %s)", task.ID, task.Type)
	
	// Route to appropriate workflow based on task type
	if task.Type == "comment_response" {
		return d.processCommentResponse(ctx, task, taskContext)
	} else {
		return d.processCodeReview(ctx, task, taskContext)
	}
}

// processCodeReview handles code review tasks with the full workflow
func (d *DeclarativeReviewChain) processCodeReview(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	d.logger.Info(ctx, "🔍 Processing code review with full declarative workflow")
	
	// Prepare enhanced inputs with context
	inputs := d.prepareWorkflowInputs(task, taskContext)
	
	// Execute declarative workflow with full observability
	startTime := time.Now()
	result, err := d.workflow.Execute(ctx, inputs)
	duration := time.Since(startTime)
	
	// Track metrics
	d.metrics.TotalProcessingAttempts++
	
	if err != nil {
		d.logger.Error(ctx, "❌ Code review workflow failed: %v", err)
		return nil, fmt.Errorf("code review workflow execution failed: %w", err)
	}
	
	d.logger.Info(ctx, "✅ Code review workflow completed successfully in %v", duration)
	
	// Post-process results with enhanced formatting
	return d.postProcessResults(ctx, result), nil
}

// processCommentResponse handles comment response tasks with a simplified workflow
func (d *DeclarativeReviewChain) processCommentResponse(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	d.logger.Info(ctx, "💬 Processing comment response with simplified declarative workflow")
	
	// For comment responses, use the CommentResponseProcessor directly
	responseProcessor := &CommentResponseProcessor{
		metrics: nil, // Use nil for now, can be enhanced later with compatible metrics
	}
	
	// Prepare context specifically for response generation
	responseInputs := d.prepareResponseInputs(ctx, task, taskContext)
	
	// Execute response generation
	startTime := time.Now()
	result, err := responseProcessor.Process(ctx, task, responseInputs)
	duration := time.Since(startTime)
	
	// Track metrics
	d.metrics.TotalProcessingAttempts++
	
	if err != nil {
		d.logger.Error(ctx, "❌ Comment response generation failed: %v", err)
		return nil, fmt.Errorf("comment response generation failed: %w", err)
	}
	
	d.logger.Info(ctx, "✅ Comment response generated successfully in %v", duration)
	
	// Post-process results with enhanced formatting
	return d.postProcessResults(ctx, result), nil
}

// prepareResponseInputs creates inputs specifically for comment response generation
func (d *DeclarativeReviewChain) prepareResponseInputs(ctx context.Context, task agents.Task, context map[string]interface{}) map[string]interface{} {
	// Safe extraction helpers
	safeGetString := func(data map[string]interface{}, key string) string {
		if val, exists := data[key]; exists && val != nil {
			if str, ok := val.(string); ok {
				return str
			}
		}
		return ""
	}
	
	// Extract response-specific data
	originalComment := safeGetString(task.Metadata, "original_comment")
	fileContent := safeGetString(task.Metadata, "file_content")
	filePath := safeGetString(task.Metadata, "file_path")
	
	// Convert line_number from float64 to int for proper processing
	var lineNumber int
	if ln, ok := task.Metadata["line_number"].(float64); ok {
		lineNumber = int(ln)
	} else if ln, ok := task.Metadata["line_number"].(int); ok {
		lineNumber = ln
	}
	
	// If line number is 0 or invalid, use 1 as default for file-level comments
	if lineNumber <= 0 {
		lineNumber = 1
		d.logger.Debug(ctx, "Using default line number 1 for file-level comment response")
	}
	
	d.logger.Debug(ctx, "Prepared response inputs: original_comment=%q, file_path=%q, line_number=%d", 
		originalComment, filePath, lineNumber)
	
	return map[string]interface{}{
		"original_comment":   originalComment,
		"file_content":       fileContent,
		"file_path":         filePath,
		"thread_context":    task.Metadata["thread_context"],
		"line_number":       lineNumber, // Now properly converted to int
		"thread_id":         task.Metadata["thread_id"],
		"category":          task.Metadata["category"],
		"processor_type":    "comment_response",
		"task_type":         "comment_response",
		"response_mode":     "declarative",
		"workflow_metadata": map[string]interface{}{
			"start_time":       time.Now(),
			"task_id":         task.ID,
			"workflow_version": "2.3-response",
			"processing_type":  "comment_response",
		},
	}
}

// prepareWorkflowInputs creates comprehensive inputs for the workflow
func (d *DeclarativeReviewChain) prepareWorkflowInputs(task agents.Task, context map[string]interface{}) map[string]interface{} {
	// Safe extraction helpers
	safeGetString := func(data map[string]interface{}, key string) string {
		if val, exists := data[key]; exists && val != nil {
			if str, ok := val.(string); ok {
				return str
			}
		}
		return ""
	}
	
	fileContent := safeGetString(task.Metadata, "file_content")
	changes := safeGetString(task.Metadata, "changes")
	
	return map[string]interface{}{
		"file_content":       fileContent,
		"changes":           changes,
		"file_path":         task.Metadata["file_path"],
		"guidelines":        context["guidelines"],
		"repository_context": context["repository_context"],
		"previous_reviews":   context["previous_reviews"],
		"team_standards":     context["team_standards"],
		"complexity_score":   estimateCodeComplexity(fileContent),
		"change_scope":       analyzeChangeScope(changes),
		"workflow_metadata": map[string]interface{}{
			"start_time":    time.Now(),
			"task_id":      task.ID,
			"workflow_version": "2.0-declarative",
		},
	}
}

// postProcessResults formats and enhances the workflow results
func (d *DeclarativeReviewChain) postProcessResults(ctx context.Context, result interface{}) interface{} {
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		d.logger.Warn(ctx, "⚠️ Unexpected result format, returning as-is")
		return result
	}
	
	// Add workflow metadata
	resultMap["processing_type"] = "declarative_workflow"
	resultMap["workflow_version"] = "2.0"
	resultMap["task_type"] = "code_review"
	resultMap["confidence_enhanced"] = true
	
	// Ensure quality metrics are present
	if _, exists := resultMap["quality_score"]; !exists {
		resultMap["quality_score"] = calculateOverallQuality(resultMap)
	}
	
	return resultMap
}

// WorkflowBuilder provides a fluent API for building complex workflows
type WorkflowBuilder struct {
	stages    []interface{}
	config    WorkflowConfig
	error     error
}

type WorkflowConfig struct {
	ErrorHandling ErrorHandlingConfig
	Metrics       MetricsConfig
	Tracing       TracingConfig
}

type ErrorHandlingConfig struct {
	Strategy        ErrorStrategy
	MaxRetries      int
	CircuitBreaker  bool
	DeadlineTimeout time.Duration
}

type ErrorStrategy int

const (
	FailFast ErrorStrategy = iota
	RetryWithBackoff
	FallbackToLegacy
	BestEffort
)

type MetricsConfig struct {
	Enabled          bool
	DetailedTiming   bool
	StageBreakdown   bool
	ResourceTracking bool
}

type TracingConfig struct {
	Enabled     bool
	SampleRate  float64
	TraceStages bool
}

// NewWorkflowBuilder creates a new workflow builder
func NewWorkflowBuilder() *WorkflowBuilder {
	return &WorkflowBuilder{
		stages: make([]interface{}, 0),
		config: WorkflowConfig{
			ErrorHandling: ErrorHandlingConfig{
				Strategy:        RetryWithBackoff,
				MaxRetries:      3,
				CircuitBreaker:  true,
				DeadlineTimeout: 5 * time.Minute,
			},
			Metrics: MetricsConfig{
				Enabled:          true,
				DetailedTiming:   true,
				StageBreakdown:   true,
				ResourceTracking: true,
			},
			Tracing: TracingConfig{
				Enabled:     true,
				SampleRate:  0.1,
				TraceStages: true,
			},
		},
	}
}

// Stage adds a sequential stage to the workflow
func (b *WorkflowBuilder) Stage(stage WorkflowStage) *WorkflowBuilder {
	if b.error != nil {
		return b
	}
	b.stages = append(b.stages, stage)
	return b
}

// Parallel adds a parallel execution stage
func (b *WorkflowBuilder) Parallel(stage ParallelStage) *WorkflowBuilder {
	if b.error != nil {
		return b
	}
	b.stages = append(b.stages, stage)
	return b
}

// Conditional adds a conditional execution stage
func (b *WorkflowBuilder) Conditional(stage ConditionalStage) *WorkflowBuilder {
	if b.error != nil {
		return b
	}
	b.stages = append(b.stages, stage)
	return b
}

// WithErrorHandling configures error handling for the workflow
func (b *WorkflowBuilder) WithErrorHandling(config ErrorHandlingConfig) *WorkflowBuilder {
	if b.error != nil {
		return b
	}
	b.config.ErrorHandling = config
	return b
}

// WithMetrics configures metrics collection
func (b *WorkflowBuilder) WithMetrics(config MetricsConfig) *WorkflowBuilder {
	if b.error != nil {
		return b
	}
	b.config.Metrics = config
	return b
}

// WithTracing configures distributed tracing
func (b *WorkflowBuilder) WithTracing(config TracingConfig) *WorkflowBuilder {
	if b.error != nil {
		return b
	}
	b.config.Tracing = config
	return b
}

// Build creates the final workflow
func (b *WorkflowBuilder) Build() *DeclarativeWorkflow {
	if b.error != nil {
		return nil
	}
	
	// Create workflow implementation
	return &DeclarativeWorkflow{
		stages: b.stages,
		config: b.config,
	}
}

// DeclarativeWorkflow implements the workflows.Workflow interface
type DeclarativeWorkflow struct {
	stages []interface{}
	config WorkflowConfig
}

// Execute runs the declarative workflow
func (w *DeclarativeWorkflow) Execute(ctx context.Context, inputs map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()
	logger.Info(ctx, "🚀 Executing declarative workflow with %d stages", len(w.stages))
	
	// Set up context with timeout
	workflowCtx, cancel := context.WithTimeout(ctx, w.config.ErrorHandling.DeadlineTimeout)
	defer cancel()
	
	// Initialize result with inputs
	currentResult := inputs
	
	// Execute stages sequentially
	for i, stageInterface := range w.stages {
		stageName := fmt.Sprintf("stage_%d", i+1)
		
		stageResult, err := w.executeStage(workflowCtx, stageName, stageInterface, currentResult)
		if err != nil {
			return w.handleStageError(workflowCtx, stageName, err, currentResult)
		}
		
		// Merge results for next stage
		currentResult = w.mergeResults(currentResult, stageResult)
	}
	
	logger.Info(ctx, "✅ Declarative workflow completed all stages successfully")
	return currentResult, nil
}

// executeStage handles execution of different stage types
func (w *DeclarativeWorkflow) executeStage(ctx context.Context, stageName string, stageInterface interface{}, inputs map[string]interface{}) (interface{}, error) {
	switch stage := stageInterface.(type) {
	case WorkflowStage:
		return w.executeSequentialStage(ctx, stageName, stage, inputs)
	case ParallelStage:
		return w.executeParallelStage(ctx, stageName, stage, inputs)
	case ConditionalStage:
		return w.executeConditionalStage(ctx, stageName, stage, inputs)
	default:
		return nil, fmt.Errorf("unknown stage type: %T", stageInterface)
	}
}

// executeSequentialStage executes a single sequential stage
func (w *DeclarativeWorkflow) executeSequentialStage(ctx context.Context, stageName string, stage WorkflowStage, inputs map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()
	logger.Debug(ctx, "🔄 Executing sequential stage: %s", stage.Name)
	
	// Set up stage context with timeout
	stageCtx := ctx
	if stage.Timeout > 0 {
		var cancel context.CancelFunc
		stageCtx, cancel = context.WithTimeout(ctx, stage.Timeout)
		defer cancel()
	}
	
	// Create task for the processor
	task := agents.Task{
		ID:       fmt.Sprintf("%s_%s", stageName, stage.Name),
		Type:     "code_review",
		Metadata: inputs,
	}
	
	// Execute with retry logic
	var result interface{}
	var err error
	
	if stage.RetryConfig != nil {
		result, err = w.executeWithRetry(stageCtx, stage.Processor, task, inputs, stage.RetryConfig)
	} else {
		result, err = stage.Processor.Process(stageCtx, task, inputs)
	}
	
	if err != nil {
		logger.Error(ctx, "❌ Sequential stage %s failed: %v", stage.Name, err)
		return nil, err
	}
	
	logger.Debug(ctx, "✅ Sequential stage %s completed successfully", stage.Name)
	return result, nil
}

// executeParallelStage executes parallel steps with sophisticated coordination
func (w *DeclarativeWorkflow) executeParallelStage(ctx context.Context, stageName string, stage ParallelStage, inputs map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()
	logger.Info(ctx, "🔄 Executing parallel stage: %s with %d steps", stage.Name, len(stage.Steps))
	
	// Set up parallel execution context
	parallelCtx := ctx
	if stage.Timeout > 0 {
		var cancel context.CancelFunc
		parallelCtx, cancel = context.WithTimeout(ctx, stage.Timeout)
		defer cancel()
	}
	
	// Execute steps in parallel
	results := make(chan StepResult, len(stage.Steps))
	
	for i, step := range stage.Steps {
		go func(stepIndex int, step WorkflowStep) {
			stepResult := w.executeParallelStep(parallelCtx, step, inputs)
			stepResult.Index = stepIndex
			results <- stepResult
		}(i, step)
	}
	
	// Collect results based on strategy
	return w.collectParallelResults(parallelCtx, stage, results)
}

// StepResult represents the result of a parallel step execution
type StepResult struct {
	Index  int
	Name   string
	Result interface{}
	Error  error
}

// executeParallelStep executes a single step in a parallel stage
func (w *DeclarativeWorkflow) executeParallelStep(ctx context.Context, step WorkflowStep, inputs map[string]interface{}) StepResult {
	logger := logging.GetLogger()
	
	// Set up step context
	stepCtx := ctx
	if step.Timeout > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, step.Timeout)
		defer cancel()
	}
	
	// Create task
	task := agents.Task{
		ID:       step.Name,
		Type:     "code_review",
		Metadata: inputs,
	}
	
	// Execute with optional retry
	var result interface{}
	var err error
	
	if step.Retry != nil {
		result, err = w.executeWithRetry(stepCtx, step.Processor, task, inputs, step.Retry)
	} else {
		result, err = step.Processor.Process(stepCtx, task, inputs)
	}
	
	if err != nil {
		logger.Warn(ctx, "⚠️ Parallel step %s failed: %v", step.Name, err)
	} else {
		logger.Debug(ctx, "✅ Parallel step %s completed", step.Name)
	}
	
	return StepResult{
		Name:   step.Name,
		Result: result,
		Error:  err,
	}
}

// collectParallelResults aggregates parallel execution results based on strategy
func (w *DeclarativeWorkflow) collectParallelResults(ctx context.Context, stage ParallelStage, results chan StepResult) (interface{}, error) {
	logger := logging.GetLogger()
	
	stepResults := make([]StepResult, len(stage.Steps))
	successCount := 0
	
	// Collect all results
	for i := 0; i < len(stage.Steps); i++ {
		result := <-results
		stepResults[result.Index] = result
		if result.Error == nil {
			successCount++
		}
	}
	
	// Apply strategy
	switch stage.Strategy {
	case AllMustSucceed:
		if successCount != len(stage.Steps) {
			return nil, fmt.Errorf("parallel stage %s: not all steps succeeded (%d/%d)", stage.Name, successCount, len(stage.Steps))
		}
	case MajorityMustSucceed:
		required := (len(stage.Steps) + 1) / 2
		if successCount < required {
			return nil, fmt.Errorf("parallel stage %s: majority failed (%d/%d succeeded, %d required)", stage.Name, successCount, len(stage.Steps), required)
		}
	case AtLeastOneMustSucceed:
		if successCount == 0 {
			return nil, fmt.Errorf("parallel stage %s: all steps failed", stage.Name)
		}
	case BestEffortStrategy:
		// Always succeed, even if all steps fail
		logger.Info(ctx, "Parallel stage %s completed with best effort: %d/%d succeeded", stage.Name, successCount, len(stage.Steps))
	}
	
	// Custom minimum success check
	if stage.MinSuccess > 0 && successCount < stage.MinSuccess {
		return nil, fmt.Errorf("parallel stage %s: insufficient successes (%d/%d, %d required)", stage.Name, successCount, len(stage.Steps), stage.MinSuccess)
	}
	
	logger.Info(ctx, "✅ Parallel stage %s completed: %d/%d steps succeeded", stage.Name, successCount, len(stage.Steps))
	
	// Merge successful results
	return w.mergeParallelResults(stepResults), nil
}

// executeConditionalStage executes conditional logic
func (w *DeclarativeWorkflow) executeConditionalStage(ctx context.Context, stageName string, stage ConditionalStage, inputs map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()
	logger.Debug(ctx, "🔄 Executing conditional stage: %s", stage.Name)
	
	// Evaluate condition
	shouldExecuteThen := stage.Condition(ctx, inputs)
	
	if shouldExecuteThen {
		logger.Debug(ctx, "✅ Condition true, executing THEN branch")
		return w.executeSequentialStage(ctx, stageName+"_then", stage.ThenStage, inputs)
	} else if stage.ElseStage != nil {
		logger.Debug(ctx, "❌ Condition false, executing ELSE branch")
		return w.executeSequentialStage(ctx, stageName+"_else", *stage.ElseStage, inputs)
	} else {
		logger.Debug(ctx, "❌ Condition false, no ELSE branch, passing through")
		return inputs, nil
	}
}

// executeWithRetry implements sophisticated retry logic
func (w *DeclarativeWorkflow) executeWithRetry(ctx context.Context, processor agents.TaskProcessor, task agents.Task, inputs map[string]interface{}, retryConfig *RetryConfig) (interface{}, error) {
	logger := logging.GetLogger()
	
	var lastErr error
	for attempt := 1; attempt <= retryConfig.MaxAttempts; attempt++ {
		result, err := processor.Process(ctx, task, inputs)
		if err == nil {
			if attempt > 1 {
				logger.Info(ctx, "✅ Retry successful on attempt %d", attempt)
			}
			return result, nil
		}
		
		lastErr = err
		
		// Check if error is retryable
		if !w.isRetryableError(err, retryConfig.RetryableErrors) {
			logger.Debug(ctx, "❌ Non-retryable error: %v", err)
			return nil, err
		}
		
		if attempt < retryConfig.MaxAttempts {
			backoffDuration := w.calculateBackoff(attempt, retryConfig.BackoffStrategy)
			logger.Debug(ctx, "⏳ Retrying in %v (attempt %d/%d)", backoffDuration, attempt, retryConfig.MaxAttempts)
			
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffDuration):
				// Continue to next attempt
			}
		}
	}
	
	logger.Error(ctx, "❌ All retry attempts exhausted")
	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// Helper functions

func (w *DeclarativeWorkflow) handleStageError(ctx context.Context, stageName string, err error, inputs map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()
	
	switch w.config.ErrorHandling.Strategy {
	case FailFast:
		return nil, err
	case FallbackToLegacy:
		logger.Warn(ctx, "⚠️ Stage %s failed, attempting fallback to legacy processing", stageName)
		return w.executeLegacyFallback(ctx, inputs)
	case BestEffort:
		logger.Warn(ctx, "⚠️ Stage %s failed, continuing with best effort", stageName)
		return inputs, nil
	default:
		return nil, err
	}
}

func (w *DeclarativeWorkflow) executeLegacyFallback(ctx context.Context, inputs map[string]interface{}) (interface{}, error) {
	// Create a basic legacy processor as fallback
	legacyProcessor := &CodeReviewProcessor{}
	
	task := agents.Task{
		ID:       "legacy_fallback",
		Type:     "code_review",
		Metadata: inputs,
	}
	
	return legacyProcessor.Process(ctx, task, inputs)
}

func (w *DeclarativeWorkflow) mergeResults(base map[string]interface{}, new interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	
	// Copy base
	for k, v := range base {
		result[k] = v
	}
	
	// Merge new results
	if newMap, ok := new.(map[string]interface{}); ok {
		for k, v := range newMap {
			result[k] = v
		}
	}
	
	return result
}

func (w *DeclarativeWorkflow) mergeParallelResults(stepResults []StepResult) map[string]interface{} {
	merged := make(map[string]interface{})
	
	for _, stepResult := range stepResults {
		if stepResult.Error == nil && stepResult.Result != nil {
			if resultMap, ok := stepResult.Result.(map[string]interface{}); ok {
				for k, v := range resultMap {
					// Prefix keys with step name to avoid conflicts
					prefixedKey := fmt.Sprintf("%s_%s", stepResult.Name, k)
					merged[prefixedKey] = v
				}
			}
		}
	}
	
	return merged
}

func (w *DeclarativeWorkflow) isRetryableError(err error, retryableErrors []string) bool {
	errStr := err.Error()
	for _, retryableErr := range retryableErrors {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(retryableErr)) {
			return true
		}
	}
	return false
}

func (w *DeclarativeWorkflow) calculateBackoff(attempt int, strategy BackoffStrategy) time.Duration {
	baseDuration := time.Second
	
	switch strategy {
	case ConstantBackoff:
		return baseDuration
	case LinearBackoff:
		return time.Duration(attempt) * baseDuration
	case ExponentialBackoff:
		return time.Duration(1<<uint(attempt-1)) * baseDuration
	default:
		return baseDuration
	}
}

// Utility functions

func extractConfidenceScore(result interface{}) float64 {
	if resultMap, ok := result.(map[string]interface{}); ok {
		if confidence, exists := resultMap["confidence"]; exists {
			if score, ok := confidence.(float64); ok {
				return score
			}
		}
	}
	return 0.5 // Default confidence
}

func estimateCodeComplexity(content string) float64 {
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

func analyzeChangeScope(changes string) string {
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
	} else {
		return "massive"
	}
}

func calculateOverallQuality(resultMap map[string]interface{}) float64 {
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

