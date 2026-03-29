package subagent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	dspysubagent "github.com/XiaoConstantine/dspy-go/pkg/agents/subagent"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	models "github.com/XiaoConstantine/mcp-go/pkg/model"
)

type processorFactory func(sessionDir string) (agents.TaskProcessor, error)

type processorToolConfig struct {
	name            string
	description     string
	parentSessionID string
	staticInput     map[string]any
	logger          *logging.Logger
	sessionManager  *SessionManager
	factory         processorFactory
}

type processorBackedAgent struct {
	logger         *logging.Logger
	sessionManager *SessionManager
	processorType  string
	factory        processorFactory

	mu        sync.RWMutex
	lastTrace *agents.ExecutionTrace
}

func ClaudeAvailable() bool {
	return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != ""
}

func GeminiAvailable() bool {
	return strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != "" || strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != ""
}

func NewClaudeTool(logger *logging.Logger, sessionManager *SessionManager, parentSessionID string, staticInput map[string]any) (core.Tool, error) {
	return newProcessorTool(processorToolConfig{
		name:            "claude",
		description:     "Delegate deeper reasoning, synthesis, and review-style analysis to Claude. Use when repository tools alone are not enough.",
		parentSessionID: parentSessionID,
		staticInput:     staticInput,
		logger:          logger,
		sessionManager:  sessionManager,
		factory: func(sessionDir string) (agents.TaskProcessor, error) {
			return NewClaudeProcessor(logger, sessionDir, "")
		},
	})
}

func NewGeminiTool(logger *logging.Logger, sessionManager *SessionManager, parentSessionID string, staticInput map[string]any) (core.Tool, error) {
	return newProcessorTool(processorToolConfig{
		name:            "gemini",
		description:     "Delegate broader search, brainstorming, or web-oriented requests to Gemini. Use sparingly after local repository inspection.",
		parentSessionID: parentSessionID,
		staticInput:     staticInput,
		logger:          logger,
		sessionManager:  sessionManager,
		factory: func(sessionDir string) (agents.TaskProcessor, error) {
			return NewGeminiProcessor(logger, sessionDir, "")
		},
	})
}

func newProcessorTool(cfg processorToolConfig) (core.Tool, error) {
	if cfg.sessionManager == nil {
		return nil, fmt.Errorf("session manager is required")
	}
	if cfg.factory == nil {
		return nil, fmt.Errorf("processor factory is required")
	}

	return dspysubagent.AsTool(dspysubagent.ToolConfig{
		Name:          cfg.name,
		Description:   cfg.description,
		SessionPolicy: dspysubagent.SessionPolicyDerived,
		StaticParentContext: dspysubagent.ParentContext{
			SessionID:       strings.TrimSpace(cfg.parentSessionID),
			ParentAgentType: "maestro",
			Input:           core.ShallowCopyMap(cfg.staticInput),
		},
		InputSchema: models.InputSchema{
			Type: "object",
			Properties: map[string]models.ParameterSchema{
				"prompt": {
					Type:        "string",
					Description: "Prompt to send to the delegated subagent.",
					Required:    true,
				},
				"task_type": {
					Type:        "string",
					Description: "Optional task type such as review, search, web, or generate.",
				},
			},
		},
		BuildAgent: func(context.Context, map[string]any) (agents.Agent, error) {
			return &processorBackedAgent{
				logger:         cfg.logger,
				sessionManager: cfg.sessionManager,
				processorType:  cfg.name,
				factory:        cfg.factory,
			}, nil
		},
		BuildInput: buildProcessorToolInput,
	})
}

func buildProcessorToolInput(args map[string]any, parent dspysubagent.ParentContext) (map[string]any, error) {
	input := core.ShallowCopyMap(parent.Input)
	if input == nil {
		input = make(map[string]any)
	}
	for key, value := range args {
		input[key] = value
	}
	return input, nil
}

func (a *processorBackedAgent) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	startedAt := time.Now()
	input = core.ShallowCopyMap(input)

	prompt := firstInputString(input, "prompt", "task", "question")
	if prompt == "" {
		err := fmt.Errorf("missing prompt in input")
		a.storeTrace(startedAt, input, nil, err)
		return nil, err
	}

	session, err := a.resolveSession(ctx, input)
	if err != nil {
		a.storeTrace(startedAt, input, nil, err)
		return nil, err
	}

	processor, err := a.factory(session.Dir)
	if err != nil {
		a.storeTrace(startedAt, input, nil, err)
		return nil, err
	}

	taskType := firstInputString(input, "task_type", "type")
	taskID := firstInputString(input, "task_id")
	if taskID == "" {
		taskID = fmt.Sprintf("%s-%d", a.processorType, startedAt.UnixNano())
	}

	task := agents.Task{
		ID:            taskID,
		Type:          a.processorType,
		ProcessorType: a.processorType,
		Metadata: map[string]interface{}{
			"prompt": prompt,
			"type":   taskType,
		},
	}

	taskContext := map[string]interface{}{}
	for _, key := range []string{"repo_path", "owner", "repo", "files"} {
		if value, ok := input[key]; ok {
			taskContext[key] = value
		}
	}

	rawOutput, err := processor.Process(ctx, task, taskContext)
	if err != nil {
		a.storeTrace(startedAt, input, nil, err)
		return nil, err
	}

	output, err := normalizeProcessorOutput(rawOutput)
	if err != nil {
		a.storeTrace(startedAt, input, nil, err)
		return nil, err
	}

	if finalAnswer := firstInputString(output, "final_answer", "answer", "response"); finalAnswer != "" {
		output["final_answer"] = finalAnswer
	}
	if _, ok := output["completed"]; !ok {
		output["completed"] = true
	}
	output["processor_type"] = a.processorType
	output["session_id"] = session.ID
	output["session_dir"] = session.Dir

	a.storeTrace(startedAt, input, output, nil)
	return output, nil
}

func (a *processorBackedAgent) GetCapabilities() []core.Tool {
	return nil
}

func (a *processorBackedAgent) GetMemory() agents.Memory {
	return nil
}

func (a *processorBackedAgent) LastExecutionTrace() *agents.ExecutionTrace {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.lastTrace == nil {
		return nil
	}
	return a.lastTrace.Clone()
}

func (a *processorBackedAgent) resolveSession(ctx context.Context, input map[string]interface{}) (*Session, error) {
	sessionID := strings.TrimSpace(firstInputString(input, "session_id"))
	if sessionID == "" {
		sessionID = fmt.Sprintf("%s-%d", a.processorType, time.Now().UnixNano())
	}

	initialContext := map[string]interface{}{
		"purpose": fmt.Sprintf("Maestro %s subagent", a.processorType),
	}
	for _, key := range []string{"owner", "repo", "repo_path"} {
		if value, ok := input[key]; ok {
			initialContext[key] = value
		}
	}

	return a.sessionManager.GetOrCreateSession(ctx, sessionID, initialContext)
}

func (a *processorBackedAgent) storeTrace(startedAt time.Time, input, output map[string]interface{}, execErr error) {
	trace := &agents.ExecutionTrace{
		AgentID:        fmt.Sprintf("%s-subagent", a.processorType),
		AgentType:      a.processorType,
		Task:           firstInputString(input, "prompt", "task", "question"),
		Input:          core.ShallowCopyMap(input),
		Output:         core.ShallowCopyMap(output),
		StartedAt:      startedAt,
		CompletedAt:    time.Now(),
		ProcessingTime: time.Since(startedAt),
		ContextMetadata: map[string]interface{}{
			"session_id": firstInputString(input, "session_id"),
		},
		TerminationCause: "processor",
	}
	if execErr != nil {
		trace.Status = agents.TraceStatusFailure
		trace.Error = execErr.Error()
	} else {
		trace.Status = agents.TraceStatusSuccess
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastTrace = trace
}

func normalizeProcessorOutput(raw interface{}) (map[string]interface{}, error) {
	if raw == nil {
		return map[string]interface{}{}, nil
	}
	if typed, ok := raw.(map[string]interface{}); ok {
		return core.ShallowCopyMap(typed), nil
	}
	return nil, fmt.Errorf("unexpected processor result type %T", raw)
}

func firstInputString(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok || raw == nil {
			continue
		}
		if text, ok := raw.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}
