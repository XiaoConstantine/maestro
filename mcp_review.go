package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
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

// (removed unused reviewPRWithPersistentMCP)

// GetPullRequestChangesWithMCP retrieves PR changes using MCP bash helper instead of GitHub API.
// It explicitly scopes gh commands to the provided owner/repo to avoid cwd-dependent behavior.
func GetPullRequestChangesWithMCP(ctx context.Context, owner, repo string, prNumber int, bashHelper *MCPBashHelper) (*PRChanges, error) {
	logger := logging.GetLogger()

	if bashHelper == nil {
		return nil, fmt.Errorf("MCP bash helper not available")
	}

	repoArg := fmt.Sprintf("--repo=%s/%s", owner, repo)
	// Get PR head SHA to fetch file contents at the correct ref
	headSHA, err := getPRHeadSHA(ctx, bashHelper, owner, repo, prNumber)
	if err != nil || headSHA == "" {
		return nil, fmt.Errorf("failed to get PR head SHA: %w", err)
	}
	// Get list of changed files
	filesOutput, err := bashHelper.ExecuteGHCommand(ctx, "pr", "diff", repoArg, fmt.Sprintf("%d", prNumber), "--name-only")
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
	fullDiff, err := bashHelper.ExecuteGHCommand(ctx, "pr", "diff", repoArg, fmt.Sprintf("%d", prNumber))
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
			// Get current file content using gh API at PR head SHA
			content, err := getFileContentWithMCP(ctx, bashHelper, owner, repo, headSHA, filename)
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

// getFileContentWithMCP fetches file content from GitHub at a specific ref using gh api.
func getFileContentWithMCP(ctx context.Context, bashHelper *MCPBashHelper, owner, repo, ref, filename string) (string, error) {
	// Build gh api path; filename should retain slashes, only ref needs query escaping
	refEscaped := url.QueryEscape(ref)
	apiPath := fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s", owner, repo, filename, refEscaped)
	resp, err := bashHelper.ExecuteGHCommand(ctx, "api", apiPath)
	if err != nil {
		return "", fmt.Errorf("gh api failed for %s: %w", filename, err)
	}

	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal([]byte(resp), &payload); err != nil {
		return "", fmt.Errorf("failed to parse gh api response for %s: %w", filename, err)
	}
	if strings.TrimSpace(payload.Content) == "" {
		return "", fmt.Errorf("empty content for %s at ref %s", filename, ref)
	}
	// GitHub returns base64 with newlines; strip and decode
	cleaned := strings.ReplaceAll(payload.Content, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to decode content for %s: %w", filename, err)
	}
	return string(decoded), nil
}

// getPRHeadSHA returns the head commit SHA for a PR via gh pr view.
func getPRHeadSHA(ctx context.Context, bashHelper *MCPBashHelper, owner, repo string, prNumber int) (string, error) {
	repoArg := fmt.Sprintf("--repo=%s/%s", owner, repo)
	out, err := bashHelper.ExecuteGHCommand(ctx, "pr", "view", repoArg, fmt.Sprintf("%d", prNumber), "--json", "headRefOid")
	if err != nil {
		return "", err
	}
	var payload struct {
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return "", err
	}
	return payload.HeadRefOid, nil
}

// (removed unused base64DecodeContent)
