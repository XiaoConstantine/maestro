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

type ConsoleInterface interface {
	StartSpinner(message string)
	StopSpinner()
	WithSpinner(ctx context.Context, message string, fn func() error) error
	ShowComments(comments []PRReviewComment, metric MetricsCollector)
	ShowCommentsInteractive(comments []PRReviewComment, onPost func([]PRReviewComment) error) error
	ShowSummary(comments []PRReviewComment, metric MetricsCollector)
	StartReview(pr *github.PullRequest)
	ReviewingFile(file string, current, total int)
	ConfirmReviewPost(commentCount int) (bool, error)
	ReviewComplete()
	UpdateSpinnerText(text string)
	ShowReviewMetrics(metrics MetricsCollector, comments []PRReviewComment)
	CollectAllFeedback(comments []PRReviewComment, metric MetricsCollector) error
	Confirm(opts PromptOptions) (bool, error)
	FileError(filepath string, err error)
	Printf(format string, a ...interface{})
	Println(a ...interface{})
	PrintHeader(text string)
	NoIssuesFound(file string, chunkNumber, totalChunks int)
	SeverityIcon(severity string) string
	Color() bool
	Spinner() *spinner.Spinner
	IsInteractive() bool
}

// Console handles user-facing output separate from logging.
type Console struct {
	w             io.Writer
	logger        *logging.Logger // For debug logs
	spinner       *spinner.Spinner
	color         bool
	isInteractive bool // Cached terminal interactivity state

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

func DefaultSpinnerConfig() SpinnerConfig {
	return SpinnerConfig{
		Color:   "cyan",
		Speed:   100 * time.Millisecond,
		CharSet: spinner.CharSets[14], // Using the character set from your example
		Prefix:  "Processing ",
		Suffix:  "",
	}
}

func NewConsole(w io.Writer, logger *logging.Logger, cfg *SpinnerConfig) ConsoleInterface {
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

	// Detect if terminal is interactive (cache at creation time before go-prompt modifies terminal)
	// Check both stdout and stdin for more robust detection
	isInteractive := false
	if f, ok := w.(*os.File); ok {
		isInteractive = isatty.IsTerminal(f.Fd())
	}
	// Fallback: also check stdin (more reliable when other libs modify stdout)
	if !isInteractive {
		isInteractive = isatty.IsTerminal(os.Stdin.Fd())
	}

	return &Console{
		w:             w,
		logger:        logger,
		color:         color,
		spinner:       s,
		isInteractive: isInteractive,
	}
}

func (c *Console) Spinner() *spinner.Spinner {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.spinner

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

func (c *Console) Color() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.color
}

func (c *Console) Confirm(opts PromptOptions) (bool, error) {
	// Create the survey prompt with styling
	prompt := &survey.Confirm{
		Message: opts.Message,
		Default: opts.Default,
		Help:    opts.HelpText,
	}

	// Configure survey settings with better terminal compatibility
	surveyOpts := []survey.AskOpt{
		survey.WithIcons(func(icons *survey.IconSet) {
			if c.color {
				icons.Question.Text = "?"
				icons.Question.Format = "cyan+b"
				icons.Help.Format = "blue"
			} else {
				// Disable all formatting for better compatibility
				icons.Question.Text = "?"
				icons.Question.Format = ""
				icons.Help.Format = ""
			}
		}),
		// Disable terminal features that might cause escape sequence issues
		survey.WithRemoveSelectAll(),
		survey.WithRemoveSelectNone(),
	}

	// Handle piped input (non-interactive mode)
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return opts.Default, nil
	}

	// Check for problematic terminal environments
	termType := os.Getenv("TERM")
	if termType == "" || strings.Contains(termType, "dumb") {
		// Fallback to simple input for problematic terminals
		fmt.Printf("%s (Y/n): ", opts.Message)
		var input string
		_, _ = fmt.Scanln(&input)
		return input == "" || strings.ToLower(input) == "y" || strings.ToLower(input) == "yes", nil
	}

	var response bool
	err := survey.AskOne(prompt, &response, surveyOpts...)

	// Special handling for interrupt (Ctrl+C)
	if err == terminal.InterruptErr {
		if c.color {
			c.Println(aurora.Red("\n✖ Operation cancelled").String())
		} else {
			c.Println("\n✖ Operation cancelled")
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
		c.Printf("%s : %v\n", label, fields[k])
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

	c.PrintHeader(fmt.Sprintf("Reviewing PR #%d: %s", pr.Number, title))

	// Now we use interface{} for the map to handle different value types
	c.printFields(map[string]interface{}{
		"Repository": fmt.Sprintf("%s/%s", owner, repoName),
		"Author":     author,
		"Files":      fmt.Sprintf("%d files changed", changedFiles),
	})
	c.Println()
}
func (c *Console) ReviewingFile(file string, current, total int) {
	if c.color {
		c.Printf("%s %s %s\n",
			aurora.Blue("⟳").Bold(),
			aurora.White(fmt.Sprintf("[%d/%d]", current, total)).Bold(),
			aurora.Cyan(fmt.Sprintf("Analyzing %s...", file)).Bold(),
		)
	} else {
		c.Printf("⟳ [%d/%d] Analyzing %s...\n", current, total, file)
	}
}

func (c *Console) NoIssuesFound(file string, chunkNumber, totalChunks int) {
	c.Printf("✨ No issues found in chunk (%d/%d) of %s\n", chunkNumber, totalChunks, file)
}

func (c *Console) ShowComments(comments []PRReviewComment, metric MetricsCollector) {
	if len(comments) == 0 {
		return
	}

	c.Println("\nReview Comments:")
	for _, comment := range comments {
		if c.color {
			c.Printf("%s %s:%d\n",
				c.SeverityIcon(comment.Severity),
				comment.FilePath,
				comment.LineNumber,
			)
		} else {
			c.Printf("[%s] %s:%d\n",
				comment.Severity,
				comment.FilePath,
				comment.LineNumber,
			)
		}
		c.Println(indent(comment.Content, 2))

		if comment.Suggestion != "" {
			if c.color {
				c.Println(aurora.Green("  Suggestion:").String())
			} else {
				c.Println("  Suggestion:")
			}
			c.Println(indent(comment.Suggestion, 4))
		}
		c.Println()
		// Note: Feedback collection moved to end of review process
	}
}

func (c *Console) ShowSummary(comments []PRReviewComment, metric MetricsCollector) {
	c.PrintHeader("Review Summary")

	// Group by severity
	bySeverity := make(map[string]int)
	for _, comment := range comments {
		bySeverity[comment.Severity]++
	}

	if len(bySeverity) == 0 {
		c.Println("✨ No issues found")
		return
	}

	// Print counts by severity
	for _, severity := range []string{"critical", "warning", "suggestion"} {
		if count := bySeverity[severity]; count > 0 {
			icon := c.SeverityIcon(severity)
			c.Printf("%s %s: %d\n", icon, severity, count)
		}
	}
	c.ShowComments(comments, metric)

}

func (c *Console) FileError(filepath string, err error) {
	if c.color {
		c.Printf("%s Error processing %s: %v\n",
			aurora.Red("✖").String(),
			aurora.Bold(filepath).String(),
			err)
	} else {
		c.Printf("✖ Error processing %s: %v\n", filepath, err)
	}
}

func (c *Console) PostingComments(count int) {
	if count > 0 {
		c.Printf("\nPosting %d comments to GitHub...\n", count)
	}
}

func (c *Console) ReviewComplete() {
	if c.color {
		c.Println(aurora.Green("\n✓ Review completed successfully").String())
	} else {
		c.Println("\n✓ Review completed successfully")
	}
}

func (c *Console) SeverityIcon(severity string) string {
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

func (c *Console) PrintHeader(text string) {
	if c.color {
		// Use subtle gray text instead of bold for minimal aesthetic
		text = aurora.Gray(12, text).String()
	}
	// Clean minimal header - just text without decorative borders
	c.Printf("\n%s\n\n", text)
}

func (c *Console) Println(a ...interface{}) {
	fmt.Fprintln(c.w, a...)
}

func (c *Console) Printf(format string, a ...interface{}) {
	fmt.Fprintf(c.w, format, a...)
}

func (c *Console) ShowReviewMetrics(metrics MetricsCollector, comments []PRReviewComment) {
	c.PrintHeader("Review Metrics Summary")

	// First show immediate review statistics
	c.Println("\nImmediate Review Impact:")

	// Group comments by category for analysis
	categoryCounts := make(map[string]int)
	severityCounts := make(map[string]int)
	for _, comment := range comments {
		categoryCounts[comment.Category]++
		severityCounts[comment.Severity]++
	}

	// Show total comments with breakdown
	if c.color {
		c.Printf("Total Comments: %s\n", aurora.Blue(len(comments)).Bold())
	} else {
		c.Printf("Total Comments: %d\n", len(comments))
	}

	// Show category distribution
	if len(categoryCounts) > 0 {
		c.Println("\nComments by Category:")
		categories := make([]string, 0, len(categoryCounts))
		for category := range categoryCounts {
			categories = append(categories, category)
		}
		sort.Strings(categories)

		for _, category := range categories {
			count := categoryCounts[category]
			percentage := float64(count) / float64(len(comments)) * 100

			if c.color {
				c.Printf("  %-25s %s (%0.1f%%)\n",
					aurora.Blue(category),
					aurora.White(count).Bold(),
					percentage)
			} else {
				c.Printf("  %-25s %d (%0.1f%%)\n", category, count, percentage)
			}

			// Show category-specific metrics
			if stats := metrics.GetCategoryMetrics(category); stats.TotalComments > 0 {
				outdatedRate := stats.GetOutdatedRate() * 100
				c.Printf("    • Historical Outdated Rate: %0.1f%%\n", outdatedRate)
			}
		}
	}

	// Show severity distribution
	if len(severityCounts) > 0 {
		c.Println("\nComments by Severity:")
		severities := []string{"critical", "warning", "suggestion"}
		for _, severity := range severities {
			if count := severityCounts[severity]; count > 0 {
				percentage := float64(count) / float64(len(comments)) * 100
				icon := c.SeverityIcon(severity)

				if c.color {
					c.Printf("  %s %-12s %s (%0.1f%%)\n",
						icon,
						severity,
						aurora.White(count).Bold(),
						percentage)
				} else {
					c.Printf("  %s %-12s %d (%0.1f%%)\n",
						icon,
						severity,
						count,
						percentage)
				}
			}
		}
	}

	// Show historical impact metrics if available
	c.Println("\nHistorical Impact Metrics:")
	c.printFields(map[string]interface{}{
		"Overall Outdated Rate": fmt.Sprintf("%0.1f%%", metrics.GetOverallOutdatedRate()*100),
		"Weekly Active Users":   metrics.GetWeeklyActiveUsers(),
		"Review Response Rate":  fmt.Sprintf("%0.1f%%", metrics.GetReviewResponseRate()*100),
	})

	// Add helpful context about the metrics
	c.Println("\nMetric Explanations:")
	c.Println("• Outdated Rate measures how often flagged code is modified, indicating review effectiveness")
	c.Println("• Review Response Rate shows how frequently developers engage with review comments")
	c.Println("• Historical metrics help track long-term impact of automated reviews")
}

func (c *Console) collectFeedback(comment PRReviewComment, metric MetricsCollector) error {
	helpful, err := c.Confirm(PromptOptions{
		Message: "Was this comment helpful?",
		Default: true,
	})
	if err != nil {
		return err
	}

	if !helpful {
		// Collect reason if not helpful
		reason, err := c.promptFeedbackReason()
		if err != nil {
			return err
		}
		// Check for nil ThreadID before dereferencing
		var threadID int64
		if comment.ThreadID != nil {
			threadID = *comment.ThreadID
		}
		metric.TrackUserFeedback(context.Background(), threadID, false, reason)
	} else {
		// Check for nil ThreadID before dereferencing
		var threadID int64
		if comment.ThreadID != nil {
			threadID = *comment.ThreadID
		}
		metric.TrackUserFeedback(context.Background(), threadID, true, "")
	}

	return nil
}

// CollectAllFeedback collects feedback for all comments at the end of the review.
func (c *Console) CollectAllFeedback(comments []PRReviewComment, metric MetricsCollector) error {
	if len(comments) == 0 {
		return nil
	}

	// Check if feedback collection is disabled
	if os.Getenv("MAESTRO_DISABLE_FEEDBACK") == "true" {
		return nil
	}

	c.PrintHeader("Feedback Collection")
	c.Println("Please provide feedback on the review comments to help improve the system.")
	c.Println()

	for i, comment := range comments {
		// Show a brief summary of the comment
		c.Printf("Comment %d/%d - %s:%d\n", i+1, len(comments), comment.FilePath, comment.LineNumber)
		c.Printf("Category: %s | Severity: %s\n", comment.Category, comment.Severity)

		// Show first 100 chars of content
		content := comment.Content
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		c.Printf("Content: %s\n", content)
		c.Println()

		// Collect feedback for this comment
		if err := c.collectFeedback(comment, metric); err != nil {
			c.logger.Warn(context.Background(), "Failed to collect feedback for comment %d: %v", i+1, err)
			continue
		}
		c.Println()
	}

	c.Println("Thank you for your feedback!")
	return nil
}

// promptFeedbackReason collects the reason for unhelpful feedback.
func (c *Console) promptFeedbackReason() (string, error) {
	reasons := []string{
		"Not relevant to the code",
		"Too vague or unclear",
		"Already fixed",
		"Incorrect suggestion",
		"Other",
	}

	var selectedReason string
	prompt := &survey.Select{
		Message: "Why wasn't this comment helpful?",
		Options: reasons,
		Default: reasons[0],
	}

	err := survey.AskOne(prompt, &selectedReason)
	if err != nil {
		return "", fmt.Errorf("failed to get feedback reason: %w", err)
	}

	// If "Other" was selected, prompt for specific reason
	if selectedReason == "Other" {
		var customReason string
		customPrompt := &survey.Input{
			Message: "Please specify the reason:",
		}

		err := survey.AskOne(customPrompt, &customReason)
		if err != nil {
			return "", fmt.Errorf("failed to get custom reason: %w", err)
		}
		return customReason, nil
	}

	return selectedReason, nil
}

// UpdateSpinnerText updates the spinner's suffix text while maintaining animation.
func (c *Console) UpdateSpinnerText(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.spinner.Active() {
		c.spinner.Suffix = fmt.Sprintf(" %s", text)
	}
}

// indent adds spaces to the start of each line.
func indent(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// IsInteractive returns whether the console is running in an interactive terminal.
// The value is cached at Console creation time to avoid issues with go-prompt
// modifying terminal state during interactive sessions.
func (c *Console) IsInteractive() bool {
	return c.isInteractive
}

// ShowCommentsInteractive launches the interactive TUI for reviewing comments.
// This provides a lazygit-style interface for navigating and managing review comments.
func (c *Console) ShowCommentsInteractive(comments []PRReviewComment, onPost func([]PRReviewComment) error) error {
	if len(comments) == 0 {
		c.Println("No comments to display.")
		return nil
	}

	// Check if we're in an interactive terminal
	if !c.IsInteractive() {
		c.Println("Non-interactive mode: falling back to standard output")
		c.ShowComments(comments, nil)
		return nil
	}

	// Convert PRReviewComment to terminal.ReviewComment
	tuiComments := make([]reviewComment, 0, len(comments))
	for _, comment := range comments {
		tuiComments = append(tuiComments, reviewComment{
			FilePath:   comment.FilePath,
			LineNumber: comment.LineNumber,
			Content:    comment.Content,
			Severity:   comment.Severity,
			Suggestion: comment.Suggestion,
			Category:   comment.Category,
		})
	}

	// Create post callback that converts back to PRReviewComment
	var postCallback func([]reviewComment) error
	if onPost != nil {
		postCallback = func(tuiComments []reviewComment) error {
			prComments := make([]PRReviewComment, 0, len(tuiComments))
			for _, tc := range tuiComments {
				prComments = append(prComments, PRReviewComment{
					FilePath:   tc.FilePath,
					LineNumber: tc.LineNumber,
					Content:    tc.Content,
					Severity:   tc.Severity,
					Suggestion: tc.Suggestion,
					Category:   tc.Category,
				})
			}
			return onPost(prComments)
		}
	}

	return runReviewTUI(tuiComments, postCallback)
}

// reviewComment is a local type that mirrors terminal.ReviewComment
// to avoid import cycles.
type reviewComment struct {
	FilePath   string
	LineNumber int
	Content    string
	Severity   string
	Suggestion string
	Category   string
	CodeBlock  string
	DiffBlock  string
}

// runReviewTUI is a wrapper that launches the review TUI.
// It's separated to allow for easier testing and to avoid import cycles.
func runReviewTUI(comments []reviewComment, onPost func([]reviewComment) error) error {
	// Import the terminal package's TUI here
	// Since we can't import terminal package from main due to cycles,
	// we need to use the exported function from terminal package
	return launchReviewTUI(comments, onPost)
}
