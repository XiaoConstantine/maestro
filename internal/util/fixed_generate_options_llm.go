package util

import (
	"context"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

// FixedGenerateOptionsLLM wraps an LLM and appends fixed generation options to
// every generation call. Embedding methods pass through unchanged.
type FixedGenerateOptionsLLM struct {
	base    core.LLM
	options []core.GenerateOption
}

func NewFixedGenerateOptionsLLM(base core.LLM, options ...core.GenerateOption) core.LLM {
	if base == nil {
		return nil
	}
	if len(options) == 0 {
		return base
	}
	return &FixedGenerateOptionsLLM{
		base:    base,
		options: append([]core.GenerateOption(nil), options...),
	}
}

func (l *FixedGenerateOptionsLLM) merged(options []core.GenerateOption) []core.GenerateOption {
	merged := make([]core.GenerateOption, 0, len(options)+len(l.options))
	merged = append(merged, options...)
	merged = append(merged, l.options...)
	return merged
}

func (l *FixedGenerateOptionsLLM) Generate(ctx context.Context, prompt string, options ...core.GenerateOption) (*core.LLMResponse, error) {
	return l.base.Generate(ctx, prompt, l.merged(options)...)
}

func (l *FixedGenerateOptionsLLM) GenerateWithJSON(ctx context.Context, prompt string, options ...core.GenerateOption) (map[string]any, error) {
	return l.base.GenerateWithJSON(ctx, prompt, l.merged(options)...)
}

func (l *FixedGenerateOptionsLLM) GenerateWithFunctions(ctx context.Context, prompt string, functions []map[string]any, options ...core.GenerateOption) (map[string]any, error) {
	return l.base.GenerateWithFunctions(ctx, prompt, functions, l.merged(options)...)
}

func (l *FixedGenerateOptionsLLM) CreateEmbedding(ctx context.Context, input string, options ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	return l.base.CreateEmbedding(ctx, input, options...)
}

func (l *FixedGenerateOptionsLLM) CreateEmbeddings(ctx context.Context, inputs []string, options ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	return l.base.CreateEmbeddings(ctx, inputs, options...)
}

func (l *FixedGenerateOptionsLLM) StreamGenerate(ctx context.Context, prompt string, options ...core.GenerateOption) (*core.StreamResponse, error) {
	return l.base.StreamGenerate(ctx, prompt, l.merged(options)...)
}

func (l *FixedGenerateOptionsLLM) GenerateWithContent(ctx context.Context, content []core.ContentBlock, options ...core.GenerateOption) (*core.LLMResponse, error) {
	return l.base.GenerateWithContent(ctx, content, l.merged(options)...)
}

func (l *FixedGenerateOptionsLLM) StreamGenerateWithContent(ctx context.Context, content []core.ContentBlock, options ...core.GenerateOption) (*core.StreamResponse, error) {
	return l.base.StreamGenerateWithContent(ctx, content, l.merged(options)...)
}

func (l *FixedGenerateOptionsLLM) ProviderName() string {
	return l.base.ProviderName()
}

func (l *FixedGenerateOptionsLLM) ModelID() string {
	return l.base.ModelID()
}

func (l *FixedGenerateOptionsLLM) Capabilities() []core.Capability {
	return l.base.Capabilities()
}
