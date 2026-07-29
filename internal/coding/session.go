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

// Executor is the execution boundary used by a coding Session. Each Session
// exclusively owns one Executor. Close must release executor-specific resources.
type Executor interface {
	ExecuteWithTrace(context.Context, map[string]any, agents.EventSink) (agents.AgentExecutionResult, error)
	Close(context.Context) error
}

// ExecutorFactory constructs an executor bound to the canonical workspace root
// supplied by Session.
type ExecutorFactory func(workspace string) (Executor, error)

type Session struct {
	workspace string
	executor  Executor

	runMu     sync.Mutex
	mu        sync.Mutex
	stop      context.CancelFunc
	runDone   chan struct{}
	closed    bool
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
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

	return &Session{
		workspace: toolset.Root(),
		executor: &nativeExecutor{
			agent:     agent,
			forwarder: forwarder,
		},
		closeDone: make(chan struct{}),
	}, nil
}

// NewSessionWithExecutor creates a coding session around an alternate execution
// implementation. The factory receives Session's canonical workspace root and
// must return a new executor for the exclusive ownership of this Session.
func NewSessionWithExecutor(workspace string, factory ExecutorFactory) (*Session, error) {
	if factory == nil {
		return nil, fmt.Errorf("coding executor factory is required")
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("workspace is required")
	}
	toolset, err := defaults.NewToolset(defaults.Config{Root: workspace})
	if err != nil {
		return nil, fmt.Errorf("resolve coding workspace: %w", err)
	}
	executor, err := factory(toolset.Root())
	if err != nil {
		return nil, fmt.Errorf("create coding executor: %w", err)
	}
	if executor == nil {
		return nil, fmt.Errorf("coding executor factory returned nil")
	}
	return &Session{
		workspace: toolset.Root(),
		executor:  executor,
		closeDone: make(chan struct{}),
	}, nil
}

func (s *Session) Workspace() string {
	if s == nil {
		return ""
	}
	return s.workspace
}

func (s *Session) Prompt(ctx context.Context, prompt string, sink agents.EventSink) (agents.AgentExecutionResult, error) {
	if s == nil || s.executor == nil {
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
	defer func() {
		cancel()
		s.mu.Lock()
		s.stop = nil
		s.runDone = nil
		close(done)
		s.mu.Unlock()
	}()

	return s.executor.ExecuteWithTrace(runCtx, map[string]any{"task": prompt}, sink)
}

func (s *Session) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	cancel := s.stop
	done := s.runDone
	executor := s.executor
	if cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	if executor == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		go func() {
			if done != nil {
				<-done
			}
			s.closeErr = executor.Close(context.Background())
			close(s.closeDone)
		}()
	})
	select {
	case <-s.closeDone:
		return s.closeErr
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

type nativeExecutor struct {
	agent     *native.Agent
	forwarder *eventForwarder
}

func (e *nativeExecutor) ExecuteWithTrace(
	ctx context.Context,
	input map[string]any,
	sink agents.EventSink,
) (agents.AgentExecutionResult, error) {
	if e == nil || e.agent == nil || e.forwarder == nil {
		return agents.AgentExecutionResult{}, fmt.Errorf("native coding executor is not initialized")
	}
	e.forwarder.Set(sink)
	defer e.forwarder.Set(nil)
	return e.agent.ExecuteWithTrace(ctx, input)
}

func (e *nativeExecutor) Close(context.Context) error {
	return nil
}

var _ Executor = (*nativeExecutor)(nil)

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
