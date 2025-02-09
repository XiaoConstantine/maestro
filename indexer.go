package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
func (ri *RepoIndexer) IndexRepository(ctx context.Context, branch, dbPath string, needFullIndex bool) error {
	logger := logging.GetLogger()
	latestSHA, err := ri.githubTools.GetLatestCommitSHA(ctx, branch)
	if err != nil {
		return fmt.Errorf("failed to get latest commit SHA: %w", err)
	}

	if !needFullIndex {
		logger.Info(ctx, "Found recent index for commit %s, skipping indexing", latestSHA[:8])
		pattern := fmt.Sprintf("%s_%s_*.db", ri.githubTools.owner, ri.githubTools.repo)
		existingDBs, err := filepath.Glob(filepath.Join(filepath.Dir(dbPath), pattern))
		if err != nil {
			return fmt.Errorf("failed to list existing databases: %w", err)
		}
		if len(existingDBs) > 0 {
			// Extract the SHA from the most recent database
			sort.Strings(existingDBs) // Sort to get the latest version
			currentDB := existingDBs[len(existingDBs)-1]
			currentSHA := extractSHAFromPath(currentDB)

			logger.Info(ctx, "Found existing index for commit %s, performing incremental update", currentSHA)

			// Get the changes between commits
			changes, err := ri.getCommitDifferences(ctx, currentSHA, latestSHA)
			if err != nil {
				return fmt.Errorf("failed to get commit differences: %w", err)
			}

			// Create new database by copying the existing one
			if err := copyFile(currentDB, dbPath); err != nil {
				return fmt.Errorf("failed to copy database: %w", err)
			}

			// Process only the changed files
			return ri.processChangedFiles(ctx, changes, dbPath)
		}

		return nil
	}
	logger.Debug(ctx, "Indexing repository with config:")
	logger.Debug(ctx, "  Owner: %s", ri.githubTools.owner)
	logger.Debug(ctx, "  Repo: %s", ri.githubTools.repo)
	logger.Debug(ctx, "  Branch: %s", branch)
	logger.Debug(ctx, "  Token set: %v", ri.githubTools.client != nil)
	logger.Debug(ctx, "=======================")
	if branch == "" {
		// Get repository information to find default branch
		repo, _, err := ri.githubTools.client.Repositories.Get(ctx, ri.githubTools.owner, ri.githubTools.repo)
		if err != nil {
			return fmt.Errorf("failed to get repository info: %w", err)
		}
		branch = repo.GetDefaultBranch()
		logger.Debug(ctx, "  Using default branch: %s", branch)
	} else {
		logger.Debug(ctx, "  Using specified branch: %s", branch)
	}
	// No existing index found, proceed full index
	_, directoryContent, resp, err := ri.githubTools.client.Repositories.GetContents(
		ctx,
		ri.githubTools.owner,
		ri.githubTools.repo,
		"", // Root directory
		&github.RepositoryContentGetOptions{
			Ref: branch,
		},
	)
	if err != nil {
		if resp != nil {
			logger.Debug(ctx, "GitHub API response status: %d", resp.StatusCode)
		}

		logger.Debug(ctx, "GitHub API response error: %v", err)
		return fmt.Errorf("failed to get repository contents: %w", err)
	}

	// Process files concurrently with a worker pool
	const maxWorkers = 5
	filesChan := make(chan *github.RepositoryContent)
	errChan := make(chan error, maxWorkers)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var walkWg sync.WaitGroup
	walkWg.Add(1)
	go func() {
		defer walkWg.Done()
		defer close(filesChan) // Ensure channel gets closed

		if err := ri.walkDirectory(workerCtx, directoryContent, branch, filesChan); err != nil {
			logger.Error(ctx, "Error during directory walk: %v", err)
			errChan <- err
			cancel()
		}
	}()

	var workerWg sync.WaitGroup
	// Start workers
	for i := 0; i < maxWorkers; i++ {
		workerWg.Add(1)
		go func(workerNum int) {
			defer workerWg.Done()
			for file := range filesChan {
				select {
				case <-workerCtx.Done():
					return
				default:
				}
				if err := ri.processFile(ctx, file, branch); err != nil {
					logger.Error(workerCtx, "Worker %d failed to process %s: %v",
						workerNum, file.GetPath(), err)
					errChan <- err
					cancel()
					return
				}
			}
		}(i)
	}

	walkWg.Wait()   // Wait for directory walk to complete
	workerWg.Wait() // Wait for workers to finish
	close(errChan)

	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		// Combine all errors into one message
		var errMsgs []string
		for _, err := range errors {
			errMsgs = append(errMsgs, err.Error())
		}
		return fmt.Errorf("indexing failed with errors: %s", strings.Join(errMsgs, "; "))
	}
	logger.Debug(ctx, "============ finish indexing =============")

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
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
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

		ri.logger.Info(ctx, "Indexed chunk %d/%d for file %s",
			i+1, len(chunks), file.GetPath())
	}
	ri.logger.Info(ctx, "Finish index file: %s", file.GetPath())

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

func (ri *RepoIndexer) getCommitDifferences(ctx context.Context, oldSHA, newSHA string) ([]*github.CommitFile, error) {
	comparison, _, err := ri.githubTools.client.Repositories.CompareCommits(
		ctx,
		ri.githubTools.owner,
		ri.githubTools.repo,
		oldSHA,
		newSHA,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to compare commits: %w", err)
	}

	return comparison.Files, nil
}

func (ri *RepoIndexer) processChangedFiles(ctx context.Context, changes []*github.CommitFile, dbPath string) error {
	logger := logging.GetLogger()

	for _, file := range changes {
		if shouldSkipFile(file.GetFilename()) {
			continue
		}

		logger.Debug(ctx, "Processing changed file: %s", file.GetFilename())

		// Process the file and update the index
		if err := ri.processChangedFile(ctx, file, dbPath); err != nil {
			return fmt.Errorf("failed to process file %s: %w", file.GetFilename(), err)
		}
	}

	return nil
}

// Add this to indexer.go.
func (ri *RepoIndexer) processChangedFile(ctx context.Context, file *github.CommitFile, dbPath string) error {
	// Get the file content
	content, err := ri.githubTools.GetFileContent(ctx, file.GetFilename())
	if err != nil {
		return fmt.Errorf("failed to get file content: %w", err)
	}

	// Configure chunking
	config := NewChunkConfig()
	config.fileMetadata = map[string]interface{}{
		"file_path": file.GetFilename(),
		"file_type": filepath.Ext(file.GetFilename()),
		"package":   filepath.Base(filepath.Dir(file.GetFilename())),
	}

	// Chunk the file content
	chunks, err := chunkfile(ctx, content, "", config)
	if err != nil {
		return fmt.Errorf("failed to chunk file: %w", err)
	}

	// Process each chunk
	for i, chunk := range chunks {
		chunkID := fmt.Sprintf("%s:chunk_%d", file.GetFilename(), i+1)
		metadata := map[string]string{
			"file_path":    file.GetFilename(),
			"chunk_number": fmt.Sprintf("%d", i+1),
			"total_chunks": fmt.Sprintf("%d", len(chunks)),
			"start_line":   fmt.Sprintf("%d", chunk.startline),
			"end_line":     fmt.Sprintf("%d", chunk.endline),
		}

		// Generate embedding for the chunk
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
	}

	return nil
}
