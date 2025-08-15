package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// AgenticSearchOrchestrator is the main orchestrator that replaces traditional RAG.
type AgenticSearchOrchestrator struct {
	agentPool     *AgentPool
	searchPlanner *SearchPlanner
	searchRouter  *SearchRouter
	synthesizer   *ResultSynthesizer
	llm           core.LLM
	logger        *logging.Logger

	// Configuration
	config *OrchestrationConfig

	// State tracking
	activeSearches map[string]*SearchExecution
	mu             sync.RWMutex
}

// OrchestrationConfig configures the orchestrator behavior.
type OrchestrationConfig struct {
	MaxParallelSearches int
	MaxTokensPerSearch  int
	DefaultTimeout      time.Duration
	AdaptiveEnabled     bool
	SynthesisEnabled    bool
	RetryAttempts       int
}

// SearchExecution tracks a complete search operation.
type SearchExecution struct {
	ID          string
	Query       string
	Plan        *SearchPlan
	Agents      []*UnifiedReActAgent
	Responses   []*SearchResponse
	FinalResult *SynthesizedResult
	Status      ExecutionStatus
	StartTime   time.Time
	Duration    time.Duration
	mu          sync.RWMutex
}

// ExecutionStatus tracks the status of a search execution.
type ExecutionStatus struct {
	Phase       string  // "planning", "executing", "synthesizing", "complete", "failed"
	Progress    float64 // 0.0 to 1.0
	ActiveSteps int
	TotalSteps  int
	LastUpdate  time.Time
	Error       error
}

// SynthesizedResult is the final result after multi-agent synthesis.
type SynthesizedResult struct {
	Query           string
	Summary         string
	KeyFindings     []string
	CodeSamples     []CodeSample
	Guidelines      []Guideline
	ConfidenceScore float64
	TokensUsed      int
	AgentsInvolved  []string
	SearchDepth     int
	Metadata        map[string]interface{}
}

// CodeSample represents a synthesized code finding.
type CodeSample struct {
	FilePath    string
	Content     string
	Explanation string
	Relevance   float64
	Context     []string
}

// Guideline represents a synthesized guideline finding.
type Guideline struct {
	Title       string
	Description string
	Examples    []string
	Relevance   float64
	Source      string
}

// ResultSynthesizer combines results from multiple agents.
type ResultSynthesizer struct {
	llm    core.LLM
	logger *logging.Logger
}

// NewAgenticSearchOrchestrator creates a new orchestrator.
func NewAgenticSearchOrchestrator(searchTool *SimpleSearchTool, logger *logging.Logger) *AgenticSearchOrchestrator {
	config := &OrchestrationConfig{
		MaxParallelSearches: 3,
		MaxTokensPerSearch:  300000, // 300K tokens total (3 agents * 100K each)
		DefaultTimeout:      time.Minute * 10,
		AdaptiveEnabled:     true,
		SynthesisEnabled:    true,
		RetryAttempts:       2,
	}

	agentPool := NewAgentPool(5, 500000, searchTool, logger) // 5 agents max, 500K tokens total
	planner := NewSearchPlanner(logger)
	router := NewSearchRouter(planner, logger)
	synthesizer := NewResultSynthesizer(logger)

	return &AgenticSearchOrchestrator{
		agentPool:      agentPool,
		searchPlanner:  planner,
		searchRouter:   router,
		synthesizer:    synthesizer,
		llm:            core.GetDefaultLLM(),
		logger:         logger,
		config:         config,
		activeSearches: make(map[string]*SearchExecution),
	}
}

// ExecuteSearch performs a complete agentic search - this replaces FindSimilar.
func (aso *AgenticSearchOrchestrator) ExecuteSearch(ctx context.Context, query string, codeContext string) (*SynthesizedResult, error) {
	startTime := time.Now()
	searchID := fmt.Sprintf("search-%d", time.Now().UnixNano())

	aso.logger.Info(ctx, "🔍 Starting agentic search [%s]: %s", searchID, query)

	// Create search execution tracker
	execution := &SearchExecution{
		ID:        searchID,
		Query:     query,
		StartTime: startTime,
		Status: ExecutionStatus{
			Phase:      "planning",
			LastUpdate: startTime,
		},
	}

	// Register execution
	aso.mu.Lock()
	aso.activeSearches[searchID] = execution
	aso.mu.Unlock()

	defer func() {
		aso.mu.Lock()
		delete(aso.activeSearches, searchID)
		aso.mu.Unlock()
	}()

	// Step 1: Plan the search
	plan, err := aso.planSearch(ctx, query, codeContext, execution)
	if err != nil {
		execution.updateStatus("failed", 0, 0, 0, err)
		return nil, fmt.Errorf("planning failed: %w", err)
	}

	// Step 2: Execute multi-agent search
	responses, err := aso.executeMultiAgentSearch(ctx, plan, execution)
	if err != nil {
		execution.updateStatus("failed", 0, 0, 0, err)
		return nil, fmt.Errorf("execution failed: %w", err)
	}

	// Step 3: Synthesize results
	result, err := aso.synthesizeResults(ctx, query, responses, execution)
	if err != nil {
		execution.updateStatus("failed", 0, 0, 0, err)
		return nil, fmt.Errorf("synthesis failed: %w", err)
	}

	// Complete execution
	execution.Duration = time.Since(startTime)
	execution.FinalResult = result
	execution.updateStatus("complete", 1.0, 0, len(plan.Steps), nil)

	aso.logger.Info(ctx, "✅ Agentic search completed [%s] in %v: %d findings, %.2f confidence",
		searchID, execution.Duration, len(result.KeyFindings), result.ConfidenceScore)

	return result, nil
}

// planSearch creates a search plan.
func (aso *AgenticSearchOrchestrator) planSearch(ctx context.Context, query string, codeContext string, execution *SearchExecution) (*SearchPlan, error) {
	aso.logger.Debug(ctx, "Planning search for: %s", query)
	execution.updateStatus("planning", 0.1, 0, 0, nil)

	plan, err := aso.searchPlanner.CreateSearchPlan(ctx, query, codeContext)
	if err != nil {
		return nil, err
	}

	execution.Plan = plan
	execution.updateStatus("planning", 0.2, 0, len(plan.Steps), nil)

	return plan, nil
}

// executeMultiAgentSearch spawns agents and executes the search plan.
func (aso *AgenticSearchOrchestrator) executeMultiAgentSearch(ctx context.Context, plan *SearchPlan, execution *SearchExecution) ([]*SearchResponse, error) {
	aso.logger.Info(ctx, "Executing multi-agent search with %d steps", len(plan.Steps))
	execution.updateStatus("executing", 0.3, 0, len(plan.Steps), nil)

	// Create context with timeout
	searchCtx, cancel := context.WithTimeout(ctx, plan.ExpectedDuration*2)
	defer cancel()

	// Spawn agents based on strategy
	agents, err := aso.agentPool.SpawnAgentGroup(searchCtx, plan.Strategy, plan.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to spawn agents: %w", err)
	}

	execution.Agents = agents
	aso.logger.Info(ctx, "Spawned %d agents", len(agents))

	// Execute searches in parallel
	var wg sync.WaitGroup
	responses := make([]*SearchResponse, len(agents))
	errors := make([]error, len(agents))

	for i, agent := range agents {
		wg.Add(1)
		go func(idx int, a *UnifiedReActAgent) {
			defer wg.Done()

			// Create request for this agent (simplified for unified agents)
			request := aso.createAgentRequest(plan, SearchAgentType("unified"))

			// Execute search
			response, err := a.ExecuteSearch(searchCtx, request)
			if err != nil {
				errors[idx] = fmt.Errorf("agent %s failed: %w", a.ID, err)
				aso.logger.Warn(searchCtx, "Agent %s failed: %v", a.ID, err)
				return
			}

			responses[idx] = response
			aso.logger.Debug(searchCtx, "Agent %s completed: %d results", a.ID, len(response.Results))

			// Update progress
			execution.updateProgress()

		}(i, agent)
	}

	// Wait for all agents to complete
	wg.Wait()

	// Clean up agents
	for _, agent := range agents {
		if err := aso.agentPool.ReleaseAgent(ctx, agent.ID); err != nil {
			aso.logger.Warn(ctx, "Failed to release agent %s: %v", agent.ID, err)
		}
	}

	// Check for errors
	var validResponses []*SearchResponse
	for i, response := range responses {
		if errors[i] != nil {
			aso.logger.Warn(ctx, "Agent error: %v", errors[i])
			continue
		}
		if response != nil {
			validResponses = append(validResponses, response)
		}
	}

	if len(validResponses) == 0 {
		return nil, fmt.Errorf("all agents failed")
	}

	execution.Responses = validResponses
	return validResponses, nil
}

// synthesizeResults combines all agent responses into a final result.
func (aso *AgenticSearchOrchestrator) synthesizeResults(ctx context.Context, query string, responses []*SearchResponse, execution *SearchExecution) (*SynthesizedResult, error) {
	aso.logger.Info(ctx, "Synthesizing results from %d agents", len(responses))
	execution.updateStatus("synthesizing", 0.8, 0, len(execution.Plan.Steps), nil)

	result, err := aso.synthesizer.Synthesize(ctx, query, responses)
	if err != nil {
		return nil, fmt.Errorf("synthesis failed: %w", err)
	}

	// Add execution metadata
	result.AgentsInvolved = make([]string, len(responses))
	totalTokens := 0
	for i, response := range responses {
		result.AgentsInvolved[i] = response.AgentID
		totalTokens += response.TokensUsed
	}
	result.TokensUsed = totalTokens

	return result, nil
}

// Helper methods

func (aso *AgenticSearchOrchestrator) createAgentRequest(plan *SearchPlan, agentType SearchAgentType) *SearchRequest {
	// Find the step for this agent type
	for _, step := range plan.Steps {
		if step.AgentType == agentType {
			return step.Request
		}
	}

	// Fallback request
	return &SearchRequest{
		Query:         plan.Query,
		Context:       plan.Intent.Primary,
		RequiredDepth: plan.Intent.Complexity,
		MaxResults:    20,
		TimeLimit:     time.Minute * 2,
	}
}

// ResultSynthesizer implementation

func NewResultSynthesizer(logger *logging.Logger) *ResultSynthesizer {
	return &ResultSynthesizer{
		llm:    core.GetDefaultLLM(),
		logger: logger,
	}
}

func (rs *ResultSynthesizer) Synthesize(ctx context.Context, query string, responses []*SearchResponse) (*SynthesizedResult, error) {
	rs.logger.Debug(ctx, "Synthesizing results for query: %s", query)

	if len(responses) == 0 {
		return &SynthesizedResult{
			Query:           query,
			Summary:         "No results found.",
			ConfidenceScore: 0.0,
		}, nil
	}

	// Collect all results by category
	codeResults := rs.collectCodeResults(responses)
	guidelineResults := rs.collectGuidelineResults(responses)

	// Generate summary using LLM
	summary, err := rs.generateSummary(ctx, query, responses)
	if err != nil {
		rs.logger.Warn(ctx, "Failed to generate summary: %v", err)
		summary = rs.fallbackSummary(responses)
	}

	// Extract key findings
	keyFindings := rs.extractKeyFindings(responses)

	// Calculate confidence score
	confidence := rs.calculateOverallConfidence(responses)

	result := &SynthesizedResult{
		Query:           query,
		Summary:         summary,
		KeyFindings:     keyFindings,
		CodeSamples:     codeResults,
		Guidelines:      guidelineResults,
		ConfidenceScore: confidence,
		SearchDepth:     len(responses),
		Metadata: map[string]interface{}{
			"agent_count":    len(responses),
			"total_results":  rs.countTotalResults(responses),
			"synthesis_time": time.Now(),
		},
	}

	return result, nil
}

func (rs *ResultSynthesizer) collectCodeResults(responses []*SearchResponse) []CodeSample {
	var samples []CodeSample

	for _, response := range responses {
		for _, result := range response.Results {
			if result.Category == "code" || result.Category == "function" || result.Category == "type" {
				sample := CodeSample{
					FilePath:    result.FilePath,
					Content:     result.Line,
					Explanation: result.Explanation,
					Relevance:   result.Relevance,
					Context:     result.Context,
				}
				samples = append(samples, sample)
			}
		}
	}

	// Sort by relevance and take top samples
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].Relevance > samples[j].Relevance
	})

	if len(samples) > 10 {
		samples = samples[:10]
	}

	return samples
}

func (rs *ResultSynthesizer) collectGuidelineResults(responses []*SearchResponse) []Guideline {
	var guidelines []Guideline

	for _, response := range responses {
		for _, result := range response.Results {
			if result.Category == "guideline" {
				guideline := Guideline{
					Title:       fmt.Sprintf("Guideline from %s", result.FilePath),
					Description: result.Line,
					Relevance:   result.Relevance,
					Source:      result.FilePath,
				}
				guidelines = append(guidelines, guideline)
			}
		}
	}

	// Sort by relevance
	sort.Slice(guidelines, func(i, j int) bool {
		return guidelines[i].Relevance > guidelines[j].Relevance
	})

	if len(guidelines) > 5 {
		guidelines = guidelines[:5]
	}

	return guidelines
}

func (rs *ResultSynthesizer) generateSummary(ctx context.Context, query string, responses []*SearchResponse) (string, error) {
	// Prepare context for summary generation
	var findings []string
	for _, response := range responses {
		if response.Synthesis != "" {
			findings = append(findings, response.Synthesis)
		}
	}

	rs.logger.Debug(ctx, "Synthesis input - Query: %s, Findings count: %d", query, len(findings))
	for i, finding := range findings {
		truncated := finding
		if len(finding) > 100 {
			truncated = finding[:100] + "..."
		}
		rs.logger.Debug(ctx, "Finding %d: %s", i+1, truncated)
	}

	// If no findings, return early
	if len(findings) == 0 {
		rs.logger.Warn(ctx, "No synthesis data available from agents")
		return "No search results were found by the agents.", nil
	}

	findingsText := strings.Join(findings, "\n---\n")
	prompt := fmt.Sprintf(`You are analyzing search results from code search agents. The query was: "%s"

Here are the agent findings:
%s

IMPORTANT: The agents found actual results above. Do NOT say "couldn't find" or "no information". 
Instead, summarize what the agents actually discovered.

Provide a clear, helpful summary of what was found:`, query, findingsText)

	response, err := rs.llm.Generate(ctx, prompt, core.WithMaxTokens(500))
	if err != nil {
		rs.logger.Warn(ctx, "LLM generation failed: %v", err)
		return "", err
	}

	rs.logger.Debug(ctx, "Generated summary: %s", response.Content)
	return response.Content, nil
}

func (rs *ResultSynthesizer) fallbackSummary(responses []*SearchResponse) string {
	totalResults := rs.countTotalResults(responses)
	return fmt.Sprintf("Found %d results across %d agents with average confidence %.2f",
		totalResults, len(responses), rs.calculateOverallConfidence(responses))
}

func (rs *ResultSynthesizer) extractKeyFindings(responses []*SearchResponse) []string {
	var findings []string

	for _, response := range responses {
		// Take top results from each agent
		for i, result := range response.Results {
			if i >= 3 { // Top 3 per agent
				break
			}

			finding := fmt.Sprintf("%s: %s", result.FilePath, result.Explanation)
			findings = append(findings, finding)
		}
	}

	return findings
}

func (rs *ResultSynthesizer) calculateOverallConfidence(responses []*SearchResponse) float64 {
	if len(responses) == 0 {
		return 0.0
	}

	total := 0.0
	for _, response := range responses {
		total += response.Confidence
	}

	return total / float64(len(responses))
}

func (rs *ResultSynthesizer) countTotalResults(responses []*SearchResponse) int {
	total := 0
	for _, response := range responses {
		total += len(response.Results)
	}
	return total
}

// SearchExecution helper methods

func (se *SearchExecution) updateStatus(phase string, progress float64, activeSteps, totalSteps int, err error) {
	se.mu.Lock()
	defer se.mu.Unlock()

	se.Status.Phase = phase
	se.Status.Progress = progress
	se.Status.ActiveSteps = activeSteps
	se.Status.TotalSteps = totalSteps
	se.Status.LastUpdate = time.Now()
	se.Status.Error = err
}

func (se *SearchExecution) updateProgress() {
	se.mu.Lock()
	defer se.mu.Unlock()

	completed := 0
	for _, response := range se.Responses {
		if response != nil {
			completed++
		}
	}

	if se.Status.TotalSteps > 0 {
		se.Status.Progress = 0.3 + (0.5 * float64(completed) / float64(se.Status.TotalSteps))
	}
	se.Status.ActiveSteps = len(se.Agents) - completed
	se.Status.LastUpdate = time.Now()
}

// Public interface methods that replace RAG functions

// FindSimilarCode replaces the old RAG FindSimilar method.
func (aso *AgenticSearchOrchestrator) FindSimilarCode(ctx context.Context, query string, codeContext string) (*SynthesizedResult, error) {
	return aso.ExecuteSearch(ctx, query, codeContext)
}

// FindGuidelines replaces guideline-specific RAG queries.
func (aso *AgenticSearchOrchestrator) FindGuidelines(ctx context.Context, query string) (*SynthesizedResult, error) {
	guidelineQuery := fmt.Sprintf("guidelines and best practices for: %s", query)
	return aso.ExecuteSearch(ctx, guidelineQuery, "")
}

// GetSearchStatus returns the status of an active search.
func (aso *AgenticSearchOrchestrator) GetSearchStatus(searchID string) (*ExecutionStatus, bool) {
	aso.mu.RLock()
	defer aso.mu.RUnlock()

	if execution, exists := aso.activeSearches[searchID]; exists {
		execution.mu.RLock()
		status := execution.Status
		execution.mu.RUnlock()
		return &status, true
	}
	return nil, false
}

// Shutdown gracefully shuts down the orchestrator.
func (aso *AgenticSearchOrchestrator) Shutdown(ctx context.Context) error {
	aso.logger.Info(ctx, "Shutting down agentic search orchestrator...")
	return aso.agentPool.Shutdown(ctx)
}
