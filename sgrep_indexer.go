package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/sgrep/pkg/index"
	"github.com/XiaoConstantine/sgrep/pkg/search"
)

// SgrepIndexer wraps sgrep's indexing and search capabilities.
// It clones repos to /tmp and uses sgrep for semantic code search.
type SgrepIndexer struct {
	log      *logging.Logger
	repoPath string                // Local path to cloned repo
	indexer  *index.Indexer
	searcher *search.Searcher
}

// NewSgrepIndexer creates a new sgrep-based indexer.
func NewSgrepIndexer(logger *logging.Logger) *SgrepIndexer {
	return &SgrepIndexer{
		log: logger,
	}
}

// CloneAndIndex clones a GitHub repo to /tmp and indexes it with sgrep.
// repoFullName should be in format "owner/repo"
func (si *SgrepIndexer) CloneAndIndex(ctx context.Context, repoFullName, branch string) error {
	// Create temp directory for the repo
	tmpDir, err := os.MkdirTemp("", "maestro-repo-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	si.repoPath = tmpDir

	si.log.Info(ctx, "Cloning %s to %s", repoFullName, tmpDir)

	// Clone using gh CLI
	args := []string{"repo", "clone", repoFullName, tmpDir}
	if branch != "" {
		args = append(args, "--", "-b", branch)
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to clone repo: %w", err)
	}

	si.log.Info(ctx, "Indexing repository with sgrep...")

	// Create sgrep indexer for the cloned path
	si.indexer, err = index.New(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to create sgrep indexer: %w", err)
	}

	// Index the repository
	if err := si.indexer.Index(ctx); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to index repository: %w", err)
	}

	// Create searcher from the indexer's store
	store, err := si.indexer.Store()
	if err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to get store: %w", err)
	}
	si.searcher = search.New(store)

	si.log.Info(ctx, "Repository indexed successfully")
	return nil
}

// IndexLocalPath indexes a local directory with sgrep.
func (si *SgrepIndexer) IndexLocalPath(ctx context.Context, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}
	si.repoPath = absPath

	si.log.Info(ctx, "Indexing local path: %s", absPath)

	// Create sgrep indexer
	si.indexer, err = index.New(absPath)
	if err != nil {
		return fmt.Errorf("failed to create sgrep indexer: %w", err)
	}

	// Index the directory
	if err := si.indexer.Index(ctx); err != nil {
		return fmt.Errorf("failed to index directory: %w", err)
	}

	// Create searcher
	store, err := si.indexer.Store()
	if err != nil {
		return fmt.Errorf("failed to get store: %w", err)
	}
	si.searcher = search.New(store)

	si.log.Info(ctx, "Directory indexed successfully")
	return nil
}

// Search performs semantic code search.
func (si *SgrepIndexer) Search(ctx context.Context, query string, limit int) ([]search.Result, error) {
	if si.searcher == nil {
		return nil, fmt.Errorf("indexer not initialized - call CloneAndIndex or IndexLocalPath first")
	}

	opts := search.DefaultSearchOptions()
	opts.Limit = limit

	return si.searcher.SearchWithOptions(ctx, query, opts)
}

// SearchSimilarCode searches and converts results to maestro's Content format.
func (si *SgrepIndexer) SearchSimilarCode(ctx context.Context, query string, limit int) ([]*Content, error) {
	results, err := si.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	contents := make([]*Content, len(results))
	for i, r := range results {
		contents[i] = &Content{
			ID:   fmt.Sprintf("%s:%d-%d", r.FilePath, r.StartLine, r.EndLine),
			Text: r.Content,
			Metadata: map[string]string{
				"file_path":  r.FilePath,
				"start_line": fmt.Sprintf("%d", r.StartLine),
				"end_line":   fmt.Sprintf("%d", r.EndLine),
				"score":      fmt.Sprintf("%.4f", r.Score),
			},
		}
	}

	return contents, nil
}

// RepoPath returns the path to the indexed repository.
func (si *SgrepIndexer) RepoPath() string {
	return si.repoPath
}

// Close cleans up resources.
func (si *SgrepIndexer) Close() error {
	if si.indexer != nil {
		si.indexer.Close()
	}
	// Clean up temp directory if it was created by CloneAndIndex
	if si.repoPath != "" && filepath.HasPrefix(si.repoPath, os.TempDir()) {
		return os.RemoveAll(si.repoPath)
	}
	return nil
}
