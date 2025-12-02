package review

import (
	"context"
	"database/sql"
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
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/agent"
	"github.com/XiaoConstantine/maestro/internal/chunk"
	"github.com/XiaoConstantine/maestro/internal/metrics"
	"github.com/XiaoConstantine/maestro/internal/rag"
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
	orchestrator     *agents.FlexibleOrchestrator // Legacy orchestrator
	declarativeChain *workflow.DeclarativeReviewChain // New declarative workflow
	memory           agents.Memory
	rag              types.RAGStore
	activeThreads    map[int64]*ThreadTracker // Track active discussion threads
	// TODO: should align with dspy agent interface
	githubTools         types.GitHubInterface // Add this field
	stopper             *Stopper
	metrics             types.MetricsCollector
	workers             *types.AgentConfig
	indexStatus         *types.IndexingStatus // Track background indexing progress
	hadExistingComments bool            // Track if any comments/reviews exist (including bots)
	clonedRepoPath      string          // Path to cloned repo in /tmp for sgrep indexing
	sgrepTool           *search.SgrepTool      // Sgrep tool for semantic search
}

type ThreadTracker struct {
	LastComment  *types.PRReviewComment
	ReviewChunks []types.ReviewChunk
	FileContent  string
	LastUpdate   time.Time
	Status       types.ThreadStatus // Using our existing ThreadStatus type

	ParentCommentID     int64
	OriginalAuthor      string // Who started the thread
	ThreadID            int64
	InReplyToMyComment  bool              // Whether this is a reply to our comment
	IsResolved          bool              // Whether the thread is resolved
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

	logger.Debug(ctx, "Starting agent initialization with dbPath: %s", dbPath)
	if config == nil {
		config = defaultAgentConfig()
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize sqlite db: %v", err)
	}

	logger.Debug(ctx, "Successfully opened database")

	var store RAGStore
	// Use traditional RAG system with SQLite
	logger.Debug(ctx, "Using traditional RAG system")
	var ragErr error
	dataDir := filepath.Dir(dbPath)
	store, ragErr = rag.NewSQLiteRAGStore(db, logger, dataDir)
	if ragErr != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize rag store: %v", ragErr)
	}

	// Populate guidelines asynchronously to avoid blocking TUI startup
	// Detect primary language via GitHub API; default to Go
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Warn(ctx, "guideline population panic recovered: %v", r)
			}
		}()
		repo := githubTool.GetRepositoryInfo(ctx)
		langs, _, err := githubTool.ListLanguages(ctx, repo.Owner, repo.Name)
		primary := "go"
		if err == nil && len(langs) > 0 {
			max := 0
			for lang, count := range langs {
				if count > max {
					max = count
					primary = strings.ToLower(lang)
				}
			}
		}
		if err := store.PopulateGuidelinesBackground(ctx, primary); err != nil {
			logger.Warn(ctx, "Failed to populate guidelines for %s: %v", primary, err)
		} else {
			logger.Info(ctx, "Guidelines populated for language: %s", primary)
		}
	}()

	metricsCollector := metrics.NewBusinessMetrics(logger)

	metricsCollector.StartOptimizationCycle(ctx)
	logger.Debug(ctx, "Successfully created RAG store")

	// Create agent components immediately - don't wait for indexing
	memory := agents.NewInMemoryStore()
	stopper := NewStopper()
	indexStatus := types.NewIndexingStatus()

	logger.Debug(ctx, "Creating orchestrator configuration")
	analyzerConfig := agents.AnalyzerConfig{
		BaseInstruction: `Analyze the input and determine the appropriate task type:
		First, examine if this is new code or follow-up interaction:

		For new code review:
		- Create a review_chain task to handle both issue detection and validation
		- Only after review_chain completes successfully, create a code_review task

		For interaction with existing reviews:
		- Create a comment_response task when processing replies to previous comments

		For repository exploration:
		- Create a repo_qa task when answering questions about code patterns


		IMPORTANT FORMAT RULES:
                0.Always ensure review_chain task completes before creating code_review/comment_response tasks.
		1. Start fields exactly with 'analysis:' or 'tasks:' (no markdown formatting)
		2. Provide raw XML directly after 'tasks:' without any wrapping
		3. Keep the exact field prefix format - no decorations or modifications
		4. Ensure proper indentation and structure in the XML
		5. When thread_id is present in the context, always create a comment_response task.
		6. FOR ALL TEXT CONTENT IN THE XML (including description, metadata items like file_content, original_comment, etc.):
		- Before inserting text into the XML, replace the following special characters to ensure well-formed XML:
			* Replace every '&' with '&amp;'
			* Replace every '<' with '&lt;'
			* Replace every '>' with '&gt;'
		- This applies to all fields that contain text content, such as description, file_content, original_comment, etc.
		- For attribute values (if any), additionally replace '"' with '&quot;' if the attribute is enclosed in double quotes, or ''' with '&apos;' if enclosed in single quotes.
			- **Examples:**
			* If file_content is:
				if (a < b && b > c) { ... }
			Then in the XML, it should be:
				<item key="file_content">if (a < b && b > c) { ... }</item>
			* If description is:
			Check for errors & warnings in <file>.xml
			Then in the XML, it should be:
				<description>Check for errors & warnings in <file>.xml</description>
			- **Important Notes:**
			* Always replace '&' with '&amp;', even if it appears in code or text (e.g., '&&' should become '&amp;&amp;').
			* Failing to escape these characters will result in invalid XML that cannot be parsed.
		`,

		FormatInstructions: `Format tasks section in following XML format:
	   <tasks>
	       <task id="1" type="{task_type}" processor="{task_type}" priority="1">
	           <description>Review {file_path} for code quality</description>
	           <metadata>
	               <item key="file_path">{file_path}</item>
	               <item key="file_content">{file_content}</item>
	               <item key="changes">{changes}</item>
	               <item key="category">{category}</item>
                       <item key="original_comment">{original_comment}</item>
                       <item key="thread_id">{thread_id}</item>
		       <item key="line_range">{line_range}</item>
		       <item key="chunk_start">{chunk_start}</item>
		       <item key="chunk_end">{chunk_end}</item>
                       <item key="chunk_number">{chunk_number}</item>
                       <item key="total_chunks">{total_chunks}</item>
			<!-- Additional metadata -->
	           </metadata>
	       </task>
	   </tasks>`,
	}

	qaProcessor := agent.NewRepoQAProcessor(store)
	streamHandler := util.CreateStreamHandler(ctx, logger)
	orchConfig := agents.OrchestrationConfig{
		MaxConcurrent:  5,
		TaskParser:     &agents.XMLTaskParser{},
		PlanCreator:    &agents.DependencyPlanCreator{},
		AnalyzerConfig: analyzerConfig,
		RetryConfig: &agents.RetryConfig{
			MaxAttempts:       5,
			BackoffMultiplier: 2.0,
		},
		CustomProcessors: map[string]agents.TaskProcessor{
			"code_review":      &CodeReviewProcessor{metrics: metricsCollector},
			"comment_response": &CommentResponseProcessor{metrics: metricsCollector},
			"repo_qa":          qaProcessor,
			"review_chain":     NewReviewChainProcessor(ctx, metricsCollector, logger),
		},
		Options: core.WithOptions(
			core.WithGenerateOptions(
				core.WithTemperature(0.3),
				core.WithMaxTokens(8192),
			),
			core.WithStreamHandler(streamHandler),
		),
	}

	orchestrator := agents.NewFlexibleOrchestrator(memory, orchConfig)

	logger.Debug(ctx, "Successfully created orchestrator")

	// Initialize declarative workflow if Phase 2 features are enabled
	var declarativeChain *workflow.DeclarativeReviewChain
	if shouldUseDeclarativeWorkflows() {
		logger.Debug(ctx, "🏗️ Initializing Declarative Workflow Builder")
		logger.Debug(ctx, "📋 Declarative workflow features: retry logic, parallel validation, conditional refinement")
		declarativeChain = workflow.NewDeclarativeReviewChain(ctx, nil, nil, nil)
		logger.Debug(ctx, "✅ Declarative Workflow initialized successfully")
	} else {
		logger.Debug(ctx, "⚪ Declarative workflows disabled - using legacy orchestrator")
	}

	agent := &PRReviewAgent{
		orchestrator:     orchestrator,
		declarativeChain: declarativeChain,
		memory:           memory,
		rag:              store,
		githubTools:      githubTool,
		stopper:          stopper,
		metrics:          metricsCollector,
		workers:          config,
		indexStatus:      indexStatus,
	}

	// Start background indexing AFTER agent creation
	logger.Debug(ctx, "🚀 Agent ready! Starting background repository indexing...")
	go agent.startBackgroundIndexing(ctx, githubTool, store, dbPath, config.IndexWorkers)

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
func (a *PRReviewAgent) processChunkWithDeclarativeWorkflow(ctx context.Context, chunkContext map[string]interface{}) (*agents.OrchestratorResult, error) {
	if a.declarativeChain == nil {
		return nil, fmt.Errorf("declarative workflow not initialized")
	}

	logger := logging.GetLogger()
	filePath, _ := chunkContext["file_path"].(string)
	chunkNum, _ := chunkContext["chunk_number"].(int)

	logger.Info(ctx, "🏗️ Processing chunk %d with Phase 2.3 Declarative Workflow: %s", chunkNum, filePath)

	// Create task for declarative workflow with enhanced metadata
	task := agents.Task{
		ID:       fmt.Sprintf("declarative_chunk_%s_%d_%d", filePath, chunkNum, time.Now().UnixNano()),
		Type:     "code_review",
		Metadata: chunkContext,
		Priority: 1,
	}

	// Add declarative workflow context
	declarativeContext := make(map[string]interface{})
	for k, v := range chunkContext {
		declarativeContext[k] = v
	}
	declarativeContext["declarative_processing"] = true
	declarativeContext["workflow_version"] = "2.3"
	declarativeContext["processing_mode"] = "declarative_chunk_review"

	// Process with declarative workflow
	startTime := time.Now()
	result, err := a.declarativeChain.Process(ctx, task, declarativeContext)
	processingDuration := time.Since(startTime)

	if err != nil {
		logger.Error(ctx, "❌ Phase 2.3 Declarative workflow failed for chunk %d of %s after %v: %v",
			chunkNum, filePath, processingDuration, err)
		return nil, err
	}

	logger.Info(ctx, "✅ Phase 2.3 Declarative workflow completed chunk %d of %s in %v",
		chunkNum, filePath, processingDuration)

	// Track declarative workflow usage
	if globalMetrics != nil {
		globalMetrics.TrackFeatureUsage(GetGlobalFeatures(), "declarative_workflows")
	}

	// Convert declarative result to orchestrator result format
	return a.convertDeclarativeToOrchestratorResult(result), nil
}

// generateResponseWithDeclarativeWorkflow generates a response using declarative workflow.
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

func (a *PRReviewAgent) startBackgroundIndexing(ctx context.Context, githubTool GitHubInterface, store RAGStore, dbPath string, workers int) {
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

	// Check if sgrep is available
	sgrepTool := search.NewSgrepTool(logger, "")
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

	logger.Info(ctx, "📦 Cloning %s to %s", repoFullName, tmpDir)

	// Clone using gh CLI
	args := []string{"repo", "clone", repoFullName, tmpDir}
	if branch != "" {
		args = append(args, "--", "-b", branch)
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to clone repo: %w (output: %s)", err, string(output))
	}

	// Only set clonedRepoPath AFTER clone completes successfully
	// This ensures ClonedRepoPath() returns empty until files are available
	a.clonedRepoPath = tmpDir

	// Update progress: clone complete, starting index
	a.indexStatus.SetProgress(0.3)

	logger.Info(ctx, "✅ Clone complete, indexing with sgrep...")

	// Update sgrep tool with the cloned path
	a.sgrepTool = search.NewSgrepTool(logger, tmpDir)

	// Index with sgrep (this takes the most time)
	// Run sgrep index and capture output for progress
	indexCmd := exec.CommandContext(ctx, "sgrep", "index", ".")
	indexCmd.Dir = tmpDir

	// Capture output to show progress
	output, err := indexCmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("sgrep indexing failed: %w (output: %s)", err, string(output))
	}

	// Log sgrep output
	if len(output) > 0 {
		logger.Info(ctx, "sgrep: %s", strings.TrimSpace(string(output)))
	}

	// Update progress: indexing complete
	a.indexStatus.SetProgress(0.9)

	logger.Info(ctx, "🔍 sgrep indexing completed for %s", repoFullName)
	return nil
}

func (a *PRReviewAgent) GetIndexingStatus() *IndexingStatus {
	return a.indexStatus
}

func (a *PRReviewAgent) GetGitHubTools() GitHubInterface {
	if a.githubTools == nil {
		panic("GitHub tools not initialized")
	}
	return a.githubTools
}

func (a *PRReviewAgent) Orchestrator(ctx context.Context) *agents.FlexibleOrchestrator {
	if a.orchestrator == nil {
		panic("Agent orchestrator not initialized")
	}
	return a.orchestrator

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

func (a *PRReviewAgent) Close() error {
	// Clean up cloned repo directory
	if a.clonedRepoPath != "" {
		os.RemoveAll(a.clonedRepoPath)
	}

	if a.rag != nil {
		return a.rag.Close()
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

	a.stopper.wg.Add(1)
	monitorCtx, cancel := context.WithCancel(ctx)

	a.stopper.cancel = cancel

	go func() {
		defer a.stopper.wg.Done()
		if err := a.monitorAndRespond(monitorCtx, prNumber, console); err != nil {
			if !errors.Is(err, context.Canceled) {
				console.FileError("monitoring", fmt.Errorf("monitoring error: %w", err))
			}
		}
	}()

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
		comments, err := a.performInitialReview(ctx, tasks, console)
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

func (a *PRReviewAgent) performInitialReview(ctx context.Context, tasks []PRReviewTask, console ConsoleInterface) ([]PRReviewComment, error) {
	logger := logging.GetLogger()
	totalStart := time.Now()

	// Phase 1: Pattern Matching - Keep this sequential as it's file-level analysis
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

	// Phase 2: Create chunks for all files
	phase2Start := time.Now()
	logger.Info(ctx, "🔧 Phase 2: Starting chunk preparation")
	_, processedTasks, err := a.prepareChunks(ctx, tasks, console)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare chunks: %w", err)
	}
	phase2Duration := time.Since(phase2Start)
	logger.Info(ctx, "✅ Phase 2 completed in %v", phase2Duration)

	// Phase 3: Parallel chunk processing
	phase3Start := time.Now()
	totalChunks := 0
	for _, task := range processedTasks {
		totalChunks += len(task.Chunks)
	}
	logger.Info(ctx, "⚡ Phase 3: Starting parallel processing of %d chunks across %d files", totalChunks, len(processedTasks))
	comments, err := a.processChunksParallel(ctx, processedTasks, repoPatterns, guidelineMatches, console)
	if err != nil {
		return nil, fmt.Errorf("failed to process chunks: %w", err)
	}
	phase3Duration := time.Since(phase3Start)
	totalDuration := time.Since(totalStart)

	if totalChunks > 0 {
		logger.Info(ctx, "✅ Phase 3 completed in %v (avg: %v/chunk)", phase3Duration, phase3Duration/time.Duration(totalChunks))
	} else {
		logger.Info(ctx, "✅ Phase 3 completed in %v (no chunks to process)", phase3Duration)
	}
	if totalDuration > 0 {
		logger.Info(ctx, "🎉 Total review completed in %v | Phase 1: %v (%.1f%%) | Phase 2: %v (%.1f%%) | Phase 3: %v (%.1f%%) | Generated %d comments",
			totalDuration,
			phase1Duration, float64(phase1Duration)/float64(totalDuration)*100,
			phase2Duration, float64(phase2Duration)/float64(totalDuration)*100,
			phase3Duration, float64(phase3Duration)/float64(totalDuration)*100,
			len(comments))
	} else {
		logger.Info(ctx, "🎉 Total review completed instantly | Generated %d comments", len(comments))
	}

	return comments, nil
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

	for _, task := range tasks {

		// Create embedding for the entire file to find similar patterns
		llm := core.GetTeacherLLM()
		if llm == nil {
			// Skip pattern analysis if teacher LLM is not configured
			continue
		}

		chunks, err := rag.SplitContentForEmbedding(task.FileContent, 1024) // Keep under 10KB limit
		if err != nil {
			return repoPatterns, guidelineMatches, fmt.Errorf("failed to split content for %s: %w", task.FilePath, err)
		}
		message := fmt.Sprintf("Processing %s (%d chunks)...", filepath.Base(task.FilePath), len(chunks))

		var totalRepoMatches, totalGuidelineMatches int
		logger := logging.GetLogger()
		
		// Phase 1: Extract patterns from all chunks upfront for file-level deduplication
		// This avoids redundant guideline searches for the same patterns across chunks
		var allFilePatterns []types.SimpleCodePattern
		seenPatterns := make(map[string]bool)
		// Use empty dataDir since ExtractCodePatterns doesn't need guidelines directory
		enhancer := rag.NewGuidelineSearchEnhancer(logger, "")
		
		for _, chunk := range chunks {
			chunkPatterns := enhancer.ExtractCodePatterns(ctx, chunk)
			for _, p := range chunkPatterns {
				if !seenPatterns[p.Name] {
					seenPatterns[p.Name] = true
					allFilePatterns = append(allFilePatterns, p)
				}
			}
		}
		
		logger.Debug(ctx, "File %s: extracted %d unique patterns from %d chunks", 
			filepath.Base(task.FilePath), len(allFilePatterns), len(chunks))
		
		// Phase 2: Do single guideline search for all deduplicated patterns
		var fileGuidelineMatches []*Content
		if len(allFilePatterns) > 0 {
			guidelineResults, err := a.rag.FindRelevantGuidelines(ctx, allFilePatterns, 10)
			if err != nil {
				logger.Warn(ctx, "Failed to use pattern-based guideline search: %v", err)
			} else {
				fileGuidelineMatches = rag.ConvertToContent(guidelineResults)

				if util.GetEnvBool("MAESTRO_RAG_DEBUG_ENABLED", false) {
					logger.Debug(ctx, "File-level pattern search found %d results", len(guidelineResults))
					for i, result := range guidelineResults {
						logger.Debug(ctx, "  %d. %s (score: %.3f, pattern: %s)",
							i+1, result.Content.ID, result.FinalScore, result.Pattern)
					}
				}
			}
		}

		err = console.WithSpinner(ctx, message, func() error {
			// Parallel chunk processing with worker pool
			numWorkers := runtime.NumCPU()
			if numWorkers > len(chunks) {
				numWorkers = len(chunks)
			}
			if numWorkers < 1 {
				numWorkers = 1
			}

			type chunkWork struct {
				index int
				chunk string
			}
			type chunkResult struct {
				patterns []*Content
				err      error
			}

			workChan := make(chan chunkWork, len(chunks))
			resultChan := make(chan chunkResult, len(chunks))
			var wg sync.WaitGroup
			
			// Track progress atomically
			var processedCount atomic.Int32

			// Use unified embedding model consistent with guidelines and code indexing
			embeddingModel := os.Getenv("MAESTRO_UNIFIED_EMBEDDING_MODEL")
			if embeddingModel == "" {
				embeddingModel = rag.GetGuidelineEmbeddingModel()
			}

			// Start workers
			for w := 0; w < numWorkers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for work := range workChan {
						embeddingStartTime := time.Now()
						fileEmbedding, err := llm.CreateEmbedding(ctx, work.chunk, core.WithModel(embeddingModel))
						embeddingTime := time.Since(embeddingStartTime)

						if err != nil {
							resultChan <- chunkResult{err: fmt.Errorf("failed to create file embedding: %w", err)}
							continue
						}

						if util.GetEnvBool("MAESTRO_LLM_RESPONSE_DEBUG", false) {
							fmt.Printf("Embedding generated for chunk in %v, dimensions: %d\n", embeddingTime, len(fileEmbedding.Vector))
						}

						var patterns []*Content
						if util.GetEnvBool("MAESTRO_RAG_DEBUG_ENABLED", false) {
							patterns, _, err = a.rag.FindSimilarWithDebug(ctx, fileEmbedding.Vector, 5, "repository")
						} else {
							patterns, err = a.rag.FindSimilar(ctx, fileEmbedding.Vector, 5, "repository")
						}

						if err != nil {
							resultChan <- chunkResult{err: fmt.Errorf("failed to find similar patterns: %w", err)}
							continue
						}

						resultChan <- chunkResult{patterns: patterns}
						
						// Update spinner progress
						processed := processedCount.Add(1)
						if s := console.Spinner(); s != nil {
							s.Suffix = fmt.Sprintf(" (chunk %d/%d) of %s", processed, len(chunks), task.FilePath)
						}
					}
				}()
			}

			// Send work
			for i, chunk := range chunks {
				workChan <- chunkWork{index: i, chunk: chunk}
			}
			close(workChan)

			// Wait for workers to finish and close result channel
			go func() {
				wg.Wait()
				close(resultChan)
			}()

			// Collect results
			for result := range resultChan {
				if result.err != nil {
					console.FileError(task.FilePath, result.err)
					continue
				}
				repoPatterns = append(repoPatterns, result.patterns...)
				totalRepoMatches += len(result.patterns)
			}

			return nil
		})
		
		// Add file-level guideline matches (already deduplicated and searched once)
		if fileGuidelineMatches != nil {
			guidelineMatches = append(guidelineMatches, fileGuidelineMatches...)
			totalGuidelineMatches = len(fileGuidelineMatches)
		}

		if err != nil {
			console.FileError(task.FilePath, fmt.Errorf("failed to analyze patterns: %w", err))
			continue
		}
		if console.Color() {
			console.Printf("%s %s %s %s %s\n",
				aurora.Green("✓").Bold(),
				aurora.White("Analysis complete for").Bold(),
				aurora.Cyan(filepath.Base(task.FilePath)).Bold(),
				aurora.White(fmt.Sprintf("found %d repository patterns and %d guideline matches across %d chunks",
					totalRepoMatches, totalGuidelineMatches, len(chunks))).Bold(),
				aurora.Blue("...").String(),
			)
		} else {
			console.Printf("✓ Analysis complete for %s: found %d repository patterns and %d guideline matches across %d chunks\n",
				filepath.Base(task.FilePath), totalRepoMatches, totalGuidelineMatches, len(chunks))
		}
	}

	return repoPatterns, guidelineMatches, nil
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

// processChunkWithEnhancements processes a chunk using declarative workflow or orchestrator.
func (a *PRReviewAgent) processChunkWithEnhancements(ctx context.Context, workData interface{}, chunkContext map[string]interface{}) (*agents.OrchestratorResult, error) {
	// Try declarative workflow first, then fall back to orchestrator
	if a.declarativeChain != nil && shouldUseDeclarativeWorkflows() {
		logger := logging.GetLogger()
		filePath, _ := chunkContext["file_path"].(string)
		logger.Debug(ctx, "🏗️ Using declarative workflow for chunk processing: %s", filePath)

		result, err := a.processChunkWithDeclarativeWorkflow(ctx, chunkContext)
		if err == nil {
			return result, nil
		}

		logger.Warn(ctx, "⚠️ Declarative workflow failed, falling back to orchestrator: %v", err)
	}

	// Fall back to original processing
	filePath, _ := chunkContext["file_path"].(string)
	return a.orchestrator.Process(ctx,
		fmt.Sprintf("Review chunk of %s", filePath),
		chunkContext)
}

// processChunksParallel handles parallel chunk processing with intelligent optimization.
func (a *PRReviewAgent) processChunksParallel(ctx context.Context, tasks []PRReviewTask, repoPatterns []*Content, guidelineMatches []*Content, console ConsoleInterface) ([]PRReviewComment, error) {
	// Check if Phase 2.2 Intelligent Parallel Processing is available
	if a.shouldUseIntelligentProcessing() {
		return a.processChunksIntelligent(ctx, tasks, repoPatterns, guidelineMatches, console)
	}

	// Fall back to manual parallel processing
	return a.processChunksManual(ctx, tasks, repoPatterns, guidelineMatches, console)
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

// processChunksIntelligent uses intelligent coordination with existing chunk processing logic.
func (a *PRReviewAgent) processChunksIntelligent(ctx context.Context, tasks []PRReviewTask, repoPatterns []*Content, guidelineMatches []*Content, console ConsoleInterface) ([]PRReviewComment, error) {
	logger := logging.GetLogger()
	logger.Info(ctx, "🚀 Starting Phase 2.2 Intelligent Parallel Processing for %d files", len(tasks))

	phase2Start := time.Now()

	// Calculate total chunks from existing optimized chunks
	totalChunks := 0
	for _, task := range tasks {
		totalChunks += len(task.Chunks)
	}
	logger.Info(ctx, "📊 Using existing optimized chunks: %d chunks across %d files", totalChunks, len(tasks))

	// Create intelligent coordination with adaptive concurrency
	maxConcurrency := a.Config().ReviewWorkers
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}

	// Apply intelligent concurrency adjustment based on system resources
	adaptiveConcurrency := a.calculateAdaptiveConcurrency(ctx, totalChunks, maxConcurrency)
	logger.Info(ctx, "⚡ Using adaptive concurrency: %d workers (base: %d)", adaptiveConcurrency, maxConcurrency)

	// Create intelligent resource monitor
	resourceMonitor := a.createResourceMonitor(ctx)
	defer resourceMonitor.Stop()

	// Process chunks with intelligent coordination but existing logic
	allComments, err := a.processChunksWithIntelligentCoordination(ctx, tasks, repoPatterns, guidelineMatches, console, adaptiveConcurrency, resourceMonitor)
	if err != nil {
		logger.Error(ctx, "❌ Intelligent coordination failed after %v: %v", time.Since(phase2Start), err)
		logger.Info(ctx, "🔄 Falling back to manual parallel processing")
		return a.processChunksManual(ctx, tasks, repoPatterns, guidelineMatches, console)
	}

	phase2Duration := time.Since(phase2Start)
	logger.Info(ctx, "✅ Intelligent Parallel Processing completed in %v", phase2Duration)

	// Track Phase 2.2 metrics
	if globalMetrics != nil {
		globalMetrics.TrackFeatureUsage(GetGlobalFeatures(), "intelligent_parallel_processing")
	}

	logger.Info(ctx, "🎯 Intelligent processing generated %d comments across %d files", len(allComments), len(tasks))
	return allComments, nil
}

// calculateAdaptiveConcurrency determines optimal concurrency based on system resources and workload.
func (a *PRReviewAgent) calculateAdaptiveConcurrency(ctx context.Context, totalChunks, baseConcurrency int) int {
	logger := logging.GetLogger()

	// Get current system resources
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	currentMemoryMB := int64(memStats.Alloc / 1024 / 1024)
	numCPU := runtime.NumCPU()
	currentGoroutines := runtime.NumGoroutine()

	logger.Debug(ctx, "📊 System resources: Memory=%dMB, CPUs=%d, Goroutines=%d",
		currentMemoryMB, numCPU, currentGoroutines)

	// Start with base concurrency
	adaptiveConcurrency := baseConcurrency

	// Adjust based on workload size
	if totalChunks > 200 {
		// Large workload - increase concurrency
		adaptiveConcurrency = int(float64(baseConcurrency) * 1.5)
	} else if totalChunks < 50 {
		// Small workload - reduce concurrency to avoid overhead
		adaptiveConcurrency = int(float64(baseConcurrency) * 0.7)
	}

	// Cap based on system resources
	maxByMemory := int(4096 / 100)             // Assume 100MB per worker max
	maxByCPU := numCPU * 3                     // 3x CPU cores
	maxByGoroutines := 100 - currentGoroutines // Leave room for other goroutines

	systemMax := maxByMemory
	if maxByCPU < systemMax {
		systemMax = maxByCPU
	}
	if maxByGoroutines > 0 && maxByGoroutines < systemMax {
		systemMax = maxByGoroutines
	}

	if adaptiveConcurrency > systemMax {
		adaptiveConcurrency = systemMax
	}

	// Ensure minimum of 1
	if adaptiveConcurrency < 1 {
		adaptiveConcurrency = 1
	}

	logger.Debug(ctx, "⚡ Adaptive concurrency calculation: base=%d, workload_adjusted=%d, system_max=%d, final=%d",
		baseConcurrency, adaptiveConcurrency, systemMax, adaptiveConcurrency)

	return adaptiveConcurrency
}

// createResourceMonitor creates a lightweight resource monitor for intelligent processing.
func (a *PRReviewAgent) createResourceMonitor(ctx context.Context) *SimpleResourceMonitor {
	return &SimpleResourceMonitor{
		ctx:    ctx,
		stopCh: make(chan struct{}),
		logger: logging.GetLogger(),
	}
}

// SimpleResourceMonitor provides basic resource monitoring.
type SimpleResourceMonitor struct {
	ctx    context.Context
	stopCh chan struct{}
	logger *logging.Logger
}

func (rm *SimpleResourceMonitor) Stop() {
	close(rm.stopCh)
}

// processChunksWithIntelligentCoordination processes chunks using intelligent coordination with existing logic.
func (a *PRReviewAgent) processChunksWithIntelligentCoordination(ctx context.Context, tasks []PRReviewTask, repoPatterns []*Content, guidelineMatches []*Content, console ConsoleInterface, concurrency int, resourceMonitor *SimpleResourceMonitor) ([]PRReviewComment, error) {
	logger := logging.GetLogger()
	logger.Info(ctx, "🎯 Starting intelligent coordination with %d workers", concurrency)

	var allComments []PRReviewComment
	var mu sync.Mutex // For thread-safe comment aggregation

	// Use the same work structure as manual processing
	type chunkWork struct {
		task     *PRReviewTask
		chunk    ReviewChunk
		chunkIdx int
		taskIdx  int
	}

	// Create channels for work distribution and results
	workChan := make(chan chunkWork, concurrency*2) // Buffered channel for better performance
	resultChan := make(chan []PRReviewComment, concurrency*2)
	errorChan := make(chan error, concurrency)

	// Calculate total chunks for progress tracking
	totalChunks := 0
	for _, task := range tasks {
		totalChunks += len(task.Chunks)
	}
	processedChunks := atomic.NewInt32(0)

	// Create cancellable context
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start intelligent worker pool with adaptive features
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Worker-specific metrics
			workerStartTime := time.Now()
			workerProcessedCount := 0

			for work := range workChan {
				select {
				case <-workCtx.Done():
					return
				default:
					chunkStart := time.Now()

					// Use the SAME chunk context as manual processing
					chunkContext := map[string]interface{}{
						"file_path": work.task.FilePath,
						"chunk":     util.EscapeFileContent(ctx, work.chunk.Content),
						"context": map[string]string{
							"leading":  work.chunk.LeadingContext,
							"trailing": work.chunk.TrailingContext,
						},
						"changes":      work.chunk.Changes,
						"chunk_start":  work.chunk.StartLine,
						"chunk_end":    work.chunk.EndLine,
						"chunk_number": work.chunkIdx + 1,
						"total_chunks": len(work.task.Chunks),
						"line_range": map[string]int{
							"start": work.chunk.StartLine,
							"end":   work.chunk.EndLine,
						},
						"review_type":   "chunk_review",
						"repo_patterns": repoPatterns,
						"guidelines":    guidelineMatches,
						// Add intelligent processing metadata
						"intelligent_processing": true,
						"worker_id":              workerID,
						"adaptive_concurrency":   concurrency,
					}

					logger.Debug(ctx, "🤖 Intelligent Worker %d: Processing chunk %d/%d of %s (lines %d-%d)",
						workerID, work.chunkIdx+1, len(work.task.Chunks), work.task.FilePath, work.chunk.StartLine, work.chunk.EndLine)

					// Use the SAME chunk processing logic as manual mode
					result, err := a.processChunkWithEnhancements(ctx, work, chunkContext)
					chunkDuration := time.Since(chunkStart)
					workerProcessedCount++

					if err != nil {
						logger.Error(ctx, "❌ Intelligent Worker %d: Failed to process chunk %d of %s after %v: %v",
							workerID, work.chunkIdx+1, work.task.FilePath, chunkDuration, err)
						errorChan <- fmt.Errorf("intelligent worker %d failed to process chunk %d of %s: %w",
							workerID, work.chunkIdx+1, work.task.FilePath, err)
						continue
					}

					logger.Debug(ctx, "✅ Intelligent Worker %d: Completed chunk %d of %s in %v",
						workerID, work.chunkIdx+1, work.task.FilePath, chunkDuration)

					// Use the SAME result processing logic as manual mode
					for _, taskResult := range result.CompletedTasks {
						taskMap, ok := taskResult.(map[string]interface{})
						if !ok {
							continue
						}

						taskType, _ := taskMap["task_type"].(string)
						processingType, hasProcessingType := taskMap["processing_type"].(string)

						switch taskType {
						case "review_chain":
							logger.Debug(ctx, "Processed review chain result: %v", taskMap)
							continue

						case "code_review", "comment_response":
							comments, err := extractComments(ctx, taskResult, work.task.FilePath, a.Metrics(ctx))
							if err != nil {
								logger.Error(ctx, "Failed to extract comments from task %s: %v", taskType, err)
								continue
							}

							if len(comments) == 0 {
								console.NoIssuesFound(work.task.FilePath, work.chunkIdx+1, len(work.task.Chunks))
							} else {
								console.ShowComments(comments, a.Metrics(ctx))
							}
							resultChan <- comments

						case "":
							if hasProcessingType {
								logger.Debug(ctx, "Processing enhanced result with type: %s", processingType)
								comments, err := extractComments(ctx, taskResult, work.task.FilePath, a.Metrics(ctx))
								if err != nil {
									logger.Debug(ctx, "Enhanced result extraction failed: %v", err)
									continue
								}
								if len(comments) == 0 {
									console.NoIssuesFound(work.task.FilePath, work.chunkIdx+1, len(work.task.Chunks))
								} else {
									console.ShowComments(comments, a.Metrics(ctx))
								}
								resultChan <- comments
							} else {
								logger.Debug(ctx, "Empty task result: %+v", taskMap)
							}

						default:
							logger.Debug(ctx, "Unknown task type '%s', checking for enhanced result with processing_type: %s", taskType, processingType)
							comments, err := extractComments(ctx, taskResult, work.task.FilePath, a.Metrics(ctx))
							if err == nil && len(comments) > 0 {
								console.ShowComments(comments, a.Metrics(ctx))
								resultChan <- comments
							}
						}
					}
				}
			}

			// Log worker completion stats
			workerDuration := time.Since(workerStartTime)
			avgPerChunk := workerDuration
			if workerProcessedCount > 0 {
				avgPerChunk = time.Duration(int64(workerDuration) / int64(workerProcessedCount))
			}
			logger.Debug(ctx, "🏁 Intelligent Worker %d completed: processed %d chunks in %v (avg: %v/chunk)",
				workerID, workerProcessedCount, workerDuration, avgPerChunk)
		}(i)
	}

	// Distribute work (same as manual processing)
	go func() {
		defer close(workChan)
		for taskIdx, task := range tasks {
			for chunkIdx, chunk := range task.Chunks {
				select {
				case <-workCtx.Done():
					return
				default:
					workChan <- chunkWork{
						task:     &task,
						chunk:    chunk,
						chunkIdx: chunkIdx,
						taskIdx:  taskIdx,
					}
				}
			}
		}
	}()

	// Collect results (same as manual processing)
	go func() {
		wg.Wait()
		logger.Debug(ctx, "All intelligent workers completed, closing channels")
		close(resultChan)
		close(errorChan)
	}()

	// Process results and errors (same as manual processing)
	var errors []error
	var errorsMu sync.Mutex
	for {
		select {
		case err, ok := <-errorChan:
			if !ok {
				errorChan = nil
			} else {
				errorsMu.Lock()
				errors = append(errors, err)
				errorsMu.Unlock()
			}
		case comments, ok := <-resultChan:
			if !ok {
				resultChan = nil
			} else {
				mu.Lock()
				allComments = append(allComments, comments...)
				processed := processedChunks.Add(1)
				percentage := float64(processed) / float64(totalChunks) * 100
				console.UpdateSpinnerText(fmt.Sprintf("Intelligent processing... %.1f%% (%d/%d)", percentage, processed, totalChunks))
				mu.Unlock()
			}

		case <-workCtx.Done():
			logger.Debug(ctx, "Intelligent coordination context cancelled")
			return nil, workCtx.Err()
		}

		if errorChan == nil && resultChan == nil {
			logger.Debug(ctx, "Both intelligent coordination channels closed: %d comments, %d errors",
				len(allComments), len(errors))
			break
		}
	}

	if len(errors) > 0 {
		logger.Error(ctx, "Intelligent coordination failed with %d errors: %v", len(errors), errors)
		return nil, fmt.Errorf("encountered errors during intelligent parallel processing: %v", errors)
	}

	logger.Info(ctx, "🎯 Intelligent coordination completed: %d comments generated", len(allComments))
	return allComments, nil
}

// processChunksManual handles manual parallel chunk processing (legacy fallback).
func (a *PRReviewAgent) processChunksManual(ctx context.Context, tasks []PRReviewTask, repoPatterns []*Content, guidelineMatches []*Content, console ConsoleInterface) ([]PRReviewComment, error) {
	logger := logging.GetLogger()
	logger.Info(ctx, "🔄 Using manual parallel processing for %d files", len(tasks))

	var allComments []PRReviewComment
	var mu sync.Mutex // For thread-safe comment aggregation

	// Create work pool for chunk processing
	type chunkWork struct {
		task     *PRReviewTask
		chunk    ReviewChunk
		chunkIdx int
		taskIdx  int
	}

	// Create channels for work distribution and results
	workChan := make(chan chunkWork)
	resultChan := make(chan []PRReviewComment)
	errorChan := make(chan error)

	// Calculate total chunks for progress tracking
	totalChunks := 0
	for _, task := range tasks {
		totalChunks += len(task.Chunks)
	}
	processedChunks := atomic.NewInt32(0)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start worker pool
	numWorkers := a.Config().ReviewWorkers // Configurable based on system resources
	if numWorkers < 1 {
		numWorkers = 1
	}
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for work := range workChan {
				select {
				case <-ctx.Done():
					return
				default:
					chunkStart := time.Now()
					chunkContext := map[string]interface{}{
						"file_path": work.task.FilePath,
						"chunk":     util.EscapeFileContent(ctx, work.chunk.Content),
						"context": map[string]string{
							"leading":  work.chunk.LeadingContext,
							"trailing": work.chunk.TrailingContext,
						},
						"changes":      work.chunk.Changes,
						"chunk_start":  work.chunk.StartLine,
						"chunk_end":    work.chunk.EndLine,
						"chunk_number": work.chunkIdx + 1,
						"total_chunks": len(work.task.Chunks),
						"line_range": map[string]int{
							"start": work.chunk.StartLine,
							"end":   work.chunk.EndLine,
						},
						"review_type":   "chunk_review",
						"repo_patterns": repoPatterns,
						"guidelines":    guidelineMatches,
					}

					logger.Debug(ctx, "🔄 Worker %d: Processing chunk %d/%d of %s (lines %d-%d)",
						workerID, work.chunkIdx+1, len(work.task.Chunks), work.task.FilePath, work.chunk.StartLine, work.chunk.EndLine)

					// Process the chunk with enhanced features if available
					result, err := a.processChunkWithEnhancements(ctx, work, chunkContext)
					chunkDuration := time.Since(chunkStart)

					if err != nil {
						logger.Error(ctx, "❌ Worker %d: Failed to process chunk %d of %s after %v: %v",
							workerID, work.chunkIdx+1, work.task.FilePath, chunkDuration, err)
						errorChan <- fmt.Errorf("failed to process chunk %d of %s: %w",
							work.chunkIdx+1, work.task.FilePath, err)
						continue
					}

					logger.Debug(ctx, "✅ Worker %d: Completed chunk %d of %s in %v",
						workerID, work.chunkIdx+1, work.task.FilePath, chunkDuration)

					// Process results
					for _, taskResult := range result.CompletedTasks {

						taskMap, ok := taskResult.(map[string]interface{})
						if !ok {
							// If the conversion fails, log with the actual type for debugging
							continue
						}

						taskType, _ := taskMap["task_type"].(string)

						// Check for enhanced processing results
						processingType, hasProcessingType := taskMap["processing_type"].(string)

						switch taskType {
						case "review_chain":
							// ReviewChain output will trigger new tasks - no comments to extract
							// We can log the chain results for debugging
							logger.Debug(ctx, "Processed review chain result: %v", taskMap)
							continue

						case "code_review", "comment_response":
							// These are the tasks that actually generate comments
							comments, err := extractComments(ctx, taskResult, work.task.FilePath, a.Metrics(ctx))
							if err != nil {
								logging.GetLogger().Error(ctx, "Failed to extract comments from task %s: %v", taskType, err)
								continue
							}

							// Thread-safe updates
							if len(comments) == 0 {
								console.NoIssuesFound(work.task.FilePath, work.chunkIdx+1, len(work.task.Chunks))
							} else {
								console.ShowComments(comments, a.Metrics(ctx))
							}
							resultChan <- comments
						case "":
							// Handle enhanced processing results without task_type
							if hasProcessingType {
								logger.Debug(ctx, "Processing enhanced result with type: %s", processingType)
								comments, err := extractComments(ctx, taskResult, work.task.FilePath, a.Metrics(ctx))
								if err != nil {
									logger.Debug(ctx, "Enhanced result extraction failed: %v", err)
									continue
								}
								if len(comments) == 0 {
									console.NoIssuesFound(work.task.FilePath, work.chunkIdx+1, len(work.task.Chunks))
								} else {
									console.ShowComments(comments, a.Metrics(ctx))
								}
								resultChan <- comments
							} else {
								logger.Debug(ctx, "Empty task result: %+v", taskMap)
							}
						default:
							logger.Debug(ctx, "Unknown task type '%s', checking for enhanced result with processing_type: %s", taskType, processingType)
							// Try to extract comments anyway for enhanced results
							comments, err := extractComments(ctx, taskResult, work.task.FilePath, a.Metrics(ctx))
							if err == nil && len(comments) > 0 {
								console.ShowComments(comments, a.Metrics(ctx))
								resultChan <- comments
							}
						}

					}

					// // Update progress
					// processed := processedChunks.Add(1)
					// percentage := float64(processed) / float64(totalChunks) * 100
					// console.UpdateSpinnerText(fmt.Sprintf("Processing chunks... %.1f%% (%d/%d)",
					// 	percentage, processed, totalChunks))
				}
			}
		}(i)
	}

	// Distribute work
	go func() {
		defer close(workChan)
		for taskIdx, task := range tasks {
			for chunkIdx, chunk := range task.Chunks {
				select {
				case <-ctx.Done():
					return
				default:
					workChan <- chunkWork{
						task:     &task,
						chunk:    chunk,
						chunkIdx: chunkIdx,
						taskIdx:  taskIdx,
					}
				}
			}
		}
	}()

	// Collect results
	go func() {
		wg.Wait()
		logger.Debug(ctx, "All workers completed, closing channels")
		close(resultChan)
		close(errorChan)
	}()

	// Process results and errors
	var errors []error
	var errorsMu sync.Mutex
	for {
		logger.Debug(ctx, "Channel status - errorChan: %v, resultChan: %v",
			errorChan == nil, resultChan == nil)
		select {
		case err, ok := <-errorChan:
			if !ok {

				logger.Debug(ctx, "Error channel closed, setting to nil")
				errorChan = nil
			} else {
				errorsMu.Lock()
				errors = append(errors, err)
				errorsMu.Unlock()
			}
		case comments, ok := <-resultChan:
			if !ok {

				logger.Debug(ctx, "Error channel closed, setting to nil")
				resultChan = nil
			} else {
				mu.Lock()
				allComments = append(allComments, comments...)
				processed := processedChunks.Add(1)
				percentage := float64(processed) / float64(totalChunks) * 100
				console.UpdateSpinnerText(fmt.Sprintf("Processing chunks... %.1f%% (%d/%d)", percentage, processed, totalChunks))
				mu.Unlock()
			}

		case <-ctx.Done():

			logger.Debug(ctx, "Context cancelled, exiting loop")
			return nil, ctx.Err()
		}

		if errorChan == nil && resultChan == nil {
			logger.Debug(ctx, "Both channels nil, exiting loop with %d comments and %d errors",
				len(allComments), len(errors))
			break
		}
	}

	if len(errors) > 0 {
		logger.Debug(ctx, "Chunk processing failed with %d errors: %v", len(errors), errors)
		return nil, fmt.Errorf("encountered errors during parallel processing: %v", errors)
	}

	logger.Debug(ctx, "Manual chunk processing completed with %d comments", len(allComments))
	return allComments, nil
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

	// Generate response using declarative workflow or orchestrator
	var result *agents.OrchestratorResult
	var err error

	if a.declarativeChain != nil && shouldUseDeclarativeWorkflows() {
		logger.Info(ctx, "🏗️ Using declarative workflow for response generation")
		result, err = a.generateResponseWithDeclarativeWorkflow(ctx, responseContext)
		if err != nil {
			logger.Warn(ctx, "⚠️ Declarative workflow failed, falling back to orchestrator: %v", err)
			result, err = a.orchestrator.Process(ctx, "Generate response", responseContext)
		}
	} else {
		result, err = a.orchestrator.Process(ctx, "Generate response", responseContext)
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

		if a.declarativeChain != nil && shouldUseDeclarativeWorkflows() {
			logger.Info(ctx, "🏗️ Using declarative workflow for response generation in generateResponse()")
			result, processErr = a.generateResponseWithDeclarativeWorkflow(ctx, responseContext)
			if processErr != nil {
				logger.Warn(ctx, "⚠️ Declarative workflow failed, falling back to orchestrator: %v", processErr)
				result, processErr = a.orchestrator.Process(ctx, "Generate response", responseContext)
			}
		} else {
			result, processErr = a.orchestrator.Process(ctx, "Generate response", responseContext)
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
