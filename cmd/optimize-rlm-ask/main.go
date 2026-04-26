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
	BaselineValidation     float64                                       `json:"baseline_validation_score"`
	BestValidation         float64                                       `json:"best_validation_score"`
	ReplayValidation       float64                                       `json:"replay_validation_score"`
	BestCandidateID        string                                        `json:"best_candidate_id,omitempty"`
	ProtectedGate          *orchestration.RLMOverviewProtectedGateReport `json:"protected_gate,omitempty"`
	ProtectedReport        *orchestration.RLMOverviewBenchmarkRunReport  `json:"protected_report,omitempty"`
	TokenUsage             map[string]int64                              `json:"token_usage,omitempty"`
	BudgetStatus           *maestrobudget.BudgetStatus                   `json:"budget_status,omitempty"`
	ArtifactMetadata       map[string]interface{}                        `json:"artifact_metadata,omitempty"`
	BestArtifacts          optimize.AgentArtifacts                       `json:"best_artifacts,omitempty"`
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
		caseTimeout                  time.Duration
		agentTimeout                 time.Duration
		maxRuntime                   time.Duration
		protectedRegressionTolerance float64
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
	flag.DurationVar(&caseTimeout, "case-timeout", 0, "Optional timeout per benchmark case")
	flag.DurationVar(&agentTimeout, "agent-timeout", 0, "Override RLM agent timeout; 0 uses Maestro default")
	flag.DurationVar(&maxRuntime, "max-runtime", 0, "Optional wall-clock limit for GEPA optimization; 0 disables it")
	flag.Float64Var(&protectedRegressionTolerance, "protected-regression-tolerance", 0, "Allowed protected-case score regression before failing the run")
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
			artifactPath:                 resolvedArtifactPath,
			cases:                        cases,
			agent:                        agent,
			baseline:                     baseline,
			workers:                      workers,
			caseTimeout:                  caseTimeout,
			maxAttempts:                  maxAttempts,
			passThreshold:                passThreshold,
			protectedRegressionTolerance: protectedRegressionTolerance,
			skipProtectedGate:            skipProtectedGate,
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
	})
	if err != nil {
		fatalf("run benchmark: %v", err)
	}

	if err := writeJSON(outputPath, report); err != nil {
		fatalf("write benchmark report: %v", err)
	}

	printSummary(modelID, cases, report, outputPath)

	if report.ProtectedGate != nil && !report.ProtectedGate.Passed {
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

func printSummary(modelID core.ModelID, cases []orchestration.RLMOverviewBenchmarkCase, report *orchestration.RLMOverviewBenchmarkRunReport, outputPath string) {
	fmt.Printf("RLM overview benchmark complete\n")
	fmt.Printf("Model:              %s\n", modelID)
	fmt.Printf("Cases:              %d\n", len(cases))
	fmt.Printf("Average score:      %.4f\n", report.AverageScore)
	fmt.Printf("Passed/failed:      %d/%d\n", report.PassedExamples, report.FailedExamples)
	fmt.Printf("Evaluation errors:  %d\n", report.EvaluationErrors)
	if report.ProtectedGate != nil {
		fmt.Printf("Protected gate:     %t\n", report.ProtectedGate.Passed)
	}
	fmt.Printf("Report:             %s\n", outputPath)
}

var errProtectedGateFailed = errors.New("protected gate failed")

type runOptimizationRequest struct {
	modelID                      core.ModelID
	suitePath                    string
	outputPath                   string
	artifactPath                 string
	cases                        []orchestration.RLMOverviewBenchmarkCase
	agent                        *orchestration.RLMOverviewBenchmarkAgent
	baseline                     *orchestration.RLMOverviewBenchmarkBaseline
	workers                      int
	caseTimeout                  time.Duration
	maxAttempts                  int
	passThreshold                float64
	protectedRegressionTolerance float64
	skipProtectedGate            bool
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

	checkpoint := buildOptimizationCheckpoint(req, workflow, protectedReport, protectedGate, ledger.snapshot(), budgetManager.Status(), metadata)
	if protectedGate != nil && !protectedGate.Passed {
		if err := writeJSON(req.outputPath, checkpoint); err != nil {
			return err
		}
		printOptimizationSummary(req, checkpoint, false)
		return errProtectedGateFailed
	}

	if err := orchestration.WriteRLMOverviewOptimizedProgram(req.artifactPath, workflow.OptimizedProgram); err != nil {
		return err
	}
	if err := writeJSON(req.outputPath, checkpoint); err != nil {
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
	})
	if err != nil {
		return err
	}
	if err := writeJSON(req.outputPath, report); err != nil {
		return err
	}
	printSummary(req.modelID, req.cases, report, req.outputPath)
	if report.ProtectedGate != nil && !report.ProtectedGate.Passed {
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
	})
	if err != nil {
		return nil, nil, err
	}
	return report, report.ProtectedGate, nil
}

func buildOptimizationCheckpoint(req runOptimizationRequest, workflow *optimize.GEPAWorkflowResult, protectedReport *orchestration.RLMOverviewBenchmarkRunReport, protectedGate *orchestration.RLMOverviewProtectedGateReport, tokenUsage map[string]int64, budgetStatus maestrobudget.BudgetStatus, artifactMetadata map[string]interface{}) optimizationCheckpoint {
	if protectedReport != nil {
		tokenUsage = combineTokenUsage(tokenUsage, protectedReport.TokenUsage)
	}
	checkpoint := optimizationCheckpoint{
		WrittenAt:        time.Now().UTC(),
		SuitePath:        req.suitePath,
		ModelID:          string(req.modelID),
		ArtifactPath:     req.artifactPath,
		ProtectedGate:    protectedGate,
		ProtectedReport:  protectedReport,
		TokenUsage:       tokenUsage,
		BudgetStatus:     &budgetStatus,
		ArtifactMetadata: artifactMetadata,
	}
	if workflow.Optimization != nil {
		checkpoint.TrainingExampleCount = workflow.Optimization.TrainingExampleCount
		checkpoint.ValidationExampleCount = workflow.Optimization.ValidationExampleCount
		checkpoint.BestArtifacts = workflow.Optimization.BestArtifacts.Clone()
		if workflow.Optimization.BestCandidate != nil {
			checkpoint.BestCandidateID = workflow.Optimization.BestCandidate.ID
		}
	}
	if workflow.BaselineRun != nil {
		checkpoint.BaselineValidation = workflow.BaselineRun.AverageScore
	}
	if workflow.ReplayRun != nil {
		checkpoint.ReplayValidation = workflow.ReplayRun.AverageScore
		checkpoint.BestValidation = workflow.ReplayRun.AverageScore
	}
	// Prefer GEPA's explicit best validation evaluation over replay, and replay
	// over the zero value when validation was not run.
	if workflow.Optimization != nil && workflow.Optimization.BestValidationEvaluation != nil {
		checkpoint.BestValidation = workflow.Optimization.BestValidationEvaluation.AverageScore
	}
	if checkpoint.BestValidation == 0 {
		checkpoint.BestValidation = checkpoint.ReplayValidation
	}
	return checkpoint
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
	fmt.Printf("Best validation:       %.4f\n", checkpoint.BestValidation)
	if checkpoint.ProtectedGate != nil {
		fmt.Printf("Protected gate:        %t\n", checkpoint.ProtectedGate.Passed)
	}
	fmt.Printf("Artifact written:      %t\n", artifactWritten)
	if artifactWritten {
		fmt.Printf("Artifact:              %s\n", req.artifactPath)
	}
	fmt.Printf("Checkpoint:            %s\n", req.outputPath)
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

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
