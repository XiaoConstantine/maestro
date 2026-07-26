package orchestration

import (
	"context"
	"math"
	"testing"

	dspysubagent "github.com/XiaoConstantine/dspy-go/pkg/agents/subagent"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	maestrocoding "github.com/XiaoConstantine/maestro/internal/coding"
	"github.com/XiaoConstantine/maestro/internal/types"
)

func TestReviewExecutionAgentUsesCanonicalContract(t *testing.T) {
	var gotPR int
	agent := newReviewExecutionAgent(func(_ context.Context, prNumber int, onProgress func(string)) ([]types.PRReviewComment, error) {
		gotPR = prNumber
		if onProgress != nil {
			onProgress("reviewing")
		}
		return []types.PRReviewComment{{FilePath: "main.go", LineNumber: 9, Content: "finding"}}, nil
	}, func(string) {})

	output, err := agent.Execute(context.Background(), map[string]any{"pr_number": 42})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotPR != 42 {
		t.Fatalf("review pr = %d, want 42", gotPR)
	}
	comments, ok := output["comments"].([]types.PRReviewComment)
	if !ok || len(comments) != 1 || comments[0].FilePath != "main.go" {
		t.Fatalf("comments = %#v", output["comments"])
	}
	contract := agent.AgentContract()
	if contract.PrimaryInput != "pr_number" || len(contract.Outputs) != 2 {
		t.Fatalf("AgentContract() = %#v", contract)
	}
}

func TestReviewSubagentToolDelegatesToReviewExecutionAgent(t *testing.T) {
	var gotPR int
	tool, err := newReviewSubagentTool(func(_ context.Context, prNumber int, _ func(string)) ([]types.PRReviewComment, error) {
		gotPR = prNumber
		return []types.PRReviewComment{{FilePath: "review.go", LineNumber: 12, Content: "finding"}}, nil
	})
	if err != nil {
		t.Fatalf("newReviewSubagentTool() error = %v", err)
	}
	info, ok := dspysubagent.InfoFromTool(tool)
	if !ok || info.Name != "review_pull_request" || info.SessionPolicy != "ephemeral" {
		t.Fatalf("InfoFromTool() = %#v, %t", info, ok)
	}
	result, err := tool.Execute(context.Background(), map[string]any{"pr_number": 17})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotPR != 17 {
		t.Fatalf("review pr = %d, want 17", gotPR)
	}
	if result.Metadata["is_error"] == true {
		t.Fatalf("tool result = %#v", result)
	}
}

func TestCodingSessionExecutesRegisteredReviewSubagent(t *testing.T) {
	var gotPR int
	tool, err := newReviewSubagentTool(func(_ context.Context, prNumber int, _ func(string)) ([]types.PRReviewComment, error) {
		gotPR = prNumber
		return []types.PRReviewComment{{FilePath: "main.go", LineNumber: 3, Content: "finding"}}, nil
	})
	if err != nil {
		t.Fatalf("newReviewSubagentTool() error = %v", err)
	}
	llm := &capturingCodingLLM{results: []map[string]any{
		{"function_call": map[string]any{"name": "review_pull_request", "arguments": map[string]any{"pr_number": 23}}},
		{"function_call": map[string]any{"name": "Finish", "arguments": map[string]any{"answer": "Review complete"}}},
	}}
	session, err := maestrocoding.NewSession(maestrocoding.Config{
		LLM: llm, Workspace: t.TempDir(), ExtraTools: []core.Tool{tool},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := session.Prompt(context.Background(), "Review PR 23", nil); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if gotPR != 23 {
		t.Fatalf("review pr = %d, want 23", gotPR)
	}
}

func TestReviewPRNumberRejectsInvalidInput(t *testing.T) {
	for _, value := range []any{nil, 0, -1, "bad", 17.9, math.NaN(), math.Inf(1)} {
		if _, err := reviewPRNumber(value); err == nil {
			t.Fatalf("reviewPRNumber(%v) error = nil", value)
		}
	}
}
