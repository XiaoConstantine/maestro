package orchestration

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/native"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/search"
	maestrosubagent "github.com/XiaoConstantine/maestro/internal/subagent"
	"github.com/XiaoConstantine/maestro/internal/types"
)

type AgentPool struct {
	reviewAgent      types.ReviewAgent
	qaAgent          *QAAgent
	memory           agents.Memory
	githubTools      types.GitHubInterface
	config           *ServiceConfig
	logger           *logging.Logger
	qaSessionID      string
	qaStore          sessionevent.SessionEventStore
	qaSessions       *maestrosubagent.SessionManager
	qaArtifacts      optimize.AgentArtifacts
	qaSkillStore     skills.Store
	qaSkillDomain    string
	qaSkillStorePath string

	mu sync.RWMutex
}

func NewAgentPool(config *ServiceConfig, memory agents.Memory, githubTools types.GitHubInterface, logger *logging.Logger, qaArtifacts optimize.AgentArtifacts, qaSkillStore skills.Store, qaSkillDomain, qaSkillStorePath string) *AgentPool {
	return &AgentPool{
		config:           config,
		memory:           memory,
		githubTools:      githubTools,
		logger:           logger,
		qaArtifacts:      qaArtifacts,
		qaSkillStore:     qaSkillStore,
		qaSkillDomain:    strings.TrimSpace(qaSkillDomain),
		qaSkillStorePath: strings.TrimSpace(qaSkillStorePath),
	}
}

func (p *AgentPool) GetQAAgent(ctx context.Context) (*QAAgent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.qaAgent != nil {
		return p.qaAgent, nil
	}

	p.qaAgent = NewQAAgent(p.memory, p.logger, p.qaSessions, p.qaStore, p.qaSessionID, p.qaArtifacts, p.qaSkillStore, p.qaSkillDomain, p.qaSkillStorePath)
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
	memory             agents.Memory
	logger             *logging.Logger
	sessionManager     *maestrosubagent.SessionManager
	sessionStore       sessionevent.SessionEventStore
	sessionID          string
	repoPath           string
	nativeAgent        *native.Agent
	artifacts          optimize.AgentArtifacts
	skillStore         skills.Store
	skillDomain        string
	skillStorePath     string
	loadedSkillVersion int

	mu sync.RWMutex
}

func NewQAAgent(memory agents.Memory, logger *logging.Logger, sessionManager *maestrosubagent.SessionManager, sessionStore sessionevent.SessionEventStore, sessionID string, artifacts optimize.AgentArtifacts, skillStore skills.Store, skillDomain, skillStorePath string) *QAAgent {
	return &QAAgent{
		memory:         memory,
		logger:         logger,
		sessionManager: sessionManager,
		sessionStore:   sessionStore,
		sessionID:      strings.TrimSpace(sessionID),
		artifacts:      mergeQAArtifactsWithDefaults(artifacts),
		skillStore:     skillStore,
		skillDomain:    strings.TrimSpace(skillDomain),
		skillStorePath: strings.TrimSpace(skillStorePath),
	}
}

func (a *QAAgent) ConfigureSession(sessionManager *maestrosubagent.SessionManager, sessionStore sessionevent.SessionEventStore, sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	sessionID = strings.TrimSpace(sessionID)
	if a.sessionID != sessionID {
		a.nativeAgent = nil
		a.loadedSkillVersion = 0
	}
	a.sessionManager = sessionManager
	a.sessionStore = sessionStore
	a.sessionID = sessionID
}

func (a *QAAgent) Ask(ctx context.Context, question, repoPath, owner, repo string) (string, float64, []string, error) {
	answer, confidence, sources, _, err := a.askWithTrace(ctx, question, repoPath, owner, repo)
	return answer, confidence, sources, err
}

func (a *QAAgent) askWithTrace(ctx context.Context, question, repoPath, owner, repo string) (string, float64, []string, *agents.ExecutionTrace, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logger.Debug(ctx, "QAAgent Ask start: session=%q repo=%q question=%q", a.sessionID, repoPath, question)

	if err := a.ensureNativeAgentLocked(ctx, repoPath, owner, repo); err != nil {
		a.logger.Debug(ctx, "QAAgent ensureNativeAgentLocked error: %v", err)
		return "", 0, nil, nil, fmt.Errorf("failed to create native QA agent: %w", err)
	}

	answer, confidence, sources, trace, err := a.askWithNativeLocked(ctx, question, owner, repo)
	if err == nil {
		var promptTokens, completionTokens, totalTokens int64
		var steps int
		if trace != nil {
			promptTokens = trace.TokenUsage["prompt_tokens"]
			completionTokens = trace.TokenUsage["completion_tokens"]
			totalTokens = trace.TokenUsage["total_tokens"]
			steps = len(trace.Steps)
		}
		a.logger.Debug(ctx, "QAAgent native ask complete: answer_len=%d confidence=%.2f sources=%d prompt_tokens=%d completion_tokens=%d total_tokens=%d steps=%d", len(answer), confidence, len(sources), promptTokens, completionTokens, totalTokens, steps)
		return answer, confidence, sources, trace, nil
	}
	a.logger.Debug(ctx, "QAAgent native ask error: %v", err)
	if shouldFallbackToLegacyQA(err) {
		a.logger.Warn(ctx, "Native QA failed, falling back to legacy ReAct: %v", err)
		answer, confidence, sources, fallbackErr := a.askWithLegacyReAct(ctx, question, repoPath, owner, repo)
		return answer, confidence, sources, trace, fallbackErr
	}
	return "", 0, nil, trace, err
}

func (a *QAAgent) SkillState() (string, int) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.skillDomain, a.loadedSkillVersion
}

func (a *QAAgent) askWithNativeLocked(ctx context.Context, question, owner, repo string) (string, float64, []string, *agents.ExecutionTrace, error) {
	execution, err := a.nativeAgent.ExecuteWithTrace(ctx, map[string]interface{}{
		"task": buildNativeQATask(question, owner, repo),
	})
	trace := execution.Trace
	if err != nil {
		return "", 0, nil, trace, err
	}

	result := execution.Output
	answer := strings.TrimSpace(stringValue(result["final_answer"]))
	if answer == "" && trace != nil {
		answer = strings.TrimSpace(stringValue(trace.Output["final_answer"]))
	}
	if answer == "" {
		if execErr := strings.TrimSpace(stringValue(result["error"])); execErr != "" {
			return "", 0, nil, trace, fmt.Errorf("%s", execErr)
		}
	}
	if answer == "" {
		answer = fmt.Sprintf("I couldn't find relevant information about \"%s\" in this repository.", question)
	}

	sources := extractSourcesFromExecutionTrace(trace)
	confidence := estimateNativeQAConfidence(trace, sources)

	return answer, confidence, sources, trace, nil
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

func (a *QAAgent) ensureNativeAgentLocked(ctx context.Context, repoPath, owner, repo string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("repository path is required")
	}

	desiredSkillVersion, skillVersionErr := bestPersistedSkillVersion(ctx, a.skillStore, a.skillDomain)
	if skillVersionErr != nil {
		a.logger.Warn(ctx, "Failed to check QA persisted skill version for domain %q: %v", a.skillDomain, skillVersionErr)
	}

	if a.nativeAgent != nil && a.repoPath == repoPath {
		if skillVersionErr != nil || desiredSkillVersion == a.loadedSkillVersion {
			return nil
		}
		a.logger.Info(ctx, "Reloading QA native agent for updated persisted skill domain=%q old_version=%d new_version=%d", a.skillDomain, a.loadedSkillVersion, desiredSkillVersion)
	}

	llm := core.GetDefaultLLM()
	nativeAgent, err := native.NewAgent(llm, buildNativeQAConfig(a.artifacts, a.memory, a.sessionID, a.sessionStore, a.skillStore, a.skillDomain))
	if err != nil {
		return err
	}
	if skillErr := nativeAgent.GetSkillLoadError(); skillErr != nil {
		a.logger.Warn(ctx, "QA native agent skill load error for domain %q: %v", a.skillDomain, skillErr)
	}
	if loadedSkill := nativeAgent.GetLoadedSkill(); loadedSkill != nil {
		a.loadedSkillVersion = loadedSkill.Version
		a.logger.Info(ctx, "QA native agent loaded persisted skill overlay domain=%q version=%d name=%q store=%q over base prompt", a.skillDomain, loadedSkill.Version, loadedSkill.Name, a.skillStorePath)
	} else {
		a.loadedSkillVersion = 0
		if a.skillStore != nil && a.skillDomain != "" {
			a.logger.Debug(ctx, "QA native agent using base prompt only; no persisted skill found for domain=%q store=%q", a.skillDomain, a.skillStorePath)
		}
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

func bestPersistedSkillVersion(ctx context.Context, store skills.Store, domain string) (int, error) {
	if store == nil || strings.TrimSpace(domain) == "" {
		return 0, nil
	}
	skill, err := store.Best(ctx, strings.TrimSpace(domain))
	if err != nil {
		return 0, err
	}
	if skill == nil {
		return 0, nil
	}
	return skill.Version, nil
}
