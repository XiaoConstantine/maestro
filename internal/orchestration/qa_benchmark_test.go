package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/native"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/optimize"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/skills"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

type fakeQABenchmarkAgent struct {
	answer      string
	trace       *agents.ExecutionTrace
	nativeTrace *native.Trace
}

type capturingBenchmarkLLM struct {
	results      []map[string]any
	index        int
	capabilities []core.Capability
	prompts      []string
}

func (a *fakeQABenchmarkAgent) Execute(context.Context, map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{
		"final_answer": a.answer,
	}, nil
}

func (m *capturingBenchmarkLLM) Generate(context.Context, string, ...core.GenerateOption) (*core.LLMResponse, error) {
	return nil, fmt.Errorf("unexpected Generate call")
}

func (m *capturingBenchmarkLLM) GenerateWithJSON(context.Context, string, ...core.GenerateOption) (map[string]interface{}, error) {
	return nil, fmt.Errorf("unexpected GenerateWithJSON call")
}

func (m *capturingBenchmarkLLM) GenerateWithFunctions(_ context.Context, prompt string, _ []map[string]interface{}, _ ...core.GenerateOption) (map[string]interface{}, error) {
	m.prompts = append(m.prompts, prompt)
	if m.index >= len(m.results) {
		return nil, fmt.Errorf("no more stubbed results")
	}
	result := m.results[m.index]
	m.index++
	return result, nil
}

func (m *capturingBenchmarkLLM) CreateEmbedding(context.Context, string, ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	return nil, fmt.Errorf("unexpected CreateEmbedding call")
}

func (m *capturingBenchmarkLLM) CreateEmbeddings(context.Context, []string, ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	return nil, fmt.Errorf("unexpected CreateEmbeddings call")
}

func (m *capturingBenchmarkLLM) StreamGenerate(context.Context, string, ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("unexpected StreamGenerate call")
}

func (m *capturingBenchmarkLLM) GenerateWithContent(context.Context, []core.ContentBlock, ...core.GenerateOption) (*core.LLMResponse, error) {
	return nil, fmt.Errorf("unexpected GenerateWithContent call")
}

func (m *capturingBenchmarkLLM) StreamGenerateWithContent(context.Context, []core.ContentBlock, ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("unexpected StreamGenerateWithContent call")
}

func (m *capturingBenchmarkLLM) ProviderName() string            { return "stub" }
func (m *capturingBenchmarkLLM) ModelID() string                 { return "stub-model" }
func (m *capturingBenchmarkLLM) Capabilities() []core.Capability { return m.capabilities }

func (a *fakeQABenchmarkAgent) GetCapabilities() []core.Tool {
	return nil
}

func (a *fakeQABenchmarkAgent) GetMemory() agents.Memory { return nil }

func (a *fakeQABenchmarkAgent) GetArtifacts() optimize.AgentArtifacts {
	return defaultQAArtifacts()
}

func (a *fakeQABenchmarkAgent) SetArtifacts(optimize.AgentArtifacts) error { return nil }

func (a *fakeQABenchmarkAgent) Clone() (optimize.OptimizableAgent, error) {
	return &fakeQABenchmarkAgent{answer: a.answer, trace: a.trace, nativeTrace: a.nativeTrace}, nil
}

func (a *fakeQABenchmarkAgent) LastExecutionTrace() *agents.ExecutionTrace {
	if a.trace == nil {
		return nil
	}
	return a.trace.Clone()
}

func (a *fakeQABenchmarkAgent) LastNativeTrace() *native.Trace {
	if a.nativeTrace == nil {
		return nil
	}
	return a.nativeTrace.Clone()
}

func TestQABenchmarkEvaluatorScoresFactCoverage(t *testing.T) {
	evaluator := NewQABenchmarkEvaluator(DefaultQABenchmarkEvaluatorConfig())
	agent := &fakeQABenchmarkAgent{
		answer: "The repository is organized around pkg/agents and pkg/modules. pkg/agents contains runtime agents.",
		trace: &agents.ExecutionTrace{
			TokenUsage: map[string]int64{
				"total_tokens": 1234,
			},
		},
		nativeTrace: &native.Trace{
			TokenUsage: native.TokenUsage{
				PromptTokens:     700,
				CompletionTokens: 300,
				TotalTokens:      1000,
			},
		},
	}

	example := QABenchmarkExamples([]QABenchmarkCase{{
		ID:            "overview",
		RepoPath:      "/tmp/repo",
		Owner:         "XiaoConstantine",
		Repo:          "dspy-go",
		Question:      "How is the repository organized?",
		ExpectedFacts: []string{"pkg/agents", "pkg/modules"},
	}})[0]

	result, err := evaluator.Evaluate(context.Background(), agent, example)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Score != 1.0 {
		t.Fatalf("score = %v, want 1.0", result.Score)
	}
	if result.SideInfo == nil || result.SideInfo.Tokens["total_tokens"] != 1234 {
		t.Fatalf("tokens = %#v, want total_tokens=1234", result.SideInfo)
	}
	if result.SideInfo.Tokens["native_total_tokens"] != 1000 {
		t.Fatalf("native_total_tokens = %#v, want 1000", result.SideInfo.Tokens)
	}
}

func TestQABenchmarkEvaluatorPenalizesForbiddenFacts(t *testing.T) {
	evaluator := NewQABenchmarkEvaluator(DefaultQABenchmarkEvaluatorConfig())
	agent := &fakeQABenchmarkAgent{
		answer: "The repo centers on pkg/agents, but it definitely uses Django.",
	}

	example := QABenchmarkExamples([]QABenchmarkCase{{
		ID:             "forbidden",
		RepoPath:       "/tmp/repo",
		Owner:          "XiaoConstantine",
		Repo:           "dspy-go",
		Question:       "What does this repo do?",
		ExpectedFacts:  []string{"pkg/agents"},
		ForbiddenFacts: []string{"django"},
	}})[0]

	result, err := evaluator.Evaluate(context.Background(), agent, example)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Score >= 1.0 {
		t.Fatalf("score = %v, want penalty for forbidden fact", result.Score)
	}
	if got := result.SideInfo.Diagnostics["forbidden_hits"]; got == nil {
		t.Fatalf("forbidden_hits missing from diagnostics")
	}
}

func TestQABenchmarkEvaluatorRewardsCleanForbiddenOnlyAnswers(t *testing.T) {
	evaluator := NewQABenchmarkEvaluator(DefaultQABenchmarkEvaluatorConfig())
	agent := &fakeQABenchmarkAgent{
		answer: "I couldn't find a local RouterWorkflow definition in this repository.",
	}

	example := QABenchmarkExamples([]QABenchmarkCase{{
		ID:             "boundary-clean",
		RepoPath:       "/tmp/repo",
		Owner:          "XiaoConstantine",
		Repo:           "maestro",
		Question:       "Which file defines RouterWorkflow in this repository?",
		ForbiddenFacts: []string{"pkg/agents/workflows/router.go", "dspy-go"},
	}})[0]

	result, err := evaluator.Evaluate(context.Background(), agent, example)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Score != 1.0 {
		t.Fatalf("score = %v, want 1.0 for a clean forbidden-only case", result.Score)
	}
}

func TestQABenchmarkEvaluatorPenalizesForbiddenOnlyHallucinations(t *testing.T) {
	evaluator := NewQABenchmarkEvaluator(DefaultQABenchmarkEvaluatorConfig())
	agent := &fakeQABenchmarkAgent{
		answer: "RouterWorkflow is defined in pkg/agents/workflows/router.go.",
	}

	example := QABenchmarkExamples([]QABenchmarkCase{{
		ID:             "boundary-hallucination",
		RepoPath:       "/tmp/repo",
		Owner:          "XiaoConstantine",
		Repo:           "maestro",
		Question:       "Which file defines RouterWorkflow in this repository?",
		ForbiddenFacts: []string{"pkg/agents/workflows/router.go"},
	}})[0]

	result, err := evaluator.Evaluate(context.Background(), agent, example)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Score >= 1.0 {
		t.Fatalf("score = %v, want penalty for forbidden-only hallucination", result.Score)
	}
	if got := result.SideInfo.Diagnostics["forbidden_hits"]; got == nil {
		t.Fatalf("forbidden_hits missing from diagnostics")
	}
}

func TestQABenchmarkExamples_RoundTripPreservesOutputs(t *testing.T) {
	example := QABenchmarkExamples([]QABenchmarkCase{{
		ID:             "roundtrip",
		RepoPath:       "/tmp/repo",
		Owner:          "XiaoConstantine",
		Repo:           "maestro",
		Question:       "What does this repo do?",
		ExpectedFacts:  []string{"internal/orchestration"},
		ForbiddenFacts: []string{"django"},
	}})[0]

	data, err := json.Marshal(example)
	if err != nil {
		t.Fatalf("json.Marshal(example) error = %v", err)
	}

	var decoded optimize.AgentExample
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(example) error = %v", err)
	}

	benchmarkCase, err := qaBenchmarkCaseFromExample(decoded)
	if err != nil {
		t.Fatalf("qaBenchmarkCaseFromExample() error = %v", err)
	}
	if len(benchmarkCase.ExpectedFacts) != 1 || benchmarkCase.ExpectedFacts[0] != "internal/orchestration" {
		t.Fatalf("ExpectedFacts = %#v, want internal/orchestration", benchmarkCase.ExpectedFacts)
	}
	if len(benchmarkCase.ForbiddenFacts) != 1 || benchmarkCase.ForbiddenFacts[0] != "django" {
		t.Fatalf("ForbiddenFacts = %#v, want django", benchmarkCase.ForbiddenFacts)
	}
}

func TestNewQABenchmarkAgent_UsesOverlayOnlySkillPackDefaults(t *testing.T) {
	agent := NewQABenchmarkAgent(nil, nil, optimize.AgentArtifacts{})
	artifacts := agent.GetArtifacts()

	if got := artifacts.Text[optimize.ArtifactSkillPack]; got != "" {
		t.Fatalf("skill_pack = %q, want empty overlay by default", got)
	}
	if got := artifacts.Int["max_turns"]; got != qaNativeDefaultMaxTurns {
		t.Fatalf("max_turns = %d, want %d", got, qaNativeDefaultMaxTurns)
	}
}

func TestComposeQABenchmarkSystemPrompt_AppendsOverlayOnce(t *testing.T) {
	base := "Base prompt"
	overlay := "Prefer exact symbol lookup."
	composed := composeQABenchmarkSystemPrompt(base, overlay)

	if strings.Count(composed, base) != 1 {
		t.Fatalf("composed prompt = %q, want base prompt exactly once", composed)
	}
	if !strings.Contains(composed, "\n\nSKILL PACK:\n"+overlay) {
		t.Fatalf("composed prompt = %q, want SKILL PACK overlay", composed)
	}
}

func TestQABenchmarkAgent_SetArtifacts_PreservesExplicitEmptyOverlay(t *testing.T) {
	agent := NewQABenchmarkAgent(nil, nil, optimize.AgentArtifacts{
		Text: map[optimize.ArtifactKey]string{
			optimize.ArtifactSkillPack:  "Prefer exact symbol lookup.",
			optimize.ArtifactToolPolicy: "Use at most one broad search.",
		},
		Int: map[string]int{
			"max_turns": 4,
		},
	})

	if err := agent.SetArtifacts(optimize.AgentArtifacts{
		Text: map[optimize.ArtifactKey]string{
			optimize.ArtifactSkillPack:  "",
			optimize.ArtifactToolPolicy: "",
		},
		Int: map[string]int{
			"max_turns": 6,
		},
	}); err != nil {
		t.Fatalf("SetArtifacts() error = %v", err)
	}

	artifacts := agent.GetArtifacts()
	if got := artifacts.Text[optimize.ArtifactSkillPack]; got != "" {
		t.Fatalf("skill_pack = %q, want explicit empty overlay preserved", got)
	}
	if got, ok := artifacts.Text[optimize.ArtifactToolPolicy]; !ok || got != "" {
		t.Fatalf("tool_policy = %q, want explicit empty policy preserved", got)
	}
	if got := artifacts.Int["max_turns"]; got != 6 {
		t.Fatalf("max_turns = %d, want 6", got)
	}
}

func TestQABenchmarkAgent_Execute_ComposesBaseAndOverlayOnce(t *testing.T) {
	llm := &capturingBenchmarkLLM{
		capabilities: []core.Capability{core.CapabilityCompletion, core.CapabilityToolCalling},
		results: []map[string]any{
			{
				"function_call": map[string]any{
					"name":      "Finish",
					"arguments": map[string]any{"answer": "done"},
				},
			},
		},
	}

	agent := NewQABenchmarkAgent(llm, nil, optimize.AgentArtifacts{
		Text: map[optimize.ArtifactKey]string{
			optimize.ArtifactSkillPack: "Prefer narrow reads.",
		},
	})

	result, err := agent.Execute(context.Background(), map[string]interface{}{
		"repo_path": t.TempDir(),
		"owner":     "XiaoConstantine",
		"repo":      "maestro",
		"question":  "Where is QABenchmarkAgent defined?",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if completed, _ := result["completed"].(bool); !completed {
		t.Fatalf("completed = %#v, want true", result["completed"])
	}
	if len(llm.prompts) != 1 {
		t.Fatalf("captured prompts = %d, want 1", len(llm.prompts))
	}

	prompt := llm.prompts[0]
	basePrompt := strings.TrimSpace(qaNativeSystemPrompt)
	if strings.Count(prompt, basePrompt) != 1 {
		t.Fatalf("prompt contains base prompt %d times, want 1\nprompt=%q", strings.Count(prompt, basePrompt), prompt)
	}
	if strings.Count(prompt, "SKILL PACK:") != 1 {
		t.Fatalf("prompt contains SKILL PACK marker %d times, want 1\nprompt=%q", strings.Count(prompt, "SKILL PACK:"), prompt)
	}
	if !strings.Contains(prompt, "Prefer narrow reads.") {
		t.Fatalf("prompt = %q, want overlay text", prompt)
	}
}

func TestQASkillStorePublishPath_LoadsOverlayWithoutDuplication(t *testing.T) {
	store := skills.NewFileStore(filepath.Join(t.TempDir(), "skills.json"))
	overlay := "Prefer exact symbol lookups before broad searches."
	if err := store.Save(context.Background(), skills.Skill{
		Name:    "qa-gepa",
		Domain:  qaDefaultSkillDomain,
		Content: overlay,
		Version: 1,
	}); err != nil {
		t.Fatalf("store.Save() error = %v", err)
	}

	llm := &capturingBenchmarkLLM{
		capabilities: []core.Capability{core.CapabilityCompletion, core.CapabilityToolCalling},
		results: []map[string]any{
			{
				"function_call": map[string]any{
					"name":      "Finish",
					"arguments": map[string]any{"answer": "done"},
				},
			},
		},
	}

	runtimeAgent, err := native.NewAgent(llm, buildNativeQAConfig(defaultQAArtifacts(), agents.NewInMemoryStore(), "", nil, store, qaDefaultSkillDomain))
	if err != nil {
		t.Fatalf("native.NewAgent() error = %v", err)
	}

	loadedSkill := runtimeAgent.GetLoadedSkill()
	if loadedSkill == nil {
		t.Fatalf("GetLoadedSkill() = nil, want persisted skill")
	}
	if loadedSkill.Content != overlay {
		t.Fatalf("loaded skill content = %q, want %q", loadedSkill.Content, overlay)
	}
	if loadedSkill.Version != 1 {
		t.Fatalf("loaded skill version = %d, want 1", loadedSkill.Version)
	}

	artifacts := runtimeAgent.GetArtifacts()
	composed := artifacts.Text[optimize.ArtifactSkillPack]
	basePrompt := strings.TrimSpace(qaNativeSystemPrompt)
	if strings.Count(composed, basePrompt) != 1 {
		t.Fatalf("artifact prompt contains base prompt %d times, want 1\nprompt=%q", strings.Count(composed, basePrompt), composed)
	}
	if strings.Count(composed, "SKILL PACK:") != 1 {
		t.Fatalf("artifact prompt contains SKILL PACK marker %d times, want 1\nprompt=%q", strings.Count(composed, "SKILL PACK:"), composed)
	}
	if !strings.Contains(composed, overlay) {
		t.Fatalf("artifact prompt = %q, want persisted overlay", composed)
	}

	result, err := runtimeAgent.Execute(context.Background(), map[string]interface{}{
		"task": "Finish immediately.",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if completed, _ := result["completed"].(bool); !completed {
		t.Fatalf("completed = %#v, want true", result["completed"])
	}
	if len(llm.prompts) != 1 {
		t.Fatalf("captured prompts = %d, want 1", len(llm.prompts))
	}
	if strings.Count(llm.prompts[0], basePrompt) != 1 {
		t.Fatalf("runtime prompt contains base prompt %d times, want 1\nprompt=%q", strings.Count(llm.prompts[0], basePrompt), llm.prompts[0])
	}
	if !strings.Contains(llm.prompts[0], overlay) {
		t.Fatalf("runtime prompt = %q, want persisted overlay", llm.prompts[0])
	}
}

func TestLoadQABenchmarkSuite_ResolvesRelativeRepoPaths(t *testing.T) {
	suiteDir := filepath.Join(t.TempDir(), "benchmarks")
	if err := os.MkdirAll(suiteDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	repoDir := filepath.Join(suiteDir, "..", "fixture-repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(repoDir) error = %v", err)
	}

	suitePath := filepath.Join(suiteDir, "qa_suite.json")
	data := []byte(`{"cases":[{"id":"c1","repo_path":"../fixture-repo","owner":"XiaoConstantine","repo":"maestro","question":"Where is QABenchmarkAgent defined?","expected_facts":["internal/orchestration/qa_benchmark.go"]}]}`)
	if err := os.WriteFile(suitePath, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cases, err := LoadQABenchmarkSuite(suitePath)
	if err != nil {
		t.Fatalf("LoadQABenchmarkSuite() error = %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("len(cases) = %d, want 1", len(cases))
	}
	if got, want := cases[0].RepoPath, filepath.Clean(repoDir); got != want {
		t.Fatalf("RepoPath = %q, want %q", got, want)
	}
}
