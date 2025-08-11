package main

import (
	"strconv"
)

// agenticParseFloatWithErr parses a string to float64, renamed to avoid conflicts
func agenticParseFloatWithErr(s string, bitSize int) (float64, error) {
	return strconv.ParseFloat(s, bitSize)
}

// parseInt parses a string to int64, helper function for query analysis
func parseInt(s string, base int, bitSize int) (int64, error) {
	return strconv.ParseInt(s, base, bitSize)
}