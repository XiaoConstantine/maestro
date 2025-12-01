package main

import (
	"time"
)

// SearchAgentType defines the type of search agent.
type SearchAgentType string

const (
	CodeSearchAgent      SearchAgentType = "code"
	GuidelineSearchAgent SearchAgentType = "guideline"
	ContextSearchAgent   SearchAgentType = "context"
	SemanticSearchAgent  SearchAgentType = "semantic"
)

// AgentStatus tracks the agent's current state.
type AgentStatus struct {
	State       string  // "idle", "searching", "synthesizing", "complete"
	Progress    float64 // 0.0 to 1.0
	LastUpdate  time.Time
	ResultCount int
	Error       error
}

// SearchRequest represents a search request for an agent.
type SearchRequest struct {
	Query           string
	Context         string
	RequiredDepth   int      // How many iterations to explore
	FocusAreas      []string // Specific areas to focus on
	ExcludePatterns []string // Patterns to exclude
	MaxResults      int
	TimeLimit       time.Duration
}

// SearchResponse represents the agent's search results.
type SearchResponse struct {
	AgentID          string
	Results          []*EnhancedSearchResult
	Synthesis        string // LLM-synthesized summary
	TokensUsed       int
	SearchIterations int
	Duration         time.Duration
	Confidence       float64
}

// EnhancedSearchResult extends basic search result with agent insights.
type EnhancedSearchResult struct {
	*SearchResult
	Relevance   float64 // Agent-determined relevance
	Explanation string  // Why this result is relevant
	Category    string  // Categorization by agent
}
