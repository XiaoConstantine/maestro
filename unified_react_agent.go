package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/react"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
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
	currentMode   react.ExecutionMode
	queryAnalyzer *QueryAnalyzer
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
	PrimaryType   QueryType
	SecondaryType QueryType
	Confidence    float64
	Keywords      []string
	Complexity    int // 1-5 scale
	SuggestedMode react.ExecutionMode
	RequiredTools []string
}

// QueryAnalyzer analyzes search queries to determine optimal configuration.
type QueryAnalyzer struct {
	llm    core.LLM
	logger *logging.Logger
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
	}
}

// AnalyzeQuery analyzes a search query and returns optimization suggestions.
func (qa *QueryAnalyzer) AnalyzeQuery(ctx context.Context, query, context string) (*QueryAnalysis, error) {
	// Simplified analysis based on keywords for now
	query = strings.ToLower(query)
	_ = context // parameter is used in analysis logic below

	analysis := &QueryAnalysis{
		PrimaryType:   MixedQuery,
		Confidence:    0.7,
		Complexity:    3,
		SuggestedMode: react.ModeReAct,
		Keywords:      strings.Fields(query),
		RequiredTools: []string{"search_content", "search_files"},
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

	return analysis, nil
}

// NewUnifiedReActAgent creates a new unified ReAct agent.
func NewUnifiedReActAgent(id string, searchTool *SimpleSearchTool, logger *logging.Logger) (*UnifiedReActAgent, error) {
	llm := core.GetDefaultLLM()

	// Create ReAct agent with flexible configuration
	reactAgent := react.NewReActAgent(
		fmt.Sprintf("unified-search-%s", id),
		"Adaptive search agent that specializes dynamically based on query type and complexity",
		react.WithExecutionMode(react.ModeReAct), // Start with ReAct mode
		react.WithReflection(true, 2),            // Enable reflection with moderate depth
		react.WithMaxIterations(6),               // Reduced to encourage earlier completion
		react.WithTimeout(5*time.Minute),         // 5 minute timeout
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

	// Add system instruction to help the agent understand available tools and XML format
	// Note: ReAct module will prepend its own formatting instructions for thought/action/observation fields
	signature = signature.WithInstruction(
		"You are a code search assistant helping to find relevant code and information in a codebase.\n\n" +
			"SEARCH STRATEGY:\n" +
			"1. Start by using search_files to find relevant files\n" +
			"2. Use search_content to search within those files\n" +
			"3. Use read_file to examine specific files in detail\n" +
			"4. Call Finish when you have found sufficient information\n\n" +
			"AVAILABLE TOOLS WITH EXACT XML FORMAT:\n\n" +
			"search_files - Find files by pattern:\n" +
			"<action><tool_name>search_files</tool_name><arguments><arg key=\"pattern\">*.go</arg></arguments></action>\n\n" +
			"search_content - Search text in files:\n" +
			"<action><tool_name>search_content</tool_name><arguments><arg key=\"query\">function main</arg></arguments></action>\n\n" +
			"read_file - Read a specific file:\n" +
			"<action><tool_name>read_file</tool_name><arguments><arg key=\"file_path\">path/to/file.go</arg></arguments></action>\n\n" +
			"Finish - Complete the search:\n" +
			"<action><tool_name>Finish</tool_name></action>\n\n" +
			"CRITICAL: Use EXACT parameter names: 'pattern' for search_files, 'query' for search_content, 'file_path' for read_file.\n" +
			"Limit to 3-4 tool calls before using Finish.",
	)

	if err := reactAgent.Initialize(llm, signature); err != nil {
		return nil, fmt.Errorf("failed to initialize ReAct agent: %w", err)
	}

	return agent, nil
}

// registerSearchTools registers maestro's search capabilities as ReAct tools.
func (ura *UnifiedReActAgent) registerSearchTools() error {
	// File search tool
	fileSearchTool := &SearchTool{
		name:        "search_files",
		description: "Search for files by pattern, name, or extension. Use patterns like '*.go' for Go files, 'test*' for test files, or specific names. This finds files in the codebase matching your pattern.",
		searchTool:  ura.searchTool,
		execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
			pattern, ok := params["pattern"].(string)
			if !ok || pattern == "" {
				// Check for alternative parameter names
				if p, exists := params["query"].(string); exists && p != "" {
					pattern = p
				} else if p, exists := params["search"].(string); exists && p != "" {
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
			query, ok := params["query"].(string)
			if !ok || query == "" {
				// Check for alternative parameter names
				if q, exists := params["search"].(string); exists && q != "" {
					query = q
				} else if q, exists := params["text"].(string); exists && q != "" {
					query = q
				} else if q, exists := params["pattern"].(string); exists && q != "" {
					query = q
				} else {
					return core.ToolResult{}, fmt.Errorf("query parameter required - provide the text or pattern to search for")
				}
			}

			path := ""
			if p, exists := params["path"]; exists && p != nil {
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
			// Log received parameters for debugging
			ura.logger.Debug(ctx, "read_file received params: %v", params)

			filePath, ok := params["file_path"].(string)
			if !ok || filePath == "" {
				// Check for alternative parameter names that the LLM might use
				if fp, exists := params["path"].(string); exists && fp != "" {
					filePath = fp
				} else if fp, exists := params["filepath"].(string); exists && fp != "" {
					filePath = fp
				} else if fp, exists := params["filename"].(string); exists && fp != "" {
					filePath = fp
				} else if fp, exists := params["file"].(string); exists && fp != "" {
					filePath = fp
				} else {
					// Log all available parameters for debugging
					var availableKeys []string
					for k := range params {
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
		}
	}

	ura.logger.Info(ctx, "Query analysis: type=%s, mode=%v, complexity=%d",
		analysis.PrimaryType, analysis.SuggestedMode, analysis.Complexity)

	// Configure the ReAct agent based on analysis
	ura.configureForQuery(analysis, request)

	ura.updateStatus("searching", 0.3, nil)

	// Create input for the ReAct agent
	input := map[string]interface{}{
		"query":            request.Query,
		"context":          request.Context,
		"max_results":      request.MaxResults,
		"search_depth":     request.RequiredDepth,
		"exclude_patterns": request.ExcludePatterns,
		"focus_areas":      request.FocusAreas,
	}

	// Execute the ReAct agent
	result, err := ura.reactAgent.Execute(ctx, input)
	if err != nil {
		ura.updateStatus("failed", 0.0, err)
		return nil, fmt.Errorf("ReAct agent execution failed: %w", err)
	}

	ura.updateStatus("synthesizing", 0.8, nil)

	// Convert ReAct result to SearchResponse
	response, err := ura.convertReActResult(result, request, analysis)
	if err != nil {
		ura.updateStatus("failed", 0.0, err)
		return nil, fmt.Errorf("result conversion failed: %w", err)
	}

	response.Duration = time.Since(startTime)
	response.AgentID = ura.ID

	ura.updateStatus("complete", 1.0, nil)
	ura.logger.Info(ctx, "Unified ReAct Agent %s completed search in %v with %d results",
		ura.ID, response.Duration, len(response.Results))

	return response, nil
}

// configureForQuery dynamically configures the ReAct agent based on query analysis.
func (ura *UnifiedReActAgent) configureForQuery(analysis *QueryAnalysis, request *SearchRequest) {
	// Update execution mode
	ura.currentMode = analysis.SuggestedMode

	ura.logger.Info(context.Background(), "Configured agent: mode=%v, complexity=%d",
		ura.currentMode, analysis.Complexity)
}

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
