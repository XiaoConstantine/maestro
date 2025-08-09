package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// TestAgenticSearch tests the basic functionality of agentic search
func TestAgenticSearch(t *testing.T) {
	// Setup test environment
	logger := logging.NewLogger(logging.Config{
		Severity: logging.DEBUG,
		Outputs:  []logging.Output{logging.NewConsoleOutput(false)},
	})
	
	// Use current directory for testing
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	
	// Create agentic search system
	adapter := NewAgenticRAGAdapter(cwd, logger)
	defer adapter.Close()
	
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*2)
	defer cancel()
	
	t.Run("TestSearchCode", func(t *testing.T) {
		result, err := adapter.SearchCode(ctx, "search for functions", "")
		if err != nil {
			t.Fatalf("SearchCode failed: %v", err)
		}
		
		if result == nil {
			t.Fatal("SearchCode returned nil result")
		}
		
		t.Logf("Search completed: %s", result.Summary)
		t.Logf("Code samples found: %d", len(result.CodeSamples))
		t.Logf("Confidence: %.2f", result.ConfidenceScore)
		
		if len(result.CodeSamples) == 0 && len(result.KeyFindings) == 0 {
			t.Error("No results found - this might indicate an issue")
		}
	})
	
	t.Run("TestFindSimilarCompatibility", func(t *testing.T) {
		// Test compatibility with old RAG interface
		contents, err := adapter.FindSimilar(ctx, make([]float32, 768), 10, ContentTypeRepository)
		if err != nil {
			t.Fatalf("FindSimilar failed: %v", err)
		}
		
		t.Logf("FindSimilar returned %d contents", len(contents))
		
		for i, content := range contents {
			if i >= 3 { // Log first 3
				break
			}
			t.Logf("Content %d: ID=%s, Type=%s", i+1, content.ID, content.Metadata["content_type"])
		}
	})
	
	t.Run("TestSearchWithIntent", func(t *testing.T) {
		result, err := adapter.SearchWithIntent(ctx, "error handling", "code_analysis", "")
		if err != nil {
			t.Fatalf("SearchWithIntent failed: %v", err)
		}
		
		t.Logf("Intent search summary: %s", result.Summary)
		t.Logf("Agents involved: %v", result.AgentsInvolved)
		t.Logf("Search depth: %d", result.SearchDepth)
	})
}

// TestSimpleSearchTool tests the basic search functionality
func TestSimpleSearchTool(t *testing.T) {
	logger := logging.NewLogger(logging.Config{
		Severity: logging.DEBUG,
		Outputs:  []logging.Output{logging.NewConsoleOutput(false)},
	})
	
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	
	searchTool := NewSimpleSearchTool(logger, cwd)
	ctx := context.Background()
	
	t.Run("TestGrepSearch", func(t *testing.T) {
		results, err := searchTool.GrepSearch(ctx, "func.*Search", "**/*.go", 2)
		if err != nil {
			t.Fatalf("GrepSearch failed: %v", err)
		}
		
		t.Logf("Found %d grep results", len(results))
		
		for i, result := range results {
			if i >= 3 {
				break
			}
			t.Logf("Result %d: %s:%d - %s", i+1, result.FilePath, result.LineNumber, result.Line)
		}
	})
	
	t.Run("TestGlobSearch", func(t *testing.T) {
		files, err := searchTool.GlobSearch(ctx, "*.go")
		if err != nil {
			t.Fatalf("GlobSearch failed: %v", err)
		}
		
		t.Logf("Found %d .go files", len(files))
		
		if len(files) == 0 {
			t.Error("No .go files found - test environment issue?")
		}
	})
	
	t.Run("TestReadFile", func(t *testing.T) {
		// Try to read a go file
		files, _ := searchTool.GlobSearch(ctx, "*.go")
		if len(files) == 0 {
			t.Skip("No .go files to read")
		}
		
		lines, err := searchTool.ReadFile(ctx, files[0], 1, 10)
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}
		
		t.Logf("Read %d lines from %s", len(lines), files[0])
		
		if len(lines) > 0 {
			t.Logf("First line: %s", lines[0])
		}
	})
}

// TestAgentSpawning tests the agent management system
func TestAgentSpawning(t *testing.T) {
	logger := logging.NewLogger(logging.Config{
		Severity: logging.DEBUG,
		Outputs:  []logging.Output{logging.NewConsoleOutput(false)},
	})
	
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	
	searchTool := NewSimpleSearchTool(logger, cwd)
	pool := NewAgentPool(3, 300000, searchTool, logger)
	
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	defer pool.Shutdown(ctx)
	
	t.Run("TestSpawnSingleAgent", func(t *testing.T) {
		agent, err := pool.SpawnAgent(ctx, CodeSearchAgent, "test query")
		if err != nil {
			t.Fatalf("Failed to spawn agent: %v", err)
		}
		
		t.Logf("Spawned agent: %s (type: %s)", agent.ID, agent.Type)
		
		// Clean up
		if err := pool.ReleaseAgent(ctx, agent.ID); err != nil {
			t.Errorf("Failed to release agent: %v", err)
		}
	})
	
	t.Run("TestSpawnAgentGroup", func(t *testing.T) {
		strategy := &SpawnStrategy{
			MaxParallel:     2,
			TokensPerAgent:  100000,
			TypeDistribution: map[SearchAgentType]float64{
				CodeSearchAgent:      0.5,
				GuidelineSearchAgent: 0.5,
			},
			AdaptiveSpawning: false,
		}
		
		agents, err := pool.SpawnAgentGroup(ctx, strategy, "test group query")
		if err != nil {
			t.Fatalf("Failed to spawn agent group: %v", err)
		}
		
		t.Logf("Spawned %d agents", len(agents))
		
		// Check pool stats
		stats := pool.GetPoolStats()
		t.Logf("Pool stats: %+v", stats)
		
		// Clean up
		for _, agent := range agents {
			if err := pool.ReleaseAgent(ctx, agent.ID); err != nil {
				t.Errorf("Failed to release agent %s: %v", agent.ID, err)
			}
		}
	})
}

// TestContextWindow tests the context window management
func TestContextWindow(t *testing.T) {
	logger := logging.NewLogger(logging.Config{
		Severity: logging.DEBUG,
		Outputs:  []logging.Output{logging.NewConsoleOutput(false)},
	})
	
	window := NewContextWindow(1000, logger) // 1000 tokens max
	ctx := context.Background()
	
	t.Run("TestAddItems", func(t *testing.T) {
		item1 := &ContextItem{
			ID:       "test1",
			Content:  "This is a test item with some content",
			Priority: 0.8,
			ItemType: "test",
		}
		
		err := window.Add(ctx, item1)
		if err != nil {
			t.Fatalf("Failed to add item: %v", err)
		}
		
		current, max := window.GetTokenUsage()
		t.Logf("Token usage: %d/%d", current, max)
		
		if current == 0 {
			t.Error("Expected non-zero token usage")
		}
	})
	
	t.Run("TestContextOverflow", func(t *testing.T) {
		// Add many items to trigger eviction
		for i := 0; i < 20; i++ {
			item := &ContextItem{
				ID:       fmt.Sprintf("overflow-%d", i),
				Content:  strings.Repeat("word ", 50), // ~200 characters each
				Priority: float64(i) / 20.0,           // Increasing priority
				ItemType: "overflow",
			}
			
			window.Add(ctx, item)
		}
		
		current, max := window.GetTokenUsage()
		t.Logf("After overflow - Token usage: %d/%d", current, max)
		
		if current > max {
			t.Errorf("Context window exceeded limit: %d > %d", current, max)
		}
	})
	
	t.Run("TestCompression", func(t *testing.T) {
		compressed := window.Compress(ctx, 0.5) // 50% compression
		t.Logf("Compressed content length: %d characters", len(compressed))
		
		if len(compressed) == 0 {
			t.Error("Compression produced empty result")
		}
	})
}

// Benchmark test for agentic search performance
func BenchmarkAgenticSearch(b *testing.B) {
	logger := logging.NewLogger(logging.Config{
		Severity: logging.WARN, // Reduce noise in benchmark
		Outputs:  []logging.Output{logging.NewConsoleOutput(false)},
	})
	
	cwd, _ := os.Getwd()
	adapter := NewAgenticRAGAdapter(cwd, logger)
	defer adapter.Close()
	
	ctx := context.Background()
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		_, err := adapter.SearchCode(ctx, "search functions", "")
		if err != nil {
			b.Fatalf("SearchCode failed: %v", err)
		}
	}
}