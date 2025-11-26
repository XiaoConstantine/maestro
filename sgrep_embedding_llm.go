package main

import (
	"context"
	"fmt"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/sgrep/pkg/embed"
)

// SgrepEmbeddingLLM wraps sgrep's Embedder to implement core.LLM interface.
// Only embedding methods are functional; other methods return errors.
type SgrepEmbeddingLLM struct {
	embedder *embed.Embedder
}

// NewSgrepEmbeddingLLM creates a new sgrep-based embedding LLM.
// The embedder auto-starts llama-server if needed.
func NewSgrepEmbeddingLLM() (*SgrepEmbeddingLLM, error) {
	return &SgrepEmbeddingLLM{
		embedder: embed.New(),
	}, nil
}

// CreateEmbedding generates an embedding using sgrep's local llama-server.
func (s *SgrepEmbeddingLLM) CreateEmbedding(ctx context.Context, input string, opts ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	vec, err := s.embedder.Embed(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("sgrep embedding failed: %w", err)
	}
	return &core.EmbeddingResult{Vector: vec}, nil
}

// CreateEmbeddings generates batch embeddings using sgrep's local llama-server.
func (s *SgrepEmbeddingLLM) CreateEmbeddings(ctx context.Context, inputs []string, opts ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	vecs, err := s.embedder.EmbedBatch(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("sgrep batch embedding failed: %w", err)
	}

	results := make([]core.EmbeddingResult, len(vecs))
	for i, vec := range vecs {
		results[i] = core.EmbeddingResult{Vector: vec}
	}
	return &core.BatchEmbeddingResult{Embeddings: results}, nil
}

// Generate is not supported - sgrep only provides embeddings.
func (s *SgrepEmbeddingLLM) Generate(ctx context.Context, prompt string, opts ...core.GenerateOption) (*core.LLMResponse, error) {
	return nil, fmt.Errorf("SgrepEmbeddingLLM only supports embeddings, not text generation")
}

// GenerateWithJSON is not supported.
func (s *SgrepEmbeddingLLM) GenerateWithJSON(ctx context.Context, prompt string, opts ...core.GenerateOption) (map[string]interface{}, error) {
	return nil, fmt.Errorf("SgrepEmbeddingLLM only supports embeddings")
}

// GenerateWithFunctions is not supported.
func (s *SgrepEmbeddingLLM) GenerateWithFunctions(ctx context.Context, prompt string, functions []map[string]interface{}, opts ...core.GenerateOption) (map[string]interface{}, error) {
	return nil, fmt.Errorf("SgrepEmbeddingLLM only supports embeddings")
}

// StreamGenerate is not supported.
func (s *SgrepEmbeddingLLM) StreamGenerate(ctx context.Context, prompt string, opts ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("SgrepEmbeddingLLM only supports embeddings")
}

// GenerateWithContent is not supported.
func (s *SgrepEmbeddingLLM) GenerateWithContent(ctx context.Context, content []core.ContentBlock, opts ...core.GenerateOption) (*core.LLMResponse, error) {
	return nil, fmt.Errorf("SgrepEmbeddingLLM only supports embeddings")
}

// StreamGenerateWithContent is not supported.
func (s *SgrepEmbeddingLLM) StreamGenerateWithContent(ctx context.Context, content []core.ContentBlock, opts ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, fmt.Errorf("SgrepEmbeddingLLM only supports embeddings")
}

// ProviderName returns the provider identifier.
func (s *SgrepEmbeddingLLM) ProviderName() string {
	return "sgrep"
}

// ModelID returns the model identifier.
func (s *SgrepEmbeddingLLM) ModelID() string {
	return "nomic-embed-text"
}

// Capabilities returns supported capabilities.
func (s *SgrepEmbeddingLLM) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityEmbedding}
}
