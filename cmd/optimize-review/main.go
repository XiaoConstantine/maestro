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
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/review"
	"github.com/XiaoConstantine/maestro/internal/util"
)

type reviewOptimizationCheckpoint struct {
	WrittenAt              time.Time                   `json:"written_at"`
	SuitePath              string                      `json:"suite_path"`
	SuitePaths             []string                    `json:"suite_paths,omitempty"`
	ModelID                string                      `json:"model_id"`
	TeacherModelID         string                      `json:"teacher_model_id,omitempty"`
	ArtifactPath           string                      `json:"artifact_path,omitempty"`
	AcceptedCaseWeight     float64                     `json:"accepted_case_weight,omitempty"`
	MatchedScoreFloor      float64                     `json:"matched_score_floor,omitempty"`
	TrainingExampleCount   int                         `json:"training_example_count"`
	ValidationExampleCount int                         `json:"validation_example_count"`
	SeedSkillVersion       int                         `json:"seed_skill_version"`
	BaselineValidation     float64                     `json:"baseline_validation_score"`
	BestValidation         float64                     `json:"best_validation_score"`
	ValidationBySuite      map[string]suiteMetrics     `json:"validation_by_suite,omitempty"`
	ValidationCaseReports  map[string]suiteCaseReports `json:"validation_case_reports,omitempty"`
	ReplayOnly             bool                        `json:"replay_only,omitempty"`
	Published              bool                        `json:"published"`
	PublishedVersion       int                         `json:"published_version,omitempty"`
	BestCandidateID        string                      `json:"best_candidate_id,omitempty"`
	BestArtifacts          optimize.AgentArtifacts     `json:"best_artifacts"`
}

const (
	defaultLocalReviewSuitePath      = "~/.maestro/review/corpora/rsc-golang-org/review_go_suite.json"
	defaultLocalReviewCheckpointPath = "~/.maestro/review/checkpoints/review_gepa_checkpoint.json"
	defaultLocalReviewArtifactPath   = "~/.maestro/review/checkpoints/review_optimized_program.json"
)

type suiteMetrics struct {
	TrainingExampleCount   int     `json:"training_example_count"`
	ValidationExampleCount int     `json:"validation_example_count"`
	BaselineValidation     float64 `json:"baseline_validation_score"`
	BestValidation         float64 `json:"best_validation_score"`
}

type suiteCaseReports struct {
	Baseline []validationCaseReport `json:"baseline,omitempty"`
	Best     []validationCaseReport `json:"best,omitempty"`
}

type validationCaseReport struct {
	ID                      string                               `json:"id"`
	Label                   string                               `json:"label,omitempty"`
	FilePath                string                               `json:"file_path,omitempty"`
	Line                    int                                  `json:"line,omitempty"`
	Score                   float64                              `json:"score"`
	RawScore                float64                              `json:"raw_score,omitempty"`
	CaseWeight              float64                              `json:"case_weight,omitempty"`
	LatencyMS               float64                              `json:"latency_ms,omitempty"`
	CommentCount            int                                  `json:"comment_count,omitempty"`
	RawCandidates           int                                  `json:"raw_candidates,omitempty"`
	PreVerificationCount    int                                  `json:"pre_verification_count,omitempty"`
	SkippedAfterFilter      int                                  `json:"skipped_after_filter,omitempty"`
	FilterDropReasons       map[string]int                       `json:"filter_drop_reasons,omitempty"`
	FilterRejections        []review.ReviewFilterRejection       `json:"filter_rejections,omitempty"`
	TotalChunks             int                                  `json:"total_chunks,omitempty"`
	SelectedChunks          int                                  `json:"selected_chunks,omitempty"`
	Matched                 bool                                 `json:"matched,omitempty"`
	MatchedComment          string                               `json:"matched_comment,omitempty"`
	VerificationEnabled     bool                                 `json:"verification_enabled,omitempty"`
	VerificationDropped     int                                  `json:"verification_dropped,omitempty"`
	VerificationDropReasons map[string]int                       `json:"verification_drop_reasons,omitempty"`
	VerificationRejections  []review.ReviewVerificationRejection `json:"verification_rejections,omitempty"`
	EvaluationError         string                               `json:"evaluation_error,omitempty"`
}

type reviewSuite struct {
	Path               string
	TrainingExamples   []optimize.AgentExample
	ValidationExamples []optimize.AgentExample
}

type suiteListFlag []string

type configuredModel struct {
	Config *util.ModelConfig
	ID     core.ModelID
}

func (f *suiteListFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *suiteListFlag) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		*f = append(*f, part)
	}
	return nil
}

func main() {
	var (
		suitePaths         suiteListFlag
		outputPath         string
		artifactPath       string
		apiKey             string
		modelSpec          string
		teacherModelSpec   string
		baseURL            string
		teacherBaseURL     string
		modelProvider      string
		modelName          string
		modelCfg           string
		teacherTracePath   string
		teacherDemoCount   int
		optimizeDemos      bool
		reviewArtifacts    string
		skillStorePath     string
		skillDomain        string
		verbose            bool
		publishIfImproved  bool
		dryRun             bool
		estimateCost       bool
		replayOnly         bool
		populationSize     int
		generations        int
		reflectionFreq     int
		evalConcurrency    int
		validationSplit    float64
		searchBatchSize    int
		stagnationLimit    int
		validationFreq     int
		maxMetricCalls     int
		scoreThreshold     float64
		maxRuntime         time.Duration
		passThreshold      float64
		acceptedCaseWeight float64
		matchedScoreFloor  float64
		maxCasesPerRun     int
		maxFilesPerCase    int
		maxChunksPerCase   int
	)
	defaultEvaluatorConfig := review.DefaultReviewBenchmarkEvaluatorConfig()

	flag.Var(&suitePaths, "suite", "Path to a review benchmark suite JSON; repeat or comma-separate to evaluate multiple reviewer suites")
	flag.StringVar(&outputPath, "output", defaultLocalReviewCheckpointPath, "Path to write the optimization checkpoint JSON")
	flag.StringVar(&artifactPath, "artifact", defaultLocalReviewArtifactPath, "Path to write/read the optimized program JSON artifact")
	flag.StringVar(&apiKey, "api-key", "", "API key for external model providers")
	flag.StringVar(&modelSpec, "model", "", `Full model specification (e.g. "google:gemini-3.0-pro" or "openai:gpt-5.4-mini")`)
	flag.StringVar(&teacherModelSpec, "teacher-model", "", `Optional full teacher model specification used for GEPA reflection (e.g. "google:gemini-3-pro-preview")`)
	flag.StringVar(&baseURL, "base-url", "", "Optional base URL override for the primary model provider; mainly used for local providers like llamacpp")
	flag.StringVar(&teacherBaseURL, "teacher-base-url", "", "Optional base URL override for the teacher model provider when it differs from the primary model")
	flag.StringVar(&modelProvider, "provider", "google", "Model provider (google, anthropic, openai, ollama, llamacpp)")
	flag.StringVar(&modelName, "model-name", "gemini-3.0-pro", "Model name")
	flag.StringVar(&modelCfg, "model-config", "", "Additional model configuration")
	flag.StringVar(&teacherTracePath, "teacher-traces", "", "Optional path to generated teacher traces used to seed few-shot demos")
	flag.IntVar(&teacherDemoCount, "teacher-demo-count", 3, "Number of top teacher demos to seed when --teacher-traces is provided")
	flag.BoolVar(&optimizeDemos, "optimize-few-shot-demos", false, "Allow GEPA to mutate the few-shot demos artifact as well as the skill pack")
	flag.StringVar(&reviewArtifacts, "review-artifacts", os.Getenv("MAESTRO_REVIEW_ARTIFACTS"), "Optional path to base review artifacts JSON")
	flag.StringVar(&skillStorePath, "store", "~/.maestro/skills.json", "Path to the persisted review skill store JSON")
	flag.StringVar(&skillDomain, "domain", review.DefaultReviewSkillDomain, "Skill domain to optimize and publish")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&publishIfImproved, "publish-if-improved", false, "Publish a new review skill version when validation score improves")
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
	flag.Float64Var(&passThreshold, "pass-threshold", 0.75, "Minimum validation score GEPA should treat as a passing candidate")
	flag.Float64Var(&acceptedCaseWeight, "accepted-case-weight", defaultEvaluatorConfig.AcceptedCaseWeight, "Score multiplier for accepted benchmark cases in the GEPA objective")
	flag.Float64Var(&matchedScoreFloor, "matched-score-floor", defaultEvaluatorConfig.MatchedScoreFloor, "Minimum raw score for matched accepted cases before accepted-case weighting")
	flag.IntVar(&maxCasesPerRun, "max-cases-per-run", 0, "Optional cap on the number of benchmark cases loaded from each suite")
	flag.IntVar(&maxFilesPerCase, "max-files-per-case", 1, "Maximum files per benchmark case; review suite cases are single-file")
	flag.IntVar(&maxChunksPerCase, "max-chunks-per-case", 0, "Optional cap on chunk prompts evaluated per benchmark case")
	flag.Parse()

	if len(suitePaths) == 0 {
		suitePaths = suiteListFlag{defaultLocalReviewSuitePath}
	}

	ctx := context.Background()
	logger := configureLogger(verbose)
	logging.SetLogger(logger)
	llms.EnsureFactory()

	studentModel, teacherModel, err := resolveOptimizationModels(modelSpec, teacherModelSpec, modelProvider, modelName, modelCfg, apiKey, baseURL, teacherBaseURL)
	if err != nil {
		fatalf("resolve model configuration: %v", err)
	}

	studentLLM, err := util.LoadLLMFromModelConfig(ctx, studentModel.Config, studentModel.ID)
	if err != nil {
		fatalf("configure default LLM: %v", err)
	}
	core.GlobalConfig.DefaultLLM = studentLLM

	teacherLLM := studentLLM
	if strings.TrimSpace(teacherModelSpec) != "" {
		teacherLLM, err = util.LoadLLMFromModelConfig(ctx, teacherModel.Config, teacherModel.ID)
		if err != nil {
			fatalf("configure teacher LLM: %v", err)
		}
	}
	core.GlobalConfig.TeacherLLM = teacherLLM

	if maxFilesPerCase > 0 && maxFilesPerCase < 1 {
		fatalf("max-files-per-case must be at least 1")
	}
	if passThreshold <= 0 || passThreshold > 1 {
		fatalf("pass-threshold must be in the range (0, 1]")
	}
	if acceptedCaseWeight <= 0 {
		fatalf("accepted-case-weight must be greater than 0")
	}
	if matchedScoreFloor < 0 || matchedScoreFloor > 1 {
		fatalf("matched-score-floor must be in the range [0, 1]")
	}
	if teacherDemoCount < 0 {
		fatalf("teacher-demo-count must be greater than or equal to 0")
	}

	suites, trainingExamples, validationExamples, err := loadReviewSuites(suitePaths, validationSplit, maxCasesPerRun)
	if err != nil {
		fatalf("load benchmark suites: %v", err)
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
		skillDomain = review.DefaultReviewSkillDomain
	}

	baseArtifacts, err := review.LoadConfiguredReviewArtifacts(reviewArtifacts)
	if err != nil {
		fatalf("load base review artifacts: %v", err)
	}
	currentSkill, err := store.Best(ctx, skillDomain)
	if err != nil {
		fatalf("load current review skill: %v", err)
	}

	seedArtifacts := baseArtifacts.Clone()
	if seedArtifacts.Text == nil {
		seedArtifacts.Text = make(map[optimize.ArtifactKey]string)
	}
	seedSkillVersion := 0
	if currentSkill != nil {
		seedArtifacts.Text[optimize.ArtifactSkillPack] = strings.TrimSpace(currentSkill.Content)
		seedSkillVersion = currentSkill.Version
	}
	seedArtifacts = review.EnsureReviewOptimizationSeedArtifacts(seedArtifacts)
	if strings.TrimSpace(teacherTracePath) != "" {
		traceReport, err := review.LoadReviewTeacherTraceReport(teacherTracePath)
		if err != nil {
			fatalf("load teacher traces: %v", err)
		}
		if demos := review.SelectTopTeacherDemos(traceReport.Traces, teacherDemoCount); demos != "" {
			seedArtifacts.Text[review.ArtifactFewShotDemos] = demos
		}
	}

	if estimateCost {
		printRunEstimate(trainingExamples, validationExamples, populationSize, generations, maxChunksPerCase)
		return
	}

	studentRuntimeLLM := core.GetDefaultLLM()
	if studentRuntimeLLM == nil {
		fatalf("default LLM is not configured")
	}

	evaluator := review.NewReviewBenchmarkEvaluator(review.ReviewBenchmarkEvaluatorConfig{
		LineSlack:            defaultEvaluatorConfig.LineSlack,
		FalsePositivePenalty: defaultEvaluatorConfig.FalsePositivePenalty,
		DuplicatePenalty:     defaultEvaluatorConfig.DuplicatePenalty,
		OffHunkPenalty:       defaultEvaluatorConfig.OffHunkPenalty,
		NegativeCasePenalty:  defaultEvaluatorConfig.NegativeCasePenalty,
		AcceptedCaseWeight:   acceptedCaseWeight,
		MatchedScoreFloor:    matchedScoreFloor,
	})
	baselineSuiteScores, baselineScore, baselineCaseReports, err := evaluateSuiteScores(ctx, evaluator, review.NewReviewBenchmarkAgent(studentRuntimeLLM, logger, seedArtifacts, maxChunksPerCase), suites)
	if err != nil {
		fatalf("evaluate baseline validation examples: %v", err)
	}

	artifactKeys := []optimize.ArtifactKey{optimize.ArtifactSkillPack}
	if optimizeDemos && strings.TrimSpace(seedArtifacts.Text[review.ArtifactFewShotDemos]) != "" {
		artifactKeys = append(artifactKeys, review.ArtifactFewShotDemos)
	}

	var (
		bestArtifacts   optimize.AgentArtifacts
		bestCandidateID string
	)
	if replayOnly {
		bestArtifacts, err = replaySavedReviewProgram(resolvedArtifactPath, seedArtifacts)
		if err != nil {
			fatalf("replay optimized review program: %v", err)
		}
	} else {
		if err := ensureParentDir(resolvedArtifactPath); err != nil {
			fatalf("create artifact directory: %v", err)
		}

		workflow, err := optimize.RunGEPAWorkflow(ctx, review.NewReviewBenchmarkAgent(studentRuntimeLLM, logger, seedArtifacts, maxChunksPerCase), optimize.GEPAWorkflowRequest{
			Evaluator:          evaluator,
			TrainingExamples:   trainingExamples,
			ValidationExamples: validationExamples,
			BaselineExamples:   validationExamples,
			ReplayExamples:     validationExamples,
			PassThreshold:      passThreshold,
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
				PassThreshold:       passThreshold,
				ArtifactKeys:        artifactKeys,
				PrimaryArtifact:     optimize.ArtifactSkillPack,
				MaxMetricCalls:      maxMetricCalls,
				ScoreThreshold:      scoreThreshold,
				MaxRuntime:          maxRuntime,
			},
		})
		if err != nil {
			fatalf("optimize review skill: %v", err)
		}

		bestArtifacts = workflow.Optimization.BestArtifacts.Clone()
		if workflow.Optimization.BestCandidate != nil {
			bestCandidateID = workflow.Optimization.BestCandidate.ID
		}
	}

	bestValidationScore := baselineScore
	bestSuiteScores := baselineSuiteScores
	bestCaseReports := baselineCaseReports
	if len(bestArtifacts.Text) > 0 {
		bestSuiteScores, bestValidationScore, bestCaseReports, err = evaluateSuiteScores(ctx, evaluator, review.NewReviewBenchmarkAgent(studentRuntimeLLM, logger, bestArtifacts, maxChunksPerCase), suites)
		if err != nil {
			fatalf("evaluate optimized validation examples: %v", err)
		}
	}

	validationBySuite := make(map[string]suiteMetrics, len(suites))
	validationCaseReports := make(map[string]suiteCaseReports, len(suites))
	for _, suite := range suites {
		validationBySuite[suite.Path] = suiteMetrics{
			TrainingExampleCount:   len(suite.TrainingExamples),
			ValidationExampleCount: len(suite.ValidationExamples),
			BaselineValidation:     baselineSuiteScores[suite.Path],
			BestValidation:         bestSuiteScores[suite.Path],
		}
		validationCaseReports[suite.Path] = suiteCaseReports{
			Baseline: append([]validationCaseReport(nil), baselineCaseReports[suite.Path]...),
			Best:     append([]validationCaseReport(nil), bestCaseReports[suite.Path]...),
		}
	}

	checkpoint := reviewOptimizationCheckpoint{
		WrittenAt:              time.Now().UTC(),
		SuitePath:              suites[0].Path,
		SuitePaths:             append([]string(nil), suitePaths...),
		ModelID:                string(studentModel.ID),
		TeacherModelID:         string(teacherModel.ID),
		ArtifactPath:           resolvedArtifactPath,
		AcceptedCaseWeight:     acceptedCaseWeight,
		MatchedScoreFloor:      matchedScoreFloor,
		TrainingExampleCount:   len(trainingExamples),
		ValidationExampleCount: len(validationExamples),
		SeedSkillVersion:       seedSkillVersion,
		BaselineValidation:     baselineScore,
		BestValidation:         bestValidationScore,
		ValidationBySuite:      validationBySuite,
		ValidationCaseReports:  validationCaseReports,
		ReplayOnly:             replayOnly,
		BestArtifacts:          bestArtifacts,
	}
	checkpoint.BestCandidateID = bestCandidateID

	bestOverlay := strings.TrimSpace(bestArtifacts.Text[optimize.ArtifactSkillPack])
	improved := bestValidationScore > baselineScore+1e-9
	improvedAllSuites := suitesImproved(validationBySuite)
	changedOverlay := currentSkill == nil || strings.TrimSpace(currentSkill.Content) != bestOverlay

	switch {
	case dryRun:
		logger.Info(ctx, "Dry run enabled; skipping review skill publication")
	case !publishIfImproved:
		logger.Info(ctx, "publish-if-improved disabled; skipping review skill publication")
	case bestOverlay == "":
		logger.Info(ctx, "Skipping review skill publication because the optimized overlay is empty")
	case !improved:
		logger.Info(ctx, "Skipping review skill publication because validation did not improve (baseline=%.4f best=%.4f)", baselineScore, bestValidationScore)
	case !improvedAllSuites:
		logger.Info(ctx, "Skipping review skill publication because at least one suite did not improve")
	case !changedOverlay:
		logger.Info(ctx, "Skipping review skill publication because the optimized overlay matches the current persisted skill")
	default:
		nextVersion := seedSkillVersion + 1
		if err := store.Save(ctx, skills.Skill{
			Name:    "review-gepa",
			Domain:  skillDomain,
			Content: bestOverlay,
			Version: nextVersion,
			Metadata: map[string]string{
				"baseline_validation_score": fmt.Sprintf("%.6f", baselineScore),
				"best_validation_score":     fmt.Sprintf("%.6f", bestValidationScore),
				"model_id":                  string(studentModel.ID),
				"teacher_model_id":          string(teacherModel.ID),
				"suite_path":                suites[0].Path,
				"suite_paths":               strings.Join(checkpoint.SuitePaths, ","),
			},
		}); err != nil {
			fatalf("publish optimized review skill: %v", err)
		}
		checkpoint.Published = true
		checkpoint.PublishedVersion = nextVersion
		logger.Info(ctx, "Published review skill domain=%q version=%d", skillDomain, nextVersion)
	}

	if err := writeCheckpoint(outputPath, checkpoint); err != nil {
		fatalf("write checkpoint: %v", err)
	}

	fmt.Printf("Review GEPA optimization complete\n")
	fmt.Printf("Model:                 %s\n", studentModel.ID)
	fmt.Printf("Teacher model:         %s\n", teacherModel.ID)
	fmt.Printf("Accepted case weight:  %.2f\n", acceptedCaseWeight)
	fmt.Printf("Matched score floor:   %.2f\n", matchedScoreFloor)
	fmt.Printf("Training examples:     %d\n", len(trainingExamples))
	fmt.Printf("Validation examples:   %d\n", len(validationExamples))
	fmt.Printf("Baseline validation:   %.4f\n", baselineScore)
	fmt.Printf("Best validation:       %.4f\n", bestValidationScore)
	fmt.Printf("Replay only:           %t\n", replayOnly)
	for _, suite := range suites {
		metrics := validationBySuite[suite.Path]
		fmt.Printf("Suite:                 %s\n", suite.Path)
		fmt.Printf("  Training examples:   %d\n", metrics.TrainingExampleCount)
		fmt.Printf("  Validation examples: %d\n", metrics.ValidationExampleCount)
		fmt.Printf("  Baseline validation: %.4f\n", metrics.BaselineValidation)
		fmt.Printf("  Best validation:     %.4f\n", metrics.BestValidation)
	}
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

func resolveOptimizationModels(modelSpec, teacherModelSpec, modelProvider, modelName, modelCfg, apiKey, baseURL, teacherBaseURL string) (configuredModel, configuredModel, error) {
	studentModel, err := resolveConfiguredModel(modelSpec, modelProvider, modelName, modelCfg, apiKey, baseURL)
	if err != nil {
		return configuredModel{}, configuredModel{}, err
	}

	teacherModelSpec = strings.TrimSpace(teacherModelSpec)
	if teacherModelSpec == "" {
		return studentModel, studentModel, nil
	}

	teacherProvider, teacherName, teacherCfg := util.ParseModelString(teacherModelSpec)
	if teacherProvider == "" {
		return configuredModel{}, configuredModel{}, fmt.Errorf("invalid teacher model specification %q", teacherModelSpec)
	}

	teacherModel, err := resolveConfiguredModel("", teacherProvider, teacherName, teacherCfg, apiKey, teacherBaseURL)
	if err != nil {
		return configuredModel{}, configuredModel{}, fmt.Errorf("teacher model: %w", err)
	}

	return studentModel, teacherModel, nil
}

func resolveConfiguredModel(modelSpec, modelProvider, modelName, modelCfg, apiKey, baseURL string) (configuredModel, error) {
	modelSpec = strings.TrimSpace(modelSpec)
	if modelSpec != "" {
		if provider, name, cfg := util.ParseModelString(modelSpec); provider != "" {
			modelProvider = provider
			modelName = name
			modelCfg = cfg
		}
	}

	modelConfig := &util.ModelConfig{
		ModelProvider: modelProvider,
		ModelName:     modelName,
		ModelConfig:   modelCfg,
		APIKey:        apiKey,
		BaseURL:       strings.TrimSpace(baseURL),
	}
	if err := util.ValidateModelConfig(modelConfig); err != nil {
		return configuredModel{}, err
	}

	return configuredModel{
		Config: modelConfig,
		ID:     util.ConstructModelID(modelConfig),
	}, nil
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

func loadReviewSuites(paths []string, validationSplit float64, maxCasesPerRun int) ([]reviewSuite, []optimize.AgentExample, []optimize.AgentExample, error) {
	loadedSuites, training, validation, err := review.LoadBenchmarkSuites(paths, validationSplit, maxCasesPerRun)
	if err != nil {
		return nil, nil, nil, err
	}
	suites := make([]reviewSuite, 0, len(loadedSuites))
	for _, suite := range loadedSuites {
		suites = append(suites, reviewSuite{
			Path:               suite.Path,
			TrainingExamples:   suite.TrainingExamples,
			ValidationExamples: suite.ValidationExamples,
		})
	}
	return suites, training, validation, nil
}

func splitAgentExamples(examples []optimize.AgentExample, validationSplit float64) ([]optimize.AgentExample, []optimize.AgentExample, error) {
	return review.SplitBenchmarkExamples(examples, validationSplit)
}

func benchmarkExampleLabel(example optimize.AgentExample) string {
	return review.BenchmarkExampleLabel(example)
}

func evaluateSuiteScores(ctx context.Context, evaluator optimize.AgentEvaluator, agent optimize.OptimizableAgent, suites []reviewSuite) (map[string]float64, float64, map[string][]validationCaseReport, error) {
	if len(suites) == 0 {
		return nil, 0, nil, fmt.Errorf("at least one suite is required")
	}
	scores := make(map[string]float64, len(suites))
	caseReports := make(map[string][]validationCaseReport, len(suites))
	var totalScore float64
	var totalExamples int

	for _, suite := range suites {
		score, reports, err := evaluateExamples(ctx, evaluator, agent, suite.ValidationExamples)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("evaluate suite %q: %w", suite.Path, err)
		}
		scores[suite.Path] = score
		caseReports[suite.Path] = reports
		totalScore += score * float64(len(suite.ValidationExamples))
		totalExamples += len(suite.ValidationExamples)
	}
	if totalExamples == 0 {
		return nil, 0, nil, fmt.Errorf("at least one validation example is required")
	}
	return scores, totalScore / float64(totalExamples), caseReports, nil
}

func suitesImproved(metrics map[string]suiteMetrics) bool {
	for _, suiteMetric := range metrics {
		if suiteMetric.BestValidation <= suiteMetric.BaselineValidation+1e-9 {
			return false
		}
	}
	return true
}

func evaluateExamples(ctx context.Context, evaluator optimize.AgentEvaluator, agent optimize.OptimizableAgent, examples []optimize.AgentExample) (float64, []validationCaseReport, error) {
	if len(examples) == 0 {
		return 0, nil, fmt.Errorf("at least one validation example is required")
	}
	var total float64
	reports := make([]validationCaseReport, 0, len(examples))
	for _, example := range examples {
		result, err := evaluator.Evaluate(ctx, agent, example)
		if err != nil {
			return 0, nil, fmt.Errorf("evaluate example %q: %w", example.ID, err)
		}
		total += result.Score
		reports = append(reports, validationCaseReportFromEvalResult(example, result))
	}
	return total / float64(len(examples)), reports, nil
}

func validationCaseReportFromEvalResult(example optimize.AgentExample, result *optimize.EvalResult) validationCaseReport {
	report := validationCaseReport{
		ID:    example.ID,
		Label: benchmarkExampleLabel(example),
	}
	if benchmarkCase, err := review.ReviewBenchmarkCaseFromAgentExample(example); err == nil {
		report.FilePath = strings.TrimSpace(benchmarkCase.FilePath)
		report.Line = benchmarkCase.Line
	}
	if result == nil {
		return report
	}
	report.Score = result.Score
	if result.SideInfo == nil {
		return report
	}
	report.LatencyMS = result.SideInfo.LatencyMS
	diagnostics := result.SideInfo.Diagnostics
	report.RawScore = diagnosticFloat(diagnostics, "raw_score")
	report.CaseWeight = diagnosticFloatDefault(diagnostics, "case_weight", 1)
	report.CommentCount = diagnosticInt(diagnostics, "comment_count")
	report.RawCandidates = diagnosticInt(diagnostics, "raw_candidates")
	report.PreVerificationCount = diagnosticInt(diagnostics, "pre_verification_count")
	report.SkippedAfterFilter = diagnosticInt(diagnostics, "skipped_after_filter")
	report.FilterDropReasons = diagnosticIntMap(diagnostics, "filter_drop_reasons")
	report.FilterRejections = diagnosticFilterRejections(diagnostics, "filter_rejections")
	report.TotalChunks = diagnosticInt(diagnostics, "total_chunks")
	report.SelectedChunks = diagnosticInt(diagnostics, "selected_chunks")
	report.Matched = diagnosticBool(diagnostics, "matched")
	report.MatchedComment = diagnosticString(diagnostics, "matched_comment")
	report.VerificationEnabled = diagnosticBool(diagnostics, "verification_enabled")
	report.VerificationDropped = diagnosticInt(diagnostics, "verification_dropped")
	report.VerificationDropReasons = diagnosticIntMap(diagnostics, "verification_drop_reasons")
	report.VerificationRejections = diagnosticVerificationRejections(diagnostics, "verification_rejections")
	report.EvaluationError = diagnosticString(diagnostics, "evaluation_error")
	return report
}

func diagnosticInt(diagnostics map[string]interface{}, key string) int {
	switch value := diagnostics[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func diagnosticBool(diagnostics map[string]interface{}, key string) bool {
	value, _ := diagnostics[key].(bool)
	return value
}

func diagnosticFloat(diagnostics map[string]interface{}, key string) float64 {
	switch value := diagnostics[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int32:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func diagnosticFloatDefault(diagnostics map[string]interface{}, key string, fallback float64) float64 {
	if value := diagnosticFloat(diagnostics, key); value != 0 {
		return value
	}
	return fallback
}

func diagnosticString(diagnostics map[string]interface{}, key string) string {
	value, _ := diagnostics[key].(string)
	return value
}

func diagnosticIntMap(diagnostics map[string]interface{}, key string) map[string]int {
	raw := diagnostics[key]
	switch value := raw.(type) {
	case map[string]int:
		if len(value) == 0 {
			return nil
		}
		return value
	case map[string]interface{}:
		if len(value) == 0 {
			return nil
		}
		out := make(map[string]int, len(value))
		for k, v := range value {
			switch n := v.(type) {
			case int:
				out[k] = n
			case int32:
				out[k] = int(n)
			case int64:
				out[k] = int(n)
			case float64:
				out[k] = int(n)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func diagnosticVerificationRejections(diagnostics map[string]interface{}, key string) []review.ReviewVerificationRejection {
	raw, ok := diagnostics[key]
	if !ok || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var rejections []review.ReviewVerificationRejection
	if err := json.Unmarshal(data, &rejections); err != nil || len(rejections) == 0 {
		return nil
	}
	return rejections
}

func diagnosticFilterRejections(diagnostics map[string]interface{}, key string) []review.ReviewFilterRejection {
	raw, ok := diagnostics[key]
	if !ok || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var rejections []review.ReviewFilterRejection
	if err := json.Unmarshal(data, &rejections); err != nil || len(rejections) == 0 {
		return nil
	}
	return rejections
}

func replaySavedReviewProgram(artifactPath string, seedArtifacts optimize.AgentArtifacts) (optimize.AgentArtifacts, error) {
	if strings.TrimSpace(artifactPath) == "" {
		return optimize.AgentArtifacts{}, fmt.Errorf("artifact path is required to restore an optimized program")
	}

	program, err := optimize.ReadOptimizedAgentProgram(artifactPath)
	if err != nil {
		return optimize.AgentArtifacts{}, fmt.Errorf("read optimized program: %w", err)
	}

	replayAgent := review.NewReviewBenchmarkAgent(nil, nil, seedArtifacts, 0)
	if err := optimize.ApplyOptimizedAgentProgram(replayAgent, program); err != nil {
		return optimize.AgentArtifacts{}, fmt.Errorf("apply optimized program: %w", err)
	}
	return replayAgent.GetArtifacts(), nil
}

func ensureParentDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is required")
	}
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func printRunEstimate(trainingExamples, validationExamples []optimize.AgentExample, populationSize, generations, maxChunksPerCase int) {
	optimizationEvaluations := populationSize * generations * len(trainingExamples)
	baselineEvaluations := len(validationExamples)
	totalEvaluations := optimizationEvaluations + baselineEvaluations
	assumedMaxChunks := maxChunksPerCase
	if assumedMaxChunks <= 0 {
		assumedMaxChunks = 6
	}
	lowLLMCalls := totalEvaluations
	highLLMCalls := totalEvaluations * assumedMaxChunks

	fmt.Printf("Review GEPA run estimate\n")
	fmt.Printf("Training examples:   %d\n", len(trainingExamples))
	fmt.Printf("Validation examples: %d\n", len(validationExamples))
	fmt.Printf("Population:          %d\n", populationSize)
	fmt.Printf("Generations:         %d\n", generations)
	fmt.Printf("Estimated evaluations: %d\n", totalEvaluations)
	fmt.Printf("Estimated LLM-call range: %d-%d (assuming 1 to %d chunk prompts per evaluation)\n", lowLLMCalls, highLLMCalls, assumedMaxChunks)
	fmt.Printf("Note: this is a run-size estimate, not a provider-billed dollar quote.\n")
}

func writeCheckpoint(path string, checkpoint reviewOptimizationCheckpoint) error {
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
