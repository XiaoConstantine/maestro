package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// ChunkingStrategy represents different approaches to chunking code.
type ChunkingStrategy string

const (
	// ChunkByFunction splits code primarily at function boundaries.
	ChunkByFunction ChunkingStrategy = "function"
	// ChunkBySize splits code into fixed-size chunks.
	ChunkBySize ChunkingStrategy = "size"
	// ChunkByLogic attempts to split at logical boundaries (classes, blocks, etc).
	ChunkByLogic ChunkingStrategy = "logic"
)

// ChunkConfig provides configuration options for code chunking.
type ChunkConfig struct {
	// Strategy determines how code should be split into chunks
	Strategy ChunkingStrategy

	// MaxTokens limits the size of each chunk in tokens
	// Recommended range: 500-2000. Larger values mean fewer chunks but more token usage
	MaxTokens int

	// ContextLines determines how many lines of surrounding context to include
	// Recommended range: 3-15. More context helps understanding but increases storage
	ContextLines int

	// MaxBytes is for byte-size limitingOptional byte limit, only used for specific cases
	MaxBytes *int

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

	GenerateDescriptions bool
}

// ChunkConfigOption is a function that modifies a ChunkConfig.
type ChunkConfigOption func(*ChunkConfig)

// NewChunkConfig creates a new chunking configuration with the given options.
func NewChunkConfig(options ...ChunkConfigOption) (*ChunkConfig, error) {
	// Start with sensible defaults
	config := &ChunkConfig{
		Strategy:             ChunkByFunction,
		MaxTokens:            1500,
		MaxBytes:             nil,
		ContextLines:         5,
		OverlapLines:         2,
		MinChunkSize:         10,
		LanguageSpecific:     make(map[string]interface{}),
		FilePatterns:         make(map[string]ChunkConfig),
		fileMetadata:         make(map[string]interface{}),
		GenerateDescriptions: true,
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

// Configuration option functions for users to customize behavior.
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

func WithMaxBytes(bytes int) ChunkConfigOption {
	return func(c *ChunkConfig) {
		c.MaxBytes = &bytes
	}
}

func WithGenerateDescriptions(generate bool) ChunkConfigOption {
	return func(c *ChunkConfig) {
		c.GenerateDescriptions = generate
	}
}

// Validation method to ensure configuration is valid.
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

// chunkfile is the main entry point for chunking code files.
func chunkfile(ctx context.Context, content string, changes string, config *ChunkConfig) ([]ReviewChunk, error) {
	logger := logging.GetLogger()

	// First validate the input
	if content == "" {
		return nil, fmt.Errorf("empty content provided")
	}
	if config == nil {
		return nil, fmt.Errorf("chunk configuration is required")
	}

	if config.fileMetadata == nil {
		return nil, fmt.Errorf("file metadata is required")
	}
	// Get file path from metadata for pattern matching
	filepathInterface, exists := config.fileMetadata["file_path"]
	if !exists {
		return nil, fmt.Errorf("file_path missing from metadata")
	}
	filename, ok := filepathInterface.(string)
	if !ok {
		return nil, fmt.Errorf("file_path in metadata is not a string: %T", filepathInterface)
	}
	// Check if we have a specific configuration for this file type
	for pattern, patternConfig := range config.FilePatterns {
		if matched, _ := filepath.Match(pattern, filename); matched {
			logger.Info(ctx, "see if matched here for test: %v", matched)
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

	// Select chunking strategy based on configuration
	var chunks []ReviewChunk
	var err error
	switch config.Strategy {
	case ChunkByFunction:
		// only chunking correct file type
		if filepath.Ext(filename) == ".go" {
			chunks, err = chunkByFunction(content, config)
		} else {
			chunks, err = chunkBySize(content, config)
		}
	case ChunkByLogic:
		chunks, err = chunkByLogic(content, config)
	default:
		chunks, err = chunkBySize(content, config)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to chunk file: %w", err)
	}
	// Apply minimum chunk size
	initialChunks := mergeSmallChunks(chunks, config.MinChunkSize)

	// Now handle oversized chunks directly instead of using enforceMaxTokens
	var finalChunks []ReviewChunk
	for _, chunk := range initialChunks {
		tokens := estimatetokens(chunk.content)
		if tokens <= config.MaxTokens {
			finalChunks = append(finalChunks, chunk)
			continue
		}

		// Split oversized chunk
		lines := strings.Split(chunk.content, "\n")
		currentChunk := ReviewChunk{
			startline: chunk.startline,
			filePath:  chunk.filePath,
		}
		currentLines := []string{}
		currentTokens := 0

		for i, line := range lines {
			lineTokens := estimatetokens(line)
			if currentTokens+lineTokens > config.MaxTokens && len(currentLines) > 0 {
				// Finalize current chunk
				currentChunk.content = strings.Join(currentLines, "\n")
				currentChunk.endline = chunk.startline + i - 1
				finalChunks = append(finalChunks, currentChunk)

				// Start new chunk
				currentLines = []string{}
				currentTokens = 0
				currentChunk = ReviewChunk{
					startline: chunk.startline + i,
					filePath:  chunk.filePath,
				}
			}
			currentLines = append(currentLines, line)
			currentTokens += lineTokens
		}

		// Add final subchunk if there's content
		if len(currentLines) > 0 {
			currentChunk.content = strings.Join(currentLines, "\n")
			currentChunk.endline = chunk.endline
			finalChunks = append(finalChunks, currentChunk)
		}
	}

	// Add context and overlap
	finalChunks = addContextAndOverlap(finalChunks, content, config)

	// Extract relevant changes
	for i := range finalChunks {
		finalChunks[i].changes = ExtractRelevantChanges(changes, finalChunks[i].startline, finalChunks[i].endline)
		if config.GenerateDescriptions {
			if estimatetokens(finalChunks[i].content) < 1000 {
				description, err := generateChunkDescription(ctx, finalChunks[i].content)
				if err != nil {
					logger.Warn(ctx, "Failed to generate description: %v", err)
				} else {
					logger.Debug(ctx, "Generated description: %v for chunk: %d", description, i)
					finalChunks[i].description = description
				}
			}
		}
	}

	logger.Debug(ctx, "Created %d chunks for file %s", len(finalChunks), filename)
	return finalChunks, nil
}

// chunkByFunction splits code at function boundaries using AST parsing.
func chunkByFunction(content string, config *ChunkConfig) ([]ReviewChunk, error) {
	// Create a file set and parse the content
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file: %w", err)
	}
	var chunks []ReviewChunk

	// Process function declarations with their associated comments
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			// Get function position
			startPos := fset.Position(d.Pos())
			endPos := fset.Position(d.End())

			// Include doc comments if present
			var codeWithComments string
			if d.Doc != nil {
				docPos := fset.Position(d.Doc.Pos())
				codeWithComments = content[d.Doc.Pos()-1 : d.End()-1]
				startPos = docPos // Update start position to include comments
			} else {
				codeWithComments = content[d.Pos()-1 : d.End()-1]
			}

			// Create chunk with comment context
			chunk := createCompleteChunk(
				codeWithComments,
				startPos.Line,
				endPos.Line,
				content,
				config,
				config.fileMetadata["file_path"].(string),
				0, // Will be updated later
				"",
			)
			chunks = append(chunks, chunk)

		case *ast.GenDecl:
			// Handle type declarations, constants, variables
			if d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						// Extract type with comments
						startPos := fset.Position(ts.Pos())
						endPos := fset.Position(ts.End())

						var codeWithComments string
						// Try spec-level doc first, then declaration-level
						if ts.Doc != nil {
							docPos := fset.Position(ts.Doc.Pos())
							codeWithComments = content[ts.Doc.Pos()-1 : ts.End()-1]
							startPos = docPos
						} else if d.Doc != nil {
							docPos := fset.Position(d.Doc.Pos())
							codeWithComments = content[d.Doc.Pos()-1 : ts.End()-1]
							startPos = docPos
						} else {
							codeWithComments = content[ts.Pos()-1 : ts.End()-1]
						}

						chunk := createCompleteChunk(
							codeWithComments,
							startPos.Line,
							endPos.Line,
							content,
							config,
							config.fileMetadata["file_path"].(string),
							0,
							"",
						)
						chunks = append(chunks, chunk)
					}
				}
			}
		}
	}
	for i := range chunks {
		chunks[i].totalChunks = len(chunks)
	}

	return chunks, nil
}

// chunkByLogic attempts to split at logical boundaries like classes, blocks, imports.
func chunkByLogic(content string, config *ChunkConfig) ([]ReviewChunk, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file: %w", err)
	}
	// First pass to count logical blocks for accurate totalChunks
	var logicalBlockCount int
	ast.Inspect(file, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.GenDecl, *ast.TypeSpec:
			logicalBlockCount++
		}
		return true
	})
	filePath := ""
	if path, ok := config.fileMetadata["file_path"].(string); ok {
		filePath = path
	}

	changes := ""
	if changesData, ok := config.fileMetadata["changes"].(string); ok {
		changes = changesData
	}
	var chunks []ReviewChunk

	// Helper to finalize current chunk
	finishChunk := func(chunkContent string, start, end token.Pos) {
		if chunkContent == "" {
			return
		}

		startPos := fset.Position(start)
		endPos := fset.Position(end)

		chunk := createCompleteChunk(
			chunkContent,
			startPos.Line,
			endPos.Line,
			content,
			config,
			filePath,
			logicalBlockCount,
			changes,
		)
		chunks = append(chunks, chunk)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			return true
		}

		switch node := n.(type) {
		case *ast.GenDecl:
			// Handle imports, constants, and type declarations
			if node.Tok == token.IMPORT || node.Tok == token.CONST || node.Tok == token.TYPE {
				finishChunk(content[node.Pos()-1:node.End()-1], node.Pos(), node.End())
				return false
			}
		case *ast.TypeSpec:
			// Handle type definitions and interfaces
			finishChunk(content[node.Pos()-1:node.End()-1], node.Pos(), node.End())
			return false
		}
		return true
	})

	return chunks, nil
}

// chunkBySize splits code into fixed-size chunks without breaking syntax.
func chunkBySize(content string, config *ChunkConfig) ([]ReviewChunk, error) {
	if content == "" {
		return nil, fmt.Errorf("empty content provided")
	}
	if config == nil {
		return nil, fmt.Errorf("nil config provided")
	}
	if config.fileMetadata == nil {
		return nil, fmt.Errorf("file metadata is required")
	}
	// Get filepath with type checking
	filepathInterface, exists := config.fileMetadata["file_path"]
	if !exists {
		return nil, fmt.Errorf("file_path missing from metadata")
	}

	filepath, ok := filepathInterface.(string)
	if !ok {
		return nil, fmt.Errorf("file_path in metadata is not a string: %T", filepathInterface)
	}
	var changes string
	if changesInterface, exists := config.fileMetadata["changes"]; exists {
		if changesStr, ok := changesInterface.(string); ok {
			changes = changesStr
		}
		// If type assertion fails, changes remains empty string
	}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("no lines to process")
	}

	estimatedChunks := (len(lines) + config.MaxTokens - 1) / config.MaxTokens
	var chunks []ReviewChunk
	var currentLines []string
	currentTokens := 0
	currentBytes := 0
	chunkStartLine := 1

	for i, line := range lines {
		lineTokens := estimatetokens(line)
		lineBytes := len(line)

		// If adding this line would exceed max tokens, finish the current chunk
		if (currentTokens+lineTokens > config.MaxTokens || config.MaxBytes != nil && currentBytes+lineBytes > *config.MaxBytes) && len(currentLines) > 0 {
			chunkContent := strings.Join(currentLines, "\n")

			chunk := createCompleteChunk(
				chunkContent,
				chunkStartLine,
				i, // Current line is where this chunk ends
				content,
				config,
				filepath,
				estimatedChunks,
				changes,
			)
			chunks = append(chunks, chunk)

			// Start new chunk
			chunkStartLine = i + 1
			currentLines = []string{}
			currentTokens = 0
		}

		currentLines = append(currentLines, line)
		currentTokens += lineTokens
		currentBytes += lineBytes
	}

	// Handle the final chunk if there's remaining content
	if len(currentLines) > 0 {
		chunkContent := strings.Join(currentLines, "\n")
		chunk := createCompleteChunk(
			chunkContent,
			chunkStartLine,
			len(lines), // Last line of the file
			content,
			config,
			filepath,
			estimatedChunks,
			changes,
		)
		chunks = append(chunks, chunk)
	}
	logging.GetLogger().Debug(context.Background(), "for file: %s, created: %d chunks", filepath, len(chunks))

	return chunks, nil
}

// Helper functions for chunk processing.
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
			var overlap string
			if overlapStart > 1 {
				overlap = strings.Join(lines[overlapStart-1:chunks[i-1].endline], "\n")
			} else {
				overlap = strings.Join(lines[0:chunks[i-1].endline], "\n")
			}
			chunks[i].content = overlap + "\n" + chunks[i].content
			chunks[i].startline = overlapStart
		}
	}

	return chunks
}

func createCompleteChunk(
	content string,
	startLine, endLine int,
	fullContent string,
	config *ChunkConfig,
	filePath string,
	estimatedTotalChunks int,
	changes string,
) ReviewChunk {
	chunk := ReviewChunk{
		content:         content,
		startline:       startLine,
		endline:         endLine,
		leadingcontext:  "", // Context will be added in addContextAndOverlap
		trailingcontext: "", // Context will be added in addContextAndOverlap
		changes:         ExtractRelevantChanges(changes, startLine, endLine),
		filePath:        filePath,
		totalChunks:     estimatedTotalChunks,
	}

	return chunk
}

// generateChunkDescription creates a natural language description of a code chunk.
func generateChunkDescription(ctx context.Context, chunk string) (string, error) {
	llm := core.GetDefaultLLM()
	prompt := fmt.Sprintf(`Generate a concise natural language description of what this Go code does:

%s

Description:`, chunk)

	resp, err := llm.Generate(ctx, prompt, core.WithMaxTokens(100),
		core.WithTemperature(0.6))

	if err != nil {
		return "", fmt.Errorf("failed to generate description: %w", err)
	}

	return strings.TrimSpace(resp.Content), nil
}
