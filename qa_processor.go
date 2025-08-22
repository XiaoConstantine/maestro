package main

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

	// Check if we have agentic search available
	if agenticAdapter, ok := p.ragStore.(*AgenticRAGAdapter); ok {
		// Use direct agentic search with the text query
		result, err := agenticAdapter.SearchWithIntent(ctx, metadata.Question, "code_assistance", "")
		if err != nil {
			return nil, fmt.Errorf("failed to perform agentic search: %w", err)
		}

		// Convert agentic results to traditional format for compatibility
		similar := p.convertAgenticResultToContent(result)
		return p.processResults(ctx, signature, metadata, similar)
	}

	// Fallback to traditional RAG with embeddings
	llm := core.GetTeacherLLM()
	questionEmbedding, err := llm.CreateEmbedding(ctx, metadata.Question)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	similar, err := p.ragStore.FindSimilar(ctx, questionEmbedding.Vector, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to find similar content: %w", err)
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

	streamHandler := CreateStreamHandler(ctx, logging.GetLogger())
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

// convertAgenticResultToContent converts agentic search results to traditional Content format.
func (p *RepoQAProcessor) convertAgenticResultToContent(result *SynthesizedResult) []*Content {
	var contents []*Content

	// MOST IMPORTANT: Include the synthesized summary as the primary content
	// This is what the QA processor will actually use to generate the answer
	if result.Summary != "" {
		summaryContent := &Content{
			ID:   "agentic-synthesis",
			Text: result.Summary,
			Metadata: map[string]string{
				"file_path":    "synthesis_result.md",
				"content_type": ContentTypeRepository,
				"source":       "agentic_search",
				"relevance":    fmt.Sprintf("%.2f", result.ConfidenceScore),
				"summary":      "true",
			},
		}
		contents = append(contents, summaryContent)
	}

	// Convert code samples to Content
	for i, sample := range result.CodeSamples {
		content := &Content{
			ID:   fmt.Sprintf("agentic-code-%d", i),
			Text: sample.Content,
			Metadata: map[string]string{
				"file_path":    sample.FilePath,
				"content_type": ContentTypeRepository,
				"explanation":  sample.Explanation,
				"relevance":    fmt.Sprintf("%.2f", sample.Relevance),
				"start_line":   "1",  // Default values for compatibility
				"end_line":     "50", // Will be improved in future
			},
		}
		contents = append(contents, content)
	}

	// Convert guidelines to Content
	for i, guideline := range result.Guidelines {
		content := &Content{
			ID:   fmt.Sprintf("agentic-guideline-%d", i),
			Text: guideline.Description,
			Metadata: map[string]string{
				"file_path":    fmt.Sprintf("guidelines/%s", guideline.Source),
				"content_type": ContentTypeGuideline,
				"title":        guideline.Title,
				"relevance":    fmt.Sprintf("%.2f", guideline.Relevance),
				"start_line":   "1",
				"end_line":     "1",
			},
		}
		contents = append(contents, content)
	}

	return contents
}
