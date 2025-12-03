package orchestration

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/search"
	"github.com/XiaoConstantine/maestro/internal/types"
)

type AgentPool struct {
	reviewAgent types.ReviewAgent
	qaAgent     *QAAgent
	memory      agents.Memory
	githubTools types.GitHubInterface
	config      *ServiceConfig
	logger      *logging.Logger

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

	p.qaAgent = NewQAAgent(p.memory, p.logger)
	return p.qaAgent, nil
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
	memory agents.Memory
	logger *logging.Logger
}

func NewQAAgent(memory agents.Memory, logger *logging.Logger) *QAAgent {
	return &QAAgent{
		memory: memory,
		logger: logger,
	}
}

func (a *QAAgent) Ask(ctx context.Context, question, repoPath, owner, repo string) (string, float64, []string, error) {
	searchTool := search.NewSimpleSearchTool(a.logger, repoPath)

	reactAgent, err := createReActAgent("qa-agent-pooled", searchTool, a.logger)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to create ReAct agent: %w", err)
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
		answer = fmt.Sprintf("I couldn't find relevant information about \"%s\" in this repository.", question)
	}

	return answer, response.Confidence, sources, nil
}

func extractAnswerAndSources(response *search.SearchResponse) (string, []string) {
	var answer string
	seen := make(map[string]bool)
	var sources []string

	for _, r := range response.Results {
		if r.SearchResult == nil {
			continue
		}
		// Phase outputs contain the agent's synthesized answer
		if strings.HasPrefix(r.FilePath, "phase-") || strings.HasPrefix(r.FilePath, "react-") {
			if r.Line != "" && len(r.Line) > len(answer) {
				answer = r.Line
			}
		} else if r.FilePath != "" && !seen[r.FilePath] {
			seen[r.FilePath] = true
			sources = append(sources, r.FilePath)
		}
	}

	return answer, sources
}
