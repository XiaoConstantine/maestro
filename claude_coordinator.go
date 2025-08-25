package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// TaskType represents different types of coordination tasks.
type TaskType int

const (
	TaskFileEdit TaskType = iota
	TaskCodeReview
	TaskTesting
	TaskResearch
	TaskDocumentation
	TaskDebugging
)

func (t TaskType) String() string {
	switch t {
	case TaskFileEdit:
		return "file_edit"
	case TaskCodeReview:
		return "code_review"
	case TaskTesting:
		return "testing"
	case TaskResearch:
		return "research"
	case TaskDocumentation:
		return "documentation"
	case TaskDebugging:
		return "debugging"
	default:
		return "unknown"
	}
}

// CoordinationTask represents a task that can be distributed across sessions.
type CoordinationTask struct {
	ID           string
	Type         TaskType
	Description  string
	Priority     int
	Dependencies []string
	AssignedTo   string
	Status       string
	CreatedAt    time.Time
	CompletedAt  *time.Time
	Result       string
}

// ClaudeCoordinator manages coordination between multiple Claude sessions.
type ClaudeCoordinator struct {
	sessionManager *ClaudeSessionManager
	tasks          map[string]*CoordinationTask
	taskQueue      []*CoordinationTask
	mutex          sync.RWMutex
	logger         *logging.Logger
	console        ConsoleInterface
}

// NewClaudeCoordinator creates a new coordinator.
func NewClaudeCoordinator(sessionManager *ClaudeSessionManager, logger *logging.Logger, console ConsoleInterface) *ClaudeCoordinator {
	return &ClaudeCoordinator{
		sessionManager: sessionManager,
		tasks:          make(map[string]*CoordinationTask),
		taskQueue:      make([]*CoordinationTask, 0),
		logger:         logger,
		console:        console,
	}
}

// CreateTask creates a new coordination task.
func (cc *ClaudeCoordinator) CreateTask(taskType TaskType, description string, priority int) *CoordinationTask {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	task := &CoordinationTask{
		ID:           fmt.Sprintf("task_%d", time.Now().UnixNano()),
		Type:         taskType,
		Description:  description,
		Priority:     priority,
		Status:       "pending",
		CreatedAt:    time.Now(),
		Dependencies: make([]string, 0),
	}

	cc.tasks[task.ID] = task
	cc.taskQueue = append(cc.taskQueue, task)

	return task
}

// AssignTask assigns a task to a specific Claude session.
func (cc *ClaudeCoordinator) AssignTask(taskID, sessionID string) error {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	task, exists := cc.tasks[taskID]
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	// Verify session exists
	_, err := cc.sessionManager.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("cannot assign to session %s: %w", sessionID, err)
	}

	task.AssignedTo = sessionID
	task.Status = "assigned"

	cc.logger.Info(context.Background(), "Assigned task %s to session %s", taskID, sessionID)
	return nil
}

// ExecuteTask executes a task on its assigned session.
func (cc *ClaudeCoordinator) ExecuteTask(ctx context.Context, taskID string) error {
	cc.mutex.RLock()
	task, exists := cc.tasks[taskID]
	cc.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	if task.AssignedTo == "" {
		return fmt.Errorf("task %s is not assigned to any session", taskID)
	}

	// Check dependencies
	if !cc.areDependenciesMet(task) {
		return fmt.Errorf("dependencies not met for task %s", taskID)
	}

	// Update task status
	cc.mutex.Lock()
	task.Status = "executing"
	cc.mutex.Unlock()

	// Deprecated: Direct command execution is no longer supported. Instruct user to use interactive mode.
	cc.console.Printf("⚠️  Direct execution deprecated. Use /switch %s then /enter to run: %s\n", task.AssignedTo, cc.buildCommand(task))

	// Mark as completed
	cc.mutex.Lock()
	task.Status = "completed"
	now := time.Now()
	task.CompletedAt = &now
	cc.mutex.Unlock()

	cc.logger.Info(ctx, "Completed task %s on session %s", taskID, task.AssignedTo)
	return nil
}

// buildCommand builds the appropriate command for a task type.
func (cc *ClaudeCoordinator) buildCommand(task *CoordinationTask) string {
	switch task.Type {
	case TaskFileEdit:
		return task.Description // Direct description for file editing
	case TaskCodeReview:
		return fmt.Sprintf("Please review the following: %s", task.Description)
	case TaskTesting:
		return fmt.Sprintf("Please test: %s", task.Description)
	case TaskResearch:
		return fmt.Sprintf("Please research: %s", task.Description)
	case TaskDocumentation:
		return fmt.Sprintf("Please document: %s", task.Description)
	case TaskDebugging:
		return fmt.Sprintf("Please debug: %s", task.Description)
	default:
		return task.Description
	}
}

// areDependenciesMet checks if all task dependencies are completed.
func (cc *ClaudeCoordinator) areDependenciesMet(task *CoordinationTask) bool {
	for _, depID := range task.Dependencies {
		depTask, exists := cc.tasks[depID]
		if !exists || depTask.Status != "completed" {
			return false
		}
	}
	return true
}

// AutoAssignTasks automatically assigns pending tasks to available sessions.
func (cc *ClaudeCoordinator) AutoAssignTasks(ctx context.Context) error {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	sessions := cc.sessionManager.ListSessions()
	availableSessions := make([]*ClaudeSession, 0)

	// Find available sessions
	for _, session := range sessions {
		if session.Status == SessionReady || session.Status == SessionIdle {
			availableSessions = append(availableSessions, session)
		}
	}

	if len(availableSessions) == 0 {
		return fmt.Errorf("no available sessions for task assignment")
	}

	// Assign pending tasks
	assignedCount := 0
	for _, task := range cc.taskQueue {
		if task.Status == "pending" && len(availableSessions) > 0 {
			// Simple round-robin assignment
			sessionIndex := assignedCount % len(availableSessions)
			session := availableSessions[sessionIndex]

			task.AssignedTo = session.ID
			task.Status = "assigned"
			assignedCount++

			cc.logger.Info(ctx, "Auto-assigned task %s to session %s", task.ID, session.ID)
		}
	}

	return nil
}

// CoordinateMultiSessionTask coordinates a complex task across multiple sessions.
func (cc *ClaudeCoordinator) CoordinateMultiSessionTask(ctx context.Context, description string) error {
	cc.console.Printf("🎯 Coordinating multi-session task: %s\n", description)

	// Analyze task and break it down
	subtasks := cc.analyzeAndBreakdownTask(description)

	if len(subtasks) == 0 {
		return fmt.Errorf("could not break down task into subtasks")
	}

	cc.console.Printf("📋 Identified %d subtasks\n", len(subtasks))

	// Create coordination tasks
	taskIDs := make([]string, len(subtasks))
	for i, subtask := range subtasks {
		task := cc.CreateTask(subtask.Type, subtask.Description, subtask.Priority)
		taskIDs[i] = task.ID

		// Set up dependencies
		if i > 0 {
			task.Dependencies = []string{taskIDs[i-1]}
		}
	}

	// Auto-assign tasks
	if err := cc.AutoAssignTasks(ctx); err != nil {
		return fmt.Errorf("failed to assign tasks: %w", err)
	}

	// Execute tasks in order
	for _, taskID := range taskIDs {
		cc.console.Printf("⚡ Executing task: %s\n", taskID)
		if err := cc.ExecuteTask(ctx, taskID); err != nil {
			return fmt.Errorf("failed to execute task %s: %w", taskID, err)
		}

		// Wait a bit between tasks
		time.Sleep(2 * time.Second)
	}

	cc.console.Printf("✅ Multi-session task completed successfully\n")
	return nil
}

// SubTask represents a breakdown of a larger task.
type SubTask struct {
	Type        TaskType
	Description string
	Priority    int
}

// analyzeAndBreakdownTask analyzes a complex task and breaks it into subtasks.
func (cc *ClaudeCoordinator) analyzeAndBreakdownTask(description string) []SubTask {
	desc := strings.ToLower(description)
	subtasks := make([]SubTask, 0)

	// Simple heuristic-based breakdown
	if strings.Contains(desc, "create") && strings.Contains(desc, "app") {
		subtasks = append(subtasks,
			SubTask{TaskFileEdit, "Create project structure", 1},
			SubTask{TaskFileEdit, "Implement main functionality", 2},
			SubTask{TaskTesting, "Write tests", 3},
			SubTask{TaskDocumentation, "Add documentation", 4},
		)
	} else if strings.Contains(desc, "review") && strings.Contains(desc, "code") {
		subtasks = append(subtasks,
			SubTask{TaskCodeReview, "Review code structure", 1},
			SubTask{TaskCodeReview, "Check for bugs", 2},
			SubTask{TaskTesting, "Verify tests", 3},
		)
	} else if strings.Contains(desc, "debug") || strings.Contains(desc, "fix") {
		subtasks = append(subtasks,
			SubTask{TaskDebugging, "Identify the issue", 1},
			SubTask{TaskFileEdit, "Implement fix", 2},
			SubTask{TaskTesting, "Test the fix", 3},
		)
	} else {
		// Default breakdown for unknown tasks
		subtasks = append(subtasks, SubTask{TaskFileEdit, description, 1})
	}

	return subtasks
}

// GetTaskStatus returns the status of a task.
func (cc *ClaudeCoordinator) GetTaskStatus(taskID string) (*CoordinationTask, error) {
	cc.mutex.RLock()
	defer cc.mutex.RUnlock()

	task, exists := cc.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	return task, nil
}

// ListTasks returns all coordination tasks.
func (cc *ClaudeCoordinator) ListTasks() []*CoordinationTask {
	cc.mutex.RLock()
	defer cc.mutex.RUnlock()

	tasks := make([]*CoordinationTask, 0, len(cc.tasks))
	for _, task := range cc.tasks {
		tasks = append(tasks, task)
	}

	return tasks
}

// GetCoordinationStats returns statistics about coordination activities.
func (cc *ClaudeCoordinator) GetCoordinationStats() map[string]interface{} {
	cc.mutex.RLock()
	defer cc.mutex.RUnlock()

	stats := map[string]interface{}{
		"total_tasks": len(cc.tasks),
		"queue_size":  len(cc.taskQueue),
	}

	statusCounts := make(map[string]int)
	typeCounts := make(map[string]int)

	for _, task := range cc.tasks {
		statusCounts[task.Status]++
		typeCounts[task.Type.String()]++
	}

	stats["status_breakdown"] = statusCounts
	stats["type_breakdown"] = typeCounts

	return stats
}
