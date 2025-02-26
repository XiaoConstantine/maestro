package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/logrusorgru/aurora"
	"golang.org/x/term"
)

// UI states for the application flow
type uiState int

const (
	stateInitial uiState = iota
	stateOwnerRepo
	stateConcurrencyOptions
	stateModelSelection
	stateVerboseOption
	stateOllamaModelInput
	stateActionSelection
	statePRInput
	stateQAInput
	stateProcessing
	stateResults
)

// maestroModel represents the application state
type maestroModel struct {
	config        *config
	state         uiState
	spinner       spinner.Model
	textInput     textinput.Model
	err           error
	logger        *logging.Logger
	console       ConsoleInterface
	choices       []ModelChoice
	cursor        int
	selectedIndex int
	question      string
	result        string
	ctx           context.Context
	inputValue    string
}

type successMsg struct {
	result string
}

type errorMsg struct {
	err error
}

// Initialize the model
func initialModel(cfg *config, logger *logging.Logger, console ConsoleInterface) maestroModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 156
	ti.Width = 50

	return maestroModel{
		config:    cfg,
		state:     stateInitial,
		spinner:   s,
		textInput: ti,
		logger:    logger,
		console:   console,
		ctx:       context.Background(),
	}
}

// Init initializes the model
func (m maestroModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		textinput.Blink,
		m.initialPrompt(),
	)
}

// initialPrompt shows the first prompt based on state
func (m maestroModel) initialPrompt() tea.Cmd {
	return func() tea.Msg {
		// If owner/repo are already set in config, skip to next state
		if m.config.owner != "" && m.config.repo != "" {
			return stateModelSelection
		}
		return stateOwnerRepo
	}
}

// Update handles UI events and state transitions
func (m maestroModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "enter":
			return m.handleEnterKey()

		case "up", "k":
			if m.state == stateModelSelection || m.state == stateActionSelection {
				m.cursor = max(m.cursor-1, 0)
			}

		case "down", "j":
			if m.state == stateModelSelection || m.state == stateActionSelection {
				m.cursor = min(m.cursor+1, len(m.choices)-1)
			}
		}

	case uiState:
		// Handle state transitions
		m.state = msg
		switch m.state {
		case stateOwnerRepo:
			m.textInput.Placeholder = "Enter repository owner/name (e.g., user/repo)"
			m.textInput.Focus()
			return m, textinput.Blink

		case stateModelSelection:
			m.choices = getModelChoices()
			m.cursor = 0
			return m, nil

		case stateActionSelection:
			m.choices = []ModelChoice{
				{DisplayName: "Review a Pull Request"},
				{DisplayName: "Ask questions about the repository"},
				{DisplayName: "Exit"},
			}
			m.cursor = 0
			return m, nil

		case stateProcessing:
			if m.state == statePRInput ||
				(m.state == stateActionSelection && m.cursor == 0) {
				// We're processing a PR review
				return m, m.processPR()
			} else if m.state == stateQAInput ||
				(m.state == stateActionSelection && m.cursor == 1) {
				// We're processing a repository question
				return m, m.processQA()
			}
			return m, nil
		case stateConcurrencyOptions:
			m.textInput.Placeholder = fmt.Sprintf("Enter number of concurrent workers (current: %d)", m.config.indexWorkers)
			m.textInput.Focus()
			return m, textinput.Blink

		case stateVerboseOption:
			m.choices = []ModelChoice{
				{DisplayName: "Yes, enable verbose logging"},
				{DisplayName: "No, use normal logging"},
			}
			m.cursor = 0
			return m, nil
		}

	case successMsg:
		m.result = msg.result
		m.state = stateResults
		return m, nil

	case errorMsg:
		m.err = msg.err
		m.state = stateResults
		return m, nil
	case spinner.TickMsg:
		var spinnerCmd tea.Cmd
		m.spinner, spinnerCmd = m.spinner.Update(msg)
		cmds = append(cmds, spinnerCmd)
	}

	// Update textinput component
	m.textInput, cmd = m.textInput.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// handleEnterKey handles the enter key press based on current state
func (m maestroModel) handleEnterKey() (tea.Model, tea.Cmd) {
	switch m.state {
	case stateOwnerRepo:
		parts := strings.Split(m.textInput.Value(), "/")
		if len(parts) == 2 {
			m.config.owner = parts[0]
			m.config.repo = parts[1]
			m.textInput.Reset()
			return m, func() tea.Msg { return stateModelSelection }
		}
		return m, nil

	case stateModelSelection:
		if len(m.choices) == 0 || m.cursor >= len(m.choices) {
			return m, nil
		}
		selected := m.choices[m.cursor]
		// Handle model selection logic based on your existing code
		m.selectedIndex = m.cursor

		// If LLaMA or Ollama was selected
		if selected.Provider == "ollama" {
			m.textInput.Placeholder = "Enter Ollama model (e.g., mistral:q4, llama2:13b)"
			m.textInput.Reset()
			m.textInput.Focus()
			return m, func() tea.Msg { return stateOllamaModelInput }
		}

		// For other providers, proceed to action selection
		return m, func() tea.Msg { return stateActionSelection }
	case stateConcurrencyOptions:
		// Parse concurrency settings
		workersStr := m.textInput.Value()
		if workersStr != "" {
			if workers, err := strconv.Atoi(workersStr); err == nil && workers > 0 {
				m.config.indexWorkers = workers
				m.config.reviewWorkers = workers
			}
		}
		m.textInput.Reset()
		return m, func() tea.Msg { return stateModelSelection }

	case stateVerboseOption:
		// Handle verbose option selection
		m.config.verbose = (m.cursor == 0) // Yes is index 0
		return m, func() tea.Msg { return stateActionSelection }

	case stateOllamaModelInput:
		// Handle Ollama model input logic
		ollamaModel := m.textInput.Value()
		if strings.Contains(ollamaModel, ":") {
			parts := strings.SplitN(ollamaModel, ":", 2)
			m.config.modelName = parts[0]
			m.config.modelConfig = parts[1]
		} else {
			m.config.modelName = ollamaModel
		}
		m.textInput.Reset()
		return m, func() tea.Msg { return stateActionSelection }

	case stateActionSelection:
		// Handle action selection based on cursor
		switch m.cursor {
		case 0: // Review PR
			m.textInput.Placeholder = "Enter PR number"
			m.textInput.Reset()
			m.textInput.Focus()
			return m, func() tea.Msg { return statePRInput }

		case 1: // QA
			m.textInput.Placeholder = "Ask a question about the repository"
			m.textInput.Reset()
			m.textInput.Focus()
			return m, func() tea.Msg { return stateQAInput }

		case 2: // Exit
			return m, tea.Quit
		}

	case statePRInput:
		// Store the PR number input for processing
		m.inputValue = m.textInput.Value()
		m.textInput.Reset()
		// Handle PR review logic
		return m, m.handlePRReview(m.textInput.Value())

	case stateQAInput:
		// Handle QA logic
		m.inputValue = m.textInput.Value()
		m.question = m.inputValue
		m.textInput.Reset()
		return m, m.handleQA(m.question)
	case stateResults:
		// Return to action selection after viewing results
		m.err = nil
		m.result = ""
		return m, func() tea.Msg { return stateActionSelection }
	}

	return m, nil
}

// handlePRReview processes a PR review request
func (m maestroModel) handlePRReview(prInput string) tea.Cmd {

	return func() tea.Msg {
		return stateProcessing
	}
}

// handleQA processes a repository question
func (m maestroModel) handleQA(question string) tea.Cmd {
	return func() tea.Msg {
		// Similar implementation to your existing QA function,
		// adapted to return results to the UI

		// For now, just a placeholder
		m.result = "Processing question: " + question

		return stateResults
	}
}

// View renders the UI
func (m maestroModel) View() string {
	var s strings.Builder
	width, _, err := term.GetSize(0)
	if err != nil {
		width = 120
	}
	// Display header in centered style
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("212")). // Slightly more muted pink
		PaddingTop(1).
		PaddingBottom(1)

	header := "✨ Maestro - Your AI Code Assistant! ✨"

	s.WriteString(headerStyle.Width(width).Align(lipgloss.Center).Render(header))

	s.WriteString("\n\n")
	// Render different views based on state
	switch m.state {
	case stateOwnerRepo:
		promptStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("252")). // Light gray
			PaddingBottom(1).
			Width(width).
			Align(lipgloss.Center)

		s.WriteString(promptStyle.Render("Enter repository owner and name:"))
		s.WriteString("\n\n")
		inputStyle := lipgloss.NewStyle().Width(width).Align(lipgloss.Center)
		s.WriteString(inputStyle.Render(m.textInput.View()))

		// Help text below
		helpStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")). // Dimmed text
			Italic(true).
			PaddingTop(1).
			Width(width).
			Align(lipgloss.Center)

		s.WriteString(helpStyle.Render("Format: owner/repo (e.g., username/project)"))

	case stateModelSelection:

		promptStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("252")). // Light gray
			PaddingBottom(1).
			Width(width).
			Align(lipgloss.Center)

		s.WriteString(promptStyle.Render("Choose a model:"))
		s.WriteString("\n\n")
		// Render model choices - centered with padding
		listStyle := lipgloss.NewStyle().PaddingLeft(15) // Indent the whole list

		var choiceList strings.Builder
		for i, choice := range m.choices {
			if strings.HasPrefix(choice.DisplayName, "===") {
				// This is a section header - style it differently
				headerStyle := lipgloss.NewStyle().
					Bold(true).
					Foreground(lipgloss.Color("69"))
				choiceList.WriteString(headerStyle.Render(choice.DisplayName) + "\n")
				continue
			}

			// Show cursor for selected item
			cursor := " "
			if m.cursor == i {
				cursor = ">"
			}

			// Item text
			itemText := fmt.Sprintf("%s %s", cursor, choice.DisplayName)

			// Description in dimmer color
			if choice.Description != "" {
				itemText += fmt.Sprintf(" - %s", choice.Description)
			}

			// Apply selected styling if this is the current item
			if m.cursor == i {
				selectedStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("212")). // Matching pink
					Bold(true)
				choiceList.WriteString(selectedStyle.Render(itemText) + "\n")
			} else {
				choiceList.WriteString(itemText + "\n")
			}
		}

		s.WriteString(listStyle.Render(choiceList.String()))

	case stateOllamaModelInput:
		s.WriteString("Enter Ollama model:\n\n")
		s.WriteString(m.textInput.View())
		s.WriteString("\n\nFormat: modelname[:tag] (e.g., mistral:q4, llama2:13b)")

	case stateActionSelection:
		s.WriteString("What would you like to do?\n\n")

		// Render action choices
		for i, choice := range m.choices {
			cursor := " "
			if m.cursor == i {
				cursor = ">"
			}

			line := fmt.Sprintf("%s %s", cursor, choice.DisplayName)

			if m.cursor == i {
				s.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color("205")).
					Render(line) + "\n")
			} else {
				s.WriteString(line + "\n")
			}
		}

	case statePRInput:
		s.WriteString("Enter PR number:\n\n")
		s.WriteString(m.textInput.View())

	case stateQAInput:
		s.WriteString("Ask a question about the repository:\n\n")
		s.WriteString(m.textInput.View())
		s.WriteString("\n\nPress Enter to submit or Esc to go back")

	case stateProcessing:
		s.WriteString("Processing... ")
		s.WriteString(m.spinner.View())

	case stateResults:
		if m.err != nil {
			s.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("9")).
				Render(fmt.Sprintf("Error: %v\n\n", m.err)))
		} else {
			s.WriteString(m.result)
		}

		s.WriteString("\n\nPress Enter to continue or Esc to exit")
	}

	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")). // Dimmed text
		PaddingTop(2).
		Width(width).
		Align(lipgloss.Left)

	s.WriteString(helpStyle.Render("Press ↑/↓ to navigate, Enter to select, Ctrl+C to quit"))

	return s.String()
}

// processPR performs the actual PR review work
func (m maestroModel) processPR() tea.Cmd {
	return func() tea.Msg {
		// Try to process the PR number
		var err error
		m.config.prNumber, err = strconv.Atoi(m.inputValue)
		if err != nil {
			return errorMsg{err: fmt.Errorf("invalid PR number: %w", err)}
		}

		// Run the CLI PR review
		if err := runCLI(m.config); err != nil {
			return errorMsg{err: fmt.Errorf("error reviewing PR: %v", err)}
		}

		// Return a success message
		return successMsg{result: fmt.Sprintf("Successfully reviewed PR #%d", m.config.prNumber)}
	}
}

func (m maestroModel) processQA() tea.Cmd {
	return func() tea.Msg {
		// Initialize necessary components for repository Q&A
		result, err := m.executeQA(m.inputValue)
		if err != nil {
			return errorMsg{err: fmt.Errorf("Q&A error: %v", err)}
		}

		return successMsg{result: result}
	}
}

// executeQA reuses the existing initializeAndAskQuestions logic
func (m maestroModel) executeQA(question string) (string, error) {
	// Create a buffer to capture output
	var outputBuffer strings.Builder

	// Create a custom console that writes to our buffer
	customConsole := NewConsole(&outputBuffer, m.logger, nil)

	// Create a custom function that processes a single question and returns
	processQuestionFn := func(ctx context.Context, cfg *config, console ConsoleInterface) error {
		// This is the core logic from initializeAndAskQuestions, but for a single question
		if cfg.githubToken == "" {
			return fmt.Errorf("GitHub token is required")
		}

		// Initialize GitHub tools and other necessary components
		githubTools := NewGitHubTools(cfg.githubToken, cfg.owner, cfg.repo)
		if githubTools == nil || githubTools.Client() == nil {
			return fmt.Errorf("failed to initialize GitHub client")
		}

		dbPath, err := CreateStoragePath(ctx, cfg.owner, cfg.repo)
		if err != nil {
			return fmt.Errorf("failed to create storage path: %w", err)
		}

		agent, err := NewPRReviewAgent(ctx, githubTools, dbPath, &AgentConfig{
			IndexWorkers:  cfg.indexWorkers,
			ReviewWorkers: cfg.reviewWorkers,
		})
		if err != nil {
			return fmt.Errorf("Failed to initialize agent due to: %v", err)
		}

		qaProcessor, _ := agent.Orchestrator(ctx).GetProcessor("repo_qa")

		// Process the question
		result, err := qaProcessor.Process(ctx, agents.Task{
			ID: "qa",
			Metadata: map[string]interface{}{
				"question": question,
			},
		}, nil)

		if err != nil {
			return fmt.Errorf("Error processing question: %v", err)
		}

		// Format response
		if response, ok := result.(*QAResponse); ok {
			// Print a separator line for visual clarity
			console.Println("\n" + strings.Repeat("─", 80))

			// Format and print the main answer using structured sections
			formattedAnswer := formatStructuredAnswer(response.Answer)
			console.Println(formattedAnswer)

			// Print source files in a tree-like structure if available
			if len(response.SourceFiles) > 0 {
				if console.Color() {
					console.Println("\n" + aurora.Blue("Source Files:").String())
				} else {
					console.Println("\nSource Files:")
				}

				// Group files by directory for better organization
				filesByDir := groupFilesByDirectory(response.SourceFiles)
				printFileTree(console, filesByDir)
			}

			// Print final separator
			console.Println("\n" + strings.Repeat("─", 80) + "\n")
		}

		return nil
	}

	// Process the question
	err := processQuestionFn(m.ctx, m.config, customConsole)
	if err != nil {
		return "", err
	}

	return outputBuffer.String(), nil
}
