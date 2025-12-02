// Package workflow provides declarative workflow patterns for code review.
package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// DeclarativeWorkflow implements the workflow execution engine.
type DeclarativeWorkflow struct {
	stages []interface{}
	config WorkflowConfig
}

// StepResult represents the result of a parallel step execution.
type StepResult struct {
	Index  int
	Name   string
	Result interface{}
	Error  error
}

// Execute runs the declarative workflow.
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

// ExecuteFirstStage runs only the first stage of the workflow.
func (w *DeclarativeWorkflow) ExecuteFirstStage(ctx context.Context, inputs map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()

	if len(w.stages) == 0 {
		return inputs, nil
	}

	// Set up context with timeout
	workflowCtx, cancel := context.WithTimeout(ctx, w.config.ErrorHandling.DeadlineTimeout)
	defer cancel()

	stageResult, err := w.executeStage(workflowCtx, "stage_1", w.stages[0], inputs)
	if err != nil {
		return w.handleStageError(workflowCtx, "stage_1", err, inputs)
	}

	logger.Info(ctx, "✅ First stage completed successfully")
	return w.mergeResults(inputs, stageResult), nil
}

// executeStage handles execution of different stage types.
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

// executeSequentialStage executes a single sequential stage.
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

// executeParallelStage executes parallel steps with sophisticated coordination.
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

// executeParallelStep executes a single step in a parallel stage.
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

// collectParallelResults aggregates parallel execution results based on strategy.
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

// executeConditionalStage executes conditional logic.
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
	}
	logger.Debug(ctx, "❌ Condition false, no ELSE branch, passing through")
	return inputs, nil
}

// executeWithRetry implements sophisticated retry logic.
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

// handleStageError handles errors during stage execution.
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

// executeLegacyFallback provides fallback processing.
func (w *DeclarativeWorkflow) executeLegacyFallback(ctx context.Context, inputs map[string]interface{}) (interface{}, error) {
	// Return inputs as-is for legacy fallback
	return inputs, nil
}

// mergeResults merges base results with new results.
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

// mergeParallelResults merges results from parallel execution.
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

// isRetryableError checks if an error is retryable.
func (w *DeclarativeWorkflow) isRetryableError(err error, retryableErrors []string) bool {
	errStr := err.Error()
	for _, retryableErr := range retryableErrors {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(retryableErr)) {
			return true
		}
	}
	return false
}

// calculateBackoff calculates backoff duration based on strategy.
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
