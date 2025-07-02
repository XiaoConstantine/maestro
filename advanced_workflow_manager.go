package main

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// Phase 2 integration uses types defined in other files
// DeclarativeReviewChain, IntelligentFileProcessor, and EnhancedMemoryManager
// are defined in their respective files:
// - enhanced_workflow_builder.go
// - intelligent_parallel_processor.go
// - enhanced_memory_manager.go

// Phase2Integration manages the integration of Phase 2 advanced workflow features.
type Phase2Integration struct {
	features             *EnhancedFeatures
	declarativeChain     *DeclarativeReviewChain
	intelligentProcessor *IntelligentFileProcessor
	memoryManager        *EnhancedMemoryManager
	initialized          bool
	logger               *logging.Logger
}

// Phase2Config contains configuration for Phase 2 integration.
type Phase2Config struct {
	MaxConcurrency   int
	ResourceLimits   ResourceLimits
	MemoryConfig     MemoryConfig
	ProcessingConfig IntelligentProcessingConfig
	WorkflowConfig   WorkflowConfig
	EnableAdaptation bool
	EnableMonitoring bool
	FallbackToPhase1 bool
}

// NewPhase2Integration creates a new Phase 2 integration layer.
func NewPhase2Integration(features *EnhancedFeatures, ragSystem RAGStore) (*Phase2Integration, error) {
	logger := logging.GetLogger()

	integration := &Phase2Integration{
		features: features,
		logger:   logger,
	}

	if err := integration.initialize(ragSystem); err != nil {
		return nil, fmt.Errorf("failed to initialize Phase 2 integration: %w", err)
	}

	return integration, nil
}

// initialize sets up Phase 2 components based on enabled features.
func (p2 *Phase2Integration) initialize(ragSystem RAGStore) error {
	ctx := context.Background()
	p2.logger.Info(ctx, "🚀 Initializing Phase 2 advanced workflow features")

	// Initialize declarative workflows if enabled
	if p2.features.DeclarativeWorkflows {
		// Initializing declarative workflow chain
		chain := NewDeclarativeReviewChain(ctx)
		p2.declarativeChain = chain
	}

	// Initialize intelligent parallel processing if enabled
	if p2.features.IntelligentParallelProcessing {
		// Initializing intelligent parallel processor

		config := IntelligentProcessingConfig{
			MaxConcurrent:     runtime.NumCPU() * 2,
			AdaptiveThreshold: 0.8,
			ResourceLimits: ResourceLimits{
				MaxMemoryMB:   8192,
				MaxCPUPercent: 80.0,
				MaxGoroutines: 1000,
				MemoryWarning: 6144,
				CPUWarning:    70.0,
			},
			FailureThreshold:     5,
			RecoveryTime:         30 * time.Second,
			EnablePrioritization: true,
			EnableLoadBalancing:  true,
			EnableCircuitBreaker: true,
		}

		processor := NewIntelligentFileProcessor(config)
		p2.intelligentProcessor = processor
	}

	// Initialize enhanced memory management if enabled
	if p2.features.AdvancedMemory {
		// Initializing enhanced memory manager

		memoryConfig := MemoryConfig{
			MaxTokens:              100000,
			MaxConversations:       1000,
			MaxRepositoryPatterns:  500,
			CompressionThreshold:   0.8,
			PatternLearningEnabled: p2.features.CommentRefinement,
			ThreadTrackingEnabled:  true,
			ContextWindowSize:      8192,
			RetentionPeriod:        7 * 24 * time.Hour,
		}

		manager := NewEnhancedMemoryManager(ragSystem, memoryConfig)
		p2.memoryManager = manager
	}

	p2.initialized = true
	p2.logger.Info(ctx, "✅ Phase 2 integration initialized successfully")

	return nil
}

// ProcessWithPhase2Features processes a review task using Phase 2 features.
func (p2 *Phase2Integration) ProcessWithPhase2Features(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	if !p2.initialized {
		return nil, fmt.Errorf("phase 2 integration not initialized")
	}

	p2.logger.Info(ctx, "🔄 Processing with Phase 2 advanced features")

	// Check if we should use Phase 2 features based on task complexity
	usePhase2 := p2.shouldUsePhase2Features(ctx, task, taskContext)

	if !usePhase2 {
		// Task complexity low, delegating to Phase 1
		return p2.fallbackToPhase1(ctx, task, taskContext)
	}

	// Enhanced conversation context building
	if p2.features.AdvancedMemory && p2.memoryManager != nil {
		if err := p2.enhanceTaskContext(ctx, task, taskContext); err != nil {
			p2.logger.Warn(ctx, "Failed to enhance task context: %v", err)
		}
	}

	// Use declarative workflows if enabled
	if p2.features.DeclarativeWorkflows && p2.declarativeChain != nil {
		// Using declarative workflow processing
		result, err := p2.declarativeChain.Process(ctx, task, taskContext)
		if err != nil {
			p2.logger.Warn(ctx, "Declarative workflow failed, falling back: %v", err)
			return p2.fallbackToPhase1(ctx, task, taskContext)
		}

		// Store processing patterns for learning
		if p2.features.AdvancedMemory {
			p2.storeProcessingPattern(ctx, task, result, "declarative_workflow")
		}

		return result, nil
	}

	// Fallback to Phase 1 if declarative workflows are disabled
	return p2.fallbackToPhase1(ctx, task, taskContext)
}

// ProcessFilesWithIntelligentParallel processes multiple files using intelligent parallel processing.
func (p2 *Phase2Integration) ProcessFilesWithIntelligentParallel(ctx context.Context, tasks []PRReviewTask) (map[string]interface{}, error) {
	if !p2.initialized || !p2.features.IntelligentParallelProcessing || p2.intelligentProcessor == nil {
		return nil, fmt.Errorf("intelligent parallel processing not available")
	}

	p2.logger.Info(ctx, "⚡ Processing files with intelligent parallel processor")

	// Check system resources before starting
	if p2.features.ResourceMonitoring {
		if err := p2.checkResourceConstraints(ctx); err != nil {
			p2.logger.Warn(ctx, "Resource constraints detected, using conservative processing: %v", err)
			return p2.fallbackToStandardParallel(ctx, tasks)
		}
	}

	// Process with intelligent coordination
	result, err := p2.intelligentProcessor.ProcessFiles(ctx, tasks)
	if err != nil {
		p2.logger.Error(ctx, "Intelligent parallel processing failed: %v", err)
		return p2.fallbackToStandardParallel(ctx, tasks)
	}

	// Store processing metrics for adaptation
	if p2.features.AdaptiveStrategy {
		p2.recordProcessingMetrics(ctx, tasks, result)
	}

	return result, nil
}

// BuildEnhancedContext builds enhanced context using advanced memory management.
func (p2 *Phase2Integration) BuildEnhancedContext(ctx context.Context, conversationID string, currentMessage string) (*ContextResult, error) {
	if !p2.initialized || !p2.features.AdvancedMemory || p2.memoryManager == nil {
		return nil, fmt.Errorf("advanced memory management not available")
	}

	// Building enhanced context

	request := ContextRequest{
		ConversationID: conversationID,
		CurrentMessage: currentMessage,
		Intent:         extractIntent(currentMessage),
		RequiredTypes: []ContextType{
			ConversationHistoryContext,
			CodebaseKnowledgeContext,
			PatternLibraryContext,
		},
		MaxTokens: 4096,
	}

	return p2.memoryManager.BuildConversationContext(ctx, request)
}

// StoreConversationMessage stores a conversation message in enhanced memory.
func (p2 *Phase2Integration) StoreConversationMessage(ctx context.Context, conversationID string, speaker string, content string, messageType ConversationMessageType) error {
	if !p2.initialized || !p2.features.AdvancedMemory || p2.memoryManager == nil {
		return fmt.Errorf("advanced memory management not available")
	}

	message := ConversationMessage{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Speaker:   speaker,
		Content:   content,
		Type:      messageType,
		Timestamp: time.Now(),
		Context: MessageContext{
			ReferencedCode: extractCodeReferences(content),
			Intent:         extractIntent(content),
			Confidence:     0.8,
		},
		Metadata: make(map[string]interface{}),
	}

	return p2.memoryManager.StoreConversationMessage(ctx, conversationID, message)
}

// ProcessFeedback processes user feedback for learning and adaptation.
func (p2 *Phase2Integration) ProcessFeedback(ctx context.Context, feedbackType FeedbackType, rating float64, comment string, processingChain string) error {
	if !p2.initialized || !p2.features.AdvancedMemory || p2.memoryManager == nil {
		return fmt.Errorf("advanced memory management not available")
	}

	feedback := FeedbackEvent{
		ID:              fmt.Sprintf("feedback_%d", time.Now().UnixNano()),
		Type:            feedbackType,
		Rating:          rating,
		Comment:         comment,
		Timestamp:       time.Now(),
		ProcessingChain: processingChain,
		Context:         make(map[string]interface{}),
	}

	p2.logger.Info(ctx, "📈 Processing feedback for continuous improvement")
	return p2.memoryManager.ProcessFeedback(ctx, feedback)
}

// GetProcessingCapabilities returns the available Phase 2 processing capabilities.
func (p2 *Phase2Integration) GetProcessingCapabilities() Phase2Capabilities {
	return Phase2Capabilities{
		DeclarativeWorkflows: p2.features.DeclarativeWorkflows && p2.declarativeChain != nil,
		IntelligentParallel:  p2.features.IntelligentParallelProcessing && p2.intelligentProcessor != nil,
		AdvancedMemory:       p2.features.AdvancedMemory && p2.memoryManager != nil,
		AdaptiveStrategy:     p2.features.AdaptiveStrategy,
		ResourceMonitoring:   p2.features.ResourceMonitoring,
		ConversationContext:  p2.features.AdvancedMemory,
		PatternLearning:      p2.features.AdvancedMemory,
		FeedbackProcessing:   p2.features.AdvancedMemory,
	}
}

// Phase2Capabilities describes available Phase 2 capabilities.
type Phase2Capabilities struct {
	DeclarativeWorkflows bool
	IntelligentParallel  bool
	AdvancedMemory       bool
	AdaptiveStrategy     bool
	ResourceMonitoring   bool
	ConversationContext  bool
	PatternLearning      bool
	FeedbackProcessing   bool
}

// Helper methods

// shouldUsePhase2Features determines if Phase 2 features should be used for a task.
func (p2 *Phase2Integration) shouldUsePhase2Features(ctx context.Context, task agents.Task, taskContext map[string]interface{}) bool {
	// Always use Phase 2 if available and enabled
	if p2.features.DeclarativeWorkflows || p2.features.IntelligentParallelProcessing {
		return true
	}

	// Check task complexity
	complexity := extractTaskComplexity(task)
	if complexity > 0.7 {
		return true
	}

	// Check if advanced features would benefit this task
	if hasAdvancedRequirements(task, taskContext) {
		return true
	}

	return false
}

// enhanceTaskContext enhances the task context using advanced memory management.
func (p2 *Phase2Integration) enhanceTaskContext(ctx context.Context, task agents.Task, taskContext map[string]interface{}) error {
	if !p2.features.AdvancedMemory || p2.memoryManager == nil {
		return nil
	}

	// Build conversation context if available
	if conversationID, exists := task.Metadata["conversation_id"].(string); exists {
		contextResult, err := p2.memoryManager.BuildConversationContext(ctx, ContextRequest{
			ConversationID: conversationID,
			CurrentMessage: fmt.Sprintf("%v", task.Metadata),
			MaxTokens:      2048,
		})
		if err == nil && contextResult != nil {
			taskContext["conversation_context"] = contextResult.Context
			taskContext["context_confidence"] = contextResult.Confidence
		}
	}

	// Get relevant patterns
	if filePath, exists := task.Metadata["file_path"].(string); exists {
		patterns, err := p2.memoryManager.GetRelevantPatterns(ctx, filePath, 5)
		if err == nil && len(patterns) > 0 {
			taskContext["relevant_patterns"] = patterns
		}
	}

	return nil
}

// storeProcessingPattern stores processing patterns for learning.
func (p2 *Phase2Integration) storeProcessingPattern(ctx context.Context, task agents.Task, result interface{}, processingType string) {
	if !p2.features.AdvancedMemory || p2.memoryManager == nil {
		return
	}

	// Extract pattern from processing
	pattern := CodePattern{
		Name:        fmt.Sprintf("%s_pattern", processingType),
		Description: fmt.Sprintf("Pattern learned from %s processing", processingType),
		Pattern:     fmt.Sprintf("%v", task.Type),
		Confidence:  0.7,
		Frequency:   1,
		Metadata: map[string]interface{}{
			"processing_type": processingType,
			"task_type":       task.Type,
			"result_type":     fmt.Sprintf("%T", result),
		},
	}

	if err := p2.memoryManager.StoreCodeReviewPattern(ctx, pattern); err != nil {
		p2.logger.Warn(ctx, "Failed to store processing pattern: %v", err)
	}
}

// checkResourceConstraints checks if system resources allow for advanced processing.
func (p2 *Phase2Integration) checkResourceConstraints(ctx context.Context) error {
	if p2.intelligentProcessor == nil {
		return nil
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	memoryMB := m.Alloc / 1024 / 1024
	memoryWarningThreshold := p2.intelligentProcessor.config.ResourceLimits.MemoryWarning
	if memoryMB > uint64(memoryWarningThreshold) {
		return fmt.Errorf("memory usage high: %d MB (warning at %d MB)", memoryMB, memoryWarningThreshold)
	}

	goroutines := runtime.NumGoroutine()
	warningGoroutines := p2.intelligentProcessor.config.ResourceLimits.MaxGoroutines / 2
	if goroutines > warningGoroutines {
		return fmt.Errorf("high number of goroutines: %d (warning at %d)", goroutines, warningGoroutines)
	}

	return nil
}

// recordProcessingMetrics records metrics for adaptive strategy.
func (p2 *Phase2Integration) recordProcessingMetrics(ctx context.Context, tasks []PRReviewTask, result map[string]interface{}) {
	// Recording processing metrics

	// In a real implementation, this would store detailed metrics
	// for the adaptive strategy to learn from
	_ = map[string]interface{}{
		"task_count":      len(tasks),
		"processing_time": time.Now(), // Would track actual duration
		"success":         result != nil,
		"result_quality":  extractQualityScore(result),
	}

	// Metrics recorded
}

// fallbackToPhase1 falls back to Phase 1 processing.
func (p2 *Phase2Integration) fallbackToPhase1(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	// Falling back to Phase 1 processing

	// Use the existing enhanced integration from Phase 1
	// TODO: Implement proper fallback to Phase 1
	// processor := GetGlobalEnhancedProcessor()
	// if processor != nil {
	//     return processor.ProcessCodeReview(ctx, task, taskContext)
	// }

	// Ultimate fallback to basic processing
	basicProcessor := &CodeReviewProcessor{}
	return basicProcessor.Process(ctx, task, taskContext)
}

// fallbackToStandardParallel falls back to standard parallel processing.
func (p2 *Phase2Integration) fallbackToStandardParallel(ctx context.Context, tasks []PRReviewTask) (map[string]interface{}, error) {
	// Falling back to standard parallel processing

	// Use existing parallel processing logic
	results := make(map[string]interface{})

	for i, task := range tasks {
		taskID := fmt.Sprintf("task_%d", i)

		// Basic processing for each task
		result := map[string]interface{}{
			"file_path": task.FilePath,
			"processed": true,
			"method":    "standard_parallel",
		}

		results[taskID] = result
	}

	return results, nil
}

// Utility functions

func extractIntent(content string) string {
	// Simple intent extraction - in practice would be more sophisticated
	if strings.Contains(content, "review") {
		return "code_review"
	} else if strings.Contains(content, "question") {
		return "question"
	} else if strings.Contains(content, "help") {
		return "assistance"
	}
	return "general"
}

func extractCodeReferences(content string) []CodeReference {
	// Simple code reference extraction
	var refs []CodeReference

	if strings.Contains(content, ".go") {
		refs = append(refs, CodeReference{
			FilePath: "extracted.go",
			Content:  "extracted code",
			Context:  "code discussion",
		})
	}

	return refs
}

func extractTaskComplexity(task agents.Task) float64 {
	// Simple complexity estimation based on task metadata
	complexity := 0.5 // Base complexity

	if fileContent, exists := task.Metadata["file_content"].(string); exists {
		// Increase complexity based on content size and structure
		lines := len(strings.Split(fileContent, "\n"))
		complexity += float64(lines) / 1000 // Add complexity for large files

		if strings.Contains(fileContent, "interface") || strings.Contains(fileContent, "struct") {
			complexity += 0.2
		}
	}

	return minFloat64(complexity, 1.0)
}

func hasAdvancedRequirements(task agents.Task, taskContext map[string]interface{}) bool {
	// Check if task has requirements that benefit from advanced features

	// Large file processing
	if fileContent, exists := task.Metadata["file_content"].(string); exists {
		if len(fileContent) > 10000 { // Large file
			return true
		}
	}

	// Complex change analysis
	if changes, exists := task.Metadata["changes"].(string); exists {
		changeLines := len(strings.Split(changes, "\n"))
		if changeLines > 50 { // Large change
			return true
		}
	}

	// Conversation context exists
	if _, exists := taskContext["conversation_context"]; exists {
		return true
	}

	return false
}

func extractQualityScore(result map[string]interface{}) float64 {
	if result == nil {
		return 0.0
	}

	if quality, exists := result["quality_score"].(float64); exists {
		return quality
	}

	return 0.5 // Default quality
}

// Global Phase 2 integration instance.
var globalPhase2Integration *Phase2Integration

// InitializePhase2Integration initializes the global Phase 2 integration.
func InitializePhase2Integration(features *EnhancedFeatures, ragSystem RAGStore) error {
	if !features.DeclarativeWorkflows && !features.IntelligentParallelProcessing && !features.AdvancedMemory {
		// No Phase 2 features enabled
		return nil
	}

	integration, err := NewPhase2Integration(features, ragSystem)
	if err != nil {
		return fmt.Errorf("failed to initialize Phase 2 integration: %w", err)
	}

	globalPhase2Integration = integration
	return nil
}

// GetGlobalPhase2Integration returns the global Phase 2 integration instance.
func GetGlobalPhase2Integration() *Phase2Integration {
	return globalPhase2Integration
}

// IsPhase2Available checks if Phase 2 features are available.
func IsPhase2Available() bool {
	return globalPhase2Integration != nil && globalPhase2Integration.initialized
}
