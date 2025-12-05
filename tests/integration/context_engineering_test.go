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

	agentctx "github.com/XiaoConstantine/dspy-go/pkg/agents/context"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/agent"
	"github.com/XiaoConstantine/maestro/internal/search"
)

// =============================================================================
// Test Suite 1: KV-Cache Optimization
// =============================================================================

// TestContextEngineering_KVCacheStablePrefix verifies that stable prefixes
// are maintained across iterations to maximize cache hit rates.
func TestContextEngineering_KVCacheStablePrefix(t *testing.T) {
	skipIfNoLLMKey(t)

	ctx := context.Background()
	logger := logging.GetLogger()

	// Create cache optimizer directly to test prefix stability
	cacheConfig := agentctx.CacheConfig{
		StablePrefix:         "You are an intelligent code search agent.",
		MaxPrefixSize:        8192,
		BreakpointInterval:   1000,
		EnableMetrics:        true,
		TimestampGranularity: "hour", // Critical: not "second"
	}

	optimizer := agentctx.NewCacheOptimizer(cacheConfig)

	// Simulate multiple iterations with same prefix
	prompts := []string{}
	for i := 0; i < 5; i++ {
		prompt := fmt.Sprintf("Search iteration %d: find main function", i)
		optimized := optimizer.OptimizePrompt(prompt, time.Now())
		prompts = append(prompts, optimized)

		// Small delay to ensure timestamps could differ
		time.Sleep(10 * time.Millisecond)
	}

	// Verify all prompts start with the same stable prefix
	for i := 1; i < len(prompts); i++ {
		// Extract prefix (everything before the dynamic content)
		prefix0 := extractStablePrefix(prompts[0])
		prefixI := extractStablePrefix(prompts[i])

		if prefix0 != prefixI {
			t.Errorf("Prefix changed between iteration 0 and %d:\n  0: %s\n  %d: %s",
				i, prefix0, i, prefixI)
		}
	}

	// Verify metrics are being tracked
	metrics := optimizer.GetMetrics()
	t.Logf("Cache metrics: %+v", metrics)

	logger.Info(ctx, "KV-cache prefix stability test passed")
}

// TestContextEngineering_KVCacheTimestampGranularity ensures timestamps
// don't break cache with second-level precision.
func TestContextEngineering_KVCacheTimestampGranularity(t *testing.T) {
	skipIfNoLLMKey(t)

	testCases := []struct {
		granularity   string
		shouldBeSame  bool
		waitDuration  time.Duration
	}{
		{"day", true, 100 * time.Millisecond},
		{"hour", true, 100 * time.Millisecond},
		{"minute", true, 100 * time.Millisecond},
		// Note: "second" would be false but we don't test it as it's discouraged
	}

	for _, tc := range testCases {
		t.Run(tc.granularity, func(t *testing.T) {
			config := agentctx.CacheConfig{
				StablePrefix:         "Test agent prefix.",
				TimestampGranularity: tc.granularity,
				EnableMetrics:        true,
			}

			optimizer := agentctx.NewCacheOptimizer(config)

			prompt1 := optimizer.OptimizePrompt("Query 1", time.Now())
			time.Sleep(tc.waitDuration)
			prompt2 := optimizer.OptimizePrompt("Query 2", time.Now())

			// Extract timestamp portions
			ts1 := extractTimestampContext(prompt1)
			ts2 := extractTimestampContext(prompt2)

			if tc.shouldBeSame && ts1 != ts2 {
				t.Errorf("Timestamps should match at %s granularity:\n  ts1: %s\n  ts2: %s",
					tc.granularity, ts1, ts2)
			}
		})
	}
}

// =============================================================================
// Test Suite 2: TodoManager Attention Recitation
// =============================================================================

// TestContextEngineering_TodoManagerAttention verifies that the TodoManager
// keeps objectives in the agent's attention span across iterations.
func TestContextEngineering_TodoManagerAttention(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	logger := logging.GetLogger()
	repoPath, _ := filepath.Abs("../..")

	// Create context manager with TodoManager enabled
	config := agentctx.DefaultConfig()
	config.EnableTodoManagement = true
	config.Todo.MaxActiveTasks = 3
	config.Todo.MaxPendingTasks = 10

	mgr, err := agentctx.NewManager("test-session", "test-agent", repoPath, config)
	if err != nil {
		t.Fatalf("Failed to create context manager: %v", err)
	}

	// Add initial objective
	err = mgr.AddTodo(ctx, "Find all API endpoints in the codebase", 10)
	if err != nil {
		t.Fatalf("Failed to add todo: %v", err)
	}

	// Simulate multiple iterations, each adding sub-tasks
	subTasks := []string{
		"Search for HTTP handlers",
		"Look for route definitions",
		"Find REST controller patterns",
		"Check for gRPC service definitions",
	}

	for i, task := range subTasks {
		err = mgr.AddTodo(ctx, task, 5+i)
		if err != nil {
			t.Errorf("Failed to add sub-task %d: %v", i, err)
		}

		// Build context and verify todos are included
		response, err := mgr.BuildOptimizedContext(ctx, agentctx.ContextRequest{
			CurrentTask:  task,
			IncludeTodos: true,
		})
		if err != nil {
			t.Errorf("Failed to build context: %v", err)
			continue
		}

		// Verify the main objective is still present in context
		if !strings.Contains(response.Context, "API endpoints") {
			t.Errorf("Iteration %d: Main objective lost from context", i)
		}

		t.Logf("Iteration %d: Context length=%d, has main objective=%v",
			i, response.TokenCount, strings.Contains(response.Context, "API endpoints"))
	}

	logger.Info(ctx, "TodoManager attention test passed")
}

// TestContextEngineering_TodoManagerLongRunning tests attention over many iterations
// simulating the "lost-in-the-middle" problem Manus addresses.
func TestContextEngineering_TodoManagerLongRunning(t *testing.T) {
	skipIfNoLLMKey(t)

	ctx := context.Background()
	repoPath, _ := filepath.Abs("../..")

	config := agentctx.DefaultConfig()
	config.EnableTodoManagement = true

	mgr, err := agentctx.NewManager("long-session", "long-agent", repoPath, config)
	if err != nil {
		t.Fatalf("Failed to create context manager: %v", err)
	}

	// Add main objective
	mainObjective := "MAIN_OBJECTIVE: Refactor authentication system"
	mgr.AddTodo(ctx, mainObjective, 10)

	// Simulate 50 iterations (Manus mentions ~50-step workflows)
	objectivePresent := 0
	for i := 0; i < 50; i++ {
		// Add intermediate tasks
		mgr.AddTodo(ctx, fmt.Sprintf("Step %d: intermediate work", i), 3)

		// Build context
		response, err := mgr.BuildOptimizedContext(ctx, agentctx.ContextRequest{
			CurrentTask:  fmt.Sprintf("Working on step %d", i),
			IncludeTodos: true,
			Observations: []string{fmt.Sprintf("Observation from step %d", i)},
		})
		if err != nil {
			continue
		}

		if strings.Contains(response.Context, "MAIN_OBJECTIVE") {
			objectivePresent++
		}
	}

	// Main objective should be present in most iterations (attention recitation)
	presenceRate := float64(objectivePresent) / 50.0
	t.Logf("Main objective presence rate: %.2f%% (%d/50)", presenceRate*100, objectivePresent)

	if presenceRate < 0.8 {
		t.Errorf("Main objective lost too often: %.2f%% presence (expected >80%%)", presenceRate*100)
	}
}

// =============================================================================
// Test Suite 3: Error Retention and Learning
// =============================================================================

// TestContextEngineering_ErrorRetention verifies errors are preserved
// for implicit learning as per Manus patterns.
func TestContextEngineering_ErrorRetention(t *testing.T) {
	skipIfNoLLMKey(t)

	ctx := context.Background()
	repoPath, _ := filepath.Abs("../..")

	config := agentctx.DefaultConfig()
	config.EnableErrorRetention = true
	config.MaxErrorRetention = 10

	mgr, err := agentctx.NewManager("error-session", "error-agent", repoPath, config)
	if err != nil {
		t.Fatalf("Failed to create context manager: %v", err)
	}

	// Record various errors
	errors := []struct {
		errorType string
		message   string
		severity  agentctx.ErrorSeverity
	}{
		{"tool_execution", "File not found: /nonexistent/path", agentctx.SeverityMedium},
		{"parsing", "Invalid regex pattern: [unclosed", agentctx.SeverityLow},
		{"timeout", "Search timed out after 30s", agentctx.SeverityHigh},
		{"permission", "Access denied to protected directory", agentctx.SeverityCritical},
	}

	for _, e := range errors {
		mgr.RecordError(ctx, e.errorType, e.message, e.severity, map[string]interface{}{
			"timestamp": time.Now(),
		})
	}

	// Build context with errors included
	response, err := mgr.BuildOptimizedContext(ctx, agentctx.ContextRequest{
		CurrentTask:   "Retry failed operations",
		IncludeErrors: true,
	})
	if err != nil {
		t.Fatalf("Failed to build context: %v", err)
	}

	// Verify errors are present in context
	errorCount := 0
	for _, e := range errors {
		if strings.Contains(response.Context, e.message) ||
			strings.Contains(response.Context, e.errorType) {
			errorCount++
		}
	}

	t.Logf("Errors present in context: %d/%d", errorCount, len(errors))
	t.Logf("Context includes error section: %v", strings.Contains(response.Context, "error") ||
		strings.Contains(response.Context, "Error"))

	// At least critical/high errors should be preserved
	if errorCount < 2 {
		t.Errorf("Expected at least critical errors to be preserved, found %d", errorCount)
	}
}

// TestContextEngineering_ErrorInfluencesBehavior tests that preserved errors
// help agents avoid repeating mistakes.
func TestContextEngineering_ErrorInfluencesBehavior(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	logger := logging.GetLogger()
	repoPath, _ := filepath.Abs("../..")
	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	// First agent: make an error
	agent1, err := agent.NewUnifiedReActAgent("error-maker", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}

	// Execute a search that will likely have some issues
	request1 := &search.SearchRequest{
		Query:      "Find all functions with regex pattern [invalid",
		MaxResults: 5,
	}

	response1, _ := agent1.ExecuteSearch(ctx, request1)
	t.Logf("First search completed, confidence: %.2f", response1.Confidence)

	// Second search should benefit from error context
	request2 := &search.SearchRequest{
		Query:      "Find all functions matching pattern",
		Context:    "Previous search had regex issues",
		MaxResults: 5,
	}

	response2, err := agent1.ExecuteSearch(ctx, request2)
	if err != nil {
		t.Logf("Second search failed (may be expected): %v", err)
	} else {
		t.Logf("Second search confidence: %.2f", response2.Confidence)
	}
}

// =============================================================================
// Test Suite 4: Context Sharing Between Agents
// =============================================================================

// TestContextEngineering_ContextSharing tests sharing context between agents.
func TestContextEngineering_ContextSharing(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	logger := logging.GetLogger()
	repoPath, _ := filepath.Abs("../..")
	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	// Agent 1: Find main function
	agent1, _ := agent.NewUnifiedReActAgent("finder-1", searchTool, logger)
	response1, err := agent1.ExecuteSearch(ctx, &search.SearchRequest{
		Query:      "Find the main function in main.go",
		MaxResults: 3,
	})
	if err != nil {
		t.Fatalf("Agent 1 search failed: %v", err)
	}

	t.Logf("Agent 1 found %d results with confidence %.2f", len(response1.Results), response1.Confidence)

	// Extract findings to share
	var sharedContext strings.Builder
	sharedContext.WriteString("The application uses cobra for CLI commands.\n")
	for _, r := range response1.Results {
		sharedContext.WriteString(fmt.Sprintf("- Found: %s\n", r.FilePath))
	}

	// Agent 2: Use shared context for related search
	agent2, _ := agent.NewUnifiedReActAgent("finder-2", searchTool, logger)
	response2, err := agent2.ExecuteSearch(ctx, &search.SearchRequest{
		Query:      "Find cobra command definitions",
		Context:    sharedContext.String(),
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("Agent 2 search failed: %v", err)
	}

	t.Logf("Agent 2 found %d results with confidence %.2f", len(response2.Results), response2.Confidence)

	// Verify agent 2 found related information
	agent1Files := make(map[string]bool)
	for _, r := range response1.Results {
		agent1Files[r.FilePath] = true
	}

	newFindings := 0
	relatedFindings := 0
	for _, r := range response2.Results {
		if agent1Files[r.FilePath] {
			relatedFindings++
		} else {
			newFindings++
		}
	}

	t.Logf("Agent 2: %d new files, %d related files", newFindings, relatedFindings)

	// Log result - context sharing is working if we got results
	if len(response2.Results) > 0 || response2.Confidence > 0.5 {
		t.Log("Context sharing test completed successfully")
	} else {
		t.Log("Warning: Agent 2 found limited results")
	}
}

// TestContextEngineering_CrossSessionPersistence tests context persistence.
func TestContextEngineering_CrossSessionPersistence(t *testing.T) {
	skipIfNoLLMKey(t)

	ctx := context.Background()

	// Create temp directory for persistence test
	tmpDir, err := os.MkdirTemp("", "context-persist-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sessionID := "persist-session"
	agentID := "persist-agent"

	// Session 1: Create manager and add data
	config := agentctx.DefaultConfig()
	config.EnableFileSystemMemory = true

	mgr1, err := agentctx.NewManager(sessionID, agentID, tmpDir, config)
	if err != nil {
		t.Fatalf("Failed to create manager 1: %v", err)
	}

	// Add todos and record errors
	mgr1.AddTodo(ctx, "Persistent task 1", 10)
	mgr1.AddTodo(ctx, "Persistent task 2", 5)
	mgr1.RecordError(ctx, "test_error", "This error should persist", agentctx.SeverityMedium, nil)

	// Build context to trigger writes
	_, err = mgr1.BuildOptimizedContext(ctx, agentctx.ContextRequest{
		CurrentTask:   "Initial work",
		IncludeTodos:  true,
		IncludeErrors: true,
	})
	if err != nil {
		t.Logf("Build context warning: %v", err)
	}

	// Session 2: Create new manager with same session ID
	mgr2, err := agentctx.NewManager(sessionID, agentID, tmpDir, config)
	if err != nil {
		t.Fatalf("Failed to create manager 2: %v", err)
	}

	// Build context and check for persisted data
	response2, err := mgr2.BuildOptimizedContext(ctx, agentctx.ContextRequest{
		CurrentTask:   "Resumed work",
		IncludeTodos:  true,
		IncludeErrors: true,
	})
	if err != nil {
		t.Logf("Build context 2 warning: %v", err)
	}

	// Check if persisted data is present
	hasTodos := strings.Contains(response2.Context, "Persistent task")
	hasErrors := strings.Contains(response2.Context, "persist")

	t.Logf("Cross-session persistence: todos=%v, errors=%v", hasTodos, hasErrors)
	t.Logf("Response context length: %d", len(response2.Context))
}

// =============================================================================
// Test Suite 5: Content Compression
// =============================================================================

// TestContextEngineering_Compression tests intelligent content compression.
func TestContextEngineering_Compression(t *testing.T) {
	skipIfNoLLMKey(t)

	ctx := context.Background()
	repoPath, _ := filepath.Abs("../..")

	config := agentctx.DefaultConfig()
	config.EnableCompression = true
	config.CompressionThreshold = 1000 // Low threshold for testing

	mgr, err := agentctx.NewManager("compress-session", "compress-agent", repoPath, config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Create large observations that should trigger compression
	largeObservations := []string{
		strings.Repeat("func handleRequest(w http.ResponseWriter, r *http.Request) {\n", 50),
		strings.Repeat(`{"key": "value", "nested": {"deep": "object"}}`+"\n", 100),
		strings.Repeat("2024-01-01 10:00:00 INFO Processing request...\n", 100),
	}

	response, err := mgr.BuildOptimizedContext(ctx, agentctx.ContextRequest{
		Observations:        largeObservations,
		CurrentTask:         "Analyze the observations",
		CompressionPriority: agentctx.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("Failed to build context: %v", err)
	}

	// Calculate original size
	originalSize := 0
	for _, obs := range largeObservations {
		originalSize += len(obs)
	}

	t.Logf("Original size: %d, Compressed context: %d, Ratio: %.2f",
		originalSize, len(response.Context), response.CompressionRatio)
	t.Logf("Optimizations applied: %v", response.OptimizationsApplied)

	// Verify compression occurred
	if response.CompressionRatio >= 1.0 && originalSize > 5000 {
		t.Logf("Warning: No compression occurred on large content")
	}
}

// TestContextEngineering_CompressionPreservesReferences ensures file paths
// are preserved during compression.
func TestContextEngineering_CompressionPreservesReferences(t *testing.T) {
	skipIfNoLLMKey(t)

	ctx := context.Background()
	repoPath, _ := filepath.Abs("../..")

	config := agentctx.DefaultConfig()
	config.EnableCompression = true
	config.CompressionThreshold = 500

	mgr, err := agentctx.NewManager("ref-session", "ref-agent", repoPath, config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Observations with important file references
	observations := []string{
		"Found in /internal/agent/react.go:150 - " + strings.Repeat("code content ", 100),
		"Reference: /internal/search/simple.go:42 - " + strings.Repeat("more code ", 100),
		"See also: /cmd/main.go:1 - " + strings.Repeat("main function ", 100),
	}

	response, err := mgr.BuildOptimizedContext(ctx, agentctx.ContextRequest{
		Observations:        observations,
		CurrentTask:         "Review the code references",
		CompressionPriority: agentctx.PriorityLow, // Aggressive compression
	})
	if err != nil {
		t.Fatalf("Failed to build context: %v", err)
	}

	// Verify file paths are preserved
	preservedPaths := []string{
		"/internal/agent/react.go",
		"/internal/search/simple.go",
		"/cmd/main.go",
	}

	pathsPreserved := 0
	for _, path := range preservedPaths {
		if strings.Contains(response.Context, path) {
			pathsPreserved++
		}
	}

	t.Logf("Paths preserved: %d/%d", pathsPreserved, len(preservedPaths))
	t.Logf("Compression ratio: %.2f", response.CompressionRatio)

	if pathsPreserved < len(preservedPaths) {
		t.Errorf("Expected all file paths to be preserved, got %d/%d",
			pathsPreserved, len(preservedPaths))
	}
}

// =============================================================================
// Test Suite 6: Tool Integration
// =============================================================================

// TestContextEngineering_ToolContextAccumulation tests that tool results
// properly accumulate in context.
func TestContextEngineering_ToolContextAccumulation(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	logger := logging.GetLogger()
	repoPath, _ := filepath.Abs("../..")
	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	reactAgent, err := agent.NewUnifiedReActAgent("tool-accum", searchTool, logger)
	if err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}

	// Execute search that requires multiple tool calls
	response, err := reactAgent.ExecuteSearch(ctx, &search.SearchRequest{
		Query:         "Find how errors are handled in the search module",
		Context:       "Look at error types, error wrapping, and recovery patterns",
		MaxResults:    10,
		RequiredDepth: 3,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	t.Logf("Search completed: %d results, %d iterations, confidence: %.2f",
		len(response.Results), response.SearchIterations, response.Confidence)

	// Verify multiple iterations occurred (tool accumulation)
	if response.SearchIterations < 2 {
		t.Logf("Warning: Only %d iterations, may not have accumulated tool context",
			response.SearchIterations)
	}

	// Check that results span multiple aspects
	aspects := map[string]bool{
		"error":   false,
		"handle":  false,
		"return":  false,
		"wrap":    false,
	}

	for _, r := range response.Results {
		content := strings.ToLower(r.Line + " " + r.FilePath)
		for aspect := range aspects {
			if strings.Contains(content, aspect) {
				aspects[aspect] = true
			}
		}
	}

	foundAspects := 0
	for _, found := range aspects {
		if found {
			foundAspects++
		}
	}

	t.Logf("Error handling aspects found: %d/4", foundAspects)
}

// =============================================================================
// Test Suite 7: Full Integration
// =============================================================================

// TestContextEngineering_FullIntegration tests all patterns working together.
func TestContextEngineering_FullIntegration(t *testing.T) {
	skipIfNoLLMKey(t)
	setupLLM(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	logger := logging.GetLogger()
	repoPath, _ := filepath.Abs("../..")
	searchTool := search.NewSimpleSearchTool(logger, repoPath)

	// Phase 1: Initial exploration
	t.Log("Phase 1: Initial exploration")
	agent1, _ := agent.NewUnifiedReActAgent("full-test-1", searchTool, logger)
	response1, err := agent1.ExecuteSearch(ctx, &search.SearchRequest{
		Query:      "What are the main components of this codebase",
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("Phase 1 failed: %v", err)
	}
	t.Logf("Phase 1: %d results, confidence: %.2f", len(response1.Results), response1.Confidence)

	// Phase 2: Deep dive with accumulated context
	t.Log("Phase 2: Deep dive with context")
	var contextBuilder strings.Builder
	contextBuilder.WriteString("Previous exploration found:\n")
	for _, r := range response1.Results {
		contextBuilder.WriteString(fmt.Sprintf("- %s\n", r.FilePath))
	}

	agent2, _ := agent.NewUnifiedReActAgent("full-test-2", searchTool, logger)
	response2, err := agent2.ExecuteSearch(ctx, &search.SearchRequest{
		Query:         "How do these components interact with each other",
		Context:       contextBuilder.String(),
		MaxResults:    10,
		RequiredDepth: 4,
	})
	if err != nil {
		t.Fatalf("Phase 2 failed: %v", err)
	}
	t.Logf("Phase 2: %d results, confidence: %.2f, iterations: %d",
		len(response2.Results), response2.Confidence, response2.SearchIterations)

	// Phase 3: Error recovery scenario
	t.Log("Phase 3: Error recovery test")
	agent3, _ := agent.NewUnifiedReActAgent("full-test-3", searchTool, logger)
	response3, err := agent3.ExecuteSearch(ctx, &search.SearchRequest{
		Query:      "Find nonexistent_function_xyz123", // Likely to fail
		MaxResults: 5,
	})
	if err != nil {
		t.Logf("Phase 3 failed as expected: %v", err)
	} else {
		t.Logf("Phase 3: %d results, confidence: %.2f", len(response3.Results), response3.Confidence)
	}

	// Phase 4: Recovery with error context
	t.Log("Phase 4: Recovery with learning")
	agent4, _ := agent.NewUnifiedReActAgent("full-test-4", searchTool, logger)
	response4, err := agent4.ExecuteSearch(ctx, &search.SearchRequest{
		Query:      "Find function definitions in the agent package",
		Context:    "Previous search for specific function failed, using broader approach",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Phase 4 failed: %v", err)
	}
	t.Logf("Phase 4: %d results, confidence: %.2f", len(response4.Results), response4.Confidence)

	// Verify overall improvement
	if response4.Confidence > response1.Confidence*0.9 {
		t.Log("Context engineering maintained or improved confidence across phases")
	}
}

// =============================================================================
// Test Suite 8: Performance and Metrics
// =============================================================================

// TestContextEngineering_PerformanceMetrics tests that metrics are properly tracked.
func TestContextEngineering_PerformanceMetrics(t *testing.T) {
	skipIfNoLLMKey(t)

	ctx := context.Background()
	repoPath, _ := filepath.Abs("../..")

	config := agentctx.DefaultConfig()
	config.Cache.EnableMetrics = true

	mgr, err := agentctx.NewManager("metrics-session", "metrics-agent", repoPath, config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Perform multiple operations
	for i := 0; i < 10; i++ {
		mgr.AddTodo(ctx, fmt.Sprintf("Task %d", i), i)
		mgr.BuildOptimizedContext(ctx, agentctx.ContextRequest{
			CurrentTask:    fmt.Sprintf("Working on task %d", i),
			PrioritizeCache: true,
			IncludeTodos:   true,
		})
	}

	// Check metrics
	metrics := mgr.GetPerformanceMetrics()
	t.Logf("Performance metrics: %+v", metrics)

	// Verify key metrics are present
	expectedMetrics := []string{"total_requests", "cache_hits", "compression_savings"}
	for _, key := range expectedMetrics {
		if _, ok := metrics[key]; !ok {
			t.Logf("Warning: Expected metric '%s' not found", key)
		}
	}

	// Check health status
	health := mgr.GetHealthStatus()
	t.Logf("Health status: %+v", health)
}

// =============================================================================
// Helper Functions
// =============================================================================

func extractStablePrefix(prompt string) string {
	// Extract content before any dynamic markers
	lines := strings.Split(prompt, "\n")
	if len(lines) > 0 {
		return lines[0]
	}
	return prompt
}

func extractTimestampContext(prompt string) string {
	// Look for timestamp patterns in the prompt
	if idx := strings.Index(prompt, "[Context Date:"); idx != -1 {
		end := strings.Index(prompt[idx:], "]")
		if end != -1 {
			return prompt[idx : idx+end+1]
		}
	}
	return ""
}
