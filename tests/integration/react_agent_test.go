//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/agent"
	"github.com/XiaoConstantine/maestro/internal/search"
)

// skipIfNoLLMKey skips the test if no LLM API key is available.
func skipIfNoLLMKey(t *testing.T) {
	t.Helper()
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("No LLM API key set (ANTHROPIC_API_KEY, GOOGLE_API_KEY, or OPENAI_API_KEY)")
	}
}

// setupLLM initializes an LLM for testing.
func setupLLM(t *testing.T) {
	t.Helper()

	llms.EnsureFactory()

	var apiKey string
	var modelID core.ModelID

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		apiKey = key
		modelID = core.ModelAnthropicSonnet
		t.Logf("Using Anthropic Claude for tests, model ID: %s", modelID)
	} else if key := os.Getenv("GOOGLE_API_KEY"); key != "" {
		apiKey = key
		modelID = core.ModelGoogleGeminiFlash
		t.Log("Using Google Gemini for tests")
	} else if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		apiKey = key
		modelID = core.ModelOpenAIGPT4oMini
		t.Log("Using OpenAI GPT for tests")
	}

	if err := core.ConfigureDefaultLLM(apiKey, modelID); err != nil {
		t.Fatalf("Failed to configure LLM: %v", err)
	}
}

// TestReActAgent_BasicSearch tests basic search functionality.
func TestReActAgent_BasicSearch(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	logger := logging.GetLogger()

	// Use maestro's own codebase as test target
	repoPath, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to get repo path: %v", err)
	}

	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	reactAgent, err := agent.NewUnifiedReActAgent("test-basic", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create ReAct agent: %v", err)
	}

	request := &search.SearchRequest{
		Query:         "Find the main entry point of the application",
		Context:       "Looking for the main function in a Go application",
		MaxResults:    5,
		RequiredDepth: 2,
	}

	t.Log("Executing basic search...")
	response, err := reactAgent.ExecuteSearch(ctx, request)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	t.Logf("Search completed in %v", response.Duration)
	t.Logf("Results: %d, Confidence: %.2f", len(response.Results), response.Confidence)
	t.Logf("Synthesis: %s", truncate(response.Synthesis, 500))

	// Verify we got results
	if len(response.Results) == 0 {
		t.Error("Expected at least one result")
	}

	// Verify confidence is reasonable
	if response.Confidence < 0.3 {
		t.Errorf("Confidence too low: %.2f", response.Confidence)
	}
}

// TestReActAgent_MultiStepReasoning tests the agent's ability to reason through multiple steps.
func TestReActAgent_MultiStepReasoning(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	logger := logging.GetLogger()

	repoPath, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to get repo path: %v", err)
	}

	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	reactAgent, err := agent.NewUnifiedReActAgent("test-reasoning", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create ReAct agent: %v", err)
	}

	// Query that requires multi-step reasoning
	request := &search.SearchRequest{
		Query:         "How does the PR review process work? Trace the flow from receiving a PR to generating comments.",
		Context:       "Need to understand the architecture and data flow",
		MaxResults:    10,
		RequiredDepth: 4,
	}

	t.Log("Executing multi-step reasoning search...")
	response, err := reactAgent.ExecuteSearch(ctx, request)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	t.Logf("Search completed in %v", response.Duration)
	t.Logf("Iterations: %d", response.SearchIterations)
	t.Logf("Results: %d, Confidence: %.2f", len(response.Results), response.Confidence)
	t.Logf("Synthesis:\n%s", response.Synthesis)

	// Multi-step queries should use multiple iterations
	if response.SearchIterations < 1 {
		t.Logf("Warning: Expected multiple iterations for complex query, got %d", response.SearchIterations)
	}

	// Should find relevant files
	foundRelevant := false
	relevantPatterns := []string{"review", "agent", "pr", "comment"}
	for _, result := range response.Results {
		for _, pattern := range relevantPatterns {
			if strings.Contains(strings.ToLower(result.FilePath), pattern) ||
				strings.Contains(strings.ToLower(result.Line), pattern) {
				foundRelevant = true
				break
			}
		}
	}

	if !foundRelevant {
		t.Log("Warning: Did not find obviously relevant results in output")
	}
}

// TestReActAgent_ContextAwareness tests the agent's ability to use context in searches.
func TestReActAgent_ContextAwareness(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	logger := logging.GetLogger()

	repoPath, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to get repo path: %v", err)
	}

	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	reactAgent, err := agent.NewUnifiedReActAgent("test-context", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create ReAct agent: %v", err)
	}

	// Query with specific context that should guide the search
	request := &search.SearchRequest{
		Query:   "error handling",
		Context: "Focus on how errors are handled in the RAG store implementation, specifically around database operations",
		MaxResults:    5,
		RequiredDepth: 3,
		FocusAreas:    []string{"internal/rag"},
	}

	t.Log("Executing context-aware search...")
	response, err := reactAgent.ExecuteSearch(ctx, request)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	t.Logf("Search completed in %v", response.Duration)
	t.Logf("Results: %d, Confidence: %.2f", len(response.Results), response.Confidence)

	// Check if results are focused on the specified area
	ragResults := 0
	for _, result := range response.Results {
		if strings.Contains(result.FilePath, "rag") {
			ragResults++
			t.Logf("Found RAG result: %s", result.FilePath)
		}
	}

	t.Logf("RAG-related results: %d/%d", ragResults, len(response.Results))
	t.Logf("Synthesis: %s", truncate(response.Synthesis, 500))
}

// TestReActAgent_SemanticSearch tests semantic search capabilities via sgrep.
func TestReActAgent_SemanticSearch(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	logger := logging.GetLogger()

	repoPath, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to get repo path: %v", err)
	}

	// Check if sgrep index exists
	sgrepIndexPath := filepath.Join(repoPath, ".sgrep")
	if _, err := os.Stat(sgrepIndexPath); os.IsNotExist(err) {
		t.Skip("sgrep index not found - run 'sgrep index .' in repo root first")
	}

	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	reactAgent, err := agent.NewUnifiedReActAgent("test-semantic", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create ReAct agent: %v", err)
	}

	// Semantic query - conceptual rather than keyword-based
	request := &search.SearchRequest{
		Query:         "How are embeddings generated and stored for code search",
		Context:       "Understanding the semantic search infrastructure",
		MaxResults:    10,
		RequiredDepth: 3,
	}

	t.Log("Executing semantic search...")
	response, err := reactAgent.ExecuteSearch(ctx, request)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	t.Logf("Search completed in %v", response.Duration)
	t.Logf("Results: %d, Confidence: %.2f", len(response.Results), response.Confidence)
	t.Logf("Synthesis:\n%s", response.Synthesis)

	// Should find embedding-related code
	foundEmbedding := false
	for _, result := range response.Results {
		if strings.Contains(strings.ToLower(result.Line), "embedding") ||
			strings.Contains(strings.ToLower(result.Line), "vector") ||
			strings.Contains(strings.ToLower(result.FilePath), "rag") {
			foundEmbedding = true
			t.Logf("Found relevant result: %s", result.FilePath)
		}
	}

	if !foundEmbedding {
		t.Log("Warning: Did not find embedding-related results")
	}
}

// TestReActAgent_ToolSelection tests that the agent selects appropriate tools.
func TestReActAgent_ToolSelection(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	logger := logging.GetLogger()

	repoPath, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to get repo path: %v", err)
	}

	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	reactAgent, err := agent.NewUnifiedReActAgent("test-tools", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create ReAct agent: %v", err)
	}

	testCases := []struct {
		name          string
		query         string
		context       string
		expectPattern string // Pattern we expect in results
	}{
		{
			name:          "File pattern search",
			query:         "Find all Go test files",
			context:       "Looking for test files in the codebase",
			expectPattern: "_test.go",
		},
		{
			name:          "Content search",
			query:         "Find where PRReviewAgent is defined",
			context:       "Looking for struct definition",
			expectPattern: "PRReviewAgent",
		},
		{
			name:          "Conceptual search",
			query:         "How does the agent pool manage concurrency",
			context:       "Understanding concurrent agent execution",
			expectPattern: "pool",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := &search.SearchRequest{
				Query:         tc.query,
				Context:       tc.context,
				MaxResults:    5,
				RequiredDepth: 2,
			}

			response, err := reactAgent.ExecuteSearch(ctx, request)
			if err != nil {
				t.Fatalf("Search failed: %v", err)
			}

			t.Logf("Query: %s", tc.query)
			t.Logf("Results: %d, Confidence: %.2f", len(response.Results), response.Confidence)

			// Check if expected pattern appears in results
			found := false
			for _, result := range response.Results {
				if strings.Contains(result.FilePath, tc.expectPattern) ||
					strings.Contains(result.Line, tc.expectPattern) {
					found = true
					break
				}
			}

			if !found {
				t.Logf("Warning: Expected pattern '%s' not found in results", tc.expectPattern)
				t.Logf("Synthesis: %s", truncate(response.Synthesis, 300))
			}
		})
	}
}

// TestReActAgent_ErrorRecovery tests the agent's ability to recover from errors.
func TestReActAgent_ErrorRecovery(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	logger := logging.GetLogger()

	repoPath, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to get repo path: %v", err)
	}

	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	reactAgent, err := agent.NewUnifiedReActAgent("test-recovery", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create ReAct agent: %v", err)
	}

	// Query for something that likely doesn't exist - agent should handle gracefully
	request := &search.SearchRequest{
		Query:         "Find the implementation of quantum computing algorithms",
		Context:       "Looking for quantum computing code",
		MaxResults:    5,
		RequiredDepth: 2,
	}

	t.Log("Executing search for non-existent content...")
	response, err := reactAgent.ExecuteSearch(ctx, request)
	if err != nil {
		t.Fatalf("Search should not fail completely: %v", err)
	}

	t.Logf("Search completed in %v", response.Duration)
	t.Logf("Results: %d, Confidence: %.2f", len(response.Results), response.Confidence)
	t.Logf("Synthesis: %s", truncate(response.Synthesis, 300))

	// Agent should complete without error, even if no results found
	// Low confidence is expected for non-existent content
}

// TestReActAgent_QueryAnalysis tests query analysis and classification.
func TestReActAgent_QueryAnalysis(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	logger := logging.GetLogger()

	repoPath, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to get repo path: %v", err)
	}

	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	queries := []struct {
		query       string
		context     string
		description string
	}{
		{
			query:       "*.go files in internal/agent",
			context:     "File listing",
			description: "Simple file pattern - should use search_files",
		},
		{
			query:       "func NewUnifiedReActAgent",
			context:     "Find function definition",
			description: "Specific code search - should use search_content",
		},
		{
			query:       "Explain the architecture of the review system",
			context:     "Need high-level understanding",
			description: "Conceptual query - should use semantic_search or multiple tools",
		},
	}

	for _, q := range queries {
		t.Run(q.description, func(t *testing.T) {
			reactAgent, err := agent.NewUnifiedReActAgent("test-analysis", searchTool, logger)
			if err != nil {
				t.Fatalf("Failed to create ReAct agent: %v", err)
			}

			request := &search.SearchRequest{
				Query:         q.query,
				Context:       q.context,
				MaxResults:    5,
				RequiredDepth: 2,
			}

			response, err := reactAgent.ExecuteSearch(ctx, request)
			if err != nil {
				t.Fatalf("Search failed: %v", err)
			}

			t.Logf("Query type: %s", q.description)
			t.Logf("Results: %d, Confidence: %.2f, Iterations: %d",
				len(response.Results), response.Confidence, response.SearchIterations)

			// Log current mode to understand query classification
			mode := reactAgent.GetCurrentMode()
			t.Logf("Agent mode: %v", mode)
		})
	}
}

// TestReActAgent_IterativeRefinement tests the agent's ability to refine searches
// based on intermediate findings - a key aspect of context engineering.
func TestReActAgent_IterativeRefinement(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	logger := logging.GetLogger()

	repoPath, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to get repo path: %v", err)
	}

	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	reactAgent, err := agent.NewUnifiedReActAgent("test-refinement", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create ReAct agent: %v", err)
	}

	// Start with a broad query that requires refinement
	request := &search.SearchRequest{
		Query:         "How does the system handle configuration",
		Context:       "Start broad, then narrow down to specific config loading and validation patterns",
		MaxResults:    10,
		RequiredDepth: 4, // Higher depth to allow refinement iterations
	}

	t.Log("Executing iterative refinement search...")
	response, err := reactAgent.ExecuteSearch(ctx, request)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	t.Logf("Search completed in %v", response.Duration)
	t.Logf("Iterations: %d", response.SearchIterations)
	t.Logf("Results: %d, Confidence: %.2f", len(response.Results), response.Confidence)

	// Check if agent used multiple iterations (indicates refinement)
	if response.SearchIterations > 1 {
		t.Logf("Agent refined search across %d iterations", response.SearchIterations)
	} else {
		t.Log("Warning: Agent completed in single iteration - may not have refined")
	}

	// Verify results span different aspects of configuration
	configAspects := map[string]bool{
		"load":     false, // config loading
		"parse":    false, // config parsing
		"validate": false, // config validation
		"default":  false, // default values
	}

	for _, result := range response.Results {
		content := strings.ToLower(result.FilePath + " " + result.Line)
		for aspect := range configAspects {
			if strings.Contains(content, aspect) {
				configAspects[aspect] = true
			}
		}
	}

	foundAspects := 0
	for aspect, found := range configAspects {
		if found {
			foundAspects++
			t.Logf("Found config aspect: %s", aspect)
		}
	}

	t.Logf("Coverage: %d/%d config aspects found", foundAspects, len(configAspects))
	t.Logf("Synthesis: %s", truncate(response.Synthesis, 500))
}

// TestReActAgent_ContextAccumulation tests the agent's ability to accumulate
// understanding across iterations and use prior findings to inform new searches.
func TestReActAgent_ContextAccumulation(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	logger := logging.GetLogger()

	repoPath, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to get repo path: %v", err)
	}

	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	// First search: Find entry points
	reactAgent1, err := agent.NewUnifiedReActAgent("test-accum-1", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create ReAct agent: %v", err)
	}

	request1 := &search.SearchRequest{
		Query:         "Find the main components and entry points",
		Context:       "Understanding the high-level architecture",
		MaxResults:    5,
		RequiredDepth: 2,
	}

	t.Log("Phase 1: Finding entry points...")
	response1, err := reactAgent1.ExecuteSearch(ctx, request1)
	if err != nil {
		t.Fatalf("Phase 1 search failed: %v", err)
	}

	t.Logf("Phase 1 - Results: %d, Confidence: %.2f", len(response1.Results), response1.Confidence)

	// Extract findings from first search to use as context
	var findings []string
	for _, result := range response1.Results {
		findings = append(findings, fmt.Sprintf("%s: %s", result.FilePath, truncate(result.Line, 100)))
	}

	// Second search: Use accumulated context to go deeper
	reactAgent2, err := agent.NewUnifiedReActAgent("test-accum-2", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create ReAct agent: %v", err)
	}

	// Build context from previous findings
	accumulatedContext := fmt.Sprintf(
		"Based on previous analysis, the system has these key components:\n%s\n\nNow trace how data flows between these components.",
		strings.Join(findings, "\n"),
	)

	request2 := &search.SearchRequest{
		Query:         "How do these components interact and pass data",
		Context:       accumulatedContext,
		MaxResults:    10,
		RequiredDepth: 3,
	}

	t.Log("Phase 2: Tracing interactions with accumulated context...")
	response2, err := reactAgent2.ExecuteSearch(ctx, request2)
	if err != nil {
		t.Fatalf("Phase 2 search failed: %v", err)
	}

	t.Logf("Phase 2 - Results: %d, Confidence: %.2f", len(response2.Results), response2.Confidence)
	t.Logf("Phase 2 - Iterations: %d", response2.SearchIterations)

	// Compare confidence - accumulated context should help
	t.Logf("Confidence progression: %.2f -> %.2f", response1.Confidence, response2.Confidence)

	// Check if phase 2 found related but deeper information
	phase1Files := make(map[string]bool)
	for _, r := range response1.Results {
		phase1Files[r.FilePath] = true
	}

	newFindings := 0
	relatedFindings := 0
	for _, r := range response2.Results {
		if phase1Files[r.FilePath] {
			relatedFindings++
		} else {
			newFindings++
		}
	}

	t.Logf("Phase 2 found %d new files, %d related files", newFindings, relatedFindings)
	t.Logf("Synthesis: %s", truncate(response2.Synthesis, 500))
}

// TestReActAgent_SelfEvolvingStrategy tests the agent's ability to adapt its
// search strategy based on what it discovers during execution.
func TestReActAgent_SelfEvolvingStrategy(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	logger := logging.GetLogger()

	repoPath, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to get repo path: %v", err)
	}

	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	reactAgent, err := agent.NewUnifiedReActAgent("test-evolving", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create ReAct agent: %v", err)
	}

	// Query that may require strategy pivots
	// Initial approach might be file search, but should evolve to content search
	request := &search.SearchRequest{
		Query:         "Find where errors are logged and how error messages are formatted",
		Context:       "Need to understand error handling patterns - may need to search both file structure and content",
		MaxResults:    10,
		RequiredDepth: 4,
	}

	t.Log("Executing self-evolving strategy search...")
	response, err := reactAgent.ExecuteSearch(ctx, request)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	t.Logf("Search completed in %v", response.Duration)
	t.Logf("Iterations: %d", response.SearchIterations)

	// Check current mode to see if strategy evolved
	mode := reactAgent.GetCurrentMode()
	t.Logf("Final agent mode: %v", mode)

	// Analyze result diversity - good strategy should find varied results
	fileTypes := make(map[string]int)
	for _, result := range response.Results {
		ext := filepath.Ext(result.FilePath)
		fileTypes[ext]++
	}

	t.Logf("Result diversity - file types: %v", fileTypes)
	t.Logf("Results: %d, Confidence: %.2f", len(response.Results), response.Confidence)

	// Check if results cover multiple aspects of error handling
	errorPatterns := []string{"error", "err", "log", "format", "wrap", "handle"}
	patternsFound := 0
	for _, pattern := range errorPatterns {
		for _, result := range response.Results {
			if strings.Contains(strings.ToLower(result.Line), pattern) {
				patternsFound++
				break
			}
		}
	}

	t.Logf("Error handling patterns found: %d/%d", patternsFound, len(errorPatterns))
	t.Logf("Synthesis: %s", truncate(response.Synthesis, 500))
}

// truncate helper to limit string length for logging.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
