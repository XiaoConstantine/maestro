// Package rag provides RAG (Retrieval Augmented Generation) store functionality.
package rag

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/logrusorgru/aurora"

	_ "github.com/mattn/go-sqlite3"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/config"
	"github.com/XiaoConstantine/maestro/internal/guideline"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
)

// Content type constants.
const (
	ContentTypeRepository = "repository"
	ContentTypeGuideline  = "guideline"
)

// DebugInfo is an alias to types.DebugInfo for interface compatibility.
type DebugInfo = types.DebugInfo

// RAGStore interface defines the RAG store operations.
type RAGStore interface {
	// StoreContent saves a content piece with its embedding
	StoreContent(ctx context.Context, content *types.Content) error

	// StoreContents saves multiple content pieces in a single transaction for better performance
	StoreContents(ctx context.Context, contents []*types.Content) error

	// FindSimilar finds the most similar content pieces to the given embedding
	FindSimilar(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*types.Content, error)

	// FindSimilarWithDebug finds similar content with detailed debugging information
	FindSimilarWithDebug(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*types.Content, DebugInfo, error)

	// UpdateContent updates an existing content piece
	UpdateContent(ctx context.Context, content *types.Content) error

	// DeleteContent removes content by ID
	DeleteContent(ctx context.Context, id string) error

	// Populate style guide, best practices based on repo language
	PopulateGuidelines(ctx context.Context, language string) error

	// PopulateGuidelinesBackground runs guideline population without console output (for async use)
	PopulateGuidelinesBackground(ctx context.Context, language string) error

	StoreRule(ctx context.Context, rule types.ReviewRule) error

	// HasContent checks if the database contains any indexed content
	HasContent(ctx context.Context) (bool, error)

	// DB version control
	GetMetadata(ctx context.Context, key string) (string, error)
	SetMetadata(ctx context.Context, key, value string) error

	// FindRelevantGuidelines performs pattern-based multi-vector search for guidelines
	FindRelevantGuidelines(ctx context.Context, patterns []types.SimpleCodePattern, limit int) ([]types.GuidelineSearchResult, error)

	// ClearPatternCache clears the pattern cache
	ClearPatternCache()

	Close() error
}

// ConsoleInterface defines console operations needed by RAG store.
type ConsoleInterface interface {
	WithSpinner(ctx context.Context, message string, fn func() error) error
	StartSpinner(message string)
	UpdateSpinnerText(message string)
	StopSpinner()
	Printf(format string, args ...interface{})
	Color() bool
}

// sqliteRAGStore implements RAGStore using SQLite.
type sqliteRAGStore struct {
	db     *sql.DB
	log    *logging.Logger
	mu     sync.RWMutex
	closed bool

	// Pattern-level cache for guideline search results to avoid redundant queries
	patternCacheMu sync.RWMutex
	patternCache   map[string][]types.GuidelineSearchResult // key: pattern name, value: search results

	// Sgrep-based guideline search
	guidelineSearcher *GuidelineSearchEnhancer
	dataDir           string

	// Console for user feedback (optional)
	console ConsoleInterface
}

// NewSQLiteRAGStore creates a new SQLite-backed RAG store.
func NewSQLiteRAGStore(db *sql.DB, logger *logging.Logger, dataDir string) (RAGStore, error) {
	store := &sqliteRAGStore{
		db:                db,
		log:               logger,
		closed:            false,
		patternCache:      make(map[string][]types.GuidelineSearchResult),
		guidelineSearcher: NewGuidelineSearchEnhancer(logger, dataDir),
		dataDir:           dataDir,
	}

	if err := store.init(); err != nil {
		return nil, fmt.Errorf("failed to initialize RAG store: %w", err)
	}

	return store, nil
}

// NewSQLiteRAGStoreWithConsole creates a new SQLite-backed RAG store with console support.
func NewSQLiteRAGStoreWithConsole(db *sql.DB, logger *logging.Logger, dataDir string, console ConsoleInterface) (RAGStore, error) {
	store := &sqliteRAGStore{
		db:                db,
		log:               logger,
		closed:            false,
		patternCache:      make(map[string][]types.GuidelineSearchResult),
		guidelineSearcher: NewGuidelineSearchEnhancer(logger, dataDir),
		dataDir:           dataDir,
		console:           console,
	}

	if err := store.init(); err != nil {
		return nil, fmt.Errorf("failed to initialize RAG store: %w", err)
	}

	return store, nil
}

// SetConsole sets the console for user feedback.
func (s *sqliteRAGStore) SetConsole(console ConsoleInterface) {
	s.console = console
}

func (s *sqliteRAGStore) init() error {
	// Get configurable vector dimensions
	vectorDims := GetVectorDimensions()
	s.log.Debug(context.Background(), "Initializing RAG store with vector dimensions: %d", vectorDims)

	// Check if we need to migrate the database for dimension changes
	if err := s.handleDimensionMigration(vectorDims); err != nil {
		return fmt.Errorf("failed to handle dimension migration: %w", err)
	}

	queries := []string{
		// db meta data table
		`CREATE TABLE IF NOT EXISTS db_metadata (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		// Original metadata table
		`CREATE TABLE IF NOT EXISTS contents (
		id TEXT PRIMARY KEY,
		text BLOB COMPRESSED,
		metadata TEXT,
		content_type TEXT, -- 'repository' or 'guideline',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Vector virtual table (REQUIRED for sqlite-vec) with configurable dimensions
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_items USING vec0(
		rowid INTEGER PRIMARY KEY,
		embedding int8[%d] distance_metric=l2,  -- Configurable embedding dimensions for unified model
		content_id TEXT PARTITION KEY  // Optimizes WHERE clause filtering
		)`, vectorDims),

		// Rule table
		`    CREATE TABLE IF NOT EXISTS review_rules (
		id TEXT PRIMARY KEY,
		dimension TEXT NOT NULL,
		category TEXT NOT NULL,
		name TEXT NOT NULL,
		description TEXT NOT NULL,
		examples JSON NOT NULL,
		metadata JSON NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE INDEX IF NOT EXISTS idx_metadata_key ON db_metadata(key)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("failed to initialize table: %w", err)
		}
	}

	// Store the current vector dimensions in metadata for future migration checks
	if err := s.SetMetadata(context.Background(), "vector_dimensions", strconv.Itoa(vectorDims)); err != nil {
		s.log.Warn(context.Background(), "Failed to store vector dimensions metadata: %v", err)
	}

	return nil
}

// GetVectorDimensions returns the configured vector dimensions.
func GetVectorDimensions() int {
	return config.GetVectorDimensions()
}

// StoreContent implements RAGStore interface.
func (s *sqliteRAGStore) StoreContent(ctx context.Context, content *types.Content) error {
	s.log.Debug(ctx, "Starting StoreContent for ID: %s", content.ID)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("store is closed")
	}

	contentType, exists := content.Metadata["content_type"]
	if !exists {
		return fmt.Errorf("content_type must be specified in metadata")
	}

	if contentType != ContentTypeRepository && contentType != ContentTypeGuideline {
		return fmt.Errorf("invalid content_type: %s", contentType)
	}

	s.log.Debug(ctx, "Beginning transaction for content ID: %s", content.ID)
	// Start a transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			s.log.Error(context.Background(), "failed to rollback transaction: %v", err)
		}
	}()

	// Convert metadata to JSON
	s.log.Debug(ctx, "Marshaling metadata for content ID: %s", content.ID)
	metadata, err := json.Marshal(content.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Compress the content text
	compressedText, err := util.CompressText(content.Text)
	if err != nil {
		return fmt.Errorf("failed to compress content: %w", err)
	}
	s.log.Debug(ctx, "Executing SQLite insert/replace for content ID: %s", content.ID)

	_, err = tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO contents (id, text, metadata, content_type)
     VALUES (?, ?, ?, ?)`,
		content.ID, compressedText, string(metadata), contentType)
	if err != nil {
		return fmt.Errorf("failed to store content metadata: %w", err)
	}

	blob, err := sqlite_vec.SerializeFloat32(content.Embedding)
	if err != nil {
		return fmt.Errorf("failed to serialize embedding: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO vec_items (embedding, content_id)
     VALUES (vec_quantize_int8(vec_f32(?), 'unit'), ?)`, // Use full embedding dimensions
		blob, content.ID)
	if err != nil {
		return fmt.Errorf("failed to store content: %w", err)
	}

	s.log.Debug(ctx, "Committing transaction for content ID: %s", content.ID)
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.log.Debug(ctx, "Successfully stored content ID: %s", content.ID)
	return nil
}

// StoreContents saves multiple content pieces in a single transaction for better performance.
func (s *sqliteRAGStore) StoreContents(ctx context.Context, contents []*types.Content) error {
	if len(contents) == 0 {
		return nil
	}

	s.log.Debug(ctx, "Starting batch StoreContents for %d items", len(contents))
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("store is closed")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			s.log.Error(context.Background(), "failed to rollback transaction: %v", err)
		}
	}()

	contentStmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO contents (id, text, metadata, content_type) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare content statement: %w", err)
	}
	defer contentStmt.Close()

	vecStmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO vec_items (embedding, content_id) VALUES (vec_quantize_int8(vec_f32(?), 'unit'), ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare vector statement: %w", err)
	}
	defer vecStmt.Close()

	for _, content := range contents {
		contentType, exists := content.Metadata["content_type"]
		if !exists {
			return fmt.Errorf("content_type must be specified in metadata for ID: %s", content.ID)
		}

		if contentType != ContentTypeRepository && contentType != ContentTypeGuideline {
			return fmt.Errorf("invalid content_type: %s for ID: %s", contentType, content.ID)
		}

		metadata, err := json.Marshal(content.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata for ID %s: %w", content.ID, err)
		}

		compressedText, err := util.CompressText(content.Text)
		if err != nil {
			return fmt.Errorf("failed to compress content for ID %s: %w", content.ID, err)
		}

		_, err = contentStmt.ExecContext(ctx, content.ID, compressedText, string(metadata), contentType)
		if err != nil {
			return fmt.Errorf("failed to store content metadata for ID %s: %w", content.ID, err)
		}

		blob, err := sqlite_vec.SerializeFloat32(content.Embedding)
		if err != nil {
			return fmt.Errorf("failed to serialize embedding for ID %s: %w", content.ID, err)
		}

		_, err = vecStmt.ExecContext(ctx, blob, content.ID)
		if err != nil {
			return fmt.Errorf("failed to store vector for ID %s: %w", content.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit batch transaction: %w", err)
	}

	s.log.Debug(ctx, "Successfully stored %d content items in batch", len(contents))
	return nil
}

// PopulateGuidelines populates guidelines during database initialization using sgrep for indexing.
func (s *sqliteRAGStore) PopulateGuidelines(ctx context.Context, language string) error {
	s.log.Debug(ctx, "Starting guideline population for language: %s", language)

	fetcher := guideline.NewFetcher(s.log)

	// Fetch guidelines
	var guidelines []types.GuidelineContent
	var err error

	if s.console != nil {
		err = s.console.WithSpinner(ctx, "Fetching coding guidelines...", func() error {
			var fetchErr error
			guidelines, fetchErr = fetcher.FetchGuidelines(ctx)
			return fetchErr
		})
	} else {
		guidelines, err = fetcher.FetchGuidelines(ctx)
	}

	if err != nil {
		return fmt.Errorf("failed to fetch guidelines: %w", err)
	}

	if s.console != nil {
		s.console.StartSpinner("Processing guidelines...")
	}
	s.log.Debug(ctx, "Fetched %d guidelines", len(guidelines))

	// Store rules in database and collect guidelines for sgrep indexing
	for i, g := range guidelines {
		if s.console != nil {
			progress := float64(i+1) / float64(len(guidelines)) * 100
			s.console.UpdateSpinnerText(fmt.Sprintf("Processing guidelines... %.1f%% (%d/%d)",
				progress, i+1, len(guidelines)))
		}

		rules, ruleErr := fetcher.ConvertGuidelineToRules(ctx, g)
		if ruleErr != nil {
			s.log.Error(ctx, "failed to convert guideline to rule: %v", ruleErr)
			continue
		}
		if len(rules) > 0 {
			if storeErr := s.StoreRule(ctx, rules[0]); storeErr != nil {
				s.log.Warn(ctx, "Failed to store rule: %v", storeErr)
				continue
			}
		}
	}

	// Write guidelines as markdown files for sgrep indexing
	if s.console != nil {
		s.console.UpdateSpinnerText("Writing guidelines for sgrep indexing...")
	}
	if err := s.guidelineSearcher.WriteGuidelines(ctx, guidelines); err != nil {
		s.log.Warn(ctx, "Failed to write guidelines for sgrep: %v", err)
	}

	// Index guidelines with sgrep
	if s.console != nil {
		s.console.UpdateSpinnerText("Indexing guidelines with sgrep...")
	}
	if err := s.guidelineSearcher.IndexGuidelines(ctx); err != nil {
		s.log.Warn(ctx, "Failed to index guidelines with sgrep: %v", err)
	}

	if s.console != nil {
		s.console.StopSpinner()
		if s.console.Color() {
			s.console.Printf("%s %s\n",
				aurora.Green("✓").Bold(),
				aurora.White(fmt.Sprintf("Successfully processed %d guidelines (sgrep indexed)", len(guidelines))).Bold(),
			)
		} else {
			s.console.Printf("✓ Successfully processed %d guidelines\n", len(guidelines))
		}
	}

	s.log.Debug(ctx, "Finished fetch guidelines")
	return nil
}

// PopulateGuidelinesBackground runs guideline population without console output.
// Use this for async initialization to avoid interfering with TUI.
func (s *sqliteRAGStore) PopulateGuidelinesBackground(ctx context.Context, language string) error {
	s.log.Info(ctx, "Starting background guideline population for language: %s", language)

	fetcher := guideline.NewFetcher(s.log)

	// Fetch guidelines
	guidelines, err := fetcher.FetchGuidelines(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch guidelines: %w", err)
	}

	s.log.Debug(ctx, "Fetched %d guidelines", len(guidelines))

	// Store rules in database and collect guidelines for sgrep indexing
	for _, g := range guidelines {
		rules, ruleErr := fetcher.ConvertGuidelineToRules(ctx, g)
		if ruleErr != nil {
			s.log.Error(ctx, "failed to convert guideline to rule: %v", ruleErr)
			continue
		}
		if len(rules) > 0 {
			if storeErr := s.StoreRule(ctx, rules[0]); storeErr != nil {
				s.log.Warn(ctx, "Failed to store rule: %v", storeErr)
				continue
			}
		}
	}

	// Write guidelines as markdown files for sgrep indexing
	if err := s.guidelineSearcher.WriteGuidelines(ctx, guidelines); err != nil {
		s.log.Warn(ctx, "Failed to write guidelines for sgrep: %v", err)
	}

	// Index guidelines with sgrep
	if err := s.guidelineSearcher.IndexGuidelines(ctx); err != nil {
		s.log.Warn(ctx, "Failed to index guidelines with sgrep: %v", err)
	}

	s.log.Info(ctx, "Background guideline population completed: %d guidelines indexed", len(guidelines))
	return nil
}

// FindSimilar implements RAGStore interface.
func (s *sqliteRAGStore) FindSimilar(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*types.Content, error) {
	results, _, err := s.FindSimilarWithDebug(ctx, embedding, limit, contentTypes...)
	return results, err
}

// FindRelevantGuidelines performs pattern-based multi-vector search for guidelines.
// This method extracts code patterns from the file and searches guidelines accordingly,
// returning only high-confidence matches with specific relevance scoring.
func (s *sqliteRAGStore) FindRelevantGuidelines(
	ctx context.Context,
	patterns []types.SimpleCodePattern,
	limit int,
) ([]types.GuidelineSearchResult, error) {
	s.log.Debug(ctx, "Starting pattern-based guideline search for %d patterns", len(patterns))

	if len(patterns) == 0 {
		s.log.Warn(ctx, "No patterns provided for guideline search")
		return []types.GuidelineSearchResult{}, nil
	}

	enhancer := NewGuidelineSearchEnhancer(s.log, s.dataDir)
	allResults := make(map[string]*types.GuidelineSearchResult)

	// First, check cache for each pattern and identify which need fresh searches
	var uncachedPatterns []types.SimpleCodePattern
	cachedCount := 0

	s.patternCacheMu.RLock()
	for _, pattern := range patterns {
		if cached, exists := s.patternCache[pattern.Name]; exists {
			cachedCount++
			s.log.Debug(ctx, "Cache hit for pattern: %s (%d results)", pattern.Name, len(cached))
			// Merge cached results
			for _, result := range cached {
				if existing, exists := allResults[result.Content.ID]; exists {
					existing.FinalScore = (existing.FinalScore + result.FinalScore) / 2.0
					existing.Pattern = existing.Pattern + ", " + result.Pattern
				} else {
					resultCopy := result
					allResults[result.Content.ID] = &resultCopy
				}
			}
		} else {
			uncachedPatterns = append(uncachedPatterns, pattern)
		}
	}
	s.patternCacheMu.RUnlock()

	if cachedCount > 0 {
		s.log.Debug(ctx, "Pattern cache: %d hits, %d misses", cachedCount, len(uncachedPatterns))
	}

	// Process uncached patterns
	if len(uncachedPatterns) > 0 {
		patternEmbeddings, err := enhancer.EnhanceGuidelineQuery(ctx, uncachedPatterns)
		if err != nil {
			return nil, fmt.Errorf("failed to enhance query: %w", err)
		}

		if len(patternEmbeddings) == 0 && cachedCount == 0 {
			s.log.Warn(ctx, "Failed to create pattern embeddings and no cached results")
			return []types.GuidelineSearchResult{}, nil
		}

		// Search guidelines for each uncached pattern
		for pattern, embedding := range patternEmbeddings {
			s.log.Debug(ctx, "Searching guidelines for pattern: %s", pattern)

			searchLimit := limit * 3
			results, _, err := s.FindSimilarWithDebug(
				ctx, embedding, searchLimit, ContentTypeGuideline,
			)
			if err != nil {
				s.log.Warn(ctx, "Failed to find guidelines for pattern %s: %v", pattern, err)
				continue
			}

			// Build results for this pattern (for caching)
			var patternResults []types.GuidelineSearchResult

			for _, result := range results {
				var docEmbed []float32
				var chunkEmbeds [][]float32
				docEmbed = embedding
				chunkEmbeds = append(chunkEmbeds, embedding)

				score := ScoreGuideline(embedding, result, docEmbed, chunkEmbeds)

				searchResult := types.GuidelineSearchResult{
					Content:           result,
					DocumentScore:     score,
					BestChunkScore:    score,
					AverageChunkScore: score,
					FinalScore:        score,
					Pattern:           pattern,
					ContextDescription: fmt.Sprintf(
						"Relevant to %s pattern detected in your code",
						pattern,
					),
				}

				patternResults = append(patternResults, searchResult)

				// Merge into all results
				if existing, exists := allResults[result.ID]; exists {
					existing.FinalScore = (existing.FinalScore + score) / 2.0
					existing.Pattern = existing.Pattern + ", " + pattern
				} else {
					allResults[result.ID] = &searchResult
				}
			}

			// Cache results for this pattern
			if len(patternResults) > 0 {
				s.patternCacheMu.Lock()
				s.patternCache[pattern] = patternResults
				s.patternCacheMu.Unlock()
				s.log.Debug(ctx, "Cached %d results for pattern: %s", len(patternResults), pattern)
			}
		}
	}

	// Convert map to slice
	var results []types.GuidelineSearchResult
	for _, res := range allResults {
		results = append(results, *res)
	}

	// Deduplicate and sort
	results = DeduplicateResults(results)

	// Filter by confidence threshold (0.65)
	results = FilterByConfidence(results, 0.65)

	// Return top N
	if len(results) > limit {
		results = results[:limit]
	}

	s.log.Debug(ctx, "Found %d relevant guidelines with confidence >= 0.65", len(results))
	return results, nil
}

// ClearPatternCache clears the pattern cache, useful for new review sessions.
func (s *sqliteRAGStore) ClearPatternCache() {
	s.patternCacheMu.Lock()
	defer s.patternCacheMu.Unlock()
	s.patternCache = make(map[string][]types.GuidelineSearchResult)
}

// FindSimilarWithDebug implements RAGStore interface with comprehensive debugging.
func (s *sqliteRAGStore) FindSimilarWithDebug(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*types.Content, DebugInfo, error) {
	startTime := time.Now()
	logger := s.log
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, DebugInfo{}, fmt.Errorf("store is closed")
	}

	// Initialize debug info
	debuginfoObj := DebugInfo{
		QueryEmbeddingDims: len(embedding),
		RetrievalTime:      time.Since(startTime), // Will be updated at the end
	}

	// Serialize embedding with performance tracking
	embeddingStartTime := time.Now()
	blob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, debuginfoObj, fmt.Errorf("failed to serialize embedding: %w", err)
	}
	embeddingSerializationTime := time.Since(embeddingStartTime)

	if isRAGDebugEnabled() {
		s.log.Debug(ctx, "Embedding serialization took %v for %d dimensions", embeddingSerializationTime, len(embedding))
	}

	baseQuery := `
	SELECT c.id, c.text, c.metadata, v.distance
	FROM vec_items v
	JOIN contents c ON v.content_id = c.id
	WHERE v.embedding MATCH(vec_quantize_int8(vec_f32(?), 'unit'), 100)`

	// Add content type filter if specified
	var whereClauses []string
	args := []interface{}{blob} // Start with just the embedding blob

	if len(contentTypes) > 0 {
		placeholders := make([]string, len(contentTypes))
		for i, ct := range contentTypes {
			placeholders[i] = "?"
			args = append(args, ct)
		}
		whereClauses = append(whereClauses,
			fmt.Sprintf("c.content_type IN (%s)", strings.Join(placeholders, ",")))
	}

	// Add final k=? constraint for vector search result limit
	whereClauses = append(whereClauses, "k = ?")
	args = append(args, limit)

	// Build final query
	finalQuery := baseQuery
	if len(whereClauses) > 0 {
		finalQuery += " AND " + strings.Join(whereClauses, " AND ")
	}
	finalQuery += " ORDER BY distance ASC"

	// Enhanced query logging
	if isRAGDebugEnabled() {
		logger.Debug(ctx, "RAG Query: %s", formatQuery(finalQuery, args))
	}

	// Execute query with performance tracking
	queryStartTime := time.Now()
	rows, err := s.db.QueryContext(ctx, finalQuery, args...)
	if err != nil {
		return nil, debuginfoObj, fmt.Errorf("failed to query similar content: %w", err)
	}
	defer rows.Close()
	queryExecutionTime := time.Since(queryStartTime)

	var results []*types.Content
	var similarities []float64
	var topMatches []string

	// Process results with detailed tracking
	rowProcessingStart := time.Now()
	for rows.Next() {
		var (
			content        types.Content
			compressedText string
			metadataStr    string
			similarity     float64
		)

		if err := rows.Scan(&content.ID, &compressedText, &metadataStr, &similarity); err != nil {
			return nil, debuginfoObj, fmt.Errorf("failed to scan row: %w", err)
		}

		// Track similarity scores for debugging
		similarities = append(similarities, similarity)

		// Decompress the text
		decompressedText, err := util.DecompressText(compressedText)
		if err != nil {
			return nil, debuginfoObj, fmt.Errorf("failed to decompress content: %w", err)
		}

		content.Text = decompressedText

		// Parse metadata JSON
		if err := json.Unmarshal([]byte(metadataStr), &content.Metadata); err != nil {
			return nil, debuginfoObj, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}

		results = append(results, &content)

		// Build top matches for debugging (limit to top 3)
		if len(topMatches) < 3 {
			matchDesc := fmt.Sprintf("%s (%.4f)", content.ID, similarity)
			if category := content.Metadata["category"]; category != "" {
				matchDesc += fmt.Sprintf(" [%s]", category)
			}
			topMatches = append(topMatches, matchDesc)
		}
	}
	rowProcessingTime := time.Since(rowProcessingStart)

	if err := rows.Err(); err != nil {
		return nil, debuginfoObj, fmt.Errorf("error iterating results: %w", err)
	}

	// Complete debug info
	duration := time.Since(startTime)
	debuginfoObj.ResultCount = len(results)
	debuginfoObj.SimilarityScores = similarities
	debuginfoObj.TopMatches = topMatches
	debuginfoObj.RetrievalTime = duration
	debuginfoObj.QualityMetrics = s.calculateQualityMetrics(similarities)

	// Enhanced debug logging with performance breakdown
	if isRAGDebugEnabled() {
		s.logRAGRetrievalDebug(ctx, debuginfoObj, contentTypes, queryExecutionTime, rowProcessingTime)
	}

	// Standard retrieval metrics logging
	s.logRetrievalMetrics(ctx, contentTypes, results, similarities)

	// Additional detailed debug logging if enabled
	if isRAGDebugEnabled() {
		s.logDetailedRAGDebug(ctx, embedding, contentTypes, results, similarities)
	}

	return results, debuginfoObj, nil
}

// UpdateContent implements RAGStore interface.
func (s *sqliteRAGStore) UpdateContent(ctx context.Context, content *types.Content) error {
	// Since our StoreContent uses INSERT OR REPLACE, we can just delegate to it
	return s.StoreContent(ctx, content)
}

// DeleteContent implements RAGStore interface.
func (s *sqliteRAGStore) DeleteContent(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("store is closed")
	}

	result, err := s.db.ExecContext(ctx, "DELETE FROM contents WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete content: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("content with ID %s not found", id)
	}

	s.log.Debug(ctx, "Deleted content with ID: %s", id)
	return nil
}

func (s *sqliteRAGStore) GetMetadata(ctx context.Context, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var value string
	err := s.db.QueryRowContext(ctx,
		"SELECT value FROM db_metadata WHERE key = ?",
		key).Scan(&value)

	if err == sql.ErrNoRows {
		return "", err
	}
	if err != nil {
		return "", fmt.Errorf("failed to get metadata: %w", err)
	}

	return value, nil
}

func (s *sqliteRAGStore) SetMetadata(ctx context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
        INSERT INTO db_metadata (key, value, updated_at)
        VALUES (?, ?, CURRENT_TIMESTAMP)
        ON CONFLICT(key) DO UPDATE SET
            value = excluded.value,
            updated_at = CURRENT_TIMESTAMP`,
		key, value)

	if err != nil {
		return fmt.Errorf("failed to set metadata: %w", err)
	}

	return nil
}

func (s *sqliteRAGStore) StoreRule(ctx context.Context, rule types.ReviewRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("store is closed")
	}

	query := `
    INSERT INTO review_rules (
        id, dimension, category, name, description, examples, metadata
    ) VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET
        dimension = excluded.dimension,
        category = excluded.category,
        name = excluded.name,
        description = excluded.description,
        examples = excluded.examples,
        metadata = excluded.metadata,
        updated_at = CURRENT_TIMESTAMP`

	s.log.Debug(ctx, "Beginning transaction for content ID: %s", rule.ID)
	// Start a transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			s.log.Error(context.Background(), "failed to rollback transaction: %v", err)
		}
	}()

	// Convert metadata to JSON
	s.log.Debug(ctx, "Marshaling examples for content ID: %s", rule.ID)

	examples, err := json.Marshal(rule.Examples)
	if err != nil {
		return fmt.Errorf("failed to marshal examples: %w", err)
	}

	metadata, err := json.Marshal(rule.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	_, err = tx.ExecContext(ctx, query,
		rule.ID, rule.Dimension, rule.Category, rule.Name,
		rule.Description, examples, metadata)
	if err != nil {
		return fmt.Errorf("failed to store content rule: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// HasContent implements RAGStore interface.
func (s *sqliteRAGStore) HasContent(ctx context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return false, fmt.Errorf("store is closed")
	}

	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM contents").Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to count contents: %w", err)
	}

	return count > 0, nil
}

// Close implements RAGStore interface.
func (s *sqliteRAGStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true
	return s.db.Close()
}

func formatQuery(query string, args []interface{}) string {
	// Make a copy of the query so we don't modify the original
	formattedQuery := query

	// For each argument, replace the first ? with its string representation
	for _, arg := range args {
		// Handle different argument types appropriately
		var argStr string
		switch v := arg.(type) {
		case string:
			// Strings need to be quoted
			argStr = fmt.Sprintf("'%s'", v)
		case []byte:
			// For blob data (like embeddings), show length instead of content
			argStr = fmt.Sprintf("<blob:%d bytes>", len(v))
		case nil:
			argStr = "NULL"
		default:
			// For numbers, booleans, etc. use standard string conversion
			argStr = fmt.Sprintf("%v", v)
		}

		// Replace first ? with the argument
		formattedQuery = strings.Replace(formattedQuery, "?", argStr, 1)
	}

	return formattedQuery
}

// GetGuidelineEmbeddingModel returns the unified embedding model for guidelines.
func GetGuidelineEmbeddingModel() string {
	// Use the same unified model as code for consistency
	if model := os.Getenv("MAESTRO_UNIFIED_EMBEDDING_MODEL"); model != "" {
		return model
	}
	return "text-embedding-004" // Default Google Gemini embedding model (768 dimensions)
}

// logRetrievalMetrics logs detailed RAG retrieval metrics for debugging.
func (s *sqliteRAGStore) logRetrievalMetrics(ctx context.Context, contentTypes []string, results []*types.Content, similarities []float64) {
	if len(results) == 0 {
		s.log.Warn(ctx, "RAG retrieval returned no results for content types: %v", contentTypes)
		return
	}

	// Calculate similarity statistics
	var minSim, maxSim, avgSim float64
	minSim = similarities[0]
	maxSim = similarities[0]
	totalSim := 0.0

	for _, sim := range similarities {
		if sim < minSim {
			minSim = sim
		}
		if sim > maxSim {
			maxSim = sim
		}
		totalSim += sim
	}
	avgSim = totalSim / float64(len(similarities))

	s.log.Debug(ctx, "RAG Retrieval Metrics - Content Types: %v, Results: %d, Similarity Range: [%.4f - %.4f], Average: %.4f",
		contentTypes, len(results), minSim, maxSim, avgSim)

	// Log individual results for detailed debugging
	for i, result := range results {
		if i < 5 { // Limit detailed logging to top 5 results
			category := result.Metadata["category"]
			if category == "" {
				category = "unknown"
			}
			s.log.Debug(ctx, "RAG Result #%d: ID=%s, Category=%s, Similarity=%.4f, TextLength=%d",
				i+1, result.ID, category, similarities[i], len(result.Text))
		}
	}

	// Quality assessment
	qualityThreshold := 0.3 // Adjust based on your embedding model
	qualityResults := 0
	for _, sim := range similarities {
		if sim <= qualityThreshold {
			qualityResults++
		}
	}

	if qualityResults > 0 {
		s.log.Debug(ctx, "RAG Quality Alert: %d/%d results above quality threshold (%.2f)",
			qualityResults, len(similarities), qualityThreshold)
	}
}

// logDetailedRAGDebug provides comprehensive debugging information for RAG retrieval.
func (s *sqliteRAGStore) logDetailedRAGDebug(ctx context.Context, queryEmbedding []float32, contentTypes []string, results []*types.Content, similarities []float64) {
	s.log.Debug(ctx, "=== DETAILED RAG DEBUG START ===")
	s.log.Debug(ctx, "Query embedding dimensions: %d", len(queryEmbedding))
	s.log.Debug(ctx, "Query content types: %v", contentTypes)
	s.log.Debug(ctx, "Total results returned: %d", len(results))

	if len(results) == 0 {
		return
	}

	// Log embedding statistics
	var minVal, maxVal, avgVal float32
	if len(queryEmbedding) > 0 {
		minVal = queryEmbedding[0]
		maxVal = queryEmbedding[0]
		sum := float32(0)
		for _, val := range queryEmbedding {
			if val < minVal {
				minVal = val
			}
			if val > maxVal {
				maxVal = val
			}
			sum += val
		}
		avgVal = sum / float32(len(queryEmbedding))
		s.log.Debug(ctx, "Query embedding stats: min=%.4f, max=%.4f, avg=%.4f", minVal, maxVal, avgVal)
	}

	// Log top 3 results in detail
	for i := 0; i < min(3, len(results)); i++ {
		result := results[i]
		similarity := similarities[i]

		s.log.Debug(ctx, "Result #%d:", i+1)
		s.log.Debug(ctx, "  ID: %s", result.ID)
		s.log.Debug(ctx, "  Similarity: %.4f", similarity)
		s.log.Debug(ctx, "  Content Type: %s", result.Metadata["content_type"])
		s.log.Debug(ctx, "  Category: %s", result.Metadata["category"])
		s.log.Debug(ctx, "  Dimension: %s", result.Metadata["dimension"])
		s.log.Debug(ctx, "  Rule Name: %s", result.Metadata["rule_name"])
		s.log.Debug(ctx, "  Text Preview: %s", truncateString(result.Text, 100))
	}

	// Analyze similarity distribution
	if len(similarities) > 0 {
		excellentCount := 0 // < 0.2
		goodCount := 0      // 0.2-0.4
		fairCount := 0      // 0.4-0.6
		poorCount := 0      // > 0.6

		for _, sim := range similarities {
			if sim < 0.2 {
				excellentCount++
			} else if sim < 0.4 {
				goodCount++
			} else if sim < 0.6 {
				fairCount++
			} else {
				poorCount++
			}
		}

		s.log.Debug(ctx, "Similarity Distribution:")
		s.log.Debug(ctx, "  Excellent (< 0.2): %d", excellentCount)
		s.log.Debug(ctx, "  Good (0.2-0.4): %d", goodCount)
		s.log.Debug(ctx, "  Fair (0.4-0.6): %d", fairCount)
		s.log.Debug(ctx, "  Poor (> 0.6): %d", poorCount)

		if excellentCount == 0 && goodCount == 0 {
			s.log.Warn(ctx, "QUALITY WARNING: No high-quality matches found - check embedding model consistency")
		}
	}

	s.log.Debug(ctx, "=== DETAILED RAG DEBUG END ===")
}

// isRAGDebugEnabled checks if detailed RAG debugging is enabled.
func isRAGDebugEnabled() bool {
	return util.GetEnvBool("MAESTRO_RAG_DEBUG_ENABLED", false)
}

// handleDimensionMigration checks if the vector table needs to be recreated due to dimension changes.
func (s *sqliteRAGStore) handleDimensionMigration(newDims int) error {
	ctx := context.Background()

	// Check if vec_items table exists
	var tableName string
	err := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='vec_items'").Scan(&tableName)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to check table existence: %w", err)
	}

	if err == sql.ErrNoRows {
		// Table doesn't exist yet, no migration needed
		s.log.Debug(ctx, "vec_items table doesn't exist, will be created with %d dimensions", newDims)
		return nil
	}

	// Check stored dimensions in metadata
	storedDims, err := s.GetMetadata(ctx, "vector_dimensions")
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to get stored dimensions: %w", err)
	}

	var currentDims int
	if err == sql.ErrNoRows {
		// No metadata stored, assume this is a legacy database with 256 dimensions
		currentDims = 256
		s.log.Info(ctx, "No vector dimensions metadata found, assuming legacy 256 dimensions")
	} else {
		currentDims, err = strconv.Atoi(storedDims)
		if err != nil {
			s.log.Warn(ctx, "Invalid stored dimensions value '%s', assuming 256", storedDims)
			currentDims = 256
		}
	}

	// Check if dimensions have changed
	if currentDims != newDims {
		s.log.Info(ctx, "Vector dimension change detected: %d -> %d", currentDims, newDims)
		s.log.Info(ctx, "Recreating vector table and clearing stored embeddings...")

		// Drop the vector table (this will also remove all vector data)
		if _, err := s.db.Exec("DROP TABLE IF EXISTS vec_items"); err != nil {
			return fmt.Errorf("failed to drop vec_items table: %w", err)
		}

		// Clear content table since embeddings will be invalid
		if _, err := s.db.Exec("DELETE FROM contents"); err != nil {
			return fmt.Errorf("failed to clear contents table: %w", err)
		}

		// Clear rules table since guideline embeddings will be invalid
		if _, err := s.db.Exec("DELETE FROM review_rules"); err != nil {
			return fmt.Errorf("failed to clear review_rules table: %w", err)
		}

		// Update stored dimensions
		if err := s.SetMetadata(ctx, "vector_dimensions", strconv.Itoa(newDims)); err != nil {
			return fmt.Errorf("failed to update vector dimensions metadata: %w", err)
		}

		s.log.Info(ctx, "Database migration completed. Vector table will be recreated with %d dimensions", newDims)
		s.log.Info(ctx, "All embeddings have been cleared and will be regenerated on next use")
	} else {
		s.log.Debug(ctx, "Vector dimensions unchanged (%d), no migration needed", currentDims)
	}

	return nil
}

// logRAGRetrievalDebug logs comprehensive RAG retrieval debugging information.
func (s *sqliteRAGStore) logRAGRetrievalDebug(ctx context.Context, debugInfo DebugInfo, contentTypes []string, queryTime, rowProcessingTime time.Duration) {
	s.log.Debug(ctx, "=== RAG RETRIEVAL DEBUG START ===")
	s.log.Debug(ctx, "Performance Metrics:")
	s.log.Debug(ctx, "  Total Retrieval Time: %v", debugInfo.RetrievalTime)
	s.log.Debug(ctx, "  Query Execution Time: %v", queryTime)
	s.log.Debug(ctx, "  Row Processing Time: %v", rowProcessingTime)
	s.log.Debug(ctx, "  Query Embedding Dimensions: %d", debugInfo.QueryEmbeddingDims)

	s.log.Debug(ctx, "Retrieval Results:")
	s.log.Debug(ctx, "  Content Types Requested: %v", contentTypes)
	s.log.Debug(ctx, "  Results Found: %d", debugInfo.ResultCount)

	if len(debugInfo.TopMatches) > 0 {
		s.log.Debug(ctx, "Top Matches:")
		for i, match := range debugInfo.TopMatches {
			s.log.Debug(ctx, "  %d. %s", i+1, match)
		}
	}

	s.log.Debug(ctx, "Quality Assessment:")
	s.log.Debug(ctx, "  Excellent (< 0.2): %d", debugInfo.QualityMetrics.ExcellentCount)
	s.log.Debug(ctx, "  Good (0.2-0.4): %d", debugInfo.QualityMetrics.GoodCount)
	s.log.Debug(ctx, "  Fair (0.4-0.6): %d", debugInfo.QualityMetrics.FairCount)
	s.log.Debug(ctx, "  Poor (> 0.6): %d", debugInfo.QualityMetrics.PoorCount)
	s.log.Debug(ctx, "  Average Score: %.4f", debugInfo.QualityMetrics.AverageScore)
	s.log.Debug(ctx, "  Best Score: %.4f", debugInfo.QualityMetrics.BestScore)
	s.log.Debug(ctx, "  Worst Score: %.4f", debugInfo.QualityMetrics.WorstScore)

	// Quality warnings
	if debugInfo.ResultCount == 0 {
		s.log.Warn(ctx, "NO RESULTS: This indicates potential issues:")
		s.log.Warn(ctx, "  1. No guidelines stored in database")
		s.log.Warn(ctx, "  2. Embedding model mismatch between query and stored content")
		s.log.Warn(ctx, "  3. Vector similarity threshold too strict")
		s.log.Warn(ctx, "  4. Content type filter too restrictive")
	} else if debugInfo.QualityMetrics.ExcellentCount == 0 && debugInfo.QualityMetrics.GoodCount == 0 {
		s.log.Warn(ctx, "QUALITY WARNING: No high-quality matches found")
		s.log.Warn(ctx, "  Check embedding model consistency between code and guidelines")
		s.log.Warn(ctx, "  Consider expanding guideline coverage for this code pattern")
	}

	s.log.Debug(ctx, "=== RAG RETRIEVAL DEBUG END ===")
}

// calculateQualityMetrics analyzes similarity scores and provides quality assessment.
func (s *sqliteRAGStore) calculateQualityMetrics(similarities []float64) types.QualityMetrics {
	if len(similarities) == 0 {
		return types.QualityMetrics{}
	}

	metrics := types.QualityMetrics{
		BestScore:  similarities[0], // First result has best (lowest) score
		WorstScore: similarities[0],
	}

	totalScore := 0.0
	for _, score := range similarities {
		totalScore += score

		if score < metrics.BestScore {
			metrics.BestScore = score
		}
		if score > metrics.WorstScore {
			metrics.WorstScore = score
		}

		// Categorize quality levels
		if score < 0.2 {
			metrics.ExcellentCount++
		} else if score < 0.4 {
			metrics.GoodCount++
		} else if score < 0.6 {
			metrics.FairCount++
		} else {
			metrics.PoorCount++
		}
	}

	metrics.AverageScore = totalScore / float64(len(similarities))
	return metrics
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// min returns the smaller of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SplitContentForEmbedding splits content into chunks for embedding.
func SplitContentForEmbedding(content string, maxBytes int) ([]string, error) {
	if len(content) <= maxBytes {
		return []string{content}, nil
	}

	var chunks []string
	lines := strings.Split(content, "\n")
	currentChunk := strings.Builder{}

	for _, line := range lines {
		if currentChunk.Len()+len(line)+1 > maxBytes {
			// Current chunk would exceed limit, start a new one
			if currentChunk.Len() > 0 {
				chunks = append(chunks, currentChunk.String())
				currentChunk.Reset()
			}
		}
		currentChunk.WriteString(line)
		currentChunk.WriteString("\n")
	}

	// Add final chunk if not empty
	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks, nil
}
