package optimize

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	dspyoptimize "github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
)

const ProtectedBaselineVersion = 1

type ProtectedCaseResult struct {
	CaseID    string             `json:"case_id"`
	Protected bool               `json:"protected,omitempty"`
	Score     float64            `json:"score"`
	Scores    map[string]float64 `json:"scores,omitempty"`
	Error     string             `json:"error,omitempty"`
}

type ProtectedBaseline struct {
	Version        int                           `json:"version"`
	AgentSignature string                        `json:"agent_signature"`
	Scores         map[string]ProtectedCaseScore `json:"scores"`
}

type ProtectedCaseScore struct {
	Score  float64            `json:"score"`
	Scores map[string]float64 `json:"scores,omitempty"`
}

type ProtectedGateReport struct {
	BaselineVersion  int                   `json:"baseline_version"`
	AgentSignature   string                `json:"agent_signature"`
	Tolerance        float64               `json:"tolerance"`
	Passed           bool                  `json:"passed"`
	Regressions      []ProtectedRegression `json:"regressions,omitempty"`
	MissingBaseline  []string              `json:"missing_baseline,omitempty"`
	ProtectedCaseIDs []string              `json:"protected_case_ids,omitempty"`
}

type ProtectedRegression struct {
	CaseID        string             `json:"case_id"`
	Baseline      ProtectedCaseScore `json:"baseline"`
	Current       ProtectedCaseScore `json:"current"`
	ScoreDelta    float64            `json:"score_delta"`
	RegressedDims []string           `json:"regressed_dims,omitempty"`
}

func NewProtectedBaseline(agentSignature string, results []ProtectedCaseResult) (*ProtectedBaseline, error) {
	agentSignature = strings.TrimSpace(agentSignature)
	if agentSignature == "" {
		return nil, fmt.Errorf("protected baseline agent signature is required")
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("protected baseline requires at least one case result")
	}
	baseline := &ProtectedBaseline{
		Version:        ProtectedBaselineVersion,
		AgentSignature: agentSignature,
		Scores:         make(map[string]ProtectedCaseScore, len(results)),
	}
	for _, result := range results {
		caseID := strings.TrimSpace(result.CaseID)
		if caseID == "" {
			return nil, fmt.Errorf("refusing to build protected baseline from case with empty id")
		}
		if strings.TrimSpace(result.Error) != "" {
			return nil, fmt.Errorf("refusing to build protected baseline from errored case %q: %s", caseID, result.Error)
		}
		baseline.Scores[caseID] = ProtectedCaseScore{
			Score:  result.Score,
			Scores: cloneFloat64Map(result.Scores),
		}
	}
	return baseline, nil
}

func (b *ProtectedBaseline) Validate(agentSignature string) error {
	if b == nil {
		return fmt.Errorf("protected baseline is nil")
	}
	if b.Version != ProtectedBaselineVersion {
		return fmt.Errorf("unsupported protected baseline version %d", b.Version)
	}
	if strings.TrimSpace(b.AgentSignature) == "" {
		return fmt.Errorf("protected baseline missing agent_signature")
	}
	if strings.TrimSpace(agentSignature) != "" && b.AgentSignature != agentSignature {
		return fmt.Errorf("protected baseline agent_signature %q does not match %q", b.AgentSignature, agentSignature)
	}
	if len(b.Scores) == 0 {
		return fmt.Errorf("protected baseline has no scores")
	}
	return nil
}

func LoadProtectedBaseline(path, agentSignature string) (*ProtectedBaseline, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("protected baseline path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read protected baseline %q: %w", path, err)
	}
	var baseline ProtectedBaseline
	if err := json.Unmarshal(data, &baseline); err != nil {
		return nil, fmt.Errorf("decode protected baseline %q: %w", path, err)
	}
	if err := baseline.Validate(agentSignature); err != nil {
		return nil, err
	}
	return &baseline, nil
}

func WriteProtectedBaseline(path string, baseline *ProtectedBaseline, agentSignature string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("protected baseline path is required")
	}
	if err := baseline.Validate(agentSignature); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create protected baseline directory: %w", err)
	}
	data, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal protected baseline: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write protected baseline %q: %w", path, err)
	}
	return nil
}

func VerifyProtectedBaselineFile(path, agentSignature string, results []ProtectedCaseResult) (*ProtectedGateReport, error) {
	baseline, err := LoadProtectedBaseline(path, agentSignature)
	if err != nil {
		return nil, err
	}
	gate := EvaluateProtectedGate(results, baseline, agentSignature, 0)
	if gate == nil || !gate.Passed {
		return gate, fmt.Errorf("protected baseline self-check failed")
	}
	return gate, nil
}

func EvaluateProtectedGate(results []ProtectedCaseResult, baseline *ProtectedBaseline, agentSignature string, tolerance float64) *ProtectedGateReport {
	gate := &ProtectedGateReport{
		BaselineVersion:  ProtectedBaselineVersion,
		AgentSignature:   strings.TrimSpace(agentSignature),
		Tolerance:        tolerance,
		Passed:           true,
		ProtectedCaseIDs: make([]string, 0),
	}
	if tolerance < 0 {
		tolerance = 0
		gate.Tolerance = 0
	}
	if baseline == nil {
		gate.Passed = false
		return gate
	}
	if len(results) == 0 {
		gate.Passed = false
		return gate
	}
	if err := baseline.Validate(agentSignature); err != nil {
		gate.Passed = false
		return gate
	}
	gate.BaselineVersion = baseline.Version
	gate.AgentSignature = baseline.AgentSignature

	gateAll := !hasExplicitProtectedCase(results)
	for _, result := range results {
		if !gateAll && !result.Protected {
			continue
		}
		caseID := strings.TrimSpace(result.CaseID)
		if caseID == "" {
			continue
		}
		gate.ProtectedCaseIDs = append(gate.ProtectedCaseIDs, caseID)
		base, ok := baseline.Scores[caseID]
		if !ok {
			gate.MissingBaseline = append(gate.MissingBaseline, caseID)
			gate.Passed = false
			continue
		}
		current := ProtectedCaseScore{Score: result.Score, Scores: cloneFloat64Map(result.Scores)}
		if current.Score < base.Score-tolerance {
			gate.Passed = false
			gate.Regressions = append(gate.Regressions, ProtectedRegression{
				CaseID:        caseID,
				Baseline:      base,
				Current:       current,
				ScoreDelta:    current.Score - base.Score,
				RegressedDims: regressedProtectedDimensions(base, current, tolerance),
			})
		}
	}
	return gate
}

func ProtectedCaseResultsFromHarness(examples []dspyoptimize.AgentExample, run *dspyoptimize.HarnessRunResult) []ProtectedCaseResult {
	if run == nil {
		return nil
	}
	exampleByID := make(map[string]dspyoptimize.AgentExample, len(examples))
	for _, example := range examples {
		exampleByID[example.ID] = example
	}
	results := make([]ProtectedCaseResult, 0, len(run.Results))
	for _, item := range run.Results {
		example := exampleByID[item.ExampleID]
		result := ProtectedCaseResult{
			CaseID:    item.ExampleID,
			Protected: boolFromInterface(example.Metadata["protected"]),
		}
		if item.Result != nil {
			result.Score = item.Result.Score
			if item.Result.SideInfo != nil {
				result.Scores = cloneFloat64Map(item.Result.SideInfo.Scores)
				if errText, ok := item.Result.SideInfo.Diagnostics["evaluation_error"].(string); ok {
					result.Error = strings.TrimSpace(errText)
				}
			}
		}
		results = append(results, result)
	}
	return results
}

func hasExplicitProtectedCase(results []ProtectedCaseResult) bool {
	for _, result := range results {
		if result.Protected {
			return true
		}
	}
	return false
}

func regressedProtectedDimensions(base, current ProtectedCaseScore, tolerance float64) []string {
	regressed := make([]string, 0)
	if current.Score < base.Score-tolerance {
		regressed = append(regressed, "score")
	}
	for key, baseValue := range base.Scores {
		if current.Scores[key] < baseValue-tolerance {
			regressed = append(regressed, key)
		}
	}
	return regressed
}

func cloneFloat64Map(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func boolFromInterface(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "on")
	default:
		return false
	}
}
