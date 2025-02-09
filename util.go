package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/logrusorgru/aurora"
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

func CreateStoragePath(ctx context.Context, owner, repo, commitSHA string) (string, bool, error) {
	logger := logging.GetLogger()
	// Get the user's home directory - this is the proper way to handle "~"
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("failed to get home directory: %w", err)
	}
	// Construct the full path for the .maestro directory
	maestroDir := filepath.Join(homeDir, ".maestro")

	// Create the directory with appropriate permissions (0755 gives read/execute to all, write to owner)
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return "", false, fmt.Errorf("failed to create directory %s: %w", maestroDir, err)
	}

	pattern := fmt.Sprintf("%s_%s_*.db", owner, repo)
	existingDBs, err := filepath.Glob(filepath.Join(maestroDir, pattern))
	if err != nil {
		return "", false, fmt.Errorf("failed to list existing databases: %w", err)
	}

	newDBName := fmt.Sprintf("%s_%s_%s.db", owner, repo, commitSHA[:8])
	newDBPath := filepath.Join(maestroDir, newDBName)
	logger.Debug(ctx, "existing dbs: %v", existingDBs)

	if len(existingDBs) == 0 {
		return newDBPath, true, nil
	}
	sort.Strings(existingDBs)
	mostRecentDB := existingDBs[len(existingDBs)-1]

	// Extract the commit SHA from the most recent database filename
	parts := strings.Split(filepath.Base(mostRecentDB), "_")
	if len(parts) >= 3 {
		existingCommit, _ := strings.CutSuffix(parts[2], ".db") // The third part is the commit SHA
		logger.Debug(ctx, "existing commit: %s, latest commit: %s", existingCommit, commitSHA[:8])

		// If the commit hasn't changed, we can use the existing database
		if existingCommit == commitSHA[:8] {
			return mostRecentDB, false, nil
		}
	}

	// We have a different commit SHA, so we need full indexing with the new path
	return newDBPath, true, nil
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

func formatStructuredAnswer(answer string) string {
	if !strings.Contains(answer, "\n") {
		// For single-line answers, keep it simple
		return fmt.Sprintf("\n%s %s\n",
			aurora.Green("Answer:").Bold().String(),
			answer)
	}

	// For multi-line answers, add structure
	sections := strings.Split(answer, "\n\n")
	var formatted strings.Builder

	for i, section := range sections {
		if i == 0 {
			// First section gets special treatment as main answer
			formatted.WriteString(fmt.Sprintf("\n%s\n%s\n",
				aurora.Green("Answer:").Bold().String(),
				section))
		} else {
			// Additional sections get indentation and formatting
			formatted.WriteString(fmt.Sprintf("\n%s\n",
				indent(section, 2)))
		}
	}

	return formatted.String()
}

func groupFilesByDirectory(files []string) map[string][]string {
	groups := make(map[string][]string)
	for _, file := range files {
		dir := filepath.Dir(file)
		groups[dir] = append(groups[dir], filepath.Base(file))
	}
	return groups
}

// Helper function to print file tree.
func printFileTree(console *Console, filesByDir map[string][]string) {
	// Sort directories for consistent output
	dirs := make([]string, 0, len(filesByDir))
	for dir := range filesByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		files := filesByDir[dir]
		if console.color {
			console.printf("📁 %s\n", aurora.Blue(dir).String())
		} else {
			console.printf("📁 %s\n", dir)
		}

		for i, file := range files {
			prefix := "   ├── "
			if i == len(files)-1 {
				prefix = "   └── "
			}
			if console.color {
				console.printf("%s%s\n", prefix, aurora.Cyan(file).String())
			} else {
				console.printf("%s%s\n", prefix, file)
			}
		}
	}
}
