package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	maestrobudget "github.com/XiaoConstantine/maestro/internal/budget"
	"github.com/XiaoConstantine/maestro/internal/types"
)

func TestRecordRLMTraceUsageUpdatesBudgetStatus(t *testing.T) {
	manager := maestrobudget.NewBudgetManager(maestrobudget.Config{MaxBudgetUSD: 1.00})
	service := &MaestroService{
		budgetManager: manager,
		logger:        logging.GetLogger(),
	}
	service.recordRLMTraceUsage(context.Background(), rlmOverviewArtifactRoute, &modrlm.RLMTrace{
		Usage: core.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 25,
			TotalTokens:      125,
			Cost:             0.05,
		},
	})

	status := service.BudgetStatus()
	if status == nil {
		t.Fatal("BudgetStatus() = nil")
	}
	if status.TotalSpentUSD != 0.05 || status.RemainingUSD != 0.95 || status.PercentUsed != 0.05 {
		t.Fatalf("budget dollars = spent %.2f remaining %.2f percent %.2f, want 0.05/0.95/0.05", status.TotalSpentUSD, status.RemainingUSD, status.PercentUsed)
	}
	if status.TotalTokens != 125 {
		t.Fatalf("TotalTokens = %d, want 125", status.TotalTokens)
	}
	if got := status.ByAgent[rlmOverviewArtifactRoute].TotalTokens; got != 125 {
		t.Fatalf("agent tokens = %d, want 125", got)
	}
}

func TestAttachBudgetMetadataPreservesExistingMetadata(t *testing.T) {
	manager := maestrobudget.NewBudgetManager(maestrobudget.DefaultConfig())
	if err := manager.RecordUsage("test-agent", 100, 50, 0.10); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}
	service := &MaestroService{budgetManager: manager}
	response := &Response{Metadata: map[string]interface{}{"existing": "value"}}

	service.attachBudgetMetadata(response)

	if response.Metadata["existing"] != "value" {
		t.Fatalf("existing metadata = %v, want value", response.Metadata["existing"])
	}
	if response.Metadata["budget_total_spent"] != 0.10 {
		t.Fatalf("budget_total_spent = %v, want 0.10", response.Metadata["budget_total_spent"])
	}
	if response.Metadata["budget_total_tokens"] != int64(150) {
		t.Fatalf("budget_total_tokens = %v, want 150", response.Metadata["budget_total_tokens"])
	}
	if response.Metadata["budget_scope"] != "running_total" {
		t.Fatalf("budget_scope = %v, want running_total", response.Metadata["budget_scope"])
	}
	if response.Metadata["budget_running_total_tokens"] != int64(150) {
		t.Fatalf("budget_running_total_tokens = %v, want 150", response.Metadata["budget_running_total_tokens"])
	}
}

func TestSetReviewAgentInjectsBudgetManager(t *testing.T) {
	manager := maestrobudget.NewBudgetManager(maestrobudget.DefaultConfig())
	agent := &budgetAwareReviewAgent{}
	service := &MaestroService{
		pool:          &AgentPool{},
		budgetManager: manager,
	}

	service.SetReviewAgent(agent)

	if agent.manager != manager {
		t.Fatal("SetReviewAgent did not inject service budget manager")
	}
}

type budgetAwareReviewAgent struct {
	manager *maestrobudget.BudgetManager
}

func (a *budgetAwareReviewAgent) SetBudgetManager(manager *maestrobudget.BudgetManager) {
	a.manager = manager
}

func (a *budgetAwareReviewAgent) ReviewPR(context.Context, int, []types.PRReviewTask, types.ConsoleInterface) ([]types.PRReviewComment, error) {
	return nil, nil
}

func (a *budgetAwareReviewAgent) ReviewPRWithChanges(context.Context, int, []types.PRReviewTask, types.ConsoleInterface, *types.PRChanges) ([]types.PRReviewComment, error) {
	return nil, nil
}

func (a *budgetAwareReviewAgent) Stop(context.Context) {}

func (a *budgetAwareReviewAgent) Metrics(context.Context) types.MetricsCollector { return nil }

func (a *budgetAwareReviewAgent) ClonedRepoPath() string { return "" }

func (a *budgetAwareReviewAgent) WaitForClone(context.Context, time.Duration) string { return "" }

func (a *budgetAwareReviewAgent) GetIndexingStatus() *types.IndexingStatus { return nil }

func (a *budgetAwareReviewAgent) Close() error { return nil }
