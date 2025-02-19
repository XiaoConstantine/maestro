package main

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
