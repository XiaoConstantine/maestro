package review

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
	"github.com/XiaoConstantine/maestro/internal/reasoning"
	"github.com/XiaoConstantine/maestro/internal/types"
)

const (
	reviewRLMAgentSignature                = "maestro.review-rlm.v1"
	reviewRLMArtifactsEnvVar               = "MAESTRO_REVIEW_RLM_ARTIFACTS"
	reviewRLMEnabledEnvVar                 = "MAESTRO_REVIEW_RLM_ENABLED"
	reviewRLMArtifactDirName               = "rlm_artifacts"
	reviewRLMOptimizedProgramFileName      = "review_optimized_program.json"
	reviewRLMOptimizedProgramVersion       = 1
	reviewRLMArtifactMetadataVersionKey    = "maestro_artifact_version"
	reviewRLMArtifactMetadataSignatureKey  = "agent_signature"
	reviewRLMArtifactMetadataRouteKey      = "route"
	reviewRLMArtifactRoute                 = "review.rlm"
	reviewRLMDefaultMaxIterations          = 4
	reviewRLMDefaultMaxTokens              = 32000
	reviewRLMDefaultOutputMaxLen           = 2400
	reviewRLMDefaultOutputMaxVarPreviewLen = 240
	reviewRLMDefaultOutputMaxHistoryEntry  = 900
)

type reviewRLMProcessor struct {
	agent         *agentrlm.Agent
	overlay       string
	logger        *logging.Logger
	budgetManager *maestrobudget.BudgetManager
}

var _ reviewChunkProcessor = (*reviewRLMProcessor)(nil)

func newRuntimeReviewChunkProcessor(ctx context.Context, metrics types.MetricsCollector, logger *logging.Logger, instructionOverlay string, budgetManager *maestrobudget.BudgetManager) (reviewChunkProcessor, string, error) {
	artifactPath := strings.TrimSpace(os.Getenv(reviewRLMArtifactsEnvVar))
	enabled := strings.EqualFold(strings.TrimSpace(os.Getenv(reviewRLMEnabledEnvVar)), "true")
	if artifactPath == "" && !enabled {
		return reasoning.NewEnhancedCodeReviewProcessor(metrics, logger, instructionOverlay), "parallel", nil
	}

	program, resolvedPath, err := loadReviewRLMOptimizedProgram(artifactPath)
	if err != nil {
		return nil, "", err
	}
	if program == nil && artifactPath != "" {
		return nil, "", fmt.Errorf("review RLM optimized program not found: %s", resolvedPath)
	}

	llm := core.GetDefaultLLM()
	if llm == nil {
		return nil, "", fmt.Errorf("review RLM processor requested but default LLM is not configured")
	}
	processor := newReviewRLMProcessor(llm, instructionOverlay, logger, budgetManager)
	if program != nil {
		if err := applyReviewRLMOptimizedProgram(processor, program); err != nil {
			return nil, "", err
		}
		if logger != nil {
			logger.Info(ctx, "Loaded review RLM optimized program path=%q", resolvedPath)
		}
	} else if logger != nil {
		logger.Info(ctx, "Review RLM enabled without optimized program; running baseline RLM")
	}
	return processor, "rlm", nil
}

func newReviewRLMProcessor(llm core.LLM, instructionOverlay string, logger *logging.Logger, budgetManager *maestrobudget.BudgetManager) *reviewRLMProcessor {
	module := modrlm.New(
		llm,
		modrlm.NewLLMSubClient(llm),
		modrlm.WithMaxIterations(reviewRLMDefaultMaxIterations),
		modrlm.WithMaxTokens(reviewRLMDefaultMaxTokens),
		modrlm.WithContextPolicyPreset(modrlm.ContextPolicyAdaptive),
		modrlm.WithAdaptiveIteration(),
		modrlm.WithOutputTruncationConfig(modrlm.OutputTruncationConfig{
			Enabled:            true,
			MaxOutputLen:       reviewRLMDefaultOutputMaxLen,
			MaxVarPreviewLen:   reviewRLMDefaultOutputMaxVarPreviewLen,
			MaxHistoryEntryLen: reviewRLMDefaultOutputMaxHistoryEntry,
		}),
	)
	return &reviewRLMProcessor{
		agent:         agentrlm.NewAgent(reviewRLMAgentSignature, module),
		overlay:       strings.TrimSpace(instructionOverlay),
		logger:        logger,
		budgetManager: budgetManager,
	}
}

func (p *reviewRLMProcessor) ProcessMultipleChunks(ctx context.Context, chunks []map[string]interface{}, taskContext map[string]interface{}) ([]*types.EnhancedReviewResult, error) {
	if len(chunks) == 0 {
		return []*types.EnhancedReviewResult{}, nil
	}
	results := make([]*types.EnhancedReviewResult, len(chunks))
	for i, chunk := range chunks {
		result, err := p.processChunk(ctx, chunk, taskContext)
		if err != nil {
			if p.logger != nil {
				p.logger.Warn(ctx, "Review RLM chunk %d failed: %v", i+1, err)
			}
			filePath, _ := chunk["file_path"].(string)
			results[i] = &types.EnhancedReviewResult{
				Issues:         []types.ReviewIssue{},
				OverallQuality: "unknown",
				ReasoningChain: "rlm_chunk_error",
				Confidence:     0,
				FilePath:       filePath,
			}
			continue
		}
		results[i] = result
	}
	return results, nil
}

func (p *reviewRLMProcessor) processChunk(ctx context.Context, chunk map[string]interface{}, taskContext map[string]interface{}) (*types.EnhancedReviewResult, error) {
	if p == nil || p.agent == nil {
		return nil, fmt.Errorf("review RLM processor is nil")
	}
	filePath := strings.TrimSpace(stringFromReviewValue(chunk["file_path"]))
	contextPayload := buildReviewRLMContext(chunk, taskContext)
	query := buildReviewRLMQuery(p.overlay)
	output, err := p.agent.Execute(ctx, map[string]interface{}{
		"context": contextPayload,
		"query":   query,
	})
	if err != nil {
		return nil, err
	}
	p.recordBudgetUsage(ctx)
	rawAnswer := strings.TrimSpace(stringFromReviewValue(output["answer"]))
	issues, err := parseReviewRLMIssues(rawAnswer, filePath)
	if err != nil {
		return nil, fmt.Errorf("parse RLM review output: %w", err)
	}
	return &types.EnhancedReviewResult{
		Issues:         issues,
		OverallQuality: determineOverallQuality(issues),
		ReasoningChain: "RLM review processor",
		Confidence:     calculateConfidence(issues),
		FilePath:       filePath,
	}, nil
}

func (p *reviewRLMProcessor) recordBudgetUsage(ctx context.Context) {
	if p == nil || p.agent == nil {
		return
	}
	trace := p.agent.LastExecutionTrace()
	delta := maestrobudget.UsageDeltaFromExecutionTrace(trace)
	if delta.Empty() {
		return
	}
	manager := p.budgetManager
	if manager == nil {
		manager = maestrobudget.DefaultManager()
	}
	if err := manager.RecordUsageDelta(reviewRLMArtifactRoute, delta); err != nil && p.logger != nil {
		p.logger.Warn(ctx, "Failed to record review RLM budget usage: %v", err)
	}
}

func (p *reviewRLMProcessor) SetBudgetManager(manager *maestrobudget.BudgetManager) {
	if p != nil {
		p.budgetManager = manager
	}
}

func (a *PRReviewAgent) SetBudgetManager(manager *maestrobudget.BudgetManager) {
	if a == nil {
		return
	}
	if budgetAware, ok := a.reviewProcessor.(interface {
		SetBudgetManager(*maestrobudget.BudgetManager)
	}); ok {
		budgetAware.SetBudgetManager(manager)
	}
}

func buildReviewRLMContext(chunk map[string]interface{}, taskContext map[string]interface{}) string {
	var builder strings.Builder
	writeSection := func(name string, value interface{}) {
		text := strings.TrimSpace(stringFromReviewValue(value))
		if text == "" {
			return
		}
		fmt.Fprintf(&builder, "## %s\n%s\n\n", name, text)
	}
	writeSection("File Path", chunk["file_path"])
	writeSection("Changed Lines", chunk["changes"])
	writeSection("File Content", chunk["file_content"])
	writeSection("Leading Context", chunk["leading_context"])
	writeSection("Trailing Context", chunk["trailing_context"])
	writeSection("Guidelines", chunk["guidelines"])
	writeSection("Repository Context", taskContext["repo_context"])
	writeSection("Chunk Context", taskContext["chunk_context"])
	writeSection("ACE Learnings", taskContext["ace_learnings"])
	return strings.TrimSpace(builder.String())
}

func buildReviewRLMQuery(overlay string) string {
	var builder strings.Builder
	builder.WriteString(`Review the changed code in the provided context.

Return strict JSON with this schema and no markdown fences:
{
  "issues": [
    {
      "file_path": "repo-relative file path",
      "line": 123,
      "category": "bug|security|performance|style",
      "severity": "critical|high|medium|low",
      "description": "specific issue grounded in the changed code",
      "suggestion": "specific fix or mitigation",
      "confidence": 0.0
    }
  ]
}

Rules:
- Return {"issues": []} when no concrete, changed-line-grounded issue is present.
- Report correctness, security, crash, data-loss, resource leak, race, or API contract issues before style.
- Style findings must be low severity and must explain concrete ambiguity or maintenance risk.
- Do not report excerpt-boundary syntax artifacts.
- Use line numbers relative to the provided file_content chunk.`)
	overlay = strings.TrimSpace(overlay)
	if overlay != "" {
		builder.WriteString("\n\nREVIEW SKILL PACK:\n")
		builder.WriteString(overlay)
	}
	return builder.String()
}

func parseReviewRLMIssues(rawAnswer, defaultFilePath string) ([]types.ReviewIssue, error) {
	rawAnswer = strings.TrimSpace(rawAnswer)
	if rawAnswer == "" {
		return nil, nil
	}
	payload := extractReviewRLMJSON(rawAnswer)
	var decoded struct {
		Issues []map[string]interface{} `json:"issues"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		var direct []map[string]interface{}
		if directErr := json.Unmarshal([]byte(payload), &direct); directErr != nil {
			return nil, err
		}
		decoded.Issues = direct
	}
	issues := make([]types.ReviewIssue, 0, len(decoded.Issues))
	for _, raw := range decoded.Issues {
		if issue := parseRawIssue(raw, defaultFilePath); issue != nil {
			issues = append(issues, *issue)
		}
	}
	return issues, nil
}

func parseRawIssue(raw map[string]interface{}, defaultFilePath string) *types.ReviewIssue {
	if raw == nil {
		return nil
	}
	description := strings.TrimSpace(stringFromReviewValue(firstReviewValue(raw, "description", "message", "content")))
	suggestion := strings.TrimSpace(stringFromReviewValue(firstReviewValue(raw, "suggestion", "fix", "recommendation")))
	if description == "" && suggestion == "" {
		return nil
	}

	filePath := strings.TrimSpace(stringFromReviewValue(firstReviewValue(raw, "file_path", "file", "path")))
	if filePath == "" {
		filePath = strings.TrimSpace(defaultFilePath)
	}
	lineStart, lineEnd := reviewLineRange(raw)
	if lineEnd == 0 {
		lineEnd = lineStart
	}

	confidence := floatFromReviewValue(firstReviewValue(raw, "confidence", "score"))
	if confidence <= 0 {
		confidence = 0.65
	}
	if confidence > 1 {
		confidence = 1
	}

	return &types.ReviewIssue{
		FilePath: filePath,
		LineRange: types.LineRange{
			Start: lineStart,
			End:   lineEnd,
			File:  filePath,
		},
		Category:    normalizeReviewRLMCategory(stringFromReviewValue(firstReviewValue(raw, "category", "type"))),
		Severity:    normalizeReviewRLMSeverity(stringFromReviewValue(firstReviewValue(raw, "severity", "priority"))),
		Description: description,
		Reasoning:   strings.TrimSpace(stringFromReviewValue(firstReviewValue(raw, "reasoning", "rationale"))),
		Suggestion:  suggestion,
		Confidence:  confidence,
		CodeExample: strings.TrimSpace(stringFromReviewValue(firstReviewValue(raw, "code_example", "example"))),
	}
}

func firstReviewValue(raw map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := raw[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func reviewLineRange(raw map[string]interface{}) (int, int) {
	if value, ok := raw["line_range"]; ok && value != nil {
		switch typed := value.(type) {
		case map[string]interface{}:
			start := intFromReviewMetadata(firstReviewValue(typed, "start", "start_line", "line"))
			end := intFromReviewMetadata(firstReviewValue(typed, "end", "end_line", "line"))
			return start, end
		case []interface{}:
			if len(typed) > 0 {
				start := intFromReviewMetadata(typed[0])
				end := start
				if len(typed) > 1 {
					end = intFromReviewMetadata(typed[1])
				}
				return start, end
			}
		}
	}
	start := intFromReviewMetadata(firstReviewValue(raw, "line", "line_number", "start_line"))
	end := intFromReviewMetadata(firstReviewValue(raw, "end_line", "line_end"))
	return start, end
}

func normalizeReviewRLMCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "security", "performance", "style", "bug":
		return strings.ToLower(strings.TrimSpace(category))
	case "correctness", "crash", "data-loss", "race", "resource-leak":
		return "bug"
	default:
		return "bug"
	}
}

func normalizeReviewRLMSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(severity))
	case "info", "nit", "minor":
		return "low"
	default:
		return "medium"
	}
}

func determineOverallQuality(issues []types.ReviewIssue) string {
	for _, issue := range issues {
		switch strings.ToLower(strings.TrimSpace(issue.Severity)) {
		case "critical", "high":
			return "needs_attention"
		}
	}
	if len(issues) > 0 {
		return "fair"
	}
	return "good"
}

func calculateConfidence(issues []types.ReviewIssue) float64 {
	if len(issues) == 0 {
		return 0.85
	}
	total := 0.0
	for _, issue := range issues {
		total += issue.Confidence
	}
	return math.Round((total/float64(len(issues)))*100) / 100
}

func extractReviewRLMJSON(rawAnswer string) string {
	rawAnswer = strings.TrimSpace(rawAnswer)
	if strings.HasPrefix(rawAnswer, "```") {
		rawAnswer = strings.Trim(rawAnswer, "`")
		rawAnswer = strings.TrimPrefix(strings.TrimSpace(rawAnswer), "json")
		rawAnswer = strings.TrimSpace(rawAnswer)
	}
	if start := strings.Index(rawAnswer, "{"); start >= 0 {
		if end := strings.LastIndex(rawAnswer, "}"); end > start {
			return rawAnswer[start : end+1]
		}
	}
	if start := strings.Index(rawAnswer, "["); start >= 0 {
		if end := strings.LastIndex(rawAnswer, "]"); end > start {
			return rawAnswer[start : end+1]
		}
	}
	return rawAnswer
}

func stringFromReviewValue(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func annotateReviewRLMOptimizedProgram(program *optimize.OptimizedAgentProgram, metadata map[string]interface{}) error {
	if program == nil {
		return fmt.Errorf("review RLM optimized program is nil")
	}
	if program.Metadata == nil {
		program.Metadata = make(map[string]interface{})
	}
	for key, value := range metadata {
		if strings.TrimSpace(key) != "" {
			program.Metadata[key] = value
		}
	}
	program.Metadata[reviewRLMArtifactMetadataVersionKey] = reviewRLMOptimizedProgramVersion
	program.Metadata[reviewRLMArtifactMetadataSignatureKey] = reviewRLMAgentSignature
	program.Metadata[reviewRLMArtifactMetadataRouteKey] = reviewRLMArtifactRoute
	if strings.TrimSpace(program.AgentType) == "" || program.AgentType == "rlm" {
		program.AgentType = reviewRLMAgentSignature
	}
	return validateReviewRLMOptimizedProgram(program)
}

func DefaultReviewRLMOptimizedProgramPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for review RLM artifacts: %w", err)
	}
	return filepath.Join(homeDir, ".maestro", reviewRLMArtifactDirName, reviewRLMOptimizedProgramFileName), nil
}

func ResolveReviewRLMOptimizedProgramPath(path string) (string, error) {
	path = strings.TrimSpace(os.ExpandEnv(path))
	if path == "" {
		path = strings.TrimSpace(os.Getenv(reviewRLMArtifactsEnvVar))
	}
	if path == "" {
		return DefaultReviewRLMOptimizedProgramPath()
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for review RLM artifacts: %w", err)
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Clean(path), nil
}

func AnnotateReviewRLMOptimizedProgram(program *optimize.OptimizedAgentProgram, metadata map[string]interface{}) error {
	return annotateReviewRLMOptimizedProgram(program, metadata)
}

func ReviewRLMAgentSignature() string {
	return reviewRLMAgentSignature
}

func ValidateReviewRLMOptimizedProgram(program *optimize.OptimizedAgentProgram) error {
	return validateReviewRLMOptimizedProgram(program)
}

func validateReviewRLMOptimizedProgram(program *optimize.OptimizedAgentProgram) error {
	if program == nil {
		return fmt.Errorf("review RLM optimized program is nil")
	}
	if err := program.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(program.AgentType) != reviewRLMAgentSignature {
		return fmt.Errorf("review RLM optimized program agent_type %q does not match %q", program.AgentType, reviewRLMAgentSignature)
	}
	if program.Metadata == nil {
		return fmt.Errorf("review RLM optimized program missing metadata")
	}
	if got := strings.TrimSpace(stringFromReviewValue(program.Metadata[reviewRLMArtifactMetadataSignatureKey])); got != reviewRLMAgentSignature {
		return fmt.Errorf("review RLM optimized program agent_signature %q does not match %q", got, reviewRLMAgentSignature)
	}
	if got := intFromReviewMetadata(program.Metadata[reviewRLMArtifactMetadataVersionKey]); got != reviewRLMOptimizedProgramVersion {
		return fmt.Errorf("unsupported review RLM optimized program artifact version %d", got)
	}
	if got := strings.TrimSpace(stringFromReviewValue(program.Metadata[reviewRLMArtifactMetadataRouteKey])); got != reviewRLMArtifactRoute {
		return fmt.Errorf("review RLM optimized program route %q does not match %q", got, reviewRLMArtifactRoute)
	}
	return nil
}

func loadReviewRLMOptimizedProgram(path string) (*optimize.OptimizedAgentProgram, string, error) {
	resolvedPath, err := ResolveReviewRLMOptimizedProgramPath(path)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(resolvedPath) == "" {
		return nil, "", fmt.Errorf("review RLM optimized program path is required")
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
	if err := validateReviewRLMOptimizedProgram(program); err != nil {
		return nil, resolvedPath, err
	}
	return program, resolvedPath, nil
}

func LoadReviewRLMOptimizedProgram(path string) (*optimize.OptimizedAgentProgram, string, error) {
	return loadReviewRLMOptimizedProgram(path)
}

func WriteReviewRLMOptimizedProgram(path string, program *optimize.OptimizedAgentProgram) error {
	resolvedPath, err := ResolveReviewRLMOptimizedProgramPath(path)
	if err != nil {
		return err
	}
	if err := validateReviewRLMOptimizedProgram(program); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return fmt.Errorf("create review RLM artifact directory: %w", err)
	}
	return optimize.WriteOptimizedAgentProgram(resolvedPath, program)
}

func applyReviewRLMOptimizedProgram(agent optimize.OptimizableAgent, program *optimize.OptimizedAgentProgram) error {
	if err := validateReviewRLMOptimizedProgram(program); err != nil {
		return err
	}
	return optimize.ApplyOptimizedAgentProgram(agent, program)
}

func ApplyReviewRLMOptimizedProgram(agent optimize.OptimizableAgent, program *optimize.OptimizedAgentProgram) error {
	return applyReviewRLMOptimizedProgram(agent, program)
}

func intFromReviewMetadata(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case int32:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	case string:
		var parsed int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &parsed); err == nil {
			return parsed
		}
	default:
		return 0
	}
	return 0
}

func floatFromReviewValue(value interface{}) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		n, _ := typed.Float64()
		return n
	case string:
		var parsed float64
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%f", &parsed); err == nil {
			return parsed
		}
	}
	return 0
}

func (p *reviewRLMProcessor) GetArtifacts() optimize.AgentArtifacts {
	if p == nil || p.agent == nil {
		return optimize.AgentArtifacts{}
	}
	return p.agent.GetArtifacts()
}

func (p *reviewRLMProcessor) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	if p == nil || p.agent == nil {
		return fmt.Errorf("review RLM processor is nil")
	}
	return p.agent.SetArtifacts(artifacts)
}

func (p *reviewRLMProcessor) Clone() (optimize.OptimizableAgent, error) {
	if p == nil || p.agent == nil {
		return nil, fmt.Errorf("review RLM processor is nil")
	}
	cloned, err := p.agent.Clone()
	if err != nil {
		return nil, err
	}
	rlmAgent, ok := cloned.(*agentrlm.Agent)
	if !ok {
		return nil, fmt.Errorf("review RLM clone produced %T", cloned)
	}
	return &reviewRLMProcessor{agent: rlmAgent, overlay: p.overlay, logger: p.logger, budgetManager: p.budgetManager}, nil
}

func (p *reviewRLMProcessor) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	if p == nil || p.agent == nil {
		return nil, fmt.Errorf("review RLM processor is nil")
	}
	return p.agent.Execute(ctx, input)
}

func (p *reviewRLMProcessor) GetCapabilities() []core.Tool {
	if p == nil || p.agent == nil {
		return nil
	}
	return p.agent.GetCapabilities()
}

func (p *reviewRLMProcessor) GetMemory() agents.Memory {
	if p == nil || p.agent == nil {
		return nil
	}
	return p.agent.GetMemory()
}

func (p *reviewRLMProcessor) LastExecutionTrace() *agents.ExecutionTrace {
	if p == nil || p.agent == nil {
		return nil
	}
	return p.agent.LastExecutionTrace()
}

func (p *reviewRLMProcessor) OptimizationAgentType() string {
	return reviewRLMAgentSignature
}

func (p *reviewRLMProcessor) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	if p == nil || p.agent == nil {
		return nil
	}
	return p.agent.ListOptimizationTargets()
}

func (p *reviewRLMProcessor) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	if p == nil || p.agent == nil {
		return fmt.Errorf("review RLM processor is nil")
	}
	return p.agent.UpdateArtifacts(update)
}
