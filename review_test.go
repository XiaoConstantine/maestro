package main

import (
	"context"
	"testing"

	"github.com/google/go-github/v68/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockGitHubTools struct {
	mock.Mock
	authenticatedUser string
}

func (m *MockGitHubTools) GetPullRequestChanges(ctx context.Context, prNumber int) (*PRChanges, error) {
	args := m.Called(ctx, prNumber)
	return args.Get(0).(*PRChanges), args.Error(1)
}

func (m *MockGitHubTools) GetFileContent(ctx context.Context, path string) (string, error) {
	args := m.Called(ctx, path)
	return args.String(0), args.Error(1)
}

func (m *MockGitHubTools) CreateReviewComments(ctx context.Context, prNumber int, comments []PRReviewComment) error {
	args := m.Called(ctx, prNumber, comments)
	return args.Error(0)
}

func (m *MockGitHubTools) GetLatestCommitSHA(ctx context.Context, branch string) (string, error) {
	args := m.Called(ctx, branch)
	return args.String(0), args.Error(1)
}

func (m *MockGitHubTools) MonitorPRComments(ctx context.Context, prNumber int, callback func(comment *github.PullRequestComment)) error {
	args := m.Called(ctx, prNumber, callback)
	return args.Error(0)
}

func (m *MockGitHubTools) PreviewReview(ctx context.Context, console ConsoleInterface, prNumber int, comments []PRReviewComment, metric MetricsCollector) (bool, error) {
	args := m.Called(ctx, console, prNumber, comments, metric)
	return args.Bool(0), args.Error(1)
}

func (m *MockGitHubTools) GetAuthenticatedUser(ctx context.Context) string {
	return m.authenticatedUser
}

func (m *MockGitHubTools) GetRepositoryInfo(ctx context.Context) RepositoryInfo {
	args := m.Called(ctx)
	return args.Get(0).(RepositoryInfo)
}

func (m *MockGitHubTools) Client() *github.Client {
	args := m.Called()
	return args.Get(0).(*github.Client)
}

// MockMetricsCollector provides a mock implementation for testing
type MockMetricsCollector struct {
	mock.Mock
}

func (m *MockMetricsCollector) TrackReviewStart(ctx context.Context, category string) {
	m.Called(ctx, category)
}

func (m *MockMetricsCollector) TrackNewThread(ctx context.Context, threadID int64, comment PRReviewComment) {
	m.Called(ctx, threadID, comment)
}

func (m *MockMetricsCollector) TrackCommentResolution(ctx context.Context, threadID int64, resolution ResolutionOutcome) {
	m.Called(ctx, threadID, resolution)
}

func (m *MockMetricsCollector) TrackReviewComment(ctx context.Context, comment PRReviewComment, isValid bool) {
	m.Called(ctx, comment, isValid)
}

func (m *MockMetricsCollector) TrackHistoricalComment(ctx context.Context, comment PRReviewComment) {
	m.Called(ctx, comment)
}

func (m *MockMetricsCollector) TrackUserFeedback(ctx context.Context, threadID int64, helpful bool, reason string) {
	m.Called(ctx, threadID, helpful, reason)
}

func (m *MockMetricsCollector) GetOutdatedRate(category string) float64 {
	args := m.Called(category)
	return args.Get(0).(float64)
}

func (m *MockMetricsCollector) GetPrecision(category string) float64 {
	args := m.Called(category)
	return args.Get(0).(float64)
}

func (m *MockMetricsCollector) GenerateReport(ctx context.Context) *BusinessReport {
	args := m.Called(ctx)
	return args.Get(0).(*BusinessReport)
}

func (m *MockMetricsCollector) GetCategoryMetrics(category string) *CategoryStats {
	args := m.Called(category)
	return args.Get(0).(*CategoryStats)
}

func (m *MockMetricsCollector) GetOverallOutdatedRate() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *MockMetricsCollector) GetWeeklyActiveUsers() int {
	args := m.Called()
	return args.Int(0)
}

func (m *MockMetricsCollector) GetReviewResponseRate() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *MockMetricsCollector) StartOptimizationCycle(ctx context.Context) {
	m.Called(ctx)
}

func (m *MockMetricsCollector) StartReviewSession(ctx context.Context, prNumber int) {
	m.Called(ctx, prNumber)
}

func (m *MockMetricsCollector) StartThreadTracking(ctx context.Context, comment PRReviewComment) {
	m.Called(ctx, comment)
}

func (m *MockMetricsCollector) TrackValidationResult(ctx context.Context, category string, validated bool) {
	m.Called(ctx, category, validated)
}

func (m *MockMetricsCollector) TrackDetectionResults(ctx context.Context, issueCount int) {
	m.Called(ctx, issueCount)
}

// TestExtractReviewMetadata tests the metadata extraction functionality
func TestExtractReviewMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]interface{}
		want     *ReviewMetadata
		wantErr  bool
	}{
		{
			name: "valid metadata",
			metadata: map[string]interface{}{
				"category":     "code-style",
				"file_path":    "test.go",
				"changes":      "test changes",
				"file_content": "package main\n\nfunc main() {}\n",
				"chunk_start":  1,
				"chunk_end":    10,
				"chunk_number": 1,
				"total_chunks": 2,
				"line_range": map[string]interface{}{
					"start": 1,
					"end":   10,
				},
			},
			want: &ReviewMetadata{
				Category:    "code-style",
				FilePath:    "test.go",
				Changes:     "test changes",
				FileContent: "package main\n\nfunc main() {}\n",
				LineRange: LineRange{
					Start: 1,
					End:   10,
					File:  "test.go",
				},
				ChunkNumber: 1,
				TotalChunks: 2,
			},
			wantErr: false,
		},
		{
			name: "missing required fields",
			metadata: map[string]interface{}{
				"category": "code-style",
				// missing file_path
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractReviewMetadata(tt.metadata)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestIsValidComment tests the comment validation logic
func TestIsValidComment(t *testing.T) {
	tests := []struct {
		name    string
		comment PRReviewComment
		want    bool
	}{
		{
			name: "valid comment",
			comment: PRReviewComment{
				LineNumber: 10,
				Content:    "This is a detailed explanation of the issue",
				Suggestion: "Here's how to fix it properly",
			},
			want: true,
		},
		{
			name: "invalid line number",
			comment: PRReviewComment{
				LineNumber: 0,
				Content:    "Valid content",
				Suggestion: "Valid suggestion",
			},
			want: false,
		},
		{
			name: "content too short",
			comment: PRReviewComment{
				LineNumber: 10,
				Content:    "Too short",
				Suggestion: "Valid suggestion",
			},
			want: false,
		},
		{
			name: "missing suggestion",
			comment: PRReviewComment{
				LineNumber: 10,
				Content:    "This is a detailed explanation of the issue",
				Suggestion: "",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidComment(tt.comment)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestFindCommentCategory tests the category lookup functionality
func TestFindCommentCategory(t *testing.T) {
	metrics := &BusinessMetrics{
		activeThreads: map[int64]*ThreadMetrics{
			1: {
				Category: "code-style",
				LastComment: &PRReviewComment{
					Category: "code-style",
				},
			},
		},
	}

	tests := []struct {
		name     string
		threadID int64
		want     string
		found    bool
	}{
		{
			name:     "existing thread",
			threadID: 1,
			want:     "code-style",
			found:    true,
		},
		{
			name:     "non-existent thread",
			threadID: 999,
			want:     "",
			found:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			category, found := metrics.findCommentCategory(tt.threadID)
			assert.Equal(t, tt.found, found)
			if tt.found {
				assert.Equal(t, tt.want, category)
			}
		})
	}
}

// type MockLLM struct {
// 	mock.Mock
// }
//
// func (m *MockLLM) Generate(ctx context.Context, prompt string, options ...core.GenerateOption) (*core.GenerateResponse, error) {
// 	args := m.Called(ctx, prompt, options)
// 	return args.Get(0).(*core.GenerateResponse), args.Error(1)
// }
//
// func (m *MockLLM) CreateEmbedding(ctx context.Context, text string) (*core.EmbeddingResponse, error) {
// 	args := m.Called(ctx, text)
// 	return args.Get(0).(*core.EmbeddingResponse), args.Error(1)
// }
//
// func TestGenerateResponse(t *testing.T) {
// 	// Create a test context
// 	ctx := context.Background()
//
// 	// Create a proper console with an output buffer
// 	var outputBuffer bytes.Buffer
// 	logger := logging.GetLogger()
// 	console := NewConsole(&outputBuffer, logger, nil)
//
// 	// Create the thread tracker with complete test data
// 	thread := &ThreadTracker{
// 		LastComment: &PRReviewComment{
// 			FilePath:   "test.go",
// 			LineNumber: 10,
// 			Content:    "Test comment",
// 			Category:   "code-style",
// 			ThreadID:   github.Ptr(int64(1)),
// 		},
// 		FileContent:    "package main\n\nfunc main() {}\n",
// 		Status:         ThreadOpen,
// 		LastUpdate:     time.Now(),
// 		OriginalAuthor: "testuser",
// 		ConversationHistory: []PRReviewComment{
// 			{
// 				FilePath:   "test.go",
// 				LineNumber: 10,
// 				Content:    "Test comment",
// 				Category:   "code-style",
// 				ThreadID:   github.Ptr(int64(1)),
// 				Author:     "testuser",
// 				Timestamp:  time.Now(),
// 			},
// 		},
// 	}
//
// 	// Create and configure mock metrics collector
// 	mockMetrics := new(MockMetricsCollector)
// 	mockMetrics.On("TrackCommentResolution",
// 		mock.Anything,
// 		mock.Anything,
// 		mock.Anything,
// 	).Return(nil)
//
// 	// Create and configure mock GitHub tools
// 	mockGH := new(MockGitHubTools)
// 	mockGH.authenticatedUser = "testuser"
// 	mockGH.On("GetAuthenticatedUser", mock.Anything).Return("testuser")
// 	mockGH.On("GetFileContent", mock.Anything, "test.go").Return(thread.FileContent, nil)
//
// 	// Create orchestrator with proper configuration
// 	orchestratorConfig := agents.OrchestrationConfig{
// 		MaxConcurrent: 1,
// 		TaskParser:    &agents.XMLTaskParser{},
// 		PlanCreator:   &agents.DependencyPlanCreator{},
// 		CustomProcessors: map[string]agents.TaskProcessor{
// 			"comment_response": &CommentResponseProcessor{metrics: mockMetrics},
// 		},
// 	}
//
// 	// Initialize the agent with all required components
// 	agent := &PRReviewAgent{
// 		metrics:      mockMetrics,
// 		githubTools:  mockGH,
// 		orchestrator: agents.NewFlexibleOrchestrator(agents.NewInMemoryStore(), orchestratorConfig),
// 		activeThreads: map[int64]*ThreadTracker{
// 			1: thread,
// 		},
// 	}
//
// 	// Run the test
// 	response, err := agent.generateResponse(ctx, thread, console)
//
// 	// Verify the results
// 	assert.NoError(t, err, "generateResponse should not return an error")
// 	assert.NotNil(t, response, "response should not be nil")
//
// 	if response != nil {
// 		assert.Equal(t, thread.LastComment.FilePath, response.FilePath)
// 		assert.Equal(t, thread.LastComment.LineNumber, response.LineNumber)
// 		assert.Equal(t, thread.LastComment.ThreadID, response.ThreadID)
// 		assert.Equal(t, "response", string(response.MessageType))
// 	}
//
// 	// Verify that all expected mock method calls were made
// 	mockMetrics.AssertExpectations(t)
// 	mockGH.AssertExpectations(t)
//
// 	// Verify console output if needed
// 	output := outputBuffer.String()
// 	assert.Contains(t, output, "Generating response...")
// }

// TestProcessExistingComments tests the processing of existing PR comments
func TestProcessExistingComments(t *testing.T) {
	ctx := context.Background()
	mockGH := new(MockGitHubTools)
	mockMetrics := new(MockMetricsCollector)
	console := NewConsole(nil, nil, nil)

	agent := &PRReviewAgent{
		githubTools:   mockGH,
		metrics:       mockMetrics,
		activeThreads: make(map[int64]*ThreadTracker),
	}

	// Set up mock responses
	mockGH.On("GetPullRequestChanges", mock.Anything, 1).Return(&PRChanges{
		Files: []PRFileChange{
			{
				FilePath:    "test.go",
				FileContent: "package main\n\nfunc main() {}\n",
			},
		},
	}, nil)

	mockGH.On("GetRepositoryInfo", mock.Anything).Return(RepositoryInfo{
		Owner: "test",
		Name:  "repo",
	})

	mockGH.authenticatedUser = "testuser"

	// Test comment processing
	err := agent.processExistingComments(ctx, 1, console)
	assert.NoError(t, err)

	mockGH.AssertExpectations(t)
	mockMetrics.AssertExpectations(t)
}

