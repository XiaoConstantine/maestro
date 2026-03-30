package review

import (
	"sort"
	"strings"
	"unicode"
)

const reviewMergeLineSlack = 2

func postProcessReviewComments(comments []PRReviewComment) []PRReviewComment {
	if len(comments) == 0 {
		return nil
	}

	normalized := make([]PRReviewComment, 0, len(comments))
	for _, comment := range comments {
		normalized = append(normalized, normalizeReviewComment(comment))
	}

	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].FilePath != normalized[j].FilePath {
			return normalized[i].FilePath < normalized[j].FilePath
		}
		if normalized[i].LineNumber != normalized[j].LineNumber {
			return normalized[i].LineNumber < normalized[j].LineNumber
		}
		if normalized[i].EndLine != normalized[j].EndLine {
			return normalized[i].EndLine < normalized[j].EndLine
		}
		return reviewPriorityScore(normalized[i]) > reviewPriorityScore(normalized[j])
	})

	merged := make([]PRReviewComment, 0, len(normalized))
	for _, candidate := range normalized {
		mergedIndex := -1
		for i := len(merged) - 1; i >= 0; i-- {
			existing := merged[i]
			if existing.FilePath != candidate.FilePath {
				break
			}
			if existing.EndLine+reviewMergeLineSlack < candidate.LineNumber {
				break
			}
			if shouldMergeReviewComments(existing, candidate) {
				mergedIndex = i
				break
			}
		}

		if mergedIndex >= 0 {
			merged[mergedIndex] = mergeReviewComments(merged[mergedIndex], candidate)
			continue
		}

		merged = append(merged, candidate)
	}

	sort.SliceStable(merged, func(i, j int) bool {
		leftScore := reviewPriorityScore(merged[i])
		rightScore := reviewPriorityScore(merged[j])
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if merged[i].FilePath != merged[j].FilePath {
			return merged[i].FilePath < merged[j].FilePath
		}
		if merged[i].LineNumber != merged[j].LineNumber {
			return merged[i].LineNumber < merged[j].LineNumber
		}
		if merged[i].EndLine != merged[j].EndLine {
			return merged[i].EndLine < merged[j].EndLine
		}
		return len(merged[i].Content) > len(merged[j].Content)
	})

	return merged
}

func normalizeReviewComment(comment PRReviewComment) PRReviewComment {
	comment.FilePath = strings.TrimSpace(comment.FilePath)
	comment.Content = strings.TrimSpace(comment.Content)
	comment.Suggestion = strings.TrimSpace(comment.Suggestion)
	comment.Category = normalizeCategory(comment.Category)
	comment.Severity = normalizeSeverity(comment.Severity)
	if comment.LineNumber < 0 {
		comment.LineNumber = 0
	}
	if comment.EndLine < comment.LineNumber {
		comment.EndLine = comment.LineNumber
	}
	if comment.Confidence <= 0 {
		comment.Confidence = 0.5
	}
	return comment
}

func shouldMergeReviewComments(left, right PRReviewComment) bool {
	if left.FilePath != right.FilePath {
		return false
	}

	if !rangesTouch(left, right, reviewMergeLineSlack) {
		return false
	}

	if left.Category != "" && right.Category != "" && left.Category != right.Category {
		return false
	}

	if exactReviewFingerprint(left) == exactReviewFingerprint(right) {
		return true
	}

	if !sameReviewKind(left, right) {
		return false
	}

	contentSimilarity := reviewTextSimilarity(left.Content, right.Content)
	if contentSimilarity >= 0.82 {
		return true
	}

	suggestionSimilarity := reviewTextSimilarity(left.Suggestion, right.Suggestion)
	if contentSimilarity >= 0.62 && suggestionSimilarity >= 0.70 {
		return true
	}

	return sharedReviewSignalCount(combinedReviewText(left), combinedReviewText(right)) >= 3
}

func mergeReviewComments(left, right PRReviewComment) PRReviewComment {
	primary, secondary := left, right
	if reviewPriorityScore(secondary) > reviewPriorityScore(primary) {
		primary, secondary = secondary, primary
	}

	merged := primary
	if secondary.LineNumber > 0 && (merged.LineNumber == 0 || secondary.LineNumber < merged.LineNumber) {
		merged.LineNumber = secondary.LineNumber
	}
	if secondary.EndLine > merged.EndLine {
		merged.EndLine = secondary.EndLine
	}
	if severityRank(secondary.Severity) > severityRank(merged.Severity) {
		merged.Severity = secondary.Severity
	}
	if secondary.Confidence > merged.Confidence {
		merged.Confidence = secondary.Confidence
	}
	if merged.Category == "" {
		merged.Category = secondary.Category
	}
	merged.Content = selectRicherReviewText(primary.Content, secondary.Content)
	merged.Suggestion = selectRicherReviewText(primary.Suggestion, secondary.Suggestion)
	return normalizeReviewComment(merged)
}

func rangesTouch(left, right PRReviewComment, slack int) bool {
	leftStart, leftEnd := left.LineNumber, left.EndLine
	rightStart, rightEnd := right.LineNumber, right.EndLine

	if leftStart == 0 || rightStart == 0 {
		return false
	}
	if leftEnd == 0 {
		leftEnd = leftStart
	}
	if rightEnd == 0 {
		rightEnd = rightStart
	}

	return leftStart <= rightEnd+slack && rightStart <= leftEnd+slack
}

func exactReviewFingerprint(comment PRReviewComment) string {
	return strings.Join([]string{
		comment.FilePath,
		comment.Category,
		normalizeReviewText(comment.Content),
		normalizeReviewText(comment.Suggestion),
	}, "|")
}

func sameReviewKind(left, right PRReviewComment) bool {
	if left.Category != "" && right.Category != "" {
		return left.Category == right.Category
	}
	return normalizeSeverity(left.Severity) == normalizeSeverity(right.Severity)
}

func reviewTextSimilarity(left, right string) float64 {
	left = normalizeReviewText(left)
	right = normalizeReviewText(right)
	if left == "" || right == "" {
		return 0
	}
	if left == right {
		return 1
	}
	if strings.Contains(left, right) || strings.Contains(right, left) {
		shorterLen := len(left)
		if len(right) < shorterLen {
			shorterLen = len(right)
		}
		if shorterLen >= 24 {
			return 0.85
		}
	}

	leftTokens := tokenizeReviewText(left)
	rightTokens := tokenizeReviewText(right)
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0
	}

	intersection := 0
	union := make(map[string]struct{}, len(leftTokens)+len(rightTokens))
	for token := range leftTokens {
		union[token] = struct{}{}
		if _, ok := rightTokens[token]; ok {
			intersection++
		}
	}
	for token := range rightTokens {
		union[token] = struct{}{}
	}

	return float64(intersection) / float64(len(union))
}

func tokenizeReviewText(value string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, token := range strings.Fields(value) {
		if len(token) < 3 || reviewStopWord(token) {
			continue
		}
		tokens[token] = struct{}{}
	}
	return tokens
}

func combinedReviewText(comment PRReviewComment) string {
	if comment.Suggestion == "" {
		return comment.Content
	}
	if comment.Content == "" {
		return comment.Suggestion
	}
	return comment.Content + "\n" + comment.Suggestion
}

func sharedReviewSignalCount(left, right string) int {
	leftTokens := tokenizeReviewText(normalizeReviewText(left))
	rightTokens := tokenizeReviewText(normalizeReviewText(right))
	count := 0
	for token := range leftTokens {
		if _, ok := rightTokens[token]; ok {
			count++
		}
	}
	return count
}

func normalizeReviewText(value string) string {
	if value == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(value))

	lastSpace := false
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			builder.WriteByte(' ')
			lastSpace = true
		}
	}

	return strings.TrimSpace(builder.String())
}

func reviewStopWord(token string) bool {
	switch token {
	case "the", "this", "that", "with", "from", "into", "will", "when", "here", "there",
		"which", "before", "after", "should", "could", "would", "where", "have", "still",
		"error", "errors", "check", "checks", "return", "returns", "nil", "function",
		"functions", "variable", "variables", "type", "types", "value", "values", "handle",
		"handles", "code", "path", "paths":
		return true
	default:
		return false
	}
}

func selectRicherReviewText(primary, secondary string) string {
	primary = strings.TrimSpace(primary)
	secondary = strings.TrimSpace(secondary)
	if primary == "" {
		return secondary
	}
	if secondary == "" {
		return primary
	}
	if normalizeReviewText(primary) == normalizeReviewText(secondary) {
		if len(secondary) > len(primary) {
			return secondary
		}
		return primary
	}
	if len(secondary) > len(primary) {
		return secondary
	}
	return primary
}

func normalizeCategory(category string) string {
	return strings.TrimSpace(strings.ToLower(category))
}

func normalizeSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return "critical"
	case "high", "error":
		return "high"
	case "medium", "warning":
		return "medium"
	case "low", "suggestion", "info":
		return "low"
	default:
		if strings.TrimSpace(severity) == "" {
			return "medium"
		}
		return strings.ToLower(strings.TrimSpace(severity))
	}
}

func severityRank(severity string) int {
	switch normalizeSeverity(severity) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func categoryPriority(category string) float64 {
	switch normalizeCategory(category) {
	case "security":
		return 0.4
	case "bug", "correctness", "error-handling":
		return 0.3
	case "performance":
		return 0.2
	case "style", "documentation":
		return 0.1
	default:
		return 0
	}
}

func reviewPriorityScore(comment PRReviewComment) float64 {
	score := float64(severityRank(comment.Severity))*6 + comment.Confidence*8 + categoryPriority(comment.Category)*5
	if comment.EndLine > comment.LineNumber && comment.LineNumber > 0 {
		score += 0.5
	}
	if len(strings.TrimSpace(comment.Suggestion)) > 0 {
		score += 0.25
	}
	return score
}
