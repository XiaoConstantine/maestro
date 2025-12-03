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

// GeminiProcessor implements agents.TaskProcessor using Gemini Pro via dspy-go.
type GeminiProcessor struct {
	logger      *logging.Logger
	llm         core.LLM
	sessionDir  string
	contextFile string
}

// NewGeminiProcessor creates a new Gemini processor using the Google AI SDK.
func NewGeminiProcessor(logger *logging.Logger, sessionDir string, apiKey string) (*GeminiProcessor, error) {
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("GOOGLE_API_KEY or GEMINI_API_KEY not set")
	}

	llm, err := llms.NewGeminiLLM(apiKey, core.ModelGoogleGemini3ProPreview)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini LLM: %w", err)
	}

	return &GeminiProcessor{
		logger:      logger,
		llm:         llm,
		sessionDir:  sessionDir,
		contextFile: filepath.Join(sessionDir, "context.md"),
	}, nil
}

// Process implements agents.TaskProcessor.
func (p *GeminiProcessor) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	prompt, _ := task.Metadata["prompt"].(string)
	if prompt == "" {
		return nil, fmt.Errorf("missing prompt in task metadata")
	}

	// Determine task type - Gemini is good for web search
	taskType, _ := task.Metadata["type"].(string)

	var response string
	var err error

	switch taskType {
	case "search", "web":
		response, err = p.executeSearch(ctx, prompt, taskContext)
	default:
		response, err = p.execute(ctx, prompt, taskContext)
	}

	if err != nil {
		return nil, err
	}

	// Write response to output file
	outputFile := filepath.Join(p.sessionDir, "gemini-output.md")
	if err := os.WriteFile(outputFile, []byte(response), 0644); err != nil {
		p.logger.Warn(ctx, "Failed to write output file: %v", err)
	}

	// Update shared context
	p.updateContext(ctx, prompt, response)

	return map[string]interface{}{
		"response":    response,
		"output_file": outputFile,
		"task_type":   taskType,
		"model":       "gemini-3-pro-preview",
	}, nil
}

func (p *GeminiProcessor) execute(ctx context.Context, prompt string, taskContext map[string]interface{}) (string, error) {
	fullPrompt := p.buildPrompt(prompt, taskContext)

	response, err := p.llm.Generate(ctx, fullPrompt)
	if err != nil {
		return "", fmt.Errorf("gemini generation failed: %w", err)
	}

	return strings.TrimSpace(response.Content), nil
}

func (p *GeminiProcessor) executeSearch(ctx context.Context, query string, taskContext map[string]interface{}) (string, error) {
	// Gemini with grounding for web search
	searchPrompt := fmt.Sprintf("Search the web and provide current information about: %s", query)
	fullPrompt := p.buildPrompt(searchPrompt, taskContext)

	response, err := p.llm.Generate(ctx, fullPrompt)
	if err != nil {
		return "", fmt.Errorf("gemini search failed: %w", err)
	}

	return strings.TrimSpace(response.Content), nil
}

func (p *GeminiProcessor) buildPrompt(prompt string, taskContext map[string]interface{}) string {
	var sb strings.Builder

	// Add shared context if exists
	if contextData, err := os.ReadFile(p.contextFile); err == nil && len(contextData) > 0 {
		sb.WriteString("Context:\n")
		sb.Write(contextData)
		sb.WriteString("\n\n")
	}

	// Add repo context
	if repoPath, ok := taskContext["repo_path"].(string); ok {
		sb.WriteString(fmt.Sprintf("Repository: %s\n", repoPath))
	}

	sb.WriteString("\nRequest: ")
	sb.WriteString(prompt)

	return sb.String()
}

func (p *GeminiProcessor) updateContext(ctx context.Context, prompt, response string) {
	f, err := os.OpenFile(p.contextFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		p.logger.Warn(ctx, "Failed to update context: %v", err)
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	entry := fmt.Sprintf("\n## [%s] Gemini Interaction\n\n**Query:** %s\n\n**Response:** %s\n\n---\n",
		timestamp, truncate(prompt, 200), truncate(response, 500))

	_, _ = f.WriteString(entry)
}
