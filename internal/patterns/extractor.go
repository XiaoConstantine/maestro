// Package patterns provides code pattern extraction for guideline matching.
package patterns

import (
	"context"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/types"
)

// Extractor extracts code patterns for guideline search.
type Extractor struct {
	logger *logging.Logger
}

// NewExtractor creates a new pattern extractor.
func NewExtractor(logger *logging.Logger) *Extractor {
	return &Extractor{
		logger: logger,
	}
}

// ExtractCodePatterns identifies patterns in Go code that can be matched against guidelines.
func (e *Extractor) ExtractCodePatterns(ctx context.Context, code string) []types.SimpleCodePattern {
	patterns := []types.SimpleCodePattern{}
	codeLower := strings.ToLower(code)

	// Pattern 1: Error Handling
	if (strings.Contains(codeLower, "error") || strings.Contains(codeLower, "err")) &&
		(strings.Contains(code, "return") || strings.Contains(code, "fmt.Errorf") || strings.Contains(code, "errors.")) {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "error handling",
			Description: "Error checking, error wrapping, and error propagation",
			Confidence:  0.85,
		})
	}

	// Pattern 2: Concurrency
	if (strings.Contains(code, "go ") || strings.Contains(code, "goroutine")) &&
		(strings.Contains(code, "chan") || strings.Contains(code, "channel") || strings.Contains(code, "sync.") || strings.Contains(code, "Mutex")) {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "concurrency",
			Description: "Goroutines, channels, mutexes, and concurrent patterns",
			Confidence:  0.9,
		})
	}

	// Pattern 3: Nil Checks
	if strings.Contains(code, "nil") && strings.Contains(code, "if") {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "nil checks",
			Description: "Nil pointer checks and defensive programming",
			Confidence:  0.75,
		})
	}

	// Pattern 4: Pointer Usage
	if strings.Contains(code, "*") && (strings.Contains(code, "&") || strings.Contains(code, "Ptr") || strings.Contains(code, "pointer")) {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "pointer usage",
			Description: "Pointer semantics, dereference operations, and pointer safety",
			Confidence:  0.8,
		})
	}

	// Pattern 5: Resource Management / Cleanup
	if strings.Contains(code, "defer") || strings.Contains(code, "Close()") || strings.Contains(code, "cleanup") {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "resource cleanup",
			Description: "Deferred cleanup, file/connection closing, resource management",
			Confidence:  0.85,
		})
	}

	// Pattern 6: Interface Usage
	if strings.Contains(code, "interface") || strings.Contains(code, "Interface{") {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "interface design",
			Description: "Interface design patterns, composition, and abstraction",
			Confidence:  0.8,
		})
	}

	// Pattern 7: Naming Conventions
	if strings.Contains(code, "const ") || strings.Contains(code, "var ") || strings.Contains(code, "func ") {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "naming conventions",
			Description: "Variable naming, function naming, and identifier conventions",
			Confidence:  0.7,
		})
	}

	// Pattern 8: Struct/Type Definitions
	if strings.Contains(code, "type ") && strings.Contains(code, "struct") {
		patterns = append(patterns, types.SimpleCodePattern{
			Name:        "type design",
			Description: "Struct design, type definitions, and data structures",
			Confidence:  0.75,
		})
	}

	if e.logger != nil {
		e.logger.Debug(ctx, "Extracted %d code patterns from file", len(patterns))
	}
	return patterns
}

// ConvertToContent converts GuidelineSearchResult back to Content for compatibility.
func ConvertToContent(results []types.GuidelineSearchResult) []*types.Content {
	contents := make([]*types.Content, len(results))
	for i, result := range results {
		contents[i] = result.Content
	}
	return contents
}

// SplitContentBySize splits content into chunks by byte size, respecting line boundaries.
func SplitContentBySize(content string, maxBytes int) ([]string, error) {
	if len(content) <= maxBytes {
		return []string{content}, nil
	}

	var chunks []string
	lines := strings.Split(content, "\n")
	currentChunk := strings.Builder{}

	for _, line := range lines {
		if currentChunk.Len()+len(line)+1 > maxBytes {
			// Current chunk would exceed limit, start a new one
			if currentChunk.Len() > 0 {
				chunks = append(chunks, currentChunk.String())
				currentChunk.Reset()
			}
		}
		currentChunk.WriteString(line)
		currentChunk.WriteString("\n")
	}

	// Add final chunk if not empty
	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks, nil
}
