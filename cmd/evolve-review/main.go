package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/review"
	"github.com/XiaoConstantine/maestro/internal/util"
)

type stringListFlag []string

type configuredModel struct {
	Config *util.ModelConfig
	ID     core.ModelID
}

func (f *stringListFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
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
		suites                       stringListFlag
		searchSuites                 stringListFlag
		protectedSuites              stringListFlag
		stateDir                     string
		inboxDir                     string
		archiveDir                   string
		baseArtifacts                string
		skillStorePath               string
		skillDomain                  string
		apiKey                       string
		modelSpec                    string
		teacherModelSpec             string
		baseURL                      string
		teacherBaseURL               string
		modelProvider                string
		modelName                    string
		modelCfg                     string
		teacherTracePath             string
		teacherDemoCount             int
		optimizeDemos                bool
		verbose                      bool
		dryRun                       bool
		resetCircuit                 bool
		minNewExamples               int
		maxRuntime                   time.Duration
		retentionRuns                int
		retentionDays                int
		failureThreshold             int
		populationSize               int
		generations                  int
		reflectionFreq               int
		evalConcurrency              int
		evalTemperature              float64
		validationSplit              float64
		searchBatchSize              int
		stagnationLimit              int
		validationFreq               int
		maxMetricCalls               int
		scoreThreshold               float64
		passThreshold                float64
		regressionTolerance          float64
		protectedRegressionTolerance float64
		acceptedCaseWeight           float64
		matchedScoreFloor            float64
		maxCasesPerRun               int
		maxSearchCasesPerSuite       int
		maxChunksPerCase             int
	)
	defaultEvaluator := review.DefaultReviewBenchmarkEvaluatorConfig()

	flag.Var(&suites, "suite", "Path to a baseline review benchmark suite JSON; repeat or comma-separate")
	flag.Var(&searchSuites, "search-suite", "Optional path to a cheaper GEPA search suite JSON; repeat or comma-separate. Defaults to --suite when omitted.")
	flag.Var(&protectedSuites, "protected-suite", "Path to a protected validation suite JSON; repeat or comma-separate")
	flag.StringVar(&stateDir, "state-dir", "~/.maestro/evolution/review", "Path to the review evolution state directory")
	flag.StringVar(&inboxDir, "inbox-dir", "", "Optional inbox directory override; defaults to <state-dir>/datasets/inbox")
	flag.StringVar(&archiveDir, "archive-dir", "", "Optional archive directory override; defaults to <state-dir>/datasets/archive")
	flag.StringVar(&baseArtifacts, "base-review-artifacts", os.Getenv("MAESTRO_REVIEW_ARTIFACTS"), "Optional seed review artifacts path used when no promoted current artifact exists")
	flag.StringVar(&skillStorePath, "store", "~/.maestro/skills.json", "Path to the persisted review skill store JSON")
	flag.StringVar(&skillDomain, "domain", review.DefaultReviewSkillDomain, "Skill domain to promote on successful runs")
	flag.StringVar(&apiKey, "api-key", "", "API key for external model providers")
	flag.StringVar(&modelSpec, "model", "", `Full model specification (e.g. "google:gemini-3.0-pro" or "openai:gpt-5.4-mini")`)
	flag.StringVar(&teacherModelSpec, "teacher-model", "", `Optional full teacher model specification used for GEPA reflection`)
	flag.StringVar(&baseURL, "base-url", "", "Optional base URL override for the primary model provider")
	flag.StringVar(&teacherBaseURL, "teacher-base-url", "", "Optional base URL override for the teacher model provider")
	flag.StringVar(&modelProvider, "provider", "google", "Model provider (google, anthropic, openai, ollama, llamacpp)")
	flag.StringVar(&modelName, "model-name", "gemini-3.0-pro", "Model name")
	flag.StringVar(&modelCfg, "model-config", "", "Additional model configuration")
	flag.StringVar(&teacherTracePath, "teacher-traces", "", "Optional path to generated teacher traces used to seed few-shot demos")
	flag.IntVar(&teacherDemoCount, "teacher-demo-count", 3, "Number of top teacher demos to seed when --teacher-traces is provided")
	flag.BoolVar(&optimizeDemos, "optimize-few-shot-demos", false, "Allow GEPA to mutate the few-shot demos artifact as well as the skill pack")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&dryRun, "dry-run", false, "Run the full loop but never promote the result")
	flag.BoolVar(&resetCircuit, "reset-circuit", false, "Reset the local circuit breaker and exit")
	flag.IntVar(&minNewExamples, "min-new-examples", 25, "Minimum eligible inbox examples required before a run starts")
	flag.DurationVar(&maxRuntime, "max-runtime", 30*time.Minute, "Wall-clock limit for one evolution run")
	flag.IntVar(&retentionRuns, "retention-runs", 20, "Number of historical run/candidate directories to keep")
	flag.IntVar(&retentionDays, "retention-days", 30, "Maximum age in days for historical run/candidate directories")
	flag.IntVar(&failureThreshold, "failure-threshold", 3, "Open the circuit after this many consecutive failed promotions")
	flag.IntVar(&populationSize, "population", 4, "GEPA population size")
	flag.IntVar(&generations, "generations", 2, "GEPA generation count")
	flag.IntVar(&reflectionFreq, "reflection-freq", 1, "GEPA reflection frequency")
	flag.IntVar(&evalConcurrency, "eval-concurrency", 1, "Concurrent GEPA evaluations")
	flag.Float64Var(&evalTemperature, "eval-temperature", 0, "Temperature override for the review evaluation/runtime model")
	flag.Float64Var(&validationSplit, "validation-split", 0.25, "Validation split for benchmark examples")
	flag.IntVar(&searchBatchSize, "search-batch-size", 4, "GEPA search batch size")
	flag.IntVar(&stagnationLimit, "stagnation-limit", 40, "GEPA stagnation limit")
	flag.IntVar(&validationFreq, "validation-frequency", 1, "Run validation every N generations")
	flag.IntVar(&maxMetricCalls, "max-metric-calls", 0, "Optional cap on GEPA metric evaluations; 0 disables the cap")
	flag.Float64Var(&scoreThreshold, "score-threshold", 0, "Optional early-stop threshold for validation score; 0 disables it")
	flag.Float64Var(&passThreshold, "pass-threshold", 0.75, "Minimum validation score GEPA should treat as a passing candidate")
	flag.Float64Var(&regressionTolerance, "regression-tolerance", 0.015, "Allow replay validation to regress by at most this amount before blocking promotion")
	flag.Float64Var(&protectedRegressionTolerance, "protected-regression-tolerance", 0, "Allow protected-suite replay validation to regress by at most this amount before blocking promotion")
	flag.Float64Var(&acceptedCaseWeight, "accepted-case-weight", defaultEvaluator.AcceptedCaseWeight, "Score multiplier for accepted benchmark cases")
	flag.Float64Var(&matchedScoreFloor, "matched-score-floor", defaultEvaluator.MatchedScoreFloor, "Minimum raw score for matched accepted cases")
	flag.IntVar(&maxCasesPerRun, "max-cases-per-run", 0, "Optional cap on the number of benchmark cases loaded per suite")
	flag.IntVar(&maxSearchCasesPerSuite, "max-search-cases-per-suite", 0, "Optional cap on the number of benchmark cases loaded per search suite; defaults to --max-cases-per-run")
	flag.IntVar(&maxChunksPerCase, "max-chunks-per-case", 0, "Optional cap on chunk prompts evaluated per benchmark case")
	flag.Parse()

	logger := configureLogger(verbose)
	logging.SetLogger(logger)
	llms.EnsureFactory()

	resolvedStateDir, err := expandPath(stateDir)
	if err != nil {
		fatalf("resolve state dir: %v", err)
	}
	if resetCircuit {
		if err := review.ResetEvolveReviewCircuit(resolvedStateDir); err != nil {
			fatalf("reset circuit: %v", err)
		}
		fmt.Printf("reset circuit at %s\n", resolvedStateDir)
		return
	}

	ctx := context.Background()
	studentModel, teacherModel, err := resolveOptimizationModels(modelSpec, teacherModelSpec, modelProvider, modelName, modelCfg, apiKey, baseURL, teacherBaseURL)
	if err != nil {
		fatalf("resolve model configuration: %v", err)
	}

	studentLLM, err := util.LoadLLMFromModelConfig(ctx, studentModel.Config, studentModel.ID)
	if err != nil {
		fatalf("configure student LLM: %v", err)
	}
	studentLLM = util.NewFixedGenerateOptionsLLM(studentLLM, core.WithTemperature(evalTemperature))
	teacherLLM := studentLLM
	if strings.TrimSpace(teacherModelSpec) != "" {
		teacherLLM, err = util.LoadLLMFromModelConfig(ctx, teacherModel.Config, teacherModel.ID)
		if err != nil {
			fatalf("configure teacher LLM: %v", err)
		}
	}
	core.GlobalConfig.DefaultLLM = studentLLM
	core.GlobalConfig.TeacherLLM = teacherLLM

	result, err := review.RunEvolveReview(ctx, review.EvolveReviewConfig{
		Logger:                       logger,
		StateDir:                     resolvedStateDir,
		SuitePaths:                   append([]string(nil), suites...),
		SearchSuitePaths:             append([]string(nil), searchSuites...),
		ProtectedSuitePaths:          append([]string(nil), protectedSuites...),
		InboxDir:                     inboxDir,
		ArchiveDir:                   archiveDir,
		BaseReviewArtifactsPath:      baseArtifacts,
		SkillStorePath:               skillStorePath,
		SkillDomain:                  skillDomain,
		StudentLLM:                   studentLLM,
		TeacherLLM:                   teacherLLM,
		StudentModelID:               studentModel.ID,
		TeacherModelID:               teacherModel.ID,
		MinNewExamples:               minNewExamples,
		MaxRuntime:                   maxRuntime,
		RetentionRuns:                retentionRuns,
		RetentionDays:                retentionDays,
		FailureThreshold:             failureThreshold,
		ValidationSplit:              validationSplit,
		MaxCasesPerRun:               maxCasesPerRun,
		MaxSearchCasesPerSuite:       maxSearchCasesPerSuite,
		MaxChunksPerCase:             maxChunksPerCase,
		TeacherTracePath:             teacherTracePath,
		TeacherDemoCount:             teacherDemoCount,
		OptimizeDemos:                optimizeDemos,
		DryRun:                       dryRun,
		PopulationSize:               populationSize,
		MaxGenerations:               generations,
		ReflectionFreq:               reflectionFreq,
		EvalConcurrency:              evalConcurrency,
		SearchBatchSize:              searchBatchSize,
		StagnationLimit:              stagnationLimit,
		ValidationFrequency:          validationFreq,
		MaxMetricCalls:               maxMetricCalls,
		ScoreThreshold:               scoreThreshold,
		PassThreshold:                passThreshold,
		RegressionTolerance:          regressionTolerance,
		ProtectedRegressionTolerance: protectedRegressionTolerance,
		AcceptedCaseWeight:           acceptedCaseWeight,
		MatchedScoreFloor:            matchedScoreFloor,
	})
	if err != nil {
		fatalf("evolve review: %v", err)
	}

	fmt.Printf("review evolution complete\n")
	fmt.Printf("decision:              %s\n", result.Decision)
	fmt.Printf("promoted:              %t\n", result.Promoted)
	fmt.Printf("circuit open:          %t\n", result.CircuitOpen)
	fmt.Printf("new examples:          %d\n", result.NewExampleCount)
	fmt.Printf("baseline validation:   %.4f\n", result.BaselineValidation)
	fmt.Printf("replay validation:     %.4f\n", result.ReplayValidation)
	if result.RunID != "" {
		fmt.Printf("run id:                %s\n", result.RunID)
	}
	if result.CandidateArtifactPath != "" {
		fmt.Printf("candidate artifact:    %s\n", result.CandidateArtifactPath)
	}
	if result.CurrentArtifactPath != "" {
		fmt.Printf("current artifact:      %s\n", result.CurrentArtifactPath)
	}
	if result.ReportPath != "" {
		fmt.Printf("report:                %s\n", result.ReportPath)
	}
}

func configureLogger(verbose bool) *logging.Logger {
	level := logging.INFO
	if verbose {
		level = logging.DEBUG
	}
	return logging.NewLogger(logging.Config{
		Severity: level,
		Outputs:  []logging.Output{logging.NewConsoleOutput(true, logging.WithColor(true))},
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
		return "", fmt.Errorf("path is required")
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Clean(path), nil
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
