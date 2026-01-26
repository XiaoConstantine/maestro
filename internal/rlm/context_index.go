// Package rlm provides RLM (Recursive Language Model) integration for Maestro.
package rlm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ContextIndexConfig configures the context index behavior.
type ContextIndexConfig struct {
	// Dir is the directory to store the index (default: ~/.maestro/context_index/)
	Dir string

	// MaxEntries limits how many entries to cache (default: 1000)
	MaxEntries int

	// MaxAgeHours specifies how long entries are valid (default: 168 = 7 days)
	MaxAgeHours int

	// ChunkSize is the target size for content chunks (default: 4000 tokens ~ 16KB)
	ChunkSize int

	// AutoPersist enables automatic persistence on updates
	AutoPersist bool
}

// DefaultContextIndexConfig returns sensible defaults.
func DefaultContextIndexConfig() ContextIndexConfig {
	homeDir, _ := os.UserHomeDir()
	return ContextIndexConfig{
		Dir:         filepath.Join(homeDir, ".maestro", "context_index"),
		MaxEntries:  1000,
		MaxAgeHours: 168, // 7 days
		ChunkSize:   4000,
		AutoPersist: true,
	}
}

// ContextIndex provides persistent caching of code analysis across sessions.
type ContextIndex struct {
	config ContextIndexConfig

	mu      sync.RWMutex
	entries map[string]*ContextEntry
	dirty   bool

	// saveMu serializes Save operations to prevent concurrent writes
	// to the same temp file when AutoPersist spawns multiple goroutines
	saveMu sync.Mutex
}

// ContextEntry represents a cached analysis of a file or content chunk.
type ContextEntry struct {
	// Key is the unique identifier (typically file path or content hash)
	Key string `json:"key"`

	// ContentHash is the SHA-256 hash of the content
	ContentHash string `json:"content_hash"`

	// FilePath is the original file path (if applicable)
	FilePath string `json:"file_path,omitempty"`

	// FileModTime is the last modification time of the source file
	FileModTime time.Time `json:"file_mod_time,omitempty"`

	// ChunkIndex is the index of this chunk (if content was chunked)
	ChunkIndex int `json:"chunk_index,omitempty"`

	// TotalChunks is the total number of chunks for this content
	TotalChunks int `json:"total_chunks,omitempty"`

	// Summary is a condensed description of the content
	Summary string `json:"summary,omitempty"`

	// Entities are key entities extracted from the content (functions, types, etc.)
	Entities []ExtractedEntity `json:"entities,omitempty"`

	// Dependencies are detected dependencies or imports
	Dependencies []string `json:"dependencies,omitempty"`

	// Language is the detected programming language
	Language string `json:"language,omitempty"`

	// TokenCount is the estimated token count of the original content
	TokenCount int `json:"token_count"`

	// Embedding is an optional vector embedding (if computed)
	Embedding []float32 `json:"embedding,omitempty"`

	// Metadata contains additional structured information
	Metadata map[string]any `json:"metadata,omitempty"`

	// Timestamps
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
	AccessCount int       `json:"access_count"`
}

// ExtractedEntity represents a code entity (function, type, variable, etc.)
type ExtractedEntity struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // function, type, variable, class, etc.
	Line     int    `json:"line,omitempty"`
	Exported bool   `json:"exported,omitempty"`
	DocString string `json:"doc,omitempty"`
}

// NewContextIndex creates a new context index.
func NewContextIndex(config ContextIndexConfig) (*ContextIndex, error) {
	if config.Dir == "" {
		config = DefaultContextIndexConfig()
	}

	// Ensure directory exists
	if err := os.MkdirAll(config.Dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create index directory: %w", err)
	}

	idx := &ContextIndex{
		config:  config,
		entries: make(map[string]*ContextEntry),
	}

	// Load existing index
	if err := idx.load(); err != nil {
		// Non-fatal: start fresh if load fails
		idx.entries = make(map[string]*ContextEntry)
	}

	return idx, nil
}

// indexPath returns the path to the index file.
func (idx *ContextIndex) indexPath() string {
	return filepath.Join(idx.config.Dir, "index.json")
}

// load reads the index from disk.
func (idx *ContextIndex) load() error {
	data, err := os.ReadFile(idx.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var entries []*ContextEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.entries = make(map[string]*ContextEntry)
	for _, entry := range entries {
		idx.entries[entry.Key] = entry
	}

	return nil
}

// Save persists the index to disk.
// Save is serialized via saveMu to prevent concurrent writes when
// AutoPersist spawns multiple goroutines.
func (idx *ContextIndex) Save() error {
	// Serialize saves to prevent concurrent writes to the temp file
	idx.saveMu.Lock()
	defer idx.saveMu.Unlock()

	// Snapshot entries under read lock
	idx.mu.RLock()
	entries := make([]*ContextEntry, 0, len(idx.entries))
	for _, entry := range idx.entries {
		entries = append(entries, entry)
	}
	idx.mu.RUnlock()

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}

	// Use unique temp file to prevent conflicts
	tmpPath := fmt.Sprintf("%s.tmp.%d", idx.indexPath(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}

	if err := os.Rename(tmpPath, idx.indexPath()); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to finalize index: %w", err)
	}

	// Mark as clean after successful save
	idx.mu.Lock()
	idx.dirty = false
	idx.mu.Unlock()

	return nil
}

// Get retrieves an entry by key.
func (idx *ContextIndex) Get(key string) (*ContextEntry, bool) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	entry, ok := idx.entries[key]
	if ok {
		entry.LastUsedAt = time.Now()
		entry.AccessCount++
		idx.dirty = true
	}
	return entry, ok
}

// GetByFilePath retrieves an entry for a file path, checking staleness.
func (idx *ContextIndex) GetByFilePath(path string) (*ContextEntry, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	entry, ok := idx.entries[path]
	if !ok {
		return nil, false, nil
	}

	// Check if stale
	if !entry.FileModTime.Equal(info.ModTime()) {
		// File has been modified, entry is stale
		delete(idx.entries, path)
		idx.dirty = true
		return nil, false, nil
	}

	entry.LastUsedAt = time.Now()
	entry.AccessCount++
	idx.dirty = true

	return entry, true, nil
}

// Put adds or updates an entry.
func (idx *ContextIndex) Put(entry *ContextEntry) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	entry.LastUsedAt = time.Now()

	idx.entries[entry.Key] = entry
	idx.dirty = true

	// Auto-persist if enabled
	if idx.config.AutoPersist {
		go idx.Save()
	}

	// Evict if over limit
	if len(idx.entries) > idx.config.MaxEntries {
		go idx.evictLRU()
	}
}

// Delete removes an entry by key.
func (idx *ContextIndex) Delete(key string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	delete(idx.entries, key)
	idx.dirty = true
}

// Has checks if an entry exists and is not stale.
func (idx *ContextIndex) Has(key string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	entry, ok := idx.entries[key]
	if !ok {
		return false
	}

	// Check age
	if idx.config.MaxAgeHours > 0 {
		maxAge := time.Duration(idx.config.MaxAgeHours) * time.Hour
		if time.Since(entry.CreatedAt) > maxAge {
			return false
		}
	}

	return true
}

// IsStale checks if a file's cached entry is stale.
func (idx *ContextIndex) IsStale(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return true, err
	}

	idx.mu.RLock()
	entry, ok := idx.entries[path]
	idx.mu.RUnlock()

	if !ok {
		return true, nil
	}

	return !entry.FileModTime.Equal(info.ModTime()), nil
}

// evictLRU removes the least recently used entries.
func (idx *ContextIndex) evictLRU() {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if len(idx.entries) <= idx.config.MaxEntries {
		return
	}

	// Sort by last used time
	type entryWithKey struct {
		key   string
		entry *ContextEntry
	}
	sorted := make([]entryWithKey, 0, len(idx.entries))
	for k, v := range idx.entries {
		sorted = append(sorted, entryWithKey{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].entry.LastUsedAt.Before(sorted[j].entry.LastUsedAt)
	})

	// Remove oldest entries
	removeCount := len(idx.entries) - idx.config.MaxEntries
	for i := 0; i < removeCount && i < len(sorted); i++ {
		delete(idx.entries, sorted[i].key)
	}
	idx.dirty = true
}

// Cleanup removes stale and expired entries.
func (idx *ContextIndex) Cleanup() (removed int) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	maxAge := time.Duration(idx.config.MaxAgeHours) * time.Hour
	now := time.Now()

	for key, entry := range idx.entries {
		shouldRemove := false

		// Check age
		if idx.config.MaxAgeHours > 0 && now.Sub(entry.CreatedAt) > maxAge {
			shouldRemove = true
		}

		// Check file staleness
		if entry.FilePath != "" {
			info, err := os.Stat(entry.FilePath)
			if err != nil || !entry.FileModTime.Equal(info.ModTime()) {
				shouldRemove = true
			}
		}

		if shouldRemove {
			delete(idx.entries, key)
			removed++
		}
	}

	if removed > 0 {
		idx.dirty = true
	}
	return removed
}

// Stats returns statistics about the index.
type IndexStats struct {
	TotalEntries  int
	TotalTokens   int
	ByLanguage    map[string]int
	OldestEntry   time.Time
	NewestEntry   time.Time
	AverageAccess float64
}

// Stats returns index statistics.
func (idx *ContextIndex) Stats() IndexStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	stats := IndexStats{
		TotalEntries: len(idx.entries),
		ByLanguage:   make(map[string]int),
	}

	var totalAccess int
	for _, entry := range idx.entries {
		stats.TotalTokens += entry.TokenCount
		if entry.Language != "" {
			stats.ByLanguage[entry.Language]++
		}
		totalAccess += entry.AccessCount

		if stats.OldestEntry.IsZero() || entry.CreatedAt.Before(stats.OldestEntry) {
			stats.OldestEntry = entry.CreatedAt
		}
		if entry.CreatedAt.After(stats.NewestEntry) {
			stats.NewestEntry = entry.CreatedAt
		}
	}

	if len(idx.entries) > 0 {
		stats.AverageAccess = float64(totalAccess) / float64(len(idx.entries))
	}

	return stats
}

// List returns all entry keys, optionally filtered.
func (idx *ContextIndex) List(filter func(*ContextEntry) bool) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var keys []string
	for key, entry := range idx.entries {
		if filter == nil || filter(entry) {
			keys = append(keys, key)
		}
	}
	return keys
}

// Clear removes all entries.
func (idx *ContextIndex) Clear() {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.entries = make(map[string]*ContextEntry)
	idx.dirty = true
}

// HashContent computes a SHA-256 hash of content.
func HashContent(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// ChunkContent splits content into chunks of approximately the target size.
func ChunkContent(content string, targetTokens int) []string {
	if targetTokens <= 0 {
		targetTokens = 4000
	}

	// Estimate 4 chars per token
	targetChars := targetTokens * 4

	lines := strings.Split(content, "\n")
	var chunks []string
	var currentChunk strings.Builder

	for _, line := range lines {
		// Check if adding this line would exceed target
		if currentChunk.Len() > 0 && currentChunk.Len()+len(line)+1 > targetChars {
			chunks = append(chunks, currentChunk.String())
			currentChunk.Reset()
		}

		if currentChunk.Len() > 0 {
			currentChunk.WriteByte('\n')
		}
		currentChunk.WriteString(line)
	}

	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks
}

// IndexFile creates context entries for a file.
func (idx *ContextIndex) IndexFile(path string) ([]*ContextEntry, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	contentStr := string(content)
	contentHash := HashContent(contentStr)

	// Detect language from extension
	language := detectLanguage(path)

	// Check if we already have this exact content
	if existing, ok := idx.Get(path); ok {
		if existing.ContentHash == contentHash {
			return []*ContextEntry{existing}, nil
		}
	}

	// Chunk if content is large
	chunks := ChunkContent(contentStr, idx.config.ChunkSize)

	var entries []*ContextEntry
	for i, chunk := range chunks {
		entry := &ContextEntry{
			Key:         path,
			ContentHash: contentHash,
			FilePath:    path,
			FileModTime: info.ModTime(),
			ChunkIndex:  i,
			TotalChunks: len(chunks),
			Language:    language,
			TokenCount:  len(chunk) / 4,
			CreatedAt:   time.Now(),
			Metadata:    make(map[string]any),
		}

		// For multi-chunk files, use composite keys
		if len(chunks) > 1 {
			entry.Key = fmt.Sprintf("%s#chunk%d", path, i)
		}

		idx.Put(entry)
		entries = append(entries, entry)
	}

	return entries, nil
}

// detectLanguage detects programming language from file extension.
func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	languages := map[string]string{
		".go":    "go",
		".py":    "python",
		".js":    "javascript",
		".ts":    "typescript",
		".tsx":   "typescript",
		".jsx":   "javascript",
		".java":  "java",
		".c":     "c",
		".cpp":   "cpp",
		".h":     "c",
		".hpp":   "cpp",
		".rs":    "rust",
		".rb":    "ruby",
		".php":   "php",
		".swift": "swift",
		".kt":    "kotlin",
		".md":    "markdown",
		".json":  "json",
		".yaml":  "yaml",
		".yml":   "yaml",
		".sql":   "sql",
		".sh":    "shell",
		".bash":  "shell",
	}
	return languages[ext]
}

// IndexDirectory indexes all files in a directory.
func (idx *ContextIndex) IndexDirectory(dir string, extensions []string) (int, error) {
	if len(extensions) == 0 {
		extensions = []string{".go", ".py", ".js", ".ts", ".tsx", ".java", ".rs", ".rb", ".cpp", ".c", ".h"}
	}

	extSet := make(map[string]bool)
	for _, ext := range extensions {
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		extSet[strings.ToLower(ext)] = true
	}

	var indexed int
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Skip hidden files and common non-code directories
		name := info.Name()
		if strings.HasPrefix(name, ".") ||
			name == "node_modules" ||
			name == "vendor" ||
			name == "__pycache__" ||
			name == "dist" ||
			name == "build" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !extSet[ext] {
			return nil
		}

		// Check if already indexed and not stale
		stale, _ := idx.IsStale(path)
		if !stale {
			indexed++
			return nil
		}

		if _, err := idx.IndexFile(path); err == nil {
			indexed++
		}

		return nil
	})

	return indexed, err
}
