package orchestration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/subagent"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/briandowns/spinner"
	gh "github.com/google/go-github/v68/github"
)

// generateSessionName creates a unique session name based on datetime, pwd, and random bits.
func generateSessionName() string {
	// Get current datetime
	now := time.Now().Format("20060102-150405")

	// Get current working directory basename
	pwd, err := os.Getwd()
	if err != nil {
		pwd = "unknown"
	} else {
		pwd = filepath.Base(pwd)
	}

	// Generate random bits
	randomBytes := make([]byte, 4)
	if _, err := rand.Read(randomBytes); err != nil {
		randomBytes = []byte{0, 0, 0, 0}
	}
	randomHex := hex.EncodeToString(randomBytes)

	return fmt.Sprintf("%s-%s-%s", now, pwd, randomHex)
}

type MemoryType int

const (
	MemoryInMemory MemoryType = iota
	MemorySQLite
)

type RequestType string

const (
	RequestReview RequestType = "review"
	RequestAsk    RequestType = "ask"
	RequestClaude RequestType = "claude"
	RequestGemini RequestType = "gemini"
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
	Prompt     string // For Claude/Gemini requests
	TaskType   string // e.g., "search", "generate", "review"
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
	pool           *AgentPool
	memory         agents.Memory
	githubTools    types.GitHubInterface
	config         *ServiceConfig
	logger         *logging.Logger
	sessionManager *subagent.SessionManager
	claudeProc     *subagent.ClaudeProcessor
	geminiProc     *subagent.GeminiProcessor
	currentSession string

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

	// Setup session directory for subagent context sharing
	// MemoryPath is typically a .db file, so use its parent directory
	var sessionDir string
	if config.MemoryPath != "" {
		sessionDir = filepath.Join(filepath.Dir(config.MemoryPath), "sessions")
	} else {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			homeDir = os.TempDir()
		}
		sessionDir = filepath.Join(homeDir, ".maestro", "sessions")
	}

	sessionManager, err := subagent.NewSessionManager(sessionDir, logger)
	if err != nil {
		logger.Warn(ctx, "Failed to create session manager: %v", err)
	}

	// Create session for subagents with unique name
	var claudeProc *subagent.ClaudeProcessor
	var geminiProc *subagent.GeminiProcessor
	sessionName := generateSessionName()
	if sessionManager != nil {
		defaultSession, err := sessionManager.GetOrCreateSession(ctx, sessionName, map[string]interface{}{
			"owner":   config.Owner,
			"repo":    config.Repo,
			"purpose": "Maestro CLI subagent communication",
		})
		if err == nil {
			// Initialize Claude processor (uses ANTHROPIC_API_KEY env var)
			claudeProc, err = subagent.NewClaudeProcessor(logger, defaultSession.Dir, "")
			if err != nil {
				logger.Info(ctx, "Claude subagent not available: %v", err)
			}

			// Initialize Gemini processor (uses GOOGLE_API_KEY or GEMINI_API_KEY env var)
			geminiProc, err = subagent.NewGeminiProcessor(logger, defaultSession.Dir, "")
			if err != nil {
				logger.Info(ctx, "Gemini subagent not available: %v", err)
			}
		}
	}

	return &MaestroService{
		pool:           pool,
		memory:         memory,
		githubTools:    githubTools,
		config:         config,
		logger:         logger,
		sessionManager: sessionManager,
		claudeProc:     claudeProc,
		geminiProc:     geminiProc,
		currentSession: sessionName,
		initialized:    true,
	}, nil
}

func (s *MaestroService) ProcessRequest(ctx context.Context, request Request) (*Response, error) {
	switch request.Type {
	case RequestReview:
		return s.handleReview(ctx, request)
	case RequestAsk:
		return s.handleAsk(ctx, request)
	case RequestClaude:
		return s.handleClaude(ctx, request)
	case RequestGemini:
		return s.handleGemini(ctx, request)
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

func (s *MaestroService) handleClaude(ctx context.Context, request Request) (*Response, error) {
	if s.claudeProc == nil {
		return nil, fmt.Errorf("claude processor not initialized")
	}

	// Build task context
	taskContext := s.buildTaskContext(ctx)
	if request.Context != nil {
		for k, v := range request.Context {
			taskContext[k] = v
		}
	}

	task := agents.Task{
		ID:            fmt.Sprintf("claude-%d", ctx.Value("request_id")),
		Type:          "claude",
		ProcessorType: "claude",
		Metadata: map[string]interface{}{
			"prompt": request.Prompt,
			"type":   request.TaskType,
		},
	}

	result, err := s.claudeProc.Process(ctx, task, taskContext)
	if err != nil {
		return nil, fmt.Errorf("claude processing failed: %w", err)
	}

	resultMap, _ := result.(map[string]interface{})
	response, _ := resultMap["response"].(string)

	return &Response{
		Type:     RequestClaude,
		Answer:   response,
		Metadata: resultMap,
	}, nil
}

func (s *MaestroService) handleGemini(ctx context.Context, request Request) (*Response, error) {
	if s.geminiProc == nil {
		return nil, fmt.Errorf("gemini processor not initialized")
	}

	taskContext := s.buildTaskContext(ctx)
	if request.Context != nil {
		for k, v := range request.Context {
			taskContext[k] = v
		}
	}

	task := agents.Task{
		ID:            fmt.Sprintf("gemini-%d", ctx.Value("request_id")),
		Type:          "gemini",
		ProcessorType: "gemini",
		Metadata: map[string]interface{}{
			"prompt": request.Prompt,
			"type":   request.TaskType,
		},
	}

	result, err := s.geminiProc.Process(ctx, task, taskContext)
	if err != nil {
		return nil, fmt.Errorf("gemini processing failed: %w", err)
	}

	resultMap, _ := result.(map[string]interface{})
	response, _ := resultMap["response"].(string)

	return &Response{
		Type:     RequestGemini,
		Answer:   response,
		Metadata: resultMap,
	}, nil
}

func (s *MaestroService) buildTaskContext(ctx context.Context) map[string]interface{} {
	taskContext := map[string]interface{}{
		"owner": s.config.Owner,
		"repo":  s.config.Repo,
	}

	// Try to get repo path from review agent
	if reviewAgent, err := s.pool.GetReviewAgent(ctx); err == nil {
		if repoPath := reviewAgent.ClonedRepoPath(); repoPath != "" {
			taskContext["repo_path"] = repoPath
		}
	}

	return taskContext
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

// Session management methods

// CreateSession creates a new session and switches to it.
// If name is empty, a unique name will be auto-generated.
func (s *MaestroService) CreateSession(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionManager == nil {
		return fmt.Errorf("session manager not initialized")
	}

	// Auto-generate name if not provided
	if name == "" {
		name = generateSessionName()
	}

	initialContext := map[string]interface{}{
		"owner":   s.config.Owner,
		"repo":    s.config.Repo,
		"purpose": fmt.Sprintf("Maestro session: %s", name),
	}

	session, err := s.sessionManager.CreateSession(ctx, name, initialContext)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Reinitialize processors with new session
	if err := s.switchToSession(ctx, session.Dir); err != nil {
		return err
	}

	s.currentSession = name
	s.logger.Info(ctx, "Created and switched to session: %s", name)
	return nil
}

// SwitchSession switches to an existing session.
func (s *MaestroService) SwitchSession(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionManager == nil {
		return fmt.Errorf("session manager not initialized")
	}

	session, err := s.sessionManager.GetSession(name)
	if err != nil {
		return fmt.Errorf("session not found: %s", name)
	}

	if err := s.switchToSession(ctx, session.Dir); err != nil {
		return err
	}

	s.currentSession = name
	s.logger.Info(ctx, "Switched to session: %s", name)
	return nil
}

// switchToSession reinitializes processors for a session directory.
func (s *MaestroService) switchToSession(ctx context.Context, sessionDir string) error {
	// Try to create Claude processor
	claudeProc, err := subagent.NewClaudeProcessor(s.logger, sessionDir, "")
	if err != nil {
		s.logger.Info(ctx, "Claude subagent not available: %v", err)
	}
	s.claudeProc = claudeProc

	// Try to create Gemini processor
	geminiProc, err := subagent.NewGeminiProcessor(s.logger, sessionDir, "")
	if err != nil {
		s.logger.Info(ctx, "Gemini subagent not available: %v", err)
	}
	s.geminiProc = geminiProc

	return nil
}

// ListSessions returns all available sessions.
func (s *MaestroService) ListSessions(ctx context.Context) ([]subagent.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sessionManager == nil {
		return nil, fmt.Errorf("session manager not initialized")
	}

	return s.sessionManager.ListSessions()
}

// GetCurrentSession returns the name of the current session.
func (s *MaestroService) GetCurrentSession() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentSession
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
