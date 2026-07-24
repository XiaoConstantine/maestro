package review

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/native"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/search"
	"github.com/XiaoConstantine/maestro/internal/util"
	models "github.com/XiaoConstantine/mcp-go/pkg/model"
)

const (
	reviewVerifierMaxTokens        = 2048
	reviewVerificationEnvVar       = "MAESTRO_REVIEW_VERIFY"
	reviewVerificationMaxComments  = 6
	reviewVerificationMinTurns     = 4
	reviewVerificationMaxTurns     = 12
	reviewVerificationMinTimeout   = 30 * time.Second
	reviewVerificationMaxTimeout   = 90 * time.Second
	reviewVerificationPerCandidate = 12 * time.Second
)

const reviewVerifierSystemPrompt = `You are Maestro's review-finding verifier working inside a local repository checkout.

You receive candidate findings from a fast chunk-based review pass. Your job is to keep only findings that are well-supported by repository evidence.
Use the available pull-request file tools before deciding.

Verification rules:
- Prefer changed-line-grounded findings.
- Drop findings that are speculative, contradicted by the code, or too subjective.
- For style or idiom comments, keep them only when they are clearly helpful and non-trivial.
- This verifier is scoped to changed files in the current pull request. If the changed files do not provide enough evidence, prefer drop over a speculative keep.

Return strict JSON only, using this shape:
{"decisions":[{"id":"c1","decision":"keep|drop","reason_code":"supported|line_mismatch|hunk_mismatch|content_check|subjective|duplicate|other","reason":"short reason"}]}

For drop decisions, set reason_code to the best match:
- line_mismatch: the finding points at the wrong line
- hunk_mismatch: the finding is not grounded in the changed hunk
- content_check: repository content contradicts or does not support it
- subjective: preference, style, or insufficiently actionable
- duplicate: duplicate of another stronger finding
- other: anything else

You must emit exactly one decision for every candidate. Missing decisions default to keep, so omit nothing.

Start with a tool call, then call Finish once you have enough evidence.`

type reviewVerifierTool struct {
	name        string
	description string
	schema      models.InputSchema
	execute     func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error)
}

type reviewVerificationCandidate struct {
	ID         string `json:"id"`
	FilePath   string `json:"file_path"`
	LineNumber int    `json:"line_number"`
	EndLine    int    `json:"end_line,omitempty"`
	Severity   string `json:"severity"`
	Category   string `json:"category,omitempty"`
	Content    string `json:"content"`
	Suggestion string `json:"suggestion,omitempty"`
}

type reviewVerificationDecision struct {
	ID         string `json:"id"`
	Decision   string `json:"decision"`
	ReasonCode string `json:"reason_code,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type reviewVerificationOutput struct {
	Decisions []reviewVerificationDecision `json:"decisions"`
}

type ReviewVerificationRejection struct {
	ID         string `json:"id"`
	FilePath   string `json:"file_path,omitempty"`
	LineNumber int    `json:"line_number,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Category   string `json:"category,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Content    string `json:"content,omitempty"`
}

type reviewVerificationReport struct {
	CandidateCount int                           `json:"candidate_count"`
	KeptCount      int                           `json:"kept_count"`
	DroppedCount   int                           `json:"dropped_count"`
	DropReasons    map[string]int                `json:"drop_reasons,omitempty"`
	Rejections     []ReviewVerificationRejection `json:"rejections,omitempty"`
}

func (t *reviewVerifierTool) Name() string { return t.name }

func (t *reviewVerifierTool) Description() string { return t.description }

func (t *reviewVerifierTool) Metadata() *core.ToolMetadata {
	return &core.ToolMetadata{
		Name:        t.name,
		Description: t.description,
		InputSchema: t.schema,
		Version:     "1.0",
	}
}

func (t *reviewVerifierTool) CanHandle(ctx context.Context, intent string) bool {
	intent = strings.ToLower(intent)
	return strings.Contains(intent, "search") || strings.Contains(intent, "read") || strings.Contains(intent, "verify")
}

func (t *reviewVerifierTool) Execute(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
	return t.execute(ctx, params)
}

func (t *reviewVerifierTool) Validate(params map[string]interface{}) error {
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

func (t *reviewVerifierTool) InputSchema() models.InputSchema { return t.schema }

func reviewVerificationEnabled() bool {
	return util.GetEnvBool(reviewVerificationEnvVar, true)
}

func (a *PRReviewAgent) verifyReviewComments(ctx context.Context, comments []PRReviewComment, tasks []PRReviewTask, console ConsoleInterface) ([]PRReviewComment, *reviewVerificationReport, error) {
	if !reviewVerificationEnabled() || len(comments) == 0 {
		return comments, nil, nil
	}
	logger := logging.GetLogger()

	repoPath := strings.TrimSpace(a.clonedRepoPath)
	if repoPath == "" {
		logger.Warn(ctx, "Review verification missing cloned repo path; waiting briefly for clone state")
		repoPath = strings.TrimSpace(a.WaitForClone(ctx, 10*time.Second))
	}
	if repoPath == "" {
		return comments, nil, fmt.Errorf("repository clone not ready for verification")
	}

	llm := core.GetDefaultLLM()
	if llm == nil {
		return comments, nil, fmt.Errorf("default LLM not configured")
	}

	allCandidates := buildReviewVerificationCandidates(comments)
	candidates := selectReviewVerificationCandidates(allCandidates)
	if len(candidates) == 0 {
		return comments, nil, nil
	}

	taskPrompt, err := buildReviewVerificationTask(candidates, tasks)
	if err != nil {
		return comments, nil, err
	}
	maxTurns := reviewVerificationTurnBudget(len(candidates))

	verifier, err := native.NewAgent(llm, native.Config{
		MaxTurns:                      maxTurns,
		MaxTokens:                     reviewVerifierMaxTokens,
		Temperature:                   0.1,
		SystemPrompt:                  reviewVerifierSystemPrompt,
		Memory:                        agents.NewInMemoryStore(),
		MaxConsecutiveNoCallResponses: 3,
	})
	if err != nil {
		return comments, nil, fmt.Errorf("create verifier agent: %w", err)
	}

	for _, tool := range buildReviewVerifierTools(repoPath, logger, tasks) {
		if err := verifier.RegisterTool(tool); err != nil {
			return comments, nil, fmt.Errorf("register verifier tool %s: %w", tool.Name(), err)
		}
	}

	verifyCtx, cancel := context.WithTimeout(ctx, reviewVerificationTimeoutFor(len(candidates)))
	defer cancel()

	var result map[string]interface{}
	err = console.WithSpinner(verifyCtx, "Verifying merged findings against repository", func() error {
		var execErr error
		result, execErr = verifier.Execute(verifyCtx, map[string]interface{}{
			"task": taskPrompt,
		})
		return execErr
	})
	if err != nil {
		return comments, nil, err
	}

	raw := strings.TrimSpace(reviewStringValue(result["final_answer"]))
	if raw == "" {
		if trace := verifier.LastExecutionTrace(); trace != nil {
			raw = strings.TrimSpace(reviewStringValue(trace.Output["final_answer"]))
		}
	}
	if raw == "" {
		if execErr := strings.TrimSpace(reviewStringValue(result["error"])); execErr != "" {
			return comments, nil, fmt.Errorf("%s", execErr)
		}
		return comments, nil, fmt.Errorf("review verifier returned no final answer")
	}

	decisions, err := parseReviewVerificationOutput(raw)
	if err != nil {
		return comments, nil, err
	}

	filtered, report := applyReviewVerificationDecisions(comments, allCandidates, decisions)
	logger.Debug(ctx, "Review verification complete: candidates=%d kept=%d dropped=%d reasons=%v", len(candidates), report.KeptCount, report.DroppedCount, report.DropReasons)
	return filtered, &report, nil
}

func buildReviewVerificationCandidates(comments []PRReviewComment) []reviewVerificationCandidate {
	candidates := make([]reviewVerificationCandidate, 0, len(comments))
	for i, comment := range comments {
		candidates = append(candidates, reviewVerificationCandidate{
			ID:         fmt.Sprintf("c%d", i+1),
			FilePath:   comment.FilePath,
			LineNumber: comment.LineNumber,
			EndLine:    comment.EndLine,
			Severity:   comment.Severity,
			Category:   comment.Category,
			Content:    strings.TrimSpace(comment.Content),
			Suggestion: strings.TrimSpace(comment.Suggestion),
		})
	}
	return candidates
}

func selectReviewVerificationCandidates(candidates []reviewVerificationCandidate) []reviewVerificationCandidate {
	if len(candidates) <= reviewVerificationMaxComments {
		return candidates
	}
	return candidates[len(candidates)-reviewVerificationMaxComments:]
}

func reviewVerificationTurnBudget(candidateCount int) int {
	turns := candidateCount*2 + 2
	if turns < reviewVerificationMinTurns {
		return reviewVerificationMinTurns
	}
	if turns > reviewVerificationMaxTurns {
		return reviewVerificationMaxTurns
	}
	return turns
}

func reviewVerificationTimeoutFor(candidateCount int) time.Duration {
	timeout := time.Duration(candidateCount) * reviewVerificationPerCandidate
	if timeout < reviewVerificationMinTimeout {
		return reviewVerificationMinTimeout
	}
	if timeout > reviewVerificationMaxTimeout {
		return reviewVerificationMaxTimeout
	}
	return timeout
}

func buildReviewVerificationTask(candidates []reviewVerificationCandidate, tasks []PRReviewTask) (string, error) {
	payload, err := json.MarshalIndent(candidates, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal verification candidates: %w", err)
	}

	var builder strings.Builder
	builder.WriteString("Verify candidate review findings for the current pull request.\n\n")
	builder.WriteString("Changed files:\n")
	for _, task := range tasks {
		fmt.Fprintf(&builder, "- %s\n", strings.TrimSpace(task.FilePath))
	}
	builder.WriteString("\nCandidates:\n")
	builder.Write(payload)
	builder.WriteString("\n\nRules:\n")
	builder.WriteString("- Use repository tools before deciding.\n")
	builder.WriteString("- Keep only findings that are concretely supported by repository evidence.\n")
	builder.WriteString("- If a finding is contradicted by the code, too subjective, or not worth commenting on, drop it.\n")
	builder.WriteString("- You must return one decision for every candidate. Missing decisions default to keep.\n")
	builder.WriteString("- When uncertain, prefer an explicit drop decision over a speculative keep.\n")
	builder.WriteString("- Return strict JSON only.\n")
	return builder.String(), nil
}

func buildReviewVerifierTools(repoPath string, logger *logging.Logger, tasks []PRReviewTask) []core.Tool {
	searchTool := search.NewSimpleSearchTool(logger, repoPath)
	changedFiles := reviewChangedFiles(tasks)
	changedFileSet := make(map[string]struct{}, len(changedFiles))
	for _, filePath := range changedFiles {
		changedFileSet[filePath] = struct{}{}
	}

	return []core.Tool{
		&reviewVerifierTool{
			name:        "search_content",
			description: "Search changed pull-request files for text or regex-like patterns.",
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
						Description: "Optional path prefix or file glob fragment to limit the search.",
					},
				},
			},
			execute: func(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
				query := strings.TrimSpace(reviewFirstString(params, "query", "search", "text", "pattern"))
				if query == "" {
					return core.ToolResult{}, fmt.Errorf("query parameter required")
				}
				pathFilter := strings.TrimSpace(reviewFirstString(params, "path"))
				results, err := reviewSearchFiles(ctx, searchTool, changedFiles, query, pathFilter)
				if err != nil {
					return core.ToolResult{}, err
				}
				display := formatReviewSearchMatches(results)
				return reviewNativeToolResult(display, display), nil
			},
		},
		&reviewVerifierTool{
			name:        "read_file",
			description: "Read a changed pull-request file, optionally restricted to a line range.",
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
				filePath := normalizeReviewFilePath(reviewFirstString(params, "file_path", "path", "file"))
				if filePath == "" {
					return core.ToolResult{}, fmt.Errorf("file_path parameter required")
				}
				if _, ok := changedFileSet[filePath]; !ok {
					display := "File not available: read_file is limited to changed pull-request files."
					return reviewNativeToolResult(display, display), nil
				}
				startLine := reviewIntValue(params["start_line"])
				endLine := reviewIntValue(params["end_line"])
				lines, err := searchTool.ReadFile(ctx, filePath, startLine, endLine)
				if err != nil {
					return core.ToolResult{}, err
				}
				display := strings.Join(lines, "\n")
				if display == "" {
					display = "(file is empty)"
				}
				return reviewNativeToolResult(display, display), nil
			},
		},
	}
}

func reviewNativeToolResult(modelText, displayText string) core.ToolResult {
	return core.ToolResult{
		Data: displayText,
		Metadata: map[string]any{
			core.ToolResultModelTextMeta:   strings.TrimSpace(modelText),
			core.ToolResultDisplayTextMeta: strings.TrimSpace(displayText),
			core.ToolResultIsErrorMeta:     false,
		},
	}
}

func formatReviewSearchMatches(results []*search.Result) string {
	if len(results) == 0 {
		return "No matches found."
	}
	lines := make([]string, 0, len(results))
	for _, result := range results {
		lines = append(lines, fmt.Sprintf("%s:%d: %s", result.FilePath, result.LineNumber, strings.TrimSpace(result.Line)))
	}
	return strings.Join(lines, "\n")
}

func reviewSearchFiles(ctx context.Context, searchTool *search.SimpleSearchTool, files []string, query, pathFilter string) ([]*search.Result, error) {
	files = reviewFilterFiles(files, pathFilter)
	matcher, err := reviewCompileSearchPattern(query)
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
			if len(results) >= 30 {
				return results, nil
			}
		}
	}

	return results, nil
}

func reviewChangedFiles(tasks []PRReviewTask) []string {
	changedFiles := make([]string, 0, len(tasks))
	seen := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		filePath := normalizeReviewFilePath(task.FilePath)
		if filePath == "" {
			continue
		}
		if _, ok := seen[filePath]; ok {
			continue
		}
		seen[filePath] = struct{}{}
		changedFiles = append(changedFiles, filePath)
	}
	sort.Strings(changedFiles)
	return changedFiles
}

func normalizeReviewFilePath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "./")
	return path
}

func reviewCompileSearchPattern(query string) (*regexp.Regexp, error) {
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

func reviewFilterFiles(files []string, pathFilter string) []string {
	pathFilter = strings.Trim(strings.TrimSpace(pathFilter), "/")
	if pathFilter == "" {
		return files
	}

	filtered := make([]string, 0, len(files))
	for _, filePath := range files {
		lowerPath := strings.ToLower(filePath)
		if strings.Contains(lowerPath, strings.ToLower(pathFilter)) || strings.Contains(strings.ToLower(filepath.Base(filePath)), strings.ToLower(pathFilter)) {
			filtered = append(filtered, filePath)
		}
	}
	return filtered
}

func reviewFirstString(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := params[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func reviewStringValue(value any) string {
	if asString, ok := value.(string); ok {
		return asString
	}
	return ""
}

func reviewIntValue(value any) int {
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

func parseReviewVerificationOutput(raw string) (reviewVerificationOutput, error) {
	var output reviewVerificationOutput
	jsonBlock, err := extractReviewVerificationJSON(raw)
	if err != nil {
		return output, err
	}

	if err := json.Unmarshal([]byte(jsonBlock), &output); err == nil {
		if len(output.Decisions) > 0 {
			return output, nil
		}
	}

	var decisions []reviewVerificationDecision
	if err := json.Unmarshal([]byte(jsonBlock), &decisions); err == nil {
		if len(decisions) == 0 {
			return output, fmt.Errorf("verification output contained no decisions")
		}
		output.Decisions = decisions
		return output, nil
	}

	return output, fmt.Errorf("failed to parse verification output")
}

func extractReviewVerificationJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty verification output")
	}

	startChar := byte(0)
	start := -1
	for _, candidate := range []byte{'{', '['} {
		idx := strings.IndexByte(raw, candidate)
		if idx >= 0 && (start < 0 || idx < start) {
			start = idx
			startChar = candidate
		}
	}
	if start < 0 {
		return "", fmt.Errorf("no JSON object found in verification output")
	}

	openChar, closeChar := startChar, byte('}')
	if startChar == '[' {
		closeChar = ']'
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(raw); i++ {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch raw[i] {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch raw[i] {
		case '"':
			inString = true
		case openChar:
			depth++
		case closeChar:
			depth--
			if depth == 0 {
				return raw[start : i+1], nil
			}
		}
	}

	return "", fmt.Errorf("no JSON object found in verification output")
}

func applyReviewVerificationDecisions(comments []PRReviewComment, candidates []reviewVerificationCandidate, output reviewVerificationOutput) ([]PRReviewComment, reviewVerificationReport) {
	report := reviewVerificationReport{
		CandidateCount: len(candidates),
		DropReasons:    make(map[string]int),
	}
	if len(comments) == 0 || len(candidates) == 0 {
		report.KeptCount = len(comments)
		return comments, report
	}

	decisionByID := make(map[string]reviewVerificationDecision, len(output.Decisions))
	for _, decision := range output.Decisions {
		decisionByID[strings.TrimSpace(decision.ID)] = decision
	}

	filtered := make([]PRReviewComment, 0, len(comments))
	limit := len(comments)
	if len(candidates) < limit {
		limit = len(candidates)
	}

	for i := 0; i < limit; i++ {
		decision, ok := decisionByID[candidates[i].ID]
		if ok && strings.EqualFold(strings.TrimSpace(decision.Decision), "drop") {
			reasonCode := normalizeReviewVerificationReasonCode(decision.ReasonCode, decision.Reason)
			report.DropReasons[reasonCode]++
			report.Rejections = append(report.Rejections, ReviewVerificationRejection{
				ID:         candidates[i].ID,
				FilePath:   candidates[i].FilePath,
				LineNumber: candidates[i].LineNumber,
				EndLine:    candidates[i].EndLine,
				Severity:   candidates[i].Severity,
				Category:   candidates[i].Category,
				ReasonCode: reasonCode,
				Reason:     strings.TrimSpace(decision.Reason),
				Content:    candidates[i].Content,
			})
			continue
		}
		filtered = append(filtered, comments[i])
	}
	if limit < len(comments) {
		filtered = append(filtered, comments[limit:]...)
	}
	report.KeptCount = len(filtered)
	report.DroppedCount = len(report.Rejections)
	if len(report.DropReasons) == 0 {
		report.DropReasons = nil
	}

	return filtered, report
}

func normalizeReviewVerificationReasonCode(reasonCode, reason string) string {
	reasonCode = strings.ToLower(strings.TrimSpace(reasonCode))
	switch reasonCode {
	case "supported", "line_mismatch", "hunk_mismatch", "content_check", "subjective", "duplicate", "other":
		return reasonCode
	}

	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(reason, "wrong line"), strings.Contains(reason, "line mismatch"), strings.Contains(reason, "different line"):
		return "line_mismatch"
	case strings.Contains(reason, "changed hunk"), strings.Contains(reason, "outside changed"), strings.Contains(reason, "off-hunk"), strings.Contains(reason, "not in the diff"):
		return "hunk_mismatch"
	case strings.Contains(reason, "subjective"), strings.Contains(reason, "style"), strings.Contains(reason, "preference"), strings.Contains(reason, "not actionable"):
		return "subjective"
	case strings.Contains(reason, "duplicate"):
		return "duplicate"
	case strings.Contains(reason, "unsupported"), strings.Contains(reason, "contradict"), strings.Contains(reason, "not supported"), strings.Contains(reason, "code shows"), strings.Contains(reason, "content"):
		return "content_check"
	default:
		return "other"
	}
}
