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
