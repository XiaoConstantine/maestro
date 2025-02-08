package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

func parseModelString(modelStr string) (provider, name, config string) {
	parts := strings.Split(modelStr, ":")
	switch len(parts) {
	case 1:
		return parts[0], "", ""
	case 2:
		return parts[0], parts[1], ""
	case 3:
		return parts[0], parts[1], parts[2]
	default:
		return "", "", ""
	}
}

func constructModelID(cfg *config) core.ModelID {
	var parts []string
	parts = append(parts, cfg.modelProvider)

	if cfg.modelName != "" {
		parts = append(parts, cfg.modelName)
	}

	if cfg.modelConfig != "" {
		parts = append(parts, cfg.modelConfig)
	}

	if cfg.modelProvider == "ollama" || cfg.modelProvider == "llamacpp:" {
		return core.ModelID(strings.Join(parts, ":"))
	} else {
		return core.ModelID(cfg.modelName)
	}
}

func validateModelConfig(cfg *config) error {
	if cfg.modelProvider == "anthropic" || cfg.modelProvider == "google" {
		key, err := checkProviderAPIKey(cfg.modelProvider, cfg.apiKey)
		if err != nil {
			return err
		}
		// Update the config with the key from environment if one was found
		cfg.apiKey = key
	}
	// Validate provider
	switch cfg.modelProvider {
	case "llamacpp:", "ollama", "anthropic", "google":
		// Valid providers
	default:
		return fmt.Errorf("unsupported model provider: %s", cfg.modelProvider)
	}

	// Validate provider-specific configurations
	switch cfg.modelProvider {
	case "anthropic", "google":
		if cfg.apiKey == "" {
			return fmt.Errorf("API key required for external providers like anthropic, google")
		}
	case "ollama":
		if cfg.modelName == "" {
			return fmt.Errorf("model name required for Ollama models")
		}
	}

	return nil
}

func checkProviderAPIKey(provider, apiKey string) (string, error) {
	// If API key is provided directly, use it
	if apiKey != "" {
		return apiKey, nil
	}

	// Define provider-specific environment variable names
	var envKey string
	switch provider {
	case "anthropic":
		// Check both older and newer Anthropic environment variable patterns
		envKey = firstNonEmpty(
			os.Getenv("ANTHROPIC_API_KEY"),
			os.Getenv("CLAUDE_API_KEY"),
		)
	case "google":
		// Google typically uses GOOGLE_API_KEY or specific service keys
		envKey = firstNonEmpty(
			os.Getenv("GOOGLE_API_KEY"),
			os.Getenv("GOOGLE_GEMINI_KEY"),
			os.Getenv("GEMINI_API_KEY"),
		)
	default:
		// For other providers, we don't check environment variables
		return "", fmt.Errorf("API key required for %s provider", provider)
	}

	if envKey == "" {
		// Provide a helpful error message listing the environment variables checked
		var envVars []string
		switch provider {
		case "anthropic":
			envVars = []string{"ANTHROPIC_API_KEY", "CLAUDE_API_KEY"}
		case "google":
			envVars = []string{"GOOGLE_API_KEY", "GOOGLE_GEMINI_KEY", "GEMINI_API_KEY"}
		}
		return "", fmt.Errorf("API key required for %s provider. Please provide via --api-key flag or set one of these environment variables: %s",
			provider, strings.Join(envVars, ", "))
	}

	return envKey, nil
}

// Helper function to return the first non-empty string from a list.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Helper function until Go 1.21's min/max functions are available.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func escapeXMLContent(content string) string {
	content = strings.ReplaceAll(content, "&", "&amp;")
	content = strings.ReplaceAll(content, "<", "&lt;")
	content = strings.ReplaceAll(content, ">", "&gt;")
	content = strings.ReplaceAll(content, "\"", "&quot;")
	content = strings.ReplaceAll(content, "'", "&apos;")
	return content
}

func escapeCodeContent(content string) string {
	return fmt.Sprintf("<![CDATA[%s]]>", content)
}

func CreateStoragePath(owner, repo, commitSHA string) (string, error) {
	// Get the user's home directory - this is the proper way to handle "~"
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	// Construct the full path for the .maestro directory
	maestroDir := filepath.Join(homeDir, ".maestro")

	// Create the directory with appropriate permissions (0755 gives read/execute to all, write to owner)
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", maestroDir, err)
	}

	// Create the database filename - using underscore instead of dash for better compatibility
	dbName := fmt.Sprintf("%s_%s_%s.db", owner, repo, commitSHA[:8])
	// Construct the full path to the database file
	dbPath := filepath.Join(maestroDir, dbName)

	return dbPath, nil
}

func extractSHAFromPath(path string) string {
	base := filepath.Base(path)
	parts := strings.Split(base, "_")
	if len(parts) >= 3 {
		return strings.TrimSuffix(parts[2], ".db")
	}
	return ""
}

func copyFile(src, dst string) error {
	// Open the source file
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	// Create the destination file
	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	// Copy the contents
	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	// Sync to ensure write is complete
	if err := destFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync destination file: %w", err)
	}

	return nil
}
