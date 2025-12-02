// Package chunk provides code chunking functionality for review processing.
package chunk

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
)

// ConfigOption is a function that modifies a ChunkConfig.
type ConfigOption func(*types.ChunkConfig)

// NewConfig creates a new chunking configuration with the given options.
func NewConfig(options ...ConfigOption) (*types.ChunkConfig, error) {
	// Start with sensible defaults
	config := &types.ChunkConfig{
		Strategy:             types.ChunkByFunction,
		MaxTokens:            4000,
		MaxBytes:             nil,
		ContextLines:         GetContextLines(),
		OverlapLines:         2,
		MinChunkSize:         10,
		LanguageSpecific:     make(map[string]interface{}),
		FilePatterns:         make(map[string]types.ChunkConfig),
		FileMetadata:         make(map[string]interface{}),
		GenerateDescriptions: true,
	}

	// Apply all options
	for _, option := range options {
		option(config)
	}

	// Validate the configuration
	if err := Validate(config); err != nil {
		return nil, fmt.Errorf("invalid chunk configuration: %w", err)
	}

	return config, nil
}

// Configuration option functions for users to customize behavior.

// WithStrategy sets the chunking strategy.
func WithStrategy(strategy types.ChunkingStrategy) ConfigOption {
	return func(c *types.ChunkConfig) {
		c.Strategy = strategy
	}
}

// WithMaxTokens sets the maximum tokens per chunk.
func WithMaxTokens(tokens int) ConfigOption {
	return func(c *types.ChunkConfig) {
		c.MaxTokens = tokens
	}
}

// WithContextLines sets the number of context lines.
func WithContextLines(lines int) ConfigOption {
	return func(c *types.ChunkConfig) {
		c.ContextLines = lines
	}
}

// WithOverlapLines sets the number of overlap lines.
func WithOverlapLines(lines int) ConfigOption {
	return func(c *types.ChunkConfig) {
		c.OverlapLines = lines
	}
}

// WithFilePattern sets a file pattern specific configuration.
func WithFilePattern(pattern string, config types.ChunkConfig) ConfigOption {
	return func(c *types.ChunkConfig) {
		c.FilePatterns[pattern] = config
	}
}

// WithMaxBytes sets the maximum bytes per chunk.
func WithMaxBytes(bytes int) ConfigOption {
	return func(c *types.ChunkConfig) {
		c.MaxBytes = &bytes
	}
}

// WithGenerateDescriptions sets whether to generate descriptions.
func WithGenerateDescriptions(generate bool) ConfigOption {
	return func(c *types.ChunkConfig) {
		c.GenerateDescriptions = generate
	}
}

// WithFilePath sets the file path in metadata.
func WithFilePath(path string) ConfigOption {
	return func(c *types.ChunkConfig) {
		if c.FileMetadata == nil {
			c.FileMetadata = make(map[string]interface{})
		}
		c.FileMetadata["file_path"] = path
	}
}

// Validate ensures configuration is valid.
func Validate(c *types.ChunkConfig) error {
	if c.MaxTokens < 100 || c.MaxTokens > 1000000 {
		return fmt.Errorf("MaxTokens must be between 100 and 1000000")
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

// ChunkFile is the main entry point for chunking code files.
func ChunkFile(ctx context.Context, content string, changes string, config *types.ChunkConfig) ([]types.ReviewChunk, error) {
	logger := logging.GetLogger()

	// First validate the input
	if content == "" {
		return nil, fmt.Errorf("empty content provided")
	}
	if config == nil {
		return nil, fmt.Errorf("chunk configuration is required")
	}

	if config.FileMetadata == nil {
		return nil, fmt.Errorf("file metadata is required")
	}
	// Get file path from metadata for pattern matching
	filepathInterface, exists := config.FileMetadata["file_path"]
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
	var chunks []types.ReviewChunk
	var err error
	switch config.Strategy {
	case types.ChunkByFunction:
		// only chunking correct file type
		if filepath.Ext(filename) == ".go" {
			chunks, err = chunkByFunction(content, config)
		} else {
			chunks, err = chunkBySize(content, config)
		}
	case types.ChunkByLogic:
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
	var finalChunks []types.ReviewChunk
	for _, chunk := range initialChunks {
		tokens := EstimateTokens(chunk.Content)
		if tokens <= config.MaxTokens {
			finalChunks = append(finalChunks, chunk)
			continue
		}

		// Split oversized chunk
		lines := strings.Split(chunk.Content, "\n")
		currentChunk := types.ReviewChunk{
			StartLine: chunk.StartLine,
			FilePath:  chunk.FilePath,
		}
		currentLines := []string{}
		currentTokens := 0

		for i, line := range lines {
			lineTokens := EstimateTokens(line)
			if currentTokens+lineTokens > config.MaxTokens && len(currentLines) > 0 {
				// Finalize current chunk
				currentChunk.Content = strings.Join(currentLines, "\n")
				currentChunk.EndLine = chunk.StartLine + i - 1
				finalChunks = append(finalChunks, currentChunk)

				// Start new chunk
				currentLines = []string{}
				currentTokens = 0
				currentChunk = types.ReviewChunk{
					StartLine: chunk.StartLine + i,
					FilePath:  chunk.FilePath,
				}
			}
			currentLines = append(currentLines, line)
			currentTokens += lineTokens
		}

		// Add final subchunk if there's content
		if len(currentLines) > 0 {
			currentChunk.Content = strings.Join(currentLines, "\n")
			currentChunk.EndLine = chunk.EndLine
			finalChunks = append(finalChunks, currentChunk)
		}
	}

	// Add context and overlap
	finalChunks = addContextAndOverlap(finalChunks, content, config)

	// Extract relevant changes
	for i := range finalChunks {
		finalChunks[i].Changes = ExtractRelevantChanges(changes, finalChunks[i].StartLine, finalChunks[i].EndLine)
	}

	// Generate LLM descriptions in parallel if explicitly enabled via env var
	if config.GenerateDescriptions && isLLMChunkDescriptionsEnabled() {
		type descResult struct {
			idx  int
			desc string
			err  error
		}

		var wg sync.WaitGroup
		descChan := make(chan descResult, len(finalChunks))
		semaphore := make(chan struct{}, 8) // Max 8 concurrent descriptions

		for i := range finalChunks {
			if EstimateTokens(finalChunks[i].Content) < 1000 {
				wg.Add(1)
				go func(idx int, content string) {
					defer wg.Done()

					// Acquire semaphore
					semaphore <- struct{}{}
					defer func() { <-semaphore }()

					description, err := generateChunkDescription(ctx, content)
					descChan <- descResult{idx: idx, desc: description, err: err}
				}(i, finalChunks[i].Content)
			}
		}

		// Close channel when all goroutines complete
		go func() {
			wg.Wait()
			close(descChan)
		}()

		// Collect results
		for result := range descChan {
			if result.err != nil {
				logger.Warn(ctx, "Failed to generate description: %v", result.err)
			} else {
				logger.Debug(ctx, "Generated description: %v for chunk: %d", result.desc, result.idx)
				finalChunks[result.idx].Description = result.desc
			}
		}
	}

	logger.Debug(ctx, "Created %d chunks for file %s", len(finalChunks), filename)
	return finalChunks, nil
}

// EstimateTokens provides a simple token estimation.
func EstimateTokens(text string) int {
	// Simple estimation: ~1.3 tokens per word
	words := len(strings.Fields(text))
	return int(float64(words) * 1.3)
}

// ExtractRelevantChanges extracts the relevant changes for a line range.
func ExtractRelevantChanges(changes string, startline, endline int) string {
	if changes == "" {
		return ""
	}

	// Parse the git diff and extract changes for the line range
	lines := strings.Split(changes, "\n")
	var relevantLines []string
	inRange := false
	currentLine := 0

	hunkHeaderRegex := regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

	for _, line := range lines {
		if matches := hunkHeaderRegex.FindStringSubmatch(line); matches != nil {
			currentLine, _ = strconv.Atoi(matches[1])
			currentLine-- // Will be incremented before use
			continue
		}

		if strings.HasPrefix(line, "-") {
			// Deleted line doesn't increment currentLine
			if inRange {
				relevantLines = append(relevantLines, line)
			}
			continue
		}

		currentLine++

		if currentLine >= startline && currentLine <= endline {
			inRange = true
			relevantLines = append(relevantLines, line)
		} else if currentLine > endline {
			inRange = false
		}
	}

	return strings.Join(relevantLines, "\n")
}

// chunkByFunction splits code at function boundaries using AST parsing.
func chunkByFunction(content string, config *types.ChunkConfig) ([]types.ReviewChunk, error) {
	// Create a file set and parse the content
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, parser.ParseComments)
	if err != nil {
		// Fallback: if Go parser fails, fall back to size-based chunking
		logging.GetLogger().Warn(context.Background(), "Go parser failed in chunkByFunction, falling back to size-based chunking: %v", err)
		return chunkBySize(content, config)
	}
	var chunks []types.ReviewChunk

	// Extract file metadata for pseudo-descriptions
	filePath, _ := config.FileMetadata["file_path"].(string)
	pkgName := file.Name.Name
	baseName := filepath.Base(filePath)

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
				filePath,
				0, // Will be updated later
				"",
			)

			// Generate AST-based pseudo-description (zero LLM cost)
			chunk.Description = buildFuncPseudoDescription(baseName, pkgName, d)
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
							filePath,
							0,
							"",
						)

						// Generate AST-based pseudo-description for types
						chunk.Description = buildTypePseudoDescription(baseName, pkgName, ts, d.Doc)
						chunks = append(chunks, chunk)
					}
				}
			}
		}
	}
	for i := range chunks {
		chunks[i].TotalChunks = len(chunks)
	}

	return chunks, nil
}

// buildFuncPseudoDescription creates a pseudo-description from AST function info.
func buildFuncPseudoDescription(fileName, pkgName string, fn *ast.FuncDecl) string {
	var b strings.Builder

	b.WriteString("Go function ")

	// Add receiver if present (method vs function)
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv := fn.Recv.List[0]
		recvType := formatExprType(recv.Type)
		b.WriteString("(")
		b.WriteString(recvType)
		b.WriteString(").")
	}

	b.WriteString(fn.Name.Name)
	b.WriteString(" in package ")
	b.WriteString(pkgName)
	b.WriteString(" (")
	b.WriteString(fileName)
	b.WriteString(")")

	// Add parameter hints
	if fn.Type.Params != nil && len(fn.Type.Params.List) > 0 {
		b.WriteString(". Parameters: ")
		var params []string
		for _, p := range fn.Type.Params.List {
			typeName := formatExprType(p.Type)
			for _, name := range p.Names {
				params = append(params, name.Name+" "+typeName)
			}
			if len(p.Names) == 0 {
				params = append(params, typeName)
			}
		}
		b.WriteString(strings.Join(params, ", "))
	}

	// Add return type hints
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		b.WriteString(". Returns: ")
		var results []string
		for _, r := range fn.Type.Results.List {
			results = append(results, formatExprType(r.Type))
		}
		b.WriteString(strings.Join(results, ", "))
	}

	// Add doc comment snippet if present
	if fn.Doc != nil {
		doc := strings.TrimSpace(fn.Doc.Text())
		if doc != "" {
			// Truncate long comments
			if len(doc) > 200 {
				doc = doc[:200] + "..."
			}
			b.WriteString(". ")
			b.WriteString(doc)
		}
	}

	return b.String()
}

// buildTypePseudoDescription creates a pseudo-description from AST type info.
func buildTypePseudoDescription(fileName, pkgName string, ts *ast.TypeSpec, doc *ast.CommentGroup) string {
	var b strings.Builder

	// Determine type kind
	var kind string
	switch ts.Type.(type) {
	case *ast.StructType:
		kind = "struct"
	case *ast.InterfaceType:
		kind = "interface"
	case *ast.ArrayType:
		kind = "array type"
	case *ast.MapType:
		kind = "map type"
	case *ast.FuncType:
		kind = "function type"
	default:
		kind = "type"
	}

	b.WriteString("Go ")
	b.WriteString(kind)
	b.WriteString(" ")
	b.WriteString(ts.Name.Name)
	b.WriteString(" in package ")
	b.WriteString(pkgName)
	b.WriteString(" (")
	b.WriteString(fileName)
	b.WriteString(")")

	// Add doc comment snippet
	docGroup := ts.Doc
	if docGroup == nil {
		docGroup = doc
	}
	if docGroup != nil {
		docText := strings.TrimSpace(docGroup.Text())
		if docText != "" {
			if len(docText) > 200 {
				docText = docText[:200] + "..."
			}
			b.WriteString(". ")
			b.WriteString(docText)
		}
	}

	return b.String()
}

// formatExprType converts an AST expression to a readable type string.
func formatExprType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + formatExprType(t.X)
	case *ast.SelectorExpr:
		return formatExprType(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + formatExprType(t.Elt)
	case *ast.MapType:
		return "map[" + formatExprType(t.Key) + "]" + formatExprType(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func"
	case *ast.ChanType:
		return "chan " + formatExprType(t.Value)
	case *ast.Ellipsis:
		return "..." + formatExprType(t.Elt)
	default:
		return "any"
	}
}

// buildSizeBasedPseudoDescription creates a simple pseudo-description for non-AST chunks.
func buildSizeBasedPseudoDescription(filePath string, startLine, endLine int, content string) string {
	baseName := filepath.Base(filePath)

	// Try to detect what's in the chunk using simple patterns
	var hints []string

	// Look for function declarations
	if strings.Contains(content, "func ") {
		funcCount := strings.Count(content, "\nfunc ") + strings.Count(content, "func ")
		if funcCount > 0 {
			hints = append(hints, fmt.Sprintf("%d function(s)", funcCount))
		}
	}

	// Look for type declarations
	if strings.Contains(content, "type ") {
		if strings.Contains(content, "struct {") || strings.Contains(content, "struct{") {
			hints = append(hints, "struct definition(s)")
		}
		if strings.Contains(content, "interface {") || strings.Contains(content, "interface{") {
			hints = append(hints, "interface definition(s)")
		}
	}

	// Look for constants
	if strings.Contains(content, "const ") || strings.Contains(content, "const(") {
		hints = append(hints, "constants")
	}

	// Look for imports
	if strings.Contains(content, "import ") || strings.Contains(content, "import(") {
		hints = append(hints, "imports")
	}

	var b strings.Builder
	b.WriteString("Go code from ")
	b.WriteString(baseName)
	b.WriteString(fmt.Sprintf(" (lines %d-%d)", startLine, endLine))

	if len(hints) > 0 {
		b.WriteString(". Contains: ")
		b.WriteString(strings.Join(hints, ", "))
	}

	return b.String()
}

// chunkByLogic attempts to split at logical boundaries like classes, blocks, imports.
func chunkByLogic(content string, config *types.ChunkConfig) ([]types.ReviewChunk, error) {
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
	if path, ok := config.FileMetadata["file_path"].(string); ok {
		filePath = path
	}

	changes := ""
	if changesData, ok := config.FileMetadata["changes"].(string); ok {
		changes = changesData
	}
	var chunks []types.ReviewChunk

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
func chunkBySize(content string, config *types.ChunkConfig) ([]types.ReviewChunk, error) {
	if content == "" {
		return nil, fmt.Errorf("empty content provided")
	}
	if config == nil {
		return nil, fmt.Errorf("nil config provided")
	}
	if config.FileMetadata == nil {
		return nil, fmt.Errorf("file metadata is required")
	}
	// Get filepath with type checking
	filepathInterface, exists := config.FileMetadata["file_path"]
	if !exists {
		return nil, fmt.Errorf("file_path missing from metadata")
	}

	filePath, ok := filepathInterface.(string)
	if !ok {
		return nil, fmt.Errorf("file_path in metadata is not a string: %T", filepathInterface)
	}
	var changes string
	if changesInterface, exists := config.FileMetadata["changes"]; exists {
		if changesStr, ok := changesInterface.(string); ok {
			changes = changesStr
		}
	}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("no lines to process")
	}

	estimatedChunks := (len(lines) + config.MaxTokens - 1) / config.MaxTokens
	var chunks []types.ReviewChunk
	var currentLines []string
	currentTokens := 0
	currentBytes := 0
	chunkStartLine := 1

	// Determine if this is a Go file for pseudo-descriptions
	isGoFile := strings.HasSuffix(filePath, ".go")

	for i, line := range lines {
		lineTokens := EstimateTokens(line)
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
				filePath,
				estimatedChunks,
				changes,
			)

			// Add pseudo-description for Go files (zero LLM cost)
			if isGoFile {
				chunk.Description = buildSizeBasedPseudoDescription(filePath, chunkStartLine, i, chunkContent)
			}

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
			filePath,
			estimatedChunks,
			changes,
		)

		// Add pseudo-description for Go files (zero LLM cost)
		if isGoFile {
			chunk.Description = buildSizeBasedPseudoDescription(filePath, chunkStartLine, len(lines), chunkContent)
		}

		chunks = append(chunks, chunk)
	}
	logging.GetLogger().Debug(context.Background(), "for file: %s, created: %d chunks", filePath, len(chunks))

	return chunks, nil
}

// Helper functions for chunk processing.
func mergeSmallChunks(chunks []types.ReviewChunk, minSize int) []types.ReviewChunk {
	if len(chunks) <= 1 {
		return chunks
	}

	var result []types.ReviewChunk
	current := chunks[0]

	for i := 1; i < len(chunks); i++ {
		lines := current.EndLine - current.StartLine + 1
		if lines < minSize {
			// Merge with next chunk
			current.Content += "\n" + chunks[i].Content
			current.EndLine = chunks[i].EndLine
		} else {
			result = append(result, current)
			current = chunks[i]
		}
	}

	// Add the last chunk
	result = append(result, current)
	return result
}

func addContextAndOverlap(chunks []types.ReviewChunk, fullContent string, config *types.ChunkConfig) []types.ReviewChunk {
	lines := strings.Split(fullContent, "\n")

	for i := range chunks {
		// Add leading context
		contextStart := util.Max(0, chunks[i].StartLine-config.ContextLines)
		chunks[i].LeadingContext = strings.Join(lines[contextStart:chunks[i].StartLine-1], "\n")

		// Add trailing context
		contextEnd := util.Min(len(lines), chunks[i].EndLine+config.ContextLines)
		chunks[i].TrailingContext = strings.Join(lines[chunks[i].EndLine:contextEnd], "\n")

		// Add overlap with previous chunk if needed
		if i > 0 && config.OverlapLines > 0 {
			overlapStart := util.Max(chunks[i-1].EndLine-config.OverlapLines, chunks[i-1].StartLine)
			var overlap string
			if overlapStart > 1 {
				overlap = strings.Join(lines[overlapStart-1:chunks[i-1].EndLine], "\n")
			} else {
				overlap = strings.Join(lines[0:chunks[i-1].EndLine], "\n")
			}
			chunks[i].Content = overlap + "\n" + chunks[i].Content
			chunks[i].StartLine = overlapStart
		}
	}

	return chunks
}

func createCompleteChunk(
	content string,
	startLine, endLine int,
	fullContent string,
	config *types.ChunkConfig,
	filePath string,
	estimatedTotalChunks int,
	changes string,
) types.ReviewChunk {
	chunk := types.ReviewChunk{
		Content:         content,
		StartLine:       startLine,
		EndLine:         endLine,
		LeadingContext:  "", // Context will be added in addContextAndOverlap
		TrailingContext: "", // Context will be added in addContextAndOverlap
		Changes:         ExtractRelevantChanges(changes, startLine, endLine),
		FilePath:        filePath,
		TotalChunks:     estimatedTotalChunks,
	}

	return chunk
}

// generateChunkDescription creates a natural language description of a code chunk.
func generateChunkDescription(ctx context.Context, chunk string) (string, error) {
	logger := logging.GetLogger()

	var llm core.LLM

	if isDescriptionLocalEnabled() {
		cachedLLM, err := getDescriptionLLM(ctx)
		if err != nil || cachedLLM == nil {
			logger.Debug(ctx, "Local description LLM unavailable, using cloud model")
			llm = core.GetDefaultLLM()
		} else {
			llm = cachedLLM
		}
	} else {
		llm = core.GetDefaultLLM()
	}

	prompt := fmt.Sprintf(`Generate a concise natural language description of what this Go code does:

%s

Description:`, chunk)

	resp, err := llm.Generate(ctx, prompt, core.WithMaxTokens(100),
		core.WithTemperature(0.3))

	if err != nil {
		return "", fmt.Errorf("failed to generate description: %w", err)
	}

	return strings.TrimSpace(resp.Content), nil
}

// isDescriptionLocalEnabled checks if local description generation is enabled.
func isDescriptionLocalEnabled() bool {
	enabled := os.Getenv("MAESTRO_LOCAL_DESCRIPTION_ENABLED")
	return enabled == "true" || enabled == "1"
}

// isLLMChunkDescriptionsEnabled checks if LLM-based chunk descriptions are enabled.
func isLLMChunkDescriptionsEnabled() bool {
	enabled := os.Getenv("MAESTRO_LLM_CHUNK_DESCRIPTIONS")
	return enabled == "true" || enabled == "1"
}

var (
	descriptionLLMOnce    sync.Once
	cachedDescriptionLLM  core.LLM
	descriptionLLMInitErr error
)

// getDescriptionLLM returns a cached LLM instance for description generation.
func getDescriptionLLM(ctx context.Context) (core.LLM, error) {
	descriptionLLMOnce.Do(func() {
		logger := logging.GetLogger()
		cachedDescriptionLLM = initializeDescriptionLLM(ctx, logger)
		if cachedDescriptionLLM == nil {
			descriptionLLMInitErr = fmt.Errorf("failed to initialize description LLM")
		}
	})
	return cachedDescriptionLLM, descriptionLLMInitErr
}

// initializeDescriptionLLM creates a local small LLM for description generation.
func initializeDescriptionLLM(ctx context.Context, logger *logging.Logger) core.LLM {
	model := os.Getenv("MAESTRO_LOCAL_DESCRIPTION_MODEL")
	if model == "" {
		model = "qwen2.5:1.5b"
	}

	provider := os.Getenv("MAESTRO_LOCAL_DESCRIPTION_PROVIDER")
	if provider == "" {
		provider = "ollama"
	}

	endpoint := os.Getenv("MAESTRO_LOCAL_DESCRIPTION_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}

	logger.Info(ctx, "Initializing cached description LLM (once): model=%s, provider=%s, endpoint=%s",
		model, provider, endpoint)

	var llm core.LLM
	var err error

	switch provider {
	case "ollama":
		llm, err = llms.NewOllamaLLM(
			core.ModelID(model),
			llms.WithBaseURL(endpoint),
		)
		if err != nil {
			logger.Warn(ctx, "Failed to initialize Ollama LLM: %v", err)
			return nil
		}
		logger.Info(ctx, "Successfully initialized Ollama description LLM at %s with model %s", endpoint, model)
		return llm
	case "llamacpp":
		llm, err = llms.NewLlamacppLLM(endpoint)
		if err != nil {
			logger.Warn(ctx, "Failed to initialize llamacpp LLM: %v", err)
			return nil
		}
		logger.Info(ctx, "Successfully initialized llamacpp description LLM at %s", endpoint)
		return llm
	default:
		logger.Warn(ctx, "Unknown description LLM provider: %s", provider)
		return nil
	}
}

// GetContextLines returns the number of context lines from environment variable or default.
func GetContextLines() int {
	if value := os.Getenv("MAESTRO_CHUNK_CONTEXT_LINES"); value != "" {
		if lines, err := strconv.Atoi(value); err == nil && lines > 0 {
			return lines
		}
	}
	return 15 // Default increased from 5 to 15 lines
}
