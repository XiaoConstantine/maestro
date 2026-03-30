package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

const (
	rlmOverviewManifestMaxChars    = 120000
	rlmOverviewMaxTokens           = 50000
	rlmOverviewMaxIterations       = 5
	rlmOverviewTimeout             = 60 * time.Second
	rlmOverviewVerificationTimeout = 20 * time.Second
	rlmOverviewTraceDirName        = "rlm_traces"
	rlmOverviewRootReadmeMaxChars  = 12000
	rlmOverviewModuleMaxChars      = 8000
	rlmOverviewPackageMaxChars     = 1800
	rlmOverviewVerificationLimit   = 1
	rlmOverviewVerifyEnvVar        = "MAESTRO_RLM_OVERVIEW_VERIFY"
	askStrategyEnvVar              = "MAESTRO_FORCE_ASK_STRATEGY"
)

var (
	overviewPathPattern       = regexp.MustCompile(`(?i)\b(?:pkg|internal|cmd|src)/[A-Za-z0-9_./-]+`)
	overviewFilePattern       = regexp.MustCompile(`(?i)\b[A-Za-z0-9_./-]+\.(?:go|md|txt|json|toml|yaml|yml)\b`)
	overviewIdentifierPattern = regexp.MustCompile(`\b(?:[A-Z][a-z0-9]+[A-Z][A-Za-z0-9]*|[A-Z]{2,}[A-Za-z0-9_]*)\b`)
	overviewTargetPattern     = regexp.MustCompile(`(?i)\b(?:package|file|module|function|method|class|struct|interface)\s+([A-Za-z0-9_./-]+)\b`)
)

type rlmOverviewManifest struct {
	Context string
	Sources []string
}

type rlmOverviewOutput struct {
	Answer            string                    `json:"answer"`
	NeedsVerification []rlmOverviewVerification `json:"needs_verification,omitempty"`
}

type rlmOverviewVerification struct {
	Package string `json:"package"`
	Reason  string `json:"reason"`
}

type capturingSubLLMClient struct {
	base      modrlm.SubLLMClient
	mu        sync.Mutex
	responses []string
}

type manifestBuilder struct {
	builder   strings.Builder
	maxChars  int
	truncated bool
}

func newManifestBuilder(maxChars int) *manifestBuilder {
	if maxChars <= 0 {
		maxChars = rlmOverviewManifestMaxChars
	}
	return &manifestBuilder{maxChars: maxChars}
}

func (b *manifestBuilder) addSection(title, content string) bool {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" || content == "" {
		return true
	}

	section := fmt.Sprintf("## %s\n%s\n\n", title, content)
	if b.builder.Len()+len(section) > b.maxChars {
		remaining := b.maxChars - b.builder.Len()
		if remaining <= 0 {
			b.truncated = true
			return false
		}

		if remaining < len("## \n") {
			b.truncated = true
			return false
		}

		truncated := section[:remaining]
		if !strings.HasSuffix(truncated, "\n") {
			truncated += "\n"
		}
		b.builder.WriteString(truncated)
		b.truncated = true
		return false
	}

	b.builder.WriteString(section)
	return true
}

func (b *manifestBuilder) writeNote(note string) {
	note = strings.TrimSpace(note)
	if note == "" {
		return
	}
	b.addSection("Manifest Note", note)
}

func (b *manifestBuilder) String() string {
	return strings.TrimSpace(b.builder.String())
}

func shouldUseRLMOverviewQuery(question string) bool {
	trimmed := strings.TrimSpace(question)
	if trimmed == "" {
		return false
	}

	lower := strings.ToLower(trimmed)
	if !containsOverviewSignal(lower) {
		return false
	}

	return !hasSpecificOverviewTarget(trimmed, lower)
}

func forcedAskStrategy() string {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(askStrategyEnvVar)))
	switch value {
	case "native", "rlm":
		return value
	default:
		return ""
	}
}

func containsOverviewSignal(lower string) bool {
	signals := []string{
		"architecture",
		"overview",
		"main packages",
		"main package",
		"major components",
		"repo structure",
		"repository structure",
		"project structure",
		"how is this repo organized",
		"how is the repo organized",
		"how is this repository organized",
		"how is the repository organized",
		"how is this project organized",
		"what does this repo do",
		"what does the repo do",
		"what does this repository do",
		"what does the repository do",
	}
	for _, signal := range signals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func hasSpecificOverviewTarget(question, lower string) bool {
	if strings.Contains(question, "`") || overviewPathPattern.MatchString(question) || overviewFilePattern.MatchString(question) {
		return true
	}
	if hasSpecificOverviewIdentifier(question) {
		return true
	}
	if strings.Contains(lower, "how does ") || strings.Contains(lower, "how do ") {
		return true
	}
	matches := overviewTargetPattern.FindStringSubmatch(question)
	if len(matches) < 2 {
		return false
	}

	target := strings.ToLower(strings.TrimSpace(matches[1]))
	if target == "" {
		return false
	}

	generic := map[string]bool{
		"repo":       true,
		"repository": true,
		"project":    true,
		"main":       true,
		"major":      true,
		"overall":    true,
		"packages":   true,
		"structure":  true,
	}
	return !generic[target]
}

func hasSpecificOverviewIdentifier(question string) bool {
	matches := overviewIdentifierPattern.FindAllString(question, -1)
	for _, match := range matches {
		if shortAllCapsAcronym(match) {
			continue
		}
		return true
	}
	return false
}

func shortAllCapsAcronym(value string) bool {
	if len(value) < 2 || len(value) > 3 {
		return false
	}
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func (s *MaestroService) handleRLMOverview(ctx context.Context, question, repoPath string) (*Response, error) {
	manifest, err := buildRLMOverviewManifest(repoPath, rlmOverviewManifestMaxChars)
	if err != nil {
		return nil, fmt.Errorf("build overview manifest: %w", err)
	}

	llm := core.GetDefaultLLM()
	if llm == nil {
		return nil, fmt.Errorf("default LLM is not configured")
	}

	opts := []modrlm.Option{
		modrlm.WithMaxIterations(rlmOverviewMaxIterations),
		modrlm.WithMaxTokens(rlmOverviewMaxTokens),
		modrlm.WithTimeout(rlmOverviewTimeout),
		modrlm.WithHistoryCompression(2, 400),
		modrlm.WithAdaptiveIteration(),
		modrlm.WithSubRLMConfig(modrlm.SubRLMConfig{
			MaxDepth:               2,
			MaxIterationsPerSubRLM: 2,
		}),
		modrlm.WithOutputTruncationConfig(modrlm.OutputTruncationConfig{
			Enabled:            true,
			MaxOutputLen:       1600,
			MaxVarPreviewLen:   160,
			MaxHistoryEntryLen: 800,
		}),
	}
	if traceDir := s.rlmOverviewTraceDir(); traceDir != "" {
		opts = append(opts, modrlm.WithTraceDir(traceDir))
	}

	subClient := newCapturingSubLLMClient(modrlm.NewLLMSubClient(llm))
	module := modrlm.New(llm, subClient, opts...)
	result, trace, err := module.CompleteWithTrace(ctx, manifest.Context, buildRLMOverviewQuery(question))
	if err != nil {
		return nil, fmt.Errorf("rlm overview failed: %w", err)
	}

	rawOutput, parsed, parseErr := parseRLMOverviewOutputWithFallback(result.Response, subClient.LastResponse())
	if parseErr != nil {
		s.logger.Warn(ctx, "Failed to parse RLM overview output as JSON: %v", parseErr)
	}

	answer := strings.TrimSpace(parsed.Answer)
	if answer == "" {
		answer = strings.TrimSpace(rawOutput)
	}
	if answer == "" {
		answer = fmt.Sprintf("I couldn't summarize %s from the repository manifest.", s.config.Owner+"/"+s.config.Repo)
	}

	sources := append([]string(nil), manifest.Sources...)
	verificationTargets := sanitizeVerificationTargets(parsed.NeedsVerification)
	metadata := map[string]any{
		"confidence":         estimateRLMOverviewConfidence(answer, verificationTargets),
		"sources":            sources,
		"strategy":           "rlm_overview",
		"needs_verification": verificationTargets,
	}
	if trace != nil {
		metadata["rlm_usage"] = map[string]any{
			"total_tokens":             trace.Usage.TotalTokens,
			"prompt_tokens":            trace.Usage.PromptTokens,
			"completion_tokens":        trace.Usage.CompletionTokens,
			"root_total_tokens":        trace.RootUsage.TotalTokens,
			"root_prompt_tokens":       trace.RootUsage.PromptTokens,
			"root_completion_tokens":   trace.RootUsage.CompletionTokens,
			"sub_total_tokens":         trace.SubUsage.TotalTokens,
			"sub_prompt_tokens":        trace.SubUsage.PromptTokens,
			"sub_completion_tokens":    trace.SubUsage.CompletionTokens,
			"subrlm_total_tokens":      trace.SubRLMUsage.TotalTokens,
			"subrlm_prompt_tokens":     trace.SubRLMUsage.PromptTokens,
			"subrlm_completion_tokens": trace.SubRLMUsage.CompletionTokens,
			"iterations":               trace.Iterations,
			"sub_llm_calls":            trace.SubLLMCallCount,
			"sub_rlm_calls":            trace.SubRLMCallCount,
			"termination_cause":        trace.TerminationCause,
		}
		s.logger.Debug(ctx, "RLM overview complete: answer_len=%d total_tokens=%d prompt_tokens=%d completion_tokens=%d root_total_tokens=%d root_prompt_tokens=%d root_completion_tokens=%d sub_total_tokens=%d sub_prompt_tokens=%d sub_completion_tokens=%d subrlm_total_tokens=%d subrlm_prompt_tokens=%d subrlm_completion_tokens=%d iterations=%d sub_llm_calls=%d sub_rlm_calls=%d termination=%q verification=%t",
			len(answer),
			trace.Usage.TotalTokens,
			trace.Usage.PromptTokens,
			trace.Usage.CompletionTokens,
			trace.RootUsage.TotalTokens,
			trace.RootUsage.PromptTokens,
			trace.RootUsage.CompletionTokens,
			trace.SubUsage.TotalTokens,
			trace.SubUsage.PromptTokens,
			trace.SubUsage.CompletionTokens,
			trace.SubRLMUsage.TotalTokens,
			trace.SubRLMUsage.PromptTokens,
			trace.SubRLMUsage.CompletionTokens,
			trace.Iterations,
			trace.SubLLMCallCount,
			trace.SubRLMCallCount,
			trace.TerminationCause,
			rlmOverviewVerificationEnabled(),
		)
	}

	if len(verificationTargets) > 0 && rlmOverviewVerificationEnabled() {
		verifiedAnswer, verifiedSources, err := s.verifyRLMOverview(ctx, question, repoPath, verificationTargets)
		if err != nil {
			s.logger.Warn(ctx, "RLM overview verification failed: %v", err)
		} else if strings.TrimSpace(verifiedAnswer) != "" {
			answer = strings.TrimSpace(answer) + "\n\nVerification:\n" + strings.TrimSpace(verifiedAnswer)
			sources = mergeStringLists(sources, verifiedSources)
			metadata["sources"] = sources
		}
	}

	return &Response{
		Type:     RequestAsk,
		Answer:   answer,
		Metadata: metadata,
	}, nil
}

func rlmOverviewVerificationEnabled() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(rlmOverviewVerifyEnvVar)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func (s *MaestroService) verifyRLMOverview(ctx context.Context, originalQuestion, repoPath string, targets []rlmOverviewVerification) (string, []string, error) {
	agent, err := s.pool.GetQAAgent(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("get QA agent: %w", err)
	}

	sections := make([]string, 0, min(rlmOverviewVerificationLimit, len(targets)))
	sources := make([]string, 0)
	for i, target := range targets {
		if i >= rlmOverviewVerificationLimit {
			break
		}

		question := buildRLMVerificationQuestion(originalQuestion, target)
		if question == "" {
			continue
		}

		verifyCtx, cancel := context.WithTimeout(ctx, rlmOverviewVerificationTimeout)
		answer, _, targetSources, err := agent.Ask(verifyCtx, question, repoPath, s.config.Owner, s.config.Repo)
		cancel()
		if err != nil {
			return "", nil, err
		}

		sections = append(sections, fmt.Sprintf("%s: %s", target.Package, strings.TrimSpace(answer)))
		sources = mergeStringLists(sources, targetSources)
	}

	return strings.Join(sections, "\n\n"), sources, nil
}

func (s *MaestroService) rlmOverviewTraceDir() string {
	baseDir := ""
	if s.config != nil && s.config.MemoryPath != "" {
		baseDir = filepath.Dir(s.config.MemoryPath)
	}
	if baseDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		baseDir = filepath.Join(homeDir, ".maestro")
	}

	traceDir := filepath.Join(baseDir, rlmOverviewTraceDirName)
	if err := os.MkdirAll(traceDir, 0755); err != nil {
		return ""
	}
	return traceDir
}

func buildRLMOverviewManifest(repoPath string, maxChars int) (rlmOverviewManifest, error) {
	if strings.TrimSpace(repoPath) == "" {
		return rlmOverviewManifest{}, fmt.Errorf("repository path is required")
	}

	builder := newManifestBuilder(maxChars)
	sources := make([]string, 0, 16)

	topLevel, err := os.ReadDir(repoPath)
	if err != nil {
		return rlmOverviewManifest{}, err
	}
	builder.addSection("Top-Level Entries", formatTopLevelEntries(topLevel))

	for _, relPath := range []string{"go.mod", "package.json", "Cargo.toml"} {
		content, err := readRepositoryFile(repoPath, relPath, rlmOverviewModuleMaxChars)
		if err != nil || content == "" {
			continue
		}
		builder.addSection(relPath, content)
		sources = appendUniqueString(sources, relPath)
	}

	for _, relPath := range []string{"README.md", "README", "README.txt"} {
		content, err := readRepositoryFile(repoPath, relPath, rlmOverviewRootReadmeMaxChars)
		if err != nil || content == "" {
			continue
		}
		builder.addSection("Repository README", content)
		sources = appendUniqueString(sources, relPath)
		break
	}

	for _, root := range []string{"pkg", "internal", "cmd", "src"} {
		rootDir := filepath.Join(repoPath, root)
		info, err := os.Stat(rootDir)
		if err != nil || !info.IsDir() {
			continue
		}

		dirs, err := immediateSubdirs(rootDir)
		if err != nil {
			return rlmOverviewManifest{}, err
		}
		if len(dirs) == 0 {
			continue
		}

		listing := make([]string, 0, len(dirs))
		for _, dir := range dirs {
			listing = append(listing, filepath.ToSlash(filepath.Join(root, dir)))
		}
		builder.addSection(fmt.Sprintf("%s packages", root), strings.Join(listing, "\n"))

		for _, dir := range dirs {
			relDir := filepath.ToSlash(filepath.Join(root, dir))
			docPath, content, err := readPackageMetadata(repoPath, relDir, rlmOverviewPackageMaxChars)
			if err != nil || content == "" {
				continue
			}
			if !builder.addSection(fmt.Sprintf("Package Metadata: %s", relDir), content) {
				break
			}
			sources = appendUniqueString(sources, docPath)
		}
	}

	if builder.truncated {
		builder.writeNote("The manifest was truncated to stay within the overview context budget.")
	}

	return rlmOverviewManifest{
		Context: builder.String(),
		Sources: sources,
	}, nil
}

func buildRLMOverviewQuery(question string) string {
	return fmt.Sprintf(`Answer the user's repository overview question using only the provided manifest.

Question: %s

Return strict JSON with this schema and no markdown fences:
{
  "answer": "direct answer to the overview question",
  "needs_verification": [
    {
      "package": "repo-relative package or directory path",
      "reason": "why a scoped code-level verification would improve the answer"
    }
  ]
}

Rules:
- Use the manifest only. Do not invent files or responsibilities that are not present.
- Keep the answer concise but useful.
- Only populate needs_verification when a specific package needs a follow-up code-level check.
- If no follow-up is needed, return an empty array.
- Verification requests must be scoped to a package or directory, not the whole repository.`, strings.TrimSpace(question))
}

func newCapturingSubLLMClient(base modrlm.SubLLMClient) *capturingSubLLMClient {
	return &capturingSubLLMClient{base: base}
}

func (c *capturingSubLLMClient) Query(ctx context.Context, prompt string) (modrlm.QueryResponse, error) {
	result, err := c.base.Query(ctx, prompt)
	if err == nil {
		c.record(result.Response)
	}
	return result, err
}

func (c *capturingSubLLMClient) QueryBatched(ctx context.Context, prompts []string) ([]modrlm.QueryResponse, error) {
	results, err := c.base.QueryBatched(ctx, prompts)
	if err == nil {
		for _, result := range results {
			c.record(result.Response)
		}
	}
	return results, err
}

func (c *capturingSubLLMClient) LastResponse() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := len(c.responses) - 1; i >= 0; i-- {
		if value := strings.TrimSpace(c.responses[i]); value != "" {
			return value
		}
	}
	return ""
}

func (c *capturingSubLLMClient) record(response string) {
	if strings.TrimSpace(response) == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.responses = append(c.responses, response)
}

func parseRLMOverviewOutput(raw string) (rlmOverviewOutput, error) {
	var result rlmOverviewOutput
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return result, fmt.Errorf("empty RLM overview output")
	}

	jsonText := extractJSONObject(raw)
	if jsonText == "" {
		return result, fmt.Errorf("no JSON object found")
	}
	if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		return result, err
	}
	return result, nil
}

func parseRLMOverviewOutputWithFallback(primary, fallback string) (string, rlmOverviewOutput, error) {
	primary = strings.TrimSpace(primary)
	parsed, err := parseRLMOverviewOutput(primary)
	if err == nil {
		return primary, parsed, nil
	}

	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return primary, rlmOverviewOutput{}, err
	}

	fallbackParsed, fallbackErr := parseRLMOverviewOutput(fallback)
	if fallbackErr == nil {
		return fallback, fallbackParsed, nil
	}

	return primary, rlmOverviewOutput{}, err
}

func extractJSONObject(raw string) string {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "```json"))
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "```"))
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "```"))

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return ""
	}
	return raw[start : end+1]
}

func sanitizeVerificationTargets(targets []rlmOverviewVerification) []rlmOverviewVerification {
	sanitized := make([]rlmOverviewVerification, 0, len(targets))
	seen := make(map[string]bool)

	for _, target := range targets {
		pkg := filepath.ToSlash(strings.TrimSpace(target.Package))
		reason := strings.TrimSpace(target.Reason)
		if pkg == "" || seen[pkg] {
			continue
		}
		if strings.Contains(pkg, " ") {
			continue
		}

		seen[pkg] = true
		sanitized = append(sanitized, rlmOverviewVerification{
			Package: pkg,
			Reason:  reason,
		})
	}

	return sanitized
}

func buildRLMVerificationQuestion(originalQuestion string, target rlmOverviewVerification) string {
	pkg := strings.TrimSpace(target.Package)
	if pkg == "" {
		return ""
	}

	reason := strings.TrimSpace(target.Reason)
	if reason == "" {
		reason = "clarify this package's role in the repository"
	}

	return fmt.Sprintf("Provide a concise structural overview of %s. Focus on %s. Briefly explain the package's role, its main responsibilities, and the most relevant files. Original overview question: %s", pkg, reason, strings.TrimSpace(originalQuestion))
}

func estimateRLMOverviewConfidence(answer string, targets []rlmOverviewVerification) float64 {
	confidence := 0.82
	if strings.TrimSpace(answer) == "" {
		confidence = 0.35
	}
	if len(targets) > 0 {
		confidence -= 0.15
	}
	if confidence < 0.2 {
		return 0.2
	}
	return confidence
}

func formatTopLevelEntries(entries []os.DirEntry) string {
	if len(entries) == 0 {
		return "(repository root is empty)"
	}

	dirs := make([]string, 0, len(entries))
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") && name != ".github" {
			continue
		}
		if entry.IsDir() {
			dirs = append(dirs, name+"/")
			continue
		}
		files = append(files, name)
	}

	sort.Strings(dirs)
	sort.Strings(files)

	parts := make([]string, 0, 2)
	if len(dirs) > 0 {
		parts = append(parts, "Directories:\n"+strings.Join(dirs, "\n"))
	}
	if len(files) > 0 {
		parts = append(parts, "Files:\n"+strings.Join(files, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func immediateSubdirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dirs = append(dirs, entry.Name())
	}
	sort.Strings(dirs)
	return dirs, nil
}

func readPackageMetadata(repoPath, relDir string, maxChars int) (string, string, error) {
	for _, name := range []string{"README.md", "README", "doc.go"} {
		relPath := filepath.ToSlash(filepath.Join(relDir, name))
		content, err := readRepositoryFile(repoPath, relPath, maxChars)
		if err != nil || content == "" {
			continue
		}
		if strings.EqualFold(name, "doc.go") {
			content = extractDocComment(content)
			content = excerptText(content, maxChars)
			if content == "" {
				continue
			}
		}
		return relPath, content, nil
	}
	return "", "", nil
}

func readRepositoryFile(repoPath, relPath string, maxChars int) (string, error) {
	content, err := os.ReadFile(filepath.Join(repoPath, filepath.FromSlash(relPath)))
	if err != nil {
		return "", err
	}
	return excerptText(string(content), maxChars), nil
}

func excerptText(text string, maxChars int) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	if maxChars > 0 && len(text) > maxChars {
		text = text[:maxChars] + "\n...(truncated)"
	}
	return text
}

func extractDocComment(content string) string {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "/*") {
		if end := strings.Index(trimmed, "*/"); end > 2 {
			return strings.TrimSpace(trimmed[2:end])
		}
	}

	lines := strings.Split(content, "\n")
	comments := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "//") {
			comments = append(comments, strings.TrimSpace(strings.TrimPrefix(trimmedLine, "//")))
			continue
		}
		if len(comments) > 0 {
			break
		}
		if trimmedLine == "" {
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(comments, "\n"))
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func mergeStringLists(left, right []string) []string {
	merged := append([]string(nil), left...)
	for _, value := range right {
		merged = appendUniqueString(merged, value)
	}
	return merged
}
