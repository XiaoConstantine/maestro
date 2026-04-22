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

const (
	defaultLocalReviewTraceOutputPath = "/tmp/review_teacher_traces.json"
	defaultLocalReviewSuitePath       = "~/.maestro/review/corpora/rsc-golang-org/review_go_suite.json"
)

type suiteListFlag []string

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
		apiKey             string
		modelSpec          string
		baseURL            string
		modelProvider      string
		modelName          string
		modelCfg           string
		reviewArtifacts    string
		skillStorePath     string
		skillDomain        string
		verbose            bool
		validationSplit    float64
		maxCasesPerRun     int
		maxChunksPerCase   int
		acceptedCaseWeight float64
		matchedScoreFloor  float64
	)
	defaultEvaluatorConfig := review.DefaultReviewBenchmarkEvaluatorConfig()

	flag.Var(&suitePaths, "suite", "Path to a review benchmark suite JSON; repeat or comma-separate to evaluate multiple reviewer suites")
	flag.StringVar(&outputPath, "out", defaultLocalReviewTraceOutputPath, "Path to write the teacher trace JSON")
	flag.StringVar(&apiKey, "api-key", "", "API key for external model providers")
	flag.StringVar(&modelSpec, "model", "", `Full model specification (e.g. "google:gemini-3-pro-preview")`)
	flag.StringVar(&baseURL, "base-url", "", "Optional base URL override for the configured model; mainly used for local providers like llamacpp")
	flag.StringVar(&modelProvider, "provider", "google", "Model provider (google, anthropic, openai, ollama, llamacpp)")
	flag.StringVar(&modelName, "model-name", "gemini-3.0-pro", "Model name")
	flag.StringVar(&modelCfg, "model-config", "", "Additional model configuration")
	flag.StringVar(&reviewArtifacts, "review-artifacts", os.Getenv("MAESTRO_REVIEW_ARTIFACTS"), "Optional path to base review artifacts JSON")
	flag.StringVar(&skillStorePath, "store", "~/.maestro/skills.json", "Path to the persisted review skill store JSON")
	flag.StringVar(&skillDomain, "domain", review.DefaultReviewSkillDomain, "Skill domain used to seed the review overlay")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.Float64Var(&validationSplit, "validation-split", 0.25, "Validation split for the benchmark suite; traces are generated from the training portion")
	flag.IntVar(&maxCasesPerRun, "max-cases-per-run", 0, "Optional cap on the number of benchmark cases loaded from each suite")
	flag.IntVar(&maxChunksPerCase, "max-chunks-per-case", 0, "Optional cap on chunk prompts evaluated per benchmark case")
	flag.Float64Var(&acceptedCaseWeight, "accepted-case-weight", defaultEvaluatorConfig.AcceptedCaseWeight, "Score multiplier for accepted benchmark cases")
	flag.Float64Var(&matchedScoreFloor, "matched-score-floor", defaultEvaluatorConfig.MatchedScoreFloor, "Minimum raw score for matched accepted cases before weighting")
	flag.Parse()

	if len(suitePaths) == 0 {
		suitePaths = suiteListFlag{defaultLocalReviewSuitePath}
	}

	ctx := context.Background()
	logger := configureLogger(verbose)
	logging.SetLogger(logger)
	llms.EnsureFactory()

	modelConfig, err := resolveConfiguredModel(modelSpec, modelProvider, modelName, modelCfg, apiKey, baseURL)
	if err != nil {
		fatalf("resolve model configuration: %v", err)
	}
	llm, err := util.LoadLLMFromModelConfig(ctx, modelConfig.Config, modelConfig.ID)
	if err != nil {
		fatalf("configure trace-generation LLM: %v", err)
	}
	core.GlobalConfig.DefaultLLM = llm
	core.GlobalConfig.TeacherLLM = llm

	if acceptedCaseWeight <= 0 {
		fatalf("accepted-case-weight must be greater than 0")
	}
	if matchedScoreFloor < 0 || matchedScoreFloor > 1 {
		fatalf("matched-score-floor must be in the range [0, 1]")
	}

	suites, trainingExamples, _, err := review.LoadBenchmarkSuites([]string(suitePaths), validationSplit, maxCasesPerRun)
	if err != nil {
		fatalf("load benchmark suites: %v", err)
	}

	resolvedStorePath, err := expandPath(skillStorePath)
	if err != nil {
		fatalf("resolve skill store path: %v", err)
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
	if currentSkill != nil {
		seedArtifacts.Text[optimize.ArtifactSkillPack] = strings.TrimSpace(currentSkill.Content)
	}
	seedArtifacts = review.EnsureReviewOptimizationSeedArtifacts(seedArtifacts)

	evaluatorConfig := review.ReviewBenchmarkEvaluatorConfig{
		LineSlack:            defaultEvaluatorConfig.LineSlack,
		FalsePositivePenalty: defaultEvaluatorConfig.FalsePositivePenalty,
		DuplicatePenalty:     defaultEvaluatorConfig.DuplicatePenalty,
		OffHunkPenalty:       defaultEvaluatorConfig.OffHunkPenalty,
		NegativeCasePenalty:  defaultEvaluatorConfig.NegativeCasePenalty,
		AcceptedCaseWeight:   acceptedCaseWeight,
		MatchedScoreFloor:    matchedScoreFloor,
	}
	agent := review.NewReviewBenchmarkAgent(llm, logger, seedArtifacts, maxChunksPerCase)

	traces := make([]review.ReviewTeacherTrace, 0, len(trainingExamples))
	for _, example := range trainingExamples {
		benchmarkCase, err := review.ReviewBenchmarkCaseFromAgentExample(example)
		if err != nil {
			fatalf("resolve benchmark case %q: %v", example.ID, err)
		}

		result, execErr := agent.Execute(ctx, map[string]interface{}{
			"benchmark_case": benchmarkCase,
		})
		evaluation, err := review.EvaluateReviewBenchmarkResult(benchmarkCase, result, evaluatorConfig)
		if err != nil {
			fatalf("evaluate teacher trace %q: %v", example.ID, err)
		}

		trace := review.ReviewTeacherTrace{
			CaseID:          example.ID,
			Label:           review.BenchmarkExampleLabel(example),
			FilePath:        strings.TrimSpace(benchmarkCase.FilePath),
			Line:            benchmarkCase.Line,
			InputDiff:       benchmarkCase.Diff,
			ReviewerComment: benchmarkCase.ReviewerComment,
			TeacherComments: append([]review.PRReviewComment(nil), evaluation.Comments...),
			Score:           evaluation.Score,
			RawScore:        evaluation.RawScore,
			CommentCount:    len(evaluation.Comments),
			Matched:         diagnosticBool(evaluation.Diagnostics, "matched"),
			MatchedComment:  diagnosticString(evaluation.Diagnostics, "matched_comment"),
		}
		if execErr != nil {
			trace.EvaluationError = execErr.Error()
		} else {
			trace.EvaluationError = diagnosticString(evaluation.Diagnostics, "evaluation_error")
		}
		traces = append(traces, trace)
	}

	review.SortReviewTeacherTraces(traces)

	report := review.ReviewTeacherTraceReport{
		GeneratedAt:          time.Now().UTC(),
		ModelID:              string(modelConfig.ID),
		SuitePaths:           append([]string(nil), suitePaths...),
		TrainingExampleCount: len(trainingExamples),
		Traces:               traces,
	}

	if err := writeJSON(outputPath, report); err != nil {
		fatalf("write trace output: %v", err)
	}

	fmt.Printf("Review teacher trace generation complete\n")
	fmt.Printf("Model:                 %s\n", modelConfig.ID)
	fmt.Printf("Training examples:     %d\n", len(trainingExamples))
	fmt.Printf("Suites:                %d\n", len(suites))
	fmt.Printf("Output:                %s\n", outputPath)
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

type configuredModel struct {
	Config *util.ModelConfig
	ID     core.ModelID
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
		return fmt.Errorf("marshal output: %w", err)
	}
	if err := os.WriteFile(resolvedPath, data, 0o644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func diagnosticBool(diagnostics map[string]interface{}, key string) bool {
	value, _ := diagnostics[key].(bool)
	return value
}

func diagnosticString(diagnostics map[string]interface{}, key string) string {
	value, _ := diagnostics[key].(string)
	return value
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
