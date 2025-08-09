package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// SimpleSearchTool provides basic file search capabilities without embeddings
type SimpleSearchTool struct {
	logger      *logging.Logger
	rootPath    string
	mu          sync.RWMutex
	fileCache   map[string][]string // Cache of file contents by path
	cacheExpiry map[string]int64    // Expiry timestamps for cache entries
}

// SearchResult represents a search match
type SearchResult struct {
	FilePath   string
	LineNumber int
	Line       string
	Context    []string // Surrounding lines for context
	MatchType  string   // "exact", "pattern", "fuzzy"
	Score      float64  // Relevance score (0-1)
}

// NewSimpleSearchTool creates a new search tool
func NewSimpleSearchTool(logger *logging.Logger, rootPath string) *SimpleSearchTool {
	return &SimpleSearchTool{
		logger:      logger,
		rootPath:    rootPath,
		fileCache:   make(map[string][]string),
		cacheExpiry: make(map[string]int64),
	}
}

// GrepSearch performs a grep-like search across files
func (s *SimpleSearchTool) GrepSearch(ctx context.Context, pattern string, filePattern string, contextLines int) ([]*SearchResult, error) {
	s.logger.Debug(ctx, "Performing grep search: pattern=%s, files=%s", pattern, filePattern)
	
	// Compile regex pattern
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}
	
	// Find matching files
	files, err := s.findFiles(filePattern)
	if err != nil {
		return nil, fmt.Errorf("failed to find files: %w", err)
	}
	
	var results []*SearchResult
	for _, file := range files {
		fileResults, err := s.searchInFile(ctx, file, re, contextLines)
		if err != nil {
			s.logger.Warn(ctx, "Failed to search in file %s: %v", file, err)
			continue
		}
		results = append(results, fileResults...)
	}
	
	return results, nil
}

// GlobSearch finds files matching a glob pattern
func (s *SimpleSearchTool) GlobSearch(ctx context.Context, pattern string) ([]string, error) {
	s.logger.Debug(ctx, "Performing glob search: pattern=%s", pattern)
	
	fullPattern := filepath.Join(s.rootPath, pattern)
	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return nil, fmt.Errorf("glob search failed: %w", err)
	}
	
	// Make paths relative to root
	var relPaths []string
	for _, match := range matches {
		rel, err := filepath.Rel(s.rootPath, match)
		if err != nil {
			continue
		}
		relPaths = append(relPaths, rel)
	}
	
	return relPaths, nil
}

// ReadFile reads a file and returns its contents with line numbers
func (s *SimpleSearchTool) ReadFile(ctx context.Context, filePath string, startLine, endLine int) ([]string, error) {
	s.logger.Debug(ctx, "Reading file: %s (lines %d-%d)", filePath, startLine, endLine)
	
	// Check cache first
	lines := s.getCachedFile(filePath)
	if lines == nil {
		// Read from disk
		fullPath := filepath.Join(s.rootPath, filePath)
		file, err := os.Open(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open file: %w", err)
		}
		defer file.Close()
		
		lines = s.readLines(file)
		s.cacheFile(filePath, lines)
	}
	
	// Extract requested range
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > len(lines) {
		endLine = len(lines)
	}
	
	if startLine > len(lines) {
		return []string{}, nil
	}
	
	return lines[startLine-1 : endLine], nil
}

// FuzzySearch performs a fuzzy text search
func (s *SimpleSearchTool) FuzzySearch(ctx context.Context, query string, filePattern string, threshold float64) ([]*SearchResult, error) {
	s.logger.Debug(ctx, "Performing fuzzy search: query=%s, threshold=%.2f", query, threshold)
	
	queryLower := strings.ToLower(query)
	queryTokens := tokenize(queryLower)
	
	files, err := s.findFiles(filePattern)
	if err != nil {
		return nil, err
	}
	
	var results []*SearchResult
	for _, file := range files {
		lines, err := s.ReadFile(ctx, file, 0, 0)
		if err != nil {
			continue
		}
		
		for i, line := range lines {
			score := s.fuzzyScore(queryTokens, strings.ToLower(line))
			if score >= threshold {
				results = append(results, &SearchResult{
					FilePath:   file,
					LineNumber: i + 1,
					Line:       line,
					MatchType:  "fuzzy",
					Score:      score,
				})
			}
		}
	}
	
	return results, nil
}

// StructuralSearch searches for code structures (functions, classes, etc.)
func (s *SimpleSearchTool) StructuralSearch(ctx context.Context, structureType, name string) ([]*SearchResult, error) {
	s.logger.Debug(ctx, "Searching for %s: %s", structureType, name)
	
	var pattern string
	switch structureType {
	case "function":
		// Go function pattern
		pattern = fmt.Sprintf(`func\s+(\([^)]+\)\s+)?%s\s*\(`, regexp.QuoteMeta(name))
	case "type":
		// Go type pattern
		pattern = fmt.Sprintf(`type\s+%s\s+`, regexp.QuoteMeta(name))
	case "interface":
		// Go interface pattern
		pattern = fmt.Sprintf(`type\s+%s\s+interface\s*\{`, regexp.QuoteMeta(name))
	case "struct":
		// Go struct pattern
		pattern = fmt.Sprintf(`type\s+%s\s+struct\s*\{`, regexp.QuoteMeta(name))
	default:
		return nil, fmt.Errorf("unsupported structure type: %s", structureType)
	}
	
	return s.GrepSearch(ctx, pattern, "**/*.go", 5)
}

// SearchWithContext performs a context-aware search
func (s *SimpleSearchTool) SearchWithContext(ctx context.Context, query string, contextClues []string) ([]*SearchResult, error) {
	s.logger.Debug(ctx, "Context-aware search: query=%s, clues=%v", query, contextClues)
	
	// First, find files that contain context clues
	relevantFiles := make(map[string]bool)
	for _, clue := range contextClues {
		files, err := s.findFilesContaining(ctx, clue)
		if err != nil {
			continue
		}
		for _, f := range files {
			relevantFiles[f] = true
		}
	}
	
	// Search for query in relevant files
	var results []*SearchResult
	for file := range relevantFiles {
		re, _ := regexp.Compile(regexp.QuoteMeta(query))
		fileResults, err := s.searchInFile(ctx, file, re, 3)
		if err != nil {
			continue
		}
		
		// Boost score for files with more context matches
		for _, result := range fileResults {
			result.Score = s.calculateContextScore(result, contextClues)
		}
		
		results = append(results, fileResults...)
	}
	
	return results, nil
}

// Helper methods

func (s *SimpleSearchTool) findFiles(pattern string) ([]string, error) {
	if pattern == "" || pattern == "**/*" {
		// Find all code files
		pattern = "**/*.go"
	}
	
	return s.GlobSearch(context.Background(), pattern)
}

func (s *SimpleSearchTool) searchInFile(ctx context.Context, filePath string, re *regexp.Regexp, contextLines int) ([]*SearchResult, error) {
	lines, err := s.ReadFile(ctx, filePath, 0, 0)
	if err != nil {
		return nil, err
	}
	
	var results []*SearchResult
	for i, line := range lines {
		if re.MatchString(line) {
			// Get context lines
			startCtx := max(0, i-contextLines)
			endCtx := min(len(lines), i+contextLines+1)
			
			result := &SearchResult{
				FilePath:   filePath,
				LineNumber: i + 1,
				Line:       line,
				Context:    lines[startCtx:endCtx],
				MatchType:  "pattern",
				Score:      1.0,
			}
			results = append(results, result)
		}
	}
	
	return results, nil
}

func (s *SimpleSearchTool) findFilesContaining(ctx context.Context, text string) ([]string, error) {
	// Simple implementation - in production, use more efficient method
	allFiles, err := s.findFiles("**/*.go")
	if err != nil {
		return nil, err
	}
	
	var matches []string
	for _, file := range allFiles {
		lines, err := s.ReadFile(ctx, file, 0, 0)
		if err != nil {
			continue
		}
		
		for _, line := range lines {
			if strings.Contains(line, text) {
				matches = append(matches, file)
				break
			}
		}
	}
	
	return matches, nil
}

func (s *SimpleSearchTool) readLines(r io.Reader) []string {
	var lines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func (s *SimpleSearchTool) getCachedFile(path string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fileCache[path]
}

func (s *SimpleSearchTool) cacheFile(path string, lines []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileCache[path] = lines
}

func (s *SimpleSearchTool) fuzzyScore(queryTokens []string, text string) float64 {
	textTokens := tokenize(text)
	if len(textTokens) == 0 {
		return 0
	}
	
	matches := 0
	for _, qt := range queryTokens {
		for _, tt := range textTokens {
			if strings.Contains(tt, qt) || strings.Contains(qt, tt) {
				matches++
				break
			}
		}
	}
	
	return float64(matches) / float64(len(queryTokens))
}

func (s *SimpleSearchTool) calculateContextScore(result *SearchResult, contextClues []string) float64 {
	score := result.Score
	contextText := strings.Join(result.Context, " ")
	
	for _, clue := range contextClues {
		if strings.Contains(contextText, clue) {
			score += 0.1
		}
	}
	
	return min(score, 1.0)
}

// Utility functions

func tokenize(text string) []string {
	// Simple tokenization - split on non-alphanumeric
	re := regexp.MustCompile(`\W+`)
	tokens := re.Split(text, -1)
	
	// Filter empty tokens
	var filtered []string
	for _, t := range tokens {
		if t != "" {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

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