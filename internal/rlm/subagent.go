package rlm

import (
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

// SubAgent extends dspy-go's SubLLMClient with orchestration metadata.
// This interface allows Maestro to manage multiple AI backends with
// identity, capabilities, pricing, and usage tracking.
//
// The embedded SubLLMClient provides:
//   - Query(ctx, prompt) (QueryResponse, error)
//   - QueryBatched(ctx, prompts) ([]QueryResponse, error)
//
// SubAgent adds orchestration concerns on top.
type SubAgent interface {
	// Embed dspy-go's SubLLMClient for RLM compatibility
	rlm.SubLLMClient

	// Name returns the agent identifier (e.g., "anthropic-claude-sonnet", "openai-gpt4o").
	Name() string

	// Capabilities returns what this agent can do.
	Capabilities() []Capability

	// TokenPricing returns cost per 1K tokens (input, output).
	TokenPricing() (input float64, output float64)

	// Stats returns usage statistics for this agent.
	Stats() AgentStats
}

// Capability represents what a SubAgent can do.
type Capability int

const (
	CapabilityCodeAnalysis Capability = iota
	CapabilityCodeGeneration
	CapabilityFileRead
	CapabilityFileWrite
	CapabilityWebSearch
	CapabilityShellExecution
)

func (c Capability) String() string {
	switch c {
	case CapabilityCodeAnalysis:
		return "code_analysis"
	case CapabilityCodeGeneration:
		return "code_generation"
	case CapabilityFileRead:
		return "file_read"
	case CapabilityFileWrite:
		return "file_write"
	case CapabilityWebSearch:
		return "web_search"
	case CapabilityShellExecution:
		return "shell_execution"
	default:
		return "unknown"
	}
}

// AgentStats contains usage statistics for a SubAgent.
type AgentStats struct {
	TotalPromptTokens     int
	TotalCompletionTokens int
	TotalQueries          int
	TotalCostUSD          float64
	CallsByTier           map[ModelTier]int // For tiered agents
}

// TotalTokens returns the sum of prompt and completion tokens.
func (s *AgentStats) TotalTokens() int {
	return s.TotalPromptTokens + s.TotalCompletionTokens
}
