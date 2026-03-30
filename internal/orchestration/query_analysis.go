package orchestration

import "strings"

type qaQueryType string

const (
	qaQueryTypeCode      qaQueryType = "code"
	qaQueryTypeGuideline qaQueryType = "guideline"
	qaQueryTypeContext   qaQueryType = "context"
	qaQueryTypeSemantic  qaQueryType = "semantic"
	qaQueryTypeMixed     qaQueryType = "mixed"
)

type qaQueryAnalysis struct {
	PrimaryType         qaQueryType
	Confidence          float64
	Complexity          int
	Keywords            []string
	RequiredTools       []string
	MaxIterations       int
	NeedsDisambiguation bool
	UserExpertise       string
}

func analyzeQAQuery(query string) qaQueryAnalysis {
	query = strings.ToLower(strings.TrimSpace(query))
	tokens := tokenizeQAQuery(query)
	entities := extractQAEntities(tokens)
	intent := classifyQAIntent(query)
	complexity := assessQAComplexity(query, entities, intent)
	expertise := detectQAUserExpertise(query)

	if intent == qaQueryTypeGuideline && complexity < 4 {
		complexity = 4
	}

	return qaQueryAnalysis{
		PrimaryType:         intent,
		Confidence:          calculateQAConfidence(entities, intent),
		Complexity:          complexity,
		Keywords:            tokens,
		RequiredTools:       planQAToolUsage(intent, complexity),
		MaxIterations:       calculateQAMaxIterations(complexity),
		NeedsDisambiguation: needsQADisambiguation(query, entities),
		UserExpertise:       expertise,
	}
}

func tokenizeQAQuery(query string) []string {
	fields := strings.Fields(query)
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if !isQAStopWord(field) {
			tokens = append(tokens, field)
		}
	}
	return tokens
}

func extractQAEntities(tokens []string) []string {
	entities := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if isQACodeEntity(token) {
			entities = append(entities, token)
		}
	}
	return entities
}

func classifyQAIntent(query string) qaQueryType {
	switch {
	case strings.Contains(query, "function"),
		strings.Contains(query, "method"),
		strings.Contains(query, "class"),
		strings.Contains(query, "implementation"),
		strings.Contains(query, "code"):
		return qaQueryTypeCode
	case strings.Contains(query, "best practice"),
		strings.Contains(query, "pattern"),
		strings.Contains(query, "guideline"),
		strings.Contains(query, "convention"):
		return qaQueryTypeGuideline
	case strings.Contains(query, "context"),
		strings.Contains(query, "related"),
		strings.Contains(query, "dependency"),
		strings.Contains(query, "usage"):
		return qaQueryTypeContext
	case strings.Contains(query, "meaning"),
		strings.Contains(query, "purpose"),
		strings.Contains(query, "understand"),
		strings.Contains(query, "explain"):
		return qaQueryTypeSemantic
	default:
		return qaQueryTypeMixed
	}
}

func assessQAComplexity(query string, entities []string, intent qaQueryType) int {
	complexity := 2
	if len(strings.Fields(query)) > 10 {
		complexity++
	}
	if len(entities) > 3 {
		complexity++
	}
	if intent == qaQueryTypeSemantic || intent == qaQueryTypeGuideline {
		complexity++
	}
	if complexity > 5 {
		return 5
	}
	return complexity
}

func detectQAUserExpertise(query string) string {
	switch {
	case strings.Contains(query, "basic"),
		strings.Contains(query, "simple"),
		strings.Contains(query, "explain"),
		strings.Contains(query, "what is"):
		return "beginner"
	case strings.Contains(query, "advanced"),
		strings.Contains(query, "optimize"),
		strings.Contains(query, "performance"),
		strings.Contains(query, "architecture"):
		return "expert"
	default:
		return "intermediate"
	}
}

func planQAToolUsage(intent qaQueryType, complexity int) []string {
	switch intent {
	case qaQueryTypeCode:
		if complexity >= 4 {
			return []string{"search_content", "read_file", "search_files"}
		}
		return []string{"search_content", "read_file"}
	case qaQueryTypeGuideline:
		return []string{"search_files", "read_file", "semantic_search"}
	case qaQueryTypeContext:
		return []string{"search_content", "read_file", "search_files"}
	case qaQueryTypeSemantic:
		return []string{"search_files", "read_file", "semantic_search"}
	default:
		if complexity <= 2 {
			return []string{"search_files", "read_file"}
		}
		return []string{"search_files", "search_content", "read_file"}
	}
}

func calculateQAMaxIterations(complexity int) int {
	switch complexity {
	case 1:
		return 2
	case 2:
		return 3
	case 3:
		return 4
	case 4:
		return 6
	case 5:
		return 8
	default:
		return 3
	}
}

func needsQADisambiguation(query string, entities []string) bool {
	if len(entities) == 0 && len(strings.Fields(query)) < 3 {
		return true
	}
	for _, term := range []string{"this", "that", "it", "something", "stuff"} {
		if strings.Contains(query, term) {
			return true
		}
	}
	return false
}

func calculateQAConfidence(entities []string, intent qaQueryType) float64 {
	confidence := 0.5 + float64(len(entities))*0.1
	if intent != qaQueryTypeMixed {
		confidence += 0.2
	}
	if confidence > 0.95 {
		return 0.95
	}
	return confidence
}

func isQAStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "from": true,
	}
	return stopWords[word]
}

func isQACodeEntity(token string) bool {
	for _, pattern := range []string{
		"func", "function", "class", "struct", "interface",
		"method", "variable", "const", "type", "package",
		"import", "error", "test", "main", "init",
	} {
		if strings.Contains(token, pattern) {
			return true
		}
	}
	return false
}

func nativeQAGuidance(analysis qaQueryAnalysis) []string {
	guidance := make([]string, 0, 4)

	switch analysis.PrimaryType {
	case qaQueryTypeCode:
		guidance = append(guidance, "Start with concrete identifier searches using search_content, then verify the implementation with read_file.")
	case qaQueryTypeGuideline:
		guidance = append(guidance, "Inspect README, docs, examples, tests, and guideline-like files before summarizing best practices.")
	case qaQueryTypeContext:
		guidance = append(guidance, "Trace dependencies, call sites, and cross-package interactions across multiple files before answering.")
	case qaQueryTypeSemantic:
		guidance = append(guidance, "Start with README or package-level docs, then confirm the explanation in source files.")
	default:
		guidance = append(guidance, "Begin with broad file discovery, then narrow to targeted content searches and file reads.")
	}

	if analysis.Complexity >= 4 {
		guidance = append(guidance, "Expect a multi-file answer and synthesize relationships between packages instead of stopping at one file.")
	}
	if analysis.NeedsDisambiguation {
		guidance = append(guidance, "If repository evidence remains ambiguous, state your assumptions explicitly instead of overclaiming.")
	}

	switch analysis.UserExpertise {
	case "beginner":
		guidance = append(guidance, "Explain key terms briefly and prefer a more accessible final answer.")
	case "expert":
		guidance = append(guidance, "Keep the final answer concise and bias toward architecture and implementation detail.")
	}

	return guidance
}
