package main

import (
	"github.com/XiaoConstantine/maestro/terminal"
)

// launchReviewTUI bridges the main package to the terminal package's TUI.
func launchReviewTUI(comments []reviewComment, onPost func([]reviewComment) error) error {
	// Convert to terminal.ReviewComment
	terminalComments := make([]terminal.ReviewComment, 0, len(comments))
	for _, c := range comments {
		terminalComments = append(terminalComments, terminal.ReviewComment{
			FilePath:   c.FilePath,
			LineNumber: c.LineNumber,
			Content:    c.Content,
			Severity:   c.Severity,
			Suggestion: c.Suggestion,
			Category:   c.Category,
			CodeBlock:  c.CodeBlock,
			DiffBlock:  c.DiffBlock,
		})
	}

	// Create post callback that converts back
	var terminalOnPost func([]terminal.ReviewComment) error
	if onPost != nil {
		terminalOnPost = func(termComments []terminal.ReviewComment) error {
			mainComments := make([]reviewComment, 0, len(termComments))
			for _, tc := range termComments {
				mainComments = append(mainComments, reviewComment{
					FilePath:   tc.FilePath,
					LineNumber: tc.LineNumber,
					Content:    tc.Content,
					Severity:   tc.Severity,
					Suggestion: tc.Suggestion,
					Category:   tc.Category,
					CodeBlock:  tc.CodeBlock,
					DiffBlock:  tc.DiffBlock,
				})
			}
			return onPost(mainComments)
		}
	}

	return terminal.RunReviewTUI(terminalComments, terminalOnPost)
}
