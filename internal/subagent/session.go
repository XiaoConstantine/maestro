package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// SessionManager manages file-based context sharing between Maestro and CLI subagents.
type SessionManager struct {
	baseDir string
	logger  *logging.Logger
}

// Session represents an active context-sharing session.
type Session struct {
	ID        string
	Dir       string
	CreatedAt time.Time
}

// NewSessionManager creates a session manager with the given base directory.
func NewSessionManager(baseDir string, logger *logging.Logger) (*SessionManager, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}
	return &SessionManager{
		baseDir: baseDir,
		logger:  logger,
	}, nil
}

// CreateSession creates a new session directory with initial context.
func (m *SessionManager) CreateSession(ctx context.Context, id string, initialContext map[string]interface{}) (*Session, error) {
	sessionDir := filepath.Join(m.baseDir, id)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session dir: %w", err)
	}

	session := &Session{
		ID:        id,
		Dir:       sessionDir,
		CreatedAt: time.Now(),
	}

	// Write initial context file
	if err := m.writeContext(session, initialContext); err != nil {
		return nil, err
	}

	m.logger.Info(ctx, "Created session %s at %s", id, sessionDir)
	return session, nil
}

// GetSession retrieves an existing session.
func (m *SessionManager) GetSession(id string) (*Session, error) {
	sessionDir := filepath.Join(m.baseDir, id)
	info, err := os.Stat(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	return &Session{
		ID:        id,
		Dir:       sessionDir,
		CreatedAt: info.ModTime(),
	}, nil
}

// GetOrCreateSession gets an existing session or creates a new one.
func (m *SessionManager) GetOrCreateSession(ctx context.Context, id string, initialContext map[string]interface{}) (*Session, error) {
	session, err := m.GetSession(id)
	if err == nil {
		return session, nil
	}
	return m.CreateSession(ctx, id, initialContext)
}

// writeContext writes context to the session's context.md file.
func (m *SessionManager) writeContext(session *Session, contextData map[string]interface{}) error {
	contextFile := filepath.Join(session.Dir, "context.md")

	f, err := os.Create(contextFile)
	if err != nil {
		return fmt.Errorf("failed to create context file: %w", err)
	}
	defer f.Close()

	// Write header
	fmt.Fprintf(f, "# Maestro Session: %s\n\n", session.ID)
	fmt.Fprintf(f, "Created: %s\n\n", session.CreatedAt.Format(time.RFC3339))
	fmt.Fprintln(f, "---")
	fmt.Fprintln(f)

	// Write context data
	if owner, ok := contextData["owner"].(string); ok {
		if repo, ok := contextData["repo"].(string); ok {
			fmt.Fprintf(f, "## Repository\n\n%s/%s\n\n", owner, repo)
		}
	}

	if repoPath, ok := contextData["repo_path"].(string); ok {
		fmt.Fprintf(f, "## Local Path\n\n%s\n\n", repoPath)
	}

	if purpose, ok := contextData["purpose"].(string); ok {
		fmt.Fprintf(f, "## Purpose\n\n%s\n\n", purpose)
	}

	fmt.Fprintln(f, "## Interaction History")
	fmt.Fprintln(f)

	return nil
}

// ExportContext exports the current session context to a string.
func (m *SessionManager) ExportContext(session *Session) (string, error) {
	contextFile := filepath.Join(session.Dir, "context.md")
	data, err := os.ReadFile(contextFile)
	if err != nil {
		return "", fmt.Errorf("failed to read context: %w", err)
	}
	return string(data), nil
}

// ImportContext imports context from another source into the session.
func (m *SessionManager) ImportContext(session *Session, content string) error {
	contextFile := filepath.Join(session.Dir, "context.md")

	f, err := os.OpenFile(contextFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open context file: %w", err)
	}
	defer f.Close()

	fmt.Fprintf(f, "\n## Imported Context\n\n%s\n\n---\n", content)
	return nil
}

// AddToContext appends a message to the session context.
func (m *SessionManager) AddToContext(session *Session, source, message string) error {
	contextFile := filepath.Join(session.Dir, "context.md")

	f, err := os.OpenFile(contextFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open context file: %w", err)
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(f, "\n### [%s] %s\n\n%s\n\n", timestamp, source, message)
	return nil
}

// ListSessions returns all available sessions.
func (m *SessionManager) ListSessions() ([]Session, error) {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions dir: %w", err)
	}

	var sessions []Session
	for _, entry := range entries {
		if entry.IsDir() {
			info, _ := entry.Info()
			sessions = append(sessions, Session{
				ID:        entry.Name(),
				Dir:       filepath.Join(m.baseDir, entry.Name()),
				CreatedAt: info.ModTime(),
			})
		}
	}
	return sessions, nil
}

// CleanupSession removes a session and its files.
func (m *SessionManager) CleanupSession(ctx context.Context, id string) error {
	sessionDir := filepath.Join(m.baseDir, id)
	if err := os.RemoveAll(sessionDir); err != nil {
		return fmt.Errorf("failed to cleanup session: %w", err)
	}
	m.logger.Info(ctx, "Cleaned up session %s", id)
	return nil
}
