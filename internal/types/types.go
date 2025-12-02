// Package types contains all shared types used across the maestro codebase.
// This package has NO internal dependencies and serves as the foundation
// for breaking circular dependencies between other packages.
package types

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/briandowns/spinner"
	"github.com/google/go-github/v68/github"
)

// =============================================================================
// Message & Thread Types
// =============================================================================

// MessageType represents the type of message in a review thread.
type MessageType string

const (
	QuestionMessage        MessageType = "question"
	ClarificationMessage   MessageType = "clarification"
	ResolutionMessage      MessageType = "resolution"
	SuggestionMessage      MessageType = "suggestion"
	AcknowledgementMessage MessageType = "acknowledgement"
)

// ThreadStatus represents the status of a review thread.
type ThreadStatus string

const (
	ThreadOpen       ThreadStatus = "open"
	ThreadResolved   ThreadStatus = "resolved"
	ThreadInProgress ThreadStatus = "in_progress"
	ThreadStale      ThreadStatus = "stale"
)

// ResolutionOutcome represents the outcome of a thread resolution.
type ResolutionOutcome string

const (
	ResolutionAccepted     ResolutionOutcome = "accepted"
	ResolutionRejected     ResolutionOutcome = "rejected"
	ResolutionNeedsWork    ResolutionOutcome = "needs_work"
	ResolutionInProgress   ResolutionOutcome = "in_progress"
	ResolutionInconclusive ResolutionOutcome = "inconclusive"
)

// ResolutionEffectiveness represents the effectiveness of a resolution.
type ResolutionEffectiveness string

const (
	HighEffectiveness    ResolutionEffectiveness = "high"
	MediumEffectiveness  ResolutionEffectiveness = "medium"
	LowEffectiveness     ResolutionEffectiveness = "low"
	UnknownEffectiveness ResolutionEffectiveness = "unknown"
)

// ResolutionAttempt represents an attempt to resolve a review issue.
type ResolutionAttempt struct {
	Proposal  string
	Outcome   ResolutionOutcome
	Timestamp time.Time
	Feedback  string
	Changes   []ReviewChunk
}

// ResponseResult represents the result of a comment response.
type ResponseResult struct {
	Response         string
	ResolutionStatus string
	ActionItems      []string
}

// ResponseMetadata contains metadata for comment response processing.
type ResponseMetadata struct {
	OriginalComment string
	ThreadContext   []PRReviewComment
	FileContent     string
	FilePath        string
	LineRange       LineRange
	ThreadID        *int64
	InReplyTo       *int64
	Category        string
	ReviewHistory   []ReviewChunk
}

// RefinedComment represents a refined review comment.
type RefinedComment struct {
	FilePath        string  `json:"file_path"`
	LineNumber      int     `json:"line_number"`
	OriginalComment string  `json:"original_comment"`
	RefinedComment  string  `json:"refined_comment"`
	QualityScore    float64 `json:"quality_score"`
	Improvement     string  `json:"improvement"`
	RefineAttempts  int     `json:"refine_attempts"`
	Category        string  `json:"category"`
	Severity        string  `json:"severity"`
	Suggestion      string  `json:"suggestion"`
}

// CommentRefinementResult represents the output of comment refinement.
type CommentRefinementResult struct {
	RefinedComments []RefinedComment `json:"refined_comments"`
	ProcessingTime  float64          `json:"processing_time_ms"`
	AverageQuality  float64          `json:"average_quality_score"`
	TotalAttempts   int              `json:"total_refine_attempts"`
}

// CommentQualityMetrics tracks comment quality aspects.
type CommentQualityMetrics struct {
	Clarity         float64 `json:"clarity"`
	Actionability   float64 `json:"actionability"`
	Professionalism float64 `json:"professionalism"`
	Specificity     float64 `json:"specificity"`
	Helpfulness     float64 `json:"helpfulness"`
	Overall         float64 `json:"overall"`
}

// =============================================================================
// Core Review Types
// =============================================================================

// LineRange represents a range of lines in a file.
type LineRange struct {
	Start int    // Starting line number (inclusive)
	End   int    // Ending line number (inclusive)
	File  string // File path this range refers to
}

// Contains checks if a line number is within the range.
func (lr LineRange) Contains(line int) bool {
	return line >= lr.Start && line <= lr.End
}

// IsValid checks if the range is valid.
func (lr LineRange) IsValid() bool {
	return lr.Start > 0 && lr.End >= lr.Start
}

// String returns a string representation of the range.
func (lr LineRange) String() string {
	return fmt.Sprintf("lines %d-%d in %s", lr.Start, lr.End, lr.File)
}

// PRReviewComment represents a review comment.
type PRReviewComment struct {
	FilePath    string
	LineNumber  int
	Content     string
	Severity    string
	Suggestion  string
	Category    string      // e.g., "security", "performance", "style"
	InReplyTo   *int64      // ID of the parent comment
	ThreadID    *int64      // Unique thread identifier
	Resolved    bool        // Track if the discussion is resolved
	Timestamp   time.Time   // When the comment was made
	Author      string
	MessageType MessageType
}

// PRReviewTask represents a single file review task.
type PRReviewTask struct {
	FilePath    string
	FileContent string
	Changes     string // Git diff content
	Chunks      []ReviewChunk
}

// ReviewChunk represents a chunk of code for review.
type ReviewChunk struct {
	Content         string
	StartLine       int
	EndLine         int
	LeadingContext  string // Previous lines for context
	TrailingContext string // Following lines for context
	Changes         string // Relevant diff section for this chunk
	FilePath        string // Path of the parent file
	TotalChunks     int    // Total number of chunks for this file
	Description     string
}

// PotentialIssue represents a detected but unvalidated code issue.
type PotentialIssue struct {
	FilePath   string
	LineNumber int
	RuleID     string            // Reference to the rule that detected this
	Confidence float64           // Initial confidence score
	Content    string            // Detected problematic code
	Context    map[string]string // Surrounding code context
	Suggestion string            // Initial suggested fix
	Category   string
	Metadata   map[string]interface{}
}

// ValidatedIssue represents an issue that has been validated.
type ValidatedIssue struct {
	// Core issue information
	FilePath  string
	LineRange LineRange
	Category  string
	Severity  string

	// Enriched context and suggestions
	Context    string  // Enhanced context from validation
	Suggestion string  // Final refined suggestion
	Confidence float64 // Combined confidence from validations

	// Validation details
	ValidationDetails struct {
		ContextValid  bool
		RuleCompliant bool
		IsActionable  bool
		ImpactScore   float64
	}
}

// ReviewIssue represents a code issue identified through reasoning.
type ReviewIssue struct {
	FilePath    string    `json:"file_path"`
	LineRange   LineRange `json:"line_range"`
	Category    string    `json:"category"`
	Severity    string    `json:"severity"`
	Description string    `json:"description"`
	Reasoning   string    `json:"reasoning"`
	Suggestion  string    `json:"suggestion"`
	Confidence  float64   `json:"confidence"`
	CodeExample string    `json:"code_example,omitempty"`
}

// EnhancedReviewResult contains the output of enhanced reasoning.
type EnhancedReviewResult struct {
	Issues         []ReviewIssue `json:"issues"`
	OverallQuality string        `json:"overall_quality"`
	ReasoningChain string        `json:"reasoning_chain"`
	Confidence     float64       `json:"confidence"`
	ProcessingTime float64       `json:"processing_time_ms"`
	FilePath       string        `json:"file_path"`
}

// ConsensusValidationResult contains the output of consensus validation.
type ConsensusValidationResult struct {
	ValidatedIssues   []ReviewIssue     `json:"validated_issues"`
	RejectedIssues    []ReviewIssue     `json:"rejected_issues"`
	ConsensusScore    float64           `json:"consensus_score"`
	ValidationReasons map[string]string `json:"validation_reasons"`
	ProcessingTime    float64           `json:"processing_time_ms"`
	ValidatorResults  []ValidatorResult `json:"validator_results"`
}

// ValidatorResult represents the result from a single validator.
type ValidatorResult struct {
	ValidatorType string  `json:"validator_type"`
	Decision      string  `json:"decision"` // "accept", "reject", "uncertain"
	Confidence    float64 `json:"confidence"`
	Reasoning     string  `json:"reasoning"`
}

// =============================================================================
// PR Types
// =============================================================================

// PRChanges contains changes made in a pull request.
type PRChanges struct {
	Files []PRFileChange
}

// PRFileChange represents changes to a single file.
type PRFileChange struct {
	FilePath    string
	FileContent string // The complete file content
	Patch       string // The diff/patch content
	Additions   int
	Deletions   int
	Hunks       []ChangeHunk
}

// ChangeHunk represents a hunk of changes in a diff.
type ChangeHunk struct {
	FilePath  string
	StartLine int
	EndLine   int
	Content   string
	Context   struct {
		Before string
		After  string
	}
	Position int // Position in the diff for GitHub API
}

// RepositoryInfo contains repository metadata.
type RepositoryInfo struct {
	Owner         string
	Name          string
	DefaultBranch string
	CloneURL      string
}

// =============================================================================
// RAG Types
// =============================================================================

// Content represents a content piece stored in the RAG store.
type Content struct {
	ID        string            // Unique identifier (e.g., "file_path:chunk_1")
	Text      string            // The actual text content
	Embedding []float32         // Vector embedding of the text
	Metadata  map[string]string // Additional info like file path, line numbers
}

// Content type constants.
const (
	ContentTypeRepository = "repository"
	ContentTypeGuideline  = "guideline"
)

// QualityMetrics provides analysis of retrieval quality.
type QualityMetrics struct {
	ExcellentCount int     `json:"excellent_count"` // < 0.2
	GoodCount      int     `json:"good_count"`      // 0.2-0.4
	FairCount      int     `json:"fair_count"`      // 0.4-0.6
	PoorCount      int     `json:"poor_count"`      // > 0.6
	AverageScore   float64 `json:"average_score"`
	BestScore      float64 `json:"best_score"`
	WorstScore     float64 `json:"worst_score"`
}

// DebugInfo tracks RAG retrieval metrics for debugging.
type DebugInfo struct {
	QueryEmbeddingDims int            `json:"query_embedding_dims"`
	ResultCount        int            `json:"result_count"`
	SimilarityScores   []float64      `json:"similarity_scores"`
	TopMatches         []string       `json:"top_matches"`
	RetrievalTime      time.Duration  `json:"retrieval_time"`
	QualityMetrics     QualityMetrics `json:"quality_metrics"`
}

// SimpleCodePattern represents a detected code pattern in a file.
type SimpleCodePattern struct {
	Name        string  // e.g., "error handling", "concurrency"
	Description string  // Human-readable description
	Confidence  float64 // How confident we are this pattern exists (0-1)
}

// GuidelineSearchResult represents a guideline with relevance scores.
type GuidelineSearchResult struct {
	Content            *Content
	DocumentScore      float64 // Document-level similarity (0-1)
	BestChunkScore     float64 // Best chunk similarity (0-1)
	AverageChunkScore  float64 // Average chunk similarity (0-1)
	FinalScore         float64 // Weighted score (0-1)
	Pattern            string  // Which pattern triggered this match
	ContextDescription string  // Why this guideline is relevant
}

// =============================================================================
// Guideline Types
// =============================================================================

// CodeExample represents good and bad code examples.
type CodeExample struct {
	Good        string
	Bad         string
	Explanation string
}

// GuidelineContent represents a coding guideline.
type GuidelineContent struct {
	ID       string            // Unique identifier for the guideline
	Text     string            // The actual guideline text
	Category string            // e.g., "Error Handling", "Pointers", etc.
	Examples []CodeExample     // Good and bad examples
	Language string            // Programming language this applies to
	Metadata map[string]string // Additional metadata
}

// ReviewRule represents a review rule.
type ReviewRule struct {
	ID          string       // Unique identifier (e.g., "ERR001")
	Dimension   string       // High-level category
	Category    string       // Specific category
	Name        string       // Human-readable name
	Description string       // Detailed description
	Severity    string       // Default severity level
	Examples    CodeExample  // Good and bad examples
	Metadata    RuleMetadata // Additional rule information
}

// RuleMetadata holds additional rule information.
type RuleMetadata struct {
	Category     string
	Impact       string
	AutoFixable  bool
	OutdatedRate float64 // Track effectiveness using BitsAI-CR's metric
}

// =============================================================================
// Agent Types
// =============================================================================

// AgentStatusType represents the status type of an agent (enum).
type AgentStatusType int

const (
	AgentStatusIdle AgentStatusType = iota
	AgentStatusRunning
	AgentStatusStopped
	AgentStatusError
)

// AgentStatus represents the runtime status of an agent.
type AgentStatus struct {
	State       string
	Progress    float64
	LastUpdate  time.Time
	Error       error
	ResultCount int
}

// SearchAgentType represents different types of search agents.
type SearchAgentType string

const (
	CodeSearchAgent      SearchAgentType = "code"
	GuidelineSearchAgent SearchAgentType = "guideline"
	SemanticSearchAgent  SearchAgentType = "semantic"
	ContextSearchAgent   SearchAgentType = "context"
)

// AgentConfig contains configuration options for the review agent.
type AgentConfig struct {
	// IndexWorkers controls concurrent workers for repository indexing
	IndexWorkers int

	// ReviewWorkers controls concurrent workers for parallel code review
	ReviewWorkers int
}

// SpawnStrategy defines how to spawn child agents.
type SpawnStrategy struct {
	MaxConcurrent int
	Priority      int
	Timeout       time.Duration
}

// IndexingStatus tracks the progress of background repository indexing.
type IndexingStatus struct {
	mu            sync.RWMutex
	isIndexing    bool
	progress      float64 // 0.0 to 1.0
	filesIndexed  int
	totalFiles    int
	lastError     error
	startTime     time.Time
	estimatedETA  time.Duration
	isComplete    bool
	hasBasicIndex bool // Enough for basic functionality
}

// NewIndexingStatus creates a new IndexingStatus.
func NewIndexingStatus() *IndexingStatus {
	return &IndexingStatus{
		startTime: time.Now(),
	}
}

// IsReady returns whether the indexing is ready for basic functionality.
func (s *IndexingStatus) IsReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasBasicIndex || s.isComplete
}

// GetProgress returns the current progress and completion status.
func (s *IndexingStatus) GetProgress() (float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.progress, s.isComplete
}

// GetStatus returns the indexing status, progress, and any error.
func (s *IndexingStatus) GetStatus() (bool, float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isIndexing, s.progress, s.lastError
}

// UpdateProgress updates the indexing progress.
func (s *IndexingStatus) UpdateProgress(indexed, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filesIndexed = indexed
	s.totalFiles = total
	if total > 0 {
		s.progress = float64(indexed) / float64(total)
	}

	// Consider "basic" indexing complete at 20% for immediate functionality
	if s.progress > 0.2 {
		s.hasBasicIndex = true
	}

	// Estimate completion time
	elapsed := time.Since(s.startTime)
	if indexed > 0 {
		rate := float64(indexed) / elapsed.Seconds()
		remaining := float64(total - indexed)
		s.estimatedETA = time.Duration(remaining/rate) * time.Second
	}
}

// SetComplete marks the indexing as complete.
func (s *IndexingStatus) SetComplete(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isComplete = true
	s.isIndexing = false
	s.lastError = err
	s.progress = 1.0
}

// SetIndexing sets the indexing state.
func (s *IndexingStatus) SetIndexing(indexing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isIndexing = indexing
}

// SetProgress sets the progress value directly (0.0 to 1.0).
func (s *IndexingStatus) SetProgress(progress float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress = progress
	// Consider "basic" indexing complete at 20% for immediate functionality
	if progress > 0.2 {
		s.hasBasicIndex = true
	}
}

// GetError returns the last error.
func (s *IndexingStatus) GetError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastError
}

// =============================================================================
// Config Types
// =============================================================================

// PromptOptions configures how the confirmation prompt behaves.
type PromptOptions struct {
	Message  string        // The question to ask
	Default  bool          // Default answer if user just hits enter
	HelpText string        // Optional help text shown below prompt
	Timeout  time.Duration // Optional timeout for response
	Color    bool          // Whether to use colors in prompt
}

// DefaultPromptOptions returns default prompt options.
func DefaultPromptOptions() PromptOptions {
	return PromptOptions{
		Message:  "Continue?",
		Default:  false,
		HelpText: "",
		Timeout:  30 * time.Second,
		Color:    true,
	}
}

// SpinnerConfig configures spinner behavior.
type SpinnerConfig struct {
	Color   string
	Speed   time.Duration
	CharSet []string
	Prefix  string
	Suffix  string
}

// =============================================================================
// Metrics Types
// =============================================================================

// BusinessReport represents a comprehensive snapshot of our review system's metrics.
type BusinessReport struct {
	GeneratedAt       time.Time
	CategoryMetrics   map[string]CategoryMetrics
	WeeklyActiveUsers int
	TotalReviews      int
}

// CategoryMetrics holds the core metrics for a specific review category.
type CategoryMetrics struct {
	Precision     float64   // Ratio of valid comments to total comments
	OutdatedRate  float64   // Ratio of modified lines to total flagged lines
	TotalComments int       // Total number of comments in this category
	LastUpdated   time.Time // When this category was last updated
}

// CategoryStats tracks statistics for each review category.
type CategoryStats struct {
	TotalComments  int
	ValidComments  int // Comments deemed technically correct
	OutdatedLines  int // Lines modified after being flagged
	TotalLines     int // Total lines flagged
	LastUpdated    time.Time
	TotalValidated int     // Total number of issues that went through validation
	ValidIssues    int     // Number of issues that passed validation
	ValidationRate float64 // Ratio of valid issues to total validated
}

// GetOutdatedRate returns the outdated rate for the category.
func (cs *CategoryStats) GetOutdatedRate() float64 {
	if cs.TotalLines == 0 {
		return 0
	}
	return float64(cs.OutdatedLines) / float64(cs.TotalLines)
}

// =============================================================================
// Chain Types
// =============================================================================

// ReviewChainOutput represents the output of the review chain.
type ReviewChainOutput struct {
	// Core review findings
	DetectedIssues []PotentialIssue `json:"detected_issues"`

	// Validation results from each step
	ContextValidation struct {
		Valid           bool    `json:"valid"`
		Confidence      float64 `json:"confidence"`
		EnhancedContext string  `json:"enhanced_context"`
	} `json:"context_validation"`

	RuleCompliance struct {
		Compliant         bool   `json:"compliant"`
		RefinedSuggestion string `json:"refined_suggestion"`
	} `json:"rule_compliance"`

	PracticalImpact struct {
		IsActionable    bool   `json:"is_actionable"`
		FinalSuggestion string `json:"final_suggestion"`
		Severity        string `json:"severity"`
	} `json:"practical_impact"`

	ReviewMetadata struct {
		FilePath  string    `json:"file_path"`
		LineRange LineRange `json:"line_range"`
		Category  string    `json:"category"`
		ThreadID  *int64    `json:"thread_id,omitempty"`
		InReplyTo *int64    `json:"in_reply_to,omitempty"`
		// Enhanced thread context with detailed conversation tracking
		ThreadContext struct {
			// Participant information
			OriginalAuthor string   `json:"original_author"`
			LastResponder  string   `json:"last_responder,omitempty"`
			Participants   []string `json:"participants"`

			// Timing and status
			CreatedAt        time.Time         `json:"created_at"`
			LastUpdate       time.Time         `json:"last_update"`
			Status           ThreadStatus      `json:"status"`
			ResolutionStatus ResolutionOutcome `json:"resolution_status"`

			// Conversation history and context
			ConversationFlow []PRReviewComment `json:"conversation_flow"`
			RelatedChanges   []ReviewChunk     `json:"related_changes"`

			// Conversation metrics and analysis
			InteractionCount   int `json:"interaction_count"`
			ResolutionAttempts []struct {
				Timestamp time.Time `json:"timestamp"`
				Proposal  string    `json:"proposal"`
				Outcome   string    `json:"outcome"`
			} `json:"resolution_attempts"`

			// Context tracking
			ContextualMetrics struct {
				CategoryFrequency map[string]int `json:"category_frequency"`
				TopicEvolution    []string       `json:"topic_evolution"`
				RelatedThreads    []int64        `json:"related_threads"`
			} `json:"contextual_metrics"`

			// Quality metrics
			QualityIndicators struct {
				ResponseLatency    []time.Duration `json:"response_latency"`
				ResolutionProgress float64         `json:"resolution_progress"`
				EffectivenesScore  float64         `json:"effectiveness_score"`
			} `json:"quality_indicators"`
		} `json:"thread_context"`
	} `json:"review_metadata"`
}

// ReviewHandoff represents handoff between review stages.
type ReviewHandoff struct {
	// Original chain output for reference
	ChainOutput ReviewChainOutput

	// Preprocessed validation results ready for comment generation
	ValidatedIssues []ValidatedIssue

	ThreadStatus ThreadStatus
}

// RuleCheckerMetadata holds metadata for rule checking.
type RuleCheckerMetadata struct {
	FilePath       string
	FileContent    string
	Changes        string
	Guidelines     []*Content
	ReviewPatterns []*Content
	LineRange      LineRange
	ChunkNumber    int
	TotalChunks    int

	Category string

	ThreadContext []PRReviewComment // Thread conversation history
	ThreadStatus  ThreadStatus      // Current thread state
}

// =============================================================================
// Interfaces
// =============================================================================

// ConsoleInterface defines the interface for console operations.
type ConsoleInterface interface {
	StartSpinner(message string)
	StopSpinner()
	WithSpinner(ctx context.Context, message string, fn func() error) error
	ShowComments(comments []PRReviewComment, metric MetricsCollector)
	ShowCommentsInteractive(comments []PRReviewComment, onPost func([]PRReviewComment) error) error
	ShowSummary(comments []PRReviewComment, metric MetricsCollector)
	StartReview(pr *github.PullRequest)
	ReviewingFile(file string, current, total int)
	ConfirmReviewPost(commentCount int) (bool, error)
	ReviewComplete()
	UpdateSpinnerText(text string)
	ShowReviewMetrics(metrics MetricsCollector, comments []PRReviewComment)
	CollectAllFeedback(comments []PRReviewComment, metric MetricsCollector) error
	Confirm(opts PromptOptions) (bool, error)
	FileError(filepath string, err error)
	Printf(format string, a ...interface{})
	Println(a ...interface{})
	PrintHeader(text string)
	NoIssuesFound(file string, chunkNumber, totalChunks int)
	SeverityIcon(severity string) string
	Color() bool
	Spinner() *spinner.Spinner
	IsInteractive() bool
}

// MetricsCollector defines the interface for collecting review metrics.
type MetricsCollector interface {
	TrackReviewStart(ctx context.Context, category string)
	TrackNewThread(ctx context.Context, threadID int64, comment PRReviewComment)
	TrackCommentResolution(ctx context.Context, threadID int64, resolution ResolutionOutcome)
	TrackReviewComment(ctx context.Context, comment PRReviewComment, isValid bool)
	TrackHistoricalComment(ctx context.Context, comment PRReviewComment)
	TrackUserFeedback(ctx context.Context, threadID int64, helpful bool, reason string)
	GetOutdatedRate(category string) float64
	GetPrecision(category string) float64
	GenerateReport(ctx context.Context) *BusinessReport
	GetCategoryMetrics(category string) *CategoryStats
	GetOverallOutdatedRate() float64
	GetWeeklyActiveUsers() int
	GetReviewResponseRate() float64
	StartOptimizationCycle(ctx context.Context)
	StartReviewSession(ctx context.Context, prNumber int)
	StartThreadTracking(ctx context.Context, comment PRReviewComment)
	TrackValidationResult(ctx context.Context, category string, validated bool)
	TrackDetectionResults(ctx context.Context, issueCount int)
}

// RAGStore defines the interface for RAG storage operations.
type RAGStore interface {
	// StoreContent saves a content piece with its embedding
	StoreContent(ctx context.Context, content *Content) error

	// StoreContents saves multiple content pieces in a single transaction for better performance
	StoreContents(ctx context.Context, contents []*Content) error

	// FindSimilar finds the most similar content pieces to the given embedding
	FindSimilar(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*Content, error)

	// FindSimilarWithDebug finds similar content with detailed debugging information
	FindSimilarWithDebug(ctx context.Context, embedding []float32, limit int, contentTypes ...string) ([]*Content, DebugInfo, error)

	// UpdateContent updates an existing content piece
	UpdateContent(ctx context.Context, content *Content) error

	// DeleteContent removes content by ID
	DeleteContent(ctx context.Context, id string) error

	// Populate style guide, best practices based on repo language
	PopulateGuidelines(ctx context.Context, language string) error

	// PopulateGuidelinesBackground runs guideline population without console output (for async use)
	PopulateGuidelinesBackground(ctx context.Context, language string) error

	StoreRule(ctx context.Context, rule ReviewRule) error

	// HasContent checks if the database contains any indexed content
	HasContent(ctx context.Context) (bool, error)

	// FindRelevantGuidelines performs pattern-based multi-vector search for guidelines
	FindRelevantGuidelines(ctx context.Context, patterns []SimpleCodePattern, limit int) ([]GuidelineSearchResult, error)

	// DB version control
	GetMetadata(ctx context.Context, key string) (string, error)
	SetMetadata(ctx context.Context, key, value string) error

	Close() error
}

// GitHubInterface defines the interface for GitHub operations.
type GitHubInterface interface {
	GetPullRequestChanges(ctx context.Context, prNumber int) (*PRChanges, error)
	GetFileContent(ctx context.Context, path string) (string, error)
	CreateReviewComments(ctx context.Context, prNumber int, comments []PRReviewComment) error
	GetLatestCommitSHA(ctx context.Context, branch string) (string, error)
	MonitorPRComments(ctx context.Context, prNumber int, callback func(comment *github.PullRequestComment)) error
	PreviewReview(ctx context.Context, console ConsoleInterface, prNumber int, comments []PRReviewComment, metric MetricsCollector) (bool, error)

	GetAuthenticatedUser(ctx context.Context) string
	GetRepositoryInfo(ctx context.Context) RepositoryInfo
	ListPullRequestComments(ctx context.Context, owner, repo string, prNumber int, opts *github.PullRequestListCommentsOptions) ([]*github.PullRequestComment, *github.Response, error)
	ListPullRequestReviews(ctx context.Context, owner, repo string, prNumber int, opts *github.ListOptions) ([]*github.PullRequestReview, *github.Response, error)
	CreatePullRequestReviewComment(ctx context.Context, owner, repo string, prNumber int, comment *github.PullRequestReviewRequest) (*github.PullRequestReview, *github.Response, error)

	GetPullRequest(ctx context.Context, owner, repo string, prNumber int) (*github.PullRequest, *github.Response, error)
	GetRepository(ctx context.Context, owner, repo string) (*github.Repository, *github.Response, error)
	GetRepositoryContents(ctx context.Context, owner, repo, path string, opts *github.RepositoryContentGetOptions) (*github.RepositoryContent, []*github.RepositoryContent, *github.Response, error)
	CompareCommits(ctx context.Context, owner, repo, base, head string, opts *github.ListOptions) (*github.CommitsComparison, *github.Response, error)
	ListLanguages(ctx context.Context, owner, repo string) (map[string]int, *github.Response, error)
	Client() *github.Client
}

// ReviewAgent defines the interface for review agents.
type ReviewAgent interface {
	ReviewPR(ctx context.Context, prNumber int, tasks []PRReviewTask, console ConsoleInterface) ([]PRReviewComment, error)
	ReviewPRWithChanges(ctx context.Context, prNumber int, tasks []PRReviewTask, console ConsoleInterface, preloadedChanges *PRChanges) ([]PRReviewComment, error)
	Stop(ctx context.Context)
	Metrics(ctx context.Context) MetricsCollector
	Orchestrator(ctx context.Context) *agents.FlexibleOrchestrator
	ClonedRepoPath() string
	WaitForClone(ctx context.Context, timeout time.Duration) string
	GetIndexingStatus() *IndexingStatus
	Close() error
}

// EmbeddingRouter defines the interface for embedding routing.
type EmbeddingRouter interface {
	GenerateEmbedding(ctx context.Context, text string, purpose string) ([]float32, error)
	GenerateEmbeddings(ctx context.Context, texts []string, purpose string) ([][]float32, error)
	GetDimensions() int
}

// =============================================================================
// Chunking Types
// =============================================================================

// ChunkingStrategy represents different approaches to chunking code.
type ChunkingStrategy string

const (
	// ChunkByFunction splits code primarily at function boundaries.
	ChunkByFunction ChunkingStrategy = "function"
	// ChunkBySize splits code into fixed-size chunks.
	ChunkBySize ChunkingStrategy = "size"
	// ChunkByLogic attempts to split at logical boundaries (classes, blocks, etc).
	ChunkByLogic ChunkingStrategy = "logic"
)

// ChunkConfig provides configuration options for code chunking.
type ChunkConfig struct {
	// Strategy determines how code should be split into chunks
	Strategy ChunkingStrategy

	// MaxTokens limits the size of each chunk in tokens
	// Recommended range: 500-2000. Larger values mean fewer chunks but more token usage
	MaxTokens int

	// ContextLines determines how many lines of surrounding context to include
	// Recommended range: 3-15. More context helps understanding but increases storage
	ContextLines int

	// MaxBytes is for byte-size limiting. Optional byte limit, only used for specific cases
	MaxBytes *int

	// OverlapLines controls how many lines overlap between chunks
	// Recommended range: 2-10. More overlap helps maintain context but increases storage
	OverlapLines int

	// MinChunkSize sets the minimum meaningful chunk size in lines
	// Chunks smaller than this will be merged with neighbors
	MinChunkSize int

	// LanguageSpecific contains language-specific chunking rules
	LanguageSpecific map[string]interface{}

	// FilePatterns allows different configs for different file patterns
	FilePatterns map[string]ChunkConfig

	// Internal use
	FileMetadata map[string]interface{}

	GenerateDescriptions bool
}

// =============================================================================
// Console Implementation Types (for use by implementations)
// =============================================================================

// Console handles user-facing output separate from logging.
type Console struct {
	W             io.Writer
	Logger        *logging.Logger // For debug logs
	Spin          *spinner.Spinner
	ColorEnabled  bool
	IsInteractive bool // Cached terminal interactivity state

	Mu sync.Mutex
}

// =============================================================================
// Utility Types
// =============================================================================

// StreamHandler is a function type for handling stream chunks.
type StreamHandler func(chunk core.StreamChunk) error

// =============================================================================
// Enhanced Features Types
// =============================================================================

// EnhancedFeatures manages feature flags for advanced DSPy-Go capabilities.
type EnhancedFeatures struct {
	// Phase 1 Features
	AdvancedReasoning   bool
	ConsensusValidation bool
	CommentRefinement   bool

	// Phase 2 Features
	DeclarativeWorkflows          bool
	IntelligentParallelProcessing bool // Phase 2.2 Intelligent Parallel Processing
	AdvancedMemory                bool
	AdaptiveStrategy              bool
	ResourceMonitoring            bool
	AdaptiveResourceManagement    bool // Adaptive resource monitoring
	LoadBalancing                 bool // Intelligent load balancing
	CircuitBreaking               bool // Circuit breaker patterns

	// Phase 3 Features (for future use)
	ReactiveProcessing bool
	ConversationalMode bool
	LearningAdaptation bool

	// Fallback settings
	FallbackToLegacy   bool
	EnableMetrics      bool
	EnableDebugLogging bool
}

// DefaultEnhancedFeatures returns the default feature configuration.
func DefaultEnhancedFeatures() *EnhancedFeatures {
	return &EnhancedFeatures{
		// Phase 1 - enabled by default for gradual rollout
		AdvancedReasoning:   true,
		ConsensusValidation: true,
		CommentRefinement:   true,

		// Phase 2 - enabled for advanced users
		DeclarativeWorkflows:          true,
		IntelligentParallelProcessing: true,
		AdvancedMemory:                true,
		AdaptiveStrategy:              true,
		ResourceMonitoring:            true,
		AdaptiveResourceManagement:    true,
		LoadBalancing:                 true,
		CircuitBreaking:               true,

		// Phase 3 - disabled until implemented
		ReactiveProcessing: false,
		ConversationalMode: false,
		LearningAdaptation: false,

		// Safety features
		FallbackToLegacy:   true,
		EnableMetrics:      true,
		EnableDebugLogging: false,
	}
}

// IsEnhancedProcessingEnabled checks if any enhanced features are enabled.
func (f *EnhancedFeatures) IsEnhancedProcessingEnabled() bool {
	return f.AdvancedReasoning || f.ConsensusValidation || f.CommentRefinement ||
		f.DeclarativeWorkflows || f.IntelligentParallelProcessing || f.AdvancedMemory ||
		f.ReactiveProcessing || f.ConversationalMode || f.LearningAdaptation
}

// GetPhaseLevel returns the highest enabled phase level.
func (f *EnhancedFeatures) GetPhaseLevel() int {
	if f.ReactiveProcessing || f.ConversationalMode || f.LearningAdaptation {
		return 3
	}
	if f.DeclarativeWorkflows || f.IntelligentParallelProcessing || f.AdvancedMemory {
		return 2
	}
	if f.AdvancedReasoning || f.ConsensusValidation || f.CommentRefinement {
		return 1
	}
	return 0
}

// =============================================================================
// Context Extraction Types
// =============================================================================

// FileContext represents the comprehensive file-level context for enhanced code review.
type FileContext struct {
	PackageDeclaration string                `json:"package_declaration"`
	Imports            []string              `json:"imports"`
	TypeDefinitions    []TypeDefinition      `json:"type_definitions"`
	Interfaces         []InterfaceDefinition `json:"interfaces"`
	Functions          []FunctionSignature   `json:"functions"`
	Methods            []MethodSignature     `json:"methods"`
	Constants          []ConstantDefinition  `json:"constants"`
	Variables          []VariableDefinition  `json:"variables"`
}

// TypeDefinition represents a type definition in the file.
type TypeDefinition struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"` // struct, interface, alias, etc.
	Fields     []FieldDefinition `json:"fields,omitempty"`
	Methods    []string          `json:"methods,omitempty"`
	DocComment string            `json:"doc_comment,omitempty"`
	LineNumber int               `json:"line_number"`
	Visibility string            `json:"visibility"` // public, private
}

// FieldDefinition represents a struct field.
type FieldDefinition struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Tag        string `json:"tag,omitempty"`
	DocComment string `json:"doc_comment,omitempty"`
}

// InterfaceDefinition represents an interface definition.
type InterfaceDefinition struct {
	Name          string            `json:"name"`
	Methods       []InterfaceMethod `json:"methods"`
	EmbeddedTypes []string          `json:"embedded_types,omitempty"`
	DocComment    string            `json:"doc_comment,omitempty"`
	LineNumber    int               `json:"line_number"`
	Visibility    string            `json:"visibility"`
}

// InterfaceMethod represents a method in an interface.
type InterfaceMethod struct {
	Name       string      `json:"name"`
	Parameters []Parameter `json:"parameters,omitempty"`
	Returns    []Parameter `json:"returns,omitempty"`
	DocComment string      `json:"doc_comment,omitempty"`
}

// FunctionSignature represents a function signature.
type FunctionSignature struct {
	Name       string      `json:"name"`
	Parameters []Parameter `json:"parameters,omitempty"`
	Returns    []Parameter `json:"returns,omitempty"`
	DocComment string      `json:"doc_comment,omitempty"`
	LineNumber int         `json:"line_number"`
	Visibility string      `json:"visibility"`
}

// MethodSignature represents a method signature with receiver.
type MethodSignature struct {
	Name       string      `json:"name"`
	Receiver   Parameter   `json:"receiver"`
	Parameters []Parameter `json:"parameters,omitempty"`
	Returns    []Parameter `json:"returns,omitempty"`
	DocComment string      `json:"doc_comment,omitempty"`
	LineNumber int         `json:"line_number"`
	Visibility string      `json:"visibility"`
}

// Parameter represents a function/method parameter or return value.
type Parameter struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
}

// ConstantDefinition represents a constant declaration.
type ConstantDefinition struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	Value      string `json:"value,omitempty"`
	DocComment string `json:"doc_comment,omitempty"`
	LineNumber int    `json:"line_number"`
	Visibility string `json:"visibility"`
}

// VariableDefinition represents a variable declaration.
type VariableDefinition struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	Value      string `json:"value,omitempty"`
	DocComment string `json:"doc_comment,omitempty"`
	LineNumber int    `json:"line_number"`
	Visibility string `json:"visibility"`
}

// ChunkDependencies represents the dependencies and references within a code chunk.
type ChunkDependencies struct {
	CalledFunctions  []FunctionSignature `json:"called_functions"`
	UsedTypes        []TypeReference     `json:"used_types"`
	ImportedPackages []string            `json:"imported_packages"`
	SemanticPurpose  string              `json:"semantic_purpose"`
}

// TypeReference represents a reference to a type within code.
type TypeReference struct {
	Name       string `json:"name"`
	Package    string `json:"package,omitempty"`
	IsPointer  bool   `json:"is_pointer,omitempty"`
	LineNumber int    `json:"line_number,omitempty"`
}
