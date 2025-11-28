package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	dspyLogging "github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/mcp-go/pkg/client"
	mcpLogging "github.com/XiaoConstantine/mcp-go/pkg/logging"
	models "github.com/XiaoConstantine/mcp-go/pkg/model"
	"github.com/XiaoConstantine/mcp-go/pkg/transport"
)

// MCPBashHelper provides a simple interface to execute bash commands via MCP.
type MCPBashHelper struct {
	mcpClient  *client.Client
	dspyLogger *dspyLogging.Logger
	mcpLogger  mcpLogging.Logger
	cmd        *exec.Cmd
}

// NewMCPBashHelper creates a new MCP bash helper.
func NewMCPBashHelper() (*MCPBashHelper, error) {
	// Start the bash MCP server as a subprocess
	// Use absolute path to the bash-mcp-server executable
	cmd := exec.Command("./bash-mcp-server")

	// Set up pipes for communication
	serverIn, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	serverOut, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Redirect server's stderr to our stderr for debugging
	cmd.Stderr = os.Stderr

	// Start the server in its own process group to prevent it from receiving
	// signals meant for the parent process (like Ctrl+C)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}

	// Start the server
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start bash MCP server: %w", err)
	}

	// Give the server a moment to initialize
	time.Sleep(500 * time.Millisecond)

	// Create loggers
	dspyLogger := dspyLogging.GetLogger()
	mcpLogger := mcpLogging.NewStdLogger(mcpLogging.InfoLevel)

	// Create a StdioTransport
	transport := transport.NewStdioTransport(serverOut, serverIn, mcpLogger)

	// Create the MCP client
	mcpClient := client.NewClient(
		transport,
		client.WithLogger(mcpLogger),
		client.WithClientInfo("maestro-bash-client", "1.0.0"),
	)

	helper := &MCPBashHelper{
		mcpClient:  mcpClient,
		dspyLogger: dspyLogger,
		mcpLogger:  mcpLogger,
		cmd:        cmd,
	}

	// Initialize the connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := mcpClient.Initialize(ctx); err != nil {
		helper.Close()
		return nil, fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	return helper, nil
}

// ExecuteCommand executes a bash command and returns the output.
func (h *MCPBashHelper) ExecuteCommand(ctx context.Context, command string) (string, error) {
	h.dspyLogger.Debug(ctx, "Executing bash command via MCP: %s", command)

	// Prepare arguments for the bash_execute tool
	args := map[string]interface{}{
		"command": command,
		"timeout": 60.0, // 60 second timeout for gh commands
	}

	// Call the bash_execute tool
	result, err := h.mcpClient.CallTool(ctx, "bash_execute", args)
	if err != nil {
		return "", fmt.Errorf("failed to execute bash command: %w", err)
	}

	// Extract text content from the result
	var output string
	for _, content := range result.Content {
		if textContent, ok := content.(models.TextContent); ok {
			output += textContent.Text
		}
	}

	// Check if the command resulted in an error
	if result.IsError {
		return output, fmt.Errorf("command execution failed: %s", output)
	}

	return output, nil
}

// ExecuteGHCommand executes a gh CLI command.
func (h *MCPBashHelper) ExecuteGHCommand(ctx context.Context, args ...string) (string, error) {
	// Build the gh command
	command := "gh"
	for _, arg := range args {
		command += " " + arg
	}

	return h.ExecuteCommand(ctx, command)
}

// ExecuteSgrepCommand executes an sgrep CLI command for semantic code search.
// sgrep provides fast local semantic search using embeddings and tree-sitter chunking.
func (h *MCPBashHelper) ExecuteSgrepCommand(ctx context.Context, args ...string) (string, error) {
	// Build the sgrep command
	command := "sgrep"
	for _, arg := range args {
		command += " " + arg
	}

	return h.ExecuteCommand(ctx, command)
}

// SgrepSearch performs semantic code search using sgrep.
// Returns JSON results with file paths, line numbers, content, and relevance scores.
func (h *MCPBashHelper) SgrepSearch(ctx context.Context, query string, limit int) (string, error) {
	// sgrep "query" -n <limit> --json
	return h.ExecuteSgrepCommand(ctx, fmt.Sprintf("%q", query), "-n", fmt.Sprintf("%d", limit), "--json")
}

// SgrepIndex indexes a directory for semantic search.
func (h *MCPBashHelper) SgrepIndex(ctx context.Context, path string) (string, error) {
	return h.ExecuteSgrepCommand(ctx, "index", path)
}

// Close closes the MCP connection and terminates the server.
func (h *MCPBashHelper) Close() error {
	var errors []error

	// Shutdown the MCP client
	if h.mcpClient != nil {
		if err := h.mcpClient.Shutdown(); err != nil {
			errors = append(errors, fmt.Errorf("error shutting down MCP client: %w", err))
		}
	}

	// Terminate the server subprocess
	if h.cmd != nil && h.cmd.Process != nil {
		h.terminateProcess()
	}

	// Return the first error if any
	if len(errors) > 0 {
		return errors[0]
	}
	return nil
}

// terminateProcess gracefully terminates the bash-mcp-server with timeout fallback to SIGKILL.
func (h *MCPBashHelper) terminateProcess() {
	if h.cmd == nil || h.cmd.Process == nil {
		return
	}

	pgid := h.cmd.Process.Pid

	// Create a channel to signal when Wait() completes
	done := make(chan error, 1)
	go func() {
		done <- h.cmd.Wait()
	}()

	// First try SIGTERM (more graceful than SIGINT for cleanup)
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		h.dspyLogger.Debug(context.Background(), "Failed to send SIGTERM to process group: %v", err)
	}

	// Wait up to 2 seconds for graceful shutdown
	select {
	case <-done:
		h.dspyLogger.Debug(context.Background(), "Bash MCP server terminated gracefully")
		return
	case <-time.After(2 * time.Second):
		h.dspyLogger.Debug(context.Background(), "Graceful shutdown timed out, sending SIGKILL")
	}

	// Force kill if still running
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		h.dspyLogger.Debug(context.Background(), "Failed to SIGKILL process group: %v", err)
		// Also try killing just the process directly
		if h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
		}
	}

	// Wait for final cleanup (with short timeout)
	select {
	case <-done:
		h.dspyLogger.Debug(context.Background(), "Bash MCP server killed")
	case <-time.After(1 * time.Second):
		h.dspyLogger.Debug(context.Background(), "Process cleanup timed out")
	}
}
