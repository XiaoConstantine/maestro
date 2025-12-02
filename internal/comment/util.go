package comment

import (
	"fmt"
	"strings"

	"github.com/XiaoConstantine/maestro/internal/types"
)

// ExtractResponseMetadata extracts response metadata from task metadata.
func ExtractResponseMetadata(metadata map[string]interface{}) (*types.ResponseMetadata, error) {
	rm := &types.ResponseMetadata{}

	// Get original comment details
	if comment, ok := metadata["original_comment"].(string); ok && comment != "" {
		rm.OriginalComment = comment
	} else {
		return nil, fmt.Errorf("missing or invalid original comment")
	}

	// Get thread context (default empty slice handled upstream)
	if thread, ok := metadata["thread_context"].([]types.PRReviewComment); ok {
		rm.ThreadContext = thread
	}

	// Get file content
	if content, ok := metadata["file_content"].(string); ok {
		rm.FileContent = content
	}

	// Get file path
	if path, ok := metadata["file_path"].(string); ok {
		rm.FilePath = path
	}

	// Get thread identifiers
	if threadID, exists := metadata["thread_id"]; exists {
		switch v := threadID.(type) {
		case int64:
			rm.ThreadID = &v
		case float64:
			val := int64(v)
			rm.ThreadID = &val
		}
	}
	if rangeData, ok := metadata["line_range"].(map[string]interface{}); ok {
		startLine, startOk := rangeData["start"].(int)
		endLine, endOk := rangeData["end"].(int)
		if !startOk || !endOk {
			return nil, fmt.Errorf("invalid line range format: start and end must be integers")
		}
		rm.LineRange = types.LineRange{Start: startLine, End: endLine, File: rm.FilePath}
	} else if line, ok := metadata["line_number"].(int); ok {
		rm.LineRange = types.LineRange{Start: line, End: line, File: rm.FilePath}
	} else {
		return nil, fmt.Errorf("missing or invalid line range information")
	}

	if replyTo, ok := metadata["in_reply_to"].(*int64); ok {
		rm.InReplyTo = replyTo
	}

	if category, ok := metadata["category"].(string); ok {
		rm.Category = category
	} else {
		rm.Category = "code-style" // Default if not provided
	}

	if rm.LineRange.Start == 0 {
		return nil, fmt.Errorf("failed to extract valid line number from metadata: %+v", metadata)
	}

	return rm, nil
}

// ParseResponseResult parses the response result from the LLM.
func ParseResponseResult(result interface{}) (*types.ResponseResult, error) {
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid result type: %T, expected map[string]interface{}", result)
	}

	response := &types.ResponseResult{}

	if respContent, ok := resultMap["response"].(string); ok {
		response.Response = respContent
	} else {
		return nil, fmt.Errorf("missing or invalid response content, got %T", resultMap["response"])
	}

	if status, ok := resultMap["resolution_status"].(string); ok {
		status = strings.ToLower(status)
		validStatuses := map[string]bool{
			"resolved":            true,
			"needs_work":          true,
			"needs_clarification": true,
			"acknowledged":        true,
		}
		if validStatuses[status] {
			response.ResolutionStatus = status
		} else {
			response.ResolutionStatus = "acknowledged"
		}
	}

	if items, ok := resultMap["action_items"].([]interface{}); ok {
		response.ActionItems = make([]string, 0, len(items))
		for _, item := range items {
			if strItem, ok := item.(string); ok {
				response.ActionItems = append(response.ActionItems, strItem)
			}
		}
	}

	return response, ValidateResponse(response)
}

// ValidateResponse validates a response result.
func ValidateResponse(response *types.ResponseResult) error {
	if response.Response == "" {
		return fmt.Errorf("empty response content")
	}

	if ContainsUnprofessionalContent(response.Response) {
		return fmt.Errorf("response contains unprofessional content")
	}

	if response.ResolutionStatus == "needs_work" && len(response.ActionItems) == 0 {
		return fmt.Errorf("status indicates work needed but no action items provided")
	}

	return nil
}

// ContainsUnprofessionalContent checks if content contains unprofessional terms.
func ContainsUnprofessionalContent(content string) bool {
	unprofessionalTerms := []string{
		"stupid",
		"lazy",
		"horrible",
		"terrible",
		"awful",
	}
	lowered := strings.ToLower(content)
	for _, term := range unprofessionalTerms {
		if strings.Contains(lowered, term) {
			return true
		}
	}
	return false
}

// DeriveSeverity derives the severity from the resolution status.
func DeriveSeverity(status string) string {
	switch status {
	case "needs_work":
		return "warning"
	case "needs_clarification":
		return "suggestion"
	case "resolved":
		return "suggestion"
	default:
		return "suggestion"
	}
}

// FormatActionItems formats action items as a string.
func FormatActionItems(items []string) string {
	var sb strings.Builder
	sb.WriteString("Suggested actions:\n")
	for _, item := range items {
		sb.WriteString(fmt.Sprintf("- %s\n", item))
	}
	return sb.String()
}

// MapResponseStatusToResolution maps response status to resolution outcome.
func MapResponseStatusToResolution(status string) types.ResolutionOutcome {
	switch strings.ToLower(status) {
	case "resolved":
		return types.ResolutionAccepted
	case "needs_work":
		return types.ResolutionNeedsWork
	case "needs_clarification":
		return types.ResolutionInconclusive
	case "acknowledged":
		return types.ResolutionInProgress
	default:
		return types.ResolutionInProgress
	}
}

// ExtractActionItems extracts action items from a suggestion string.
func ExtractActionItems(suggestion string) []string {
	lines := strings.Split(suggestion, "\n")
	var items []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			items = append(items, strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "))
		}
	}
	return items
}
