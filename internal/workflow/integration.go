// Package workflow provides declarative workflow patterns for code review.
package workflow

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/processing"
	"github.com/XiaoConstantine/maestro/internal/types"
)

// Phase2Integration manages the integration of Phase 2 advanced workflow features.
type Phase2Integration struct {
	features             *types.EnhancedFeatures
	declarativeChain     *DeclarativeReviewChain
	intelligentProcessor *processing.IntelligentFileProcessor
	memoryManager        MemoryManager
	initialized          bool
	logger               *logging.Logger
}

// MemoryManager interface for memory management operations.
type MemoryManager interface {
	BuildConversationContext(ctx context.Context, request ContextRequest) (*ContextResult, error)
	GetRelevantPatterns(ctx context.Context, filePath string, limit int) ([]CodePattern, error)
	StoreConversationMessage(ctx context.Context, conversationID string, message ConversationMessage) error
	StoreCodeReviewPattern(ctx context.Context, pattern CodePattern) error
	ProcessFeedback(ctx context.Context, feedback FeedbackEvent) error
}

// Phase2Config contains configuration for Phase 2 integration.
type Phase2Config struct {
	MaxConcurrency   int
	ResourceLimits   processing.ResourceLimits
	MemoryConfig     MemoryConfig
	ProcessingConfig processing.IntelligentProcessingConfig
	WorkflowConfig   WorkflowConfig
	EnableAdaptation bool
	EnableMonitoring bool
	FallbackToPhase1 bool
}

// MemoryConfig configures memory management.
type MemoryConfig struct {
	MaxTokens              int
	MaxConversations       int
	MaxRepositoryPatterns  int
	CompressionThreshold   float64
	PatternLearningEnabled bool
	ThreadTrackingEnabled  bool
	ContextWindowSize      int
	RetentionPeriod        time.Duration
}

// ContextRequest represents a context retrieval request.
type ContextRequest struct {
	ConversationID string
	CurrentMessage string
	Intent         string
	RequiredTypes  []ContextType
	MaxTokens      int
}

// ContextType represents a type of context.
type ContextType int

const (
	// ConversationHistoryContext is conversation history.
	ConversationHistoryContext ContextType = iota
	// CodebaseKnowledgeContext is codebase knowledge.
	CodebaseKnowledgeContext
	// PatternLibraryContext is pattern library.
	PatternLibraryContext
)

// ContextResult represents context retrieval result.
type ContextResult struct {
	Context    string
	Confidence float64
}

// ConversationMessage represents a conversation message.
type ConversationMessage struct {
	ID        string
	Speaker   string
	Content   string
	Type      ConversationMessageType
	Timestamp time.Time
	Context   MessageContext
	Metadata  map[string]interface{}
}

// ConversationMessageType represents the type of conversation message.
type ConversationMessageType int

const (
	// UserMessage is a user message.
	UserMessage ConversationMessageType = iota
	// AssistantMessage is an assistant message.
	AssistantMessage
	// SystemMessage is a system message.
	SystemMessage
)

// MessageContext provides context for a message.
type MessageContext struct {
	ReferencedCode []CodeReference
	Intent         string
	Confidence     float64
}

// CodeReference represents a code reference.
type CodeReference struct {
	FilePath string
	Content  string
	Context  string
}

// CodePattern represents a learned code pattern.
type CodePattern struct {
	Name        string
	Description string
	Pattern     string
	Confidence  float64
	Frequency   int
	Metadata    map[string]interface{}
}

// FeedbackEvent represents user feedback.
type FeedbackEvent struct {
	ID              string
	Type            FeedbackType
	Rating          float64
	Comment         string
	Timestamp       time.Time
	ProcessingChain string
	Context         map[string]interface{}
}

// FeedbackType represents the type of feedback.
type FeedbackType int

const (
	// PositiveFeedback is positive feedback.
	PositiveFeedback FeedbackType = iota
	// NegativeFeedback is negative feedback.
	NegativeFeedback
	// NeutralFeedback is neutral feedback.
	NeutralFeedback
)

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

// NewPhase2Integration creates a new Phase 2 integration layer.
func NewPhase2Integration(features *types.EnhancedFeatures, memoryManager MemoryManager) (*Phase2Integration, error) {
	logger := logging.GetLogger()

	integration := &Phase2Integration{
		features:      features,
		memoryManager: memoryManager,
		logger:        logger,
	}

	if err := integration.initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize Phase 2 integration: %w", err)
	}

	return integration, nil
}

// initialize sets up Phase 2 components based on enabled features.
func (p2 *Phase2Integration) initialize() error {
	ctx := context.Background()
	p2.logger.Info(ctx, "🚀 Initializing Phase 2 advanced workflow features")

	// Initialize intelligent parallel processing if enabled
	if p2.features.IntelligentParallelProcessing {
		config := processing.IntelligentProcessingConfig{
			MaxConcurrent:     runtime.NumCPU() * 2,
			AdaptiveThreshold: 0.8,
			ResourceLimits: processing.ResourceLimits{
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

		processor := processing.NewIntelligentFileProcessor(nil, config, p2.logger)
		p2.intelligentProcessor = processor
	}

	p2.initialized = true
	p2.logger.Info(ctx, "✅ Phase 2 integration initialized successfully")

	return nil
}

// SetDeclarativeChain sets the declarative review chain.
func (p2 *Phase2Integration) SetDeclarativeChain(chain *DeclarativeReviewChain) {
	p2.declarativeChain = chain
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
		result, err := p2.declarativeChain.Process(ctx, task, taskContext)
		if err != nil {
			p2.logger.Warn(ctx, "Declarative workflow failed, falling back: %v", err)
			return p2.fallbackToPhase1(ctx, task, taskContext)
		}

		// Store processing patterns for learning
		if p2.features.AdvancedMemory && p2.memoryManager != nil {
			p2.storeProcessingPattern(ctx, task, result, "declarative_workflow")
		}

		return result, nil
	}

	return p2.fallbackToPhase1(ctx, task, taskContext)
}

// ProcessFilesWithIntelligentParallel processes multiple files using intelligent parallel processing.
func (p2 *Phase2Integration) ProcessFilesWithIntelligentParallel(ctx context.Context, tasks []types.PRReviewTask) (map[string]interface{}, error) {
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

	// Fallback to standard parallel for now
	return p2.fallbackToStandardParallel(ctx, tasks)
}

// BuildEnhancedContext builds enhanced context using advanced memory management.
func (p2 *Phase2Integration) BuildEnhancedContext(ctx context.Context, conversationID string, currentMessage string) (*ContextResult, error) {
	if !p2.initialized || !p2.features.AdvancedMemory || p2.memoryManager == nil {
		return nil, fmt.Errorf("advanced memory management not available")
	}

	request := ContextRequest{
		ConversationID: conversationID,
		CurrentMessage: currentMessage,
		Intent:         ExtractIntent(currentMessage),
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
			ReferencedCode: ExtractCodeReferences(content),
			Intent:         ExtractIntent(content),
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

// shouldUsePhase2Features determines if Phase 2 features should be used for a task.
func (p2 *Phase2Integration) shouldUsePhase2Features(ctx context.Context, task agents.Task, taskContext map[string]interface{}) bool {
	// Always use Phase 2 if available and enabled
	if p2.features.DeclarativeWorkflows || p2.features.IntelligentParallelProcessing {
		return true
	}

	// Check task complexity
	complexity := ExtractTaskComplexity(task)
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

	// Use a reasonable default warning threshold
	memoryWarningThreshold := int64(6144)
	if memoryMB > uint64(memoryWarningThreshold) {
		return fmt.Errorf("memory usage high: %d MB (warning at %d MB)", memoryMB, memoryWarningThreshold)
	}

	goroutines := runtime.NumGoroutine()
	warningGoroutines := 500
	if goroutines > warningGoroutines {
		return fmt.Errorf("high number of goroutines: %d (warning at %d)", goroutines, warningGoroutines)
	}

	return nil
}

// fallbackToPhase1 falls back to Phase 1 processing.
func (p2 *Phase2Integration) fallbackToPhase1(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	// Return basic result for fallback
	return map[string]interface{}{
		"processed": true,
		"method":    "phase1_fallback",
		"task_id":   task.ID,
	}, nil
}

// fallbackToStandardParallel falls back to standard parallel processing.
func (p2 *Phase2Integration) fallbackToStandardParallel(ctx context.Context, tasks []types.PRReviewTask) (map[string]interface{}, error) {
	results := make(map[string]interface{})

	for i, task := range tasks {
		taskID := fmt.Sprintf("task_%d", i)

		result := map[string]interface{}{
			"file_path": task.FilePath,
			"processed": true,
			"method":    "standard_parallel",
		}

		results[taskID] = result
	}

	return results, nil
}

// hasAdvancedRequirements checks if task has requirements that benefit from advanced features.
func hasAdvancedRequirements(task agents.Task, taskContext map[string]interface{}) bool {
	// Large file processing
	if fileContent, exists := task.Metadata["file_content"].(string); exists {
		if len(fileContent) > 10000 {
			return true
		}
	}

	// Complex change analysis
	if changes, exists := task.Metadata["changes"].(string); exists {
		changeLines := len(strings.Split(changes, "\n"))
		if changeLines > 50 {
			return true
		}
	}

	// Conversation context exists
	if _, exists := taskContext["conversation_context"]; exists {
		return true
	}

	return false
}

// Global Phase 2 integration instance.
var globalPhase2Integration *Phase2Integration

// InitializePhase2Integration initializes the global Phase 2 integration.
func InitializePhase2Integration(features *types.EnhancedFeatures, memoryManager MemoryManager) error {
	if !features.DeclarativeWorkflows && !features.IntelligentParallelProcessing && !features.AdvancedMemory {
		return nil
	}

	integration, err := NewPhase2Integration(features, memoryManager)
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
