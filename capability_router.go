package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// CapabilityType represents different types of capabilities.
type CapabilityType int

const (
	ReviewCapability CapabilityType = iota
	CodeEditingCapability
	WebSearchCapability
	PlanningCapability
	FileManagementCapability
	GitOperationsCapability
	TestingCapability
	DebugCapability
)

// TaskIntent represents the inferred intent of a user request.
type TaskIntent struct {
	Type        CapabilityType
	Confidence  float64
	Description string
	Keywords    []string
}

// CapabilityRouter determines which tool/capability should handle a request.
type CapabilityRouter struct {
	cliManager *CLIToolManager
	logger     *logging.Logger
	console    ConsoleInterface
	patterns   map[CapabilityType][]string
}

// NewCapabilityRouter creates a new capability router.
func NewCapabilityRouter(cliManager *CLIToolManager, logger *logging.Logger, console ConsoleInterface) *CapabilityRouter {
	router := &CapabilityRouter{
		cliManager: cliManager,
		logger:     logger,
		console:    console,
		patterns:   make(map[CapabilityType][]string),
	}

	router.initializePatterns()
	return router
}

// initializePatterns sets up keyword patterns for different capabilities.
func (r *CapabilityRouter) initializePatterns() {
	r.patterns[ReviewCapability] = []string{
		"review", "analyze code", "check pr", "pull request", "code quality", "security check",
		"review pr", "code review", "find bugs", "lint", "quality",
	}

	r.patterns[CodeEditingCapability] = []string{
		"edit", "modify", "change", "update", "fix", "implement", "create file", "write code",
		"refactor", "add function", "delete", "replace", "insert", "generate code",
		"create class", "add method", "fix bug", "implement feature",
	}

	r.patterns[WebSearchCapability] = []string{
		"search", "find information", "look up", "research", "google", "web search",
		"documentation", "docs", "api reference", "tutorial", "example",
		"how to", "what is", "what's", "explain", "latest", "current", "tell me about",
		"information about", "learn about", "understand", "definition", "meaning",
		"who is", "when", "where", "why", "history of", "overview", "introduction",
	}

	r.patterns[PlanningCapability] = []string{
		"plan", "strategy", "approach", "architecture", "design", "roadmap",
		"steps", "breakdown", "organize", "structure", "outline", "workflow",
		"project plan", "implementation plan", "task breakdown",
	}

	r.patterns[FileManagementCapability] = []string{
		"create directory", "mkdir", "copy file", "move file", "rename", "delete file",
		"list files", "find file", "organize files", "file structure",
	}

	r.patterns[GitOperationsCapability] = []string{
		"git", "commit", "branch", "merge", "pull", "push", "clone", "checkout",
		"git status", "git log", "git diff", "create branch", "merge conflict",
	}

	r.patterns[TestingCapability] = []string{
		"test", "unit test", "integration test", "run tests", "write test", "test coverage",
		"testing", "spec", "assert", "mock", "fixture",
	}

	r.patterns[DebugCapability] = []string{
		"debug", "troubleshoot", "error", "exception", "stack trace", "logs",
		"investigate", "diagnose", "issue", "problem", "fix error",
	}
}

// AnalyzeIntent analyzes user input to determine the best capability to handle it.
func (r *CapabilityRouter) AnalyzeIntent(input string) []TaskIntent {
	input = strings.ToLower(input)
	var intents []TaskIntent

	for capType, patterns := range r.patterns {
		confidence := r.calculateConfidence(input, patterns)
		if confidence > 0.05 { // Lower threshold for inclusion
			intents = append(intents, TaskIntent{
				Type:        capType,
				Confidence:  confidence,
				Description: r.getCapabilityDescription(capType),
				Keywords:    r.getMatchingKeywords(input, patterns),
			})
		}
	}

	// Sort by confidence (highest first)
	for i := 0; i < len(intents)-1; i++ {
		for j := i + 1; j < len(intents); j++ {
			if intents[i].Confidence < intents[j].Confidence {
				intents[i], intents[j] = intents[j], intents[i]
			}
		}
	}

	return intents
}

// calculateConfidence calculates how well the input matches capability patterns.
func (r *CapabilityRouter) calculateConfidence(input string, patterns []string) float64 {
	if len(patterns) == 0 {
		return 0
	}

	matches := 0
	weightedScore := 0.0

	for _, pattern := range patterns {
		if strings.Contains(input, pattern) {
			matches++

			// Give higher weight to longer pattern matches
			patternWeight := float64(len(pattern)) / 10.0
			if patternWeight < 0.1 {
				patternWeight = 0.1
			}
			weightedScore += patternWeight

			// Boost for exact word matches
			if input == pattern {
				weightedScore += 0.5
			}
		}
	}

	if matches == 0 {
		return 0
	}

	// Normalize the score
	confidence := weightedScore / float64(len(patterns))

	// Give a minimum confidence boost for any matches
	if matches > 0 {
		confidence += 0.2
	}

	// Cap at 1.0
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// getMatchingKeywords returns keywords that matched in the input.
func (r *CapabilityRouter) getMatchingKeywords(input string, patterns []string) []string {
	var keywords []string
	for _, pattern := range patterns {
		if strings.Contains(input, pattern) {
			keywords = append(keywords, pattern)
		}
	}
	return keywords
}

// getCapabilityDescription returns a description for each capability type.
func (r *CapabilityRouter) getCapabilityDescription(capType CapabilityType) string {
	descriptions := map[CapabilityType]string{
		ReviewCapability:         "Code review and analysis",
		CodeEditingCapability:    "Code editing and file modifications",
		WebSearchCapability:      "Web search and information retrieval",
		PlanningCapability:       "Project planning and architecture design",
		FileManagementCapability: "File and directory operations",
		GitOperationsCapability:  "Git version control operations",
		TestingCapability:        "Testing and quality assurance",
		DebugCapability:          "Debugging and troubleshooting",
	}
	return descriptions[capType]
}

// RouteTask routes a task to the appropriate tool based on capability analysis.
func (r *CapabilityRouter) RouteTask(ctx context.Context, input string) error {
	intents := r.AnalyzeIntent(input)

	// If no specific intent detected or confidence is very low, use smart fallback
	if len(intents) == 0 || intents[0].Confidence < 0.3 {
		return r.handleSmartFallback(ctx, input)
	}

	topIntent := intents[0]

	r.logger.Info(ctx, "Routing task with intent: %s (confidence: %.2f)",
		topIntent.Description, topIntent.Confidence)

	switch topIntent.Type {
	case ReviewCapability:
		return r.handleReviewTask(ctx, input)
	case CodeEditingCapability:
		return r.handleCodeEditingTask(ctx, input)
	case WebSearchCapability:
		return r.handleWebSearchTask(ctx, input)
	case PlanningCapability:
		return r.handlePlanningTask(ctx, input)
	case FileManagementCapability:
		return r.handleFileManagementTask(ctx, input)
	case GitOperationsCapability:
		return r.handleGitOperationsTask(ctx, input)
	case TestingCapability:
		return r.handleTestingTask(ctx, input)
	case DebugCapability:
		return r.handleDebugTask(ctx, input)
	default:
		// Fallback to Claude Code for general tasks
		return r.delegateToClaudeCode(ctx, input)
	}
}

// handleReviewTask handles code review tasks using Maestro's native capabilities.
func (r *CapabilityRouter) handleReviewTask(ctx context.Context, input string) error {
	// Extract PR number if mentioned
	prRegex := regexp.MustCompile(`(?i)pr\s*#?(\d+)|pull request\s*#?(\d+)`)
	matches := prRegex.FindStringSubmatch(input)

	if len(matches) > 1 && matches[1] != "" {
		// PR number found, delegate to review system
		return fmt.Errorf("please use /review %s command for PR reviews", matches[1])
	}

	// For general code analysis, delegate to Claude Code
	return r.delegateToClaudeCode(ctx, input)
}

// handleCodeEditingTask delegates code editing to Claude Code.
func (r *CapabilityRouter) handleCodeEditingTask(ctx context.Context, input string) error {
	return r.delegateToClaudeCode(ctx, input)
}

// handleWebSearchTask delegates web search to Gemini CLI.
func (r *CapabilityRouter) handleWebSearchTask(ctx context.Context, input string) error {
	return r.delegateToGeminiCLI(ctx, input)
}

// handlePlanningTask delegates planning to Claude Code (better for structured thinking).
func (r *CapabilityRouter) handlePlanningTask(ctx context.Context, input string) error {
	return r.delegateToClaudeCode(ctx, input)
}

// handleFileManagementTask delegates to Claude Code.
func (r *CapabilityRouter) handleFileManagementTask(ctx context.Context, input string) error {
	return r.delegateToClaudeCode(ctx, input)
}

// handleGitOperationsTask delegates to Claude Code.
func (r *CapabilityRouter) handleGitOperationsTask(ctx context.Context, input string) error {
	return r.delegateToClaudeCode(ctx, input)
}

// handleTestingTask delegates to Claude Code.
func (r *CapabilityRouter) handleTestingTask(ctx context.Context, input string) error {
	return r.delegateToClaudeCode(ctx, input)
}

// handleDebugTask delegates to Claude Code.
func (r *CapabilityRouter) handleDebugTask(ctx context.Context, input string) error {
	return r.delegateToClaudeCode(ctx, input)
}

// delegateToClaudeCode delegates tasks to Claude Code CLI.
func (r *CapabilityRouter) delegateToClaudeCode(ctx context.Context, input string) error {
	if !r.cliManager.IsToolInstalled(ClaudeCode) {
		if err := r.cliManager.SetupTool(ctx, ClaudeCode); err != nil {
			return fmt.Errorf("failed to setup Claude Code: %w", err)
		}
	}

	// Use interactive mode with stdin input
	return r.cliManager.ExecuteToolInteractively(ctx, ClaudeCode, input)
}

// delegateToGeminiCLI delegates tasks to Gemini CLI.
func (r *CapabilityRouter) delegateToGeminiCLI(ctx context.Context, input string) error {
	if !r.cliManager.IsToolInstalled(GeminiCLI) {
		if err := r.cliManager.SetupTool(ctx, GeminiCLI); err != nil {
			return fmt.Errorf("failed to setup Gemini CLI: %w", err)
		}
	}

	// For Gemini CLI, pass the input as a prompt using -p flag
	args := []string{"-p", input, "--yolo"}
	return r.cliManager.ExecuteToolQuietly(ctx, GeminiCLI, args)
}

// ShowCapabilityAnalysis shows the capability analysis for debugging.
func (r *CapabilityRouter) ShowCapabilityAnalysis(input string) {
	intents := r.AnalyzeIntent(input)

	r.console.PrintHeader("Capability Analysis")
	r.console.Printf("Input: %s\n\n", input)

	for i, intent := range intents {
		r.console.Printf("%d. %s (%.2f confidence)\n",
			i+1, intent.Description, intent.Confidence)
		if len(intent.Keywords) > 0 {
			r.console.Printf("   Keywords: %s\n", strings.Join(intent.Keywords, ", "))
		}
	}

	if len(intents) > 0 {
		r.console.Printf("\n→ Would route to: %s\n", intents[0].Description)
	} else {
		r.console.Printf("\n→ Would use smart fallback\n")
	}
}

// handleSmartFallback handles requests that don't match specific patterns.
func (r *CapabilityRouter) handleSmartFallback(ctx context.Context, input string) error {
	// Simple heuristics for fallback routing
	inputLower := strings.ToLower(input)

	// Questions typically go to search/Gemini
	if strings.Contains(inputLower, "what") || strings.Contains(inputLower, "how") ||
		strings.Contains(inputLower, "why") || strings.Contains(inputLower, "?") {
		return r.delegateToGeminiCLI(ctx, input)
	}

	// Code-related terms go to Claude Code
	if strings.Contains(inputLower, "code") || strings.Contains(inputLower, "file") ||
		strings.Contains(inputLower, "function") || strings.Contains(inputLower, "variable") {
		return r.delegateToClaudeCode(ctx, input)
	}

	// Default to Gemini for general queries
	return r.delegateToGeminiCLI(ctx, input)
}
