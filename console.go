package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/briandowns/spinner"
	"github.com/google/go-github/v68/github"
	"github.com/logrusorgru/aurora"
	"github.com/mattn/go-isatty"
)

// Console handles user-facing output separate from logging.
type Console struct {
	w       io.Writer
	logger  *logging.Logger // For debug logs
	spinner *spinner.Spinner
	color   bool

	mu sync.Mutex
}

type SpinnerConfig struct {
	Color   string
	Speed   time.Duration
	CharSet []string
	Prefix  string
	Suffix  string
}

// PromptOptions configures how the confirmation prompt behaves.
type PromptOptions struct {
	Message  string        // The question to ask
	Default  bool          // Default answer if user just hits enter
	HelpText string        // Optional help text shown below prompt
	Timeout  time.Duration // Optional timeout for response
	Color    bool          // Whether to use colors in prompt
}

func DefaultPromptOptions() PromptOptions {
	return PromptOptions{
		Message:  "Continue?",
		Default:  false,
		HelpText: "",
		Timeout:  30 * time.Second,
		Color:    true,
	}
}

func (c *Console) Confirm(opts PromptOptions) (bool, error) {
	// Create the survey prompt with styling
	prompt := &survey.Confirm{
		Message: opts.Message,
		Default: opts.Default,
		Help:    opts.HelpText,
	}

	// Configure survey settings
	surveyOpts := []survey.AskOpt{
		survey.WithIcons(func(icons *survey.IconSet) {
			if c.color {
				icons.Question.Text = "?"
				icons.Question.Format = "cyan+b"
				icons.Help.Format = "blue"
			}
		}),
	}

	// Handle piped input (non-interactive mode)
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return opts.Default, nil
	}

	var response bool
	err := survey.AskOne(prompt, &response, surveyOpts...)

	// Special handling for interrupt (Ctrl+C)
	if err == terminal.InterruptErr {
		if c.color {
			c.println(aurora.Red("\n✖ Operation cancelled").String())
		} else {
			c.println("\n✖ Operation cancelled")
		}
		return false, nil
	}

	return response, err
}

func (c *Console) ConfirmReviewPost(commentCount int) (bool, error) {
	// Format message based on comment count
	message := fmt.Sprintf("Post %d review comment", commentCount)
	if commentCount != 1 {
		message += "s"
	}
	message += " to GitHub?"

	// Create options with helpful context
	opts := DefaultPromptOptions()
	opts.Message = message
	opts.HelpText = "This will create a pull request review with the comments shown above"
	opts.Color = c.color

	return c.Confirm(opts)
}

func DefaultSpinnerConfig() SpinnerConfig {
	return SpinnerConfig{
		Color:   "cyan",
		Speed:   100 * time.Millisecond,
		CharSet: spinner.CharSets[14], // Using the character set from your example
		Prefix:  "Processing ",
		Suffix:  "",
	}
}

func NewConsole(w io.Writer, logger *logging.Logger, cfg *SpinnerConfig) *Console {
	if cfg == nil {
		defaultCfg := DefaultSpinnerConfig()
		cfg = &defaultCfg
	}

	s := spinner.New(cfg.CharSet, cfg.Speed)
	s.Prefix = cfg.Prefix
	s.Suffix = cfg.Suffix

	if err := s.Color(cfg.Color); err != nil {
		logger.Warn(context.Background(), "Failed to set spinner color: %v", err)
	}
	// Detect if terminal supports color
	color := true
	if f, ok := w.(*os.File); ok {
		color = isatty.IsTerminal(f.Fd())
	}

	return &Console{
		w:       w,
		logger:  logger,
		color:   color,
		spinner: s,
	}
}

func (c *Console) StartSpinner(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.spinner.Suffix = fmt.Sprintf(" %s", message)
	c.spinner.Start()
}

// StopSpinner stops the current spinner.
func (c *Console) StopSpinner() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.spinner.Active() {
		c.spinner.Stop()
	}
}

// Helper method for handling LLM operations with spinner.
func (c *Console) WithSpinner(ctx context.Context, message string, fn func() error) error {
	c.StartSpinner(message)
	defer c.StopSpinner()

	// Create a channel for the result
	errCh := make(chan error, 1)

	// Run the operation in a goroutine
	go func() {
		errCh <- fn()
	}()

	// Wait for either completion or context cancellation
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
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

func (c *Console) ShowReviewMetrics(metrics *BusinessMetrics, comments []PRReviewComment) {
	c.printHeader("Review Metrics Summary")

	// First show immediate review statistics
	c.println("\nImmediate Review Impact:")

	// Group comments by category for analysis
	categoryCounts := make(map[string]int)
	severityCounts := make(map[string]int)
	for _, comment := range comments {
		categoryCounts[comment.Category]++
		severityCounts[comment.Severity]++
	}

	// Show total comments with breakdown
	if c.color {
		c.printf("Total Comments: %s\n", aurora.Blue(len(comments)).Bold())
	} else {
		c.printf("Total Comments: %d\n", len(comments))
	}

	// Show category distribution
	if len(categoryCounts) > 0 {
		c.println("\nComments by Category:")
		categories := make([]string, 0, len(categoryCounts))
		for category := range categoryCounts {
			categories = append(categories, category)
		}
		sort.Strings(categories)

		for _, category := range categories {
			count := categoryCounts[category]
			percentage := float64(count) / float64(len(comments)) * 100

			if c.color {
				c.printf("  %-25s %s (%0.1f%%)\n",
					aurora.Blue(category),
					aurora.White(count).Bold(),
					percentage)
			} else {
				c.printf("  %-25s %d (%0.1f%%)\n", category, count, percentage)
			}

			// Show category-specific metrics
			if stats := metrics.GetCategoryMetrics(category); stats.TotalComments > 0 {
				outdatedRate := stats.GetOutdatedRate() * 100
				c.printf("    • Historical Outdated Rate: %0.1f%%\n", outdatedRate)
			}
		}
	}

	// Show severity distribution
	if len(severityCounts) > 0 {
		c.println("\nComments by Severity:")
		severities := []string{"critical", "warning", "suggestion"}
		for _, severity := range severities {
			if count := severityCounts[severity]; count > 0 {
				percentage := float64(count) / float64(len(comments)) * 100
				icon := c.severityIcon(severity)

				if c.color {
					c.printf("  %s %-12s %s (%0.1f%%)\n",
						icon,
						severity,
						aurora.White(count).Bold(),
						percentage)
				} else {
					c.printf("  %s %-12s %d (%0.1f%%)\n",
						icon,
						severity,
						count,
						percentage)
				}
			}
		}
	}

	// Show historical impact metrics if available
	c.println("\nHistorical Impact Metrics:")
	c.printFields(map[string]interface{}{
		"Overall Outdated Rate": fmt.Sprintf("%0.1f%%", metrics.GetOverallOutdatedRate()*100),
		"Weekly Active Users":   metrics.GetWeeklyActiveUsers(),
		"Review Response Rate":  fmt.Sprintf("%0.1f%%", metrics.GetReviewResponseRate()*100),
	})

	// Add helpful context about the metrics
	c.println("\nMetric Explanations:")
	c.println("• Outdated Rate measures how often flagged code is modified, indicating review effectiveness")
	c.println("• Review Response Rate shows how frequently developers engage with review comments")
	c.println("• Historical metrics help track long-term impact of automated reviews")
}

// indent adds spaces to the start of each line.
func indent(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}
