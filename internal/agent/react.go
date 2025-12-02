// Package agent provides agent pool and lifecycle management for code review.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/react"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/interceptors"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
	"github.com/XiaoConstantine/maestro/internal/search"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/xml"
	models "github.com/XiaoConstantine/mcp-go/pkg/model"
)

// UnifiedReActAgent wraps a dspy-go ReAct agent with dynamic specialization.
type UnifiedReActAgent struct {
	ID         string
	reactAgent *react.ReActAgent
	searchTool *search.SimpleSearchTool
	logger     *logging.Logger
	status     types.AgentStatus
	mu         sync.RWMutex

	// Dynamic configuration
	currentMode       react.ExecutionMode
	queryAnalyzer     *QueryAnalyzer
	searchContext     *SearchContext
	qualityTracker    *QualityTracker
	xmlFactory        *xml.ConfigFactory
	toolCallParser    *xml.ToolCallParser
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

// QualityAssessment represents the quality assessment of search results.
type QualityAssessment struct {
	Completeness   float64
	Consistency    float64
	Relevance      float64
	Actionability  float64
	OverallScore   float64
	MeetsThreshold bool
}

// SearchTool implements core.Tool for maestro search operations.
type SearchTool struct {
	name        string
	description string
	execute     func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error)
	searchTool  *search.SimpleSearchTool
}

// Implement core.Tool interface.
func (st *SearchTool) Name() string        { return st.name }
func (st *SearchTool) Description() string { return st.description }
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
func (st *SearchTool) Validate(params map[string]interface{}) error { return nil }
func (st *SearchTool) InputSchema() models.InputSchema             { return models.InputSchema{} }

// NewUnifiedReActAgent creates a new unified ReAct agent.
func NewUnifiedReActAgent(id string, searchTool *search.SimpleSearchTool, logger *logging.Logger) (*UnifiedReActAgent, error) {
	llm := core.GetDefaultLLM()

	// Create ReAct agent with flexible configuration
	reactAgent := react.NewReActAgent(
		fmt.Sprintf("unified-search-%s", id),
		"Adaptive search agent that specializes dynamically based on query type and complexity",
		react.WithExecutionMode(react.ModeReAct),
		react.WithReflection(false, 0),
		react.WithMaxIterations(15),
		react.WithTimeout(120*time.Second),
	)

	agent := &UnifiedReActAgent{
		ID:         id,
		reactAgent: reactAgent,
		searchTool: searchTool,
		logger:     logger,
		status: types.AgentStatus{
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
		xmlFactory:        xml.NewConfigFactory(),
		toolCallParser:    xml.NewToolCallParser(),
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
	).WithInstruction(getBaseInstruction())

	if err := reactAgent.Initialize(llm, signature); err != nil {
		return nil, fmt.Errorf("failed to initialize ReAct agent: %w", err)
	}

	return agent, nil
}

func getBaseInstruction() string {
	return "You are a code search assistant helping to find relevant code and information in a codebase.\n\n" +
		"AVAILABLE TOOLS:\n" +
		"- search_files: Find files by pattern (parameter: 'pattern')\n" +
		"- search_content: Search text in files (parameters: 'query', optional 'path')\n" +
		"- semantic_search: Find conceptually similar code using sgrep (parameter: 'query')\n" +
		"- sgrep_index: Index repository for semantic search (parameter: 'path')\n" +
		"- sgrep_status: Check if repository is indexed for semantic search\n" +
		"- read_file: Read file contents (parameter: 'file_path')\n" +
		"- Finish: Complete the search (no parameters)\n\n" +
		"Tool calls are automatically formatted in XML by the system.\n" +
		"For conceptual queries, prefer semantic_search over search_content."
}

// registerSearchTools registers maestro's search capabilities as ReAct tools.
func (ura *UnifiedReActAgent) registerSearchTools() error {
	ura.initializeXMLModules()
	return ura.registerSearchToolsWithXML()
}

// initializeXMLModules creates XML-enabled Predict modules for all tool types.
func (ura *UnifiedReActAgent) initializeXMLModules() {
	searchConfig := ura.xmlFactory.GetSearchToolConfig()
	fileConfig := ura.xmlFactory.GetFileToolConfig()
	generalConfig := ura.xmlFactory.GetGeneralConfig()

	ura.createXMLEnabledModule("search_files", "file pattern searching", searchConfig)
	ura.createXMLEnabledModule("search_content", "content searching", searchConfig)
	ura.createXMLEnabledModule("read_file", "file reading", fileConfig)
	ura.createXMLEnabledModule("Finish", "task completion", generalConfig)
}

// createXMLEnabledModule creates a Predict module with XML output for tool calling.
func (ura *UnifiedReActAgent) createXMLEnabledModule(toolName, description string, xmlConfig interceptors.XMLConfig) *modules.Predict {
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

	predict := modules.NewPredict(signature).WithXMLOutput(xmlConfig)
	ura.xmlEnabledModules[toolName] = predict
	return predict
}

// extractXMLParameters uses XML-enabled module to parse tool parameters.
func (ura *UnifiedReActAgent) extractXMLParameters(ctx context.Context, toolName string, rawParams map[string]interface{}) (map[string]interface{}, error) {
	_, exists := ura.xmlEnabledModules[toolName]
	if !exists {
		return rawParams, nil
	}

	if err := ura.toolCallParser.ValidateToolArguments(toolName, rawParams); err != nil {
		return nil, fmt.Errorf("parameter validation failed: %w", err)
	}

	return rawParams, nil
}

// registerSearchToolsWithXML registers tools with native XML parsing support.
func (ura *UnifiedReActAgent) registerSearchToolsWithXML() error {
	// File search tool
	fileSearchTool := &SearchTool{
		name:        "search_files",
		description: "Search for files by pattern, name, or extension. Use patterns like '*.go' for Go files.",
		searchTool:  ura.searchTool,
		execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
			validatedParams, err := ura.extractXMLParameters(ctx, "search_files", params)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("XML parameter extraction failed: %w", err)
			}

			pattern, ok := validatedParams["pattern"].(string)
			if !ok || pattern == "" {
				if p, exists := validatedParams["query"].(string); exists && p != "" {
					pattern = p
				} else if p, exists := validatedParams["search"].(string); exists && p != "" {
					pattern = p
				} else {
					return core.ToolResult{}, fmt.Errorf("pattern parameter required")
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
		description: "Search for specific text, patterns, or code within files using grep-like functionality.",
		searchTool:  ura.searchTool,
		execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
			validatedParams, err := ura.extractXMLParameters(ctx, "search_content", params)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("XML parameter extraction failed: %w", err)
			}

			query, ok := validatedParams["query"].(string)
			if !ok || query == "" {
				if q, exists := validatedParams["search"].(string); exists && q != "" {
					query = q
				} else if q, exists := validatedParams["text"].(string); exists && q != "" {
					query = q
				} else if q, exists := validatedParams["pattern"].(string); exists && q != "" {
					query = q
				} else {
					return core.ToolResult{}, fmt.Errorf("query parameter required")
				}
			}

			path := ""
			if p, exists := validatedParams["path"]; exists && p != nil {
				path = p.(string)
			}

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
					"result_count": len(results),
				},
			}, nil
		},
	}

	// File reading tool
	readFileTool := &SearchTool{
		name:        "read_file",
		description: "Read the complete contents of a specific file.",
		searchTool:  ura.searchTool,
		execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
			validatedParams, err := ura.extractXMLParameters(ctx, "read_file", params)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("XML parameter extraction failed: %w", err)
			}

			filePath, ok := validatedParams["file_path"].(string)
			if !ok || filePath == "" {
				if fp, exists := validatedParams["path"].(string); exists && fp != "" {
					filePath = fp
				} else if fp, exists := validatedParams["filepath"].(string); exists && fp != "" {
					filePath = fp
				} else if fp, exists := validatedParams["file"].(string); exists && fp != "" {
					filePath = fp
				} else {
					return core.ToolResult{}, fmt.Errorf("file_path parameter required")
				}
			}

			lines, err := ura.searchTool.ReadFile(ctx, filePath, 0, 0)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("file read failed: %w", err)
			}

			content := strings.Join(lines, "\n")
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
				},
			}, nil
		},
	}

	// Semantic search tool
	semanticSearchTool := &SearchTool{
		name:        "semantic_search",
		description: "Perform semantic code search using embeddings to find conceptually similar code.",
		searchTool:  ura.searchTool,
		execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
			validatedParams, err := ura.extractXMLParameters(ctx, "semantic_search", params)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("XML parameter extraction failed: %w", err)
			}

			query, ok := validatedParams["query"].(string)
			if !ok || query == "" {
				if q, exists := validatedParams["search"].(string); exists && q != "" {
					query = q
				} else {
					return core.ToolResult{}, fmt.Errorf("query parameter required")
				}
			}

			limit := 10
			if l, exists := validatedParams["limit"]; exists {
				if lInt, ok := l.(int); ok {
					limit = lInt
				}
			}

			results, err := ura.executeSgrepSearch(ctx, query, limit)
			if err != nil {
				// Fallback to content search if sgrep not available
				ura.logger.Debug(ctx, "sgrep semantic search unavailable, falling back to content search: %v", err)
				grepResults, grepErr := ura.searchTool.GrepSearch(ctx, query, "**/*", 2)
				if grepErr != nil {
					return core.ToolResult{}, fmt.Errorf("both semantic and text search failed: %w", grepErr)
				}
				return core.ToolResult{
					Data: ura.formatSearchResults(grepResults),
					Metadata: map[string]interface{}{
						"tool":         "semantic_search",
						"fallback":     "content_search",
						"result_count": len(grepResults),
					},
				}, nil
			}

			return core.ToolResult{
				Data: results,
				Metadata: map[string]interface{}{
					"tool":  "semantic_search",
					"query": query,
					"limit": limit,
				},
			}, nil
		},
	}

	// sgrep index tool
	sgrepIndexTool := &SearchTool{
		name:        "sgrep_index",
		description: "Index a directory for semantic code search.",
		searchTool:  ura.searchTool,
		execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
			path := "."
			if p, exists := params["path"].(string); exists && p != "" {
				path = p
			}

			sgrepTool := search.NewSgrepTool(ura.logger, ura.searchTool.RootPath())
			result, err := sgrepTool.Execute(ctx, map[string]interface{}{
				"action": "index",
				"path":   path,
			})
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("sgrep index failed: %w", err)
			}
			return result, nil
		},
	}

	// sgrep status tool
	sgrepStatusTool := &SearchTool{
		name:        "sgrep_status",
		description: "Check if the current repository is indexed for semantic search.",
		searchTool:  ura.searchTool,
		execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
			sgrepTool := search.NewSgrepTool(ura.logger, ura.searchTool.RootPath())
			result, err := sgrepTool.Execute(ctx, map[string]interface{}{
				"action": "status",
			})
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("sgrep status failed: %w", err)
			}
			return result, nil
		},
	}

	// Register all tools
	tools := []core.Tool{fileSearchTool, contentSearchTool, readFileTool, semanticSearchTool, sgrepIndexTool, sgrepStatusTool}
	for _, tool := range tools {
		if err := ura.reactAgent.RegisterTool(tool); err != nil {
			return fmt.Errorf("failed to register %s tool: %w", tool.Name(), err)
		}
	}

	return nil
}

// ExecuteSearch performs an intelligent search using dynamic ReAct configuration.
func (ura *UnifiedReActAgent) ExecuteSearch(ctx context.Context, request *search.SearchRequest) (*search.SearchResponse, error) {
	startTime := time.Now()
	ura.updateStatus("analyzing", 0.1, nil)
	ura.resetSearchContext()

	ura.logger.Info(ctx, "Unified ReAct Agent %s starting search: %s", ura.ID, request.Query)

	// Analyze the query to determine optimal configuration
	analysis, err := ura.queryAnalyzer.AnalyzeQuery(ctx, request.Query, request.Context)
	if err != nil {
		ura.logger.Warn(ctx, "Query analysis failed, using default configuration: %v", err)
		analysis = &QueryAnalysis{
			PrimaryType:   MixedQuery,
			SuggestedMode: react.ModeReAct,
			Complexity:    3,
			RequiredTools: []string{"search_content", "search_files"},
			MaxIterations: 3,
			QualityTarget: 0.7,
		}
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
func (ura *UnifiedReActAgent) executeProgressiveSearch(ctx context.Context, request *search.SearchRequest, analysis *QueryAnalysis) (*search.SearchResponse, error) {
	var allResults []*search.EnhancedSearchResult
	currentConfidence := 0.0

	for phaseIdx, phase := range analysis.SearchStrategy.Phases {
		ura.searchContext.CurrentPhase = phase.PhaseNumber
		ura.logger.Info(ctx, "Executing phase %d: %s", phase.PhaseNumber, phase.Description)

		if currentConfidence >= analysis.QualityTarget {
			ura.logger.Info(ctx, "Quality target reached (%.2f >= %.2f), skipping remaining phases",
				currentConfidence, analysis.QualityTarget)
			break
		}

		phaseInput := ura.preparePhaseInput(request, phase, allResults)
		phaseResult, err := ura.executePhase(ctx, phaseInput, phase, analysis.MaxIterations)
		if err != nil {
			ura.recordDecisionPoint(ura.searchContext.IterationCount,
				fmt.Sprintf("Phase %d failed: %v", phase.PhaseNumber, err),
				"Attempting next phase or recovery")
			continue
		}

		if enhancedResults := ura.processPhaseResults(phaseResult, phase); enhancedResults != nil {
			allResults = append(allResults, enhancedResults...)
			currentConfidence = ura.calculateResultConfidence(allResults)
			ura.searchContext.ConfidenceTracker = append(ura.searchContext.ConfidenceTracker, currentConfidence)
		}

		if phaseIdx < len(analysis.SearchStrategy.Phases)-1 &&
			currentConfidence < analysis.SearchStrategy.EscalationThreshold {
			ura.logger.Info(ctx, "Confidence %.2f below escalation threshold %.2f, proceeding to next phase",
				currentConfidence, analysis.SearchStrategy.EscalationThreshold)
		}
	}

	return ura.synthesizeProgressiveResults(allResults, request, analysis), nil
}

// preparePhaseInput prepares input for a specific search phase.
func (ura *UnifiedReActAgent) preparePhaseInput(request *search.SearchRequest, phase ToolPhase, previousResults []*search.EnhancedSearchResult) map[string]interface{} {
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
	for _, tool := range phase.Tools {
		ura.recordToolUsage(tool, phase.PhaseNumber, "Phase execution", 0.0)
	}

	phaseCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	result, err := ura.reactAgent.Execute(phaseCtx, input)
	if err != nil {
		return nil, err
	}

	ura.searchContext.IterationCount++
	return result, nil
}

// processPhaseResults processes results from a search phase.
func (ura *UnifiedReActAgent) processPhaseResults(result map[string]interface{}, phase ToolPhase) []*search.EnhancedSearchResult {
	var results []*search.EnhancedSearchResult

	if data, exists := result["answer"]; exists {
		if dataStr, ok := data.(string); ok && dataStr != "" {
			enhancedResult := &search.EnhancedSearchResult{
				SearchResult: &search.SearchResult{
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
			ura.recordFinding(dataStr, 0.7, phase.Description)
		}
	}

	return results
}

// executeErrorRecovery attempts to recover from search errors.
func (ura *UnifiedReActAgent) executeErrorRecovery(ctx context.Context, request *search.SearchRequest, analysis *QueryAnalysis, originalErr error) (*search.SearchResponse, error) {
	ura.logger.Warn(ctx, "Attempting error recovery for: %v", originalErr)

	simplifiedAnalysis := *analysis
	simplifiedAnalysis.Complexity = 2
	simplifiedAnalysis.MaxIterations = 2

	input := map[string]interface{}{
		"query":       request.Query,
		"context":     request.Context,
		"max_results": 5,
	}

	result, err := ura.reactAgent.Execute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("recovery failed: %w", err)
	}

	return ura.convertReActResult(result, request, &simplifiedAnalysis)
}

// validateResultQuality assesses the quality of search results.
func (ura *UnifiedReActAgent) validateResultQuality(response *search.SearchResponse, analysis *QueryAnalysis) *QualityAssessment {
	ura.qualityTracker.mu.Lock()
	defer ura.qualityTracker.mu.Unlock()

	assessment := &QualityAssessment{}

	if len(response.Results) > 0 {
		completeness := float64(len(response.Results)) / float64(analysis.Complexity*2)
		if completeness > 1.0 {
			completeness = 1.0
		}
		assessment.Completeness = completeness
	}

	assessment.Consistency = 0.8

	totalRelevance := 0.0
	for _, result := range response.Results {
		totalRelevance += result.Relevance
	}
	if len(response.Results) > 0 {
		assessment.Relevance = totalRelevance / float64(len(response.Results))
	}

	if response.Synthesis != "" {
		assessment.Actionability = 0.7
	}

	assessment.OverallScore = (assessment.Completeness + assessment.Consistency +
		assessment.Relevance + assessment.Actionability) / 4.0
	assessment.MeetsThreshold = assessment.OverallScore >= analysis.QualityTarget

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

func (ura *UnifiedReActAgent) calculateResultConfidence(results []*search.EnhancedSearchResult) float64 {
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

func (ura *UnifiedReActAgent) synthesizeProgressiveResults(results []*search.EnhancedSearchResult, request *search.SearchRequest, analysis *QueryAnalysis) *search.SearchResponse {
	response := &search.SearchResponse{
		Results:          results,
		TokensUsed:       ura.estimateTokensUsed(nil),
		SearchIterations: ura.searchContext.IterationCount,
	}

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

func (ura *UnifiedReActAgent) generateBeginnerSynthesis(results []*search.EnhancedSearchResult, query string) string {
	if len(results) == 0 {
		return "No relevant information found for your query."
	}

	synthesis := fmt.Sprintf("Found %d relevant results for '%s':\n\n", len(results), query)
	for i, result := range results {
		if i >= 3 {
			break
		}
		synthesis += fmt.Sprintf("%d. %s\n", i+1, result.Explanation)
	}

	return synthesis
}

func (ura *UnifiedReActAgent) generateIntermediateSynthesis(results []*search.EnhancedSearchResult, query string) string {
	if len(results) == 0 {
		return "No relevant information found for your query."
	}

	synthesis := fmt.Sprintf("## Search Results for: %s\n\n", query)
	synthesis += fmt.Sprintf("### Key Findings (%d sources)\n", len(results))

	for i, result := range results {
		if i >= 5 {
			break
		}
		synthesis += fmt.Sprintf("- %s (confidence: %.1f)\n", result.Explanation, result.Relevance)
	}

	return synthesis
}

func (ura *UnifiedReActAgent) generateExpertSynthesis(results []*search.EnhancedSearchResult, query string) string {
	if len(results) == 0 {
		return "No relevant information found for your query."
	}

	synthesis := fmt.Sprintf("## Analysis: %s\n\n", query)
	synthesis += "### Results Summary\n"
	synthesis += fmt.Sprintf("- Total findings: %d\n", len(results))
	synthesis += fmt.Sprintf("- Search iterations: %d\n", ura.searchContext.IterationCount)

	if len(ura.searchContext.ConfidenceTracker) > 0 {
		synthesis += fmt.Sprintf("- Confidence evolution: %.2f -> %.2f\n\n",
			ura.searchContext.ConfidenceTracker[0],
			ura.searchContext.ConfidenceTracker[len(ura.searchContext.ConfidenceTracker)-1])
	}

	synthesis += "### Detailed Findings\n"
	for i, result := range results {
		if i >= 10 {
			break
		}
		synthesis += fmt.Sprintf("%d. [%s] %s\n   Relevance: %.2f | Category: %s\n",
			i+1, result.FilePath, result.Explanation, result.Relevance, result.Category)
	}

	return synthesis
}

// configureForQuery dynamically configures the ReAct agent based on query analysis.
func (ura *UnifiedReActAgent) configureForQuery(analysis *QueryAnalysis, request *search.SearchRequest) {
	ura.currentMode = analysis.SuggestedMode

	newInstruction := ura.generateContextualInstructions(analysis)

	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.NewField("query", core.WithDescription("The search query to process"))},
			{Field: core.NewField("context", core.WithDescription("Additional context for the search"))},
		},
		[]core.OutputField{
			{Field: core.NewField("answer", core.WithDescription("The final search results and analysis"))},
		},
	).WithInstruction(newInstruction)

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

	baseInstruction += fmt.Sprintf(`SEARCH PARAMETERS:
- Complexity Level: %d/5
- Max Iterations: %d
- Quality Target: %.2f
- User Expertise: %s

`,
		analysis.Complexity, analysis.MaxIterations, analysis.QualityTarget, analysis.UserExpertise)

	if analysis.SearchStrategy != nil && len(analysis.SearchStrategy.Phases) > 0 {
		baseInstruction += "PROGRESSIVE TOOL STRATEGY:\n"
		for _, phase := range analysis.SearchStrategy.Phases {
			baseInstruction += fmt.Sprintf("Phase %d (%s): Use %v (max %d attempts)\n",
				phase.PhaseNumber, phase.Description, phase.Tools, phase.MaxAttempts)
		}
		baseInstruction += "\n"
	}

	baseInstruction += `AVAILABLE TOOLS:

search_files - Find files by pattern (provide 'pattern' parameter)
search_content - Search text in files (provide 'query' parameter, optional 'path')
semantic_search - Find conceptually similar code using embeddings (provide 'query' parameter)
read_file - Read a specific file (provide 'file_path' parameter)
Finish - Complete the search (no parameters needed)

Use semantic_search for natural language queries like 'error handling' or 'database connection'.
Tool parameters will be automatically formatted in XML by the system.
Focus on selecting the right tool and parameters for the task.

`

	switch analysis.UserExpertise {
	case "beginner":
		baseInstruction += "RESPONSE STYLE: Provide clear, simple explanations with examples. Avoid jargon.\n"
	case "expert":
		baseInstruction += "RESPONSE STYLE: Provide detailed technical analysis with architectural insights.\n"
	default:
		baseInstruction += "RESPONSE STYLE: Balanced detail with practical examples and context.\n"
	}

	baseInstruction += "\nTool parameters are automatically validated and parsed with XML support.\n"
	maxIter := analysis.MaxIterations
	minIter := maxIter - 1
	if minIter < 2 {
		minIter = 2
	}
	baseInstruction += fmt.Sprintf("Aim for %d-%d tool calls to thoroughly explore before completing the search.\n",
		minIter, maxIter)

	baseInstruction += `
CRITICAL: When you have gathered enough information to answer the user's question:
1. Call Finish immediately with <action><tool_name>Finish</tool_name></action>
2. Put your complete answer in the 'answer' field
3. DO NOT keep exploring if you already have the answer
4. Summarize your findings clearly in the answer field
`

	return baseInstruction
}

// convertReActResult converts ReAct execution result to SearchResponse.
func (ura *UnifiedReActAgent) convertReActResult(result map[string]interface{}, request *search.SearchRequest, analysis *QueryAnalysis) (*search.SearchResponse, error) {
	response := &search.SearchResponse{
		Results:          []*search.EnhancedSearchResult{},
		Synthesis:        "ReAct agent execution completed successfully",
		TokensUsed:       ura.estimateTokensUsed(result),
		SearchIterations: ura.estimateIterations(result),
		Confidence:       0.8,
	}

	if data, exists := result["answer"]; exists {
		if dataStr, ok := data.(string); ok && dataStr != "" {
			enhancedResult := &search.EnhancedSearchResult{
				SearchResult: &search.SearchResult{
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
		if dataStr, ok := data.(string); ok {
			enhancedResult := &search.EnhancedSearchResult{
				SearchResult: &search.SearchResult{
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

	if synthesis, exists := result["reasoning"]; exists {
		if synthesisStr, ok := synthesis.(string); ok {
			response.Synthesis = synthesisStr
		}
	}

	return response, nil
}

func (ura *UnifiedReActAgent) estimateTokensUsed(result map[string]interface{}) int {
	tokens := 100
	if output, exists := result["output"]; exists {
		if outputStr, ok := output.(string); ok {
			tokens += len(strings.Split(outputStr, " "))
		}
	}
	return tokens
}

func (ura *UnifiedReActAgent) estimateIterations(result map[string]interface{}) int {
	return 3
}

func (ura *UnifiedReActAgent) formatGlobResults(results []string) string {
	var formatted []string
	for _, result := range results {
		formatted = append(formatted, fmt.Sprintf("📁 %s", result))
	}
	return strings.Join(formatted, "\n")
}

func (ura *UnifiedReActAgent) formatSearchResults(results []*search.SearchResult) string {
	var formatted []string
	for i, result := range results {
		if i >= 10 {
			formatted = append(formatted, fmt.Sprintf("... and %d more results", len(results)-i))
			break
		}
		formatted = append(formatted, fmt.Sprintf("📄 %s:%d\n%s\nScore: %.2f",
			result.FilePath, result.LineNumber, result.Line, result.Score))
	}
	return strings.Join(formatted, "\n---\n")
}

// executeSgrepSearch runs sgrep CLI for semantic code search.
func (ura *UnifiedReActAgent) executeSgrepSearch(ctx context.Context, query string, limit int) (string, error) {
	checkCmd := exec.CommandContext(ctx, "which", "sgrep")
	if err := checkCmd.Run(); err != nil {
		return "", fmt.Errorf("sgrep not installed: %w", err)
	}

	cmd := exec.CommandContext(ctx, "sgrep", query, "-n", fmt.Sprintf("%d", limit), "--json")
	cmd.Dir = ura.searchTool.RootPath()

	ura.logger.Debug(ctx, "Executing sgrep: %s in %s", query, ura.searchTool.RootPath())

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "not indexed") {
				return "", fmt.Errorf("repository not indexed - run 'sgrep index .' first")
			}
			return "", fmt.Errorf("sgrep search failed: %s", stderr)
		}
		return "", fmt.Errorf("sgrep search failed: %w", err)
	}

	return ura.formatSgrepResults(string(output))
}

func (ura *UnifiedReActAgent) formatSgrepResults(jsonOutput string) (string, error) {
	var results []struct {
		FilePath  string  `json:"file_path"`
		StartLine int     `json:"start_line"`
		EndLine   int     `json:"end_line"`
		Content   string  `json:"content"`
		Score     float64 `json:"score"`
	}

	if err := json.Unmarshal([]byte(jsonOutput), &results); err != nil {
		return jsonOutput, nil
	}

	if len(results) == 0 {
		return "No semantic matches found.", nil
	}

	var formatted []string
	for i, r := range results {
		if i >= 10 {
			formatted = append(formatted, fmt.Sprintf("... and %d more results", len(results)-i))
			break
		}

		content := r.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}

		relevance := 1.0 - r.Score
		if relevance < 0 {
			relevance = 0
		}

		formatted = append(formatted, fmt.Sprintf("🔍 %s:%d-%d (relevance: %.2f)\n```\n%s\n```",
			r.FilePath, r.StartLine, r.EndLine, relevance, content))
	}

	return strings.Join(formatted, "\n\n"), nil
}

func (ura *UnifiedReActAgent) updateStatus(state string, progress float64, err error) {
	ura.mu.Lock()
	defer ura.mu.Unlock()

	ura.status.State = state
	ura.status.Progress = progress
	ura.status.LastUpdate = time.Now()
	ura.status.Error = err
}

// GetStatus returns the current agent status.
func (ura *UnifiedReActAgent) GetStatus() types.AgentStatus {
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
