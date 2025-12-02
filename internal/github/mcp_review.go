package github

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/types"
)

// GetPullRequestChangesWithMCP retrieves PR changes using MCP bash helper instead of GitHub API.
// It explicitly scopes gh commands to the provided owner/repo to avoid cwd-dependent behavior.
// localRepoPath is the path to the cloned repo on disk for reading file contents.
func GetPullRequestChangesWithMCP(ctx context.Context, owner, repo string, prNumber int, bashHelper *MCPBashHelper, localRepoPath string) (*types.PRChanges, error) {
	logger := logging.GetLogger()

	if bashHelper == nil {
		return nil, fmt.Errorf("MCP bash helper not available")
	}

	repoArg := fmt.Sprintf("--repo=%s/%s", owner, repo)

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

	changes := &types.PRChanges{
		Files: make([]types.PRFileChange, 0, len(changedFiles)),
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
		if ShouldSkipFile(filename) {
			logger.Debug(ctx, "Skipping file: %s (matched skip criteria)", filename)
			continue
		}

		// Get file diff info
		fileDiff, exists := fileDiffs[filename]
		if !exists {
			logger.Warn(ctx, "Could not find diff for file %s", filename)
			continue
		}

		fileChange := types.PRFileChange{
			FilePath:  filename,
			Patch:     fileDiff.patch,
			Additions: fileDiff.additions,
			Deletions: fileDiff.deletions,
		}

		// Parse hunks for this file
		hunks, err := ParseHunks(fileDiff.patch, filename)
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
			// Read file content from local clone
			if localRepoPath != "" {
				localPath := filepath.Join(localRepoPath, filename)
				content, err := os.ReadFile(localPath)
				if err != nil {
					logger.Warn(ctx, "Skipping file %s: could not read from local clone: %v", filename, err)
					continue
				}
				fileChange.FileContent = string(content)
			} else {
				logger.Warn(ctx, "Skipping file %s: no local repo path provided", filename)
				continue
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

			// Extract filename from "diff --git a/path b/path"
			// Find the " b/" marker which separates the two paths
			currentFile = extractFilenameFromDiffLine(line)
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

// extractFilenameFromDiffLine extracts the destination filename from a diff --git line.
// Format: "diff --git a/path/to/file b/path/to/file"
// For renames: "diff --git a/old/path b/new/path"
// We want the "b/" path (destination file).
func extractFilenameFromDiffLine(line string) string {
	// Find " b/" which marks the start of the destination path
	bMarker := " b/"
	idx := strings.LastIndex(line, bMarker)
	if idx != -1 {
		return line[idx+len(bMarker):]
	}

	// Fallback: try splitting on spaces (original logic)
	parts := strings.Fields(line)
	if len(parts) >= 4 {
		return strings.TrimPrefix(parts[3], "b/")
	}

	return ""
}
