package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// EmbeddingRouter provides intelligent routing between local and cloud embedding models.
// It implements a smart caching strategy and automatic fallback mechanism.
type EmbeddingRouter struct {
	local  core.LLM              // Local LLM (ollama/llamacpp)
	cloud  core.LLM              // Cloud LLM (Gemini, Claude, etc.)
	cache  *EmbeddingCache       // Embedding result cache
	logger *logging.Logger       // Logger instance
	stats  *EmbeddingRouterStats // Performance statistics
}

// EmbeddingRouterStats tracks routing decisions and performance metrics.
type EmbeddingRouterStats struct {
	TotalRequests     atomic.Int64
	LocalSuccesses    atomic.Int64
	CloudSuccesses    atomic.Int64
	LocalFailures     atomic.Int64
	CloudFailures     atomic.Int64
	CacheHits         atomic.Int64
	LocalLatencySum   atomic.Int64 // in milliseconds
	CloudLatencySum   atomic.Int64 // in milliseconds
	LocalLatencyCount atomic.Int64
	CloudLatencyCount atomic.Int64
}

// NewEmbeddingRouter creates a new embedding router.
func NewEmbeddingRouter(local, cloud core.LLM) *EmbeddingRouter {
	logger := logging.GetLogger()

	router := &EmbeddingRouter{
		local:  local,
		cloud:  cloud,
		cache:  NewEmbeddingCache(10000),
		logger: logger,
		stats:  &EmbeddingRouterStats{},
	}

	logger.Info(context.Background(), "Embedding router initialized with local and cloud models")
	return router
}

// CreateEmbedding routes a single embedding request based on configuration.
func (r *EmbeddingRouter) CreateEmbedding(ctx context.Context, input string, opts ...EmbeddingOption) (*core.EmbeddingResult, error) {
	r.stats.TotalRequests.Add(1)

	config := ParseEmbeddingOptions(opts...)

	// Step 1: Check cache first (unless explicitly disabled)
	if config.AllowCache {
		if cached := r.cache.Get(input); cached != nil {
			r.stats.CacheHits.Add(1)
			r.logger.Debug(ctx, "Cache hit for embedding request")
			return cached, nil
		}
	}

	// Step 2: Route based on configuration
	var result *core.EmbeddingResult
	var err error
	var source string

	shouldUseLocal := config.ShouldUseLocal()
	r.logger.Debug(ctx, "Embedding routing: shouldUseLocal=%v, localAvailable=%v, batch=%v", shouldUseLocal, r.local != nil, config.Batch)

	if shouldUseLocal && r.local != nil {
		// Try local first
		r.logger.Debug(ctx, "Using local embedding (sgrep)")
		result, err = r.createWithTiming(ctx, r.local, input, config, "local")
		if err == nil {
			source = "local"
			r.stats.LocalSuccesses.Add(1)
		} else {
			r.stats.LocalFailures.Add(1)
			r.logger.Debug(ctx, "Local embedding failed, attempting cloud fallback: %v", err)

			// Fallback to cloud
			if r.cloud != nil {
				result, err = r.createWithTiming(ctx, r.cloud, input, config, "cloud")
				if err == nil {
					source = "cloud"
					r.stats.CloudSuccesses.Add(1)
				} else {
					r.stats.CloudFailures.Add(1)
					r.logger.Error(ctx, "Both local and cloud embeddings failed: %v", err)
				}
			}
		}
	} else if r.cloud != nil {
		// Use cloud directly (latency-critical path)
		result, err = r.createWithTiming(ctx, r.cloud, input, config, "cloud")
		if err == nil {
			source = "cloud"
			r.stats.CloudSuccesses.Add(1)
		} else {
			r.stats.CloudFailures.Add(1)
			r.logger.Debug(ctx, "Cloud embedding failed, attempting local fallback: %v", err)

			// Fallback to local
			if r.local != nil {
				result, err = r.createWithTiming(ctx, r.local, input, config, "local")
				if err == nil {
					source = "local"
					r.stats.LocalSuccesses.Add(1)
				} else {
					r.stats.LocalFailures.Add(1)
					r.logger.Error(ctx, "Both cloud and local embeddings failed: %v", err)
				}
			}
		}
	} else {
		return nil, fmt.Errorf("no embedding models available (local: %v, cloud: %v)", r.local != nil, r.cloud != nil)
	}

	if err != nil {
		return nil, err
	}

	// Step 3: Cache successful result
	if config.AllowCache && result != nil {
		r.cache.Set(input, result, source)
	}

	return result, nil
}

// createWithTiming wraps CreateEmbedding call with timing measurement.
func (r *EmbeddingRouter) createWithTiming(ctx context.Context, llm core.LLM, input string, config *EmbeddingConfig, source string) (*core.EmbeddingResult, error) {
	startTime := time.Now()

	// Build options to pass to underlying LLM
	var result *core.EmbeddingResult
	var err error

	if config.Model != "" {
		result, err = llm.CreateEmbedding(ctx, input, core.WithModel(config.Model))
	} else {
		result, err = llm.CreateEmbedding(ctx, input)
	}

	duration := time.Since(startTime).Milliseconds()

	switch source {
	case "local":
		r.stats.LocalLatencySum.Add(duration)
		r.stats.LocalLatencyCount.Add(1)
	case "cloud":
		r.stats.CloudLatencySum.Add(duration)
		r.stats.CloudLatencyCount.Add(1)
	}

	return result, err
}

// CreateEmbeddings routes batch embedding requests (not yet implemented in dspy-go).
// Falls back to sequential CreateEmbedding calls.
func (r *EmbeddingRouter) CreateEmbeddings(ctx context.Context, inputs []string, opts ...EmbeddingOption) ([]*core.EmbeddingResult, error) {
	config := ParseEmbeddingOptions(opts...)

	// Mark as batch operation
	config.Batch = true

	results := make([]*core.EmbeddingResult, len(inputs))
	var batchErr error

	// Process embeddings sequentially (can be optimized to parallel later)
	for i, input := range inputs {
		result, err := r.CreateEmbedding(ctx, input, func(c *EmbeddingConfig) {
			*c = *config // Copy config
			c.Batch = true
		})

		if err != nil {
			if batchErr == nil {
				batchErr = err
			}
			r.logger.Warn(ctx, "Failed to create embedding for input %d: %v", i, err)
			continue
		}

		results[i] = result
	}

	return results, batchErr
}

// Stats returns current routing statistics.
// Note: Returns only the loaded values, not the atomic types themselves.
func (r *EmbeddingRouter) Stats() map[string]int64 {
	return map[string]int64{
		"TotalRequests":     r.stats.TotalRequests.Load(),
		"LocalSuccesses":    r.stats.LocalSuccesses.Load(),
		"CloudSuccesses":    r.stats.CloudSuccesses.Load(),
		"LocalFailures":     r.stats.LocalFailures.Load(),
		"CloudFailures":     r.stats.CloudFailures.Load(),
		"CacheHits":         r.stats.CacheHits.Load(),
		"LocalLatencySum":   r.stats.LocalLatencySum.Load(),
		"CloudLatencySum":   r.stats.CloudLatencySum.Load(),
		"LocalLatencyCount": r.stats.LocalLatencyCount.Load(),
		"CloudLatencyCount": r.stats.CloudLatencyCount.Load(),
	}
}

// PrintStats logs detailed routing statistics.
func (r *EmbeddingRouter) PrintStats(ctx context.Context) {
	stats := r.Stats()

	totalRequests := stats["TotalRequests"]
	if totalRequests == 0 {
		r.logger.Info(ctx, "No embedding requests processed yet")
		return
	}

	localTotal := stats["LocalSuccesses"] + stats["LocalFailures"]
	cloudTotal := stats["CloudSuccesses"] + stats["CloudFailures"]
	cacheHits := stats["CacheHits"]

	localPercent := float64(0)
	if totalRequests > 0 {
		localPercent = float64(localTotal) * 100.0 / float64(totalRequests)
	}
	cloudPercent := float64(0)
	if totalRequests > 0 {
		cloudPercent = float64(cloudTotal) * 100.0 / float64(totalRequests)
	}
	cacheHitRate := float64(0)
	if totalRequests > 0 {
		cacheHitRate = float64(cacheHits) * 100.0 / float64(totalRequests)
	}

	avgLocalLatency := float64(0)
	if stats["LocalLatencyCount"] > 0 {
		avgLocalLatency = float64(stats["LocalLatencySum"]) / float64(stats["LocalLatencyCount"])
	}

	avgCloudLatency := float64(0)
	if stats["CloudLatencyCount"] > 0 {
		avgCloudLatency = float64(stats["CloudLatencySum"]) / float64(stats["CloudLatencyCount"])
	}

	r.logger.Info(ctx, "=== Embedding Router Metrics ===")
	r.logger.Info(ctx, "Total Requests: %d", totalRequests)
	r.logger.Info(ctx, "Local: %d (%.1f%%), Cloud: %d (%.1f%%)", localTotal, localPercent, cloudTotal, cloudPercent)
	r.logger.Info(ctx, "Cache Hits: %d (%.1f%% hit rate)", cacheHits, cacheHitRate)
	r.logger.Info(ctx, "Local Latency: %.2fms, Cloud Latency: %.2fms", avgLocalLatency, avgCloudLatency)
	r.logger.Info(ctx, "Failures - Local: %d, Cloud: %d", stats["LocalFailures"], stats["CloudFailures"])
}

// ClearCache clears the embedding cache.
func (r *EmbeddingRouter) ClearCache() {
	r.cache.Clear()
	r.logger.Debug(context.Background(), "Embedding cache cleared")
}

// CacheSize returns the current size of the embedding cache.
func (r *EmbeddingRouter) CacheSize() int {
	return r.cache.Size()
}

// CacheStats returns cache performance statistics.
func (r *EmbeddingRouter) CacheStats() CacheStats {
	return r.cache.Stats()
}
