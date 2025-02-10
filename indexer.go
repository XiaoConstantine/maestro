package main

import (
	"context"
	"database/sql"
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

type LanguageStats struct {
	Language string
	Count    int
	Bytes    int64
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
func (ri *RepoIndexer) IndexRepository(ctx context.Context, branch, dbPath string) error {
	logger := ri.logger
	latestSHA, err := ri.githubTools.GetLatestCommitSHA(ctx, branch)
	if err != nil {
		return fmt.Errorf("failed to get latest commit SHA: %w", err)
	}

	needFullIndex := false
	lastIndexedSHA, err := ri.getLastIndexedCommit(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			logger.Info(ctx, "No previous index found, requiring full index")
			needFullIndex = true
		} else {
			return fmt.Errorf("failed to check last indexed commit: %w", err)
		}
	}
	if needFullIndex {
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

		return ri.fullIndex(ctx, branch, latestSHA)

	}
	changes, err := ri.getCommitDifferences(ctx, lastIndexedSHA, latestSHA)
	if err != nil {
		return fmt.Errorf("failed to get commit diff")
	}
	// Process only the changed files
	err = ri.processChangedFiles(ctx, changes, dbPath)
	if err != nil {
		return fmt.Errorf("failed to increment index")
	}
	if err := ri.updateLastIndexedCommit(ctx, latestSHA); err != nil {
		return fmt.Errorf("failed to update last indexed commit: %w", err)
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

		embeddingContent, err := preprocessForEmbedding(chunk.content)
		if err != nil {
			return fmt.Errorf("failed to preprocess chunk for embedding: %w", err)
		}
		// Generate embedding for the chunk using the default LLM
		llm := core.GetDefaultLLM()
		embedding, err := llm.CreateEmbedding(ctx, embeddingContent)

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

// detectRepositoryLanguage analyzes repository contents to determine the primary language.
func (ri *RepoIndexer) detectRepositoryLanguage(ctx context.Context) (string, error) {
	logger := logging.GetLogger()
	logger.Debug(ctx, "Detecting repository primary language")

	// Get repository language statistics from GitHub API
	langs, _, err := ri.githubTools.client.Repositories.ListLanguages(
		ctx,
		ri.githubTools.owner,
		ri.githubTools.repo,
	)
	if err != nil {
		return "", fmt.Errorf("failed to get language statistics: %w", err)
	}

	// Convert map to slice for sorting
	var stats []LanguageStats
	for lang, bytes := range langs {
		stats = append(stats, LanguageStats{
			Language: lang,
			Bytes:    int64(bytes),
		})
	}

	// Sort by bytes in descending order
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Bytes > stats[j].Bytes
	})

	if len(stats) == 0 {
		logger.Warn(ctx, "No language statistics found, defaulting to Go")
		return "Go", nil
	}

	primaryLang := stats[0].Language
	logger.Info(ctx, "Detected primary language: %s (%d bytes)",
		primaryLang, stats[0].Bytes)

	// Log secondary languages if present
	if len(stats) > 1 {
		var secondary []string
		for _, stat := range stats[1:] {
			secondary = append(secondary, fmt.Sprintf("%s (%d bytes)",
				stat.Language, stat.Bytes))
		}
		logger.Debug(ctx, "Secondary languages: %s", strings.Join(secondary, ", "))
	}

	return primaryLang, nil
}

func (ri *RepoIndexer) getLastIndexedCommit(ctx context.Context) (string, error) {
	return ri.ragStore.GetMetadata(ctx, "last_indexed_commit")
}

func (ri *RepoIndexer) updateLastIndexedCommit(ctx context.Context, sha string) error {
	return ri.ragStore.SetMetadata(ctx, "last_indexed_commit", sha)
}

func (ri *RepoIndexer) fullIndex(ctx context.Context, branch, latestSHA string) error {
	logger := ri.logger
	language, err := ri.detectRepositoryLanguage(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to detect language: %v, defaulting to Go", err)
		language = "Go"
	}
	if err := ri.ragStore.PopulateGuidelines(ctx, language); err != nil {
		return fmt.Errorf("failed to populate guidelines: %w", err)
	}
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
	if err := ri.updateLastIndexedCommit(ctx, latestSHA); err != nil {
		return fmt.Errorf("failed to update last indexed commit: %w", err)
	}

	return nil

}
