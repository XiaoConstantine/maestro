package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// PRInfo represents basic PR information from gh CLI.
type PRInfo struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
}

// reviewPRWithPersistentMCP reviews a PR using persistent MCP bash helper.
func reviewPRWithPersistentMCP(ctx context.Context, prNumber int, console ConsoleInterface, bashHelper *MCPBashHelper) error {
	logger := logging.GetLogger()

	if bashHelper == nil {
		return fmt.Errorf("MCP bash helper not available")
	}

	console.Printf("↳ Fetching PR #%d information via gh CLI...\n", prNumber)

	// Get PR information using gh CLI
	prInfoJSON, err := bashHelper.ExecuteGHCommand(ctx, "pr", "view", fmt.Sprintf("%d", prNumber), "--json", "number,title,body,author,headRefName,baseRefName")
	if err != nil {
		if strings.Contains(err.Error(), "gh auth login") || strings.Contains(err.Error(), "GH_TOKEN") {
			return fmt.Errorf("GitHub CLI not authenticated. Please run 'gh auth login' or set GH_TOKEN environment variable")
		}
		return fmt.Errorf("PR #%d not found: %w", prNumber, err)
	}

	// Parse PR information
	var prInfo PRInfo
	if err := json.Unmarshal([]byte(prInfoJSON), &prInfo); err != nil {
		return fmt.Errorf("failed to parse PR information: %w", err)
	}

	console.Printf("\nReviewing PR #%d: %s\n", prInfo.Number, prInfo.Title)
	console.Printf("Author     : %s\n", prInfo.Author.Login)
	console.Printf("Branch     : %s -> %s\n", prInfo.HeadRefName, prInfo.BaseRefName)

	// Get PR diff using gh CLI
	console.Println("↳ Fetching PR changes...")
	diffOutput, err := bashHelper.ExecuteGHCommand(ctx, "pr", "diff", fmt.Sprintf("%d", prNumber), "--name-only")
	if err != nil {
		return fmt.Errorf("failed to get PR changes: %w", err)
	}

	changedFiles := strings.Split(strings.TrimSpace(diffOutput), "\n")
	if len(changedFiles) == 0 || (len(changedFiles) == 1 && changedFiles[0] == "") {
		return fmt.Errorf("no reviewable files found in PR #%d", prNumber)
	}

	console.Printf("Files      : %d files changed\n", len(changedFiles))

	// Show the changed files
	console.Println("\nChanged files:")
	for i, file := range changedFiles {
		if strings.TrimSpace(file) != "" {
			console.Printf("  %d. %s\n", i+1, file)
		}
	}

	// Get detailed diff for review
	console.Println("\n↳ Getting detailed changes for analysis...")
	detailedDiff, err := bashHelper.ExecuteGHCommand(ctx, "pr", "diff", fmt.Sprintf("%d", prNumber))
	if err != nil {
		logger.Debug(ctx, "Failed to get detailed diff: %v", err)
		detailedDiff = ""
	}

	// For now, just show that we've successfully fetched the data
	// In a full implementation, you would:
	// 1. Parse the diff and extract meaningful changes
	// 2. Run the changes through your review agent
	// 3. Generate review comments
	// 4. Post the review back using gh CLI

	console.Printf("\n✅ Successfully fetched PR data using MCP bash tool!\n")
	console.Printf("📊 Diff size: %d characters\n", len(detailedDiff))

	// Show first few lines of diff as proof of concept
	if detailedDiff != "" {
		lines := strings.Split(detailedDiff, "\n")
		console.Println("\nFirst 5 lines of diff:")
		for i, line := range lines {
			if i >= 5 {
				break
			}
			console.Printf("  %s\n", line)
		}
		if len(lines) > 5 {
			console.Printf("  ... (%d more lines)\n", len(lines)-5)
		}
	}

	// Demonstrate posting a comment (commented out to avoid spam)
	console.Println("\n💡 To post a review comment, you could run:")
	console.Printf("   gh pr review %d --comment --body \"Review comment here\"\n", prNumber)

	return nil
}

// GetPullRequestChangesWithMCP retrieves PR changes using MCP bash helper instead of GitHub API.
func GetPullRequestChangesWithMCP(ctx context.Context, prNumber int, bashHelper *MCPBashHelper) (*PRChanges, error) {
	logger := logging.GetLogger()

	if bashHelper == nil {
		return nil, fmt.Errorf("MCP bash helper not available")
	}

	// Get list of changed files
	filesOutput, err := bashHelper.ExecuteGHCommand(ctx, "pr", "diff", fmt.Sprintf("%d", prNumber), "--name-only")
	if err != nil {
		return nil, fmt.Errorf("failed to get changed files: %w", err)
	}

	changedFiles := strings.Split(strings.TrimSpace(filesOutput), "\n")
	if len(changedFiles) == 0 || (len(changedFiles) == 1 && changedFiles[0] == "") {
		return nil, fmt.Errorf("no files found in PR #%d", prNumber)
	}

	logger.Debug(ctx, "Retrieved %d files from PR via MCP", len(changedFiles))

	changes := &PRChanges{
		Files: make([]PRFileChange, 0, len(changedFiles)),
	}

	// Get full diff for parsing patches
	fullDiff, err := bashHelper.ExecuteGHCommand(ctx, "pr", "diff", fmt.Sprintf("%d", prNumber))
	if err != nil {
		return nil, fmt.Errorf("failed to get PR diff: %w", err)
	}

	// Parse the diff to extract per-file patches and stats
	fileDiffs := parseDiffOutput(fullDiff)

	for _, filename := range changedFiles {
		filename = strings.TrimSpace(filename)
		if filename == "" {
			continue
		}

		logger.Debug(ctx, "Processing file: %s", filename)

		// Skip files we don't want to review
		if shouldSkipFile(filename) {
			logger.Debug(ctx, "Skipping file: %s (matched skip criteria)", filename)
			continue
		}

		// Get file diff info
		fileDiff, exists := fileDiffs[filename]
		if !exists {
			logger.Warn(ctx, "Could not find diff for file %s", filename)
			continue
		}

		fileChange := PRFileChange{
			FilePath:  filename,
			Patch:     fileDiff.patch,
			Additions: fileDiff.additions,
			Deletions: fileDiff.deletions,
		}

		// Parse hunks for this file
		hunks, err := parseHunks(fileDiff.patch, filename)
		if err != nil {
			return nil, fmt.Errorf("failed to parse hunks for %s: %w", filename, err)
		}
		fileChange.Hunks = hunks

		// Get file content based on status
		if fileDiff.status == "removed" {
			// For removed files, we'd need the previous content
			// For now, leave empty - the patch contains the removed content
			fileChange.FileContent = ""
		} else {
			// Get current file content using gh CLI
			content, err := getFileContentWithMCP(ctx, bashHelper, filename)
			if err != nil {
				logger.Warn(ctx, "Could not get content for file %s: %v", filename, err)
				fileChange.FileContent = ""
			} else {
				fileChange.FileContent = content
			}
		}

		changes.Files = append(changes.Files, fileChange)
	}

	return changes, nil
}

// FileDiffInfo holds diff information for a single file.
type FileDiffInfo struct {
	patch     string
	additions int
	deletions int
	status    string // added, modified, removed
}

// parseDiffOutput parses the output of `gh pr diff` and extracts per-file information.
func parseDiffOutput(diffOutput string) map[string]FileDiffInfo {
	fileDiffs := make(map[string]FileDiffInfo)

	lines := strings.Split(diffOutput, "\n")
	var currentFile string
	var currentPatch strings.Builder
	var additions, deletions int
	var status string

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			// Save previous file if exists
			if currentFile != "" {
				fileDiffs[currentFile] = FileDiffInfo{
					patch:     currentPatch.String(),
					additions: additions,
					deletions: deletions,
					status:    status,
				}
			}

			// Extract filename from diff --git a/file b/file
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				currentFile = strings.TrimPrefix(parts[3], "b/")
			}
			currentPatch.Reset()
			additions, deletions = 0, 0
			status = "modified"
		} else if strings.HasPrefix(line, "new file mode") {
			status = "added"
		} else if strings.HasPrefix(line, "deleted file mode") {
			status = "removed"
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			additions++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			deletions++
		}

		// Add line to current patch
		currentPatch.WriteString(line)
		currentPatch.WriteString("\n")
	}

	// Save last file
	if currentFile != "" {
		fileDiffs[currentFile] = FileDiffInfo{
			patch:     currentPatch.String(),
			additions: additions,
			deletions: deletions,
			status:    status,
		}
	}

	return fileDiffs
}

// getFileContentWithMCP gets the current content of a file using gh CLI.
func getFileContentWithMCP(ctx context.Context, bashHelper *MCPBashHelper, filename string) (string, error) {
	// First try to get it from the local working directory
	// This is more reliable and faster than API calls
	content, err := bashHelper.ExecuteCommand(ctx, fmt.Sprintf("cat '%s'", filename))
	if err != nil {
		// If local file access fails, try gh CLI to show file from repo
		content, err = bashHelper.ExecuteCommand(ctx, fmt.Sprintf("gh repo view --web=false && cat '%s'", filename))
		if err != nil {
			return "", fmt.Errorf("failed to get file content: %w", err)
		}
	}

	return content, nil
}

// base64DecodeContent decodes base64 content and handles line breaks.
func base64DecodeContent(encoded string) (string, error) {
	// Remove any whitespace/newlines from base64 string
	cleaned := strings.ReplaceAll(encoded, "\n", "")
	cleaned = strings.ReplaceAll(cleaned, " ", "")

	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return "", err
	}

	return string(decoded), nil
}
