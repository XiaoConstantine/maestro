package coding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/native"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/tools/defaults"
)

var (
	ErrRunActive     = errors.New("coding session already has an active run")
	ErrSessionClosed = errors.New("coding session is closed")
)

type Config struct {
	LLM          core.LLM
	Workspace    string
	SessionID    string
	SessionStore sessionevent.SessionEventStore
	SystemPrompt string
	MaxTurns     int
	AllowBash    bool
	ExtraTools   []core.Tool
}

type Session struct {
	workspace string
	agent     *native.Agent
	forwarder *eventForwarder

	runMu   sync.Mutex
	mu      sync.Mutex
	stop    context.CancelFunc
	runDone chan struct{}
	closed  bool
}

func NewSession(cfg Config) (*Session, error) {
	if cfg.LLM == nil {
		return nil, fmt.Errorf("llm is required")
	}
	if strings.TrimSpace(cfg.Workspace) == "" {
		return nil, fmt.Errorf("workspace is required")
	}

	toolset, err := defaults.NewToolset(defaults.Config{Root: cfg.Workspace})
	if err != nil {
		return nil, fmt.Errorf("create coding tools: %w", err)
	}
	forwarder := &eventForwarder{}
	agent, err := native.NewAgent(cfg.LLM, native.Config{
		MaxTurns:          cfg.MaxTurns,
		SystemPrompt:      cfg.SystemPrompt,
		SessionID:         strings.TrimSpace(cfg.SessionID),
		SessionEventStore: cfg.SessionStore,
		EventSink:         forwarder,
	})
	if err != nil {
		return nil, fmt.Errorf("create coding agent: %w", err)
	}
	for _, tool := range toolset.Tools() {
		if tool.Name() == "bash" && !cfg.AllowBash {
			continue
		}
		if err := agent.RegisterTool(tool); err != nil {
			return nil, fmt.Errorf("register coding tool %s: %w", tool.Name(), err)
		}
	}
	for _, tool := range cfg.ExtraTools {
		if tool == nil {
			continue
		}
		if err := agent.RegisterTool(tool); err != nil {
			return nil, fmt.Errorf("register extra coding tool %s: %w", tool.Name(), err)
		}
	}

	return &Session{workspace: toolset.Root(), agent: agent, forwarder: forwarder}, nil
}

func (s *Session) Workspace() string {
	if s == nil {
		return ""
	}
	return s.workspace
}

func (s *Session) Prompt(ctx context.Context, prompt string, sink agents.EventSink) (agents.AgentExecutionResult, error) {
	if s == nil || s.agent == nil {
		return agents.AgentExecutionResult{}, fmt.Errorf("coding session is not initialized")
	}
	if strings.TrimSpace(prompt) == "" {
		return agents.AgentExecutionResult{}, fmt.Errorf("prompt is required")
	}
	if !s.runMu.TryLock() {
		return agents.AgentExecutionResult{}, ErrRunActive
	}
	defer s.runMu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return agents.AgentExecutionResult{}, ErrSessionClosed
	}
	done := make(chan struct{})
	s.stop = cancel
	s.runDone = done
	s.mu.Unlock()
	s.forwarder.Set(sink)
	defer func() {
		s.forwarder.Set(nil)
		cancel()
		s.mu.Lock()
		s.stop = nil
		s.runDone = nil
		close(done)
		s.mu.Unlock()
	}()

	return s.agent.ExecuteWithTrace(runCtx, map[string]any{"task": prompt})
}

func (s *Session) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	cancel := s.stop
	done := s.runDone
	if cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) Cancel() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop == nil {
		return false
	}
	s.stop()
	return true
}

type eventForwarder struct {
	mu   sync.RWMutex
	sink agents.EventSink
}

func (f *eventForwarder) Set(sink agents.EventSink) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sink = sink
}

func (f *eventForwarder) EmitEvent(ctx context.Context, event agents.ExecutionEvent) {
	f.mu.RLock()
	sink := f.sink
	f.mu.RUnlock()
	if sink != nil {
		sink.EmitEvent(ctx, event)
	}
}
