package main

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

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

type Content struct {
	ID        string            // Unique identifier (e.g., "file_path:chunk_1")
	Text      string            // The actual text content
	Embedding []float32         // Vector embedding of the text
	Metadata  map[string]string // Additional info like file path, line numbers
}

// DebugInfo tracks RAG retrieval metrics for debugging.
type DebugInfo struct {
	QueryEmbeddingDims int            `json:"query_embedding_dims"`
	ResultCount        int            `json:"result_count"`
	SimilarityScores   []float64      `json:"similarity_scores"`
	TopMatches         []string       `json:"top_matches"`
	RetrievalTime      time.Duration  `json:"retrieval_time"`
	QualityMetrics     QualityMetrics `json:"quality_metrics"`
}

// QualityMetrics provides analysis of retrieval quality.
type QualityMetrics struct {
	ExcellentCount int     `json:"excellent_count"` // < 0.2
	GoodCount      int     `json:"good_count"`      // 0.2-0.4
	FairCount      int     `json:"fair_count"`      // 0.4-0.6
	PoorCount      int     `json:"poor_count"`      // > 0.6
	AverageScore   float64 `json:"average_score"`
	BestScore      float64 `json:"best_score"`
	WorstScore     float64 `json:"worst_score"`
}

const (
	ContentTypeRepository = "repository"
	ContentTypeGuideline  = "guideline"
)

type RAGStore interface {
	// StoreContent saves a content piece with its embedding
	StoreContent(ctx context.Context, content *Content) error

	// FindSimilar finds the most similar content pieces to the given embedding
	FindSimilar(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*Content, error)

	// FindSimilarWithDebug finds similar content with detailed debugging information
	FindSimilarWithDebug(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*Content, DebugInfo, error)

	// FindSimilarSubmodular uses submodular optimization for better guideline selection
	FindSimilarSubmodular(ctx context.Context, embedding []float32, limit int, codeContext string, contentTypes ...string) ([]*Content, error)

	// FindSimilarWithLateInteraction combines submodular optimization with late interaction refinement
	FindSimilarWithLateInteraction(ctx context.Context, embedding []float32, limit int, codeContext, queryContext string, contentTypes ...string) ([]*Content, *RefinementResult, error)

	// UpdateContent updates an existing content piece
	UpdateContent(ctx context.Context, content *Content) error

	// DeleteContent removes content by ID
	DeleteContent(ctx context.Context, id string) error

	// Populate style guide, best practices based on repo language
	PopulateGuidelines(ctx context.Context, language string) error

	StoreRule(ctx context.Context, rule ReviewRule) error

	// HasContent checks if the database contains any indexed content
	HasContent(ctx context.Context) (bool, error)

	// DB version control
	GetMetadata(ctx context.Context, key string) (string, error)
	SetMetadata(ctx context.Context, key, value string) error

	Close() error
}

type sqliteRAGStore struct {
	db     *sql.DB
	log    *logging.Logger
	mu     sync.RWMutex
	closed bool
}

func (s *sqliteRAGStore) init() error {
	// Get configurable vector dimensions
	vectorDims := getVectorDimensions()
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

// NewSQLiteRAGStore creates a new SQLite-backed RAG store.
func NewSQLiteRAGStore(db *sql.DB, logger *logging.Logger) (RAGStore, error) {
	store := &sqliteRAGStore{
		db:     db,
		log:    logger,
		closed: false,
	}

	if err := store.init(); err != nil {
		return nil, fmt.Errorf("failed to initialize RAG store: %w", err)
	}

	return store, nil
}

// StoreContent implements RAGStore interface.
func (s *sqliteRAGStore) StoreContent(ctx context.Context, content *Content) error {
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
	compressedText, err := compressText(content.Text)
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

// populate guidelines during database initialization.
func (s *sqliteRAGStore) PopulateGuidelines(ctx context.Context, language string) error {

	s.log.Debug(ctx, "Starting guideline population for language: %s", language)

	console := NewConsole(os.Stdout, s.log, nil)
	fetcher := NewGuidelineFetcher(s.log)
	// Start with fetching guidelines
	var guidelines []GuidelineContent
	err := console.WithSpinner(ctx, "Fetching coding guidelines...", func() error {
		var err error
		fetcher := NewGuidelineFetcher(s.log)
		guidelines, err = fetcher.FetchGuidelines(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch guidelines: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	console.StartSpinner("Processing guidelines...")
	// Enhanced chunking config for guidelines with larger chunks to preserve context
	chunkConfig, err := NewChunkConfig(
		WithStrategy(ChunkBySize),
		WithMaxTokens(getGuidelineChunkSize()), // Configurable chunk size for guidelines
		WithContextLines(10),                   // More context for better understanding
		WithOverlapLines(3),                    // More overlap to preserve semantic continuity
	)
	if err != nil {
		return fmt.Errorf("failed to create chunking strategy: %w", err)
	}

	s.log.Debug(ctx, "Fetched %d guidelines", len(guidelines))

	totalChunks := 0
	// Store guidelines in the same database
	for i, guideline := range guidelines {

		progress := float64(i+1) / float64(len(guidelines)) * 100
		console.UpdateSpinnerText(fmt.Sprintf("Processing guidelines... %.1f%% (%d/%d)",
			progress, i+1, len(guidelines)))
		rule, err := fetcher.ConvertGuidelineToRules(ctx, guideline)
		if err != nil {
			s.log.Error(ctx, "failed to convert guideline to rule")
		}
		if err := s.StoreRule(ctx, rule[0]); err != nil {
			console.StopSpinner()
			return fmt.Errorf("failed to store rule: %w", err)
		}

		content := FormatRuleContent(rule[0])
		chunks, err := chunkfile(ctx, content, "", chunkConfig)
		if err != nil {
			s.log.Debug(ctx, "Failed to chunk guideline: %v, storing as single unit", err)
			// Fall back to original approach if chunking fails
			chunks = []ReviewChunk{{
				content:     content,
				startline:   1,
				endline:     len(strings.Split(content, "\n")),
				filePath:    fmt.Sprintf("guideline_%s.md", guideline.ID),
				totalChunks: 1,
			}}
		}
		for j, chunk := range chunks {
			chunkID := fmt.Sprintf("guideline_%s_chunk_%d", guideline.ID, j+1)
			metadata := map[string]string{
				"content_type": ContentTypeGuideline,
				"language":     language,
				"category":     guideline.Category,
				"dimension":    rule[0].Dimension,
				"impact":       rule[0].Metadata.Impact,
				"guideline_id": guideline.ID,
				"chunk_number": fmt.Sprintf("%d", j+1),
				"total_chunks": fmt.Sprintf("%d", len(chunks)),
				"start_line":   fmt.Sprintf("%d", chunk.startline),
				"end_line":     fmt.Sprintf("%d", chunk.endline),
				"rule_name":    rule[0].Name,
			}
			// Enhanced embedding text with context for better semantic matching
			embeddingText := s.createEnhancedGuidelineEmbedding(ctx, chunk.content, rule[0], guideline)

			// Generate embedding using unified model
			embeddingModel := getGuidelineEmbeddingModel()
			s.log.Debug(ctx, "Using unified embedding model '%s' for guideline: %s", embeddingModel, guideline.ID)
			llm := core.GetTeacherLLM()
			embedding, err := llm.CreateEmbedding(ctx, embeddingText, core.WithModel(embeddingModel))
			if err != nil {
				s.log.Warn(ctx, "Failed to create embedding: %v", err)
				continue
			}
			// Store the chunk with enhanced embedding
			err = s.StoreContent(ctx, &Content{
				ID:        chunkID,
				Text:      chunk.content,    // Original text
				Embedding: embedding.Vector, // Enhanced embedding
				Metadata:  metadata,         // Rich metadata
			})
			if err != nil {
				s.log.Warn(ctx, "Failed to store guideline chunk: %v", err)
				continue
			}

			totalChunks++
		}
	}
	console.StopSpinner()

	if console.Color() {
		console.Printf("%s %s\n",
			aurora.Green("✓").Bold(),
			aurora.White(fmt.Sprintf("Successfully processed %d guidelines", len(guidelines))).Bold(),
		)
	} else {
		console.Printf("✓ Successfully processed %d guidelines\n", len(guidelines))
	}
	s.log.Debug(ctx, "Finished fetch guidelines")
	return nil
}

// FindSimilar implements RAGStore interface.
func (s *sqliteRAGStore) FindSimilar(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*Content, error) {
	results, _, err := s.FindSimilarWithDebug(ctx, embedding, limit, contentTypes...)
	return results, err
}

// FindSimilarSubmodular implements submodular optimization for guideline selection.
func (s *sqliteRAGStore) FindSimilarSubmodular(ctx context.Context, embedding []float32, limit int, codeContext string, contentTypes ...string) ([]*Content, error) {
	// First, get a larger set of candidates using traditional similarity search
	candidateLimit := min(limit*3, 50) // Get more candidates for optimization
	candidates, _, err := s.FindSimilarWithDebug(ctx, embedding, candidateLimit, contentTypes...)
	if err != nil {
		return nil, fmt.Errorf("failed to get candidates: %w", err)
	}

	s.log.Debug(ctx, "Retrieved %d candidates for submodular optimization", len(candidates))

	if len(candidates) == 0 {
		return candidates, nil
	}

	// Skip submodular optimization if we have fewer candidates than requested
	if len(candidates) <= limit {
		s.log.Debug(ctx, "Candidate count (%d) <= limit (%d), skipping submodular optimization", len(candidates), limit)
		return candidates, nil
	}

	// Create submodular optimizer
	optimizer := NewSubmodularOptimizer(s.log)

	// Use facility location formulation for guidelines (better for relevance + diversity)
	selected, err := optimizer.SelectGuidelinesSubmodular(ctx, embedding, candidates, limit, true)
	if err != nil {
		s.log.Warn(ctx, "Submodular optimization failed, falling back to traditional selection: %v", err)
		// Fallback to traditional selection
		if len(candidates) > limit {
			return candidates[:limit], nil
		}
		return candidates, nil
	}

	s.log.Debug(ctx, "Submodular optimization selected %d guidelines from %d candidates", len(selected), len(candidates))
	return selected, nil
}

// FindSimilarWithLateInteraction implements combined submodular + late interaction.
func (s *sqliteRAGStore) FindSimilarWithLateInteraction(ctx context.Context, embedding []float32, limit int, codeContext, queryContext string, contentTypes ...string) ([]*Content, *RefinementResult, error) {
	// Get candidates using larger search
	candidateLimit := min(limit*4, 100) // Get even more candidates for late interaction
	candidates, _, err := s.FindSimilarWithDebug(ctx, embedding, candidateLimit, contentTypes...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get candidates: %w", err)
	}

	s.log.Debug(ctx, "Retrieved %d candidates for submodular + late interaction processing", len(candidates))

	if len(candidates) == 0 {
		return candidates, nil, nil
	}

	// Skip optimization if we have fewer candidates than requested
	if len(candidates) <= limit {
		s.log.Debug(ctx, "Candidate count (%d) <= limit (%d), returning candidates without optimization", len(candidates), limit)
		return candidates, nil, nil
	}

	// Create submodular optimizer and run combined processing
	optimizer := NewSubmodularOptimizer(s.log)
	selected, refinementResult, err := optimizer.ProcessGuidelinesWithLateInteraction(
		ctx, embedding, candidates, codeContext, queryContext, limit)
	if err != nil {
		s.log.Warn(ctx, "Combined optimization failed, falling back to traditional selection: %v", err)
		// Fallback to traditional selection
		if len(candidates) > limit {
			return candidates[:limit], nil, nil
		}
		return candidates, nil, nil
	}

	s.log.Debug(ctx, "Combined optimization completed: %d final guidelines selected", len(selected))
	return selected, refinementResult, nil
}

// FindSimilarWithDebug implements RAGStore interface with comprehensive debugging.
func (s *sqliteRAGStore) FindSimilarWithDebug(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*Content, DebugInfo, error) {
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
		s.log.Debug(ctx, "📊 Embedding serialization took %v for %d dimensions", embeddingSerializationTime, len(embedding))
	}

	baseQuery := `
	SELECT c.id, c.text, c.metadata, v.distance
	FROM vec_items v
	JOIN contents c ON v.content_id = c.id
	WHERE v.embedding MATCH(vec_quantize_int8(vec_f32(?), 'unit'), 100)`

	// Add content type filter if specified
	var whereClauses []string
	args := []interface{}{blob, limit} // Maintain parameter order

	if len(contentTypes) > 0 {
		placeholders := make([]string, len(contentTypes))
		for i, ct := range contentTypes {
			placeholders[i] = "?"
			args = append(args, ct)
		}
		whereClauses = append(whereClauses,
			fmt.Sprintf("c.content_type IN (%s)", strings.Join(placeholders, ",")))
	}

	// Add final k=? constraint
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
		logger.Debug(ctx, "🔍 RAG Query: %s", formatQuery(finalQuery, args))
	}

	// Execute query with performance tracking
	queryStartTime := time.Now()
	rows, err := s.db.QueryContext(ctx, finalQuery, args...)
	if err != nil {
		return nil, debuginfoObj, fmt.Errorf("failed to query similar content: %w", err)
	}
	defer rows.Close()
	queryExecutionTime := time.Since(queryStartTime)

	var results []*Content
	var similarities []float64
	var topMatches []string

	// Process results with detailed tracking
	rowProcessingStart := time.Now()
	for rows.Next() {
		var (
			content        Content
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
		decompressedText, err := decompressText(compressedText)
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
func (s *sqliteRAGStore) UpdateContent(ctx context.Context, content *Content) error {
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

func (s *sqliteRAGStore) StoreRule(ctx context.Context, rule ReviewRule) error {
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

func splitContentForEmbedding(content string, maxBytes int) ([]string, error) {
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

// by combining content with metadata context for better semantic matching.
func (s *sqliteRAGStore) createEnhancedGuidelineEmbedding(ctx context.Context, content string, rule ReviewRule, guideline GuidelineContent) string {
	// Combine guideline content with rich context for better embedding
	var embeddingText strings.Builder

	// Add category and dimension context first
	embeddingText.WriteString(fmt.Sprintf("Category: %s\n", guideline.Category))
	embeddingText.WriteString(fmt.Sprintf("Dimension: %s\n", rule.Dimension))
	embeddingText.WriteString(fmt.Sprintf("Impact: %s\n", rule.Metadata.Impact))
	embeddingText.WriteString(fmt.Sprintf("Rule: %s\n", rule.Name))
	embeddingText.WriteString("\n")

	// Add the main content
	embeddingText.WriteString("Content:\n")
	embeddingText.WriteString(content)

	// Add examples if available to improve context
	if rule.Examples.Good != "" || rule.Examples.Bad != "" {
		embeddingText.WriteString("\n\nExamples:\n")
		if rule.Examples.Good != "" {
			embeddingText.WriteString(fmt.Sprintf("Good example: %s\n", truncateString(rule.Examples.Good, 100)))
		}
		if rule.Examples.Bad != "" {
			embeddingText.WriteString(fmt.Sprintf("Bad example: %s\n", truncateString(rule.Examples.Bad, 100)))
		}
		if rule.Examples.Explanation != "" {
			embeddingText.WriteString(fmt.Sprintf("Explanation: %s\n", truncateString(rule.Examples.Explanation, 150)))
		}
	}

	// Also add examples from the original guideline if available
	if len(guideline.Examples) > 0 {
		embeddingText.WriteString("\n\nAdditional Examples:\n")
		for i, example := range guideline.Examples {
			if i >= 2 { // Limit examples to avoid too much noise
				break
			}
			if example.Good != "" {
				embeddingText.WriteString(fmt.Sprintf("- Good: %s\n", truncateString(example.Good, 80)))
			}
			if example.Bad != "" {
				embeddingText.WriteString(fmt.Sprintf("- Bad: %s\n", truncateString(example.Bad, 80)))
			}
		}
	}

	s.log.Debug(ctx, "Enhanced guideline embedding text length: %d chars for rule %s",
		embeddingText.Len(), rule.Name)

	return embeddingText.String()
}

// getGuidelineChunkSize returns the configured chunk size for guidelines.
func getGuidelineChunkSize() int {
	if value := os.Getenv("MAESTRO_GUIDELINE_CHUNK_SIZE"); value != "" {
		if size, err := strconv.Atoi(value); err == nil && size > 100 && size < 32000 {
			return size
		}
	}
	return 6000 // Default to larger chunks (about 4096 bytes) for better context preservation
}

// getGuidelineEmbeddingModel returns the unified embedding model for guidelines.
func getGuidelineEmbeddingModel() string {
	// Use the same unified model as code for consistency
	if model := os.Getenv("MAESTRO_UNIFIED_EMBEDDING_MODEL"); model != "" {
		return model
	}
	return "text-embedding-004" // Default Google Gemini embedding model (768 dimensions)
}

// logRetrievalMetrics logs detailed RAG retrieval metrics for debugging.
func (s *sqliteRAGStore) logRetrievalMetrics(ctx context.Context, contentTypes []string, results []*Content, similarities []float64) {
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
func (s *sqliteRAGStore) logDetailedRAGDebug(ctx context.Context, queryEmbedding []float32, contentTypes []string, results []*Content, similarities []float64) {
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

		s.log.Debug(ctx, "📋 Result #%d:", i+1)
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

		s.log.Debug(ctx, "📊 Similarity Distribution:")
		s.log.Debug(ctx, "  Excellent (< 0.2): %d", excellentCount)
		s.log.Debug(ctx, "  Good (0.2-0.4): %d", goodCount)
		s.log.Debug(ctx, "  Fair (0.4-0.6): %d", fairCount)
		s.log.Debug(ctx, "  Poor (> 0.6): %d", poorCount)

		if excellentCount == 0 && goodCount == 0 {
			s.log.Warn(ctx, "⚠️  QUALITY WARNING: No high-quality matches found - check embedding model consistency")
		}
	}

	s.log.Debug(ctx, "=== DETAILED RAG DEBUG END ===")
}

// isRAGDebugEnabled checks if detailed RAG debugging is enabled.
func isRAGDebugEnabled() bool {
	return getEnvBool("MAESTRO_RAG_DEBUG_ENABLED", false)
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
		s.log.Info(ctx, "🔄 Vector dimension change detected: %d → %d", currentDims, newDims)
		s.log.Info(ctx, "🗑️  Recreating vector table and clearing stored embeddings...")

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

		s.log.Info(ctx, "✅ Database migration completed. Vector table will be recreated with %d dimensions", newDims)
		s.log.Info(ctx, "ℹ️  All embeddings have been cleared and will be regenerated on next use")
	} else {
		s.log.Debug(ctx, "Vector dimensions unchanged (%d), no migration needed", currentDims)
	}

	return nil
}

// logRAGRetrievalDebug logs comprehensive RAG retrieval debugging information.
func (s *sqliteRAGStore) logRAGRetrievalDebug(ctx context.Context, debugInfo DebugInfo, contentTypes []string, queryTime, rowProcessingTime time.Duration) {
	s.log.Debug(ctx, "🔍 === RAG RETRIEVAL DEBUG START ===")
	s.log.Debug(ctx, "📊 Performance Metrics:")
	s.log.Debug(ctx, "  • Total Retrieval Time: %v", debugInfo.RetrievalTime)
	s.log.Debug(ctx, "  • Query Execution Time: %v", queryTime)
	s.log.Debug(ctx, "  • Row Processing Time: %v", rowProcessingTime)
	s.log.Debug(ctx, "  • Query Embedding Dimensions: %d", debugInfo.QueryEmbeddingDims)

	s.log.Debug(ctx, "🎯 Retrieval Results:")
	s.log.Debug(ctx, "  • Content Types Requested: %v", contentTypes)
	s.log.Debug(ctx, "  • Results Found: %d", debugInfo.ResultCount)

	if len(debugInfo.TopMatches) > 0 {
		s.log.Debug(ctx, "🏆 Top Matches:")
		for i, match := range debugInfo.TopMatches {
			s.log.Debug(ctx, "  %d. %s", i+1, match)
		}
	}

	s.log.Debug(ctx, "📈 Quality Assessment:")
	s.log.Debug(ctx, "  • Excellent (< 0.2): %d", debugInfo.QualityMetrics.ExcellentCount)
	s.log.Debug(ctx, "  • Good (0.2-0.4): %d", debugInfo.QualityMetrics.GoodCount)
	s.log.Debug(ctx, "  • Fair (0.4-0.6): %d", debugInfo.QualityMetrics.FairCount)
	s.log.Debug(ctx, "  • Poor (> 0.6): %d", debugInfo.QualityMetrics.PoorCount)
	s.log.Debug(ctx, "  • Average Score: %.4f", debugInfo.QualityMetrics.AverageScore)
	s.log.Debug(ctx, "  • Best Score: %.4f", debugInfo.QualityMetrics.BestScore)
	s.log.Debug(ctx, "  • Worst Score: %.4f", debugInfo.QualityMetrics.WorstScore)

	// Quality warnings
	if debugInfo.ResultCount == 0 {
		s.log.Warn(ctx, "❌ NO RESULTS: This indicates potential issues:")
		s.log.Warn(ctx, "  1. No guidelines stored in database")
		s.log.Warn(ctx, "  2. Embedding model mismatch between query and stored content")
		s.log.Warn(ctx, "  3. Vector similarity threshold too strict")
		s.log.Warn(ctx, "  4. Content type filter too restrictive")
	} else if debugInfo.QualityMetrics.ExcellentCount == 0 && debugInfo.QualityMetrics.GoodCount == 0 {
		s.log.Warn(ctx, "⚠️  QUALITY WARNING: No high-quality matches found")
		s.log.Warn(ctx, "  • Check embedding model consistency between code and guidelines")
		s.log.Warn(ctx, "  • Consider expanding guideline coverage for this code pattern")
	}

	s.log.Debug(ctx, "🔍 === RAG RETRIEVAL DEBUG END ===")
}

// calculateQualityMetrics analyzes similarity scores and provides quality assessment.
func (s *sqliteRAGStore) calculateQualityMetrics(similarities []float64) QualityMetrics {
	if len(similarities) == 0 {
		return QualityMetrics{}
	}

	metrics := QualityMetrics{
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

// logGuidelineMatchAnalysis provides detailed analysis of guideline matching for specific code patterns.
func (s *sqliteRAGStore) logGuidelineMatchAnalysis(ctx context.Context, codePattern string, matches []*Content, debugInfo DebugInfo) {
	if !isRAGDebugEnabled() {
		return
	}

	s.log.Debug(ctx, "🎯 === GUIDELINE MATCH ANALYSIS ===")
	s.log.Debug(ctx, "📝 Code Pattern: %s", truncateString(codePattern, 80))

	if len(matches) == 0 {
		s.log.Warn(ctx, "❌ No guidelines matched this code pattern")
		s.log.Debug(ctx, "🎯 === GUIDELINE MATCH ANALYSIS END ===")
		return
	}

	s.log.Debug(ctx, "📊 Match Analysis:")
	categoryCount := make(map[string]int)
	dimensionCount := make(map[string]int)

	for i, match := range matches {
		if i >= 5 { // Limit detailed analysis to top 5
			break
		}

		category := match.Metadata["category"]
		dimension := match.Metadata["dimension"]
		ruleName := match.Metadata["rule_name"]
		similarity := debugInfo.SimilarityScores[i]

		categoryCount[category]++
		dimensionCount[dimension]++

		s.log.Debug(ctx, "  Match #%d:", i+1)
		s.log.Debug(ctx, "    • Rule: %s", ruleName)
		s.log.Debug(ctx, "    • Category: %s", category)
		s.log.Debug(ctx, "    • Dimension: %s", dimension)
		s.log.Debug(ctx, "    • Similarity: %.4f", similarity)
		s.log.Debug(ctx, "    • Content Preview: %s", truncateString(match.Text, 60))
	}

	s.log.Debug(ctx, "📈 Pattern Summary:")
	s.log.Debug(ctx, "  • Categories: %v", categoryCount)
	s.log.Debug(ctx, "  • Dimensions: %v", dimensionCount)
	s.log.Debug(ctx, "  • Best Match Quality: %.4f", debugInfo.QualityMetrics.BestScore)

	s.log.Debug(ctx, "🎯 === GUIDELINE MATCH ANALYSIS END ===")
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
