package embedding

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

// Cache provides an in-memory cache for embedding vectors.
type Cache struct {
	mu      sync.RWMutex
	cache   map[string]*CachedEmbedding
	maxSize int
}

// CachedEmbedding stores an embedding result with metadata.
type CachedEmbedding struct {
	Result    *core.EmbeddingResult
	Timestamp time.Time
	Hits      int
	Source    string // "local" or "cloud"
}

// NewCache creates a new embedding cache.
func NewCache(maxSize int) *Cache {
	if maxSize <= 0 {
		maxSize = 10000 // Default to 10k entries
	}

	return &Cache{
		cache:   make(map[string]*CachedEmbedding),
		maxSize: maxSize,
	}
}

// cacheKey generates a cache key from input text.
func (ec *Cache) cacheKey(input string) string {
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash)
}

// Get retrieves a cached embedding result.
func (ec *Cache) Get(input string) *core.EmbeddingResult {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	key := ec.cacheKey(input)
	if cached, exists := ec.cache[key]; exists {
		cached.Hits++
		return cached.Result
	}

	return nil
}

// GetWithMetadata retrieves a cached embedding result along with metadata.
func (ec *Cache) GetWithMetadata(input string) (*core.EmbeddingResult, *CachedEmbedding) {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	key := ec.cacheKey(input)
	if cached, exists := ec.cache[key]; exists {
		cached.Hits++
		return cached.Result, cached
	}

	return nil, nil
}

// Set stores an embedding result in the cache.
func (ec *Cache) Set(input string, result *core.EmbeddingResult, source string) {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	key := ec.cacheKey(input)

	// Simple eviction: if cache is full, remove oldest entry
	if len(ec.cache) >= ec.maxSize {
		var oldestKey string
		var oldestTime time.Time

		for k, v := range ec.cache {
			if oldestTime.IsZero() || v.Timestamp.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.Timestamp
			}
		}

		if oldestKey != "" {
			delete(ec.cache, oldestKey)
		}
	}

	ec.cache[key] = &CachedEmbedding{
		Result:    result,
		Timestamp: time.Now(),
		Hits:      0,
		Source:    source,
	}
}

// Clear removes all cached entries.
func (ec *Cache) Clear() {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	ec.cache = make(map[string]*CachedEmbedding)
}

// Size returns the current number of cached entries.
func (ec *Cache) Size() int {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	return len(ec.cache)
}

// Stats returns cache statistics.
func (ec *Cache) Stats() CacheStats {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	stats := CacheStats{
		TotalEntries: len(ec.cache),
		LocalCount:   0,
		CloudCount:   0,
	}

	totalHits := 0
	for _, cached := range ec.cache {
		switch cached.Source {
		case "local":
			stats.LocalCount++
		case "cloud":
			stats.CloudCount++
		}
		totalHits += cached.Hits
	}

	stats.TotalHits = totalHits
	if stats.TotalEntries > 0 {
		stats.HitRate = float64(totalHits) / float64(stats.TotalEntries)
	}

	return stats
}

// CacheStats holds cache performance metrics.
type CacheStats struct {
	TotalEntries int
	LocalCount   int
	CloudCount   int
	TotalHits    int
	HitRate      float64
}
