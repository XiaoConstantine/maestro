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

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/orchestration"
	"github.com/XiaoConstantine/maestro/internal/util"
)

func main() {
	var (
		suitePath                    string
		outputPath                   string
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
		caseTimeout                  time.Duration
		agentTimeout                 time.Duration
		protectedRegressionTolerance float64
	)

	flag.StringVar(&suitePath, "suite", "./benchmarks/rlm_overview_suite.json", "Path to the RLM overview benchmark suite JSON")
	flag.StringVar(&outputPath, "output", "./benchmark_results/rlm_overview_benchmark.json", "Path to write the benchmark report JSON")
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
	flag.DurationVar(&caseTimeout, "case-timeout", 0, "Optional timeout per benchmark case")
	flag.DurationVar(&agentTimeout, "agent-timeout", 0, "Override RLM agent timeout; 0 uses Maestro default")
	flag.Float64Var(&protectedRegressionTolerance, "protected-regression-tolerance", 0, "Allowed protected-case score regression before failing the run")
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
