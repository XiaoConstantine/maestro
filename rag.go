package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"

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
		text TEXT NOT NULL,
		metadata TEXT,
		content_type TEXT, -- 'repository' or 'guideline',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Vector virtual table (REQUIRED for sqlite-vec)
		`CREATE VIRTUAL TABLE IF NOT EXISTS vec_items USING vec0(
		rowid INTEGER PRIMARY KEY,
		embedding float[768] distance_metric=cosine,  -- Match your embedding dimensions
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
func NewSQLiteRAGStore(db *sql.DB, logger *logging.Logger) (*sqliteRAGStore, error) {
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

	s.log.Debug(ctx, "Executing SQLite insert/replace for content ID: %s", content.ID)

	_, err = tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO contents (id, text, metadata, content_type)
     VALUES (?, ?, ?, ?)`,
		content.ID, content.Text, string(metadata), contentType)
	if err != nil {
		return fmt.Errorf("failed to store content metadata: %w", err)
	}

	blob, err := sqlite_vec.SerializeFloat32(content.Embedding)
	if err != nil {
		return fmt.Errorf("failed to serialize embedding: %w", err)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO vec_items (embedding, content_id)
     VALUES (?, ?)`, // Directly use serialized blob
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

	s.log.Info(ctx, "Starting guideline population for language: %s", language)
	fetcher := NewGuidelineFetcher(s.log)

	// Fetch guidelines for the specified language
	guidelines, err := fetcher.FetchGuidelines(ctx)
	if err != nil {
		s.log.Info(ctx, "failed to fetch gl: %v", err)
		return fmt.Errorf("failed to fetch guidelines: %w", err)
	}

	s.log.Info(ctx, "Fetched %d guidelines", len(guidelines))
	// Store guidelines in the same database
	for _, guideline := range guidelines {
		// Generate embedding for the guideline
		llm := core.GetDefaultLLM()
		rule, err := fetcher.ConvertGuidelineToRules(ctx, guideline)
		if err != nil {
			s.log.Error(ctx, "failed to convert guideline to rule")
		}
		if err := s.StoreRule(ctx, rule[0]); err != nil {
			return fmt.Errorf("failed to store rule: %w", err)
		}

		content := FormatRuleContent(rule[0])
		embedding, err := llm.CreateEmbedding(ctx, content)
		if err != nil {
			return fmt.Errorf("failed to create embedding: %w", err)
		}

		// Store in the database with content_type = 'guideline'
		err = s.StoreContent(ctx, &Content{
			ID:        "guideline_" + guideline.ID,
			Text:      content,
			Embedding: embedding.Vector,
			Metadata: map[string]string{
				"content_type": ContentTypeGuideline,
				"language":     language,
				"category":     guideline.Category,
				"dimension":    rule[0].Dimension,
				"impact":       rule[0].Metadata.Impact,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to store guideline: %w", err)
		}
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
	SELECT c.id, c.text, c.metadata,
	vec_distance_cosine(v.embedding, ?) as distance
	FROM vec_items v
	JOIN contents c ON v.content_id = c.id
	WHERE v.embedding MATCH(?, 0.8)`

	// Add content type filter if specified
	var whereClauses []string
	args := []interface{}{blob, blob} // Maintain parameter order

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
			content     Content
			metadataStr string
			similarity  float64
		)

		if err := rows.Scan(&content.ID, &content.Text, &metadataStr, &similarity); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

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
