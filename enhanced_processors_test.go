package main

import (
	"context"
	"os"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// MockMetricsCollector for testing
type MockMetricsCollector struct{}

func (m *MockMetricsCollector) TrackReviewStart(ctx context.Context, category string) {}
func (m *MockMetricsCollector) TrackNewThread(ctx context.Context, threadID int64, comment PRReviewComment) {}
func (m *MockMetricsCollector) TrackCommentResolution(ctx context.Context, threadID int64, resolution ResolutionOutcome) {}
func (m *MockMetricsCollector) TrackReviewComment(ctx context.Context, comment PRReviewComment, isValid bool) {}
func (m *MockMetricsCollector) TrackHistoricalComment(ctx context.Context, comment PRReviewComment) {}
func (m *MockMetricsCollector) TrackUserFeedback(ctx context.Context, threadID int64, helpful bool, reason string) {}
func (m *MockMetricsCollector) GetOutdatedRate(category string) float64 { return 0.0 }
func (m *MockMetricsCollector) GetPrecision(category string) float64 { return 0.0 }
func (m *MockMetricsCollector) GenerateReport(ctx context.Context) *BusinessReport { return &BusinessReport{} }
func (m *MockMetricsCollector) GetCategoryMetrics(category string) *CategoryStats { return &CategoryStats{} }

// Test setup
func setupTest() (*MockMetricsCollector, *logging.Logger) {
	// Set conservative feature flags for testing
	os.Setenv("MAESTRO_ADVANCED_REASONING", "true")
	os.Setenv("MAESTRO_CONSENSUS_VALIDATION", "true")
	os.Setenv("MAESTRO_COMMENT_REFINEMENT", "true")
	os.Setenv("MAESTRO_FALLBACK_LEGACY", "true")
	
	logger := logging.GetLogger()
	return &MockMetricsCollector{}, logger
}

// Test EnhancedCodeReviewProcessor
func TestEnhancedCodeReviewProcessor(t *testing.T) {
	metrics, logger := setupTest()
	processor := NewEnhancedCodeReviewProcessor(metrics, logger)

	ctx := context.Background()
	
	// Create test task
	task := agents.Task{
		ID:   "test-task-1",
		Type: "code_review",
		Metadata: map[string]interface{}{
			"file_content": `package main

import "fmt"

func main() {
	password := "hardcoded123" // Security issue
	fmt.Println("Hello", password)
}`,
			"changes": "Added main function with hardcoded password",
			"file_path": "main.go",
		},
	}

	taskContext := map[string]interface{}{
		"guidelines": "Follow security best practices",
		"repository_context": "Go web application",
	}

	// Test processing
	result, err := processor.Process(ctx, task, taskContext)
	if err != nil {
		t.Fatalf("Enhanced processing failed: %v", err)
	}

	// Verify result type
	enhancedResult, ok := result.(*EnhancedReviewResult)
	if !ok {
		t.Fatalf("Expected EnhancedReviewResult, got %T", result)
	}

	// Verify basic properties
	if enhancedResult.Confidence <= 0 || enhancedResult.Confidence > 1 {
		t.Errorf("Invalid confidence score: %f", enhancedResult.Confidence)
	}

	if enhancedResult.ProcessingTime <= 0 {
		t.Errorf("Invalid processing time: %f", enhancedResult.ProcessingTime)
	}

	t.Logf("Enhanced review found %d issues with %.2f confidence", 
		len(enhancedResult.Issues), enhancedResult.Confidence)
}

// Test ConsensusValidationProcessor
func TestConsensusValidationProcessor(t *testing.T) {
	metrics, logger := setupTest()
	processor := NewConsensusValidationProcessor(metrics, logger)

	ctx := context.Background()

	// Create test issue
	testIssue := ReviewIssue{
		FilePath:    "main.go",
		LineRange:   LineRange{Start: 6, End: 6},
		Category:    "security",
		Severity:    "high",
		Description: "Hardcoded password detected",
		Suggestion:  "Use environment variables for sensitive data",
		Confidence:  0.9,
	}

	// Create enhanced result to validate
	enhancedResult := &EnhancedReviewResult{
		Issues: []ReviewIssue{testIssue},
		Confidence: 0.9,
	}

	// Create validation task
	task := agents.Task{
		ID:   "test-validation-1",
		Type: "consensus_validation",
		Metadata: map[string]interface{}{
			"enhanced_result": enhancedResult,
		},
	}

	taskContext := map[string]interface{}{
		"code_context": "Go application main function",
		"guidelines": "Security-first development",
		"repository_context": "Web application with user authentication",
	}

	// Test validation
	result, err := processor.Process(ctx, task, taskContext)
	if err != nil {
		t.Fatalf("Consensus validation failed: %v", err)
	}

	// Verify result type
	validationResult, ok := result.(*ConsensusValidationResult)
	if !ok {
		t.Fatalf("Expected ValidationResult, got %T", result)
	}

	// Verify validation results
	if validationResult.ConsensusScore <= 0 || validationResult.ConsensusScore > 1 {
		t.Errorf("Invalid consensus score: %f", validationResult.ConsensusScore)
	}

	totalIssues := len(validationResult.ValidatedIssues) + len(validationResult.RejectedIssues)
	if totalIssues == 0 {
		t.Error("No issues processed during validation")
	}

	t.Logf("Validation: %d validated, %d rejected, %.2f consensus score", 
		len(validationResult.ValidatedIssues), 
		len(validationResult.RejectedIssues),
		validationResult.ConsensusScore)
}

// Test CommentRefinementProcessor
func TestCommentRefinementProcessor(t *testing.T) {
	metrics, logger := setupTest()
	processor := NewCommentRefinementProcessor(metrics, logger)

	ctx := context.Background()

	// Create test issues
	testIssues := []ReviewIssue{
		{
			FilePath:    "main.go",
			LineRange:   LineRange{Start: 6, End: 6},
			Category:    "security",
			Severity:    "high",
			Description: "Bad password",
			Suggestion:  "Fix it",
			Confidence:  0.8,
		},
	}

	// Create validation result to refine
	validationResult := &ConsensusValidationResult{
		ValidatedIssues: testIssues,
		ConsensusScore: 0.8,
	}

	// Create refinement task
	task := agents.Task{
		ID:   "test-refinement-1",
		Type: "comment_refinement",
		Metadata: map[string]interface{}{
			"validation_result": validationResult,
		},
	}

	taskContext := map[string]interface{}{
		"code_context": "Go application with hardcoded credentials",
	}

	// Test refinement
	result, err := processor.Process(ctx, task, taskContext)
	if err != nil {
		t.Fatalf("Comment refinement failed: %v", err)
	}

	// Verify result type
	refinementResult, ok := result.(*CommentRefinementResult)
	if !ok {
		t.Fatalf("Expected CommentRefinementResult, got %T", result)
	}

	// Verify refinement results
	if len(refinementResult.RefinedComments) == 0 {
		t.Error("No comments were refined")
	}

	if refinementResult.AverageQuality <= 0 || refinementResult.AverageQuality > 1 {
		t.Errorf("Invalid average quality: %f", refinementResult.AverageQuality)
	}

	// Check comment quality improvement
	for _, refined := range refinementResult.RefinedComments {
		if refined.QualityScore <= 0 || refined.QualityScore > 1 {
			t.Errorf("Invalid quality score for comment: %f", refined.QualityScore)
		}
		
		// Refined comment should be more substantial than original
		if len(refined.RefinedComment) <= len(refined.OriginalComment) {
			t.Logf("Warning: Refined comment not substantially longer than original")
		}
	}

	t.Logf("Refined %d comments with %.2f average quality", 
		len(refinementResult.RefinedComments), refinementResult.AverageQuality)
}

// Test Enhanced Features Configuration
func TestEnhancedFeatures(t *testing.T) {
	// Test default configuration
	features := DefaultEnhancedFeatures()
	if !features.AdvancedReasoning {
		t.Error("Advanced reasoning should be enabled by default")
	}

	// Test profile loading
	conservativeFeatures := GetFeatureProfile("conservative")
	if conservativeFeatures.AdvancedReasoning {
		t.Error("Conservative profile should disable advanced reasoning")
	}

	experimentalFeatures := GetFeatureProfile("experimental")
	if !experimentalFeatures.AdvancedReasoning {
		t.Error("Experimental profile should enable advanced reasoning")
	}

	// Test validation
	warnings := experimentalFeatures.ValidateConfiguration()
	if len(warnings) == 0 {
		t.Log("No configuration warnings for experimental profile")
	} else {
		t.Logf("Configuration warnings: %v", warnings)
	}

	// Test phase level detection
	phaseLevel := experimentalFeatures.GetPhaseLevel()
	if phaseLevel < 1 {
		t.Error("Experimental profile should have phase level >= 1")
	}

	t.Logf("Experimental features: %s (Phase %d)", experimentalFeatures.String(), phaseLevel)
}

// Test Enhanced Integration
func TestEnhancedProcessorRegistry(t *testing.T) {
	metrics, logger := setupTest()
	registry := NewEnhancedProcessorRegistry(metrics, logger)

	ctx := context.Background()

	// Create comprehensive test task
	task := agents.Task{
		ID:   "test-integration-1",
		Type: "code_review",
		Metadata: map[string]interface{}{
			"file_content": `package main

import (
	"database/sql"
	"fmt"
)

func getUserData(userID string) {
	// SQL injection vulnerability
	query := "SELECT * FROM users WHERE id = '" + userID + "'"
	rows, err := db.Query(query)
	if err != nil {
		return // No error handling
	}
	defer rows.Close()
}`,
			"changes": "Added user data retrieval function",
			"file_path": "user.go",
		},
	}

	taskContext := map[string]interface{}{
		"guidelines": "Follow secure coding practices and proper error handling",
		"repository_context": "User management service with database access",
		"code_context": "Database query function in user service",
	}

	// Test end-to-end processing
	result, err := registry.ProcessCodeReview(ctx, task, taskContext)
	if err != nil {
		t.Fatalf("Enhanced processing failed: %v", err)
	}

	// Verify result format
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map result, got %T", result)
	}

	// Check for required fields
	if _, exists := resultMap["comments"]; !exists {
		t.Error("Result missing 'comments' field")
	}

	if _, exists := resultMap["processing_type"]; !exists {
		t.Error("Result missing 'processing_type' field")
	}

	processingType := resultMap["processing_type"].(string)
	t.Logf("Processing completed with type: %s", processingType)

	// Verify comments
	if comments, ok := resultMap["comments"].([]PRReviewComment); ok {
		if len(comments) == 0 {
			t.Error("No comments generated")
		} else {
			t.Logf("Generated %d review comments", len(comments))
			
			// Check for security issue detection
			hasSecurityIssue := false
			for _, comment := range comments {
				if comment.Category == "security" {
					hasSecurityIssue = true
					t.Logf("Security issue detected: %s", comment.Content)
				}
			}
			
			if !hasSecurityIssue {
				t.Log("Warning: Expected security issue not detected in test code")
			}
		}
	} else {
		t.Error("Comments field has wrong type")
	}
}

// Test Feature Flags
func TestFeatureFlags(t *testing.T) {
	// Test individual feature flag functions
	originalEnv := os.Getenv("MAESTRO_ADVANCED_REASONING")
	defer os.Setenv("MAESTRO_ADVANCED_REASONING", originalEnv)

	// Test enabled
	os.Setenv("MAESTRO_ADVANCED_REASONING", "true")
	if !isEnhancedProcessingEnabled() {
		t.Error("Advanced reasoning should be enabled")
	}

	// Test disabled
	os.Setenv("MAESTRO_ADVANCED_REASONING", "false")
	globalFeatures = nil // Reset cache
	if isEnhancedProcessingEnabled() {
		t.Error("Advanced reasoning should be disabled")
	}

	// Test feature profile environment variable
	originalProfile := os.Getenv("MAESTRO_FEATURE_PROFILE")
	defer os.Setenv("MAESTRO_FEATURE_PROFILE", originalProfile)

	os.Setenv("MAESTRO_FEATURE_PROFILE", "phase1")
	globalFeatures = nil // Reset cache
	InitializeEnhancedFeatures()
	
	features := GetGlobalFeatures()
	if !features.AdvancedReasoning {
		t.Error("Phase1 profile should enable advanced reasoning")
	}
	if features.ReactiveProcessing {
		t.Error("Phase1 profile should not enable reactive processing")
	}
}

// Test comment quality evaluation
func TestCommentQualityEvaluation(t *testing.T) {
	tests := []struct {
		comment  string
		minScore float64
		maxScore float64
	}{
		{
			comment:  "Bad code",
			minScore: 0.0,
			maxScore: 0.3,
		},
		{
			comment:  "Consider using parameterized queries to prevent SQL injection vulnerabilities. This approach is more secure because it separates SQL code from data.",
			minScore: 0.4,
			maxScore: 0.8,
		},
		{
			comment: `The current query construction is vulnerable to SQL injection. Consider using parameterized queries:

` + "```go\nquery := \"SELECT * FROM users WHERE id = ?\"\nrows, err := db.Query(query, userID)\n```" + `

This prevents malicious input from being interpreted as SQL commands.`,
			minScore: 0.7,
			maxScore: 1.0,
		},
	}

	for i, test := range tests {
		score := evaluateCommentQuality(test.comment)
		if score < test.minScore || score > test.maxScore {
			t.Errorf("Test %d: quality score %.2f not in range [%.2f, %.2f] for comment: %s", 
				i, score, test.minScore, test.maxScore, test.comment)
		} else {
			t.Logf("Test %d: quality score %.2f (expected range [%.2f, %.2f])", 
				i, score, test.minScore, test.maxScore)
		}
	}
}

// Benchmark tests
func BenchmarkEnhancedProcessing(b *testing.B) {
	metrics, logger := setupTest()
	processor := NewEnhancedCodeReviewProcessor(metrics, logger)
	
	ctx := context.Background()
	task := agents.Task{
		ID:   "benchmark-task",
		Type: "code_review",
		Metadata: map[string]interface{}{
			"file_content": "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}",
			"changes":      "Added hello world function",
			"file_path":    "main.go",
		},
	}
	
	taskContext := map[string]interface{}{
		"guidelines": "Follow Go best practices",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := processor.Process(ctx, task, taskContext)
		if err != nil {
			b.Fatalf("Processing failed: %v", err)
		}
	}
}

// Test error handling and fallbacks
func TestErrorHandlingAndFallbacks(t *testing.T) {
	metrics, logger := setupTest()
	
	// Test with invalid task data
	processor := NewEnhancedCodeReviewProcessor(metrics, logger)
	ctx := context.Background()
	
	invalidTask := agents.Task{
		ID:   "invalid-task",
		Type: "code_review",
		Metadata: map[string]interface{}{
			// Missing required fields
		},
	}
	
	result, err := processor.Process(ctx, invalidTask, nil)
	
	// Should either handle gracefully or fall back to legacy
	if err != nil {
		t.Logf("Expected error for invalid task: %v", err)
	} else {
		t.Logf("Fallback processing succeeded: %T", result)
	}
}

// Test metrics collection
func TestMetricsCollection(t *testing.T) {
	metrics := &MockMetricsCollector{}
	logger := logging.GetLogger()
	
	// Initialize global metrics
	InitializeEnhancedFeatures()
	globalMetrics = &FeatureUsageMetrics{}
	
	// Simulate feature usage
	features := GetGlobalFeatures()
	globalMetrics.TrackFeatureUsage(features, "advanced_reasoning")
	globalMetrics.TrackFeatureUsage(features, "consensus_validation")
	globalMetrics.TrackFeatureUsage(features, "legacy_fallback")
	
	// Get usage report
	report := globalMetrics.GetUsageReport()
	
	if totalAttempts, ok := report["total_attempts"].(int64); !ok || totalAttempts != 3 {
		t.Errorf("Expected 3 total attempts, got %v", report["total_attempts"])
	}
	
	t.Logf("Usage report: %+v", report)
}