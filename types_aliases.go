// Package main - Type aliases from internal/types
// This file provides backward compatibility by re-exporting types from internal/types
// as package-level types. This is the SINGLE SOURCE OF TRUTH for type definitions.
package main

import (
	"github.com/XiaoConstantine/maestro/internal/types"
)

// =============================================================================.
type (
	ReviewChunk     = types.ReviewChunk
	PRReviewTask    = types.PRReviewTask
	PRReviewComment = types.PRReviewComment
	LineRange       = types.LineRange
	MessageType     = types.MessageType
)

// =============================================================================.
type (
	PRChanges      = types.PRChanges
	PRFileChange   = types.PRFileChange
	ChangeHunk     = types.ChangeHunk
	RepositoryInfo = types.RepositoryInfo
)

// =============================================================================.
type (
	Content               = types.Content
	DebugInfo             = types.DebugInfo
	QualityMetrics        = types.QualityMetrics
	RAGStore              = types.RAGStore
	ReviewRule            = types.ReviewRule
	RuleMetadata          = types.RuleMetadata
	GuidelineSearchResult = types.GuidelineSearchResult
	SimpleCodePattern     = types.SimpleCodePattern
	GuidelineContent      = types.GuidelineContent
	CodeExample           = types.CodeExample
)

// Content type constants.
const (
	ContentTypeRepository = types.ContentTypeRepository
	ContentTypeGuideline  = types.ContentTypeGuideline
)

// =============================================================================.
type (
	MetricsCollector  = types.MetricsCollector
	BusinessReport    = types.BusinessReport
	CategoryMetrics   = types.CategoryMetrics
	CategoryStats     = types.CategoryStats
	ResolutionOutcome = types.ResolutionOutcome
)

// Resolution constants.
const (
	ResolutionAccepted   = types.ResolutionAccepted
	ResolutionRejected   = types.ResolutionRejected
	ResolutionNeedsWork  = types.ResolutionNeedsWork
	ResolutionInProgress = types.ResolutionInProgress
)

// =============================================================================.
type (
	ThreadStatus = types.ThreadStatus
)

// Thread status constants.
const (
	ThreadOpen       = types.ThreadOpen
	ThreadResolved   = types.ThreadResolved
	ThreadInProgress = types.ThreadInProgress
	ThreadStale      = types.ThreadStale
)

// =============================================================================.
const (
	QuestionMessage        = types.QuestionMessage
	ClarificationMessage   = types.ClarificationMessage
	ResolutionMessage      = types.ResolutionMessage
	SuggestionMessage      = types.SuggestionMessage
	AcknowledgementMessage = types.AcknowledgementMessage
)

// =============================================================================.
type (
	ValidatedIssue       = types.ValidatedIssue
	ReviewHandoff        = types.ReviewHandoff
	ReviewIssue          = types.ReviewIssue
	PotentialIssue       = types.PotentialIssue
	EnhancedReviewResult = types.EnhancedReviewResult
	RuleCheckerMetadata  = types.RuleCheckerMetadata
	ReviewChainOutput    = types.ReviewChainOutput
)

// Resolution effectiveness and attempt types.
type (
	ResolutionEffectiveness = types.ResolutionEffectiveness
	ResolutionAttempt       = types.ResolutionAttempt
)

// Resolution effectiveness constants.
const (
	HighEffectiveness      = types.HighEffectiveness
	MediumEffectiveness    = types.MediumEffectiveness
	LowEffectiveness       = types.LowEffectiveness
	UnknownEffectiveness   = types.UnknownEffectiveness
	ResolutionInconclusive = types.ResolutionInconclusive
)

// =============================================================================.
type (
	GitHubInterface = types.GitHubInterface
	ReviewAgent     = types.ReviewAgent
)

// =============================================================================.
type (
	AgentConfig    = types.AgentConfig
	IndexingStatus = types.IndexingStatus
	PromptOptions  = types.PromptOptions
	SpinnerConfig  = types.SpinnerConfig
)

// =============================================================================.
type ConsoleInterface = types.ConsoleInterface

// =============================================================================
// Helper Functions (delegates to types)
// =============================================================================

// NewIndexingStatus creates a new IndexingStatus.
func NewIndexingStatus() *IndexingStatus {
	return types.NewIndexingStatus()
}

// DefaultPromptOptions returns default prompt options.
func DefaultPromptOptions() PromptOptions {
	return types.DefaultPromptOptions()
}
