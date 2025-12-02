// Package workflow provides declarative workflow patterns for code review.
package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// DeclarativeReviewChain implements advanced workflow patterns for code review.
type DeclarativeReviewChain struct {
	workflow *DeclarativeWorkflow
	metrics  *FeatureUsageMetrics
	logger   *logging.Logger
}

// WorkflowStage represents a stage in the declarative workflow.
type WorkflowStage struct {
	Name        string
	Processor   agents.TaskProcessor
	Timeout     time.Duration
	RetryConfig *RetryConfig
	Priority    int
	Condition   func(context.Context, interface{}) bool
}

// RetryConfig defines retry behavior for workflow stages.
type RetryConfig struct {
	MaxAttempts     int
	BackoffStrategy BackoffStrategy
	RetryableErrors []string
}

// BackoffStrategy defines the backoff approach for retries.
type BackoffStrategy int

const (
	// ConstantBackoff uses constant delay between retries.
	ConstantBackoff BackoffStrategy = iota
	// LinearBackoff uses linearly increasing delay.
	LinearBackoff
	// ExponentialBackoff uses exponentially increasing delay.
	ExponentialBackoff
)

// ParallelStage represents parallel execution of multiple processors.
type ParallelStage struct {
	Name       string
	Steps      []WorkflowStep
	Strategy   ParallelStrategy
	Timeout    time.Duration
	MinSuccess int // Minimum number of steps that must succeed
}

// ParallelStrategy defines how parallel execution results are evaluated.
type ParallelStrategy int

const (
	// AllMustSucceed requires all steps to succeed.
	AllMustSucceed ParallelStrategy = iota
	// MajorityMustSucceed requires majority of steps to succeed.
	MajorityMustSucceed
	// AtLeastOneMustSucceed requires at least one step to succeed.
	AtLeastOneMustSucceed
	// BestEffortStrategy succeeds even if all steps fail.
	BestEffortStrategy
)

// WorkflowStep represents a single step in a parallel stage.
type WorkflowStep struct {
	Name      string
	Processor agents.TaskProcessor
	Priority  int
	Timeout   time.Duration
	Retry     *RetryConfig
}

// ConditionalStage represents conditional execution based on previous results.
type ConditionalStage struct {
	Name      string
	Condition func(context.Context, interface{}) bool
	ThenStage WorkflowStage
	ElseStage *WorkflowStage // Optional
}

// FeatureUsageMetrics tracks feature usage for workflows.
type FeatureUsageMetrics struct {
	TotalProcessingAttempts int64
}

// NewDeclarativeReviewChain creates a sophisticated workflow for code review.
func NewDeclarativeReviewChain(ctx context.Context, enhancedReviewProcessor, consensusValidator, commentRefiner agents.TaskProcessor) *DeclarativeReviewChain {
	logger := logging.GetLogger()

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
			MinSuccess: 2,
		}).

		// Stage 3: Conditional Refinement Based on Confidence
		Conditional(ConditionalStage{
			Name: "confidence_based_refinement",
			Condition: func(ctx context.Context, result interface{}) bool {
				return ExtractConfidenceScore(result) < 0.8
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
			Enabled:          true,
			DetailedTiming:   true,
			StageBreakdown:   true,
			ResourceTracking: true,
		}).
		WithTracing(TracingConfig{
			Enabled:     true,
			SampleRate:  1.0,
			TraceStages: true,
		}).
		Build()

	return &DeclarativeReviewChain{
		workflow: workflow,
		metrics:  &FeatureUsageMetrics{},
		logger:   logger,
	}
}

// Process executes the declarative workflow for a code review task.
func (d *DeclarativeReviewChain) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	d.logger.Info(ctx, "🔄 Starting declarative workflow for %s (type: %s)", task.ID, task.Type)

	// Route to appropriate workflow based on task type
	if task.Type == "comment_response" {
		return d.processCommentResponse(ctx, task, taskContext)
	}
	return d.processCodeReview(ctx, task, taskContext)
}

// processCodeReview handles code review tasks with the full workflow.
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

// processCommentResponse handles comment response tasks with a simplified workflow.
func (d *DeclarativeReviewChain) processCommentResponse(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	d.logger.Info(ctx, "💬 Processing comment response with simplified declarative workflow")

	// Prepare context specifically for response generation
	responseInputs := d.prepareResponseInputs(ctx, task, taskContext)

	// Execute first stage only for comment responses
	startTime := time.Now()
	result, err := d.workflow.ExecuteFirstStage(ctx, responseInputs)
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

// prepareResponseInputs creates inputs specifically for comment response generation.
func (d *DeclarativeReviewChain) prepareResponseInputs(ctx context.Context, task agents.Task, taskContext map[string]interface{}) map[string]interface{} {
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
		"original_comment": originalComment,
		"file_content":     fileContent,
		"file_path":        filePath,
		"thread_context":   task.Metadata["thread_context"],
		"line_number":      lineNumber,
		"thread_id":        task.Metadata["thread_id"],
		"category":         task.Metadata["category"],
		"processor_type":   "comment_response",
		"task_type":        "comment_response",
		"response_mode":    "declarative",
		"workflow_metadata": map[string]interface{}{
			"start_time":       time.Now(),
			"task_id":          task.ID,
			"workflow_version": "2.3-response",
			"processing_type":  "comment_response",
		},
	}
}

// prepareWorkflowInputs creates comprehensive inputs for the workflow.
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
		"changes":            changes,
		"file_path":          task.Metadata["file_path"],
		"guidelines":         context["guidelines"],
		"repository_context": context["repository_context"],
		"previous_reviews":   context["previous_reviews"],
		"team_standards":     context["team_standards"],
		"complexity_score":   EstimateCodeComplexity(fileContent),
		"change_scope":       AnalyzeChangeScope(changes),
		"workflow_metadata": map[string]interface{}{
			"start_time":       time.Now(),
			"task_id":          task.ID,
			"workflow_version": "2.0-declarative",
		},
	}
}

// postProcessResults formats and enhances the workflow results.
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
		resultMap["quality_score"] = CalculateOverallQuality(resultMap)
	}

	return resultMap
}

// WorkflowBuilder provides a fluent API for building complex workflows.
type WorkflowBuilder struct {
	stages []interface{}
	config WorkflowConfig
	err    error
}

// WorkflowConfig holds configuration for the workflow.
type WorkflowConfig struct {
	ErrorHandling ErrorHandlingConfig
	Metrics       MetricsConfig
	Tracing       TracingConfig
}

// ErrorHandlingConfig configures error handling for the workflow.
type ErrorHandlingConfig struct {
	Strategy        ErrorStrategy
	MaxRetries      int
	CircuitBreaker  bool
	DeadlineTimeout time.Duration
}

// ErrorStrategy defines error handling approach.
type ErrorStrategy int

const (
	// FailFast stops on first error.
	FailFast ErrorStrategy = iota
	// RetryWithBackoff retries with backoff.
	RetryWithBackoff
	// FallbackToLegacy falls back to legacy processing.
	FallbackToLegacy
	// BestEffort continues despite errors.
	BestEffort
)

// MetricsConfig configures metrics collection.
type MetricsConfig struct {
	Enabled          bool
	DetailedTiming   bool
	StageBreakdown   bool
	ResourceTracking bool
}

// TracingConfig configures distributed tracing.
type TracingConfig struct {
	Enabled     bool
	SampleRate  float64
	TraceStages bool
}

// NewWorkflowBuilder creates a new workflow builder.
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

// Stage adds a sequential stage to the workflow.
func (b *WorkflowBuilder) Stage(stage WorkflowStage) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.stages = append(b.stages, stage)
	return b
}

// Parallel adds a parallel execution stage.
func (b *WorkflowBuilder) Parallel(stage ParallelStage) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.stages = append(b.stages, stage)
	return b
}

// Conditional adds a conditional execution stage.
func (b *WorkflowBuilder) Conditional(stage ConditionalStage) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.stages = append(b.stages, stage)
	return b
}

// WithErrorHandling configures error handling for the workflow.
func (b *WorkflowBuilder) WithErrorHandling(config ErrorHandlingConfig) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.config.ErrorHandling = config
	return b
}

// WithMetrics configures metrics collection.
func (b *WorkflowBuilder) WithMetrics(config MetricsConfig) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.config.Metrics = config
	return b
}

// WithTracing configures distributed tracing.
func (b *WorkflowBuilder) WithTracing(config TracingConfig) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.config.Tracing = config
	return b
}

// Build creates the final workflow.
func (b *WorkflowBuilder) Build() *DeclarativeWorkflow {
	if b.err != nil {
		return nil
	}

	return &DeclarativeWorkflow{
		stages: b.stages,
		config: b.config,
	}
}
