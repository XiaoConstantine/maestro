package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
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
	Category   string    // e.g., "security", "performance", "style"
	InReplyTo  *int64    // ID of the parent comment
	ThreadID   *int64    // Unique thread identifier
	Resolved   bool      // Track if the discussion is resolved
	Timestamp  time.Time // When the comment was made
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
			MaxAttempts:       2,
			BackoffMultiplier: 2.0,
		},
		CustomProcessors: map[string]agents.TaskProcessor{
			"code_review":      &CodeReviewProcessor{},
			"comment_response": &CommentResponseProcessor{},
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
		// Execute review for this chunk
		// 	result, err := a.orchestrator.Process(ctx,
		// 		fmt.Sprintf("Review chunk %d of %s", i+1, task.FilePath),
		// 		chunkContext)
		// 	if err != nil {
		// 		return nil, fmt.Errorf("failed to process chunk %d of %s: %w", i+1, task.FilePath, err)
		// 	}
		//
		// 	// Collect comments from this chunk
		// 	for taskID, taskResult := range result.CompletedTasks {
		// 		// Convert task results to comments
		// 		if reviewComments, err := extractComments(taskResult, task.FilePath); err != nil {
		// 			logging.GetLogger().Error(ctx, "Failed to extract comments from task %s: %v", taskID, err)
		// 			continue
		// 		} else {
		// 			// Show the results for this file
		// 			if len(reviewComments) == 0 {
		// 				console.NoIssuesFound(task.FilePath)
		// 			} else {
		// 				console.ShowComments(reviewComments)
		// 			}
		// 			allComments = append(allComments, reviewComments...)
		// 		}
		// 	}
		// }
	}
	return allComments, nil
}
