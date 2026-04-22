package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestResolveCLIStoragePath_DirectoryPathUsesRepoDBName(t *testing.T) {
	cfg := &config{
		owner:      "XiaoConstantine",
		repo:       "maestro",
		memoryPath: filepath.Join(t.TempDir(), "state") + string(filepath.Separator),
	}

	got, err := resolveCLIStoragePath(context.Background(), cfg)
	if err != nil {
		t.Fatalf("resolveCLIStoragePath() error = %v", err)
	}

	want := filepath.Join(cfg.memoryPath, "XiaoConstantine_maestro.db")
	if got != want {
		t.Fatalf("resolveCLIStoragePath() = %q, want %q", got, want)
	}
}

func TestResolveCLIStoragePath_FilePathPreserved(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom.db")
	cfg := &config{
		owner:      "XiaoConstantine",
		repo:       "maestro",
		memoryPath: want,
	}

	got, err := resolveCLIStoragePath(context.Background(), cfg)
	if err != nil {
		t.Fatalf("resolveCLIStoragePath() error = %v", err)
	}
	if got != want {
		t.Fatalf("resolveCLIStoragePath() = %q, want %q", got, want)
	}
}
