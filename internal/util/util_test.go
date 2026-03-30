package util

import (
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

func TestNormalizeModelName(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{
			name:     "google gemini 3 dot 0 pro alias",
			provider: "google",
			model:    "gemini-3.0-pro",
			want:     string(core.ModelGoogleGemini3ProPreview),
		},
		{
			name:     "google gemini 3 flash alias",
			provider: "google",
			model:    "gemini-3-flash",
			want:     string(core.ModelGoogleGemini3FlashPreview),
		},
		{
			name:     "other provider unchanged",
			provider: "anthropic",
			model:    "claude-sonnet-4-5-20250929",
			want:     "claude-sonnet-4-5-20250929",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeModelName(tc.provider, tc.model)
			if got != tc.want {
				t.Fatalf("normalizeModelName(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
			}
		})
	}
}

func TestConstructModelIDNormalizesGoogleAliases(t *testing.T) {
	cfg := &ModelConfig{
		ModelProvider: "google",
		ModelName:     "gemini-3.0-pro",
	}

	got := ConstructModelID(cfg)
	if got != core.ModelGoogleGemini3ProPreview {
		t.Fatalf("ConstructModelID() = %q, want %q", got, core.ModelGoogleGemini3ProPreview)
	}
}
