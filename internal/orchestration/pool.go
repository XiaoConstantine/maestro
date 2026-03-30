package orchestration

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/native"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/search"
	maestrosubagent "github.com/XiaoConstantine/maestro/internal/subagent"
	"github.com/XiaoConstantine/maestro/internal/types"
)

type AgentPool struct {
	reviewAgent types.ReviewAgent
	qaAgent     *QAAgent
	memory      agents.Memory
	githubTools types.GitHubInterface
	config      *ServiceConfig
	logger      *logging.Logger
	qaSessionID string
	qaStore     sessionevent.SessionEventStore
	qaSessions  *maestrosubagent.SessionManager

	mu sync.RWMutex
}

func NewAgentPool(config *ServiceConfig, memory agents.Memory, githubTools types.GitHubInterface, logger *logging.Logger) *AgentPool {
	return &AgentPool{
		config:      config,
		memory:      memory,
		githubTools: githubTools,
		logger:      logger,
	}
}

func (p *AgentPool) GetQAAgent(ctx context.Context) (*QAAgent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.qaAgent != nil {
		return p.qaAgent, nil
	}

	p.qaAgent = NewQAAgent(p.memory, p.logger, p.qaSessions, p.qaStore, p.qaSessionID)
	return p.qaAgent, nil
}

func (p *AgentPool) ConfigureQA(sessionManager *maestrosubagent.SessionManager, sessionStore sessionevent.SessionEventStore, sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.qaSessions = sessionManager
	p.qaStore = sessionStore
	p.qaSessionID = strings.TrimSpace(sessionID)
	if p.qaAgent != nil {
		p.qaAgent.ConfigureSession(sessionManager, sessionStore, p.qaSessionID)
	}
}

func (p *AgentPool) GetReviewAgent(ctx context.Context) (types.ReviewAgent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.reviewAgent != nil {
		return p.reviewAgent, nil
	}

	return nil, fmt.Errorf("review agent not set - call SetReviewAgent first")
}

func (p *AgentPool) SetReviewAgent(agent types.ReviewAgent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reviewAgent = agent
}

func (p *AgentPool) Shutdown(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.reviewAgent != nil {
		p.reviewAgent.Stop(ctx)
		p.reviewAgent.Close()
	}
}

type QAAgent struct {
	memory         agents.Memory
	logger         *logging.Logger
	sessionManager *maestrosubagent.SessionManager
	sessionStore   sessionevent.SessionEventStore
	sessionID      string
	repoPath       string
	nativeAgent    *native.Agent

	mu sync.Mutex
}

func NewQAAgent(memory agents.Memory, logger *logging.Logger, sessionManager *maestrosubagent.SessionManager, sessionStore sessionevent.SessionEventStore, sessionID string) *QAAgent {
	return &QAAgent{
		memory:         memory,
		logger:         logger,
		sessionManager: sessionManager,
		sessionStore:   sessionStore,
		sessionID:      strings.TrimSpace(sessionID),
	}
}

func (a *QAAgent) ConfigureSession(sessionManager *maestrosubagent.SessionManager, sessionStore sessionevent.SessionEventStore, sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	sessionID = strings.TrimSpace(sessionID)
	if a.sessionID != sessionID {
		a.nativeAgent = nil
	}
	a.sessionManager = sessionManager
	a.sessionStore = sessionStore
	a.sessionID = sessionID
}

func (a *QAAgent) Ask(ctx context.Context, question, repoPath, owner, repo string) (string, float64, []string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logger.Debug(ctx, "QAAgent Ask start: session=%q repo=%q question=%q", a.sessionID, repoPath, question)

	if err := a.ensureNativeAgentLocked(repoPath, owner, repo); err != nil {
		a.logger.Debug(ctx, "QAAgent ensureNativeAgentLocked error: %v", err)
		return "", 0, nil, fmt.Errorf("failed to create native QA agent: %w", err)
	}

	answer, confidence, sources, err := a.askWithNativeLocked(ctx, question, owner, repo)
	if err == nil {
		trace := a.nativeAgent.LastNativeTrace()
		var promptTokens, completionTokens, totalTokens int64
		var steps int
		if trace != nil {
			promptTokens = trace.TokenUsage.PromptTokens
			completionTokens = trace.TokenUsage.CompletionTokens
			totalTokens = trace.TokenUsage.TotalTokens
			steps = len(trace.Steps)
		}
		a.logger.Debug(ctx, "QAAgent native ask complete: answer_len=%d confidence=%.2f sources=%d prompt_tokens=%d completion_tokens=%d total_tokens=%d steps=%d", len(answer), confidence, len(sources), promptTokens, completionTokens, totalTokens, steps)
		return answer, confidence, sources, nil
	}
	a.logger.Debug(ctx, "QAAgent native ask error: %v", err)
	if shouldFallbackToLegacyQA(err) {
		a.logger.Warn(ctx, "Native QA failed, falling back to legacy ReAct: %v", err)
		return a.askWithLegacyReAct(ctx, question, repoPath, owner, repo)
	}
	return "", 0, nil, err
}

func (a *QAAgent) askWithNativeLocked(ctx context.Context, question, owner, repo string) (string, float64, []string, error) {
	result, err := a.nativeAgent.Execute(ctx, map[string]interface{}{
		"task": buildNativeQATask(question, owner, repo),
	})
	if err != nil {
		return "", 0, nil, err
	}

	answer := strings.TrimSpace(stringValue(result["final_answer"]))
	if answer == "" {
		if trace := a.nativeAgent.LastNativeTrace(); trace != nil {
			answer = strings.TrimSpace(trace.FinalAnswer)
		}
	}
	if answer == "" {
		if execErr := strings.TrimSpace(stringValue(result["error"])); execErr != "" {
			return "", 0, nil, fmt.Errorf("%s", execErr)
		}
	}
	if answer == "" {
		answer = fmt.Sprintf("I couldn't find relevant information about \"%s\" in this repository.", question)
	}

	trace := a.nativeAgent.LastNativeTrace()
	sources := extractSourcesFromNativeTrace(trace)
	confidence := estimateNativeQAConfidence(trace, sources)

	return answer, confidence, sources, nil
}

func (a *QAAgent) askWithLegacyReAct(ctx context.Context, question, repoPath, owner, repo string) (string, float64, []string, error) {
	searchTool := search.NewSimpleSearchTool(a.logger, repoPath)

	reactAgent, err := createReActAgent("qa-agent-pooled-fallback", searchTool, a.logger)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to create ReAct fallback agent: %w", err)
	}

	searchRequest := &search.SearchRequest{
		Query:         question,
		Context:       fmt.Sprintf("Repository: %s/%s. Answer the user's question by exploring the codebase. For overview questions, start by reading README.md.", owner, repo),
		MaxResults:    10,
		RequiredDepth: 3,
	}

	response, err := reactAgent.ExecuteSearch(ctx, searchRequest)
	if err != nil {
		return "", 0, nil, err
	}

	answer, sources := extractAnswerAndSources(response)
	if answer == "" {
		answer = response.Synthesis
	}
	if answer == "" {
		answer = fmt.Sprintf("I couldn't find relevant information about %q in this repository.", question)
	}

	return answer, response.Confidence, sources, nil
}

func shouldFallbackToLegacyQA(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "statuscode=400") && strings.Contains(text, "gemini-3")
}

func (a *QAAgent) ensureNativeAgentLocked(repoPath, owner, repo string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("repository path is required")
	}

	if a.nativeAgent != nil && a.repoPath == repoPath {
		return nil
	}

	llm := core.GetDefaultLLM()
	nativeAgent, err := native.NewAgent(llm, native.Config{
		MaxTurns:                      12,
		MaxTokens:                     2048,
		Temperature:                   0.1,
		SystemPrompt:                  qaNativeSystemPrompt,
		Memory:                        a.memory,
		SessionID:                     a.sessionID,
		SessionEventStore:             a.sessionStore,
		SessionRecallLimit:            4,
		SessionRecallMaxChars:         1800,
		MaxConsecutiveNoCallResponses: 4,
	})
	if err != nil {
		return err
	}

	for _, tool := range buildNativeQATools(repoPath, owner, repo, a.logger, a.sessionManager, a.sessionStore, a.sessionID) {
		if err := nativeAgent.RegisterTool(tool); err != nil {
			return fmt.Errorf("register %s: %w", tool.Name(), err)
		}
	}

	a.repoPath = repoPath
	a.nativeAgent = nativeAgent
	return nil
}

func extractAnswerAndSources(response *search.SearchResponse) (string, []string) {
	var answer string
	seen := make(map[string]bool)
	var sources []string

	for _, r := range response.Results {
		if r.SearchResult == nil {
			continue
		}
		if strings.HasPrefix(r.FilePath, "phase-") || strings.HasPrefix(r.FilePath, "react-") {
			if r.Line != "" && len(r.Line) > len(answer) {
				answer = r.Line
			}
			continue
		}
		if r.FilePath == "" || seen[r.FilePath] {
			continue
		}
		seen[r.FilePath] = true
		sources = append(sources, r.FilePath)
	}

	return answer, sources
}
