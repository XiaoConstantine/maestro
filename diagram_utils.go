package main

import (
	"fmt"
)


// DiagramType represents different types of architecture diagrams.
type DiagramType string

const (
	DiagramTypeOverview     DiagramType = "overview"
	DiagramTypeDetailed     DiagramType = "detailed"
	DiagramTypeDataFlow     DiagramType = "dataflow"
	DiagramTypeDeployment   DiagramType = "deployment"
	DiagramTypeSecurity     DiagramType = "security"
	DiagramTypeMicroservices DiagramType = "microservices"
)

// GetSupportedDiagramTypes returns all supported diagram types.
func GetSupportedDiagramTypes() []DiagramType {
	return []DiagramType{
		DiagramTypeOverview,
		DiagramTypeDetailed,
		DiagramTypeDataFlow,
		DiagramTypeDeployment,
		DiagramTypeSecurity,
		DiagramTypeMicroservices,
	}
}

// GetDiagramTypeDescription returns a description for a diagram type.
func GetDiagramTypeDescription(diagramType DiagramType) string {
	switch diagramType {
	case DiagramTypeOverview:
		return "High-level system overview showing main components"
	case DiagramTypeDetailed:
		return "Detailed view with all components and connections"
	case DiagramTypeDataFlow:
		return "Focus on data flow and processing pipelines"
	case DiagramTypeDeployment:
		return "Infrastructure and deployment architecture"
	case DiagramTypeSecurity:
		return "Security components and trust boundaries"
	case DiagramTypeMicroservices:
		return "Microservices architecture with service boundaries"
	default:
		return "Unknown diagram type"
	}
}

// ValidateDiagramType checks if a diagram type is supported.
func ValidateDiagramType(diagramType string) (DiagramType, error) {
	dt := DiagramType(diagramType)
	for _, supported := range GetSupportedDiagramTypes() {
		if dt == supported {
			return dt, nil
		}
	}
	return "", fmt.Errorf("unsupported diagram type: %s", diagramType)
}

// FormatDiagramResult formats a diagram generation result for display.
func FormatDiagramResult(result *DiagramGenerationResult, console ConsoleInterface) {
	if result.Success {
		console.Printf("✅ Successfully generated %s diagram\n", result.DiagramType)
		console.Printf("📁 Diagram saved to: %s\n", result.DiagramPath)
		console.Printf("⏱️  Generated at: %s\n", result.GeneratedAt.Format("2006-01-02 15:04:05"))
		
		if result.Analysis != nil {
			console.Printf("📊 Analysis: %d components, %d connections\n", 
				len(result.Analysis.Components), len(result.Analysis.Connections))
		}
	} else {
		console.Printf("❌ Failed to generate %s diagram\n", result.DiagramType)
		if result.ErrorMessage != "" {
			console.Printf("Error: %s\n", result.ErrorMessage)
		}
	}
}

// ShowDiagramHelp displays help information for diagram commands.
func ShowDiagramHelp(console ConsoleInterface) {
	console.PrintHeader("🎨 Architecture Diagram Commands")
	
	console.Println("Available Commands:")
	console.Printf("  /diagram generate [type]  - Generate architecture diagram\n")
	console.Printf("  /diagram list            - List available diagram types\n")
	console.Printf("  /diagram check           - Check dependencies\n")
	console.Printf("  /diagram help            - Show this help\n")
	
	console.Println("\nSupported Diagram Types:")
	for _, dt := range GetSupportedDiagramTypes() {
		console.Printf("  %-15s - %s\n", string(dt), GetDiagramTypeDescription(dt))
	}
	
	console.Println("\nExamples:")
	console.Printf("  /diagram generate overview     - Create high-level overview\n")
	console.Printf("  /diagram generate microservices - Focus on service architecture\n")
	console.Printf("  /diagram generate dataflow     - Show data processing flow\n")
	
	console.Println("\nDependencies:")
	console.Printf("  - uv (Python package runner)\n")
	console.Printf("  - diagrams library (auto-installed via uv)\n")
	console.Printf("  - Graphviz (brew install graphviz or apt-get install graphviz)\n")
}

