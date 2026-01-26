package rlm

import (
	"context"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

// mockSubAgent implements SubAgent for testing.
type mockSubAgent struct {
	name         string
	capabilities []Capability
	inputPrice   float64
	outputPrice  float64
	stats        AgentStats
	queryFunc    func(ctx context.Context, prompt string) (rlm.QueryResponse, error)
}

func (m *mockSubAgent) Query(ctx context.Context, prompt string) (rlm.QueryResponse, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, prompt)
	}
	return rlm.QueryResponse{
		Response:         "mock response",
		PromptTokens:     10,
		CompletionTokens: 20,
	}, nil
}

func (m *mockSubAgent) QueryBatched(ctx context.Context, prompts []string) ([]rlm.QueryResponse, error) {
	results := make([]rlm.QueryResponse, len(prompts))
	for i, prompt := range prompts {
		resp, err := m.Query(ctx, prompt)
		if err != nil {
			return nil, err
		}
		results[i] = resp
	}
	return results, nil
}

func (m *mockSubAgent) Name() string {
	return m.name
}

func (m *mockSubAgent) Capabilities() []Capability {
	return m.capabilities
}

func (m *mockSubAgent) TokenPricing() (input float64, output float64) {
	return m.inputPrice, m.outputPrice
}

func (m *mockSubAgent) Stats() AgentStats {
	return m.stats
}

func TestAgentStats_TotalTokens(t *testing.T) {
	stats := AgentStats{
		TotalPromptTokens:     200,
		TotalCompletionTokens: 100,
	}

	if got := stats.TotalTokens(); got != 300 {
		t.Errorf("TotalTokens() = %d, want 300", got)
	}
}

func TestCapability_String(t *testing.T) {
	tests := []struct {
		cap  Capability
		want string
	}{
		{CapabilityCodeAnalysis, "code_analysis"},
		{CapabilityCodeGeneration, "code_generation"},
		{CapabilityFileRead, "file_read"},
		{CapabilityFileWrite, "file_write"},
		{CapabilityWebSearch, "web_search"},
		{CapabilityShellExecution, "shell_execution"},
		{Capability(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.cap.String(); got != tt.want {
			t.Errorf("Capability(%d).String() = %q, want %q", tt.cap, got, tt.want)
		}
	}
}

func TestSubAgentRegistry(t *testing.T) {
	t.Run("Register and Get", func(t *testing.T) {
		registry := NewSubAgentRegistry()

		agent1 := &mockSubAgent{name: "agent1"}
		agent2 := &mockSubAgent{name: "agent2"}

		if err := registry.Register(agent1); err != nil {
			t.Fatalf("Register agent1: %v", err)
		}
		if err := registry.Register(agent2); err != nil {
			t.Fatalf("Register agent2: %v", err)
		}

		got, err := registry.Get("agent1")
		if err != nil {
			t.Fatalf("Get agent1: %v", err)
		}
		if got.Name() != "agent1" {
			t.Errorf("Get agent1 name = %q, want %q", got.Name(), "agent1")
		}
	})

	t.Run("First registered becomes default", func(t *testing.T) {
		registry := NewSubAgentRegistry()

		agent1 := &mockSubAgent{name: "first"}
		agent2 := &mockSubAgent{name: "second"}

		registry.Register(agent1)
		registry.Register(agent2)

		got, err := registry.GetDefault()
		if err != nil {
			t.Fatalf("GetDefault: %v", err)
		}
		if got.Name() != "first" {
			t.Errorf("GetDefault name = %q, want %q", got.Name(), "first")
		}
	})

	t.Run("SetDefault", func(t *testing.T) {
		registry := NewSubAgentRegistry()

		agent1 := &mockSubAgent{name: "first"}
		agent2 := &mockSubAgent{name: "second"}

		registry.Register(agent1)
		registry.Register(agent2)

		if err := registry.SetDefault("second"); err != nil {
			t.Fatalf("SetDefault: %v", err)
		}

		got, _ := registry.GetDefault()
		if got.Name() != "second" {
			t.Errorf("GetDefault after SetDefault = %q, want %q", got.Name(), "second")
		}
	})

	t.Run("Duplicate registration fails", func(t *testing.T) {
		registry := NewSubAgentRegistry()

		agent := &mockSubAgent{name: "agent"}
		registry.Register(agent)

		if err := registry.Register(agent); err == nil {
			t.Error("Expected error for duplicate registration")
		}
	})

	t.Run("Empty name fails", func(t *testing.T) {
		registry := NewSubAgentRegistry()
		agent := &mockSubAgent{name: ""}

		if err := registry.Register(agent); err == nil {
			t.Error("Expected error for empty agent name")
		}
	})

	t.Run("Get nonexistent fails", func(t *testing.T) {
		registry := NewSubAgentRegistry()

		_, err := registry.Get("nonexistent")
		if err == nil {
			t.Error("Expected error for nonexistent agent")
		}
	})

	t.Run("List returns all names", func(t *testing.T) {
		registry := NewSubAgentRegistry()

		registry.Register(&mockSubAgent{name: "a"})
		registry.Register(&mockSubAgent{name: "b"})
		registry.Register(&mockSubAgent{name: "c"})

		names := registry.List()
		if len(names) != 3 {
			t.Errorf("List() returned %d names, want 3", len(names))
		}
	})

	t.Run("HasAgent", func(t *testing.T) {
		registry := NewSubAgentRegistry()
		registry.Register(&mockSubAgent{name: "exists"})

		if !registry.HasAgent("exists") {
			t.Error("HasAgent returned false for registered agent")
		}
		if registry.HasAgent("missing") {
			t.Error("HasAgent returned true for unregistered agent")
		}
	})

	t.Run("AggregateStats", func(t *testing.T) {
		registry := NewSubAgentRegistry()

		registry.Register(&mockSubAgent{
			name: "agent1",
			stats: AgentStats{
				TotalPromptTokens:     100,
				TotalCompletionTokens: 50,
				TotalQueries:          5,
				TotalCostUSD:          0.10,
				CallsByTier:           map[ModelTier]int{TierSmart: 5},
			},
		})
		registry.Register(&mockSubAgent{
			name: "agent2",
			stats: AgentStats{
				TotalPromptTokens:     200,
				TotalCompletionTokens: 100,
				TotalQueries:          10,
				TotalCostUSD:          0.20,
				CallsByTier:           map[ModelTier]int{TierFast: 8, TierSmart: 2},
			},
		})

		stats := registry.AggregateStats()

		if stats.TotalPromptTokens != 300 {
			t.Errorf("TotalPromptTokens = %d, want 300", stats.TotalPromptTokens)
		}
		if stats.TotalCompletionTokens != 150 {
			t.Errorf("TotalCompletionTokens = %d, want 150", stats.TotalCompletionTokens)
		}
		if stats.TotalQueries != 15 {
			t.Errorf("TotalQueries = %d, want 15", stats.TotalQueries)
		}
		if stats.CallsByTier[TierSmart] != 7 {
			t.Errorf("CallsByTier[TierSmart] = %d, want 7", stats.CallsByTier[TierSmart])
		}
	})
}

func TestSubAgentInterface(t *testing.T) {
	// Verify mockSubAgent implements SubAgent
	var _ SubAgent = (*mockSubAgent)(nil)

	t.Run("Query returns response", func(t *testing.T) {
		agent := &mockSubAgent{name: "test"}
		resp, err := agent.Query(context.Background(), "test prompt")
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if resp.Response != "mock response" {
			t.Errorf("Response = %q, want %q", resp.Response, "mock response")
		}
	})

	t.Run("QueryBatched returns responses", func(t *testing.T) {
		agent := &mockSubAgent{name: "test"}
		prompts := []string{"prompt1", "prompt2", "prompt3"}
		results, err := agent.QueryBatched(context.Background(), prompts)
		if err != nil {
			t.Fatalf("QueryBatched failed: %v", err)
		}
		if len(results) != 3 {
			t.Errorf("Results length = %d, want 3", len(results))
		}
	})
}
