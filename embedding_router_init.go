package main

import (
	"context"
	"os"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// Global embedding router instance.
var (
	globalEmbeddingRouter *EmbeddingRouter
	routerInitOnce        sync.Once
)

// GetEmbeddingRouter returns the global embedding router instance.
// Initializes it on first call.
func GetEmbeddingRouter() *EmbeddingRouter {
	routerInitOnce.Do(func() {
		globalEmbeddingRouter = initializeEmbeddingRouter()
	})
	return globalEmbeddingRouter
}

// initializeEmbeddingRouter sets up the embedding router with local and cloud models.
func initializeEmbeddingRouter() *EmbeddingRouter {
	logger := logging.GetLogger()
	ctx := context.Background()

	// Cloud LLM is always available (uses dspy-go default)
	cloudLLM := core.GetTeacherLLM()

	// Local LLM is optional - only if enabled
	var localLLM core.LLM
	if isLocalEmbeddingEnabled() {
		localLLM = initializeLocalLLM(ctx, logger)
	}

	router := NewEmbeddingRouter(localLLM, cloudLLM)

	if localLLM != nil {
		logger.Info(ctx, "Local embeddings enabled - using smart routing for cost savings")
	} else {
		logger.Info(ctx, "Local embeddings disabled - using cloud models only")
	}

	return router
}

// isLocalEmbeddingEnabled checks if local embeddings are enabled via environment variables.
func isLocalEmbeddingEnabled() bool {
	enabled := os.Getenv("MAESTRO_LOCAL_EMBEDDING_ENABLED")
	return enabled == "true" || enabled == "1"
}

// initializeLocalLLM creates the local embedding model (ollama/llamacpp/sgrep).
func initializeLocalLLM(ctx context.Context, logger *logging.Logger) core.LLM {
	provider := os.Getenv("MAESTRO_LOCAL_EMBEDDING_PROVIDER")
	if provider == "" {
		provider = "ollama" // Default to Ollama
	}

	endpoint := os.Getenv("MAESTRO_LOCAL_EMBEDDING_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:11434" // Default Ollama endpoint
	}

	model := os.Getenv("MAESTRO_LOCAL_EMBEDDING_MODEL")
	if model == "" {
		model = "nomic-embed-text" // Default model (768 dims, perfect for compatibility)
	}

	logger.Info(ctx, "Initializing local embedding model: provider=%s, model=%s, endpoint=%s", provider, model, endpoint)

	var llm core.LLM
	var err error

	// Try to initialize based on provider
	switch provider {
	case "ollama":
		// Ollama requires ModelID and functional options for endpoint
		llm, err = llms.NewOllamaLLM(
			core.ModelID(model),
			llms.WithBaseURL(endpoint),
		)
		if err != nil {
			logger.Warn(ctx, "Failed to initialize Ollama LLM: %v - will use cloud fallback", err)
			return nil
		}
		logger.Info(ctx, "Successfully initialized Ollama LLM at %s with model %s", endpoint, model)
	case "llamacpp":
		// Llamacpp only needs endpoint (model is loaded server-side)
		llm, err = llms.NewLlamacppLLM(endpoint)
		if err != nil {
			logger.Warn(ctx, "Failed to initialize llamacpp LLM: %v - will use cloud fallback", err)
			return nil
		}
		logger.Info(ctx, "Successfully initialized llamacpp LLM at %s", endpoint)
	case "sgrep":
		// Use sgrep's local embedding server (auto-starts llama-server)
		llm, err = NewSgrepEmbeddingLLM()
		if err != nil {
			logger.Warn(ctx, "Failed to initialize sgrep embedder: %v - will use cloud fallback", err)
			return nil
		}
		logger.Info(ctx, "Successfully initialized sgrep local embedding (nomic-embed-text)")
	default:
		logger.Warn(ctx, "Unknown local embedding provider: %s - will use cloud only", provider)
		return nil
	}

	return llm
}

// SetEmbeddingRouter sets the global embedding router instance (for testing).
func SetEmbeddingRouter(router *EmbeddingRouter) {
	globalEmbeddingRouter = router
}
