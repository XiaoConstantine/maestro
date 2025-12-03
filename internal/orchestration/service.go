package orchestration

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/briandowns/spinner"
	gh "github.com/google/go-github/v68/github"
)

type MemoryType int

const (
	MemoryInMemory MemoryType = iota
	MemorySQLite
)

type RequestType string

const (
	RequestReview RequestType = "review"
	RequestAsk    RequestType = "ask"
)

type ServiceConfig struct {
	MemoryType    MemoryType
	MemoryPath    string
	Owner         string
	Repo          string
	GitHubToken   string
	IndexWorkers  int
	ReviewWorkers int
}

type Request struct {
	Type       RequestType
	PRNumber   int
	Question   string
	Context    map[string]interface{}
	OnProgress func(status string)
}

type Response struct {
	Type     RequestType
	Comments []types.PRReviewComment
	Answer   string
	Metadata map[string]interface{}
}

type MaestroService struct {
	pool        *AgentPool
	memory      agents.Memory
	githubTools types.GitHubInterface
	config      *ServiceConfig
	logger      *logging.Logger

	mu          sync.RWMutex
	initialized bool
}

func NewMaestroService(ctx context.Context, config *ServiceConfig, githubTools types.GitHubInterface) (*MaestroService, error) {
	logger := logging.GetLogger()

	var memory agents.Memory
	switch config.MemoryType {
	case MemorySQLite:
		if config.MemoryPath == "" {
			return nil, fmt.Errorf("memory path required for SQLite")
		}
		// TODO: use SQLite store when available
		memory = agents.NewInMemoryStore()
	default:
		memory = agents.NewInMemoryStore()
	}

	if envType := os.Getenv("MAESTRO_MEMORY_TYPE"); envType == "sqlite" {
		logger.Info(ctx, "Memory type override from environment: sqlite")
	}

	pool := NewAgentPool(config, memory, githubTools, logger)

	return &MaestroService{
		pool:        pool,
		memory:      memory,
		githubTools: githubTools,
		config:      config,
		logger:      logger,
		initialized: true,
	}, nil
}

func (s *MaestroService) ProcessRequest(ctx context.Context, request Request) (*Response, error) {
	switch request.Type {
	case RequestReview:
		return s.handleReview(ctx, request)
	case RequestAsk:
		return s.handleAsk(ctx, request)
	default:
		return nil, fmt.Errorf("unknown request type: %s", request.Type)
	}
}

func (s *MaestroService) handleReview(ctx context.Context, request Request) (*Response, error) {
	agent, err := s.pool.GetReviewAgent(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get review agent: %w", err)
	}

	changes, err := s.githubTools.GetPullRequestChanges(ctx, request.PRNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR changes: %w", err)
	}

	tasks := make([]types.PRReviewTask, 0, len(changes.Files))
	for _, file := range changes.Files {
		tasks = append(tasks, types.PRReviewTask{
			FilePath:    file.FilePath,
			FileContent: file.FileContent,
			Changes:     file.Patch,
		})
	}

	progressConsole := &serviceProgressConsole{onProgress: request.OnProgress}
	comments, err := agent.ReviewPRWithChanges(ctx, request.PRNumber, tasks, progressConsole, changes)
	if err != nil {
		return nil, err
	}

	return &Response{
		Type:     RequestReview,
		Comments: comments,
	}, nil
}

func (s *MaestroService) handleAsk(ctx context.Context, request Request) (*Response, error) {
	agent, err := s.pool.GetQAAgent(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get QA agent: %w", err)
	}

	repoPath := ""
	if reviewAgent, err := s.pool.GetReviewAgent(ctx); err == nil {
		repoPath = reviewAgent.ClonedRepoPath()
	}

	if repoPath == "" {
		return &Response{
			Type:   RequestAsk,
			Answer: "Repository is still being cloned. Please wait a moment and try again.",
		}, nil
	}

	answer, confidence, sources, err := agent.Ask(ctx, request.Question, repoPath, s.config.Owner, s.config.Repo)
	if err != nil {
		return nil, err
	}

	return &Response{
		Type:   RequestAsk,
		Answer: answer,
		Metadata: map[string]interface{}{
			"confidence": confidence,
			"sources":    sources,
		},
	}, nil
}

func (s *MaestroService) IsReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
}

func (s *MaestroService) Shutdown(ctx context.Context) error {
	s.pool.Shutdown(ctx)
	return nil
}

func (s *MaestroService) SetReviewAgent(agent types.ReviewAgent) {
	s.pool.SetReviewAgent(agent)
}

type serviceProgressConsole struct {
	onProgress func(status string)
}

func (c *serviceProgressConsole) StartSpinner(message string) {
	if c.onProgress != nil {
		c.onProgress(message)
	}
}

func (c *serviceProgressConsole) StopSpinner() {}

func (c *serviceProgressConsole) WithSpinner(ctx context.Context, message string, fn func() error) error {
	return fn()
}

func (c *serviceProgressConsole) ShowComments(comments []types.PRReviewComment, metric types.MetricsCollector) {
}

func (c *serviceProgressConsole) ShowReviewMetrics(metrics types.MetricsCollector, comments []types.PRReviewComment) {
}

func (c *serviceProgressConsole) ShowCommentsInteractive(comments []types.PRReviewComment, onPost func([]types.PRReviewComment) error) error {
	return nil
}

func (c *serviceProgressConsole) ShowSummary(comments []types.PRReviewComment, metric types.MetricsCollector) {
}

func (c *serviceProgressConsole) StartReview(pr *gh.PullRequest) {
	if c.onProgress != nil && pr != nil {
		c.onProgress(fmt.Sprintf("Starting review: %s", pr.GetTitle()))
	}
}

func (c *serviceProgressConsole) ReviewingFile(file string, current, total int) {
	if c.onProgress != nil {
		c.onProgress(fmt.Sprintf("Reviewing %s (%d/%d)", file, current, total))
	}
}

func (c *serviceProgressConsole) ConfirmReviewPost(commentCount int) (bool, error) {
	return false, nil
}

func (c *serviceProgressConsole) ReviewComplete() {
	if c.onProgress != nil {
		c.onProgress("Review complete")
	}
}

func (c *serviceProgressConsole) UpdateSpinnerText(text string) {
	if c.onProgress != nil {
		c.onProgress(text)
	}
}

func (c *serviceProgressConsole) CollectAllFeedback(comments []types.PRReviewComment, metric types.MetricsCollector) error {
	return nil
}

func (c *serviceProgressConsole) Confirm(opts types.PromptOptions) (bool, error) {
	return false, nil
}

func (c *serviceProgressConsole) FileError(filepath string, err error) {
	if c.onProgress != nil {
		c.onProgress(fmt.Sprintf("Error in %s: %v", filepath, err))
	}
}

func (c *serviceProgressConsole) Printf(format string, a ...interface{}) {}

func (c *serviceProgressConsole) Println(a ...interface{}) {}

func (c *serviceProgressConsole) PrintHeader(text string) {}

func (c *serviceProgressConsole) NoIssuesFound(file string, chunkNumber, totalChunks int) {}

func (c *serviceProgressConsole) SeverityIcon(severity string) string { return "" }

func (c *serviceProgressConsole) Color() bool { return false }

func (c *serviceProgressConsole) Spinner() *spinner.Spinner { return nil }

func (c *serviceProgressConsole) IsInteractive() bool { return false }
