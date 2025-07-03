package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/logrusorgru/aurora"
)

// ClaudeDashboard provides visualization and management for Claude sessions.
type ClaudeDashboard struct {
	sessionManager *ClaudeSessionManager
	coordinator    *ClaudeCoordinator
	console        ConsoleInterface
}

// NewClaudeDashboard creates a new dashboard.
func NewClaudeDashboard(sessionManager *ClaudeSessionManager, coordinator *ClaudeCoordinator, console ConsoleInterface) *ClaudeDashboard {
	return &ClaudeDashboard{
		sessionManager: sessionManager,
		coordinator:    coordinator,
		console:        console,
	}
}

// ShowOverview displays a comprehensive overview of all Claude sessions.
func (cd *ClaudeDashboard) ShowOverview() {
	cd.console.PrintHeader("🤖 Claude Session Dashboard")

	// Session statistics
	stats := cd.sessionManager.GetSessionStats()
	cd.showSessionStats(stats)

	cd.console.Println()

	// Active sessions
	cd.showActiveSessions()

	cd.console.Println()

	// Coordination overview
	cd.showCoordinationOverview()

	cd.console.Println()

	// Recent activity
	cd.showRecentActivity()
}

// showSessionStats displays session statistics.
func (cd *ClaudeDashboard) showSessionStats(stats map[string]interface{}) {
	cd.console.PrintHeader("📊 Session Statistics")

	total := stats["total_sessions"].(int)
	active := stats["active_sessions"].(int)
	maxSessions := stats["max_sessions"].(int)

	if cd.console.Color() {
		cd.console.Printf("%s %s\n",
			aurora.Blue("Total Sessions:").Bold(),
			aurora.Cyan(fmt.Sprintf("%d/%d", total, maxSessions)).Bold())
		cd.console.Printf("%s %s\n",
			aurora.Green("Active Sessions:").Bold(),
			aurora.Cyan(fmt.Sprintf("%d", active)).Bold())
	} else {
		cd.console.Printf("Total Sessions: %d/%d\n", total, maxSessions)
		cd.console.Printf("Active Sessions: %d\n", active)
	}

	// Status breakdown
	if statusBreakdown, ok := stats["status_breakdown"].(map[string]int); ok {
		cd.console.Println("\nStatus Breakdown:")
		for status, count := range statusBreakdown {
			icon := cd.getStatusIcon(status)
			if cd.console.Color() {
				cd.console.Printf("  %s %s: %s\n", icon,
					aurora.White(status).Bold(),
					aurora.Cyan(fmt.Sprintf("%d", count)))
			} else {
				cd.console.Printf("  %s %s: %d\n", icon, status, count)
			}
		}
	}
}

// showActiveSessions displays detailed info about active sessions.
func (cd *ClaudeDashboard) showActiveSessions() {
	cd.console.PrintHeader("🔥 Active Sessions")

	sessions := cd.sessionManager.ListSessions()
	if len(sessions) == 0 {
		cd.console.Println("No Claude sessions currently running.")
		return
	}

	// Sort sessions by creation time
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
	})

	// Create table header
	cd.console.Printf("%-12s %-20s %-15s %-12s %-15s %s\n",
		"ID", "Name", "Status", "Uptime", "Last Used", "Purpose")
	cd.console.Printf("%s\n", strings.Repeat("─", 100))

	for _, session := range sessions {
		id := cd.truncateString(session.ID, 12)
		name := cd.truncateString(session.Name, 20)
		status := session.Status.String()
		uptime := cd.formatDuration(time.Since(session.CreatedAt))
		lastUsed := cd.formatDuration(time.Since(session.LastUsed))
		purpose := cd.truncateString(session.Purpose, 30)

		statusIcon := cd.getStatusIcon(status)

		if cd.console.Color() {
			statusColor := cd.getStatusColor(status)
			cd.console.Printf("%-12s %-20s %s %-14s %-12s %-15s %s\n",
				aurora.Yellow(id),
				aurora.White(name).Bold(),
				statusColor(statusIcon+" "+status),
				uptime,
				lastUsed,
				aurora.Gray(12, purpose))
		} else {
			cd.console.Printf("%-12s %-20s %s %-11s %-12s %-15s %s\n",
				id, name, statusIcon+" "+status, uptime, lastUsed, purpose)
		}
	}
}

// showCoordinationOverview displays coordination task overview.
func (cd *ClaudeDashboard) showCoordinationOverview() {
	cd.console.PrintHeader("🎯 Task Coordination")

	coordStats := cd.coordinator.GetCoordinationStats()
	totalTasks := coordStats["total_tasks"].(int)
	queueSize := coordStats["queue_size"].(int)

	if cd.console.Color() {
		cd.console.Printf("%s %s\n",
			aurora.Blue("Total Tasks:").Bold(),
			aurora.Cyan(fmt.Sprintf("%d", totalTasks)))
		cd.console.Printf("%s %s\n",
			aurora.Blue("Queued Tasks:").Bold(),
			aurora.Cyan(fmt.Sprintf("%d", queueSize)))
	} else {
		cd.console.Printf("Total Tasks: %d\n", totalTasks)
		cd.console.Printf("Queued Tasks: %d\n", queueSize)
	}

	// Task status breakdown
	if statusBreakdown, ok := coordStats["status_breakdown"].(map[string]int); ok {
		cd.console.Println("\nTask Status:")
		for status, count := range statusBreakdown {
			icon := cd.getTaskStatusIcon(status)
			if cd.console.Color() {
				cd.console.Printf("  %s %s: %s\n", icon,
					aurora.White(status).Bold(),
					aurora.Cyan(fmt.Sprintf("%d", count)))
			} else {
				cd.console.Printf("  %s %s: %d\n", icon, status, count)
			}
		}
	}

	// Task type breakdown
	if typeBreakdown, ok := coordStats["type_breakdown"].(map[string]int); ok {
		cd.console.Println("\nTask Types:")
		for taskType, count := range typeBreakdown {
			icon := cd.getTaskTypeIcon(taskType)
			if cd.console.Color() {
				cd.console.Printf("  %s %s: %s\n", icon,
					aurora.White(taskType).Bold(),
					aurora.Cyan(fmt.Sprintf("%d", count)))
			} else {
				cd.console.Printf("  %s %s: %d\n", icon, taskType, count)
			}
		}
	}
}

// showRecentActivity displays recent session activity.
func (cd *ClaudeDashboard) showRecentActivity() {
	cd.console.PrintHeader("📈 Recent Activity")

	sessions := cd.sessionManager.ListSessions()
	if len(sessions) == 0 {
		cd.console.Println("No recent activity.")
		return
	}

	// Sort by last used (most recent first)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastUsed.After(sessions[j].LastUsed)
	})

	// Show top 5 most recently active sessions
	limit := 5
	if len(sessions) < limit {
		limit = len(sessions)
	}

	for i := 0; i < limit; i++ {
		session := sessions[i]
		ago := cd.formatDuration(time.Since(session.LastUsed))

		if cd.console.Color() {
			cd.console.Printf("• %s (%s) - %s ago\n",
				aurora.Yellow(session.Name).Bold(),
				aurora.Gray(12, session.ID),
				aurora.Blue(ago))
		} else {
			cd.console.Printf("• %s (%s) - %s ago\n",
				session.Name, session.ID, ago)
		}
	}
}

// ShowSessionDetail displays detailed information about a specific session.
func (cd *ClaudeDashboard) ShowSessionDetail(sessionID string) error {
	session, err := cd.sessionManager.GetSession(sessionID)
	if err != nil {
		return err
	}

	cd.console.PrintHeader(fmt.Sprintf("🤖 Session Details: %s", session.Name))

	// Basic info
	if cd.console.Color() {
		cd.console.Printf("%s %s\n", aurora.Blue("ID:").Bold(), aurora.Yellow(session.ID))
		cd.console.Printf("%s %s\n", aurora.Blue("Name:").Bold(), aurora.White(session.Name).Bold())
		cd.console.Printf("%s %s\n", aurora.Blue("Purpose:").Bold(), session.Purpose)
		cd.console.Printf("%s %s\n", aurora.Blue("Status:").Bold(), cd.getStatusColor(session.Status.String())(session.Status.String()))
		cd.console.Printf("%s %s\n", aurora.Blue("Created:").Bold(), session.CreatedAt.Format("2006-01-02 15:04:05"))
		cd.console.Printf("%s %s\n", aurora.Blue("Last Used:").Bold(), session.LastUsed.Format("2006-01-02 15:04:05"))
		cd.console.Printf("%s %s\n", aurora.Blue("Working Dir:").Bold(), session.WorkingDir)
	} else {
		cd.console.Printf("ID: %s\n", session.ID)
		cd.console.Printf("Name: %s\n", session.Name)
		cd.console.Printf("Purpose: %s\n", session.Purpose)
		cd.console.Printf("Status: %s\n", session.Status.String())
		cd.console.Printf("Created: %s\n", session.CreatedAt.Format("2006-01-02 15:04:05"))
		cd.console.Printf("Last Used: %s\n", session.LastUsed.Format("2006-01-02 15:04:05"))
		cd.console.Printf("Working Dir: %s\n", session.WorkingDir)
	}

	// Tags
	if len(session.tags) > 0 {
		cd.console.Printf("\nTags: %s\n", strings.Join(session.tags, ", "))
	}

	// Recent output
	output, err := cd.sessionManager.GetSessionOutput(sessionID, 10)
	if err == nil && len(output) > 0 {
		cd.console.Println("\nRecent Output:")
		cd.console.Println(strings.Repeat("─", 50))
		for _, line := range output {
			cd.console.Printf("  %s\n", line)
		}
	}

	return nil
}

// Interactive command to manage sessions.
func (cd *ClaudeDashboard) HandleCommand(ctx context.Context, command string) error {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	switch parts[0] {
	case "create":
		return cd.handleCreateSession(ctx, parts[1:])
	case "close":
		return cd.handleCloseSession(parts[1:])
	case "detail":
		return cd.handleSessionDetail(parts[1:])
	case "send":
		return cd.handleSendCommand(parts[1:])
	case "coordinate":
		return cd.handleCoordinate(ctx, parts[1:])
	case "overview":
		cd.ShowOverview()
		return nil
	default:
		return fmt.Errorf("unknown command: %s", parts[0])
	}
}

// Helper methods.
func (cd *ClaudeDashboard) truncateString(s string, length int) string {
	if len(s) <= length {
		return s
	}
	return s[:length-3] + "..."
}

func (cd *ClaudeDashboard) formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	} else {
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (cd *ClaudeDashboard) getStatusIcon(status string) string {
	switch status {
	case "ready":
		return "✅"
	case "busy":
		return "⚡"
	case "idle":
		return "💤"
	case "error":
		return "❌"
	case "closed":
		return "🔒"
	case "initializing":
		return "🔄"
	default:
		return "❓"
	}
}

func (cd *ClaudeDashboard) getStatusColor(status string) func(interface{}) aurora.Value {
	switch status {
	case "ready":
		return aurora.Green
	case "busy":
		return aurora.Yellow
	case "idle":
		return aurora.Blue
	case "error":
		return aurora.Red
	case "closed":
		return func(arg interface{}) aurora.Value { return aurora.Gray(12, arg) }
	case "initializing":
		return aurora.Cyan
	default:
		return aurora.White
	}
}

func (cd *ClaudeDashboard) getTaskStatusIcon(status string) string {
	switch status {
	case "pending":
		return "⏳"
	case "assigned":
		return "📋"
	case "executing":
		return "⚡"
	case "completed":
		return "✅"
	case "failed":
		return "❌"
	default:
		return "❓"
	}
}

func (cd *ClaudeDashboard) getTaskTypeIcon(taskType string) string {
	switch taskType {
	case "file_edit":
		return "✏️"
	case "code_review":
		return "🔍"
	case "testing":
		return "🧪"
	case "research":
		return "📚"
	case "documentation":
		return "📝"
	case "debugging":
		return "🐛"
	default:
		return "📋"
	}
}

// Command handlers.
func (cd *ClaudeDashboard) handleCreateSession(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: create <name> <purpose> [working_dir]")
	}

	name := args[0]
	purpose := args[1]
	workingDir := "."
	if len(args) > 2 {
		workingDir = args[2]
	}

	session, err := cd.sessionManager.CreateSessionWithValidation(ctx, name, purpose, workingDir)
	if err != nil {
		return err
	}

	cd.console.Printf("✅ Created session %s (%s)\n", session.ID, session.Name)
	return nil
}

func (cd *ClaudeDashboard) handleCloseSession(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: close <session_id>")
	}

	err := cd.sessionManager.CloseSession(args[0])
	if err != nil {
		return err
	}

	cd.console.Printf("✅ Closed session %s\n", args[0])
	return nil
}

func (cd *ClaudeDashboard) handleSessionDetail(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: detail <session_id>")
	}

	return cd.ShowSessionDetail(args[0])
}

func (cd *ClaudeDashboard) handleSendCommand(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: send <session_id> <command>")
	}

	sessionID := args[0]
	command := strings.Join(args[1:], " ")

	err := cd.sessionManager.SendCommand(sessionID, command)
	if err != nil {
		return err
	}

	cd.console.Printf("✅ Sent command to session %s\n", sessionID)
	return nil
}

func (cd *ClaudeDashboard) handleCoordinate(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: coordinate <task_description>")
	}

	description := strings.Join(args, " ")
	return cd.coordinator.CoordinateMultiSessionTask(ctx, description)
}
