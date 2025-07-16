package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// ArchitectureComponent represents a component in the system architecture.
type ArchitectureComponent struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`         // service, database, api, frontend, etc.
	Technology   string            `json:"technology"`   // go, react, postgres, etc.
	Description  string            `json:"description"`
	Dependencies []string          `json:"dependencies"` // other component names
	Metadata     map[string]string `json:"metadata"`     // additional context
}

// ArchitectureAnalysis represents the analyzed architecture of a repository.
type ArchitectureAnalysis struct {
	ProjectName    string                   `json:"project_name"`
	ProjectType    string                   `json:"project_type"`    // web_service, microservices, cli_tool, etc.
	Language       string                   `json:"language"`        // primary language
	Framework      string                   `json:"framework"`       // primary framework
	Components     []ArchitectureComponent  `json:"components"`
	Connections    []ArchitectureConnection `json:"connections"`
	Summary        string                   `json:"summary"`
	Recommendations []string                `json:"recommendations"`
}

// ArchitectureConnection represents a connection between components.
type ArchitectureConnection struct {
	From        string `json:"from"`        // source component name
	To          string `json:"to"`          // target component name
	Type        string `json:"type"`        // http, database, file, etc.
	Description string `json:"description"`
}

// DiagramGenerationResult contains the generated diagram and metadata.
type DiagramGenerationResult struct {
	PythonCode    string    `json:"python_code"`
	DiagramPath   string    `json:"diagram_path"`
	DiagramType   string    `json:"diagram_type"`
	GeneratedAt   time.Time `json:"generated_at"`
	Analysis      *ArchitectureAnalysis `json:"analysis"`
	Success       bool      `json:"success"`
	ErrorMessage  string    `json:"error_message,omitempty"`
}

// ArchitectureAnalyzer analyzes repository structure and generates architecture diagrams.
type ArchitectureAnalyzer struct {
	ragStore    RAGStore
	agent       ReviewAgent
	processor   *ArchitectureAnalysisProcessor
	logger      *logging.Logger
	console     ConsoleInterface
	workingDir  string
	pythonPath  string
}

// NewArchitectureAnalyzer creates a new architecture analyzer.
func NewArchitectureAnalyzer(ragStore RAGStore, agent ReviewAgent, logger *logging.Logger, console ConsoleInterface, workingDir string) *ArchitectureAnalyzer {
	return &ArchitectureAnalyzer{
		ragStore:   ragStore,
		agent:      agent,
		processor:  NewArchitectureAnalysisProcessor(agent),
		logger:     logger,
		console:    console,
		workingDir: workingDir,
		pythonPath: "python3", // default, can be configured
	}
}

// AnalyzeRepository performs comprehensive repository analysis using the RAG system.
func (aa *ArchitectureAnalyzer) AnalyzeRepository(ctx context.Context) (*ArchitectureAnalysis, error) {
	aa.logger.Info(ctx, "Starting repository architecture analysis")
	
	// Step 1: Gather repository context using RAG system
	repoContext, err := aa.gatherRepositoryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to gather repository context: %w", err)
	}

	// Step 2: Use DSPy signature to analyze architecture
	analysis, err := aa.processor.AnalyzeArchitecture(ctx, repoContext, "comprehensive")
	if err != nil {
		return nil, fmt.Errorf("failed to analyze architecture with DSPy: %w", err)
	}

	aa.logger.Info(ctx, "Completed repository architecture analysis: %d components, %d connections", 
		len(analysis.Components), len(analysis.Connections))

	return analysis, nil
}

// GenerateDiagram creates a Python script using the diagrams library and executes it.
func (aa *ArchitectureAnalyzer) GenerateDiagram(ctx context.Context, analysis *ArchitectureAnalysis, diagramType string) (*DiagramGenerationResult, error) {
	aa.logger.Info(ctx, "Generating architecture diagram of type: %s", diagramType)

	// Step 1: Generate Python code using DSPy signature
	pythonCode, err := aa.processor.GeneratePythonCode(ctx, analysis, diagramType)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Python code: %w", err)
	}

	// Step 2: Execute Python code to generate diagram
	diagramPath, err := aa.executePythonDiagram(ctx, pythonCode, analysis.ProjectName, diagramType)
	if err != nil {
		return &DiagramGenerationResult{
			PythonCode:   pythonCode,
			DiagramType:  diagramType,
			GeneratedAt:  time.Now(),
			Analysis:     analysis,
			Success:      false,
			ErrorMessage: err.Error(),
		}, err
	}

	return &DiagramGenerationResult{
		PythonCode:  pythonCode,
		DiagramPath: diagramPath,
		DiagramType: diagramType,
		GeneratedAt: time.Now(),
		Analysis:    analysis,
		Success:     true,
	}, nil
}

// gatherRepositoryContext uses the existing RAG system to understand repository structure.
func (aa *ArchitectureAnalyzer) gatherRepositoryContext(ctx context.Context) (string, error) {
	// Create a comprehensive query to understand the repository
	queries := []string{
		"What is the main purpose and architecture of this codebase?",
		"What are the key components, services, and modules?",
		"What technologies, frameworks, and databases are used?",
		"How do different parts of the system communicate?",
		"What are the main entry points and APIs?",
	}

	var contextBuilder strings.Builder
	contextBuilder.WriteString("Repository Architecture Context:\n\n")

	for _, query := range queries {
		// Use the agent's QA processor to get relevant information
		// This leverages the existing RAG system within the agent
		qaProcessor, _ := aa.agent.Orchestrator(ctx).GetProcessor("repo_qa")
		if qaProcessor == nil {
			aa.logger.Warn(ctx, "No QA processor available for query: %s", query)
			continue
		}

		result, err := qaProcessor.Process(ctx, agents.Task{
			ID: "context_query",
			Metadata: map[string]interface{}{
				"question": query,
			},
		}, nil)
		if err != nil {
			aa.logger.Warn(ctx, "Failed to process query: %s", query)
			continue
		}

		if response, ok := result.(*QAResponse); ok {
			contextBuilder.WriteString(fmt.Sprintf("Query: %s\n", query))
			contextBuilder.WriteString(fmt.Sprintf("Answer: %s\n", response.Answer))
			if len(response.SourceFiles) > 0 {
				contextBuilder.WriteString("Source Files: ")
				contextBuilder.WriteString(strings.Join(response.SourceFiles, ", "))
				contextBuilder.WriteString("\n")
			}
			contextBuilder.WriteString("\n")
		}
	}

	return contextBuilder.String(), nil
}



// executePythonDiagram executes the generated Python code to create the diagram.
func (aa *ArchitectureAnalyzer) executePythonDiagram(ctx context.Context, pythonCode, projectName, diagramType string) (string, error) {
	// Get absolute path for working directory
	workingDir, err := filepath.Abs(aa.workingDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute working directory: %w", err)
	}
	
	// Create temporary Python file
	tempDir := filepath.Join(workingDir, "diagrams")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create diagrams directory: %w", err)
	}

	scriptPath := filepath.Join(tempDir, fmt.Sprintf("%s_diagram.py", projectName))
	if err := os.WriteFile(scriptPath, []byte(pythonCode), 0644); err != nil {
		return "", fmt.Errorf("failed to write Python script: %w", err)
	}

	aa.logger.Info(ctx, "Executing Python script: %s", scriptPath)
	aa.logger.Info(ctx, "Working directory: %s", tempDir)

	// Execute Python script using uv with diagrams dependency
	cmd := exec.CommandContext(ctx, "uv", "run", "--with", "diagrams", scriptPath)
	cmd.Dir = tempDir
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("python execution failed: %w\nOutput: %s", err, string(output))
	}

	// Look for generated diagram file
	expectedPath := filepath.Join(tempDir, fmt.Sprintf("%s_architecture.png", projectName))
	if _, err := os.Stat(expectedPath); err != nil {
		// Try to find any PNG file in the directory
		files, _ := filepath.Glob(filepath.Join(tempDir, "*.png"))
		if len(files) > 0 {
			expectedPath = files[0]
		} else {
			return "", fmt.Errorf("diagram file not found after execution")
		}
	}

	aa.logger.Info(ctx, "Successfully generated diagram: %s", expectedPath)
	return expectedPath, nil
}


// SetPythonPath allows configuration of Python executable path.
func (aa *ArchitectureAnalyzer) SetPythonPath(path string) {
	aa.pythonPath = path
}

// CheckDependencies verifies that required dependencies are available.
func (aa *ArchitectureAnalyzer) CheckDependencies(ctx context.Context) error {
	// Check uv
	if _, err := exec.LookPath("uv"); err != nil {
		return fmt.Errorf("uv not found. Please install uv: https://docs.astral.sh/uv/getting-started/installation/")
	}

	// Check if uv can run with diagrams
	cmd := exec.CommandContext(ctx, "uv", "run", "--with", "diagrams", "-c", "import diagrams")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("uv cannot run with diagrams dependency. Error: %w", err)
	}

	// Check Graphviz
	if _, err := exec.LookPath("dot"); err != nil {
		return fmt.Errorf("graphviz not found. Please install Graphviz")
	}

	return nil
}

