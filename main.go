package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
)

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
	FilePath   string
	LineNumber int
	Content    string
	Severity   string
	Suggestion string
	Category   string // e.g., "security", "performance", "style"
}

// PRReviewAgent handles code review using dspy-go.
type PRReviewAgent struct {
	orchestrator *agents.FlexibleOrchestrator
	memory       agents.Memory
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
		maxtokens:    4000,                         // Reserve space for model response
		contextlines: 10,                           // Lines of surrounding context
		overlaplines: 5,                            // Overlapping lines between chunks
		fileMetadata: make(map[string]interface{}), // Initialize empty metadata map
	}
}

// chunkfile splits a file into reviewable chunks while preserving context.
func chunkfile(content string, changes string, config ChunkConfig) ([]ReviewChunk, error) {
	logger := logging.GetLogger()
	ctx := context.Background() // For logging
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
func NewPRReviewAgent() (*PRReviewAgent, error) {
	memory := agents.NewInMemoryStore()

	analyzerConfig := agents.AnalyzerConfig{
		BaseInstruction: `
		IMPORTANT FORMAT RULES:
		1. Start fields exactly with 'analysis:' or 'tasks:' (no markdown formatting)
		2. Provide raw XML directly after 'tasks:' without any wrapping
		3. Keep the exact field prefix format - no decorations or modifications
		4. Ensure proper indentation and structure in the XML`,
		FormatInstructions: `Format tasks section in following XML format:
	   <tasks>
	       <task id="1" type="code_review" processor="code_review" priority="1">
	           <description>Review {file_path} for code quality</description>
	           <metadata>
	               <item key="file_path">{file_path}</item>
	               <item key="file_content">{file_content}</item>
	               <item key="changes">{changes}</item>
	               <item key="category">code_review</item>
	           </metadata>
	       </task>
	   </tasks>`,
	}

	config := agents.OrchestrationConfig{
		MaxConcurrent:  5,
		TaskParser:     &agents.XMLTaskParser{},
		PlanCreator:    &agents.DependencyPlanCreator{},
		AnalyzerConfig: analyzerConfig,
		RetryConfig: &agents.RetryConfig{
			MaxAttempts:       1,
			BackoffMultiplier: 2.0,
		},
		CustomProcessors: map[string]agents.TaskProcessor{
			"code_review": &CodeReviewProcessor{},
		},
	}

	orchestrator := agents.NewFlexibleOrchestrator(memory, config)

	return &PRReviewAgent{
		orchestrator: orchestrator,
		memory:       memory,
	}, nil
}

// ReviewPR reviews a complete pull request.
func (a *PRReviewAgent) ReviewPR(ctx context.Context, tasks []PRReviewTask, console *Console) ([]PRReviewComment, error) {
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
		chunks, err := chunkfile(task.FileContent, task.Changes, config)
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
			// Execute review for this chunk
			result, err := a.orchestrator.Process(ctx,
				fmt.Sprintf("Review chunk %d of %s", i+1, task.FilePath),
				chunkContext)
			if err != nil {
				return nil, fmt.Errorf("failed to process chunk %d of %s: %w", i+1, task.FilePath, err)
			}

			// Collect comments from this chunk
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
		}
	}
	return allComments, nil
}

// CodeReviewProcessor implements the core review logic.
type CodeReviewProcessor struct{}

func (p *CodeReviewProcessor) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	logger := logging.GetLogger()
	// Create signature for code review
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "file_content"}},
			{Field: core.Field{Name: "changes"}},
		},
		[]core.OutputField{
			{Field: core.NewField("comments")},
			{Field: core.NewField("summary")},
		},
	).WithInstruction(`Review the code changes and provide specific, actionable feedback.
For each issue found, output in this format:

comments:
  file: [filename]
  line: [specific line number where the issue occurs]
  severity: [must be one of: critical, warning, suggestion]
  category: [must be one of: error-handling, code-style, performance, security, documentation]
  content: [clear explanation of the issue and why it matters]
  suggestion: [specific code example or clear steps to fix the issue]

Review for these specific issues:
1. Error Handling
   - Missing error checks or ignored errors
   - Inconsistent error handling patterns
   - Silent failures
2. Code Quality
   - Function complexity and length
   - Code duplication
   - Unclear logic or control flow
3. Documentation
   - Missing documentation for exported items
   - Unclear or incomplete comments
4. Performance
   - Inefficient patterns
   - Resource leaks
   - Unnecessary allocations
5. Best Practices
   - Go idioms and conventions
   - Package organization
   - Clear naming conventions`)

	// Create predict module for review
	predict := modules.NewPredict(signature)

	metadata, err := extractReviewMetadata(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("task %s: %w", task.ID, err)
	}
	if metadata.FileContent == "" && metadata.Changes == "" {
		return nil, fmt.Errorf("both file content and changes cannot be empty for file %s", metadata.FilePath)
	}
	logger.Debug(ctx, "Extracted metadata for task %s: file_path=%s, content_length=%d",
		task.ID, metadata.FilePath, len(metadata.FileContent))
	// Process the review
	result, err := predict.Process(ctx, map[string]interface{}{
		"file_content": metadata.FileContent,
		"changes":      metadata.Changes,
	})
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	// Parse and format comments
	comments, err := extractComments(result, metadata.FilePath)

	if err != nil {
		return nil, fmt.Errorf("failed to parse comments for task %s: %w", task.ID, err)
	}

	logger.Debug(ctx, "Successfully processed review for task %s with %d comments",
		task.ID, len(comments))

	return comments, nil
}

// Helper functions.
func parseReviewComments(filePath string, commentsStr string) ([]PRReviewComment, error) {
	var comments []PRReviewComment

	// Parse the YAML-like format from the LLM response
	sections := strings.Split(commentsStr, "\n-")
	for _, section := range sections {
		if strings.TrimSpace(section) == "" {
			continue
		}

		// Extract comment fields
		comment := PRReviewComment{FilePath: filePath}

		// Parse each field
		lines := strings.Split(section, "\n")
		for _, line := range lines {
			parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
			if len(parts) != 2 {
				continue
			}

			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "line":
				if lineNum, err := strconv.Atoi(value); err == nil {
					comment.LineNumber = lineNum
				}
			case "severity":
				comment.Severity = value
			case "content":
				comment.Content = value
			case "suggestion":
				comment.Suggestion = value
			case "category":
				comment.Category = value
			}
		}

		// Add valid comments
		if comment.Content != "" {
			comments = append(comments, comment)
		}
	}

	return comments, nil
}

func extractComments(result interface{}, filePath string) ([]PRReviewComment, error) {
	if comments, ok := result.([]PRReviewComment); ok {
		return comments, nil
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid task result type: %T", result)
	}
	commentsRaw, exists := resultMap["comments"]
	if !exists {
		return nil, fmt.Errorf("prediction result missing 'comments' field")
	}

	commentsStr, ok := commentsRaw.(string)
	if !ok {
		return nil, fmt.Errorf("comments must be string, got %T", commentsRaw)
	}

	return parseReviewComments(filePath, commentsStr)
}

func extractReviewMetadata(metadata map[string]interface{}) (*ReviewMetadata, error) {
	rm := &ReviewMetadata{}

	// Extract category (always required)
	categoryRaw, exists := metadata["category"]
	if !exists {
		return nil, fmt.Errorf("missing required field 'category' in metadata")
	}
	category, ok := categoryRaw.(string)
	if !ok {
		return nil, fmt.Errorf("field 'category' must be string, got %T", categoryRaw)
	}
	rm.Category = category

	filePathRaw, exists := metadata["file_path"]
	if !exists {
		return nil, fmt.Errorf("missing required field 'file_path' for file review")
	}
	filePath, ok := filePathRaw.(string)
	if !ok {
		return nil, fmt.Errorf("field 'file_path' must be string, got %T", filePathRaw)
	}
	rm.FilePath = filePath

	// Extract changes (required for file reviews)
	changesRaw, exists := metadata["changes"]
	if !exists {
		return nil, fmt.Errorf("missing required field 'changes' for file review")
	}
	changes, ok := changesRaw.(string)
	if !ok {
		return nil, fmt.Errorf("field 'changes' must be string, got %T", changesRaw)
	}
	rm.Changes = changes

	if fileContent, ok := metadata["file_content"]; ok {
		if str, ok := fileContent.(string); ok {
			rm.FileContent = str
		}
	}

	return rm, nil
}

func main() {
	ctx := core.WithExecutionState(context.Background())
	apiKey := flag.String("api-key", "", "Anthropic API Key")
	githubToken := flag.String("github-token", os.Getenv("GITHUB_TOKEN"), "GitHub Token")
	owner := flag.String("owner", "", "Repository owner")
	repo := flag.String("repo", "", "Repository name")
	prNumber := flag.Int("pr", 0, "Pull Request number")
	debug := flag.Bool("debug", false, "Enable debug logging")
	verifyOnly := flag.Bool("verify-only", false, "Only verify token permissions without running review")

	flag.Parse()

	if *githubToken == "" || *owner == "" || *repo == "" || *prNumber == 0 {
		fmt.Println("Missing required flags. Please provide:")
		fmt.Println("  -github-token or set GITHUB_TOKEN")
		fmt.Println("  -owner (repository owner)")
		fmt.Println("  -repo (repository name)")
		fmt.Println("  -pr (pull request number)")
		os.Exit(1)
	}
	logLevel := logging.INFO
	if *debug {
		logLevel = logging.DEBUG
	}

	output := logging.NewConsoleOutput(true, logging.WithColor(true))

	logger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  []logging.Output{output},
	})
	logging.SetLogger(logger)

	console := NewConsole(os.Stdout, logger)
	err := VerifyTokenPermissions(ctx, *githubToken, *owner, *repo)
	if err != nil {
		logger.Error(ctx, "Token permission verification failed: %v", err)
		os.Exit(1)
	}

	if *verifyOnly {
		os.Exit(0)
	}
	llms.EnsureFactory()

	err = core.ConfigureDefaultLLM(*apiKey, "llamacpp:")
	//	err = core.ConfigureDefaultLLM(*apiKey, "ollama:deepseek-r1:14b-qwen-distill-q4_K_M")
	if err != nil {
		logger.Error(ctx, "Failed to configure LLM: %v", err)
	}
	agent, err := NewPRReviewAgent()
	if err != nil {
		panic(err)
	}

	githubTools := NewGitHubTools(*githubToken, *owner, *repo)
	logger.Info(ctx, "Fetching changes for PR #%d", *prNumber)
	pr, _, err := githubTools.client.PullRequests.Get(ctx, *owner, *repo, *prNumber)
	console.StartReview(pr)

	changes, err := githubTools.GetPullRequestChanges(ctx, *prNumber)
	if err != nil {
		logger.Error(ctx, "Failed to get PR changes: %v", err)
		os.Exit(1)
	}
	tasks := make([]PRReviewTask, 0, len(changes.Files))
	for _, file := range changes.Files {
		// Log file being processed
		logger.Info(ctx, "Processing file: %s (+%d/-%d lines)",
			file.FilePath,
			file.Additions,
			file.Deletions,
		)

		tasks = append(tasks, PRReviewTask{
			FilePath:    file.FilePath,
			FileContent: file.FileContent,
			Changes:     file.Patch,
		})
	}
	if err != nil {
		logger.Error(ctx, "Failed to get PR changes: %v", err)
		os.Exit(1)
	}

	logger.Info(ctx, "Starting code review for %d files", len(tasks))

	comments, err := agent.ReviewPR(ctx, tasks, console)
	if err != nil {
		logger.Error(ctx, "Failed to review PR: %v", err)
		os.Exit(1)
	}
	console.ShowSummary(comments)

	logger.Info(ctx, "Posting review comments to GitHub")
	// err = githubTools.CreateReviewComments(ctx, *prNumber, comments)
	// if err != nil {
	// 	logger.Error(ctx, "Failed to post review comments: %v", err)
	// 	os.Exit(1)
	// }

	console.ReviewComplete()
	logger.Info(ctx, "Successfully completed PR review")
}
