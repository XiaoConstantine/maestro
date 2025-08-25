package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
		if err := h.cmd.Process.Signal(os.Interrupt); err != nil {
			h.dspyLogger.Debug(context.Background(), "Failed to send interrupt signal, killing process")
			if err := h.cmd.Process.Kill(); err != nil {
				errors = append(errors, fmt.Errorf("failed to kill bash MCP server: %w", err))
			}
		}

		// Wait for the server to exit
		if err := h.cmd.Wait(); err != nil {
			// Exit errors are expected when we kill the process
			h.dspyLogger.Debug(context.Background(), "Bash MCP server exited: %v", err)
		}
	}

	// Return the first error if any
	if len(errors) > 0 {
		return errors[0]
	}
	return nil
}
