package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// ClaudeSessionStatus represents the current state of a Claude session.
type ClaudeSessionStatus int

const (
	SessionInitializing ClaudeSessionStatus = iota
	SessionReady
	SessionBusy
	SessionIdle
	SessionError
	SessionClosed
)

func (s ClaudeSessionStatus) String() string {
	switch s {
	case SessionInitializing:
		return "initializing"
	case SessionReady:
		return "ready"
	case SessionBusy:
		return "busy"
	case SessionIdle:
		return "idle"
	case SessionError:
		return "error"
	case SessionClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// ClaudeSession represents a single Claude Code session.
type ClaudeSession struct {
	ID         string
	Name       string
	Purpose    string
	Status     ClaudeSessionStatus
	CreatedAt  time.Time
	LastUsed   time.Time
	WorkingDir string

	// Claude session management
	ClaudeSessionID  string // Claude's internal session ID for --resume
	HasActiveSession bool   // Whether this session has been used before

	// Session state
	mutex    sync.RWMutex
	isActive bool

	// Coordination
	tags     []string
	metadata map[string]interface{}
}

// ClaudeSessionManager manages multiple Claude Code sessions.
type ClaudeSessionManager struct {
	sessions    map[string]*ClaudeSession
	activeSess  map[string]*ClaudeSession
	mutex       sync.RWMutex
	logger      *logging.Logger
	console     ConsoleInterface
	maxSessions int
}

// NewClaudeSessionManager creates a new session manager.
func NewClaudeSessionManager(logger *logging.Logger, console ConsoleInterface) *ClaudeSessionManager {
	return &ClaudeSessionManager{
		sessions:    make(map[string]*ClaudeSession),
		activeSess:  make(map[string]*ClaudeSession),
		logger:      logger,
		console:     console,
		maxSessions: 10, // Configurable limit
	}
}

// CreateSession creates a new Claude Code session.
func (sm *ClaudeSessionManager) CreateSession(ctx context.Context, name, purpose string, workingDir string) (*ClaudeSession, error) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	// Check session limits
	if len(sm.sessions) >= sm.maxSessions {
		return nil, fmt.Errorf("maximum number of sessions (%d) reached", sm.maxSessions)
	}

	// Validate and normalize working directory
	if workingDir == "" {
		workingDir = "."
	}

	// Convert to absolute path for clarity
	absDir, err := filepath.Abs(workingDir)
	if err == nil {
		workingDir = absDir
	}

	// Generate unique ID
	sessionID := fmt.Sprintf("claude_%d", time.Now().Unix())

	// Create session object
	session := &ClaudeSession{
		ID:               sessionID,
		Name:             name,
		Purpose:          purpose,
		Status:           SessionReady,
		CreatedAt:        time.Now(),
		LastUsed:         time.Now(),
		WorkingDir:       workingDir,
		ClaudeSessionID:  "", // Will be set after first Claude session
		HasActiveSession: false,
		isActive:         false,
		tags:             make([]string, 0),
		metadata:         make(map[string]interface{}),
	}

	// Mark session as ready - we'll start Claude on-demand
	session.Status = SessionReady
	session.isActive = true

	// Store session
	sm.sessions[sessionID] = session
	sm.activeSess[sessionID] = session

	sm.logger.Info(ctx, "Created Claude session: %s (%s)", sessionID, name)
	return session, nil
}

// These methods are no longer needed since we use on-demand Claude process launching

// Commands are now sent directly to Claude via the session switcher.
func (sm *ClaudeSessionManager) SendCommand(sessionID, command string) error {
	// This method is no longer used since we delegate directly to Claude processes
	return fmt.Errorf("SendCommand is deprecated - use session switcher for direct Claude interaction")
}

// GetSession retrieves a session by ID.
func (sm *ClaudeSessionManager) GetSession(sessionID string) (*ClaudeSession, error) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	session, exists := sm.sessions[sessionID]
	if !exists {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	return session, nil
}

// ListSessions returns all sessions.
func (sm *ClaudeSessionManager) ListSessions() []*ClaudeSession {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	sessions := make([]*ClaudeSession, 0, len(sm.sessions))
	for _, session := range sm.sessions {
		sessions = append(sessions, session)
	}

	return sessions
}

// CloseSession closes a specific Claude session.
func (sm *ClaudeSessionManager) CloseSession(sessionID string) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	session, exists := sm.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session %s not found", sessionID)
	}

	// Update status (no process to kill since we use on-demand launching)
	session.Status = SessionClosed
	session.isActive = false

	// Remove from active sessions
	delete(sm.activeSess, sessionID)

	sm.logger.Info(context.Background(), "Closed Claude session: %s", sessionID)
	return nil
}

// CloseAllSessions closes all Claude sessions.
func (sm *ClaudeSessionManager) CloseAllSessions() error {
	sm.mutex.RLock()
	sessionIDs := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		sessionIDs = append(sessionIDs, id)
	}
	sm.mutex.RUnlock()

	for _, id := range sessionIDs {
		if err := sm.CloseSession(id); err != nil {
			sm.logger.Error(context.Background(), "Failed to close session %s: %v", id, err)
		}
	}

	return nil
}

// GetSessionOutput is deprecated since we use direct Claude process delegation.
func (sm *ClaudeSessionManager) GetSessionOutput(sessionID string, lines int) ([]string, error) {
	// Output is now handled directly by Claude processes
	return []string{"Session output handled by Claude directly"}, nil
}

// AddSessionTag adds a tag to a session for organization.
func (sm *ClaudeSessionManager) AddSessionTag(sessionID, tag string) error {
	session, err := sm.GetSession(sessionID)
	if err != nil {
		return err
	}

	session.mutex.Lock()
	defer session.mutex.Unlock()

	// Check if tag already exists
	for _, existingTag := range session.tags {
		if existingTag == tag {
			return nil // Tag already exists
		}
	}

	session.tags = append(session.tags, tag)
	return nil
}

// FindSessionsByTag finds sessions with a specific tag.
func (sm *ClaudeSessionManager) FindSessionsByTag(tag string) []*ClaudeSession {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	var result []*ClaudeSession
	for _, session := range sm.sessions {
		session.mutex.RLock()
		for _, sessionTag := range session.tags {
			if sessionTag == tag {
				result = append(result, session)
				break
			}
		}
		session.mutex.RUnlock()
	}

	return result
}

// SetSessionMetadata sets metadata for a session.
func (sm *ClaudeSessionManager) SetSessionMetadata(sessionID, key string, value interface{}) error {
	session, err := sm.GetSession(sessionID)
	if err != nil {
		return err
	}

	session.mutex.Lock()
	defer session.mutex.Unlock()

	session.metadata[key] = value
	return nil
}

// GetSessionStats returns statistics about all sessions.
func (sm *ClaudeSessionManager) GetSessionStats() map[string]interface{} {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	stats := map[string]interface{}{
		"total_sessions":  len(sm.sessions),
		"active_sessions": len(sm.activeSess),
		"max_sessions":    sm.maxSessions,
	}

	statusCounts := make(map[string]int)
	for _, session := range sm.sessions {
		status := session.Status.String()
		statusCounts[status]++
	}
	stats["status_breakdown"] = statusCounts

	return stats
}

// directoryExists checks if a directory exists.
func (sm *ClaudeSessionManager) directoryExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// CreateSessionWithValidation creates a session with directory validation.
func (sm *ClaudeSessionManager) CreateSessionWithValidation(ctx context.Context, name, purpose, workingDir string) (*ClaudeSession, error) {
	// Validate inputs
	if name == "" {
		return nil, fmt.Errorf("session name cannot be empty")
	}

	if purpose == "" {
		return nil, fmt.Errorf("session purpose cannot be empty")
	}

	// Default working directory to current directory
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			workingDir = "."
		}
	}

	// Check if directory exists, offer to create or use current
	if !sm.directoryExists(workingDir) {
		sm.console.Printf("⚠️  Directory '%s' does not exist.\n", workingDir)
		currentDir, _ := os.Getwd()
		sm.console.Printf("Using current directory instead: %s\n", currentDir)
		workingDir = currentDir
	}

	return sm.CreateSession(ctx, name, purpose, workingDir)
}
