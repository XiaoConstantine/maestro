package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
)

const (
	RLMOverviewBenchmarkBaselineVersion      = 1
	RLMOverviewBenchmarkAgentSignature       = "maestro.rlm-overview-benchmark.v1"
	RLMOverviewDirectBenchmarkAgentSignature = "maestro.rlm-overview-direct-baseline.v1"
	RLMOverviewBenchmarkDefaultPassThreshold = 0.70
)

type RLMOverviewAgentResult struct {
	Answer            string
	RawAnswer         string
	Sources           []string
	NeedsVerification []rlmOverviewVerification
	ManifestContext   string
	Trace             *agents.ExecutionTrace
	ParseError        string
	ManifestChars     int
	FullContextCap    int
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

type RLMOverviewDirectBenchmarkAgent struct {
	llm       core.LLM
	cfg       RLMOverviewBenchmarkAgentConfig
	lastTrace *agents.ExecutionTrace
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
	MinFinalAnswerRate           float64
	MaxParseErrors               int
	MaxCostPerCorrectUSD         float64
}

type RLMOverviewBenchmarkRunReport struct {
	Version            int                                  `json:"version"`
	AgentSignature     string                               `json:"agent_signature"`
	StartedAt          time.Time                            `json:"started_at"`
	CompletedAt        time.Time                            `json:"completed_at"`
	AverageScore       float64                              `json:"average_score"`
	AverageQuality     RLMOverviewQualitySummary            `json:"average_quality"`
	PassedExamples     int                                  `json:"passed_examples"`
	FailedExamples     int                                  `json:"failed_examples"`
	CompletedExamples  int                                  `json:"completed_examples"`
	EvaluationErrors   int                                  `json:"evaluation_errors"`
	TokenUsage         map[string]int64                     `json:"token_usage,omitempty"`
	CostUSD            float64                              `json:"cost_usd,omitempty"`
	LatencyMS          RLMOverviewLatencySummary            `json:"latency_ms,omitempty"`
	RLMMetrics         RLMOverviewTraceMetricsSummary       `json:"rlm_metrics"`
	Results            []RLMOverviewBenchmarkCaseReport     `json:"results"`
	ProtectedGate      *RLMOverviewProtectedGateReport      `json:"protected_gate,omitempty"`
	AcceptanceGate     *RLMOverviewAcceptanceGateReport     `json:"acceptance_gate,omitempty"`
	DirectBaseline     *RLMOverviewBenchmarkRunReport       `json:"direct_baseline,omitempty"`
	BaselineComparison *RLMOverviewBaselineComparisonReport `json:"baseline_comparison,omitempty"`
	Ablations          *RLMOverviewAblationSummary          `json:"ablations,omitempty"`
	FailureClasses     map[string]int                       `json:"failure_classes,omitempty"`
}

type RLMOverviewBenchmarkCaseReport struct {
	CaseID                  string                      `json:"case_id"`
	Protected               bool                        `json:"protected,omitempty"`
	Score                   float64                     `json:"score"`
	ExactGroundingScore     float64                     `json:"exact_grounding_score"`
	SemanticQualityScore    float64                     `json:"semantic_quality_score"`
	FactRecall              float64                     `json:"fact_recall"`
	FactPrecision           float64                     `json:"fact_precision"`
	SourceCoverage          float64                     `json:"source_coverage"`
	SourceRecall            float64                     `json:"source_recall"`
	SourcePrecision         float64                     `json:"source_precision"`
	SemanticFactRecall      float64                     `json:"semantic_fact_recall"`
	SemanticSourceCoverage  float64                     `json:"semantic_source_coverage"`
	SemanticSourceRecall    float64                     `json:"semantic_source_recall"`
	SemanticSourcePrecision float64                     `json:"semantic_source_precision"`
	ManifestSourceCoverage  float64                     `json:"manifest_source_coverage"`
	EvidenceCoverage        RLMOverviewEvidenceCoverage `json:"evidence_coverage"`
	RepoEvidenceCoverage    RLMOverviewEvidenceCoverage `json:"repo_evidence_coverage"`
	SchemaValid             bool                        `json:"schema_valid"`
	Terseness               float64                     `json:"terseness"`
	ForbiddenHits           []string                    `json:"forbidden_hits,omitempty"`
	CitedSources            []string                    `json:"cited_sources,omitempty"`
	UnexpectedSources       []string                    `json:"unexpected_sources,omitempty"`
	MissingFacts            []string                    `json:"missing_facts,omitempty"`
	MissingSources          []string                    `json:"missing_sources,omitempty"`
	Tokens                  map[string]int64            `json:"tokens,omitempty"`
	CostUSD                 float64                     `json:"cost_usd,omitempty"`
	LatencyMS               float64                     `json:"latency_ms,omitempty"`
	RLMMetrics              RLMOverviewTraceMetrics     `json:"rlm_metrics"`
	Attempts                int                         `json:"attempts,omitempty"`
	Error                   string                      `json:"error,omitempty"`
	FailureClassification   string                      `json:"failure_classification,omitempty"`
	Diagnostics             map[string]interface{}      `json:"diagnostics,omitempty"`
}

type RLMOverviewQualitySummary struct {
	ExactGroundingScore     float64 `json:"exact_grounding_score"`
	SemanticQualityScore    float64 `json:"semantic_quality_score"`
	FactRecall              float64 `json:"fact_recall"`
	FactPrecision           float64 `json:"fact_precision"`
	SourceCoverage          float64 `json:"source_coverage"`
	SourceRecall            float64 `json:"source_recall"`
	SourcePrecision         float64 `json:"source_precision"`
	SemanticFactRecall      float64 `json:"semantic_fact_recall"`
	SemanticSourceCoverage  float64 `json:"semantic_source_coverage"`
	SemanticSourceRecall    float64 `json:"semantic_source_recall"`
	SemanticSourcePrecision float64 `json:"semantic_source_precision"`
	ManifestSourceCoverage  float64 `json:"manifest_source_coverage"`
	EvidenceFactCoverage    float64 `json:"evidence_fact_coverage"`
	EvidenceSourceCoverage  float64 `json:"evidence_source_coverage"`
	RepoFactCoverage        float64 `json:"repo_fact_coverage"`
	RepoSourceCoverage      float64 `json:"repo_source_coverage"`
	SchemaValidRate         float64 `json:"schema_valid_rate"`
	Terseness               float64 `json:"terseness"`
}

type RLMOverviewLatencySummary struct {
	Average float64 `json:"average,omitempty"`
	P50     float64 `json:"p50,omitempty"`
	P95     float64 `json:"p95,omitempty"`
}

type RLMOverviewTraceMetrics struct {
	RootPromptMaxTokens          int64          `json:"root_prompt_max_tokens"`
	RootPromptMeanTokens         int64          `json:"root_prompt_mean_tokens"`
	FullContextQuerySuccessCount int            `json:"full_context_query_success_count"`
	FullContextQueryBlockCount   int            `json:"full_context_query_block_count,omitempty"`
	SliceQueryRatio              float64        `json:"slice_query_ratio"`
	SubcallUsefulRatio           float64        `json:"subcall_useful_ratio"`
	NoOpIterationCount           int            `json:"no_op_iteration_count"`
	ParseErrorCount              int            `json:"parse_error_count"`
	FinalAnswerRate              float64        `json:"final_answer_rate"`
	TerminationCause             string         `json:"termination_cause,omitempty"`
	SubLLMCallCount              int            `json:"sub_llm_call_count,omitempty"`
	SubRLMCallCount              int            `json:"sub_rlm_call_count,omitempty"`
	QueryActionCount             int            `json:"query_action_count,omitempty"`
	QueryActionSuccessCount      int            `json:"query_action_success_count,omitempty"`
	QueryModeCounts              map[string]int `json:"query_mode_counts,omitempty"`
	ManifestChars                int            `json:"manifest_chars,omitempty"`
	FullContextCap               int            `json:"full_context_cap,omitempty"`
	ObservabilityNotes           []string       `json:"observability_notes,omitempty"`
}

type RLMOverviewTraceMetricsSummary struct {
	RootPromptMaxTokens          int64          `json:"root_prompt_max_tokens"`
	RootPromptMeanTokens         int64          `json:"root_prompt_mean_tokens"`
	FullContextQuerySuccessCount int            `json:"full_context_query_success_count"`
	FullContextQueryBlockCount   int            `json:"full_context_query_block_count,omitempty"`
	SliceQueryRatio              float64        `json:"slice_query_ratio"`
	SubcallUsefulRatio           float64        `json:"subcall_useful_ratio"`
	NoOpIterationCount           int            `json:"no_op_iteration_count"`
	ParseErrorCount              int            `json:"parse_error_count"`
	FinalAnswerRate              float64        `json:"final_answer_rate"`
	TerminationCause             string         `json:"termination_cause,omitempty"`
	TerminationCauses            map[string]int `json:"termination_causes,omitempty"`
	SubLLMCallCount              int            `json:"sub_llm_call_count,omitempty"`
	SubRLMCallCount              int            `json:"sub_rlm_call_count,omitempty"`
	QueryActionCount             int            `json:"query_action_count,omitempty"`
	QueryActionSuccessCount      int            `json:"query_action_success_count,omitempty"`
	QueryModeCounts              map[string]int `json:"query_mode_counts,omitempty"`
}

type RLMOverviewAcceptanceGateReport struct {
	Passed             bool     `json:"passed"`
	Decision           string   `json:"decision"`
	Reasons            []string `json:"reasons,omitempty"`
	MinFinalAnswerRate float64  `json:"min_final_answer_rate"`
	MaxParseErrors     int      `json:"max_parse_errors"`
	MaxCostPerCorrect  float64  `json:"max_cost_per_correct_usd,omitempty"`
}

type RLMOverviewBaselineComparisonReport struct {
	RLMAverageScore            float64 `json:"rlm_average_score"`
	DirectAverageScore         float64 `json:"direct_average_score"`
	QualityDelta               float64 `json:"quality_delta"`
	RLMExactGroundingScore     float64 `json:"rlm_exact_grounding_score"`
	DirectExactGroundingScore  float64 `json:"direct_exact_grounding_score"`
	ExactGroundingDelta        float64 `json:"exact_grounding_delta"`
	RLMSemanticQualityScore    float64 `json:"rlm_semantic_quality_score"`
	DirectSemanticQualityScore float64 `json:"direct_semantic_quality_score"`
	SemanticQualityDelta       float64 `json:"semantic_quality_delta"`
	RLMTokens                  int64   `json:"rlm_tokens,omitempty"`
	DirectTokens               int64   `json:"direct_tokens,omitempty"`
	TokenDelta                 int64   `json:"token_delta,omitempty"`
	TokenSavingsRatio          float64 `json:"token_savings_ratio,omitempty"`
	RLMLatencyAverageMS        float64 `json:"rlm_latency_average_ms,omitempty"`
	DirectLatencyAverageMS     float64 `json:"direct_latency_average_ms,omitempty"`
	RLMCostPerCorrect          float64 `json:"rlm_cost_per_correct,omitempty"`
	DirectCostPerCorrect       float64 `json:"direct_cost_per_correct,omitempty"`
}

type RLMOverviewAblationSummary struct {
	ExactGroundingAverage         float64 `json:"exact_grounding_average"`
	SemanticQualityAverage        float64 `json:"semantic_quality_average"`
	SemanticQualityDelta          float64 `json:"semantic_quality_delta"`
	SemanticRescuedCases          int     `json:"semantic_rescued_cases"`
	CurrentManifestFactCoverage   float64 `json:"current_manifest_fact_coverage"`
	RicherManifestFactCoverage    float64 `json:"richer_manifest_fact_coverage"`
	ManifestFactCoverageDelta     float64 `json:"manifest_fact_coverage_delta"`
	CurrentManifestSourceCoverage float64 `json:"current_manifest_source_coverage"`
	RicherManifestSourceCoverage  float64 `json:"richer_manifest_source_coverage"`
	ManifestSourceCoverageDelta   float64 `json:"manifest_source_coverage_delta"`
	ContextMissingCases           int     `json:"context_missing_cases"`
}

type RLMOverviewBenchmarkBaseline struct {
	Version        int                                              `json:"version"`
	AgentSignature string                                           `json:"agent_signature"`
	Scores         map[string]RLMOverviewBenchmarkBaselineCaseScore `json:"scores"`
}

type RLMOverviewBenchmarkBaselineCaseScore struct {
	Score                  float64  `json:"score"`
	ExactGroundingScore    float64  `json:"exact_grounding_score,omitempty"`
	SemanticQualityScore   float64  `json:"semantic_quality_score,omitempty"`
	FactRecall             float64  `json:"fact_recall"`
	FactPrecision          float64  `json:"fact_precision"`
	SourceCoverage         float64  `json:"source_coverage"`
	SourceRecall           float64  `json:"source_recall"`
	SourcePrecision        float64  `json:"source_precision"`
	ManifestSourceCoverage float64  `json:"manifest_source_coverage"`
	SchemaValid            bool     `json:"schema_valid"`
	Terseness              float64  `json:"terseness"`
	ForbiddenHits          []string `json:"forbidden_hits,omitempty"`
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
var _ optimize.OptimizableAgent = (*RLMOverviewDirectBenchmarkAgent)(nil)

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

func NewRLMOverviewDirectBenchmarkAgent(llm core.LLM, cfg RLMOverviewBenchmarkAgentConfig) (*RLMOverviewDirectBenchmarkAgent, error) {
	if llm == nil {
		return nil, fmt.Errorf("RLM overview direct baseline LLM is nil")
	}
	return &RLMOverviewDirectBenchmarkAgent{
		llm: llm,
		cfg: normalizeRLMOverviewBenchmarkAgentConfig(cfg),
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
		modrlm.WithMaxFullContextQueryChars(rlmMaxFullContextQueryChars),
		modrlm.WithContextInfoPreviewChars(0),
		modrlm.WithAdaptiveIterationConfig(rlmOverviewAdaptiveIterationConfig(cfg.MaxIterations)),
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

	focusedEvidence := buildRLMOverviewFocusedEvidence(repoPath, manifest, question, rlmOverviewFocusedEvidenceMaxChars)
	effectiveEvidenceContext := strings.TrimSpace(manifest.Context)
	if strings.TrimSpace(focusedEvidence.Text) != "" {
		effectiveEvidenceContext = strings.TrimSpace(effectiveEvidenceContext + "\n\n## Focused Manifest Evidence\n" + focusedEvidence.Text)
	}
	result, err := a.agent.Execute(ctx, map[string]interface{}{
		"context": manifest.Context,
		"query":   buildRLMOverviewQueryWithFocusedEvidence(question, focusedEvidence.Text),
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
		"sources":            mergeStringLists(manifest.Sources, focusedEvidence.Sources),
		"needs_verification": sanitizeVerificationTargets(parsed.NeedsVerification),
		"manifest_context":   effectiveEvidenceContext,
		"manifest_chars":     len(manifest.Context),
		"full_context_cap":   rlmMaxFullContextQueryChars,
	}
	if strings.TrimSpace(focusedEvidence.Text) != "" {
		output["focused_evidence_chars"] = len(focusedEvidence.Text)
	}
	if parseErr != nil && rawAnswer != "" {
		output["parse_error"] = parseErr.Error()
	}
	return output, err
}

func (a *RLMOverviewDirectBenchmarkAgent) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	if a == nil || a.llm == nil {
		return nil, fmt.Errorf("RLM overview direct baseline agent is nil")
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
	prompt := buildRLMOverviewDirectPrompt(manifest.Context, question)
	startedAt := time.Now()
	response, err := a.llm.Generate(ctx, prompt, core.WithMaxTokens(a.cfg.MaxTokens), core.WithTemperature(0))
	completedAt := time.Now()
	if err != nil {
		a.lastTrace = &agents.ExecutionTrace{
			AgentType:        RLMOverviewDirectBenchmarkAgentSignature,
			Input:            map[string]interface{}{"repo_path": repoPath, "question": question},
			Status:           agents.TraceStatusFailure,
			Error:            err.Error(),
			StartedAt:        startedAt,
			CompletedAt:      completedAt,
			ProcessingTime:   completedAt.Sub(startedAt),
			ContextMetadata:  map[string]interface{}{},
			TerminationCause: "error",
		}
		return nil, err
	}
	if response == nil {
		err := fmt.Errorf("direct baseline LLM returned nil response")
		a.lastTrace = &agents.ExecutionTrace{
			AgentType:        RLMOverviewDirectBenchmarkAgentSignature,
			Input:            map[string]interface{}{"repo_path": repoPath, "question": question},
			Status:           agents.TraceStatusFailure,
			Error:            err.Error(),
			StartedAt:        startedAt,
			CompletedAt:      completedAt,
			ProcessingTime:   completedAt.Sub(startedAt),
			ContextMetadata:  map[string]interface{}{},
			TerminationCause: "error",
		}
		return nil, err
	}

	rawAnswer := strings.TrimSpace(response.Content)
	rawOutput, parsed, parseErr := parseRLMOverviewOutputWithFallback(rawAnswer, "")
	answer := strings.TrimSpace(parsed.Answer)
	if answer == "" {
		answer = strings.TrimSpace(rawOutput)
	}

	tokenUsage := tokenUsageFromLLMResponse(response)
	metadata := cloneInterfaceMap(response.Metadata)
	a.lastTrace = &agents.ExecutionTrace{
		AgentType:        RLMOverviewDirectBenchmarkAgentSignature,
		Task:             question,
		Input:            map[string]interface{}{"repo_path": repoPath, "question": question},
		Output:           map[string]interface{}{"answer": answer},
		Status:           agents.TraceStatusSuccess,
		StartedAt:        startedAt,
		CompletedAt:      completedAt,
		ProcessingTime:   completedAt.Sub(startedAt),
		TokenUsage:       tokenUsage,
		ContextMetadata:  metadata,
		TerminationCause: "direct_answer",
	}

	output := map[string]interface{}{
		"answer":             answer,
		"raw_answer":         rawAnswer,
		"sources":            append([]string(nil), manifest.Sources...),
		"needs_verification": sanitizeVerificationTargets(parsed.NeedsVerification),
		"manifest_context":   manifest.Context,
		"manifest_chars":     len(manifest.Context),
		"full_context_cap":   rlmMaxFullContextQueryChars,
	}
	if parseErr != nil && rawAnswer != "" {
		output["parse_error"] = parseErr.Error()
	}
	return output, nil
}

func buildRLMOverviewDirectPrompt(contextData, question string) string {
	return fmt.Sprintf(`You are answering a repository overview benchmark case.

Use only the repository context below. Return strict JSON with this shape:
{"answer":"concise repo-grounded answer with source filenames when relevant","needs_verification":[]}

Repository context:
%s

Question:
%s`, strings.TrimSpace(contextData), buildRLMOverviewQuery(question))
}

func (a *RLMOverviewBenchmarkAgent) GetCapabilities() []core.Tool {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.GetCapabilities()
}

func (a *RLMOverviewDirectBenchmarkAgent) GetCapabilities() []core.Tool {
	return nil
}

func (a *RLMOverviewBenchmarkAgent) GetMemory() agents.Memory {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.GetMemory()
}

func (a *RLMOverviewDirectBenchmarkAgent) GetMemory() agents.Memory {
	return nil
}

func (a *RLMOverviewBenchmarkAgent) GetArtifacts() optimize.AgentArtifacts {
	if a == nil || a.agent == nil {
		return optimize.AgentArtifacts{}
	}
	return a.agent.GetArtifacts()
}

func (a *RLMOverviewDirectBenchmarkAgent) GetArtifacts() optimize.AgentArtifacts {
	return optimize.AgentArtifacts{}
}

func (a *RLMOverviewBenchmarkAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	if a == nil || a.agent == nil {
		return fmt.Errorf("RLM overview benchmark agent is nil")
	}
	return a.agent.SetArtifacts(artifacts)
}

func (a *RLMOverviewDirectBenchmarkAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	return nil
}

func (a *RLMOverviewBenchmarkAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	if a == nil || a.agent == nil {
		return fmt.Errorf("RLM overview benchmark agent is nil")
	}
	return a.agent.UpdateArtifacts(update)
}

func (a *RLMOverviewDirectBenchmarkAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	if update == nil {
		return nil
	}
	_, err := update(optimize.AgentArtifacts{})
	return err
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

func (a *RLMOverviewDirectBenchmarkAgent) Clone() (optimize.OptimizableAgent, error) {
	if a == nil || a.llm == nil {
		return nil, fmt.Errorf("RLM overview direct baseline agent is nil")
	}
	return &RLMOverviewDirectBenchmarkAgent{
		llm: a.llm,
		cfg: a.cfg,
	}, nil
}

func (a *RLMOverviewBenchmarkAgent) LastExecutionTrace() *agents.ExecutionTrace {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.LastExecutionTrace()
}

func (a *RLMOverviewDirectBenchmarkAgent) LastExecutionTrace() *agents.ExecutionTrace {
	if a == nil || a.lastTrace == nil {
		return nil
	}
	return a.lastTrace.Clone()
}

func (a *RLMOverviewBenchmarkAgent) OptimizationAgentType() string {
	return RLMOverviewBenchmarkAgentSignature
}

func (a *RLMOverviewDirectBenchmarkAgent) OptimizationAgentType() string {
	return RLMOverviewDirectBenchmarkAgentSignature
}

func (a *RLMOverviewBenchmarkAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	if a == nil || a.agent == nil {
		return nil
	}
	return a.agent.ListOptimizationTargets()
}

func (a *RLMOverviewDirectBenchmarkAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	return nil
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
	metrics := rlmOverviewTraceMetrics(agentResult, evaluation)
	if evaluation.Diagnostics == nil {
		evaluation.Diagnostics = make(map[string]interface{})
	}
	evaluation.Diagnostics["rlm_metrics"] = metrics
	tokens := traceTokenUsage(agentResult.Trace)

	sideInfo := &optimize.SideInfo{
		LatencyMS: latencyMS,
		Trace:     agentResult.Trace,
		Tokens:    tokens,
		Cost:      rlmOverviewTraceCostUSD(agentResult.Trace, tokens),
		Scores: map[string]float64{
			"exact_grounding_score":     evaluation.ExactGroundingScore,
			"semantic_quality_score":    evaluation.SemanticQualityScore,
			"fact_recall":               evaluation.FactRecall,
			"fact_precision":            evaluation.FactPrecision,
			"source_coverage":           evaluation.SourceCoverage,
			"source_recall":             evaluation.SourceRecall,
			"source_precision":          evaluation.SourcePrecision,
			"semantic_fact_recall":      evaluation.SemanticFactRecall,
			"semantic_source_coverage":  evaluation.SemanticSourceCoverage,
			"semantic_source_recall":    evaluation.SemanticSourceRecall,
			"semantic_source_precision": evaluation.SemanticSourcePrecision,
			"manifest_source_coverage":  evaluation.ManifestSourceCoverage,
			"evidence_fact_coverage":    evaluation.EvidenceCoverage.FactCoverage,
			"evidence_source_coverage":  evaluation.EvidenceCoverage.SourceCoverage,
			"repo_fact_coverage":        evaluation.RepoEvidenceCoverage.FactCoverage,
			"repo_source_coverage":      evaluation.RepoEvidenceCoverage.SourceCoverage,
			"schema_valid":              boolScore(evaluation.SchemaValid),
			"terseness":                 evaluation.Terseness,
		},
		Diagnostics: rlmOverviewEvaluationDiagnostics(evaluation),
	}
	return &optimize.EvalResult{
		Score:    evaluation.Score,
		SideInfo: sideInfo,
	}, nil
}

func EvaluateRLMOverviewAgentResult(benchmarkCase RLMOverviewBenchmarkCase, result RLMOverviewAgentResult, cfg RLMOverviewEvaluatorConfig) RLMOverviewEvaluation {
	schemaValid := strings.TrimSpace(result.ParseError) == "" && strings.TrimSpace(result.Answer) != ""
	evaluation := evaluateRLMOverviewAnswerWithEvidence(benchmarkCase, result.Answer, result.Sources, result.ManifestContext, schemaValid, cfg)
	if evaluation.Diagnostics == nil {
		evaluation.Diagnostics = make(map[string]interface{})
	}
	evaluation.Diagnostics["raw_answer"] = result.RawAnswer
	evaluation.Diagnostics["sources"] = append([]string(nil), result.Sources...)
	evaluation.Diagnostics["schema_valid"] = evaluation.SchemaValid
	if result.ParseError != "" {
		evaluation.Diagnostics["parse_error"] = result.ParseError
	}
	if result.ManifestChars > 0 {
		evaluation.Diagnostics["manifest_chars"] = result.ManifestChars
	}
	if result.FullContextCap > 0 {
		evaluation.Diagnostics["full_context_cap"] = result.FullContextCap
	}
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
		AgentSignature: rlmOverviewOptimizationAgentType(agent),
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

	for i := range report.Results {
		report.Results[i].FailureClassification = classifyRLMOverviewCaseReport(report.Results[i], cfg.PassThreshold)
	}

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
		report.CostUSD += result.CostUSD
	}
	if report.CompletedExamples > 0 {
		report.AverageScore /= float64(report.CompletedExamples)
	}
	report.AverageQuality = summarizeRLMOverviewQuality(report.Results)
	report.LatencyMS = summarizeRLMOverviewLatency(report.Results)
	report.RLMMetrics = summarizeRLMOverviewTraceMetrics(report.Results)
	report.Ablations = summarizeRLMOverviewAblations(report.Results, cfg.PassThreshold)
	report.FailureClasses = summarizeRLMOverviewFailureClasses(report.Results)
	if len(report.TokenUsage) == 0 {
		report.TokenUsage = nil
	}
	if cfg.Baseline != nil {
		report.ProtectedGate = EvaluateRLMOverviewProtectedGate(report, cfg.Baseline, cfg.ProtectedRegressionTolerance)
	}
	report.AcceptanceGate = EvaluateRLMOverviewAcceptanceGate(report, cfg)
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
	if cfg.MinFinalAnswerRate <= 0 || cfg.MinFinalAnswerRate > 1 {
		cfg.MinFinalAnswerRate = 0.95
	}
	if cfg.MaxParseErrors < 0 {
		cfg.MaxParseErrors = 0
	}
	if cfg.MaxCostPerCorrectUSD < 0 {
		cfg.MaxCostPerCorrectUSD = 0
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
	report.CostUSD = result.SideInfo.Cost
	report.LatencyMS = result.SideInfo.LatencyMS
	report.ExactGroundingScore = result.SideInfo.Scores["exact_grounding_score"]
	if report.ExactGroundingScore == 0 && result.Score > 0 {
		report.ExactGroundingScore = result.Score
	}
	report.SemanticQualityScore = result.SideInfo.Scores["semantic_quality_score"]
	report.FactRecall = result.SideInfo.Scores["fact_recall"]
	report.FactPrecision = result.SideInfo.Scores["fact_precision"]
	report.SourceCoverage = result.SideInfo.Scores["source_coverage"]
	report.SourceRecall = result.SideInfo.Scores["source_recall"]
	report.SourcePrecision = result.SideInfo.Scores["source_precision"]
	report.SemanticFactRecall = result.SideInfo.Scores["semantic_fact_recall"]
	report.SemanticSourceCoverage = result.SideInfo.Scores["semantic_source_coverage"]
	report.SemanticSourceRecall = result.SideInfo.Scores["semantic_source_recall"]
	report.SemanticSourcePrecision = result.SideInfo.Scores["semantic_source_precision"]
	report.ManifestSourceCoverage = result.SideInfo.Scores["manifest_source_coverage"]
	report.EvidenceCoverage = evidenceCoverageFromDiagnostics(result.SideInfo.Diagnostics, "evidence_coverage")
	report.RepoEvidenceCoverage = evidenceCoverageFromDiagnostics(result.SideInfo.Diagnostics, "repo_evidence_coverage")
	report.SchemaValid = result.SideInfo.Scores["schema_valid"] >= 1
	report.Terseness = result.SideInfo.Scores["terseness"]
	if hits, ok := result.SideInfo.Diagnostics["forbidden_hits"].([]string); ok {
		report.ForbiddenHits = append([]string(nil), hits...)
	}
	if cited, ok := result.SideInfo.Diagnostics["cited_sources"].([]string); ok {
		report.CitedSources = append([]string(nil), cited...)
	}
	if unexpected, ok := result.SideInfo.Diagnostics["unexpected_sources"].([]string); ok {
		report.UnexpectedSources = append([]string(nil), unexpected...)
	}
	if missing, ok := result.SideInfo.Diagnostics["missing_facts"].([]string); ok {
		report.MissingFacts = append([]string(nil), missing...)
	}
	if missing, ok := result.SideInfo.Diagnostics["missing_sources"].([]string); ok {
		report.MissingSources = append([]string(nil), missing...)
	}
	if classification, ok := result.SideInfo.Diagnostics["failure_classification"].(string); ok {
		report.FailureClassification = classification
	}
	report.RLMMetrics = rlmOverviewMetricsFromDiagnostics(result.SideInfo.Diagnostics)
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
			Score:                  result.Score,
			ExactGroundingScore:    result.ExactGroundingScore,
			SemanticQualityScore:   result.SemanticQualityScore,
			FactRecall:             result.FactRecall,
			FactPrecision:          result.FactPrecision,
			SourceCoverage:         result.SourceCoverage,
			SourceRecall:           result.SourceRecall,
			SourcePrecision:        result.SourcePrecision,
			ManifestSourceCoverage: result.ManifestSourceCoverage,
			SchemaValid:            result.SchemaValid,
			Terseness:              result.Terseness,
			ForbiddenHits:          append([]string(nil), result.ForbiddenHits...),
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
		Score:                  result.Score,
		ExactGroundingScore:    result.ExactGroundingScore,
		SemanticQualityScore:   result.SemanticQualityScore,
		FactRecall:             result.FactRecall,
		FactPrecision:          result.FactPrecision,
		SourceCoverage:         result.SourceCoverage,
		SourceRecall:           result.SourceRecall,
		SourcePrecision:        result.SourcePrecision,
		ManifestSourceCoverage: result.ManifestSourceCoverage,
		SchemaValid:            result.SchemaValid,
		Terseness:              result.Terseness,
		ForbiddenHits:          append([]string(nil), result.ForbiddenHits...),
	}
}

func regressedRLMOverviewDimensions(base, current RLMOverviewBenchmarkBaselineCaseScore, tolerance float64) []string {
	regressed := make([]string, 0, 8)
	if current.FactRecall < base.FactRecall-tolerance {
		regressed = append(regressed, "fact_recall")
	}
	if current.FactPrecision < base.FactPrecision-tolerance {
		regressed = append(regressed, "fact_precision")
	}
	if current.SourceCoverage < base.SourceCoverage-tolerance {
		regressed = append(regressed, "source_coverage")
	}
	if current.SourceRecall < base.SourceRecall-tolerance {
		regressed = append(regressed, "source_recall")
	}
	if current.SourcePrecision < base.SourcePrecision-tolerance {
		regressed = append(regressed, "source_precision")
	}
	if current.ManifestSourceCoverage < base.ManifestSourceCoverage-tolerance {
		regressed = append(regressed, "manifest_source_coverage")
	}
	if base.SchemaValid && !current.SchemaValid {
		regressed = append(regressed, "schema_valid")
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
		ManifestContext:   strings.TrimSpace(stringValue(output["manifest_context"])),
		ParseError:        strings.TrimSpace(stringValue(output["parse_error"])),
		ManifestChars:     intValue(output["manifest_chars"]),
		FullContextCap:    intValue(output["full_context_cap"]),
	}
}

func rlmOverviewEvaluationDiagnostics(evaluation RLMOverviewEvaluation) map[string]interface{} {
	diagnostics := cloneInterfaceMap(evaluation.Diagnostics)
	diagnostics["score"] = evaluation.Score
	diagnostics["exact_grounding_score"] = evaluation.ExactGroundingScore
	diagnostics["semantic_quality_score"] = evaluation.SemanticQualityScore
	diagnostics["fact_recall"] = evaluation.FactRecall
	diagnostics["fact_precision"] = evaluation.FactPrecision
	diagnostics["source_coverage"] = evaluation.SourceCoverage
	diagnostics["source_recall"] = evaluation.SourceRecall
	diagnostics["source_precision"] = evaluation.SourcePrecision
	diagnostics["semantic_fact_recall"] = evaluation.SemanticFactRecall
	diagnostics["semantic_source_coverage"] = evaluation.SemanticSourceCoverage
	diagnostics["semantic_source_recall"] = evaluation.SemanticSourceRecall
	diagnostics["semantic_source_precision"] = evaluation.SemanticSourcePrecision
	diagnostics["manifest_source_coverage"] = evaluation.ManifestSourceCoverage
	diagnostics["evidence_coverage"] = evaluation.EvidenceCoverage
	diagnostics["repo_evidence_coverage"] = evaluation.RepoEvidenceCoverage
	diagnostics["schema_valid"] = evaluation.SchemaValid
	diagnostics["terseness"] = evaluation.Terseness
	diagnostics["answer_words"] = evaluation.AnswerWords
	diagnostics["matched_facts"] = append([]string(nil), evaluation.MatchedFacts...)
	diagnostics["missing_facts"] = append([]string(nil), evaluation.MissingFacts...)
	diagnostics["semantic_matched_facts"] = append([]string(nil), evaluation.SemanticMatchedFacts...)
	diagnostics["semantic_missing_facts"] = append([]string(nil), evaluation.SemanticMissingFacts...)
	diagnostics["forbidden_hits"] = append([]string(nil), evaluation.ForbiddenHits...)
	diagnostics["cited_sources"] = append([]string(nil), evaluation.CitedSources...)
	diagnostics["unexpected_sources"] = append([]string(nil), evaluation.UnexpectedSources...)
	diagnostics["matched_sources"] = append([]string(nil), evaluation.MatchedSources...)
	diagnostics["missing_sources"] = append([]string(nil), evaluation.MissingSources...)
	diagnostics["semantic_matched_sources"] = append([]string(nil), evaluation.SemanticMatchedSources...)
	diagnostics["semantic_missing_sources"] = append([]string(nil), evaluation.SemanticMissingSources...)
	diagnostics["manifest_matched_sources"] = append([]string(nil), evaluation.ManifestMatchedSources...)
	diagnostics["failure_classification"] = classifyRLMOverviewEvaluation(evaluation)
	return diagnostics
}

func EvaluateRLMOverviewAcceptanceGate(report *RLMOverviewBenchmarkRunReport, cfg RLMOverviewBenchmarkRunConfig) *RLMOverviewAcceptanceGateReport {
	cfg = normalizeRLMOverviewBenchmarkRunConfig(cfg)
	gate := &RLMOverviewAcceptanceGateReport{
		Passed:             true,
		Decision:           "accepted",
		MinFinalAnswerRate: cfg.MinFinalAnswerRate,
		MaxParseErrors:     cfg.MaxParseErrors,
		MaxCostPerCorrect:  cfg.MaxCostPerCorrectUSD,
	}
	if report == nil {
		gate.Passed = false
		gate.Decision = "rejected"
		gate.Reasons = append(gate.Reasons, "benchmark report is nil")
		return gate
	}
	if report.EvaluationErrors > 0 {
		gate.Reasons = append(gate.Reasons, fmt.Sprintf("evaluation_errors=%d", report.EvaluationErrors))
	}
	if report.RLMMetrics.FinalAnswerRate < cfg.MinFinalAnswerRate {
		gate.Reasons = append(gate.Reasons, fmt.Sprintf("final_answer_rate %.4f below %.4f", report.RLMMetrics.FinalAnswerRate, cfg.MinFinalAnswerRate))
	}
	if report.RLMMetrics.ParseErrorCount > cfg.MaxParseErrors {
		gate.Reasons = append(gate.Reasons, fmt.Sprintf("parse_error_count=%d exceeds %d", report.RLMMetrics.ParseErrorCount, cfg.MaxParseErrors))
	}
	if report.RLMMetrics.FullContextQuerySuccessCount > 0 {
		gate.Reasons = append(gate.Reasons, fmt.Sprintf("full_context_query_success_count=%d", report.RLMMetrics.FullContextQuerySuccessCount))
	}
	if cfg.MaxCostPerCorrectUSD > 0 {
		actual := costPerCorrect(report.CostUSD, report.PassedExamples)
		if actual > cfg.MaxCostPerCorrectUSD {
			gate.Reasons = append(gate.Reasons, fmt.Sprintf("cost_per_correct_usd %.6f exceeds %.6f", actual, cfg.MaxCostPerCorrectUSD))
		}
	}
	if report.ProtectedGate != nil && !report.ProtectedGate.Passed {
		gate.Reasons = append(gate.Reasons, "protected gate failed")
	}
	if len(gate.Reasons) > 0 {
		gate.Passed = false
		gate.Decision = "rejected"
	}
	return gate
}

func AttachRLMOverviewDirectBaseline(report, direct *RLMOverviewBenchmarkRunReport) *RLMOverviewBaselineComparisonReport {
	if report == nil || direct == nil {
		return nil
	}
	comparison := &RLMOverviewBaselineComparisonReport{
		RLMAverageScore:            report.AverageScore,
		DirectAverageScore:         direct.AverageScore,
		QualityDelta:               report.AverageScore - direct.AverageScore,
		RLMExactGroundingScore:     report.AverageQuality.ExactGroundingScore,
		DirectExactGroundingScore:  direct.AverageQuality.ExactGroundingScore,
		ExactGroundingDelta:        report.AverageQuality.ExactGroundingScore - direct.AverageQuality.ExactGroundingScore,
		RLMSemanticQualityScore:    report.AverageQuality.SemanticQualityScore,
		DirectSemanticQualityScore: direct.AverageQuality.SemanticQualityScore,
		SemanticQualityDelta:       report.AverageQuality.SemanticQualityScore - direct.AverageQuality.SemanticQualityScore,
		RLMTokens:                  totalOverviewTokens(report.TokenUsage),
		DirectTokens:               totalOverviewTokens(direct.TokenUsage),
		RLMLatencyAverageMS:        report.LatencyMS.Average,
		DirectLatencyAverageMS:     direct.LatencyMS.Average,
		RLMCostPerCorrect:          costPerCorrect(report.CostUSD, report.PassedExamples),
		DirectCostPerCorrect:       costPerCorrect(direct.CostUSD, direct.PassedExamples),
	}
	comparison.TokenDelta = comparison.RLMTokens - comparison.DirectTokens
	if comparison.DirectTokens > 0 {
		comparison.TokenSavingsRatio = float64(comparison.DirectTokens-comparison.RLMTokens) / float64(comparison.DirectTokens)
	}
	direct.DirectBaseline = nil
	direct.BaselineComparison = nil
	report.DirectBaseline = direct
	report.BaselineComparison = comparison
	if report.AcceptanceGate != nil && comparison.QualityDelta <= 0 {
		report.AcceptanceGate.Passed = false
		report.AcceptanceGate.Decision = "rejected"
		report.AcceptanceGate.Reasons = append(report.AcceptanceGate.Reasons, fmt.Sprintf("quality_delta_vs_direct %.4f is not positive", comparison.QualityDelta))
	}
	return comparison
}

func rlmOverviewTraceMetrics(result RLMOverviewAgentResult, evaluation RLMOverviewEvaluation) RLMOverviewTraceMetrics {
	metrics := RLMOverviewTraceMetrics{
		ManifestChars:   result.ManifestChars,
		FullContextCap:  result.FullContextCap,
		QueryModeCounts: make(map[string]int),
	}
	if result.ParseError != "" {
		metrics.ParseErrorCount++
	}
	trace := result.Trace
	if trace == nil {
		metrics.ObservabilityNotes = append(metrics.ObservabilityNotes, "execution trace unavailable")
		return metrics
	}

	metrics.RootPromptMaxTokens = int64FromMetric(trace.ContextMetadata[modrlm.TraceMetadataRootPromptMaxTokens])
	metrics.RootPromptMeanTokens = int64FromMetric(trace.ContextMetadata[modrlm.TraceMetadataRootPromptMeanTokens])
	metrics.SubLLMCallCount = intFromMetric(trace.ContextMetadata[modrlm.TraceMetadataSubLLMCallCount])
	metrics.SubRLMCallCount = intFromMetric(trace.ContextMetadata[modrlm.TraceMetadataSubRLMCallCount])
	metrics.TerminationCause = strings.TrimSpace(trace.TerminationCause)
	if metrics.TerminationCause == "" {
		metrics.TerminationCause = strings.TrimSpace(stringValue(trace.ContextMetadata[modrlm.TraceMetadataTerminationCause]))
	}
	if isFinalAnswerTermination(metrics.TerminationCause) {
		metrics.FinalAnswerRate = 1
	}

	queryActionSuccessCount := 0
	for _, step := range trace.Steps {
		if strings.TrimSpace(step.ActionRaw) == "" &&
			strings.TrimSpace(step.Tool) == "" &&
			strings.TrimSpace(step.Observation) == "" &&
			strings.TrimSpace(step.Error) == "" {
			metrics.NoOpIterationCount++
		}
		if containsParseSignal(step.Error) || containsParseSignal(step.Observation) {
			metrics.ParseErrorCount++
		}
		if containsFullContextQueryBlock(step.Error) || containsFullContextQueryBlock(step.Observation) {
			metrics.FullContextQueryBlockCount++
		}
		if strings.EqualFold(strings.TrimSpace(step.ActionRaw), "query") {
			metrics.QueryActionCount++
			mode := rlmOverviewQueryMode(step)
			metrics.QueryModeCounts[mode]++
			if step.Success && !containsFullContextQueryBlock(step.Error) && !containsFullContextQueryBlock(step.Observation) {
				queryActionSuccessCount++
				metrics.QueryActionSuccessCount++
			}
		}
	}
	if containsParseSignal(trace.Error) {
		metrics.ParseErrorCount++
	}
	if containsFullContextQueryBlock(trace.Error) {
		metrics.FullContextQueryBlockCount++
	}

	subcalls := metrics.SubLLMCallCount + metrics.SubRLMCallCount
	if queryActionSuccessCount > 0 && metrics.ManifestChars > 0 && metrics.FullContextCap > 0 && metrics.ManifestChars <= metrics.FullContextCap {
		metrics.ObservabilityNotes = append(metrics.ObservabilityNotes, "query actions on small contexts are tracked separately; dspy-go v0.83.1 does not expose enough code detail to classify Query vs QueryRaw vs QueryWith as full-context")
	}
	if subcalls > 0 {
		if len(evaluation.MatchedFacts) > 0 || evaluation.SourceRecall > 0 || evaluation.SourcePrecision > 0 {
			metrics.SubcallUsefulRatio = 1
		}
		if metrics.ManifestChars > metrics.FullContextCap && metrics.FullContextCap > 0 {
			if metrics.FullContextQueryBlockCount == 0 {
				metrics.SliceQueryRatio = 1
			}
			metrics.ObservabilityNotes = append(metrics.ObservabilityNotes, "dspy-go v0.83.1 exposes subcall counts and full-context guardrail blocks, but not QueryWith vs QueryRaw call mode")
		} else {
			metrics.ObservabilityNotes = append(metrics.ObservabilityNotes, "subcall mode is not exposed by dspy-go v0.83.1 for small-context cases")
		}
	}
	if len(metrics.QueryModeCounts) == 0 {
		metrics.QueryModeCounts = nil
	}
	return metrics
}

func summarizeRLMOverviewQuality(results []RLMOverviewBenchmarkCaseReport) RLMOverviewQualitySummary {
	if len(results) == 0 {
		return RLMOverviewQualitySummary{}
	}
	var summary RLMOverviewQualitySummary
	for _, result := range results {
		summary.ExactGroundingScore += result.ExactGroundingScore
		summary.SemanticQualityScore += result.SemanticQualityScore
		summary.FactRecall += result.FactRecall
		summary.FactPrecision += result.FactPrecision
		summary.SourceCoverage += result.SourceCoverage
		summary.SourceRecall += result.SourceRecall
		summary.SourcePrecision += result.SourcePrecision
		summary.SemanticFactRecall += result.SemanticFactRecall
		summary.SemanticSourceCoverage += result.SemanticSourceCoverage
		summary.SemanticSourceRecall += result.SemanticSourceRecall
		summary.SemanticSourcePrecision += result.SemanticSourcePrecision
		summary.ManifestSourceCoverage += result.ManifestSourceCoverage
		summary.EvidenceFactCoverage += result.EvidenceCoverage.FactCoverage
		summary.EvidenceSourceCoverage += result.EvidenceCoverage.SourceCoverage
		summary.RepoFactCoverage += result.RepoEvidenceCoverage.FactCoverage
		summary.RepoSourceCoverage += result.RepoEvidenceCoverage.SourceCoverage
		if result.SchemaValid {
			summary.SchemaValidRate++
		}
		summary.Terseness += result.Terseness
	}
	count := float64(len(results))
	summary.ExactGroundingScore /= count
	summary.SemanticQualityScore /= count
	summary.FactRecall /= count
	summary.FactPrecision /= count
	summary.SourceCoverage /= count
	summary.SourceRecall /= count
	summary.SourcePrecision /= count
	summary.SemanticFactRecall /= count
	summary.SemanticSourceCoverage /= count
	summary.SemanticSourceRecall /= count
	summary.SemanticSourcePrecision /= count
	summary.ManifestSourceCoverage /= count
	summary.EvidenceFactCoverage /= count
	summary.EvidenceSourceCoverage /= count
	summary.RepoFactCoverage /= count
	summary.RepoSourceCoverage /= count
	summary.SchemaValidRate /= count
	summary.Terseness /= count
	return summary
}

func summarizeRLMOverviewAblations(results []RLMOverviewBenchmarkCaseReport, passThreshold float64) *RLMOverviewAblationSummary {
	if len(results) == 0 {
		return nil
	}
	summary := &RLMOverviewAblationSummary{}
	for _, result := range results {
		summary.ExactGroundingAverage += result.ExactGroundingScore
		summary.SemanticQualityAverage += result.SemanticQualityScore
		summary.CurrentManifestFactCoverage += result.EvidenceCoverage.FactCoverage
		summary.RicherManifestFactCoverage += result.RepoEvidenceCoverage.FactCoverage
		summary.CurrentManifestSourceCoverage += result.EvidenceCoverage.SourceCoverage
		summary.RicherManifestSourceCoverage += result.RepoEvidenceCoverage.SourceCoverage
		if result.ExactGroundingScore < passThreshold && result.SemanticQualityScore >= passThreshold {
			summary.SemanticRescuedCases++
		}
		if result.FailureClassification == "context_missing" {
			summary.ContextMissingCases++
		}
	}
	count := float64(len(results))
	summary.ExactGroundingAverage /= count
	summary.SemanticQualityAverage /= count
	summary.SemanticQualityDelta = summary.SemanticQualityAverage - summary.ExactGroundingAverage
	summary.CurrentManifestFactCoverage /= count
	summary.RicherManifestFactCoverage /= count
	summary.ManifestFactCoverageDelta = summary.RicherManifestFactCoverage - summary.CurrentManifestFactCoverage
	summary.CurrentManifestSourceCoverage /= count
	summary.RicherManifestSourceCoverage /= count
	summary.ManifestSourceCoverageDelta = summary.RicherManifestSourceCoverage - summary.CurrentManifestSourceCoverage
	return summary
}

func summarizeRLMOverviewFailureClasses(results []RLMOverviewBenchmarkCaseReport) map[string]int {
	counts := make(map[string]int)
	for _, result := range results {
		classification := strings.TrimSpace(result.FailureClassification)
		if classification == "" {
			continue
		}
		counts[classification]++
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func summarizeRLMOverviewLatency(results []RLMOverviewBenchmarkCaseReport) RLMOverviewLatencySummary {
	latencies := make([]float64, 0, len(results))
	for _, result := range results {
		if result.LatencyMS > 0 {
			latencies = append(latencies, result.LatencyMS)
		}
	}
	if len(latencies) == 0 {
		return RLMOverviewLatencySummary{}
	}
	sort.Float64s(latencies)
	var total float64
	for _, value := range latencies {
		total += value
	}
	return RLMOverviewLatencySummary{
		Average: total / float64(len(latencies)),
		P50:     percentileOverviewLatency(latencies, 0.50),
		P95:     percentileOverviewLatency(latencies, 0.95),
	}
}

func summarizeRLMOverviewTraceMetrics(results []RLMOverviewBenchmarkCaseReport) RLMOverviewTraceMetricsSummary {
	summary := RLMOverviewTraceMetricsSummary{
		TerminationCauses: make(map[string]int),
		QueryModeCounts:   make(map[string]int),
	}
	if len(results) == 0 {
		summary.TerminationCauses = nil
		summary.QueryModeCounts = nil
		return summary
	}
	var meanPromptTotal int64
	var meanPromptCount int64
	var sliceRatioTotal float64
	var subcallUsefulTotal float64
	for _, result := range results {
		metrics := result.RLMMetrics
		if metrics.RootPromptMaxTokens > summary.RootPromptMaxTokens {
			summary.RootPromptMaxTokens = metrics.RootPromptMaxTokens
		}
		if metrics.RootPromptMeanTokens > 0 {
			meanPromptTotal += metrics.RootPromptMeanTokens
			meanPromptCount++
		}
		summary.FullContextQuerySuccessCount += metrics.FullContextQuerySuccessCount
		summary.FullContextQueryBlockCount += metrics.FullContextQueryBlockCount
		sliceRatioTotal += metrics.SliceQueryRatio
		subcallUsefulTotal += metrics.SubcallUsefulRatio
		summary.NoOpIterationCount += metrics.NoOpIterationCount
		summary.ParseErrorCount += metrics.ParseErrorCount
		summary.FinalAnswerRate += metrics.FinalAnswerRate
		summary.SubLLMCallCount += metrics.SubLLMCallCount
		summary.SubRLMCallCount += metrics.SubRLMCallCount
		summary.QueryActionCount += metrics.QueryActionCount
		summary.QueryActionSuccessCount += metrics.QueryActionSuccessCount
		for mode, count := range metrics.QueryModeCounts {
			summary.QueryModeCounts[mode] += count
		}
		cause := strings.TrimSpace(metrics.TerminationCause)
		if cause != "" {
			summary.TerminationCauses[cause]++
		}
	}
	if meanPromptCount > 0 {
		summary.RootPromptMeanTokens = int64(math.Round(float64(meanPromptTotal) / float64(meanPromptCount)))
	}
	count := float64(len(results))
	summary.SliceQueryRatio = sliceRatioTotal / count
	summary.SubcallUsefulRatio = subcallUsefulTotal / count
	summary.FinalAnswerRate /= count
	if len(summary.TerminationCauses) == 0 {
		summary.TerminationCauses = nil
	} else if len(summary.TerminationCauses) == 1 {
		for cause := range summary.TerminationCauses {
			summary.TerminationCause = cause
		}
	} else {
		summary.TerminationCause = "mixed"
	}
	if len(summary.QueryModeCounts) == 0 {
		summary.QueryModeCounts = nil
	}
	return summary
}

func rlmOverviewMetricsFromDiagnostics(diagnostics map[string]interface{}) RLMOverviewTraceMetrics {
	if diagnostics == nil {
		return RLMOverviewTraceMetrics{}
	}
	switch metrics := diagnostics["rlm_metrics"].(type) {
	case RLMOverviewTraceMetrics:
		return metrics
	case *RLMOverviewTraceMetrics:
		if metrics != nil {
			return *metrics
		}
	case map[string]interface{}:
		return RLMOverviewTraceMetrics{
			RootPromptMaxTokens:          int64FromMetric(metrics["root_prompt_max_tokens"]),
			RootPromptMeanTokens:         int64FromMetric(metrics["root_prompt_mean_tokens"]),
			FullContextQuerySuccessCount: intFromMetric(metrics["full_context_query_success_count"]),
			FullContextQueryBlockCount:   intFromMetric(metrics["full_context_query_block_count"]),
			SliceQueryRatio:              floatFromMetric(metrics["slice_query_ratio"]),
			SubcallUsefulRatio:           floatFromMetric(metrics["subcall_useful_ratio"]),
			NoOpIterationCount:           intFromMetric(metrics["no_op_iteration_count"]),
			ParseErrorCount:              intFromMetric(metrics["parse_error_count"]),
			FinalAnswerRate:              floatFromMetric(metrics["final_answer_rate"]),
			TerminationCause:             strings.TrimSpace(stringValue(metrics["termination_cause"])),
			SubLLMCallCount:              intFromMetric(metrics["sub_llm_call_count"]),
			SubRLMCallCount:              intFromMetric(metrics["sub_rlm_call_count"]),
			QueryActionCount:             intFromMetric(metrics["query_action_count"]),
			QueryActionSuccessCount:      intFromMetric(metrics["query_action_success_count"]),
			QueryModeCounts:              intMapFromMetric(metrics["query_mode_counts"]),
			ManifestChars:                intFromMetric(metrics["manifest_chars"]),
			FullContextCap:               intFromMetric(metrics["full_context_cap"]),
		}
	}
	return RLMOverviewTraceMetrics{}
}

func classifyRLMOverviewEvaluation(evaluation RLMOverviewEvaluation) string {
	if evaluation.EvidenceCoverage.FactCoverage < 1 || evaluation.EvidenceCoverage.SourceCoverage < 1 {
		return "context_missing"
	}
	if evaluation.SemanticQualityScore > evaluation.ExactGroundingScore+0.000001 &&
		(evaluation.SemanticFactRecall > evaluation.FactRecall || evaluation.SemanticSourceRecall > evaluation.SourceRecall || evaluation.SemanticSourcePrecision > evaluation.SourcePrecision) {
		return "semantic_match"
	}
	if len(evaluation.MissingFacts) > 0 || len(evaluation.MissingSources) > 0 || evaluation.SourceRecall < 1 || evaluation.FactRecall < 1 {
		return "answer_missing"
	}
	return "real_behavior_failure"
}

func classifyRLMOverviewCaseReport(result RLMOverviewBenchmarkCaseReport, passThreshold float64) string {
	if result.Error != "" || !result.SchemaValid || result.RLMMetrics.ParseErrorCount > 0 || (result.RLMMetrics.FinalAnswerRate > 0 && result.RLMMetrics.FinalAnswerRate < 1) {
		return "real_behavior_failure"
	}
	if result.Score >= passThreshold {
		return ""
	}
	if result.EvidenceCoverage.FactCoverage < 1 || result.EvidenceCoverage.SourceCoverage < 1 {
		return "context_missing"
	}
	if result.SemanticQualityScore > result.ExactGroundingScore+0.000001 &&
		(result.SemanticFactRecall > result.FactRecall || result.SemanticSourceRecall > result.SourceRecall || result.SemanticSourcePrecision > result.SourcePrecision) {
		return "semantic_match"
	}
	if len(result.MissingFacts) > 0 || len(result.MissingSources) > 0 || result.FactRecall < 1 || result.SourceRecall < 1 {
		return "answer_missing"
	}
	return "real_behavior_failure"
}

func rlmOverviewQueryMode(step agents.TraceStep) string {
	value := strings.ToLower(strings.Join([]string{
		step.ActionRaw,
		step.Tool,
		step.Thought,
		step.Observation,
		step.Error,
	}, "\n"))
	switch {
	case strings.Contains(value, "querywith"):
		return "query_with"
	case strings.Contains(value, "queryraw"):
		return "query_raw"
	case strings.Contains(value, "querybatchedraw"):
		return "query_batched_raw"
	case strings.Contains(value, "querybatched"):
		return "query_batched"
	case strings.Contains(value, "queryasync"):
		return "query_async"
	default:
		return "query_unknown"
	}
}

func evidenceCoverageFromDiagnostics(diagnostics map[string]interface{}, key string) RLMOverviewEvidenceCoverage {
	if diagnostics == nil {
		return RLMOverviewEvidenceCoverage{}
	}
	switch value := diagnostics[key].(type) {
	case RLMOverviewEvidenceCoverage:
		return value
	case *RLMOverviewEvidenceCoverage:
		if value != nil {
			return *value
		}
	case map[string]interface{}:
		return RLMOverviewEvidenceCoverage{
			FactCoverage:   floatFromMetric(value["fact_coverage"]),
			SourceCoverage: floatFromMetric(value["source_coverage"]),
			MatchedFacts:   stringsFromAgentOutput(value["matched_facts"]),
			MissingFacts:   stringsFromAgentOutput(value["missing_facts"]),
			MatchedSources: stringsFromAgentOutput(value["matched_sources"]),
			MissingSources: stringsFromAgentOutput(value["missing_sources"]),
		}
	}
	return RLMOverviewEvidenceCoverage{}
}

func intMapFromMetric(value interface{}) map[string]int {
	switch typed := value.(type) {
	case map[string]int:
		if len(typed) == 0 {
			return nil
		}
		cloned := make(map[string]int, len(typed))
		for key, count := range typed {
			cloned[key] = count
		}
		return cloned
	case map[string]interface{}:
		result := make(map[string]int, len(typed))
		for key, raw := range typed {
			if count := intFromMetric(raw); count > 0 {
				result[key] = count
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	default:
		return nil
	}
}

func tokenUsageFromLLMResponse(response *core.LLMResponse) map[string]int64 {
	if response == nil || response.Usage == nil {
		return nil
	}
	return map[string]int64{
		"prompt_tokens":     int64(response.Usage.PromptTokens),
		"completion_tokens": int64(response.Usage.CompletionTokens),
		"total_tokens":      int64(response.Usage.TotalTokens),
	}
}

func rlmOverviewTraceCostUSD(trace *agents.ExecutionTrace, tokens map[string]int64) float64 {
	if trace == nil {
		return 0
	}
	return maestrobudget.UsageDeltaFromTokenMap(tokens, trace.ContextMetadata).CostUSD
}

func percentileOverviewLatency(sorted []float64, percentile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if percentile <= 0 {
		return sorted[0]
	}
	if percentile >= 1 {
		return sorted[len(sorted)-1]
	}
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func boolScore(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func intFromMetric(value interface{}) int {
	return int(int64FromMetric(value))
}

func int64FromMetric(value interface{}) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float32:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}

func floatFromMetric(value interface{}) float64 {
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
		parsed, err := typed.Float64()
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}

func containsParseSignal(value string) bool {
	lower := strings.ToLower(value)
	signals := []string{
		"parse error",
		"parse_error",
		"failed to parse",
		"no json object found",
		"invalid json",
		"json parse",
		"json decode",
		"json unmarshal",
		"invalid character",
		"syntax error",
	}
	for _, signal := range signals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func containsFullContextQueryBlock(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "disabled for contexts larger than") ||
		strings.Contains(lower, "use querywith or queryraw") ||
		strings.Contains(lower, "full-context query")
}

func isFinalAnswerTermination(cause string) bool {
	switch strings.TrimSpace(strings.ToLower(cause)) {
	case "final_answer", "state_final", "regex_final", "direct_answer", "finish":
		return true
	default:
		return false
	}
}

func totalOverviewTokens(tokens map[string]int64) int64 {
	if len(tokens) == 0 {
		return 0
	}
	if tokens["total_tokens"] > 0 {
		return tokens["total_tokens"]
	}
	total := tokens["prompt_tokens"] + tokens["completion_tokens"]
	if total > 0 {
		return total
	}
	return tokens["root_total_tokens"] + tokens["sub_total_tokens"] + tokens["subrlm_total_tokens"]
}

func costPerCorrect(cost float64, correct int) float64 {
	if correct <= 0 {
		return 0
	}
	return cost / float64(correct)
}

func rlmOverviewOptimizationAgentType(agent optimize.OptimizableAgent) string {
	if typed, ok := agent.(interface{ OptimizationAgentType() string }); ok {
		if value := strings.TrimSpace(typed.OptimizationAgentType()); value != "" {
			return value
		}
	}
	return RLMOverviewBenchmarkAgentSignature
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
