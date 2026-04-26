package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/ace"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/chunk"
	"github.com/XiaoConstantine/maestro/internal/guideline"
	"github.com/XiaoConstantine/maestro/internal/metrics"
	"github.com/XiaoConstantine/maestro/internal/patterns"
	"github.com/XiaoConstantine/maestro/internal/reasoning"
	"github.com/XiaoConstantine/maestro/internal/search"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
	"github.com/XiaoConstantine/maestro/internal/workflow"
	"github.com/google/go-github/v68/github"
	"github.com/logrusorgru/aurora"
	"go.uber.org/atomic"
)

// Type aliases are defined in types_aliases.go - this file uses them

// PRReviewAgent handles code review using dspy-go.
type PRReviewAgent struct {
	reviewProcessor  reviewChunkProcessor             // High-performance chunk processor
	declarativeChain *workflow.DeclarativeReviewChain // Declarative workflow for complex reviews
	memory           agents.Memory
	guidelineSearch  *guideline.Searcher      // Sgrep-based guideline search
	activeThreads    map[int64]*ThreadTracker // Track active discussion threads
	// TODO: should align with dspy agent interface
	githubTools         types.GitHubInterface // Add this field
	stopper             *Stopper
	metrics             types.MetricsCollector
	workers             *types.AgentConfig
	indexStatus         *types.IndexingStatus // Track background indexing progress
	hadExistingComments bool                  // Track if any comments/reviews exist (including bots)
	clonedRepoPath      string                // Path to cloned repo in /tmp for sgrep indexing
	sgrepTool           *search.SgrepTool     // Sgrep tool for semantic search

	// ACE (Agentic Context Engineering) for self-improving reviews
	aceManager        *ace.Manager            // ACE manager for trajectory recording and learnings
	currentTrajectory *ace.TrajectoryRecorder // Current review trajectory for learning
	aceEnabled        bool                    // Whether ACE is enabled for this agent
	sgrepHome         string
}

type reviewPipelineResult struct {
	ProcessedTasks       []PRReviewTask
	Comments             []PRReviewComment
	TotalChunks          int
	SelectedChunks       int
	RawCommentCount      int
	PreVerificationCount int
	SkippedAfterFilter   int
	FilterDropReasons    map[string]int
	FilterRejected       []ReviewFilterRejection
	VerificationDropped  int
	VerificationReasons  map[string]int
	VerificationRejected []ReviewVerificationRejection
	Phase2Duration       time.Duration
	Phase3Duration       time.Duration
	Phase4Duration       time.Duration
	Phase5Duration       time.Duration
}

type reviewChunkProcessor interface {
	ProcessMultipleChunks(ctx context.Context, chunks []map[string]interface{}, taskContext map[string]interface{}) ([]*types.EnhancedReviewResult, error)
}

const reviewChunkContextRadius = 1

type ThreadTracker struct {
	LastComment  *types.PRReviewComment
	ReviewChunks []types.ReviewChunk
	FileContent  string
	LastUpdate   time.Time
	Status       types.ThreadStatus // Using our existing ThreadStatus type

	ParentCommentID     int64
	OriginalAuthor      string // Who started the thread
	ThreadID            int64
	InReplyToMyComment  bool                    // Whether this is a reply to our comment
	IsResolved          bool                    // Whether the thread is resolved
	ConversationHistory []types.PRReviewComment // Full history of the thread
}

type ReviewMetadata struct {
	FilePath       string
	FileContent    string
	Changes        string
	Category       string
	LineRange      types.LineRange
	ChunkNumber    int
	TotalChunks    int
	ReviewType     string
	ReviewPatterns []*types.Content // Added for repository patterns
	Guidelines     []*types.Content // Added for guidelines
}

type ReviewFilterRejection struct {
	FilePath   string `json:"file_path,omitempty"`
	LineNumber int    `json:"line_number,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
	Content    string `json:"content,omitempty"`
}

func parseHunkHeader(line string) (int, error) {
	// First, verify this is actually a hunk header
	if !strings.HasPrefix(line, "@@") {
		return 0, fmt.Errorf("not a valid hunk header: %s", line)
	}

	// Extract the part between @@ markers
	parts := strings.Split(line, "@@")
	if len(parts) < 2 {
		return 0, fmt.Errorf("malformed hunk header: %s", line)
	}

	// Parse the line numbers section
	// It looks like: -34,6 +34,8
	numbers := strings.TrimSpace(parts[1])

	// Split into old and new changes
	ranges := strings.Split(numbers, " ")
	if len(ranges) < 2 {
		return 0, fmt.Errorf("invalid hunk range format: %s", numbers)
	}

	// Get the new file range (starts with +)
	newRange := ranges[1]
	if !strings.HasPrefix(newRange, "+") {
		return 0, fmt.Errorf("new range must start with +: %s", newRange)
	}

	// Remove the + and split into start,count if there's a comma
	newRange = strings.TrimPrefix(newRange, "+")
	newParts := strings.Split(newRange, ",")

	// Parse the starting line number
	startLine, err := strconv.Atoi(newParts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid line number: %w", err)
	}

	return startLine, nil
}

// ExtractRelevantChanges extracts the portion of git diff relevant to the chunk.
func ExtractRelevantChanges(changes string, startline, endline int) string {
	// Parse the git diff and extract changes for the line range
	// This is a simplified version - would need proper diff parsing
	difflines := strings.Split(changes, "\n")
	relevantdiff := make([]string, 0)

	currentLine := 0
	for _, line := range difflines {
		if strings.HasPrefix(line, "@@") {
			newStart, err := parseHunkHeader(line)
			if err != nil {
				// Handle error appropriately
				continue
			}
			currentLine = newStart
			continue
		}

		if currentLine >= startline && currentLine < endline {
			relevantdiff = append(relevantdiff, line)
		}

		if !strings.HasPrefix(line, "-") {
			currentLine++
		}
	}

	return strings.Join(relevantdiff, "\n")
}

// NewPRReviewAgent creates a new PR review agent.
func NewPRReviewAgent(ctx context.Context, githubTool GitHubInterface, dbPath string, config *AgentConfig) (ReviewAgent, error) {
	logger := logging.GetLogger()

	logger.Debug(ctx, "Starting agent initialization")
	if config == nil {
		config = defaultAgentConfig()
	}

	// Use dbPath directory for .maestro storage (even though we don't use SQLite anymore)
	dataDir := reviewStateDirFromDBPath(dbPath)
	sgrepHome := reviewSgrepHome(dataDir)

	// Initialize sgrep-based guideline searcher with .maestro directory
	guidelineSearcher := guideline.NewSearcher(dataDir, logger)

	// Ensure guidelines are cached and indexed in background
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Warn(ctx, "guideline setup panic recovered: %v", r)
			}
		}()
		if err := guidelineSearcher.EnsureReady(ctx); err != nil {
			logger.Warn(ctx, "Failed to setup guidelines: %v", err)
		} else {
			logger.Info(ctx, "Guidelines ready at %s", guidelineSearcher.GuidelinesDir())
		}
	}()

	metricsCollector := metrics.NewBusinessMetrics(logger)
	metricsCollector.StartOptimizationCycle(ctx)

	reviewArtifacts, bestSkill, reviewSkillStorePath, reviewSkillDomain, err := loadRuntimeReviewArtifacts(ctx, dbPath, config)
	if err != nil {
		return nil, fmt.Errorf("load review runtime artifacts: %w", err)
	}
	reviewInstructionOverlay := materializeReviewInstructionOverlay(reviewArtifacts, bestSkill)

	// Create agent components immediately - don't wait for indexing
	memory := agents.NewInMemoryStore()
	stopper := NewStopper()
	indexStatus := types.NewIndexingStatus()

	reviewProcessor, reviewProcessorMode, err := newRuntimeReviewChunkProcessor(ctx, metricsCollector, logger, reviewInstructionOverlay)
	if err != nil {
		return nil, fmt.Errorf("initialize review chunk processor: %w", err)
	}
	if reviewProcessorMode == "parallel" {
		logger.Debug(ctx, "✅ Created parallel review processor with %d workers", 120)
	} else {
		logger.Debug(ctx, "✅ Created %s review processor", reviewProcessorMode)
	}
	if bestSkill != nil {
		logger.Info(ctx, "Loaded persisted review skill domain=%q version=%d name=%q store=%q", reviewSkillDomain, bestSkill.Version, bestSkill.Name, reviewSkillStorePath)
	} else if strings.TrimSpace(reviewInstructionOverlay) != "" {
		logger.Info(ctx, "Loaded review instruction overlay from artifacts path=%q", config.ReviewArtifactsPath)
	}

	// Initialize declarative workflow if Phase 2 features are enabled
	var declarativeChain *workflow.DeclarativeReviewChain
	if shouldUseDeclarativeWorkflows() {
		logger.Debug(ctx, "🏗️ Initializing Declarative Workflow Builder")
		logger.Debug(ctx, "📋 Declarative workflow features: retry logic, parallel validation, conditional refinement")
		declarativeChain = workflow.NewDeclarativeReviewChain(ctx, nil, nil, nil)
		logger.Debug(ctx, "✅ Declarative Workflow initialized successfully")
	}

	agent := &PRReviewAgent{
		reviewProcessor:  reviewProcessor,
		declarativeChain: declarativeChain,
		memory:           memory,
		guidelineSearch:  guidelineSearcher,
		githubTools:      githubTool,
		stopper:          stopper,
		metrics:          metricsCollector,
		workers:          config,
		indexStatus:      indexStatus,
		sgrepHome:        sgrepHome,
	}

	// Start background indexing AFTER agent creation
	logger.Debug(ctx, "🚀 Agent ready! Starting background repository indexing...")
	go agent.startBackgroundIndexing(ctx, githubTool, config.IndexWorkers)

	return agent, nil
}

func reviewStateDirFromDBPath(dbPath string) string {
	return filepath.Dir(dbPath)
}

func reviewSgrepHome(stateDir string) string {
	if strings.TrimSpace(stateDir) == "" {
		return ""
	}
	return filepath.Join(stateDir, "sgrep")
}

// NewPRReviewAgentWithACE creates a new PR review agent with ACE (Agentic Context Engineering) enabled.
func NewPRReviewAgentWithACE(ctx context.Context, githubTool GitHubInterface, dbPath string, config *AgentConfig, aceManager *ace.Manager) (ReviewAgent, error) {
	agent, err := NewPRReviewAgent(ctx, githubTool, dbPath, config)
	if err != nil {
		return nil, err
	}

	// Enable ACE if manager is provided
	if aceManager != nil {
		if prAgent, ok := agent.(*PRReviewAgent); ok {
			prAgent.aceManager = aceManager
			prAgent.aceEnabled = true
			logging.GetLogger().Info(ctx, "ACE enabled for PRReviewAgent - learnings will be recorded and applied")
		}
	}

	return agent, nil
}

// shouldUseDeclarativeWorkflows determines if declarative workflows should be used.
func shouldUseDeclarativeWorkflows() bool {
	features := GetGlobalFeatures()
	if features == nil {
		return false
	}
	return features.DeclarativeWorkflows
}

// processChunkWithDeclarativeWorkflow processes a chunk using the declarative workflow system.

func (a *PRReviewAgent) generateResponseWithDeclarativeWorkflow(ctx context.Context, responseContext map[string]interface{}) (*agents.OrchestratorResult, error) {
	if a.declarativeChain == nil {
		return nil, fmt.Errorf("declarative workflow not initialized")
	}

	logger := logging.GetLogger()
	filePath, _ := responseContext["file_path"].(string)

	// Extract line number (might be stored as float64)
	var lineNum int
	if ln, ok := responseContext["line_number"].(float64); ok {
		lineNum = int(ln)
	} else if ln, ok := responseContext["line_number"].(int); ok {
		lineNum = ln
	}

	logger.Info(ctx, "🏗️ Generating response with Phase 2.3 Declarative Workflow: %s:%d", filePath, lineNum)

	// Create task for response generation with properly mapped context
	// Map responseContext fields to the expected task metadata format
	taskMetadata := map[string]interface{}{
		"file_content":     responseContext["file_content"],
		"changes":          "", // Response generation doesn't need changes
		"file_path":        responseContext["file_path"],
		"original_comment": responseContext["original_comment"],
		"thread_context":   responseContext["thread_context"],
		"line_number":      responseContext["line_number"],
		"thread_id":        responseContext["thread_id"],
		"category":         responseContext["category"],
		"processor_type":   responseContext["processor_type"],
		"task_type":        responseContext["task_type"],
	}

	task := agents.Task{
		ID:       fmt.Sprintf("declarative_response_%s_%d_%d", filePath, lineNum, time.Now().UnixNano()),
		Type:     "comment_response",
		Metadata: taskMetadata,
		Priority: 1,
	}

	// Add declarative workflow context for response generation
	declarativeContext := make(map[string]interface{})
	for k, v := range responseContext {
		declarativeContext[k] = v
	}
	declarativeContext["declarative_processing"] = true
	declarativeContext["workflow_version"] = "2.3"
	declarativeContext["processing_mode"] = "declarative_response_generation"
	declarativeContext["response_type"] = "comment_reply"

	// Process with declarative workflow
	startTime := time.Now()
	result, err := a.declarativeChain.Process(ctx, task, declarativeContext)
	processingDuration := time.Since(startTime)

	if err != nil {
		logger.Error(ctx, "❌ Phase 2.3 Declarative response generation failed for %s:%d after %v: %v",
			filePath, lineNum, processingDuration, err)
		return nil, err
	}

	logger.Info(ctx, "✅ Phase 2.3 Declarative response generation completed for %s:%d in %v",
		filePath, lineNum, processingDuration)

	// Track declarative workflow usage for response generation
	if globalMetrics != nil {
		globalMetrics.TrackFeatureUsage(GetGlobalFeatures(), "declarative_workflows")
	}

	// Convert declarative result to orchestrator result format
	return a.convertDeclarativeToOrchestratorResult(result), nil
}

// convertDeclarativeToOrchestratorResult converts declarative workflow result to orchestrator format.
func (a *PRReviewAgent) convertDeclarativeToOrchestratorResult(result interface{}) *agents.OrchestratorResult {
	if resultMap, ok := result.(map[string]interface{}); ok {
		return &agents.OrchestratorResult{
			CompletedTasks: map[string]interface{}{
				"declarative_review": resultMap,
			},
			FailedTasks: make(map[string]error),
			Analysis:    "Declarative workflow processing completed",
			Metadata: map[string]interface{}{
				"declarative_processing": true,
				"processing_type":        resultMap["processing_type"],
				"workflow_version":       resultMap["workflow_version"],
			},
		}
	}

	// Fallback for unexpected result types
	return &agents.OrchestratorResult{
		CompletedTasks: map[string]interface{}{
			"declarative_review": result,
		},
		FailedTasks: make(map[string]error),
		Analysis:    "Declarative workflow processing completed",
		Metadata: map[string]interface{}{
			"declarative_processing": true,
		},
	}
}

func (a *PRReviewAgent) startBackgroundIndexing(ctx context.Context, githubTool GitHubInterface, workers int) {
	logger := logging.GetLogger()

	a.indexStatus.SetIndexing(true)

	// Get repo info for cloning
	repoInfo := githubTool.GetRepositoryInfo(ctx)
	repoFullName := fmt.Sprintf("%s/%s", repoInfo.Owner, repoInfo.Name)

	logger.Debug(ctx, "Starting background indexing for %s using sgrep", repoFullName)

	// Clone repo to /tmp and index with sgrep
	err := a.cloneAndIndexWithSgrep(ctx, repoFullName, "")

	if err != nil {
		a.indexStatus.SetComplete(err)
		// Only log errors to debug level to avoid console spam
		logger.Debug(ctx, "Background indexing failed: %v", err)
	} else {
		a.indexStatus.SetComplete(nil)
		// Only log completion to debug level to avoid console spam
		logger.Debug(ctx, "Background indexing completed successfully")
	}
}

// cloneAndIndexWithSgrep clones a repo to /tmp and indexes it with sgrep.
func (a *PRReviewAgent) cloneAndIndexWithSgrep(ctx context.Context, repoFullName, branch string) error {
	logger := logging.GetLogger()

	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Check if sgrep is available
	sgrepTool := search.NewSgrepToolWithHome(logger, "", a.sgrepHome)
	if !sgrepTool.IsAvailable(ctx) {
		return fmt.Errorf("sgrep not installed")
	}

	// Update progress: starting clone
	a.indexStatus.SetProgress(0.1)

	// Create temp directory for the repo
	tmpDir, err := os.MkdirTemp("", "maestro-repo-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Ensure cleanup on error or cancellation
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			os.RemoveAll(tmpDir)
		}
	}()

	logger.Info(ctx, "📦 Cloning %s to %s", repoFullName, tmpDir)

	// Clone using gh CLI with context for cancellation
	args := []string{"repo", "clone", repoFullName, tmpDir}
	if branch != "" {
		args = append(args, "--", "-b", branch)
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Check if error was due to cancellation
		if ctx.Err() != nil {
			return fmt.Errorf("clone cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("failed to clone repo: %w (output: %s)", err, string(output))
	}

	// Check for cancellation after clone
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Only set clonedRepoPath AFTER clone completes successfully
	// This ensures ClonedRepoPath() returns empty until files are available
	a.clonedRepoPath = tmpDir

	// Update progress: clone complete, starting index
	a.indexStatus.SetProgress(0.3)

	logger.Info(ctx, "✅ Clone complete, indexing with sgrep...")

	// Update sgrep tool with the cloned path
	a.sgrepTool = search.NewSgrepToolWithHome(logger, tmpDir, a.sgrepHome)

	// Index with sgrep (this takes the most time)
	// Run sgrep index and capture output for progress
	indexCmd := exec.CommandContext(ctx, "sgrep", "index", ".")
	indexCmd.Dir = tmpDir
	if strings.TrimSpace(a.sgrepHome) != "" {
		indexCmd.Env = append(os.Environ(), "SGREP_HOME="+a.sgrepHome)
	}

	// Capture output to show progress
	output, err := indexCmd.CombinedOutput()
	if err != nil {
		// Check if error was due to cancellation
		if ctx.Err() != nil {
			return fmt.Errorf("indexing cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("sgrep indexing failed: %w (output: %s)", err, string(output))
	}

	// Log sgrep output
	if len(output) > 0 {
		logger.Info(ctx, "sgrep: %s", strings.TrimSpace(string(output)))
	}

	// Update progress: indexing complete
	a.indexStatus.SetProgress(0.9)

	// Success - don't cleanup
	cleanupOnError = false

	logger.Info(ctx, "🔍 sgrep indexing completed for %s", repoFullName)
	return nil
}

func (a *PRReviewAgent) GetIndexingStatus() *IndexingStatus {
	return a.indexStatus
}

func (a *PRReviewAgent) GetGitHubTools() GitHubInterface {
	return a.githubTools
}

func (a *PRReviewAgent) Metrics(ctx context.Context) MetricsCollector {
	return a.metrics

}

// ClonedRepoPath returns the path to the cloned repository on disk.
// Returns empty string if clone hasn't completed yet.
func (a *PRReviewAgent) ClonedRepoPath() string {
	return a.clonedRepoPath
}

// WaitForClone waits for the repository clone to complete, with a timeout.
// Returns the cloned repo path, or empty string if timeout or clone failed.
func (a *PRReviewAgent) WaitForClone(ctx context.Context, timeout time.Duration) string {
	logger := logging.GetLogger()

	deadline := time.Now().Add(timeout)
	checkInterval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		if a.clonedRepoPath != "" {
			return a.clonedRepoPath
		}

		// Check if indexing failed
		if lastErr := a.indexStatus.GetError(); lastErr != nil {
			logger.Debug(ctx, "Clone/indexing failed: %v", lastErr)
			return ""
		}

		select {
		case <-ctx.Done():
			return ""
		case <-time.After(checkInterval):
			// Continue waiting
		}
	}

	logger.Debug(ctx, "Timeout waiting for clone to complete")
	return ""
}

// extractSearchQuery extracts a meaningful search query from a code chunk.
func extractSearchQuery(chunk string) string {
	lines := strings.Split(chunk, "\n")

	// Find the first meaningful line (not empty, not just a comment)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 10 {
			continue
		}
		// Skip pure comment lines
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		// Found a meaningful line - truncate if too long
		if len(trimmed) > 100 {
			trimmed = trimmed[:100]
		}
		return trimmed
	}

	// Fallback: use truncated chunk content
	if len(chunk) > 100 {
		return chunk[:100]
	}
	return chunk
}

func (a *PRReviewAgent) Close() error {
	logger := logging.GetLogger()
	ctx := context.Background()

	// Stop background processes
	a.stopper.Stop()

	// Clean up cloned repo directory
	if a.clonedRepoPath != "" {
		logger.Debug(ctx, "Cleaning up cloned repository: %s", a.clonedRepoPath)
		if err := os.RemoveAll(a.clonedRepoPath); err != nil {
			logger.Warn(ctx, "Failed to cleanup cloned repo %s: %v", a.clonedRepoPath, err)
		} else {
			logger.Debug(ctx, "Successfully cleaned up cloned repository")
		}
		a.clonedRepoPath = ""
	}

	return nil
}

// ReviewPR reviews a complete pull request.
func (a *PRReviewAgent) ReviewPR(ctx context.Context, prNumber int, tasks []PRReviewTask, console ConsoleInterface) ([]PRReviewComment, error) {
	return a.ReviewPRWithChanges(ctx, prNumber, tasks, console, nil)
}

// ReviewPRWithChanges reviews a complete pull request with pre-fetched changes data.
func (a *PRReviewAgent) ReviewPRWithChanges(ctx context.Context, prNumber int, tasks []PRReviewTask, console ConsoleInterface, preloadedChanges *PRChanges) ([]PRReviewComment, error) {
	logger := logging.GetLogger()
	reviewStart := time.Now()
	logger.Info(ctx, "🎬 Starting PR #%d review for %d files", prNumber, len(tasks))

	// ACE: Start trajectory recording for self-improvement
	if a.aceEnabled && a.aceManager != nil {
		taskQuery := fmt.Sprintf("review PR #%d with %d files", prNumber, len(tasks))
		a.currentTrajectory = a.aceManager.StartTrajectory("pr_review_agent", "code_review", taskQuery)
		if a.currentTrajectory != nil {
			// Record review initiation step
			a.currentTrajectory.RecordStep("review_init", "", fmt.Sprintf("Starting review for PR #%d", prNumber), nil, map[string]any{
				"pr_number":  prNumber,
				"file_count": len(tasks),
			}, nil)
		}
		logger.Debug(ctx, "ACE trajectory started for PR #%d review", prNumber)
	}

	// Reset state for new review to avoid stale data from previous reviews
	a.activeThreads = make(map[int64]*ThreadTracker)
	a.hadExistingComments = false
	// Reset stopper - sync.Once and closed channels don't reset automatically
	a.stopper = NewStopper()

	// Show indexing status to user
	isIndexing, progress, indexErr := a.indexStatus.GetStatus()
	if isIndexing {
		if console.Color() {
			console.Printf("🔄 Repository indexing in progress: %.1f%% complete\n", progress*100)
			console.Printf("💡 Starting review with available data. Quality will improve as indexing completes.\n\n")
		} else {
			console.Printf("Repository indexing in progress: %.1f%% complete\n", progress*100)
			console.Printf("Starting review with available data. Quality will improve as indexing completes.\n\n")
		}
	} else if indexErr != nil {
		console.Printf("⚠️  Background indexing encountered an error: %v\n", indexErr)
		console.Printf("Proceeding with basic review capabilities.\n\n")
	}

	if err := a.processExistingCommentsWithChanges(ctx, prNumber, console, preloadedChanges); err != nil {
		return nil, fmt.Errorf("failed to process existing comments: %w", err)
	}

	a.metrics.StartReviewSession(ctx, prNumber)

	monitorCtx, cancel := context.WithCancel(ctx)
	a.stopper.cancel = cancel

	// Go 1.25: Use wg.Go() for automatic Add/Done management
	a.stopper.Go(func() {
		if err := a.monitorAndRespond(monitorCtx, prNumber, console); err != nil {
			if !errors.Is(err, context.Canceled) {
				console.FileError("monitoring", fmt.Errorf("monitoring error: %w", err))
			}
		}
	})

	var (
		myOpenThreads      []*ThreadTracker // Threads I started that need follow-up
		repliestoMe        []*ThreadTracker // Replies to my comments
		newThreadsByOthers []*ThreadTracker // New threads started by others
	)
	var allComments []PRReviewComment
	logger.Debug(ctx, "🔍 Categorizing %d active threads", len(a.activeThreads))
	for _, thread := range a.activeThreads {
		if thread.OriginalAuthor == a.githubTools.GetAuthenticatedUser(ctx) {
			// This is a thread I started
			if !thread.IsResolved {
				myOpenThreads = append(myOpenThreads, thread)
			}
		} else if thread.LastComment.Author != a.githubTools.GetAuthenticatedUser(ctx) {
			// Someone else made the last comment
			if thread.InReplyToMyComment {
				repliestoMe = append(repliestoMe, thread)
			} else {
				newThreadsByOthers = append(newThreadsByOthers, thread)
			}
		}
	}

	for _, thread := range newThreadsByOthers {
		console.Printf("Generating response to new thread %d (file: %s)\n",
			thread.ThreadID, thread.LastComment.FilePath)

		_, err := a.generateResponse(ctx, thread, console)
		if err != nil {
			console.FileError(thread.LastComment.FilePath,
				fmt.Errorf("failed to generate response: %w", err))
			continue
		}
	}
	logger.Debug(ctx, "📊 Thread categorization: myOpenThreads=%d, repliestoMe=%d, newThreadsByOthers=%d",
		len(myOpenThreads), len(repliestoMe), len(newThreadsByOthers))
	if len(myOpenThreads) == 0 && len(repliestoMe) == 0 {
		if console.Color() {
			msg := "No existing review found, performing initial review"
			if a.hadExistingComments {
				msg = "Existing comments found (no actionable threads), performing initial review"
			}
			console.Printf("%s %s\n", aurora.Cyan("⋮").Bold(), aurora.White(msg).Bold())
		} else {
			if a.hadExistingComments {
				console.Println("⋮ Existing comments found (no actionable threads), performing initial review")
			} else {
				console.Println("⋮ No existing review found, performing initial review")
			}
		}
		initialReviewStart := time.Now()
		comments, err := a.performInitialReview(ctx, tasks, console, preloadedChanges)
		if err != nil {
			return nil, fmt.Errorf("initial review failed: %w", err)
		}
		initialReviewDuration := time.Since(initialReviewStart)
		logger.Info(ctx, "🎯 Initial review completed in %v", initialReviewDuration)
		// Track new threads from initial review
		for _, comment := range comments {
			if comment.ThreadID != nil {

				a.metrics.StartThreadTracking(ctx, comment)
				a.activeThreads[*comment.ThreadID] = &ThreadTracker{
					LastComment:  &comment,
					ReviewChunks: findRelevantChunks(tasks, comment),
					FileContent:  findFileContent(tasks, comment.FilePath),
					LastUpdate:   time.Now(),
					Status:       ThreadOpen, // Initial status for new threads
				}
			}
		}
		allComments = comments
	}

	if len(allComments) == 0 {
		console.Println(aurora.Cyan("\nNo valid comments found need to reply"))
	} else {
		for _, comment := range allComments {
			if comment.ThreadID != nil {
				// Track outdated rate by monitoring thread creation
				a.metrics.TrackNewThread(ctx, *comment.ThreadID, comment)
			}
		}
	}
	cancel()

	totalReviewDuration := time.Since(reviewStart)
	logger.Info(ctx, "🏁 PR #%d review completed in %v | Generated %d comments for %d files",
		prNumber, totalReviewDuration, len(allComments), len(tasks))

	// ACE: End trajectory with outcome based on review results
	if a.aceEnabled && a.aceManager != nil && a.currentTrajectory != nil {
		// Record final step with summary
		a.currentTrajectory.RecordStep("review_complete", "", fmt.Sprintf("Review completed: %d comments generated", len(allComments)), nil, map[string]any{
			"comment_count":   len(allComments),
			"duration_ms":     totalReviewDuration.Milliseconds(),
			"files_reviewed":  len(tasks),
			"threads_created": len(a.activeThreads),
		}, nil)

		// Determine outcome based on review quality
		outcome := ace.OutcomeSuccess
		if len(allComments) == 0 && len(tasks) > 0 {
			// No comments for files with changes might indicate partial success
			outcome = ace.OutcomePartial
		}

		a.aceManager.EndTrajectory(ctx, a.currentTrajectory, outcome)
		logger.Debug(ctx, "ACE trajectory ended for PR #%d with outcome: %v", prNumber, outcome)
		a.currentTrajectory = nil
	}

	return allComments, nil
}

func (a *PRReviewAgent) Stop(ctx context.Context) {
	logger := logging.GetLogger()
	a.stopper.stopOnce.Do(func() {
		if a.stopper.cancel != nil {
			a.stopper.cancel()
		}
		close(a.stopper.stop)

		done := make(chan struct{})
		go func() {
			a.stopper.wg.Wait()
			close(a.stopper.stopped)
			close(done)
		}()

		// Wait with timeout
		select {
		case <-done:
		case <-ctx.Done():
			// Log timeout but continue
			logger.Warn(ctx, "Warning: shutdown timed out")
		}
	})
}

func (a *PRReviewAgent) performInitialReview(ctx context.Context, tasks []PRReviewTask, console ConsoleInterface, changes *PRChanges) ([]PRReviewComment, error) {
	logger := logging.GetLogger()
	totalStart := time.Now()

	// ACE: Inject learnings context if available
	var learningsContext string
	if a.aceEnabled && a.aceManager != nil {
		learningsContext = a.aceManager.LearningsContext()
		if learningsContext != "" {
			logger.Debug(ctx, "ACE: Injecting %d chars of learned strategies into review context", len(learningsContext))
		}
	}

	// Phase 1: Pattern matching across files with bounded concurrency.
	phase1Start := time.Now()
	logger.Info(ctx, "🔍 Phase 1: Starting pattern analysis for %d files", len(tasks))
	repoPatterns, guidelineMatches, err := a.analyzePatterns(ctx, tasks, console)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze patterns: %w", err)
	}
	phase1Duration := time.Since(phase1Start)
	if len(tasks) > 0 {
		logger.Info(ctx, "✅ Phase 1 completed in %v (avg: %v/file)", phase1Duration, phase1Duration/time.Duration(len(tasks)))
	} else {
		logger.Info(ctx, "✅ Phase 1 completed in %v (no files to process)", phase1Duration)
	}

	// ACE: Record pattern analysis phase
	if a.aceEnabled && a.currentTrajectory != nil {
		a.currentTrajectory.RecordStep("pattern_analysis", "", fmt.Sprintf("Analyzed %d files, found %d patterns and %d guidelines", len(tasks), len(repoPatterns), len(guidelineMatches)), nil, map[string]any{
			"file_count":      len(tasks),
			"pattern_count":   len(repoPatterns),
			"guideline_count": len(guidelineMatches),
			"duration_ms":     phase1Duration.Milliseconds(),
		}, nil)
	}

	// Phase 2: Create chunks for all files
	logger.Info(ctx, "🔧 Phase 2: Starting chunk preparation")
	pipeline, err := a.runReviewPipeline(ctx, tasks, changes, console, repoPatterns, guidelineMatches, learningsContext, true)
	if err != nil {
		return nil, err
	}
	phase2Duration := pipeline.Phase2Duration
	phase3Duration := pipeline.Phase3Duration
	phase4Duration := pipeline.Phase4Duration
	phase5Duration := pipeline.Phase5Duration
	phase2TotalChunks := pipeline.TotalChunks
	selectedChunks := pipeline.SelectedChunks
	processedTasks := pipeline.ProcessedTasks
	totalChunks := pipeline.TotalChunks
	comments := pipeline.Comments
	rawCommentCount := pipeline.RawCommentCount
	verifiedCommentCount := pipeline.PreVerificationCount
	skippedAfterFilter := pipeline.SkippedAfterFilter
	totalDuration := time.Since(totalStart)

	logger.Info(ctx, "✅ Phase 2 completed in %v", phase2Duration)

	// ACE: Record chunk preparation phase
	if a.aceEnabled && a.currentTrajectory != nil {
		a.currentTrajectory.RecordStep("chunk_preparation", "", fmt.Sprintf("Prepared %d chunks from %d files", phase2TotalChunks, len(processedTasks)), nil, map[string]any{
			"chunk_count": phase2TotalChunks,
			"file_count":  len(processedTasks),
			"duration_ms": phase2Duration.Milliseconds(),
		}, nil)
	}

	if selectedChunks > 0 {
		logger.Info(ctx, "✅ Phase 3 completed in %v (avg: %v/chunk across %d selected of %d total)", phase3Duration, phase3Duration/time.Duration(selectedChunks), selectedChunks, totalChunks)
	} else {
		logger.Info(ctx, "✅ Phase 3 completed in %v (no chunks to process)", phase3Duration)
	}
	if rawCommentCount != verifiedCommentCount {
		logger.Info(ctx, "🧹 Phase 4 merged %d raw comments into %d candidate findings in %v", rawCommentCount, verifiedCommentCount, phase4Duration)
	} else {
		logger.Info(ctx, "🧹 Phase 4 completed in %v (no merges applied)", phase4Duration)
	}
	if skippedAfterFilter > 0 {
		logger.Info(ctx, "🪓 Phase 4 filtered %d comments outside changed hunks or without a valid line number before verification", skippedAfterFilter)
	}
	if verifiedCommentCount != len(comments) {
		logger.Info(ctx, "🧪 Phase 5 verified %d candidate findings down to %d final comments in %v", verifiedCommentCount, len(comments), phase5Duration)
	} else {
		logger.Info(ctx, "🧪 Phase 5 completed in %v (no findings dropped)", phase5Duration)
	}
	if totalDuration > 0 {
		logger.Info(ctx, "🎉 Total review completed in %v | Phase 1: %v (%.1f%%) | Phase 2: %v (%.1f%%) | Phase 3: %v (%.1f%%) | Phase 4: %v (%.1f%%) | Phase 5: %v (%.1f%%) | Generated %d comments",
			totalDuration,
			phase1Duration, float64(phase1Duration)/float64(totalDuration)*100,
			phase2Duration, float64(phase2Duration)/float64(totalDuration)*100,
			phase3Duration, float64(phase3Duration)/float64(totalDuration)*100,
			phase4Duration, float64(phase4Duration)/float64(totalDuration)*100,
			phase5Duration, float64(phase5Duration)/float64(totalDuration)*100,
			len(comments))
	} else {
		logger.Info(ctx, "🎉 Total review completed instantly | Generated %d comments", len(comments))
	}

	// ACE: Record chunk processing phase
	if a.aceEnabled && a.currentTrajectory != nil {
		a.currentTrajectory.RecordStep("chunk_processing", "", fmt.Sprintf("Processed %d chunks, generated %d comments", totalChunks, len(comments)), nil, map[string]any{
			"chunk_count":              totalChunks,
			"comment_count":            len(comments),
			"raw_comment_count":        rawCommentCount,
			"duration_ms":              phase3Duration.Milliseconds(),
			"post_process_duration_ms": phase4Duration.Milliseconds(),
			"verification_duration_ms": phase5Duration.Milliseconds(),
			"verified_comment_count":   verifiedCommentCount,
			"had_learnings":            learningsContext != "",
		}, nil)
	}

	return comments, nil
}

func (a *PRReviewAgent) runReviewPipeline(ctx context.Context, tasks []PRReviewTask, changes *PRChanges, console ConsoleInterface, repoPatterns []*Content, guidelineMatches []*Content, learningsContext string, verify bool) (*reviewPipelineResult, error) {
	phase2Start := time.Now()
	_, processedTasks, err := a.prepareChunks(ctx, tasks, console)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare chunks: %w", err)
	}
	phase2Duration := time.Since(phase2Start)
	result, err := a.runPreparedReviewPipeline(ctx, processedTasks, changes, console, repoPatterns, guidelineMatches, learningsContext, verify)
	if err != nil {
		return nil, err
	}
	result.Phase2Duration = phase2Duration
	return result, nil
}

func (a *PRReviewAgent) runPreparedReviewPipeline(ctx context.Context, processedTasks []PRReviewTask, changes *PRChanges, console ConsoleInterface, repoPatterns []*Content, guidelineMatches []*Content, learningsContext string, verify bool) (*reviewPipelineResult, error) {
	logger := logging.GetLogger()

	totalChunks := countReviewTaskChunks(processedTasks)
	selectedTasks := selectTasksForChangedHunks(processedTasks, changes, reviewChunkContextRadius)
	selectedChunks := countReviewTaskChunks(selectedTasks)
	if selectedChunks != totalChunks {
		logger.Info(ctx, "🎯 Phase 2.5: Selected %d/%d diff-relevant chunks for review", selectedChunks, totalChunks)
	}

	logger.Info(ctx, "⚡ Phase 3: Starting parallel processing of %d chunks across %d files", selectedChunks, len(selectedTasks))
	phase3Start := time.Now()
	comments, err := a.processChunksParallel(ctx, selectedTasks, repoPatterns, guidelineMatches, console, learningsContext)
	if err != nil {
		return nil, fmt.Errorf("failed to process chunks: %w", err)
	}
	phase3Duration := time.Since(phase3Start)

	phase4Start := time.Now()
	rawCommentCount := len(comments)
	comments = postProcessReviewComments(comments)
	skippedAfterFilter := 0
	var filterDropReasons map[string]int
	var filterRejected []ReviewFilterRejection
	if changes != nil {
		var skippedComments []PRReviewComment
		comments, skippedComments, filterRejected = partitionReviewCommentsByChangesDetailed(comments, changes)
		skippedAfterFilter = len(skippedComments)
		if len(filterRejected) > 0 {
			filterDropReasons = make(map[string]int)
			for _, rejection := range filterRejected {
				filterDropReasons[rejection.ReasonCode]++
			}
		}
	}
	phase4Duration := time.Since(phase4Start)

	preVerificationCount := len(comments)
	phase5Duration := time.Duration(0)
	verificationDropped := 0
	var verificationReasons map[string]int
	var verificationRejected []ReviewVerificationRejection
	if verify {
		phase5Start := time.Now()
		if verifiedComments, verificationReport, verifyErr := a.verifyReviewComments(ctx, comments, selectedTasks, console); verifyErr != nil {
			logger.Warn(ctx, "Review verification skipped after failure: %v", verifyErr)
		} else {
			comments = verifiedComments
			if verificationReport != nil {
				verificationDropped = verificationReport.DroppedCount
				verificationReasons = verificationReport.DropReasons
				verificationRejected = verificationReport.Rejections
			}
		}
		phase5Duration = time.Since(phase5Start)
	}

	return &reviewPipelineResult{
		ProcessedTasks:       selectedTasks,
		Comments:             comments,
		TotalChunks:          totalChunks,
		SelectedChunks:       selectedChunks,
		RawCommentCount:      rawCommentCount,
		PreVerificationCount: preVerificationCount,
		SkippedAfterFilter:   skippedAfterFilter,
		FilterDropReasons:    filterDropReasons,
		FilterRejected:       filterRejected,
		VerificationDropped:  verificationDropped,
		VerificationReasons:  verificationReasons,
		VerificationRejected: verificationRejected,
		Phase3Duration:       phase3Duration,
		Phase4Duration:       phase4Duration,
		Phase5Duration:       phase5Duration,
	}, nil
}

func countReviewTaskChunks(tasks []PRReviewTask) int {
	total := 0
	for _, task := range tasks {
		total += len(task.Chunks)
	}
	return total
}

func selectTasksForChangedHunks(tasks []PRReviewTask, changes *PRChanges, contextRadius int) []PRReviewTask {
	if changes == nil {
		return tasks
	}

	hunksByFile := make(map[string][]ChangeHunk, len(changes.Files))
	for _, file := range changes.Files {
		hunksByFile[file.FilePath] = file.Hunks
	}

	selectedTasks := make([]PRReviewTask, len(tasks))
	for i, task := range tasks {
		selectedTasks[i] = task
		hunks := hunksByFile[task.FilePath]
		if len(task.Chunks) == 0 || len(hunks) == 0 {
			continue
		}
		selectedTasks[i].Chunks = selectChunksForChangedHunks(task.Chunks, hunks, contextRadius)
	}
	return selectedTasks
}

func selectChunksForChangedHunks(chunks []ReviewChunk, hunks []ChangeHunk, contextRadius int) []ReviewChunk {
	if len(chunks) == 0 || len(hunks) == 0 {
		return chunks
	}
	if contextRadius < 0 {
		contextRadius = 0
	}

	selected := make([]bool, len(chunks))
	for i, chunk := range chunks {
		if !chunkIntersectsChangedHunks(chunk, hunks) {
			continue
		}
		start := i - contextRadius
		if start < 0 {
			start = 0
		}
		end := i + contextRadius
		if end >= len(chunks) {
			end = len(chunks) - 1
		}
		for j := start; j <= end; j++ {
			selected[j] = true
		}
	}

	filtered := make([]ReviewChunk, 0, len(chunks))
	for i, keep := range selected {
		if keep {
			filtered = append(filtered, chunks[i])
		}
	}
	if len(filtered) == 0 {
		return chunks
	}
	return filtered
}

func chunkIntersectsChangedHunks(chunk ReviewChunk, hunks []ChangeHunk) bool {
	for _, hunk := range hunks {
		if chunk.EndLine >= hunk.StartLine && chunk.StartLine <= hunk.EndLine {
			return true
		}
	}
	return false
}

func partitionReviewCommentsByChanges(comments []PRReviewComment, changes *PRChanges) ([]PRReviewComment, []PRReviewComment) {
	valid, skipped, _ := partitionReviewCommentsByChangesDetailed(comments, changes)
	return valid, skipped
}

func partitionReviewCommentsByChangesDetailed(comments []PRReviewComment, changes *PRChanges) ([]PRReviewComment, []PRReviewComment, []ReviewFilterRejection) {
	if changes == nil {
		return comments, nil, nil
	}

	lineSlack := types.DefaultCommentHunkLineSlack
	hunksByFile := make(map[string][]ChangeHunk, len(changes.Files))
	for _, file := range changes.Files {
		hunksByFile[file.FilePath] = file.Hunks
	}

	validComments := make([]PRReviewComment, 0, len(comments))
	skippedComments := make([]PRReviewComment, 0)
	filterRejected := make([]ReviewFilterRejection, 0)
	for _, comment := range comments {
		// Preserve general file-level comments. Only line-anchored comments
		// need to be matched back to diff hunks.
		if comment.LineNumber <= 0 {
			validComments = append(validComments, comment)
			continue
		}
		hunks := hunksByFile[comment.FilePath]
		if types.CommentInChangedHunks(comment, hunks, lineSlack) {
			validComments = append(validComments, comment)
			continue
		}
		skippedComments = append(skippedComments, comment)
		filterRejected = append(filterRejected, ReviewFilterRejection{
			FilePath:   comment.FilePath,
			LineNumber: comment.LineNumber,
			ReasonCode: classifyChangedHunkMiss(comment.LineNumber, hunks, lineSlack),
			Content:    strings.TrimSpace(comment.Content),
		})
	}

	return validComments, skippedComments, filterRejected
}

func classifyChangedHunkMiss(lineNumber int, hunks []ChangeHunk, lineSlack int) string {
	if lineNumber <= 0 {
		return "missing_line"
	}
	if len(hunks) == 0 {
		return "missing_hunks"
	}
	if lineSlack < 0 {
		lineSlack = 0
	}
	if lineNumber < hunks[0].StartLine-lineSlack {
		return "before_first_hunk"
	}
	last := hunks[len(hunks)-1]
	if lineNumber > last.EndLine+lineSlack {
		return "after_last_hunk"
	}
	for i := 0; i < len(hunks)-1; i++ {
		if lineNumber > hunks[i].EndLine+lineSlack && lineNumber < hunks[i+1].StartLine-lineSlack {
			return "between_hunks"
		}
	}
	return "outside_changed_hunks"
}

// analyzePatterns keeps the existing pattern matching logic intact.
func (a *PRReviewAgent) analyzePatterns(ctx context.Context, tasks []PRReviewTask, console ConsoleInterface) ([]*Content, []*Content, error) {
	var repoPatterns []*Content
	var guidelineMatches []*Content

	// Check if we have enough indexing for pattern analysis
	if !a.indexStatus.IsReady() {
		progress, isComplete := a.indexStatus.GetProgress()
		if !isComplete {
			if console.Color() {
				console.Printf("⏳ Repository indexing in progress (%.1f%%). Using basic analysis mode...\n", progress*100)
			} else {
				console.Printf("Repository indexing in progress (%.1f%%). Using basic analysis mode...\n", progress*100)
			}
			// Return empty patterns for now, but don't fail
			return []*Content{}, []*Content{}, nil
		}
	}

	if len(tasks) == 0 {
		return repoPatterns, guidelineMatches, nil
	}

	type patternTaskResult struct {
		filePath            string
		repoPatterns        []*Content
		guidelineMatches    []*Content
		repoMatchCount      int
		guidelineMatchCount int
		chunkCount          int
		err                 error
	}

	fileWorkers, chunkWorkers := a.patternAnalysisWorkerCounts(len(tasks))
	workChan := make(chan PRReviewTask, len(tasks))
	resultChan := make(chan patternTaskResult, len(tasks))
	var wg sync.WaitGroup

	for worker := 0; worker < fileWorkers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range workChan {
				repo, guidelines, repoCount, guidelineCount, chunkCount, err := a.analyzePatternTask(ctx, task, chunkWorkers)
				resultChan <- patternTaskResult{
					filePath:            task.FilePath,
					repoPatterns:        repo,
					guidelineMatches:    guidelines,
					repoMatchCount:      repoCount,
					guidelineMatchCount: guidelineCount,
					chunkCount:          chunkCount,
					err:                 err,
				}
			}
		}()
	}

	for _, task := range tasks {
		workChan <- task
	}
	close(workChan)

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	completed := 0
	guidelinesByFile := make(map[string][]*Content, len(tasks))
	for result := range resultChan {
		completed++
		if result.err != nil {
			console.FileError(result.filePath, fmt.Errorf("failed to analyze patterns: %w", result.err))
			continue
		}
		repoPatterns = append(repoPatterns, result.repoPatterns...)
		guidelineMatches = append(guidelineMatches, result.guidelineMatches...)
		if len(result.guidelineMatches) > 0 {
			guidelinesByFile[result.filePath] = result.guidelineMatches
		}
		console.UpdateSpinnerText(fmt.Sprintf("Analyzing patterns... %d/%d (%s)", completed, len(tasks), filepath.Base(result.filePath)))
		if console.Color() {
			console.Printf("%s %s %s %s %s\n",
				aurora.Green("✓").Bold(),
				aurora.White("Analysis complete for").Bold(),
				aurora.Cyan(filepath.Base(result.filePath)).Bold(),
				aurora.White(fmt.Sprintf("found %d repository patterns and %d guideline matches across %d chunks",
					result.repoMatchCount, result.guidelineMatchCount, result.chunkCount)).Bold(),
				aurora.Blue("...").String(),
			)
		} else {
			console.Printf("✓ Analysis complete for %s: found %d repository patterns and %d guideline matches across %d chunks\n",
				filepath.Base(result.filePath), result.repoMatchCount, result.guidelineMatchCount, result.chunkCount)
		}
	}

	for i := range tasks {
		tasks[i].Guidelines = guidelinesByFile[tasks[i].FilePath]
	}

	return repoPatterns, guidelineMatches, nil
}

func (a *PRReviewAgent) patternAnalysisWorkerCounts(taskCount int) (int, int) {
	totalWorkers := runtime.NumCPU()
	if a.workers != nil && a.workers.ReviewWorkers > 0 {
		totalWorkers = a.workers.ReviewWorkers
	}
	if totalWorkers < 1 {
		totalWorkers = 1
	}

	fileWorkers := totalWorkers
	if fileWorkers > taskCount {
		fileWorkers = taskCount
	}
	if fileWorkers > 4 {
		fileWorkers = 4
	}
	if fileWorkers < 1 {
		fileWorkers = 1
	}

	chunkWorkers := totalWorkers / fileWorkers
	if chunkWorkers < 1 {
		chunkWorkers = 1
	}
	if chunkWorkers > 4 {
		chunkWorkers = 4
	}

	return fileWorkers, chunkWorkers
}

func (a *PRReviewAgent) analyzePatternTask(ctx context.Context, task PRReviewTask, chunkWorkers int) ([]*Content, []*Content, int, int, int, error) {
	logger := logging.GetLogger()

	if core.GetTeacherLLM() == nil {
		return nil, nil, 0, 0, 0, nil
	}

	chunks, err := patterns.SplitContentBySize(task.FileContent, 1024)
	if err != nil {
		return nil, nil, 0, 0, 0, fmt.Errorf("failed to split content for %s: %w", task.FilePath, err)
	}

	var allFilePatterns []types.SimpleCodePattern
	seenPatterns := make(map[string]bool)
	extractor := patterns.NewExtractor(logger)

	for _, chunk := range chunks {
		chunkPatterns := extractor.ExtractCodePatterns(ctx, chunk)
		for _, p := range chunkPatterns {
			if !seenPatterns[p.Name] {
				seenPatterns[p.Name] = true
				allFilePatterns = append(allFilePatterns, p)
			}
		}
	}

	logger.Debug(ctx, "File %s: extracted %d unique patterns from %d chunks",
		filepath.Base(task.FilePath), len(allFilePatterns), len(chunks))

	var guidelineMatches []*Content
	if len(allFilePatterns) > 0 && a.guidelineSearch != nil {
		guidelineResults, err := a.guidelineSearch.SearchForPatterns(ctx, allFilePatterns, 10)
		if err != nil {
			logger.Warn(ctx, "Failed to use sgrep guideline search: %v", err)
		} else {
			guidelineMatches = patterns.ConvertToContent(guidelineResults)
			if util.GetEnvBool("MAESTRO_RAG_DEBUG_ENABLED", false) {
				logger.Debug(ctx, "Sgrep guideline search found %d results", len(guidelineResults))
				for i, result := range guidelineResults {
					logger.Debug(ctx, "  %d. %s (score: %.3f, pattern: %s)",
						i+1, result.Content.ID, result.FinalScore, result.Pattern)
				}
			}
		}
	}

	repoPatterns := make([]*Content, 0)
	if a.clonedRepoPath == "" || a.sgrepTool == nil || !a.sgrepTool.IsPathIndexed(ctx, a.clonedRepoPath) {
		return repoPatterns, guidelineMatches, 0, len(guidelineMatches), len(chunks), nil
	}

	numWorkers := chunkWorkers
	if numWorkers > len(chunks) {
		numWorkers = len(chunks)
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	type chunkResult struct {
		patterns []*Content
	}

	workChan := make(chan string, len(chunks))
	resultChan := make(chan chunkResult, len(chunks))
	var wg sync.WaitGroup
	var processedCount atomic.Int32

	for worker := 0; worker < numWorkers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunk := range workChan {
				var foundPatterns []*Content
				query := extractSearchQuery(chunk)
				if query != "" {
					results, err := a.sgrepTool.SearchInPath(ctx, a.clonedRepoPath, query, 5)
					if err != nil {
						if util.GetEnvBool("MAESTRO_RAG_DEBUG_ENABLED", false) {
							logger.Debug(ctx, "Sgrep search failed for chunk: %v", err)
						}
					} else {
						for _, r := range results {
							relevance := 1.0 - r.Score
							if relevance < 0 {
								relevance = 0
							}
							foundPatterns = append(foundPatterns, &Content{
								ID:   fmt.Sprintf("%s:%d-%d", r.FilePath, r.StartLine, r.EndLine),
								Text: r.Content,
								Metadata: map[string]string{
									"file_path":    r.FilePath,
									"start_line":   fmt.Sprintf("%d", r.StartLine),
									"end_line":     fmt.Sprintf("%d", r.EndLine),
									"relevance":    fmt.Sprintf("%.4f", relevance),
									"content_type": "repository",
									"source":       "sgrep",
								},
							})
						}
					}
				}
				resultChan <- chunkResult{patterns: foundPatterns}
				processedCount.Add(1)
			}
		}()
	}

	for _, chunk := range chunks {
		workChan <- chunk
	}
	close(workChan)

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	for result := range resultChan {
		repoPatterns = append(repoPatterns, result.patterns...)
	}

	return repoPatterns, guidelineMatches, len(repoPatterns), len(guidelineMatches), int(processedCount.Load()), nil
}

// prepareChunks handles chunk creation for all files.
func (a *PRReviewAgent) prepareChunks(ctx context.Context, tasks []PRReviewTask, console ConsoleInterface) (map[string]map[string]interface{}, []PRReviewTask, error) {
	fileData := make(map[string]map[string]interface{})
	processedTasks := make([]PRReviewTask, len(tasks))
	copy(processedTasks, tasks)

	for i := range processedTasks {
		task := &processedTasks[i]
		config, err := chunk.NewConfig(
			chunk.WithGenerateDescriptions(false), // Disable expensive LLM calls for chunk descriptions in live reviews
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create chunk config: %w", err)
		}

		config.FileMetadata = map[string]interface{}{
			"file_path": task.FilePath,
			"file_type": filepath.Ext(task.FilePath),
			"package":   filepath.Base(filepath.Dir(task.FilePath)),
		}

		chunks, err := chunk.ChunkFile(ctx, task.FileContent, task.Changes, config)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to chunk file %s: %w", task.FilePath, err)
		}

		task.Chunks = chunks
		fileData[task.FilePath] = map[string]interface{}{
			"file_content": task.FileContent,
			"changes":      task.Changes,
			"chunks":       chunks,
		}
	}

	return fileData, processedTasks, nil
}

func (a *PRReviewAgent) Config() *AgentConfig {
	return a.workers
}

// processChunksParallel handles parallel chunk processing with intelligent optimization.
func (a *PRReviewAgent) processChunksParallel(ctx context.Context, tasks []PRReviewTask, repoPatterns []*Content, guidelineMatches []*Content, console ConsoleInterface, learningsContext string) ([]PRReviewComment, error) {
	// Check if Phase 2.2 Intelligent Parallel Processing is available
	if a.shouldUseIntelligentProcessing() {
		return a.processChunksIntelligent(ctx, tasks, repoPatterns, guidelineMatches, console, learningsContext)
	}

	// Fall back to manual parallel processing
	return a.processChunksManual(ctx, tasks, repoPatterns, guidelineMatches, console, learningsContext)
}

// shouldUseIntelligentProcessing determines if intelligent processing should be used.
func (a *PRReviewAgent) shouldUseIntelligentProcessing() bool {
	features := GetGlobalFeatures()
	if features == nil {
		return false
	}

	// Use intelligent processing if Phase 2 features are enabled
	return features.IntelligentParallelProcessing ||
		features.AdaptiveResourceManagement ||
		features.LoadBalancing
}

// processChunksIntelligent delegates to processChunksManual which uses the high-performance parallel processor.
func (a *PRReviewAgent) processChunksIntelligent(ctx context.Context, tasks []PRReviewTask, repoPatterns []*Content, guidelineMatches []*Content, console ConsoleInterface, learningsContext string) ([]PRReviewComment, error) {
	// Both paths now use the same high-performance modules.Parallel processor
	return a.processChunksManual(ctx, tasks, repoPatterns, guidelineMatches, console, learningsContext)
}

// processChunksManual uses the high-performance parallel processor with modules.Parallel.
func (a *PRReviewAgent) processChunksManual(ctx context.Context, tasks []PRReviewTask, repoPatterns []*Content, guidelineMatches []*Content, console ConsoleInterface, learningsContext string) ([]PRReviewComment, error) {
	logger := logging.GetLogger()

	// Calculate total chunks
	totalChunks := 0
	for _, task := range tasks {
		totalChunks += len(task.Chunks)
	}
	logger.Info(ctx, "🔄 Using parallel processor for %d files (%d chunks)", len(tasks), totalChunks)

	// Build chunk inputs for batch processing
	chunks := make([]map[string]interface{}, 0, totalChunks)
	chunkMeta := make([]struct {
		filePath    string
		chunkIdx    int
		totalInFile int
		startLine   int
	}, 0, totalChunks)

	for _, task := range tasks {
		guidelinesText := buildReviewGuidelinesText(task.Guidelines)
		if learningsContext != "" {
			guidelinesText += "\n\n## Learned Strategies from Past Reviews\n" + learningsContext
		}
		for chunkIdx, chk := range task.Chunks {
			chunkInput := map[string]interface{}{
				"file_path":        task.FilePath,
				"file_content":     chk.Content,
				"changes":          chk.Changes,
				"guidelines":       guidelinesText,
				"leading_context":  chk.LeadingContext,
				"trailing_context": chk.TrailingContext,
				"chunk_start":      chk.StartLine,
				"chunk_end":        chk.EndLine,
			}

			chunks = append(chunks, chunkInput)
			chunkMeta = append(chunkMeta, struct {
				filePath    string
				chunkIdx    int
				totalInFile int
				startLine   int
			}{task.FilePath, chunkIdx, len(task.Chunks), chk.StartLine})
		}
	}

	// Create task context with guidelines and ACE learnings
	taskContext := map[string]interface{}{
		"repo_context":  "Repository patterns and practices",
		"chunk_context": reasoning.ChunkReviewContext(),
	}

	// ACE: Inject learned strategies into review context
	if learningsContext != "" {
		taskContext["ace_learnings"] = learningsContext
	}

	// Process all chunks in parallel using the high-performance processor
	results, err := a.reviewProcessor.ProcessMultipleChunks(ctx, chunks, taskContext)
	if err != nil {
		return nil, fmt.Errorf("parallel processing failed: %w", err)
	}

	// Convert results to comments
	var allComments []PRReviewComment
	for i, result := range results {
		if result == nil {
			continue
		}

		meta := chunkMeta[i]
		if chunkContent, ok := chunks[i]["file_content"].(string); ok {
			result.Issues = filterChunkBoundaryIssues(result.Issues, chunkContent)
		}
		result.Issues = filterLowSignalAdvisoryIssues(result.Issues)
		shiftReviewIssuesToFileLines(result.Issues, meta.startLine)

		// Convert ReviewIssues to PRReviewComments
		startIdx := len(allComments)
		for _, issue := range result.Issues {
			allComments = append(allComments, commentFromReviewIssue(issue))
		}

		// Update console
		if len(result.Issues) == 0 {
			console.NoIssuesFound(meta.filePath, meta.chunkIdx+1, meta.totalInFile)
		} else {
			console.ShowComments(allComments[startIdx:], a.Metrics(ctx))
		}

		// Update progress
		percentage := float64(i+1) / float64(len(results)) * 100
		console.UpdateSpinnerText(fmt.Sprintf("Processing chunks... %.1f%% (%d/%d)", percentage, i+1, len(results)))
	}

	logger.Info(ctx, "✅ Parallel processing completed: %d comments from %d chunks", len(allComments), totalChunks)
	return allComments, nil
}

func commentFromReviewIssue(issue types.ReviewIssue) PRReviewComment {
	return PRReviewComment{
		FilePath:   issue.FilePath,
		LineNumber: issue.LineRange.Start,
		EndLine:    issue.LineRange.End,
		Content:    issue.Description,
		Category:   issue.Category,
		Severity:   issue.Severity,
		Confidence: issue.Confidence,
		Suggestion: issue.Suggestion,
	}
}

func buildReviewGuidelinesText(guidelineMatches []*Content) string {
	const base = "Use Go best practices and project guidelines only to confirm concrete issues in the changed code. Ignore preference-only guidance such as naming, compile-time interface assertions, error-string wording, enum/type-alias refactors, or broad encapsulation advice unless the change introduces a real bug, compatibility problem, or maintenance hazard."

	if len(guidelineMatches) == 0 {
		return base
	}

	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\nOnly use these guideline excerpts when the changed code clearly violates them:\n")

	seen := make(map[string]struct{}, len(guidelineMatches))
	count := 0
	for _, match := range guidelineMatches {
		text := abbreviateGuidelineText(match.Text, 240)
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			continue
		}
		seen[text] = struct{}{}
		sb.WriteString("- ")
		sb.WriteString(text)
		sb.WriteString("\n")
		count++
		if count >= 3 {
			break
		}
	}

	if count == 0 {
		return base
	}

	return strings.TrimSpace(sb.String())
}

func abbreviateGuidelineText(text string, maxLen int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" || maxLen <= 0 {
		return ""
	}
	if len(text) <= maxLen {
		return text
	}

	cut := maxLen
	for cut > maxLen/2 && cut < len(text) && text[cut] != ' ' {
		cut--
	}
	if cut <= maxLen/2 {
		cut = maxLen
	}

	return strings.TrimSpace(text[:cut]) + "..."
}

func shiftReviewIssuesToFileLines(issues []types.ReviewIssue, chunkStartLine int) {
	if chunkStartLine <= 1 {
		return
	}
	lineOffset := chunkStartLine - 1
	for i := range issues {
		if issues[i].LineRange.Start > 0 {
			issues[i].LineRange.Start += lineOffset
		}
		if issues[i].LineRange.End > 0 {
			issues[i].LineRange.End += lineOffset
		}
	}
}

func filterChunkBoundaryIssues(issues []types.ReviewIssue, chunkContent string) []types.ReviewIssue {
	if len(issues) == 0 || chunkContent == "" {
		return issues
	}

	chunkLineCount := 1 + strings.Count(chunkContent, "\n")
	filtered := make([]types.ReviewIssue, 0, len(issues))
	for _, issue := range issues {
		if isChunkBoundarySyntaxArtifact(issue, chunkLineCount) {
			continue
		}
		filtered = append(filtered, issue)
	}
	return filtered
}

func isChunkBoundarySyntaxArtifact(issue types.ReviewIssue, chunkLineCount int) bool {
	if chunkLineCount <= 0 {
		return false
	}

	startLine := issue.LineRange.Start
	endLine := issue.LineRange.End
	if endLine <= 0 {
		endLine = startLine
	}
	if startLine <= 0 && endLine <= 0 {
		return false
	}

	const boundarySlack = 3
	atChunkStart := startLine > 0 && startLine <= boundarySlack
	atChunkEnd := endLine > 0 && endLine >= chunkLineCount-boundarySlack+1
	if !atChunkStart && !atChunkEnd {
		return false
	}

	text := strings.ToLower(issue.Description + "\n" + issue.Suggestion + "\n" + issue.Reasoning)
	for _, marker := range []string{
		"syntax error",
		"invalid go syntax",
		"compile error",
		"compilation error",
		"will not compile",
		"outside of any function",
		"outside a function",
		"within a function body",
		"closing brace",
		"missing brace",
		"unmatched brace",
		"extraneous",
		"incomplete statement",
		"function cannot be closed twice",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}

	return false
}

func filterLowSignalAdvisoryIssues(issues []types.ReviewIssue) []types.ReviewIssue {
	if len(issues) == 0 {
		return issues
	}

	filtered := make([]types.ReviewIssue, 0, len(issues))
	for _, issue := range issues {
		if isLowSignalAdvisoryIssue(issue) {
			continue
		}
		filtered = append(filtered, issue)
	}
	return filtered
}

func isLowSignalAdvisoryIssue(issue types.ReviewIssue) bool {
	category := strings.ToLower(strings.TrimSpace(issue.Category))
	switch category {
	case "bug", "security", "performance", "correctness":
		return false
	}

	text := strings.ToLower(strings.TrimSpace(issue.Description + "\n" + issue.Suggestion + "\n" + issue.Reasoning))
	if text == "" {
		return false
	}
	if containsAny(text, []string{
		"panic",
		"nil pointer",
		"compile error",
		"compilation error",
		"will not compile",
		"syntax error",
		"data race",
		"deadlock",
		"leak",
		"out of bounds",
		"incorrect",
		"wrong result",
		"security",
		"vulnerability",
		"broken",
		"runtime",
	}) {
		return false
	}

	if strings.HasSuffix(strings.ToLower(issue.FilePath), ".md") {
		return containsAny(text, []string{
			"clarify",
			"documentation",
			"confusion",
			"unusual",
			"speculative",
		})
	}

	if containsAny(text, []string{
		"compile-time check",
		"compile-time interface",
		"compile-time verification",
		"interface compliance",
		"implements the interface",
		"var _ ",
		"error message uses 'failed to' prefix",
		"error string uses",
		"error strings should",
		"breaks encapsulation",
		"exposes the underlying",
		"underlying client",
		"enum-like behavior",
		"define a custom type",
		"lacks compile-time type safety",
	}) {
		return true
	}

	if !containsAny(text, []string{
		"not idiomatic",
		"magic number",
		"named constant",
		"consider renaming",
		"rename the",
		"more descriptive",
		"too generic",
		"smaller, more focused interfaces",
		"break this monolithic interface",
		"clear documentation",
		"assertion library",
		"add a test case",
		"consider adding",
		"edge case",
		"readability",
		"maintainability",
		"go naming conventions",
		"hardcoded",
		"could be defined as",
		"reconsider the need for",
		"move the",
		"global constant",
	}) {
		return false
	}

	return containsAny(text, []string{
		"consider",
		"could",
		"would",
		"better practice",
		"improve readability",
		"improve maintainability",
		"more descriptive",
		"clarify",
		"rename",
		"documentation",
	})
}

func containsAny(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func (a *PRReviewAgent) processExistingCommentsWithChanges(ctx context.Context, prNumber int, console ConsoleInterface, preloadedChanges *PRChanges) error {
	logger := logging.GetLogger()
	if console.Color() {
		console.Printf("\n%s %s\n",
			aurora.Blue("↳").Bold(),
			aurora.White("Processing existing comments...").Bold(),
		)
	} else {
		console.Println("\n↳ Processing existing comments...")
	}
	githubTools := a.GetGitHubTools()

	var changes *PRChanges
	var err error

	if preloadedChanges != nil {
		changes = preloadedChanges
	} else {
		changes, err = githubTools.GetPullRequestChanges(ctx, prNumber)
		if err != nil {
			return fmt.Errorf("failed to fetch PR changes: %w", err)
		}
	}
	fileContents := make(map[string]string)
	for _, change := range changes.Files {
		fileContents[change.FilePath] = util.EscapeFileContent(ctx, change.FileContent)
	}
	repoInfo := githubTools.GetRepositoryInfo(ctx)
	comments, _, err := githubTools.ListPullRequestComments(ctx,
		repoInfo.Owner, repoInfo.Name, prNumber,
		&github.PullRequestListCommentsOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch existing comments: %w", err)
	}

	// Also fetch pull request reviews
	reviews, _, err := githubTools.ListPullRequestReviews(ctx,
		repoInfo.Owner, repoInfo.Name, prNumber,
		&github.ListOptions{})
	if err != nil {
		logger.Warn(ctx, "Failed to fetch existing reviews: %v", err)
	}

	logger.Debug(ctx, "Found %d existing review comments, %d reviews", len(comments), len(reviews))
	// Track presence of any existing discussion (including issue comments)
	a.hadExistingComments = (len(comments) + len(reviews)) > 0
	commentsByID := make(map[int64]*github.PullRequestComment)
	threadHistory := make(map[int64][]PRReviewComment)
	// Debug log to see who made the comments
	for _, comment := range comments {
		logger.Debug(ctx, "Comment by user %s on file %s",
			comment.GetUser().GetLogin(),
			comment.GetPath())
	}
	commentMap := make(map[int64]*github.PullRequestComment)
	for _, comment := range comments {
		commentMap[comment.GetID()] = comment
		reviewComment := convertGitHubComment(comment)

		// If this is a reply, add it to the parent thread's history
		if parentID := comment.GetInReplyTo(); parentID != 0 {
			threadHistory[parentID] = append(threadHistory[parentID], reviewComment)
		} else {
			// Start a new thread history
			threadHistory[comment.GetID()] = []PRReviewComment{reviewComment}
		}

		a.metrics.TrackHistoricalComment(ctx, reviewComment)
	}
	for _, comment := range comments {
		// Skip bot comments (CodeCov, CI bots, etc.)
		if isBotComment(comment) {
			logger.Debug(ctx, "Skipping bot comment from %s", comment.GetUser().GetLogin())
			continue
		}

		commentID := comment.GetID()
		parentID := comment.GetInReplyTo()

		filePath := comment.GetPath()

		// Create a thread tracker if it doesn't exist
		if _, exists := a.activeThreads[commentID]; !exists {
			// Convert GitHub comment to our format
			reviewComment := convertGitHubComment(comment)
			reviewComment.Author = comment.GetUser().GetLogin()

			a.metrics.StartThreadTracking(ctx, reviewComment)
			a.activeThreads[commentID] = &ThreadTracker{
				LastComment:     &reviewComment,
				ParentCommentID: parentID,
				LastUpdate:      comment.GetCreatedAt().Time,
				Status:          ThreadOpen,
				FileContent:     fileContents[filePath],

				OriginalAuthor:      comment.GetUser().GetLogin(),
				ConversationHistory: threadHistory[commentID],
				ThreadID:            commentID,
				InReplyToMyComment:  isReplyToMyComment(comment, commentsByID, githubTools.GetAuthenticatedUser(ctx)),
			}
			// If this is a reply, link it to the parent thread
			if parentID != 0 {
				if parentThread, exists := a.activeThreads[parentID]; exists {
					// Update the parent thread with this comment
					parentThread.LastComment = &reviewComment
					parentThread.LastUpdate = comment.GetCreatedAt().Time
				}
			}
		}
	}

	// Process GitHub Reviews (like the one from gemini-code-assist)
	for _, review := range reviews {
		// Skip bot reviews using the same logic
		if isBotReview(review) {
			logger.Debug(ctx, "Skipping bot review from %s", review.GetUser().GetLogin())
			continue
		}

		// Only process reviews with a body (general review comments)
		if review.GetBody() != "" && review.GetState() != "PENDING" {
			logger.Debug(ctx, "Processing review from %s: %s",
				review.GetUser().GetLogin(), review.GetState())
			a.processReview(ctx, review, prNumber, console)
		}
	}

	// Fetch general PR (issue) comments as well – many bots and users comment here rather than as review comments
	issueComments, _, err := githubTools.Client().Issues.ListComments(ctx, repoInfo.Owner, repoInfo.Name, prNumber, &github.IssueListCommentsOptions{})
	if err != nil {
		logger.Warn(ctx, "Failed to fetch PR issue comments: %v", err)
	}

	// Process general PR (issue) comments as separate discussions
	authenticatedUser := githubTools.GetAuthenticatedUser(ctx)
	for _, ic := range issueComments {
		login := ic.GetUser().GetLogin()
		lower := strings.ToLower(login)
		// Skip obvious bots
		if strings.Contains(lower, "bot") || strings.Contains(lower, "codecov") || strings.Contains(lower, "actions") {
			continue
		}
		// Skip our own issue comments - they're not actionable threads for review
		if login == authenticatedUser {
			logger.Debug(ctx, "Skipping own issue comment from %s", login)
			continue
		}
		id := ic.GetID()
		reviewComment := PRReviewComment{
			FilePath:   "",
			LineNumber: 1,
			Content:    ic.GetBody(),
			ThreadID:   &id,
			Timestamp:  ic.GetCreatedAt().Time,
			Author:     login,
			Severity:   "info",
			Category:   "discussion",
		}
		a.metrics.StartThreadTracking(ctx, reviewComment)
		a.activeThreads[id] = &ThreadTracker{
			LastComment:     &reviewComment,
			ParentCommentID: 0,
			LastUpdate:      ic.GetCreatedAt().Time,
			Status:          ThreadOpen,
			FileContent:     "",
			OriginalAuthor:  login,
			ConversationHistory: []PRReviewComment{
				reviewComment,
			},
			ThreadID:           id,
			InReplyToMyComment: false,
		}
		// Flag presence of comments
		a.hadExistingComments = true
	}

	return nil
}

func (a *PRReviewAgent) monitorAndRespond(ctx context.Context, prNumber int, console ConsoleInterface) error {
	githubTools := a.GetGitHubTools()

	return githubTools.MonitorPRComments(ctx, prNumber, func(comment *github.PullRequestComment) {
		// Only process comments from other users
		if comment.GetUser().GetLogin() != githubTools.GetAuthenticatedUser(ctx) {
			a.processComment(ctx, comment, console)
		}
	})

}

// processReview handles the processing of a GitHub review (like from gemini-code-assist).
func (a *PRReviewAgent) processReview(ctx context.Context, review *github.PullRequestReview, prNumber int, console ConsoleInterface) {
	logger := logging.GetLogger()

	// Extract review identifiers
	reviewID := review.GetID()
	reviewBody := review.GetBody()
	reviewState := review.GetState()
	reviewUser := review.GetUser().GetLogin()

	logger.Info(ctx, "Processing review ID: %d from %s, state: %s", reviewID, reviewUser, reviewState)

	// Create a pseudo-comment to represent this review
	reviewComment := PRReviewComment{
		FilePath:    "", // General review comment, not tied to specific file
		LineNumber:  1,  // Use line 1 for general review comments
		Content:     reviewBody,
		Severity:    "info",
		Category:    "review",
		Author:      reviewUser,
		ThreadID:    &reviewID,
		Timestamp:   review.GetSubmittedAt().Time,
		MessageType: "review",
	}

	// Create a thread tracker for this review
	threadStatus := &ThreadTracker{
		LastComment:         &reviewComment,
		LastUpdate:          review.GetSubmittedAt().Time,
		Status:              ThreadOpen,
		ParentCommentID:     0,
		ThreadID:            reviewID,
		InReplyToMyComment:  false,
		ConversationHistory: []PRReviewComment{reviewComment},
		OriginalAuthor:      reviewUser,
		FileContent:         "", // No specific file content for general reviews
	}

	// Store in active threads
	a.activeThreads[reviewID] = threadStatus

	logger.Info(ctx, "Created thread tracker for review ID: %d", reviewID)

	// Generate a response to this review
	console.Printf("Generating response to review %d from %s\n", reviewID, reviewUser)
	response, err := a.generateResponse(ctx, threadStatus, console)
	if err != nil {
		console.FileError("", fmt.Errorf("failed to generate response to review: %w", err))
		return
	}

	// Post the response as a new review
	githubTools := a.GetGitHubTools()
	repoInfo := githubTools.GetRepositoryInfo(ctx)

	reviewRequest := &github.PullRequestReviewRequest{
		Body:  github.Ptr(response.Content),
		Event: github.Ptr("COMMENT"), // Submit as a comment review
	}

	_, _, err = githubTools.CreatePullRequestReviewComment(ctx,
		repoInfo.Owner, repoInfo.Name,
		prNumber, reviewRequest)
	if err != nil {
		console.FileError("", fmt.Errorf("failed to post review response: %v", err))
		return
	}

	logger.Info(ctx, "Successfully posted response to review %d", reviewID)
}

// processComment handles the processing of a single PR comment.
func (a *PRReviewAgent) processComment(ctx context.Context, comment *github.PullRequestComment, console ConsoleInterface) {
	logger := logging.GetLogger()

	// Extract comment identifiers
	commentID := comment.GetID()
	parentID := comment.GetInReplyTo()

	logger.Info(ctx, "Processing comment ID: %d, Parent ID: %d", commentID, parentID)

	var threadStatus *ThreadTracker
	var exists bool

	// Check parent thread first
	if parentID != 0 {
		threadStatus, exists = a.activeThreads[parentID]
	}

	// If no parent thread, check comment thread
	if !exists {
		threadStatus, exists = a.activeThreads[commentID]
	}
	if !exists {
		reviewComment := convertGitHubComment(comment)
		threadStatus = &ThreadTracker{
			LastComment:     &reviewComment,
			LastUpdate:      comment.GetCreatedAt().Time,
			Status:          ThreadOpen,
			ParentCommentID: parentID,
		}
		a.activeThreads[commentID] = threadStatus
		logger.Info(ctx, "Created new thread tracker for comment ID: %d", commentID)
	}
	if err := a.refreshThreadContent(ctx, threadStatus); err != nil {
		logger.Error(ctx, "Failed to get file content: %v", err)
		return
	}

	// Prepare context for response generation
	responseContext := map[string]interface{}{
		"original_comment": threadStatus.LastComment.Content,
		"thread_context":   []PRReviewComment{*threadStatus.LastComment},
		"file_content":     threadStatus.FileContent,
		"file_path":        threadStatus.LastComment.FilePath,
		"line_number":      threadStatus.LastComment.LineNumber,
		"thread_id":        threadStatus.LastComment.ThreadID,
		"in_reply_to":      commentID,
		"category":         threadStatus.LastComment.Category,
	}

	// Generate response using declarative workflow
	var result *agents.OrchestratorResult
	var err error

	if a.declarativeChain != nil {
		logger.Info(ctx, "🏗️ Using declarative workflow for response generation")
		result, err = a.generateResponseWithDeclarativeWorkflow(ctx, responseContext)
	} else {
		err = fmt.Errorf("declarative workflow not initialized for response generation")
	}

	if err != nil {
		console.FileError(threadStatus.LastComment.FilePath,
			fmt.Errorf("failed to generate response: %w", err))
		return
	}

	// Process the orchestrator result
	response, err := handleOrchestratorResult(result, threadStatus.LastComment.LineNumber)
	if err != nil {
		console.FileError(threadStatus.LastComment.FilePath,
			fmt.Errorf("failed to process response: %w", err))
		return
	}

	// Update thread status
	threadStatus.LastComment = response
	threadStatus.LastUpdate = time.Now()
	threadStatus.ParentCommentID = parentID

	// Update thread trackers
	a.activeThreads[commentID] = threadStatus
	if parentID != 0 {
		a.activeThreads[parentID] = threadStatus
	}

	// Post the response if needed
	if response.ThreadID != nil {
		githubTools := a.GetGitHubTools()
		err = githubTools.CreateReviewComments(ctx,
			int(comment.GetPullRequestReviewID()),
			[]PRReviewComment{*response})
		if err != nil {
			console.FileError(response.FilePath,
				fmt.Errorf("failed to post response: %v", err))
		}
	}
}
func (a *PRReviewAgent) generateResponse(ctx context.Context, thread *ThreadTracker, console ConsoleInterface) (*PRReviewComment, error) {
	logger := logging.GetLogger()
	console.Println(aurora.Cyan("Generating response..."))
	// Only try to refresh file content if there's a file path (skip PR-level comments)
	if thread.FileContent == "" && thread.LastComment.FilePath != "" {
		if err := a.refreshThreadContent(ctx, thread); err != nil {
			logger.Warn(ctx, "Could not refresh file content for %s: %v",
				thread.LastComment.FilePath, err)
		}
	}
	if thread.LastComment.LineNumber == 0 {
		logger.Warn(ctx, "Missing line number in thread %d", thread.ThreadID)
		// Try to recover line number from conversation history
		for _, comment := range thread.ConversationHistory {
			if comment.LineNumber > 0 {
				thread.LastComment.LineNumber = comment.LineNumber
				break
			}
		}
	}
	responseContext := map[string]interface{}{
		"processor_type":   "comment_response",
		"task_type":        "comment_response",
		"original_comment": thread.LastComment.Content,
		"thread_context":   []PRReviewComment{*thread.LastComment},
		"file_content":     thread.FileContent,
		"file_path":        thread.LastComment.FilePath,
		"line_number":      float64(thread.LastComment.LineNumber),
		"thread_id":        thread.LastComment.ThreadID,
		"category":         thread.LastComment.Category,
	}

	logger.Info(ctx, "Generating response for comment in file %s at line %d",
		thread.LastComment.FilePath, thread.LastComment.LineNumber)

	msg := fmt.Sprintf("Generating response for comment in file %s at line %d",
		thread.LastComment.FilePath, thread.LastComment.LineNumber)

	var result *agents.OrchestratorResult
	err := console.WithSpinner(ctx, msg, func() error {
		var processErr error

		if a.declarativeChain != nil {
			logger.Info(ctx, "🏗️ Using declarative workflow for response generation in generateResponse()")
			result, processErr = a.generateResponseWithDeclarativeWorkflow(ctx, responseContext)
		} else {
			processErr = fmt.Errorf("declarative workflow not initialized for response generation")
		}

		return processErr
	})

	if err != nil {
		return nil, err
	}

	response, err := handleOrchestratorResult(result, thread.LastComment.LineNumber)
	if err != nil {
		return nil, err
	}

	// Set the InReplyTo field to maintain the thread
	response.InReplyTo = thread.LastComment.ThreadID
	response.ThreadID = thread.LastComment.ThreadID
	response.MessageType = "response"
	response.FilePath = thread.LastComment.FilePath
	response.LineNumber = thread.LastComment.LineNumber

	return response, nil
}

func (a *PRReviewAgent) refreshThreadContent(ctx context.Context, thread *ThreadTracker) error {
	if thread.FileContent == "" {
		// Fetch current file content
		content, err := a.githubTools.GetFileContent(ctx, thread.LastComment.FilePath)
		if err != nil {
			return fmt.Errorf("failed to refresh file content for %s: %w",
				thread.LastComment.FilePath, err)
		}
		thread.FileContent = content
		logging.GetLogger().Info(ctx, "Successfully refreshed content for file: %s",
			thread.LastComment.FilePath)
	}
	return nil
}

// findRelevantChunks locates the code chunks that are relevant to a specific comment.
func findRelevantChunks(tasks []PRReviewTask, comment PRReviewComment) []ReviewChunk {
	var relevantChunks []ReviewChunk

	// Find the task containing the file
	for _, task := range tasks {
		if task.FilePath == comment.FilePath {
			// Look through chunks to find those containing the comment line
			for _, chunk := range task.Chunks {
				if chunk.StartLine <= comment.LineNumber && chunk.EndLine >= comment.LineNumber {
					relevantChunks = append(relevantChunks, chunk)
				}
			}
			break
		}
	}

	return relevantChunks
}

// findFileContent retrieves the full content of a specific file from the tasks.
func findFileContent(tasks []PRReviewTask, filePath string) string {
	for _, task := range tasks {
		if task.FilePath == filePath {
			return task.FileContent
		}
	}
	return ""
}

func handleOrchestratorResult(result *agents.OrchestratorResult, originalLineNumber int) (*PRReviewComment, error) {
	logger := logging.GetLogger()
	// Look for completed tasks
	for _, taskResult := range result.CompletedTasks {

		logger.Info(context.Background(), "Processing taskResult of type: %T", taskResult)
		logger.Info(context.Background(), "TaskResult content: %+v", taskResult)
		// Try to convert the task result to a PRReviewComment
		if comment, ok := taskResult.(PRReviewComment); ok {
			logger.Info(context.Background(), "Successfully converted to PRReviewComment: %+v", comment)
			if !isValidComment(comment) {
				logger.Info(context.Background(), "Comment failed validation: LineNumber=%d, Content='%s', Severity='%s', Category='%s'",
					comment.LineNumber, comment.Content, comment.Severity, comment.Category)
				continue
			}
			if comment.LineNumber == 0 {
				comment.LineNumber = originalLineNumber
			}
			return &comment, nil
		}

		// If it's a map, try to construct a PRReviewComment
		if resultMap, ok := taskResult.(map[string]interface{}); ok {
			comment := &PRReviewComment{
				LineNumber: originalLineNumber,
			}

			// Extract fields from the map
			if content, ok := resultMap["content"].(string); ok {
				comment.Content = content
			}
			if severity, ok := resultMap["severity"].(string); ok {
				comment.Severity = severity
			}
			if suggestion, ok := resultMap["suggestion"].(string); ok {
				comment.Suggestion = suggestion
			}
			if category, ok := resultMap["category"].(string); ok {
				comment.Category = category
			}
			if lineNumber, ok := resultMap["line_number"].(int); ok {
				comment.LineNumber = lineNumber
			}
			if !isValidComment(*comment) {
				logger.Info(context.Background(), "Constructed comment failed validation: LineNumber=%d, Content='%s', Severity='%s', Category='%s'",
					comment.LineNumber, comment.Content, comment.Severity, comment.Category)
				continue
			}

			return comment, nil
		}
	}

	return nil, fmt.Errorf("no valid review comment found in orchestrator result")
}

func convertGitHubComment(comment *github.PullRequestComment) PRReviewComment {
	return PRReviewComment{
		FilePath:   comment.GetPath(),
		LineNumber: comment.GetLine(),
		Content:    comment.GetBody(),
		ThreadID:   github.Ptr(comment.GetID()),
		InReplyTo:  github.Ptr(comment.GetInReplyTo()),
		Timestamp:  comment.GetCreatedAt().Time,
		Author:     comment.GetUser().GetLogin(),
	}
}

// isBotComment checks if a comment is from a known bot.
func isBotComment(comment *github.PullRequestComment) bool {
	if comment == nil || comment.GetUser() == nil {
		return false
	}

	userLogin := strings.ToLower(comment.GetUser().GetLogin())

	// Known bot patterns
	botPatterns := []string{
		"codecov",
		"dependabot",
		"renovate",
		"github-actions",
		"greenkeeper",
		"snyk-bot",
		"sonarcloud",
		"sonarqube",
		"codeclimate",
		"coveralls",
		"circleci",
		"travis-ci",
		"jenkins",
		"azure-pipelines",
	}

	for _, pattern := range botPatterns {
		if strings.Contains(userLogin, pattern) {
			return true
		}
	}

	// Check if user type is "Bot"
	if comment.GetUser().GetType() == "Bot" {
		return true
	}

	return false
}

// isBotReview checks if a review is from a known bot (similar to isBotComment).
func isBotReview(review *github.PullRequestReview) bool {
	if review == nil || review.GetUser() == nil {
		return false
	}

	userLogin := strings.ToLower(review.GetUser().GetLogin())

	// Use same bot patterns as comments
	botPatterns := []string{
		"codecov",
		"dependabot",
		"renovate",
		"github-actions",
		"greenkeeper",
		"snyk-bot",
		"sonarcloud",
		"sonarqube",
		"codeclimate",
		"coveralls",
		"circleci",
		"travis-ci",
		"jenkins",
		"azure-pipelines",
	}

	for _, pattern := range botPatterns {
		if strings.Contains(userLogin, pattern) {
			return true
		}
	}

	// Check if user type is "Bot"
	if review.GetUser().GetType() == "Bot" {
		return true
	}

	return false
}

func isReplyToMyComment(comment *github.PullRequestComment,
	commentMap map[int64]*github.PullRequestComment,
	botUser string) bool {

	parentID := comment.GetInReplyTo()
	if parentID == 0 {
		return false
	}

	if parent, exists := commentMap[parentID]; exists {
		return parent.GetUser().GetLogin() == botUser
	}
	return false
}

// defaultAgentConfig provides sensible defaults for the agent configuration.
func defaultAgentConfig() *AgentConfig {
	return &AgentConfig{
		IndexWorkers:  runtime.NumCPU(), // Default to CPU count for indexing
		ReviewWorkers: runtime.NumCPU(), // Default to CPU count for review
	}
}
