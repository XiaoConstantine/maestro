package rlm

import (
	"fmt"
	"sync"
)

// SubAgentRegistry manages registration and lookup of SubAgent implementations.
type SubAgentRegistry struct {
	mu       sync.RWMutex
	agents   map[string]SubAgent
	default_ string // default agent name
}

// NewSubAgentRegistry creates a new empty registry.
func NewSubAgentRegistry() *SubAgentRegistry {
	return &SubAgentRegistry{
		agents: make(map[string]SubAgent),
	}
}

// Register adds a SubAgent to the registry.
// If this is the first agent registered, it becomes the default.
func (r *SubAgentRegistry) Register(agent SubAgent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := agent.Name()
	if name == "" {
		return fmt.Errorf("agent name cannot be empty")
	}

	if _, exists := r.agents[name]; exists {
		return fmt.Errorf("agent %q already registered", name)
	}

	r.agents[name] = agent

	// First registered agent becomes default
	if r.default_ == "" {
		r.default_ = name
	}

	return nil
}

// Get retrieves a SubAgent by name.
func (r *SubAgentRegistry) Get(name string) (SubAgent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.agents[name]
	if !ok {
		return nil, fmt.Errorf("agent %q not found", name)
	}
	return agent, nil
}

// GetDefault retrieves the default SubAgent.
func (r *SubAgentRegistry) GetDefault() (SubAgent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.default_ == "" {
		return nil, fmt.Errorf("no default agent set")
	}

	agent, ok := r.agents[r.default_]
	if !ok {
		return nil, fmt.Errorf("default agent %q not found", r.default_)
	}
	return agent, nil
}

// SetDefault sets the default agent by name.
func (r *SubAgentRegistry) SetDefault(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.agents[name]; !ok {
		return fmt.Errorf("agent %q not registered", name)
	}

	r.default_ = name
	return nil
}

// List returns names of all registered agents.
func (r *SubAgentRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	return names
}

// All returns all registered agents.
func (r *SubAgentRegistry) All() []SubAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]SubAgent, 0, len(r.agents))
	for _, agent := range r.agents {
		agents = append(agents, agent)
	}
	return agents
}

// HasAgent checks if an agent is registered.
func (r *SubAgentRegistry) HasAgent(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.agents[name]
	return ok
}

// DefaultName returns the name of the default agent.
func (r *SubAgentRegistry) DefaultName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.default_
}

// AggregateStats returns combined stats from all registered agents.
func (r *SubAgentRegistry) AggregateStats() AgentStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	aggregate := AgentStats{
		CallsByTier: make(map[ModelTier]int),
	}

	for _, agent := range r.agents {
		stats := agent.Stats()
		aggregate.TotalPromptTokens += stats.TotalPromptTokens
		aggregate.TotalCompletionTokens += stats.TotalCompletionTokens
		aggregate.TotalQueries += stats.TotalQueries
		aggregate.TotalCostUSD += stats.TotalCostUSD

		for tier, count := range stats.CallsByTier {
			aggregate.CallsByTier[tier] += count
		}
	}

	return aggregate
}

// Global registry instance
var globalRegistry *SubAgentRegistry
var registryOnce sync.Once

// GlobalRegistry returns the global SubAgent registry.
func GlobalRegistry() *SubAgentRegistry {
	registryOnce.Do(func() {
		globalRegistry = NewSubAgentRegistry()
	})
	return globalRegistry
}

// RegisterGlobal registers an agent with the global registry.
func RegisterGlobal(agent SubAgent) error {
	return GlobalRegistry().Register(agent)
}

// GetGlobal retrieves an agent from the global registry.
func GetGlobal(name string) (SubAgent, error) {
	return GlobalRegistry().Get(name)
}

// GetDefaultGlobal retrieves the default agent from the global registry.
func GetDefaultGlobal() (SubAgent, error) {
	return GlobalRegistry().GetDefault()
}
