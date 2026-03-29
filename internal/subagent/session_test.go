package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

func TestSessionManagerDualWriteCreateSession(t *testing.T) {
	t.Helper()

	baseDir := t.TempDir()
	store := newTestSessionEventStore(t, baseDir)
	manager, err := NewSessionManager(baseDir, logging.GetLogger(), WithSessionEventStore(store))
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}

	session, err := manager.CreateSession(context.Background(), "alpha", map[string]interface{}{
		"owner":   "acme",
		"repo":    "rocket",
		"purpose": "Alpha session",
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if session.ID != "alpha" {
		t.Fatalf("session.ID = %q, want alpha", session.ID)
	}

	eventSession, err := store.GetSession(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("event store GetSession() error = %v", err)
	}
	if eventSession.Title != "Alpha session" {
		t.Fatalf("event session title = %q, want Alpha session", eventSession.Title)
	}
	if got := eventSession.Metadata["repo"]; got != "rocket" {
		t.Fatalf("event session metadata repo = %v, want rocket", got)
	}

	contextData, err := manager.ExportContext(session)
	if err != nil {
		t.Fatalf("ExportContext() error = %v", err)
	}
	if want := "acme/rocket"; !strings.Contains(contextData, want) {
		t.Fatalf("context.md missing %q: %s", want, contextData)
	}
}

func TestSessionManagerGetSessionMaterializesFromEventStore(t *testing.T) {
	t.Helper()

	baseDir := t.TempDir()
	store := newTestSessionEventStore(t, baseDir)
	_, _, err := store.CreateSession(context.Background(), sessionevent.CreateSessionParams{
		ID:    "beta",
		Title: "Beta session",
		Metadata: map[string]any{
			"owner":   "acme",
			"repo":    "rocket",
			"purpose": "Materialized from event store",
		},
	})
	if err != nil {
		t.Fatalf("CreateSession(event store) error = %v", err)
	}

	manager, err := NewSessionManager(baseDir, logging.GetLogger(), WithSessionEventStore(store))
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}

	session, err := manager.GetSession("beta")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}

	contextPath := filepath.Join(session.Dir, "context.md")
	if _, err := os.Stat(contextPath); err != nil {
		t.Fatalf("context.md was not materialized: %v", err)
	}

	contextData, err := manager.ExportContext(session)
	if err != nil {
		t.Fatalf("ExportContext() error = %v", err)
	}
	if want := "Materialized from event store"; !strings.Contains(contextData, want) {
		t.Fatalf("context.md missing %q: %s", want, contextData)
	}
}

func TestSessionManagerGetSessionUsesTitleWhenPurposeMetadataMissing(t *testing.T) {
	t.Helper()

	baseDir := t.TempDir()
	store := newTestSessionEventStore(t, baseDir)
	_, _, err := store.CreateSession(context.Background(), sessionevent.CreateSessionParams{
		ID:    "gamma",
		Title: "Gamma session title",
		Metadata: map[string]any{
			"owner": "acme",
			"repo":  "rocket",
		},
	})
	if err != nil {
		t.Fatalf("CreateSession(event store) error = %v", err)
	}

	manager, err := NewSessionManager(baseDir, logging.GetLogger(), WithSessionEventStore(store))
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}

	session, err := manager.GetSession("gamma")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}

	contextData, err := manager.ExportContext(session)
	if err != nil {
		t.Fatalf("ExportContext() error = %v", err)
	}
	if want := "Gamma session title"; !strings.Contains(contextData, want) {
		t.Fatalf("context.md missing %q: %s", want, contextData)
	}
}

func TestSessionManagerListSessionsMergesEventStoreAndLegacyDirs(t *testing.T) {
	t.Helper()

	baseDir := t.TempDir()
	store := newTestSessionEventStore(t, baseDir)
	_, _, err := store.CreateSession(context.Background(), sessionevent.CreateSessionParams{
		ID:    "event-only",
		Title: "Event Session",
	})
	if err != nil {
		t.Fatalf("CreateSession(event store) error = %v", err)
	}

	legacyDir := filepath.Join(baseDir, "legacy-only")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}

	manager, err := NewSessionManager(baseDir, logging.GetLogger(), WithSessionEventStore(store))
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}

	sessions, err := manager.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}

	if !hasSessionID(sessions, "event-only") {
		t.Fatalf("ListSessions() missing event-only session: %#v", sessions)
	}
	if !hasSessionID(sessions, "legacy-only") {
		t.Fatalf("ListSessions() missing legacy-only session: %#v", sessions)
	}
}

func TestSessionManagerCleanupSessionRemovesEventState(t *testing.T) {
	t.Helper()

	baseDir := t.TempDir()
	store := newTestSessionEventStore(t, baseDir)
	manager, err := NewSessionManager(baseDir, logging.GetLogger(), WithSessionEventStore(store))
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}

	session, err := manager.CreateSession(context.Background(), "cleanup-me", map[string]interface{}{
		"purpose": "cleanup test",
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if err := manager.CleanupSession(context.Background(), session.ID); err != nil {
		t.Fatalf("CleanupSession() error = %v", err)
	}

	if _, err := store.GetSession(context.Background(), session.ID); err == nil {
		t.Fatalf("expected session to be removed from event store")
	}
	if _, err := os.Stat(session.Dir); !os.IsNotExist(err) {
		t.Fatalf("expected session dir to be removed, got err=%v", err)
	}

	sessions, err := manager.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if hasSessionID(sessions, session.ID) {
		t.Fatalf("session %q still present after cleanup", session.ID)
	}
}

func newTestSessionEventStore(t *testing.T, baseDir string) *SQLiteSessionStore {
	t.Helper()

	storePath := filepath.Join(baseDir, "sessionevent.db")
	store, err := NewSQLiteSessionStore(storePath)
	if err != nil {
		t.Fatalf("NewSQLiteSessionStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func hasSessionID(sessions []Session, id string) bool {
	for _, session := range sessions {
		if session.ID == id {
			return true
		}
	}
	return false
}
