package types

const DefaultCommentHunkLineSlack = 5

// CommentInChangedHunks reports whether a line-anchored review comment falls
// within any changed hunk for its file, allowing lineSlack lines of unchanged
// context around each hunk boundary.
func CommentInChangedHunks(comment PRReviewComment, hunks []ChangeHunk, lineSlack int) bool {
	if lineSlack < 0 {
		lineSlack = 0
	}
	for i := range hunks {
		if comment.LineNumber >= hunks[i].StartLine-lineSlack && comment.LineNumber <= hunks[i].EndLine+lineSlack {
			return true
		}
	}
	return false
}
