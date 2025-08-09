package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// SearchAgentType defines the type of search agent
type SearchAgentType string

const (
	CodeSearchAgent      SearchAgentType = "code"
	GuidelineSearchAgent SearchAgentType = "guideline"
	ContextSearchAgent   SearchAgentType = "context"
	SemanticSearchAgent  SearchAgentType = "semantic"
)

// SearchAgent represents a sub-agent with its own context window
type SearchAgent struct {
	ID            string
	Type          SearchAgentType
	ContextWindow *ContextWindow
	SearchTool    *SimpleSearchTool
	LLM           core.LLM
	Logger        *logging.Logger
	ParentQuery   string // Original query from parent
	Status        AgentStatus
	mu            sync.RWMutex
}

// AgentStatus tracks the agent's current state
type AgentStatus struct {
	State       string    // "idle", "searching", "synthesizing", "complete"
	Progress    float64   // 0.0 to 1.0
	LastUpdate  time.Time
	ResultCount int
	Error       error
}

// SearchRequest represents a search request for an agent
type SearchRequest struct {
	Query           string
	Context         string
	RequiredDepth   int      // How many iterations to explore
	FocusAreas      []string // Specific areas to focus on
	ExcludePatterns []string // Patterns to exclude
	MaxResults      int
	TimeLimit       time.Duration
}

// SearchResponse represents the agent's search results
type SearchResponse struct {
	AgentID          string
	Results          []*EnhancedSearchResult
	Synthesis        string // LLM-synthesized summary
	TokensUsed       int
	SearchIterations int
	Duration         time.Duration
	Confidence       float64
}

// EnhancedSearchResult extends basic search result with agent insights
type EnhancedSearchResult struct {
	*SearchResult
	Relevance   float64 // Agent-determined relevance
	Explanation string  // Why this result is relevant
	Category    string  // Categorization by agent
}

// NewSearchAgent creates a new search agent
func NewSearchAgent(id string, agentType SearchAgentType, maxTokens int, searchTool *SimpleSearchTool, logger *logging.Logger) *SearchAgent {
	return &SearchAgent{
		ID:            id,
		Type:          agentType,
		ContextWindow: NewContextWindow(maxTokens, logger),
		SearchTool:    searchTool,
		LLM:           core.GetDefaultLLM(),
		Logger:        logger,
		Status: AgentStatus{
			State:      "idle",
			LastUpdate: time.Now(),
		},
	}
}

// ExecuteSearch performs an iterative, intelligent search
func (sa *SearchAgent) ExecuteSearch(ctx context.Context, request *SearchRequest) (*SearchResponse, error) {
	startTime := time.Now()
	sa.updateStatus("searching", 0.0, nil)
	
	sa.Logger.Info(ctx, "Agent %s starting search: %s", sa.ID, request.Query)
	
	// Initialize response
	response := &SearchResponse{
		AgentID: sa.ID,
		Results: make([]*EnhancedSearchResult, 0),
	}
	
	// Perform iterative search based on agent type
	switch sa.Type {
	case CodeSearchAgent:
		err := sa.performCodeSearch(ctx, request, response)
		if err != nil {
			sa.updateStatus("error", 0.0, err)
			return nil, err
		}
		
	case GuidelineSearchAgent:
		err := sa.performGuidelineSearch(ctx, request, response)
		if err != nil {
			sa.updateStatus("error", 0.0, err)
			return nil, err
		}
		
	case ContextSearchAgent:
		err := sa.performContextSearch(ctx, request, response)
		if err != nil {
			sa.updateStatus("error", 0.0, err)
			return nil, err
		}
		
	case SemanticSearchAgent:
		err := sa.performSemanticSearch(ctx, request, response)
		if err != nil {
			sa.updateStatus("error", 0.0, err)
			return nil, err
		}
		
	default:
		return nil, fmt.Errorf("unsupported agent type: %s", sa.Type)
	}
	
	// Synthesize findings
	sa.updateStatus("synthesizing", 0.8, nil)
	synthesis, err := sa.synthesizeFindings(ctx, request, response.Results)
	if err != nil {
		sa.Logger.Warn(ctx, "Failed to synthesize findings: %v", err)
		synthesis = sa.fallbackSynthesis(response.Results)
	}
	response.Synthesis = synthesis
	
	// Calculate final metrics
	current, max := sa.ContextWindow.GetTokenUsage()
	response.TokensUsed = current
	response.Duration = time.Since(startTime)
	response.Confidence = sa.calculateConfidence(response.Results)
	
	sa.updateStatus("complete", 1.0, nil)
	sa.Logger.Info(ctx, "Agent %s completed search in %v: %d results, %d/%d tokens used",
		sa.ID, response.Duration, len(response.Results), current, max)
	
	return response, nil
}

// performCodeSearch executes code-focused search
func (sa *SearchAgent) performCodeSearch(ctx context.Context, request *SearchRequest, response *SearchResponse) error {
	sa.Logger.Debug(ctx, "Performing code search for: %s", request.Query)
	
	// Step 1: Find relevant functions and types
	sa.updateStatus("searching", 0.2, nil)
	structuralResults := sa.findCodeStructures(ctx, request.Query)
	
	// Step 2: Search for usage patterns
	sa.updateStatus("searching", 0.4, nil)
	usageResults := sa.findUsagePatterns(ctx, request.Query, structuralResults)
	
	// Step 3: Find related code
	sa.updateStatus("searching", 0.6, nil)
	relatedResults := sa.findRelatedCode(ctx, structuralResults, usageResults)
	
	// Add all results to context window
	allResults := append(structuralResults, usageResults...)
	allResults = append(allResults, relatedResults...)
	
	for _, result := range allResults {
		err := sa.ContextWindow.Add(ctx, &ContextItem{
			ID:       fmt.Sprintf("result-%d", len(response.Results)),
			Content:  result.formatForContext(),
			Priority: result.Relevance,
			ItemType: "code",
			Source:   sa.ID,
		})
		if err != nil {
			sa.Logger.Debug(ctx, "Context window full, stopping search")
			break
		}
		response.Results = append(response.Results, result)
	}
	
	response.SearchIterations = 3
	return nil
}

// performGuidelineSearch executes guideline-focused search
func (sa *SearchAgent) performGuidelineSearch(ctx context.Context, request *SearchRequest, response *SearchResponse) error {
	sa.Logger.Debug(ctx, "Performing guideline search for: %s", request.Query)
	
	// Extract key concepts from query
	concepts := sa.extractConcepts(ctx, request.Query)
	
	// Search for guidelines matching concepts
	var allResults []*EnhancedSearchResult
	for i, concept := range concepts {
		sa.updateStatus("searching", float64(i)/float64(len(concepts)), nil)
		
		results, err := sa.SearchTool.FuzzySearch(ctx, concept, "**/*.md", 0.6)
		if err != nil {
			continue
		}
		
		for _, r := range results {
			enhanced := &EnhancedSearchResult{
				SearchResult: r,
				Relevance:    sa.calculateRelevance(r, request),
				Category:     "guideline",
				Explanation:  fmt.Sprintf("Matches concept: %s", concept),
			}
			allResults = append(allResults, enhanced)
		}
	}
	
	// Add to context and response
	for _, result := range allResults {
		err := sa.ContextWindow.Add(ctx, &ContextItem{
			ID:       fmt.Sprintf("guideline-%d", len(response.Results)),
			Content:  result.formatForContext(),
			Priority: result.Relevance,
			ItemType: "guideline",
			Source:   sa.ID,
		})
		if err != nil {
			break
		}
		response.Results = append(response.Results, result)
	}
	
	response.SearchIterations = len(concepts)
	return nil
}

// performContextSearch gathers surrounding context
func (sa *SearchAgent) performContextSearch(ctx context.Context, request *SearchRequest, response *SearchResponse) error {
	sa.Logger.Debug(ctx, "Gathering context for: %s", request.Query)
	
	// Find files mentioned in the query
	files := sa.extractFileReferences(request.Query)
	
	// Gather context from each file
	for i, file := range files {
		sa.updateStatus("searching", float64(i)/float64(len(files)), nil)
		
		lines, err := sa.SearchTool.ReadFile(ctx, file, 0, 0)
		if err != nil {
			continue
		}
		
		// Create context summary
		summary := sa.summarizeFileContext(ctx, file, lines, request.Query)
		
		enhanced := &EnhancedSearchResult{
			SearchResult: &SearchResult{
				FilePath:  file,
				Line:      summary,
				MatchType: "context",
			},
			Relevance:   0.8,
			Category:    "context",
			Explanation: "File context",
		}
		
		sa.ContextWindow.Add(ctx, &ContextItem{
			ID:       fmt.Sprintf("context-%s", file),
			Content:  summary,
			Priority: 0.8,
			ItemType: "context",
			Source:   sa.ID,
		})
		
		response.Results = append(response.Results, enhanced)
	}
	
	return nil
}

// performSemanticSearch performs meaning-based search
func (sa *SearchAgent) performSemanticSearch(ctx context.Context, request *SearchRequest, response *SearchResponse) error {
	sa.Logger.Debug(ctx, "Performing semantic search for: %s", request.Query)
	
	// Use LLM to understand query intent
	intent := sa.understandQueryIntent(ctx, request.Query)
	
	// Generate search variations
	variations := sa.generateSearchVariations(ctx, intent)
	
	// Search with each variation
	for i, variation := range variations {
		sa.updateStatus("searching", float64(i)/float64(len(variations)), nil)
		
		results, err := sa.SearchTool.GrepSearch(ctx, variation, "**/*.go", 3)
		if err != nil {
			continue
		}
		
		for _, r := range results {
			enhanced := &EnhancedSearchResult{
				SearchResult: r,
				Relevance:    sa.calculateSemanticRelevance(r, intent),
				Category:     "semantic",
				Explanation:  fmt.Sprintf("Semantic match for: %s", variation),
			}
			response.Results = append(response.Results, enhanced)
		}
	}
	
	response.SearchIterations = len(variations)
	return nil
}

// Helper methods

func (sa *SearchAgent) findCodeStructures(ctx context.Context, query string) []*EnhancedSearchResult {
	var results []*EnhancedSearchResult
	
	// Search for functions
	funcResults, _ := sa.SearchTool.StructuralSearch(ctx, "function", query)
	for _, r := range funcResults {
		results = append(results, &EnhancedSearchResult{
			SearchResult: r,
			Relevance:    0.9,
			Category:     "function",
			Explanation:  "Function definition",
		})
	}
	
	// Search for types
	typeResults, _ := sa.SearchTool.StructuralSearch(ctx, "type", query)
	for _, r := range typeResults {
		results = append(results, &EnhancedSearchResult{
			SearchResult: r,
			Relevance:    0.8,
			Category:     "type",
			Explanation:  "Type definition",
		})
	}
	
	return results
}

func (sa *SearchAgent) findUsagePatterns(ctx context.Context, query string, structures []*EnhancedSearchResult) []*EnhancedSearchResult {
	var results []*EnhancedSearchResult
	
	// Find where structures are used
	for _, structure := range structures {
		// Extract name from structure
		name := sa.extractStructureName(structure.Line)
		if name == "" {
			continue
		}
		
		usages, _ := sa.SearchTool.GrepSearch(ctx, name, "**/*.go", 2)
		for _, u := range usages {
			results = append(results, &EnhancedSearchResult{
				SearchResult: u,
				Relevance:    0.7,
				Category:     "usage",
				Explanation:  fmt.Sprintf("Usage of %s", name),
			})
		}
	}
	
	return results
}

func (sa *SearchAgent) findRelatedCode(ctx context.Context, structures, usages []*EnhancedSearchResult) []*EnhancedSearchResult {
	// This would use more sophisticated logic to find related code
	// For now, return empty
	return []*EnhancedSearchResult{}
}

func (sa *SearchAgent) synthesizeFindings(ctx context.Context, request *SearchRequest, results []*EnhancedSearchResult) (string, error) {
	if len(results) == 0 {
		return "No relevant results found.", nil
	}
	
	// Prepare context for LLM
	var findings []string
	for i, result := range results {
		if i >= 10 { // Limit to top 10 for synthesis
			break
		}
		findings = append(findings, fmt.Sprintf("%d. [%s] %s\n   %s",
			i+1, result.Category, result.FilePath, result.Line))
	}
	
	prompt := fmt.Sprintf(`Synthesize these search results for the query: "%s"

Findings:
%s

Create a concise summary that highlights:
1. Key findings
2. Patterns observed
3. Relevant insights

Summary:`, request.Query, strings.Join(findings, "\n"))
	
	synthesis, err := sa.LLM.Generate(ctx, prompt, core.WithMaxTokens(500))
	if err != nil {
		return "", err
	}
	
	return synthesis, nil
}

func (sa *SearchAgent) fallbackSynthesis(results []*EnhancedSearchResult) string {
	if len(results) == 0 {
		return "No results found."
	}
	
	return fmt.Sprintf("Found %d results across %d categories.",
		len(results), sa.countCategories(results))
}

func (sa *SearchAgent) calculateConfidence(results []*EnhancedSearchResult) float64 {
	if len(results) == 0 {
		return 0.0
	}
	
	total := 0.0
	for _, r := range results {
		total += r.Relevance
	}
	
	return total / float64(len(results))
}

func (sa *SearchAgent) extractConcepts(ctx context.Context, query string) []string {
	// Simple extraction - in production, use NLP
	words := strings.Fields(query)
	var concepts []string
	
	for _, word := range words {
		if len(word) > 3 { // Skip short words
			concepts = append(concepts, word)
		}
	}
	
	return concepts
}

func (sa *SearchAgent) extractFileReferences(query string) []string {
	// Extract file paths from query
	var files []string
	words := strings.Fields(query)
	
	for _, word := range words {
		if strings.Contains(word, ".go") || strings.Contains(word, "/") {
			files = append(files, word)
		}
	}
	
	return files
}

func (sa *SearchAgent) summarizeFileContext(ctx context.Context, file string, lines []string, query string) string {
	// Simple summary - in production, use LLM
	return fmt.Sprintf("File %s contains %d lines of code related to %s",
		file, len(lines), query)
}

func (sa *SearchAgent) understandQueryIntent(ctx context.Context, query string) string {
	// Use LLM to understand intent
	prompt := fmt.Sprintf("What is the intent of this search query: %s\n\nIntent:", query)
	intent, _ := sa.LLM.Generate(ctx, prompt, core.WithMaxTokens(50))
	return intent
}

func (sa *SearchAgent) generateSearchVariations(ctx context.Context, intent string) []string {
	// Generate variations based on intent
	return []string{intent} // Simplified for now
}

func (sa *SearchAgent) calculateRelevance(result *SearchResult, request *SearchRequest) float64 {
	// Simple relevance calculation
	relevance := result.Score
	
	// Boost if file path contains focus areas
	for _, focus := range request.FocusAreas {
		if strings.Contains(result.FilePath, focus) {
			relevance += 0.1
		}
	}
	
	return min(relevance, 1.0)
}

func (sa *SearchAgent) calculateSemanticRelevance(result *SearchResult, intent string) float64 {
	// Simple semantic relevance
	if strings.Contains(strings.ToLower(result.Line), strings.ToLower(intent)) {
		return 0.8
	}
	return 0.5
}

func (sa *SearchAgent) extractStructureName(line string) string {
	// Extract function or type name from line
	parts := strings.Fields(line)
	for i, part := range parts {
		if part == "func" || part == "type" {
			if i+1 < len(parts) {
				return strings.TrimSuffix(parts[i+1], "(")
			}
		}
	}
	return ""
}

func (sa *SearchAgent) countCategories(results []*EnhancedSearchResult) int {
	categories := make(map[string]bool)
	for _, r := range results {
		categories[r.Category] = true
	}
	return len(categories)
}

func (sa *SearchAgent) updateStatus(state string, progress float64, err error) {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	
	sa.Status.State = state
	sa.Status.Progress = progress
	sa.Status.LastUpdate = time.Now()
	sa.Status.Error = err
}

func (er *EnhancedSearchResult) formatForContext() string {
	return fmt.Sprintf("[%s] %s:%d\n%s\nRelevance: %.2f\nExplanation: %s",
		er.Category, er.FilePath, er.LineNumber, er.Line,
		er.Relevance, er.Explanation)
}