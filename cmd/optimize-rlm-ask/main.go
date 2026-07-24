package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/optimizers"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
	maestrooptimize "github.com/XiaoConstantine/maestro/internal/optimize"
	"github.com/XiaoConstantine/maestro/internal/orchestration"
	"github.com/XiaoConstantine/maestro/internal/util"
)

type optimizationCheckpoint struct {
	WrittenAt              time.Time                                     `json:"written_at"`
	SuitePath              string                                        `json:"suite_path"`
	ModelID                string                                        `json:"model_id"`
	ArtifactPath           string                                        `json:"artifact_path,omitempty"`
	TrainingExampleCount   int                                           `json:"training_example_count"`
	ValidationExampleCount int                                           `json:"validation_example_count"`
	BaselineValidation     float64                                       `json:"baseline_validation"`
	BaselineEvalErrors     int                                           `json:"baseline_evaluation_errors,omitempty"`
	BestSearch             float64                                       `json:"best_search"`
	BestValidation         float64                                       `json:"best_validation"`
	BestValidationErrors   int                                           `json:"best_validation_errors,omitempty"`
	ReplayValidation       float64                                       `json:"replay_validation"`
	ReplayEvalErrors       int                                           `json:"replay_evaluation_errors,omitempty"`
	ValidationDelta        float64                                       `json:"validation_delta"`
	SearchToReplayGap      float64                                       `json:"search_to_replay_gap"`
	ProtectedDelta         float64                                       `json:"protected_delta"`
	MetricCallCount        int                                           `json:"metric_call_count"`
	CandidateCount         int                                           `json:"candidate_count"`
	ParetoFrontierSize     int                                           `json:"pareto_frontier_size"`
	ArtifactApplySuccess   bool                                          `json:"artifact_apply_success"`
	ArtifactWritten        bool                                          `json:"artifact_written"`
	Decision               string                                        `json:"decision"`
	BestCandidateID        string                                        `json:"best_candidate_id,omitempty"`
	ProtectedGate          *orchestration.RLMOverviewProtectedGateReport `json:"protected_gate,omitempty"`
	ProtectedReport        *orchestration.RLMOverviewBenchmarkRunReport  `json:"protected_report,omitempty"`
	AcceptanceGate         *optimizationAcceptanceGateReport             `json:"acceptance_gate,omitempty"`
	TokenUsage             map[string]int64                              `json:"token_usage,omitempty"`
	BudgetStatus           *maestrobudget.BudgetStatus                   `json:"budget_status,omitempty"`
	ArtifactMetadata       map[string]interface{}                        `json:"artifact_metadata,omitempty"`
	BestArtifacts          optimize.AgentArtifacts                       `json:"best_artifacts,omitempty"`
}

type optimizationAcceptanceGateReport struct {
	Passed                bool     `json:"passed"`
	Decision              string   `json:"decision"`
	Reasons               []string `json:"reasons,omitempty"`
	AllowNoImprovement    bool     `json:"allow_no_improvement,omitempty"`
	MaxSearchToReplayGap  float64  `json:"max_search_to_replay_gap"`
	MaxCostPerCorrectUSD  float64  `json:"max_cost_per_correct_usd,omitempty"`
	ProtectedGateRequired bool     `json:"protected_gate_required"`
	ProtectedGateSkipped  bool     `json:"protected_gate_skipped,omitempty"`
}

type tokenAccountingEvaluator struct {
	base    optimize.AgentEvaluator
	ledger  *tokenLedger
	budget  *maestrobudget.BudgetManager
	agentID string
}

type tokenLedger struct {
	mu     sync.Mutex
	totals map[string]int64
}

const rlmOverviewMinimumGEPAExamples = 16

func main() {
	var (
		suitePath                    string
		outputPath                   string
		markdownOutputPath           string
		artifactPath                 string
		baselinePath                 string
		writeBaselinePath            string
		apiKey                       string
		modelSpec                    string
		modelProvider                string
		modelName                    string
		modelCfg                     string
		traceDir                     string
		workers                      int
		maxAttempts                  int
		maxIterations                int
		maxTokens                    int
		passThreshold                float64
		verbose                      bool
		optimizeRun                  bool
		replayOnly                   bool
		skipProtectedGate            bool
		compareDirect                bool
		allowNoImprovement           bool
		caseTimeout                  time.Duration
		agentTimeout                 time.Duration
		maxRuntime                   time.Duration
		protectedRegressionTolerance float64
		maxCostPerCorrectUSD         float64
		maxSearchToReplayGap         float64
		populationSize               int
		generations                  int
		reflectionFreq               int
		evalConcurrency              int
		validationFreq               int
		searchBatchSize              int
		stagnationLimit              int
		maxMetricCalls               int
		scoreThreshold               float64
		validationSplit              float64
	)

	flag.StringVar(&suitePath, "suite", "./benchmarks/rlm_overview_suite.json", "Path to the RLM overview benchmark suite JSON")
	flag.StringVar(&outputPath, "output", "./benchmark_results/rlm_overview_benchmark.json", "Path to write the benchmark report or GEPA checkpoint JSON")
	flag.StringVar(&markdownOutputPath, "markdown-output", "", "Path to write the companion Markdown report; defaults to --output with .md")
	flag.StringVar(&artifactPath, "artifact", "", "Path to write/read the optimized RLM overview program JSON; defaults to ~/.maestro/rlm_artifacts/overview_optimized_program.json")
	flag.StringVar(&baselinePath, "baseline", "", "Optional versioned baseline JSON for strict protected regression gates")
	flag.StringVar(&writeBaselinePath, "write-baseline", "", "Optional path to write a versioned baseline JSON from this run")
	flag.StringVar(&apiKey, "api-key", "", "API key for external model providers")
	flag.StringVar(&modelSpec, "model", "", `Full model specification (e.g. "anthropic:claude-sonnet-4-6" or "openai:gpt-5.4-mini")`)
	flag.StringVar(&modelProvider, "provider", "anthropic", "Model provider (anthropic, google, openai, ollama, llamacpp)")
	flag.StringVar(&modelName, "model-name", "claude-sonnet-4-6", "Model name")
	flag.StringVar(&modelCfg, "model-config", "", "Additional model configuration")
	flag.StringVar(&traceDir, "trace-dir", "", "Optional directory for dspy-go RLM JSONL traces")
	flag.IntVar(&workers, "workers", 1, "Concurrent benchmark workers")
	flag.IntVar(&maxAttempts, "max-attempts", 1, "Attempts per benchmark case")
	flag.IntVar(&maxIterations, "max-iterations", 0, "Override RLM max iterations; 0 uses Maestro default")
	flag.IntVar(&maxTokens, "max-tokens", 0, "Override RLM max tokens; 0 uses Maestro default")
	flag.Float64Var(&passThreshold, "pass-threshold", orchestration.RLMOverviewBenchmarkDefaultPassThreshold, "Score threshold for informational pass/fail counts")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&optimizeRun, "optimize", false, "Run GEPA optimization instead of benchmark-only replay; default GEPA knobs are smoke-test grade")
	flag.BoolVar(&replayOnly, "replay-only", false, "Replay the optimized program from --artifact without running GEPA")
	flag.BoolVar(&skipProtectedGate, "skip-protected-gate", false, "Allow writing optimized artifacts without a supplied protected baseline")
	flag.BoolVar(&compareDirect, "compare-direct", true, "Run a direct/native baseline on the same benchmark cases and compare quality, tokens, cost, and latency")
	flag.BoolVar(&allowNoImprovement, "allow-no-improvement", false, "Allow GEPA output without replay improvement; the report is marked smoke-tested instead of accepted")
	flag.DurationVar(&caseTimeout, "case-timeout", 0, "Optional timeout per benchmark case")
	flag.DurationVar(&agentTimeout, "agent-timeout", 0, "Override RLM agent timeout; 0 uses Maestro default")
	flag.DurationVar(&maxRuntime, "max-runtime", 0, "Optional wall-clock limit for GEPA optimization; 0 disables it")
	flag.Float64Var(&protectedRegressionTolerance, "protected-regression-tolerance", 0, "Allowed protected-case score regression before failing the run")
	flag.Float64Var(&maxCostPerCorrectUSD, "max-cost-per-correct-usd", 0, "Optional hard gate for benchmark cost per passed case; 0 disables it")
	flag.Float64Var(&maxSearchToReplayGap, "max-search-to-replay-gap", 0.03, "Maximum allowed GEPA best-search to replay-validation score gap before artifact rejection")
	flag.IntVar(&populationSize, "population", 4, "GEPA population size; default is smoke-test grade, use 12+ for production runs")
	flag.IntVar(&generations, "generations", 2, "GEPA generation count; default is smoke-test grade, use 8+ for production runs")
	flag.IntVar(&reflectionFreq, "reflection-freq", 1, "GEPA reflection frequency")
	flag.IntVar(&evalConcurrency, "eval-concurrency", 1, "Concurrent GEPA evaluations")
	flag.IntVar(&validationFreq, "validation-frequency", 1, "Run validation every N GEPA generations")
	flag.IntVar(&searchBatchSize, "search-batch-size", 4, "GEPA search batch size")
	flag.IntVar(&stagnationLimit, "stagnation-limit", 40, "GEPA stagnation limit")
	flag.IntVar(&maxMetricCalls, "max-metric-calls", 0, "Optional cap on GEPA metric evaluations; 0 disables the cap")
	flag.Float64Var(&scoreThreshold, "score-threshold", 0, "Optional early-stop threshold for validation score; 0 disables it")
	flag.Float64Var(&validationSplit, "validation-split", 0.25, "Validation split for the benchmark suite when running GEPA")
	flag.Parse()

	if err := validateRunMode(optimizeRun, replayOnly); err != nil {
		fatalf("%v", err)
	}
	if err := maestrooptimize.ValidateUnitThreshold("pass-threshold", passThreshold); err != nil {
		fatalf("%v", err)
	}
	if maxCostPerCorrectUSD < 0 {
		fatalf("--max-cost-per-correct-usd must be non-negative")
	}
	if maxSearchToReplayGap < 0 {
		fatalf("--max-search-to-replay-gap must be non-negative")
	}

	if modelSpec != "" {
		if provider, name, cfg := util.ParseModelString(modelSpec); provider != "" {
			modelProvider = provider
			modelName = name
			modelCfg = cfg
		}
	}

	ctx := context.Background()
	logger := configureLogger(verbose)
	logging.SetLogger(logger)
	llms.EnsureFactory()

	modelConfig := &util.ModelConfig{
		ModelProvider: modelProvider,
		ModelName:     modelName,
		ModelConfig:   modelCfg,
		APIKey:        apiKey,
	}
	if err := util.ValidateModelConfig(modelConfig); err != nil {
		fatalf("invalid model configuration: %v", err)
	}

	modelID := util.ConstructModelID(modelConfig)
	llm, err := util.LoadLLMFromModelConfig(ctx, modelConfig, modelID)
	if err != nil {
		fatalf("configure LLM: %v", err)
	}
	core.GlobalConfig.DefaultLLM = llm

	cases, err := orchestration.LoadRLMOverviewBenchmarkSuite(suitePath)
	if err != nil {
		fatalf("load benchmark suite: %v", err)
	}

	agentCfg := orchestration.DefaultRLMOverviewBenchmarkAgentConfig()
	if maxIterations > 0 {
		agentCfg.MaxIterations = maxIterations
	}
	if maxTokens > 0 {
		agentCfg.MaxTokens = maxTokens
	}
	if agentTimeout > 0 {
		agentCfg.Timeout = agentTimeout
	}
	if traceDir != "" {
		resolvedTraceDir, err := expandPath(traceDir)
		if err != nil {
			fatalf("resolve trace dir: %v", err)
		}
		agentCfg.TraceDir = resolvedTraceDir
	}

	agent, err := orchestration.NewRLMOverviewBenchmarkAgent(llm, agentCfg)
	if err != nil {
		fatalf("create RLM overview benchmark agent: %v", err)
	}

	var baseline *orchestration.RLMOverviewBenchmarkBaseline
	if strings.TrimSpace(baselinePath) != "" {
		baseline, err = orchestration.LoadRLMOverviewBenchmarkBaseline(baselinePath)
		if err != nil {
			fatalf("load baseline: %v", err)
		}
	}

	if optimizeRun || replayOnly {
		resolvedArtifactPath, err := orchestration.ResolveRLMOverviewOptimizedProgramPath(artifactPath)
		if err != nil {
			fatalf("resolve artifact path: %v", err)
		}
		if err := runOptimization(ctx, runOptimizationRequest{
			modelID:                      modelID,
			suitePath:                    suitePath,
			outputPath:                   outputPath,
			markdownOutputPath:           markdownOutputPath,
			artifactPath:                 resolvedArtifactPath,
			cases:                        cases,
			agent:                        agent,
			llm:                          llm,
			agentCfg:                     agentCfg,
			baseline:                     baseline,
			workers:                      workers,
			caseTimeout:                  caseTimeout,
			maxAttempts:                  maxAttempts,
			passThreshold:                passThreshold,
			protectedRegressionTolerance: protectedRegressionTolerance,
			skipProtectedGate:            skipProtectedGate,
			compareDirect:                compareDirect,
			maxCostPerCorrectUSD:         maxCostPerCorrectUSD,
			maxSearchToReplayGap:         maxSearchToReplayGap,
			allowNoImprovement:           allowNoImprovement,
			replayOnly:                   replayOnly,
			populationSize:               populationSize,
			generations:                  generations,
			reflectionFreq:               reflectionFreq,
			evalConcurrency:              evalConcurrency,
			validationFreq:               validationFreq,
			searchBatchSize:              searchBatchSize,
			stagnationLimit:              stagnationLimit,
			maxMetricCalls:               maxMetricCalls,
			scoreThreshold:               scoreThreshold,
			maxRuntime:                   maxRuntime,
			validationSplit:              validationSplit,
			maxIterationsMutationCeiling: agentCfg.MaxIterations,
			maxTokensMutationCeiling:     agentCfg.MaxTokens,
		}); err != nil {
			if errors.Is(err, errProtectedGateFailed) {
				os.Exit(2)
			}
			fatalf("optimize RLM overview: %v", err)
		}
		return
	}

	report, err := orchestration.RunRLMOverviewBenchmark(ctx, agent, cases, orchestration.RLMOverviewBenchmarkRunConfig{
		Workers:                      workers,
		CaseTimeout:                  caseTimeout,
		MaxAttempts:                  maxAttempts,
		PassThreshold:                passThreshold,
		ProtectedRegressionTolerance: protectedRegressionTolerance,
		Baseline:                     baseline,
		MaxCostPerCorrectUSD:         maxCostPerCorrectUSD,
	})
	if err != nil {
		fatalf("run benchmark: %v", err)
	}
	if compareDirect {
		directReport, err := runDirectBaseline(ctx, llm, agentCfg, cases, orchestration.RLMOverviewBenchmarkRunConfig{
			Workers:              workers,
			CaseTimeout:          caseTimeout,
			MaxAttempts:          maxAttempts,
			PassThreshold:        passThreshold,
			MaxCostPerCorrectUSD: maxCostPerCorrectUSD,
		})
		if err != nil {
			fatalf("run direct baseline: %v", err)
		}
		orchestration.AttachRLMOverviewDirectBaseline(report, directReport)
	}

	if err := writeJSON(outputPath, report); err != nil {
		fatalf("write benchmark report: %v", err)
	}
	markdownPath, err := resolveMarkdownOutputPath(markdownOutputPath, outputPath)
	if err != nil {
		fatalf("resolve markdown report path: %v", err)
	}
	if err := writeBenchmarkMarkdown(markdownPath, modelID, cases, report); err != nil {
		fatalf("write benchmark markdown report: %v", err)
	}

	printSummary(modelID, cases, report, outputPath, markdownPath)

	if report.AcceptanceGate != nil && !report.AcceptanceGate.Passed {
		os.Exit(2)
	}

	if strings.TrimSpace(writeBaselinePath) != "" {
		nextBaseline, err := orchestration.NewRLMOverviewBenchmarkBaseline(report)
		if err != nil {
			fatalf("build baseline: %v", err)
		}
		if err := orchestration.WriteRLMOverviewBenchmarkBaseline(writeBaselinePath, nextBaseline); err != nil {
			fatalf("write baseline: %v", err)
		}
	}
}

func printSummary(modelID core.ModelID, cases []orchestration.RLMOverviewBenchmarkCase, report *orchestration.RLMOverviewBenchmarkRunReport, outputPath, markdownPath string) {
	fmt.Printf("RLM overview benchmark complete\n")
	fmt.Printf("Model:              %s\n", modelID)
	fmt.Printf("Cases:              %d\n", len(cases))
	fmt.Printf("Average score:      %.4f\n", report.AverageScore)
	fmt.Printf("Passed/failed:      %d/%d\n", report.PassedExamples, report.FailedExamples)
	fmt.Printf("Evaluation errors:  %d\n", report.EvaluationErrors)
	if report.ProtectedGate != nil {
		fmt.Printf("Protected gate:     %t\n", report.ProtectedGate.Passed)
	}
	if report.AcceptanceGate != nil {
		fmt.Printf("Decision:           %s\n", report.AcceptanceGate.Decision)
	}
	fmt.Printf("Report:             %s\n", outputPath)
	if strings.TrimSpace(markdownPath) != "" {
		fmt.Printf("Markdown report:    %s\n", markdownPath)
	}
}

var errProtectedGateFailed = errors.New("protected gate failed")

type runOptimizationRequest struct {
	modelID                      core.ModelID
	suitePath                    string
	outputPath                   string
	markdownOutputPath           string
	artifactPath                 string
	cases                        []orchestration.RLMOverviewBenchmarkCase
	agent                        *orchestration.RLMOverviewBenchmarkAgent
	llm                          core.LLM
	agentCfg                     orchestration.RLMOverviewBenchmarkAgentConfig
	baseline                     *orchestration.RLMOverviewBenchmarkBaseline
	workers                      int
	caseTimeout                  time.Duration
	maxAttempts                  int
	passThreshold                float64
	protectedRegressionTolerance float64
	skipProtectedGate            bool
	compareDirect                bool
	maxCostPerCorrectUSD         float64
	maxSearchToReplayGap         float64
	allowNoImprovement           bool
	replayOnly                   bool
	populationSize               int
	generations                  int
	reflectionFreq               int
	evalConcurrency              int
	validationFreq               int
	searchBatchSize              int
	stagnationLimit              int
	maxMetricCalls               int
	scoreThreshold               float64
	maxRuntime                   time.Duration
	validationSplit              float64
	maxIterationsMutationCeiling int
	maxTokensMutationCeiling     int
}

func runOptimization(ctx context.Context, req runOptimizationRequest) error {
	if req.agent == nil {
		return fmt.Errorf("RLM overview benchmark agent is nil")
	}
	if req.replayOnly {
		return replayOptimizedProgram(ctx, req)
	}
	if req.baseline == nil && !req.skipProtectedGate {
		return fmt.Errorf("--baseline is required for GEPA artifact acceptance; pass --skip-protected-gate only for local experiments")
	}

	examples := orchestration.RLMOverviewBenchmarkExamples(req.cases)
	trainingExamples, validationExamples, err := splitAgentExamples(examples, req.validationSplit)
	if err != nil {
		return err
	}

	ledger := newTokenLedger()
	budgetManager := maestrobudget.NewBudgetManager(maestrobudget.DefaultConfig())
	evaluator := &tokenAccountingEvaluator{
		base:    orchestration.NewRLMOverviewBenchmarkEvaluator(orchestration.DefaultRLMOverviewEvaluatorConfig()),
		ledger:  ledger,
		budget:  budgetManager,
		agentID: "ask.rlm_overview",
	}

	workflow, err := optimize.RunGEPAWorkflow(ctx, req.agent, optimize.GEPAWorkflowRequest{
		Evaluator:          evaluator,
		TrainingExamples:   trainingExamples,
		ValidationExamples: validationExamples,
		BaselineExamples:   validationExamples,
		ReplayExamples:     validationExamples,
		PassThreshold:      req.passThreshold,
		// Keep the base agent unmodified; protected-gate replay applies the
		// exported program to a fresh clone before any artifact is accepted.
		ApplyBest: false,
		Config: optimize.GEPAAdapterConfig{
			PopulationSize:      req.populationSize,
			MaxGenerations:      req.generations,
			ReflectionFreq:      req.reflectionFreq,
			SearchBatchSize:     req.searchBatchSize,
			StagnationLimit:     req.stagnationLimit,
			ValidationSplit:     0,
			ValidationFrequency: req.validationFreq,
			EvalConcurrency:     req.evalConcurrency,
			PassThreshold:       req.passThreshold,
			PrimaryArtifact:     optimize.ArtifactRLMOuterPrompt,
			ArtifactKeys: []optimize.ArtifactKey{
				optimize.ArtifactRLMOuterPrompt,
				optimize.ArtifactRLMIterationPrompt,
			},
			IntMutationPlans: rlmOverviewIntMutationPlans(req.maxIterationsMutationCeiling, req.maxTokensMutationCeiling),
			MaxMetricCalls:   req.maxMetricCalls,
			ScoreThreshold:   req.scoreThreshold,
			MaxRuntime:       req.maxRuntime,
		},
	})
	if err != nil {
		return err
	}
	if workflow == nil || workflow.Optimization == nil || workflow.OptimizedProgram == nil {
		return fmt.Errorf("GEPA workflow returned incomplete optimization result")
	}

	metadata := orchestration.NewRLMOverviewOptimizedProgramMetadata(string(req.modelID), req.suitePath, len(trainingExamples), len(validationExamples))
	if workflow.BaselineRun != nil {
		metadata["baseline_validation_score"] = workflow.BaselineRun.AverageScore
	}
	if workflow.ReplayRun != nil {
		metadata["replay_validation_score"] = workflow.ReplayRun.AverageScore
	}
	if workflow.Optimization.BestCandidate != nil {
		metadata["best_candidate_id"] = workflow.Optimization.BestCandidate.ID
	}
	if err := orchestration.AnnotateRLMOverviewOptimizedProgram(workflow.OptimizedProgram, metadata); err != nil {
		return err
	}

	artifactApplySuccess := verifyRLMOverviewProgramApplies(req.agent, workflow.OptimizedProgram)
	protectedReport, protectedGate, err := runProtectedGate(ctx, req, workflow.OptimizedProgram)
	if err != nil {
		return err
	}
	if protectedReport != nil {
		delta := maestrobudget.UsageDeltaFromTokenMap(protectedReport.TokenUsage, nil)
		if !delta.Empty() {
			_ = budgetManager.RecordUsageDelta("ask.rlm_overview.protected_gate", delta)
		}
	}

	checkpoint := buildOptimizationCheckpoint(req, workflow, protectedReport, protectedGate, ledger.snapshot(), budgetManager.Status(), metadata, artifactApplySuccess)
	checkpoint.AcceptanceGate = evaluateOptimizationAcceptance(req, checkpoint)
	checkpoint.Decision = checkpoint.AcceptanceGate.Decision
	if !checkpoint.AcceptanceGate.Passed && checkpoint.Decision == "rejected" {
		if err := writeJSON(req.outputPath, checkpoint); err != nil {
			return err
		}
		if err := writeOptimizationMarkdownForRequest(req, checkpoint); err != nil {
			return err
		}
		printOptimizationSummary(req, checkpoint, false)
		return errProtectedGateFailed
	}

	if err := orchestration.WriteRLMOverviewOptimizedProgram(req.artifactPath, workflow.OptimizedProgram); err != nil {
		return err
	}
	checkpoint.ArtifactWritten = true
	if err := writeJSON(req.outputPath, checkpoint); err != nil {
		return err
	}
	if err := writeOptimizationMarkdownForRequest(req, checkpoint); err != nil {
		return err
	}
	printOptimizationSummary(req, checkpoint, true)
	return nil
}

func replayOptimizedProgram(ctx context.Context, req runOptimizationRequest) error {
	program, _, err := orchestration.LoadRLMOverviewOptimizedProgram(req.artifactPath)
	if err != nil {
		return err
	}
	if program == nil {
		return fmt.Errorf("optimized RLM overview artifact not found: %s", req.artifactPath)
	}
	if err := orchestration.ApplyRLMOverviewOptimizedProgram(req.agent, program); err != nil {
		return err
	}
	report, err := orchestration.RunRLMOverviewBenchmark(ctx, req.agent, req.cases, orchestration.RLMOverviewBenchmarkRunConfig{
		Workers:                      req.workers,
		CaseTimeout:                  req.caseTimeout,
		MaxAttempts:                  req.maxAttempts,
		PassThreshold:                req.passThreshold,
		ProtectedRegressionTolerance: req.protectedRegressionTolerance,
		Baseline:                     req.baseline,
		MaxCostPerCorrectUSD:         req.maxCostPerCorrectUSD,
	})
	if err != nil {
		return err
	}
	if req.compareDirect {
		directReport, err := runDirectBaseline(ctx, req.llm, req.agentCfg, req.cases, orchestration.RLMOverviewBenchmarkRunConfig{
			Workers:              req.workers,
			CaseTimeout:          req.caseTimeout,
			MaxAttempts:          req.maxAttempts,
			PassThreshold:        req.passThreshold,
			MaxCostPerCorrectUSD: req.maxCostPerCorrectUSD,
		})
		if err != nil {
			return err
		}
		orchestration.AttachRLMOverviewDirectBaseline(report, directReport)
	}
	if err := writeJSON(req.outputPath, report); err != nil {
		return err
	}
	markdownPath, err := resolveMarkdownOutputPath(req.markdownOutputPath, req.outputPath)
	if err != nil {
		return err
	}
	if err := writeBenchmarkMarkdown(markdownPath, req.modelID, req.cases, report); err != nil {
		return err
	}
	printSummary(req.modelID, req.cases, report, req.outputPath, markdownPath)
	if report.AcceptanceGate != nil && !report.AcceptanceGate.Passed {
		return errProtectedGateFailed
	}
	return nil
}

func runProtectedGate(ctx context.Context, req runOptimizationRequest, program *optimize.OptimizedAgentProgram) (*orchestration.RLMOverviewBenchmarkRunReport, *orchestration.RLMOverviewProtectedGateReport, error) {
	if req.baseline == nil {
		return nil, nil, nil
	}
	candidateAgent, err := req.agent.Clone()
	if err != nil {
		return nil, nil, fmt.Errorf("clone optimized RLM overview agent for protected gate: %w", err)
	}
	if err := orchestration.ApplyRLMOverviewOptimizedProgram(candidateAgent, program); err != nil {
		return nil, nil, err
	}
	report, err := orchestration.RunRLMOverviewBenchmark(ctx, candidateAgent, req.cases, orchestration.RLMOverviewBenchmarkRunConfig{
		Workers:                      req.workers,
		CaseTimeout:                  req.caseTimeout,
		MaxAttempts:                  req.maxAttempts,
		PassThreshold:                req.passThreshold,
		ProtectedRegressionTolerance: req.protectedRegressionTolerance,
		Baseline:                     req.baseline,
		MaxCostPerCorrectUSD:         req.maxCostPerCorrectUSD,
	})
	if err != nil {
		return nil, nil, err
	}
	return report, report.ProtectedGate, nil
}

func buildOptimizationCheckpoint(req runOptimizationRequest, workflow *optimize.GEPAWorkflowResult, protectedReport *orchestration.RLMOverviewBenchmarkRunReport, protectedGate *orchestration.RLMOverviewProtectedGateReport, tokenUsage map[string]int64, budgetStatus maestrobudget.BudgetStatus, artifactMetadata map[string]interface{}, artifactApplySuccess bool) optimizationCheckpoint {
	if protectedReport != nil {
		tokenUsage = combineTokenUsage(tokenUsage, protectedReport.TokenUsage)
	}
	checkpoint := optimizationCheckpoint{
		WrittenAt:            time.Now().UTC(),
		SuitePath:            req.suitePath,
		ModelID:              string(req.modelID),
		ArtifactPath:         req.artifactPath,
		ProtectedGate:        protectedGate,
		ProtectedReport:      protectedReport,
		TokenUsage:           tokenUsage,
		BudgetStatus:         &budgetStatus,
		ArtifactMetadata:     artifactMetadata,
		ArtifactApplySuccess: artifactApplySuccess,
	}
	if workflow.Optimization != nil {
		checkpoint.TrainingExampleCount = workflow.Optimization.TrainingExampleCount
		checkpoint.ValidationExampleCount = workflow.Optimization.ValidationExampleCount
		checkpoint.BestArtifacts = workflow.Optimization.BestArtifacts.Clone()
		if workflow.Optimization.BestCandidate != nil {
			checkpoint.BestCandidateID = workflow.Optimization.BestCandidate.ID
		}
		if workflow.Optimization.OptimizationState != nil {
			state := workflow.Optimization.OptimizationState
			checkpoint.BestSearch = state.BestFitness
			checkpoint.MetricCallCount = state.MetricCallCount()
			checkpoint.CandidateCount = gepaCandidateCount(state.PopulationHistory)
			checkpoint.ParetoFrontierSize = len(state.GetParetoArchive())
		}
	}
	if workflow.BaselineRun != nil {
		checkpoint.BaselineValidation = workflow.BaselineRun.AverageScore
		checkpoint.BaselineEvalErrors = workflow.BaselineRun.EvaluationErrors
	}
	if workflow.ReplayRun != nil {
		checkpoint.ReplayValidation = workflow.ReplayRun.AverageScore
		checkpoint.ReplayEvalErrors = workflow.ReplayRun.EvaluationErrors
		checkpoint.BestValidation = workflow.ReplayRun.AverageScore
	}
	// Prefer GEPA's explicit best validation evaluation over replay, and replay
	// over the zero value when validation was not run.
	if workflow.Optimization != nil && workflow.Optimization.BestValidationEvaluation != nil {
		checkpoint.BestValidation = workflow.Optimization.BestValidationEvaluation.AverageScore
		if workflow.Optimization.BestValidationEvaluation.Run != nil {
			checkpoint.BestValidationErrors = workflow.Optimization.BestValidationEvaluation.Run.EvaluationErrors
		}
	}
	if checkpoint.BestValidation == 0 {
		checkpoint.BestValidation = checkpoint.ReplayValidation
	}
	checkpoint.ValidationDelta = checkpoint.ReplayValidation - checkpoint.BaselineValidation
	checkpoint.SearchToReplayGap = checkpoint.BestSearch - checkpoint.ReplayValidation
	if checkpoint.SearchToReplayGap < 0 {
		checkpoint.SearchToReplayGap = 0
	}
	checkpoint.ProtectedDelta = protectedScoreDelta(protectedReport, req.baseline)
	return checkpoint
}

func evaluateOptimizationAcceptance(req runOptimizationRequest, checkpoint optimizationCheckpoint) *optimizationAcceptanceGateReport {
	gate := &optimizationAcceptanceGateReport{
		Passed:                true,
		Decision:              "accepted",
		AllowNoImprovement:    req.allowNoImprovement,
		MaxSearchToReplayGap:  req.maxSearchToReplayGap,
		MaxCostPerCorrectUSD:  req.maxCostPerCorrectUSD,
		ProtectedGateRequired: !req.skipProtectedGate,
		ProtectedGateSkipped:  req.baseline == nil,
	}
	if !checkpoint.ArtifactApplySuccess {
		gate.Reasons = append(gate.Reasons, "artifact_apply_success=false")
	}
	if checkpoint.ReplayValidation == 0 {
		gate.Reasons = append(gate.Reasons, "replay_validation_score is zero or missing")
	}
	if checkpoint.BaselineEvalErrors > 0 {
		gate.Reasons = append(gate.Reasons, fmt.Sprintf("baseline_evaluation_errors=%d", checkpoint.BaselineEvalErrors))
	}
	if checkpoint.BestValidationErrors > 0 {
		gate.Reasons = append(gate.Reasons, fmt.Sprintf("best_validation_errors=%d", checkpoint.BestValidationErrors))
	}
	if checkpoint.ReplayEvalErrors > 0 {
		gate.Reasons = append(gate.Reasons, fmt.Sprintf("replay_evaluation_errors=%d", checkpoint.ReplayEvalErrors))
	}
	if checkpoint.ValidationDelta <= 0 {
		reason := fmt.Sprintf("validation_delta %.4f is not positive", checkpoint.ValidationDelta)
		if req.allowNoImprovement {
			gate.Decision = "smoke_tested"
		} else {
			gate.Reasons = append(gate.Reasons, reason)
		}
	}
	if checkpoint.SearchToReplayGap > req.maxSearchToReplayGap {
		gate.Reasons = append(gate.Reasons, fmt.Sprintf("search_to_replay_gap %.4f exceeds %.4f", checkpoint.SearchToReplayGap, req.maxSearchToReplayGap))
	}
	if checkpoint.ProtectedGate != nil && !checkpoint.ProtectedGate.Passed {
		gate.Reasons = append(gate.Reasons, "protected gate failed")
	}
	if checkpoint.ProtectedReport != nil && checkpoint.ProtectedReport.AcceptanceGate != nil && !checkpoint.ProtectedReport.AcceptanceGate.Passed {
		gate.Reasons = append(gate.Reasons, "protected replay acceptance gate failed")
	}
	if req.baseline == nil {
		gate.Decision = "smoke_tested"
		if !req.skipProtectedGate {
			gate.Reasons = append(gate.Reasons, "protected baseline missing")
		}
	}
	if req.maxCostPerCorrectUSD > 0 && checkpoint.ProtectedReport != nil {
		costPerCorrect := 0.0
		if checkpoint.ProtectedReport.PassedExamples > 0 {
			costPerCorrect = checkpoint.ProtectedReport.CostUSD / float64(checkpoint.ProtectedReport.PassedExamples)
		}
		if costPerCorrect > req.maxCostPerCorrectUSD {
			gate.Reasons = append(gate.Reasons, fmt.Sprintf("protected cost_per_correct_usd %.6f exceeds %.6f", costPerCorrect, req.maxCostPerCorrectUSD))
		}
	}
	if len(gate.Reasons) > 0 {
		gate.Passed = false
		gate.Decision = "rejected"
	}
	if gate.Decision == "smoke_tested" {
		gate.Passed = true
	}
	return gate
}

func verifyRLMOverviewProgramApplies(agent *orchestration.RLMOverviewBenchmarkAgent, program *optimize.OptimizedAgentProgram) bool {
	if agent == nil || program == nil {
		return false
	}
	cloned, err := agent.Clone()
	if err != nil {
		return false
	}
	return orchestration.ApplyRLMOverviewOptimizedProgram(cloned, program) == nil
}

func gepaCandidateCount(populations []*optimizers.Population) int {
	seen := make(map[string]bool)
	for _, population := range populations {
		if population == nil {
			continue
		}
		for _, candidate := range population.Candidates {
			if candidate == nil {
				continue
			}
			if strings.TrimSpace(candidate.ID) != "" {
				seen[candidate.ID] = true
			}
		}
	}
	return len(seen)
}

func protectedScoreDelta(report *orchestration.RLMOverviewBenchmarkRunReport, baseline *orchestration.RLMOverviewBenchmarkBaseline) float64 {
	if report == nil || baseline == nil {
		return 0
	}
	var currentTotal float64
	var baselineTotal float64
	var count int
	for _, result := range report.Results {
		if !result.Protected {
			continue
		}
		base, ok := baseline.Scores[result.CaseID]
		if !ok {
			continue
		}
		currentTotal += result.Score
		baselineTotal += base.Score
		count++
	}
	if count == 0 {
		return 0
	}
	return currentTotal/float64(count) - baselineTotal/float64(count)
}

func combineTokenUsage(a, b map[string]int64) map[string]int64 {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	merged := make(map[string]int64, len(a)+len(b))
	for key, value := range a {
		merged[key] = value
	}
	for key, value := range b {
		merged[key] += value
	}
	return merged
}

func printOptimizationSummary(req runOptimizationRequest, checkpoint optimizationCheckpoint, artifactWritten bool) {
	fmt.Printf("RLM overview GEPA optimization complete\n")
	fmt.Printf("Model:                 %s\n", req.modelID)
	fmt.Printf("Training examples:     %d\n", checkpoint.TrainingExampleCount)
	fmt.Printf("Validation examples:   %d\n", checkpoint.ValidationExampleCount)
	fmt.Printf("Baseline validation:   %.4f\n", checkpoint.BaselineValidation)
	fmt.Printf("Best search:           %.4f\n", checkpoint.BestSearch)
	fmt.Printf("Best validation:       %.4f\n", checkpoint.BestValidation)
	fmt.Printf("Replay validation:     %.4f\n", checkpoint.ReplayValidation)
	fmt.Printf("Validation delta:      %.4f\n", checkpoint.ValidationDelta)
	fmt.Printf("Search/replay gap:     %.4f\n", checkpoint.SearchToReplayGap)
	if checkpoint.ProtectedGate != nil {
		fmt.Printf("Protected gate:        %t\n", checkpoint.ProtectedGate.Passed)
	}
	if checkpoint.AcceptanceGate != nil {
		fmt.Printf("Decision:              %s\n", checkpoint.AcceptanceGate.Decision)
	}
	fmt.Printf("Artifact written:      %t\n", artifactWritten)
	if artifactWritten {
		fmt.Printf("Artifact:              %s\n", req.artifactPath)
	}
	fmt.Printf("Checkpoint:            %s\n", req.outputPath)
	if markdownPath, err := resolveMarkdownOutputPath(req.markdownOutputPath, req.outputPath); err == nil {
		fmt.Printf("Markdown report:       %s\n", markdownPath)
	}
}

func rlmOverviewIntMutationPlans(maxIterations, maxTokens int) map[string]optimize.IntMutationConfig {
	plans := make(map[string]optimize.IntMutationConfig)
	if maxIterations > 1 {
		plans[agentrlm.ArtifactMaxIterations] = optimize.IntMutationConfig{Min: 1, Max: maxIterations, Step: 1}
	}
	if maxTokens > 0 {
		step := maxTokens / 5
		if step < 1000 {
			step = 1000
		}
		plans[agentrlm.ArtifactMaxTokens] = optimize.IntMutationConfig{Min: step, Max: maxTokens, Step: step}
	}
	return plans
}

func splitAgentExamples(examples []optimize.AgentExample, validationSplit float64) ([]optimize.AgentExample, []optimize.AgentExample, error) {
	return maestrooptimize.SplitAgentExamples(examples, validationSplit, rlmOverviewMinimumGEPAExamples)
}

func runDirectBaseline(ctx context.Context, llm core.LLM, agentCfg orchestration.RLMOverviewBenchmarkAgentConfig, cases []orchestration.RLMOverviewBenchmarkCase, cfg orchestration.RLMOverviewBenchmarkRunConfig) (*orchestration.RLMOverviewBenchmarkRunReport, error) {
	directAgent, err := orchestration.NewRLMOverviewDirectBenchmarkAgent(llm, agentCfg)
	if err != nil {
		return nil, err
	}
	cfg.Baseline = nil
	return orchestration.RunRLMOverviewBenchmark(ctx, directAgent, cases, cfg)
}

func validateRunMode(optimizeRun, replayOnly bool) error {
	if optimizeRun && replayOnly {
		return fmt.Errorf("--optimize and --replay-only are mutually exclusive")
	}
	return nil
}

func newTokenLedger() *tokenLedger {
	return &tokenLedger{totals: make(map[string]int64)}
}

func (e *tokenAccountingEvaluator) Evaluate(ctx context.Context, agent optimize.OptimizableAgent, ex optimize.AgentExample) (*optimize.EvalResult, error) {
	result, err := e.base.Evaluate(ctx, agent, ex)
	if result != nil && result.SideInfo != nil && e.ledger != nil {
		e.ledger.record(result.SideInfo.Tokens)
	}
	if result != nil && result.SideInfo != nil && e.budget != nil {
		delta := maestrobudget.UsageDeltaFromTokenMap(result.SideInfo.Tokens, result.SideInfo.Diagnostics)
		if delta.CostUSD == 0 {
			delta.CostUSD = result.SideInfo.Cost
		}
		if !delta.Empty() {
			_ = e.budget.RecordUsageDelta(e.agentID, delta)
		}
	}
	return result, err
}

func (l *tokenLedger) record(tokens map[string]int64) {
	if l == nil || len(tokens) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.totals == nil {
		l.totals = make(map[string]int64)
	}
	for key, value := range tokens {
		l.totals[key] += value
	}
}

func (l *tokenLedger) snapshot() map[string]int64 {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.totals) == 0 {
		return nil
	}
	out := make(map[string]int64, len(l.totals))
	for key, value := range l.totals {
		out[key] = value
	}
	return out
}

func configureLogger(verbose bool) *logging.Logger {
	logLevel := logging.INFO
	if verbose {
		logLevel = logging.DEBUG
	}
	consoleOutput := logging.NewConsoleOutput(true, logging.WithColor(true))
	return logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  []logging.Output{consoleOutput},
	})
}

func expandPath(path string) (string, error) {
	path = strings.TrimSpace(os.ExpandEnv(path))
	if path == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Clean(path), nil
}

func resolveMarkdownOutputPath(markdownPath, jsonPath string) (string, error) {
	if strings.TrimSpace(markdownPath) != "" {
		return expandPath(markdownPath)
	}
	resolvedJSONPath, err := expandPath(jsonPath)
	if err != nil {
		return "", err
	}
	ext := filepath.Ext(resolvedJSONPath)
	if ext == "" {
		return resolvedJSONPath + ".md", nil
	}
	return strings.TrimSuffix(resolvedJSONPath, ext) + ".md", nil
}

func writeJSON(path string, value any) error {
	resolvedPath, err := expandPath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	if err := os.WriteFile(resolvedPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write JSON %q: %w", resolvedPath, err)
	}
	return nil
}

func writeBenchmarkMarkdown(path string, modelID core.ModelID, cases []orchestration.RLMOverviewBenchmarkCase, report *orchestration.RLMOverviewBenchmarkRunReport) error {
	if report == nil {
		return fmt.Errorf("benchmark report is nil")
	}
	var builder strings.Builder
	decision := "unknown"
	if report.AcceptanceGate != nil {
		decision = report.AcceptanceGate.Decision
	}
	builder.WriteString("# RLM Overview Benchmark Report\n\n")
	fmt.Fprintf(&builder, "- Decision: %s\n", decision)
	fmt.Fprintf(&builder, "- Model: %s\n", modelID)
	fmt.Fprintf(&builder, "- Cases: %d\n", len(cases))
	fmt.Fprintf(&builder, "- Average score: %.4f\n", report.AverageScore)
	fmt.Fprintf(&builder, "- Passed/failed/errors: %d/%d/%d\n", report.PassedExamples, report.FailedExamples, report.EvaluationErrors)
	fmt.Fprintf(&builder, "- Total tokens: %d\n", totalTokens(report.TokenUsage))
	fmt.Fprintf(&builder, "- Cost USD: %.6f\n", report.CostUSD)
	fmt.Fprintf(&builder, "- Average latency ms: %.2f\n\n", report.LatencyMS.Average)

	builder.WriteString("## Why\n\n")
	if report.AcceptanceGate != nil && len(report.AcceptanceGate.Reasons) > 0 {
		for _, reason := range report.AcceptanceGate.Reasons {
			fmt.Fprintf(&builder, "- %s\n", reason)
		}
	} else {
		builder.WriteString("- All configured benchmark promotion gates passed.\n")
	}
	builder.WriteString("\n## Quality Metrics\n\n")
	builder.WriteString("| Metric | Value |\n| --- | ---: |\n")
	fmt.Fprintf(&builder, "| exact_grounding_score | %.4f |\n", report.AverageQuality.ExactGroundingScore)
	fmt.Fprintf(&builder, "| semantic_quality_score | %.4f |\n", report.AverageQuality.SemanticQualityScore)
	fmt.Fprintf(&builder, "| fact_recall | %.4f |\n", report.AverageQuality.FactRecall)
	fmt.Fprintf(&builder, "| fact_precision | %.4f |\n", report.AverageQuality.FactPrecision)
	fmt.Fprintf(&builder, "| source_recall | %.4f |\n", report.AverageQuality.SourceRecall)
	fmt.Fprintf(&builder, "| source_precision | %.4f |\n", report.AverageQuality.SourcePrecision)
	fmt.Fprintf(&builder, "| semantic_fact_recall | %.4f |\n", report.AverageQuality.SemanticFactRecall)
	fmt.Fprintf(&builder, "| semantic_source_recall | %.4f |\n", report.AverageQuality.SemanticSourceRecall)
	fmt.Fprintf(&builder, "| evidence_fact_coverage | %.4f |\n", report.AverageQuality.EvidenceFactCoverage)
	fmt.Fprintf(&builder, "| evidence_source_coverage | %.4f |\n", report.AverageQuality.EvidenceSourceCoverage)
	fmt.Fprintf(&builder, "| repo_fact_coverage | %.4f |\n", report.AverageQuality.RepoFactCoverage)
	fmt.Fprintf(&builder, "| repo_source_coverage | %.4f |\n", report.AverageQuality.RepoSourceCoverage)
	fmt.Fprintf(&builder, "| manifest_source_coverage | %.4f |\n", report.AverageQuality.ManifestSourceCoverage)
	fmt.Fprintf(&builder, "| schema_valid_rate | %.4f |\n", report.AverageQuality.SchemaValidRate)
	fmt.Fprintf(&builder, "| terseness | %.4f |\n\n", report.AverageQuality.Terseness)

	builder.WriteString("## Failure Classification\n\n")
	if len(report.FailureClasses) > 0 {
		builder.WriteString("| Class | Count |\n| --- | ---: |\n")
		for _, class := range []string{"context_missing", "semantic_match", "answer_missing", "real_behavior_failure"} {
			if count := report.FailureClasses[class]; count > 0 {
				fmt.Fprintf(&builder, "| %s | %d |\n", class, count)
			}
		}
		builder.WriteString("\n")
	} else {
		builder.WriteString("No failed cases were classified.\n\n")
	}

	builder.WriteString("## RLM Behavior Metrics\n\n")
	builder.WriteString("| Metric | Value |\n| --- | ---: |\n")
	fmt.Fprintf(&builder, "| root_prompt_max_tokens | %d |\n", report.RLMMetrics.RootPromptMaxTokens)
	fmt.Fprintf(&builder, "| root_prompt_mean_tokens | %d |\n", report.RLMMetrics.RootPromptMeanTokens)
	fmt.Fprintf(&builder, "| full_context_query_success_count | %d |\n", report.RLMMetrics.FullContextQuerySuccessCount)
	fmt.Fprintf(&builder, "| full_context_query_block_count | %d |\n", report.RLMMetrics.FullContextQueryBlockCount)
	fmt.Fprintf(&builder, "| slice_query_ratio | %.4f |\n", report.RLMMetrics.SliceQueryRatio)
	fmt.Fprintf(&builder, "| subcall_useful_ratio | %.4f |\n", report.RLMMetrics.SubcallUsefulRatio)
	fmt.Fprintf(&builder, "| query_action_count | %d |\n", report.RLMMetrics.QueryActionCount)
	fmt.Fprintf(&builder, "| query_action_success_count | %d |\n", report.RLMMetrics.QueryActionSuccessCount)
	fmt.Fprintf(&builder, "| no_op_iteration_count | %d |\n", report.RLMMetrics.NoOpIterationCount)
	fmt.Fprintf(&builder, "| parse_error_count | %d |\n", report.RLMMetrics.ParseErrorCount)
	fmt.Fprintf(&builder, "| final_answer_rate | %.4f |\n", report.RLMMetrics.FinalAnswerRate)
	fmt.Fprintf(&builder, "| termination_cause | %s |\n\n", report.RLMMetrics.TerminationCause)
	if len(report.RLMMetrics.QueryModeCounts) > 0 {
		builder.WriteString("Query modes:\n\n")
		builder.WriteString("| Mode | Count |\n| --- | ---: |\n")
		for _, mode := range []string{"query_with", "query_raw", "query_batched", "query_batched_raw", "query_async", "query_unknown"} {
			if count := report.RLMMetrics.QueryModeCounts[mode]; count > 0 {
				fmt.Fprintf(&builder, "| %s | %d |\n", mode, count)
			}
		}
		builder.WriteString("\n")
	}

	builder.WriteString("## Ablations\n\n")
	if report.Ablations != nil {
		ab := report.Ablations
		builder.WriteString("| Ablation | Current | Alternative | Delta |\n| --- | ---: | ---: | ---: |\n")
		fmt.Fprintf(&builder, "| exact_vs_semantic_score | %.4f | %.4f | %.4f |\n", ab.ExactGroundingAverage, ab.SemanticQualityAverage, ab.SemanticQualityDelta)
		fmt.Fprintf(&builder, "| current_vs_richer_manifest_fact_coverage | %.4f | %.4f | %.4f |\n", ab.CurrentManifestFactCoverage, ab.RicherManifestFactCoverage, ab.ManifestFactCoverageDelta)
		fmt.Fprintf(&builder, "| current_vs_richer_manifest_source_coverage | %.4f | %.4f | %.4f |\n", ab.CurrentManifestSourceCoverage, ab.RicherManifestSourceCoverage, ab.ManifestSourceCoverageDelta)
		builder.WriteString("\n")
		fmt.Fprintf(&builder, "- Semantic rescued cases: %d\n", ab.SemanticRescuedCases)
		fmt.Fprintf(&builder, "- Context-missing cases: %d\n\n", ab.ContextMissingCases)
	} else {
		builder.WriteString("Ablation summary was not available.\n\n")
	}

	builder.WriteString("## Direct Baseline\n\n")
	if report.BaselineComparison != nil {
		cmp := report.BaselineComparison
		builder.WriteString("| Metric | RLM | Direct | Delta |\n| --- | ---: | ---: | ---: |\n")
		fmt.Fprintf(&builder, "| average_score | %.4f | %.4f | %.4f |\n", cmp.RLMAverageScore, cmp.DirectAverageScore, cmp.QualityDelta)
		fmt.Fprintf(&builder, "| exact_grounding_score | %.4f | %.4f | %.4f |\n", cmp.RLMExactGroundingScore, cmp.DirectExactGroundingScore, cmp.ExactGroundingDelta)
		fmt.Fprintf(&builder, "| semantic_quality_score | %.4f | %.4f | %.4f |\n", cmp.RLMSemanticQualityScore, cmp.DirectSemanticQualityScore, cmp.SemanticQualityDelta)
		fmt.Fprintf(&builder, "| total_tokens | %d | %d | %d |\n", cmp.RLMTokens, cmp.DirectTokens, cmp.TokenDelta)
		fmt.Fprintf(&builder, "| average_latency_ms | %.2f | %.2f | %.2f |\n", cmp.RLMLatencyAverageMS, cmp.DirectLatencyAverageMS, cmp.RLMLatencyAverageMS-cmp.DirectLatencyAverageMS)
		fmt.Fprintf(&builder, "| cost_per_correct_usd | %.6f | %.6f | %.6f |\n", cmp.RLMCostPerCorrect, cmp.DirectCostPerCorrect, cmp.RLMCostPerCorrect-cmp.DirectCostPerCorrect)
	} else {
		builder.WriteString("Direct baseline comparison was not run.\n")
	}
	builder.WriteString("\n## Protected Gate\n\n")
	if report.ProtectedGate != nil {
		fmt.Fprintf(&builder, "- Passed: %t\n", report.ProtectedGate.Passed)
		fmt.Fprintf(&builder, "- Protected cases: %d\n", len(report.ProtectedGate.ProtectedCaseIDs))
		fmt.Fprintf(&builder, "- Regressions: %d\n", len(report.ProtectedGate.Regressions))
		fmt.Fprintf(&builder, "- Missing baseline cases: %d\n", len(report.ProtectedGate.MissingBaseline))
	} else {
		builder.WriteString("Protected gate was not configured.\n")
	}
	return writeMarkdown(path, builder.String())
}

func writeOptimizationMarkdownForRequest(req runOptimizationRequest, checkpoint optimizationCheckpoint) error {
	path, err := resolveMarkdownOutputPath(req.markdownOutputPath, req.outputPath)
	if err != nil {
		return err
	}
	return writeOptimizationMarkdown(path, req, checkpoint)
}

func writeOptimizationMarkdown(path string, req runOptimizationRequest, checkpoint optimizationCheckpoint) error {
	var builder strings.Builder
	builder.WriteString("# RLM Overview GEPA Report\n\n")
	fmt.Fprintf(&builder, "- Decision: %s\n", checkpoint.Decision)
	fmt.Fprintf(&builder, "- Model: %s\n", req.modelID)
	fmt.Fprintf(&builder, "- Training examples: %d\n", checkpoint.TrainingExampleCount)
	fmt.Fprintf(&builder, "- Validation examples: %d\n", checkpoint.ValidationExampleCount)
	fmt.Fprintf(&builder, "- Artifact apply success: %t\n", checkpoint.ArtifactApplySuccess)
	fmt.Fprintf(&builder, "- Artifact written: %t\n\n", checkpoint.ArtifactWritten)

	builder.WriteString("## Why\n\n")
	if checkpoint.AcceptanceGate != nil && len(checkpoint.AcceptanceGate.Reasons) > 0 {
		for _, reason := range checkpoint.AcceptanceGate.Reasons {
			fmt.Fprintf(&builder, "- %s\n", reason)
		}
	} else if checkpoint.AcceptanceGate != nil && checkpoint.AcceptanceGate.Decision == "smoke_tested" {
		builder.WriteString("- The run is smoke-tested because a required production gate was intentionally skipped or no-improvement mode was enabled.\n")
	} else {
		builder.WriteString("- All configured GEPA promotion gates passed.\n")
	}
	builder.WriteString("\n## GEPA Validation\n\n")
	builder.WriteString("| Metric | Value |\n| --- | ---: |\n")
	fmt.Fprintf(&builder, "| baseline_validation | %.4f |\n", checkpoint.BaselineValidation)
	fmt.Fprintf(&builder, "| baseline_evaluation_errors | %d |\n", checkpoint.BaselineEvalErrors)
	fmt.Fprintf(&builder, "| best_search | %.4f |\n", checkpoint.BestSearch)
	fmt.Fprintf(&builder, "| best_validation | %.4f |\n", checkpoint.BestValidation)
	fmt.Fprintf(&builder, "| best_validation_errors | %d |\n", checkpoint.BestValidationErrors)
	fmt.Fprintf(&builder, "| replay_validation | %.4f |\n", checkpoint.ReplayValidation)
	fmt.Fprintf(&builder, "| replay_evaluation_errors | %d |\n", checkpoint.ReplayEvalErrors)
	fmt.Fprintf(&builder, "| validation_delta | %.4f |\n", checkpoint.ValidationDelta)
	fmt.Fprintf(&builder, "| search_to_replay_gap | %.4f |\n", checkpoint.SearchToReplayGap)
	fmt.Fprintf(&builder, "| protected_delta | %.4f |\n", checkpoint.ProtectedDelta)
	fmt.Fprintf(&builder, "| metric_call_count | %d |\n", checkpoint.MetricCallCount)
	fmt.Fprintf(&builder, "| candidate_count | %d |\n", checkpoint.CandidateCount)
	fmt.Fprintf(&builder, "| pareto_frontier_size | %d |\n\n", checkpoint.ParetoFrontierSize)

	builder.WriteString("## Protected Replay\n\n")
	if checkpoint.ProtectedReport != nil {
		fmt.Fprintf(&builder, "- Average score: %.4f\n", checkpoint.ProtectedReport.AverageScore)
		fmt.Fprintf(&builder, "- Final answer rate: %.4f\n", checkpoint.ProtectedReport.RLMMetrics.FinalAnswerRate)
		fmt.Fprintf(&builder, "- Parse errors: %d\n", checkpoint.ProtectedReport.RLMMetrics.ParseErrorCount)
		fmt.Fprintf(&builder, "- Full-context query successes: %d\n", checkpoint.ProtectedReport.RLMMetrics.FullContextQuerySuccessCount)
		if checkpoint.ProtectedReport.AcceptanceGate != nil {
			fmt.Fprintf(&builder, "- Replay decision: %s\n", checkpoint.ProtectedReport.AcceptanceGate.Decision)
		}
	} else {
		builder.WriteString("Protected replay was not run.\n")
	}
	return writeMarkdown(path, builder.String())
}

func writeMarkdown(path, content string) error {
	resolvedPath, err := expandPath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return fmt.Errorf("create markdown output directory: %w", err)
	}
	if err := os.WriteFile(resolvedPath, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write markdown %q: %w", resolvedPath, err)
	}
	return nil
}

func totalTokens(tokens map[string]int64) int64 {
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

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
