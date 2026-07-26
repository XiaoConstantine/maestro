package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	dspysubagent "github.com/XiaoConstantine/dspy-go/pkg/agents/subagent"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/maestro/internal/types"
	models "github.com/XiaoConstantine/mcp-go/pkg/model"
)

type reviewRunFunc func(context.Context, int, func(string)) ([]types.PRReviewComment, error)

type reviewExecutionAgent struct {
	run        reviewRunFunc
	onProgress func(string)
	memory     agents.Memory
}

var (
	_ agents.Agent                 = (*reviewExecutionAgent)(nil)
	_ agents.AgentContractProvider = (*reviewExecutionAgent)(nil)
)

func newReviewExecutionAgent(run reviewRunFunc, onProgress func(string)) *reviewExecutionAgent {
	return &reviewExecutionAgent{
		run:        run,
		onProgress: onProgress,
		memory:     agents.NewInMemoryStore(),
	}
}

func (a *reviewExecutionAgent) Execute(ctx context.Context, input map[string]any) (map[string]any, error) {
	if a == nil || a.run == nil {
		return nil, fmt.Errorf("review execution agent is not configured")
	}
	prNumber, err := reviewPRNumber(input["pr_number"])
	if err != nil {
		return nil, err
	}
	comments, err := a.run(ctx, prNumber, a.onProgress)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"completed": true,
		"pr_number": prNumber,
		"comments":  comments,
	}, nil
}

func (*reviewExecutionAgent) GetCapabilities() []core.Tool { return nil }

func (a *reviewExecutionAgent) GetMemory() agents.Memory {
	if a == nil {
		return nil
	}
	return a.memory
}

func (*reviewExecutionAgent) AgentContract() agents.AgentContract {
	return agents.AgentContract{
		Inputs: []agents.AgentField{
			{Name: "pr_number", Description: "GitHub pull request number to review", Required: true},
		},
		Outputs: []agents.AgentField{
			{Name: "comments", Description: "Structured Maestro review comments", Required: true},
			{Name: "completed", Description: "Whether the review completed", Required: true},
		},
		PrimaryInput: "pr_number",
	}
}

func reviewPRNumber(value any) (int, error) {
	var number int
	switch value := value.(type) {
	case int:
		number = value
	case int64:
		number = int(value)
	case float64:
		maxInt := float64(int(^uint(0) >> 1))
		minInt := -maxInt - 1
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value > maxInt || value < minInt {
			return 0, fmt.Errorf("pr_number must be a finite integer")
		}
		number = int(value)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("invalid pr_number %q", value)
		}
		number = parsed
	default:
		return 0, fmt.Errorf("pr_number is required")
	}
	if number <= 0 {
		return 0, fmt.Errorf("pr_number must be positive")
	}
	return number, nil
}

func (s *MaestroService) reviewSubagentTool() (core.Tool, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if _, err := s.pool.GetReviewAgent(context.Background()); err != nil {
		return nil, nil
	}
	return newReviewSubagentTool(s.executeReview)
}

func newReviewSubagentTool(run reviewRunFunc) (core.Tool, error) {
	return dspysubagent.AsTool(dspysubagent.ToolConfig{
		Name:        "review_pull_request",
		Description: "Run Maestro's specialized code-review agent for a GitHub pull request.",
		InputSchema: models.InputSchema{
			Type: "object",
			Properties: map[string]models.ParameterSchema{
				"pr_number": {
					Type:        "integer",
					Description: "GitHub pull request number to review.",
					Required:    true,
				},
			},
		},
		BuildAgent: func(context.Context, map[string]any) (agents.Agent, error) {
			return newReviewExecutionAgent(run, nil), nil
		},
		BuildResult: func(output map[string]any, runContext dspysubagent.ResultContext) (core.ToolResult, error) {
			comments, _ := output["comments"].([]types.PRReviewComment)
			encoded, err := json.MarshalIndent(comments, "", "  ")
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("encode review comments: %w", err)
			}
			text := fmt.Sprintf("Review completed with %d finding(s):\n%s", len(comments), encoded)
			return core.ToolResult{
				Data: text,
				Metadata: map[string]any{
					core.ToolResultModelTextMeta:   text,
					core.ToolResultDisplayTextMeta: text,
					core.ToolResultIsErrorMeta:     false,
				},
				Annotations: map[string]any{
					core.ToolResultDetailsAnnotation: map[string]any{
						"subagent":      true,
						"subagent_name": "review_pull_request",
						"completed":     runContext.Completed,
						"output":        output,
						"trace":         runContext.TraceRef("review_pull_request"),
					},
				},
			}, nil
		},
		SessionPolicy: dspysubagent.SessionPolicyEphemeral,
	})
}
