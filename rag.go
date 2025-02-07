package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

type Content struct {
	ID        string            // Unique identifier (e.g., "file_path:chunk_1")
	Text      string            // The actual text content
	Embedding []float32         // Vector embedding of the text
	Metadata  map[string]string // Additional info like file path, line numbers
}

type RAGStore interface {
	// StoreContent saves a content piece with its embedding
	StoreContent(ctx context.Context, content *Content) error

	// FindSimilar finds the most similar content pieces to the given embedding
	FindSimilar(ctx context.Context, embedding []float32, limit int) ([]*Content, error)

	// UpdateContent updates an existing content piece
	UpdateContent(ctx context.Context, content *Content) error

	// DeleteContent removes content by ID
	DeleteContent(ctx context.Context, id string) error

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
		`CREATE TABLE IF NOT EXISTS contents (
            id TEXT PRIMARY KEY,
            text TEXT NOT NULL,
            embedding BLOB NOT NULL, -- Store embeddings as serialized bytes
            metadata TEXT, -- JSON encoded metadata
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )`,
		// Index for faster lookups
		`CREATE INDEX IF NOT EXISTS idx_contents_created ON contents(created_at)`,
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

// serializeEmbedding converts a float32 slice to bytes for storage.
func serializeEmbedding(embedding []float32) ([]byte, error) {
	buf := new(bytes.Buffer)
	for _, f := range embedding {
		err := binary.Write(buf, binary.LittleEndian, f)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize embedding: %w", err)
		}
	}
	return buf.Bytes(), nil
}

// deserializeEmbedding converts stored bytes back to float32 slice.
func deserializeEmbedding(data []byte) ([]float32, error) {
	embedding := make([]float32, len(data)/4)
	buf := bytes.NewReader(data)
	err := binary.Read(buf, binary.LittleEndian, embedding)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize embedding: %w", err)
	}
	return embedding, nil
}

// StoreContent implements RAGStore interface.
func (s *sqliteRAGStore) StoreContent(ctx context.Context, content *Content) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("store is closed")
	}

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

	// Serialize embedding
	embedding, err := serializeEmbedding(content.Embedding)
	if err != nil {
		return err
	}

	// Convert metadata to JSON
	metadata, err := json.Marshal(content.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Create vector from embedding using sqlite-vec
	_, err = tx.ExecContext(ctx,
		`WITH vector AS (
            SELECT vec_from_raw(?, 'float32') as vec
        )
        INSERT OR REPLACE INTO contents (id, text, embedding, metadata)
        SELECT ?, ?, vec, ?
        FROM vector`,
		embedding, content.ID, content.Text, string(metadata))

	if err != nil {
		return fmt.Errorf("failed to store content: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.log.Debug(ctx, "Stored content with ID: %s", content.ID)
	return nil
}

// FindSimilar implements RAGStore interface.
func (s *sqliteRAGStore) FindSimilar(ctx context.Context, embedding []float32, limit int) ([]*Content, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, fmt.Errorf("store is closed")
	}

	// Serialize query embedding
	queryEmbedding, err := serializeEmbedding(embedding)
	if err != nil {
		return nil, err
	}

	// Query using cosine similarity with sqlite-vec
	rows, err := s.db.QueryContext(ctx,
		`WITH query AS (
            SELECT vec_from_raw(?, 'float32') as vec
        )
        SELECT c.id, c.text, c.embedding, c.metadata,
               vec_cosine_similarity(c.embedding, query.vec) as similarity
        FROM contents c, query
        ORDER BY similarity DESC
        LIMIT ?`,
		queryEmbedding, limit)

	if err != nil {
		return nil, fmt.Errorf("failed to query similar content: %w", err)
	}
	defer rows.Close()

	var results []*Content
	for rows.Next() {
		var (
			content        Content
			embeddingBytes []byte
			metadataStr    string
			similarity     float64
		)

		if err := rows.Scan(&content.ID, &content.Text, &embeddingBytes, &metadataStr, &similarity); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Deserialize embedding
		content.Embedding, err = deserializeEmbedding(embeddingBytes)
		if err != nil {
			return nil, err
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

	s.log.Debug(ctx, "Found %d similar contents", len(results))
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
