package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	agentrlm "github.com/XiaoConstantine/dspy-go/pkg/agents/rlm"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	maestrooptimize "github.com/XiaoConstantine/maestro/internal/optimize"
	"github.com/XiaoConstantine/maestro/internal/orchestration"
	"github.com/XiaoConstantine/maestro/internal/util"
)

type targetedAskOptimizationCheckpoint struct {
	WrittenAt              time.Time               `json:"written_at"`
	SuitePath              string                  `json:"suite_path"`
	ModelID                string                  `json:"model_id"`
	ArtifactPath           string                  `json:"artifact_path,omitempty"`
	TrainingExampleCount   int                     `json:"training_example_count"`
	ValidationExampleCount int                     `json:"validation_example_count"`
	BaselineValidation     float64                 `json:"baseline_validation_score"`
	BestValidation         float64                 `json:"best_validation_score"`
	ValidationDelta        float64                 `json:"validation_delta"`
	Outcome                string                  `json:"outcome"`
	ReplayOnly             bool                    `json:"replay_only,omitempty"`
	ArtifactWritten        bool                    `json:"artifact_written"`
	BestCandidateID        string                  `json:"best_candidate_id,omitempty"`
	ArtifactMetadata       map[string]interface{}  `json:"artifact_metadata,omitempty"`
	BestArtifacts          optimize.AgentArtifacts `json:"best_artifacts,omitempty"`
}

const (
	targetedAskMinimumGEPAExamples = 16
	defaultTargetedAskSuitePath    = "./benchmarks/rlm_overview_suite.json"
	defaultTargetedAskOutputPath   = "./benchmark_results/rlm_targeted_ask_optimization.json"
	validationRegressionEpsilon    = 1e-9
)

func main() {
	var (
		suitePath          string
		outputPath         string
		artifactPath       string
		apiKey             string
		modelSpec          string
		modelProvider      string
		modelName          string
		modelCfg           string
		baseURL            string
		traceDir           string
		replayOnly         bool
		allowNoImprovement bool
		verbose            bool
		populationSize     int
		generations        int
		reflectionFreq     int
		evalConcurrency    int
		validationFreq     int
		searchBatchSize    int
		stagnationLimit    int
		maxMetricCalls     int
		scoreThreshold     float64
		passThreshold      float64
		maxRuntime         time.Duration
		validationSplit    float64
		maxIterations      int
		maxTokens          int
		maxContextChars    int
	)

	flag.StringVar(&suitePath, "suite", defaultTargetedAskSuitePath, "Path to the targeted ask benchmark suite JSON; uses the RLM overview suite schema")
	flag.StringVar(&outputPath, "output", defaultTargetedAskOutputPath, "Path to write the targeted ask optimization checkpoint JSON")
	flag.StringVar(&artifactPath, "artifact", "", "Path to write/read the optimized targeted ask RLM program JSON; defaults to ~/.maestro/rlm_artifacts/targeted_ask_optimized_program.json")
	flag.StringVar(&apiKey, "api-key", "", "API key for external model providers")
	flag.StringVar(&modelSpec, "model", "", `Full model specification (e.g. "anthropic:claude-sonnet-4-6" or "openai:gpt-5.4-mini")`)
	flag.StringVar(&modelProvider, "provider", "anthropic", "Model provider (anthropic, google, openai, ollama, llamacpp)")
	flag.StringVar(&modelName, "model-name", "claude-sonnet-4-6", "Model name")
	flag.StringVar(&modelCfg, "model-config", "", "Additional model configuration")
	flag.StringVar(&baseURL, "base-url", "", "Optional base URL override for local providers")
	flag.StringVar(&traceDir, "trace-dir", "", "Optional directory for dspy-go RLM JSONL traces")
	flag.BoolVar(&replayOnly, "replay-only", false, "Replay a previously saved optimized targeted ask program instead of running GEPA")
	flag.BoolVar(&allowNoImprovement, "allow-no-improvement", false, "Write a GEPA artifact even when validation does not improve over baseline")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.IntVar(&populationSize, "population", 4, "GEPA population size; default is smoke-test grade, use 12+ for production runs")
	flag.IntVar(&generations, "generations", 2, "GEPA generation count; default is smoke-test grade, use 8+ for production runs")
	flag.IntVar(&reflectionFreq, "reflection-freq", 1, "GEPA reflection frequency")
	flag.IntVar(&evalConcurrency, "eval-concurrency", 1, "Concurrent GEPA evaluations")
	flag.IntVar(&validationFreq, "validation-frequency", 1, "Run validation every N GEPA generations")
	flag.IntVar(&searchBatchSize, "search-batch-size", 4, "GEPA search batch size")
	flag.IntVar(&stagnationLimit, "stagnation-limit", 40, "GEPA stagnation limit")
	flag.IntVar(&maxMetricCalls, "max-metric-calls", 0, "Optional cap on GEPA metric evaluations; 0 disables it")
	flag.Float64Var(&scoreThreshold, "score-threshold", 0, "Optional early-stop threshold for validation score; 0 disables it")
	flag.Float64Var(&passThreshold, "pass-threshold", orchestration.RLMOverviewBenchmarkDefaultPassThreshold, "Minimum validation score GEPA should treat as a passing candidate")
	flag.DurationVar(&maxRuntime, "max-runtime", 0, "Optional wall-clock limit for GEPA optimization; 0 disables it")
	flag.Float64Var(&validationSplit, "validation-split", 0.25, "Validation split for the benchmark suite")
	flag.IntVar(&maxIterations, "max-iterations", 5, "Upper bound for mutable RLM max iterations")
	flag.IntVar(&maxTokens, "max-tokens", 42000, "Upper bound for mutable RLM max tokens")
	flag.IntVar(&maxContextChars, "max-context-chars", 90000, "Maximum repository context characters supplied to targeted ask")
	flag.Parse()

	if err := maestrooptimize.ValidateUnitThreshold("pass-threshold", passThreshold); err != nil {
		fatalf("%v", err)
	}

	ctx := context.Background()
	logger := configureLogger(verbose)
	logging.SetLogger(logger)
	llms.EnsureFactory()

	modelConfig, modelID, err := resolveModel(modelSpec, modelProvider, modelName, modelCfg, apiKey, baseURL)
	if err != nil {
		fatalf("resolve model configuration: %v", err)
	}
	llm, err := util.LoadLLMFromModelConfig(ctx, modelConfig, modelID)
	if err != nil {
		fatalf("configure LLM: %v", err)
	}
	core.GlobalConfig.DefaultLLM = llm

	cases, err := orchestration.LoadRLMOverviewBenchmarkSuite(suitePath)
	if err != nil {
		fatalf("load benchmark suite: %v", err)
	}
	examples := orchestration.RLMOverviewBenchmarkExamples(cases)
	trainingExamples, validationExamples, err := splitAgentExamples(examples, validationSplit)
	if err != nil {
		fatalf("split benchmark suite: %v", err)
	}

	resolvedArtifactPath, err := orchestration.ResolveRLMTargetedAskOptimizedProgramPath(artifactPath)
	if err != nil {
		fatalf("resolve artifact path: %v", err)
	}
	resolvedTraceDir := ""
	if strings.TrimSpace(traceDir) != "" {
		resolvedTraceDir, err = expandPath(traceDir)
		if err != nil {
			fatalf("resolve trace dir: %v", err)
		}
	}
	agentCfg := orchestration.DefaultRLMTargetedAskBenchmarkAgentConfig()
	if maxIterations > 0 {
		agentCfg.MaxIterations = maxIterations
	}
	if maxTokens > 0 {
		agentCfg.MaxTokens = maxTokens
	}
	if maxContextChars > 0 {
		agentCfg.MaxContextChars = maxContextChars
	}
	agentCfg.TraceDir = resolvedTraceDir

	seedAgent, err := orchestration.NewRLMTargetedAskBenchmarkAgent(llm, agentCfg)
	if err != nil {
		fatalf("create targeted ask benchmark agent: %v", err)
	}
	evaluator := orchestration.NewRLMTargetedAskBenchmarkEvaluator(orchestration.DefaultRLMOverviewEvaluatorConfig())
	seedArtifacts := seedAgent.GetArtifacts()

	bestArtifacts := seedArtifacts.Clone()
	baselineScore := 0.0
	bestValidation := 0.0
	validationDelta := 0.0
	outcome := "replay"
	bestCandidateID := ""
	artifactWritten := false
	metadata := targetedAskArtifactMetadata(string(modelID), suitePath, len(trainingExamples), len(validationExamples), baselineScore)

	if replayOnly {
		program, _, err := orchestration.LoadRLMTargetedAskOptimizedProgram(resolvedArtifactPath)
		if err != nil {
			fatalf("load optimized program: %v", err)
		}
		if program == nil {
			fatalf("optimized targeted ask artifact not found: %s", resolvedArtifactPath)
		}
		replayAgent, err := orchestration.NewRLMTargetedAskBenchmarkAgent(llm, agentCfg)
		if err != nil {
			fatalf("create replay agent: %v", err)
		}
		if err := orchestration.ApplyRLMTargetedAskOptimizedProgram(replayAgent, program); err != nil {
			fatalf("apply optimized program: %v", err)
		}
		bestArtifacts = replayAgent.GetArtifacts()
		bestValidation, err = evaluateExamples(ctx, evaluator, replayAgent, validationExamples)
		if err != nil {
			fatalf("evaluate replay: %v", err)
		}
	} else {
		workflow, err := optimize.RunGEPAWorkflow(ctx, seedAgent, optimize.GEPAWorkflowRequest{
			Evaluator:          evaluator,
			TrainingExamples:   trainingExamples,
			ValidationExamples: validationExamples,
			BaselineExamples:   validationExamples,
			ReplayExamples:     validationExamples,
			PassThreshold:      passThreshold,
			ApplyBest:          false,
			Config: optimize.GEPAAdapterConfig{
				PopulationSize:      populationSize,
				MaxGenerations:      generations,
				ReflectionFreq:      reflectionFreq,
				SearchBatchSize:     searchBatchSize,
				StagnationLimit:     stagnationLimit,
				ValidationSplit:     0,
				ValidationFrequency: validationFreq,
				EvalConcurrency:     evalConcurrency,
				PassThreshold:       passThreshold,
				PrimaryArtifact:     optimize.ArtifactRLMOuterPrompt,
				ArtifactKeys: []optimize.ArtifactKey{
					optimize.ArtifactRLMOuterPrompt,
					optimize.ArtifactRLMIterationPrompt,
				},
				IntMutationPlans: rlmIntMutationPlans(maxIterations, maxTokens),
				MaxMetricCalls:   maxMetricCalls,
				ScoreThreshold:   scoreThreshold,
				MaxRuntime:       maxRuntime,
			},
		})
		if err != nil {
			fatalf("optimize targeted ask RLM: %v", err)
		}
		if workflow == nil || workflow.Optimization == nil || workflow.OptimizedProgram == nil {
			fatalf("GEPA workflow returned incomplete optimization result")
		}
		baselineScore = workflowBaselineValidation(workflow)
		bestValidation = workflowBestValidation(workflow)
		validationDelta = bestValidation - baselineScore
		outcome = validationOutcome(validationDelta)
		metadata = targetedAskArtifactMetadata(string(modelID), suitePath, len(trainingExamples), len(validationExamples), baselineScore)
		bestArtifacts = workflow.Optimization.BestArtifacts.Clone()
		if workflow.Optimization.BestCandidate != nil {
			bestCandidateID = workflow.Optimization.BestCandidate.ID
			metadata["best_candidate_id"] = bestCandidateID
		}
		if workflow.BaselineRun != nil {
			metadata["baseline_validation_score"] = workflow.BaselineRun.AverageScore
		}
		if workflow.ReplayRun != nil {
			metadata["replay_validation_score"] = workflow.ReplayRun.AverageScore
		}
		if err := orchestration.AnnotateRLMTargetedAskOptimizedProgram(workflow.OptimizedProgram, metadata); err != nil {
			fatalf("annotate optimized program: %v", err)
		}
		if shouldWriteValidationArtifact(outcome, allowNoImprovement) {
			if err := orchestration.WriteRLMTargetedAskOptimizedProgram(resolvedArtifactPath, workflow.OptimizedProgram); err != nil {
				fatalf("write optimized program: %v", err)
			}
			artifactWritten = true
		}
	}

	checkpoint := targetedAskOptimizationCheckpoint{
		WrittenAt:              time.Now().UTC(),
		SuitePath:              suitePath,
		ModelID:                string(modelID),
		ArtifactPath:           resolvedArtifactPath,
		TrainingExampleCount:   len(trainingExamples),
		ValidationExampleCount: len(validationExamples),
		BaselineValidation:     baselineScore,
		BestValidation:         bestValidation,
		ValidationDelta:        validationDelta,
		Outcome:                outcome,
		ReplayOnly:             replayOnly,
		ArtifactWritten:        artifactWritten,
		BestCandidateID:        bestCandidateID,
		ArtifactMetadata:       metadata,
		BestArtifacts:          bestArtifacts,
	}
	if err := writeJSON(outputPath, checkpoint); err != nil {
		fatalf("write checkpoint: %v", err)
	}

	fmt.Printf("RLM targeted ask GEPA optimization complete\n")
	fmt.Printf("Model:               %s\n", modelID)
	fmt.Printf("Training examples:   %d\n", len(trainingExamples))
	fmt.Printf("Validation examples: %d\n", len(validationExamples))
	fmt.Printf("Baseline validation: %.4f\n", baselineScore)
	fmt.Printf("Best validation:     %.4f\n", bestValidation)
	fmt.Printf("Validation delta:    %.4f\n", validationDelta)
	fmt.Printf("Outcome:             %s\n", outcome)
	fmt.Printf("Replay only:         %t\n", replayOnly)
	fmt.Printf("Artifact written:    %t\n", artifactWritten)
	fmt.Printf("Artifact:            %s\n", resolvedArtifactPath)
	fmt.Printf("Checkpoint:          %s\n", outputPath)
	if outcome == "regressed" {
		os.Exit(2)
	}
}

func splitAgentExamples(examples []optimize.AgentExample, validationSplit float64) ([]optimize.AgentExample, []optimize.AgentExample, error) {
	return maestrooptimize.SplitAgentExamples(examples, validationSplit, targetedAskMinimumGEPAExamples)
}

func configureLogger(verbose bool) *logging.Logger {
	logLevel := logging.INFO
	if verbose {
		logLevel = logging.DEBUG
	}
	return logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  []logging.Output{logging.NewConsoleOutput(true, logging.WithColor(true))},
	})
}

func resolveModel(modelSpec, provider, modelName, modelCfg, apiKey, baseURL string) (*util.ModelConfig, core.ModelID, error) {
	if strings.TrimSpace(modelSpec) != "" {
		if parsedProvider, parsedName, parsedCfg := util.ParseModelString(modelSpec); parsedProvider != "" {
			provider = parsedProvider
			modelName = parsedName
			modelCfg = parsedCfg
		}
	}
	cfg := &util.ModelConfig{
		ModelProvider: provider,
		ModelName:     modelName,
		ModelConfig:   modelCfg,
		APIKey:        apiKey,
		BaseURL:       strings.TrimSpace(baseURL),
	}
	if err := util.ValidateModelConfig(cfg); err != nil {
		return nil, "", err
	}
	return cfg, util.ConstructModelID(cfg), nil
}

func evaluateExamples(ctx context.Context, evaluator optimize.AgentEvaluator, agent optimize.OptimizableAgent, examples []optimize.AgentExample) (float64, error) {
	if len(examples) == 0 {
		return 0, fmt.Errorf("at least one validation example is required")
	}
	total := 0.0
	for _, example := range examples {
		cloned, err := agent.Clone()
		if err != nil {
			return 0, err
		}
		result, err := evaluator.Evaluate(ctx, cloned, example)
		if err != nil {
			return 0, fmt.Errorf("evaluate example %q: %w", example.ID, err)
		}
		total += result.Score
	}
	return total / float64(len(examples)), nil
}

func workflowBaselineValidation(workflow *optimize.GEPAWorkflowResult) float64 {
	if workflow != nil && workflow.BaselineRun != nil {
		return workflow.BaselineRun.AverageScore
	}
	return 0
}

func workflowBestValidation(workflow *optimize.GEPAWorkflowResult) float64 {
	if workflow == nil {
		return 0
	}
	if workflow.Optimization != nil && workflow.Optimization.BestValidationEvaluation != nil {
		return workflow.Optimization.BestValidationEvaluation.AverageScore
	}
	if workflow.ReplayRun != nil {
		return workflow.ReplayRun.AverageScore
	}
	return 0
}

func validationOutcome(delta float64) string {
	if delta > validationRegressionEpsilon {
		return "improved"
	}
	if delta < -validationRegressionEpsilon {
		return "regressed"
	}
	return "no_change"
}

func shouldWriteValidationArtifact(outcome string, allowNoImprovement bool) bool {
	return outcome == "improved" || (allowNoImprovement && outcome == "no_change")
}

func rlmIntMutationPlans(maxIterations, maxTokens int) map[string]optimize.IntMutationConfig {
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

func targetedAskArtifactMetadata(modelID, suitePath string, trainingCount, validationCount int, baselineScore float64) map[string]interface{} {
	return map[string]interface{}{
		"created_at":                time.Now().UTC().Format(time.RFC3339),
		"model_id":                  modelID,
		"suite_path":                suitePath,
		"training_example_count":    trainingCount,
		"validation_example_count":  validationCount,
		"baseline_validation_score": baselineScore,
		"optimized_program_schema":  "dspy-go.optimized-agent-program",
		"optimized_program_version": 1,
	}
}

func writeJSON(path string, value interface{}) error {
	resolvedPath, err := expandPath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(resolvedPath, append(data, '\n'), 0o644)
}

func expandPath(path string) (string, error) {
	path = strings.TrimSpace(os.ExpandEnv(path))
	if path == "" {
		return "", fmt.Errorf("path is required")
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

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
