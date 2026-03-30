package orchestration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
