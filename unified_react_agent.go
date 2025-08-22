package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/react"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/interceptors"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
	models "github.com/XiaoConstantine/mcp-go/pkg/model"
)

// UnifiedReActAgent wraps a dspy-go ReAct agent with dynamic specialization.
type UnifiedReActAgent struct {
	ID         string
	reactAgent *react.ReActAgent
	searchTool *SimpleSearchTool
	logger     *logging.Logger
	status     AgentStatus
	mu         sync.RWMutex

	// Dynamic configuration
	currentMode       react.ExecutionMode
	queryAnalyzer     *QueryAnalyzer
	searchContext     *SearchContext
	qualityTracker    *QualityTracker
	xmlFactory        *XMLConfigFactory
	toolCallParser    *ToolCallParser
	xmlEnabledModules map[string]*modules.Predict
}

// SearchContext tracks the evolution of a search.
type SearchContext struct {
	QueryEvolution    []string          // How query understanding evolved
	ToolUsageHistory  []ToolUsageRecord // What tools were used and why
	FindingsTimeline  []Finding         // Chronological findings
	ConfidenceTracker []float64         // Confidence evolution
	DecisionPoints    []DecisionPoint   // Key decision moments
	CurrentPhase      int               // Current tool phase
	IterationCount    int               // Actual iterations performed
}

// ToolUsageRecord tracks tool usage.
type ToolUsageRecord struct {
	ToolName      string
	Phase         int
	Reason        string
	ResultQuality float64
	Timestamp     time.Time
}

// Finding represents a search finding.
type Finding struct {
	Content   string
	Relevance float64
	Source    string
	Timestamp time.Time
}

// DecisionPoint represents a key decision in the search.
type DecisionPoint struct {
	Iteration    int
	Reason       string
	NextStrategy string
	Timestamp    time.Time
}

// QualityTracker monitors search result quality.
type QualityTracker struct {
	completeness  float64
	consistency   float64
	relevance     float64
	actionability float64
	mu            sync.RWMutex
}

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
	MaxIterations       int     // Dynamic based on complexity
	QualityTarget       float64 // Target confidence for results
	SearchStrategy      *SearchStrategy
	NeedsDisambiguation bool
	UserExpertise       string // "beginner", "intermediate", "expert"
}

// QueryAnalyzer analyzes search queries to determine optimal configuration.
type QueryAnalyzer struct {
	llm     core.LLM
	logger  *logging.Logger
	history *SearchHistory // Track previous searches for learning
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
	patterns map[string]float64 // Pattern -> Success rate
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

// SearchTool implements core.Tool for maestro search operations.
type SearchTool struct {
	name        string
	description string
	execute     func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error)
	searchTool  *SimpleSearchTool
}

// Implement core.Tool interface.
func (st *SearchTool) Name() string {
	return st.name
}

func (st *SearchTool) Description() string {
	return st.description
}

func (st *SearchTool) Metadata() *core.ToolMetadata {
	return &core.ToolMetadata{
		Name:        st.name,
		Description: st.description,
		Version:     "1.0",
	}
}

func (st *SearchTool) CanHandle(ctx context.Context, intent string) bool {
	return strings.Contains(intent, "search") || strings.Contains(intent, "find")
}

func (st *SearchTool) Execute(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
	return st.execute(ctx, params)
}

func (st *SearchTool) Validate(params map[string]interface{}) error {
	// Basic validation - could be more sophisticated
	return nil
}

func (st *SearchTool) InputSchema() models.InputSchema {
	return models.InputSchema{} // Simplified for now
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
		analysis.SuggestedMode = react.ModeReWOO // Plan-then-execute for systematic code search
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

// Helper methods for enhanced query analysis.
func (qa *QueryAnalyzer) tokenizeAndNormalize(query string) []string {
	// Advanced tokenization beyond simple split
	tokens := strings.Fields(query)
	normalized := make([]string, 0, len(tokens))
	for _, token := range tokens {
		// Remove common stop words for better analysis
		if !qa.isStopWord(token) {
			normalized = append(normalized, token)
		}
	}
	return normalized
}

func (qa *QueryAnalyzer) extractEntities(tokens []string) []string {
	entities := []string{}
	for _, token := range tokens {
		// Identify code-related entities
		if qa.isCodeEntity(token) {
			entities = append(entities, token)
		}
	}
	return entities
}

func (qa *QueryAnalyzer) classifyIntent(query, context string) QueryType {
	// Enhanced intent classification
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
	// Base complexity on multiple factors
	complexity := 2 // Start with medium

	// Increase for longer queries
	if len(strings.Fields(query)) > 10 {
		complexity++
	}

	// Increase for multiple entities
	if len(entities) > 3 {
		complexity++
	}

	// Adjust based on intent
	if intent == SemanticFocusedQuery || intent == GuidelineFocusedQuery {
		complexity++
	}

	// Cap at 5
	if complexity > 5 {
		complexity = 5
	}

	return complexity
}

func (qa *QueryAnalyzer) detectUserExpertise(query string) string {
	// Detect user expertise level from query patterns
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

	// Define phases based on intent and complexity
	if complexity <= 2 {
		// Simple search - single phase
		strategy.Phases = []ToolPhase{
			{PhaseNumber: 1, Tools: []string{"search_files", "search_content"}, Description: "Quick search", MaxAttempts: 2},
		}
	} else if complexity <= 4 {
		// Medium search - two phases
		strategy.Phases = []ToolPhase{
			{PhaseNumber: 1, Tools: []string{"search_files"}, Description: "Initial file discovery", MaxAttempts: 2},
			{PhaseNumber: 2, Tools: []string{"search_content", "read_file"}, Description: "Deep content search", MaxAttempts: 3},
		}
	} else {
		// Complex search - three phases
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
	// Check if query is ambiguous and needs clarification
	if len(entities) == 0 && len(strings.Fields(query)) < 3 {
		return true
	}

	// Check for vague terms
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

	// More entities = higher confidence
	confidence += float64(len(entities)) * 0.1

	// Clear intent = higher confidence
	if intent != MixedQuery {
		confidence += 0.2
	}

	// Cap at 0.95
	if confidence > 0.95 {
		confidence = 0.95
	}

	return confidence
}

func (qa *QueryAnalyzer) selectOptimalMode(complexity int) react.ExecutionMode {
	if complexity <= 2 {
		return react.ModeReAct // Simple reactive mode
	} else if complexity <= 4 {
		return react.ModeReWOO // Plan-then-execute for medium
	}
	return react.ModeReAct // Back to ReAct for complex adaptive searches
}

func (qa *QueryAnalyzer) calculateMaxIterations(complexity int) int {
	// Dynamic iteration control based on complexity
	complexityMultiplier := map[int]int{
		1: 2, // Simple queries: 2 iterations
		2: 3, // Medium queries: 3 iterations
		3: 4, // Complex queries: 4 iterations
		4: 6, // Very complex: 6 iterations
		5: 8, // Extremely complex: 8 iterations
	}

	if iterations, ok := complexityMultiplier[complexity]; ok {
		return iterations
	}
	return 3 // Default
}

func (qa *QueryAnalyzer) setQualityTarget(complexity int) float64 {
	// Higher complexity = slightly lower quality target (more realistic)
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
	// Check if token represents a code entity
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

	// Keep only last 100 entries
	if len(qa.history.recent) > 100 {
		qa.history.recent = qa.history.recent[1:]
	}
}

// NewUnifiedReActAgent creates a new unified ReAct agent.
func NewUnifiedReActAgent(id string, searchTool *SimpleSearchTool, logger *logging.Logger) (*UnifiedReActAgent, error) {
	llm := core.GetDefaultLLM()

	// Create ReAct agent with flexible configuration
	reactAgent := react.NewReActAgent(
		fmt.Sprintf("unified-search-%s", id),
		"Adaptive search agent that specializes dynamically based on query type and complexity",
		react.WithExecutionMode(react.ModeReAct), // Start with ReAct mode
		react.WithReflection(false, 0),           // Disable reflection for faster execution
		react.WithMaxIterations(8),               // Maximum iterations, will be dynamically adjusted
		react.WithTimeout(60*time.Second),        // Increased timeout for complex searches
	)

	agent := &UnifiedReActAgent{
		ID:         id,
		reactAgent: reactAgent,
		searchTool: searchTool,
		logger:     logger,
		status: AgentStatus{
			State:      "idle",
			LastUpdate: time.Now(),
		},
		currentMode:   react.ModeReAct,
		queryAnalyzer: NewQueryAnalyzer(llm, logger),
		searchContext: &SearchContext{
			QueryEvolution:    []string{},
			ToolUsageHistory:  []ToolUsageRecord{},
			FindingsTimeline:  []Finding{},
			ConfidenceTracker: []float64{},
			DecisionPoints:    []DecisionPoint{},
			CurrentPhase:      1,
		},
		qualityTracker:    &QualityTracker{},
		xmlFactory:        NewXMLConfigFactory(),
		toolCallParser:    NewToolCallParser(),
		xmlEnabledModules: make(map[string]*modules.Predict),
	}

	// Register search tools with the ReAct agent
	if err := agent.registerSearchTools(); err != nil {
		return nil, fmt.Errorf("failed to register search tools: %w", err)
	}

	// Initialize the ReAct agent with LLM and signature
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.NewField("query", core.WithDescription("The search query to process"))},
			{Field: core.NewField("context", core.WithDescription("Additional context for the search"))},
		},
		[]core.OutputField{
			{Field: core.NewField("answer", core.WithDescription("The final search results and analysis"))},
		},
	)

	// Add initial system instruction with native XML support
	signature = signature.WithInstruction(
		"You are a code search assistant helping to find relevant code and information in a codebase.\n\n" +
			"AVAILABLE TOOLS:\n" +
			"- search_files: Find files by pattern (parameter: 'pattern')\n" +
			"- search_content: Search text in files (parameters: 'query', optional 'path')\n" +
			"- read_file: Read file contents (parameter: 'file_path')\n" +
			"- Finish: Complete the search (no parameters)\n\n" +
			"Tool calls are automatically formatted in XML by the system. " +
			"Focus on selecting appropriate tools and parameters for the task.\n" +
			"This instruction will be dynamically updated based on query analysis.",
	)

	if err := reactAgent.Initialize(llm, signature); err != nil {
		return nil, fmt.Errorf("failed to initialize ReAct agent: %w", err)
	}

	return agent, nil
}

// createXMLEnabledModule creates a Predict module with XML output for tool calling.
func (ura *UnifiedReActAgent) createXMLEnabledModule(toolName, description string, xmlConfig interceptors.XMLConfig) *modules.Predict {
	// Create signature for tool execution
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.NewField("tool_request", core.WithDescription("The tool request to process"))},
			{Field: core.NewField("context", core.WithDescription("Additional context for tool execution"))},
		},
		[]core.OutputField{
			{Field: core.NewField("tool_call", core.WithDescription("Structured XML tool call"))},
		},
	).WithInstruction(fmt.Sprintf(
		"You are a %s tool executor. Process the request and generate a structured XML tool call. "+
			"Focus on extracting the correct parameters for the %s operation.",
		toolName, description,
	))

	// Create Predict module with XML output
	predict := modules.NewPredict(signature).WithXMLOutput(xmlConfig)

	// Cache the module for reuse
	ura.xmlEnabledModules[toolName] = predict

	return predict
}

// extractXMLParameters uses XML-enabled module to parse tool parameters.
func (ura *UnifiedReActAgent) extractXMLParameters(ctx context.Context, toolName string, rawParams map[string]interface{}) (map[string]interface{}, error) {
	// Check if XML-enabled module exists for this tool
	_, exists := ura.xmlEnabledModules[toolName]
	if !exists {
		// Fallback to manual parameter extraction if XML module not available
		return rawParams, nil
	}

	// Use XML module to validate and parse parameters
	// In future, we can use the module for more sophisticated XML processing
	if err := ura.toolCallParser.ValidateToolArguments(toolName, rawParams); err != nil {
		return nil, fmt.Errorf("parameter validation failed: %w", err)
	}

	return rawParams, nil
}

// registerSearchTools registers maestro's search capabilities as ReAct tools with native XML support.
func (ura *UnifiedReActAgent) registerSearchTools() error {
	// Initialize XML-enabled modules for each tool type
	ura.initializeXMLModules()

	return ura.registerSearchToolsWithXML()
}

// initializeXMLModules creates XML-enabled Predict modules for all tool types.
func (ura *UnifiedReActAgent) initializeXMLModules() {
	// Create XML configs for different tool types
	searchConfig := ura.xmlFactory.GetSearchToolXMLConfig()
	fileConfig := ura.xmlFactory.GetFileToolXMLConfig()
	generalConfig := ura.xmlFactory.GetGeneralXMLConfig()

	// Initialize modules for each tool
	ura.createXMLEnabledModule("search_files", "file pattern searching", searchConfig)
	ura.createXMLEnabledModule("search_content", "content searching", searchConfig)
	ura.createXMLEnabledModule("read_file", "file reading", fileConfig)
	ura.createXMLEnabledModule("Finish", "task completion", generalConfig)
}

// registerSearchToolsWithXML registers tools with native XML parsing support.
func (ura *UnifiedReActAgent) registerSearchToolsWithXML() error {
	// File search tool
	fileSearchTool := &SearchTool{
		name:        "search_files",
		description: "Search for files by pattern, name, or extension. Use patterns like '*.go' for Go files, 'test*' for test files, or specific names. This finds files in the codebase matching your pattern.",
		searchTool:  ura.searchTool,
		execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
			// Use XML-enabled parameter extraction
			validatedParams, err := ura.extractXMLParameters(ctx, "search_files", params)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("XML parameter extraction failed: %w", err)
			}

			// Extract pattern with improved fallback handling
			pattern, ok := validatedParams["pattern"].(string)
			if !ok || pattern == "" {
				// Check for alternative parameter names with XML validation
				if p, exists := validatedParams["query"].(string); exists && p != "" {
					pattern = p
				} else if p, exists := validatedParams["search"].(string); exists && p != "" {
					pattern = p
				} else {
					return core.ToolResult{}, fmt.Errorf("pattern parameter required - provide a file pattern like '*.go' or 'search*'")
				}
			}

			results, err := ura.searchTool.GlobSearch(ctx, pattern)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("file search failed: %w", err)
			}

			return core.ToolResult{
				Data: ura.formatGlobResults(results),
				Metadata: map[string]interface{}{
					"tool":         "search_files",
					"pattern":      pattern,
					"result_count": len(results),
					"xml_enabled":  true,
				},
			}, nil
		},
	}

	// Content search tool
	contentSearchTool := &SearchTool{
		name:        "search_content",
		description: "Search for specific text, patterns, or code within files using grep-like functionality. Use this to find functions, variables, comments, or any text content in the codebase. Provide a 'query' parameter with the text to search for.",
		searchTool:  ura.searchTool,
		execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
			// Use XML-enabled parameter extraction
			validatedParams, err := ura.extractXMLParameters(ctx, "search_content", params)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("XML parameter extraction failed: %w", err)
			}

			query, ok := validatedParams["query"].(string)
			if !ok || query == "" {
				// Check for alternative parameter names with XML validation
				if q, exists := validatedParams["search"].(string); exists && q != "" {
					query = q
				} else if q, exists := validatedParams["text"].(string); exists && q != "" {
					query = q
				} else if q, exists := validatedParams["pattern"].(string); exists && q != "" {
					query = q
				} else {
					return core.ToolResult{}, fmt.Errorf("query parameter required - provide the text or pattern to search for")
				}
			}

			path := ""
			if p, exists := validatedParams["path"]; exists && p != nil {
				path = p.(string)
			}

			// Use grep search with file pattern if path is provided
			filePattern := "**/*"
			if path != "" {
				filePattern = path + "/**/*"
			}

			results, err := ura.searchTool.GrepSearch(ctx, query, filePattern, 2)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("content search failed: %w", err)
			}

			return core.ToolResult{
				Data: ura.formatSearchResults(results),
				Metadata: map[string]interface{}{
					"tool":         "search_content",
					"query":        query,
					"path":         path,
					"result_count": len(results),
					"xml_enabled":  true,
				},
			}, nil
		},
	}

	// File reading tool
	readFileTool := &SearchTool{
		name:        "read_file",
		description: "Read the complete contents of a specific file. Provide the 'file_path' parameter with the path to the file you want to read. Use this after finding interesting files with search_files or search_content.",
		searchTool:  ura.searchTool,
		execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
			// Use XML-enabled parameter extraction
			validatedParams, err := ura.extractXMLParameters(ctx, "read_file", params)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("XML parameter extraction failed: %w", err)
			}

			// Log validated parameters for debugging
			ura.logger.Debug(ctx, "read_file validated params: %v", validatedParams)

			filePath, ok := validatedParams["file_path"].(string)
			if !ok || filePath == "" {
				// Check for alternative parameter names with XML validation
				if fp, exists := validatedParams["path"].(string); exists && fp != "" {
					filePath = fp
				} else if fp, exists := validatedParams["filepath"].(string); exists && fp != "" {
					filePath = fp
				} else if fp, exists := validatedParams["filename"].(string); exists && fp != "" {
					filePath = fp
				} else if fp, exists := validatedParams["file"].(string); exists && fp != "" {
					filePath = fp
				} else {
					// Log all available parameters for debugging
					var availableKeys []string
					for k := range validatedParams {
						availableKeys = append(availableKeys, k)
					}
					return core.ToolResult{}, fmt.Errorf("file_path parameter required - provide the full path to the file you want to read. Available parameters: %v", availableKeys)
				}
			}

			lines, err := ura.searchTool.ReadFile(ctx, filePath, 0, 0)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("file read failed: %w", err)
			}

			content := strings.Join(lines, "\n")

			// Limit content size to prevent context overflow
			maxSize := 10000
			if len(content) > maxSize {
				content = content[:maxSize] + "\n\n... (content truncated)"
			}

			return core.ToolResult{
				Data: content,
				Metadata: map[string]interface{}{
					"tool":           "read_file",
					"file_path":      filePath,
					"content_length": len(content),
					"line_count":     len(lines),
					"xml_enabled":    true,
				},
			}, nil
		},
	}

	// Register all tools with the ReAct agent
	if err := ura.reactAgent.RegisterTool(fileSearchTool); err != nil {
		return fmt.Errorf("failed to register file search tool: %w", err)
	}

	if err := ura.reactAgent.RegisterTool(contentSearchTool); err != nil {
		return fmt.Errorf("failed to register content search tool: %w", err)
	}

	if err := ura.reactAgent.RegisterTool(readFileTool); err != nil {
		return fmt.Errorf("failed to register read file tool: %w", err)
	}

	return nil
}

// ExecuteSearch performs an intelligent search using dynamic ReAct configuration.
func (ura *UnifiedReActAgent) ExecuteSearch(ctx context.Context, request *SearchRequest) (*SearchResponse, error) {
	startTime := time.Now()
	ura.updateStatus("analyzing", 0.1, nil)
	ura.resetSearchContext()

	ura.logger.Info(ctx, "Unified ReAct Agent %s starting search: %s", ura.ID, request.Query)

	// Analyze the query to determine optimal configuration
	analysis, err := ura.queryAnalyzer.AnalyzeQuery(ctx, request.Query, request.Context)
	if err != nil {
		ura.logger.Warn(ctx, "Query analysis failed, using default configuration: %v", err)
		// Use default analysis if query analysis fails
		analysis = &QueryAnalysis{
			PrimaryType:   MixedQuery,
			SuggestedMode: react.ModeReAct,
			Complexity:    3,
			RequiredTools: []string{"search_content", "search_files"},
			MaxIterations: 3,
			QualityTarget: 0.7,
		}
	}

	// Check if disambiguation is needed
	if analysis.NeedsDisambiguation {
		ura.logger.Info(ctx, "Query needs disambiguation, may require clarification")
		// In a real implementation, we'd ask for clarification here
	}

	ura.logger.Info(ctx, "Query analysis: type=%s, mode=%v, complexity=%d, max_iterations=%d",
		analysis.PrimaryType, analysis.SuggestedMode, analysis.Complexity, analysis.MaxIterations)

	// Configure the ReAct agent based on analysis
	ura.configureForQuery(analysis, request)
	ura.searchContext.QueryEvolution = append(ura.searchContext.QueryEvolution,
		fmt.Sprintf("Initial analysis: %s query with complexity %d", analysis.PrimaryType, analysis.Complexity))

	ura.updateStatus("searching", 0.3, nil)

	// Execute progressive search with quality monitoring
	response, err := ura.executeProgressiveSearch(ctx, request, analysis)
	if err != nil {
		// Try error recovery
		recoveryResponse, recoveryErr := ura.executeErrorRecovery(ctx, request, analysis, err)
		if recoveryErr != nil {
			ura.updateStatus("failed", 0.0, recoveryErr)
			return nil, fmt.Errorf("search failed with recovery error: %w", recoveryErr)
		}
		response = recoveryResponse
	}

	ura.updateStatus("synthesizing", 0.8, nil)

	// Validate result quality
	qualityAssessment := ura.validateResultQuality(response, analysis)
	if !qualityAssessment.MeetsThreshold {
		ura.logger.Warn(ctx, "Result quality below threshold: %.2f < %.2f",
			qualityAssessment.OverallScore, analysis.QualityTarget)
		// Could trigger additional search here if needed
	}

	response.Duration = time.Since(startTime)
	response.AgentID = ura.ID
	response.Confidence = qualityAssessment.OverallScore

	ura.updateStatus("complete", 1.0, nil)
	ura.logger.Info(ctx, "Unified ReAct Agent %s completed search in %v with %d results, confidence: %.2f",
		ura.ID, response.Duration, len(response.Results), response.Confidence)

	return response, nil
}

// executeProgressiveSearch implements phased tool execution with quality monitoring.
func (ura *UnifiedReActAgent) executeProgressiveSearch(ctx context.Context, request *SearchRequest, analysis *QueryAnalysis) (*SearchResponse, error) {
	var allResults []*EnhancedSearchResult
	currentConfidence := 0.0

	for phaseIdx, phase := range analysis.SearchStrategy.Phases {
		ura.searchContext.CurrentPhase = phase.PhaseNumber
		ura.logger.Info(ctx, "Executing phase %d: %s", phase.PhaseNumber, phase.Description)

		// Check if we should continue based on current confidence
		if currentConfidence >= analysis.QualityTarget {
			ura.logger.Info(ctx, "Quality target reached (%.2f >= %.2f), skipping remaining phases",
				currentConfidence, analysis.QualityTarget)
			break
		}

		// Execute phase with limited iterations
		phaseInput := ura.preparePhaseInput(request, phase, allResults)
		phaseResult, err := ura.executePhase(ctx, phaseInput, phase, analysis.MaxIterations)
		if err != nil {
			ura.recordDecisionPoint(ura.searchContext.IterationCount,
				fmt.Sprintf("Phase %d failed: %v", phase.PhaseNumber, err),
				"Attempting next phase or recovery")
			continue
		}

		// Process and accumulate results
		if enhancedResults := ura.processPhaseResults(phaseResult, phase); enhancedResults != nil {
			allResults = append(allResults, enhancedResults...)
			currentConfidence = ura.calculateResultConfidence(allResults)
			ura.searchContext.ConfidenceTracker = append(ura.searchContext.ConfidenceTracker, currentConfidence)
		}

		// Check escalation threshold
		if phaseIdx < len(analysis.SearchStrategy.Phases)-1 &&
			currentConfidence < analysis.SearchStrategy.EscalationThreshold {
			ura.logger.Info(ctx, "Confidence %.2f below escalation threshold %.2f, proceeding to next phase",
				currentConfidence, analysis.SearchStrategy.EscalationThreshold)
		}
	}

	return ura.synthesizeProgressiveResults(allResults, request, analysis), nil
}

// preparePhaseInput prepares input for a specific search phase.
func (ura *UnifiedReActAgent) preparePhaseInput(request *SearchRequest, phase ToolPhase, previousResults []*EnhancedSearchResult) map[string]interface{} {
	input := map[string]interface{}{
		"query":            request.Query,
		"context":          request.Context,
		"max_results":      request.MaxResults,
		"search_depth":     request.RequiredDepth,
		"exclude_patterns": request.ExcludePatterns,
		"focus_areas":      request.FocusAreas,
		"phase":            phase.PhaseNumber,
		"allowed_tools":    phase.Tools,
	}

	// Add context from previous results if available
	if len(previousResults) > 0 {
		var previousFindings []string
		for _, result := range previousResults {
			if result.Relevance > 0.6 {
				previousFindings = append(previousFindings, result.Line)
			}
		}
		input["previous_findings"] = previousFindings
	}

	return input
}

// executePhase executes a single phase of the search strategy.
func (ura *UnifiedReActAgent) executePhase(ctx context.Context, input map[string]interface{}, phase ToolPhase, maxIterations int) (map[string]interface{}, error) {
	// Record tool usage
	for _, tool := range phase.Tools {
		ura.recordToolUsage(tool, phase.PhaseNumber, "Phase execution", 0.0)
	}

	// Use a timeout specific to this phase
	phaseCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Execute with the ReAct agent
	result, err := ura.reactAgent.Execute(phaseCtx, input)
	if err != nil {
		return nil, err
	}

	ura.searchContext.IterationCount++
	return result, nil
}

// processPhaseResults processes results from a search phase.
func (ura *UnifiedReActAgent) processPhaseResults(result map[string]interface{}, phase ToolPhase) []*EnhancedSearchResult {
	var results []*EnhancedSearchResult

	if data, exists := result["answer"]; exists {
		if dataStr, ok := data.(string); ok && dataStr != "" {
			enhancedResult := &EnhancedSearchResult{
				SearchResult: &SearchResult{
					FilePath:   fmt.Sprintf("phase-%d-output", phase.PhaseNumber),
					LineNumber: 1,
					Line:       dataStr,
					MatchType:  "phase_result",
					Score:      0.7,
				},
				Relevance:   0.7,
				Explanation: fmt.Sprintf("Result from %s", phase.Description),
				Category:    phase.Description,
			}
			results = append(results, enhancedResult)

			// Record finding
			ura.recordFinding(dataStr, 0.7, phase.Description)
		}
	}

	return results
}

// executeErrorRecovery attempts to recover from search errors.
func (ura *UnifiedReActAgent) executeErrorRecovery(ctx context.Context, request *SearchRequest, analysis *QueryAnalysis, originalErr error) (*SearchResponse, error) {
	ura.logger.Warn(ctx, "Attempting error recovery for: %v", originalErr)

	// Simple recovery: try with reduced complexity
	simplifiedAnalysis := *analysis
	simplifiedAnalysis.Complexity = 2
	simplifiedAnalysis.MaxIterations = 2

	// Create simplified input
	input := map[string]interface{}{
		"query":       request.Query,
		"context":     request.Context,
		"max_results": 5, // Reduced results
	}

	result, err := ura.reactAgent.Execute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("recovery failed: %w", err)
	}

	return ura.convertReActResult(result, request, &simplifiedAnalysis)
}

// QualityAssessment represents the quality assessment of search results.
type QualityAssessment struct {
	Completeness   float64
	Consistency    float64
	Relevance      float64
	Actionability  float64
	OverallScore   float64
	MeetsThreshold bool
}

// validateResultQuality assesses the quality of search results.
func (ura *UnifiedReActAgent) validateResultQuality(response *SearchResponse, analysis *QueryAnalysis) *QualityAssessment {
	ura.qualityTracker.mu.Lock()
	defer ura.qualityTracker.mu.Unlock()

	assessment := &QualityAssessment{}

	// Assess completeness
	if len(response.Results) > 0 {
		completeness := float64(len(response.Results)) / float64(analysis.Complexity*2)
		if completeness > 1.0 {
			completeness = 1.0
		}
		assessment.Completeness = completeness
	}

	// Assess consistency (simplified - would check for contradictions in real implementation)
	assessment.Consistency = 0.8

	// Assess relevance
	totalRelevance := 0.0
	for _, result := range response.Results {
		totalRelevance += result.Relevance
	}
	if len(response.Results) > 0 {
		assessment.Relevance = totalRelevance / float64(len(response.Results))
	}

	// Assess actionability
	if response.Synthesis != "" {
		assessment.Actionability = 0.7
	}

	// Calculate overall score
	assessment.OverallScore = (assessment.Completeness + assessment.Consistency +
		assessment.Relevance + assessment.Actionability) / 4.0

	assessment.MeetsThreshold = assessment.OverallScore >= analysis.QualityTarget

	// Update tracker
	ura.qualityTracker.completeness = assessment.Completeness
	ura.qualityTracker.consistency = assessment.Consistency
	ura.qualityTracker.relevance = assessment.Relevance
	ura.qualityTracker.actionability = assessment.Actionability

	return assessment
}

// Helper methods for context management.
func (ura *UnifiedReActAgent) resetSearchContext() {
	ura.searchContext = &SearchContext{
		QueryEvolution:    []string{},
		ToolUsageHistory:  []ToolUsageRecord{},
		FindingsTimeline:  []Finding{},
		ConfidenceTracker: []float64{},
		DecisionPoints:    []DecisionPoint{},
		CurrentPhase:      1,
		IterationCount:    0,
	}
}

func (ura *UnifiedReActAgent) recordToolUsage(toolName string, phase int, reason string, quality float64) {
	record := ToolUsageRecord{
		ToolName:      toolName,
		Phase:         phase,
		Reason:        reason,
		ResultQuality: quality,
		Timestamp:     time.Now(),
	}
	ura.searchContext.ToolUsageHistory = append(ura.searchContext.ToolUsageHistory, record)
}

func (ura *UnifiedReActAgent) recordFinding(content string, relevance float64, source string) {
	finding := Finding{
		Content:   content,
		Relevance: relevance,
		Source:    source,
		Timestamp: time.Now(),
	}
	ura.searchContext.FindingsTimeline = append(ura.searchContext.FindingsTimeline, finding)
}

func (ura *UnifiedReActAgent) recordDecisionPoint(iteration int, reason string, nextStrategy string) {
	decision := DecisionPoint{
		Iteration:    iteration,
		Reason:       reason,
		NextStrategy: nextStrategy,
		Timestamp:    time.Now(),
	}
	ura.searchContext.DecisionPoints = append(ura.searchContext.DecisionPoints, decision)
}

func (ura *UnifiedReActAgent) calculateResultConfidence(results []*EnhancedSearchResult) float64 {
	if len(results) == 0 {
		return 0.0
	}

	totalConfidence := 0.0
	for _, result := range results {
		totalConfidence += result.Relevance
	}

	result := totalConfidence / float64(len(results))
	if result > 0.95 {
		return 0.95
	}
	return result
}

func (ura *UnifiedReActAgent) synthesizeProgressiveResults(results []*EnhancedSearchResult, request *SearchRequest, analysis *QueryAnalysis) *SearchResponse {
	response := &SearchResponse{
		Results:          results,
		TokensUsed:       ura.estimateTokensUsed(nil),
		SearchIterations: ura.searchContext.IterationCount,
	}

	// Generate structured synthesis based on user expertise
	switch analysis.UserExpertise {
	case "beginner":
		response.Synthesis = ura.generateBeginnerSynthesis(results, request.Query)
	case "expert":
		response.Synthesis = ura.generateExpertSynthesis(results, request.Query)
	default:
		response.Synthesis = ura.generateIntermediateSynthesis(results, request.Query)
	}

	return response
}

func (ura *UnifiedReActAgent) generateBeginnerSynthesis(results []*EnhancedSearchResult, query string) string {
	if len(results) == 0 {
		return "No relevant information found for your query."
	}

	// Simple, clear explanation
	synthesis := fmt.Sprintf("Found %d relevant results for '%s':\n\n", len(results), query)
	for i, result := range results {
		if i >= 3 { // Limit to top 3 for beginners
			break
		}
		synthesis += fmt.Sprintf("%d. %s\n", i+1, result.Explanation)
	}

	return synthesis
}

func (ura *UnifiedReActAgent) generateIntermediateSynthesis(results []*EnhancedSearchResult, query string) string {
	if len(results) == 0 {
		return "No relevant information found for your query."
	}

	// Balanced detail
	synthesis := fmt.Sprintf("## Search Results for: %s\n\n", query)
	synthesis += fmt.Sprintf("### Key Findings (%d sources)\n", len(results))

	for i, result := range results {
		if i >= 5 { // Show top 5
			break
		}
		synthesis += fmt.Sprintf("- %s (confidence: %.1f)\n", result.Explanation, result.Relevance)
	}

	return synthesis
}

func (ura *UnifiedReActAgent) generateExpertSynthesis(results []*EnhancedSearchResult, query string) string {
	if len(results) == 0 {
		return "No relevant information found for your query."
	}

	// Detailed technical synthesis
	synthesis := fmt.Sprintf("## Analysis: %s\n\n", query)
	synthesis += "### Results Summary\n"
	synthesis += fmt.Sprintf("- Total findings: %d\n", len(results))
	synthesis += fmt.Sprintf("- Search iterations: %d\n", ura.searchContext.IterationCount)
	synthesis += fmt.Sprintf("- Confidence evolution: %.2f -> %.2f\n\n",
		ura.searchContext.ConfidenceTracker[0],
		ura.searchContext.ConfidenceTracker[len(ura.searchContext.ConfidenceTracker)-1])

	synthesis += "### Detailed Findings\n"
	for i, result := range results {
		if i >= 10 { // Show more for experts
			break
		}
		synthesis += fmt.Sprintf("%d. [%s] %s\n   Relevance: %.2f | Category: %s\n",
			i+1, result.FilePath, result.Explanation, result.Relevance, result.Category)
	}

	return synthesis
}

// Using existing min function from util.go

// configureForQuery dynamically configures the ReAct agent based on query analysis.
func (ura *UnifiedReActAgent) configureForQuery(analysis *QueryAnalysis, request *SearchRequest) {
	// Update execution mode
	ura.currentMode = analysis.SuggestedMode

	// Dynamic configuration based on analysis
	// Note: MaxIterations is tracked in our execution logic since the ReAct agent
	// doesn't expose a method to update iterations after initialization

	// Update instruction with contextual information
	newInstruction := ura.generateContextualInstructions(analysis)

	// Create new signature with updated instruction
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.NewField("query", core.WithDescription("The search query to process"))},
			{Field: core.NewField("context", core.WithDescription("Additional context for the search"))},
		},
		[]core.OutputField{
			{Field: core.NewField("answer", core.WithDescription("The final search results and analysis"))},
		},
	).WithInstruction(newInstruction)

	// Re-initialize with new instruction
	llm := core.GetDefaultLLM()
	if err := ura.reactAgent.Initialize(llm, signature); err != nil {
		ura.logger.Warn(context.Background(), "Failed to reinitialize with new instruction: %v", err)
	}

	ura.logger.Info(context.Background(), "Configured agent: mode=%v, complexity=%d, max_iterations=%d, quality_target=%.2f",
		ura.currentMode, analysis.Complexity, analysis.MaxIterations, analysis.QualityTarget)
}

// generateContextualInstructions creates dynamic instructions based on query analysis.
func (ura *UnifiedReActAgent) generateContextualInstructions(analysis *QueryAnalysis) string {
	baseInstruction := "You are a specialized code search agent. "

	// Adapt based on query type
	switch analysis.PrimaryType {
	case CodeFocusedQuery:
		baseInstruction += `Your mission is to find and analyze code implementations.

FOCUS: Code implementation details, functions, classes, and patterns
STRATEGY: Start with search_files for relevant code files, then search_content for specific implementations
QUALITY: Prioritize working code examples with their context and usage patterns

`
	case GuidelineFocusedQuery:
		baseInstruction += `Your mission is to find best practices, standards, and guidelines.

FOCUS: Best practices, coding standards, and architectural guidelines
STRATEGY: Search documentation, README files, comment patterns, and established conventions
QUALITY: Look for authoritative sources and consistent recommendations

`
	case SemanticFocusedQuery:
		baseInstruction += `Your mission is to understand concepts, relationships, and meanings.

FOCUS: Understanding concepts, architectural relationships, and system design
STRATEGY: Broad search followed by deep contextual analysis across multiple files
QUALITY: Prioritize comprehensive understanding over quick answers

`
	case ContextFocusedQuery:
		baseInstruction += `Your mission is to understand relationships and dependencies.

FOCUS: Context relationships, dependencies, and interconnections
STRATEGY: Search for related files, trace dependencies, understand system interactions
QUALITY: Map connections between different parts of the system

`
	default:
		baseInstruction += `Your mission is to perform comprehensive code search and analysis.

FOCUS: General code and documentation search
STRATEGY: Systematic search across files and content
QUALITY: Balanced approach to code discovery and analysis

`
	}

	// Add complexity-specific guidance
	baseInstruction += fmt.Sprintf(`SEARCH PARAMETERS:
- Complexity Level: %d/5
- Max Iterations: %d
- Quality Target: %.2f
- User Expertise: %s

`,
		analysis.Complexity, analysis.MaxIterations, analysis.QualityTarget, analysis.UserExpertise)

	// Add phase-specific tool strategy if available
	if analysis.SearchStrategy != nil && len(analysis.SearchStrategy.Phases) > 0 {
		baseInstruction += "PROGRESSIVE TOOL STRATEGY:\n"
		for _, phase := range analysis.SearchStrategy.Phases {
			baseInstruction += fmt.Sprintf("Phase %d (%s): Use %v (max %d attempts)\n",
				phase.PhaseNumber, phase.Description, phase.Tools, phase.MaxAttempts)
		}
		baseInstruction += "\n"
	}

	// Add tool specifications (XML formatting handled by dspy-go natively)
	baseInstruction += `AVAILABLE TOOLS:

search_files - Find files by pattern (provide 'pattern' parameter)
search_content - Search text in files (provide 'query' parameter, optional 'path')
read_file - Read a specific file (provide 'file_path' parameter)
Finish - Complete the search (no parameters needed)

Tool parameters will be automatically formatted in XML by the system.
Focus on selecting the right tool and parameters for the task.

`

	// Add expertise-specific guidance
	switch analysis.UserExpertise {
	case "beginner":
		baseInstruction += "RESPONSE STYLE: Provide clear, simple explanations with examples. Avoid jargon.\n"
	case "expert":
		baseInstruction += "RESPONSE STYLE: Provide detailed technical analysis with architectural insights.\n"
	default:
		baseInstruction += "RESPONSE STYLE: Balanced detail with practical examples and context.\n"
	}

	baseInstruction += "\nTool parameters are automatically validated and parsed with XML support.\n"
	baseInstruction += fmt.Sprintf("Aim for %d-%d tool calls to thoroughly explore before completing the search.\n",
		max(2, analysis.MaxIterations-1), analysis.MaxIterations)

	return baseInstruction
}

// Using existing max function from util.go

// convertReActResult converts ReAct execution result to SearchResponse.
func (ura *UnifiedReActAgent) convertReActResult(result map[string]interface{}, request *SearchRequest, analysis *QueryAnalysis) (*SearchResponse, error) {
	response := &SearchResponse{
		Results:          []*EnhancedSearchResult{},
		Synthesis:        "ReAct agent execution completed successfully",
		TokensUsed:       ura.estimateTokensUsed(result),
		SearchIterations: ura.estimateIterations(result),
		Confidence:       0.8, // Default confidence
	}

	// Extract results from ReAct output - check both "answer" and "output" fields for compatibility
	if data, exists := result["answer"]; exists {
		if dataStr, ok := data.(string); ok && dataStr != "" {
			// Create a single enhanced result from the ReAct output
			enhancedResult := &EnhancedSearchResult{
				SearchResult: &SearchResult{
					FilePath:   "react-output",
					LineNumber: 1,
					Line:       dataStr,
					MatchType:  "react_agent",
					Score:      0.8,
				},
				Relevance:   0.8,
				Explanation: fmt.Sprintf("ReAct agent analysis for %s query", analysis.PrimaryType),
				Category:    string(analysis.PrimaryType),
			}
			response.Results = append(response.Results, enhancedResult)
		}
	} else if data, exists := result["output"]; exists {
		// Fallback to "output" field for backward compatibility
		if dataStr, ok := data.(string); ok {
			// Create a single enhanced result from the ReAct output
			enhancedResult := &EnhancedSearchResult{
				SearchResult: &SearchResult{
					FilePath:   "react-output",
					LineNumber: 1,
					Line:       dataStr,
					MatchType:  "react_agent",
					Score:      0.8,
				},
				Relevance:   0.8,
				Explanation: fmt.Sprintf("ReAct agent analysis for %s query", analysis.PrimaryType),
				Category:    string(analysis.PrimaryType),
			}
			response.Results = append(response.Results, enhancedResult)
		}
	}

	// If we have synthesis information
	if synthesis, exists := result["reasoning"]; exists {
		if synthesisStr, ok := synthesis.(string); ok {
			response.Synthesis = synthesisStr
		}
	}

	return response, nil
}

// Helper methods.
func (ura *UnifiedReActAgent) estimateTokensUsed(result map[string]interface{}) int {
	// Simple estimation based on result content
	tokens := 100 // Base tokens for processing

	if output, exists := result["output"]; exists {
		if outputStr, ok := output.(string); ok {
			tokens += len(strings.Split(outputStr, " "))
		}
	}

	return tokens
}

func (ura *UnifiedReActAgent) estimateIterations(result map[string]interface{}) int {
	// Default to 3 iterations - could be extracted from ReAct execution history
	return 3
}

func (ura *UnifiedReActAgent) formatGlobResults(results []string) string {
	var formatted []string
	for _, result := range results {
		formatted = append(formatted, fmt.Sprintf("📁 %s", result))
	}
	return strings.Join(formatted, "\n")
}

func (ura *UnifiedReActAgent) formatSearchResults(results []*SearchResult) string {
	var formatted []string
	for i, result := range results {
		if i >= 10 { // Limit results to prevent context overflow
			formatted = append(formatted, fmt.Sprintf("... and %d more results", len(results)-i))
			break
		}
		formatted = append(formatted, fmt.Sprintf("📄 %s:%d\n%s\nScore: %.2f",
			result.FilePath, result.LineNumber, result.Line, result.Score))
	}
	return strings.Join(formatted, "\n---\n")
}

// formatContentResults is now handled by formatSearchResults

func (ura *UnifiedReActAgent) updateStatus(state string, progress float64, err error) {
	ura.mu.Lock()
	defer ura.mu.Unlock()

	ura.status.State = state
	ura.status.Progress = progress
	ura.status.LastUpdate = time.Now()
	ura.status.Error = err
}

// GetStatus returns the current agent status.
func (ura *UnifiedReActAgent) GetStatus() AgentStatus {
	ura.mu.RLock()
	defer ura.mu.RUnlock()
	return ura.status
}

// GetCurrentMode returns the current execution mode.
func (ura *UnifiedReActAgent) GetCurrentMode() react.ExecutionMode {
	ura.mu.RLock()
	defer ura.mu.RUnlock()
	return ura.currentMode
}
