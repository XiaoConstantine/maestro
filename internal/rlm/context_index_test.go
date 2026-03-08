package rlm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContextIndex_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		MaxAgeHours: 24,
		ChunkSize:   4000,
		AutoPersist: false,
	}

	idx, err := NewContextIndex(config)
	if err != nil {
		t.Fatalf("failed to create context index: %v", err)
	}

	entry := &ContextEntry{
		Key:         "/path/to/file.go",
		ContentHash: "abc123",
		FilePath:    "/path/to/file.go",
		Language:    "go",
		TokenCount:  1000,
		Summary:     "A Go source file",
	}

	idx.Put(entry)

	got, ok := idx.Get("/path/to/file.go")
	if !ok {
		t.Fatal("entry not found")
	}

	if got.ContentHash != "abc123" {
		t.Errorf("expected hash abc123, got %s", got.ContentHash)
	}
	if got.Language != "go" {
		t.Errorf("expected language go, got %s", got.Language)
	}
	if got.AccessCount != 1 {
		t.Errorf("expected access count 1, got %d", got.AccessCount)
	}
}

func TestContextIndex_Has(t *testing.T) {
	tmpDir := t.TempDir()
	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		MaxAgeHours: 24,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	if idx.Has("nonexistent") {
		t.Error("Has should return false for nonexistent entry")
	}

	idx.Put(&ContextEntry{Key: "test"})

	if !idx.Has("test") {
		t.Error("Has should return true for existing entry")
	}
}

func TestContextIndex_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	idx.Put(&ContextEntry{Key: "test"})
	if !idx.Has("test") {
		t.Fatal("entry should exist")
	}

	idx.Delete("test")
	if idx.Has("test") {
		t.Error("entry should be deleted")
	}
}

func TestContextIndex_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		AutoPersist: false,
	}

	idx1, _ := NewContextIndex(config)

	// Add entries
	idx1.Put(&ContextEntry{
		Key:         "file1.go",
		ContentHash: "hash1",
		Language:    "go",
		TokenCount:  1000,
	})
	idx1.Put(&ContextEntry{
		Key:         "file2.py",
		ContentHash: "hash2",
		Language:    "python",
		TokenCount:  2000,
	})

	// Save
	if err := idx1.Save(); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Create new index and load
	idx2, _ := NewContextIndex(config)

	entry1, ok := idx2.Get("file1.go")
	if !ok {
		t.Fatal("file1.go not found after load")
	}
	if entry1.Language != "go" {
		t.Errorf("expected go, got %s", entry1.Language)
	}

	entry2, ok := idx2.Get("file2.py")
	if !ok {
		t.Fatal("file2.py not found after load")
	}
	if entry2.TokenCount != 2000 {
		t.Errorf("expected 2000 tokens, got %d", entry2.TokenCount)
	}
}

func TestContextIndex_GetByFilePath_Staleness(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file
	testFile := filepath.Join(tmpDir, "test.go")
	err := os.WriteFile(testFile, []byte("package test"), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	info, _ := os.Stat(testFile)

	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	// Add entry with correct mod time
	idx.Put(&ContextEntry{
		Key:         testFile,
		FilePath:    testFile,
		FileModTime: info.ModTime(),
		ContentHash: "original",
	})

	// Should find entry
	entry, found, err := idx.GetByFilePath(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("entry should be found")
	}
	if entry.ContentHash != "original" {
		t.Errorf("expected original hash, got %s", entry.ContentHash)
	}

	// Modify file
	time.Sleep(10 * time.Millisecond)
	err = os.WriteFile(testFile, []byte("package test // modified"), 0644)
	if err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}

	// Should detect staleness
	_, found, err = idx.GetByFilePath(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("entry should be stale and not found")
	}
}

func TestContextIndex_IsStale(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.go")
	err := os.WriteFile(testFile, []byte("package test"), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	info, _ := os.Stat(testFile)

	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	// Entry doesn't exist - stale
	stale, err := idx.IsStale(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stale {
		t.Error("nonexistent entry should be stale")
	}

	// Add entry
	idx.Put(&ContextEntry{
		Key:         testFile,
		FilePath:    testFile,
		FileModTime: info.ModTime(),
	})

	// Not stale
	stale, err = idx.IsStale(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stale {
		t.Error("fresh entry should not be stale")
	}
}

func TestContextIndex_Cleanup(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file
	testFile := filepath.Join(tmpDir, "test.go")
	err := os.WriteFile(testFile, []byte("package test"), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	info, _ := os.Stat(testFile)

	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		MaxAgeHours: 0, // No age limit for this test
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	// Add entry for existing file
	idx.Put(&ContextEntry{
		Key:         testFile,
		FilePath:    testFile,
		FileModTime: info.ModTime(),
	})

	// Add entry for non-existent file
	idx.Put(&ContextEntry{
		Key:      filepath.Join(tmpDir, "nonexistent.go"),
		FilePath: filepath.Join(tmpDir, "nonexistent.go"),
	})

	removed := idx.Cleanup()
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	// Valid entry should still exist
	if !idx.Has(testFile) {
		t.Error("valid entry should remain")
	}
}

func TestContextIndex_Stats(t *testing.T) {
	tmpDir := t.TempDir()
	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	idx.Put(&ContextEntry{Key: "a.go", Language: "go", TokenCount: 1000})
	idx.Put(&ContextEntry{Key: "b.go", Language: "go", TokenCount: 2000})
	idx.Put(&ContextEntry{Key: "c.py", Language: "python", TokenCount: 500})

	stats := idx.Stats()

	if stats.TotalEntries != 3 {
		t.Errorf("expected 3 entries, got %d", stats.TotalEntries)
	}
	if stats.TotalTokens != 3500 {
		t.Errorf("expected 3500 tokens, got %d", stats.TotalTokens)
	}
	if stats.ByLanguage["go"] != 2 {
		t.Errorf("expected 2 go files, got %d", stats.ByLanguage["go"])
	}
	if stats.ByLanguage["python"] != 1 {
		t.Errorf("expected 1 python file, got %d", stats.ByLanguage["python"])
	}
}

func TestContextIndex_List(t *testing.T) {
	tmpDir := t.TempDir()
	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	idx.Put(&ContextEntry{Key: "a.go", Language: "go"})
	idx.Put(&ContextEntry{Key: "b.py", Language: "python"})
	idx.Put(&ContextEntry{Key: "c.go", Language: "go"})

	// All entries
	all := idx.List(nil)
	if len(all) != 3 {
		t.Errorf("expected 3 entries, got %d", len(all))
	}

	// Filter by language
	goFiles := idx.List(func(e *ContextEntry) bool {
		return e.Language == "go"
	})
	if len(goFiles) != 2 {
		t.Errorf("expected 2 go files, got %d", len(goFiles))
	}
}

func TestContextIndex_Clear(t *testing.T) {
	tmpDir := t.TempDir()
	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	idx.Put(&ContextEntry{Key: "a"})
	idx.Put(&ContextEntry{Key: "b"})

	idx.Clear()

	stats := idx.Stats()
	if stats.TotalEntries != 0 {
		t.Errorf("expected 0 entries after clear, got %d", stats.TotalEntries)
	}
}

func TestContextIndex_EvictLRU(t *testing.T) {
	tmpDir := t.TempDir()
	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  3,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	// Add entries
	idx.Put(&ContextEntry{Key: "a"})
	time.Sleep(10 * time.Millisecond)
	idx.Put(&ContextEntry{Key: "b"})
	time.Sleep(10 * time.Millisecond)
	idx.Put(&ContextEntry{Key: "c"})

	// Access 'a' to make it more recent
	idx.Get("a")

	// Add one more (should evict 'b')
	idx.Put(&ContextEntry{Key: "d"})

	// Allow eviction to run
	time.Sleep(50 * time.Millisecond)

	// Check that we have at most MaxEntries
	stats := idx.Stats()
	if stats.TotalEntries > 3 {
		t.Errorf("expected at most 3 entries, got %d", stats.TotalEntries)
	}
}

func TestHashContent(t *testing.T) {
	hash1 := HashContent("hello world")
	hash2 := HashContent("hello world")
	hash3 := HashContent("hello worlds")

	if hash1 != hash2 {
		t.Error("same content should produce same hash")
	}
	if hash1 == hash3 {
		t.Error("different content should produce different hash")
	}
	if len(hash1) != 64 {
		t.Errorf("expected 64 char hash, got %d", len(hash1))
	}
}

func TestChunkContent(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"

	// Small chunk size to force chunking
	chunks := ChunkContent(content, 5) // ~20 chars per chunk

	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}

	// Verify content is preserved
	var reconstructed string
	for i, chunk := range chunks {
		if i > 0 {
			reconstructed += "\n"
		}
		reconstructed += chunk
	}

	if reconstructed != content {
		t.Error("content not preserved after chunking")
	}
}

func TestContextIndex_IndexFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test Go file
	testFile := filepath.Join(tmpDir, "test.go")
	content := `package main

func main() {
    println("hello")
}
`
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		ChunkSize:   4000,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	entries, err := idx.IndexFile(testFile)
	if err != nil {
		t.Fatalf("failed to index file: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Language != "go" {
		t.Errorf("expected go language, got %s", entry.Language)
	}
	if entry.ContentHash == "" {
		t.Error("content hash should not be empty")
	}
	if entry.TokenCount == 0 {
		t.Error("token count should not be zero")
	}

	// Indexing again should return cached entry
	entries2, err := idx.IndexFile(testFile)
	if err != nil {
		t.Fatalf("failed to index file second time: %v", err)
	}
	if entries2[0].ContentHash != entries[0].ContentHash {
		t.Error("hash should match on re-index")
	}
}

func TestContextIndex_IndexDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files
	files := map[string]string{
		"main.go":    "package main",
		"util.go":    "package main\nfunc util() {}",
		"script.py":  "print('hello')",
		"readme.md":  "# Readme",
		".hidden.go": "package hidden", // Should be skipped
		"data.txt":   "some data",      // Not in default extensions
	}

	for name, content := range files {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("failed to create %s: %v", name, err)
		}
	}

	config := ContextIndexConfig{
		Dir:         filepath.Join(tmpDir, "index"),
		MaxEntries:  100,
		ChunkSize:   4000,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	indexed, err := idx.IndexDirectory(tmpDir, []string{".go", ".py"})
	if err != nil {
		t.Fatalf("failed to index directory: %v", err)
	}

	// Should index main.go, util.go, script.py (not .hidden.go or readme.md)
	if indexed != 3 {
		t.Errorf("expected 3 files indexed, got %d", indexed)
	}

	// Verify entries exist
	if !idx.Has(filepath.Join(tmpDir, "main.go")) {
		t.Error("main.go should be indexed")
	}
	if !idx.Has(filepath.Join(tmpDir, "script.py")) {
		t.Error("script.py should be indexed")
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/path/to/file.go", "go"},
		{"/path/to/file.py", "python"},
		{"/path/to/file.js", "javascript"},
		{"/path/to/file.ts", "typescript"},
		{"/path/to/file.tsx", "typescript"},
		{"/path/to/file.java", "java"},
		{"/path/to/file.rs", "rust"},
		{"/path/to/file.cpp", "cpp"},
		{"/path/to/file.unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := detectLanguage(tt.path)
			if got != tt.expected {
				t.Errorf("detectLanguage(%s) = %s, want %s", tt.path, got, tt.expected)
			}
		})
	}
}

func TestContextEntry_Entities(t *testing.T) {
	tmpDir := t.TempDir()
	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	entry := &ContextEntry{
		Key:      "test.go",
		Language: "go",
		Entities: []ExtractedEntity{
			{Name: "main", Type: "function", Line: 5, Exported: false},
			{Name: "Handler", Type: "type", Line: 10, Exported: true},
			{Name: "ProcessData", Type: "function", Line: 15, Exported: true, DocString: "ProcessData handles..."},
		},
	}

	idx.Put(entry)

	got, ok := idx.Get("test.go")
	if !ok {
		t.Fatal("entry not found")
	}

	if len(got.Entities) != 3 {
		t.Errorf("expected 3 entities, got %d", len(got.Entities))
	}

	if got.Entities[1].Exported != true {
		t.Error("Handler should be exported")
	}
}

func TestContextEntry_Metadata(t *testing.T) {
	tmpDir := t.TempDir()
	config := ContextIndexConfig{
		Dir:         tmpDir,
		MaxEntries:  100,
		AutoPersist: false,
	}

	idx, _ := NewContextIndex(config)

	entry := &ContextEntry{
		Key:      "test.go",
		Language: "go",
		Metadata: map[string]any{
			"complexity": 15,
			"loc":        100,
			"tests":      true,
		},
	}

	idx.Put(entry)

	// Save and reload
	if err := idx.Save(); err != nil {
		t.Fatalf("failed to save index: %v", err)
	}

	idx2, _ := NewContextIndex(config)
	got, ok := idx2.Get("test.go")
	if !ok {
		t.Fatal("entry not found after reload")
	}

	if got.Metadata == nil {
		t.Fatal("metadata should not be nil")
	}

	// JSON unmarshal converts numbers to float64
	if complexity, ok := got.Metadata["complexity"].(float64); !ok || complexity != 15 {
		t.Errorf("unexpected complexity: %v", got.Metadata["complexity"])
	}
}
