package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/google/go-github/v68/github"
	"github.com/logrusorgru/aurora"
)

// RepoIndexer handles the indexing of repository content into the RAG store.
type RepoIndexer struct {
	githubTools GitHubInterface
	ragStore    RAGStore
	logger      *logging.Logger

	workers int
}

type LanguageStats struct {
	Language string
	Count    int
	Bytes    int64
}

// NewRepoIndexer creates a new repository indexer.
func NewRepoIndexer(githubTools GitHubInterface, ragStore RAGStore, workers int) *RepoIndexer {
	return &RepoIndexer{
		githubTools: githubTools,
		ragStore:    ragStore,
		logger:      logging.GetLogger(),
		workers:     workers,
	}
}

// IndexRepository indexes the entire repository content into the RAG store.
// IndexRepositoryBackground runs indexing silently without console output (for background mode)
func (ri *RepoIndexer) IndexRepositoryBackground(ctx context.Context, branch, dbPath string) error {
	logger := ri.logger
	latestSHA, err := ri.githubTools.GetLatestCommitSHA(ctx, branch)
	if err != nil {
		return fmt.Errorf("failed to get latest commit SHA: %w", err)
	}

	needFullIndex := false
	lastIndexedSHA, err := ri.getLastIndexedCommit(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			logger.Debug(ctx, "No previous index found, requiring full index")
			needFullIndex = true
		} else {
			return fmt.Errorf("failed to check last indexed commit: %w", err)
		}
	}

	if latestSHA == lastIndexedSHA && !needFullIndex {
		logger.Debug(ctx, "Repository already indexed at latest commit %s", latestSHA)
		return nil
	}

	logger.Debug(ctx, "Repository indexing in progress (commit: %s)", latestSHA)

	if needFullIndex {
		// Silent full index
		if err := ri.performFullIndexSilent(ctx, latestSHA); err != nil {
			return fmt.Errorf("full index failed: %w", err)
		}
	} else {
		// Silent incremental index
		if err := ri.performIncrementalIndexSilent(ctx, lastIndexedSHA, latestSHA); err != nil {
			return fmt.Errorf("incremental index failed: %w", err)
		}
	}

	// Update last indexed commit
	if err := ri.updateLastIndexedCommit(ctx, latestSHA); err != nil {
		return fmt.Errorf("failed to update last indexed commit: %w", err)
	}

	logger.Debug(ctx, "✅ Repository indexing completed successfully")
	return nil
}

// performFullIndexSilent runs full indexing without console output
func (ri *RepoIndexer) performFullIndexSilent(ctx context.Context, latestSHA string) error {
	logger := ri.logger
	language, err := ri.detectRepositoryLanguage(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to detect language: %v, defaulting to Go", err)
		language = "Go"
	}

	if err := ri.ragStore.PopulateGuidelines(ctx, language); err != nil {
		return fmt.Errorf("failed to populate guidelines: %w", err)
	}

	var directoryContent []*github.RepositoryContent
	repoInfo := ri.githubTools.GetRepositoryInfo(ctx)
	_, content, _, err := ri.githubTools.GetRepositoryContents(
		ctx,
		repoInfo.Owner,
		repoInfo.Name,
		"", // Root directory
		&github.RepositoryContentGetOptions{
			Ref: "",
		},
	)
	directoryContent = content
	if err != nil {
		return fmt.Errorf("failed to get repository contents: %w", err)
	}

	// Count total files first
	var totalFiles int32
	countChan := make(chan *github.RepositoryContent)
	var countWg sync.WaitGroup
	countWg.Add(1)
	go func() {
		defer countWg.Done()
		defer close(countChan)
		if err := ri.walkDirectory(ctx, directoryContent, "", countChan); err != nil {
			logger.Error(ctx, "Error during file count: %v", err)
		}
	}()

	for range countChan {
		atomic.AddInt32(&totalFiles, 1)
	}
	countWg.Wait()

	// Process files concurrently with a worker pool
	maxWorkers := ri.workers
	filesChan := make(chan *github.RepositoryContent)
	errChan := make(chan error, maxWorkers)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var processedFiles int32

	var walkWg sync.WaitGroup
	walkWg.Add(1)
	go func() {
		defer walkWg.Done()
		defer close(filesChan)

		if err := ri.walkDirectory(workerCtx, directoryContent, "", filesChan); err != nil {
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
				if err := ri.processFile(ctx, file, ""); err != nil {
					logger.Error(workerCtx, "Worker %d failed to process %s: %v",
						workerNum, file.GetPath(), err)
					errChan <- err
					cancel()
					return
				}

				atomic.AddInt32(&processedFiles, 1)
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

	logger.Debug(ctx, "Silent full indexing completed")
	return nil
}

// performIncrementalIndexSilent runs incremental indexing without console output
func (ri *RepoIndexer) performIncrementalIndexSilent(ctx context.Context, lastIndexedSHA, latestSHA string) error {
	logger := ri.logger
	changes, err := ri.getCommitDifferences(ctx, lastIndexedSHA, latestSHA)
	if err != nil {
		return fmt.Errorf("failed to get commit differences: %w", err)
	}

	var totalChanges int
	for _, file := range changes {
		if !shouldSkipFile(file.GetFilename()) {
			totalChanges++
		}
	}

	logger.Debug(ctx, "Processing %d changed files silently", totalChanges)

	for _, file := range changes {
		if shouldSkipFile(file.GetFilename()) {
			continue
		}

		logger.Debug(ctx, "Processing changed file: %s", file.GetFilename())

		// Process the file and update the index
		if err := ri.processChangedFileSilent(ctx, file); err != nil {
			logger.Error(ctx, "Failed to process file %s: %v", file.GetFilename(), err)
			return fmt.Errorf("failed to process file %s: %w", file.GetFilename(), err)
		}
	}

	logger.Debug(ctx, "Successfully processed %d changed files", totalChanges)
	return nil
}

// processChangedFileSilent processes a changed file without console output
func (ri *RepoIndexer) processChangedFileSilent(ctx context.Context, file *github.CommitFile) error {
	logger := ri.logger
	// Get the file content
	content, err := ri.githubTools.GetFileContent(ctx, file.GetFilename())
	if err != nil {
		logger.Debug(ctx, "failed to get file content: %v", err)
		return fmt.Errorf("failed to get file content: %w", err)
	}

	// Configure chunking
	config, err := NewChunkConfig(
		WithMaxBytes(9000),
	)
	if err != nil {
		logger.Debug(ctx, "failed to create chunk config: %v", err)
		return fmt.Errorf("failed to create chunk config: %w", err)
	}
	config.fileMetadata = map[string]interface{}{
		"file_path": file.GetFilename(),
		"file_type": filepath.Ext(file.GetFilename()),
		"package":   filepath.Base(filepath.Dir(file.GetFilename())),
	}
	// Chunk the file content
	chunks, err := chunkfile(ctx, content, file.GetPatch(), config)
	if err != nil {
		logger.Debug(ctx, "failed to chunk file: %v", err)
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
			"content_type": ContentTypeRepository,
			"description":  chunk.description,
		}
		embeddingText := chunk.content
		if description, exists := metadata["description"]; exists {
			embeddingText = fmt.Sprintf("%s\n\n# Description:\n%s", chunk.content, description)
		}
		// Generate embedding for the chunk
		llm := core.GetTeacherLLM()
		embedding, err := llm.CreateEmbedding(ctx, embeddingText)
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
			logger.Debug(ctx, "No previous index found, requiring full index")
			needFullIndex = true
		} else {
			return fmt.Errorf("failed to check last indexed commit: %w", err)
		}
	}
	if needFullIndex {
		repoInfo := ri.githubTools.GetRepositoryInfo(ctx)
		logger.Debug(ctx, "Indexing repository with config:")
		logger.Debug(ctx, "  Owner: %s", repoInfo.Owner)
		logger.Debug(ctx, "  Repo: %s", repoInfo.Name)
		logger.Debug(ctx, "  Branch: %s", branch)
		logger.Debug(ctx, "  Token set: %v", ri.githubTools.Client() != nil)
		logger.Debug(ctx, "=======================")
		if branch == "" {
			// Get repository information to find default branch
			repo, _, err := ri.githubTools.GetRepository(ctx, repoInfo.Owner, repoInfo.Name)
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
		logger.Info(ctx, "failed to increment index: %v", err)
		return fmt.Errorf("failed to increment index: %v", err)
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

			repoInfo := ri.githubTools.GetRepositoryInfo(ctx)
			_, subContents, _, err := ri.githubTools.GetRepositoryContents(
				ctx,
				repoInfo.Owner,
				repoInfo.Name,
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
	if shouldSkipFile(file.GetName()) {
		return nil
	}
	content, err := ri.githubTools.GetFileContent(ctx, file.GetPath())

	if err != nil {
		return fmt.Errorf("failed to get file content: %w", err)
	}

	// Configure chunking
	config, err := NewChunkConfig(WithMaxBytes(9000))
	if err != nil {
		return fmt.Errorf("failed to create chunk config: %w", err)
	}
	if content == "" {
		return fmt.Errorf("empty file content for %s", file.GetPath())
	}
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
			"content_type": ContentTypeRepository,
		}

		embeddingContent, err := preprocessForEmbedding(chunk.content)
		if err != nil {
			return fmt.Errorf("failed to preprocess chunk for embedding: %w", err)
		}
		// Generate embedding for the chunk using the default LLM
		llm := core.GetTeacherLLM()
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

		ri.logger.Debug(ctx, "Indexed chunk %d/%d for file %s",
			i+1, len(chunks), file.GetPath())
	}
	ri.logger.Debug(ctx, "Finish index file: %s", file.GetPath())

	return nil
}

// SearchSimilarCode finds similar code snippets in the indexed repository.
func (ri *RepoIndexer) SearchSimilarCode(ctx context.Context, query string, limit int) ([]*Content, error) {
	// Generate embedding for the query
	llm := core.GetTeacherLLM()
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
	repoInfo := ri.githubTools.GetRepositoryInfo(ctx)
	comparison, _, err := ri.githubTools.CompareCommits(
		ctx,
		repoInfo.Owner,
		repoInfo.Name,
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

	console := NewConsole(os.Stdout, logger, nil)
	var totalChanges int
	for _, file := range changes {
		if !shouldSkipFile(file.GetFilename()) {
			totalChanges++
		}
	}

	if console.Color() {
		console.Printf("%s %s %s\n",
			aurora.Green("⚡").Bold(),
			aurora.White(fmt.Sprintf("Starting incremental indexing for %d %s",
				totalChanges,
				pluralize("file", totalChanges))).Bold(),
			aurora.Blue("...").String(),
		)
	} else {
		console.Printf("⚡ Starting incremental indexing for %d %s...\n",
			totalChanges,
			pluralize("file", totalChanges))
	}

	var processedFiles int32
	console.StartSpinner("")

	// Create a ticker for updating progress
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	// Start progress updates in background
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := atomic.LoadInt32(&processedFiles)
				if totalChanges > 0 {
					percentage := float64(current) / float64(totalChanges) * 100
					console.UpdateSpinnerText(fmt.Sprintf("Indexing changed files... %.1f%% (%d/%d files)\n",
						percentage, current, totalChanges))
				}
			}
		}
	}()

	for _, file := range changes {
		if shouldSkipFile(file.GetFilename()) {
			continue
		}

		logger.Debug(ctx, "Processing changed file: %s", file.GetFilename())

		// Process the file and update the index
		if err := ri.processChangedFile(ctx, file, dbPath); err != nil {
			console.StopSpinner()
			console.FileError(file.GetFilename(), fmt.Errorf("failed to process file: %w", err))
			return fmt.Errorf("failed to process file %s: %w", file.GetFilename(), err)
		}

		atomic.AddInt32(&processedFiles, 1)
	}

	console.StopSpinner()

	// Show completion message
	if console.Color() {
		console.Printf("%s %s\n",
			aurora.Green("✓").Bold(),
			aurora.White(fmt.Sprintf("Successfully processed %d %s",
				totalChanges,
				pluralize("file", totalChanges))).Bold(),
		)
	} else {
		console.Printf("✓ Successfully processed %d %s\n",
			totalChanges,
			pluralize("file", totalChanges))
	}

	return nil
}

// Add this to indexer.go.
func (ri *RepoIndexer) processChangedFile(ctx context.Context, file *github.CommitFile, dbPath string) error {
	logger := logging.GetLogger()
	// Get the file content
	content, err := ri.githubTools.GetFileContent(ctx, file.GetFilename())
	if err != nil {
		logger.Debug(ctx, "failed to get file content: %v", err)
		return fmt.Errorf("failed to get file content: %w", err)
	}

	// Configure chunking
	config, err := NewChunkConfig(
		WithMaxBytes(9000),
	)
	if err != nil {
		logger.Debug(ctx, "failed to create chunk config: %v", err)
		return fmt.Errorf("failed to create chunk config: %w", err)
	}
	config.fileMetadata = map[string]interface{}{
		"file_path": file.GetFilename(),
		"file_type": filepath.Ext(file.GetFilename()),
		"package":   filepath.Base(filepath.Dir(file.GetFilename())),
	}
	// Chunk the file content
	chunks, err := chunkfile(ctx, content, file.GetPatch(), config)
	if err != nil {
		logger.Debug(ctx, "failed to chunk file: %v", err)
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
			"content_type": ContentTypeRepository,
			"description":  chunk.description,
		}
		embeddingText := chunk.content
		if description, exists := metadata["description"]; exists {
			embeddingText = fmt.Sprintf("%s\n\n# Description:\n%s", chunk.content, description)
		}
		// Generate embedding for the chunk
		llm := core.GetTeacherLLM()
		embedding, err := llm.CreateEmbedding(ctx, embeddingText)
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

	repoInfo := ri.githubTools.GetRepositoryInfo(ctx)
	// Get repository language statistics from GitHub API
	langs, _, err := ri.githubTools.ListLanguages(
		ctx,
		repoInfo.Owner,
		repoInfo.Name,
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
	logger.Debug(ctx, "Detected primary language: %s (%d bytes)",
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

	console := NewConsole(os.Stdout, logger, nil)

	if err := ri.ragStore.PopulateGuidelines(ctx, language); err != nil {
		return fmt.Errorf("failed to populate guidelines: %w", err)
	}

	var directoryContent []*github.RepositoryContent
	repoInfo := ri.githubTools.GetRepositoryInfo(ctx)
	err = console.WithSpinner(ctx, "Fetching repository contents...", func() error {
		_, content, _, err := ri.githubTools.GetRepositoryContents(
			ctx,
			repoInfo.Owner,
			repoInfo.Name,
			"", // Root directory
			&github.RepositoryContentGetOptions{
				Ref: branch,
			},
		)
		directoryContent = content
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to get repository contents: %w", err)
	}
	// Count total files first
	var totalFiles int32
	countChan := make(chan *github.RepositoryContent)
	var countWg sync.WaitGroup
	countWg.Add(1)
	go func() {
		defer countWg.Done()
		defer close(countChan)
		if err := ri.walkDirectory(ctx, directoryContent, branch, countChan); err != nil {
			logger.Error(ctx, "Error during file count: %v", err)
		}
	}()

	for range countChan {
		atomic.AddInt32(&totalFiles, 1)
	}
	countWg.Wait()
	// Process files concurrently with a worker pool
	maxWorkers := ri.workers
	filesChan := make(chan *github.RepositoryContent)
	errChan := make(chan error, maxWorkers)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var processedFiles int32
	console.StartSpinner("Indexing repository...")
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				current := atomic.LoadInt32(&processedFiles)
				total := atomic.LoadInt32(&totalFiles)
				if total > 0 {
					percentage := float64(current) / float64(total) * 100
					console.UpdateSpinnerText(fmt.Sprintf("Indexing repository... %.1f%% (%d/%d files)",
						percentage, current, total))
				}
			}
		}
	}()

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

				atomic.AddInt32(&processedFiles, 1)
			}
		}(i)
	}

	walkWg.Wait()   // Wait for directory walk to complete
	workerWg.Wait() // Wait for workers to finish

	console.StopSpinner()
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
