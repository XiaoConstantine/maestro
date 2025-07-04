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

	// Create embedding and find similar content
	llm := core.GetTeacherLLM()
	questionEmbedding, err := llm.CreateEmbedding(ctx, metadata.Question)

	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	similar, err := p.ragStore.FindSimilar(ctx, questionEmbedding.Vector, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to find similar content: %w", err)
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
		return fmt.Errorf("invalid result type: %T", result)
	}

	if answer, ok := resultMap["answer"].(string); ok {
		response.Answer = answer
	} else {
		return fmt.Errorf("missing or invalid answer")
	}

	if confidence, ok := resultMap["confidence"].(float64); ok {
		response.Confidence = confidence
	} else {
		response.Confidence = 0.7
	}

	return nil
}
