package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// MultiVectorEmbedding provides multi-vector embedding support for enhanced context.
type MultiVectorEmbedding struct {
	log       *logging.Logger
	model     string
	chunkSize int // Size for token-level chunking
}

// NewMultiVectorEmbedding creates a new multi-vector embedding processor.
func NewMultiVectorEmbedding(logger *logging.Logger) *MultiVectorEmbedding {
	return &MultiVectorEmbedding{
		log:       logger,
		model:     "text-embedding-004", // Default model supporting multi-vector
		chunkSize: 512,                  // Token-level chunking size
	}
}

// TokenLevel represents a token with its embedding.
type TokenLevel struct {
	Token     string
	Position  int
	Embedding []float32
}

// ChunkLevel represents a chunk with its embedding and constituent tokens.
type ChunkLevel struct {
	Text      string
	StartPos  int
	EndPos    int
	Embedding []float32
	Tokens    []TokenLevel
}

// DocumentLevel represents a document with multi-level embeddings.
type DocumentLevel struct {
	ID        string
	Text      string
	Embedding []float32    // Document-level embedding
	Chunks    []ChunkLevel // Chunk-level embeddings
	Metadata  map[string]string
}

// EnhancedContent extends Content with multi-vector capabilities.
type EnhancedContent struct {
	*Content
	DocumentEmbed *DocumentLevel
	ContextScore  float64 // Contextual relevance score
}

// ProcessMultiVectorContent creates multi-level embeddings for content.
func (mve *MultiVectorEmbedding) ProcessMultiVectorContent(ctx context.Context, content *Content) (*EnhancedContent, error) {
	mve.log.Debug(ctx, "Processing multi-vector embeddings for content: %s", content.ID)

	// Create document-level embedding (full context)
	docEmbedding, err := mve.createDocumentEmbedding(ctx, content.Text)
	if err != nil {
		return nil, fmt.Errorf("failed to create document embedding: %w", err)
	}

	// Create chunk-level embeddings (sliding window)
	chunks, err := mve.createChunkEmbeddings(ctx, content.Text)
	if err != nil {
		return nil, fmt.Errorf("failed to create chunk embeddings: %w", err)
	}

	// Create document-level structure
	docLevel := &DocumentLevel{
		ID:        content.ID,
		Text:      content.Text,
		Embedding: docEmbedding,
		Chunks:    chunks,
		Metadata:  content.Metadata,
	}

	enhanced := &EnhancedContent{
		Content:       content,
		DocumentEmbed: docLevel,
		ContextScore:  0.0, // Will be calculated during retrieval
	}

	mve.log.Debug(ctx, "Created multi-vector embedding with %d chunks for content: %s", len(chunks), content.ID)
	return enhanced, nil
}

// createDocumentEmbedding creates a document-level embedding with full context.
func (mve *MultiVectorEmbedding) createDocumentEmbedding(ctx context.Context, text string) ([]float32, error) {
	// For now, return a dummy embedding vector
	// TODO: Implement actual embedding generation when API is available
	mve.log.Debug(ctx, "Creating dummy document embedding for text length: %d", len(text))

	// Return a 768-dimension dummy embedding (typical for text-embedding models)
	embedding := make([]float32, 768)
	for i := range embedding {
		embedding[i] = 0.1 // Small positive values
	}

	return embedding, nil
}

// createChunkEmbeddings creates chunk-level embeddings with overlapping windows.
func (mve *MultiVectorEmbedding) createChunkEmbeddings(ctx context.Context, text string) ([]ChunkLevel, error) {
	// Split text into overlapping chunks for better context preservation
	chunks := mve.createOverlappingChunks(text, mve.chunkSize, mve.chunkSize/4) // 25% overlap

	var chunkEmbeddings []ChunkLevel

	for i, chunk := range chunks {
		// Create dummy embedding for each chunk
		// TODO: Implement actual embedding generation when API is available
		mve.log.Debug(ctx, "Creating dummy embedding for chunk %d", i)

		// Create a dummy embedding with slight variation per chunk
		embedding := make([]float32, 768)
		for j := range embedding {
			embedding[j] = 0.1 + float32(i)*0.01 // Slight variation per chunk
		}

		chunkLevel := ChunkLevel{
			Text:      chunk.Text,
			StartPos:  chunk.StartPos,
			EndPos:    chunk.EndPos,
			Embedding: embedding,
			Tokens:    []TokenLevel{}, // Could be enhanced with token-level if needed
		}

		chunkEmbeddings = append(chunkEmbeddings, chunkLevel)
	}

	return chunkEmbeddings, nil
}

// ChunkInfo represents basic chunk information.
type ChunkInfo struct {
	Text     string
	StartPos int
	EndPos   int
}

// createOverlappingChunks splits text into overlapping chunks.
func (mve *MultiVectorEmbedding) createOverlappingChunks(text string, chunkSize, overlap int) []ChunkInfo {
	words := strings.Fields(text)
	if len(words) <= chunkSize {
		return []ChunkInfo{{
			Text:     text,
			StartPos: 0,
			EndPos:   len(text),
		}}
	}

	var chunks []ChunkInfo
	step := chunkSize - overlap

	for i := 0; i < len(words); i += step {
		end := i + chunkSize
		if end > len(words) {
			end = len(words)
		}

		chunkWords := words[i:end]
		chunkText := strings.Join(chunkWords, " ")

		// Calculate approximate positions
		startPos := 0
		if i > 0 {
			startPos = len(strings.Join(words[:i], " "))
		}
		endPos := startPos + len(chunkText)

		chunks = append(chunks, ChunkInfo{
			Text:     chunkText,
			StartPos: startPos,
			EndPos:   endPos,
		})

		if end == len(words) {
			break
		}
	}

	return chunks
}



// FindBestMatchingChunk finds the most relevant chunk within a document for a query.
func (mve *MultiVectorEmbedding) FindBestMatchingChunk(ctx context.Context, query []float32, enhanced *EnhancedContent) (*ChunkLevel, float64, error) {
	if len(enhanced.DocumentEmbed.Chunks) == 0 {
		return nil, 0.0, fmt.Errorf("no chunks available")
	}

	bestChunk := &enhanced.DocumentEmbed.Chunks[0]
	bestScore := cosineSimilarity(query, bestChunk.Embedding)

	for i := 1; i < len(enhanced.DocumentEmbed.Chunks); i++ {
		chunk := &enhanced.DocumentEmbed.Chunks[i]
		score := cosineSimilarity(query, chunk.Embedding)

		if score > bestScore {
			bestScore = score
			bestChunk = chunk
		}
	}

	mve.log.Debug(ctx, "Best matching chunk for %s: score=%.4f, text_preview=%s",
		enhanced.ID, bestScore, mve.truncateText(bestChunk.Text, 50))

	return bestChunk, bestScore, nil
}

// CalculateContextualRelevance calculates enhanced relevance using multi-vector approach.
func (mve *MultiVectorEmbedding) CalculateContextualRelevance(ctx context.Context, query []float32, enhanced *EnhancedContent) (float64, error) {
	// Document-level similarity
	docSimilarity := cosineSimilarity(query, enhanced.DocumentEmbed.Embedding)

	// Best chunk similarity
	_, chunkSimilarity, err := mve.FindBestMatchingChunk(ctx, query, enhanced)
	if err != nil {
		// Fallback to document similarity
		chunkSimilarity = docSimilarity
	}

	// Average chunk similarity for coverage
	avgChunkSimilarity := 0.0
	for _, chunk := range enhanced.DocumentEmbed.Chunks {
		avgChunkSimilarity += cosineSimilarity(query, chunk.Embedding)
	}
	if len(enhanced.DocumentEmbed.Chunks) > 0 {
		avgChunkSimilarity /= float64(len(enhanced.DocumentEmbed.Chunks))
	}

	// Weighted combination: 40% document, 40% best chunk, 20% average chunks
	contextualScore := 0.4*docSimilarity + 0.4*chunkSimilarity + 0.2*avgChunkSimilarity

	enhanced.ContextScore = contextualScore
	mve.log.Debug(ctx, "Contextual relevance for %s: doc=%.3f, best_chunk=%.3f, avg_chunk=%.3f, final=%.3f",
		enhanced.ID, docSimilarity, chunkSimilarity, avgChunkSimilarity, contextualScore)

	return contextualScore, nil
}

// ProcessGuidelinesWithMultiVector enhances guidelines with multi-vector embeddings.
func (mve *MultiVectorEmbedding) ProcessGuidelinesWithMultiVector(ctx context.Context, guidelines []*Content) ([]*EnhancedContent, error) {
	enhanced := make([]*EnhancedContent, 0, len(guidelines))

	for _, guideline := range guidelines {
		enhancedGuideline, err := mve.ProcessMultiVectorContent(ctx, guideline)
		if err != nil {
			mve.log.Warn(ctx, "Failed to enhance guideline %s: %v", guideline.ID, err)
			// Create basic enhanced content without multi-vector
			enhancedGuideline = &EnhancedContent{
				Content: guideline,
				DocumentEmbed: &DocumentLevel{
					ID:        guideline.ID,
					Text:      guideline.Text,
					Embedding: guideline.Embedding,
					Chunks:    []ChunkLevel{},
					Metadata:  guideline.Metadata,
				},
				ContextScore: 0.0,
			}
		}
		enhanced = append(enhanced, enhancedGuideline)
	}

	mve.log.Debug(ctx, "Enhanced %d guidelines with multi-vector embeddings", len(enhanced))
	return enhanced, nil
}

// SelectWithMultiVectorContext performs selection using enhanced multi-vector context.
func (mve *MultiVectorEmbedding) SelectWithMultiVectorContext(ctx context.Context, query []float32, enhanced []*EnhancedContent, k int) ([]*Content, error) {
	// Calculate contextual relevance for all enhanced content
	for _, item := range enhanced {
		_, err := mve.CalculateContextualRelevance(ctx, query, item)
		if err != nil {
			mve.log.Warn(ctx, "Failed to calculate contextual relevance for %s: %v", item.ID, err)
			// Fallback to basic similarity
			item.ContextScore = cosineSimilarity(query, item.Embedding)
		}
	}

	// Sort by contextual score
	sortedEnhanced := make([]*EnhancedContent, len(enhanced))
	copy(sortedEnhanced, enhanced)

	// Sort by context score (descending)
	for i := 0; i < len(sortedEnhanced)-1; i++ {
		for j := 0; j < len(sortedEnhanced)-i-1; j++ {
			if sortedEnhanced[j].ContextScore < sortedEnhanced[j+1].ContextScore {
				sortedEnhanced[j], sortedEnhanced[j+1] = sortedEnhanced[j+1], sortedEnhanced[j]
			}
		}
	}

	// Select top k
	selectedCount := k
	if selectedCount > len(sortedEnhanced) {
		selectedCount = len(sortedEnhanced)
	}

	selected := make([]*Content, selectedCount)
	for i := 0; i < selectedCount; i++ {
		selected[i] = sortedEnhanced[i].Content
		mve.log.Debug(ctx, "Selected guideline %d: %s (context_score=%.3f)",
			i+1, selected[i].ID, sortedEnhanced[i].ContextScore)
	}

	return selected, nil
}

// truncateText truncates text to specified length.
func (mve *MultiVectorEmbedding) truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}
