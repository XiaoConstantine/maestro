package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

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

const (
	ContentTypeRepository = "repository"
	ContentTypeGuideline  = "guideline"
)

type RAGStore interface {
	// StoreContent saves a content piece with its embedding
	StoreContent(ctx context.Context, content *Content) error

	// FindSimilar finds the most similar content pieces to the given embedding
	FindSimilar(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*Content, error)

	// UpdateContent updates an existing content piece
	UpdateContent(ctx context.Context, content *Content) error

	// DeleteContent removes content by ID
	DeleteContent(ctx context.Context, id string) error

	// Populate style guide, best practices based on repo language
	PopulateGuidelines(ctx context.Context, language string) error

	StoreRule(ctx context.Context, rule ReviewRule) error

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

		// Vector virtual table (REQUIRED for sqlite-vec)
		`CREATE VIRTUAL TABLE IF NOT EXISTS vec_items USING vec0(
		rowid INTEGER PRIMARY KEY,
		embedding int8[256] distance_metric=l2,  -- Match your embedding dimensions
		content_id TEXT PARTITION KEY  // Optimizes WHERE clause filtering
		)`,

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
     VALUES (vec_quantize_int8(vec_slice(vec_f32(?), 0, 256), 'unit'), ?)`, // Directly use serialized blob
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
	chunkConfig, err := NewChunkConfig(WithStrategy(ChunkBySize))
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
			embeddingText := chunk.content
			// Generate embedding using combined text
			llm := core.GetTeacherLLM()
			embedding, err := llm.CreateEmbedding(ctx, embeddingText)
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
	logger := s.log
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, fmt.Errorf("store is closed")
	}

	// First add the common arguments
	blob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize embedding: %w", err)
	}

	baseQuery := `
	SELECT c.id, c.text, c.metadata, v.distance
	FROM vec_items v
	JOIN contents c ON v.content_id = c.id
	WHERE v.embedding MATCH(vec_quantize_int8(vec_slice(vec_f32(?), 0, 256), 'unit'), 100)`

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
	logger.Debug(ctx, "final query: %s", formatQuery(finalQuery, args))
	rows, err := s.db.QueryContext(ctx, finalQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query similar content: %w", err)
	}
	defer rows.Close()

	var results []*Content
	for rows.Next() {
		var (
			content        Content
			compressedText string
			metadataStr    string
			similarity     float64
		)

		if err := rows.Scan(&content.ID, &compressedText, &metadataStr, &similarity); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		// Decompress the text
		decompressedText, err := decompressText(compressedText)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress content: %w", err)
		}

		content.Text = decompressedText

		// Parse metadata JSON
		if err := json.Unmarshal([]byte(metadataStr), &content.Metadata); err != nil {

			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}

		results = append(results, &content)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating results: %w", err)
	}

	s.log.Debug(ctx, "Found %d similar contents in content_type: %s", len(results), contentTypes)

	return results, nil
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
