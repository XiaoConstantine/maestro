package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/google/go-github/v68/github"
)

// RepoIndexer handles the indexing of repository content into the RAG store.
type RepoIndexer struct {
	githubTools *GitHubTools
	ragStore    RAGStore
	logger      *logging.Logger
}

// NewRepoIndexer creates a new repository indexer.
func NewRepoIndexer(githubTools *GitHubTools, ragStore RAGStore) *RepoIndexer {
	return &RepoIndexer{
		githubTools: githubTools,
		ragStore:    ragStore,
		logger:      logging.GetLogger(),
	}
}

// IndexRepository indexes the entire repository content into the RAG store.
func (ri *RepoIndexer) IndexRepository(ctx context.Context, branch string) error {
	// Get repository content
	_, directoryContent, _, err := ri.githubTools.client.Repositories.GetContents(
		ctx,
		ri.githubTools.owner,
		ri.githubTools.repo,
		"", // Root directory
		&github.RepositoryContentGetOptions{
			Ref: branch,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to get repository contents: %w", err)
	}

	// Process files concurrently with a worker pool
	const maxWorkers = 5
	filesChan := make(chan *github.RepositoryContent)
	var wg sync.WaitGroup
	errChan := make(chan error, maxWorkers)

	// Start workers
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range filesChan {
				if err := ri.processFile(ctx, file, branch); err != nil {
					errChan <- fmt.Errorf("failed to process %s: %w", file.GetPath(), err)
					return
				}
			}
		}()
	}

	// Walk through repository content recursively
	err = ri.walkDirectory(ctx, directoryContent, branch, filesChan)
	if err != nil {
		return err
	}

	close(filesChan)

	// Wait for all workers to finish
	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

// walkDirectory recursively walks through repository directories.
func (ri *RepoIndexer) walkDirectory(
	ctx context.Context,
	contents []*github.RepositoryContent,
	branch string,
	filesChan chan<- *github.RepositoryContent,
) error {
	for _, content := range contents {
		if content.GetType() == "dir" {
			// Get contents of subdirectory
			_, subContents, _, err := ri.githubTools.client.Repositories.GetContents(
				ctx,
				ri.githubTools.owner,
				ri.githubTools.repo,
				content.GetPath(),
				&github.RepositoryContentGetOptions{
					Ref: branch,
				},
			)
			if err != nil {
				return fmt.Errorf("failed to get contents of %s: %w", content.GetPath(), err)
			}

			// Recursively process subdirectory
			if err := ri.walkDirectory(ctx, subContents, branch, filesChan); err != nil {
				return err
			}
		} else if content.GetType() == "file" && !shouldSkipFile(content.GetPath()) {
			filesChan <- content
		}
	}
	return nil
}

// processFile handles the indexing of a single file.
func (ri *RepoIndexer) processFile(ctx context.Context, file *github.RepositoryContent, branch string) error {
	content, err := ri.githubTools.GetFileContent(ctx, file.GetPath())
	if err != nil {
		return fmt.Errorf("failed to get file content: %w", err)
	}

	// Configure chunking
	config := NewChunkConfig()
	config.fileMetadata = map[string]interface{}{
		"file_path": file.GetPath(),
		"file_type": filepath.Ext(file.GetPath()),
		"package":   filepath.Base(filepath.Dir(file.GetPath())),
	}

	// Chunk the file content
	chunks, err := chunkfile(ctx, content, "", config)
	if err != nil {
		return fmt.Errorf("failed to chunk file: %w", err)
	}

	// Process each chunk
	for i, chunk := range chunks {
		// Generate unique ID for the chunk
		chunkID := fmt.Sprintf("%s:chunk_%d", file.GetPath(), i+1)

		// Create metadata
		metadata := map[string]string{
			"file_path":    file.GetPath(),
			"chunk_number": fmt.Sprintf("%d", i+1),
			"total_chunks": fmt.Sprintf("%d", len(chunks)),
			"start_line":   fmt.Sprintf("%d", chunk.startline),
			"end_line":     fmt.Sprintf("%d", chunk.endline),
		}

		// Generate embedding for the chunk using the default LLM
		llm := core.GetDefaultLLM()
		embedding, err := llm.CreateEmbedding(ctx, chunk.content)
		if err != nil {
			return fmt.Errorf("failed to create embedding: %w", err)
		}

		// Store the chunk in RAG store
		err = ri.ragStore.StoreContent(ctx, &Content{
			ID:        chunkID,
			Text:      chunk.content,
			Embedding: embedding.Vector,
			Metadata:  metadata,
		})
		if err != nil {
			return fmt.Errorf("failed to store content: %w", err)
		}

		ri.logger.Debug(ctx, "Indexed chunk %d/%d for file %s",
			i+1, len(chunks), file.GetPath())
	}

	return nil
}

// SearchSimilarCode finds similar code snippets in the indexed repository.
func (ri *RepoIndexer) SearchSimilarCode(ctx context.Context, query string, limit int) ([]*Content, error) {
	// Generate embedding for the query
	llm := core.GetDefaultLLM()
	queryEmbedding, err := llm.CreateEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to create query embedding: %w", err)
	}

	// Find similar content
	similar, err := ri.ragStore.FindSimilar(ctx, queryEmbedding.Vector, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to find similar content: %w", err)
	}

	return similar, nil
}
