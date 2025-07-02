package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// EnhancedMemoryManager provides sophisticated memory management for conversations and patterns.
type EnhancedMemoryManager struct {
	conversationalMemory *ConversationalMemory
	repositoryMemory     *RepositoryMemory
	threadMemory         *ThreadAwareMemory
	patternLearner       *PatternLearner
	contextBuilder       *ContextBuilder
	compressionEngine    *CompressionEngine
	logger               *logging.Logger
	config               MemoryConfig
}

// MemoryConfig configures the enhanced memory system.
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

// ConversationalMemory manages conversation context and history.
type ConversationalMemory struct {
	conversations map[string]*Conversation
	globalContext *GlobalContext
	compression   *CompressionEngine
	mu            sync.RWMutex
	config        ConversationalConfig
}

type ConversationalConfig struct {
	MaxTokensPerConversation int
	CompressionStrategy      CompressionStrategy
	ContextDecayRate         float64
}

type CompressionStrategy int

const (
	SummarizeOldest CompressionStrategy = iota
	KeepMostRelevant
	HierarchicalCompression
	SemanticClustering
)

// Conversation represents a single conversation thread.
type Conversation struct {
	ID               string
	Participants     []string
	Messages         []ConversationMessage
	Context          ConversationContext
	CreatedAt        time.Time
	LastActivity     time.Time
	TokenCount       int
	CompressionLevel int
	Metadata         map[string]interface{}
}

// ConversationMessage represents a single message in a conversation.
type ConversationMessage struct {
	ID        string
	Speaker   string
	Content   string
	Type      ConversationMessageType
	Timestamp time.Time
	Context   MessageContext
	Metadata  map[string]interface{}
}

type ConversationMessageType int

const (
	UserMessage ConversationMessageType = iota
	AssistantMessage
	SystemMessage
	ToolMessage
	ErrorMessage
)

// ConversationContext maintains conversation-level context.
type ConversationContext struct {
	Topic           string
	PrimaryIntent   string
	CurrentTask     string
	ReferencedFiles []string
	KeyEntities     []string
	SentimentScore  float64
	ComplexityLevel int
}

// MessageContext maintains message-level context.
type MessageContext struct {
	ReplyToID      string
	ReferencedCode []CodeReference
	Emotions       []string
	Intent         string
	Confidence     float64
}

// CodeReference represents a reference to code in the conversation.
type CodeReference struct {
	FilePath  string
	StartLine int
	EndLine   int
	Content   string
	Context   string
}

// RepositoryMemory manages repository-specific knowledge and patterns.
type RepositoryMemory struct {
	patterns        map[string]*CodePattern
	codebaseContext *CodebaseContext
	learningHistory []LearningEvent
	mu              sync.RWMutex
	config          RepositoryConfig
}

type RepositoryConfig struct {
	MaxPatterns              int
	PatternDecayRate         float64
	LearningThreshold        float64
	ContextUpdateInterval    time.Duration
	EnableSemanticClustering bool
}

// CodePattern represents a learned pattern in the codebase.
type CodePattern struct {
	ID              string
	Name            string
	Description     string
	Pattern         string
	Examples        []CodeExample
	Frequency       int
	Confidence      float64
	LastSeen        time.Time
	RelatedPatterns []string
	Metadata        map[string]interface{}
}

// CodebaseContext maintains overall codebase understanding.
type CodebaseContext struct {
	Architecture    ArchitectureInfo
	CodingStandards []CodingStandard
	CommonPatterns  []string
	TechnicalDebt   []TechnicalDebtItem
	QualityMetrics  QualityMetrics
	LastUpdated     time.Time
}

// ArchitectureInfo describes the codebase architecture.
type ArchitectureInfo struct {
	Language         string
	Framework        string
	ArchitectureType string
	KeyComponents    []string
	Dependencies     []string
	Patterns         []string
}

// ThreadAwareMemory manages multi-threaded conversations.
type ThreadAwareMemory struct {
	threads         map[string]*ConversationThread
	threadRelations map[string][]string
	globalMemory    *ConversationalMemory
	mu              sync.RWMutex
	config          ThreadConfig
}

type ThreadConfig struct {
	MaxThreads       int
	ThreadTTL        time.Duration
	CrossThreadLimit int
}

// ConversationThread represents a thread of related conversations.
type ConversationThread struct {
	ID            string
	Name          string
	Conversations []string
	SharedContext ThreadContext
	CreatedAt     time.Time
	LastActivity  time.Time
	Participants  []string
}

// ThreadContext maintains thread-level context.
type ThreadContext struct {
	MainTopic        string
	SubTopics        []string
	SharedEntities   []string
	CumulativeIntent string
	ThreadSummary    string
}

// PatternLearner learns from interactions and feedback.
type PatternLearner struct {
	feedbackHistory []FeedbackEvent
	learnedPatterns map[string]*LearnedPattern
	mu              sync.RWMutex
	config          LearningConfig
}

type LearningConfig struct {
	FeedbackWindow      int
	LearningRate        float64
	PatternThreshold    float64
	AdaptationThreshold float64
	EnableReinforcement bool
}

// FeedbackEvent represents user feedback.
type FeedbackEvent struct {
	ID              string
	Type            FeedbackType
	Rating          float64
	Comment         string
	Context         map[string]interface{}
	Timestamp       time.Time
	ProcessingChain string
}

type FeedbackType int

const (
	PositiveFeedback FeedbackType = iota
	NegativeFeedback
	NeutralFeedback
	CorrectionFeedback
)

// LearnedPattern represents a pattern learned from feedback.
type LearnedPattern struct {
	ID          string
	Pattern     string
	Confidence  float64
	Occurrences int
	LastSeen    time.Time
	Impact      float64
}

// ContextBuilder constructs intelligent context for conversations.
type ContextBuilder struct {
	memory            *EnhancedMemoryManager
	ragSystem         RAGStore
	contextStrategies []ContextStrategy
	config            ContextConfig
}

type ContextConfig struct {
	MaxContextTokens   int
	RelevanceThreshold float64
	IncludeHistory     bool
	IncludePatterns    bool
	IncludeCodebase    bool
}

// ContextStrategy defines how to build context.
type ContextStrategy interface {
	BuildContext(ctx context.Context, request ContextRequest) (*ContextResult, error)
	Priority() int
	Name() string
}

// ContextRequest represents a request for context.
type ContextRequest struct {
	ConversationID string
	ThreadID       string
	CurrentMessage string
	Intent         string
	RequiredTypes  []ContextType
	MaxTokens      int
}

type ContextType int

const (
	ConversationHistoryContext ContextType = iota
	CodebaseKnowledgeContext
	PatternLibraryContext
	ThreadSummaryContext
	GlobalKnowledgeContext
)

// ContextResult contains built context.
type ContextResult struct {
	Context      string
	Sources      []ContextSource
	Confidence   float64
	TokenCount   int
	ContextTypes []ContextType
}

// ContextSource describes the source of context information.
type ContextSource struct {
	Type        ContextType
	Source      string
	Confidence  float64
	TokenCount  int
	LastUpdated time.Time
}

// CompressionEngine handles intelligent memory compression.
type CompressionEngine struct {
	strategies map[CompressionStrategy]CompressionAlgorithm
	config     CompressionConfig
}

type CompressionConfig struct {
	CompressionRatio     float64
	PreserveCriticalInfo bool
	SemanticClustering   bool
	HierarchicalSummary  bool
}

// CompressionAlgorithm defines compression behavior.
type CompressionAlgorithm interface {
	Compress(ctx context.Context, content string, ratio float64) (string, error)
	Decompress(ctx context.Context, compressed string) (string, error)
	EstimateCompression(content string) float64
}

// NewEnhancedMemoryManager creates a sophisticated memory management system.
func NewEnhancedMemoryManager(ragSystem RAGStore, config MemoryConfig) *EnhancedMemoryManager {
	logger := logging.GetLogger()

	// Create compression engine
	compressionEngine := NewCompressionEngine()

	// Create conversational memory
	conversationalMemory := NewConversationalMemory(ConversationalConfig{
		MaxTokensPerConversation: config.MaxTokens / 10,
		CompressionStrategy:      HierarchicalCompression,
		ContextDecayRate:         0.1,
	}, compressionEngine)

	// Create repository memory
	repositoryMemory := NewRepositoryMemory(RepositoryConfig{
		MaxPatterns:              config.MaxRepositoryPatterns,
		PatternDecayRate:         0.05,
		LearningThreshold:        0.7,
		ContextUpdateInterval:    time.Hour,
		EnableSemanticClustering: true,
	})

	// Create thread-aware memory
	threadMemory := NewThreadAwareMemory(ThreadConfig{
		MaxThreads:       100,
		ThreadTTL:        24 * time.Hour,
		CrossThreadLimit: 5,
	}, conversationalMemory)

	// Create pattern learner
	patternLearner := NewPatternLearner(LearningConfig{
		FeedbackWindow:      100,
		LearningRate:        0.1,
		PatternThreshold:    0.8,
		AdaptationThreshold: 0.6,
		EnableReinforcement: true,
	})

	manager := &EnhancedMemoryManager{
		conversationalMemory: conversationalMemory,
		repositoryMemory:     repositoryMemory,
		threadMemory:         threadMemory,
		patternLearner:       patternLearner,
		compressionEngine:    compressionEngine,
		logger:               logger,
		config:               config,
	}

	// Create context builder
	manager.contextBuilder = NewContextBuilder(manager, ragSystem, ContextConfig{
		MaxContextTokens:   config.ContextWindowSize,
		RelevanceThreshold: 0.7,
		IncludeHistory:     true,
		IncludePatterns:    config.PatternLearningEnabled,
		IncludeCodebase:    true,
	})

	return manager
}

// StoreConversationMessage stores a message in the appropriate conversation.
func (m *EnhancedMemoryManager) StoreConversationMessage(ctx context.Context, conversationID string, message ConversationMessage) error {
	m.logger.Debug(ctx, "💭 Storing conversation message in %s", conversationID)

	// Store in conversational memory
	if err := m.conversationalMemory.AddMessage(ctx, conversationID, message); err != nil {
		return fmt.Errorf("failed to store conversation message: %w", err)
	}

	// Update thread context if applicable
	if m.config.ThreadTrackingEnabled {
		if err := m.threadMemory.UpdateThreadContext(ctx, conversationID, message); err != nil {
			m.logger.Warn(ctx, "Failed to update thread context: %v", err)
		}
	}

	// Extract and store patterns if enabled
	if m.config.PatternLearningEnabled {
		if err := m.extractAndStorePatterns(ctx, message); err != nil {
			m.logger.Warn(ctx, "Failed to extract patterns: %v", err)
		}
	}

	return nil
}

// BuildConversationContext builds intelligent context for a conversation.
func (m *EnhancedMemoryManager) BuildConversationContext(ctx context.Context, request ContextRequest) (*ContextResult, error) {
	m.logger.Debug(ctx, "🔍 Building conversation context for %s", request.ConversationID)

	return m.contextBuilder.BuildContext(ctx, request)
}

// StoreCodeReviewPattern stores a learned pattern from code review.
func (m *EnhancedMemoryManager) StoreCodeReviewPattern(ctx context.Context, pattern CodePattern) error {
	m.logger.Debug(ctx, "📚 Storing code review pattern: %s", pattern.Name)

	return m.repositoryMemory.StorePattern(ctx, pattern)
}

// GetRelevantPatterns retrieves patterns relevant to the current context.
func (m *EnhancedMemoryManager) GetRelevantPatterns(ctx context.Context, codeContext string, maxResults int) ([]CodePattern, error) {
	return m.repositoryMemory.GetRelevantPatterns(ctx, codeContext, maxResults)
}

// ProcessFeedback processes user feedback to improve future interactions.
func (m *EnhancedMemoryManager) ProcessFeedback(ctx context.Context, feedback FeedbackEvent) error {
	m.logger.Info(ctx, "📈 Processing feedback event: %d", int(feedback.Type))

	// Store feedback
	if err := m.patternLearner.ProcessFeedback(ctx, feedback); err != nil {
		return fmt.Errorf("failed to process feedback: %w", err)
	}

	// Update patterns based on feedback
	if err := m.adaptBasedOnFeedback(ctx, feedback); err != nil {
		m.logger.Warn(ctx, "Failed to adapt based on feedback: %v", err)
	}

	return nil
}

// GetConversationHistory retrieves conversation history with intelligent filtering.
func (m *EnhancedMemoryManager) GetConversationHistory(ctx context.Context, conversationID string, maxMessages int) ([]ConversationMessage, error) {
	return m.conversationalMemory.GetHistory(ctx, conversationID, maxMessages)
}

// CreateConversationThread creates a new conversation thread.
func (m *EnhancedMemoryManager) CreateConversationThread(ctx context.Context, name string, participants []string) (string, error) {
	return m.threadMemory.CreateThread(ctx, name, participants)
}

// LinkConversationsToThread links conversations to a thread.
func (m *EnhancedMemoryManager) LinkConversationsToThread(ctx context.Context, threadID string, conversationIDs []string) error {
	return m.threadMemory.LinkConversations(ctx, threadID, conversationIDs)
}

// CompressMemory compresses memory when approaching limits.
func (m *EnhancedMemoryManager) CompressMemory(ctx context.Context) error {
	m.logger.Info(ctx, "🗜️ Starting memory compression")

	// Compress conversations
	if err := m.conversationalMemory.Compress(ctx); err != nil {
		return fmt.Errorf("failed to compress conversations: %w", err)
	}

	// Compress repository patterns
	if err := m.repositoryMemory.Compress(ctx); err != nil {
		return fmt.Errorf("failed to compress repository patterns: %w", err)
	}

	m.logger.Info(ctx, "✅ Memory compression completed")
	return nil
}

// UpdateCodebaseContext updates the overall codebase understanding.
func (m *EnhancedMemoryManager) UpdateCodebaseContext(ctx context.Context, context CodebaseContext) error {
	return m.repositoryMemory.UpdateCodebaseContext(ctx, context)
}

// Implementation of component interfaces

// ConversationalMemory implementation.
func NewConversationalMemory(config ConversationalConfig, compression *CompressionEngine) *ConversationalMemory {
	return &ConversationalMemory{
		conversations: make(map[string]*Conversation),
		globalContext: NewGlobalContext(),
		compression:   compression,
		config:        config,
	}
}

func (cm *ConversationalMemory) AddMessage(ctx context.Context, conversationID string, message ConversationMessage) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	conversation, exists := cm.conversations[conversationID]
	if !exists {
		conversation = &Conversation{
			ID:        conversationID,
			Messages:  []ConversationMessage{},
			Context:   ConversationContext{},
			CreatedAt: time.Now(),
			Metadata:  make(map[string]interface{}),
		}
		cm.conversations[conversationID] = conversation
	}

	// Add message
	conversation.Messages = append(conversation.Messages, message)
	conversation.LastActivity = time.Now()
	conversation.TokenCount += len(message.Content) / 4 // Rough token estimate

	// Update conversation context
	cm.updateConversationContext(conversation, message)

	// Check for compression need
	if conversation.TokenCount > cm.config.MaxTokensPerConversation {
		if err := cm.compressConversation(ctx, conversation); err != nil {
			return fmt.Errorf("compression failed: %w", err)
		}
	}

	return nil
}

func (cm *ConversationalMemory) GetHistory(ctx context.Context, conversationID string, maxMessages int) ([]ConversationMessage, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	conversation, exists := cm.conversations[conversationID]
	if !exists {
		return []ConversationMessage{}, nil
	}

	messages := conversation.Messages
	if len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}

	return messages, nil
}

func (cm *ConversationalMemory) Compress(ctx context.Context) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for _, conversation := range cm.conversations {
		if err := cm.compressConversation(ctx, conversation); err != nil {
			return err
		}
	}

	return nil
}

func (cm *ConversationalMemory) updateConversationContext(conversation *Conversation, message ConversationMessage) {
	// Update topic if not set
	if conversation.Context.Topic == "" {
		conversation.Context.Topic = extractTopic(message.Content)
	}

	// Update referenced files
	if refs := extractFileReferences(message.Content); len(refs) > 0 {
		conversation.Context.ReferencedFiles = append(conversation.Context.ReferencedFiles, refs...)
	}

	// Update sentiment
	conversation.Context.SentimentScore = calculateSentiment(message.Content)
}

func (cm *ConversationalMemory) compressConversation(ctx context.Context, conversation *Conversation) error {
	if len(conversation.Messages) <= 5 {
		return nil // Don't compress small conversations
	}

	// Use compression strategy
	strategy := cm.compression.strategies[cm.config.CompressionStrategy]

	// Compress older messages
	messagesToCompress := conversation.Messages[:len(conversation.Messages)/2]
	content := cm.messagesToText(messagesToCompress)

	compressed, err := strategy.Compress(ctx, content, 0.5)
	if err != nil {
		return err
	}

	// Replace compressed messages with summary
	summaryMessage := ConversationMessage{
		ID:        fmt.Sprintf("summary_%d", time.Now().Unix()),
		Speaker:   "system",
		Content:   compressed,
		Type:      SystemMessage,
		Timestamp: time.Now(),
		Metadata:  map[string]interface{}{"compressed": true},
	}

	// Keep recent messages and add summary
	conversation.Messages = append([]ConversationMessage{summaryMessage}, conversation.Messages[len(conversation.Messages)/2:]...)
	conversation.CompressionLevel++

	return nil
}

func (cm *ConversationalMemory) messagesToText(messages []ConversationMessage) string {
	var parts []string
	for _, msg := range messages {
		parts = append(parts, fmt.Sprintf("%s: %s", msg.Speaker, msg.Content))
	}
	return strings.Join(parts, "\n")
}

// RepositoryMemory implementation.
func NewRepositoryMemory(config RepositoryConfig) *RepositoryMemory {
	return &RepositoryMemory{
		patterns:        make(map[string]*CodePattern),
		codebaseContext: &CodebaseContext{},
		learningHistory: []LearningEvent{},
		config:          config,
	}
}

func (rm *RepositoryMemory) StorePattern(ctx context.Context, pattern CodePattern) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Generate pattern ID if not set
	if pattern.ID == "" {
		pattern.ID = generatePatternID(pattern.Pattern)
	}

	// Check if pattern already exists
	if existing, exists := rm.patterns[pattern.ID]; exists {
		existing.Frequency++
		existing.LastSeen = time.Now()
		existing.Confidence = (existing.Confidence + pattern.Confidence) / 2
	} else {
		pattern.LastSeen = time.Now()
		rm.patterns[pattern.ID] = &pattern
	}

	// Cleanup old patterns if needed
	if len(rm.patterns) > rm.config.MaxPatterns {
		rm.cleanupOldPatterns()
	}

	return nil
}

func (rm *RepositoryMemory) GetRelevantPatterns(ctx context.Context, codeContext string, maxResults int) ([]CodePattern, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	type patternScore struct {
		pattern *CodePattern
		score   float64
	}

	var scored []patternScore

	// Score patterns based on relevance
	for _, pattern := range rm.patterns {
		score := rm.calculateRelevanceScore(pattern, codeContext)
		if score > 0.3 { // Minimum relevance threshold
			scored = append(scored, patternScore{pattern, score})
		}
	}

	// Sort by score
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Return top results
	var results []CodePattern
	limit := min(len(scored), maxResults)
	for i := 0; i < limit; i++ {
		results = append(results, *scored[i].pattern)
	}

	return results, nil
}

func (rm *RepositoryMemory) Compress(ctx context.Context) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Remove patterns with low confidence and frequency
	for id, pattern := range rm.patterns {
		if pattern.Confidence < 0.5 && pattern.Frequency < 3 {
			delete(rm.patterns, id)
		}
	}

	return nil
}

func (rm *RepositoryMemory) UpdateCodebaseContext(ctx context.Context, context CodebaseContext) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.codebaseContext = &context
	rm.codebaseContext.LastUpdated = time.Now()

	return nil
}

func (rm *RepositoryMemory) calculateRelevanceScore(pattern *CodePattern, context string) float64 {
	score := 0.0

	// String matching
	if strings.Contains(context, pattern.Pattern) {
		score += 0.5
	}

	// Frequency bonus
	score += float64(pattern.Frequency) * 0.1

	// Confidence bonus
	score += pattern.Confidence * 0.3

	// Recency bonus
	timeFactor := time.Since(pattern.LastSeen).Hours() / 24
	score += (1.0 / (1.0 + timeFactor)) * 0.2

	return minFloat64(score, 1.0)
}

func (rm *RepositoryMemory) cleanupOldPatterns() {
	type patternAge struct {
		id  string
		age time.Duration
	}

	var ages []patternAge
	for id, pattern := range rm.patterns {
		ages = append(ages, patternAge{id, time.Since(pattern.LastSeen)})
	}

	// Sort by age (oldest first)
	sort.Slice(ages, func(i, j int) bool {
		return ages[i].age > ages[j].age
	})

	// Remove oldest 10%
	removeCount := len(ages) / 10
	for i := 0; i < removeCount; i++ {
		delete(rm.patterns, ages[i].id)
	}
}

// ThreadAwareMemory implementation.
func NewThreadAwareMemory(config ThreadConfig, globalMemory *ConversationalMemory) *ThreadAwareMemory {
	return &ThreadAwareMemory{
		threads:         make(map[string]*ConversationThread),
		threadRelations: make(map[string][]string),
		globalMemory:    globalMemory,
		config:          config,
	}
}

func (tm *ThreadAwareMemory) CreateThread(ctx context.Context, name string, participants []string) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	threadID := generateThreadID(name, participants)

	thread := &ConversationThread{
		ID:           threadID,
		Name:         name,
		Participants: participants,
		CreatedAt:    time.Now(),
		SharedContext: ThreadContext{
			MainTopic: name,
		},
	}

	tm.threads[threadID] = thread

	return threadID, nil
}

func (tm *ThreadAwareMemory) LinkConversations(ctx context.Context, threadID string, conversationIDs []string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	thread, exists := tm.threads[threadID]
	if !exists {
		return fmt.Errorf("thread not found: %s", threadID)
	}

	thread.Conversations = append(thread.Conversations, conversationIDs...)
	thread.LastActivity = time.Now()

	return nil
}

func (tm *ThreadAwareMemory) UpdateThreadContext(ctx context.Context, conversationID string, message ConversationMessage) error {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	// Find thread containing this conversation
	for _, thread := range tm.threads {
		for _, convID := range thread.Conversations {
			if convID == conversationID {
				tm.updateThreadContextFromMessage(thread, message)
				return nil
			}
		}
	}

	return nil // Not an error if conversation isn't in a thread
}

func (tm *ThreadAwareMemory) updateThreadContextFromMessage(thread *ConversationThread, message ConversationMessage) {
	// Update shared entities
	entities := extractEntities(message.Content)
	for _, entity := range entities {
		if !contains(thread.SharedContext.SharedEntities, entity) {
			thread.SharedContext.SharedEntities = append(thread.SharedContext.SharedEntities, entity)
		}
	}

	// Update thread summary
	thread.SharedContext.ThreadSummary = tm.generateThreadSummary(thread)
}

func (tm *ThreadAwareMemory) generateThreadSummary(thread *ConversationThread) string {
	// Simple summary generation - in practice, would use LLM
	return fmt.Sprintf("Thread '%s' with %d conversations discussing %s",
		thread.Name, len(thread.Conversations), thread.SharedContext.MainTopic)
}

// Helper functions and other implementations...

// PatternLearner implementation.
func NewPatternLearner(config LearningConfig) *PatternLearner {
	return &PatternLearner{
		feedbackHistory: []FeedbackEvent{},
		learnedPatterns: make(map[string]*LearnedPattern),
		config:          config,
	}
}

func (pl *PatternLearner) ProcessFeedback(ctx context.Context, feedback FeedbackEvent) error {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	pl.feedbackHistory = append(pl.feedbackHistory, feedback)

	// Keep only recent feedback
	if len(pl.feedbackHistory) > pl.config.FeedbackWindow {
		pl.feedbackHistory = pl.feedbackHistory[1:]
	}

	// Extract patterns from feedback
	if err := pl.extractPatternsFromFeedback(feedback); err != nil {
		return err
	}

	return nil
}

func (pl *PatternLearner) extractPatternsFromFeedback(feedback FeedbackEvent) error {
	// Simple pattern extraction - in practice, would be more sophisticated
	patternKey := fmt.Sprintf("%d_%s", int(feedback.Type), feedback.ProcessingChain)

	if pattern, exists := pl.learnedPatterns[patternKey]; exists {
		pattern.Occurrences++
		pattern.LastSeen = time.Now()

		// Update confidence based on feedback
		switch feedback.Type {
		case PositiveFeedback:
			pattern.Confidence = minFloat64(pattern.Confidence+pl.config.LearningRate, 1.0)
		case NegativeFeedback:
			pattern.Confidence = maxFloat64(pattern.Confidence-pl.config.LearningRate, 0.0)
		}
	} else {
		confidence := 0.5
		switch feedback.Type {
		case PositiveFeedback:
			confidence = 0.7
		case NegativeFeedback:
			confidence = 0.3
		}

		pl.learnedPatterns[patternKey] = &LearnedPattern{
			ID:          patternKey,
			Pattern:     feedback.ProcessingChain,
			Confidence:  confidence,
			Occurrences: 1,
			LastSeen:    time.Now(),
			Impact:      feedback.Rating,
		}
	}

	return nil
}

// ContextBuilder implementation.
func NewContextBuilder(memory *EnhancedMemoryManager, ragSystem RAGStore, config ContextConfig) *ContextBuilder {
	return &ContextBuilder{
		memory:    memory,
		ragSystem: ragSystem,
		config:    config,
		contextStrategies: []ContextStrategy{
			&ConversationHistoryStrategy{},
			&CodebaseKnowledgeStrategy{ragSystem: ragSystem},
			&PatternLibraryStrategy{},
		},
	}
}

func (cb *ContextBuilder) BuildContext(ctx context.Context, request ContextRequest) (*ContextResult, error) {
	var contextParts []string
	var sources []ContextSource
	totalTokens := 0

	// Sort strategies by priority
	sort.Slice(cb.contextStrategies, func(i, j int) bool {
		return cb.contextStrategies[i].Priority() > cb.contextStrategies[j].Priority()
	})

	// Apply strategies
	for _, strategy := range cb.contextStrategies {
		if totalTokens >= cb.config.MaxContextTokens {
			break
		}

		result, err := strategy.BuildContext(ctx, request)
		if err != nil {
			continue // Skip failed strategies
		}

		if result.Confidence >= cb.config.RelevanceThreshold {
			remainingTokens := cb.config.MaxContextTokens - totalTokens
			if result.TokenCount <= remainingTokens {
				contextParts = append(contextParts, result.Context)
				sources = append(sources, result.Sources...)
				totalTokens += result.TokenCount
			}
		}
	}

	return &ContextResult{
		Context:    strings.Join(contextParts, "\n\n"),
		Sources:    sources,
		Confidence: cb.calculateOverallConfidence(sources),
		TokenCount: totalTokens,
	}, nil
}

func (cb *ContextBuilder) calculateOverallConfidence(sources []ContextSource) float64 {
	if len(sources) == 0 {
		return 0.0
	}

	total := 0.0
	for _, source := range sources {
		total += source.Confidence
	}

	return total / float64(len(sources))
}

// CompressionEngine implementation.
func NewCompressionEngine() *CompressionEngine {
	return &CompressionEngine{
		strategies: map[CompressionStrategy]CompressionAlgorithm{
			SummarizeOldest:         &SummarizationAlgorithm{},
			KeepMostRelevant:        &RelevanceAlgorithm{},
			HierarchicalCompression: &HierarchicalAlgorithm{},
			SemanticClustering:      &ClusteringAlgorithm{},
		},
		config: CompressionConfig{
			CompressionRatio:     0.5,
			PreserveCriticalInfo: true,
			SemanticClustering:   true,
			HierarchicalSummary:  true,
		},
	}
}

// Context strategy implementations.
type ConversationHistoryStrategy struct{}

func (s *ConversationHistoryStrategy) BuildContext(ctx context.Context, request ContextRequest) (*ContextResult, error) {
	// Implementation would retrieve and format conversation history
	return &ContextResult{
		Context:    "Conversation history context...",
		Confidence: 0.8,
		TokenCount: 100,
		Sources: []ContextSource{
			{
				Type:       ConversationHistoryContext,
				Source:     request.ConversationID,
				Confidence: 0.8,
				TokenCount: 100,
			},
		},
	}, nil
}

func (s *ConversationHistoryStrategy) Priority() int { return 5 }
func (s *ConversationHistoryStrategy) Name() string  { return "conversation_history" }

type CodebaseKnowledgeStrategy struct {
	ragSystem RAGStore
}

func (s *CodebaseKnowledgeStrategy) BuildContext(ctx context.Context, request ContextRequest) (*ContextResult, error) {
	// Implementation would query RAG system for relevant code context
	return &ContextResult{
		Context:    "Codebase knowledge context...",
		Confidence: 0.7,
		TokenCount: 150,
		Sources: []ContextSource{
			{
				Type:       CodebaseKnowledgeContext,
				Source:     "rag_system",
				Confidence: 0.7,
				TokenCount: 150,
			},
		},
	}, nil
}

func (s *CodebaseKnowledgeStrategy) Priority() int { return 4 }
func (s *CodebaseKnowledgeStrategy) Name() string  { return "codebase_knowledge" }

type PatternLibraryStrategy struct{}

func (s *PatternLibraryStrategy) BuildContext(ctx context.Context, request ContextRequest) (*ContextResult, error) {
	// Implementation would retrieve relevant patterns
	return &ContextResult{
		Context:    "Pattern library context...",
		Confidence: 0.6,
		TokenCount: 80,
		Sources: []ContextSource{
			{
				Type:       PatternLibraryContext,
				Source:     "pattern_library",
				Confidence: 0.6,
				TokenCount: 80,
			},
		},
	}, nil
}

func (s *PatternLibraryStrategy) Priority() int { return 3 }
func (s *PatternLibraryStrategy) Name() string  { return "pattern_library" }

// Compression algorithm implementations.
type SummarizationAlgorithm struct{}

func (a *SummarizationAlgorithm) Compress(ctx context.Context, content string, ratio float64) (string, error) {
	// Simple implementation - in practice would use LLM for summarization
	targetLength := int(float64(len(content)) * ratio)
	if len(content) <= targetLength {
		return content, nil
	}
	return content[:targetLength] + "...", nil
}

func (a *SummarizationAlgorithm) Decompress(ctx context.Context, compressed string) (string, error) {
	return compressed, nil // Cannot decompress summaries
}

func (a *SummarizationAlgorithm) EstimateCompression(content string) float64 {
	return 0.3 // Estimate 30% compression
}

// Similar implementations for other compression algorithms...
type RelevanceAlgorithm struct{}
type HierarchicalAlgorithm struct{}
type ClusteringAlgorithm struct{}

// Implement the remaining compression algorithms...
func (a *RelevanceAlgorithm) Compress(ctx context.Context, content string, ratio float64) (string, error) {
	return content, nil // Placeholder
}
func (a *RelevanceAlgorithm) Decompress(ctx context.Context, compressed string) (string, error) {
	return compressed, nil
}
func (a *RelevanceAlgorithm) EstimateCompression(content string) float64 { return 0.4 }

func (a *HierarchicalAlgorithm) Compress(ctx context.Context, content string, ratio float64) (string, error) {
	return content, nil // Placeholder
}
func (a *HierarchicalAlgorithm) Decompress(ctx context.Context, compressed string) (string, error) {
	return compressed, nil
}
func (a *HierarchicalAlgorithm) EstimateCompression(content string) float64 { return 0.5 }

func (a *ClusteringAlgorithm) Compress(ctx context.Context, content string, ratio float64) (string, error) {
	return content, nil // Placeholder
}
func (a *ClusteringAlgorithm) Decompress(ctx context.Context, compressed string) (string, error) {
	return compressed, nil
}
func (a *ClusteringAlgorithm) EstimateCompression(content string) float64 { return 0.6 }

// Utility functions

func NewGlobalContext() *GlobalContext {
	return &GlobalContext{}
}

type GlobalContext struct {
	// Global conversation context
}

type LearningEvent struct {
	// Learning event data
}

type TechnicalDebtItem struct {
	// Technical debt information
}

type QualityMetrics struct {
	// Code quality metrics
}

type CodingStandard struct {
	// Coding standard information
}

func (m *EnhancedMemoryManager) extractAndStorePatterns(ctx context.Context, message ConversationMessage) error {
	// Extract code patterns from messages
	if message.Type == UserMessage || message.Type == AssistantMessage {
		// Simple pattern extraction - in practice would be more sophisticated
		if strings.Contains(message.Content, "func ") {
			pattern := CodePattern{
				Name:        "function_discussion",
				Description: "Discussion about function implementation",
				Pattern:     "func ",
				Confidence:  0.7,
				Frequency:   1,
			}
			return m.repositoryMemory.StorePattern(ctx, pattern)
		}
	}
	return nil
}

func (m *EnhancedMemoryManager) adaptBasedOnFeedback(ctx context.Context, feedback FeedbackEvent) error {
	// Adapt memory strategies based on feedback
	if feedback.Type == NegativeFeedback {
		// Reduce confidence in related patterns
		patterns, err := m.repositoryMemory.GetRelevantPatterns(ctx, feedback.Comment, 10)
		if err != nil {
			return err
		}

		for _, pattern := range patterns {
			if pattern.Confidence > 0.1 {
				pattern.Confidence -= 0.1
				_ = m.repositoryMemory.StorePattern(ctx, pattern)
			}
		}
	}
	return nil
}

// Utility functions.
func extractTopic(content string) string {
	// Simple topic extraction
	words := strings.Fields(content)
	if len(words) > 0 {
		return words[0]
	}
	return "general"
}

func extractFileReferences(content string) []string {
	// Extract file references from content
	var files []string
	// Simple regex would be used here
	if strings.Contains(content, ".go") {
		files = append(files, "extracted_file.go")
	}
	return files
}

func calculateSentiment(content string) float64 {
	// Simple sentiment analysis
	if strings.Contains(content, "good") || strings.Contains(content, "great") {
		return 0.8
	} else if strings.Contains(content, "bad") || strings.Contains(content, "error") {
		return 0.2
	}
	return 0.5
}

func extractEntities(content string) []string {
	// Simple entity extraction
	entities := []string{}
	words := strings.Fields(content)
	for _, word := range words {
		if len(word) > 5 && strings.Contains(word, "func") {
			entities = append(entities, word)
		}
	}
	return entities
}

func generatePatternID(pattern string) string {
	hash := sha256.Sum256([]byte(pattern))
	return fmt.Sprintf("pattern_%x", hash[:8])
}

func generateThreadID(name string, participants []string) string {
	data := name + strings.Join(participants, ",")
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("thread_%x", hash[:8])
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
