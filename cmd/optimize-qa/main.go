package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/orchestration"
	"github.com/XiaoConstantine/maestro/internal/util"
)

type optimizationCheckpoint struct {
	WrittenAt              time.Time               `json:"written_at"`
	SuitePath              string                  `json:"suite_path"`
	ModelID                string                  `json:"model_id"`
	ArtifactPath           string                  `json:"artifact_path,omitempty"`
	TrainingExampleCount   int                     `json:"training_example_count"`
	ValidationExampleCount int                     `json:"validation_example_count"`
	SeedSkillVersion       int                     `json:"seed_skill_version"`
	BaselineValidation     float64                 `json:"baseline_validation_score"`
	BestValidation         float64                 `json:"best_validation_score"`
	ReplayOnly             bool                    `json:"replay_only,omitempty"`
	Published              bool                    `json:"published"`
	PublishedVersion       int                     `json:"published_version,omitempty"`
	BestCandidateID        string                  `json:"best_candidate_id,omitempty"`
	BestArtifacts          optimize.AgentArtifacts `json:"best_artifacts"`
}

const defaultQAArtifactPath = "./benchmark_results/qa_optimized_program.json"

func main() {
	var (
		suitePath         string
		outputPath        string
		artifactPath      string
		apiKey            string
		modelSpec         string
		modelProvider     string
		modelName         string
		modelCfg          string
		qaArtifactsPath   string
		skillStorePath    string
		skillDomain       string
		verbose           bool
		publishIfImproved bool
		dryRun            bool
		estimateCost      bool
		replayOnly        bool
		populationSize    int
		generations       int
		reflectionFreq    int
		evalConcurrency   int
		validationSplit   float64
		searchBatchSize   int
		stagnationLimit   int
		validationFreq    int
		maxMetricCalls    int
		scoreThreshold    float64
		maxRuntime        time.Duration
	)

	flag.StringVar(&suitePath, "suite", "./benchmarks/qa_suite.json", "Path to the QA benchmark suite JSON")
	flag.StringVar(&outputPath, "output", "./benchmark_results/qa_gepa_checkpoint.json", "Path to write the optimization checkpoint JSON")
	flag.StringVar(&artifactPath, "artifact", defaultQAArtifactPath, "Path to write/read the optimized program JSON artifact")
	flag.StringVar(&apiKey, "api-key", "", "API key for external model providers")
	flag.StringVar(&modelSpec, "model", "", `Full model specification (e.g. "google:gemini-3.0-pro" or "openai:gpt-5.4-mini")`)
	flag.StringVar(&modelProvider, "provider", "google", "Model provider (google, anthropic, openai, ollama, llamacpp)")
	flag.StringVar(&modelName, "model-name", "gemini-3.0-pro", "Model name")
	flag.StringVar(&modelCfg, "model-config", "", "Additional model configuration")
	flag.StringVar(&qaArtifactsPath, "qa-artifacts", os.Getenv("MAESTRO_QA_ARTIFACTS"), "Optional path to base QA artifacts JSON")
	flag.StringVar(&skillStorePath, "store", "~/.maestro/skills.json", "Path to the persisted QA skill store JSON")
	flag.StringVar(&skillDomain, "domain", orchestration.DefaultQASkillDomain, "Skill domain to optimize and publish")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&publishIfImproved, "publish-if-improved", false, "Publish a new skill version when validation score improves")
	flag.BoolVar(&dryRun, "dry-run", false, "Run optimization and write the checkpoint, but never publish a skill")
	flag.BoolVar(&estimateCost, "estimate-cost", false, "Print the expected run size and approximate LLM-call range without running optimization")
	flag.BoolVar(&replayOnly, "replay-only", false, "Skip optimization and replay a previously saved optimized program")
	flag.IntVar(&populationSize, "population", 4, "GEPA population size")
	flag.IntVar(&generations, "generations", 2, "GEPA generation count")
	flag.IntVar(&reflectionFreq, "reflection-freq", 1, "GEPA reflection frequency")
	flag.IntVar(&evalConcurrency, "eval-concurrency", 1, "Concurrent GEPA evaluations")
	flag.Float64Var(&validationSplit, "validation-split", 0.25, "Validation split for the benchmark suite")
	flag.IntVar(&searchBatchSize, "search-batch-size", 4, "GEPA search batch size")
	flag.IntVar(&stagnationLimit, "stagnation-limit", 40, "GEPA stagnation limit")
	flag.IntVar(&validationFreq, "validation-frequency", 1, "Run validation every N generations")
	flag.IntVar(&maxMetricCalls, "max-metric-calls", 0, "Optional cap on GEPA metric evaluations; 0 disables the cap")
	flag.Float64Var(&scoreThreshold, "score-threshold", 0, "Optional early-stop threshold for validation score; 0 disables it")
	flag.DurationVar(&maxRuntime, "max-runtime", 0, "Optional wall-clock limit for GEPA optimization; 0 disables it")
	flag.Parse()

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
	defaultLLM, err := util.LoadLLMFromModelConfig(ctx, modelConfig, modelID)
	if err != nil {
		fatalf("configure default LLM: %v", err)
	}
	core.GlobalConfig.DefaultLLM = defaultLLM
	teacherLLM, err := util.LoadLLMFromModelConfig(ctx, modelConfig, modelID)
	if err != nil {
		fatalf("configure teacher LLM: %v", err)
	}
	core.GlobalConfig.TeacherLLM = teacherLLM

	cases, err := orchestration.LoadQABenchmarkSuite(suitePath)
	if err != nil {
		fatalf("load benchmark suite: %v", err)
	}
	examples := orchestration.QABenchmarkExamples(cases)
	trainingExamples, validationExamples, err := splitAgentExamples(examples, validationSplit)
	if err != nil {
		fatalf("split benchmark examples: %v", err)
	}

	resolvedStorePath, err := expandPath(skillStorePath)
	if err != nil {
		fatalf("resolve skill store path: %v", err)
	}
	resolvedArtifactPath, err := expandPath(artifactPath)
	if err != nil {
		fatalf("resolve artifact path: %v", err)
	}
	store := skills.NewFileStore(resolvedStorePath)
	skillDomain = strings.TrimSpace(skillDomain)
	if skillDomain == "" {
		skillDomain = orchestration.DefaultQASkillDomain
	}

	baseArtifacts, err := orchestration.LoadConfiguredQAArtifacts(qaArtifactsPath)
	if err != nil {
		fatalf("load base QA artifacts: %v", err)
	}

	var currentSkill *skills.Skill
	currentSkill, err = store.Best(ctx, skillDomain)
	if err != nil {
		fatalf("load current QA skill: %v", err)
	}

	seedArtifacts := baseArtifacts.Clone()
	if seedArtifacts.Text == nil {
		seedArtifacts.Text = make(map[optimize.ArtifactKey]string)
	}
	seedArtifacts.Text[optimize.ArtifactSkillPack] = ""
	seedSkillVersion := 0
	if currentSkill != nil {
		seedArtifacts.Text[optimize.ArtifactSkillPack] = currentSkill.Content
		seedSkillVersion = currentSkill.Version
	}

	maxTurns := seedArtifacts.Int["max_turns"]
	if maxTurns <= 0 {
		maxTurns = 12
	}

	if estimateCost {
		printRunEstimate(trainingExamples, validationExamples, populationSize, generations, maxTurns)
		return
	}

	llm := core.GetDefaultLLM()
	if llm == nil {
		fatalf("default LLM is not configured")
	}

	evaluator := orchestration.NewQABenchmarkEvaluator(orchestration.DefaultQABenchmarkEvaluatorConfig())
	seedAgent := orchestration.NewQABenchmarkAgent(llm, logger, seedArtifacts)
	var (
		baselineScore       float64
		bestValidationScore float64
		bestArtifacts       optimize.AgentArtifacts
		bestCandidateID     string
	)

	if replayOnly {
		harness := &optimize.Harness{Evaluator: evaluator, PassThreshold: 1.0}
		baselineRun, err := harness.Run(ctx, seedAgent, validationExamples)
		if err != nil {
			fatalf("evaluate baseline validation examples: %v", err)
		}
		baselineScore = baselineRun.AverageScore

		replayRun, replayArtifacts, err := replaySavedQAProgram(ctx, llm, logger, resolvedArtifactPath, seedArtifacts, validationExamples)
		if err != nil {
			fatalf("replay optimized QA program: %v", err)
		}
		bestValidationScore = replayRun.AverageScore
		bestArtifacts = replayArtifacts
	} else {
		if err := ensureParentDir(resolvedArtifactPath); err != nil {
			fatalf("create artifact directory: %v", err)
		}

		workflow, err := optimize.RunGEPAWorkflow(ctx, seedAgent, optimize.GEPAWorkflowRequest{
			Evaluator:          evaluator,
			TrainingExamples:   trainingExamples,
			ValidationExamples: validationExamples,
			BaselineExamples:   validationExamples,
			ReplayExamples:     validationExamples,
			PassThreshold:      1.0,
			ApplyBest:          true,
			ArtifactPath:       resolvedArtifactPath,
			Config: optimize.GEPAAdapterConfig{
				PopulationSize:      populationSize,
				MaxGenerations:      generations,
				ReflectionFreq:      reflectionFreq,
				SearchBatchSize:     searchBatchSize,
				StagnationLimit:     stagnationLimit,
				ValidationSplit:     0,
				ValidationFrequency: validationFreq,
				EvalConcurrency:     evalConcurrency,
				PassThreshold:       1.0,
				PrimaryArtifact:     optimize.ArtifactSkillPack,
				MaxMetricCalls:      maxMetricCalls,
				ScoreThreshold:      scoreThreshold,
				MaxRuntime:          maxRuntime,
				ArtifactKeys:        []optimize.ArtifactKey{optimize.ArtifactSkillPack},
			},
		})
		if err != nil {
			fatalf("optimize QA skill: %v", err)
		}

		if workflow.BaselineRun != nil {
			baselineScore = workflow.BaselineRun.AverageScore
		}
		if workflow.ReplayRun != nil {
			bestValidationScore = workflow.ReplayRun.AverageScore
		} else {
			bestValidationScore = baselineScore
		}
		bestArtifacts = workflow.Optimization.BestArtifacts.Clone()
		if workflow.Optimization.BestCandidate != nil {
			bestCandidateID = workflow.Optimization.BestCandidate.ID
		}
	}

	checkpoint := optimizationCheckpoint{
		WrittenAt:              time.Now().UTC(),
		SuitePath:              suitePath,
		ModelID:                string(modelID),
		ArtifactPath:           resolvedArtifactPath,
		TrainingExampleCount:   len(trainingExamples),
		ValidationExampleCount: len(validationExamples),
		SeedSkillVersion:       seedSkillVersion,
		BaselineValidation:     baselineScore,
		BestValidation:         bestValidationScore,
		ReplayOnly:             replayOnly,
		BestArtifacts:          bestArtifacts.Clone(),
	}
	checkpoint.BestCandidateID = bestCandidateID

	bestOverlay := strings.TrimSpace(bestArtifacts.Text[optimize.ArtifactSkillPack])
	improved := bestValidationScore > baselineScore+1e-9
	changedOverlay := currentSkill == nil || strings.TrimSpace(currentSkill.Content) != bestOverlay

	switch {
	case dryRun:
		logger.Info(ctx, "Dry run enabled; skipping skill publication")
	case !publishIfImproved:
		logger.Info(ctx, "publish-if-improved disabled; skipping skill publication")
	case bestOverlay == "":
		logger.Info(ctx, "Skipping skill publication because the optimized overlay is empty")
	case !improved:
		logger.Info(ctx, "Skipping skill publication because validation did not improve (baseline=%.4f best=%.4f)", baselineScore, bestValidationScore)
	case !changedOverlay:
		logger.Info(ctx, "Skipping skill publication because the optimized overlay matches the current persisted skill")
	default:
		nextVersion := seedSkillVersion + 1
		if err := store.Save(ctx, skills.Skill{
			Name:    "qa-gepa",
			Domain:  skillDomain,
			Content: bestOverlay,
			Version: nextVersion,
			Metadata: map[string]string{
				"baseline_validation_score": fmt.Sprintf("%.6f", baselineScore),
				"best_validation_score":     fmt.Sprintf("%.6f", bestValidationScore),
				"model_id":                  string(modelID),
				"suite_path":                suitePath,
			},
		}); err != nil {
			fatalf("publish optimized skill: %v", err)
		}
		checkpoint.Published = true
		checkpoint.PublishedVersion = nextVersion
		logger.Info(ctx, "Published QA skill domain=%q version=%d", skillDomain, nextVersion)
	}

	if err := writeCheckpoint(outputPath, checkpoint); err != nil {
		fatalf("write checkpoint: %v", err)
	}

	fmt.Printf("QA GEPA optimization complete\n")
	fmt.Printf("Model:                 %s\n", modelID)
	fmt.Printf("Training examples:     %d\n", len(trainingExamples))
	fmt.Printf("Validation examples:   %d\n", len(validationExamples))
	fmt.Printf("Baseline validation:   %.4f\n", baselineScore)
	fmt.Printf("Best validation:       %.4f\n", bestValidationScore)
	fmt.Printf("Replay only:           %t\n", replayOnly)
	fmt.Printf("Published:             %t\n", checkpoint.Published)
	if checkpoint.Published {
		fmt.Printf("Published version:     %d\n", checkpoint.PublishedVersion)
	}
	fmt.Printf("Artifact:              %s\n", resolvedArtifactPath)
	fmt.Printf("Checkpoint:            %s\n", outputPath)
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

func splitAgentExamples(examples []optimize.AgentExample, validationSplit float64) ([]optimize.AgentExample, []optimize.AgentExample, error) {
	if len(examples) == 0 {
		return nil, nil, fmt.Errorf("at least one benchmark example is required")
	}
	if len(examples) == 1 {
		return nil, nil, fmt.Errorf("at least two benchmark examples are required to create a validation split")
	}
	if validationSplit <= 0 || validationSplit >= 1 {
		return nil, nil, fmt.Errorf("validation split must be between 0 and 1")
	}
	validationCount := int(math.Ceil(float64(len(examples)) * validationSplit))
	if validationCount <= 0 {
		validationCount = 1
	}
	if validationCount >= len(examples) {
		validationCount = len(examples) - 1
	}
	splitIndex := len(examples) - validationCount
	training := append([]optimize.AgentExample(nil), examples[:splitIndex]...)
	validation := append([]optimize.AgentExample(nil), examples[splitIndex:]...)
	return training, validation, nil
}

func replaySavedQAProgram(ctx context.Context, llm core.LLM, logger *logging.Logger, artifactPath string, seedArtifacts optimize.AgentArtifacts, examples []optimize.AgentExample) (*optimize.HarnessRunResult, optimize.AgentArtifacts, error) {
	if strings.TrimSpace(artifactPath) == "" {
		return nil, optimize.AgentArtifacts{}, fmt.Errorf("artifact path is required to restore an optimized program")
	}

	program, err := optimize.ReadOptimizedAgentProgram(artifactPath)
	if err != nil {
		return nil, optimize.AgentArtifacts{}, fmt.Errorf("read optimized program: %w", err)
	}

	replayAgent := orchestration.NewQABenchmarkAgent(llm, logger, seedArtifacts)
	if err := optimize.ApplyOptimizedAgentProgram(replayAgent, program); err != nil {
		return nil, optimize.AgentArtifacts{}, fmt.Errorf("apply optimized program: %w", err)
	}

	harness := &optimize.Harness{
		Evaluator:     orchestration.NewQABenchmarkEvaluator(orchestration.DefaultQABenchmarkEvaluatorConfig()),
		PassThreshold: 1.0,
	}
	replayRun, err := harness.Run(ctx, replayAgent, examples)
	if err != nil {
		return nil, optimize.AgentArtifacts{}, fmt.Errorf("replay harness run: %w", err)
	}

	return replayRun, replayAgent.GetArtifacts(), nil
}

func ensureParentDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is required")
	}
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func printRunEstimate(trainingExamples, validationExamples []optimize.AgentExample, populationSize, generations, maxTurns int) {
	optimizationEvaluations := populationSize * generations * len(trainingExamples)
	baselineEvaluations := len(validationExamples)
	totalEvaluations := optimizationEvaluations + baselineEvaluations
	lowLLMCalls := totalEvaluations
	highLLMCalls := totalEvaluations * maxTurns

	fmt.Printf("QA GEPA run estimate\n")
	fmt.Printf("Training examples:   %d\n", len(trainingExamples))
	fmt.Printf("Validation examples: %d\n", len(validationExamples))
	fmt.Printf("Population:          %d\n", populationSize)
	fmt.Printf("Generations:         %d\n", generations)
	fmt.Printf("Estimated evaluations: %d\n", totalEvaluations)
	fmt.Printf("Estimated LLM-call range: %d-%d (assuming 1 to %d turns per evaluation)\n", lowLLMCalls, highLLMCalls, maxTurns)
	fmt.Printf("Note: this is a run-size estimate, not a provider-billed dollar quote.\n")
}

func writeCheckpoint(path string, checkpoint optimizationCheckpoint) error {
	resolvedPath, err := expandPath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	if err := os.WriteFile(resolvedPath, data, 0o644); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
