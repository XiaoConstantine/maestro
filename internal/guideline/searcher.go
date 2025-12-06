// Package guideline handles fetching and processing coding guidelines.
package guideline

import (
	"context"
	"fmt"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/search"
	"github.com/XiaoConstantine/maestro/internal/types"
)

// Searcher provides sgrep-based guideline search.
type Searcher struct {
	fetcher   *Fetcher
	sgrepTool *search.SgrepTool
	logger    *logging.Logger
	mu        sync.RWMutex
	indexed   bool
}

// NewSearcher creates a new guideline searcher.
func NewSearcher(baseDir string, logger *logging.Logger) *Searcher {
	return &Searcher{
		fetcher:   NewFetcherWithStorage(logger, baseDir),
		sgrepTool: search.NewSgrepTool(logger, ""),
		logger:    logger,
	}
}

// EnsureReady ensures guidelines are downloaded and indexed.
func (s *Searcher) EnsureReady(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure guidelines are downloaded
	guidelinesDir, err := s.fetcher.EnsureGuidelines(ctx)
	if err != nil {
		return fmt.Errorf("failed to ensure guidelines: %w", err)
	}

	// Check if already indexed
	if s.sgrepTool.IsPathIndexed(ctx, guidelinesDir) {
		s.indexed = true
		s.logger.Debug(ctx, "Guidelines already indexed at %s", guidelinesDir)
		return nil
	}

	// Index the guidelines directory
	s.logger.Info(ctx, "Indexing guidelines at %s", guidelinesDir)
	if err := s.sgrepTool.IndexPath(ctx, guidelinesDir); err != nil {
		return fmt.Errorf("failed to index guidelines: %w", err)
	}

	s.indexed = true
	return nil
}

// Search finds relevant guidelines for a query.
func (s *Searcher) Search(ctx context.Context, query string, limit int) ([]types.GuidelineSearchResult, error) {
	s.mu.RLock()
	indexed := s.indexed
	s.mu.RUnlock()

	if !indexed {
		if err := s.EnsureReady(ctx); err != nil {
			return nil, err
		}
	}

	guidelinesDir := s.fetcher.GuidelinesDir()
	if guidelinesDir == "" {
		return nil, fmt.Errorf("guidelines directory not configured")
	}

	results, err := s.sgrepTool.SearchInPath(ctx, guidelinesDir, query, limit)
	if err != nil {
		return nil, fmt.Errorf("guideline search failed: %w", err)
	}

	// Convert to GuidelineSearchResult (matching existing interface)
	guidelineResults := make([]types.GuidelineSearchResult, 0, len(results))
	for _, r := range results {
		// Convert distance score to relevance (lower distance = higher relevance)
		relevance := 1.0 - r.Score
		if relevance < 0 {
			relevance = 0
		}

		guidelineResults = append(guidelineResults, types.GuidelineSearchResult{
			Content: &types.Content{
				ID:   fmt.Sprintf("%s:%d-%d", r.FilePath, r.StartLine, r.EndLine),
				Text: r.Content,
				Metadata: map[string]string{
					"file_path":    r.FilePath,
					"start_line":   fmt.Sprintf("%d", r.StartLine),
					"end_line":     fmt.Sprintf("%d", r.EndLine),
					"content_type": "guideline",
					"source":       "sgrep",
				},
			},
			FinalScore: relevance,
			Pattern:    query,
		})
	}

	return guidelineResults, nil
}

// SearchForPatterns finds guidelines relevant to code patterns.
func (s *Searcher) SearchForPatterns(ctx context.Context, patterns []types.SimpleCodePattern, limit int) ([]types.GuidelineSearchResult, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	// Build a combined query from patterns, prioritizing by confidence
	var queries []string
	for _, p := range patterns {
		if p.Name != "" {
			queries = append(queries, p.Name)
		}
		if p.Description != "" && len(p.Description) < 50 {
			// Use short descriptions as additional search terms
			queries = append(queries, p.Description)
		}
	}

	if len(queries) == 0 {
		return nil, nil
	}

	// Search for the most significant pattern
	// (sgrep handles semantic similarity, so one good query often suffices)
	query := queries[0]
	if len(queries) > 1 {
		// Combine top patterns for better semantic matching
		query = queries[0] + " " + queries[1]
	}

	return s.Search(ctx, query, limit)
}

// GuidelinesDir returns the path to the guidelines directory.
func (s *Searcher) GuidelinesDir() string {
	return s.fetcher.GuidelinesDir()
}

// IsReady returns whether the searcher is ready (indexed).
func (s *Searcher) IsReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.indexed
}
