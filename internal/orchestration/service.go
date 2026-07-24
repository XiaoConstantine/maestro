package orchestration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
	dspysubagent "github.com/XiaoConstantine/dspy-go/pkg/agents/subagent"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	maestroace "github.com/XiaoConstantine/maestro/internal/ace"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
	maestrocoding "github.com/XiaoConstantine/maestro/internal/coding"
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
	RequestCoding RequestType = "coding"
	RequestAsk    RequestType = "ask"
	RequestClaude RequestType = "claude"
	RequestGemini RequestType = "gemini"
)

type ServiceConfig struct {
	MemoryType                  MemoryType
	MemoryPath                  string
	QAArtifactsPath             string
	QASkillStorePath            string
	QASkillDomain               string
	RLMOverviewSkillStorePath   string
	RLMOverviewSkillDomain      string
	RLMOverviewArtifactsPath    string
	RLMTargetedAskArtifactsPath string
	Owner                       string
	Repo                        string
	GitHubToken                 string
	IndexWorkers                int
	ReviewWorkers               int
	BudgetConfig                maestrobudget.Config
	BudgetManager               *maestrobudget.BudgetManager
	AllowCodingBash             bool
}

type Request struct {
	Type       RequestType
	PRNumber   int
	Question   string
	Prompt     string // For Claude/Gemini requests
	TaskType   string // e.g., "search", "generate", "review"
	Context    map[string]interface{}
	OnProgress func(status string)
	EventSink  agents.EventSink
}

type Response struct {
	Type     RequestType
	Comments []types.PRReviewComment
	Answer   string
	Metadata map[string]interface{}
}

type MaestroService struct {
	pool                   *AgentPool
	memory                 agents.Memory
	githubTools            types.GitHubInterface
	config                 *ServiceConfig
	logger                 *logging.Logger
	sessionManager         *subagent.SessionManager
	sessionStore           *subagent.SQLiteSessionStore
	claudeTool             core.Tool
	geminiTool             core.Tool
	currentSession         string
	aceManager             *maestroace.MaestroACEManager
	rlmOverviewSkillStore  skills.Store
	rlmOverviewSkillDomain string
	budgetManager          *maestrobudget.BudgetManager

	codingMu           sync.Mutex
	codingSession      *maestrocoding.Session
	codingSessionID    string
	codingWorkspace    string
	codingShuttingDown bool

	mu          sync.RWMutex
	initialized bool
}

func NewMaestroService(ctx context.Context, config *ServiceConfig, githubTools types.GitHubInterface) (*MaestroService, error) {
	logger := logging.GetLogger()

	if config.MemoryType == MemorySQLite {
		return nil, fmt.Errorf("MemorySQLite is no longer supported; Maestro now persists interactive state through sessionevent.db")
	}

	if envType := os.Getenv("MAESTRO_MEMORY_TYPE"); envType == "sqlite" {
		return nil, fmt.Errorf("MAESTRO_MEMORY_TYPE=sqlite is no longer supported; Maestro now persists interactive state through sessionevent.db")
	}

	memory := agents.NewInMemoryStore()
	budgetManager := config.BudgetManager
	if budgetManager == nil {
		budgetManager = maestrobudget.NewBudgetManager(config.BudgetConfig)
	}

	qaArtifacts, err := loadConfiguredQAArtifacts(config.QAArtifactsPath)
	if err != nil {
		return nil, fmt.Errorf("load QA artifacts: %w", err)
	}

	qaSkillStorePath, err := resolveQASkillStorePath(config.QASkillStorePath, config.MemoryPath)
	if err != nil {
		return nil, fmt.Errorf("resolve QA skill store: %w", err)
	}
	qaSkillDomain := resolveQASkillDomain(config.QASkillDomain)
	qaSkillStore := skills.NewFileStore(qaSkillStorePath)
	logger.Debug(ctx, "Configured QA skill store path=%q domain=%q", qaSkillStorePath, qaSkillDomain)

	rlmOverviewSkillStorePath, err := resolveRLMOverviewSkillStorePath(config.RLMOverviewSkillStorePath, config.MemoryPath, qaSkillStorePath)
	if err != nil {
		return nil, fmt.Errorf("resolve RLM overview skill store: %w", err)
	}
	rlmOverviewSkillDomain := resolveRLMOverviewSkillDomain(config.RLMOverviewSkillDomain)
	var rlmOverviewSkillStore skills.Store
	// RLM overview defaults to the QA store path unless explicitly separated;
	// domains keep the published skills isolated even when the JSON backing file is shared.
	if rlmOverviewSkillStorePath == qaSkillStorePath {
		rlmOverviewSkillStore = qaSkillStore
	} else {
		rlmOverviewSkillStore = skills.NewFileStore(rlmOverviewSkillStorePath)
	}
	logger.Debug(ctx, "Configured RLM overview skill store path=%q domain=%q", rlmOverviewSkillStorePath, rlmOverviewSkillDomain)

	pool := NewAgentPool(config, memory, githubTools, logger, qaArtifacts, qaSkillStore, qaSkillDomain, qaSkillStorePath)

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

	sessionStorePath := filepath.Join(filepath.Dir(sessionDir), "sessionevent.db")
	sessionStore, err := subagent.NewSQLiteSessionStore(sessionStorePath)
	if err != nil {
		logger.Warn(ctx, "Failed to create session event store: %v", err)
	}

	sessionOpts := []subagent.SessionManagerOption{}
	if sessionStore != nil {
		sessionOpts = append(sessionOpts, subagent.WithSessionEventStore(sessionStore))
	}

	sessionManager, err := subagent.NewSessionManager(
		sessionDir,
		logger,
		sessionOpts...,
	)
	if err != nil {
		logger.Warn(ctx, "Failed to create session manager: %v", err)
	}

	// Create session for subagents with unique name
	var claudeTool core.Tool
	var geminiTool core.Tool
	sessionName := generateSessionName()
	if sessionManager != nil {
		defaultSession, err := sessionManager.GetOrCreateSession(ctx, sessionName, map[string]interface{}{
			"owner":   config.Owner,
			"repo":    config.Repo,
			"purpose": "Maestro CLI subagent communication",
		})
		if err == nil {
			staticInput := map[string]any{
				"owner": config.Owner,
				"repo":  config.Repo,
			}
			// Initialize Claude subagent tool (uses ANTHROPIC_API_KEY env var)
			claudeTool, err = subagent.NewClaudeTool(logger, sessionManager, defaultSession.ID, staticInput)
			if err != nil {
				logger.Info(ctx, "Claude subagent not available: %v", err)
			}

			// Initialize Gemini subagent tool (uses GOOGLE_API_KEY or GEMINI_API_KEY env var)
			geminiTool, err = subagent.NewGeminiTool(logger, sessionManager, defaultSession.ID, staticInput)
			if err != nil {
				logger.Info(ctx, "Gemini subagent not available: %v", err)
			}
		}
	}

	pool.ConfigureQA(sessionManager, sessionStore, sessionName)

	// Initialize ACE (Agentic Context Engineering) manager for self-improving agents
	aceConfig := maestroace.LoadConfigFromEnv()
	var aceBasePath string
	if config.MemoryPath != "" {
		aceBasePath = filepath.Dir(config.MemoryPath)
	} else {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			homeDir = os.TempDir()
		}
		aceBasePath = filepath.Join(homeDir, ".maestro")
	}

	aceManager, err := maestroace.NewMaestroACEManager(aceBasePath, aceConfig, logger)
	if err != nil {
		logger.Warn(ctx, "Failed to initialize ACE manager: %v", err)
	} else if aceManager.IsEnabled() {
		logger.Info(ctx, "ACE (Agentic Context Engineering) enabled for self-improving agents")
	}

	return &MaestroService{
		pool:                   pool,
		memory:                 memory,
		githubTools:            githubTools,
		config:                 config,
		logger:                 logger,
		sessionManager:         sessionManager,
		sessionStore:           sessionStore,
		claudeTool:             claudeTool,
		geminiTool:             geminiTool,
		currentSession:         sessionName,
		aceManager:             aceManager,
		rlmOverviewSkillStore:  rlmOverviewSkillStore,
		rlmOverviewSkillDomain: rlmOverviewSkillDomain,
		budgetManager:          budgetManager,
		initialized:            true,
	}, nil
}

func (s *MaestroService) ProcessRequest(ctx context.Context, request Request) (*Response, error) {
	switch request.Type {
	case RequestReview:
		return s.withBudgetMetadata(s.handleReview(ctx, request))
	case RequestCoding:
		return s.withBudgetMetadata(s.handleCoding(ctx, request))
	case RequestAsk:
		return s.withBudgetMetadata(s.handleAsk(ctx, request))
	case RequestClaude:
		return s.withBudgetMetadata(s.handleClaude(ctx, request))
	case RequestGemini:
		return s.withBudgetMetadata(s.handleGemini(ctx, request))
	default:
		return nil, fmt.Errorf("unknown request type: %s", request.Type)
	}
}

func (s *MaestroService) withBudgetMetadata(response *Response, err error) (*Response, error) {
	if err != nil {
		return nil, err
	}
	s.attachBudgetMetadata(response)
	return response, nil
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

func (s *MaestroService) handleCoding(ctx context.Context, request Request) (*Response, error) {
	workspace := s.repositoryWorkspace(ctx)
	if workspace == "" {
		return &Response{
			Type:   RequestCoding,
			Answer: "Repository is still being cloned. Please wait a moment and try again.",
		}, nil
	}

	session, err := s.codingSessionFor(ctx, workspace)
	if err != nil {
		return nil, err
	}
	result, err := session.Prompt(ctx, request.Prompt, request.EventSink)
	s.recordExecutionTraceUsage(ctx, "coding.native", result.Trace)
	if err != nil {
		return nil, err
	}

	answer, err := codingAnswer(result)
	if err != nil {
		return nil, err
	}
	return &Response{
		Type:   RequestCoding,
		Answer: answer,
		Metadata: map[string]interface{}{
			"workspace": workspace,
			"trace":     result.Trace,
		},
	}, nil
}

func codingAnswer(result agents.AgentExecutionResult) (string, error) {
	answer := strings.TrimSpace(stringValue(result.Output["final_answer"]))
	if answer == "" && result.Trace != nil {
		answer = strings.TrimSpace(stringValue(result.Trace.Output["final_answer"]))
	}
	if answer == "" {
		if diagnostic := strings.TrimSpace(stringValue(result.Output["error"])); diagnostic != "" {
			if result.Trace != nil && result.Trace.Status == agents.TraceStatusPartial {
				return "Coding run stopped: " + diagnostic, nil
			}
			return "", fmt.Errorf("coding run failed: %s", diagnostic)
		}
		if result.Trace != nil && result.Trace.Status != agents.TraceStatusSuccess {
			diagnostic := strings.TrimSpace(result.Trace.Error)
			if diagnostic == "" {
				diagnostic = strings.TrimSpace(result.Trace.TerminationCause)
			}
			if diagnostic == "" {
				diagnostic = "no final answer"
			}
			return "Coding run stopped: " + diagnostic, nil
		}
	}
	if answer == "" {
		return "", fmt.Errorf("coding run completed without a final answer")
	}
	return answer, nil
}

func (s *MaestroService) codingSessionFor(ctx context.Context, workspace string) (*maestrocoding.Session, error) {
	s.codingMu.Lock()
	defer s.codingMu.Unlock()
	if s.codingShuttingDown {
		return nil, fmt.Errorf("coding service is shutting down")
	}

	sessionID := codingSessionID(s.GetCurrentSession())
	if s.codingSession != nil && s.codingWorkspace == workspace && s.codingSessionID == sessionID {
		return s.codingSession, nil
	}
	if s.codingSession != nil {
		if err := s.codingSession.Close(ctx); err != nil {
			return nil, fmt.Errorf("close previous coding session: %w", err)
		}
		s.codingSession = nil
		s.codingWorkspace = ""
		s.codingSessionID = ""
	}
	session, err := maestrocoding.NewSession(maestrocoding.Config{
		LLM:          core.GetDefaultLLM(),
		Workspace:    workspace,
		SessionID:    sessionID,
		SessionStore: s.sessionStore,
		AllowBash:    s.config.AllowCodingBash,
	})
	if err != nil {
		return nil, fmt.Errorf("create coding session: %w", err)
	}
	s.codingSession = session
	s.codingWorkspace = workspace
	s.codingSessionID = sessionID
	return session, nil
}

const codingSessionNamespace = "maestro:internal:coding:"

func codingSessionID(sessionName string) string {
	digest := sha256.Sum256([]byte(sessionName))
	return codingSessionNamespace + hex.EncodeToString(digest[:])
}

func (s *MaestroService) repositoryWorkspace(ctx context.Context) string {
	if reviewAgent, err := s.pool.GetReviewAgent(ctx); err == nil {
		return reviewAgent.ClonedRepoPath()
	}
	return ""
}

func (s *MaestroService) CancelCodingRun() bool {
	s.codingMu.Lock()
	session := s.codingSession
	s.codingMu.Unlock()
	return session != nil && session.Cancel()
}

func (s *MaestroService) handleAsk(ctx context.Context, request Request) (*Response, error) {
	repoPath := s.repositoryWorkspace(ctx)

	if repoPath == "" {
		return &Response{
			Type:   RequestAsk,
			Answer: "Repository is still being cloned. Please wait a moment and try again.",
		}, nil
	}

	switch forcedAskStrategy() {
	case "native":
		s.logger.Debug(ctx, "Forcing native ask strategy for question: %q", request.Question)
	case "rlm":
		s.logger.Debug(ctx, "Forcing RLM ask strategy for question: %q", request.Question)
		response, err := s.handleRLMOverview(ctx, request.Question, repoPath)
		if err == nil {
			return response, nil
		}
		s.logger.Warn(ctx, "Forced RLM overview path failed, falling back to native QA: %v", err)
	case "targeted", "rlm-targeted", "rlm_targeted":
		s.logger.Debug(ctx, "Forcing RLM targeted ask strategy for question: %q", request.Question)
		response, err := s.handleRLMTargetedAsk(ctx, request.Question, repoPath)
		if err == nil {
			return response, nil
		}
		s.logger.Warn(ctx, "Forced RLM targeted ask path failed, falling back to native QA: %v", err)
	default:
		if shouldUseRLMOverviewQuery(request.Question) {
			response, err := s.handleRLMOverview(ctx, request.Question, repoPath)
			if err == nil {
				return response, nil
			}
			s.logger.Warn(ctx, "RLM overview path failed, falling back to native QA: %v", err)
		}
	}

	agent, err := s.pool.GetQAAgent(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get QA agent: %w", err)
	}

	answer, confidence, sources, trace, err := agent.askWithTrace(ctx, request.Question, repoPath, s.config.Owner, s.config.Repo)
	s.recordExecutionTraceUsage(ctx, "ask.native", trace)
	if err != nil {
		return nil, err
	}
	skillDomain, skillVersion := agent.SkillState()

	return &Response{
		Type:   RequestAsk,
		Answer: answer,
		Metadata: map[string]interface{}{
			"confidence":       confidence,
			"sources":          sources,
			"qa_skill_domain":  skillDomain,
			"qa_skill_version": skillVersion,
		},
	}, nil
}

func (s *MaestroService) handleClaude(ctx context.Context, request Request) (*Response, error) {
	return s.executeSubagentTool(ctx, s.claudeTool, RequestClaude, "claude", request)
}

func (s *MaestroService) handleGemini(ctx context.Context, request Request) (*Response, error) {
	return s.executeSubagentTool(ctx, s.geminiTool, RequestGemini, "gemini", request)
}

func (s *MaestroService) executeSubagentTool(ctx context.Context, tool core.Tool, responseType RequestType, subagentName string, request Request) (*Response, error) {
	if tool == nil {
		return nil, fmt.Errorf("%s subagent not initialized", subagentName)
	}

	taskID := fmt.Sprintf("%s-%d", subagentName, time.Now().UnixNano())
	taskContext := s.buildTaskContext(ctx)
	if request.Context != nil {
		for k, v := range request.Context {
			taskContext[k] = v
		}
	}
	taskContext["prompt"] = request.Prompt
	taskContext["task_type"] = request.TaskType
	taskContext["task_id"] = taskID

	parent := dspysubagent.ParentContext{
		TaskID:          taskID,
		ParentAgentID:   "maestro-service",
		ParentAgentType: "maestro-service",
		SessionID:       s.currentSession,
		Input:           maps.Clone(taskContext),
	}
	result, err := tool.Execute(dspysubagent.WithParentContext(ctx, parent), taskContext)
	if err != nil {
		return nil, fmt.Errorf("%s processing failed: %w", subagentName, err)
	}

	metadata := subagentToolMetadata(result)
	answer := stringMetadata(metadata, "response")
	if answer == "" {
		answer = core.ToolResultMetadataString(result.Metadata, core.ToolResultDisplayTextMeta)
	}

	return &Response{
		Type:     responseType,
		Answer:   answer,
		Metadata: metadata,
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

func (s *MaestroService) GetBudgetManager() *maestrobudget.BudgetManager {
	if s == nil {
		return nil
	}
	return s.budgetManager
}

func (s *MaestroService) BudgetStatus() *maestrobudget.BudgetStatus {
	if s == nil || s.budgetManager == nil {
		return nil
	}
	status := s.budgetManager.Status()
	return &status
}

func (s *MaestroService) attachBudgetMetadata(response *Response) {
	if response == nil || s == nil || s.budgetManager == nil {
		return
	}
	status := s.budgetManager.Status()
	if response.Metadata == nil {
		response.Metadata = make(map[string]interface{})
	}
	response.Metadata["budget_total_spent"] = status.TotalSpentUSD
	response.Metadata["budget_remaining"] = status.RemainingUSD
	response.Metadata["budget_percent_used"] = status.PercentUsed
	response.Metadata["budget_total_tokens"] = status.TotalTokens
	response.Metadata["budget_weighted_total_tokens"] = status.WeightedTotalTokens
	response.Metadata["budget_cache_read_input_tokens"] = status.CacheReadInputTokens
	response.Metadata["budget_cache_token_weight_unavailable"] = status.CacheTokenWeightUnavailable
	response.Metadata["budget_scope"] = "running_total"
	response.Metadata["budget_running_total_spent"] = status.TotalSpentUSD
	response.Metadata["budget_running_remaining"] = status.RemainingUSD
	response.Metadata["budget_running_percent_used"] = status.PercentUsed
	response.Metadata["budget_running_total_tokens"] = status.TotalTokens
	response.Metadata["budget_running_weighted_total_tokens"] = status.WeightedTotalTokens
}

func (s *MaestroService) IsReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
}

func (s *MaestroService) Shutdown(ctx context.Context) error {
	var shutdownErrs []error
	codingStopped := true

	s.codingMu.Lock()
	s.codingShuttingDown = true
	codingSession := s.codingSession
	s.codingMu.Unlock()
	if codingSession != nil {
		if err := codingSession.Close(ctx); err != nil {
			codingStopped = false
			s.logger.Warn(ctx, "Failed to stop coding session: %v", err)
			shutdownErrs = append(shutdownErrs, fmt.Errorf("stop coding session: %w", err))
		}
	}

	if codingStopped {
		s.pool.Shutdown(ctx)
	}

	if codingStopped && s.sessionStore != nil {
		if err := s.sessionStore.Close(); err != nil {
			s.logger.Warn(ctx, "Failed to close session event store: %v", err)
			shutdownErrs = append(shutdownErrs, fmt.Errorf("close session event store: %w", err))
		}
	}

	// Close ACE independently so pending learnings are flushed even when a run resists cancellation.
	if s.aceManager != nil {
		if err := s.aceManager.Close(); err != nil {
			s.logger.Warn(ctx, "Failed to close ACE manager: %v", err)
			shutdownErrs = append(shutdownErrs, fmt.Errorf("close ACE manager: %w", err))
		}
	}

	return errors.Join(shutdownErrs...)
}

// GetACEManager returns the ACE manager for self-improving agent capabilities.
func (s *MaestroService) GetACEManager() *maestroace.MaestroACEManager {
	return s.aceManager
}

func (s *MaestroService) SetReviewAgent(agent types.ReviewAgent) {
	if budgetAware, ok := agent.(interface {
		SetBudgetManager(*maestrobudget.BudgetManager)
	}); ok {
		budgetAware.SetBudgetManager(s.budgetManager)
	}
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
	if strings.HasPrefix(name, "maestro:internal:") {
		return fmt.Errorf("session name uses reserved Maestro namespace")
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

	// Reinitialize subagent tools with new session
	if err := s.switchToSession(ctx, session.ID); err != nil {
		return err
	}

	s.currentSession = name
	s.pool.ConfigureQA(s.sessionManager, s.sessionStore, name)
	s.logger.Info(ctx, "Created and switched to session: %s", name)
	return nil
}

// SwitchSession switches to an existing session.
func (s *MaestroService) SwitchSession(ctx context.Context, name string) error {
	if strings.HasPrefix(name, "maestro:internal:") {
		return fmt.Errorf("session name uses reserved Maestro namespace")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionManager == nil {
		return fmt.Errorf("session manager not initialized")
	}

	session, err := s.sessionManager.GetSession(name)
	if err != nil {
		return fmt.Errorf("session not found: %s", name)
	}

	if err := s.switchToSession(ctx, session.ID); err != nil {
		return err
	}

	s.currentSession = name
	s.pool.ConfigureQA(s.sessionManager, s.sessionStore, name)
	s.logger.Info(ctx, "Switched to session: %s", name)
	return nil
}

// switchToSession reinitializes subagent tools for an active session.
func (s *MaestroService) switchToSession(ctx context.Context, sessionID string) error {
	staticInput := s.buildTaskContext(ctx)

	// Try to create Claude subagent tool
	claudeTool, err := subagent.NewClaudeTool(s.logger, s.sessionManager, sessionID, staticInput)
	if err != nil {
		s.logger.Info(ctx, "Claude subagent not available: %v", err)
	}
	s.claudeTool = claudeTool

	// Try to create Gemini subagent tool
	geminiTool, err := subagent.NewGeminiTool(s.logger, s.sessionManager, sessionID, staticInput)
	if err != nil {
		s.logger.Info(ctx, "Gemini subagent not available: %v", err)
	}
	s.geminiTool = geminiTool

	return nil
}

func subagentToolMetadata(result core.ToolResult) map[string]interface{} {
	metadata := make(map[string]interface{})
	details, _ := result.Annotations[core.ToolResultDetailsAnnotation].(map[string]any)
	if output, ok := details["output"].(map[string]any); ok {
		for key, value := range output {
			metadata[key] = value
		}
	}
	for _, key := range []string{"subagent", "subagent_name", "session_policy", "completed", "duration_ms", "trace"} {
		if value, ok := details[key]; ok {
			metadata[key] = value
		}
	}
	return metadata
}

func stringMetadata(values map[string]interface{}, key string) string {
	raw, ok := values[key]
	if !ok || raw == nil {
		return ""
	}
	text, ok := raw.(string)
	if !ok {
		return ""
	}
	return text
}

// ListSessions returns all available sessions.
func (s *MaestroService) ListSessions(ctx context.Context) ([]subagent.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sessionManager == nil {
		return nil, fmt.Errorf("session manager not initialized")
	}

	sessions, err := s.sessionManager.ListSessions()
	if err != nil {
		return nil, err
	}
	visible := sessions[:0]
	for _, session := range sessions {
		if !strings.HasPrefix(session.ID, "maestro:internal:") {
			visible = append(visible, session)
		}
	}
	return visible, nil
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
