package orchestration

import (
	"context"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/maestro/internal/search"
)

type PooledQAProcessor struct {
	pool *AgentPool
}

func NewPooledQAProcessor(pool *AgentPool) *PooledQAProcessor {
	return &PooledQAProcessor{pool: pool}
}

func (p *PooledQAProcessor) Process(ctx context.Context, task agents.Task, taskContext map[string]interface{}) (interface{}, error) {
	qaAgent, err := p.pool.GetQAAgent(ctx)
	if err != nil {
		return nil, err
	}

	question, _ := task.Metadata["question"].(string)
	repoPath, _ := taskContext["repo_path"].(string)
	owner, _ := taskContext["owner"].(string)
	repo, _ := taskContext["repo"].(string)

	answer, confidence, sources, err := qaAgent.Ask(ctx, question, repoPath, owner, repo)
	if err != nil {
		return nil, err
	}

	return &search.SearchResponse{
		Synthesis:  answer,
		Confidence: confidence,
		Results:    sourcesToResults(sources),
	}, nil
}

func sourcesToResults(sources []string) []*search.EnhancedSearchResult {
	results := make([]*search.EnhancedSearchResult, 0, len(sources))
	for _, src := range sources {
		results = append(results, &search.EnhancedSearchResult{
			SearchResult: &search.SearchResult{FilePath: src},
		})
	}
	return results
}
