package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"

	_ "github.com/mattn/go-sqlite3"

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
		// Original metadata table
		`CREATE TABLE IF NOT EXISTS contents (
		id TEXT PRIMARY KEY,
		text TEXT NOT NULL,
		metadata TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Vector virtual table (REQUIRED for sqlite-vec)
		`CREATE VIRTUAL TABLE IF NOT EXISTS vec_items USING vec0(
		rowid INTEGER PRIMARY KEY,
		embedding float[768] distance_metric=cosine,  -- Match your embedding dimensions
		content_id TEXT PARTITION KEY  // Optimizes WHERE clause filtering
		)`,
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
	s.log.Debug(ctx, "Starting StoreContent for ID: %s", content.ID)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("store is closed")
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
		`INSERT OR REPLACE INTO contents (id, text, metadata)
     VALUES (?, ?, ?)`,
		content.ID, content.Text, string(metadata))

	blob, err := sqlite_vec.SerializeFloat32(content.Embedding)
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

	blob, err := sqlite_vec.SerializeFloat32(embedding)
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.text, c.metadata, 
            vec_distance_cosine(v.embedding, ?) as distance
     FROM vec_items v
     JOIN contents c ON v.content_id = c.id
     WHERE v.embedding MATCH ?  AND k = ?
     ORDER BY distance ASC`,
		blob, blob, limit)
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

	s.log.Info(ctx, "Found %d similar contents", len(results))

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
