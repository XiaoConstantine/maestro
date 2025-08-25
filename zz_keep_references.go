package main

// Package-level references to mark otherwise-unused symbols as used for linters.
var (
	_ = reviewPRWithPersistentMCP
	_ = base64DecodeContent
	_ = (*PRReviewAgent).processExistingComments
)
