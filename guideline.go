package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

type CodeExample struct {
	Good        string
	Bad         string
	Explanation string
}

type GuidelineContent struct {
	ID       string            // Unique identifier for the guideline
	Text     string            // The actual guideline text
	Category string            // e.g., "Error Handling", "Pointers", etc.
	Examples []CodeExample     // Good and bad examples
	Language string            // Programming language this applies to
	Metadata map[string]string // Additional metadata
}

// GuidelineFetcher handles retrieving guidelines from external sources.
type GuidelineFetcher struct {
	client *http.Client
	logger *logging.Logger
}

func NewGuidelineFetcher(logger *logging.Logger) *GuidelineFetcher {
	return &GuidelineFetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// FetchGuidelines retrieves guidelines from various sources and processes them.
func (f *GuidelineFetcher) FetchGuidelines(ctx context.Context) ([]GuidelineContent, error) {
	// Define our sources for Go guidelines
	sources := []struct {
		URL      string
		Parser   func([]byte) ([]GuidelineContent, error)
		Language string
	}{
		{
			URL:      "https://raw.githubusercontent.com/uber-go/guide/master/style.md",
			Parser:   parseMarkdownGuidelines,
			Language: "Go",
		},
	}

	var allGuidelines []GuidelineContent
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Create an error channel to collect any errors during fetching
	errorChan := make(chan error, len(sources))

	for _, source := range sources {
		wg.Add(1)
		go func(src struct {
			URL      string
			Parser   func([]byte) ([]GuidelineContent, error)
			Language string
		}) {
			defer wg.Done()

			// Fetch the content
			content, err := f.fetchContent(ctx, src.URL)
			if err != nil {
				errorChan <- fmt.Errorf("failed to fetch from %s: %w", src.URL, err)
				return
			}

			// Parse the guidelines
			guidelines, err := src.Parser(content)
			if err != nil {
				errorChan <- fmt.Errorf("failed to parse content from %s: %w", src.URL, err)
				return
			}

			// Add language information
			for i := range guidelines {
				guidelines[i].Language = src.Language
			}

			// Add to our collection
			mu.Lock()
			allGuidelines = append(allGuidelines, guidelines...)
			mu.Unlock()
		}(source)
	}

	// Wait for all fetches to complete
	wg.Wait()
	close(errorChan)

	// Check for any errors
	var errors []error
	for err := range errorChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return allGuidelines, fmt.Errorf("encountered errors while fetching guidelines: %v", errors)
	}

	return allGuidelines, nil
}

// fetchContent retrieves content from a given URL with proper error handling.
func (f *GuidelineFetcher) fetchContent(ctx context.Context, url string) ([]byte, error) {
	// Create a new request with context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add appropriate headers
	req.Header.Set("Accept", "text/plain, text/markdown, text/html")
	req.Header.Set("User-Agent", "Guideline-Fetcher/1.0")

	// Perform the request
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch content: %w", err)
	}

	f.logger.Debug(ctx, "Received response status: %s", resp.Status)
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read the response body
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	f.logger.Debug(ctx, "Successfully fetched %d bytes of content", len(content))

	return content, nil
}

func (f *GuidelineFetcher) ConvertGuidelineToRules(ctx context.Context,
	guideline GuidelineContent) ([]ReviewRule, error) {

	rules := make([]ReviewRule, 0)

	// Convert each guideline example into a review rule
	ruleID := generateRuleID(guideline.Category)

	var example CodeExample
	if len(guideline.Examples) > 0 {
		example = guideline.Examples[0]
	}

	rule := ReviewRule{
		ID:          ruleID,
		Dimension:   mapGuidlineDimension(guideline.Category),
		Category:    guideline.Category,
		Name:        guideline.ID,
		Description: guideline.Text,
		Examples:    example,
		Metadata: RuleMetadata{
			Category:    guideline.Category,
			Impact:      determineImpact(guideline),
			AutoFixable: isAutoFixable(guideline),
		},
	}

	rules = append(rules, rule)
	return rules, nil
}

func parseMarkdownGuidelines(content []byte) ([]GuidelineContent, error) {
	var guidelines []GuidelineContent
	logger := logging.GetLogger()

	sections := bytes.Split(content, []byte("## "))
	logger.Debug(context.Background(), "Split content into %d sections", len(sections))

	for _, section := range sections {
		if len(bytes.TrimSpace(section)) == 0 {
			continue
		}

		lines := bytes.Split(section, []byte("\n"))
		if len(lines) == 0 {
			continue
		}

		sectionTitle := string(bytes.TrimSpace(lines[0]))
		logger.Debug(context.Background(), "Processing section: %s", sectionTitle)

		var examples []CodeExample
		var currentExample CodeExample
		var parsingState string // Tracks what we're currently parsing: "bad", "good", or ""
		var explanation strings.Builder

		for i := 1; i < len(lines); i++ {
			line := string(bytes.TrimSpace(lines[i]))

			switch {
			case strings.HasPrefix(line, "Bad:"):
				// Start a new example when we see "Bad:"
				parsingState = "bad"
				currentExample = CodeExample{} // Reset the current example
				var badCode strings.Builder
				i++ // Skip the "Bad:" line
				for ; i < len(lines); i++ {
					line = string(lines[i])
					if strings.HasPrefix(line, "Good:") {
						i-- // Back up so we catch "Good:" in next iteration
						break
					}
					badCode.WriteString(line + "\n")
				}
				currentExample.Bad = strings.TrimSpace(badCode.String())

			case strings.HasPrefix(line, "Good:"):
				// Only process "Good:" if we were previously parsing a bad example
				if parsingState == "bad" {
					var goodCode strings.Builder
					i++ // Skip the "Good:" line
					for ; i < len(lines); i++ {
						line = string(lines[i])
						if strings.HasPrefix(line, "```") && goodCode.Len() > 0 {
							break
						}
						goodCode.WriteString(line + "\n")
					}
					currentExample.Good = strings.TrimSpace(goodCode.String())

					// If we have both bad and good examples, add to our collection
					if currentExample.Bad != "" && currentExample.Good != "" {
						examples = append(examples, currentExample)
						logger.Debug(context.Background(), "Added example pair to section %s", sectionTitle)
					}
				}
				parsingState = "" // Reset state after processing a complete example

			default:
				// If we're not parsing an example, collect explanation text
				if parsingState == "" && len(line) > 0 {
					explanation.WriteString(line + "\n")
				}
			}
		}

		// Create a guideline if we have either examples or explanation text
		if len(examples) > 0 || explanation.Len() > 0 {
			guideline := GuidelineContent{
				ID:       generateID(sectionTitle),
				Text:     explanation.String(),
				Category: sectionTitle,
				Examples: examples,
				Language: "Go",
			}
			guidelines = append(guidelines, guideline)
			logger.Debug(context.Background(), "Added guideline: %s with %d examples",
				guideline.ID, len(guideline.Examples))
		}
	}
	return guidelines, nil
}

// generateID creates a unique identifier for a guideline.
func generateID(title string) string {
	// Convert to lowercase and replace spaces with hyphens
	id := strings.ToLower(title)
	id = strings.ReplaceAll(id, " ", "-")

	// Remove any special characters
	id = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(id, "")

	// Add a timestamp to ensure uniqueness
	return fmt.Sprintf("%s-%d", id, time.Now().Unix())
}

// generateRuleID creates a unique identifier for a review rule based on its category
// For example: "ERROR_HANDLING_001" or "SECURITY_VULN_001".
func generateRuleID(category string) string {
	// Create a map of category prefixes for consistent naming
	categoryPrefixes := map[string]string{
		"error handling":         "ERR",
		"security vulnerability": "SEC",
		"code style":             "STYLE",
		"performance":            "PERF",
		"documentation":          "DOC",
		"maintainability":        "MAINT",
	}

	// Normalize the category name
	normalizedCategory := strings.ToLower(strings.TrimSpace(category))

	// Get the prefix, defaulting to "RULE" if category isn't in our map
	prefix := categoryPrefixes[normalizedCategory]
	if prefix == "" {
		prefix = "RULE"
	}

	// Generate a unique number using timestamp to ensure uniqueness
	timestamp := time.Now().UnixNano()
	uniqueNumber := fmt.Sprintf("%03d", timestamp%1000)

	return fmt.Sprintf("%s_%s", prefix, uniqueNumber)
}

// mapGuidlineDimension maps a guideline category to one of our main review dimensions
// This helps organize rules into the high-level structure similar to BitsAI-CR.
func mapGuidlineDimension(category string) string {
	// Go-specific mapping of review categories to dimensions
	// This aligns with both Go's standard practices and common issues
	dimensionMap := map[string]string{
		// Code Defects cover Go-specific error handling and common mistakes
		"error handling":   "Code Defect", // e.g., unchecked errors
		"defer usage":      "Code Defect", // e.g., incorrect defer ordering
		"goroutine leak":   "Code Defect", // e.g., unbounded goroutines
		"channel usage":    "Code Defect", // e.g., channel deadlocks
		"context handling": "Code Defect", // e.g., missing context propagation

		// Security issues specific to Go applications
		"input validation":   "Security Vulnerability", // e.g., unsafe file paths
		"sql injection":      "Security Vulnerability", // e.g., raw SQL queries
		"template injection": "Security Vulnerability", // e.g., html/template misuse

		// Go's strong opinions about code organization and style
		"package organization": "Maintainability and Readability", // e.g., package naming
		"interface design":     "Maintainability and Readability", // e.g., interface size
		"type naming":          "Maintainability and Readability", // e.g., stuttering names
		"comment style":        "Maintainability and Readability", // e.g., godoc format

		// Performance concerns particular to Go
		"memory allocation": "Performance Issue", // e.g., unnecessary allocations
		"mutex usage":       "Performance Issue", // e.g., lock contention
		"slice operations":  "Performance Issue", // e.g., inefficient append
	}
	normalizedCategory := strings.ToLower(strings.TrimSpace(category))

	// Look up the dimension, defaulting to "Other" if not found
	dimension := dimensionMap[normalizedCategory]
	if dimension == "" {
		dimension = "Other"
	}

	return dimension
}

func determineImpact(guideline GuidelineContent) string {
	content := strings.ToLower(guideline.Text)

	// High-impact issues in Go codebases
	highImpactPatterns := []string{
		"race condition",     // Concurrent access issues
		"goroutine leak",     // Resource leaks
		"context deadline",   // Timing and cancellation
		"memory leak",        // Resource management
		"deadlock",           // Concurrency issues
		"panic",              // Runtime crashes
		"nil pointer",        // Common runtime error
		"unbuffered channel", // Potential deadlocks
	}

	// Medium-impact issues specific to Go
	mediumImpactPatterns := []string{
		"defer",               // Resource cleanup
		"error wrapping",      // Error chain integrity
		"interface pollution", // API design
		"package coupling",    // Code organization
		"slice capacity",      // Memory usage
		"method receiver",     // Type design
		"mutex lock",          // Concurrency control
	}

	// Check for high impact patterns first
	for _, pattern := range highImpactPatterns {
		if strings.Contains(content, pattern) {
			return "high"
		}
	}

	// Then check medium impact patterns
	for _, pattern := range mediumImpactPatterns {
		if strings.Contains(content, pattern) {
			return "medium"
		}
	}

	// Default to low impact for style and documentation issues
	return "low"
}

// isAutoFixable determines if a rule violation can be automatically fixed
// based on the guideline content and examples.
func isAutoFixable(guideline GuidelineContent) bool {
	// Simple patterns that can usually be auto-fixed
	autoFixablePatterns := []string{
		"naming convention",
		"formatting",
		"whitespace",
		"import order",
		"line length",
	}

	content := strings.ToLower(guideline.Text)

	// Check if the guideline matches any auto-fixable patterns
	for _, pattern := range autoFixablePatterns {
		if strings.Contains(content, pattern) {
			return true
		}
	}

	// If we have both good and bad examples, and they're simple transformations,
	// it might be auto-fixable
	if len(guideline.Examples) > 0 {
		example := guideline.Examples[0]
		if example.Good != "" && example.Bad != "" {
			// If the difference is small and systematic, it's likely auto-fixable
			if levenshteinDistance(example.Good, example.Bad) < 10 {
				return true
			}
		}
	}

	return false
}
