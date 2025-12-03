package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// ClaudeProcessor implements agents.TaskProcessor using Claude Opus 4 via dspy-go.
type ClaudeProcessor struct {
	logger      *logging.Logger
	llm         core.LLM
	sessionDir  string
	contextFile string
}

// NewClaudeProcessor creates a new Claude processor using the Anthropic SDK.
func NewClaudeProcessor(logger *logging.Logger, sessionDir string, apiKey string) (*ClaudeProcessor, error) {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	llm, err := llms.NewAnthropicLLM(apiKey, "claude-sonnet-4-5-20250929")
	if err != nil {
		return nil, fmt.Errorf("failed to create Claude LLM: %w", err)
	}

	return &ClaudeProcessor{
		logger:      logger,
		llm:         llm,
		sessionDir:  sessionDir,
		contextFile: filepath.Join(sessionDir, "context.md"),
	}, nil
}

// Process implements agents.TaskProcessor.
func (p *ClaudeProcessor) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	prompt, _ := task.Metadata["prompt"].(string)
	if prompt == "" {
		return nil, fmt.Errorf("missing prompt in task metadata")
	}

	// Build context from task context and session files
	fullPrompt := p.buildPrompt(prompt, taskContext)

	// Write prompt to input file for debugging/logging
	inputFile := filepath.Join(p.sessionDir, "input.md")
	if err := os.WriteFile(inputFile, []byte(fullPrompt), 0644); err != nil {
		p.logger.Warn(ctx, "Failed to write input file: %v", err)
	}

	// Execute via dspy-go LLM
	response, err := p.execute(ctx, fullPrompt)
	if err != nil {
		return nil, err
	}

	// Write response to output file
	outputFile := filepath.Join(p.sessionDir, "output.md")
	if err := os.WriteFile(outputFile, []byte(response), 0644); err != nil {
		p.logger.Warn(ctx, "Failed to write output file: %v", err)
	}

	// Update shared context with this interaction
	p.updateContext(ctx, prompt, response)

	return map[string]interface{}{
		"response":    response,
		"input_file":  inputFile,
		"output_file": outputFile,
		"model":       "claude-sonnet-4.5",
	}, nil
}

func (p *ClaudeProcessor) buildPrompt(prompt string, taskContext map[string]interface{}) string {
	var sb strings.Builder

	// Add shared context if exists
	if contextData, err := os.ReadFile(p.contextFile); err == nil && len(contextData) > 0 {
		sb.WriteString("## Shared Context\n\n")
		sb.Write(contextData)
		sb.WriteString("\n\n---\n\n")
	}

	// Add task context
	if repoPath, ok := taskContext["repo_path"].(string); ok {
		sb.WriteString(fmt.Sprintf("Repository: %s\n", repoPath))
	}
	if owner, ok := taskContext["owner"].(string); ok {
		if repo, ok := taskContext["repo"].(string); ok {
			sb.WriteString(fmt.Sprintf("GitHub: %s/%s\n", owner, repo))
		}
	}

	// Add any files from context
	if files, ok := taskContext["files"].([]string); ok && len(files) > 0 {
		sb.WriteString("\n## Relevant Files\n")
		for _, f := range files {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}

	sb.WriteString("\n## Request\n\n")
	sb.WriteString(prompt)

	return sb.String()
}

func (p *ClaudeProcessor) execute(ctx context.Context, prompt string) (string, error) {
	response, err := p.llm.Generate(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("claude generation failed: %w", err)
	}

	return strings.TrimSpace(response.Content), nil
}

func (p *ClaudeProcessor) updateContext(ctx context.Context, prompt, response string) {
	f, err := os.OpenFile(p.contextFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		p.logger.Warn(ctx, "Failed to update context: %v", err)
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	entry := fmt.Sprintf("\n## [%s] Claude Interaction\n\n**Prompt:** %s\n\n**Response:** %s\n\n---\n",
		timestamp, truncate(prompt, 200), truncate(response, 500))

	_, _ = f.WriteString(entry)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
