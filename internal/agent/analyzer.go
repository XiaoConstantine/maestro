// Package agent provides agent pool and lifecycle management for code review.
package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/react"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// QueryType represents different types of search queries.
type QueryType string

const (
	CodeFocusedQuery      QueryType = "code"
	GuidelineFocusedQuery QueryType = "guideline"
	ContextFocusedQuery   QueryType = "context"
	SemanticFocusedQuery  QueryType = "semantic"
	MixedQuery            QueryType = "mixed"
)

// QueryAnalysis contains the analysis results of a search query.
type QueryAnalysis struct {
	PrimaryType         QueryType
	SecondaryType       QueryType
	Confidence          float64
	Keywords            []string
	Complexity          int // 1-5 scale
	SuggestedMode       react.ExecutionMode
	RequiredTools       []string
	MaxIterations       int
	QualityTarget       float64
	SearchStrategy      *SearchStrategy
	NeedsDisambiguation bool
	UserExpertise       string // "beginner", "intermediate", "expert"
}

// QueryAnalyzer analyzes search queries to determine optimal configuration.
type QueryAnalyzer struct {
	llm     core.LLM
	logger  *logging.Logger
	history *SearchHistory
}

// SearchStrategy defines the approach for searching.
type SearchStrategy struct {
	Phases              []ToolPhase
	EscalationThreshold float64
	TimeoutPerTool      time.Duration
}

// ToolPhase represents a phase of tool usage.
type ToolPhase struct {
	PhaseNumber int
	Tools       []string
	Description string
	MaxAttempts int
}

// SearchHistory tracks previous searches for learning.
type SearchHistory struct {
	recent   []HistoryEntry
	patterns map[string]float64
	mu       sync.RWMutex
}

// HistoryEntry represents a past search.
type HistoryEntry struct {
	Query      string
	Complexity int
	Iterations int
	Confidence float64
	Timestamp  time.Time
}

// NewQueryAnalyzer creates a new query analyzer.
func NewQueryAnalyzer(llm core.LLM, logger *logging.Logger) *QueryAnalyzer {
	return &QueryAnalyzer{
		llm:    llm,
		logger: logger,
		history: &SearchHistory{
			recent:   make([]HistoryEntry, 0, 100),
			patterns: make(map[string]float64),
		},
	}
}

// AnalyzeQuery analyzes a search query and returns optimization suggestions.
func (qa *QueryAnalyzer) AnalyzeQuery(ctx context.Context, query, context string) (*QueryAnalysis, error) {
	// Phase 1: Syntactic Analysis
	query = strings.ToLower(query)
	tokens := qa.tokenizeAndNormalize(query)
	entities := qa.extractEntities(tokens)

	// Phase 2: Semantic Analysis
	intent := qa.classifyIntent(query, context)
	complexity := qa.assessComplexity(query, entities, intent)
	userExpertise := qa.detectUserExpertise(query)

	// Phase 3: Strategy Formulation
	searchStrategy := qa.formulateStrategy(intent, complexity)
	toolSequence := qa.planToolUsage(searchStrategy)

	// Check if disambiguation is needed
	needsDisambiguation := qa.checkAmbiguity(query, entities)

	analysis := &QueryAnalysis{
		PrimaryType:         intent,
		Confidence:          qa.calculateConfidence(entities, intent),
		Complexity:          complexity,
		SuggestedMode:       qa.selectOptimalMode(complexity),
		Keywords:            tokens,
		RequiredTools:       toolSequence,
		MaxIterations:       qa.calculateMaxIterations(complexity),
		QualityTarget:       qa.setQualityTarget(complexity),
		SearchStrategy:      searchStrategy,
		NeedsDisambiguation: needsDisambiguation,
		UserExpertise:       userExpertise,
	}

	// Check for code-focused patterns
	if strings.Contains(query, "function") || strings.Contains(query, "method") ||
		strings.Contains(query, "class") || strings.Contains(query, "implementation") {
		analysis.PrimaryType = CodeFocusedQuery
		analysis.SuggestedMode = react.ModeReWOO
	}

	// Check for guideline patterns
	if strings.Contains(query, "best practice") || strings.Contains(query, "pattern") ||
		strings.Contains(query, "guideline") {
		analysis.PrimaryType = GuidelineFocusedQuery
		analysis.Complexity = 4
	}

	// Check for context patterns
	if strings.Contains(query, "context") || strings.Contains(query, "related") ||
		strings.Contains(query, "dependency") {
		analysis.PrimaryType = ContextFocusedQuery
	}

	// Check for semantic patterns
	if strings.Contains(query, "meaning") || strings.Contains(query, "purpose") ||
		strings.Contains(query, "understand") {
		analysis.PrimaryType = SemanticFocusedQuery
		analysis.Complexity = 5
	}

	// Record in history for future learning
	qa.recordSearch(query, complexity)

	return analysis, nil
}

func (qa *QueryAnalyzer) tokenizeAndNormalize(query string) []string {
	tokens := strings.Fields(query)
	normalized := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if !qa.isStopWord(token) {
			normalized = append(normalized, token)
		}
	}
	return normalized
}

func (qa *QueryAnalyzer) extractEntities(tokens []string) []string {
	entities := []string{}
	for _, token := range tokens {
		if qa.isCodeEntity(token) {
			entities = append(entities, token)
		}
	}
	return entities
}

func (qa *QueryAnalyzer) classifyIntent(query, context string) QueryType {
	if strings.Contains(query, "function") || strings.Contains(query, "method") ||
		strings.Contains(query, "class") || strings.Contains(query, "implementation") ||
		strings.Contains(query, "code") {
		return CodeFocusedQuery
	}

	if strings.Contains(query, "best practice") || strings.Contains(query, "pattern") ||
		strings.Contains(query, "guideline") || strings.Contains(query, "convention") {
		return GuidelineFocusedQuery
	}

	if strings.Contains(query, "context") || strings.Contains(query, "related") ||
		strings.Contains(query, "dependency") || strings.Contains(query, "usage") {
		return ContextFocusedQuery
	}

	if strings.Contains(query, "meaning") || strings.Contains(query, "purpose") ||
		strings.Contains(query, "understand") || strings.Contains(query, "explain") {
		return SemanticFocusedQuery
	}

	return MixedQuery
}

func (qa *QueryAnalyzer) assessComplexity(query string, entities []string, intent QueryType) int {
	complexity := 2

	if len(strings.Fields(query)) > 10 {
		complexity++
	}

	if len(entities) > 3 {
		complexity++
	}

	if intent == SemanticFocusedQuery || intent == GuidelineFocusedQuery {
		complexity++
	}

	if complexity > 5 {
		complexity = 5
	}

	return complexity
}

func (qa *QueryAnalyzer) detectUserExpertise(query string) string {
	if strings.Contains(query, "basic") || strings.Contains(query, "simple") ||
		strings.Contains(query, "explain") || strings.Contains(query, "what is") {
		return "beginner"
	}

	if strings.Contains(query, "advanced") || strings.Contains(query, "optimize") ||
		strings.Contains(query, "performance") || strings.Contains(query, "architecture") {
		return "expert"
	}

	return "intermediate"
}

func (qa *QueryAnalyzer) formulateStrategy(intent QueryType, complexity int) *SearchStrategy {
	strategy := &SearchStrategy{
		EscalationThreshold: 0.6,
		TimeoutPerTool:      30 * time.Second,
	}

	if complexity <= 2 {
		strategy.Phases = []ToolPhase{
			{PhaseNumber: 1, Tools: []string{"search_files", "search_content"}, Description: "Quick search", MaxAttempts: 2},
		}
	} else if complexity <= 4 {
		strategy.Phases = []ToolPhase{
			{PhaseNumber: 1, Tools: []string{"search_files"}, Description: "Initial file discovery", MaxAttempts: 2},
			{PhaseNumber: 2, Tools: []string{"search_content", "read_file"}, Description: "Deep content search", MaxAttempts: 3},
		}
	} else {
		strategy.Phases = []ToolPhase{
			{PhaseNumber: 1, Tools: []string{"search_files"}, Description: "Broad file discovery", MaxAttempts: 3},
			{PhaseNumber: 2, Tools: []string{"search_content"}, Description: "Targeted content search", MaxAttempts: 4},
			{PhaseNumber: 3, Tools: []string{"read_file"}, Description: "Detailed analysis", MaxAttempts: 2},
		}
	}

	return strategy
}

func (qa *QueryAnalyzer) planToolUsage(strategy *SearchStrategy) []string {
	tools := []string{}
	for _, phase := range strategy.Phases {
		tools = append(tools, phase.Tools...)
	}
	return tools
}

func (qa *QueryAnalyzer) checkAmbiguity(query string, entities []string) bool {
	if len(entities) == 0 && len(strings.Fields(query)) < 3 {
		return true
	}

	vagueTerms := []string{"this", "that", "it", "something", "stuff"}
	for _, term := range vagueTerms {
		if strings.Contains(query, term) {
			return true
		}
	}

	return false
}

func (qa *QueryAnalyzer) calculateConfidence(entities []string, intent QueryType) float64 {
	confidence := 0.5

	confidence += float64(len(entities)) * 0.1

	if intent != MixedQuery {
		confidence += 0.2
	}

	if confidence > 0.95 {
		confidence = 0.95
	}

	return confidence
}

func (qa *QueryAnalyzer) selectOptimalMode(complexity int) react.ExecutionMode {
	if complexity <= 2 {
		return react.ModeReAct
	} else if complexity <= 4 {
		return react.ModeReWOO
	}
	return react.ModeReAct
}

func (qa *QueryAnalyzer) calculateMaxIterations(complexity int) int {
	complexityMultiplier := map[int]int{
		1: 2,
		2: 3,
		3: 4,
		4: 6,
		5: 8,
	}

	if iterations, ok := complexityMultiplier[complexity]; ok {
		return iterations
	}
	return 3
}

func (qa *QueryAnalyzer) setQualityTarget(complexity int) float64 {
	qualityTargets := map[int]float64{
		1: 0.9,
		2: 0.85,
		3: 0.8,
		4: 0.75,
		5: 0.7,
	}

	if target, ok := qualityTargets[complexity]; ok {
		return target
	}
	return 0.8
}

func (qa *QueryAnalyzer) isStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "from": true,
	}
	return stopWords[word]
}

func (qa *QueryAnalyzer) isCodeEntity(token string) bool {
	codePatterns := []string{
		"func", "function", "class", "struct", "interface",
		"method", "variable", "const", "type", "package",
		"import", "error", "test", "main", "init",
	}

	for _, pattern := range codePatterns {
		if strings.Contains(token, pattern) {
			return true
		}
	}
	return false
}

func (qa *QueryAnalyzer) recordSearch(query string, complexity int) {
	qa.history.mu.Lock()
	defer qa.history.mu.Unlock()

	entry := HistoryEntry{
		Query:      query,
		Complexity: complexity,
		Timestamp:  time.Now(),
	}

	qa.history.recent = append(qa.history.recent, entry)

	if len(qa.history.recent) > 100 {
		qa.history.recent = qa.history.recent[1:]
	}
}
