package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/briandowns/spinner"
)

func main() {
	ctx := core.WithExecutionState(context.Background())
	apiKey := flag.String("api-key", "", "Anthropic API Key")
	githubToken := flag.String("github-token", os.Getenv("GITHUB_TOKEN"), "GitHub Token")
	owner := flag.String("owner", "", "Repository owner")
	repo := flag.String("repo", "", "Repository name")
	prNumber := flag.Int("pr", 0, "Pull Request number")
	debug := flag.Bool("debug", false, "Enable debug logging")
	verifyOnly := flag.Bool("verify-only", false, "Only verify token permissions without running review")

	flag.Parse()

	if *githubToken == "" || *owner == "" || *repo == "" || *prNumber == 0 {
		fmt.Println("Missing required flags. Please provide:")
		fmt.Println("  -github-token or set GITHUB_TOKEN")
		fmt.Println("  -owner (repository owner)")
		fmt.Println("  -repo (repository name)")
		fmt.Println("  -pr (pull request number)")
		os.Exit(1)
	}
	logLevel := logging.INFO
	if *debug {
		logLevel = logging.DEBUG
	}

	output := logging.NewConsoleOutput(true, logging.WithColor(true))

	logger := logging.NewLogger(logging.Config{
		Severity: logLevel,
		Outputs:  []logging.Output{output},
	})
	logging.SetLogger(logger)

	console := NewConsole(os.Stdout, logger)

	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Prefix = "Processing "
	err := s.Color("cyan")
	if err != nil {
		logger.Error(ctx, "Failed to start spinner properly")
	}
	err = VerifyTokenPermissions(ctx, *githubToken, *owner, *repo)
	if err != nil {
		logger.Error(ctx, "Token permission verification failed: %v", err)
		os.Exit(1)
	}

	if *verifyOnly {
		os.Exit(0)
	}
	llms.EnsureFactory()

	err = core.ConfigureDefaultLLM(*apiKey, "llamacpp:")
	//	err = core.ConfigureDefaultLLM(*apiKey, "ollama:deepseek-r1:14b-qwen-distill-q4_K_M")
	if err != nil {
		logger.Error(ctx, "Failed to configure LLM: %v", err)
	}
	agent, err := NewPRReviewAgent()
	if err != nil {
		panic(err)
	}

	githubTools := NewGitHubTools(*githubToken, *owner, *repo)
	logger.Info(ctx, "Fetching changes for PR #%d", *prNumber)
	pr, _, _ := githubTools.client.PullRequests.Get(ctx, *owner, *repo, *prNumber)
	console.StartReview(pr)

	changes, err := githubTools.GetPullRequestChanges(ctx, *prNumber)
	if err != nil {
		logger.Error(ctx, "Failed to get PR changes: %v", err)
		os.Exit(1)
	}
	tasks := make([]PRReviewTask, 0, len(changes.Files))
	for _, file := range changes.Files {
		// Log file being processed
		logger.Info(ctx, "Processing file: %s (+%d/-%d lines)",
			file.FilePath,
			file.Additions,
			file.Deletions,
		)

		tasks = append(tasks, PRReviewTask{
			FilePath:    file.FilePath,
			FileContent: file.FileContent,
			Changes:     file.Patch,
		})
	}
	if err != nil {
		logger.Error(ctx, "Failed to get PR changes: %v", err)
		os.Exit(1)
	}

	logger.Info(ctx, "Starting code review for %d files", len(tasks))

	s.Start()
	comments, err := agent.ReviewPR(ctx, tasks, console)
	s.Stop()
	if err != nil {
		logger.Error(ctx, "Failed to review PR: %v", err)
		os.Exit(1)
	}
	console.ShowSummary(comments)

	logger.Info(ctx, "Posting review comments to GitHub")
	// err = githubTools.CreateReviewComments(ctx, *prNumber, comments)
	// if err != nil {
	// 	logger.Error(ctx, "Failed to post review comments: %v", err)
	// 	os.Exit(1)
	// }

	console.ReviewComplete()
	logger.Info(ctx, "Successfully completed PR review")
}
