// Package rlm provides RLM (Recursive Language Model) integration for Maestro.
package rlm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CheckpointConfig configures checkpoint behavior.
type CheckpointConfig struct {
	// Dir is the directory to store checkpoints (default: ~/.maestro/rlm_checkpoints/)
	Dir string

	// Interval is how often to checkpoint (every N iterations, default: 5)
	Interval int

	// MaxCheckpoints limits how many checkpoints to retain per session (default: 10)
	MaxCheckpoints int

	// AutoCleanup enables automatic cleanup of old checkpoints
	AutoCleanup bool

	// RetentionDays specifies how long to keep checkpoints (default: 7 days)
	RetentionDays int
}

// DefaultCheckpointConfig returns sensible defaults.
func DefaultCheckpointConfig() CheckpointConfig {
	homeDir, _ := os.UserHomeDir()
	return CheckpointConfig{
		Dir:            filepath.Join(homeDir, ".maestro", "rlm_checkpoints"),
		Interval:       5,
		MaxCheckpoints: 10,
		AutoCleanup:    true,
		RetentionDays:  7,
	}
}

// CheckpointManager handles saving and restoring RLM state.
type CheckpointManager struct {
	config    CheckpointConfig
	sessionID string

	mu              sync.Mutex
	currentState    *CheckpointState
	checkpointCount int
}

// CheckpointState represents the complete RLM state at a checkpoint.
type CheckpointState struct {
	// SessionID uniquely identifies this session
	SessionID string `json:"session_id"`

	// Iteration is the current iteration number
	Iteration int `json:"iteration"`

	// REPLVariables contains all REPL state variables
	REPLVariables map[string]any `json:"repl_variables"`

	// PartialResults contains intermediate results
	PartialResults []PartialResult `json:"partial_results,omitempty"`

	// TokensUsed tracks total tokens consumed
	TokensUsed TokenUsage `json:"tokens_used"`

	// CostUSD tracks total cost
	CostUSD float64 `json:"cost_usd"`

	// Query is the original query
	Query string `json:"query"`

	// Context metadata (not full content, just reference info)
	ContextRef ContextReference `json:"context_ref,omitempty"`

	// Timestamps
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	CheckpointAt time.Time `json:"checkpoint_at"`

	// Status indicates execution state
	Status CheckpointStatus `json:"status"`

	// Error message if status is failed
	Error string `json:"error,omitempty"`
}

// PartialResult stores intermediate computation results.
type PartialResult struct {
	Key       string    `json:"key"`
	Value     any       `json:"value"`
	Iteration int       `json:"iteration"`
	Timestamp time.Time `json:"timestamp"`
}

// ContextReference stores metadata about the context without the full content.
type ContextReference struct {
	// Path to the content (file or directory)
	Path string `json:"path,omitempty"`

	// Hash of the content for staleness detection
	ContentHash string `json:"content_hash,omitempty"`

	// Size in bytes
	SizeBytes int64 `json:"size_bytes"`

	// LastModified time
	LastModified time.Time `json:"last_modified,omitempty"`
}

// CheckpointStatus indicates the state of execution at checkpoint.
type CheckpointStatus string

const (
	StatusInProgress CheckpointStatus = "in_progress"
	StatusCompleted  CheckpointStatus = "completed"
	StatusFailed     CheckpointStatus = "failed"
	StatusResumed    CheckpointStatus = "resumed"
)

// NewCheckpointManager creates a new checkpoint manager.
func NewCheckpointManager(config CheckpointConfig) (*CheckpointManager, error) {
	if config.Dir == "" {
		config = DefaultCheckpointConfig()
	}

	// Ensure directory exists
	if err := os.MkdirAll(config.Dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Generate session ID
	sessionID := fmt.Sprintf("rlm_%d", time.Now().UnixNano())

	cm := &CheckpointManager{
		config:    config,
		sessionID: sessionID,
		currentState: &CheckpointState{
			SessionID:     sessionID,
			REPLVariables: make(map[string]any),
			Status:        StatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		},
	}

	// Run cleanup if enabled
	if config.AutoCleanup {
		go cm.cleanupOldCheckpoints()
	}

	return cm, nil
}

// SetSessionID sets the session ID (for resumption).
func (m *CheckpointManager) SetSessionID(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionID = id
	m.currentState.SessionID = id
}

// GetSessionID returns the current session ID.
func (m *CheckpointManager) GetSessionID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionID
}

// UpdateState updates the current state without creating a checkpoint.
func (m *CheckpointManager) UpdateState(iteration int, replVars map[string]any, tokens TokenUsage, costUSD float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.currentState.Iteration = iteration
	m.currentState.TokensUsed = tokens
	m.currentState.CostUSD = costUSD
	m.currentState.UpdatedAt = time.Now()

	// Deep copy REPL variables
	m.currentState.REPLVariables = make(map[string]any)
	for k, v := range replVars {
		m.currentState.REPLVariables[k] = v
	}
}

// SetQuery sets the original query.
func (m *CheckpointManager) SetQuery(query string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentState.Query = query
}

// SetContextRef sets the context reference metadata.
func (m *CheckpointManager) SetContextRef(ref ContextReference) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentState.ContextRef = ref
}

// AddPartialResult adds an intermediate result.
func (m *CheckpointManager) AddPartialResult(key string, value any, iteration int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.currentState.PartialResults = append(m.currentState.PartialResults, PartialResult{
		Key:       key,
		Value:     value,
		Iteration: iteration,
		Timestamp: time.Now(),
	})
}

// ShouldCheckpoint returns true if we should create a checkpoint at this iteration.
func (m *CheckpointManager) ShouldCheckpoint(iteration int) bool {
	if m.config.Interval <= 0 {
		return false
	}
	return iteration > 0 && iteration%m.config.Interval == 0
}

// Save creates a checkpoint file for the current state.
func (m *CheckpointManager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.currentState.CheckpointAt = time.Now()
	m.checkpointCount++

	// Generate checkpoint filename
	filename := fmt.Sprintf("%s_iter%d_%d.json",
		m.sessionID,
		m.currentState.Iteration,
		time.Now().Unix())
	path := filepath.Join(m.config.Dir, filename)

	// Marshal state
	data, err := json.MarshalIndent(m.currentState, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint: %w", err)
	}

	// Write atomically (write to temp, then rename)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write checkpoint: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to finalize checkpoint: %w", err)
	}

	// Cleanup excess checkpoints for this session
	if m.config.MaxCheckpoints > 0 {
		go m.pruneSessionCheckpoints()
	}

	return nil
}

// Load restores state from a checkpoint file.
func (m *CheckpointManager) Load(path string) (*CheckpointState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint: %w", err)
	}

	var state CheckpointState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse checkpoint: %w", err)
	}

	// Update manager state
	m.mu.Lock()
	m.sessionID = state.SessionID
	m.currentState = &state
	m.currentState.Status = StatusResumed
	m.currentState.UpdatedAt = time.Now()
	m.mu.Unlock()

	return &state, nil
}

// LoadLatest loads the most recent checkpoint for a session.
func (m *CheckpointManager) LoadLatest(sessionID string) (*CheckpointState, error) {
	checkpoints, err := m.ListCheckpoints(sessionID)
	if err != nil {
		return nil, err
	}
	if len(checkpoints) == 0 {
		return nil, fmt.Errorf("no checkpoints found for session %s", sessionID)
	}

	// Checkpoints are sorted by time descending, so first is latest
	return m.Load(checkpoints[0].Path)
}

// CheckpointInfo contains metadata about a checkpoint file.
type CheckpointInfo struct {
	Path       string
	SessionID  string
	Iteration  int
	Timestamp  time.Time
	SizeBytes  int64
}

// ListCheckpoints lists all checkpoints, optionally filtered by session.
func (m *CheckpointManager) ListCheckpoints(sessionID string) ([]CheckpointInfo, error) {
	entries, err := os.ReadDir(m.config.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read checkpoint directory: %w", err)
	}

	var checkpoints []CheckpointInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Parse checkpoint filename
		name := strings.TrimSuffix(entry.Name(), ".json")
		parts := strings.Split(name, "_")
		if len(parts) < 3 {
			continue
		}

		// Extract session ID (may contain underscores)
		sid := strings.Join(parts[:len(parts)-2], "_")

		// Filter by session if specified
		if sessionID != "" && sid != sessionID {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		checkpoints = append(checkpoints, CheckpointInfo{
			Path:      filepath.Join(m.config.Dir, entry.Name()),
			SessionID: sid,
			Timestamp: info.ModTime(),
			SizeBytes: info.Size(),
		})
	}

	// Sort by timestamp descending (most recent first)
	sort.Slice(checkpoints, func(i, j int) bool {
		return checkpoints[i].Timestamp.After(checkpoints[j].Timestamp)
	})

	return checkpoints, nil
}

// ListSessions returns a list of unique session IDs with checkpoints.
func (m *CheckpointManager) ListSessions() ([]string, error) {
	checkpoints, err := m.ListCheckpoints("")
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var sessions []string
	for _, cp := range checkpoints {
		if !seen[cp.SessionID] {
			seen[cp.SessionID] = true
			sessions = append(sessions, cp.SessionID)
		}
	}
	return sessions, nil
}

// Delete removes a checkpoint file.
func (m *CheckpointManager) Delete(path string) error {
	return os.Remove(path)
}

// DeleteSession removes all checkpoints for a session.
func (m *CheckpointManager) DeleteSession(sessionID string) error {
	checkpoints, err := m.ListCheckpoints(sessionID)
	if err != nil {
		return err
	}

	var lastErr error
	for _, cp := range checkpoints {
		if err := m.Delete(cp.Path); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// pruneSessionCheckpoints removes excess checkpoints for the current session.
func (m *CheckpointManager) pruneSessionCheckpoints() {
	checkpoints, err := m.ListCheckpoints(m.sessionID)
	if err != nil || len(checkpoints) <= m.config.MaxCheckpoints {
		return
	}

	// Remove oldest checkpoints (list is sorted newest first)
	for i := m.config.MaxCheckpoints; i < len(checkpoints); i++ {
		os.Remove(checkpoints[i].Path)
	}
}

// cleanupOldCheckpoints removes checkpoints older than retention period.
func (m *CheckpointManager) cleanupOldCheckpoints() {
	if m.config.RetentionDays <= 0 {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -m.config.RetentionDays)
	checkpoints, err := m.ListCheckpoints("")
	if err != nil {
		return
	}

	for _, cp := range checkpoints {
		if cp.Timestamp.Before(cutoff) {
			os.Remove(cp.Path)
		}
	}
}

// MarkCompleted marks the session as completed.
func (m *CheckpointManager) MarkCompleted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentState.Status = StatusCompleted
	m.currentState.UpdatedAt = time.Now()
}

// MarkFailed marks the session as failed with an error message.
func (m *CheckpointManager) MarkFailed(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentState.Status = StatusFailed
	if err != nil {
		m.currentState.Error = err.Error()
	}
	m.currentState.UpdatedAt = time.Now()
}

// CurrentState returns the current state.
func (m *CheckpointManager) CurrentState() CheckpointState {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Return a copy
	state := *m.currentState
	state.REPLVariables = make(map[string]any)
	for k, v := range m.currentState.REPLVariables {
		state.REPLVariables[k] = v
	}
	return state
}

// IsResumable checks if a checkpoint can be resumed (context still valid).
func IsResumable(state *CheckpointState) (bool, string) {
	if state.Status == StatusCompleted {
		return false, "session already completed"
	}

	// Check if context file still exists and hasn't changed
	if state.ContextRef.Path != "" {
		info, err := os.Stat(state.ContextRef.Path)
		if err != nil {
			return false, fmt.Sprintf("context path no longer accessible: %v", err)
		}

		// Check if modified
		if !info.ModTime().Equal(state.ContextRef.LastModified) {
			return true, "warning: context has been modified since checkpoint"
		}
	}

	return true, ""
}
