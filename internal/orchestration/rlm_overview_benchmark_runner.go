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
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

const (
	RLMOverviewBenchmarkBaselineVersion      = 1
	RLMOverviewBenchmarkAgentSignature       = "maestro.rlm-overview-benchmark.v1"
	RLMOverviewBenchmarkDefaultPassThreshold = 0.70
)

type RLMOverviewAgentResult struct {
	Answer            string
	RawAnswer         string
	Sources           []string
	NeedsVerification []rlmOverviewVerification
	Trace             *agents.ExecutionTrace
	Error             string
}

type RLMOverviewBenchmarkAgentConfig struct {
	MaxManifestChars int
	MaxIterations    int
	MaxTokens        int
	Timeout          time.Duration
	TraceDir         string
}

type RLMOverviewBenchmarkAgent struct {
	agent *agentrlm.Agent
	cfg   RLMOverviewBenchmarkAgentConfig
}

type rlmOverviewBenchmarkEvaluator struct {
	cfg RLMOverviewEvaluatorConfig
}

type RLMOverviewBenchmarkRunConfig struct {
	Workers                      int
	CaseTimeout                  time.Duration
	MaxAttempts                  int
	PassThreshold                float64
	ProtectedRegressionTolerance float64
	Baseline                     *RLMOverviewBenchmarkBaseline
	EvaluatorConfig              RLMOverviewEvaluatorConfig
}

type RLMOverviewBenchmarkRunReport struct {
	Version           int                              `json:"version"`
	AgentSignature    string                           `json:"agent_signature"`
	StartedAt         time.Time                        `json:"started_at"`
	CompletedAt       time.Time                        `json:"completed_at"`
	AverageScore      float64                          `json:"average_score"`
	PassedExamples    int                              `json:"passed_examples"`
	FailedExamples    int                              `json:"failed_examples"`
	CompletedExamples int                              `json:"completed_examples"`
	EvaluationErrors  int                              `json:"evaluation_errors"`
	TokenUsage        map[string]int64                 `json:"token_usage,omitempty"`
	Results           []RLMOverviewBenchmarkCaseReport `json:"results"`
	ProtectedGate     *RLMOverviewProtectedGateReport  `json:"protected_gate,omitempty"`
}

type RLMOverviewBenchmarkCaseReport struct {
	CaseID         string                 `json:"case_id"`
	Protected      bool                   `json:"protected,omitempty"`
	Score          float64                `json:"score"`
	FactRecall     float64                `json:"fact_recall"`
	SourceCoverage float64                `json:"source_coverage"`
	Terseness      float64                `json:"terseness"`
	ForbiddenHits  []string               `json:"forbidden_hits,omitempty"`
	MissingFacts   []string               `json:"missing_facts,omitempty"`
	MissingSources []string               `json:"missing_sources,omitempty"`
	Tokens         map[string]int64       `json:"tokens,omitempty"`
	Attempts       int                    `json:"attempts,omitempty"`
	Error          string                 `json:"error,omitempty"`
	Diagnostics    map[string]interface{} `json:"diagnostics,omitempty"`
}

type RLMOverviewBenchmarkBaseline struct {
	Version        int                                              `json:"version"`
	AgentSignature string                                           `json:"agent_signature"`
	Scores         map[string]RLMOverviewBenchmarkBaselineCaseScore `json:"scores"`
}

type RLMOverviewBenchmarkBaselineCaseScore struct {
	Score          float64  `json:"score"`
	FactRecall     float64  `json:"fact_recall"`
	SourceCoverage float64  `json:"source_coverage"`
	Terseness      float64  `json:"terseness"`
	ForbiddenHits  []string `json:"forbidden_hits,omitempty"`
}

type RLMOverviewProtectedGateReport struct {
	BaselineVersion  int                              `json:"baseline_version"`
	AgentSignature   string                           `json:"agent_signature"`
	Tolerance        float64                          `json:"tolerance"`
	Passed           bool                             `json:"passed"`
	Regressions      []RLMOverviewProtectedRegression `json:"regressions,omitempty"`
	MissingBaseline  []string                         `json:"missing_baseline,omitempty"`
	ProtectedCaseIDs []string                         `json:"protected_case_ids,omitempty"`
}

type RLMOverviewProtectedRegression struct {
	CaseID        string                                `json:"case_id"`
	Baseline      RLMOverviewBenchmarkBaselineCaseScore `json:"baseline"`
	Current       RLMOverviewBenchmarkBaselineCaseScore `json:"current"`
	ScoreDelta    float64                               `json:"score_delta"`
	RegressedDims []string                              `json:"regressed_dims,omitempty"`
}

var _ optimize.OptimizableAgent = (*RLMOverviewBenchmarkAgent)(nil)

func DefaultRLMOverviewBenchmarkAgentConfig() RLMOverviewBenchmarkAgentConfig {
	return RLMOverviewBenchmarkAgentConfig{
		MaxManifestChars: rlmOverviewManifestMaxChars,
		MaxIterations:    rlmOverviewMaxIterations,
		MaxTokens:        rlmOverviewMaxTokens,
		Timeout:          rlmOverviewTimeout,
	}
}

func NewRLMOverviewBenchmarkAgent(llm core.LLM, cfg RLMOverviewBenchmarkAgentConfig) (*RLMOverviewBenchmarkAgent, error) {
	if llm == nil {
		return nil, fmt.Errorf("RLM overview benchmark LLM is nil")
	}
	cfg = normalizeRLMOverviewBenchmarkAgentConfig(cfg)
	module := modrlm.New(llm, modrlm.NewLLMSubClient(llm), rlmOverviewBenchmarkModuleOptions(cfg)...)
	return &RLMOverviewBenchmarkAgent{
		agent: agentrlm.NewAgent(RLMOverviewBenchmarkAgentSignature, module),
		cfg:   cfg,
	}, nil
}

func normalizeRLMOverviewBenchmarkAgentConfig(cfg RLMOverviewBenchmarkAgentConfig) RLMOverviewBenchmarkAgentConfig {
	defaults := DefaultRLMOverviewBenchmarkAgentConfig()
	if cfg.MaxManifestChars <= 0 {
		cfg.MaxManifestChars = defaults.MaxManifestChars
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = defaults.MaxIterations
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaults.MaxTokens
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaults.Timeout
	}
	cfg.TraceDir = strings.TrimSpace(cfg.TraceDir)
	return cfg
}

func rlmOverviewBenchmarkModuleOptions(cfg RLMOverviewBenchmarkAgentConfig) []modrlm.Option {
	opts := []modrlm.Option{
		modrlm.WithMaxIterations(cfg.MaxIterations),
		modrlm.WithMaxTokens(cfg.MaxTokens),
		modrlm.WithTimeout(cfg.Timeout),
		modrlm.WithContextPolicyPreset(modrlm.ContextPolicyAdaptive),
		modrlm.WithAdaptiveCheckpointThreshold(rlmOverviewAdaptiveThreshold),
		modrlm.WithAdaptiveIteration(),
		modrlm.WithSubRLMConfig(modrlm.SubRLMConfig{
			MaxDepth:               2,
			MaxIterationsPerSubRLM: 2,
			MaxDirectSubRLMCalls:   rlmOverviewMaxDirectSubRLM,
			MaxTotalSubRLMCalls:    rlmOverviewMaxTotalSubRLM,
		}),
		modrlm.WithOutputTruncationConfig(modrlm.OutputTruncationConfig{
			Enabled:            true,
			MaxOutputLen:       1600,
			MaxVarPreviewLen:   160,
			MaxHistoryEntryLen: 800,
		}),
	}
	if cfg.TraceDir != "" {
		opts = append(opts, modrlm.WithTraceDir(cfg.TraceDir))
	}
	return opts
}

func (a *RLMOverviewBenchmarkAgent) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	if a == nil || a.agent == nil {
		return nil, fmt.Errorf("RLM overview benchmark agent is nil")
	}
	repoPath := strings.TrimSpace(stringValue(input["repo_path"]))
	if repoPath == "" {
		return nil, fmt.Errorf("repo_path is required")
	}
	question := strings.TrimSpace(stringValue(input["question"]))
	if question == "" {
		return nil, fmt.Errorf("question is required")
	}

	manifest, err := buildRLMOverviewManifest(repoPath, a.cfg.MaxManifestChars)
	if err != nil {
		return nil, fmt.Errorf("build overview manifest: %w", err)
	}

	result, err := a.agent.Execute(ctx, map[string]interface{}{
		"context": manifest.Context,
		"query":   buildRLMOverviewQuery(question),
	})
	rawAnswer := strings.TrimSpace(stringValue(result["answer"]))
	rawOutput, parsed, parseErr := parseRLMOverviewOutputWithFallback(rawAnswer, "")
	answer := strings.TrimSpace(parsed.Answer)
	if answer == "" {
		answer = strings.TrimSpace(rawOutput)
	}

	output := map[string]interface{}{
		"answer":             answer,
		"raw_answer":         rawAnswer,
		"sources":            append([]string(nil), manifest.Sources...),
		"needs_verification": sanitizeVerificationTargets(parsed.NeedsVerification),
	}
	if parseErr != nil && rawAnswer != "" {
		output["parse_error"] = parseErr.Error()
	}
	return output, err
}

func (a *RLMOverviewBenchmarkAgent) GetCapabilities() []core.Tool {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.GetCapabilities()
}

func (a *RLMOverviewBenchmarkAgent) GetMemory() agents.Memory {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.GetMemory()
}

func (a *RLMOverviewBenchmarkAgent) GetArtifacts() optimize.AgentArtifacts {
	if a == nil || a.agent == nil {
		return optimize.AgentArtifacts{}
	}
	return a.agent.GetArtifacts()
}

func (a *RLMOverviewBenchmarkAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	if a == nil || a.agent == nil {
		return fmt.Errorf("RLM overview benchmark agent is nil")
	}
	return a.agent.SetArtifacts(artifacts)
}

func (a *RLMOverviewBenchmarkAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	if a == nil || a.agent == nil {
		return fmt.Errorf("RLM overview benchmark agent is nil")
	}
	return a.agent.UpdateArtifacts(update)
}

func (a *RLMOverviewBenchmarkAgent) Clone() (optimize.OptimizableAgent, error) {
	if a == nil || a.agent == nil {
		return nil, fmt.Errorf("RLM overview benchmark agent is nil")
	}
	cloned, err := a.agent.Clone()
	if err != nil {
		return nil, err
	}
	rlmAgent, ok := cloned.(*agentrlm.Agent)
	if !ok {
		return nil, fmt.Errorf("RLM overview benchmark clone produced %T", cloned)
	}
	return &RLMOverviewBenchmarkAgent{
		agent: rlmAgent,
		cfg:   a.cfg,
	}, nil
}

func (a *RLMOverviewBenchmarkAgent) LastExecutionTrace() *agents.ExecutionTrace {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.LastExecutionTrace()
}

func (a *RLMOverviewBenchmarkAgent) OptimizationAgentType() string {
	return RLMOverviewBenchmarkAgentSignature
}

func (a *RLMOverviewBenchmarkAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.ListOptimizationTargets()
}

func NewRLMOverviewBenchmarkEvaluator(cfg RLMOverviewEvaluatorConfig) optimize.AgentEvaluator {
	return &rlmOverviewBenchmarkEvaluator{cfg: normalizeRLMOverviewEvaluatorConfig(cfg)}
}

func (e *rlmOverviewBenchmarkEvaluator) Evaluate(ctx context.Context, agent optimize.OptimizableAgent, ex optimize.AgentExample) (*optimize.EvalResult, error) {
	benchmarkCase, err := rlmOverviewCaseFromExample(ex)
	if err != nil {
		return nil, err
	}

	startedAt := time.Now()
	result, execErr := agent.Execute(ctx, map[string]interface{}{
		"case_id":   benchmarkCase.ID,
		"repo_path": benchmarkCase.RepoPath,
		"owner":     benchmarkCase.Owner,
		"repo":      benchmarkCase.Repo,
		"question":  benchmarkCase.Question,
	})
	latencyMS := float64(time.Since(startedAt)) / float64(time.Millisecond)

	agentResult := rlmOverviewAgentResultFromOutput(result)
	if execErr != nil {
		agentResult.Error = execErr.Error()
	}
	if traceProvider, ok := agent.(interface{ LastExecutionTrace() *agents.ExecutionTrace }); ok {
		agentResult.Trace = traceProvider.LastExecutionTrace()
	}

	evaluation := EvaluateRLMOverviewAgentResult(benchmarkCase, agentResult, e.cfg)
	if execErr != nil {
		evaluation.Score = 0
		if evaluation.Diagnostics == nil {
			evaluation.Diagnostics = make(map[string]interface{})
		}
		evaluation.Diagnostics["evaluation_error"] = execErr.Error()
	}

	sideInfo := &optimize.SideInfo{
		LatencyMS: latencyMS,
		Trace:     agentResult.Trace,
		Tokens:    traceTokenUsage(agentResult.Trace),
		Scores: map[string]float64{
			"fact_recall":     evaluation.FactRecall,
			"source_coverage": evaluation.SourceCoverage,
			"terseness":       evaluation.Terseness,
		},
		Diagnostics: rlmOverviewEvaluationDiagnostics(evaluation),
	}
	return &optimize.EvalResult{
		Score:    evaluation.Score,
		SideInfo: sideInfo,
	}, nil
}

func EvaluateRLMOverviewAgentResult(benchmarkCase RLMOverviewBenchmarkCase, result RLMOverviewAgentResult, cfg RLMOverviewEvaluatorConfig) RLMOverviewEvaluation {
	evaluation := EvaluateRLMOverviewAnswer(benchmarkCase, result.Answer, result.Sources, cfg)
	if evaluation.Diagnostics == nil {
		evaluation.Diagnostics = make(map[string]interface{})
	}
	evaluation.Diagnostics["raw_answer"] = result.RawAnswer
	evaluation.Diagnostics["sources"] = append([]string(nil), result.Sources...)
	if len(result.NeedsVerification) > 0 {
		evaluation.Diagnostics["needs_verification"] = append([]rlmOverviewVerification(nil), result.NeedsVerification...)
	}
	if result.Trace != nil {
		evaluation.Diagnostics["trace_token_usage"] = traceTokenUsage(result.Trace)
	}
	if result.Error != "" {
		evaluation.Diagnostics["evaluation_error"] = result.Error
	}
	return evaluation
}

func RunRLMOverviewBenchmark(ctx context.Context, agent optimize.OptimizableAgent, cases []RLMOverviewBenchmarkCase, cfg RLMOverviewBenchmarkRunConfig) (*RLMOverviewBenchmarkRunReport, error) {
	if agent == nil {
		return nil, fmt.Errorf("RLM overview benchmark agent is nil")
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("at least one RLM overview benchmark case is required")
	}
	cfg = normalizeRLMOverviewBenchmarkRunConfig(cfg)
	if cfg.Baseline != nil {
		if err := cfg.Baseline.Validate(RLMOverviewBenchmarkAgentSignature); err != nil {
			return nil, err
		}
	}

	examples := RLMOverviewBenchmarkExamples(cases)
	report := &RLMOverviewBenchmarkRunReport{
		Version:        RLMOverviewBenchmarkBaselineVersion,
		AgentSignature: RLMOverviewBenchmarkAgentSignature,
		StartedAt:      time.Now().UTC(),
		Results:        make([]RLMOverviewBenchmarkCaseReport, len(examples)),
		TokenUsage:     make(map[string]int64),
	}
	evaluator := NewRLMOverviewBenchmarkEvaluator(cfg.EvaluatorConfig)

	type job struct {
		index   int
		example optimize.AgentExample
	}
	jobs := make(chan job)
	var wg sync.WaitGroup

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				report.Results[item.index] = runRLMOverviewBenchmarkExample(ctx, agent, evaluator, item.example, cases[item.index], cfg)
			}
		}()
	}

	for i, example := range examples {
		jobs <- job{index: i, example: example}
	}
	close(jobs)
	wg.Wait()

	for _, result := range report.Results {
		report.CompletedExamples++
		report.AverageScore += result.Score
		if result.Score >= cfg.PassThreshold {
			report.PassedExamples++
		} else {
			report.FailedExamples++
		}
		if result.Error != "" {
			report.EvaluationErrors++
		}
		mergeTokenUsage(report.TokenUsage, result.Tokens)
	}
	if report.CompletedExamples > 0 {
		report.AverageScore /= float64(report.CompletedExamples)
	}
	if len(report.TokenUsage) == 0 {
		report.TokenUsage = nil
	}
	if cfg.Baseline != nil {
		report.ProtectedGate = EvaluateRLMOverviewProtectedGate(report, cfg.Baseline, cfg.ProtectedRegressionTolerance)
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func normalizeRLMOverviewBenchmarkRunConfig(cfg RLMOverviewBenchmarkRunConfig) RLMOverviewBenchmarkRunConfig {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	if cfg.PassThreshold <= 0 || cfg.PassThreshold > 1 {
		cfg.PassThreshold = RLMOverviewBenchmarkDefaultPassThreshold
	}
	if cfg.ProtectedRegressionTolerance < 0 {
		cfg.ProtectedRegressionTolerance = 0
	}
	return cfg
}

func runRLMOverviewBenchmarkExample(ctx context.Context, baseAgent optimize.OptimizableAgent, evaluator optimize.AgentEvaluator, example optimize.AgentExample, benchmarkCase RLMOverviewBenchmarkCase, cfg RLMOverviewBenchmarkRunConfig) RLMOverviewBenchmarkCaseReport {
	var last RLMOverviewBenchmarkCaseReport
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		caseCtx := ctx
		cancel := func() {}
		if cfg.CaseTimeout > 0 {
			caseCtx, cancel = context.WithTimeout(ctx, cfg.CaseTimeout)
		}

		agent, err := baseAgent.Clone()
		if err != nil {
			cancel()
			last = failedRLMOverviewBenchmarkCaseReport(benchmarkCase, err, attempt)
			continue
		}
		evalResult, err := evaluator.Evaluate(caseCtx, agent, example)
		cancel()
		if err != nil {
			last = failedRLMOverviewBenchmarkCaseReport(benchmarkCase, err, attempt)
			continue
		}
		if evalResult == nil {
			last = failedRLMOverviewBenchmarkCaseReport(benchmarkCase, fmt.Errorf("nil evaluation result"), attempt)
			continue
		}
		last = rlmOverviewBenchmarkCaseReportFromEval(benchmarkCase, evalResult)
		last.Attempts = attempt
		if last.Error == "" {
			return last
		}
	}
	return last
}

func failedRLMOverviewBenchmarkCaseReport(benchmarkCase RLMOverviewBenchmarkCase, err error, attempts int) RLMOverviewBenchmarkCaseReport {
	message := ""
	if err != nil {
		message = err.Error()
	}
	diagnostics := map[string]interface{}{}
	if message != "" {
		diagnostics["error"] = message
	}
	return RLMOverviewBenchmarkCaseReport{
		CaseID:      benchmarkCase.ID,
		Protected:   benchmarkCase.Protected,
		Score:       0,
		Attempts:    attempts,
		Error:       message,
		Diagnostics: diagnostics,
	}
}

func rlmOverviewBenchmarkCaseReportFromEval(benchmarkCase RLMOverviewBenchmarkCase, result *optimize.EvalResult) RLMOverviewBenchmarkCaseReport {
	report := RLMOverviewBenchmarkCaseReport{
		CaseID:    benchmarkCase.ID,
		Protected: benchmarkCase.Protected,
		Score:     result.Score,
	}
	if result.SideInfo == nil {
		return report
	}
	report.Tokens = cloneInt64Map(result.SideInfo.Tokens)
	report.Diagnostics = cloneInterfaceMap(result.SideInfo.Diagnostics)
	report.FactRecall = result.SideInfo.Scores["fact_recall"]
	report.SourceCoverage = result.SideInfo.Scores["source_coverage"]
	report.Terseness = result.SideInfo.Scores["terseness"]
	if hits, ok := result.SideInfo.Diagnostics["forbidden_hits"].([]string); ok {
		report.ForbiddenHits = append([]string(nil), hits...)
	}
	if missing, ok := result.SideInfo.Diagnostics["missing_facts"].([]string); ok {
		report.MissingFacts = append([]string(nil), missing...)
	}
	if missing, ok := result.SideInfo.Diagnostics["missing_sources"].([]string); ok {
		report.MissingSources = append([]string(nil), missing...)
	}
	if evalErr, ok := result.SideInfo.Diagnostics["evaluation_error"].(string); ok {
		report.Error = evalErr
		if report.Diagnostics == nil {
			report.Diagnostics = make(map[string]interface{})
		}
		report.Diagnostics["error"] = evalErr
	}
	return report
}

func NewRLMOverviewBenchmarkBaseline(report *RLMOverviewBenchmarkRunReport) (*RLMOverviewBenchmarkBaseline, error) {
	if report == nil {
		return nil, fmt.Errorf("RLM overview benchmark report is nil")
	}
	baseline := &RLMOverviewBenchmarkBaseline{
		Version:        RLMOverviewBenchmarkBaselineVersion,
		AgentSignature: report.AgentSignature,
		Scores:         make(map[string]RLMOverviewBenchmarkBaselineCaseScore, len(report.Results)),
	}
	if baseline.AgentSignature == "" {
		baseline.AgentSignature = RLMOverviewBenchmarkAgentSignature
	}
	for _, result := range report.Results {
		if strings.TrimSpace(result.Error) != "" {
			return nil, fmt.Errorf("refusing to build RLM overview baseline from errored case %q: %s", result.CaseID, result.Error)
		}
		baseline.Scores[result.CaseID] = RLMOverviewBenchmarkBaselineCaseScore{
			Score:          result.Score,
			FactRecall:     result.FactRecall,
			SourceCoverage: result.SourceCoverage,
			Terseness:      result.Terseness,
			ForbiddenHits:  append([]string(nil), result.ForbiddenHits...),
		}
	}
	return baseline, nil
}

func (b *RLMOverviewBenchmarkBaseline) Validate(agentSignature string) error {
	if b == nil {
		return fmt.Errorf("RLM overview benchmark baseline is nil")
	}
	if b.Version != RLMOverviewBenchmarkBaselineVersion {
		return fmt.Errorf("unsupported RLM overview baseline version %d", b.Version)
	}
	if strings.TrimSpace(b.AgentSignature) == "" {
		return fmt.Errorf("RLM overview baseline missing agent_signature")
	}
	if strings.TrimSpace(agentSignature) != "" && b.AgentSignature != agentSignature {
		return fmt.Errorf("RLM overview baseline agent_signature %q does not match %q", b.AgentSignature, agentSignature)
	}
	if len(b.Scores) == 0 {
		return fmt.Errorf("RLM overview baseline has no scores")
	}
	return nil
}

func LoadRLMOverviewBenchmarkBaseline(path string) (*RLMOverviewBenchmarkBaseline, error) {
	resolvedPath, err := expandBenchmarkPath(path, "")
	if err != nil {
		return nil, fmt.Errorf("resolve RLM overview baseline path %q: %w", path, err)
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("read RLM overview baseline %q: %w", resolvedPath, err)
	}
	var baseline RLMOverviewBenchmarkBaseline
	if err := json.Unmarshal(data, &baseline); err != nil {
		return nil, fmt.Errorf("decode RLM overview baseline %q: %w", resolvedPath, err)
	}
	if err := baseline.Validate(RLMOverviewBenchmarkAgentSignature); err != nil {
		return nil, err
	}
	return &baseline, nil
}

func WriteRLMOverviewBenchmarkBaseline(path string, baseline *RLMOverviewBenchmarkBaseline) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("RLM overview baseline path is required")
	}
	if err := baseline.Validate(RLMOverviewBenchmarkAgentSignature); err != nil {
		return err
	}
	resolvedPath, err := expandBenchmarkPath(path, "")
	if err != nil {
		return fmt.Errorf("resolve RLM overview baseline path %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return fmt.Errorf("create RLM overview baseline directory: %w", err)
	}
	data, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal RLM overview baseline: %w", err)
	}
	if err := os.WriteFile(resolvedPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write RLM overview baseline %q: %w", resolvedPath, err)
	}
	return nil
}

func EvaluateRLMOverviewProtectedGate(report *RLMOverviewBenchmarkRunReport, baseline *RLMOverviewBenchmarkBaseline, tolerance float64) *RLMOverviewProtectedGateReport {
	gate := &RLMOverviewProtectedGateReport{
		BaselineVersion:  RLMOverviewBenchmarkBaselineVersion,
		AgentSignature:   RLMOverviewBenchmarkAgentSignature,
		Tolerance:        tolerance,
		Passed:           true,
		ProtectedCaseIDs: make([]string, 0),
	}
	if report == nil || baseline == nil {
		gate.Passed = false
		return gate
	}
	if tolerance < 0 {
		tolerance = 0
		gate.Tolerance = 0
	}
	gate.BaselineVersion = baseline.Version
	gate.AgentSignature = baseline.AgentSignature
	for _, result := range report.Results {
		if !result.Protected {
			continue
		}
		gate.ProtectedCaseIDs = append(gate.ProtectedCaseIDs, result.CaseID)
		base, ok := baseline.Scores[result.CaseID]
		if !ok {
			gate.MissingBaseline = append(gate.MissingBaseline, result.CaseID)
			gate.Passed = false
			continue
		}
		current := baselineScoreFromCaseReport(result)
		if current.Score < base.Score-tolerance {
			gate.Passed = false
			gate.Regressions = append(gate.Regressions, RLMOverviewProtectedRegression{
				CaseID:        result.CaseID,
				Baseline:      base,
				Current:       current,
				ScoreDelta:    current.Score - base.Score,
				RegressedDims: regressedRLMOverviewDimensions(base, current, tolerance),
			})
		}
	}
	return gate
}

func baselineScoreFromCaseReport(result RLMOverviewBenchmarkCaseReport) RLMOverviewBenchmarkBaselineCaseScore {
	return RLMOverviewBenchmarkBaselineCaseScore{
		Score:          result.Score,
		FactRecall:     result.FactRecall,
		SourceCoverage: result.SourceCoverage,
		Terseness:      result.Terseness,
		ForbiddenHits:  append([]string(nil), result.ForbiddenHits...),
	}
}

func regressedRLMOverviewDimensions(base, current RLMOverviewBenchmarkBaselineCaseScore, tolerance float64) []string {
	regressed := make([]string, 0, 4)
	if current.FactRecall < base.FactRecall-tolerance {
		regressed = append(regressed, "fact_recall")
	}
	if current.SourceCoverage < base.SourceCoverage-tolerance {
		regressed = append(regressed, "source_coverage")
	}
	if current.Terseness < base.Terseness-tolerance {
		regressed = append(regressed, "terseness")
	}
	if len(current.ForbiddenHits) > len(base.ForbiddenHits) {
		regressed = append(regressed, "forbidden_hits")
	}
	return regressed
}

func rlmOverviewAgentResultFromOutput(output map[string]interface{}) RLMOverviewAgentResult {
	if output == nil {
		return RLMOverviewAgentResult{}
	}
	return RLMOverviewAgentResult{
		Answer:            strings.TrimSpace(stringValue(output["answer"])),
		RawAnswer:         strings.TrimSpace(stringValue(output["raw_answer"])),
		Sources:           stringsFromAgentOutput(output["sources"]),
		NeedsVerification: rlmOverviewVerificationsFromOutput(output["needs_verification"]),
	}
}

func rlmOverviewEvaluationDiagnostics(evaluation RLMOverviewEvaluation) map[string]interface{} {
	diagnostics := cloneInterfaceMap(evaluation.Diagnostics)
	diagnostics["score"] = evaluation.Score
	diagnostics["fact_recall"] = evaluation.FactRecall
	diagnostics["source_coverage"] = evaluation.SourceCoverage
	diagnostics["terseness"] = evaluation.Terseness
	diagnostics["answer_words"] = evaluation.AnswerWords
	diagnostics["matched_facts"] = append([]string(nil), evaluation.MatchedFacts...)
	diagnostics["missing_facts"] = append([]string(nil), evaluation.MissingFacts...)
	diagnostics["forbidden_hits"] = append([]string(nil), evaluation.ForbiddenHits...)
	diagnostics["matched_sources"] = append([]string(nil), evaluation.MatchedSources...)
	diagnostics["missing_sources"] = append([]string(nil), evaluation.MissingSources...)
	return diagnostics
}

func rlmOverviewVerificationsFromOutput(value interface{}) []rlmOverviewVerification {
	switch typed := value.(type) {
	case []rlmOverviewVerification:
		return append([]rlmOverviewVerification(nil), typed...)
	case []interface{}:
		result := make([]rlmOverviewVerification, 0, len(typed))
		for _, item := range typed {
			data, err := json.Marshal(item)
			if err != nil {
				continue
			}
			var verification rlmOverviewVerification
			if err := json.Unmarshal(data, &verification); err == nil && strings.TrimSpace(verification.Package) != "" {
				result = append(result, verification)
			}
		}
		return result
	default:
		return nil
	}
}

func mergeTokenUsage(dst map[string]int64, src map[string]int64) {
	if dst == nil || src == nil {
		return
	}
	for key, value := range src {
		dst[key] += value
	}
}

func cloneInt64Map(src map[string]int64) map[string]int64 {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]int64, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneInterfaceMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return map[string]interface{}{}
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
