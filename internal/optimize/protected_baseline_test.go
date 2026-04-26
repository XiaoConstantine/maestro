package optimize

import (
	"testing"

	dspyoptimize "github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
)

func TestProtectedBaselineRoundTripAndGate(t *testing.T) {
	baseline, err := NewProtectedBaseline("agent.v1", []ProtectedCaseResult{
		{CaseID: "protected", Protected: true, Score: 0.9, Scores: map[string]float64{"precision": 0.8}},
		{CaseID: "unprotected", Score: 0.1},
	})
	if err != nil {
		t.Fatalf("NewProtectedBaseline() error = %v", err)
	}

	path := t.TempDir() + "/baseline.json"
	if err := WriteProtectedBaseline(path, baseline, "agent.v1"); err != nil {
		t.Fatalf("WriteProtectedBaseline() error = %v", err)
	}
	selfCheck, err := VerifyProtectedBaselineFile(path, "agent.v1", []ProtectedCaseResult{
		{CaseID: "protected", Protected: true, Score: 0.9, Scores: map[string]float64{"precision": 0.8}},
		{CaseID: "unprotected", Score: 0.1},
	})
	if err != nil {
		t.Fatalf("VerifyProtectedBaselineFile() error = %v", err)
	}
	if !selfCheck.Passed {
		t.Fatalf("selfCheck.Passed = false, want true")
	}
	loaded, err := LoadProtectedBaseline(path, "agent.v1")
	if err != nil {
		t.Fatalf("LoadProtectedBaseline() error = %v", err)
	}

	gate := EvaluateProtectedGate([]ProtectedCaseResult{
		{CaseID: "protected", Protected: true, Score: 0.7, Scores: map[string]float64{"precision": 0.6}},
		{CaseID: "unprotected", Score: 0},
	}, loaded, "agent.v1", 0)
	if gate.Passed {
		t.Fatalf("gate.Passed = true, want protected regression failure")
	}
	if len(gate.Regressions) != 1 || gate.Regressions[0].CaseID != "protected" {
		t.Fatalf("Regressions = %#v, want protected regression only", gate.Regressions)
	}
	if len(gate.Regressions[0].RegressedDims) != 2 {
		t.Fatalf("RegressedDims = %#v, want score and precision", gate.Regressions[0].RegressedDims)
	}
}

func TestProtectedGateFallsBackToAllCasesWithoutProtectedMarkers(t *testing.T) {
	baseline, err := NewProtectedBaseline("agent.v1", []ProtectedCaseResult{
		{CaseID: "case-a", Score: 0.9},
	})
	if err != nil {
		t.Fatalf("NewProtectedBaseline() error = %v", err)
	}

	gate := EvaluateProtectedGate([]ProtectedCaseResult{
		{CaseID: "case-a", Score: 0.95},
		{CaseID: "case-b", Score: 1.0},
	}, baseline, "agent.v1", 0)
	if gate.Passed {
		t.Fatalf("gate.Passed = true, want missing-baseline failure")
	}
	if len(gate.MissingBaseline) != 1 || gate.MissingBaseline[0] != "case-b" {
		t.Fatalf("MissingBaseline = %#v, want case-b", gate.MissingBaseline)
	}
}

func TestProtectedGateHonorsExplicitProtectedMarkers(t *testing.T) {
	baseline, err := NewProtectedBaseline("agent.v1", []ProtectedCaseResult{
		{CaseID: "critical", Protected: true, Score: 0.9},
		{CaseID: "opt-out", Score: 0.9},
	})
	if err != nil {
		t.Fatalf("NewProtectedBaseline() error = %v", err)
	}

	gate := EvaluateProtectedGate([]ProtectedCaseResult{
		{CaseID: "critical", Protected: true, Score: 0.95},
		{CaseID: "opt-out", Score: 0.1},
	}, baseline, "agent.v1", 0)
	if !gate.Passed {
		t.Fatalf("gate.Passed = false, want unprotected opt-out regression ignored: %#v", gate)
	}
	if len(gate.ProtectedCaseIDs) != 1 || gate.ProtectedCaseIDs[0] != "critical" {
		t.Fatalf("ProtectedCaseIDs = %#v, want only critical", gate.ProtectedCaseIDs)
	}
}

func TestVerifyProtectedBaselineFileFailsMismatch(t *testing.T) {
	baseline, err := NewProtectedBaseline("agent.v1", []ProtectedCaseResult{
		{CaseID: "case-a", Score: 0.9},
	})
	if err != nil {
		t.Fatalf("NewProtectedBaseline() error = %v", err)
	}
	path := t.TempDir() + "/baseline.json"
	if err := WriteProtectedBaseline(path, baseline, "agent.v1"); err != nil {
		t.Fatalf("WriteProtectedBaseline() error = %v", err)
	}

	gate, err := VerifyProtectedBaselineFile(path, "agent.v1", []ProtectedCaseResult{
		{CaseID: "case-a", Score: 0.8},
	})
	if err == nil {
		t.Fatalf("VerifyProtectedBaselineFile() error = nil, want self-check failure")
	}
	if gate == nil || gate.Passed {
		t.Fatalf("gate = %#v, want failed gate", gate)
	}
}

func TestNewProtectedBaselineRefusesErroredCases(t *testing.T) {
	if _, err := NewProtectedBaseline("agent.v1", []ProtectedCaseResult{
		{CaseID: "bad", Score: 0, Error: "boom"},
	}); err == nil {
		t.Fatalf("NewProtectedBaseline() error = nil, want errored-case refusal")
	}
}

func TestProtectedCaseResultsFromHarness(t *testing.T) {
	run := &dspyoptimize.HarnessRunResult{
		Results: []dspyoptimize.HarnessExampleResult{
			{
				ExampleID: "protected",
				Result: &dspyoptimize.EvalResult{
					Score: 0.75,
					SideInfo: &dspyoptimize.SideInfo{
						Scores: map[string]float64{"recall": 1},
					},
				},
			},
			{
				ExampleID: "failed",
				Result: &dspyoptimize.EvalResult{
					Score: 0,
					SideInfo: &dspyoptimize.SideInfo{
						Diagnostics: map[string]interface{}{"evaluation_error": "boom"},
					},
				},
			},
		},
	}
	results := ProtectedCaseResultsFromHarness([]dspyoptimize.AgentExample{
		{ID: "protected", Metadata: map[string]interface{}{"protected": true}},
		{ID: "failed"},
	}, run)

	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if !results[0].Protected || results[0].Scores["recall"] != 1 {
		t.Fatalf("protected result = %#v, want protected recall=1", results[0])
	}
	if results[1].Error != "boom" {
		t.Fatalf("failed error = %q, want boom", results[1].Error)
	}
}
