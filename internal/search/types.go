// Package search provides search capabilities for code analysis.
package search

import (
	"time"

	"github.com/XiaoConstantine/maestro/internal/types"
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

// Result represents a search match.
type Result struct {
	FilePath   string
	LineNumber int
	Line       string
	Context    []string // Surrounding lines for context
	MatchType  string   // "exact", "pattern", "fuzzy"
	Score      float64  // Relevance score (0-1)
}

// SearchResult is an alias for Result for backward compatibility.
type SearchResult = Result

// SpawnStrategy determines how agents are spawned.
type SpawnStrategy struct {
	MaxParallel      int                               // Max agents running in parallel
	TokensPerAgent   int                               // Tokens allocated per agent
	TypeDistribution map[types.SearchAgentType]float64 // Distribution of agent types
	AdaptiveSpawning bool                              // Adapt based on results
}

// Plan represents a comprehensive search strategy.
type Plan struct {
	Query            string
	Intent           Intent
	Strategy         *SpawnStrategy
	Steps            []*Step
	ExpectedDuration time.Duration
	SuccessMetrics   *SuccessMetrics
}

// Intent categorizes the type of search needed.
type Intent struct {
	Primary    string   // "code_analysis", "guideline_check", "context_gathering", "debugging"
	Secondary  []string // Additional intents
	Complexity int      // 1-5 scale
	Scope      string   // "narrow", "medium", "broad"
	Keywords   []string // Key terms extracted from query
	FileHints  []string // File patterns to focus on
	Exclusions []string // Patterns to exclude
}

// Step represents a single step in the search plan.
type Step struct {
	ID           string
	Description  string
	AgentType    SearchAgentType
	Dependencies []string // IDs of steps that must complete first
	Request      *SearchRequest
	Priority     float64
	Estimated    time.Duration
}

// SuccessMetrics defines what constitutes a successful search.
type SuccessMetrics struct {
	MinResults       int
	MinConfidence    float64
	RequiredTypes    []string
	MaxDuration      time.Duration
	QualityThreshold float64
}

// RoutingRule defines conditions for routing decisions.
type RoutingRule struct {
	ID          string
	Condition   func(query string) bool
	AgentTypes  []SearchAgentType
	Priority    float64
	Description string
}

// SgrepSearchResult represents a single sgrep search result.
type SgrepSearchResult struct {
	FilePath  string  `json:"file_path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
}
