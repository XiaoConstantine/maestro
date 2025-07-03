package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/logrusorgru/aurora"
)

// SessionSwitcher manages switching between Claude sessions in interactive mode.
type SessionSwitcher struct {
	sessionManager *ClaudeSessionManager
	dashboard      *ClaudeDashboard
	console        ConsoleInterface
	currentSession *ClaudeSession
	promptMode     bool
}

// NewSessionSwitcher creates a new session switcher.
func NewSessionSwitcher(sessionManager *ClaudeSessionManager, dashboard *ClaudeDashboard, console ConsoleInterface) *SessionSwitcher {
	return &SessionSwitcher{
		sessionManager: sessionManager,
		dashboard:      dashboard,
		console:        console,
		currentSession: nil,
		promptMode:     false,
	}
}

// SwitchToSession switches to a specific session by ID or name.
func (ss *SessionSwitcher) SwitchToSession(sessionIdentifier string) error {
	// Try to find session by ID first
	session, err := ss.sessionManager.GetSession(sessionIdentifier)
	if err != nil {
		// Try to find by name
		session = ss.findSessionByName(sessionIdentifier)
		if session == nil {
			return fmt.Errorf("session not found: %s", sessionIdentifier)
		}
	}

	ss.currentSession = session
	ss.console.Printf("🔄 Switched to session: %s (%s)\n",
		ss.getSessionDisplayName(session), session.Purpose)

	return nil
}

// EnterInteractiveMode enters interactive mode with the current session.
func (ss *SessionSwitcher) EnterInteractiveMode(ctx context.Context) error {
	if ss.currentSession == nil {
		return fmt.Errorf("no session selected. Use /switch <session> first")
	}

	ss.console.Printf("🎯 Entering interactive mode with %s\n", ss.getSessionDisplayName(ss.currentSession))
	ss.console.Printf("Connecting to Claude process...\n")
	ss.console.Printf("─────────────────────────────────────────────────\n")

	// Directly connect to the Claude process using os.Exec
	// This delegates the entire interactive session to Claude
	return ss.delegateToClaudeProcess(ctx)
}






// getSessionDisplayName returns a display name for the session.
func (ss *SessionSwitcher) getSessionDisplayName(session *ClaudeSession) string {
	if ss.console.Color() {
		return aurora.Cyan(session.Name).Bold().String()
	}
	return session.Name
}



// findSessionByName finds a session by name.
func (ss *SessionSwitcher) findSessionByName(name string) *ClaudeSession {
	sessions := ss.sessionManager.ListSessions()
	for _, session := range sessions {
		if session.Name == name {
			return session
		}
	}
	return nil
}

// GetCurrentSession returns the currently selected session.
func (ss *SessionSwitcher) GetCurrentSession() *ClaudeSession {
	return ss.currentSession
}

// ListAvailableSessions shows all available sessions for switching.
func (ss *SessionSwitcher) ListAvailableSessions() {
	sessions := ss.sessionManager.ListSessions()
	if len(sessions) == 0 {
		ss.console.Printf("No Claude sessions available. Create one with: /sessions create <name> <purpose>\n")
		return
	}

	ss.console.Printf("📋 Available Sessions:\n")
	ss.console.Printf("─────────────────────────────────────────────────\n")

	for i, session := range sessions {
		marker := "  "
		if ss.currentSession != nil && session.ID == ss.currentSession.ID {
			marker = "▶ " // Current session marker
		}

		status := session.Status.String()
		statusIcon := ss.getStatusIcon(status)

		if ss.console.Color() {
			ss.console.Printf("%s%d. %s %s %s - %s\n",
				marker,
				i+1,
				statusIcon,
				aurora.Cyan(session.Name).Bold(),
				aurora.Gray(12, fmt.Sprintf("(%s)", session.ID[:8])),
				session.Purpose)
		} else {
			ss.console.Printf("%s%d. %s %s (%s) - %s\n",
				marker, i+1, statusIcon, session.Name, session.ID[:8], session.Purpose)
		}
	}

	ss.console.Printf("─────────────────────────────────────────────────\n")
	ss.console.Printf("💡 Use: /switch <name_or_number> to switch sessions\n")
	ss.console.Printf("💡 Use: /enter to start interactive mode with current session\n")
}

// SwitchByNumber switches to a session by its number in the list.
func (ss *SessionSwitcher) SwitchByNumber(number int) error {
	sessions := ss.sessionManager.ListSessions()
	if number < 1 || number > len(sessions) {
		return fmt.Errorf("invalid session number. Use /sessions to see available sessions")
	}

	session := sessions[number-1]
	ss.currentSession = session
	ss.console.Printf("🔄 Switched to session: %s (%s)\n",
		ss.getSessionDisplayName(session), session.Purpose)

	return nil
}

// QuickSwitch provides a quick way to switch and enter interactive mode.
func (ss *SessionSwitcher) QuickSwitch(ctx context.Context, identifier string) error {
	err := ss.SwitchToSession(identifier)
	if err != nil {
		return err
	}

	return ss.EnterInteractiveMode(ctx)
}

// Helper methods.
func (ss *SessionSwitcher) getStatusIcon(status string) string {
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



// delegateToClaudeProcess directly delegates to a Claude process with resume capability.
func (ss *SessionSwitcher) delegateToClaudeProcess(ctx context.Context) error {
	var cmd *exec.Cmd

	// Use the session's intended working directory, not a separate session directory
	workingDir := ss.currentSession.WorkingDir
	if workingDir == "" {
		workingDir = "."
	}

	// For now, just start fresh Claude sessions each time
	// TODO: Implement proper session persistence later
	ss.console.Printf("🚀 Starting Claude in %s\n", workingDir)
	cmd = exec.CommandContext(ctx, "claude")

	// Set working directory to the intended project directory
	cmd.Dir = workingDir

	// Set up environment for proper TTY
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	// Connect directly to current terminal
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run the interactive Claude session
	err := cmd.Run()

	// Mark session as having been used
	ss.currentSession.HasActiveSession = true
	ss.currentSession.LastUsed = time.Now()

	// TODO: Extract Claude session ID from output for future resumes
	// This would require parsing Claude's output to get the session ID

	// When Claude exits, show return message
	ss.console.Printf("─────────────────────────────────────────────────\n")
	ss.console.Printf("🔙 Returned to Maestro\n")

	return err
}

