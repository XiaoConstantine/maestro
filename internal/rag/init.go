package rag

import (
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/maestro/internal/embedding"
)

func init() {
	// Register the sgrep initializer with the embedding package.
	// This allows the embedding router to use sgrep for local embeddings
	// without creating a circular import dependency.
	embedding.RegisterSgrepInitializer(func() (core.LLM, error) {
		return NewSgrepEmbeddingLLM()
	})
}
