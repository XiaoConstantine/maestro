package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/google/uuid"
)

// AgentPool manages a pool of unified ReAct search agents
type AgentPool struct {
	agents      map[string]*UnifiedReActAgent
	maxAgents   int
	activeCount int
	searchTool  *SimpleSearchTool
	logger      *logging.Logger
	mu          sync.RWMutex
	
	// Lifecycle management
	lifecycle   *AgentLifecycleManager
	
	// Resource tracking
	totalTokensUsed int
	maxTotalTokens  int
}

// AgentLifecycleManager handles agent lifecycle events
type AgentLifecycleManager struct {
	spawned   map[string]time.Time
	completed map[string]time.Time
	metrics   map[string]*AgentMetrics
	mu        sync.RWMutex
	logger    *logging.Logger
}

// AgentMetrics tracks performance metrics for an agent
type AgentMetrics struct {
	AgentID         string
	Type            SearchAgentType
	SpawnTime       time.Time
	CompleteTime    time.Time
	TokensUsed      int
	ResultsFound    int
	SearchDuration  time.Duration
	IterationCount  int
	SuccessRate     float64
}

// SpawnStrategy determines how agents are spawned
type SpawnStrategy struct {
	MaxParallel      int                          // Max agents running in parallel
	TokensPerAgent   int                          // Tokens allocated per agent
	TypeDistribution map[SearchAgentType]float64  // Distribution of agent types
	AdaptiveSpawning bool                         // Adapt based on results
}

// NewAgentPool creates a new agent pool
func NewAgentPool(maxAgents int, maxTotalTokens int, searchTool *SimpleSearchTool, logger *logging.Logger) *AgentPool {
	return &AgentPool{
		agents:         make(map[string]*UnifiedReActAgent),
		maxAgents:      maxAgents,
		maxTotalTokens: maxTotalTokens,
		searchTool:     searchTool,
		logger:         logger,
		lifecycle:      NewAgentLifecycleManager(logger),
	}
}

// NewAgentLifecycleManager creates a new lifecycle manager
func NewAgentLifecycleManager(logger *logging.Logger) *AgentLifecycleManager {
	return &AgentLifecycleManager{
		spawned:   make(map[string]time.Time),
		completed: make(map[string]time.Time),
		metrics:   make(map[string]*AgentMetrics),
		logger:    logger,
	}
}

// SpawnUnifiedAgent creates a new unified ReAct search agent
func (ap *AgentPool) SpawnUnifiedAgent(ctx context.Context, parentQuery string) (*UnifiedReActAgent, error) {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	
	// Check if we can spawn more agents
	if ap.activeCount >= ap.maxAgents {
		return nil, fmt.Errorf("agent pool at capacity (%d/%d)", ap.activeCount, ap.maxAgents)
	}
	
	// Check token budget
	tokensPerAgent := 100000 // 100K tokens per agent as per Claude Code pattern
	if ap.totalTokensUsed+tokensPerAgent > ap.maxTotalTokens {
		return nil, fmt.Errorf("insufficient token budget for new agent")
	}
	
	// Create agent ID
	agentID := fmt.Sprintf("unified-%s", uuid.New().String()[:8])
	
	// Create the unified ReAct agent
	agent, err := NewUnifiedReActAgent(agentID, ap.searchTool, ap.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create unified ReAct agent: %w", err)
	}
	
	// Register with pool
	ap.agents[agentID] = agent
	ap.activeCount++
	ap.totalTokensUsed += tokensPerAgent
	
	// Register lifecycle event (using MixedQuery as a general type)
	ap.lifecycle.OnSpawn(agentID, SearchAgentType("unified"))
	
	ap.logger.Info(ctx, "Spawned unified ReAct agent %s - Pool: %d/%d agents, %d/%d tokens",
		agentID, ap.activeCount, ap.maxAgents, ap.totalTokensUsed, ap.maxTotalTokens)
	
	return agent, nil
}

// SpawnAgent creates a new search agent (backward compatibility)
func (ap *AgentPool) SpawnAgent(ctx context.Context, agentType SearchAgentType, parentQuery string) (*UnifiedReActAgent, error) {
	// For now, all agent types use the unified ReAct agent
	return ap.SpawnUnifiedAgent(ctx, parentQuery)
}

// SpawnAgentGroup spawns multiple unified ReAct agents based on strategy
func (ap *AgentPool) SpawnAgentGroup(ctx context.Context, strategy *SpawnStrategy, parentQuery string) ([]*UnifiedReActAgent, error) {
	ap.logger.Info(ctx, "Spawning unified ReAct agent group - target count: %d", strategy.MaxParallel)
	
	var agents []*UnifiedReActAgent
	var errors []error
	
	// For unified agents, we spawn the requested number of agents
	// The agents will dynamically specialize based on query analysis
	for i := 0; i < strategy.MaxParallel; i++ {
		agent, err := ap.SpawnUnifiedAgent(ctx, parentQuery)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to spawn unified agent %d: %w", i+1, err))
			// Continue trying to spawn remaining agents
			continue
		}
		agents = append(agents, agent)
	}
	
	if len(errors) > 0 && len(agents) == 0 {
		return nil, fmt.Errorf("failed to spawn any agents: %v", errors)
	}
	
	if len(errors) > 0 {
		ap.logger.Warn(ctx, "Spawned %d/%d agents with %d errors", len(agents), strategy.MaxParallel, len(errors))
	} else {
		ap.logger.Info(ctx, "Successfully spawned %d unified ReAct agents", len(agents))
	}
	
	return agents, nil
}

// ReleaseAgent releases an agent back to the pool
func (ap *AgentPool) ReleaseAgent(ctx context.Context, agentID string) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	
	agent, exists := ap.agents[agentID]
	if !exists {
		return fmt.Errorf("agent %s not found in pool", agentID)
	}
	
	// Get token usage before releasing (simplified for UnifiedReActAgent)
	tokensUsed := 100000 // Estimate for now
	
	// Update lifecycle
	status := agent.GetStatus()
	ap.lifecycle.OnComplete(agentID, tokensUsed, status.ResultCount)
	
	// Clean up
	delete(ap.agents, agentID)
	ap.activeCount--
	ap.totalTokensUsed -= 100000 // Release the reserved tokens
	
	ap.logger.Info(ctx, "Released agent %s - Pool: %d/%d agents", agentID, ap.activeCount, ap.maxAgents)
	
	return nil
}

// GetActiveAgents returns all active agents
func (ap *AgentPool) GetActiveAgents() []*UnifiedReActAgent {
	ap.mu.RLock()
	defer ap.mu.RUnlock()
	
	agents := make([]*UnifiedReActAgent, 0, len(ap.agents))
	for _, agent := range ap.agents {
		agents = append(agents, agent)
	}
	return agents
}

// WaitForCompletion waits for all agents to complete
func (ap *AgentPool) WaitForCompletion(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for agents to complete")
			}
			
			ap.mu.RLock()
			activeCount := ap.activeCount
			ap.mu.RUnlock()
			
			if activeCount == 0 {
				return nil
			}
		}
	}
}

// AdaptiveSpawn spawns agents based on current results
func (ap *AgentPool) AdaptiveSpawn(ctx context.Context, currentResults []*SearchResponse) ([]*UnifiedReActAgent, error) {
	ap.logger.Info(ctx, "Adaptive spawning based on %d current results", len(currentResults))
	
	// Analyze current results to determine what's needed
	needs := ap.analyzeResultGaps(currentResults)
	
	// Create adaptive strategy
	strategy := &SpawnStrategy{
		MaxParallel:      3,
		TokensPerAgent:   100000,
		TypeDistribution: needs,
		AdaptiveSpawning: true,
	}
	
	return ap.SpawnAgentGroup(ctx, strategy, "")
}

// calculateAgentDistribution determines how many agents of each type to spawn
func (ap *AgentPool) calculateAgentDistribution(strategy *SpawnStrategy) map[SearchAgentType]int {
	distribution := make(map[SearchAgentType]int)
	
	if strategy.TypeDistribution == nil || len(strategy.TypeDistribution) == 0 {
		// Default distribution
		distribution[CodeSearchAgent] = 1
		distribution[GuidelineSearchAgent] = 1
		return distribution
	}
	
	// Calculate based on percentages
	total := strategy.MaxParallel
	allocated := 0
	
	for agentType, percentage := range strategy.TypeDistribution {
		count := int(float64(total) * percentage)
		if count > 0 {
			distribution[agentType] = count
			allocated += count
		}
	}
	
	// Allocate remaining slots to highest priority type
	if allocated < total {
		distribution[CodeSearchAgent] += (total - allocated)
	}
	
	return distribution
}

// analyzeResultGaps identifies what types of agents are needed based on current results
func (ap *AgentPool) analyzeResultGaps(results []*SearchResponse) map[SearchAgentType]float64 {
	needs := make(map[SearchAgentType]float64)
	
	// Count results by type
	typeCounts := make(map[string]int)
	totalConfidence := 0.0
	
	for _, result := range results {
		for _, r := range result.Results {
			typeCounts[r.Category]++
			totalConfidence += result.Confidence
		}
	}
	
	avgConfidence := 0.0
	if len(results) > 0 {
		avgConfidence = totalConfidence / float64(len(results))
	}
	
	// Determine needs based on gaps
	if typeCounts["code"] < 5 {
		needs[CodeSearchAgent] = 0.4
	}
	if typeCounts["guideline"] < 3 {
		needs[GuidelineSearchAgent] = 0.3
	}
	if avgConfidence < 0.7 {
		needs[SemanticSearchAgent] = 0.2
	}
	if typeCounts["context"] < 2 {
		needs[ContextSearchAgent] = 0.1
	}
	
	// Normalize
	total := 0.0
	for _, v := range needs {
		total += v
	}
	if total > 0 {
		for k, v := range needs {
			needs[k] = v / total
		}
	} else {
		// Default if no specific needs
		needs[CodeSearchAgent] = 0.5
		needs[GuidelineSearchAgent] = 0.5
	}
	
	return needs
}

// Lifecycle management methods

func (alm *AgentLifecycleManager) OnSpawn(agentID string, agentType SearchAgentType) {
	alm.mu.Lock()
	defer alm.mu.Unlock()
	
	now := time.Now()
	alm.spawned[agentID] = now
	alm.metrics[agentID] = &AgentMetrics{
		AgentID:   agentID,
		Type:      agentType,
		SpawnTime: now,
	}
	
	alm.logger.Debug(context.Background(), "Lifecycle: Agent %s spawned at %v", agentID, now)
}

func (alm *AgentLifecycleManager) OnComplete(agentID string, tokensUsed int, resultsFound int) {
	alm.mu.Lock()
	defer alm.mu.Unlock()
	
	now := time.Now()
	alm.completed[agentID] = now
	
	if metrics, exists := alm.metrics[agentID]; exists {
		metrics.CompleteTime = now
		metrics.TokensUsed = tokensUsed
		metrics.ResultsFound = resultsFound
		metrics.SearchDuration = now.Sub(metrics.SpawnTime)
		
		alm.logger.Debug(context.Background(), 
			"Lifecycle: Agent %s completed - Duration: %v, Tokens: %d, Results: %d",
			agentID, metrics.SearchDuration, tokensUsed, resultsFound)
	}
}

func (alm *AgentLifecycleManager) GetMetrics(agentID string) *AgentMetrics {
	alm.mu.RLock()
	defer alm.mu.RUnlock()
	return alm.metrics[agentID]
}

func (alm *AgentLifecycleManager) GetAllMetrics() []*AgentMetrics {
	alm.mu.RLock()
	defer alm.mu.RUnlock()
	
	metrics := make([]*AgentMetrics, 0, len(alm.metrics))
	for _, m := range alm.metrics {
		metrics = append(metrics, m)
	}
	return metrics
}

// GetPoolStats returns current pool statistics
func (ap *AgentPool) GetPoolStats() map[string]interface{} {
	ap.mu.RLock()
	defer ap.mu.RUnlock()
	
	agentTypes := make(map[SearchAgentType]int)
	for _, agent := range ap.agents {
		// For unified agents, we track them as "unified" type
		agentTypes[SearchAgentType("unified")]++
		_ = agent // Suppress unused variable warning
	}
	
	return map[string]interface{}{
		"active_agents":    ap.activeCount,
		"max_agents":       ap.maxAgents,
		"tokens_used":      ap.totalTokensUsed,
		"max_tokens":       ap.maxTotalTokens,
		"agent_types":      agentTypes,
		"capacity_percent": float64(ap.activeCount) / float64(ap.maxAgents) * 100,
	}
}

// Shutdown gracefully shuts down the agent pool
func (ap *AgentPool) Shutdown(ctx context.Context) error {
	ap.logger.Info(ctx, "Shutting down agent pool...")
	
	// Release all agents
	ap.mu.Lock()
	agentIDs := make([]string, 0, len(ap.agents))
	for id := range ap.agents {
		agentIDs = append(agentIDs, id)
	}
	ap.mu.Unlock()
	
	for _, id := range agentIDs {
		if err := ap.ReleaseAgent(ctx, id); err != nil {
			ap.logger.Warn(ctx, "Failed to release agent %s: %v", id, err)
		}
	}
	
	ap.logger.Info(ctx, "Agent pool shutdown complete")
	return nil
}