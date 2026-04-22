package util

import (
	"context"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

type fixedOptionsStubLLM struct {
	lastOptions *core.GenerateOptions
}

func (s *fixedOptionsStubLLM) Generate(_ context.Context, _ string, options ...core.GenerateOption) (*core.LLMResponse, error) {
	s.lastOptions = core.NewGenerateOptions()
	for _, opt := range options {
		opt(s.lastOptions)
	}
	return &core.LLMResponse{Content: "ok"}, nil
}

func (s *fixedOptionsStubLLM) GenerateWithJSON(_ context.Context, _ string, options ...core.GenerateOption) (map[string]any, error) {
	s.lastOptions = core.NewGenerateOptions()
	for _, opt := range options {
		opt(s.lastOptions)
	}
	return map[string]any{"ok": true}, nil
}

func (s *fixedOptionsStubLLM) GenerateWithFunctions(_ context.Context, _ string, _ []map[string]any, options ...core.GenerateOption) (map[string]any, error) {
	s.lastOptions = core.NewGenerateOptions()
	for _, opt := range options {
		opt(s.lastOptions)
	}
	return map[string]any{"ok": true}, nil
}

func (s *fixedOptionsStubLLM) CreateEmbedding(context.Context, string, ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	return &core.EmbeddingResult{}, nil
}

func (s *fixedOptionsStubLLM) CreateEmbeddings(context.Context, []string, ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	return &core.BatchEmbeddingResult{}, nil
}

func (s *fixedOptionsStubLLM) StreamGenerate(context.Context, string, ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, nil
}

func (s *fixedOptionsStubLLM) GenerateWithContent(context.Context, []core.ContentBlock, ...core.GenerateOption) (*core.LLMResponse, error) {
	return &core.LLMResponse{Content: "ok"}, nil
}

func (s *fixedOptionsStubLLM) StreamGenerateWithContent(context.Context, []core.ContentBlock, ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, nil
}

func (s *fixedOptionsStubLLM) ProviderName() string { return "stub" }
func (s *fixedOptionsStubLLM) ModelID() string      { return "stub-model" }
func (s *fixedOptionsStubLLM) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityCompletion}
}

func TestFixedGenerateOptionsLLMOverridesTemperature(t *testing.T) {
	stub := &fixedOptionsStubLLM{}
	wrapped := NewFixedGenerateOptionsLLM(stub, core.WithTemperature(0))

	_, err := wrapped.Generate(context.Background(), "hello", core.WithTemperature(0.8), core.WithMaxTokens(123))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if stub.lastOptions == nil {
		t.Fatalf("Generate() did not record options")
	}
	if stub.lastOptions.Temperature != 0 {
		t.Fatalf("Temperature = %v, want 0", stub.lastOptions.Temperature)
	}
	if stub.lastOptions.MaxTokens != 123 {
		t.Fatalf("MaxTokens = %d, want 123", stub.lastOptions.MaxTokens)
	}
}
