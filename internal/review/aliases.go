// Package review - Type aliases for backward compatibility
// These aliases allow code to use bare type names without the types. prefix
package review

import "github.com/XiaoConstantine/maestro/internal/types"

// Interface types.
type (
	GitHubInterface  = types.GitHubInterface
	ReviewAgent      = types.ReviewAgent
	MetricsCollector = types.MetricsCollector
	ConsoleInterface = types.ConsoleInterface
	RAGStore         = types.RAGStore
)

// Config types.
type (
	AgentConfig    = types.AgentConfig
	IndexingStatus = types.IndexingStatus
)

// Core review types.
type (
	PRReviewComment = types.PRReviewComment
	PRReviewTask    = types.PRReviewTask
	PRChanges       = types.PRChanges
	PRFileChange    = types.PRFileChange
	ReviewChunk     = types.ReviewChunk
	Content         = types.Content
	LineRange       = types.LineRange
	ChangeHunk      = types.ChangeHunk
)

// Validation and processing types.
type (
	PotentialIssue      = types.PotentialIssue
	ValidatedIssue      = types.ValidatedIssue
	ReviewIssue         = types.ReviewIssue
	ReviewChainOutput   = types.ReviewChainOutput
	ReviewHandoff       = types.ReviewHandoff
	RuleCheckerMetadata = types.RuleCheckerMetadata
)

// Thread types.
type (
	ThreadStatus      = types.ThreadStatus
	ResolutionOutcome = types.ResolutionOutcome
	MessageType       = types.MessageType
)

// Thread status constants.
const (
	ThreadOpen       = types.ThreadOpen
	ThreadResolved   = types.ThreadResolved
	ThreadInProgress = types.ThreadInProgress
	ThreadStale      = types.ThreadStale
)

// Context extraction types.
type (
	FileContext         = types.FileContext
	TypeDefinition      = types.TypeDefinition
	FieldDefinition     = types.FieldDefinition
	InterfaceDefinition = types.InterfaceDefinition
	InterfaceMethod     = types.InterfaceMethod
	FunctionSignature   = types.FunctionSignature
	MethodSignature     = types.MethodSignature
	Parameter           = types.Parameter
	ConstantDefinition  = types.ConstantDefinition
	VariableDefinition  = types.VariableDefinition
	ChunkDependencies   = types.ChunkDependencies
	TypeReference       = types.TypeReference
)
