package orchestration

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/native"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/search"
	maestrosubagent "github.com/XiaoConstantine/maestro/internal/subagent"
	models "github.com/XiaoConstantine/mcp-go/pkg/model"
)

const qaNativeSystemPrompt = `You are Maestro's repository question-answering agent working inside a local code checkout.

Use the available repo search tools to inspect the codebase before answering.
Start with a tool call instead of a plain-text answer.
For overview or architecture questions, inspect README.md or top-level documentation early when present.
Use read_file after search results to verify exact behavior.
Prefer semantic_search for conceptual questions and search_content for concrete identifiers or strings.
If Claude or Gemini delegation tools are available, use them only when the repository tools are not enough.
Call Finish once you have enough evidence.`

type nativeQATool struct {
	name        string
	description string
	schema      models.InputSchema
	execute     func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error)
}

func (t *nativeQATool) Name() string { return t.name }

func (t *nativeQATool) Description() string { return t.description }

func (t *nativeQATool) Metadata() *core.ToolMetadata {
	return &core.ToolMetadata{
		Name:        t.name,
		Description: t.description,
		InputSchema: t.schema,
		Version:     "1.0",
	}
}

func (t *nativeQATool) CanHandle(ctx context.Context, intent string) bool {
	intent = strings.ToLower(intent)
	return strings.Contains(intent, "search") || strings.Contains(intent, "read") || strings.Contains(intent, "find")
}

func (t *nativeQATool) Execute(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
	return t.execute(ctx, params)
}

func (t *nativeQATool) Validate(params map[string]interface{}) error {
	for name, param := range t.schema.Properties {
		if !param.Required {
			continue
		}
		if value, ok := params[name]; !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return fmt.Errorf("missing required parameter: %s", name)
		}
	}
	return nil
}

func (t *nativeQATool) InputSchema() models.InputSchema { return t.schema }

func buildNativeQATask(question, owner, repo string) string {
	repoName := strings.Trim(strings.TrimSpace(owner)+"/"+strings.TrimSpace(repo), "/")
	if repoName == "" {
		repoName = "current repository"
	}

	analysis := analyzeQAQuery(question)
	var builder strings.Builder
	fmt.Fprintf(&builder, `Answer the user's question about %s using the available repository tools.

Question: %s

Rules:
- Start with a tool call.
- Ground the answer in the checked-out repository.
- For overview questions, inspect README.md first if it exists.
- Mention the most relevant files in the final answer when helpful.
- Call Finish once you have enough evidence.`, repoName, strings.TrimSpace(question))

	guidance := nativeQAGuidance(analysis)
	if len(guidance) > 0 {
		builder.WriteString("\n\nSearch guidance:\n")
		for _, line := range guidance {
			fmt.Fprintf(&builder, "- %s\n", line)
		}
	}
	if len(analysis.RequiredTools) > 0 {
		fmt.Fprintf(&builder, "- Prefer this initial tool sequence when it fits: %s.\n", strings.Join(analysis.RequiredTools, " -> "))
	}
	if analysis.MaxIterations > 0 {
		fmt.Fprintf(&builder, "- Aim to finish within roughly %d focused tool turns once the repository evidence is sufficient.\n", analysis.MaxIterations)
	}

	return builder.String()
}

func buildNativeSearchTools(repoPath string, logger *logging.Logger) []core.Tool {
	return buildNativeQATools(repoPath, "", "", logger, nil, nil, "")
}

func buildNativeQATools(repoPath, owner, repo string, logger *logging.Logger, sessionManager *maestrosubagent.SessionManager, sessionStore sessionevent.SessionEventStore, sessionID string) []core.Tool {
	searchTool := search.NewSimpleSearchTool(logger, repoPath)
	sgrepTool := search.NewSgrepTool(logger, repoPath)

	tools := []core.Tool{
		&nativeQATool{
			name:        "search_files",
			description: "Search for files by glob, path fragment, basename, or extension.",
			schema: models.InputSchema{
				Type: "object",
				Properties: map[string]models.ParameterSchema{
					"pattern": {
						Type:        "string",
						Description: "Glob or path fragment to match, such as README.md, *.go, or internal/orchestration.",
						Required:    true,
					},
				},
			},
			execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
				pattern := strings.TrimSpace(firstString(params, "pattern", "query", "search"))
				if pattern == "" {
					return core.ToolResult{}, fmt.Errorf("pattern parameter required")
				}

				matches, err := findRepoFiles(repoPath, pattern, "")
				if err != nil {
					return core.ToolResult{}, err
				}

				display := formatFileMatches(matches)
				return newNativeToolResult(display, display, map[string]any{
					"files": matches,
				}), nil
			},
		},
		&nativeQATool{
			name:        "search_content",
			description: "Search file contents for text or regex-like patterns and return matching files and lines.",
			schema: models.InputSchema{
				Type: "object",
				Properties: map[string]models.ParameterSchema{
					"query": {
						Type:        "string",
						Description: "Text or regex-like pattern to search for.",
						Required:    true,
					},
					"path": {
						Type:        "string",
						Description: "Optional path prefix or glob to limit the search.",
					},
				},
			},
			execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
				query := strings.TrimSpace(firstString(params, "query", "search", "text", "pattern"))
				if query == "" {
					return core.ToolResult{}, fmt.Errorf("query parameter required")
				}

				pathFilter := strings.TrimSpace(firstString(params, "path"))
				results, err := grepRepoFiles(ctx, searchTool, repoPath, query, pathFilter)
				if err != nil {
					return core.ToolResult{}, err
				}

				display := formatContentMatches(results)
				return newNativeToolResult(display, display, map[string]any{
					"files":   extractSearchResultFiles(results),
					"results": searchResultsToTraceDetails(results),
				}), nil
			},
		},
		&nativeQATool{
			name:        "read_file",
			description: "Read a repository file, optionally restricted to a line range.",
			schema: models.InputSchema{
				Type: "object",
				Properties: map[string]models.ParameterSchema{
					"file_path": {
						Type:        "string",
						Description: "Repository-relative file path to read.",
						Required:    true,
					},
					"start_line": {
						Type:        "integer",
						Description: "Optional 1-based start line.",
					},
					"end_line": {
						Type:        "integer",
						Description: "Optional 1-based end line.",
					},
				},
			},
			execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
				filePath := strings.TrimSpace(firstString(params, "file_path", "path", "filepath", "file"))
				if filePath == "" {
					return core.ToolResult{}, fmt.Errorf("file_path parameter required")
				}

				startLine := intValue(params["start_line"])
				endLine := intValue(params["end_line"])
				lines, err := searchTool.ReadFile(ctx, filePath, startLine, endLine)
				if err != nil {
					return core.ToolResult{}, err
				}

				display := strings.Join(lines, "\n")
				if display == "" {
					display = "(file is empty)"
				}

				return newNativeToolResult(display, display, map[string]any{
					"file_path":  filePath,
					"files":      []string{filePath},
					"start_line": startLine,
					"end_line":   endLine,
				}), nil
			},
		},
		&nativeQATool{
			name:        "semantic_search",
			description: "Search conceptually related code using sgrep and fall back to content search when unavailable.",
			schema: models.InputSchema{
				Type: "object",
				Properties: map[string]models.ParameterSchema{
					"query": {
						Type:        "string",
						Description: "Conceptual search query.",
						Required:    true,
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of results to return.",
					},
				},
			},
			execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
				query := strings.TrimSpace(firstString(params, "query", "search"))
				if query == "" {
					return core.ToolResult{}, fmt.Errorf("query parameter required")
				}

				limit := intValue(params["limit"])
				if limit <= 0 {
					limit = 10
				}

				results, err := sgrepTool.Search(ctx, query, limit)
				if err == nil {
					display := formatSemanticMatches(results)
					return newNativeToolResult(display, display, map[string]any{
						"files":   extractSemanticResultFiles(results),
						"results": semanticResultsToTraceDetails(results),
					}), nil
				}

				logger.Debug(ctx, "semantic search unavailable, falling back to content search: %v", err)
				fallback, fallbackErr := grepRepoFiles(ctx, searchTool, repoPath, query, "")
				if fallbackErr != nil {
					return core.ToolResult{}, fallbackErr
				}

				display := formatContentMatches(fallback)
				return newNativeToolResult(display, display, map[string]any{
					"files":    extractSearchResultFiles(fallback),
					"results":  searchResultsToTraceDetails(fallback),
					"fallback": "search_content",
				}), nil
			},
		},
		&nativeQATool{
			name:        "sgrep_index",
			description: "Index the current repository for semantic code search.",
			schema: models.InputSchema{
				Type: "object",
				Properties: map[string]models.ParameterSchema{
					"path": {
						Type:        "string",
						Description: "Optional path to index. Defaults to the repository root.",
					},
				},
			},
			execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
				pathArg := strings.TrimSpace(firstString(params, "path"))
				if pathArg == "" {
					pathArg = "."
				}
				result, err := sgrepTool.Execute(ctx, map[string]interface{}{
					"action": "index",
					"path":   pathArg,
				})
				if err != nil {
					return core.ToolResult{}, err
				}
				display := strings.TrimSpace(fmt.Sprint(result.Data))
				return newNativeToolResult(display, display, map[string]any{
					"path": pathArg,
				}), nil
			},
		},
		&nativeQATool{
			name:        "sgrep_status",
			description: "Check whether semantic search is available and the repository is indexed.",
			schema: models.InputSchema{
				Type:       "object",
				Properties: map[string]models.ParameterSchema{},
			},
			execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
				result, err := sgrepTool.Execute(ctx, map[string]interface{}{
					"action": "status",
				})
				if err != nil {
					return core.ToolResult{}, err
				}
				display := strings.TrimSpace(fmt.Sprint(result.Data))
				return newNativeToolResult(display, display, map[string]any{}), nil
			},
		},
	}

	if sessionManager == nil || sessionStore == nil || strings.TrimSpace(sessionID) == "" {
		return tools
	}

	staticInput := map[string]any{
		"repo_path": repoPath,
	}
	if strings.TrimSpace(owner) != "" {
		staticInput["owner"] = owner
	}
	if strings.TrimSpace(repo) != "" {
		staticInput["repo"] = repo
	}

	if maestrosubagent.ClaudeAvailable() {
		tool, err := maestrosubagent.NewClaudeTool(logger, sessionManager, sessionID, staticInput)
		if err != nil {
			logger.Warn(context.Background(), "Failed to register Claude delegation tool: %v", err)
		} else {
			tools = append(tools, tool)
		}
	}
	if maestrosubagent.GeminiAvailable() {
		tool, err := maestrosubagent.NewGeminiTool(logger, sessionManager, sessionID, staticInput)
		if err != nil {
			logger.Warn(context.Background(), "Failed to register Gemini delegation tool: %v", err)
		} else {
			tools = append(tools, tool)
		}
	}

	return tools
}

func newNativeToolResult(modelText, displayText string, details map[string]any) core.ToolResult {
	return core.ToolResult{
		Data: displayText,
		Metadata: map[string]any{
			core.ToolResultModelTextMeta:   strings.TrimSpace(modelText),
			core.ToolResultDisplayTextMeta: strings.TrimSpace(displayText),
			core.ToolResultIsErrorMeta:     false,
		},
		Annotations: map[string]any{
			core.ToolResultDetailsAnnotation: details,
		},
	}
}

func extractSourcesFromNativeTrace(trace *native.Trace) []string {
	if trace == nil {
		return nil
	}

	seen := make(map[string]bool)
	var sources []string
	add := func(filePath string) {
		filePath = strings.TrimSpace(filePath)
		if filePath == "" || filePath == "." || filePath == ".." || seen[filePath] {
			return
		}
		seen[filePath] = true
		sources = append(sources, filePath)
	}

	for _, step := range trace.Steps {
		addTraceSources(add, step.ToolName, step.ObservationDetails)
		if strings.EqualFold(step.ToolName, "read_file") {
			add(firstString(step.Arguments, "file_path", "path", "filepath", "file"))
		}
	}

	return sources
}

func addTraceSources(add func(string), toolName string, details map[string]any) {
	if details == nil {
		return
	}

	switch strings.ToLower(toolName) {
	case "search_files", "search_content", "semantic_search", "read_file":
		addFilesFromValue(add, details["files"])
		if filePath, ok := details["file_path"].(string); ok {
			add(filePath)
		}
		addFilesFromResults(add, details["results"])
	}
}

func addFilesFromValue(add func(string), value any) {
	switch v := value.(type) {
	case []string:
		for _, item := range v {
			add(item)
		}
	case []any:
		for _, item := range v {
			if asString, ok := item.(string); ok {
				add(asString)
			}
		}
	}
}

func addFilesFromResults(add func(string), value any) {
	results, ok := value.([]map[string]any)
	if ok {
		for _, result := range results {
			if filePath, ok := result["file_path"].(string); ok {
				add(filePath)
			}
		}
		return
	}

	rawResults, ok := value.([]any)
	if !ok {
		return
	}
	for _, raw := range rawResults {
		result, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if filePath, ok := result["file_path"].(string); ok {
			add(filePath)
		}
	}
}

func estimateNativeQAConfidence(trace *native.Trace, sources []string) float64 {
	if trace == nil || !trace.Completed {
		return 0
	}

	confidence := 0.7
	if len(sources) > 0 {
		confidence = 0.9
	}
	for _, step := range trace.Steps {
		if step.IsError {
			confidence -= 0.2
		}
	}
	if confidence < 0 {
		return 0
	}
	if confidence > 1 {
		return 1
	}
	return confidence
}

func firstString(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := params[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	if asString, ok := value.(string); ok {
		return asString
	}
	return ""
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return parsed
		}
	}
	return 0
}

func findRepoFiles(repoPath, pattern, pathFilter string) ([]string, error) {
	files, err := listRepoFiles(repoPath)
	if err != nil {
		return nil, err
	}

	filtered := filterFiles(files, pathFilter)
	matches := make([]string, 0, len(filtered))
	for _, filePath := range filtered {
		if matchesFilePattern(filePath, pattern) {
			matches = append(matches, filePath)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func grepRepoFiles(ctx context.Context, searchTool *search.SimpleSearchTool, repoPath, query, pathFilter string) ([]*search.Result, error) {
	files, err := listRepoFiles(repoPath)
	if err != nil {
		return nil, err
	}

	files = filterFiles(files, pathFilter)
	matcher, err := compileSearchPattern(query)
	if err != nil {
		return nil, err
	}

	results := make([]*search.Result, 0)
	for _, filePath := range files {
		lines, err := searchTool.ReadFile(ctx, filePath, 0, 0)
		if err != nil {
			continue
		}
		for idx, line := range lines {
			if !matcher.MatchString(line) {
				continue
			}
			results = append(results, &search.Result{
				FilePath:   filePath,
				LineNumber: idx + 1,
				Line:       line,
				MatchType:  "pattern",
				Score:      1.0,
			})
			if len(results) >= 25 {
				return results, nil
			}
		}
	}

	return results, nil
}

func compileSearchPattern(query string) (*regexp.Regexp, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query parameter required")
	}

	re, err := regexp.Compile(query)
	if err == nil {
		return re, nil
	}
	return regexp.Compile(regexp.QuoteMeta(query))
}

func listRepoFiles(repoPath string) ([]string, error) {
	files := make([]string, 0, 128)
	err := filepath.WalkDir(repoPath, func(fullPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".maestro":
				return fs.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(repoPath, fullPath)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(relPath))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func filterFiles(files []string, pathFilter string) []string {
	pathFilter = strings.Trim(strings.TrimSpace(pathFilter), "/")
	if pathFilter == "" {
		return files
	}

	pathFilter = filepath.ToSlash(pathFilter)
	filtered := make([]string, 0, len(files))
	for _, filePath := range files {
		switch {
		case strings.ContainsAny(pathFilter, "*?["):
			matched, err := path.Match(pathFilter, filePath)
			if err == nil && matched {
				filtered = append(filtered, filePath)
			}
		case filePath == pathFilter,
			strings.HasPrefix(filePath, pathFilter+"/"),
			strings.Contains(strings.ToLower(filePath), strings.ToLower(pathFilter)):
			filtered = append(filtered, filePath)
		}
	}
	return filtered
}

func matchesFilePattern(filePath, pattern string) bool {
	pattern = strings.Trim(strings.TrimSpace(pattern), "/")
	if pattern == "" {
		return false
	}

	pattern = filepath.ToSlash(pattern)
	if strings.ContainsAny(pattern, "*?[") {
		if matched, err := path.Match(pattern, filePath); err == nil && matched {
			return true
		}
		if !strings.Contains(pattern, "/") {
			matched, err := path.Match(pattern, path.Base(filePath))
			return err == nil && matched
		}
		return false
	}

	lowerPattern := strings.ToLower(pattern)
	lowerPath := strings.ToLower(filePath)
	return strings.Contains(lowerPath, lowerPattern) || strings.Contains(strings.ToLower(path.Base(filePath)), lowerPattern)
}

func formatFileMatches(matches []string) string {
	if len(matches) == 0 {
		return "No matching files found."
	}

	limit := min(len(matches), 20)
	lines := make([]string, 0, limit+1)
	lines = append(lines, matches[:limit]...)
	if len(matches) > limit {
		lines = append(lines, fmt.Sprintf("... and %d more", len(matches)-limit))
	}
	return strings.Join(lines, "\n")
}

func formatContentMatches(results []*search.Result) string {
	if len(results) == 0 {
		return "No matching content found."
	}

	limit := min(len(results), 15)
	lines := make([]string, 0, limit+1)
	for _, result := range results[:limit] {
		lines = append(lines, fmt.Sprintf("%s:%d\n%s", result.FilePath, result.LineNumber, result.Line))
	}
	if len(results) > limit {
		lines = append(lines, fmt.Sprintf("... and %d more results", len(results)-limit))
	}
	return strings.Join(lines, "\n---\n")
}

func formatSemanticMatches(results []search.SgrepSearchResult) string {
	if len(results) == 0 {
		return "No semantic matches found."
	}

	limit := min(len(results), 10)
	lines := make([]string, 0, limit+1)
	for _, result := range results[:limit] {
		content := result.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		lines = append(lines, fmt.Sprintf("%s:%d-%d\n%s", result.FilePath, result.StartLine, result.EndLine, content))
	}
	if len(results) > limit {
		lines = append(lines, fmt.Sprintf("... and %d more results", len(results)-limit))
	}
	return strings.Join(lines, "\n---\n")
}

func extractSearchResultFiles(results []*search.Result) []string {
	seen := make(map[string]bool)
	files := make([]string, 0, len(results))
	for _, result := range results {
		if result == nil || result.FilePath == "" || seen[result.FilePath] {
			continue
		}
		seen[result.FilePath] = true
		files = append(files, result.FilePath)
	}
	sort.Strings(files)
	return files
}

func searchResultsToTraceDetails(results []*search.Result) []map[string]any {
	details := make([]map[string]any, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		details = append(details, map[string]any{
			"file_path":   result.FilePath,
			"line_number": result.LineNumber,
		})
	}
	return details
}

func extractSemanticResultFiles(results []search.SgrepSearchResult) []string {
	seen := make(map[string]bool)
	files := make([]string, 0, len(results))
	for _, result := range results {
		if result.FilePath == "" || seen[result.FilePath] {
			continue
		}
		seen[result.FilePath] = true
		files = append(files, result.FilePath)
	}
	sort.Strings(files)
	return files
}

func semanticResultsToTraceDetails(results []search.SgrepSearchResult) []map[string]any {
	details := make([]map[string]any, 0, len(results))
	for _, result := range results {
		details = append(details, map[string]any{
			"file_path":  result.FilePath,
			"start_line": result.StartLine,
			"end_line":   result.EndLine,
		})
	}
	return details
}
