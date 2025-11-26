package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// GuidelineSearchEnhancer provides pattern-based guideline search with semantic enrichment.
type GuidelineSearchEnhancer struct {
	log *logging.Logger
}

// NewGuidelineSearchEnhancer creates a new guideline search enhancer.
func NewGuidelineSearchEnhancer(logger *logging.Logger) *GuidelineSearchEnhancer {
	return &GuidelineSearchEnhancer{
		log: logger,
	}
}

// SimpleCodePattern represents a detected code pattern in a file for guideline search.
type SimpleCodePattern struct {
	Name        string // e.g., "error handling", "concurrency"
	Description string // Human-readable description
	Confidence  float64 // How confident we are this pattern exists (0-1)
}

// GuidelineSearchResult represents a guideline with relevance scores.
type GuidelineSearchResult struct {
	Content            *Content
	DocumentScore      float64 // Document-level similarity (0-1)
	BestChunkScore     float64 // Best chunk similarity (0-1)
	AverageChunkScore  float64 // Average chunk similarity (0-1)
	FinalScore         float64 // Weighted score (0-1)
	Pattern            string  // Which pattern triggered this match
	ContextDescription string  // Why this guideline is relevant
}

// ExtractCodePatterns detects code patterns in the given file content.
func (gse *GuidelineSearchEnhancer) ExtractCodePatterns(ctx context.Context, code string) []SimpleCodePattern {
	patterns := []SimpleCodePattern{}
	codeLower := strings.ToLower(code)

	// Pattern 1: Error Handling
	if (strings.Contains(codeLower, "error") || strings.Contains(codeLower, "err")) &&
		(strings.Contains(code, "return") || strings.Contains(code, "fmt.Errorf") || strings.Contains(code, "errors.")) {
		patterns = append(patterns, SimpleCodePattern{
			Name:        "error handling",
			Description: "Error checking, error wrapping, and error propagation",
			Confidence:  0.85,
		})
	}

	// Pattern 2: Concurrency
	if (strings.Contains(code, "go ") || strings.Contains(code, "goroutine")) &&
		(strings.Contains(code, "chan") || strings.Contains(code, "channel") || strings.Contains(code, "sync.") || strings.Contains(code, "Mutex")) {
		patterns = append(patterns, SimpleCodePattern{
			Name:        "concurrency",
			Description: "Goroutines, channels, mutexes, and concurrent patterns",
			Confidence:  0.9,
		})
	}

	// Pattern 3: Nil Checks
	if strings.Contains(code, "nil") && strings.Contains(code, "if") {
		patterns = append(patterns, SimpleCodePattern{
			Name:        "nil checks",
			Description: "Nil pointer checks and defensive programming",
			Confidence:  0.75,
		})
	}

	// Pattern 4: Pointer Usage
	if strings.Contains(code, "*") && (strings.Contains(code, "&") || strings.Contains(code, "Ptr") || strings.Contains(code, "pointer")) {
		patterns = append(patterns, SimpleCodePattern{
			Name:        "pointer usage",
			Description: "Pointer semantics, dereference operations, and pointer safety",
			Confidence:  0.8,
		})
	}

	// Pattern 5: Resource Management / Cleanup
	if strings.Contains(code, "defer") || strings.Contains(code, "Close()") || strings.Contains(code, "cleanup") {
		patterns = append(patterns, SimpleCodePattern{
			Name:        "resource cleanup",
			Description: "Deferred cleanup, file/connection closing, resource management",
			Confidence:  0.85,
		})
	}

	// Pattern 6: Interface Usage
	if strings.Contains(code, "interface") || strings.Contains(code, "Interface{") {
		patterns = append(patterns, SimpleCodePattern{
			Name:        "interface design",
			Description: "Interface design patterns, composition, and abstraction",
			Confidence:  0.8,
		})
	}

	// Pattern 7: Naming Conventions
	if strings.Contains(code, "const ") || strings.Contains(code, "var ") || strings.Contains(code, "func ") {
		patterns = append(patterns, SimpleCodePattern{
			Name:        "naming conventions",
			Description: "Variable naming, function naming, and identifier conventions",
			Confidence:  0.7,
		})
	}

	// Pattern 8: Struct/Type Definitions
	if strings.Contains(code, "type ") && strings.Contains(code, "struct") {
		patterns = append(patterns, SimpleCodePattern{
			Name:        "type design",
			Description: "Struct design, type definitions, and data structures",
			Confidence:  0.75,
		})
	}

	gse.log.Debug(ctx, "Extracted %d code patterns from file", len(patterns))
	return patterns
}

// EnhanceGuidelineQuery creates specific embeddings for guideline patterns.
func (gse *GuidelineSearchEnhancer) EnhanceGuidelineQuery(
	ctx context.Context,
	patterns []SimpleCodePattern,
) (map[string][]float32, error) {
	patternQueries := make(map[string][]float32)
	router := GetEmbeddingRouter()

	for _, pattern := range patterns {
		// Create a specific query for this pattern
		query := fmt.Sprintf(
			"Go best practices, guidelines, and recommendations for %s: "+
				"%s. Include patterns, anti-patterns, common mistakes, and proper implementation.",
			pattern.Name,
			pattern.Description,
		)

		gse.log.Debug(ctx, "Creating embedding for pattern: %s", pattern.Name)

		embedding, err := router.CreateEmbedding(ctx, query, WithBatch(true))
		if err != nil {
			gse.log.Warn(ctx, "Failed to create query embedding for %s: %v", pattern.Name, err)
			continue
		}

		patternQueries[pattern.Name] = embedding.Vector
	}

	gse.log.Debug(ctx, "Created %d pattern embeddings", len(patternQueries))
	return patternQueries, nil
}

// ScoreGuideline computes multi-vector relevance score for a guideline.
func ScoreGuideline(
	patternEmbedding []float32,
	guideline *Content,
	docEmbedding []float32,
	chunkEmbeddings [][]float32,
) float64 {
	if len(docEmbedding) == 0 {
		return 0.0
	}

	// Document-level similarity: match against full guideline
	docScore := cosineSimilarity(patternEmbedding, docEmbedding)
	if docScore < 0 {
		docScore = 0
	}

	// Chunk-level similarity: find best matching section
	bestChunkScore := 0.0
	for _, chunkEmbed := range chunkEmbeddings {
		if len(chunkEmbed) > 0 {
			score := cosineSimilarity(patternEmbedding, chunkEmbed)
			if score > bestChunkScore {
				bestChunkScore = score
			}
		}
	}
	if bestChunkScore < 0 {
		bestChunkScore = 0
	}

	// Average chunk similarity: coverage across sections
	avgChunkScore := 0.0
	if len(chunkEmbeddings) > 0 {
		for _, chunkEmbed := range chunkEmbeddings {
			if len(chunkEmbed) > 0 {
				score := cosineSimilarity(patternEmbedding, chunkEmbed)
				if score > 0 {
					avgChunkScore += score
				}
			}
		}
		avgChunkScore /= float64(len(chunkEmbeddings))
	}

	// Weighted combination: 40% document + 40% best chunk + 20% average
	finalScore := 0.4*docScore + 0.4*bestChunkScore + 0.2*avgChunkScore

	// Ensure score is in valid range
	if finalScore < 0 {
		finalScore = 0
	}
	if finalScore > 1 {
		finalScore = 1
	}

	return finalScore
}

// DeduplicateResults removes duplicate guidelines from search results, keeping highest score.
func DeduplicateResults(results []GuidelineSearchResult) []GuidelineSearchResult {
	seen := make(map[string]*GuidelineSearchResult)

	for i := range results {
		id := results[i].Content.ID
		if existing, found := seen[id]; found {
			// Keep the higher score
			if results[i].FinalScore > existing.FinalScore {
				seen[id] = &results[i]
			}
		} else {
			seen[id] = &results[i]
		}
	}

	// Convert back to slice
	deduped := make([]GuidelineSearchResult, 0, len(seen))
	for _, item := range seen {
		deduped = append(deduped, *item)
	}

	// Sort by score (descending)
	for i := 0; i < len(deduped)-1; i++ {
		for j := 0; j < len(deduped)-i-1; j++ {
			if deduped[j].FinalScore < deduped[j+1].FinalScore {
				deduped[j], deduped[j+1] = deduped[j+1], deduped[j]
			}
		}
	}

	return deduped
}

// FilterByConfidence filters results to only high-confidence matches.
func FilterByConfidence(results []GuidelineSearchResult, threshold float64) []GuidelineSearchResult {
	filtered := make([]GuidelineSearchResult, 0, len(results))
	for _, result := range results {
		if result.FinalScore >= threshold {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

// ConvertToContent converts GuidelineSearchResult back to Content for compatibility.
func ConvertToContent(results []GuidelineSearchResult) []*Content {
	contents := make([]*Content, len(results))
	for i, result := range results {
		contents[i] = result.Content
	}
	return contents
}
