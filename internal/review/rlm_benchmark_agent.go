package review

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
	internalgithub "github.com/XiaoConstantine/maestro/internal/github"
)

// ReviewRLMBenchmarkAgent runs the normal PR review benchmark pipeline with
// the RLM chunk processor instead of the default parallel reviewer.
type ReviewRLMBenchmarkAgent struct {
	llm              core.LLM
	logger           *logging.Logger
	processor        *reviewRLMProcessor
	overlay          string
	maxChunksPerCase int
	verify           bool
	budgetManager    *maestrobudget.BudgetManager
}

var _ optimize.OptimizableAgent = (*ReviewRLMBenchmarkAgent)(nil)

func NewReviewRLMBenchmarkAgent(llm core.LLM, logger *logging.Logger, artifacts optimize.AgentArtifacts, overlay string, maxChunksPerCase int, budgetManagers ...*maestrobudget.BudgetManager) (*ReviewRLMBenchmarkAgent, error) {
	if llm == nil {
		return nil, fmt.Errorf("review RLM benchmark LLM is nil")
	}
	var budgetManager *maestrobudget.BudgetManager
	if len(budgetManagers) > 0 {
		budgetManager = budgetManagers[0]
	}
	processor := newReviewRLMProcessor(llm, overlay, logger, budgetManager)
	if !reviewRLMArtifactsEmpty(artifacts) {
		if err := processor.SetArtifacts(artifacts); err != nil {
			return nil, err
		}
	}
	return &ReviewRLMBenchmarkAgent{
		llm:              llm,
		logger:           logger,
		processor:        processor,
		overlay:          overlay,
		maxChunksPerCase: maxChunksPerCase,
		verify:           reviewRLMBenchmarkVerificationEnabled(),
		budgetManager:    budgetManager,
	}, nil
}

func (a *ReviewRLMBenchmarkAgent) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	if a == nil || a.processor == nil {
		return nil, fmt.Errorf("review RLM benchmark agent is nil")
	}
	benchmarkCase, err := reviewBenchmarkCaseFromInput(input)
	if err != nil {
		return nil, err
	}
	hunks, err := internalgithub.ParseHunks(benchmarkCase.Diff, benchmarkCase.FilePath)
	if err != nil {
		return nil, fmt.Errorf("parse benchmark hunks: %w", err)
	}

	benchmarkReviewAgent := &PRReviewAgent{reviewProcessor: a.processor}
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

func (a *ReviewRLMBenchmarkAgent) GetCapabilities() []core.Tool {
	if a == nil || a.processor == nil {
		return nil
	}
	return a.processor.GetCapabilities()
}

func (a *ReviewRLMBenchmarkAgent) GetMemory() agents.Memory {
	if a == nil || a.processor == nil {
		return nil
	}
	return a.processor.GetMemory()
}

func (a *ReviewRLMBenchmarkAgent) GetArtifacts() optimize.AgentArtifacts {
	if a == nil || a.processor == nil {
		return optimize.AgentArtifacts{}
	}
	return a.processor.GetArtifacts()
}

func (a *ReviewRLMBenchmarkAgent) SetArtifacts(artifacts optimize.AgentArtifacts) error {
	if a == nil || a.processor == nil {
		return fmt.Errorf("review RLM benchmark agent is nil")
	}
	return a.processor.SetArtifacts(artifacts)
}

func (a *ReviewRLMBenchmarkAgent) Clone() (optimize.OptimizableAgent, error) {
	if a == nil || a.processor == nil {
		return nil, fmt.Errorf("review RLM benchmark agent is nil")
	}
	clonedProcessor, err := a.processor.Clone()
	if err != nil {
		return nil, err
	}
	processor, ok := clonedProcessor.(*reviewRLMProcessor)
	if !ok {
		return nil, fmt.Errorf("review RLM benchmark clone produced %T", clonedProcessor)
	}
	return &ReviewRLMBenchmarkAgent{
		llm:              a.llm,
		logger:           a.logger,
		processor:        processor,
		overlay:          a.overlay,
		maxChunksPerCase: a.maxChunksPerCase,
		verify:           a.verify,
		budgetManager:    a.budgetManager,
	}, nil
}

func (a *ReviewRLMBenchmarkAgent) LastExecutionTrace() *agents.ExecutionTrace {
	if a == nil || a.processor == nil {
		return nil
	}
	return a.processor.LastExecutionTrace()
}

func (a *ReviewRLMBenchmarkAgent) OptimizationAgentType() string {
	return reviewRLMAgentSignature
}

func (a *ReviewRLMBenchmarkAgent) ListOptimizationTargets() []optimize.OptimizationTargetDescriptor {
	if a == nil || a.processor == nil {
		return nil
	}
	return a.processor.ListOptimizationTargets()
}

func (a *ReviewRLMBenchmarkAgent) UpdateArtifacts(update func(optimize.AgentArtifacts) (optimize.AgentArtifacts, error)) error {
	if a == nil || a.processor == nil {
		return fmt.Errorf("review RLM benchmark agent is nil")
	}
	return a.processor.UpdateArtifacts(update)
}

func reviewRLMArtifactsEmpty(artifacts optimize.AgentArtifacts) bool {
	return len(artifacts.Text) == 0 && len(artifacts.Int) == 0 && len(artifacts.Bool) == 0
}

func reviewRLMBenchmarkVerificationEnabled() bool {
	value := strings.TrimSpace(os.Getenv(reviewBenchmarkVerificationEnvVar))
	return value == "1" || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes") || strings.EqualFold(value, "on")
}
