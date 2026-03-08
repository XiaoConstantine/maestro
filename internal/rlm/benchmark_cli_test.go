package rlm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadTestCasesHydratesBuiltInCases(t *testing.T) {
	tempDir := t.TempDir()
	largeSource := strings.Repeat("package sample\n\nfunc Example(v int) int { return v + 1 }\n", 8_000)
	if err := os.WriteFile(filepath.Join(tempDir, "sample.go"), []byte(largeSource), 0o644); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWD)
	}()

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to switch to temp directory: %v", err)
	}

	runner := NewBenchmarkRunner(DefaultBenchmarkCLIConfig(), nil, nil)
	if err := runner.LoadTestCases(); err != nil {
		t.Fatalf("LoadTestCases returned error: %v", err)
	}

	if len(runner.testCases) != 6 {
		t.Fatalf("expected 6 built-in test cases, got %d", len(runner.testCases))
	}

	sizeMaxLen := map[string]int{}
	for _, tc := range runner.testCases {
		if strings.TrimSpace(tc.Context) == "" {
			t.Fatalf("test case %q has empty context", tc.ID)
		}
		if strings.TrimSpace(tc.Query) == "" {
			t.Fatalf("test case %q has empty query", tc.ID)
		}

		for _, tag := range tc.Tags {
			if tag == "small" || tag == "medium" || tag == "large" {
				if len(tc.Context) > sizeMaxLen[tag] {
					sizeMaxLen[tag] = len(tc.Context)
				}
			}
		}
	}

	if sizeMaxLen["small"] == 0 || sizeMaxLen["medium"] == 0 || sizeMaxLen["large"] == 0 {
		t.Fatalf("missing size buckets in hydrated test cases: %#v", sizeMaxLen)
	}
	if sizeMaxLen["small"] > sizeMaxLen["medium"] {
		t.Fatalf("small context should not exceed medium context: %d > %d", sizeMaxLen["small"], sizeMaxLen["medium"])
	}
	if sizeMaxLen["medium"] > sizeMaxLen["large"] {
		t.Fatalf("medium context should not exceed large context: %d > %d", sizeMaxLen["medium"], sizeMaxLen["large"])
	}
}

func TestLoadTestCasesFailsWhenBuiltInContextIsUnavailable(t *testing.T) {
	tempDir := t.TempDir()

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWD)
	}()

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to switch to temp directory: %v", err)
	}

	runner := NewBenchmarkRunner(DefaultBenchmarkCLIConfig(), nil, nil)
	err = runner.LoadTestCases()
	if err == nil {
		t.Fatal("expected error when built-in context cannot be created")
	}
	if !strings.Contains(err.Error(), "no code files found") {
		t.Fatalf("expected no-code-files error, got: %v", err)
	}
}
