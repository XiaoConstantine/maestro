package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// CLIToolType represents the type of CLI tool.
type CLIToolType int

const (
	ClaudeCode CLIToolType = iota
	GeminiCLI
)

// CLIToolConfig holds configuration for CLI tools.
type CLIToolConfig struct {
	Type        CLIToolType
	Name        string
	Command     string
	InstallCmd  string
	CheckCmd    string
	Version     string
	Description string
	RequiresAPI bool
	APIEnvVar   string
}

// CLIToolManager manages external CLI tools.
type CLIToolManager struct {
	tools   map[CLIToolType]*CLIToolConfig
	logger  *logging.Logger
	console ConsoleInterface
}

// NewCLIToolManager creates a new CLI tool manager.
func NewCLIToolManager(logger *logging.Logger, console ConsoleInterface) *CLIToolManager {
	manager := &CLIToolManager{
		tools:   make(map[CLIToolType]*CLIToolConfig),
		logger:  logger,
		console: console,
	}

	// Register available CLI tools
	manager.registerTools()
	return manager
}

// registerTools registers all available CLI tools.
func (m *CLIToolManager) registerTools() {
	// Claude Code CLI
	m.tools[ClaudeCode] = &CLIToolConfig{
		Type:        ClaudeCode,
		Name:        "Claude Code",
		Command:     "claude",
		InstallCmd:  "npm install -g @anthropic-ai/claude-code",
		CheckCmd:    "claude --version",
		Description: "Anthropic's official AI coding assistant CLI",
		RequiresAPI: true,
		APIEnvVar:   "ANTHROPIC_API_KEY",
	}

	// Gemini CLI
	m.tools[GeminiCLI] = &CLIToolConfig{
		Type:        GeminiCLI,
		Name:        "Gemini CLI",
		Command:     "gemini",
		InstallCmd:  "npm install -g @google/gemini-cli",
		CheckCmd:    "gemini --version",
		Description: "Google's open-source AI terminal agent",
		RequiresAPI: true, // Actually does need API key for reliable usage
		APIEnvVar:   "GOOGLE_API_KEY",
	}
}

// IsToolInstalled checks if a CLI tool is installed.
func (m *CLIToolManager) IsToolInstalled(toolType CLIToolType) bool {
	config, exists := m.tools[toolType]
	if !exists {
		return false
	}

	cmd := exec.Command("which", config.Command)
	err := cmd.Run()
	return err == nil
}

// InstallTool installs a CLI tool.
func (m *CLIToolManager) InstallTool(ctx context.Context, toolType CLIToolType) error {
	config, exists := m.tools[toolType]
	if !exists {
		return fmt.Errorf("unknown tool type: %v", toolType)
	}

	m.console.Printf("Installing %s...\n", config.Name)

	// Check if npm is available
	if !m.isNPMAvailable() {
		return fmt.Errorf("npm is required to install %s. Please install Node.js and npm first", config.Name)
	}

	// Execute installation command
	parts := strings.Fields(config.InstallCmd)
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install %s: %w", config.Name, err)
	}

	m.console.Printf("✅ %s installed successfully\n", config.Name)
	return nil
}

// isNPMAvailable checks if npm is available.
func (m *CLIToolManager) isNPMAvailable() bool {
	cmd := exec.Command("which", "npm")
	return cmd.Run() == nil
}

// SetupTool handles setup and configuration for a CLI tool.
func (m *CLIToolManager) SetupTool(ctx context.Context, toolType CLIToolType) error {
	config, exists := m.tools[toolType]
	if !exists {
		return fmt.Errorf("unknown tool type: %v", toolType)
	}

	// Check if tool is installed
	if !m.IsToolInstalled(toolType) {
		var install bool
		prompt := &survey.Confirm{
			Message: fmt.Sprintf("%s is not installed. Would you like to install it now?", config.Name),
			Default: true,
		}
		if err := survey.AskOne(prompt, &install); err != nil {
			return err
		}

		if install {
			if err := m.InstallTool(ctx, toolType); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("tool setup cancelled")
		}
	}

	// Handle API key setup if required
	if config.RequiresAPI && os.Getenv(config.APIEnvVar) == "" {
		if err := m.setupAPIKey(config); err != nil {
			return err
		}
	}

	return nil
}

// setupAPIKey prompts for and sets up API key.
func (m *CLIToolManager) setupAPIKey(config *CLIToolConfig) error {
	var apiKey string
	prompt := &survey.Password{
		Message: fmt.Sprintf("Enter API key for %s (will be stored in %s environment variable):", config.Name, config.APIEnvVar),
		Help:    fmt.Sprintf("This is required for %s to function. You can also set %s environment variable manually.", config.Name, config.APIEnvVar),
	}

	if err := survey.AskOne(prompt, &apiKey); err != nil {
		return err
	}

	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("API key cannot be empty")
	}

	// Set environment variable for current session
	os.Setenv(config.APIEnvVar, apiKey)

	m.console.Printf("✅ API key configured for %s\n", config.Name)
	return nil
}

// ExecuteTool executes a CLI tool with given arguments.
func (m *CLIToolManager) ExecuteTool(ctx context.Context, toolType CLIToolType, args []string) error {
	config, exists := m.tools[toolType]
	if !exists {
		return fmt.Errorf("unknown tool type: %v", toolType)
	}

	// Ensure tool is set up
	if err := m.SetupTool(ctx, toolType); err != nil {
		return err
	}

	// Prepare command
	cmdArgs := append([]string{config.Command}, args...)

	// Create a timeout context for CLI tools (2 minutes max)
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, cmdArgs[0], cmdArgs[1:]...)

	// Set up environment
	cmd.Env = os.Environ()
	cmd.Dir = "." // Use current directory

	// Connect to terminal
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	m.logger.Info(ctx, "Executing %s with args: %v", config.Name, args)

	// Execute command
	if err := cmd.Run(); err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s timed out after 2 minutes - this may indicate authentication issues", config.Name)
		}
		return fmt.Errorf("failed to execute %s: %w", config.Name, err)
	}

	return nil
}

// ExecuteToolQuietly executes a CLI tool with minimal logging for better UX.
func (m *CLIToolManager) ExecuteToolQuietly(ctx context.Context, toolType CLIToolType, args []string) error {
	config, exists := m.tools[toolType]
	if !exists {
		return fmt.Errorf("unknown tool type: %v", toolType)
	}

	// Ensure tool is set up
	if err := m.SetupTool(ctx, toolType); err != nil {
		return err
	}

	// Prepare command
	cmdArgs := append([]string{config.Command}, args...)

	// Create a timeout context for CLI tools (2 minutes max)
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, cmdArgs[0], cmdArgs[1:]...)

	// Set up environment
	cmd.Env = os.Environ()
	cmd.Dir = "." // Use current directory

	// Connect to terminal
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Execute command without verbose logging
	if err := cmd.Run(); err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s timed out after 2 minutes - this may indicate authentication issues", config.Name)
		}
		return fmt.Errorf("failed to execute %s: %w", config.Name, err)
	}

	return nil
}

// ExecuteToolInteractively executes a CLI tool in interactive mode with stdin input.
func (m *CLIToolManager) ExecuteToolInteractively(ctx context.Context, toolType CLIToolType, input string) error {
	config, exists := m.tools[toolType]
	if !exists {
		return fmt.Errorf("unknown tool type: %v", toolType)
	}

	// Ensure tool is set up
	if err := m.SetupTool(ctx, toolType); err != nil {
		return err
	}

	// Create a timeout context for CLI tools (5 minutes max for interactive)
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Start tool (Claude starts in interactive mode by default)
	cmd := exec.CommandContext(timeoutCtx, config.Command)

	// Set up environment
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cmd.Dir = "." // Use current directory

	// Set up pipes for communication
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Connect stdout and stderr to terminal for user to see
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", config.Name, err)
	}

	// Send input to the tool
	go func() {
		defer stdin.Close()

		// Wait a moment for the tool to initialize
		time.Sleep(time.Second)

		// Send the input
		if _, err := stdin.Write([]byte(input + "\n")); err != nil {
			m.logger.Error(ctx, "Failed to send input to %s: %v", config.Name, err)
		}
	}()

	// Wait for the command to complete
	if err := cmd.Wait(); err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s timed out after 5 minutes", config.Name)
		}
		return fmt.Errorf("failed to execute %s: %w", config.Name, err)
	}

	return nil
}

// GetToolInfo returns information about a CLI tool.
func (m *CLIToolManager) GetToolInfo(toolType CLIToolType) (*CLIToolConfig, error) {
	config, exists := m.tools[toolType]
	if !exists {
		return nil, fmt.Errorf("unknown tool type: %v", toolType)
	}
	return config, nil
}

// ListTools returns all available CLI tools.
func (m *CLIToolManager) ListTools() []*CLIToolConfig {
	var tools []*CLIToolConfig
	for _, config := range m.tools {
		tools = append(tools, config)
	}
	return tools
}

// CheckToolStatus checks the status of all CLI tools.
func (m *CLIToolManager) CheckToolStatus(ctx context.Context) error {
	m.console.PrintHeader("CLI Tools Status")

	for _, config := range m.tools {
		installed := m.IsToolInstalled(config.Type)

		status := "❌ Not installed"
		if installed {
			status = "✅ Installed"
		}

		m.console.Printf("%-15s %s - %s\n", config.Name, status, config.Description)
	}

	return nil
}

// InteractiveSetup provides interactive setup for CLI tools.
func (m *CLIToolManager) InteractiveSetup(ctx context.Context) error {
	m.console.PrintHeader("CLI Tools Setup")

	var toolChoices []string
	var toolMap = make(map[string]CLIToolType)

	for _, config := range m.tools {
		choice := fmt.Sprintf("%s - %s", config.Name, config.Description)
		toolChoices = append(toolChoices, choice)
		toolMap[choice] = config.Type
	}

	var selectedTools []string
	prompt := &survey.MultiSelect{
		Message: "Select CLI tools to set up:",
		Options: toolChoices,
		Help:    "Use space to select/deselect, enter to confirm",
	}

	if err := survey.AskOne(prompt, &selectedTools); err != nil {
		return err
	}

	for _, selected := range selectedTools {
		toolType := toolMap[selected]
		config := m.tools[toolType]

		m.console.Printf("\n🔧 Setting up %s...\n", config.Name)
		if err := m.SetupTool(ctx, toolType); err != nil {
			m.console.Printf("❌ Failed to set up %s: %v\n", config.Name, err)
			continue
		}
		m.console.Printf("✅ %s setup complete\n", config.Name)
	}

	return nil
}

// CreateSessionFile creates a session file for tool integration.
func (m *CLIToolManager) CreateSessionFile(toolType CLIToolType, sessionData map[string]interface{}) (string, error) {
	config, exists := m.tools[toolType]
	if !exists {
		return "", fmt.Errorf("unknown tool type: %v", toolType)
	}

	// Create temporary session file
	sessionDir := filepath.Join(os.TempDir(), "maestro-sessions")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return "", err
	}

	sessionFile := filepath.Join(sessionDir, fmt.Sprintf("%s-session-%d.json", strings.ToLower(config.Name), time.Now().Unix()))

	// This would contain session context, current files, etc.
	// For now, just create a placeholder
	file, err := os.Create(sessionFile)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Write basic session info
	fmt.Fprintf(file, `{
	"tool": "%s",
	"timestamp": "%s",
	"context": %v
}`, config.Name, time.Now().Format(time.RFC3339), sessionData)

	return sessionFile, nil
}
