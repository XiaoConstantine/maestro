package orchestration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	modrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
)

func TestShouldUseRLMOverviewQuery(t *testing.T) {
	tests := []struct {
		name     string
		question string
		want     bool
	}{
		{
			name:     "repo architecture question",
			question: "What is the architecture of this repository?",
			want:     true,
		},
		{
			name:     "main packages question",
			question: "What are the main packages in this repository and what is each for?",
			want:     true,
		},
		{
			name:     "specific package question",
			question: "How is pkg/agents organized?",
			want:     false,
		},
		{
			name:     "specific function question",
			question: "How does NewAgent work?",
			want:     false,
		},
		{
			name:     "specific package name question",
			question: "Give an overview of package agents",
			want:     false,
		},
		{
			name:     "overview question with short acronym stays on RLM path",
			question: "Give me an overview of the API structure in this repository",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUseRLMOverviewQuery(tt.question); got != tt.want {
				t.Fatalf("shouldUseRLMOverviewQuery(%q) = %v, want %v", tt.question, got, tt.want)
			}
		})
	}
}

func TestBuildRLMOverviewManifestIncludesMetadataOnly(t *testing.T) {
	repoDir := t.TempDir()
	mustWriteFile(t, filepath.Join(repoDir, "go.mod"), "module github.com/example/repo\n\ngo 1.25.0\n")
	mustWriteFile(t, filepath.Join(repoDir, "README.md"), "# Example Repo\n\nA repo for testing.\n")
	mustWriteFile(t, filepath.Join(repoDir, "pkg", "agents", "README.md"), "# Agents\n\nAgent package docs.\n")
	mustWriteFile(t, filepath.Join(repoDir, "pkg", "core", "doc.go"), "// Package core contains shared runtime primitives.\npackage core\n")
	mustWriteFile(t, filepath.Join(repoDir, "pkg", "core", "core.go"), "package core\n\nfunc Core() {}\n")

	manifest, err := buildRLMOverviewManifest(repoDir, 20000)
	if err != nil {
		t.Fatalf("buildRLMOverviewManifest() error = %v", err)
	}

	if !strings.Contains(manifest.Context, "go.mod") {
		t.Fatalf("manifest missing go.mod section: %s", manifest.Context)
	}
	if !strings.Contains(manifest.Context, "Repository README") {
		t.Fatalf("manifest missing root README section: %s", manifest.Context)
	}
	if !strings.Contains(manifest.Context, "Package Metadata: pkg/agents") {
		t.Fatalf("manifest missing pkg/agents metadata: %s", manifest.Context)
	}
	if !strings.Contains(manifest.Context, "Package Metadata: pkg/core") {
		t.Fatalf("manifest missing pkg/core metadata: %s", manifest.Context)
	}
	if strings.Contains(manifest.Context, "func Core()") {
		t.Fatalf("manifest should not include representative source files: %s", manifest.Context)
	}
	if !strings.Contains(manifest.Context, "pkg/core/core.go") {
		t.Fatalf("manifest should include package file names for grounding: %s", manifest.Context)
	}

	wantSources := []string{"go.mod", "README.md", "pkg/agents/README.md", "pkg/core/doc.go"}
	for _, source := range wantSources {
		if !containsString(manifest.Sources, source) {
			t.Fatalf("manifest sources missing %q: %#v", source, manifest.Sources)
		}
	}
	if containsString(manifest.Sources, "pkg/core/core.go") {
		t.Fatalf("manifest sources should exclude representative source files: %#v", manifest.Sources)
	}
}

func TestBuildRLMOverviewFocusedEvidenceFindsRelevantPaths(t *testing.T) {
	repoDir := t.TempDir()
	mustWriteFile(t, filepath.Join(repoDir, "go.mod"), "module github.com/example/maestro\n")
	mustWriteFile(t, filepath.Join(repoDir, "internal", "github", "client.go"), "package github\n\ntype Client struct{}\n")
	mustWriteFile(t, filepath.Join(repoDir, "internal", "github", "mcp_review.go"), "package github\n\nfunc ReviewTool() {}\n")
	mustWriteFile(t, filepath.Join(repoDir, "internal", "github", "mcp_bash.go"), "package github\n\nfunc BashTool() {}\n")
	mustWriteFile(t, filepath.Join(repoDir, "internal", "search", "planner.go"), "package search\n")

	manifest, err := buildRLMOverviewManifest(repoDir, 30000)
	if err != nil {
		t.Fatalf("buildRLMOverviewManifest() error = %v", err)
	}
	evidence := buildRLMOverviewFocusedEvidence(repoDir, manifest, "What GitHub-related adapters does Maestro expose?", 12000)

	for _, want := range []string{"internal/github/client.go", "internal/github/mcp_review.go", "internal/github/mcp_bash.go"} {
		if !strings.Contains(evidence.Text, want) {
			t.Fatalf("focused evidence missing %q:\n%s", want, evidence.Text)
		}
	}
	if !containsString(evidence.Sources, "internal/github/client.go") {
		t.Fatalf("focused evidence sources missing github client: %#v", evidence.Sources)
	}
}

func TestBuildRLMOverviewFocusedEvidenceFindsSymbolsAndLiterals(t *testing.T) {
	repoDir := t.TempDir()
	mustWriteFile(t, filepath.Join(repoDir, "go.mod"), "module github.com/example/maestro\n")
	mustWriteFile(t, filepath.Join(repoDir, "internal", "orchestration", "rlm_overview.go"), `package orchestration

func (s *Service) handleRLMOverview() {}
func buildRLMOverviewManifest() {}
func record() {
	metadata["rlm_usage"] = map[string]any{}
}
`)
	mustWriteFile(t, filepath.Join(repoDir, "internal", "orchestration", "rlm_artifacts.go"), `package orchestration

func buildRLMOverviewQueryWithOverlay(question, overlay string) string { return question + overlay }
`)

	manifest, err := buildRLMOverviewManifest(repoDir, 30000)
	if err != nil {
		t.Fatalf("buildRLMOverviewManifest() error = %v", err)
	}
	evidence := buildRLMOverviewFocusedEvidence(repoDir, manifest, "What are the main pieces of the RLM overview path?", 12000)

	for _, want := range []string{"handleRLMOverview", "buildRLMOverviewManifest", "buildRLMOverviewQueryWithOverlay", "rlm_usage"} {
		if !strings.Contains(evidence.Text, want) {
			t.Fatalf("focused evidence missing %q:\n%s", want, evidence.Text)
		}
	}
}

func TestParseRLMOverviewOutput(t *testing.T) {
	raw := "```json\n{\"answer\":\"Repo overview\",\"needs_verification\":[{\"package\":\"pkg/agents\",\"reason\":\"verify package layout\"}]}\n```"

	parsed, err := parseRLMOverviewOutput(raw)
	if err != nil {
		t.Fatalf("parseRLMOverviewOutput() error = %v", err)
	}
	if parsed.Answer != "Repo overview" {
		t.Fatalf("Answer = %q, want Repo overview", parsed.Answer)
	}
	if len(parsed.NeedsVerification) != 1 {
		t.Fatalf("NeedsVerification len = %d, want 1", len(parsed.NeedsVerification))
	}
	if parsed.NeedsVerification[0].Package != "pkg/agents" {
		t.Fatalf("Package = %q, want pkg/agents", parsed.NeedsVerification[0].Package)
	}
}

func TestParseRLMOverviewOutputWithFallback(t *testing.T) {
	primary := "res"
	fallback := "{\"answer\":\"Repo overview\",\"needs_verification\":[]}"

	raw, parsed, err := parseRLMOverviewOutputWithFallback(primary, fallback)
	if err != nil {
		t.Fatalf("parseRLMOverviewOutputWithFallback() error = %v", err)
	}
	if raw != fallback {
		t.Fatalf("raw = %q, want fallback %q", raw, fallback)
	}
	if parsed.Answer != "Repo overview" {
		t.Fatalf("Answer = %q, want Repo overview", parsed.Answer)
	}
}

func TestBuildRLMVerificationQuestion(t *testing.T) {
	question := buildRLMVerificationQuestion("What are the main packages?", rlmOverviewVerification{
		Package: "pkg/agents",
		Reason:  "clarify the package's role",
	})
	if !strings.Contains(question, "pkg/agents") {
		t.Fatalf("verification question missing package: %s", question)
	}
	if !strings.Contains(question, "What are the main packages?") {
		t.Fatalf("verification question missing original question: %s", question)
	}
}

func TestRLMOverviewBenchmarkOptionsGuardFullContextQueries(t *testing.T) {
	cfg := modrlm.DefaultConfig()
	for _, opt := range rlmOverviewBenchmarkModuleOptions(DefaultRLMOverviewBenchmarkAgentConfig()) {
		opt(&cfg)
	}
	if cfg.MaxFullContextQueryChars != rlmMaxFullContextQueryChars {
		t.Fatalf("MaxFullContextQueryChars = %d, want %d", cfg.MaxFullContextQueryChars, rlmMaxFullContextQueryChars)
	}
	if cfg.ContextInfoPreviewChars != 0 {
		t.Fatalf("ContextInfoPreviewChars = %d, want 0 to force explicit manifest inspection", cfg.ContextInfoPreviewChars)
	}
	if cfg.AdaptiveIteration == nil {
		t.Fatalf("AdaptiveIteration = nil, want bounded adaptive iteration")
	}
	if cfg.AdaptiveIteration.MaxIterations != rlmOverviewMaxIterations {
		t.Fatalf("AdaptiveIteration.MaxIterations = %d, want %d", cfg.AdaptiveIteration.MaxIterations, rlmOverviewMaxIterations)
	}
	if cfg.AdaptiveIteration.EnableEarlyTermination {
		t.Fatalf("AdaptiveIteration.EnableEarlyTermination = true, want false to avoid shallow default answers")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
