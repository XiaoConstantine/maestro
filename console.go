package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/google/go-github/v68/github"
	"github.com/logrusorgru/aurora"
	"github.com/mattn/go-isatty"
)

// Console handles user-facing output separate from logging.
type Console struct {
	w      io.Writer
	logger *logging.Logger // For debug logs
	color  bool
}

func NewConsole(w io.Writer, logger *logging.Logger) *Console {
	// Detect if terminal supports color
	color := true
	if f, ok := w.(*os.File); ok {
		color = isatty.IsTerminal(f.Fd())
	}

	return &Console{
		w:      w,
		logger: logger,
		color:  color,
	}
}

func (c *Console) printFields(fields map[string]interface{}) {
	// Get maximum key length for alignment
	maxKeyLen := 0
	for k := range fields {
		if len(k) > maxKeyLen {
			maxKeyLen = len(k)
		}
	}

	// Sort keys for consistent output
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Print each field with proper alignment
	for _, k := range keys {
		// Create the label with proper padding
		label := fmt.Sprintf("%-*s", maxKeyLen, k)

		if c.color {
			// Color the label but not the value
			label = aurora.Blue(label).String()
		}

		// Print the formatted line
		c.printf("%s : %v\n", label, fields[k])
	}
}

func (c *Console) StartReview(pr *github.PullRequest) {
	// Safely handle GitHub API pointer types
	title := ""
	if pr.Title != nil {
		title = *pr.Title
	}

	owner := ""
	if pr.Base != nil && pr.Base.Repo != nil && pr.Base.Repo.Owner != nil && pr.Base.Repo.Owner.Login != nil {
		owner = *pr.Base.Repo.Owner.Login
	}

	repoName := ""
	if pr.Base != nil && pr.Base.Repo != nil && pr.Base.Repo.Name != nil {
		repoName = *pr.Base.Repo.Name
	}

	author := ""
	if pr.User != nil && pr.User.Login != nil {
		author = *pr.User.Login
	}

	changedFiles := 0
	if pr.ChangedFiles != nil {
		changedFiles = *pr.ChangedFiles
	}

	c.printHeader(fmt.Sprintf("Reviewing PR #%d: %s", pr.Number, title))

	// Now we use interface{} for the map to handle different value types
	c.printFields(map[string]interface{}{
		"Repository": fmt.Sprintf("%s/%s", owner, repoName),
		"Author":     author,
		"Files":      fmt.Sprintf("%d files changed", changedFiles),
	})
	c.println()
}
func (c *Console) ReviewingFile(file string, current, total int) {
	prefix := fmt.Sprintf("[%d/%d]", current, total)
	if c.color {
		prefix = aurora.Blue(prefix).String()
	}
	c.printf("%s Analyzing %s...\n", prefix, file)
}

func (c *Console) NoIssuesFound(file string) {
	c.printf("✨ No issues found in %s\n", file)
}

func (c *Console) ShowComments(comments []PRReviewComment) {
	if len(comments) == 0 {
		return
	}

	c.println("\nReview Comments:")
	for _, comment := range comments {
		if c.color {
			c.printf("%s %s:%d\n",
				c.severityIcon(comment.Severity),
				comment.FilePath,
				comment.LineNumber,
			)
		} else {
			c.printf("[%s] %s:%d\n",
				comment.Severity,
				comment.FilePath,
				comment.LineNumber,
			)
		}
		c.println(indent(comment.Content, 2))

		if comment.Suggestion != "" {
			if c.color {
				c.println(aurora.Green("  Suggestion:").String())
			} else {
				c.println("  Suggestion:")
			}
			c.println(indent(comment.Suggestion, 4))
		}
		c.println()
	}
}

func (c *Console) ShowSummary(comments []PRReviewComment) {
	c.printHeader("Review Summary")

	// Group by severity
	bySeverity := make(map[string]int)
	for _, comment := range comments {
		bySeverity[comment.Severity]++
	}

	if len(bySeverity) == 0 {
		c.println("✨ No issues found")
		return
	}

	// Print counts by severity
	for _, severity := range []string{"critical", "warning", "suggestion"} {
		if count := bySeverity[severity]; count > 0 {
			icon := c.severityIcon(severity)
			c.printf("%s %s: %d\n", icon, severity, count)
		}
	}
	c.ShowComments(comments)

}

func (c *Console) FileError(filepath string, err error) {
	if c.color {
		c.printf("%s Error processing %s: %v\n",
			aurora.Red("✖").String(),
			aurora.Bold(filepath).String(),
			err)
	} else {
		c.printf("✖ Error processing %s: %v\n", filepath, err)
	}
}

func (c *Console) PostingComments(count int) {
	if count > 0 {
		c.printf("\nPosting %d comments to GitHub...\n", count)
	}
}

func (c *Console) ReviewComplete() {
	if c.color {
		c.println(aurora.Green("\n✓ Review completed successfully").String())
	} else {
		c.println("\n✓ Review completed successfully")
	}
}

// Helper methods

func (c *Console) severityIcon(severity string) string {
	if !c.color {
		return "[" + severity + "]"
	}

	switch severity {
	case "critical":
		return aurora.Red("●").String()
	case "warning":
		return aurora.Yellow("●").String()
	case "suggestion":
		return aurora.Blue("●").String()
	default:
		return "●"
	}
}

func (c *Console) printHeader(text string) {
	if c.color {
		text = aurora.Bold(text).String()
	}
	c.printf("\n=== %s ===\n", text)
}

func (c *Console) println(a ...interface{}) {
	fmt.Fprintln(c.w, a...)
}

func (c *Console) printf(format string, a ...interface{}) {
	fmt.Fprintf(c.w, format, a...)
}

// indent adds spaces to the start of each line.
func indent(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}
