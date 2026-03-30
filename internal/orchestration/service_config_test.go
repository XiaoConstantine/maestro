package orchestration

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewMaestroServiceRejectsMemorySQLite(t *testing.T) {
	_, err := NewMaestroService(context.Background(), &ServiceConfig{
		MemoryType: MemorySQLite,
		MemoryPath: filepath.Join(t.TempDir(), "maestro.db"),
		Owner:      "XiaoConstantine",
		Repo:       "dspy-go",
	}, nil)
	if err == nil {
		t.Fatal("expected MemorySQLite to be rejected")
	}
	if !strings.Contains(err.Error(), "sessionevent.db") {
		t.Fatalf("err = %v, want sessionevent guidance", err)
	}
}
