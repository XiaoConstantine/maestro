package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// SearchPlanner analyzes queries and creates search strategies.
type SearchPlanner struct {
	llm    core.LLM
	logger *logging.Logger
}

// SearchPlan represents a comprehensive search strategy.
type SearchPlan struct {
	Query            string
	Intent           SearchIntent
	Strategy         *SpawnStrategy
	Steps            []*SearchStep
	ExpectedDuration time.Duration
	SuccessMetrics   *SuccessMetrics
}

// SearchIntent categorizes the type of search needed.
type SearchIntent struct {
	Primary    string   // "code_analysis", "guideline_check", "context_gathering", "debugging"
	Secondary  []string // Additional intents
	Complexity int      // 1-5 scale
	Scope      string   // "narrow", "medium", "broad"
	Keywords   []string // Key terms extracted from query
	FileHints  []string // File patterns to focus on
	Exclusions []string // Patterns to exclude
}

// SearchStep represents a single step in the search plan.
type SearchStep struct {
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

// SearchRouter routes queries to appropriate search strategies.
type SearchRouter struct {
	planner *SearchPlanner
	rules   []*RoutingRule
	logger  *logging.Logger
}

// RoutingRule defines conditions for routing decisions.
type RoutingRule struct {
	ID          string
	Condition   func(query string) bool
	AgentTypes  []SearchAgentType
	Priority    float64
	Description string
}

// NewSearchPlanner creates a new search planner.
func NewSearchPlanner(logger *logging.Logger) *SearchPlanner {
	return &SearchPlanner{
		llm:    core.GetDefaultLLM(),
		logger: logger,
	}
}

// CreateSearchPlan analyzes a query and creates a comprehensive search plan.
func (sp *SearchPlanner) CreateSearchPlan(ctx context.Context, query string, codeContext string) (*SearchPlan, error) {
	sp.logger.Info(ctx, "Creating search plan for query: %s", query)

	// Step 1: Analyze the query to understand intent
	intent, err := sp.analyzeIntent(ctx, query, codeContext)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze intent: %w", err)
	}

	// Step 2: Create spawn strategy based on intent
	strategy := sp.createSpawnStrategy(intent)

	// Step 3: Generate search steps
	steps := sp.generateSearchSteps(intent, strategy)

	// Step 4: Estimate duration and success metrics
	duration := sp.estimateDuration(steps)
	metrics := sp.defineSuccessMetrics(intent)

	plan := &SearchPlan{
		Query:            query,
		Intent:           *intent,
		Strategy:         strategy,
		Steps:            steps,
		ExpectedDuration: duration,
		SuccessMetrics:   metrics,
	}

	sp.logger.Info(ctx, "Created search plan with %d steps, estimated duration: %v",
		len(steps), duration)

	return plan, nil
}

// analyzeIntent uses LLM to understand the search query intent.
func (sp *SearchPlanner) analyzeIntent(ctx context.Context, query string, codeContext string) (*SearchIntent, error) {
	prompt := fmt.Sprintf(`Analyze this search query and determine the search intent:

Query: "%s"

Code Context: %s

Please determine:
1. Primary intent (code_analysis, guideline_check, context_gathering, debugging, or other)
2. Complexity level (1-5, where 1 is simple keyword search, 5 is complex multi-step analysis)
3. Scope (narrow, medium, broad)
4. Key terms to search for
5. File patterns to focus on (if any)
6. Patterns to exclude (if any)

Respond in this format:
Primary: [intent]
Complexity: [1-5]
Scope: [narrow/medium/broad]
Keywords: [comma-separated list]
Files: [patterns or "none"]
Exclude: [patterns or "none"]`, query, truncateText(codeContext, 500))

	llmResponse, err := sp.llm.Generate(ctx, prompt, core.WithMaxTokens(300))
	if err != nil {
		return nil, err
	}

	// Parse the LLM response
	intent := sp.parseIntentResponse(llmResponse.Content)

	// Add query-specific analysis
	intent.Keywords = append(intent.Keywords, sp.extractKeywords(query)...)
	intent.FileHints = append(intent.FileHints, sp.extractFileHints(query)...)

	return intent, nil
}

// parseIntentResponse parses the LLM response into SearchIntent.
func (sp *SearchPlanner) parseIntentResponse(response string) *SearchIntent {
	intent := &SearchIntent{
		Primary:    "code_analysis", // default
		Complexity: 3,               // default
		Scope:      "medium",        // default
	}

	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "Primary:") {
			intent.Primary = strings.TrimSpace(strings.TrimPrefix(line, "Primary:"))
		} else if strings.HasPrefix(line, "Complexity:") {
			if complexity := extractNumber(line); complexity > 0 {
				intent.Complexity = complexity
			}
		} else if strings.HasPrefix(line, "Scope:") {
			intent.Scope = strings.TrimSpace(strings.TrimPrefix(line, "Scope:"))
		} else if strings.HasPrefix(line, "Keywords:") {
			keywords := strings.TrimSpace(strings.TrimPrefix(line, "Keywords:"))
			if keywords != "none" {
				intent.Keywords = strings.Split(keywords, ",")
				for i, kw := range intent.Keywords {
					intent.Keywords[i] = strings.TrimSpace(kw)
				}
			}
		} else if strings.HasPrefix(line, "Files:") {
			files := strings.TrimSpace(strings.TrimPrefix(line, "Files:"))
			if files != "none" {
				intent.FileHints = strings.Split(files, ",")
				for i, f := range intent.FileHints {
					intent.FileHints[i] = strings.TrimSpace(f)
				}
			}
		} else if strings.HasPrefix(line, "Exclude:") {
			exclude := strings.TrimSpace(strings.TrimPrefix(line, "Exclude:"))
			if exclude != "none" {
				intent.Exclusions = strings.Split(exclude, ",")
				for i, e := range intent.Exclusions {
					intent.Exclusions[i] = strings.TrimSpace(e)
				}
			}
		}
	}

	return intent
}

// createSpawnStrategy creates an agent spawn strategy based on intent.
func (sp *SearchPlanner) createSpawnStrategy(intent *SearchIntent) *SpawnStrategy {
	strategy := &SpawnStrategy{
		MaxParallel:      3, // default
		TokensPerAgent:   100000,
		TypeDistribution: make(map[SearchAgentType]float64),
		AdaptiveSpawning: true,
	}

	// Adjust strategy based on intent
	switch intent.Primary {
	case "code_analysis":
		strategy.TypeDistribution[CodeSearchAgent] = 0.6
		strategy.TypeDistribution[ContextSearchAgent] = 0.3
		strategy.TypeDistribution[SemanticSearchAgent] = 0.1

	case "guideline_check":
		strategy.TypeDistribution[GuidelineSearchAgent] = 0.7
		strategy.TypeDistribution[CodeSearchAgent] = 0.3

	case "context_gathering":
		strategy.TypeDistribution[ContextSearchAgent] = 0.5
		strategy.TypeDistribution[CodeSearchAgent] = 0.3
		strategy.TypeDistribution[SemanticSearchAgent] = 0.2

	case "debugging":
		strategy.TypeDistribution[CodeSearchAgent] = 0.4
		strategy.TypeDistribution[ContextSearchAgent] = 0.3
		strategy.TypeDistribution[SemanticSearchAgent] = 0.3

	default:
		// Balanced approach
		strategy.TypeDistribution[CodeSearchAgent] = 0.4
		strategy.TypeDistribution[GuidelineSearchAgent] = 0.3
		strategy.TypeDistribution[ContextSearchAgent] = 0.2
		strategy.TypeDistribution[SemanticSearchAgent] = 0.1
	}

	// Adjust parallel agents based on complexity and scope
	if intent.Complexity >= 4 || intent.Scope == "broad" {
		strategy.MaxParallel = 5
	} else if intent.Complexity <= 2 && intent.Scope == "narrow" {
		strategy.MaxParallel = 2
	}

	return strategy
}

// generateSearchSteps creates ordered search steps.
func (sp *SearchPlanner) generateSearchSteps(intent *SearchIntent, strategy *SpawnStrategy) []*SearchStep {
	var steps []*SearchStep
	stepID := 0

	// Generate steps based on agent distribution
	for agentType, distribution := range strategy.TypeDistribution {
		if distribution > 0 {
			count := int(float64(strategy.MaxParallel) * distribution)
			if count < 1 {
				count = 1
			}

			for i := 0; i < count; i++ {
				stepID++
				step := &SearchStep{
					ID:          fmt.Sprintf("step-%d", stepID),
					Description: sp.generateStepDescription(agentType, intent),
					AgentType:   agentType,
					Request:     sp.createSearchRequest(agentType, intent),
					Priority:    distribution,
					Estimated:   sp.estimateStepDuration(agentType, intent.Complexity),
				}
				steps = append(steps, step)
			}
		}
	}

	return steps
}

// generateStepDescription creates a description for a search step.
func (sp *SearchPlanner) generateStepDescription(agentType SearchAgentType, intent *SearchIntent) string {
	switch agentType {
	case CodeSearchAgent:
		return fmt.Sprintf("Search for code patterns related to: %s", strings.Join(intent.Keywords, ", "))
	case GuidelineSearchAgent:
		return fmt.Sprintf("Find guidelines and best practices for: %s", intent.Primary)
	case ContextSearchAgent:
		return fmt.Sprintf("Gather context and related information for: %s", strings.Join(intent.Keywords, ", "))
	case SemanticSearchAgent:
		return fmt.Sprintf("Perform semantic analysis for: %s", intent.Primary)
	default:
		return "Generic search step"
	}
}

// createSearchRequest creates a SearchRequest for an agent type.
func (sp *SearchPlanner) createSearchRequest(agentType SearchAgentType, intent *SearchIntent) *SearchRequest {
	request := &SearchRequest{
		Query:           strings.Join(intent.Keywords, " "),
		Context:         intent.Primary,
		RequiredDepth:   intent.Complexity,
		FocusAreas:      intent.FileHints,
		ExcludePatterns: intent.Exclusions,
		MaxResults:      sp.getMaxResults(agentType, intent.Scope),
		TimeLimit:       sp.getTimeLimit(intent.Complexity),
	}

	return request
}

// Helper functions

func (sp *SearchPlanner) getMaxResults(agentType SearchAgentType, scope string) int {
	base := map[SearchAgentType]int{
		CodeSearchAgent:      20,
		GuidelineSearchAgent: 15,
		ContextSearchAgent:   10,
		SemanticSearchAgent:  25,
	}[agentType]

	switch scope {
	case "narrow":
		return base / 2
	case "broad":
		return base * 2
	default:
		return base
	}
}

func (sp *SearchPlanner) getTimeLimit(complexity int) time.Duration {
	base := time.Minute * 2
	return base * time.Duration(complexity)
}

func (sp *SearchPlanner) estimateStepDuration(agentType SearchAgentType, complexity int) time.Duration {
	base := map[SearchAgentType]time.Duration{
		CodeSearchAgent:      time.Second * 30,
		GuidelineSearchAgent: time.Second * 20,
		ContextSearchAgent:   time.Second * 25,
		SemanticSearchAgent:  time.Second * 45,
	}[agentType]

	return base * time.Duration(complexity)
}

func (sp *SearchPlanner) estimateDuration(steps []*SearchStep) time.Duration {
	maxDuration := time.Duration(0)
	for _, step := range steps {
		if step.Estimated > maxDuration {
			maxDuration = step.Estimated
		}
	}
	return maxDuration
}

func (sp *SearchPlanner) defineSuccessMetrics(intent *SearchIntent) *SuccessMetrics {
	metrics := &SuccessMetrics{
		MinResults:       5,
		MinConfidence:    0.6,
		MaxDuration:      time.Minute * 5,
		QualityThreshold: 0.7,
	}

	// Adjust based on intent
	switch intent.Primary {
	case "guideline_check":
		metrics.RequiredTypes = []string{"guideline"}
		metrics.MinResults = 3
	case "code_analysis":
		metrics.RequiredTypes = []string{"code", "context"}
		metrics.MinResults = 10
	case "debugging":
		metrics.MinResults = 15
		metrics.MinConfidence = 0.8
	}

	// Adjust based on complexity
	if intent.Complexity >= 4 {
		metrics.MaxDuration = time.Minute * 10
		metrics.MinResults *= 2
	}

	return metrics
}

// Utility functions

func (sp *SearchPlanner) extractKeywords(query string) []string {
	// Simple keyword extraction
	words := strings.Fields(strings.ToLower(query))
	var keywords []string

	// Filter out common words
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "is": true,
		"are": true, "was": true, "were": true, "be": true, "been": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "should": true, "could": true,
		"can": true, "may": true, "might": true, "must": true,
	}

	for _, word := range words {
		if len(word) > 2 && !stopWords[word] {
			keywords = append(keywords, word)
		}
	}

	return keywords
}

func (sp *SearchPlanner) extractFileHints(query string) []string {
	var hints []string
	words := strings.Fields(query)

	for _, word := range words {
		// Look for file extensions or paths
		if strings.Contains(word, ".go") || strings.Contains(word, ".md") || strings.Contains(word, "/") {
			hints = append(hints, word)
		}
	}

	return hints
}

// NewSearchRouter creates a new search router with default rules.
func NewSearchRouter(planner *SearchPlanner, logger *logging.Logger) *SearchRouter {
	router := &SearchRouter{
		planner: planner,
		logger:  logger,
		rules:   make([]*RoutingRule, 0),
	}

	// Add default routing rules
	router.addDefaultRules()

	return router
}

func (sr *SearchRouter) addDefaultRules() {
	sr.rules = []*RoutingRule{
		{
			ID: "function-search",
			Condition: func(query string) bool {
				return strings.Contains(strings.ToLower(query), "function") ||
					strings.Contains(strings.ToLower(query), "method")
			},
			AgentTypes:  []SearchAgentType{CodeSearchAgent, ContextSearchAgent},
			Priority:    0.9,
			Description: "Route function/method searches to code and context agents",
		},
		{
			ID: "best-practice",
			Condition: func(query string) bool {
				return strings.Contains(strings.ToLower(query), "best practice") ||
					strings.Contains(strings.ToLower(query), "guideline") ||
					strings.Contains(strings.ToLower(query), "standard")
			},
			AgentTypes:  []SearchAgentType{GuidelineSearchAgent},
			Priority:    0.95,
			Description: "Route guideline searches to guideline agents",
		},
		{
			ID: "error-debug",
			Condition: func(query string) bool {
				return strings.Contains(strings.ToLower(query), "error") ||
					strings.Contains(strings.ToLower(query), "bug") ||
					strings.Contains(strings.ToLower(query), "issue")
			},
			AgentTypes:  []SearchAgentType{CodeSearchAgent, SemanticSearchAgent},
			Priority:    0.8,
			Description: "Route debugging searches to code and semantic agents",
		},
	}
}

// Route determines the best search strategy for a query.
func (sr *SearchRouter) Route(ctx context.Context, query string) (*SearchPlan, error) {
	sr.logger.Debug(ctx, "Routing query: %s", query)

	// Check routing rules
	var matchedRule *RoutingRule
	for _, rule := range sr.rules {
		if rule.Condition(query) {
			if matchedRule == nil || rule.Priority > matchedRule.Priority {
				matchedRule = rule
			}
		}
	}

	if matchedRule != nil {
		sr.logger.Debug(ctx, "Matched routing rule: %s", matchedRule.ID)
	}

	// Create plan using planner
	return sr.planner.CreateSearchPlan(ctx, query, "")
}

// Utility functions

func extractNumber(line string) int {
	parts := strings.Fields(line)
	for _, part := range parts {
		if len(part) == 1 && part[0] >= '1' && part[0] <= '9' {
			return int(part[0] - '0')
		}
	}
	return 0
}

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
