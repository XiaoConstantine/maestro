//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLive_SimpleContextPass tests basic context sharing between Claude and Gemini.
// Claude stores a unique code, then Gemini retrieves it from the shared context.
func TestLive_SimpleContextPass(t *testing.T) {
	skipIfNoAPIKeys(t)

	env := SetupTestEnv(t, "test-simple-context")
	taskContext := DefaultTaskContext()

	// Step 1: Claude stores information
	t.Log("Step 1: Sending prompt to Claude to store code...")
	claudeTask := agents.Task{
		ID:   "claude-store-1",
		Type: "claude",
		Metadata: map[string]interface{}{
			"prompt": "Remember this code: PHOENIX-2024. Just acknowledge that you've noted it.",
		},
	}

	claudeResult, err := env.Claude.Process(env.Ctx, claudeTask, taskContext)
	require.NoError(t, err, "Claude processing should succeed")

	claudeResponse := claudeResult.(map[string]interface{})["response"].(string)
	t.Logf("Claude response: %s", claudeResponse)

	// Step 2: Gemini retrieves the information
	t.Log("Step 2: Asking Gemini to retrieve the code from context...")
	geminiTask := agents.Task{
		ID:   "gemini-retrieve-1",
		Type: "gemini",
		Metadata: map[string]interface{}{
			"prompt": "What code did the previous assistant mention? Reply with just the code, nothing else.",
		},
	}

	geminiResult, err := env.Gemini.Process(env.Ctx, geminiTask, taskContext)
	require.NoError(t, err, "Gemini processing should succeed")

	geminiResponse := geminiResult.(map[string]interface{})["response"].(string)
	t.Logf("Gemini response: %s", geminiResponse)

	// Assert: Gemini should have retrieved the code
	assert.Contains(t, geminiResponse, "PHOENIX-2024",
		"Gemini should retrieve the code from shared context")

	// Verify context.md contains both interactions
	contextContent, err := env.SessionMgr.ExportContext(env.Session)
	require.NoError(t, err, "Should be able to export context")

	assert.Contains(t, contextContent, "Claude Interaction",
		"Context should contain Claude interaction header")
	assert.Contains(t, contextContent, "Gemini Interaction",
		"Context should contain Gemini interaction header")
	assert.Contains(t, contextContent, "PHOENIX-2024",
		"Context should contain the stored code")

	t.Log("Simple context pass test completed successfully")
}

// TestLive_MultiTurnConversation tests accumulating context across multiple exchanges.
// Alternates between Claude and Gemini, building up context over 4 turns.
func TestLive_MultiTurnConversation(t *testing.T) {
	skipIfNoAPIKeys(t)

	env := SetupTestEnv(t, "test-multi-turn")
	taskContext := DefaultTaskContext()

	type exchange struct {
		provider string
		prompt   string
	}

	exchanges := []exchange{
		{"claude", "We're analyzing a Go web server. Note: it uses the chi router. Acknowledge this."},
		{"gemini", "What router framework is being used? Reply briefly."},
		{"claude", "The server also uses sqlx for database access. Confirm you can see the router info from earlier."},
		{"gemini", "List all technical details mentioned in our conversation so far."},
	}

	var lastResponse string

	for i, ex := range exchanges {
		t.Logf("Turn %d: %s", i+1, ex.provider)

		task := agents.Task{
			ID:   ex.provider + "-turn",
			Type: ex.provider,
			Metadata: map[string]interface{}{
				"prompt": ex.prompt,
			},
		}

		var result interface{}
		var err error

		if ex.provider == "claude" {
			result, err = env.Claude.Process(env.Ctx, task, taskContext)
		} else {
			result, err = env.Gemini.Process(env.Ctx, task, taskContext)
		}

		require.NoError(t, err, "Turn %d (%s) should succeed", i+1, ex.provider)

		response := result.(map[string]interface{})["response"].(string)
		t.Logf("  Response: %.100s...", response)
		lastResponse = response
	}

	// Assert: Final Gemini response should contain accumulated context
	lastResponseLower := strings.ToLower(lastResponse)
	assert.True(t,
		strings.Contains(lastResponseLower, "chi") || strings.Contains(lastResponseLower, "router"),
		"Final response should mention chi/router")
	assert.True(t,
		strings.Contains(lastResponseLower, "sqlx") || strings.Contains(lastResponseLower, "database"),
		"Final response should mention sqlx/database")

	// Verify context.md has all interactions
	contextContent, err := env.SessionMgr.ExportContext(env.Session)
	require.NoError(t, err)

	// Count interactions
	claudeCount := strings.Count(contextContent, "Claude Interaction")
	geminiCount := strings.Count(contextContent, "Gemini Interaction")

	assert.Equal(t, 2, claudeCount, "Should have 2 Claude interactions")
	assert.Equal(t, 2, geminiCount, "Should have 2 Gemini interactions")

	t.Log("Multi-turn conversation test completed successfully")
}

// TestLive_ContextPersistenceAcrossRestart tests that context survives service re-initialization.
// Phase A: Create session, Claude stores info, processors go out of scope.
// Phase B: New processors, same session, Gemini retrieves the info.
func TestLive_ContextPersistenceAcrossRestart(t *testing.T) {
	skipIfNoAPIKeys(t)

	// Use a fixed temp directory that persists across phases
	sessionDir := t.TempDir()
	sessionID := "persistence-test"
	taskContext := DefaultTaskContext()
	ctx := context.Background()

	logger := logging.GetLogger()

	// Phase A: First "service instance"
	t.Log("Phase A: Creating first service instance and storing info...")
	{
		sessionMgr, session := SetupSessionOnly(t, sessionDir, sessionID)

		claude, err := subagent.NewClaudeProcessor(logger, session.Dir, "")
		require.NoError(t, err, "Should create Claude processor")

		task := agents.Task{
			ID:   "claude-persist",
			Type: "claude",
			Metadata: map[string]interface{}{
				"prompt": "Critical info: API version is v2.5.0. Acknowledge.",
			},
		}

		result, err := claude.Process(ctx, task, taskContext)
		require.NoError(t, err, "Claude should process successfully")

		response := result.(map[string]interface{})["response"].(string)
		t.Logf("Phase A Claude response: %s", response)

		// Verify context was written
		content, err := sessionMgr.ExportContext(session)
		require.NoError(t, err)
		assert.Contains(t, content, "v2.5.0", "Context should contain version info")

		t.Log("Phase A complete - processors going out of scope")
		// Claude processor goes out of scope here, simulating shutdown
	}

	// Phase B: New "service instance", same session
	t.Log("Phase B: Creating new service instance with same session...")
	{
		sessionMgr, session := GetExistingSession(t, sessionDir, sessionID)

		gemini, err := subagent.NewGeminiProcessor(logger, session.Dir, "")
		require.NoError(t, err, "Should create Gemini processor")

		task := agents.Task{
			ID:   "gemini-retrieve",
			Type: "gemini",
			Metadata: map[string]interface{}{
				"prompt": "What API version was mentioned earlier? Reply with just the version number.",
			},
		}

		result, err := gemini.Process(ctx, task, taskContext)
		require.NoError(t, err, "Gemini should process successfully")

		response := result.(map[string]interface{})["response"].(string)
		t.Logf("Phase B Gemini response: %s", response)

		// Assert: Gemini should retrieve the persisted info
		assert.Contains(t, response, "2.5.0",
			"Gemini should retrieve API version from persisted context")

		// Verify both interactions are in context
		content, err := sessionMgr.ExportContext(session)
		require.NoError(t, err)
		assert.Contains(t, content, "Claude Interaction")
		assert.Contains(t, content, "Gemini Interaction")
	}

	t.Log("Context persistence test completed successfully")
}
