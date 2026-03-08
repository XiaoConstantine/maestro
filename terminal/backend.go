package terminal

import "context"

// MaestroBackend defines the interface for backend operations.
// This allows the TUI to interact with the agent without direct dependencies.
type MaestroBackend interface {
	// ReviewPR initiates a PR review and returns comments.
	ReviewPR(ctx context.Context, prNumber int, onProgress func(status string)) ([]ReviewComment, error)

	// AskQuestion sends a question to the agent and returns the response.
	AskQuestion(ctx context.Context, question string) (string, error)

	// AskWithRLM uses Recursive Language Model for large-context analysis.
	// It enables efficient processing of large content by keeping state in
	// REPL variables rather than the conversation context.
	AskWithRLM(ctx context.Context, question string, opts RLMOptions) (string, error)

	// Claude sends a prompt to Claude CLI subagent and returns the response.
	Claude(ctx context.Context, prompt string) (string, error)

	// Gemini sends a prompt to Gemini CLI subagent and returns the response.
	Gemini(ctx context.Context, prompt string, taskType string) (string, error)

	// GetRepoInfo returns information about the current repository.
	GetRepoInfo() RepoInfo

	// IsReady returns true if the backend is initialized and ready.
	IsReady() bool

	// Session management
	// CreateSession creates a new session with the given name.
	CreateSession(ctx context.Context, name string) error

	// SwitchSession switches to an existing session.
	SwitchSession(ctx context.Context, name string) error

	// ListSessions returns all available sessions.
	ListSessions(ctx context.Context) ([]SessionInfo, error)

	// GetCurrentSession returns the current session name.
	GetCurrentSession() string
}

// RLMOptions configures RLM (Recursive Language Model) processing.
type RLMOptions struct {
	// ContentPath is the path to content to analyze (file or directory).
	// If empty, uses the cloned repository.
	ContentPath string
	// MaxIterations limits the number of REPL iterations (0 = default).
	MaxIterations int
	// ModelTier selects the model tier: "fast", "smart", or "best".
	ModelTier string
	// OnProgress is called with status updates during processing.
	OnProgress func(status string)
}

// SessionInfo contains metadata about a session.
type SessionInfo struct {
	Name      string
	CreatedAt string
	IsCurrent bool
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

func (b *NoOpBackend) AskWithRLM(ctx context.Context, question string, opts RLMOptions) (string, error) {
	return "RLM backend not configured. Please initialize with a valid agent.", nil
}

func (b *NoOpBackend) Claude(ctx context.Context, prompt string) (string, error) {
	return "Claude CLI not available. Backend not configured.", nil
}

func (b *NoOpBackend) Gemini(ctx context.Context, prompt string, taskType string) (string, error) {
	return "Gemini CLI not available. Backend not configured.", nil
}

func (b *NoOpBackend) GetRepoInfo() RepoInfo {
	return b.repoInfo
}

func (b *NoOpBackend) IsReady() bool {
	return false
}

func (b *NoOpBackend) CreateSession(ctx context.Context, name string) error {
	return nil
}

func (b *NoOpBackend) SwitchSession(ctx context.Context, name string) error {
	return nil
}

func (b *NoOpBackend) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	return nil, nil
}

func (b *NoOpBackend) GetCurrentSession() string {
	return "default"
}
