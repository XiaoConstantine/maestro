//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/subagent"
)

// skipIfNoAPIKeys skips the test if required API keys are not set.
func skipIfNoAPIKeys(t *testing.T) {
	t.Helper()
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	if os.Getenv("GOOGLE_API_KEY") == "" && os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("GOOGLE_API_KEY or GEMINI_API_KEY not set")
	}
}

// TestEnv holds all the components needed for integration testing.
type TestEnv struct {
	SessionDir string
	SessionMgr *subagent.SessionManager
	Session    *subagent.Session
	Claude     *subagent.ClaudeProcessor
	Gemini     *subagent.GeminiProcessor
	Logger     *logging.Logger
	Ctx        context.Context
}

// SetupTestEnv creates a complete test environment with session manager and both processors.
func SetupTestEnv(t *testing.T, sessionID string) *TestEnv {
	t.Helper()

	ctx := context.Background()
	logger := logging.GetLogger()

	// Use t.TempDir() for automatic cleanup
	sessionDir := t.TempDir()

	sessionMgr, err := subagent.NewSessionManager(sessionDir, logger)
	if err != nil {
		t.Fatalf("Failed to create session manager: %v", err)
	}

	initialContext := map[string]interface{}{
		"owner":   "test-owner",
		"repo":    "test-repo",
		"purpose": "Integration testing Claude-Gemini context sharing",
	}

	session, err := sessionMgr.CreateSession(ctx, sessionID, initialContext)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	claude, err := subagent.NewClaudeProcessor(logger, session.Dir, "")
	if err != nil {
		t.Fatalf("Failed to create Claude processor: %v", err)
	}

	gemini, err := subagent.NewGeminiProcessor(logger, session.Dir, "")
	if err != nil {
		t.Fatalf("Failed to create Gemini processor: %v", err)
	}

	return &TestEnv{
		SessionDir: sessionDir,
		SessionMgr: sessionMgr,
		Session:    session,
		Claude:     claude,
		Gemini:     gemini,
		Logger:     logger,
		Ctx:        ctx,
	}
}

// SetupSessionOnly creates a test environment with just the session manager (no processors).
// Useful for testing persistence across processor lifecycle.
func SetupSessionOnly(t *testing.T, sessionDir, sessionID string) (*subagent.SessionManager, *subagent.Session) {
	t.Helper()

	ctx := context.Background()
	logger := logging.GetLogger()

	sessionMgr, err := subagent.NewSessionManager(sessionDir, logger)
	if err != nil {
		t.Fatalf("Failed to create session manager: %v", err)
	}

	initialContext := map[string]interface{}{
		"owner":   "test-owner",
		"repo":    "test-repo",
		"purpose": "Integration testing context persistence",
	}

	session, err := sessionMgr.GetOrCreateSession(ctx, sessionID, initialContext)
	if err != nil {
		t.Fatalf("Failed to get or create session: %v", err)
	}

	return sessionMgr, session
}

// GetExistingSession retrieves an existing session without creating a new one.
func GetExistingSession(t *testing.T, sessionDir, sessionID string) (*subagent.SessionManager, *subagent.Session) {
	t.Helper()

	logger := logging.GetLogger()

	sessionMgr, err := subagent.NewSessionManager(sessionDir, logger)
	if err != nil {
		t.Fatalf("Failed to create session manager: %v", err)
	}

	session, err := sessionMgr.GetSession(sessionID)
	if err != nil {
		t.Fatalf("Failed to get existing session: %v", err)
	}

	return sessionMgr, session
}

// DefaultTaskContext returns a basic task context for testing.
func DefaultTaskContext() map[string]interface{} {
	return map[string]interface{}{
		"owner":     "test-owner",
		"repo":      "test-repo",
		"repo_path": "/tmp/test-repo",
	}
}
