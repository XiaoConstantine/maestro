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
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
	"github.com/XiaoConstantine/maestro/internal/orchestration"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
)

type probeOutput struct {
	Type         orchestration.RequestType   `json:"type"`
	Answer       string                      `json:"answer,omitempty"`
	Metadata     map[string]interface{}      `json:"metadata,omitempty"`
	BudgetStatus *maestrobudget.BudgetStatus `json:"budget_status,omitempty"`
}

type localReviewAgent struct {
	repoPath string
	status   *types.IndexingStatus
}

func (a *localReviewAgent) ReviewPR(context.Context, int, []types.PRReviewTask, types.ConsoleInterface) ([]types.PRReviewComment, error) {
	return nil, errors.New("maestro-probe local review agent does not review PRs")
}

func (a *localReviewAgent) ReviewPRWithChanges(context.Context, int, []types.PRReviewTask, types.ConsoleInterface, *types.PRChanges) ([]types.PRReviewComment, error) {
	return nil, errors.New("maestro-probe local review agent does not review PRs")
}

func (a *localReviewAgent) Stop(context.Context) {}

func (a *localReviewAgent) Metrics(context.Context) types.MetricsCollector {
	return nil
}

func (a *localReviewAgent) ClonedRepoPath() string {
	return a.repoPath
}

func (a *localReviewAgent) WaitForClone(context.Context, time.Duration) string {
	return a.repoPath
}

func (a *localReviewAgent) GetIndexingStatus() *types.IndexingStatus {
	if a.status == nil {
		a.status = types.NewIndexingStatus()
	}
	return a.status
}

func (a *localReviewAgent) Close() error {
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "maestro-probe: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		question                 string
		repoPath                 string
		owner                    string
		repo                     string
		modelSpec                string
		modelProvider            string
		modelName                string
		modelConfig              string
		apiKey                   string
		baseURL                  string
		strategy                 string
		memoryPath               string
		rlmOverviewArtifactsPath string
		rlmTargetedArtifactsPath string
		timeout                  time.Duration
		verbose                  bool
		allowCodingBash          bool
	)

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: maestro-probe --question <question> [flags]\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Runs a Maestro ask or coding request against a local repository and prints response metadata as JSON.\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Use --strategy coding for workspace tools; PR review is not exercised.\n\n")
		flag.PrintDefaults()
	}

	flag.StringVar(&question, "question", "", "Question to ask through Maestro")
	flag.StringVar(&repoPath, "repo-path", ".", "Local repository path Maestro should inspect")
	flag.StringVar(&owner, "owner", "local", "Repository owner metadata")
	flag.StringVar(&repo, "repo", "", "Repository name metadata; defaults to --repo-path basename")
	flag.StringVar(&modelSpec, "model", "", `Full model specification (for example "google:gemini-2.5-pro")`)
	flag.StringVar(&modelProvider, "provider", "google", "Model provider")
	flag.StringVar(&modelName, "model-name", "gemini-2.5-pro", "Model name")
	flag.StringVar(&modelConfig, "model-config", "", "Additional model configuration")
	flag.StringVar(&apiKey, "api-key", "", "API key for external model providers")
	flag.StringVar(&baseURL, "base-url", "", "Optional base URL override")
	flag.StringVar(&strategy, "strategy", "", "Execution strategy: native, rlm, rlm-targeted, or coding")
	flag.StringVar(&memoryPath, "memory-path", "", "Optional Maestro memory path; defaults under /tmp")
	flag.StringVar(&rlmOverviewArtifactsPath, "rlm-overview-artifact", "", "Optional RLM overview optimized program path")
	flag.StringVar(&rlmTargetedArtifactsPath, "rlm-targeted-ask-artifact", "", "Optional RLM targeted ask optimized program path")
	flag.DurationVar(&timeout, "timeout", 5*time.Minute, "Request timeout")
	flag.BoolVar(&verbose, "verbose", false, "Enable debug logging to stderr")
	flag.BoolVar(&allowCodingBash, "allow-coding-bash", false, "Allow unrestricted shell commands for --strategy coding")
	flag.Parse()

	if strings.TrimSpace(question) == "" {
		return errors.New("--question is required")
	}

	resolvedRepoPath, err := filepath.Abs(strings.TrimSpace(repoPath))
	if err != nil {
		return fmt.Errorf("resolve repo path: %w", err)
	}
	info, err := os.Stat(resolvedRepoPath)
	if err != nil {
		return fmt.Errorf("stat repo path %q: %w", resolvedRepoPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("repo path %q is not a directory", resolvedRepoPath)
	}
	if strings.TrimSpace(repo) == "" {
		repo = filepath.Base(resolvedRepoPath)
	}
	if strings.TrimSpace(memoryPath) == "" {
		memoryPath = filepath.Join(os.TempDir(), "maestro-probe", fmt.Sprintf("%s_%s.db", sanitizePathPart(owner), sanitizePathPart(repo)))
	}
	if err := os.MkdirAll(filepath.Dir(memoryPath), 0755); err != nil {
		return fmt.Errorf("create memory directory: %w", err)
	}

	if modelSpec != "" {
		provider, name, cfg := util.ParseModelString(modelSpec)
		if provider != "" {
			modelProvider = provider
		}
		if name != "" {
			modelName = name
		}
		if cfg != "" {
			modelConfig = cfg
		}
	}

	logger := configureLogger(verbose)
	logging.SetLogger(logger)
	ctx, cancel := context.WithTimeout(core.WithExecutionState(context.Background()), timeout)
	defer cancel()

	modelCfg := &util.ModelConfig{
		ModelProvider: modelProvider,
		ModelName:     modelName,
		ModelConfig:   modelConfig,
		APIKey:        apiKey,
		BaseURL:       baseURL,
	}
	if err := util.ValidateModelConfig(modelCfg); err != nil {
		return fmt.Errorf("model config is incorrect: %w", err)
	}
	llms.EnsureFactory()
	modelID := util.ConstructModelID(modelCfg)
	defaultLLM, err := util.LoadLLMFromModelConfig(ctx, modelCfg, modelID)
	if err != nil {
		return fmt.Errorf("failed to configure LLM: %w", err)
	}
	core.GlobalConfig.DefaultLLM = defaultLLM

	previousStrategy, hadStrategy := os.LookupEnv("MAESTRO_FORCE_ASK_STRATEGY")
	strategy = strings.TrimSpace(strategy)
	if strategy != "" && strategy != "coding" {
		if err := os.Setenv("MAESTRO_FORCE_ASK_STRATEGY", strategy); err != nil {
			return fmt.Errorf("set ask strategy: %w", err)
		}
		defer restoreEnv("MAESTRO_FORCE_ASK_STRATEGY", previousStrategy, hadStrategy)
	}

	budgetManager := maestrobudget.NewBudgetManager(maestrobudget.DefaultConfig())
	service, err := orchestration.NewMaestroService(ctx, &orchestration.ServiceConfig{
		MemoryType:                  orchestration.MemoryInMemory,
		MemoryPath:                  memoryPath,
		RLMOverviewArtifactsPath:    rlmOverviewArtifactsPath,
		RLMTargetedAskArtifactsPath: rlmTargetedArtifactsPath,
		Owner:                       owner,
		Repo:                        repo,
		BudgetManager:               budgetManager,
		AllowCodingBash:             allowCodingBash,
	}, nil)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = service.Shutdown(shutdownCtx)
	}()
	service.SetReviewAgent(&localReviewAgent{repoPath: resolvedRepoPath, status: types.NewIndexingStatus()})

	request := orchestration.Request{Type: orchestration.RequestAsk, Question: question}
	if strategy == "coding" {
		request = orchestration.Request{Type: orchestration.RequestCoding, Prompt: question}
	}
	response, err := service.ProcessRequest(ctx, request)
	if err != nil {
		return fmt.Errorf("process %s request: %w", request.Type, err)
	}

	output := probeOutput{
		Type:         response.Type,
		Answer:       response.Answer,
		Metadata:     response.Metadata,
		BudgetStatus: service.BudgetStatus(),
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func configureLogger(verbose bool) *logging.Logger {
	severity := logging.WARN
	if verbose {
		severity = logging.DEBUG
	}
	return logging.NewLogger(logging.Config{
		Severity: severity,
		Outputs:  []logging.Output{logging.NewConsoleOutput(true, logging.WithColor(false))},
	})
}

func restoreEnv(key, previous string, hadPrevious bool) {
	if hadPrevious {
		_ = os.Setenv(key, previous)
		return
	}
	_ = os.Unsetenv(key)
}

func sanitizePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(value)
}
