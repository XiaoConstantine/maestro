package subagent

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	dspyerrors "github.com/XiaoConstantine/dspy-go/pkg/errors"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// SessionManager manages file-based context sharing between Maestro and CLI subagents.
type SessionManager struct {
	baseDir    string
	logger     *logging.Logger
	eventStore sessionevent.SessionEventStore
}

// Session represents an active context-sharing session.
type Session struct {
	ID        string
	Dir       string
	CreatedAt time.Time
}

type sessionManagerConfig struct {
	eventStore sessionevent.SessionEventStore
}

type sessionEventCleaner interface {
	DeleteSession(ctx context.Context, sessionID string) error
}

// SessionManagerOption configures optional dual-write session behavior.
type SessionManagerOption func(*sessionManagerConfig)

// WithSessionEventStore enables dual-write/listing behavior using sessionevent.
func WithSessionEventStore(store sessionevent.SessionEventStore) SessionManagerOption {
	return func(cfg *sessionManagerConfig) {
		if isNilSessionEventStore(store) {
			return
		}
		cfg.eventStore = store
	}
}

// NewSessionManager creates a session manager with the given base directory.
func NewSessionManager(baseDir string, logger *logging.Logger, opts ...SessionManagerOption) (*SessionManager, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	cfg := sessionManagerConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	return &SessionManager{
		baseDir:    baseDir,
		logger:     logger,
		eventStore: cfg.eventStore,
	}, nil
}

// CreateSession creates a new session directory with initial context.
func (m *SessionManager) CreateSession(ctx context.Context, id string, initialContext map[string]interface{}) (*Session, error) {
	sessionDir := filepath.Join(m.baseDir, id)
	if _, err := os.Stat(sessionDir); err == nil {
		return nil, fmt.Errorf("session already exists: %s", id)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to stat session dir: %w", err)
	}

	if m.eventStore != nil {
		if _, err := m.eventStore.GetSession(ctx, id); err == nil {
			return nil, fmt.Errorf("session already exists: %s", id)
		} else if !isNotFoundError(err) {
			return nil, fmt.Errorf("failed to check session event store: %w", err)
		}
	}

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

	if err := m.persistSessionEvent(ctx, session, initialContext); err != nil {
		_ = os.RemoveAll(sessionDir)
		return nil, err
	}

	m.logger.Info(ctx, "Created session %s at %s", id, sessionDir)
	return session, nil
}

// GetSession retrieves an existing session.
func (m *SessionManager) GetSession(id string) (*Session, error) {
	sessionDir := filepath.Join(m.baseDir, id)
	info, err := os.Stat(sessionDir)
	if err == nil {
		return &Session{
			ID:        id,
			Dir:       sessionDir,
			CreatedAt: info.ModTime(),
		}, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to stat session: %w", err)
	}

	if m.eventStore == nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	eventSession, eventErr := m.eventStore.GetSession(context.Background(), id)
	if eventErr != nil {
		return nil, fmt.Errorf("session not found: %w", eventErr)
	}

	contextData := metadataToContext(eventSession.Metadata)
	if _, ok := contextData["purpose"]; !ok && eventSession.Title != "" {
		contextData["purpose"] = eventSession.Title
	}

	session := &Session{
		ID:        eventSession.ID,
		Dir:       sessionDir,
		CreatedAt: eventSession.CreatedAt,
	}
	if err := m.materializeSessionFiles(session, contextData); err != nil {
		return nil, err
	}

	return session, nil
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
	sessionsByID := make(map[string]Session)

	if m.eventStore != nil {
		eventSessions, err := m.eventStore.ListSessions(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to list sessions from event store: %w", err)
		}
		for _, session := range eventSessions {
			sessionsByID[session.ID] = Session{
				ID:        session.ID,
				Dir:       filepath.Join(m.baseDir, session.ID),
				CreatedAt: session.CreatedAt,
			}
		}
	}

	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				m.logger.Warn(context.Background(), "Skipping session entry %s: %v", entry.Name(), err)
				continue
			}
			if existing, ok := sessionsByID[entry.Name()]; ok {
				existing.Dir = filepath.Join(m.baseDir, entry.Name())
				if info.ModTime().After(existing.CreatedAt) {
					existing.CreatedAt = info.ModTime()
				}
				sessionsByID[entry.Name()] = existing
				continue
			}
			sessionsByID[entry.Name()] = Session{
				ID:        entry.Name(),
				Dir:       filepath.Join(m.baseDir, entry.Name()),
				CreatedAt: info.ModTime(),
			}
		}
	}

	sessions := make([]Session, 0, len(sessionsByID))
	for _, session := range sessionsByID {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})
	return sessions, nil
}

// CleanupSession removes a session and its files.
func (m *SessionManager) CleanupSession(ctx context.Context, id string) error {
	if m.eventStore != nil {
		cleaner, ok := m.eventStore.(sessionEventCleaner)
		if !ok {
			return fmt.Errorf("session cleanup requires a deletable session event store")
		}
		if err := cleaner.DeleteSession(ctx, id); err != nil && !isNotFoundError(err) {
			return fmt.Errorf("failed to cleanup session event state: %w", err)
		}
	}

	sessionDir := filepath.Join(m.baseDir, id)
	if err := os.RemoveAll(sessionDir); err != nil {
		return fmt.Errorf("failed to cleanup session: %w", err)
	}
	m.logger.Info(ctx, "Cleaned up session %s", id)
	return nil
}

func (m *SessionManager) persistSessionEvent(ctx context.Context, session *Session, initialContext map[string]interface{}) error {
	if m.eventStore == nil {
		return nil
	}

	metadata := contextToMetadata(initialContext)
	_, _, err := m.eventStore.CreateSession(ctx, sessionevent.CreateSessionParams{
		ID:         session.ID,
		Title:      sessionTitle(session.ID, initialContext),
		BranchName: "main",
		Metadata:   metadata,
	})
	if err != nil {
		return fmt.Errorf("failed to persist session event state: %w", err)
	}
	return nil
}

func (m *SessionManager) materializeSessionFiles(session *Session, contextData map[string]interface{}) error {
	if err := os.MkdirAll(session.Dir, 0755); err != nil {
		return fmt.Errorf("failed to create session dir: %w", err)
	}

	contextFile := filepath.Join(session.Dir, "context.md")
	if _, err := os.Stat(contextFile); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat context file: %w", err)
	}

	if err := m.writeContext(session, contextData); err != nil {
		return err
	}
	return nil
}

func sessionTitle(id string, contextData map[string]interface{}) string {
	if purpose, ok := contextData["purpose"].(string); ok && purpose != "" {
		return purpose
	}
	return id
}

func contextToMetadata(contextData map[string]interface{}) map[string]any {
	if len(contextData) == 0 {
		return map[string]any{}
	}
	metadata := make(map[string]any, len(contextData))
	for k, v := range contextData {
		metadata[k] = v
	}
	return metadata
}

func metadataToContext(metadata map[string]any) map[string]interface{} {
	if len(metadata) == 0 {
		return map[string]interface{}{}
	}
	contextData := make(map[string]interface{}, len(metadata))
	for k, v := range metadata {
		contextData[k] = v
	}
	return contextData
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var typedErr *dspyerrors.Error
	return stderrors.As(err, &typedErr) && typedErr.Code() == dspyerrors.ResourceNotFound
}

func isNilSessionEventStore(store sessionevent.SessionEventStore) bool {
	if store == nil {
		return true
	}
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
