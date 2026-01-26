package util

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateModelConfig(t *testing.T) {
	t.Run("accepts anthropic provider with API key", func(t *testing.T) {
		cfg := &ModelConfig{
			ModelProvider: "anthropic",
			APIKey:        "test-key",
		}
		err := ValidateModelConfig(cfg)
		assert.NoError(t, err)
	})

	t.Run("accepts openai provider with API key", func(t *testing.T) {
		cfg := &ModelConfig{
			ModelProvider: "openai",
			APIKey:        "test-key",
		}
		err := ValidateModelConfig(cfg)
		assert.NoError(t, err)
	})

	t.Run("accepts google provider with API key", func(t *testing.T) {
		cfg := &ModelConfig{
			ModelProvider: "google",
			APIKey:        "test-key",
		}
		err := ValidateModelConfig(cfg)
		assert.NoError(t, err)
	})

	t.Run("accepts ollama with model name", func(t *testing.T) {
		cfg := &ModelConfig{
			ModelProvider: "ollama",
			ModelName:     "mistral",
		}
		err := ValidateModelConfig(cfg)
		assert.NoError(t, err)
	})

	t.Run("rejects ollama without model name", func(t *testing.T) {
		cfg := &ModelConfig{
			ModelProvider: "ollama",
		}
		err := ValidateModelConfig(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "model name required")
	})

	t.Run("rejects unsupported provider", func(t *testing.T) {
		cfg := &ModelConfig{
			ModelProvider: "unsupported",
		}
		err := ValidateModelConfig(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported model provider")
	})

	t.Run("accepts llamacpp provider", func(t *testing.T) {
		cfg := &ModelConfig{
			ModelProvider: "llamacpp",
		}
		err := ValidateModelConfig(cfg)
		assert.NoError(t, err)
	})

	t.Run("accepts llamacpp: provider", func(t *testing.T) {
		cfg := &ModelConfig{
			ModelProvider: "llamacpp:",
		}
		err := ValidateModelConfig(cfg)
		assert.NoError(t, err)
	})
}

func TestCheckProviderAPIKey(t *testing.T) {
	t.Run("returns provided API key directly", func(t *testing.T) {
		key, err := CheckProviderAPIKey("anthropic", "my-api-key")
		assert.NoError(t, err)
		assert.Equal(t, "my-api-key", key)
	})

	t.Run("reads OPENAI_API_KEY from environment", func(t *testing.T) {
		// Set up environment
		oldKey := os.Getenv("OPENAI_API_KEY")
		os.Setenv("OPENAI_API_KEY", "test-openai-key")
		defer os.Setenv("OPENAI_API_KEY", oldKey)

		key, err := CheckProviderAPIKey("openai", "")
		assert.NoError(t, err)
		assert.Equal(t, "test-openai-key", key)
	})

	t.Run("reads ANTHROPIC_API_KEY from environment", func(t *testing.T) {
		// Set up environment
		oldKey := os.Getenv("ANTHROPIC_API_KEY")
		os.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
		defer os.Setenv("ANTHROPIC_API_KEY", oldKey)

		key, err := CheckProviderAPIKey("anthropic", "")
		assert.NoError(t, err)
		assert.Equal(t, "test-anthropic-key", key)
	})

	t.Run("errors when openai key not found", func(t *testing.T) {
		// Clear any existing keys
		oldKey := os.Getenv("OPENAI_API_KEY")
		os.Unsetenv("OPENAI_API_KEY")
		defer os.Setenv("OPENAI_API_KEY", oldKey)

		_, err := CheckProviderAPIKey("openai", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "OPENAI_API_KEY")
	})

	t.Run("errors for unknown provider", func(t *testing.T) {
		_, err := CheckProviderAPIKey("unknown", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "API key required")
	})
}

func TestParseModelString(t *testing.T) {
	tests := []struct {
		input    string
		provider string
		name     string
		config   string
	}{
		{"anthropic", "anthropic", "", ""},
		{"anthropic:claude-3", "anthropic", "claude-3", ""},
		{"ollama:mistral:q4", "ollama", "mistral", "q4"},
		{"", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			provider, name, config := ParseModelString(tt.input)
			assert.Equal(t, tt.provider, provider)
			assert.Equal(t, tt.name, name)
			assert.Equal(t, tt.config, config)
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	assert.Equal(t, "first", FirstNonEmpty("first", "second", "third"))
	assert.Equal(t, "second", FirstNonEmpty("", "second", "third"))
	assert.Equal(t, "third", FirstNonEmpty("", "", "third"))
	assert.Equal(t, "", FirstNonEmpty("", "", ""))
}
