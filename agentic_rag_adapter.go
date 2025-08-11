package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// AgenticRAGAdapter adapts the agentic search system to work with existing RAG interface.
type AgenticRAGAdapter struct {
	orchestrator *AgenticSearchOrchestrator
	searchTool   *SimpleSearchTool
	logger       *logging.Logger
}

// NewAgenticRAGAdapter creates an adapter for the existing RAG interface.
func NewAgenticRAGAdapter(rootPath string, logger *logging.Logger) *AgenticRAGAdapter {
	searchTool := NewSimpleSearchTool(logger, rootPath)
	orchestrator := NewAgenticSearchOrchestrator(searchTool, logger)

	return &AgenticRAGAdapter{
		orchestrator: orchestrator,
		searchTool:   searchTool,
		logger:       logger,
	}
}

// FindSimilar replaces the traditional RAG FindSimilar method.
func (ara *AgenticRAGAdapter) FindSimilar(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*Content, error) {
	// Since we don't use embeddings, we need to convert this call to a text query
	// This is a compatibility layer - in practice, callers should use the new interface
	query := ara.inferQueryFromEmbedding(contentTypes)

	result, err := ara.orchestrator.ExecuteSearch(ctx, query, "")
	if err != nil {
		return nil, fmt.Errorf("agentic search failed: %w", err)
	}

	// Convert SynthesizedResult to []*Content for compatibility
	return ara.convertToContent(result, limit), nil
}

// FindSimilarWithDebug provides debug information (compatibility method).
func (ara *AgenticRAGAdapter) FindSimilarWithDebug(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*Content, DebugInfo, error) {
	startTime := time.Now()

	contents, err := ara.FindSimilar(ctx, embedding, limit, contentTypes...)
	if err != nil {
		return nil, DebugInfo{}, err
	}

	// Create debug info for compatibility
	debugInfo := DebugInfo{
		QueryEmbeddingDims: len(embedding),
		ResultCount:        len(contents),
		RetrievalTime:      time.Since(startTime),
		TopMatches:         ara.createTopMatches(contents),
		QualityMetrics: QualityMetrics{
			ExcellentCount: ara.countByRelevance(contents, 0.8, 1.0),
			GoodCount:      ara.countByRelevance(contents, 0.6, 0.8),
			FairCount:      ara.countByRelevance(contents, 0.4, 0.6),
			PoorCount:      ara.countByRelevance(contents, 0.0, 0.4),
			AverageScore:   ara.calculateAverageScore(contents),
		},
	}

	return contents, debugInfo, nil
}

// FindSimilarWithLateInteraction performs iterative search refinement.
func (ara *AgenticRAGAdapter) FindSimilarWithLateInteraction(ctx context.Context, embedding []float32, limit int, codeContext, queryContext string, contentTypes ...string) ([]*Content, *RefinementResult, error) {
	// Create query from both contexts
	query := fmt.Sprintf("%s %s", queryContext, ara.extractKeywords(codeContext))

	result, err := ara.orchestrator.ExecuteSearch(ctx, query, codeContext)
	if err != nil {
		return nil, nil, fmt.Errorf("interactive agentic search failed: %w", err)
	}

	contents := ara.convertToContent(result, limit)
	refinementResult := &RefinementResult{
		OriginalCount:  1,
		FinalCount:     len(contents),
		ProcessingTime: time.Second, // Placeholder
		QualityMetrics: RefinementQualityMetrics{
			ConfidenceScore: result.ConfidenceScore,
			RelevanceScore:  result.ConfidenceScore,
		},
	}

	return contents, refinementResult, nil
}

// Enhanced methods that provide better agentic search interface

// SearchCode performs code-specific agentic search.
func (ara *AgenticRAGAdapter) SearchCode(ctx context.Context, query string, codeContext string) (*SynthesizedResult, error) {
	codeQuery := fmt.Sprintf("code analysis: %s", query)
	return ara.orchestrator.ExecuteSearch(ctx, codeQuery, codeContext)
}

// SearchGuidelines performs guideline-specific agentic search.
func (ara *AgenticRAGAdapter) SearchGuidelines(ctx context.Context, query string) (*SynthesizedResult, error) {
	return ara.orchestrator.FindGuidelines(ctx, query)
}

// SearchWithIntent performs intent-aware search.
func (ara *AgenticRAGAdapter) SearchWithIntent(ctx context.Context, query string, intent string, codeContext string) (*SynthesizedResult, error) {
	intentQuery := fmt.Sprintf("[Intent: %s] %s", intent, query)
	return ara.orchestrator.ExecuteSearch(ctx, intentQuery, codeContext)
}

// Helper methods for conversion and compatibility

func (ara *AgenticRAGAdapter) inferQueryFromEmbedding(contentTypes []string) string {
	// Since we can't reverse engineer a query from embeddings,
	// create a generic query based on content types
	if len(contentTypes) > 0 {
		return fmt.Sprintf("search for %s content", strings.Join(contentTypes, " and "))
	}
	return "generic code search"
}

func (ara *AgenticRAGAdapter) convertToContent(result *SynthesizedResult, limit int) []*Content {
	var contents []*Content

	// Convert code samples to Content
	for i, sample := range result.CodeSamples {
		if i >= limit {
			break
		}

		content := &Content{
			ID:   fmt.Sprintf("code-%d", i),
			Text: sample.Content,
			Metadata: map[string]string{
				"file_path":    sample.FilePath,
				"content_type": ContentTypeRepository,
				"explanation":  sample.Explanation,
				"relevance":    fmt.Sprintf("%.2f", sample.Relevance),
			},
		}
		contents = append(contents, content)
	}

	// Convert guidelines to Content
	remaining := limit - len(contents)
	for i, guideline := range result.Guidelines {
		if i >= remaining {
			break
		}

		content := &Content{
			ID:   fmt.Sprintf("guideline-%d", i),
			Text: guideline.Description,
			Metadata: map[string]string{
				"title":        guideline.Title,
				"content_type": ContentTypeGuideline,
				"source":       guideline.Source,
				"relevance":    fmt.Sprintf("%.2f", guideline.Relevance),
			},
		}
		contents = append(contents, content)
	}

	return contents
}


func (ara *AgenticRAGAdapter) extractKeywords(text string) string {
	// Simple keyword extraction
	words := strings.Fields(text)
	var keywords []string

	for _, word := range words {
		if len(word) > 3 && !ara.isStopWord(word) {
			keywords = append(keywords, word)
		}
	}

	if len(keywords) > 5 {
		keywords = keywords[:5] // Limit to top 5
	}

	return strings.Join(keywords, " ")
}

func (ara *AgenticRAGAdapter) isStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "are": true,
		"but": true, "not": true, "you": true, "all": true,
		"can": true, "had": true, "her": true, "was": true,
		"one": true, "our": true, "out": true, "day": true,
	}
	return stopWords[strings.ToLower(word)]
}

func (ara *AgenticRAGAdapter) createTopMatches(contents []*Content) []string {
	var matches []string
	for i, content := range contents {
		if i >= 3 { // Top 3 matches
			break
		}

		relevance := "unknown"
		if rel, exists := content.Metadata["relevance"]; exists {
			relevance = rel
		}

		match := fmt.Sprintf("%s (relevance: %s)", content.ID, relevance)
		matches = append(matches, match)
	}
	return matches
}

func (ara *AgenticRAGAdapter) countByRelevance(contents []*Content, min, max float64) int {
	count := 0
	for _, content := range contents {
		if relStr, exists := content.Metadata["relevance"]; exists {
			if rel := agenticParseFloat(relStr); rel >= min && rel < max {
				count++
			}
		}
	}
	return count
}

func (ara *AgenticRAGAdapter) calculateAverageScore(contents []*Content) float64 {
	if len(contents) == 0 {
		return 0.0
	}

	total := 0.0
	count := 0

	for _, content := range contents {
		if relStr, exists := content.Metadata["relevance"]; exists {
			total += agenticParseFloat(relStr)
			count++
		}
	}

	if count == 0 {
		return 0.0
	}

	return total / float64(count)
}

// agenticParseFloat safely parses a float from string.
func agenticParseFloat(s string) float64 {
	if f, err := fmt.Sscanf(s, "%f", new(float64)); err == nil && f == 1 {
		var result float64
		_, _ = fmt.Sscanf(s, "%f", &result)
		return result
	}
	return 0.0
}

// Legacy compatibility methods - these maintain the old RAG interface

func (ara *AgenticRAGAdapter) StoreContent(ctx context.Context, content *Content) error {
	// In agentic search, we don't store individual content pieces
	// Content is searched dynamically from files
	ara.logger.Debug(ctx, "StoreContent called (no-op in agentic search): %s", content.ID)
	return nil
}

func (ara *AgenticRAGAdapter) UpdateContent(ctx context.Context, content *Content) error {
	return ara.StoreContent(ctx, content) // Same as store in our system
}

func (ara *AgenticRAGAdapter) DeleteContent(ctx context.Context, id string) error {
	// No-op since we don't store content
	ara.logger.Debug(ctx, "DeleteContent called (no-op in agentic search): %s", id)
	return nil
}

func (ara *AgenticRAGAdapter) PopulateGuidelines(ctx context.Context, language string) error {
	// Guidelines are searched dynamically, but we can pre-index common patterns
	ara.logger.Info(ctx, "PopulateGuidelines called for language: %s", language)

	// Use the search tool to discover guideline files
	guidelineFiles, err := ara.searchTool.GlobSearch(ctx, "**/*.md")
	if err != nil {
		return fmt.Errorf("failed to find guideline files: %w", err)
	}

	ara.logger.Info(ctx, "Found %d potential guideline files", len(guidelineFiles))
	return nil
}

func (ara *AgenticRAGAdapter) StoreRule(ctx context.Context, rule ReviewRule) error {
	// Rules are handled dynamically through search
	ara.logger.Debug(ctx, "StoreRule called (no-op in agentic search): %s", rule.ID)
	return nil
}

func (ara *AgenticRAGAdapter) HasContent(ctx context.Context) (bool, error) {
	// Check if we have files to search
	files, err := ara.searchTool.GlobSearch(ctx, "**/*.go")
	if err != nil {
		return false, err
	}
	return len(files) > 0, nil
}

func (ara *AgenticRAGAdapter) GetMetadata(ctx context.Context, key string) (string, error) {
	// Simple in-memory metadata store for compatibility
	ara.logger.Debug(ctx, "GetMetadata called: %s", key)
	return "", fmt.Errorf("metadata key not found: %s", key)
}

func (ara *AgenticRAGAdapter) SetMetadata(ctx context.Context, key, value string) error {
	ara.logger.Debug(ctx, "SetMetadata called: %s = %s", key, value)
	return nil // No-op for now
}

func (ara *AgenticRAGAdapter) Close() error {
	return ara.orchestrator.Shutdown(context.Background())
}

// AgenticRAGFactory creates agentic RAG instances.
type AgenticRAGFactory struct {
	logger *logging.Logger
}

func NewAgenticRAGFactory(logger *logging.Logger) *AgenticRAGFactory {
	return &AgenticRAGFactory{logger: logger}
}

// CreateRAGStore creates an agentic RAG adapter that implements RAGStore interface.
func (arf *AgenticRAGFactory) CreateRAGStore(rootPath string) RAGStore {
	return NewAgenticRAGAdapter(rootPath, arf.logger)
}
