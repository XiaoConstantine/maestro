// Package util provides utility functions used across the maestro codebase.
package util

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/types"
)

// ModelConfig holds model configuration for parsing and validation.
type ModelConfig struct {
	ModelProvider string
	ModelName     string
	ModelConfig   string
	APIKey        string
}

// ParseModelString parses a model string into provider, name, and config parts.
func ParseModelString(modelStr string) (provider, name, config string) {
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

// ConstructModelID constructs a model ID from the configuration.
func ConstructModelID(cfg *ModelConfig) core.ModelID {
	var parts []string
	parts = append(parts, cfg.ModelProvider)

	if cfg.ModelName != "" {
		if cfg.ModelName == "llamacpp:" {
			parts = append(parts, "")
		} else {
			parts = append(parts, cfg.ModelName)
		}
	}

	if cfg.ModelConfig != "" {
		parts = append(parts, cfg.ModelConfig)
	}

	if cfg.ModelProvider == "ollama" || cfg.ModelProvider == "llamacpp" {
		return core.ModelID(strings.Join(parts, ":"))
	} else {
		return core.ModelID(cfg.ModelName)
	}
}

// ValidateModelConfig validates the model configuration.
func ValidateModelConfig(cfg *ModelConfig) error {
	if cfg.ModelProvider == "anthropic" || cfg.ModelProvider == "google" {
		key, err := CheckProviderAPIKey(cfg.ModelProvider, cfg.APIKey)
		if err != nil {
			return err
		}
		// Update the config with the key from environment if one was found
		cfg.APIKey = key
	}
	// Validate provider
	switch cfg.ModelProvider {
	case "llamacpp:", "ollama", "anthropic", "google", "llamacpp":
		// Valid providers
	default:
		return fmt.Errorf("unsupported model provider: %s", cfg.ModelProvider)
	}

	// Validate provider-specific configurations
	switch cfg.ModelProvider {
	case "anthropic", "google":
		if cfg.APIKey == "" {
			return fmt.Errorf("API key required for external providers like anthropic, google")
		}
	case "ollama":
		if cfg.ModelName == "" {
			return fmt.Errorf("model name required for Ollama models")
		}
	}

	return nil
}

// CheckProviderAPIKey checks for an API key from various sources.
func CheckProviderAPIKey(provider, apiKey string) (string, error) {
	// If API key is provided directly, use it
	if apiKey != "" {
		return apiKey, nil
	}

	// Define provider-specific environment variable names
	var envKey string
	switch provider {
	case "anthropic":
		// Check both older and newer Anthropic environment variable patterns
		envKey = FirstNonEmpty(
			os.Getenv("ANTHROPIC_API_KEY"),
			os.Getenv("CLAUDE_API_KEY"),
		)
	case "google":
		// Google typically uses GOOGLE_API_KEY or specific service keys
		envKey = FirstNonEmpty(
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

// FirstNonEmpty returns the first non-empty string from a list.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Min returns the minimum of two integers.
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Max returns the maximum of two integers.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// CreateStoragePath creates a storage path for the given owner and repo.
func CreateStoragePath(ctx context.Context, owner, repo string) (string, error) {
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
	// Use a single database file per repository
	dbName := fmt.Sprintf("%s_%s.db", owner, repo)
	return filepath.Join(maestroDir, dbName), nil
}

// LevenshteinDistance calculates the minimum number of single-character edits
// required to change one string into another. This helps us determine if two
// code examples are similar enough that the transformation could be automated.
func LevenshteinDistance(s1, s2 string) int {
	// Create a matrix of size (len(s1)+1) x (len(s2)+1)
	// The extra row and column are for the empty string case
	rows := len(s1) + 1
	cols := len(s2) + 1
	matrix := make([][]int, rows)
	for i := range matrix {
		matrix[i] = make([]int, cols)
	}

	// Initialize the first row and column
	// These represent the distance from an empty string
	for i := 0; i < rows; i++ {
		matrix[i][0] = i
	}
	for j := 0; j < cols; j++ {
		matrix[0][j] = j
	}

	// Fill in the rest of the matrix
	for i := 1; i < rows; i++ {
		for j := 1; j < cols; j++ {
			// If characters match, cost is 0; otherwise 1
			cost := 1
			if s1[i-1] == s2[j-1] {
				cost = 0
			}

			// Take the minimum of:
			// 1. Delete a character from s1 (matrix[i-1][j] + 1)
			// 2. Insert a character into s1 (matrix[i][j-1] + 1)
			// 3. Substitute a character (matrix[i-1][j-1] + cost)
			matrix[i][j] = Min(
				matrix[i-1][j]+1, // deletion
				Min(
					matrix[i][j-1]+1,      // insertion
					matrix[i-1][j-1]+cost, // substitution
				),
			)
		}
	}

	// The bottom-right cell contains the minimum number of operations needed
	return matrix[rows-1][cols-1]
}

// CompressText compresses text using gzip and base64 encoding.
func CompressText(text string) (string, error) {
	// Create a buffer to hold compressed data
	var compressed bytes.Buffer

	// Create a gzip writer with best compression
	gzWriter, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip writer: %w", err)
	}

	// Write the text and close the writer
	if _, err := gzWriter.Write([]byte(text)); err != nil {
		return "", fmt.Errorf("failed to compress text: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize compression: %w", err)
	}

	// Encode as base64 for safe storage in SQLite
	encoded := base64.StdEncoding.EncodeToString(compressed.Bytes())
	return encoded, nil
}

// DecompressText decompresses gzip+base64 encoded text.
func DecompressText(compressed string) (string, error) {
	// Decode from base64
	decoded, err := base64.StdEncoding.DecodeString(compressed)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	// Create a gzip reader
	gzReader, err := gzip.NewReader(bytes.NewReader(decoded))
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// Read and decompress
	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		return "", fmt.Errorf("failed to decompress text: %w", err)
	}

	return string(decompressed), nil
}

// Pluralize returns a pluralized word if count is not 1.
func Pluralize(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

// IsEmptyResult safely checks if a result contains no items, handling different
// potential result types that could come from our review stages. This helps us
// distinguish between valid empty results and errors.
func IsEmptyResult(result interface{}) bool {
	if result == nil {
		return true
	}

	switch v := result.(type) {
	case []types.PotentialIssue:
		// For rule checker results
		return len(v) == 0
	case []types.PRReviewComment:
		// For review filter and final review results
		return len(v) == 0
	case map[string]interface{}:
		// For structured results that might contain comments or issues
		return len(v) == 0
	default:
		// For any other type, we consider it empty if it's not one of our
		// expected result types
		return true
	}
}

// EscapeFileContent safely escapes a string intended for XML's file_content field.
// It ensures special characters like &, <, and > are properly escaped.
func EscapeFileContent(ctx context.Context, content string) string {
	logger := logging.GetLogger()

	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(content)); err != nil {
		logger.Error(ctx, "Failed to escape file_content: %v, falling back to original content", err)
		return content // Fallback to unescaped content (log and proceed)
	}
	escaped := buf.String()

	logger.Debug(ctx, "Escaped file_content: original length=%d, escaped length=%d", len(content), len(escaped))
	return escaped
}

// SafeGetString safely gets a string from a map.
func SafeGetString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// SafeGetInt safely gets an int from a map.
func SafeGetInt(m map[string]interface{}, key string) int {
	if val, ok := m[key].(float64); ok {
		return int(val)
	}
	return 0
}

// SafeGetFloat safely gets a float64 from a map.
func SafeGetFloat(m map[string]interface{}, key string) float64 {
	if val, ok := m[key].(float64); ok {
		return val
	}
	return 0.0
}

// SafeGetBool safely gets a bool from a map.
func SafeGetBool(m map[string]interface{}, key string) bool {
	if val, ok := m[key].(bool); ok {
		return val
	}
	return false
}

// CreateStreamHandler creates a stream handler for logging.
func CreateStreamHandler(ctx context.Context, logger *logging.Logger) func(chunk core.StreamChunk) error {
	return func(chunk core.StreamChunk) error {
		switch {
		case chunk.Error != nil:
			logger.Error(ctx, "\nError: %v\n", chunk.Error)
			return chunk.Error
		case chunk.Done:
			logger.Info(ctx, "\n[DONE]")
		default:
			logger.Debug(ctx, "Content: %v", chunk.Content)

		}
		return nil
	}
}

// GetEnvBool gets a boolean value from an environment variable.
func GetEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1"
}

// GetEnvInt gets an integer value from an environment variable.
func GetEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	var result int
	_, err := fmt.Sscanf(value, "%d", &result)
	if err != nil {
		return defaultValue
	}
	return result
}

// GetCurrentTimeMs returns the current time in milliseconds.
func GetCurrentTimeMs() float64 {
	return float64(time.Now().UnixNano()) / 1e6
}

// GetStringFromContext extracts a string value from a context map.
func GetStringFromContext(context map[string]interface{}, key, defaultValue string) string {
	if value, ok := context[key].(string); ok {
		return value
	}
	return defaultValue
}
