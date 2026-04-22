package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/native"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

type QABenchmarkCase struct {
	ID             string   `json:"id,omitempty"`
	RepoPath       string   `json:"repo_path"`
	Owner          string   `json:"owner"`
	Repo           string   `json:"repo"`
	Question       string   `json:"question"`
	ExpectedFacts  []string `json:"expected_facts"`
	ForbiddenFacts []string `json:"forbidden_facts,omitempty"`
}

type QABenchmarkSuite struct {
	Cases []QABenchmarkCase `json:"cases"`
}

type QABenchmarkEvaluatorConfig struct {
	ForbiddenFactPenalty float64
}

type qaBenchmarkEvaluator struct {
	cfg QABenchmarkEvaluatorConfig
}

type QABenchmarkAgent struct {
	llm       core.LLM
	logger    *logging.Logger
	artifacts optimize.AgentArtifacts

	mu              sync.RWMutex
	lastTrace       *agents.ExecutionTrace
	lastNativeTrace *native.Trace
}

var _ optimize.OptimizableAgent = (*QABenchmarkAgent)(nil)

const qaBenchmarkOptimizationAgentType = "maestro.qa-benchmark"

func qaBenchmarkOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	return []optimize.OptimizationTargetDescriptor{
		{
			ID:          "root.skill_pack",
			Kind:        optimize.OptimizationTargetText,
			Description: "QA guidance overlay appended to Maestro's base QA system prompt.",
			ArtifactKey: optimize.ArtifactSkillPack,
		},
		{
			ID:          "root.tool_policy",
			Kind:        optimize.OptimizationTargetText,
			Description: "QA tool-use policy and evidence-gathering guidance.",
			ArtifactKey: optimize.ArtifactToolPolicy,
		},
		{
			ID:          "root.max_turns",
			Kind:        optimize.OptimizationTargetInt,
			Description: "Maximum repository-tool turns allowed for one answer.",
			IntKey:      "max_turns",
		},
	}
}

func DefaultQABenchmarkEvaluatorConfig() QABenchmarkEvaluatorConfig {
	return QABenchmarkEvaluatorConfig{
		ForbiddenFactPenalty: 0.25,
	}
}

func NewQABenchmarkEvaluator(cfg QABenchmarkEvaluatorConfig) optimize.AgentEvaluator {
	if cfg.ForbiddenFactPenalty <= 0 {
		cfg = DefaultQABenchmarkEvaluatorConfig()
	}
	return &qaBenchmarkEvaluator{cfg: cfg}
}

func defaultQABenchmarkArtifacts() optimize.AgentArtifacts {
	artifacts := defaultQAArtifacts()
	if artifacts.Text == nil {
		artifacts.Text = make(map[optimize.ArtifactKey]string)
	}
	// Benchmark optimization mutates only the SKILL PACK overlay, not the base prompt.
	artifacts.Text[optimize.ArtifactSkillPack] = ""
	return artifacts
}

func mergeQABenchmarkArtifactsWithDefaults(artifacts optimize.AgentArtifacts) optimize.AgentArtifacts {
	merged := defaultQABenchmarkArtifacts()

	for key, value := range artifacts.Text {
		if merged.Text == nil {
			merged.Text = make(map[optimize.ArtifactKey]string)
		}
		merged.Text[key] = strings.TrimSpace(value)
	}
	for key, value := range artifacts.Int {
		if value <= 0 {
			continue
		}
		if merged.Int == nil {
			merged.Int = make(map[string]int)
		}
		merged.Int[key] = value
	}
	for key, value := range artifacts.Bool {
		if merged.Bool == nil {
			merged.Bool = make(map[string]bool)
		}
		merged.Bool[key] = value
	}

	return merged
}

func qaBenchmarkSkillOverlay(artifacts optimize.AgentArtifacts) string {
	return strings.TrimSpace(artifacts.Text[optimize.ArtifactSkillPack])
}

func composeQABenchmarkSystemPrompt(basePrompt, overlay string) string {
	basePrompt = strings.TrimSpace(basePrompt)
	overlay = strings.TrimSpace(overlay)
	if overlay == "" {
		return basePrompt
	}
	return basePrompt + "\n\nSKILL PACK:\n" + overlay
}

func NewQABenchmarkAgent(llm core.LLM, logger *logging.Logger, artifacts optimize.AgentArtifacts) *QABenchmarkAgent {
	return &QABenchmarkAgent{
		llm:       llm,
		logger:    logger,
		artifacts: mergeQABenchmarkArtifactsWithDefaults(artifacts),
	}
}

func LoadQABenchmarkSuite(path string) ([]QABenchmarkCase, error) {
	resolvedPath, err := expandBenchmarkPath(path, "")
	if err != nil {
		return nil, fmt.Errorf("resolve QA benchmark suite path %q: %w", path, err)
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("read QA benchmark suite %q: %w", resolvedPath, err)
	}

	var suite QABenchmarkSuite
	if err := json.Unmarshal(data, &suite); err == nil && len(suite.Cases) > 0 {
		return normalizeQABenchmarkSuitePaths(filepath.Dir(resolvedPath), suite.Cases)
	}

	var cases []QABenchmarkCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("decode QA benchmark suite %q: %w", resolvedPath, err)
	}
	return normalizeQABenchmarkSuitePaths(filepath.Dir(resolvedPath), cases)
}

func normalizeQABenchmarkSuitePaths(baseDir string, cases []QABenchmarkCase) ([]QABenchmarkCase, error) {
	normalized := make([]QABenchmarkCase, 0, len(cases))
	for _, benchmarkCase := range cases {
		if benchmarkCase.RepoPath != "" {
			resolvedPath, err := expandBenchmarkPath(benchmarkCase.RepoPath, baseDir)
			if err != nil {
				return nil, fmt.Errorf("resolve repo_path for benchmark case %q: %w", benchmarkCase.ID, err)
			}
			benchmarkCase.RepoPath = resolvedPath
		}
		normalized = append(normalized, benchmarkCase)
	}
	return normalized, nil
}

func expandBenchmarkPath(path, baseDir string) (string, error) {
	path = strings.TrimSpace(os.ExpandEnv(path))
	if path == "" {
		return "", nil
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	} else if !filepath.IsAbs(path) && strings.TrimSpace(baseDir) != "" {
		path = filepath.Join(baseDir, path)
	}
	return filepath.Clean(path), nil
}

func QABenchmarkExamples(cases []QABenchmarkCase) []optimize.AgentExample {
	examples := make([]optimize.AgentExample, 0, len(cases))
	for i, benchmarkCase := range cases {
		id := strings.TrimSpace(benchmarkCase.ID)
		if id == "" {
			id = fmt.Sprintf("qa-case-%d", i+1)
		}
		examples = append(examples, optimize.AgentExample{
			ID: id,
			Inputs: map[string]interface{}{
				"repo_path": benchmarkCase.RepoPath,
				"owner":     benchmarkCase.Owner,
				"repo":      benchmarkCase.Repo,
				"question":  benchmarkCase.Question,
			},
			Outputs: map[string]interface{}{
				"expected_facts":  append([]string(nil), benchmarkCase.ExpectedFacts...),
				"forbidden_facts": append([]string(nil), benchmarkCase.ForbiddenFacts...),
			},
			Metadata: map[string]interface{}{
				"qa_case": benchmarkCase,
			},
		})
	}
	return examples
}

func (a *QABenchmarkAgent) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	if a == nil {
		return nil, fmt.Errorf("qa benchmark agent is nil")
	}
	if a.llm == nil {
		return nil, fmt.Errorf("qa benchmark agent LLM is nil")
	}

	repoPath := strings.TrimSpace(stringValue(input["repo_path"]))
	if repoPath == "" {
		return nil, fmt.Errorf("repo_path is required")
	}
	owner := strings.TrimSpace(stringValue(input["owner"]))
	repo := strings.TrimSpace(stringValue(input["repo"]))
	task := strings.TrimSpace(stringValue(input["task"]))
	if task == "" {
		question := strings.TrimSpace(stringValue(input["question"]))
		if question == "" {
			return nil, fmt.Errorf("question is required")
		}
		task = buildNativeQATask(question, owner, repo)
	}

	artifacts := a.GetArtifacts()
	cfg := buildNativeQAConfig(artifacts, agents.NewInMemoryStore(), "", nil, nil, "")
	cfg.SkillStore = nil
	cfg.SkillDomain = ""
	cfg.SystemPrompt = composeQABenchmarkSystemPrompt(qaNativeSystemPrompt, qaBenchmarkSkillOverlay(artifacts))

	runtimeAgent, err := native.NewAgent(a.llm, cfg)
	if err != nil {
		return nil, err
	}
	for _, tool := range buildNativeQATools(repoPath, owner, repo, a.logger, nil, nil, "") {
		if err := runtimeAgent.RegisterTool(tool); err != nil {
			return nil, fmt.Errorf("register QA benchmark tool %s: %w", tool.Name(), err)
		}
	}

	result, err := runtimeAgent.Execute(ctx, map[string]interface{}{
		"task": task,
	})

	a.mu.Lock()
	a.lastTrace = runtimeAgent.LastExecutionTrace()
	a.lastNativeTrace = runtimeAgent.LastNativeTrace()
	a.mu.Unlock()

	return result, err
}

func (a *QABenchmarkAgent) GetCapabilities() []core.Tool {
	return nil
}

func (a *QABenchmarkAgent) GetMemory() agents.Memory {
	return nil
}

func (a *QABenchmarkAgent) GetArtifacts() optimize.AgentArtifacts {
	if a == nil {
		return optimize.AgentArtifacts{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.artifacts.Clone()
}

func (a *QABenchmarkAgent) OptimizationAgentType() string {
	return qaBenchmarkOptimizationAgentType
}

func (a *QABenchmarkAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	return qaBenchmarkOptimizationTargets()
}

func (a *QABenchmarkAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	if a == nil {
		return fmt.Errorf("qa benchmark agent is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.artifacts = mergeQABenchmarkArtifactsWithDefaults(artifacts)
	return nil
}

func (a *QABenchmarkAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	if a == nil {
		return fmt.Errorf("qa benchmark agent is nil")
	}
	if update == nil {
		return fmt.Errorf("qa benchmark update function is nil")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	next, err := update(a.artifacts.Clone())
	if err != nil {
		return err
	}
	a.artifacts = mergeQABenchmarkArtifactsWithDefaults(next)
	return nil
}

func (a *QABenchmarkAgent) Clone() (optimize.OptimizableAgent, error) {
	if a == nil {
		return nil, fmt.Errorf("qa benchmark agent is nil")
	}

	cloned := &QABenchmarkAgent{
		llm:       a.llm,
		logger:    a.logger,
		artifacts: a.GetArtifacts(),
	}
	if trace := a.LastExecutionTrace(); trace != nil {
		cloned.lastTrace = trace
	}
	if trace := a.LastNativeTrace(); trace != nil {
		cloned.lastNativeTrace = trace
	}
	return cloned, nil
}

func (a *QABenchmarkAgent) LastExecutionTrace() *agents.ExecutionTrace {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.lastTrace == nil {
		return nil
	}
	return a.lastTrace.Clone()
}

func (a *QABenchmarkAgent) LastNativeTrace() *native.Trace {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.lastNativeTrace == nil {
		return nil
	}
	return a.lastNativeTrace.Clone()
}

func (e *qaBenchmarkEvaluator) Evaluate(ctx context.Context, agent optimize.OptimizableAgent, ex optimize.AgentExample) (*optimize.EvalResult, error) {
	benchmarkCase, err := qaBenchmarkCaseFromExample(ex)
	if err != nil {
		return nil, err
	}

	task := buildNativeQATask(benchmarkCase.Question, benchmarkCase.Owner, benchmarkCase.Repo)
	startedAt := time.Now()
	result, execErr := agent.Execute(ctx, map[string]interface{}{
		"task":      task,
		"question":  benchmarkCase.Question,
		"repo_path": benchmarkCase.RepoPath,
		"owner":     benchmarkCase.Owner,
		"repo":      benchmarkCase.Repo,
	})
	latencyMS := float64(time.Since(startedAt)) / float64(time.Millisecond)

	answer := strings.TrimSpace(stringValue(result["final_answer"]))
	if answer == "" {
		answer = strings.TrimSpace(stringValue(result["answer"]))
	}

	matchedFacts, missingFacts := qaMatchedFacts(answer, benchmarkCase.ExpectedFacts)
	forbiddenHits := qaMatchedFactsOnly(answer, benchmarkCase.ForbiddenFacts)

	score := 1.0
	if len(benchmarkCase.ExpectedFacts) > 0 {
		score = float64(len(matchedFacts)) / float64(len(benchmarkCase.ExpectedFacts))
	}
	score -= float64(len(forbiddenHits)) * e.cfg.ForbiddenFactPenalty
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	sideInfo := &optimize.SideInfo{
		LatencyMS: latencyMS,
		Scores: map[string]float64{
			"fact_recall": score,
		},
		Diagnostics: map[string]interface{}{
			"answer":          answer,
			"matched_facts":   matchedFacts,
			"missing_facts":   missingFacts,
			"forbidden_hits":  forbiddenHits,
			"question":        benchmarkCase.Question,
			"repo_path":       benchmarkCase.RepoPath,
			"expected_facts":  append([]string(nil), benchmarkCase.ExpectedFacts...),
			"forbidden_facts": append([]string(nil), benchmarkCase.ForbiddenFacts...),
		},
	}
	if execErr != nil {
		sideInfo.Diagnostics["evaluation_error"] = execErr.Error()
	}

	if traceProvider, ok := agent.(interface{ LastExecutionTrace() *agents.ExecutionTrace }); ok {
		trace := traceProvider.LastExecutionTrace()
		sideInfo.Trace = trace
		if trace != nil {
			sideInfo.Tokens = traceTokenUsage(trace)
		}
	}
	if nativeTraceProvider, ok := agent.(interface{ LastNativeTrace() *native.Trace }); ok {
		if trace := nativeTraceProvider.LastNativeTrace(); trace != nil {
			sideInfo.Diagnostics["native_prompt_tokens"] = trace.TokenUsage.PromptTokens
			sideInfo.Diagnostics["native_completion_tokens"] = trace.TokenUsage.CompletionTokens
			sideInfo.Diagnostics["native_total_tokens"] = trace.TokenUsage.TotalTokens
			if sideInfo.Tokens == nil {
				sideInfo.Tokens = map[string]int64{}
			}
			sideInfo.Tokens["native_prompt_tokens"] = trace.TokenUsage.PromptTokens
			sideInfo.Tokens["native_completion_tokens"] = trace.TokenUsage.CompletionTokens
			sideInfo.Tokens["native_total_tokens"] = trace.TokenUsage.TotalTokens
		}
	}

	return &optimize.EvalResult{
		Score:    score,
		SideInfo: sideInfo,
	}, nil
}

func qaBenchmarkCaseFromExample(ex optimize.AgentExample) (QABenchmarkCase, error) {
	if raw, ok := ex.Metadata["qa_case"]; ok {
		if benchmarkCase, ok := raw.(QABenchmarkCase); ok {
			return benchmarkCase, nil
		}
		if benchmarkCase, err := decodeQABenchmarkCase(raw); err == nil {
			return benchmarkCase, nil
		}
	}

	benchmarkCase := QABenchmarkCase{
		ID:       ex.ID,
		RepoPath: strings.TrimSpace(stringValue(ex.Inputs["repo_path"])),
		Owner:    strings.TrimSpace(stringValue(ex.Inputs["owner"])),
		Repo:     strings.TrimSpace(stringValue(ex.Inputs["repo"])),
		Question: strings.TrimSpace(stringValue(ex.Inputs["question"])),
	}
	if benchmarkCase.RepoPath == "" || benchmarkCase.Question == "" {
		return QABenchmarkCase{}, fmt.Errorf("qa benchmark example %q missing repo_path or question", ex.ID)
	}
	if expected, ok := ex.Outputs["expected_facts"].([]string); ok {
		benchmarkCase.ExpectedFacts = append([]string(nil), expected...)
	}
	if expected, ok := ex.Outputs["expected_facts"].([]interface{}); ok {
		for _, item := range expected {
			benchmarkCase.ExpectedFacts = append(benchmarkCase.ExpectedFacts, strings.TrimSpace(stringValue(item)))
		}
	}
	if forbidden, ok := ex.Outputs["forbidden_facts"].([]string); ok {
		benchmarkCase.ForbiddenFacts = append([]string(nil), forbidden...)
	}
	if forbidden, ok := ex.Outputs["forbidden_facts"].([]interface{}); ok {
		for _, item := range forbidden {
			benchmarkCase.ForbiddenFacts = append(benchmarkCase.ForbiddenFacts, strings.TrimSpace(stringValue(item)))
		}
	}
	return benchmarkCase, nil
}

func decodeQABenchmarkCase(raw interface{}) (QABenchmarkCase, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return QABenchmarkCase{}, err
	}
	var benchmarkCase QABenchmarkCase
	if err := json.Unmarshal(data, &benchmarkCase); err != nil {
		return QABenchmarkCase{}, err
	}
	return benchmarkCase, nil
}

func qaMatchedFacts(answer string, facts []string) ([]string, []string) {
	if len(facts) == 0 {
		return nil, nil
	}
	lowerAnswer := strings.ToLower(answer)
	matched := make([]string, 0, len(facts))
	missing := make([]string, 0, len(facts))
	for _, fact := range facts {
		fact = strings.TrimSpace(fact)
		if fact == "" {
			continue
		}
		if strings.Contains(lowerAnswer, strings.ToLower(fact)) {
			matched = append(matched, fact)
			continue
		}
		missing = append(missing, fact)
	}
	return matched, missing
}

func qaMatchedFactsOnly(answer string, facts []string) []string {
	matched, _ := qaMatchedFacts(answer, facts)
	return matched
}

func traceTokenUsage(trace *agents.ExecutionTrace) map[string]int64 {
	if trace == nil || len(trace.TokenUsage) == 0 {
		return nil
	}
	usage := make(map[string]int64, len(trace.TokenUsage))
	for key, value := range trace.TokenUsage {
		usage[key] = value
	}
	return usage
}
