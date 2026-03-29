package subagent

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	dspyerrors "github.com/XiaoConstantine/dspy-go/pkg/errors"
	_ "github.com/mattn/go-sqlite3"
)

// SQLiteSessionStore wraps dspy-go's SQLite sessionevent store with cleanup helpers
// Maestro needs for dual-write session lifecycle management.
type SQLiteSessionStore struct {
	*sessionevent.SQLiteStore
	path string
}

// NewSQLiteSessionStore creates a file-backed sessionevent store with cleanup support.
func NewSQLiteSessionStore(path string) (*SQLiteSessionStore, error) {
	store, err := sessionevent.NewSQLiteStore(path)
	if err != nil {
		return nil, err
	}
	return &SQLiteSessionStore{
		SQLiteStore: store,
		path:        path,
	}, nil
}

// DeleteSession removes a session and its dependent branches, entries, and summaries.
func (s *SQLiteSessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	if s == nil || s.SQLiteStore == nil {
		return fmt.Errorf("session event store is nil")
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return dspyerrors.New(dspyerrors.InvalidInput, "session id is required")
	}
	if strings.TrimSpace(s.path) == "" || s.path == ":memory:" {
		return fmt.Errorf("session deletion requires a file-backed session store")
	}

	db, err := sql.Open("sqlite3", s.path)
	if err != nil {
		return fmt.Errorf("failed to open cleanup database: %w", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("failed to enable foreign keys for cleanup: %w", err)
	}

	result, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("failed to delete session %s from event store: %w", sessionID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err == nil && rowsAffected == 0 {
		return dspyerrors.New(dspyerrors.ResourceNotFound, "session not found")
	}
	return nil
}
