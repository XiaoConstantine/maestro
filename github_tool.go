package main

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/google/go-github/v68/github"
	"github.com/logrusorgru/aurora"
	"golang.org/x/oauth2"
)

// GitHubTools handles interactions with GitHub API.
type GitHubTools struct {
	client            *github.Client
	owner             string
	repo              string
	authenticatedUser string
}

// NewGitHubTools creates a new GitHub tools instance.
func NewGitHubTools(token, owner, repo string) *GitHubTools {
	// Create an authenticated GitHub client
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	user, _, err := client.Users.Get(ctx, "")
	if err != nil {
		return nil
	}
	return &GitHubTools{
		client:            client,
		owner:             owner,
		repo:              repo,
		authenticatedUser: user.GetLogin(),
	}
}

// PRChanges contains changes made in a pull request.
type PRChanges struct {
	Files []PRFileChange
}

type ChangeHunk struct {
	FilePath  string
	StartLine int
	EndLine   int
	Content   string
	Context   struct {
		Before string
		After  string
	}
	Position int // Position in the diff for GitHub API
}

// PRFileChange represents changes to a single file.
type PRFileChange struct {
	FilePath    string
	FileContent string // The complete file content
	Patch       string // The diff/patch content
	Additions   int
	Deletions   int
	Hunks       []ChangeHunk
}

type PreviewOptions struct {
	ShowColors      bool   // Use ANSI colors for formatting
	ContextLines    int    // Number of context lines around changes
	FilePathStyle   string // How to display file paths (full, relative, basename)
	ShowLineNumbers bool   // Whether to show line numbers
}

type DiffPosition struct {
	Path     string // File path
	Line     int    // Line number in the new file
	Position int    // Position in the diff (required by GitHub API)
}

// CodeContext represents lines of code around a specific line.
type CodeContext struct {
	StartLine int
	Lines     []string
}

type fileFilterRules struct {
	// Simple path contains matches - fastest check
	pathContains []string

	// File extension matches - very fast check
	extensions []string

	// Regex patterns for complex matches
	regexPatterns []*regexp.Regexp
}

// GetPullRequestChanges retrieves the changes from a pull request.
func (g *GitHubTools) GetPullRequestChanges(ctx context.Context, prNumber int) (*PRChanges, error) {
	// Get the list of files changed in the PR
	logger := logging.GetLogger()
	files, _, err := g.client.PullRequests.ListFiles(ctx, g.owner, g.repo, prNumber, &github.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list PR files: %w", err)
	}

	logger.Debug(ctx, "Retrieved %d files from PR", len(files))
	changes := &PRChanges{
		Files: make([]PRFileChange, 0, len(files)),
	}

	for _, file := range files {
		// Skip files we don't want to review (like dependencies or generated files)
		filename := file.GetFilename()
		logger.Debug(ctx, "Processing file: %s", filename)

		if shouldSkipFile(filename) {
			logger.Debug(ctx, "Skipping file: %s (matched skip criteria)", filename)

			continue
		}
		fileChange := PRFileChange{
			FilePath:  file.GetFilename(),
			Patch:     file.GetPatch(),
			Additions: file.GetAdditions(),
			Deletions: file.GetDeletions(),
		}

		// Parse hunks for this file
		hunks, err := parseHunks(file.GetPatch(), file.GetFilename())
		if err != nil {
			return nil, fmt.Errorf("failed to parse hunks for %s: %w",
				file.GetFilename(), err)
		}
		fileChange.Hunks = hunks

		// Get file content if needed
		if file.GetStatus() != "removed" {
			content, err := g.GetFileContent(ctx, file.GetFilename())
			if err != nil {
				return nil, fmt.Errorf("failed to get content for %s: %w",
					file.GetFilename(), err)
			}
			fileChange.FileContent = content
		}

		changes.Files = append(changes.Files, fileChange)
	}

	if len(changes.Files) == 0 {
		return nil, fmt.Errorf("no reviewable files found in PR #%d", prNumber)
	}
	return changes, nil
}

// CreateReviewComments posts review comments back to GitHub.
func (g *GitHubTools) CreateReviewComments(ctx context.Context, prNumber int, comments []PRReviewComment) error {
	changes, err := g.GetPullRequestChanges(ctx, prNumber)
	if err != nil {
		return fmt.Errorf("Failed to get changes for: %d", prNumber)
	}
	// Map file paths to their hunks for quick lookup
	hunksByFile := make(map[string][]ChangeHunk)
	for _, file := range changes.Files {
		hunksByFile[file.FilePath] = file.Hunks
	}

	var validComments []*github.DraftReviewComment

	for _, comment := range comments {
		// Find hunks for this file
		hunks, exists := hunksByFile[comment.FilePath]
		if !exists {
			continue
		}

		// Find the hunk containing this line
		var targetHunk *ChangeHunk
		for i := range hunks {
			if comment.LineNumber >= hunks[i].StartLine &&
				comment.LineNumber <= hunks[i].EndLine {
				targetHunk = &hunks[i]
				break
			}
		}

		if targetHunk == nil {
			logging.GetLogger().Warn(ctx,
				"Skipping comment - line %d not in any change hunk for %s",
				comment.LineNumber, comment.FilePath)
			continue
		}

		// Create GitHub comment using hunk's position
		body := formatCommentBody(comment)
		validComments = append(validComments, &github.DraftReviewComment{
			Path:     &comment.FilePath,
			Position: github.Ptr(targetHunk.Position),
			Body:     &body,
		})
	}

	if len(validComments) == 0 {
		return fmt.Errorf("no valid comments to create")
	}

	// Create the review
	review := &github.PullRequestReviewRequest{
		CommitID: nil,
		Body:     github.Ptr("Code Review Comments"),
		Event:    github.Ptr("COMMENT"),
		Comments: validComments,
	}

	_, _, err = g.client.PullRequests.CreateReview(ctx, g.owner, g.repo,
		prNumber, review)
	return err
}

func (g *GitHubTools) MonitorPRComments(ctx context.Context, prNumber int, callback func(comment *github.PullRequestComment)) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	lastChecked := time.Now().Add(-5 * time.Minute)
	logger := logging.GetLogger()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			comments, _, err := g.client.PullRequests.ListComments(ctx, g.owner, g.repo, prNumber, &github.PullRequestListCommentsOptions{
				Since:     lastChecked,
				Sort:      "created",
				Direction: "desc",
			})
			if err != nil {
				logger.Warn(ctx, "Failed to fetch comments: %v", err)
				continue
			}

			for _, comment := range comments {
				if comment.GetUser().GetLogin() != g.authenticatedUser {
					logger.Info(ctx, "Processing comment from user %s",
						comment.GetUser().GetLogin())
					callback(comment)
				} else {
					logger.Debug(ctx, "Skipping comment from authenticated user %s on file %s",
						g.authenticatedUser, comment.GetPath())
				}
			}

			lastChecked = time.Now()
		}
	}
}

// GetFileContent retrieves the content of a specific file from the repository.
// This uses the GitHub API's GetContents endpoint to fetch file content.
func (g *GitHubTools) GetFileContent(ctx context.Context, filePath string) (string, error) {
	// Get the content using GitHub's API
	content, _, resp, err := g.client.Repositories.GetContents(
		ctx,
		g.owner,
		g.repo,
		filePath,
		&github.RepositoryContentGetOptions{},
	)

	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return "", fmt.Errorf("file not found: %s", filePath)
		}
		return "", fmt.Errorf("failed to get file content: %w", err)
	}

	// content will be nil for directories
	if content == nil {
		return "", fmt.Errorf("no content available for %s", filePath)
	}

	// Decode the content
	fileContent, err := content.GetContent()
	if err != nil {
		return "", fmt.Errorf("failed to decode content: %w", err)
	}

	return fileContent, nil
}

func (g *GitHubTools) GetLatestCommitSHA(ctx context.Context, branch string) (string, error) {

	if branch == "" {
		repo, _, err := g.client.Repositories.Get(ctx, g.owner, g.repo)
		if err != nil {
			return "", fmt.Errorf("failed to get repository info: %w", err)
		}
		branch = repo.GetDefaultBranch()
	}
	ref, _, err := g.client.Git.GetRef(ctx, g.owner, g.repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		return "", fmt.Errorf("failed to get ref for branch %s: %w", branch, err)
	}
	return ref.Object.GetSHA(), nil
}

func (g *GitHubTools) PreviewReview(ctx context.Context, console *Console, prNumber int, comments []PRReviewComment, metric *BusinessMetrics) (bool, error) {
	// Use spinner while fetching PR changes
	var changes *PRChanges
	err := console.WithSpinner(ctx, "Fetching PR changes", func() error {
		var err error
		changes, err = g.GetPullRequestChanges(ctx, prNumber)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("failed to get PR changes: %w", err)
	}
	var validComments []PRReviewComment

	var skippedComments []PRReviewComment

	for _, comment := range comments {
		isValid := false
		for _, file := range changes.Files {
			if file.FilePath != comment.FilePath {
				continue
			}

			// Check if comment is in any hunk
			for _, hunk := range file.Hunks {
				if comment.LineNumber >= hunk.StartLine &&
					comment.LineNumber <= hunk.EndLine {
					isValid = true
					break
				}
			}
		}

		if isValid {
			validComments = append(validComments, comment)
		} else {
			skippedComments = append(skippedComments, comment)
			console.printf("\nNote: Comment on %s line %d will be skipped (unchanged line)\n",
				comment.FilePath, comment.LineNumber)
		}
	}
	if len(skippedComments) != 0 {
		console.printf("\nSkipping :%d comments", len(skippedComments))
	}
	if len(validComments) == 0 {
		console.println("\nNo comments can be posted - all comments are on unchanged lines")
		return false, nil
	}
	// Group comments by file
	commentsByFile := make(map[string][]PRReviewComment)
	for _, comment := range validComments {
		if comment.LineNumber <= 0 {
			logging.GetLogger().Warn(ctx,
				"Skipping comment with invalid line number %d for file %s",
				comment.LineNumber,
				comment.FilePath)
			continue
		}
		commentsByFile[comment.FilePath] = append(commentsByFile[comment.FilePath], comment)
	}

	// Print preview header
	if console.color {
		console.printHeader(aurora.Bold("Pull Request Review Preview").String())
	} else {
		console.printHeader("Pull Request Review Preview")
	}

	// For each file with comments
	for filePath, fileComments := range commentsByFile {
		// Find the file changes
		var fileChange *PRFileChange
		for i := range changes.Files {
			if changes.Files[i].FilePath == filePath {
				fileChange = &changes.Files[i]
				break
			}
		}
		if fileChange == nil {
			continue
		}

		// Print file header with styling
		if console.color {
			console.println(aurora.Blue("📄").String(), aurora.Bold(filePath).String())
		} else {
			console.printf("📄 %s\n", filePath)
		}
		console.println(strings.Repeat("─", 80))

		// Sort comments by line number
		sort.Slice(fileComments, func(i, j int) bool {
			return fileComments[i].LineNumber < fileComments[j].LineNumber
		})

		shownLines := make(map[int]bool)

		// Print each comment in context
		for _, comment := range fileComments {
			if comment.LineNumber <= 0 {
				continue
			}
			// Extract context around the comment
			context, err := extractContext(fileChange.FileContent, comment.LineNumber, 3)
			if err != nil {
				logging.GetLogger().Warn(ctx, "Failed to extract context for file %s line %d: %v",
					filePath, comment.LineNumber, err)
				continue
			}

			if !shownLines[comment.LineNumber] {
				// Print the code context with gutters
				console.println(aurora.Cyan("┃").String() + " " +
					aurora.Cyan("┃").String() + " " +
					aurora.Cyan("┃").String())

				for i, line := range context.Lines {
					lineNum := context.StartLine + i

					shownLines[lineNum] = true
					if lineNum == comment.LineNumber {
						// Highlight commented line
						if console.color {
							console.printf("%s %4d %s %s\n",
								aurora.Blue("┃").String(),
								lineNum,
								aurora.Blue("│").String(),
								aurora.Cyan(line).String())
						} else {
							console.printf("┃ %4d │ %s\n", lineNum, line)
						}
					} else {
						if console.color {
							console.printf("%s %4d %s %s\n",
								aurora.Blue("┃").String(),
								lineNum,
								aurora.Blue("│").String(),
								line)
						} else {
							console.printf("┃ %4d │ %s\n", lineNum, line)
						}
					}
				}

				// Print the review comment using existing console methods
				console.println(aurora.Cyan("┃").String() + " " +
					aurora.Cyan("┃").String() + " " +
					aurora.Cyan("┃").String())
			}
			// Print severity icon and comment
			icon := console.severityIcon(comment.Severity)
			if console.color {
				console.printf("%s %s:\n", icon, aurora.Bold(strings.ToUpper(comment.Severity)))
			} else {
				console.printf("%s %s:\n", icon, strings.ToUpper(comment.Severity))
			}

			console.println(indent(comment.Content, 4))

			// Print suggestion if present
			if comment.Suggestion != "" {
				if console.color {
					console.println(aurora.Green("  ✨ Suggestion:").String())
				} else {
					console.println("  ✨ Suggestion:")
				}
				console.println(indent(comment.Suggestion, 4))
			}

			// Print category
			if console.color {
				console.printf("\n  %s %s: %s\n\n",
					aurora.Blue("🏷").String(),
					aurora.Blue("Category").String(),
					comment.Category)
			} else {
				console.printf("\n  🏷 Category: %s\n\n", comment.Category)
			}
		}
	}

	// Print summary using existing console method
	console.ShowSummary(comments, metric)

	shouldPost, err := console.ConfirmReviewPost(len(comments))
	if err != nil {
		return false, fmt.Errorf("failed to get confirmation: %w", err)
	}

	if !shouldPost {
		if console.color {
			console.println(aurora.Yellow("\nReview cancelled - no comments posted").String())
		} else {
			console.println("\nReview cancelled - no comments posted")
		}
		return false, nil
	}

	return true, nil
}

func newFileFilterRules() *fileFilterRules {
	// Simple contains matches for common paths
	pathContains := []string{
		"vendor/",
		"generated/",
		"node_modules/",
		".git",
		"dist/",
		"build/",
	}

	// Direct extension matches
	extensions := []string{
		".pb.go",   // Generated protobuf
		".gen.go",  // Other generated files
		".md",      // Documentation
		".txt",     // Text files
		".yaml",    // Config files
		".yml",     // Config files
		".json",    // Config files
		".lock",    // Lock files
		".sum",     // Checksum files
		".min.js",  // Minified JavaScript
		".min.css", // Minified CSS
	}

	// Complex patterns that need regex
	patterns := []string{
		// Generated code patterns
		`\.generated\..*$`,
		`_generated\..*$`,

		// IDE and system files
		`\.idea/.*$`,
		`\.vscode/.*$`,
		`\.DS_Store$`,

		// Test fixtures and data files
		`testdata/.*$`,
		`fixtures/.*$`,

		// Build artifacts
		`\.exe$`,
		`\.dll$`,
		`\.so$`,
		`\.dylib$`,
	}

	// Compile all regex patterns
	regexPatterns := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		regex := regexp.MustCompile(pattern)
		regexPatterns = append(regexPatterns, regex)
	}

	return &fileFilterRules{
		pathContains:  pathContains,
		extensions:    extensions,
		regexPatterns: regexPatterns,
	}
}

var (
	filterRules     *fileFilterRules
	filterRulesOnce sync.Once
)

// getFilterRules returns the singleton instance of filter rules.
func getFilterRules() *fileFilterRules {
	filterRulesOnce.Do(func() {
		filterRules = newFileFilterRules()
	})
	return filterRules
}

// Helper functions.
func shouldSkipFile(filename string) bool {
	// Use a singleton instance of filter rules
	rules := getFilterRules()

	// 1. Check specific filenames first (fastest)
	specificFiles := map[string]bool{
		"go.mod":            true,
		"go.sum":            true,
		"package-lock.json": true,
		"yarn.lock":         true,
		"Cargo.lock":        true,
	}
	if specificFiles[filename] {
		return true
	}

	// 2. Check path contains (very fast)
	for _, path := range rules.pathContains {
		if strings.Contains(filename, path) {
			return true
		}
	}

	// 3. Check file extensions (fast)
	ext := filepath.Ext(filename)
	for _, skipExt := range rules.extensions {
		if ext == skipExt || strings.HasSuffix(filename, skipExt) {
			return true
		}
	}

	// 4. Check regex patterns (slower but handles complex cases)
	for _, pattern := range rules.regexPatterns {
		if pattern.MatchString(filename) {
			return true
		}
	}

	return false
}

func formatCommentBody(comment PRReviewComment) string {
	var sb strings.Builder

	// Add severity indicator
	sb.WriteString(fmt.Sprintf("**%s**: ", strings.ToUpper(comment.Severity)))

	// Add the main comment
	sb.WriteString(comment.Content)

	// Add suggestion if present
	if comment.Suggestion != "" {
		sb.WriteString("\n\n**Suggestion:**\n")
		sb.WriteString(comment.Suggestion)
	}

	// Add category tag
	sb.WriteString(fmt.Sprintf("\n\n_Category: %s_", comment.Category))

	return sb.String()
}

func VerifyTokenPermissions(ctx context.Context, token, owner, repo string) error {
	// Create an authenticated client
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// First, let's check the token's basic information
	fmt.Println("Checking token permissions...")

	// Check token validity and scopes
	user, resp, err := client.Users.Get(ctx, "") // Empty string gets authenticated user
	if err != nil {
		if resp != nil && resp.StatusCode == 401 {
			return fmt.Errorf("invalid token or token has expired")
		}
		return fmt.Errorf("error checking token: %w", err)
	}

	fmt.Printf("\nToken belongs to user: %s\n", user.GetLogin())
	fmt.Printf("Token scopes: %s\n", resp.Header.Get("X-OAuth-Scopes"))

	fmt.Printf("Checking access to repository: %s/%s\n", owner, repo)

	// Now let's check specific permissions we need
	permissionChecks := []struct {
		name  string
		check func() error
	}{
		{
			name: "Repository read access",
			check: func() error {
				_, resp, err := client.Repositories.Get(ctx, owner, repo)
				if err != nil {
					if resp != nil && resp.StatusCode == 404 {
						return fmt.Errorf("repository not found or no access")
					}
					return err
				}
				return nil
			},
		},
		{
			name: "Pull request read access",
			check: func() error {
				_, resp, err := client.PullRequests.List(ctx, owner, repo, &github.PullRequestListOptions{
					ListOptions: github.ListOptions{PerPage: 1},
				})
				if err != nil {
					if resp != nil && resp.StatusCode == 403 {
						return fmt.Errorf("no access to pull requests")
					}
					return err
				}
				return nil
			},
		},
		//		{
		// 	name: "Pull request write access (comment creation)",
		// 	check: func() error {
		// 		// Try to create a draft review to check write permissions
		// 		// We'll delete it right after
		// 		_, _, err := client.PullRequests.CreateReview(ctx, owner, repo, 1,
		// 			&github.PullRequestReviewRequest{
		// 				Body:  github.Ptr("Permission check - please ignore"),
		// 				Event: github.Ptr("COMMENT"),
		// 			})
		// 		if err != nil {
		// 			if strings.Contains(err.Error(), "403") {
		// 				return fmt.Errorf("no permission to create reviews")
		// 			}
		// 			// Don't return error if PR #1 doesn't exist
		// 			if !strings.Contains(err.Error(), "404") {
		// 				return err
		// 			}
		// 		}
		//
		// 		return nil
		// 	},
		// },
	}

	// Run all permission checks
	fmt.Println("\nPermission Check Results:")
	fmt.Println("------------------------")
	allPassed := true
	for _, check := range permissionChecks {
		fmt.Printf("%-30s: ", check.name)
		if err := check.check(); err != nil {
			fmt.Printf("❌ Failed - %v\n", err)
			allPassed = false
		} else {
			fmt.Printf("✅ Passed\n")
		}
	}

	if !allPassed {
		return fmt.Errorf("\nsome permission checks failed - token may not have sufficient access")
	}

	fmt.Println("\n✅ Token has all required permissions for PR review functionality")
	return nil
}

// extractContext gets lines of code around a specific line number.
func extractContext(content string, line int, contextLines int) (*CodeContext, error) {
	if content == "" {
		return nil, fmt.Errorf("empty file content")
	}

	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil, fmt.Errorf("line number out of range")
	}

	startLine := max(1, line-contextLines)
	endLine := min(len(lines), line+contextLines)

	context := &CodeContext{
		StartLine: startLine,
		Lines:     lines[startLine-1 : endLine],
	}

	return context, nil
}

func parseHunks(patch string, filePath string) ([]ChangeHunk, error) {
	var hunks []ChangeHunk
	var currentHunk *ChangeHunk

	lines := strings.Split(patch, "\n")
	position := 0 // Track position in diff for GitHub API

	for _, line := range lines {
		position++

		switch {
		case strings.HasPrefix(line, "@@"):
			// Parse hunk header like @@ -1,5 +2,6 @@
			matches := regexp.MustCompile(`\+(\d+),?(\d+)?`).FindStringSubmatch(line)
			if len(matches) >= 2 {
				start, _ := strconv.Atoi(matches[1])

				// If we were building a hunk, append it
				if currentHunk != nil {
					hunks = append(hunks, *currentHunk)
				}

				currentHunk = &ChangeHunk{
					FilePath:  filePath,
					StartLine: start,
					Position:  position,
				}
			}

		case strings.HasPrefix(line, "+"):
			// This is new or modified code
			if currentHunk != nil {
				currentHunk.Content += line[1:] + "\n"
				currentHunk.EndLine = currentHunk.StartLine +
					strings.Count(currentHunk.Content, "\n")
			}

		case strings.HasPrefix(line, " "):
			// Context line - store in Before/After based on position
			if currentHunk != nil {
				if currentHunk.Content == "" {
					currentHunk.Context.Before += line[1:] + "\n"
				} else {
					currentHunk.Context.After += line[1:] + "\n"
				}
			}
		}
	}

	// Don't forget the last hunk
	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	return hunks, nil
}
