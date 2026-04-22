package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	internalgithub "github.com/XiaoConstantine/maestro/internal/github"
	"github.com/XiaoConstantine/maestro/internal/reasoning"
	"github.com/XiaoConstantine/maestro/internal/util"
	"github.com/briandowns/spinner"
	gh "github.com/google/go-github/v68/github"
)

const reviewBenchmarkVerificationEnvVar = "MAESTRO_REVIEW_BENCHMARK_VERIFY"

type ReviewBenchmarkAgent struct {
	llm              core.LLM
	logger           *logging.Logger
	artifacts        optimize.AgentArtifacts
	maxChunksPerCase int
	verify           bool

	mu sync.RWMutex
}

var _ optimize.OptimizableAgent = (*ReviewBenchmarkAgent)(nil)

const reviewBenchmarkOptimizationAgentType = "maestro.review-benchmark"

func reviewBenchmarkOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	return []optimize.OptimizationTargetDescriptor{
		{
			ID:          "root.skill_pack",
			Kind:        optimize.OptimizationTargetText,
			Description: "Review guidance overlay used to generate findings.",
			ArtifactKey: optimize.ArtifactSkillPack,
		},
		{
			ID:          "root.few_shot_demos",
			Kind:        optimize.OptimizationTargetText,
			Description: "Few-shot review demonstrations appended to the reviewer prompt.",
			ArtifactKey: ArtifactFewShotDemos,
		},
	}
}

func NewReviewBenchmarkAgent(llm core.LLM, logger *logging.Logger, artifacts optimize.AgentArtifacts, maxChunksPerCase int) *ReviewBenchmarkAgent {
	return &ReviewBenchmarkAgent{
		llm:              llm,
		logger:           logger,
		artifacts:        mergeReviewArtifactsWithDefaults(artifacts),
		maxChunksPerCase: maxChunksPerCase,
		verify:           util.GetEnvBool(reviewBenchmarkVerificationEnvVar, false),
	}
}

func (a *ReviewBenchmarkAgent) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	benchmarkCase, err := reviewBenchmarkCaseFromInput(input)
	if err != nil {
		return nil, err
	}

	hunks, err := internalgithub.ParseHunks(benchmarkCase.Diff, benchmarkCase.FilePath)
	if err != nil {
		return nil, fmt.Errorf("parse benchmark hunks: %w", err)
	}

	benchmarkReviewAgent := &PRReviewAgent{
		reviewProcessor: reasoning.NewEnhancedCodeReviewProcessor(nil, a.logger, reviewBenchmarkSkillOverlay(a.GetArtifacts())),
	}
	if a.verify {
		repoPath, err := materializeBenchmarkVerificationRepo(benchmarkCase)
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(repoPath)
		benchmarkReviewAgent.clonedRepoPath = repoPath
	}

	tasks := []PRReviewTask{{
		FilePath:    benchmarkCase.FilePath,
		FileContent: benchmarkCase.FileContent,
		Changes:     benchmarkCase.Diff,
	}}
	changes := &PRChanges{
		Files: []PRFileChange{{
			FilePath:    benchmarkCase.FilePath,
			FileContent: benchmarkCase.FileContent,
			Patch:       benchmarkCase.Diff,
			Hunks:       hunks,
		}},
	}

	console := &benchmarkConsole{}
	phase2Start := time.Now()
	_, processedTasks, err := benchmarkReviewAgent.prepareChunks(ctx, tasks, console)
	if err != nil {
		return nil, err
	}
	phase2Duration := time.Since(phase2Start)
	if a.maxChunksPerCase > 0 {
		for i := range processedTasks {
			if len(processedTasks[i].Chunks) > a.maxChunksPerCase {
				processedTasks[i].Chunks = append([]ReviewChunk(nil), processedTasks[i].Chunks[:a.maxChunksPerCase]...)
			}
		}
	}

	pipeline, err := benchmarkReviewAgent.runPreparedReviewPipeline(ctx, processedTasks, changes, console, nil, nil, "", a.verify)
	if err != nil {
		return nil, err
	}
	pipeline.Phase2Duration = phase2Duration

	result := map[string]interface{}{
		"comments":               pipeline.Comments,
		"comment_count":          len(pipeline.Comments),
		"raw_candidates":         pipeline.RawCommentCount,
		"pre_verification_count": pipeline.PreVerificationCount,
		"skipped_after_filter":   pipeline.SkippedAfterFilter,
		"filter_drop_reasons":    pipeline.FilterDropReasons,
		"filter_rejections":      pipeline.FilterRejected,
		"total_chunks":           pipeline.TotalChunks,
		"selected_chunks":        pipeline.SelectedChunks,
		"label":                  benchmarkCase.Label,
		"verification_enabled":   a.verify,
	}
	if a.verify {
		result["verification_dropped"] = pipeline.VerificationDropped
		result["verification_drop_reasons"] = pipeline.VerificationReasons
		result["verification_rejections"] = pipeline.VerificationRejected
	}
	return result, nil
}

func materializeBenchmarkVerificationRepo(benchmarkCase ReviewBenchmarkCase) (string, error) {
	repoPath, err := os.MkdirTemp("", "maestro-review-benchmark-*")
	if err != nil {
		return "", fmt.Errorf("create benchmark verification repo: %w", err)
	}
	filePath := filepath.Join(repoPath, filepath.FromSlash(strings.TrimSpace(benchmarkCase.FilePath)))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		_ = os.RemoveAll(repoPath)
		return "", fmt.Errorf("create benchmark verification directories: %w", err)
	}
	if err := os.WriteFile(filePath, []byte(benchmarkCase.FileContent), 0o644); err != nil {
		_ = os.RemoveAll(repoPath)
		return "", fmt.Errorf("write benchmark verification file: %w", err)
	}
	return repoPath, nil
}

func (a *ReviewBenchmarkAgent) GetCapabilities() []core.Tool { return nil }

func (a *ReviewBenchmarkAgent) GetMemory() agents.Memory { return nil }

func (a *ReviewBenchmarkAgent) GetArtifacts() optimize.AgentArtifacts {
	if a == nil {
		return optimize.AgentArtifacts{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.artifacts.Clone()
}

func (a *ReviewBenchmarkAgent) OptimizationAgentType() string {
	return reviewBenchmarkOptimizationAgentType
}

func (a *ReviewBenchmarkAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	return reviewBenchmarkOptimizationTargets()
}

func (a *ReviewBenchmarkAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	if a == nil {
		return fmt.Errorf("review benchmark agent is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.artifacts = mergeReviewArtifactsWithDefaults(artifacts)
	return nil
}

func (a *ReviewBenchmarkAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	if a == nil {
		return fmt.Errorf("review benchmark agent is nil")
	}
	if update == nil {
		return fmt.Errorf("review benchmark update function is nil")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	next, err := update(a.artifacts.Clone())
	if err != nil {
		return err
	}
	a.artifacts = mergeReviewArtifactsWithDefaults(next)
	return nil
}

func (a *ReviewBenchmarkAgent) Clone() (optimize.OptimizableAgent, error) {
	if a == nil {
		return nil, fmt.Errorf("review benchmark agent is nil")
	}
	return &ReviewBenchmarkAgent{
		llm:              a.llm,
		logger:           a.logger,
		artifacts:        a.GetArtifacts(),
		maxChunksPerCase: a.maxChunksPerCase,
		verify:           a.verify,
	}, nil
}

func reviewBenchmarkCaseFromInput(input map[string]interface{}) (ReviewBenchmarkCase, error) {
	raw, ok := input["benchmark_case"]
	if !ok {
		return ReviewBenchmarkCase{}, fmt.Errorf("benchmark_case is required")
	}
	if benchmarkCase, ok := raw.(ReviewBenchmarkCase); ok {
		return benchmarkCase, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return ReviewBenchmarkCase{}, err
	}
	var benchmarkCase ReviewBenchmarkCase
	if err := json.Unmarshal(data, &benchmarkCase); err != nil {
		return ReviewBenchmarkCase{}, err
	}
	return benchmarkCase, nil
}

func reviewBenchmarkSkillOverlay(artifacts optimize.AgentArtifacts) string {
	overlay := materializeReviewInstructionOverlay(artifacts, nil)
	demos := strings.TrimSpace(artifacts.Text[ArtifactFewShotDemos])
	if demos == "" {
		return overlay
	}
	if overlay == "" {
		return "## Examples of good reviews:\n" + demos
	}
	return overlay + "\n\n## Examples of good reviews:\n" + demos
}

type benchmarkConsole struct{}

func (c *benchmarkConsole) StartSpinner(message string) {}
func (c *benchmarkConsole) StopSpinner()                {}
func (c *benchmarkConsole) WithSpinner(ctx context.Context, message string, fn func() error) error {
	return fn()
}
func (c *benchmarkConsole) ShowComments(comments []PRReviewComment, metric MetricsCollector) {}
func (c *benchmarkConsole) ShowCommentsInteractive(comments []PRReviewComment, onPost func([]PRReviewComment) error) error {
	return nil
}
func (c *benchmarkConsole) ShowSummary(comments []PRReviewComment, metric MetricsCollector)        {}
func (c *benchmarkConsole) StartReview(pr *gh.PullRequest)                                         {}
func (c *benchmarkConsole) ReviewingFile(file string, current, total int)                          {}
func (c *benchmarkConsole) ConfirmReviewPost(commentCount int) (bool, error)                       { return false, nil }
func (c *benchmarkConsole) ReviewComplete()                                                        {}
func (c *benchmarkConsole) UpdateSpinnerText(text string)                                          {}
func (c *benchmarkConsole) ShowReviewMetrics(metrics MetricsCollector, comments []PRReviewComment) {}
func (c *benchmarkConsole) CollectAllFeedback(comments []PRReviewComment, metric MetricsCollector) error {
	return nil
}
func (c *benchmarkConsole) Confirm(opts PromptOptions) (bool, error) { return false, nil }
func (c *benchmarkConsole) FileError(filepath string, err error)     {}
func (c *benchmarkConsole) Printf(format string, a ...interface{})   {}
func (c *benchmarkConsole) Println(a ...interface{})                 {}
func (c *benchmarkConsole) PrintHeader(text string)                  {}
func (c *benchmarkConsole) NoIssuesFound(file string, chunkNumber, totalChunks int) {
}
func (c *benchmarkConsole) SeverityIcon(severity string) string { return "" }
func (c *benchmarkConsole) Color() bool                         { return false }
func (c *benchmarkConsole) Spinner() *spinner.Spinner           { return nil }
func (c *benchmarkConsole) IsInteractive() bool                 { return false }
