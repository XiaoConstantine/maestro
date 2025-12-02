// Package review provides the code review agent and orchestration logic.
package review

import (
	"context"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/maestro/internal/types"
)

// Agent defines the interface for code review operations.
type Agent interface {
	ReviewPR(ctx context.Context, prNumber int, tasks []types.PRReviewTask, console types.ConsoleInterface) ([]types.PRReviewComment, error)
	ReviewPRWithChanges(ctx context.Context, prNumber int, tasks []types.PRReviewTask, console types.ConsoleInterface, preloadedChanges *types.PRChanges) ([]types.PRReviewComment, error)
	Stop(ctx context.Context)
	Metrics(ctx context.Context) types.MetricsCollector
	Orchestrator(ctx context.Context) *agents.FlexibleOrchestrator
	ClonedRepoPath() string
	WaitForClone(ctx context.Context, timeout time.Duration) string
	GetIndexingStatus() *types.IndexingStatus
	Close() error
}

// Stopper manages graceful shutdown of background operations.
type Stopper struct {
	stop     chan struct{}
	stopped  chan struct{}
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// NewStopper creates a new Stopper instance.
func NewStopper() *Stopper {
	return &Stopper{
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

// Stop signals all goroutines to stop.
func (s *Stopper) Stop() {
	s.stopOnce.Do(func() {
		close(s.stop)
		if s.cancel != nil {
			s.cancel()
		}
	})
}

// Done returns a channel that's closed when all goroutines have stopped.
func (s *Stopper) Done() <-chan struct{} {
	return s.stopped
}

// Wait waits for all goroutines to stop.
func (s *Stopper) Wait() {
	s.wg.Wait()
	close(s.stopped)
}

// IsStopping returns true if stop has been called.
func (s *Stopper) IsStopping() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

// SetCancel sets the context cancel function.
func (s *Stopper) SetCancel(cancel context.CancelFunc) {
	s.cancel = cancel
}

// Add adds a goroutine to the wait group.
func (s *Stopper) Add(delta int) {
	s.wg.Add(delta)
}

// Done_ marks a goroutine as done.
func (s *Stopper) Done_() {
	s.wg.Done()
}

// StopChannel returns the stop channel.
func (s *Stopper) StopChannel() <-chan struct{} {
	return s.stop
}

// Config contains configuration options for the review agent.
type Config struct {
	// IndexWorkers controls concurrent workers for repository indexing
	IndexWorkers int

	// ReviewWorkers controls concurrent workers for parallel code review
	ReviewWorkers int

	// MaxFilesPerReview limits the number of files reviewed in a single PR
	MaxFilesPerReview int

	// EnableConsensusValidation enables multi-chain validation
	EnableConsensusValidation bool

	// EnableCommentRefinement enables iterative comment improvement
	EnableCommentRefinement bool

	// SkipPatterns defines file patterns to skip during review
	SkipPatterns []string
}

// DefaultConfig returns a default configuration.
func DefaultConfig() Config {
	return Config{
		IndexWorkers:              4,
		ReviewWorkers:             4,
		MaxFilesPerReview:         50,
		EnableConsensusValidation: true,
		EnableCommentRefinement:   true,
		SkipPatterns: []string{
			"*.md",
			"*.lock",
			"*.sum",
			"vendor/*",
			"node_modules/*",
		},
	}
}
