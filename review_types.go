package main

import "time"

type ReviewHandoff struct {
	// Original chain output for reference
	ChainOutput ReviewChainOutput

	// Preprocessed validation results ready for comment generation
	ValidatedIssues []ValidatedIssue
}

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

	ThreadID            *int64
	ThreadHistory       []PRReviewComment
	ParentComment       *PRReviewComment
	ConversationContext struct {
		OriginalAuthor    string
		LastUpdate        time.Time
		Status            ThreadStatus
		PreviousResponses []string
		ResolutionStatus  ResolutionOutcome
	}
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
