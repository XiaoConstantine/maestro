package rlm

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewClaudeCodeAdapter(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		adapter := NewClaudeCodeAdapter(ClaudeCodeConfig{})

		if adapter.cliPath != "claude" {
			t.Errorf("cliPath = %q, want %q", adapter.cliPath, "claude")
		}
		if adapter.timeout != 5*time.Minute {
			t.Errorf("timeout = %v, want %v", adapter.timeout, 5*time.Minute)
		}
	})

	t.Run("custom config", func(t *testing.T) {
		adapter := NewClaudeCodeAdapter(ClaudeCodeConfig{
			CLIPath:      "/usr/local/bin/claude",
			WorkDir:      "/tmp/work",
			AllowedTools: []string{"Read", "Grep"},
			Timeout:      10 * time.Minute,
			SessionID:    "test-session-123",
		})

		if adapter.cliPath != "/usr/local/bin/claude" {
			t.Errorf("cliPath = %q, want %q", adapter.cliPath, "/usr/local/bin/claude")
		}
		if adapter.workDir != "/tmp/work" {
			t.Errorf("workDir = %q, want %q", adapter.workDir, "/tmp/work")
		}
		if len(adapter.allowedTools) != 2 {
			t.Errorf("allowedTools length = %d, want 2", len(adapter.allowedTools))
		}
		if adapter.timeout != 10*time.Minute {
			t.Errorf("timeout = %v, want %v", adapter.timeout, 10*time.Minute)
		}
		if adapter.sessionID != "test-session-123" {
			t.Errorf("sessionID = %q, want %q", adapter.sessionID, "test-session-123")
		}
	})
}

func TestClaudeCodeAdapter_Name(t *testing.T) {
	adapter := NewClaudeCodeAdapter(ClaudeCodeConfig{})
	if name := adapter.Name(); name != "claude-code" {
		t.Errorf("Name() = %q, want %q", name, "claude-code")
	}
}

func TestClaudeCodeAdapter_Capabilities(t *testing.T) {
	adapter := NewClaudeCodeAdapter(ClaudeCodeConfig{})
	caps := adapter.Capabilities()

	expected := []Capability{
		CapabilityCodeAnalysis,
		CapabilityCodeGeneration,
		CapabilityFileRead,
		CapabilityFileWrite,
		CapabilityWebSearch,
		CapabilityShellExecution,
	}

	if len(caps) != len(expected) {
		t.Errorf("Capabilities() length = %d, want %d", len(caps), len(expected))
	}

	for i, cap := range expected {
		if caps[i] != cap {
			t.Errorf("Capabilities()[%d] = %v, want %v", i, caps[i], cap)
		}
	}
}

func TestClaudeCodeAdapter_TokenPricing(t *testing.T) {
	adapter := NewClaudeCodeAdapter(ClaudeCodeConfig{})
	input, output := adapter.TokenPricing()

	if input != 0.003 {
		t.Errorf("input pricing = %f, want %f", input, 0.003)
	}
	if output != 0.015 {
		t.Errorf("output pricing = %f, want %f", output, 0.015)
	}
}

func TestClaudeCodeAdapter_SessionManagement(t *testing.T) {
	adapter := NewClaudeCodeAdapter(ClaudeCodeConfig{})

	// Initially empty
	if id := adapter.GetSessionID(); id != "" {
		t.Errorf("initial session ID = %q, want empty", id)
	}

	// Set session
	adapter.SetSessionID("session-abc-123")
	if id := adapter.GetSessionID(); id != "session-abc-123" {
		t.Errorf("session ID after set = %q, want %q", id, "session-abc-123")
	}

	// Reset session
	adapter.ResetSession()
	if id := adapter.GetSessionID(); id != "" {
		t.Errorf("session ID after reset = %q, want empty", id)
	}
}

func TestClaudeCodeAdapter_Stats(t *testing.T) {
	adapter := NewClaudeCodeAdapter(ClaudeCodeConfig{})

	// Simulate usage recording
	adapter.recordUsage(ClaudeCodeResponse{
		Usage: struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		}{
			InputTokens:  100,
			OutputTokens: 50,
		},
		TotalCostUSD: 0.001,
	})

	adapter.recordUsage(ClaudeCodeResponse{
		Usage: struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		}{
			InputTokens:  200,
			OutputTokens: 100,
		},
		TotalCostUSD: 0.002,
	})

	stats := adapter.Stats()

	if stats.TotalPromptTokens != 300 {
		t.Errorf("TotalPromptTokens = %d, want 300", stats.TotalPromptTokens)
	}
	if stats.TotalCompletionTokens != 150 {
		t.Errorf("TotalCompletionTokens = %d, want 150", stats.TotalCompletionTokens)
	}
	if stats.TotalQueries != 2 {
		t.Errorf("TotalQueries = %d, want 2", stats.TotalQueries)
	}
	if stats.TotalCostUSD != 0.003 {
		t.Errorf("TotalCostUSD = %f, want 0.003", stats.TotalCostUSD)
	}
	if stats.CallsByTier[TierBest] != 2 {
		t.Errorf("CallsByTier[TierBest] = %d, want 2", stats.CallsByTier[TierBest])
	}
}

func TestClaudeCodeAdapter_Reset(t *testing.T) {
	adapter := NewClaudeCodeAdapter(ClaudeCodeConfig{
		SessionID: "existing-session",
	})

	// Record some usage
	adapter.recordUsage(ClaudeCodeResponse{
		Usage: struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		}{
			InputTokens:  100,
			OutputTokens: 50,
		},
		TotalCostUSD: 0.001,
	})

	adapter.Reset()

	if id := adapter.GetSessionID(); id != "" {
		t.Errorf("session ID after reset = %q, want empty", id)
	}

	stats := adapter.Stats()
	if stats.TotalPromptTokens != 0 {
		t.Errorf("TotalPromptTokens after reset = %d, want 0", stats.TotalPromptTokens)
	}
	if stats.TotalQueries != 0 {
		t.Errorf("TotalQueries after reset = %d, want 0", stats.TotalQueries)
	}
}

func TestClaudeCodeResponse_JSONParsing(t *testing.T) {
	jsonData := `{
		"type": "result",
		"subtype": "success",
		"session_id": "550e8400-e29b-41d4-a716-446655440000",
		"result": "Analysis complete. Found 3 issues.",
		"is_error": false,
		"num_turns": 5,
		"duration_ms": 12345,
		"duration_api_ms": 10000,
		"total_cost_usd": 0.0045,
		"usage": {
			"input_tokens": 1500,
			"output_tokens": 300,
			"cache_read_input_tokens": 1200,
			"cache_creation_input_tokens": 0
		},
		"modelUsage": {
			"claude-sonnet-4-5": {
				"inputTokens": 1500,
				"outputTokens": 300,
				"costUSD": 0.0045,
				"contextWindow": 200000
			}
		}
	}`

	var resp ClaudeCodeResponse
	if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if resp.Type != "result" {
		t.Errorf("Type = %q, want %q", resp.Type, "result")
	}
	if resp.Subtype != "success" {
		t.Errorf("Subtype = %q, want %q", resp.Subtype, "success")
	}
	if resp.SessionID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("SessionID = %q, want expected UUID", resp.SessionID)
	}
	if resp.Result != "Analysis complete. Found 3 issues." {
		t.Errorf("Result = %q, want expected text", resp.Result)
	}
	if resp.IsError {
		t.Error("IsError = true, want false")
	}
	if resp.NumTurns != 5 {
		t.Errorf("NumTurns = %d, want 5", resp.NumTurns)
	}
	if resp.TotalCostUSD != 0.0045 {
		t.Errorf("TotalCostUSD = %f, want 0.0045", resp.TotalCostUSD)
	}
	if resp.Usage.InputTokens != 1500 {
		t.Errorf("Usage.InputTokens = %d, want 1500", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 300 {
		t.Errorf("Usage.OutputTokens = %d, want 300", resp.Usage.OutputTokens)
	}
}

func TestClaudeCodeResponse_ErrorParsing(t *testing.T) {
	jsonData := `{
		"type": "result",
		"subtype": "error_during_execution",
		"session_id": "test-session",
		"result": "Failed to read file",
		"is_error": true,
		"errors": ["permission denied", "file not found"]
	}`

	var resp ClaudeCodeResponse
	if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if !resp.IsError {
		t.Error("IsError = false, want true")
	}
	if resp.Subtype != "error_during_execution" {
		t.Errorf("Subtype = %q, want %q", resp.Subtype, "error_during_execution")
	}
	if len(resp.Errors) != 2 {
		t.Errorf("Errors length = %d, want 2", len(resp.Errors))
	}
}

func TestClaudeCodeAdapter_ImplementsSubAgent(t *testing.T) {
	// Verify ClaudeCodeAdapter implements SubAgent interface
	var _ SubAgent = (*ClaudeCodeAdapter)(nil)
}
