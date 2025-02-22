package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/briandowns/spinner"
	"github.com/google/go-github/v68/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockLLM is a mock implementation of core.LLM.
type MockLLM struct {
	mock.Mock
}

func (m *MockLLM) Generate(ctx context.Context, prompt string, opts ...core.GenerateOption) (*core.LLMResponse, error) {
	args := m.Called(ctx, prompt, opts)
	// Handle both string and struct returns
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	if response, ok := args.Get(0).(*core.LLMResponse); ok {
		return response, args.Error(1)
	}
	// Fall back to string conversion for simple cases
	return &core.LLMResponse{Content: args.String(0)}, args.Error(1)
}

func (m *MockLLM) GenerateWithJSON(ctx context.Context, prompt string, opts ...core.GenerateOption) (map[string]interface{}, error) {
	args := m.Called(ctx, prompt, opts)
	result := args.Get(0)
	if result == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

// CreateEmbedding mocks the single embedding creation following the same pattern as Generate.
func (m *MockLLM) CreateEmbedding(ctx context.Context, input string, options ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	// Record the method call and get the mock results
	args := m.Called(ctx, input, options)

	// Handle nil case first - if first argument is nil, return error
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	// Check if we got a properly structured EmbeddingResult
	if result, ok := args.Get(0).(*core.EmbeddingResult); ok {
		return result, args.Error(1)
	}

	// Fallback case: create a simple embedding result with basic values
	// This is similar to how Generate falls back to string conversion
	return &core.EmbeddingResult{
		Vector:     []float32{0.1, 0.2, 0.3}, // Default vector
		TokenCount: len(input),
		Metadata: map[string]interface{}{
			"fallback": true,
		},
	}, args.Error(1)
}

// CreateEmbeddings mocks the batch embedding creation.
func (m *MockLLM) CreateEmbeddings(ctx context.Context, inputs []string, options ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	// Record the method call and get the mock results
	args := m.Called(ctx, inputs, options)

	// Handle nil case
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	// Check if we got a properly structured BatchEmbeddingResult
	if result, ok := args.Get(0).(*core.BatchEmbeddingResult); ok {
		return result, args.Error(1)
	}

	// Similar to the single embedding case, provide a fallback
	embeddings := make([]core.EmbeddingResult, len(inputs))
	for i := range inputs {
		embeddings[i] = core.EmbeddingResult{
			Vector:     []float32{0.1, 0.2, 0.3},
			TokenCount: len(inputs[i]),
			Metadata: map[string]interface{}{
				"fallback": true,
				"index":    i,
			},
		}
	}

	return &core.BatchEmbeddingResult{
		Embeddings: embeddings,
		Error:      nil,
		ErrorIndex: -1,
	}, args.Error(1)
}

// ModelID mocks the GetModelID method from the LLM interface.
func (m *MockLLM) ModelID() string {
	args := m.Called()

	ret0, _ := args.Get(0).(string)

	return ret0
}

// GetProviderName mocks the GetProviderName method from the LLM interface.
func (m *MockLLM) ProviderName() string {
	args := m.Called()

	ret0, _ := args.Get(0).(string)

	return ret0
}

func (m *MockLLM) Capabilities() []core.Capability {
	return []core.Capability{}
}

// MockRAGStore implements the RAGStore interface.
type MockRAGStore struct{}

func (m *MockRAGStore) StoreContent(ctx context.Context, content *Content) error {
	return nil
}

func (m *MockRAGStore) FindSimilar(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*Content, error) {
	return []*Content{
		{ID: "mock-content", Text: "mock text", Embedding: embedding, Metadata: map[string]string{"file_path": "test.go"}},
	}, nil
}

func (m *MockRAGStore) UpdateContent(ctx context.Context, content *Content) error {
	return nil
}

func (m *MockRAGStore) DeleteContent(ctx context.Context, id string) error {
	return nil
}

func (m *MockRAGStore) PopulateGuidelines(ctx context.Context, language string) error {
	return nil
}

func (m *MockRAGStore) StoreRule(ctx context.Context, rule ReviewRule) error {
	return nil
}

func (m *MockRAGStore) GetMetadata(ctx context.Context, key string) (string, error) {
	return "mock-sha", nil
}

func (m *MockRAGStore) SetMetadata(ctx context.Context, key, value string) error {
	return nil
}

func (m *MockRAGStore) Close() error {
	return nil
}

type MockGitHubTools struct {
	pullRequests *MockPullRequestsService
}

func NewMockGitHubTools() *MockGitHubTools {
	return &MockGitHubTools{
		pullRequests: &MockPullRequestsService{},
	}
}

func (m *MockGitHubTools) GetPullRequestChanges(ctx context.Context, prNumber int) (*PRChanges, error) {
	return &PRChanges{
		Files: []PRFileChange{
			{FilePath: "test.go", FileContent: "func main() {}", Patch: "+func main() {}"},
		},
	}, nil
}

func (m *MockGitHubTools) GetFileContent(ctx context.Context, path string) (string, error) {
	return "func main() {}", nil
}

func (m *MockGitHubTools) CreateReviewComments(ctx context.Context, prNumber int, comments []PRReviewComment) error {
	return nil
}

func (m *MockGitHubTools) GetLatestCommitSHA(ctx context.Context, branch string) (string, error) {
	return "abc123", nil
}

func (m *MockGitHubTools) MonitorPRComments(ctx context.Context, prNumber int, callback func(*github.PullRequestComment)) error {
	return nil
}

func (m *MockGitHubTools) PreviewReview(ctx context.Context, console ConsoleInterface, prNumber int, comments []PRReviewComment, metric MetricsCollector) (bool, error) {
	return true, nil
}

func (m *MockGitHubTools) GetAuthenticatedUser(ctx context.Context) string {
	return "testuser"
}

func (m *MockGitHubTools) GetRepositoryInfo(ctx context.Context) RepositoryInfo {
	return RepositoryInfo{Owner: "test", Name: "repo"}
}

func (m *MockGitHubTools) Client() *github.Client {
	client := github.NewClient(nil) // No transport needed
	return client
}

func (m *MockGitHubTools) ListPullRequestComments(ctx context.Context, owner, repo string, prNumber int, opts *github.PullRequestListCommentsOptions) ([]*github.PullRequestComment, *github.Response, error) {
	return []*github.PullRequestComment{}, &github.Response{Response: &http.Response{StatusCode: 200}}, nil
}

func (m *MockGitHubTools) GetRepositoryContents(ctx context.Context, owner, repo, path string, opts *github.RepositoryContentGetOptions) (*github.RepositoryContent, []*github.RepositoryContent, *github.Response, error) {
	if path == "" {
		return nil, []*github.RepositoryContent{
			{Path: github.Ptr("test.go"), Type: github.Ptr("file")},
			{Path: github.Ptr("README.md"), Type: github.Ptr("file")},
		}, &github.Response{Response: &http.Response{StatusCode: 200}}, nil
	}
	return nil, []*github.RepositoryContent{{Path: github.Ptr(path)}}, &github.Response{Response: &http.Response{StatusCode: 200}}, nil
}

func (m *MockGitHubTools) ListLanguages(ctx context.Context, owner, repo string) (map[string]int, *github.Response, error) {
	return map[string]int{"Go": 1000}, &github.Response{Response: &http.Response{StatusCode: 200}}, nil
}

func (m *MockGitHubTools) GetPullRequest(ctx context.Context, owner, repo string, prNumber int) (*github.PullRequest, *github.Response, error) {
	return &github.PullRequest{Number: github.Ptr(prNumber)}, &github.Response{Response: &http.Response{StatusCode: 200}}, nil
}

func (m *MockGitHubTools) GetRepository(ctx context.Context, owner, repo string) (*github.Repository, *github.Response, error) {
	return &github.Repository{Name: github.Ptr(repo)}, &github.Response{Response: &http.Response{StatusCode: 200}}, nil
}

func (m *MockGitHubTools) CompareCommits(ctx context.Context, owner, repo, base, head string, opts *github.ListOptions) (*github.CommitsComparison, *github.Response, error) {
	return &github.CommitsComparison{}, &github.Response{Response: &http.Response{StatusCode: 200}}, nil
}

// MockPullRequestsService.
type MockPullRequestsService struct{}

func (s *MockPullRequestsService) ListComments(ctx context.Context, owner, repo string, number int, opts *github.PullRequestListCommentsOptions) ([]*github.PullRequestComment, *github.Response, error) {
	resp := &github.Response{
		Response: &http.Response{
			Status:     "200 OK",
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader([]byte(`[]`))),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		},
	}
	return []*github.PullRequestComment{}, resp, nil
}

// Minimal implementations for other methods.
func (s *MockPullRequestsService) List(ctx context.Context, owner, repo string, opts *github.PullRequestListOptions) ([]*github.PullRequest, *github.Response, error) {
	return nil, nil, nil
}

func (s *MockPullRequestsService) Get(ctx context.Context, owner, repo string, number int) (*github.PullRequest, *github.Response, error) {
	return nil, nil, nil
}

func (s *MockPullRequestsService) CreateReview(ctx context.Context, owner, repo string, number int, review *github.PullRequestReviewRequest) (*github.PullRequestReview, *github.Response, error) {
	return nil, nil, nil
}

func (s *MockPullRequestsService) Create(ctx context.Context, owner, repo string, pull *github.NewPullRequest) (*github.PullRequest, *github.Response, error) {
	return nil, nil, nil
}

func (s *MockPullRequestsService) Edit(ctx context.Context, owner, repo string, number int, pull *github.PullRequest) (*github.PullRequest, *github.Response, error) {
	return nil, nil, nil
}

func (s *MockPullRequestsService) ListCommits(ctx context.Context, owner, repo string, number int, opts *github.ListOptions) ([]*github.RepositoryCommit, *github.Response, error) {
	return nil, nil, nil
}

func (s *MockPullRequestsService) ListFiles(ctx context.Context, owner, repo string, number int, opts *github.ListOptions) ([]*github.CommitFile, *github.Response, error) {
	return nil, nil, nil
}

type MockConsole struct {
	Buffer *bytes.Buffer
}

func NewMockConsole() *MockConsole {
	return &MockConsole{Buffer: &bytes.Buffer{}}
}

func (m *MockConsole) StartSpinner(message string) {
	fmt.Fprintf(m.Buffer, "Starting spinner: %s\n", message)
}

func (m *MockConsole) StopSpinner() {
	fmt.Fprintln(m.Buffer, "Stopping spinner")
}

func (m *MockConsole) WithSpinner(ctx context.Context, message string, fn func() error) error {
	fmt.Fprintf(m.Buffer, "%s...\n", message)
	err := fn() // Execute the function directly, no goroutine
	return err
}

func (m *MockConsole) ShowComments(comments []PRReviewComment, metric MetricsCollector) {
	fmt.Fprintf(m.Buffer, "Showing %d comments\n", len(comments))
}

func (m *MockConsole) ShowSummary(comments []PRReviewComment, metric MetricsCollector) {
	fmt.Fprintf(m.Buffer, "Summary: %d comments\n", len(comments))
}

func (m *MockConsole) StartReview(pr *github.PullRequest) {
	fmt.Fprintf(m.Buffer, "Starting review for PR #%d\n", *pr.Number)
}

func (m *MockConsole) ReviewingFile(file string, current, total int) {
	fmt.Fprintf(m.Buffer, "Reviewing file %s (%d/%d)\n", file, current, total)
}

func (m *MockConsole) ConfirmReviewPost(commentCount int) (bool, error) {
	fmt.Fprintf(m.Buffer, "Confirming post of %d comments\n", commentCount)
	return true, nil // Always confirm in mock
}

func (m *MockConsole) ReviewComplete() {
	fmt.Fprintln(m.Buffer, "Review complete")
}

func (m *MockConsole) UpdateSpinnerText(text string) {
	fmt.Fprintf(m.Buffer, "Updating spinner: %s\n", text)
}

func (m *MockConsole) ShowReviewMetrics(metrics MetricsCollector, comments []PRReviewComment) {
	fmt.Fprintf(m.Buffer, "Showing metrics for %d comments\n", len(comments))
}

func (m *MockConsole) Confirm(opts PromptOptions) (bool, error) {
	fmt.Fprintf(m.Buffer, "Confirming prompt: %s\n", opts.Message)
	return true, nil // Always confirm in mock
}

func (m *MockConsole) FileError(filepath string, err error) {
	fmt.Fprintf(m.Buffer, "Error in file %s: %v\n", filepath, err)
}

func (m *MockConsole) Printf(format string, a ...interface{}) {
	fmt.Fprintf(m.Buffer, format, a...)
}

func (m *MockConsole) Println(a ...interface{}) {
	fmt.Fprintln(m.Buffer, a...)
}

func (m *MockConsole) PrintHeader(text string) {
	fmt.Fprintf(m.Buffer, "=== %s ===\n", text)
}

func (m *MockConsole) NoIssuesFound(file string, chunkNumber, totalChunks int) {
	fmt.Fprintf(m.Buffer, "No issues found in %s (%d/%d)\n", file, chunkNumber, totalChunks)
}

func (m *MockConsole) SeverityIcon(severity string) string {
	return fmt.Sprintf("[%s]", severity)
}

func (m *MockConsole) Color() bool {
	return false // No color in mock
}

func (m *MockConsole) Spinner() *spinner.Spinner {
	return nil // No real spinner in mock
}

// TestPRReviewAgent_ReviewPR.
func TestPRReviewAgent_ReviewPR(t *testing.T) {
	// Set up logging
	logger := logging.NewLogger(logging.Config{
		Severity: logging.DEBUG,
		Outputs:  []logging.Output{logging.NewConsoleOutput(true)},
	})
	logging.SetLogger(logger)

	// Configure mock LLM
	ctx := context.Background()
	mockLLM := &MockLLM{}
	core.SetDefaultLLM(mockLLM)

	// Use mock GitHub tools and RAG store
	githubTools := NewMockGitHubTools()
	mockRAG := &MockRAGStore{}

	// Manually create PRReviewAgent with mock dependencies
	metrics := NewBusinessMetrics(logger)
	agent := &PRReviewAgent{
		orchestrator:  agents.NewFlexibleOrchestrator(agents.NewInMemoryStore(), agents.OrchestrationConfig{}),
		memory:        agents.NewInMemoryStore(),
		rag:           mockRAG,
		githubTools:   githubTools,
		stopper:       NewStopper(),
		metrics:       metrics,
		activeThreads: make(map[int64]*ThreadTracker),
	}
	assert.NotNil(t, agent, "Agent should not be nil")

	// Prepare test data
	tasks := []PRReviewTask{
		{FilePath: "test.go", FileContent: "func main() {}", Changes: "+func main() {}"},
	}

	// Set up console
	console := NewMockConsole()
	// Run the ReviewPR method
	comments, err := agent.ReviewPR(ctx, 1, tasks, console)
	assert.NoError(t, err, "ReviewPR should not return an error")
	assert.NotNil(t, comments, "Comments should not be nil")
	t.Logf("ReviewPR output: %v", console.Buffer.String())
}
