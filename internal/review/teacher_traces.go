package review

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type ReviewTeacherTrace struct {
	CaseID          string            `json:"case_id"`
	Label           string            `json:"label,omitempty"`
	FilePath        string            `json:"file_path,omitempty"`
	Line            int               `json:"line,omitempty"`
	InputDiff       string            `json:"input_diff,omitempty"`
	ReviewerComment string            `json:"reviewer_comment,omitempty"`
	TeacherComments []PRReviewComment `json:"teacher_comments,omitempty"`
	Score           float64           `json:"score"`
	RawScore        float64           `json:"raw_score,omitempty"`
	CommentCount    int               `json:"comment_count,omitempty"`
	Matched         bool              `json:"matched,omitempty"`
	MatchedComment  string            `json:"matched_comment,omitempty"`
	EvaluationError string            `json:"evaluation_error,omitempty"`
}

type ReviewTeacherTraceReport struct {
	GeneratedAt          time.Time            `json:"generated_at"`
	ModelID              string               `json:"model_id"`
	SuitePaths           []string             `json:"suite_paths,omitempty"`
	TrainingExampleCount int                  `json:"training_example_count"`
	Traces               []ReviewTeacherTrace `json:"traces"`
}

func SortReviewTeacherTraces(traces []ReviewTeacherTrace) {
	sort.SliceStable(traces, func(i, j int) bool {
		if traces[i].Score == traces[j].Score {
			return traces[i].CaseID < traces[j].CaseID
		}
		return traces[i].Score > traces[j].Score
	})
}

func LoadReviewTeacherTraceReport(path string) (ReviewTeacherTraceReport, error) {
	resolvedPath, err := expandReviewPath(path)
	if err != nil {
		return ReviewTeacherTraceReport{}, fmt.Errorf("resolve teacher trace path %q: %w", path, err)
	}
	if resolvedPath == "" {
		return ReviewTeacherTraceReport{}, fmt.Errorf("teacher trace path is required")
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return ReviewTeacherTraceReport{}, fmt.Errorf("read teacher traces %q: %w", resolvedPath, err)
	}

	var report ReviewTeacherTraceReport
	if err := json.Unmarshal(data, &report); err != nil {
		return ReviewTeacherTraceReport{}, fmt.Errorf("decode teacher traces %q: %w", resolvedPath, err)
	}
	return report, nil
}

func SelectTopTeacherDemos(traces []ReviewTeacherTrace, limit int) string {
	selected := selectTopTeacherTraceCandidates(traces, limit)
	if len(selected) == 0 {
		return ""
	}

	var sections []string
	for i, trace := range selected {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Example %d\n", i+1))
		if trace.FilePath != "" {
			sb.WriteString(fmt.Sprintf("File: %s\n", trace.FilePath))
		}
		if trace.Line > 0 {
			sb.WriteString(fmt.Sprintf("Line: %d\n", trace.Line))
		}
		if diff := strings.TrimSpace(trace.InputDiff); diff != "" {
			sb.WriteString("Patch:\n")
			sb.WriteString(diff)
			sb.WriteString("\n")
		}
		sb.WriteString("Good review output:\n")
		for _, comment := range trace.TeacherComments {
			content := strings.TrimSpace(comment.Content)
			if content == "" {
				continue
			}
			if comment.LineNumber > 0 {
				sb.WriteString(fmt.Sprintf("- line %d: %s\n", comment.LineNumber, content))
			} else {
				sb.WriteString("- ")
				sb.WriteString(content)
				sb.WriteString("\n")
			}
			if suggestion := strings.TrimSpace(comment.Suggestion); suggestion != "" {
				sb.WriteString("  Suggestion: ")
				sb.WriteString(suggestion)
				sb.WriteString("\n")
			}
		}
		sections = append(sections, strings.TrimSpace(sb.String()))
	}

	return strings.Join(sections, "\n\n")
}

func selectTopTeacherTraceCandidates(traces []ReviewTeacherTrace, limit int) []ReviewTeacherTrace {
	if limit <= 0 || len(traces) == 0 {
		return nil
	}

	candidates := make([]ReviewTeacherTrace, 0, len(traces))
	for _, trace := range traces {
		if strings.TrimSpace(trace.EvaluationError) != "" {
			continue
		}
		if len(trace.TeacherComments) == 0 {
			continue
		}
		if !trace.Matched || trace.Score <= 0 {
			continue
		}
		candidates = append(candidates, trace)
	}

	if len(candidates) == 0 {
		for _, trace := range traces {
			if strings.TrimSpace(trace.EvaluationError) != "" || len(trace.TeacherComments) == 0 {
				continue
			}
			candidates = append(candidates, trace)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	SortReviewTeacherTraces(candidates)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return append([]ReviewTeacherTrace(nil), candidates...)
}
