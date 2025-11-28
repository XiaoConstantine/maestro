package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	models "github.com/XiaoConstantine/mcp-go/pkg/model"
)

// SgrepTool provides semantic code search capabilities as an agentic tool.
// It wraps the sgrep CLI for semantic search, indexing, and status operations.
type SgrepTool struct {
	logger   *logging.Logger
	rootPath string // Root path for the repository
}

// SgrepSearchResult represents a single sgrep search result.
type SgrepSearchResult struct {
	FilePath  string  `json:"file_path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
}

// NewSgrepTool creates a new sgrep tool instance.
func NewSgrepTool(logger *logging.Logger, rootPath string) *SgrepTool {
	return &SgrepTool{
		logger:   logger,
		rootPath: rootPath,
	}
}

// Name returns the tool name.
func (s *SgrepTool) Name() string {
	return "sgrep"
}

// Description returns the tool description.
func (s *SgrepTool) Description() string {
	return `Semantic code search tool for conceptual queries. Unlike text search, sgrep understands meaning.

Actions:
- search: Find code by meaning (e.g., "error handling", "database connection pooling")
- hybrid_search: Combine semantic + keyword search for specific terms
- index: Index a directory for semantic search
- status: Check if the current repository is indexed
- is_available: Check if sgrep is installed

Use 'search' for natural language queries about code concepts.
Use 'hybrid_search' when your query contains specific function/variable names.`
}

// Metadata returns tool metadata.
func (s *SgrepTool) Metadata() *core.ToolMetadata {
	return &core.ToolMetadata{
		Name:        s.Name(),
		Description: s.Description(),
		Version:     "1.0",
	}
}

// CanHandle checks if this tool can handle the given intent.
func (s *SgrepTool) CanHandle(ctx context.Context, intent string) bool {
	intentLower := strings.ToLower(intent)
	return strings.Contains(intentLower, "semantic") ||
		strings.Contains(intentLower, "meaning") ||
		strings.Contains(intentLower, "concept") ||
		strings.Contains(intentLower, "how does") ||
		strings.Contains(intentLower, "understand")
}

// Execute runs the sgrep tool with the given parameters.
func (s *SgrepTool) Execute(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
	action, ok := params["action"].(string)
	if !ok {
		action = "search" // Default action
	}

	switch action {
	case "search":
		return s.executeSearch(ctx, params, false)
	case "hybrid_search":
		return s.executeSearch(ctx, params, true)
	case "index":
		return s.executeIndex(ctx, params)
	case "status":
		return s.executeStatus(ctx)
	case "is_available":
		return s.checkAvailability(ctx)
	default:
		return core.ToolResult{}, fmt.Errorf("unknown action: %s. Valid actions: search, hybrid_search, index, status, is_available", action)
	}
}

// Validate validates the tool parameters.
func (s *SgrepTool) Validate(params map[string]interface{}) error {
	action, _ := params["action"].(string)
	if action == "" {
		action = "search"
	}

	switch action {
	case "search", "hybrid_search":
		if _, ok := params["query"].(string); !ok {
			return fmt.Errorf("'query' parameter required for %s action", action)
		}
	case "index":
		// path is optional, defaults to current directory
	case "status", "is_available":
		// no parameters needed
	}

	return nil
}

// InputSchema returns the input schema for MCP compatibility.
func (s *SgrepTool) InputSchema() models.InputSchema {
	return models.InputSchema{
		Type: "object",
		Properties: map[string]models.ParameterSchema{
			"action": {
				Type:        "string",
				Description: "Action to perform: search, hybrid_search, index, status, is_available",
			},
			"query": {
				Type:        "string",
				Description: "Search query (required for search/hybrid_search)",
			},
			"limit": {
				Type:        "integer",
				Description: "Maximum number of results (default: 10)",
			},
			"path": {
				Type:        "string",
				Description: "Path to index (for index action, defaults to current directory)",
			},
		},
	}
}

// executeSearch performs semantic or hybrid search.
func (s *SgrepTool) executeSearch(ctx context.Context, params map[string]interface{}, hybrid bool) (core.ToolResult, error) {
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return core.ToolResult{}, fmt.Errorf("query parameter required for search")
	}

	limit := 10
	if l, ok := params["limit"]; ok {
		switch v := l.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		case string:
			if parsed, err := strconv.Atoi(v); err == nil {
				limit = parsed
			}
		}
	}

	// Build sgrep command
	args := []string{query, "--json", "-n", fmt.Sprintf("%d", limit)}
	if hybrid {
		args = append(args, "--hybrid")
	}

	cmd := exec.CommandContext(ctx, "sgrep", args...)
	if s.rootPath != "" {
		cmd.Dir = s.rootPath
	}

	s.logger.Debug(ctx, "Executing sgrep: sgrep %s (hybrid=%v)", query, hybrid)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "not indexed") {
				return core.ToolResult{
					Data: "Repository not indexed. Run sgrep with action='index' first, or use 'sgrep index .' in terminal.",
					Metadata: map[string]interface{}{
						"error":       "not_indexed",
						"action":      "search",
						"hybrid":      hybrid,
						"needs_index": true,
					},
				}, nil
			}
			if strings.Contains(stderr, "no index found") {
				return core.ToolResult{
					Data: "No sgrep index found for this directory. Index the repository first with action='index'.",
					Metadata: map[string]interface{}{
						"error":       "no_index",
						"action":      "search",
						"hybrid":      hybrid,
						"needs_index": true,
					},
				}, nil
			}
			return core.ToolResult{}, fmt.Errorf("sgrep search failed: %s", stderr)
		}
		return core.ToolResult{}, fmt.Errorf("sgrep search failed: %w", err)
	}

	// Parse results
	var results []SgrepSearchResult
	if err := json.Unmarshal(output, &results); err != nil {
		// Return raw output if not JSON
		return core.ToolResult{
			Data: string(output),
			Metadata: map[string]interface{}{
				"action":       "search",
				"hybrid":       hybrid,
				"query":        query,
				"raw_response": true,
			},
		}, nil
	}

	// Format results for display
	formatted := s.formatResults(results)

	return core.ToolResult{
		Data: formatted,
		Metadata: map[string]interface{}{
			"action":       "search",
			"hybrid":       hybrid,
			"query":        query,
			"result_count": len(results),
			"results":      results, // Include raw results for programmatic access
		},
	}, nil
}

// executeIndex indexes a directory.
func (s *SgrepTool) executeIndex(ctx context.Context, params map[string]interface{}) (core.ToolResult, error) {
	path := "."
	if p, ok := params["path"].(string); ok && p != "" {
		path = p
	}

	cmd := exec.CommandContext(ctx, "sgrep", "index", path)
	if s.rootPath != "" && path == "." {
		cmd.Dir = s.rootPath
	}

	s.logger.Info(ctx, "Indexing directory: %s", path)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("sgrep index failed: %s", string(output))
	}

	return core.ToolResult{
		Data: string(output),
		Metadata: map[string]interface{}{
			"action":  "index",
			"path":    path,
			"success": true,
		},
	}, nil
}

// executeStatus checks the index status.
func (s *SgrepTool) executeStatus(ctx context.Context) (core.ToolResult, error) {
	cmd := exec.CommandContext(ctx, "sgrep", "status")
	if s.rootPath != "" {
		cmd.Dir = s.rootPath
	}

	output, err := cmd.Output()
	if err != nil {
		// Status command failing usually means not indexed
		return core.ToolResult{
			Data: "Repository not indexed or sgrep not available.",
			Metadata: map[string]interface{}{
				"action":    "status",
				"indexed":   false,
				"available": s.isAvailable(ctx),
			},
		}, nil
	}

	return core.ToolResult{
		Data: string(output),
		Metadata: map[string]interface{}{
			"action":    "status",
			"indexed":   true,
			"available": true,
		},
	}, nil
}

// checkAvailability checks if sgrep is installed.
func (s *SgrepTool) checkAvailability(ctx context.Context) (core.ToolResult, error) {
	available := s.isAvailable(ctx)

	var message string
	if available {
		message = "sgrep is installed and available for semantic code search."
	} else {
		message = "sgrep is not installed. Install from: https://github.com/XiaoConstantine/sgrep"
	}

	return core.ToolResult{
		Data: message,
		Metadata: map[string]interface{}{
			"action":    "is_available",
			"available": available,
		},
	}, nil
}

// isAvailable checks if sgrep CLI is installed.
func (s *SgrepTool) isAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "which", "sgrep")
	return cmd.Run() == nil
}

// IsIndexed checks if the current repository is indexed.
func (s *SgrepTool) IsIndexed(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "sgrep", "status")
	if s.rootPath != "" {
		cmd.Dir = s.rootPath
	}
	return cmd.Run() == nil
}

// formatResults formats search results for display.
func (s *SgrepTool) formatResults(results []SgrepSearchResult) string {
	if len(results) == 0 {
		return "No semantic matches found."
	}

	var formatted []string
	for i, r := range results {
		if i >= 15 { // Limit displayed results
			formatted = append(formatted, fmt.Sprintf("... and %d more results", len(results)-i))
			break
		}

		// Truncate content if too long
		content := r.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}

		// Convert distance score to relevance (lower distance = higher relevance)
		relevance := 1.0 - r.Score
		if relevance < 0 {
			relevance = 0
		}

		formatted = append(formatted, fmt.Sprintf("🔍 %s:%d-%d (relevance: %.2f)\n```\n%s\n```",
			r.FilePath, r.StartLine, r.EndLine, relevance, content))
	}

	return strings.Join(formatted, "\n\n")
}

// Search is a convenience method for direct search without going through Execute.
func (s *SgrepTool) Search(ctx context.Context, query string, limit int) ([]SgrepSearchResult, error) {
	args := []string{query, "--json", "-n", fmt.Sprintf("%d", limit)}

	cmd := exec.CommandContext(ctx, "sgrep", args...)
	if s.rootPath != "" {
		cmd.Dir = s.rootPath
	}

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			return nil, fmt.Errorf("sgrep search failed: %s", stderr)
		}
		return nil, fmt.Errorf("sgrep search failed: %w", err)
	}

	var results []SgrepSearchResult
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("failed to parse sgrep output: %w", err)
	}

	return results, nil
}

// HybridSearch performs hybrid semantic + keyword search.
func (s *SgrepTool) HybridSearch(ctx context.Context, query string, limit int) ([]SgrepSearchResult, error) {
	args := []string{query, "--json", "-n", fmt.Sprintf("%d", limit), "--hybrid"}

	cmd := exec.CommandContext(ctx, "sgrep", args...)
	if s.rootPath != "" {
		cmd.Dir = s.rootPath
	}

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			return nil, fmt.Errorf("sgrep hybrid search failed: %s", stderr)
		}
		return nil, fmt.Errorf("sgrep hybrid search failed: %w", err)
	}

	var results []SgrepSearchResult
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("failed to parse sgrep output: %w", err)
	}

	return results, nil
}

// Index indexes a directory for semantic search.
func (s *SgrepTool) Index(ctx context.Context, path string) error {
	if path == "" {
		path = "."
	}

	cmd := exec.CommandContext(ctx, "sgrep", "index", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sgrep index failed: %s", string(output))
	}

	s.logger.Info(ctx, "Successfully indexed: %s", path)
	return nil
}

// SearchToContent converts search results to Content format for RAG compatibility.
func (s *SgrepTool) SearchToContent(ctx context.Context, query string, limit int) ([]*Content, error) {
	results, err := s.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	contents := make([]*Content, len(results))
	for i, r := range results {
		// Convert distance to relevance
		relevance := 1.0 - r.Score
		if relevance < 0 {
			relevance = 0
		}

		contents[i] = &Content{
			ID:   fmt.Sprintf("%s:%d-%d", r.FilePath, r.StartLine, r.EndLine),
			Text: r.Content,
			Metadata: map[string]string{
				"file_path":    r.FilePath,
				"start_line":   fmt.Sprintf("%d", r.StartLine),
				"end_line":     fmt.Sprintf("%d", r.EndLine),
				"score":        fmt.Sprintf("%.4f", r.Score),
				"relevance":    fmt.Sprintf("%.4f", relevance),
				"content_type": ContentTypeRepository,
				"source":       "sgrep",
			},
		}
	}

	return contents, nil
}
