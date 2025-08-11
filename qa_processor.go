package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
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
	signatureHash := hex.EncodeToString(hasher.Sum(nil))

	// Try to get from cache
	if cached, ok := qaModuleCache.Load(signatureHash); ok {
		if predict, ok := cached.(*modules.Predict); ok {
			return predict
		}
	}

	// Create new and cache
	predict := modules.NewPredict(signature).WithName("QAAnalyzer")
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
			{Field: core.NewField("answer")},
			{Field: core.NewField("confidence")},
			{Field: core.NewField("source_files")},
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
		similar := convertAgenticResultToContent(result)
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

	for _, content := range similar {
		contextBuilder.WriteString(fmt.Sprintf("File: %s\n", content.Metadata["file_path"]))
		contextBuilder.WriteString(fmt.Sprintf("Lines %s-%s:\n",
			content.Metadata["start_line"],
			content.Metadata["end_line"]))
		contextBuilder.WriteString(content.Text)
		contextBuilder.WriteString("\n---\n")

		sourceFiles = append(sourceFiles, content.Metadata["file_path"])
	}

	// Use cached predict module
	predict := getCachedPredictModule(signature)
	streamHandler := CreateStreamHandler(ctx, logging.GetLogger())
	result, err := predict.Process(ctx, map[string]interface{}{
		"question":         metadata.Question,
		"relevant_context": contextBuilder.String(),
	}, core.WithStreamHandler(streamHandler))
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

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
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		// If we can't parse the result, treat it as a string answer
		if str, ok := result.(string); ok && str != "" {
			response.Answer = "I found some information, but couldn't parse it properly. Raw response: " + str
			response.Confidence = 0.3
			return nil
		}
		return fmt.Errorf("invalid result type: %T", result)
	}

	if answer, ok := resultMap["answer"].(string); ok && answer != "" {
		response.Answer = answer
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
		// Try to parse confidence as string
		if conf, err := fmt.Sscanf(confidenceStr, "%f", new(float64)); err == nil && conf == 1 {
			var parsedConf float64
			_, _ = fmt.Sscanf(confidenceStr, "%f", &parsedConf)
			response.Confidence = parsedConf
		} else {
			response.Confidence = 0.5
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

// convertAgenticResultToContent converts agentic search results to traditional Content format.
func convertAgenticResultToContent(result *SynthesizedResult) []*Content {
	var contents []*Content

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
