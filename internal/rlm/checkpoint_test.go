package rlm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpointManager_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:            tmpDir,
		Interval:       5,
		MaxCheckpoints: 10,
		AutoCleanup:    false,
	}

	cm, err := NewCheckpointManager(config)
	if err != nil {
		t.Fatalf("failed to create checkpoint manager: %v", err)
	}

	if cm.GetSessionID() == "" {
		t.Error("session ID should not be empty")
	}
}

func TestCheckpointManager_UpdateState(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:      tmpDir,
		Interval: 5,
	}

	cm, err := NewCheckpointManager(config)
	if err != nil {
		t.Fatalf("failed to create checkpoint manager: %v", err)
	}

	replVars := map[string]any{
		"chunks":  []string{"chunk1", "chunk2"},
		"results": map[string]int{"a": 1, "b": 2},
	}
	tokens := TokenUsage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}

	cm.UpdateState(5, replVars, tokens, 0.50)
	cm.SetQuery("What is the architecture?")

	state := cm.CurrentState()

	if state.Iteration != 5 {
		t.Errorf("expected iteration 5, got %d", state.Iteration)
	}
	if state.Query != "What is the architecture?" {
		t.Errorf("unexpected query: %s", state.Query)
	}
	if state.TokensUsed.TotalTokens != 1500 {
		t.Errorf("expected 1500 tokens, got %d", state.TokensUsed.TotalTokens)
	}
	if state.CostUSD != 0.50 {
		t.Errorf("expected 0.50 cost, got %f", state.CostUSD)
	}
}

func TestCheckpointManager_ShouldCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:      tmpDir,
		Interval: 5,
	}

	cm, err := NewCheckpointManager(config)
	if err != nil {
		t.Fatalf("failed to create checkpoint manager: %v", err)
	}

	tests := []struct {
		iteration int
		expected  bool
	}{
		{0, false},
		{1, false},
		{4, false},
		{5, true},
		{10, true},
		{15, true},
		{7, false},
	}

	for _, tt := range tests {
		if got := cm.ShouldCheckpoint(tt.iteration); got != tt.expected {
			t.Errorf("ShouldCheckpoint(%d) = %v, want %v", tt.iteration, got, tt.expected)
		}
	}
}

func TestCheckpointManager_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:            tmpDir,
		Interval:       5,
		MaxCheckpoints: 10,
	}

	cm, err := NewCheckpointManager(config)
	if err != nil {
		t.Fatalf("failed to create checkpoint manager: %v", err)
	}

	sessionID := cm.GetSessionID()

	replVars := map[string]any{
		"analysis": "Some analysis result",
		"count":    42,
	}
	tokens := TokenUsage{
		PromptTokens:     2000,
		CompletionTokens: 1000,
		TotalTokens:      3000,
	}

	cm.UpdateState(10, replVars, tokens, 1.25)
	cm.SetQuery("Analyze the codebase")
	cm.AddPartialResult("step1", "completed", 5)

	// Save checkpoint
	err = cm.Save()
	if err != nil {
		t.Fatalf("failed to save checkpoint: %v", err)
	}

	// Verify file exists
	checkpoints, err := cm.ListCheckpoints(sessionID)
	if err != nil {
		t.Fatalf("failed to list checkpoints: %v", err)
	}
	if len(checkpoints) != 1 {
		t.Errorf("expected 1 checkpoint, got %d", len(checkpoints))
	}

	// Create new manager and load
	cm2, err := NewCheckpointManager(config)
	if err != nil {
		t.Fatalf("failed to create second checkpoint manager: %v", err)
	}

	state, err := cm2.Load(checkpoints[0].Path)
	if err != nil {
		t.Fatalf("failed to load checkpoint: %v", err)
	}

	if state.SessionID != sessionID {
		t.Errorf("session ID mismatch: got %s, want %s", state.SessionID, sessionID)
	}
	if state.Iteration != 10 {
		t.Errorf("expected iteration 10, got %d", state.Iteration)
	}
	if state.Query != "Analyze the codebase" {
		t.Errorf("unexpected query: %s", state.Query)
	}
	if len(state.PartialResults) != 1 {
		t.Errorf("expected 1 partial result, got %d", len(state.PartialResults))
	}
	if state.Status != StatusResumed {
		t.Errorf("expected status resumed, got %s", state.Status)
	}
}

func TestCheckpointManager_LoadLatest(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:            tmpDir,
		Interval:       1, // Checkpoint every iteration
		MaxCheckpoints: 10,
	}

	cm, err := NewCheckpointManager(config)
	if err != nil {
		t.Fatalf("failed to create checkpoint manager: %v", err)
	}

	sessionID := cm.GetSessionID()

	// Create multiple checkpoints
	for i := 1; i <= 3; i++ {
		cm.UpdateState(i, map[string]any{"iter": i}, TokenUsage{TotalTokens: i * 100}, float64(i)*0.10)
		time.Sleep(10 * time.Millisecond) // Ensure different timestamps
		if err := cm.Save(); err != nil {
			t.Fatalf("failed to save checkpoint %d: %v", i, err)
		}
	}

	// Load latest
	state, err := cm.LoadLatest(sessionID)
	if err != nil {
		t.Fatalf("failed to load latest: %v", err)
	}

	if state.Iteration != 3 {
		t.Errorf("expected iteration 3, got %d", state.Iteration)
	}
}

func TestCheckpointManager_ListSessions(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:            tmpDir,
		Interval:       1,
		MaxCheckpoints: 10,
	}

	// Create first session
	cm1, _ := NewCheckpointManager(config)
	cm1.UpdateState(1, nil, TokenUsage{}, 0)
	if err := cm1.Save(); err != nil {
		t.Fatalf("failed to save checkpoint: %v", err)
	}

	// Create second session
	cm2, _ := NewCheckpointManager(config)
	cm2.UpdateState(1, nil, TokenUsage{}, 0)
	if err := cm2.Save(); err != nil {
		t.Fatalf("failed to save checkpoint: %v", err)
	}

	sessions, err := cm2.ListSessions()
	if err != nil {
		t.Fatalf("failed to list sessions: %v", err)
	}

	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestCheckpointManager_DeleteSession(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:            tmpDir,
		Interval:       1,
		MaxCheckpoints: 10,
	}

	cm, _ := NewCheckpointManager(config)
	sessionID := cm.GetSessionID()

	// Create checkpoints
	for i := 1; i <= 3; i++ {
		cm.UpdateState(i, nil, TokenUsage{}, 0)
		if err := cm.Save(); err != nil {
			t.Fatalf("failed to save checkpoint: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify checkpoints exist
	checkpoints, _ := cm.ListCheckpoints(sessionID)
	if len(checkpoints) != 3 {
		t.Errorf("expected 3 checkpoints, got %d", len(checkpoints))
	}

	// Delete session
	err := cm.DeleteSession(sessionID)
	if err != nil {
		t.Errorf("failed to delete session: %v", err)
	}

	// Verify deleted
	checkpoints, _ = cm.ListCheckpoints(sessionID)
	if len(checkpoints) != 0 {
		t.Errorf("expected 0 checkpoints after delete, got %d", len(checkpoints))
	}
}

func TestCheckpointManager_PruneCheckpoints(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:            tmpDir,
		Interval:       1,
		MaxCheckpoints: 3,
	}

	cm, _ := NewCheckpointManager(config)
	sessionID := cm.GetSessionID()

	// Create more checkpoints than allowed
	for i := 1; i <= 5; i++ {
		cm.UpdateState(i, nil, TokenUsage{}, 0)
		if err := cm.Save(); err != nil {
			t.Fatalf("failed to save checkpoint: %v", err)
		}
		time.Sleep(50 * time.Millisecond) // Ensure pruning goroutine runs
	}

	// Wait for pruning
	time.Sleep(100 * time.Millisecond)

	checkpoints, _ := cm.ListCheckpoints(sessionID)
	if len(checkpoints) > 3 {
		t.Errorf("expected at most 3 checkpoints, got %d", len(checkpoints))
	}
}

func TestCheckpointManager_AddPartialResult(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:      tmpDir,
		Interval: 5,
	}

	cm, _ := NewCheckpointManager(config)

	cm.AddPartialResult("analysis", "Found 10 functions", 2)
	cm.AddPartialResult("summary", map[string]int{"files": 5}, 3)

	state := cm.CurrentState()

	if len(state.PartialResults) != 2 {
		t.Errorf("expected 2 partial results, got %d", len(state.PartialResults))
	}

	if state.PartialResults[0].Key != "analysis" {
		t.Errorf("unexpected key: %s", state.PartialResults[0].Key)
	}
	if state.PartialResults[0].Iteration != 2 {
		t.Errorf("expected iteration 2, got %d", state.PartialResults[0].Iteration)
	}
}

func TestCheckpointManager_ContextRef(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.go")
	err := os.WriteFile(testFile, []byte("package test"), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	info, _ := os.Stat(testFile)

	config := CheckpointConfig{
		Dir:      tmpDir,
		Interval: 5,
	}

	cm, _ := NewCheckpointManager(config)
	cm.SetContextRef(ContextReference{
		Path:         testFile,
		ContentHash:  "abc123",
		SizeBytes:    info.Size(),
		LastModified: info.ModTime(),
	})

	state := cm.CurrentState()

	if state.ContextRef.Path != testFile {
		t.Errorf("unexpected path: %s", state.ContextRef.Path)
	}
	if state.ContextRef.ContentHash != "abc123" {
		t.Errorf("unexpected hash: %s", state.ContextRef.ContentHash)
	}
}

func TestCheckpointManager_MarkStatus(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:      tmpDir,
		Interval: 5,
	}

	cm, _ := NewCheckpointManager(config)

	state := cm.CurrentState()
	if state.Status != StatusInProgress {
		t.Errorf("expected in_progress, got %s", state.Status)
	}

	cm.MarkCompleted()
	state = cm.CurrentState()
	if state.Status != StatusCompleted {
		t.Errorf("expected completed, got %s", state.Status)
	}
}

func TestCheckpointManager_MarkFailed(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:      tmpDir,
		Interval: 5,
	}

	cm, _ := NewCheckpointManager(config)

	testErr := os.ErrNotExist
	cm.MarkFailed(testErr)

	state := cm.CurrentState()
	if state.Status != StatusFailed {
		t.Errorf("expected failed, got %s", state.Status)
	}
	if state.Error == "" {
		t.Error("expected error message")
	}
}

func TestIsResumable(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.go")
	err := os.WriteFile(testFile, []byte("package test"), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	info, _ := os.Stat(testFile)

	tests := []struct {
		name       string
		state      *CheckpointState
		wantResume bool
	}{
		{
			name: "completed session",
			state: &CheckpointState{
				Status: StatusCompleted,
			},
			wantResume: false,
		},
		{
			name: "in progress no context",
			state: &CheckpointState{
				Status: StatusInProgress,
			},
			wantResume: true,
		},
		{
			name: "in progress with valid context",
			state: &CheckpointState{
				Status: StatusInProgress,
				ContextRef: ContextReference{
					Path:         testFile,
					LastModified: info.ModTime(),
				},
			},
			wantResume: true,
		},
		{
			name: "in progress with missing context",
			state: &CheckpointState{
				Status: StatusInProgress,
				ContextRef: ContextReference{
					Path: filepath.Join(tmpDir, "nonexistent.go"),
				},
			},
			wantResume: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := IsResumable(tt.state)
			if got != tt.wantResume {
				t.Errorf("IsResumable() = %v, want %v", got, tt.wantResume)
			}
		})
	}
}

func TestCheckpointManager_SetSessionID(t *testing.T) {
	tmpDir := t.TempDir()
	config := CheckpointConfig{
		Dir:      tmpDir,
		Interval: 5,
	}

	cm, _ := NewCheckpointManager(config)

	cm.SetSessionID("custom-session-123")

	if got := cm.GetSessionID(); got != "custom-session-123" {
		t.Errorf("expected custom-session-123, got %s", got)
	}

	state := cm.CurrentState()
	if state.SessionID != "custom-session-123" {
		t.Errorf("state session ID mismatch: %s", state.SessionID)
	}
}
