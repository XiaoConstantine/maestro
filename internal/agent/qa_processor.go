package agent

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/interceptors"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
	"github.com/XiaoConstantine/maestro/internal/embedding"
	"github.com/XiaoConstantine/maestro/internal/search"
	"github.com/XiaoConstantine/maestro/internal/types"
	"github.com/XiaoConstantine/maestro/internal/util"
)

// Simple cache for Predict modules.
var qaModuleCache = sync.Map{}

// getCachedPredictModule returns a cached Predict module or creates a new one.
func getCachedPredictModule(signature core.Signature) *modules.Predict {
	// Create signature hash
	hasher := md5.New()
	for _, input := range signature.Inputs {
		hasher.Write([]byte(input.Name + ":" + input.Description))
	}
	for _, output := range signature.Outputs {
		hasher.Write([]byte(output.Name + ":" + output.Description))
	}
	hasher.Write([]byte(signature.Instruction))
	// Include XML config in hash to force cache miss when XML config changes
	hasher.Write([]byte("xml-enabled-v3"))
	signatureHash := hex.EncodeToString(hasher.Sum(nil))

	logger := logging.GetLogger()
	logger.Debug(context.Background(), "Looking for cached module with hash: %s", signatureHash)

	// Try to get from cache
	if cached, ok := qaModuleCache.Load(signatureHash); ok {
		if predict, ok := cached.(*modules.Predict); ok {
			logger.Debug(context.Background(), "Using cached predict module")
			return predict
		}
	}

	logger.Debug(context.Background(), "Creating new predict module with XML output")

	// Create new and cache with XML output enabled
	predict := modules.NewPredict(signature).
		WithName("QAAnalyzer").
		WithXMLOutput(interceptors.XMLConfig{
			StrictParsing:      false,
			FallbackToText:     true,  // Enable fallback for markdown-wrapped XML
			ValidateXML:        false, // Disable validation for LLM output
			MaxDepth:           10,    // Security limit
			MaxSize:            10000,
			ParseTimeout:       10 * time.Second, // Correct type
			CustomTags:         make(map[string]string),
			IncludeTypeHints:   false,
			PreserveWhitespace: false,
		})
	qaModuleCache.Store(signatureHash, predict)
	return predict
}

type RepoQAProcessor struct {
	ragStore RAGStore
}

type QAResponse struct {
	Answer      string   `json:"answer"`       // The detailed answer to the question
	Confidence  float64  `json:"confidence"`   // How confident the system is about the answer (0.0-1.0)
	SourceFiles []string `json:"source_files"` // Files referenced in the answer
}

func NewRepoQAProcessor(store RAGStore) *RepoQAProcessor {
	return &RepoQAProcessor{
		ragStore: store,
	}
}

// SearchQueries contains optimized queries for code search.
type SearchQueries struct {
	Queries         []string // List of search queries to try
	IncludeReadme   bool     // Whether to prioritize README/doc files
	UseHybridSearch bool     // Whether to use hybrid (semantic + keyword) search
}

// GenerateSearchQueries uses LLM to generate optimized search queries for sgrep.
// This helps translate vague user questions into effective semantic search queries.
func (p *RepoQAProcessor) GenerateSearchQueries(ctx context.Context, question, repoName string) (*SearchQueries, error) {
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "user_question", Description: "The user's question about the codebase"}},
			{Field: core.Field{Name: "repo_name", Description: "Repository name for context"}},
		},
		[]core.OutputField{
			{Field: core.Field{Name: "search_queries", Description: "3 optimized search queries, one per line"}},
			{Field: core.Field{Name: "include_readme", Description: "yes or no"}},
			{Field: core.Field{Name: "use_hybrid", Description: "yes or no"}},
		},
	).WithInstruction(`Generate optimized search queries for sgrep, a semantic code search tool.

ABOUT SGREP:
- Semantic search: finds code by meaning, not exact text match
- Works well with: technical terms, concept descriptions, specific patterns
- Works poorly with: vague questions like "what does this do", "how does it work"
- Scores are L2 distance (lower = more similar), typically 0.6-0.9 range

YOUR TASK:
Convert the user's question into 3 effective search queries that will find relevant code.

GUIDELINES:
1. Include the repo/project name in at least one query for overview questions
2. Use technical terms and programming concepts
3. Be specific - "error handling retry mechanism" beats "how errors work"
4. For architecture questions, include terms like "main", "entry point", "core", "module"
5. Each query should be 3-8 words

Set include_readme=yes for: overview, purpose, architecture, getting started questions
Set use_hybrid=yes for: queries with specific function/variable names

OUTPUT FORMAT:
search_queries: one query per line, exactly 3 queries
include_readme: yes or no
use_hybrid: yes or no`)

	predict := modules.NewPredict(signature).
		WithName("SearchQueryGenerator").
		WithXMLOutput(interceptors.XMLConfig{
			StrictParsing:  false,
			FallbackToText: true,
			ValidateXML:    false,
			MaxDepth:       10,
			ParseTimeout:   5 * time.Second,
		})

	logger := logging.GetLogger()
	result, err := predict.Process(ctx, map[string]interface{}{
		"user_question": question,
		"repo_name":     repoName,
	})
	if err != nil {
		logger.Warn(ctx, "Failed to generate search queries, using original question: %v", err)
		return &SearchQueries{
			Queries:       []string{question},
			IncludeReadme: isOverviewQuestion(question),
		}, nil
	}

	return parseSearchQueries(ctx, result, question)
}

// isOverviewQuestion checks if question is about repo overview/purpose.
func isOverviewQuestion(question string) bool {
	q := strings.ToLower(question)
	terms := []string{
		"what does", "what is", "purpose", "overview", "about",
		"explain", "describe", "introduction", "architecture",
		"how does this repo", "how does this project",
	}
	for _, term := range terms {
		if strings.Contains(q, term) {
			return true
		}
	}
	return false
}

// parseSearchQueries extracts search queries from LLM response.
func parseSearchQueries(ctx context.Context, result interface{}, originalQuestion string) (*SearchQueries, error) {
	logger := logging.GetLogger()
	queries := &SearchQueries{
		Queries:       []string{originalQuestion},
		IncludeReadme: isOverviewQuestion(originalQuestion),
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return queries, nil
	}

	// Extract search queries
	if searchQueriesStr, ok := resultMap["search_queries"].(string); ok && searchQueriesStr != "" {
		lines := strings.Split(searchQueriesStr, "\n")
		var extracted []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			// Remove numbering like "1.", "2.", etc.
			if len(line) > 2 && line[1] == '.' {
				line = strings.TrimSpace(line[2:])
			}
			if line != "" {
				extracted = append(extracted, line)
			}
		}
		if len(extracted) > 0 {
			queries.Queries = extracted
			logger.Info(ctx, "Generated %d search queries from question: %s", len(extracted), originalQuestion)
			for i, q := range extracted {
				logger.Debug(ctx, "  Query %d: %s", i+1, q)
			}
		}
	}

	// Extract include_readme
	if includeReadme, ok := resultMap["include_readme"].(string); ok {
		queries.IncludeReadme = strings.ToLower(strings.TrimSpace(includeReadme)) == "yes"
	}

	// Extract use_hybrid
	if useHybrid, ok := resultMap["use_hybrid"].(string); ok {
		queries.UseHybridSearch = strings.ToLower(strings.TrimSpace(useHybrid)) == "yes"
	}

	return queries, nil
}

// AnswerWithContext answers a question using pre-fetched content (e.g., from sgrep).
// This allows the caller to provide search results from any source (sgrep, RAG, etc.).
func (p *RepoQAProcessor) AnswerWithContext(ctx context.Context, question string, contents []*Content) (*QAResponse, error) {
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "question"}},
			{Field: core.Field{Name: "relevant_context"}},
		},
		[]core.OutputField{
			{Field: core.Field{Name: "answer"}},
			{Field: core.Field{Name: "confidence"}},
			{Field: core.Field{Name: "source_files"}},
		},
	).WithInstruction(`Answer questions about the repository using the provided context.
    Follow repository conventions and patterns when explaining code.
    Reference specific files and line numbers when available.`)

	metadata := &QAMetadata{Question: question}
	return p.processResults(ctx, signature, metadata, contents)
}

// Process implements the TaskProcessor interface.
func (p *RepoQAProcessor) Process(ctx context.Context, task agents.Task, context map[string]interface{}) (interface{}, error) {
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "question"}},
			{Field: core.Field{Name: "relevant_context"}},
		},
		[]core.OutputField{
			{Field: core.Field{Name: "answer"}},
			{Field: core.Field{Name: "confidence"}},
			{Field: core.Field{Name: "source_files"}},
		},
	).WithInstruction(`Answer questions about the repository using the provided context.
    Follow repository conventions and patterns when explaining code.
    Reference specific files and line numbers when available.`)

	metadata, err := extractQAMetadata(task.Metadata)

	if err != nil {
		return nil, fmt.Errorf("task %s: %w", task.ID, err)
	}

	logger := logging.GetLogger()

	// Try sgrep semantic search first (if available and indexed)
	sgrepResults := p.searchWithSgrep(ctx, metadata.Question, 5)
	if len(sgrepResults) > 0 {
		logger.Info(ctx, "sgrep semantic search found %d results for: %s", len(sgrepResults), metadata.Question)
	}

	// Use traditional RAG with embeddings (latency-critical query)
	router := embedding.GetRouter()
	questionEmbedding, err := router.CreateEmbedding(ctx, metadata.Question, embedding.WithLatencyCritical(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	similar, err := p.ragStore.FindSimilar(ctx, questionEmbedding.Vector, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to find similar content: %w", err)
	}

	// Merge sgrep results with RAG results
	if len(sgrepResults) > 0 {
		similar = p.mergeSgrepResults(similar, sgrepResults)
	}

	return p.processResults(ctx, signature, metadata, similar)
}

// processResults handles the common result processing for both agentic and traditional RAG.
func (p *RepoQAProcessor) processResults(ctx context.Context, signature core.Signature, metadata *QAMetadata, similar []*Content) (*QAResponse, error) {
	// Handle case where no results were found
	if len(similar) == 0 {
		return &QAResponse{
			Answer:      fmt.Sprintf("I couldn't find any relevant information about \"%s\" in this repository. This codebase may not contain the patterns or examples you're looking for.", metadata.Question),
			Confidence:  0.1,
			SourceFiles: []string{},
		}, nil
	}

	// Format context for LLM
	contextBuilder := strings.Builder{}
	sourceFiles := make([]string, 0, len(similar))
	seenFiles := make(map[string]bool) // Track seen files for deduplication

	for _, content := range similar {
		contextBuilder.WriteString(fmt.Sprintf("File: %s\n", content.Metadata["file_path"]))
		contextBuilder.WriteString(fmt.Sprintf("Lines %s-%s:\n",
			content.Metadata["start_line"],
			content.Metadata["end_line"]))
		contextBuilder.WriteString(content.Text)
		contextBuilder.WriteString("\n---\n")

		// Only add file if not already seen
		filePath := content.Metadata["file_path"]
		if !seenFiles[filePath] {
			seenFiles[filePath] = true
			sourceFiles = append(sourceFiles, filePath)
		}
	}

	// Use cached predict module
	predict := getCachedPredictModule(signature)

	// Debug: Check if the predict module has XML output enabled
	logger := logging.GetLogger()
	logger.Debug(ctx, "Using predict module: %T, with signature: %+v", predict, signature)

	streamHandler := util.CreateStreamHandler(ctx, logging.GetLogger())
	result, err := predict.Process(ctx, map[string]interface{}{
		"question":         metadata.Question,
		"relevant_context": contextBuilder.String(),
	}, core.WithStreamHandler(streamHandler))
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	// Debug: Log the raw result before processing
	logger.Debug(ctx, "Raw result from predict.Process: type=%T, value=%+v", result, result)

	response := &QAResponse{
		SourceFiles: sourceFiles,
	}

	if err := extractQAResult(result, response); err != nil {
		return nil, fmt.Errorf("failed to extract response: %w", err)
	}

	return response, nil
}

// Helper structs and functions.
type QAMetadata struct {
	Question string
}

func extractQAMetadata(metadata map[string]interface{}) (*QAMetadata, error) {
	question, ok := metadata["question"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid question in metadata")
	}

	return &QAMetadata{
		Question: question,
	}, nil
}

func extractQAResult(result interface{}, response *QAResponse) error {
	// Debug logging
	logger := logging.GetLogger()
	logger.Debug(context.Background(), "extractQAResult input type: %T, value: %+v", result, result)

	resultMap, ok := result.(map[string]interface{})
	// TODO(human): Trigger manual parsing for empty maps too
	if !ok || len(resultMap) == 0 {
		// If we can't parse the result, treat it as a string answer
		if str, ok := result.(string); ok && str != "" {
			// Remove markdown code block formatting
			cleanXML := str
			if strings.Contains(str, "```xml") {
				// Extract content between ```xml and ```
				start := strings.Index(str, "```xml")
				if start != -1 {
					start += 6 // Skip "```xml"
					end := strings.Index(str[start:], "```")
					if end != -1 {
						cleanXML = strings.TrimSpace(str[start : start+end])
					}
				}
			}

			// Try to parse XML manually if the automatic parsing failed
			if strings.Contains(cleanXML, "<answer>") && strings.Contains(cleanXML, "</answer>") {
				if answer := extractXMLField(cleanXML, "answer"); answer != "" {
					response.Answer = answer
					if confidence := extractXMLField(cleanXML, "confidence"); confidence != "" {
						response.Confidence = parseConfidence(confidence)
					} else {
						response.Confidence = 0.7
					}
					logger.Debug(context.Background(), "Manual XML parsing successful: answer=%s, confidence=%.2f", response.Answer, response.Confidence)
					return nil
				}
			}
			response.Answer = "I found some information, but couldn't parse it properly. Raw response: " + str
			response.Confidence = 0.3
			return nil
		}
		return fmt.Errorf("invalid result type: %T", result)
	}

	// Debug log the resultMap
	logger.Debug(context.Background(), "resultMap contents: %+v", resultMap)

	if answer, ok := resultMap["answer"].(string); ok && answer != "" {
		logger.Debug(context.Background(), "Found answer field: %s", answer)
		// Handle dspy-go XML format that includes field name prefix
		if strings.HasPrefix(answer, "answer:") {
			response.Answer = strings.TrimPrefix(answer, "answer:")
			logger.Debug(context.Background(), "Stripped answer prefix, result: %s", response.Answer)
		} else {
			response.Answer = answer
		}
	} else {
		// Check if there's any text content we can use
		if rawContent, exists := resultMap["content"].(string); exists && rawContent != "" {
			response.Answer = rawContent
		} else {
			// Fallback: create a generic "no information found" response
			response.Answer = "I was unable to find relevant information to answer your question in this repository."
		}
	}

	if confidence, ok := resultMap["confidence"].(float64); ok {
		response.Confidence = confidence
	} else if confidenceStr, ok := resultMap["confidence"].(string); ok {
		// Handle dspy-go XML format that includes field name prefix
		confidenceStr = strings.TrimPrefix(confidenceStr, "confidence:")
		// Parse confidence value - handle "High", "Medium", "Low" or numeric values
		switch strings.ToLower(strings.TrimSpace(confidenceStr)) {
		case "high":
			response.Confidence = 0.9
		case "medium":
			response.Confidence = 0.7
		case "low":
			response.Confidence = 0.4
		default:
			// Try to parse as numeric
			if conf, err := fmt.Sscanf(confidenceStr, "%f", new(float64)); err == nil && conf == 1 {
				var parsedConf float64
				_, _ = fmt.Sscanf(confidenceStr, "%f", &parsedConf)
				response.Confidence = parsedConf
			} else {
				response.Confidence = 0.5
			}
		}
	} else {
		// Default confidence when answer was found but confidence wasn't specified
		if response.Answer != "" {
			response.Confidence = 0.7
		} else {
			response.Confidence = 0.1
		}
	}

	return nil
}

// extractXMLField extracts content from XML tags manually.
func extractXMLField(xmlString, fieldName string) string {
	pattern := fmt.Sprintf(`<%s>(.*?)</%s>`, fieldName, fieldName)
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(xmlString)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// parseConfidence converts confidence string to float64.
func parseConfidence(confidenceStr string) float64 {
	confidenceStr = strings.TrimSpace(confidenceStr)

	// Handle text values
	switch strings.ToLower(confidenceStr) {
	case "high":
		return 0.9
	case "medium":
		return 0.7
	case "low":
		return 0.4
	default:
		// Try to parse as numeric
		if conf, err := strconv.ParseFloat(confidenceStr, 64); err == nil {
			return conf
		}
		return 0.7 // Default
	}
}

// sgrepResult represents a single sgrep search result.
type sgrepResult struct {
	FilePath  string  `json:"file_path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
}

// searchWithSgrep performs semantic code search using SgrepTool.
// Returns empty slice if sgrep is not available or not indexed.
func (p *RepoQAProcessor) searchWithSgrep(ctx context.Context, query string, limit int) []sgrepResult {
	logger := logging.GetLogger()

	// Use SgrepTool for search
	sgrepTool := search.NewSgrepTool(logger, "")

	// Check if sgrep is available
	if !sgrepTool.IsAvailable(ctx) {
		logger.Debug(ctx, "sgrep not installed, skipping semantic search")
		return nil
	}

	// Execute search using SgrepTool
	results, err := sgrepTool.Search(ctx, query, limit)
	if err != nil {
		if strings.Contains(err.Error(), "not indexed") {
			logger.Debug(ctx, "Repository not indexed for sgrep, skipping semantic search")
		} else {
			logger.Debug(ctx, "sgrep search failed: %v", err)
		}
		return nil
	}

	// Convert SgrepSearchResult to sgrepResult for backward compatibility
	sgrepResults := make([]sgrepResult, len(results))
	for i, r := range results {
		sgrepResults[i] = sgrepResult(r)
	}

	logger.Debug(ctx, "sgrep returned %d semantic matches for query: %s", len(sgrepResults), query)
	return sgrepResults
}

// mergeSgrepResults merges sgrep semantic results with existing Content results.
// sgrep results are prepended as they're semantically relevant.
func (p *RepoQAProcessor) mergeSgrepResults(existing []*Content, sgrepResults []sgrepResult) []*Content {
	merged := make([]*Content, 0, len(existing)+len(sgrepResults))

	// Add sgrep results first (semantically relevant)
	for i, r := range sgrepResults {
		// Convert distance score to relevance (lower distance = higher relevance)
		relevance := 1.0 - r.Score
		if relevance < 0 {
			relevance = 0
		}

		content := &Content{
			ID:   fmt.Sprintf("sgrep-semantic-%d", i),
			Text: r.Content,
			Metadata: map[string]string{
				"file_path":    r.FilePath,
				"start_line":   fmt.Sprintf("%d", r.StartLine),
				"end_line":     fmt.Sprintf("%d", r.EndLine),
				"content_type": types.ContentTypeRepository,
				"source":       "sgrep_semantic",
				"relevance":    fmt.Sprintf("%.2f", relevance),
			},
		}
		merged = append(merged, content)
	}

	// Add existing results, avoiding duplicates
	seenFiles := make(map[string]bool)
	for _, r := range sgrepResults {
		seenFiles[r.FilePath] = true
	}

	for _, c := range existing {
		filePath := c.Metadata["file_path"]
		// Skip if we already have this file from sgrep
		if seenFiles[filePath] {
			continue
		}
		merged = append(merged, c)
	}

	return merged
}
