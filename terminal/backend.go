package terminal

import "context"

// MaestroBackend defines the interface for backend operations.
// This allows the TUI to interact with the agent without direct dependencies.
type MaestroBackend interface {
	// ReviewPR initiates a PR review and returns comments.
	ReviewPR(ctx context.Context, prNumber int, onProgress func(status string)) ([]ReviewComment, error)

	// AskQuestion sends a question to the agent and returns the response.
	AskQuestion(ctx context.Context, question string) (string, error)

	// GetRepoInfo returns information about the current repository.
	GetRepoInfo() RepoInfo

	// IsReady returns true if the backend is initialized and ready.
	IsReady() bool
}

// RepoInfo contains repository metadata.
type RepoInfo struct {
	Owner  string
	Repo   string
	Branch string
}

// MaestroConfig holds configuration for the TUI.
type MaestroConfig struct {
	Owner         string
	Repo          string
	GitHubToken   string
	Verbose       bool
	IndexWorkers  int
	ReviewWorkers int
}

// NoOpBackend is a placeholder backend for testing.
type NoOpBackend struct {
	repoInfo RepoInfo
}

// NewNoOpBackend creates a no-op backend for testing.
func NewNoOpBackend(owner, repo string) *NoOpBackend {
	return &NoOpBackend{
		repoInfo: RepoInfo{
			Owner:  owner,
			Repo:   repo,
			Branch: "main",
		},
	}
}

func (b *NoOpBackend) ReviewPR(ctx context.Context, prNumber int, onProgress func(status string)) ([]ReviewComment, error) {
	if onProgress != nil {
		onProgress("No backend configured")
	}
	return nil, nil
}

func (b *NoOpBackend) AskQuestion(ctx context.Context, question string) (string, error) {
	return "Backend not configured. Please initialize with a valid agent.", nil
}

func (b *NoOpBackend) GetRepoInfo() RepoInfo {
	return b.repoInfo
}

func (b *NoOpBackend) IsReady() bool {
	return false
}
