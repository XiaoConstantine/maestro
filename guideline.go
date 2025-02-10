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

	return content, nil
}

// Markdown parser specifically for Go style guides.
func parseMarkdownGuidelines(content []byte) ([]GuidelineContent, error) {
	var guidelines []GuidelineContent

	// Split content into sections
	sections := bytes.Split(content, []byte("##"))

	for _, section := range sections {
		// Parse each section to extract guidelines
		guidelines = append(guidelines, extractGuidelinesFromSection(section)...)
	}

	return guidelines, nil
}

func extractGuidelinesFromSection(section []byte) []GuidelineContent {
	var guidelines []GuidelineContent

	// Use regular expressions to identify guideline patterns
	// For example, looking for patterns like:
	// ### Guideline Name
	// Description
	// Bad:
	// ```go
	// code
	// ```
	// Good:
	// ```go
	// code
	// ```

	re := regexp.MustCompile(`(?ms)### (.+?)\n(.+?)\nBad:\n` +
		"```go\n(.+?)\n```\nGood:\n```go\n(.+?)\n```")

	matches := re.FindAllSubmatch(section, -1)

	for _, match := range matches {
		if len(match) >= 5 {
			guideline := GuidelineContent{
				ID:   generateID(string(match[1])),
				Text: strings.TrimSpace(string(match[2])),
				Examples: []CodeExample{
					{
						Bad:         strings.TrimSpace(string(match[3])),
						Good:        strings.TrimSpace(string(match[4])),
						Explanation: extractExplanation(section, match[1]),
					},
				},
				Category: extractCategory(section),
			}
			guidelines = append(guidelines, guideline)
		}
	}

	return guidelines
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

// extractExplanation gets the explanation text for a guideline.
func extractExplanation(section []byte, title []byte) string {
	// Look for explanation text between the title and examples
	re := regexp.MustCompile(fmt.Sprintf(`(?ms)%s\n(.+?)\n(Bad|Good):`,
		regexp.QuoteMeta(string(title))))

	matches := re.FindSubmatch(section)
	if len(matches) >= 2 {
		return strings.TrimSpace(string(matches[1]))
	}
	return ""
}

// extractCategory determines the category of a guideline section.
func extractCategory(section []byte) string {
	// Look for category indicators in the section header
	categoryPatterns := map[string]string{
		"Error":      "Error Handling",
		"Interface":  "Interface Design",
		"Concurrent": "Concurrency",
		"Package":    "Package Design",
		"Testing":    "Testing",
	}

	sectionText := string(section)
	for pattern, category := range categoryPatterns {
		if strings.Contains(sectionText, pattern) {
			return category
		}
	}

	return "General"
}

// formatGuidelineContent creates a rich text representation of a guideline.
func formatGuidelineContent(guideline GuidelineContent) string {
	var sb strings.Builder

	// Write the main content
	sb.WriteString(fmt.Sprintf("# %s\n\n", guideline.ID))
	sb.WriteString(fmt.Sprintf("Category: %s\n\n", guideline.Category))
	sb.WriteString(guideline.Text)
	sb.WriteString("\n\n")

	// Add examples if they exist
	for _, example := range guideline.Examples {
		if example.Bad != "" {
			sb.WriteString("Bad Example:\n```go\n")
			sb.WriteString(example.Bad)
			sb.WriteString("\n```\n\n")
		}

		if example.Good != "" {
			sb.WriteString("Good Example:\n```go\n")
			sb.WriteString(example.Good)
			sb.WriteString("\n```\n\n")
		}

		if example.Explanation != "" {
			sb.WriteString("Explanation:\n")
			sb.WriteString(example.Explanation)
			sb.WriteString("\n\n")
		}
	}

	return sb.String()
}
