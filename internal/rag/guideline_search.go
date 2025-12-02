package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/embedding"
	"github.com/XiaoConstantine/maestro/internal/types"
)

// GuidelineSearchEnhancer provides pattern-based guideline search with sgrep semantic search.
type GuidelineSearchEnhancer struct {
	log           *logging.Logger
	guidelinesDir string
	indexed       bool
	indexMu       sync.RWMutex
}

// NewGuidelineSearchEnhancer creates a new guideline search enhancer.
func NewGuidelineSearchEnhancer(logger *logging.Logger, dataDir string) *GuidelineSearchEnhancer {
	guidelinesDir := filepath.Join(dataDir, "guidelines")
	return &GuidelineSearchEnhancer{
		log:           logger,
		guidelinesDir: guidelinesDir,
		indexed:       false,
	}
}

// SgrepGuidelineResult represents a sgrep search result for guidelines.
type SgrepGuidelineResult struct {
	FilePath  string  `json:"file_path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
}

// WriteGuidelines writes guidelines as markdown files for sgrep indexing.
func (gse *GuidelineSearchEnhancer) WriteGuidelines(ctx context.Context, guidelines []types.GuidelineContent) error {
	if gse.guidelinesDir == "" {
		return fmt.Errorf("guidelines directory not configured")
	}

	// Create guidelines directory if it doesn't exist
	if err := os.MkdirAll(gse.guidelinesDir, 0755); err != nil {
		return fmt.Errorf("failed to create guidelines directory: %w", err)
	}

	for _, g := range guidelines {
		filename := fmt.Sprintf("%s.md", sanitizeFilename(g.ID))
		filePath := filepath.Join(gse.guidelinesDir, filename)

		content := gse.formatGuidelineAsMarkdown(g)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			gse.log.Warn(ctx, "Failed to write guideline %s: %v", g.ID, err)
			continue
		}
	}

	gse.log.Debug(ctx, "Wrote %d guidelines to %s", len(guidelines), gse.guidelinesDir)
	return nil
}

// formatGuidelineAsMarkdown formats a guideline as markdown for sgrep indexing.
func (gse *GuidelineSearchEnhancer) formatGuidelineAsMarkdown(g types.GuidelineContent) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# %s\n\n", g.Category))
	sb.WriteString(fmt.Sprintf("Category: %s\n", g.Category))
	sb.WriteString(fmt.Sprintf("Language: %s\n\n", g.Language))

	sb.WriteString("## Description\n\n")
	sb.WriteString(g.Text)
	sb.WriteString("\n\n")

	for i, example := range g.Examples {
		if i >= 3 { // Limit examples
			break
		}
		if example.Bad != "" {
			sb.WriteString("## Bad Example\n\n```go\n")
			sb.WriteString(example.Bad)
			sb.WriteString("\n```\n\n")
		}
		if example.Good != "" {
			sb.WriteString("## Good Example\n\n```go\n")
			sb.WriteString(example.Good)
			sb.WriteString("\n```\n\n")
		}
		if example.Explanation != "" {
			sb.WriteString("## Explanation\n\n")
			sb.WriteString(example.Explanation)
			sb.WriteString("\n\n")
		}
	}

	return sb.String()
}

// IndexGuidelines runs sgrep index on the guidelines directory.
func (gse *GuidelineSearchEnhancer) IndexGuidelines(ctx context.Context) error {
	gse.indexMu.Lock()
	defer gse.indexMu.Unlock()

	if gse.guidelinesDir == "" {
		return fmt.Errorf("guidelines directory not configured")
	}

	// Check if guidelines directory exists
	if _, err := os.Stat(gse.guidelinesDir); os.IsNotExist(err) {
		return fmt.Errorf("guidelines directory does not exist: %s", gse.guidelinesDir)
	}

	cmd := exec.CommandContext(ctx, "sgrep", "index", ".")
	cmd.Dir = gse.guidelinesDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sgrep index failed: %s: %w", string(output), err)
	}

	gse.indexed = true
	gse.log.Debug(ctx, "Successfully indexed guidelines directory: %s", gse.guidelinesDir)
	return nil
}

// IsIndexed returns whether guidelines have been indexed.
func (gse *GuidelineSearchEnhancer) IsIndexed() bool {
	gse.indexMu.RLock()
	defer gse.indexMu.RUnlock()
	return gse.indexed
}

// SearchGuidelines searches guidelines using sgrep semantic search.
func (gse *GuidelineSearchEnhancer) SearchGuidelines(ctx context.Context, query string, limit int) ([]types.GuidelineSearchResult, error) {
	if gse.guidelinesDir == "" || !gse.isSgrepAvailable(ctx) {
		gse.log.Warn(ctx, "sgrep not available or guidelines dir not set, returning empty results")
		return []types.GuidelineSearchResult{}, nil
	}

	args := []string{query, "--json", "-n", fmt.Sprintf("%d", limit)}
	cmd := exec.CommandContext(ctx, "sgrep", args...)
	cmd.Dir = gse.guidelinesDir

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "not indexed") || strings.Contains(stderr, "no index found") {
				gse.log.Warn(ctx, "Guidelines not indexed, attempting to index...")
				if indexErr := gse.IndexGuidelines(ctx); indexErr != nil {
					return nil, fmt.Errorf("failed to index guidelines: %w", indexErr)
				}
				// Retry search after indexing
				cmd = exec.CommandContext(ctx, "sgrep", args...)
				cmd.Dir = gse.guidelinesDir
				output, err = cmd.Output()
				if err != nil {
					return nil, fmt.Errorf("sgrep search failed after indexing: %w", err)
				}
			} else {
				return nil, fmt.Errorf("sgrep search failed: %s", stderr)
			}
		} else {
			return nil, fmt.Errorf("sgrep search failed: %w", err)
		}
	}

	// Parse JSON results
	var sgrepResults []SgrepGuidelineResult
	if err := json.Unmarshal(output, &sgrepResults); err != nil {
		gse.log.Warn(ctx, "Failed to parse sgrep output as JSON: %v", err)
		return []types.GuidelineSearchResult{}, nil
	}

	// Convert to GuidelineSearchResult
	results := make([]types.GuidelineSearchResult, 0, len(sgrepResults))
	for _, r := range sgrepResults {
		// Convert distance score to relevance (lower distance = higher relevance)
		relevance := 1.0 - r.Score
		if relevance < 0 {
			relevance = 0
		}

		result := types.GuidelineSearchResult{
			Content: &types.Content{
				ID:   fmt.Sprintf("%s:%d-%d", r.FilePath, r.StartLine, r.EndLine),
				Text: r.Content,
				Metadata: map[string]string{
					"file_path":    r.FilePath,
					"start_line":   fmt.Sprintf("%d", r.StartLine),
					"end_line":     fmt.Sprintf("%d", r.EndLine),
					"content_type": ContentTypeGuideline,
					"source":       "sgrep",
				},
			},
			FinalScore: relevance,
			Pattern:    query,
		}
		results = append(results, result)
	}

	gse.log.Debug(ctx, "Sgrep search for '%s' returned %d results", query, len(results))
	return results, nil
}

// FindRelevantGuidelinesWithSgrep finds guidelines relevant to the given code using sgrep.
func (gse *GuidelineSearchEnhancer) FindRelevantGuidelinesWithSgrep(ctx context.Context, code string, limit int) ([]types.GuidelineSearchResult, error) {
	patterns := gse.ExtractCodePatterns(ctx, code)
	if len(patterns) == 0 {
		gse.log.Debug(ctx, "No patterns detected in code")
		return []types.GuidelineSearchResult{}, nil
	}

	var allResults []types.GuidelineSearchResult
	perPatternLimit := max(limit/len(patterns), 3)

	for _, pattern := range patterns {
		query := fmt.Sprintf("Go best practices for %s: %s", pattern.Name, pattern.Description)
		results, err := gse.SearchGuidelines(ctx, query, perPatternLimit)
		if err != nil {
			gse.log.Warn(ctx, "sgrep search failed for pattern %s: %v", pattern.Name, err)
			continue
		}

		// Add pattern context to results
		for i := range results {
			results[i].Pattern = pattern.Name
			results[i].ContextDescription = fmt.Sprintf("Relevant to %s pattern detected in your code", pattern.Name)
		}

		allResults = append(allResults, results...)
	}

	// Deduplicate and sort
	allResults = DeduplicateResults(allResults)

	// Return top N
	if len(allResults) > limit {
		allResults = allResults[:limit]
	}

	gse.log.Debug(ctx, "Found %d relevant guidelines via sgrep", len(allResults))
	return allResults, nil
}

// isSgrepAvailable checks if sgrep CLI is installed.
func (gse *GuidelineSearchEnhancer) isSgrepAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "which", "sgrep")
	return cmd.Run() == nil
}

// sanitizeFilename creates a safe filename from a guideline ID.
func sanitizeFilename(id string) string {
	safe := strings.ReplaceAll(id, "/", "-")
	safe = strings.ReplaceAll(safe, " ", "-")
	safe = strings.ReplaceAll(safe, ":", "-")
	return safe
}

// ExtractCodePatterns detects code patterns in the given file content.
func (gse *GuidelineSearchEnhancer) ExtractCodePatterns(ctx context.Context, code string) []types.SimpleCodePattern {
	patterns := []types.SimpleCodePattern{}
	codeLower := strings.ToLower(code)

	// Pattern 1: Error Handling
	if (strings.Contains(codeLower, "error") || strings.Contains(codeLower, "err")) &&
		(strings.Contains(code, "return") || strings.Contains(code, "fmt.Errorf") || strings.Contains(code, "errors.")) {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "error handling",
			Description: "Error checking, error wrapping, and error propagation",
			Confidence:  0.85,
		})
	}

	// Pattern 2: Concurrency
	if (strings.Contains(code, "go ") || strings.Contains(code, "goroutine")) &&
		(strings.Contains(code, "chan") || strings.Contains(code, "channel") || strings.Contains(code, "sync.") || strings.Contains(code, "Mutex")) {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "concurrency",
			Description: "Goroutines, channels, mutexes, and concurrent patterns",
			Confidence:  0.9,
		})
	}

	// Pattern 3: Nil Checks
	if strings.Contains(code, "nil") && strings.Contains(code, "if") {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "nil checks",
			Description: "Nil pointer checks and defensive programming",
			Confidence:  0.75,
		})
	}

	// Pattern 4: Pointer Usage
	if strings.Contains(code, "*") && (strings.Contains(code, "&") || strings.Contains(code, "Ptr") || strings.Contains(code, "pointer")) {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "pointer usage",
			Description: "Pointer semantics, dereference operations, and pointer safety",
			Confidence:  0.8,
		})
	}

	// Pattern 5: Resource Management / Cleanup
	if strings.Contains(code, "defer") || strings.Contains(code, "Close()") || strings.Contains(code, "cleanup") {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "resource cleanup",
			Description: "Deferred cleanup, file/connection closing, resource management",
			Confidence:  0.85,
		})
	}

	// Pattern 6: Interface Usage
	if strings.Contains(code, "interface") || strings.Contains(code, "Interface{") {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "interface design",
			Description: "Interface design patterns, composition, and abstraction",
			Confidence:  0.8,
		})
	}

	// Pattern 7: Naming Conventions
	if strings.Contains(code, "const ") || strings.Contains(code, "var ") || strings.Contains(code, "func ") {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "naming conventions",
			Description: "Variable naming, function naming, and identifier conventions",
			Confidence:  0.7,
		})
	}

	// Pattern 8: Struct/Type Definitions
	if strings.Contains(code, "type ") && strings.Contains(code, "struct") {
		patterns = append(patterns, types.SimpleCodePattern{
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
	patterns []types.SimpleCodePattern,
) (map[string][]float32, error) {
	patternQueries := make(map[string][]float32)
	router := embedding.GetRouter()

	for _, pattern := range patterns {
		// Create a specific query for this pattern
		query := fmt.Sprintf(
			"Go best practices, guidelines, and recommendations for %s: "+
				"%s. Include patterns, anti-patterns, common mistakes, and proper implementation.",
			pattern.Name,
			pattern.Description,
		)

		gse.log.Debug(ctx, "Creating embedding for pattern: %s", pattern.Name)

		result, err := router.CreateEmbedding(ctx, query, embedding.WithBatch(true))
		if err != nil {
			gse.log.Warn(ctx, "Failed to create query embedding for %s: %v", pattern.Name, err)
			continue
		}

		patternQueries[pattern.Name] = result.Vector
	}

	gse.log.Debug(ctx, "Created %d pattern embeddings", len(patternQueries))
	return patternQueries, nil
}

// ScoreGuideline computes multi-vector relevance score for a guideline.
func ScoreGuideline(
	patternEmbedding []float32,
	guideline *types.Content,
	docEmbedding []float32,
	chunkEmbeddings [][]float32,
) float64 {
	if len(docEmbedding) == 0 {
		return 0.0
	}

	// Document-level similarity: match against full guideline
	docScore := CosineSimilarity(patternEmbedding, docEmbedding)
	if docScore < 0 {
		docScore = 0
	}

	// Chunk-level similarity: find best matching section
	bestChunkScore := 0.0
	for _, chunkEmbed := range chunkEmbeddings {
		if len(chunkEmbed) > 0 {
			score := CosineSimilarity(patternEmbedding, chunkEmbed)
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
				score := CosineSimilarity(patternEmbedding, chunkEmbed)
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
func DeduplicateResults(results []types.GuidelineSearchResult) []types.GuidelineSearchResult {
	seen := make(map[string]*types.GuidelineSearchResult)

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
	deduped := make([]types.GuidelineSearchResult, 0, len(seen))
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
func FilterByConfidence(results []types.GuidelineSearchResult, threshold float64) []types.GuidelineSearchResult {
	filtered := make([]types.GuidelineSearchResult, 0, len(results))
	for _, result := range results {
		if result.FinalScore >= threshold {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

// ConvertToContent converts GuidelineSearchResult back to Content for compatibility.
func ConvertToContent(results []types.GuidelineSearchResult) []*types.Content {
	contents := make([]*types.Content, len(results))
	for i, result := range results {
		contents[i] = result.Content
	}
	return contents
}

// max returns the larger of two integers.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
