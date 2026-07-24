package util

import (
	"context"
	"os"
	"path/filepath"
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

func TestValidateModelConfigAcceptsOpenAIProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	cfg := &ModelConfig{
		ModelProvider: "openai",
		ModelName:     "gpt-5.4-mini",
	}

	if err := ValidateModelConfig(cfg); err != nil {
		t.Fatalf("ValidateModelConfig() error = %v", err)
	}
	if cfg.APIKey != "test-openai-key" {
		t.Fatalf("ValidateModelConfig() APIKey = %q, want %q", cfg.APIKey, "test-openai-key")
	}
}

func TestCheckProviderAPIKeyOpenAIIgnoresSubscriptionToken(t *testing.T) {
	t.Setenv("OPENAI_OAUTH_TOKEN", "oat-env")
	t.Setenv("OPENAI_API_KEY", "api-key")

	got, err := CheckProviderAPIKey("openai", "")
	if err != nil {
		t.Fatalf("CheckProviderAPIKey() error = %v", err)
	}
	if got != "api-key" {
		t.Fatalf("CheckProviderAPIKey() = %q, want api-key", got)
	}
}

func TestCheckProviderAPIKeyOpenAI(t *testing.T) {
	old := os.Getenv("OPENAI_API_KEY")
	t.Cleanup(func() {
		if old == "" {
			_ = os.Unsetenv("OPENAI_API_KEY")
			return
		}
		_ = os.Setenv("OPENAI_API_KEY", old)
	})
	_ = os.Setenv("OPENAI_API_KEY", "env-openai-key")

	got, err := CheckProviderAPIKey("openai", "")
	if err != nil {
		t.Fatalf("CheckProviderAPIKey() error = %v", err)
	}
	if got != "env-openai-key" {
		t.Fatalf("CheckProviderAPIKey() = %q, want %q", got, "env-openai-key")
	}
}

func TestValidateAndLoadOpenAICodexUsesStoredSubscription(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("MAESTRO_CREDENTIALS_PATH", path)
	credential := `{"version":1,"openai":{"access_token":"oat-stored","account_id":"account","expires_at":"2000-01-01T00:00:00Z"}}`
	if err := os.WriteFile(path, []byte(credential), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := &ModelConfig{ModelProvider: "openai-codex", ModelName: "gpt-5.4"}
	if err := ValidateModelConfig(cfg); err != nil {
		t.Fatalf("ValidateModelConfig() error = %v", err)
	}
	llm, err := LoadLLMFromModelConfig(context.Background(), cfg, "gpt-5.4")
	if err != nil {
		t.Fatalf("LoadLLMFromModelConfig() error = %v", err)
	}
	if got := llm.ProviderName(); got != "openai-codex" {
		t.Fatalf("ProviderName() = %q, want openai-codex", got)
	}
}

func TestCheckProviderAPIKeyAnthropicUsesOAuthToken(t *testing.T) {
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "sk-ant-oat-test")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-api-key")
	t.Setenv("CLAUDE_API_KEY", "claude-api-key")

	got, err := CheckProviderAPIKey("anthropic", "")
	if err != nil {
		t.Fatalf("CheckProviderAPIKey() error = %v", err)
	}
	if got != "sk-ant-oat-test" {
		t.Fatalf("CheckProviderAPIKey() = %q, want OAuth token", got)
	}
}

func TestValidateModelConfigDefaultsOpenAIBaseURL(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_BASE", "")

	cfg := &ModelConfig{
		ModelProvider: "openai",
		ModelName:     "gpt-5.4-mini",
	}

	if err := ValidateModelConfig(cfg); err != nil {
		t.Fatalf("ValidateModelConfig() error = %v", err)
	}
	if cfg.BaseURL != defaultOpenAIBaseURL {
		t.Fatalf("ValidateModelConfig() BaseURL = %q, want %q", cfg.BaseURL, defaultOpenAIBaseURL)
	}
}

func TestValidateModelConfigSkipsAPIKeyForLocalOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	cfg := &ModelConfig{
		ModelProvider: "openai",
		ModelName:     "Qwen3.5-9B-MLX-4bit",
		BaseURL:       "http://127.0.0.1:8081",
	}

	if err := ValidateModelConfig(cfg); err != nil {
		t.Fatalf("ValidateModelConfig() error = %v, want nil for local base URL", err)
	}
}

func TestValidateModelConfigRequiresAPIKeyForRemoteOpenAI(t *testing.T) {
	t.Setenv("OPENAI_OAUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("MAESTRO_CREDENTIALS_PATH", filepath.Join(t.TempDir(), "missing.json"))

	cfg := &ModelConfig{
		ModelProvider: "openai",
		ModelName:     "gpt-5.4-mini",
		BaseURL:       "https://api.openai.com",
	}

	if err := ValidateModelConfig(cfg); err == nil {
		t.Fatalf("ValidateModelConfig() error = nil, want API key error for remote base URL")
	}
}

func TestProviderConfigFromModelConfigUsesOpenAIEnvBaseURL(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://custom.openai.example")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	cfg := &ModelConfig{
		ModelProvider: "openai",
		ModelName:     "gpt-5.4-mini",
	}
	if err := ValidateModelConfig(cfg); err != nil {
		t.Fatalf("ValidateModelConfig() error = %v", err)
	}

	got := ProviderConfigFromModelConfig(cfg)
	if got.Name != "openai" {
		t.Fatalf("ProviderConfigFromModelConfig() Name = %q, want %q", got.Name, "openai")
	}
	if got.BaseURL != "https://custom.openai.example" {
		t.Fatalf("ProviderConfigFromModelConfig() BaseURL = %q, want %q", got.BaseURL, "https://custom.openai.example")
	}
	if got.Endpoint == nil || got.Endpoint.BaseURL != "https://custom.openai.example" {
		t.Fatalf("ProviderConfigFromModelConfig() Endpoint.BaseURL = %v, want %q", got.Endpoint, "https://custom.openai.example")
	}
}

func TestProviderConfigFromModelConfigSetsLongerTimeoutForLocalOpenAI(t *testing.T) {
	cfg := &ModelConfig{
		ModelProvider: "openai",
		ModelName:     "mlx-community/Qwen3.5-9B-OptiQ-4bit",
		BaseURL:       "http://127.0.0.1:8081",
	}
	if err := ValidateModelConfig(cfg); err != nil {
		t.Fatalf("ValidateModelConfig() error = %v", err)
	}

	got := ProviderConfigFromModelConfig(cfg)
	if got.Endpoint == nil {
		t.Fatalf("ProviderConfigFromModelConfig() Endpoint = nil, want timeout-enabled endpoint")
	}
	if got.Endpoint.TimeoutSec != defaultLocalLLMTimeoutSec {
		t.Fatalf("ProviderConfigFromModelConfig() Endpoint.TimeoutSec = %d, want %d", got.Endpoint.TimeoutSec, defaultLocalLLMTimeoutSec)
	}
}
