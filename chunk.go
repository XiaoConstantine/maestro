package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// ChunkingStrategy represents different approaches to chunking code
type ChunkingStrategy string

const (
	// ChunkByFunction splits code primarily at function boundaries
	ChunkByFunction ChunkingStrategy = "function"
	// ChunkBySize splits code into fixed-size chunks
	ChunkBySize ChunkingStrategy = "size"
	// ChunkByLogic attempts to split at logical boundaries (classes, blocks, etc)
	ChunkByLogic ChunkingStrategy = "logic"
)

// ChunkConfig provides configuration options for code chunking
type ChunkConfig struct {
	// Strategy determines how code should be split into chunks
	Strategy ChunkingStrategy

	// MaxTokens limits the size of each chunk in tokens
	// Recommended range: 500-2000. Larger values mean fewer chunks but more token usage
	MaxTokens int

	// ContextLines determines how many lines of surrounding context to include
	// Recommended range: 3-15. More context helps understanding but increases storage
	ContextLines int

	// OverlapLines controls how many lines overlap between chunks
	// Recommended range: 2-10. More overlap helps maintain context but increases storage
	OverlapLines int

	// MinChunkSize sets the minimum meaningful chunk size in lines
	// Chunks smaller than this will be merged with neighbors
	MinChunkSize int

	// LanguageSpecific contains language-specific chunking rules
	LanguageSpecific map[string]interface{}

	// FilePatterns allows different configs for different file patterns
	FilePatterns map[string]ChunkConfig

	// Internal use
	fileMetadata map[string]interface{}
}

// ChunkConfigOption is a function that modifies a ChunkConfig
type ChunkConfigOption func(*ChunkConfig)

// NewChunkConfig creates a new chunking configuration with the given options
func NewChunkConfig(options ...ChunkConfigOption) (*ChunkConfig, error) {
	// Start with sensible defaults
	config := &ChunkConfig{
		Strategy:         ChunkByLogic,
		MaxTokens:        1000,
		ContextLines:     5,
		OverlapLines:     2,
		MinChunkSize:     10,
		LanguageSpecific: make(map[string]interface{}),
		FilePatterns:     make(map[string]ChunkConfig),
		fileMetadata:     make(map[string]interface{}),
	}

	// Apply all options
	for _, option := range options {
		option(config)
	}

	// Validate the configuration
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("invalid chunk configuration: %w", err)
	}

	return config, nil
}

// Configuration option functions for users to customize behavior
func WithStrategy(strategy ChunkingStrategy) ChunkConfigOption {
	return func(c *ChunkConfig) {
		c.Strategy = strategy
	}
}

func WithMaxTokens(tokens int) ChunkConfigOption {
	return func(c *ChunkConfig) {
		c.MaxTokens = tokens
	}
}

func WithContextLines(lines int) ChunkConfigOption {
	return func(c *ChunkConfig) {
		c.ContextLines = lines
	}
}

func WithOverlapLines(lines int) ChunkConfigOption {
	return func(c *ChunkConfig) {
		c.OverlapLines = lines
	}
}

func WithFilePattern(pattern string, config ChunkConfig) ChunkConfigOption {
	return func(c *ChunkConfig) {
		c.FilePatterns[pattern] = config
	}
}

// Validation method to ensure configuration is valid
func (c *ChunkConfig) validate() error {
	if c.MaxTokens < 100 || c.MaxTokens > 4000 {
		return fmt.Errorf("MaxTokens must be between 100 and 4000")
	}
	if c.ContextLines < 0 || c.ContextLines > 50 {
		return fmt.Errorf("ContextLines must be between 0 and 50")
	}
	if c.OverlapLines < 0 || c.OverlapLines > c.ContextLines {
		return fmt.Errorf("OverlapLines must be between 0 and ContextLines")
	}
	if c.MinChunkSize < 5 {
		return fmt.Errorf("MinChunkSize must be at least 5 lines")
	}
	return nil
}

// chunkfile is the main entry point for chunking code files
func chunkfile(ctx context.Context, content string, changes string, config *ChunkConfig) ([]ReviewChunk, error) {
	logger := logging.GetLogger()

	// First validate the input
	if content == "" {
		return nil, fmt.Errorf("empty content provided")
	}
	if config == nil {
		return nil, fmt.Errorf("chunk configuration is required")
	}

	// Get file path from metadata for pattern matching
	filename, ok := config.fileMetadata["file_path"].(string)
	if !ok {
		return nil, fmt.Errorf("file path not found in metadata")
	}

	// Check if we have a specific configuration for this file type
	for pattern, patternConfig := range config.FilePatterns {
		if matched, _ := filepath.Match(pattern, filename); matched {
			// Create a new config that merges pattern-specific settings
			newConfig := *config // Copy base config
			// Override with pattern-specific settings
			newConfig.Strategy = patternConfig.Strategy
			newConfig.MaxTokens = patternConfig.MaxTokens
			newConfig.ContextLines = patternConfig.ContextLines
			newConfig.OverlapLines = patternConfig.OverlapLines
			config = &newConfig
			break
		}
	}

	logger.Debug(ctx, "Chunking file %s using strategy: %s", filename, config.Strategy)

	// Select chunking strategy based on configuration
	var chunks []ReviewChunk
	var err error
	switch config.Strategy {
	case ChunkByFunction:
		chunks, err = chunkByFunction(content, config)
	case ChunkByLogic:
		chunks, err = chunkByLogic(content, config)
	default:
		chunks, err = chunkBySize(content, config)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to chunk file: %w", err)
	}

	// Apply minimum chunk size - merge small chunks
	chunks = mergeSmallChunks(chunks, config.MinChunkSize)

	// Ensure chunks don't exceed maximum token limit
	chunks, err = enforceMaxTokens(chunks, config.MaxTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to enforce token limits: %w", err)
	}

	// Add context and overlap between chunks
	chunks = addContextAndOverlap(chunks, content, config)

	// Extract relevant changes for each chunk
	for i := range chunks {
		chunks[i].changes = ExtractRelevantChanges(changes, chunks[i].startline, chunks[i].endline)
	}

	logger.Debug(ctx, "Created %d chunks for file %s", len(chunks), filename)
	return chunks, nil
}

// chunkByFunction splits code at function boundaries using AST parsing
func chunkByFunction(content string, config *ChunkConfig) ([]ReviewChunk, error) {
	// Create a file set and parse the content
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file: %w", err)
	}

	var chunks []ReviewChunk
	ast.Inspect(file, func(n ast.Node) bool {
		// Look for function declarations
		if fn, ok := n.(*ast.FuncDecl); ok {
			// Get the position information
			start := fset.Position(fn.Pos())
			end := fset.Position(fn.End())

			chunks = append(chunks, ReviewChunk{
				startline: start.Line,
				endline:   end.Line,
				content:   content[fn.Pos()-1 : fn.End()-1],
			})
		}
		return true
	})

	return chunks, nil
}

// chunkByLogic attempts to split at logical boundaries like classes, blocks, imports
func chunkByLogic(content string, config *ChunkConfig) ([]ReviewChunk, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file: %w", err)
	}

	var chunks []ReviewChunk
	var currentChunk *ReviewChunk

	// Helper to finalize current chunk
	finishChunk := func() {
		if currentChunk != nil && currentChunk.content != "" {
			chunks = append(chunks, *currentChunk)
			currentChunk = nil
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			return true
		}

		switch node := n.(type) {
		case *ast.GenDecl:
			// Handle imports, constants, and type declarations
			if node.Tok == token.IMPORT || node.Tok == token.CONST || node.Tok == token.TYPE {
				finishChunk()
				start := fset.Position(node.Pos())
				end := fset.Position(node.End())
				chunks = append(chunks, ReviewChunk{
					startline: start.Line,
					endline:   end.Line,
					content:   content[node.Pos()-1 : node.End()-1],
				})
				return false
			}
		case *ast.TypeSpec:
			// Handle type definitions and interfaces
			finishChunk()
			start := fset.Position(node.Pos())
			end := fset.Position(node.End())
			chunks = append(chunks, ReviewChunk{
				startline: start.Line,
				endline:   end.Line,
				content:   content[node.Pos()-1 : node.End()-1],
			})
			return false
		}
		return true
	})

	finishChunk()
	return chunks, nil
}

// chunkBySize splits code into fixed-size chunks without breaking syntax
func chunkBySize(content string, config *ChunkConfig) ([]ReviewChunk, error) {
	lines := strings.Split(content, "\n")
	var chunks []ReviewChunk

	currentChunk := ReviewChunk{startline: 1}
	currentLines := []string{}
	currentTokens := 0

	for i, line := range lines {
		lineTokens := estimatetokens(line)

		// If adding this line would exceed max tokens, finish the current chunk
		if currentTokens+lineTokens > config.MaxTokens && len(currentLines) > 0 {
			currentChunk.content = strings.Join(currentLines, "\n")
			currentChunk.endline = i
			chunks = append(chunks, currentChunk)

			// Start new chunk
			currentChunk = ReviewChunk{startline: i + 1}
			currentLines = []string{}
			currentTokens = 0
		}

		currentLines = append(currentLines, line)
		currentTokens += lineTokens
	}

	// Add final chunk if there's content
	if len(currentLines) > 0 {
		currentChunk.content = strings.Join(currentLines, "\n")
		currentChunk.endline = len(lines)
		chunks = append(chunks, currentChunk)
	}

	return chunks, nil
}

// Helper functions for chunk processing

func mergeSmallChunks(chunks []ReviewChunk, minSize int) []ReviewChunk {
	if len(chunks) <= 1 {
		return chunks
	}

	var result []ReviewChunk
	current := chunks[0]

	for i := 1; i < len(chunks); i++ {
		lines := current.endline - current.startline + 1
		if lines < minSize {
			// Merge with next chunk
			current.content += "\n" + chunks[i].content
			current.endline = chunks[i].endline
		} else {
			result = append(result, current)
			current = chunks[i]
		}
	}

	// Add the last chunk
	result = append(result, current)
	return result
}

func enforceMaxTokens(chunks []ReviewChunk, maxTokens int) ([]ReviewChunk, error) {
	var result []ReviewChunk

	for _, chunk := range chunks {
		tokens := estimatetokens(chunk.content)
		if tokens > maxTokens {
			// Split the chunk further using chunkBySize
			subConfig := &ChunkConfig{MaxTokens: maxTokens}
			subChunks, err := chunkBySize(chunk.content, subConfig)
			if err != nil {
				return nil, err
			}

			// Adjust line numbers for sub-chunks
			offset := chunk.startline - 1
			for i := range subChunks {
				subChunks[i].startline += offset
				subChunks[i].endline += offset
			}

			result = append(result, subChunks...)
		} else {
			result = append(result, chunk)
		}
	}

	return result, nil
}

func addContextAndOverlap(chunks []ReviewChunk, fullContent string, config *ChunkConfig) []ReviewChunk {
	lines := strings.Split(fullContent, "\n")

	for i := range chunks {
		// Add leading context
		contextStart := max(0, chunks[i].startline-config.ContextLines)
		chunks[i].leadingcontext = strings.Join(lines[contextStart:chunks[i].startline-1], "\n")

		// Add trailing context
		contextEnd := min(len(lines), chunks[i].endline+config.ContextLines)
		chunks[i].trailingcontext = strings.Join(lines[chunks[i].endline:contextEnd], "\n")

		// Add overlap with previous chunk if needed
		if i > 0 && config.OverlapLines > 0 {
			overlapStart := max(chunks[i-1].endline-config.OverlapLines, chunks[i-1].startline)
			overlapEnd := chunks[i-1].endline
			overlap := strings.Join(lines[overlapStart-1:overlapEnd], "\n")
			chunks[i].content = overlap + "\n" + chunks[i].content
			chunks[i].startline = overlapStart
		}
	}

	return chunks
}
