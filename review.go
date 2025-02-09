package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/google/go-github/v68/github"
	"github.com/logrusorgru/aurora"
)

type Stopper struct {
	stop     chan struct{}
	stopped  chan struct{}
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

func NewStopper() *Stopper {
	return &Stopper{
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

type ReviewChunk struct {
	content         string
	startline       int
	endline         int
	leadingcontext  string // Previous lines for context
	trailingcontext string // Following lines for context
	changes         string // Relevant diff section for this chunk
	filePath        string // Path of the parent file
	totalChunks     int    // Total number of chunks for this file
}

// PRReviewTask represents a single file review task.
type PRReviewTask struct {
	FilePath    string
	FileContent string
	Changes     string // Git diff content
	Chunks      []ReviewChunk
}

// PRReviewComment represents a review comment.
type PRReviewComment struct {
	FilePath    string
	LineNumber  int
	Content     string
	Severity    string
	Suggestion  string
	Category    string    // e.g., "security", "performance", "style"
	InReplyTo   *int64    // ID of the parent comment
	ThreadID    *int64    // Unique thread identifier
	Resolved    bool      // Track if the discussion is resolved
	Timestamp   time.Time // When the comment was made
	Author      string
	MessageType MessageType
}

type LineRange struct {
	Start int    // Starting line number (inclusive)
	End   int    // Ending line number (inclusive)
	File  string // File path this range refers to
}

// Add helper methods to make working with ranges easier.
func (lr LineRange) Contains(line int) bool {
	return line >= lr.Start && line <= lr.End
}

func (lr LineRange) IsValid() bool {
	return lr.Start > 0 && lr.End >= lr.Start
}

func (lr LineRange) String() string {
	return fmt.Sprintf("lines %d-%d in %s", lr.Start, lr.End, lr.File)
}

type MessageType string

const (
	QuestionMessage        MessageType = "question"
	ClarificationMessage   MessageType = "clarification"
	ResolutionMessage      MessageType = "resolution"
	SuggestionMessage      MessageType = "suggestion"
	AcknowledgementMessage MessageType = "acknowledgement"
)

// PRReviewAgent handles code review using dspy-go.
type PRReviewAgent struct {
	orchestrator  *agents.FlexibleOrchestrator
	memory        agents.Memory
	rag           RAGStore
	activeThreads map[int64]*ThreadTracker // Track active discussion threads
	// TODO: should align with dspy agent interface
	githubTools *GitHubTools // Add this field
	stopper     *Stopper
}

type ThreadTracker struct {
	LastComment  *PRReviewComment
	ReviewChunks []ReviewChunk
	FileContent  string
	LastUpdate   time.Time
	Status       ThreadStatus // Using our existing ThreadStatus type

	ParentCommentID     int64
	OriginalAuthor      string // Who started the thread
	ThreadID            int64
	InReplyToMyComment  bool              // Whether this is a reply to our comment
	IsResolved          bool              // Whether the thread is resolved
	ConversationHistory []PRReviewComment // Full history of the thread
}

type ReviewMetadata struct {
	FilePath    string
	FileContent string
	Changes     string
	Category    string
	ReviewType  string
}

// Chunkconfig holds configuration for code chunking.
type ChunkConfig struct {
	maxtokens    int // Maximum tokens per chunk
	contextlines int // Number of context lines to include
	overlaplines int // Number of lines to overlap between chunks

	fileMetadata map[string]interface{}
}

func NewChunkConfig() ChunkConfig {
	return ChunkConfig{
		maxtokens:    1000,                         // Reserve space for model response
		contextlines: 10,                           // Lines of surrounding context
		overlaplines: 5,                            // Overlapping lines between chunks
		fileMetadata: make(map[string]interface{}), // Initialize empty metadata map
	}
}

// chunkfile splits a file into reviewable chunks while preserving context.
func chunkfile(ctx context.Context, content string, changes string, config ChunkConfig) ([]ReviewChunk, error) {
	logger := logging.GetLogger()
	lines := strings.Split(content, "\n")
	chunks := make([]ReviewChunk, 0)

	logger.Debug(ctx, "Processing file with %d lines of content", len(lines))
	var currentchunk []string
	currenttokens := 0

	filePath, filePathExists := config.fileMetadata["file_path"].(string)
	if !filePathExists {
		// Provide a reasonable default or return an error
		filePath = "unknown"
	}
	estimatedChunks := (len(lines) + config.maxtokens - 1) / config.maxtokens
	chunkIndex := 0
	for i := 0; i < len(lines); i++ {
		// Estimate tokens for current line
		linetokens := estimatetokens(lines[i])

		if currenttokens+linetokens > config.maxtokens && len(currentchunk) > 0 {
			// Create chunk with context
			chunk := ReviewChunk{
				content:         strings.Join(currentchunk, "\n"),
				startline:       max(0, i-len(currentchunk)),
				endline:         i,
				leadingcontext:  getleadingcontext(lines, i-len(currentchunk), config.contextlines),
				trailingcontext: gettrailingcontext(lines, i, config.contextlines),
				changes:         ExtractRelevantChanges(changes, i-len(currentchunk), i),
				filePath:        filePath,
				totalChunks:     estimatedChunks,
			}
			logger.Debug(ctx, "Created chunk: lines %d-%d, content length: %d, leading context: %d chars, trailing context: %d chars",
				chunk.startline, chunk.endline, len(chunk.content),
				len(chunk.leadingcontext), len(chunk.trailingcontext))
			if fileType, ok := config.fileMetadata["file_type"].(string); ok {
				// Example: Special handling for different file types
				switch fileType {
				case ".go":
					// Maybe add extra context for Go files
					if pkg, ok := config.fileMetadata["package"].(string); ok {
						// Could use package info for context
						_ = pkg // Using _ to show we could use this
					}
				case ".md":
					// Different handling for markdown files
					break
				}
			}
			chunks = append(chunks, chunk)

			chunkIndex++
			// Start new chunk with overlap
			overlapstart := max(0, len(currentchunk)-config.overlaplines)
			currentchunk = currentchunk[overlapstart:]
			currenttokens = estimatetokens(strings.Join(currentchunk, "\n"))
		}

		currentchunk = append(currentchunk, lines[i])
		currenttokens += linetokens
		i++
	}

	// Add final chunk if needed
	if len(currentchunk) > 0 {
		chunks = append(chunks, ReviewChunk{
			content:         strings.Join(currentchunk, "\n"),
			startline:       max(0, len(lines)-len(currentchunk)),
			endline:         len(lines),
			leadingcontext:  getleadingcontext(lines, len(lines)-len(currentchunk), config.contextlines),
			trailingcontext: "",
			changes:         ExtractRelevantChanges(changes, len(lines)-len(currentchunk), len(lines)),
		})

	}

	// Validate the chunking result
	if len(chunks) == 0 {
		logger.Error(ctx, "No chunks created from file with %d lines", len(lines))
		// If file is small enough, create a single chunk
		if len(lines) > 0 && estimatetokens(content) <= config.maxtokens {
			chunk := ReviewChunk{
				content:         content,
				startline:       0,
				endline:         len(lines),
				leadingcontext:  "",
				trailingcontext: "",
				changes:         changes,
			}
			logger.Info(ctx, "Created single chunk for small file: %d lines", len(lines))
			return []ReviewChunk{chunk}, nil
		}
	}

	return chunks, nil
}

// Helper functions to manage context and changes.
func getleadingcontext(lines []string, start, contextlines int) string {
	contextstart := max(0, start-contextlines)
	if contextstart >= start {
		return ""
	}
	return strings.Join(lines[contextstart:start], "\n")
}

func gettrailingcontext(lines []string, end, contextlines int) string {
	if end >= len(lines) {
		return ""
	}
	contextend := min(len(lines), end+contextlines)
	return strings.Join(lines[end:contextend], "\n")
}

// estimatetokens provides a rough estimate of tokens in a string.
func estimatetokens(text string) int {
	// Simple estimation: ~1.3 tokens per word
	words := len(strings.Fields(text))
	return int(float64(words) * 1.3)
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
func NewPRReviewAgent(ctx context.Context, githubTool *GitHubTools, dbPath string, needFullIndex bool) (*PRReviewAgent, error) {
	logger := logging.GetLogger()

	logger.Debug(ctx, "Starting agent initialization with dbPath: %s", dbPath)
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize sqlite db: %v", err)
	}

	logger.Debug(ctx, "Successfully opened database")
	store, err := NewSQLiteRAGStore(db, logging.GetLogger())
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize rag store: %v", err)
	}

	logger.Debug(ctx, "Successfully created RAG store")
	indexer := NewRepoIndexer(githubTool, store)

	logger.Debug(ctx, "Starting repository indexing")
	if err := indexer.IndexRepository(ctx, "", dbPath, needFullIndex); err != nil {
		store.Close()

		return nil, fmt.Errorf("failed to index repository: %w", err)
	}

	logger.Debug(ctx, "Successfully indexed repository")
	memory := agents.NewInMemoryStore()
	stopper := NewStopper()

	logger.Debug(ctx, "Creating orchestrator configuration")
	analyzerConfig := agents.AnalyzerConfig{
		BaseInstruction: `Analyze the input and determine the appropriate task type:
		- If responding to existing comments, create a comment_response task
		- If reviewing new code, create a code_review task
		- If answering questions about the repository, create a repo_qa task

		IMPORTANT FORMAT RULES:
		1. Start fields exactly with 'analysis:' or 'tasks:' (no markdown formatting)
		2. Provide raw XML directly after 'tasks:' without any wrapping
		3. Keep the exact field prefix format - no decorations or modifications
		4. Ensure proper indentation and structure in the XML
		5. When thread_id is present in the context, always create a comment_response task.
		6 FOR TASK FIELD 'file_content':
		- Before inserting it into the XML, replace every '<' with '&lt; and every '>' with '&gt;'.**  
	        - For example, '<reasoning>' should become '&lt;reasoning&gt;' and '<answer>' should become '&lt;answer&gt;'.
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
	           </metadata>
	       </task>
	   </tasks>`,
	}

	qaProcessor := NewRepoQAProcessor(store)

	config := agents.OrchestrationConfig{
		MaxConcurrent:  5,
		TaskParser:     &agents.XMLTaskParser{},
		PlanCreator:    &agents.DependencyPlanCreator{},
		AnalyzerConfig: analyzerConfig,
		RetryConfig: &agents.RetryConfig{
			MaxAttempts:       2,
			BackoffMultiplier: 2.0,
		},
		CustomProcessors: map[string]agents.TaskProcessor{
			"code_review":      &CodeReviewProcessor{},
			"comment_response": &CommentResponseProcessor{},
			"repo_qa":          qaProcessor,
		},
	}

	orchestrator := agents.NewFlexibleOrchestrator(memory, config)

	logger.Debug(ctx, "Successfully created orchestrator")
	return &PRReviewAgent{
		orchestrator: orchestrator,
		memory:       memory,
		rag:          store,
		githubTools:  githubTool,
		stopper:      stopper,
	}, nil
}

func (a *PRReviewAgent) GetGitHubTools() *GitHubTools {
	if a.githubTools == nil {
		panic("GitHub tools not initialized")
	}
	return a.githubTools
}

func (a *PRReviewAgent) GetOrchestrator() *agents.FlexibleOrchestrator {
	if a.orchestrator == nil {
		panic("Agent orchestrator not initialized")
	}
	return a.orchestrator

}

func (a *PRReviewAgent) Close() error {
	if a.rag != nil {
		return a.rag.Close()
	}
	return nil

}

// ReviewPR reviews a complete pull request.
func (a *PRReviewAgent) ReviewPR(ctx context.Context, prNumber int, tasks []PRReviewTask, console *Console) ([]PRReviewComment, error) {
	if a.activeThreads == nil {
		a.activeThreads = make(map[int64]*ThreadTracker)
	}

	if err := a.processExistingComments(ctx, prNumber, console); err != nil {
		return nil, fmt.Errorf("failed to process existing comments: %w", err)
	}

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
	for _, thread := range a.activeThreads {
		if thread.OriginalAuthor == a.githubTools.authenticatedUser {
			// This is a thread I started
			if !thread.IsResolved {
				myOpenThreads = append(myOpenThreads, thread)
			}
		} else if thread.LastComment.Author != a.githubTools.authenticatedUser {
			// Someone else made the last comment
			if thread.InReplyToMyComment {
				repliestoMe = append(repliestoMe, thread)
			} else {
				newThreadsByOthers = append(newThreadsByOthers, thread)
			}
		}
	}

	for _, thread := range newThreadsByOthers {
		console.printf("Generating response to new thread %d (file: %s)\n",
			thread.ThreadID, thread.LastComment.FilePath)

		_, err := a.generateResponse(ctx, thread, console)
		if err != nil {
			console.FileError(thread.LastComment.FilePath,
				fmt.Errorf("failed to generate response: %w", err))
			continue
		}
	}
	if len(myOpenThreads) == 0 && len(repliestoMe) == 0 {
		console.println(aurora.Cyan("No existing review found, performing initial review"))
		comments, err := a.performInitialReview(ctx, tasks, console)
		if err != nil {
			return nil, fmt.Errorf("initial review failed: %w", err)
		}
		// Track new threads from initial review
		for _, comment := range comments {
			if comment.ThreadID != nil {
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
		console.println(aurora.Cyan("\nNo valid comments found need to reply"))
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

func (a *PRReviewAgent) performInitialReview(ctx context.Context, tasks []PRReviewTask, console *Console) ([]PRReviewComment, error) {
	var allComments []PRReviewComment
	// Create review context
	fileData := make(map[string]map[string]interface{})
	for i := range tasks {
		task := tasks[i]
		config := NewChunkConfig()
		config.fileMetadata = map[string]interface{}{
			"file_path": task.FilePath,
			"file_type": filepath.Ext(task.FilePath),
			"package":   filepath.Base(filepath.Dir(task.FilePath)),
		}
		chunks, err := chunkfile(ctx, task.FileContent, task.Changes, config)
		if err != nil {
			return nil, fmt.Errorf("failed to chunk file %s: %w", task.FilePath, err)
		}

		tasks[i].Chunks = chunks

		fileData[task.FilePath] = map[string]interface{}{
			"file_content": task.FileContent,
			"changes":      task.Changes,
			"chunks":       chunks,
		}
	}

	for i := range tasks {
		// Process each chunk separately
		task := tasks[i]

		for i, chunk := range task.Chunks {
			chunkContext := map[string]interface{}{
				"file_path": task.FilePath,
				"chunk":     chunk.content,
				"context": map[string]string{
					"leading":  chunk.leadingcontext,
					"trailing": chunk.trailingcontext,
				},
				"changes": chunk.changes,
				"line_range": map[string]int{
					"start": chunk.startline,
					"end":   chunk.endline,
				},
				"review_type": "chunk_review",
			}

			console.ReviewingFile(task.FilePath, i+1, len(task.Changes))

			message := fmt.Sprintf("Reviewing %s (chunk %d/%d)", task.FilePath, i+1, len(task.Chunks))
			err := console.WithSpinner(ctx, message, func() error {
				result, err := a.orchestrator.Process(ctx,
					fmt.Sprintf("Review chunk %d of %s", i+1, task.FilePath),
					chunkContext)
				if err != nil {
					return err
				}
				for taskID, taskResult := range result.CompletedTasks {
					// Convert task results to comments
					if reviewComments, err := extractComments(taskResult, task.FilePath); err != nil {
						logging.GetLogger().Error(ctx, "Failed to extract comments from task %s: %v", taskID, err)
						continue
					} else {
						// Show the results for this file
						if len(reviewComments) == 0 {
							console.NoIssuesFound(task.FilePath)
						} else {
							console.ShowComments(reviewComments)
						}
						allComments = append(allComments, reviewComments...)
					}
				}
				return nil

			})
			if err != nil {
				return nil, fmt.Errorf("failed to process chunk %d of %s: %w", i+1, task.FilePath, err)
			}
		}
	}
	return allComments, nil

}

func (a *PRReviewAgent) processExistingComments(ctx context.Context, prNumber int, console *Console) error {
	logger := logging.GetLogger()
	console.println(aurora.Cyan("\nProcess existing comments..."))
	githubTools := a.GetGitHubTools()

	changes, err := githubTools.GetPullRequestChanges(ctx, prNumber)
	if err != nil {
		return fmt.Errorf("failed to fetch PR changes: %w", err)
	}
	fileContents := make(map[string]string)
	for _, change := range changes.Files {
		fileContents[change.FilePath] = change.FileContent
	}
	comments, _, err := githubTools.client.PullRequests.ListComments(ctx,
		githubTools.owner, githubTools.repo, prNumber,
		&github.PullRequestListCommentsOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch existing comments: %w", err)
	}
	logger.Debug(ctx, "Found %d existing comments", len(comments))
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
	}
	for _, comment := range comments {
		commentID := comment.GetID()
		parentID := comment.GetInReplyTo()

		filePath := comment.GetPath()

		// Create a thread tracker if it doesn't exist
		if _, exists := a.activeThreads[commentID]; !exists {
			// Convert GitHub comment to our format
			reviewComment := convertGitHubComment(comment)
			reviewComment.Author = comment.GetUser().GetLogin()

			a.activeThreads[commentID] = &ThreadTracker{
				LastComment:     &reviewComment,
				ParentCommentID: parentID,
				LastUpdate:      comment.GetCreatedAt().Time,
				Status:          ThreadOpen,
				FileContent:     fileContents[filePath],

				OriginalAuthor:      comment.GetUser().GetLogin(),
				ConversationHistory: threadHistory[commentID],
				ThreadID:            commentID,
				InReplyToMyComment:  isReplyToMyComment(comment, commentsByID, githubTools.authenticatedUser),
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
	return nil
}

func (a *PRReviewAgent) monitorAndRespond(ctx context.Context, prNumber int, console *Console) error {
	githubTools := a.GetGitHubTools()

	return githubTools.MonitorPRComments(ctx, prNumber, func(comment *github.PullRequestComment) {
		// Only process comments from other users
		if comment.GetUser().GetLogin() != githubTools.authenticatedUser {
			a.processComment(ctx, comment, console)
		}
	})

}

// processComment handles the processing of a single PR comment.
func (a *PRReviewAgent) processComment(ctx context.Context, comment *github.PullRequestComment, console *Console) {
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

	// Generate response using the orchestrator
	result, err := a.orchestrator.Process(ctx, "Generate response", responseContext)
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
func (a *PRReviewAgent) generateResponse(ctx context.Context, thread *ThreadTracker, console *Console) (*PRReviewComment, error) {
	logger := logging.GetLogger()
	console.println(aurora.Cyan("Generating response..."))
	if thread.FileContent == "" {
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
		result, processErr = a.orchestrator.Process(ctx, "Generate response", responseContext)
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
				if chunk.startline <= comment.LineNumber && chunk.endline >= comment.LineNumber {
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
