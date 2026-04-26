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
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/XiaoConstantine/maestro/internal/search"
)

const (
	RLMTargetedAskAgentSignature                  = "maestro.rlm-targeted-ask.v1"
	RLMTargetedAskOptimizedProgramArtifactVersion = 1
	RLMTargetedAskArtifactsEnvVar                 = "MAESTRO_RLM_TARGETED_ASK_ARTIFACTS"
	rlmTargetedAskOptimizedProgramFileName        = "targeted_ask_optimized_program.json"
	rlmTargetedAskArtifactRoute                   = "ask.rlm_targeted"
	rlmTargetedAskMaxContextChars                 = 90000
	rlmTargetedAskMaxFileChars                    = 16000
	rlmTargetedAskMaxSearchTerms                  = 5
	rlmTargetedAskMaxSearchResults                = 24
	rlmTargetedAskMaxSourceFiles                  = 8
	rlmTargetedAskMaxIterations                   = 5
	rlmTargetedAskMaxTokens                       = 42000
	rlmTargetedAskTimeout                         = 60 * time.Second
)

var targetedAskTokenPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_./-]{2,}`)

type rlmTargetedAskContext struct {
	Context string
	Sources []string
}

type rlmTargetedAskOutput struct {
	Answer  string   `json:"answer"`
	Sources []string `json:"sources,omitempty"`
}

func (s *MaestroService) handleRLMTargetedAsk(ctx context.Context, question, repoPath string) (*Response, error) {
	manifest, err := buildRLMTargetedAskContext(ctx, repoPath, question, rlmTargetedAskMaxContextChars)
	if err != nil {
		return nil, fmt.Errorf("build targeted ask context: %w", err)
	}

	llm := core.GetDefaultLLM()
	if llm == nil {
		return nil, fmt.Errorf("default LLM is not configured")
	}

	opts := []modrlm.Option{
		modrlm.WithMaxIterations(rlmTargetedAskMaxIterations),
		modrlm.WithMaxTokens(rlmTargetedAskMaxTokens),
		modrlm.WithTimeout(rlmTargetedAskTimeout),
		modrlm.WithContextPolicyPreset(modrlm.ContextPolicyAdaptive),
		modrlm.WithAdaptiveIteration(),
		modrlm.WithOutputTruncationConfig(modrlm.OutputTruncationConfig{
			Enabled:            true,
			MaxOutputLen:       2200,
			MaxVarPreviewLen:   220,
			MaxHistoryEntryLen: 900,
		}),
	}
	if traceDir := s.rlmOverviewTraceDir(); traceDir != "" {
		opts = append(opts, modrlm.WithTraceDir(traceDir))
	}

	subClient := newCapturingSubLLMClient(modrlm.NewLLMSubClient(llm))
	module := modrlm.New(llm, subClient, opts...)
	artifactPath, artifactApplied := s.loadAndApplyRLMTargetedAskOptimizedProgram(ctx, module)

	result, trace, err := module.CompleteWithTrace(ctx, manifest.Context, buildRLMTargetedAskQuery(question, manifest.Sources))
	if err != nil {
		return nil, fmt.Errorf("rlm targeted ask failed: %w", err)
	}
	s.recordRLMTraceUsage(ctx, rlmTargetedAskArtifactRoute, trace)

	rawOutput, parsed, parseErr := parseRLMTargetedAskOutputWithFallback(result.Response, subClient.LastResponse())
	if parseErr != nil && s != nil && s.logger != nil {
		s.logger.Warn(ctx, "Failed to parse RLM targeted ask output as JSON: %v", parseErr)
	}
	answer := strings.TrimSpace(parsed.Answer)
	if answer == "" {
		answer = strings.TrimSpace(rawOutput)
	}
	if answer == "" {
		answer = "I could not answer from the targeted repository context."
	}

	sources := sanitizeRLMTargetedAskSources(parsed.Sources, manifest.Sources)
	if len(sources) == 0 {
		sources = append([]string(nil), manifest.Sources...)
	}
	metadata := map[string]interface{}{
		"confidence": 0.9,
		"sources":    sources,
		"strategy":   "rlm_targeted",
	}
	if artifactApplied {
		metadata["rlm_artifact_path"] = artifactPath
		metadata["rlm_artifact_applied"] = true
	}
	if trace != nil {
		metadata["rlm_usage"] = map[string]interface{}{
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
	}

	return &Response{
		Type:     RequestAsk,
		Answer:   answer,
		Metadata: metadata,
	}, nil
}

func buildRLMTargetedAskContext(ctx context.Context, repoPath, question string, maxChars int) (rlmTargetedAskContext, error) {
	if strings.TrimSpace(repoPath) == "" {
		return rlmTargetedAskContext{}, fmt.Errorf("repository path is required")
	}
	if maxChars <= 0 {
		maxChars = rlmTargetedAskMaxContextChars
	}

	builder := newManifestBuilder(maxChars)
	sources := make([]string, 0, 12)
	files, err := listRepoFiles(repoPath)
	if err != nil {
		return rlmTargetedAskContext{}, err
	}

	explicitFiles := explicitRLMTargetedAskFiles(repoPath, files, question)
	for _, filePath := range explicitFiles {
		content, err := readRepositoryFile(repoPath, filePath, rlmTargetedAskMaxFileChars)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		if !builder.addSection("File: "+filePath, content) {
			break
		}
		sources = appendUniqueString(sources, filePath)
	}

	searchTool := search.NewSimpleSearchTool(logging.GetLogger(), repoPath)
	searchSnippets, searchSources := targetedAskSearchSnippets(ctx, searchTool, repoPath, question)
	if strings.TrimSpace(searchSnippets) != "" {
		builder.addSection("Search Results", searchSnippets)
		for _, filePath := range searchSources {
			sources = appendUniqueString(sources, filePath)
		}
	}

	if len(sources) == 0 {
		for _, filePath := range fallbackRLMTargetedAskFiles(files) {
			content, err := readRepositoryFile(repoPath, filePath, rlmTargetedAskMaxFileChars/2)
			if err != nil || strings.TrimSpace(content) == "" {
				continue
			}
			if !builder.addSection("Fallback File: "+filePath, content) {
				break
			}
			sources = appendUniqueString(sources, filePath)
		}
	}

	if builder.truncated {
		builder.writeNote("The targeted ask context was truncated to stay within the RLM context budget.")
	}

	return rlmTargetedAskContext{
		Context: builder.String(),
		Sources: sources,
	}, nil
}

func explicitRLMTargetedAskFiles(repoPath string, files []string, question string) []string {
	seen := make(map[string]bool)
	add := func(out []string, filePath string) []string {
		filePath = filepath.ToSlash(strings.Trim(strings.TrimSpace(filePath), "`'\".,;:()[]{}"))
		if filePath == "" || seen[filePath] {
			return out
		}
		fullPath := filepath.Join(repoPath, filepath.FromSlash(filePath))
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			seen[filePath] = true
			return append(out, filePath)
		}
		return out
	}

	matches := make([]string, 0, 8)
	for _, candidate := range overviewFilePattern.FindAllString(question, -1) {
		matches = add(matches, candidate)
	}
	for _, candidate := range overviewPathPattern.FindAllString(question, -1) {
		for _, filePath := range files {
			if strings.HasPrefix(filePath, strings.Trim(candidate, "/")+"/") || filePath == strings.Trim(candidate, "/") {
				matches = add(matches, filePath)
				if len(matches) >= rlmTargetedAskMaxSourceFiles {
					return matches
				}
			}
		}
	}
	for _, token := range targetedAskQuestionTerms(question) {
		if !looksLikeCodeIdentifier(token) {
			continue
		}
		lowerToken := strings.ToLower(token)
		for _, filePath := range files {
			if strings.Contains(strings.ToLower(filePath), lowerToken) {
				matches = add(matches, filePath)
				if len(matches) >= rlmTargetedAskMaxSourceFiles {
					return matches
				}
			}
		}
	}
	return matches
}

func targetedAskSearchSnippets(ctx context.Context, searchTool *search.SimpleSearchTool, repoPath, question string) (string, []string) {
	if searchTool == nil {
		return "", nil
	}
	terms := targetedAskQuestionTerms(question)
	if len(terms) == 0 {
		return "", nil
	}

	seenResults := make(map[string]bool)
	results := make([]*search.Result, 0, rlmTargetedAskMaxSearchResults)
	for _, term := range terms {
		termResults, err := grepRepoFiles(ctx, searchTool, repoPath, regexp.QuoteMeta(term), "")
		if err != nil {
			continue
		}
		for _, result := range termResults {
			if result == nil {
				continue
			}
			key := fmt.Sprintf("%s:%d:%s", result.FilePath, result.LineNumber, result.Line)
			if seenResults[key] {
				continue
			}
			seenResults[key] = true
			results = append(results, result)
			if len(results) >= rlmTargetedAskMaxSearchResults {
				return formatContentMatches(results), extractSearchResultFiles(results)
			}
		}
	}
	if len(results) == 0 {
		return "", nil
	}
	return formatContentMatches(results), extractSearchResultFiles(results)
}

func targetedAskQuestionTerms(question string) []string {
	matches := targetedAskTokenPattern.FindAllString(question, -1)
	seen := make(map[string]bool)
	terms := make([]string, 0, rlmTargetedAskMaxSearchTerms)
	for _, match := range matches {
		term := strings.Trim(match, "`'\".,;:()[]{}")
		lower := strings.ToLower(term)
		if lower == "" || seen[lower] || targetedAskStopWords[lower] {
			continue
		}
		if strings.Contains(term, "/") || strings.Contains(term, ".") || looksLikeCodeIdentifier(term) || len(term) >= 5 {
			seen[lower] = true
			terms = append(terms, term)
		}
		if len(terms) >= rlmTargetedAskMaxSearchTerms {
			break
		}
	}
	return terms
}

var targetedAskStopWords = map[string]bool{
	"about": true, "after": true, "before": true, "between": true, "does": true,
	"from": true, "have": true, "inside": true, "should": true, "that": true,
	"their": true, "there": true, "these": true, "this": true, "what": true,
	"when": true, "where": true, "which": true, "while": true, "with": true,
	"would": true, "your": true,
}

func looksLikeCodeIdentifier(value string) bool {
	if strings.ContainsAny(value, "_./-") {
		return true
	}
	hasLower := false
	hasUpper := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' {
			hasLower = true
		}
		if r >= 'A' && r <= 'Z' {
			hasUpper = true
		}
	}
	return hasLower && hasUpper
}

func fallbackRLMTargetedAskFiles(files []string) []string {
	preferred := []string{"README.md", "README", "go.mod", "package.json", "Cargo.toml"}
	selected := make([]string, 0, len(preferred))
	seen := make(map[string]bool)
	for _, preferredPath := range preferred {
		for _, filePath := range files {
			if strings.EqualFold(filePath, preferredPath) && !seen[filePath] {
				seen[filePath] = true
				selected = append(selected, filePath)
				break
			}
		}
	}
	sort.Strings(selected)
	return selected
}

func buildRLMTargetedAskQuery(question string, sources []string) string {
	return fmt.Sprintf(`Answer the user's targeted repository question using only the provided context.

Question: %s

Allowed sources:
%s

Return strict JSON with this schema and no markdown fences:
{
  "answer": "concise answer grounded in the provided context",
  "sources": ["repo-relative file path from Allowed sources"]
}

Rules:
- If the context is insufficient, say what is missing instead of guessing.
- Cite only files from Allowed sources.
- Prefer concrete behavior, symbols, and file names over broad repository summaries.
- Keep the answer terse but complete.`, strings.TrimSpace(question), strings.Join(sources, "\n"))
}

func parseRLMTargetedAskOutput(raw string) (rlmTargetedAskOutput, error) {
	var result rlmTargetedAskOutput
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return result, fmt.Errorf("empty RLM targeted ask output")
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

func parseRLMTargetedAskOutputWithFallback(primary, fallback string) (string, rlmTargetedAskOutput, error) {
	primary = strings.TrimSpace(primary)
	parsed, err := parseRLMTargetedAskOutput(primary)
	if err == nil {
		return primary, parsed, nil
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return primary, rlmTargetedAskOutput{}, err
	}
	fallbackParsed, fallbackErr := parseRLMTargetedAskOutput(fallback)
	if fallbackErr == nil {
		return fallback, fallbackParsed, nil
	}
	return primary, rlmTargetedAskOutput{}, err
}

func sanitizeRLMTargetedAskSources(candidateSources, allowedSources []string) []string {
	allowed := make(map[string]string, len(allowedSources))
	for _, source := range allowedSources {
		normalized := filepath.ToSlash(strings.TrimSpace(source))
		if normalized != "" {
			allowed[strings.ToLower(normalized)] = normalized
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	sources := make([]string, 0, len(candidateSources))
	seen := make(map[string]bool)
	for _, source := range candidateSources {
		normalized := filepath.ToSlash(strings.Trim(strings.TrimSpace(source), "`'\".,;:()[]{}"))
		if normalized == "" {
			continue
		}
		if allowedSource, ok := allowed[strings.ToLower(normalized)]; ok && !seen[allowedSource] {
			seen[allowedSource] = true
			sources = append(sources, allowedSource)
		}
	}
	return sources
}

func DefaultRLMTargetedAskOptimizedProgramPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for RLM targeted ask artifacts: %w", err)
	}
	return filepath.Join(homeDir, ".maestro", rlmOverviewArtifactDirName, rlmTargetedAskOptimizedProgramFileName), nil
}

func ResolveRLMTargetedAskOptimizedProgramPath(path string) (string, error) {
	path = strings.TrimSpace(os.ExpandEnv(path))
	if path == "" {
		path = strings.TrimSpace(os.Getenv(RLMTargetedAskArtifactsEnvVar))
	}
	if path == "" {
		return DefaultRLMTargetedAskOptimizedProgramPath()
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for RLM targeted ask artifacts: %w", err)
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Clean(path), nil
}

func AnnotateRLMTargetedAskOptimizedProgram(program *optimize.OptimizedAgentProgram, metadata map[string]interface{}) error {
	if program == nil {
		return fmt.Errorf("RLM targeted ask optimized program is nil")
	}
	if program.Metadata == nil {
		program.Metadata = make(map[string]interface{})
	}
	for key, value := range metadata {
		if strings.TrimSpace(key) != "" {
			program.Metadata[key] = value
		}
	}
	program.Metadata[rlmOverviewArtifactMetadataVersionKey] = RLMTargetedAskOptimizedProgramArtifactVersion
	program.Metadata[rlmOverviewArtifactMetadataSignatureKey] = RLMTargetedAskAgentSignature
	program.Metadata[rlmOverviewArtifactMetadataRouteKey] = rlmTargetedAskArtifactRoute
	if strings.TrimSpace(program.AgentType) == "" || program.AgentType == "rlm" {
		program.AgentType = RLMTargetedAskAgentSignature
	}
	return ValidateRLMTargetedAskOptimizedProgram(program)
}

func ValidateRLMTargetedAskOptimizedProgram(program *optimize.OptimizedAgentProgram) error {
	if program == nil {
		return fmt.Errorf("RLM targeted ask optimized program is nil")
	}
	if err := program.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(program.AgentType) != "" && program.AgentType != RLMTargetedAskAgentSignature {
		return fmt.Errorf("RLM targeted ask optimized program agent_type %q does not match %q", program.AgentType, RLMTargetedAskAgentSignature)
	}
	if program.Metadata == nil {
		return fmt.Errorf("RLM targeted ask optimized program missing metadata")
	}
	if got := strings.TrimSpace(stringValue(program.Metadata[rlmOverviewArtifactMetadataSignatureKey])); got != RLMTargetedAskAgentSignature {
		return fmt.Errorf("RLM targeted ask optimized program agent_signature %q does not match %q", got, RLMTargetedAskAgentSignature)
	}
	if got := intMetadataValue(program.Metadata[rlmOverviewArtifactMetadataVersionKey]); got != RLMTargetedAskOptimizedProgramArtifactVersion {
		return fmt.Errorf("unsupported RLM targeted ask optimized program artifact version %d", got)
	}
	if got := strings.TrimSpace(stringValue(program.Metadata[rlmOverviewArtifactMetadataRouteKey])); got != "" && got != rlmTargetedAskArtifactRoute {
		return fmt.Errorf("RLM targeted ask optimized program route %q does not match %q", got, rlmTargetedAskArtifactRoute)
	}
	return nil
}

func LoadRLMTargetedAskOptimizedProgram(path string) (*optimize.OptimizedAgentProgram, string, error) {
	resolvedPath, err := ResolveRLMTargetedAskOptimizedProgramPath(path)
	if err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(resolvedPath); err != nil {
		if os.IsNotExist(err) {
			return nil, resolvedPath, nil
		}
		return nil, resolvedPath, err
	}
	program, err := optimize.ReadOptimizedAgentProgram(resolvedPath)
	if err != nil {
		return nil, resolvedPath, err
	}
	if err := ValidateRLMTargetedAskOptimizedProgram(program); err != nil {
		return nil, resolvedPath, err
	}
	return program, resolvedPath, nil
}

func WriteRLMTargetedAskOptimizedProgram(path string, program *optimize.OptimizedAgentProgram) error {
	resolvedPath, err := ResolveRLMTargetedAskOptimizedProgramPath(path)
	if err != nil {
		return err
	}
	if err := ValidateRLMTargetedAskOptimizedProgram(program); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return fmt.Errorf("create RLM targeted ask artifact directory: %w", err)
	}
	return optimize.WriteOptimizedAgentProgram(resolvedPath, program)
}

func ApplyRLMTargetedAskOptimizedProgram(agent optimize.OptimizableAgent, program *optimize.OptimizedAgentProgram) error {
	if err := ValidateRLMTargetedAskOptimizedProgram(program); err != nil {
		return err
	}
	return optimize.ApplyOptimizedAgentProgram(agent, program)
}

func applyRLMTargetedAskOptimizedProgramToModule(module *modrlm.RLM, program *optimize.OptimizedAgentProgram) error {
	if module == nil {
		return fmt.Errorf("RLM targeted ask module is nil")
	}
	return ApplyRLMTargetedAskOptimizedProgram(&rlmOverviewRuntimeArtifactsAgent{
		agent:     agentrlm.NewAgent(RLMTargetedAskAgentSignature, module),
		agentType: RLMTargetedAskAgentSignature,
	}, program)
}

func (s *MaestroService) loadAndApplyRLMTargetedAskOptimizedProgram(ctx context.Context, module *modrlm.RLM) (string, bool) {
	path := ""
	if s != nil && s.config != nil {
		path = s.config.RLMTargetedAskArtifactsPath
	}
	program, resolvedPath, err := LoadRLMTargetedAskOptimizedProgram(path)
	if err != nil {
		if s != nil && s.logger != nil {
			s.logger.Warn(ctx, "Skipping RLM targeted ask optimized program path=%q: %v", resolvedPath, err)
		}
		return resolvedPath, false
	}
	if program == nil {
		return resolvedPath, false
	}
	if err := applyRLMTargetedAskOptimizedProgramToModule(module, program); err != nil {
		if s != nil && s.logger != nil {
			s.logger.Warn(ctx, "Failed to apply RLM targeted ask optimized program path=%q: %v", resolvedPath, err)
		}
		return resolvedPath, false
	}
	return resolvedPath, true
}
