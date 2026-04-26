package orchestration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

type RLMTargetedAskBenchmarkAgentConfig struct {
	MaxContextChars int
	MaxIterations   int
	MaxTokens       int
	Timeout         time.Duration
	TraceDir        string
}

type RLMTargetedAskBenchmarkAgent struct {
	agent *agentrlm.Agent
	cfg   RLMTargetedAskBenchmarkAgentConfig
}

type rlmTargetedAskBenchmarkEvaluator struct {
	cfg RLMOverviewEvaluatorConfig
}

var _ optimize.OptimizableAgent = (*RLMTargetedAskBenchmarkAgent)(nil)

func DefaultRLMTargetedAskBenchmarkAgentConfig() RLMTargetedAskBenchmarkAgentConfig {
	return RLMTargetedAskBenchmarkAgentConfig{
		MaxContextChars: rlmTargetedAskMaxContextChars,
		MaxIterations:   rlmTargetedAskMaxIterations,
		MaxTokens:       rlmTargetedAskMaxTokens,
		Timeout:         rlmTargetedAskTimeout,
	}
}

func NewRLMTargetedAskBenchmarkAgent(llm core.LLM, cfg RLMTargetedAskBenchmarkAgentConfig) (*RLMTargetedAskBenchmarkAgent, error) {
	if llm == nil {
		return nil, fmt.Errorf("RLM targeted ask benchmark LLM is nil")
	}
	cfg = normalizeRLMTargetedAskBenchmarkAgentConfig(cfg)
	module := modrlm.New(llm, modrlm.NewLLMSubClient(llm), rlmTargetedAskBenchmarkModuleOptions(cfg)...)
	return &RLMTargetedAskBenchmarkAgent{
		agent: agentrlm.NewAgent(RLMTargetedAskAgentSignature, module),
		cfg:   cfg,
	}, nil
}

func normalizeRLMTargetedAskBenchmarkAgentConfig(cfg RLMTargetedAskBenchmarkAgentConfig) RLMTargetedAskBenchmarkAgentConfig {
	defaults := DefaultRLMTargetedAskBenchmarkAgentConfig()
	if cfg.MaxContextChars <= 0 {
		cfg.MaxContextChars = defaults.MaxContextChars
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

func rlmTargetedAskBenchmarkModuleOptions(cfg RLMTargetedAskBenchmarkAgentConfig) []modrlm.Option {
	opts := []modrlm.Option{
		modrlm.WithMaxIterations(cfg.MaxIterations),
		modrlm.WithMaxTokens(cfg.MaxTokens),
		modrlm.WithTimeout(cfg.Timeout),
		modrlm.WithContextPolicyPreset(modrlm.ContextPolicyAdaptive),
		modrlm.WithAdaptiveIteration(),
		modrlm.WithOutputTruncationConfig(modrlm.OutputTruncationConfig{
			Enabled:            true,
			MaxOutputLen:       2200,
			MaxVarPreviewLen:   220,
			MaxHistoryEntryLen: 900,
		}),
	}
	if cfg.TraceDir != "" {
		opts = append(opts, modrlm.WithTraceDir(cfg.TraceDir))
	}
	return opts
}

func (a *RLMTargetedAskBenchmarkAgent) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	if a == nil || a.agent == nil {
		return nil, fmt.Errorf("RLM targeted ask benchmark agent is nil")
	}
	repoPath := strings.TrimSpace(stringValue(input["repo_path"]))
	if repoPath == "" {
		return nil, fmt.Errorf("repo_path is required")
	}
	question := strings.TrimSpace(stringValue(input["question"]))
	if question == "" {
		return nil, fmt.Errorf("question is required")
	}

	manifest, err := buildRLMTargetedAskContext(ctx, repoPath, question, a.cfg.MaxContextChars)
	if err != nil {
		return nil, fmt.Errorf("build targeted ask context: %w", err)
	}

	result, err := a.agent.Execute(ctx, map[string]interface{}{
		"context": manifest.Context,
		"query":   buildRLMTargetedAskQuery(question, manifest.Sources),
	})
	rawAnswer := strings.TrimSpace(stringValue(result["answer"]))
	rawOutput, parsed, parseErr := parseRLMTargetedAskOutputWithFallback(rawAnswer, "")
	answer := strings.TrimSpace(parsed.Answer)
	if answer == "" {
		answer = strings.TrimSpace(rawOutput)
	}
	sources := sanitizeRLMTargetedAskSources(parsed.Sources, manifest.Sources)
	if len(sources) == 0 {
		sources = append([]string(nil), manifest.Sources...)
	}

	output := map[string]interface{}{
		"answer":     answer,
		"raw_answer": rawAnswer,
		"sources":    sources,
	}
	if parseErr != nil && rawAnswer != "" {
		output["parse_error"] = parseErr.Error()
	}
	return output, err
}

func (a *RLMTargetedAskBenchmarkAgent) GetCapabilities() []core.Tool {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.GetCapabilities()
}

func (a *RLMTargetedAskBenchmarkAgent) GetMemory() agents.Memory {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.GetMemory()
}

func (a *RLMTargetedAskBenchmarkAgent) GetArtifacts() optimize.AgentArtifacts {
	if a == nil || a.agent == nil {
		return optimize.AgentArtifacts{}
	}
	return a.agent.GetArtifacts()
}

func (a *RLMTargetedAskBenchmarkAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	if a == nil || a.agent == nil {
		return fmt.Errorf("RLM targeted ask benchmark agent is nil")
	}
	return a.agent.SetArtifacts(artifacts)
}

func (a *RLMTargetedAskBenchmarkAgent) Clone() (optimize.OptimizableAgent, error) {
	if a == nil || a.agent == nil {
		return nil, fmt.Errorf("RLM targeted ask benchmark agent is nil")
	}
	cloned, err := a.agent.Clone()
	if err != nil {
		return nil, err
	}
	rlmAgent, ok := cloned.(*agentrlm.Agent)
	if !ok {
		return nil, fmt.Errorf("RLM targeted ask benchmark clone produced %T", cloned)
	}
	return &RLMTargetedAskBenchmarkAgent{agent: rlmAgent, cfg: a.cfg}, nil
}

func (a *RLMTargetedAskBenchmarkAgent) LastExecutionTrace() *agents.ExecutionTrace {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.LastExecutionTrace()
}

func (a *RLMTargetedAskBenchmarkAgent) OptimizationAgentType() string {
	return RLMTargetedAskAgentSignature
}

func (a *RLMTargetedAskBenchmarkAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.ListOptimizationTargets()
}

func (a *RLMTargetedAskBenchmarkAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	if a == nil || a.agent == nil {
		return fmt.Errorf("RLM targeted ask benchmark agent is nil")
	}
	return a.agent.UpdateArtifacts(update)
}

func NewRLMTargetedAskBenchmarkEvaluator(cfg RLMOverviewEvaluatorConfig) optimize.AgentEvaluator {
	return &rlmTargetedAskBenchmarkEvaluator{cfg: normalizeRLMOverviewEvaluatorConfig(cfg)}
}

func (e *rlmTargetedAskBenchmarkEvaluator) Evaluate(ctx context.Context, agent optimize.OptimizableAgent, ex optimize.AgentExample) (*optimize.EvalResult, error) {
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

	return &optimize.EvalResult{
		Score: evaluation.Score,
		SideInfo: &optimize.SideInfo{
			LatencyMS: latencyMS,
			Trace:     agentResult.Trace,
			Tokens:    traceTokenUsage(agentResult.Trace),
			Scores: map[string]float64{
				"fact_recall":     evaluation.FactRecall,
				"source_coverage": evaluation.SourceCoverage,
				"terseness":       evaluation.Terseness,
			},
			Diagnostics: rlmOverviewEvaluationDiagnostics(evaluation),
		},
	}, nil
}
