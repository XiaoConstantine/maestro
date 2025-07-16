package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// DiagramData represents structured data for diagram generation.
type DiagramData struct {
	Title       string                `json:"title"`
	Filename    string                `json:"filename"`
	Components  []DiagramComponent    `json:"components"`
	Clusters    []DiagramCluster      `json:"clusters"`
	Connections []DiagramConnection   `json:"connections"`
	Metadata    map[string]string     `json:"metadata"`
}

// DiagramComponent represents a single component in the diagram.
type DiagramComponent struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`        // go, user, database, etc.
	Icon        string            `json:"icon"`        // specific diagrams icon class
	Label       string            `json:"label"`       // display text
	VarName     string            `json:"var_name"`    // Python variable name
	ClusterName string            `json:"cluster_name"` // which cluster this belongs to
	Metadata    map[string]string `json:"metadata"`
}

// DiagramCluster represents a grouping of components.
type DiagramCluster struct {
	Name       string   `json:"name"`
	Components []string `json:"components"` // component var names
}

// DiagramConnection represents a connection between components.
type DiagramConnection struct {
	From        string `json:"from"`        // source component var name
	To          string `json:"to"`          // target component var name
	Label       string `json:"label"`       // connection description
	Style       string `json:"style"`       // solid, dashed, dotted
	Direction   string `json:"direction"`   // ->, <<, <>, etc.
}

// PythonDiagramTemplate contains the Go template for generating Python code.
const PythonDiagramTemplate = `from diagrams import Diagram, Cluster, Edge
{{- range .Imports}}
from diagrams.{{.}} import *
{{- end}}

with Diagram("{{.Title}}", filename="{{.Filename}}", show=False, direction="TB"):
    # Components
    {{- range .Components}}
    {{.VarName}} = {{.Icon}}("{{.Label}}")
    {{- end}}
    
    # Connections
    {{- range .Connections}}
    {{.From}} >> {{if .Label}}Edge(label="{{.Label}}"{{if .Style}}, style="{{.Style}}"{{end}}) >> {{end}}{{.To}}
    {{- end}}
`

// TemplateData represents the final data structure for template rendering.
type TemplateData struct {
	Title       string
	Filename    string
	Imports     []string
	Components  []DiagramComponent
	Clusters    []DiagramCluster
	Connections []DiagramConnection
}

// DiagramTemplateGenerator handles template-based Python code generation.
type DiagramTemplateGenerator struct {
	template *template.Template
}

// NewDiagramTemplateGenerator creates a new template generator.
func NewDiagramTemplateGenerator() (*DiagramTemplateGenerator, error) {
	tmpl, err := template.New("python_diagram").Parse(PythonDiagramTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}
	
	return &DiagramTemplateGenerator{
		template: tmpl,
	}, nil
}

// GeneratePythonCode generates Python code from structured diagram data.
func (g *DiagramTemplateGenerator) GeneratePythonCode(data *DiagramData) (string, error) {
	// Convert DiagramData to TemplateData
	templateData := g.prepareTemplateData(data)
	
	// Execute template
	var buf bytes.Buffer
	if err := g.template.Execute(&buf, templateData); err != nil {
		return "", fmt.Errorf("template execution failed: %w", err)
	}
	
	pythonCode := buf.String()
	
	// Validate generated Python syntax
	if err := g.validatePythonSyntax(pythonCode); err != nil {
		return "", fmt.Errorf("generated Python has syntax errors: %w", err)
	}
	
	return pythonCode, nil
}

// prepareTemplateData converts DiagramData to TemplateData for template rendering.
func (g *DiagramTemplateGenerator) prepareTemplateData(data *DiagramData) *TemplateData {
	// Determine required imports based on component types
	imports := g.determineImports(data.Components)
	
	// Ensure all components have valid variable names
	components := make([]DiagramComponent, len(data.Components))
	for i, comp := range data.Components {
		components[i] = comp
		if components[i].VarName == "" {
			components[i].VarName = g.generateVarName(comp.Name)
		}
		if components[i].Icon == "" {
			components[i].Icon = g.determineIcon(comp.Type)
		}
	}
	
	// Prepare clusters with component references
	clusters := make([]DiagramCluster, len(data.Clusters))
	for i, cluster := range data.Clusters {
		clusters[i] = cluster
		// Map component names to variable names
		for j, compName := range cluster.Components {
			for _, comp := range components {
				if comp.Name == compName {
					clusters[i].Components[j] = comp.VarName
					break
				}
			}
		}
	}
	
	// Prepare connections with proper direction symbols
	connections := make([]DiagramConnection, len(data.Connections))
	for i, conn := range data.Connections {
		connections[i] = conn
		if connections[i].Direction == "" {
			connections[i].Direction = ">>"
		}
	}
	
	return &TemplateData{
		Title:       data.Title,
		Filename:    data.Filename,
		Imports:     imports,
		Components:  components,
		Clusters:    clusters,
		Connections: connections,
	}
}

// determineImports determines which diagrams modules to import based on component types.
func (g *DiagramTemplateGenerator) determineImports(components []DiagramComponent) []string {
	importSet := make(map[string]bool)
	
	for _, comp := range components {
		switch comp.Type {
		case "go", "python", "java", "javascript":
			importSet["programming.language"] = true
		case "user", "client":
			importSet["onprem.client"] = true
		case "database", "sql", "postgres", "mysql":
			importSet["onprem.database"] = true
		case "aws":
			importSet["aws.compute"] = true
		case "kubernetes", "docker":
			importSet["generic.virtualization"] = true
		default:
			importSet["generic.blank"] = true
		}
	}
	
	var imports []string
	for imp := range importSet {
		imports = append(imports, imp)
	}
	
	return imports
}

// determineIcon maps component types to specific diagrams icons.
func (g *DiagramTemplateGenerator) determineIcon(componentType string) string {
	iconMap := map[string]string{
		"go":         "Go",
		"python":     "Python",
		"java":       "Java", 
		"javascript": "Javascript",
		"user":       "User",
		"client":     "User",
		"database":   "Postgresql",
		"sql":        "Postgresql",
		"postgres":   "Postgresql",
		"mysql":      "Mysql",
		"aws":        "EC2",
		"kubernetes": "K8S",
		"docker":     "Docker",
	}
	
	if icon, exists := iconMap[strings.ToLower(componentType)]; exists {
		return icon
	}
	
	return "Blank" // fallback
}

// generateVarName creates a valid Python variable name from a component name.
func (g *DiagramTemplateGenerator) generateVarName(name string) string {
	// Remove special characters and spaces
	reg := regexp.MustCompile(`[^a-zA-Z0-9_]`)
	varName := reg.ReplaceAllString(name, "_")
	
	// Ensure it starts with a letter
	if len(varName) > 0 && varName[0] >= '0' && varName[0] <= '9' {
		varName = "comp_" + varName
	}
	
	// Convert to lowercase
	varName = strings.ToLower(varName)
	
	// Ensure it's not empty
	if varName == "" {
		varName = "component"
	}
	
	return varName
}

// validatePythonSyntax validates that the generated Python code is syntactically correct.
func (g *DiagramTemplateGenerator) validatePythonSyntax(pythonCode string) error {
	// For now, do basic validation by checking for common issues
	lines := strings.Split(pythonCode, "\n")
	
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		
		// Check for indentation issues after colons
		if strings.HasSuffix(trimmed, ":") && i+1 < len(lines) {
			nextLine := lines[i+1]
			if strings.TrimSpace(nextLine) != "" && !strings.HasPrefix(nextLine, "    ") {
				return fmt.Errorf("indentation error after line %d: '%s'", i+1, trimmed)
			}
		}
		
		// Check for basic Python syntax issues
		if strings.Contains(trimmed, ">>") && !strings.Contains(trimmed, "Edge") {
			// Check if it's a valid connection
			if !regexp.MustCompile(`^\w+\s*>>\s*\w+`).MatchString(trimmed) &&
			   !regexp.MustCompile(`^\w+\s*>>\s*Edge\(.*\)\s*>>\s*\w+`).MatchString(trimmed) {
				return fmt.Errorf("invalid connection syntax on line %d: '%s'", i+1, trimmed)
			}
		}
	}
	
	return nil
}


// CreateDefaultDiagramData creates a default diagram structure from architecture analysis.
func CreateDefaultDiagramData(analysis *ArchitectureAnalysis, diagramType string) *DiagramData {
	data := &DiagramData{
		Title:       fmt.Sprintf("%s Architecture", analysis.ProjectName),
		Filename:    sanitizeFilename(analysis.ProjectName),
		Components:  make([]DiagramComponent, 0),
		Clusters:    make([]DiagramCluster, 0),
		Connections: make([]DiagramConnection, 0),
		Metadata:    make(map[string]string),
	}
	
	// Add a user component
	data.Components = append(data.Components, DiagramComponent{
		Name:    "User",
		Type:    "user",
		Icon:    "User",
		Label:   "User",
		VarName: "user",
	})
	
	// Convert architecture components
	var clusterComponents []string
	for _, comp := range analysis.Components {
		diagComp := DiagramComponent{
			Name:        comp.Name,
			Type:        comp.Technology,
			Label:       fmt.Sprintf("%s\\n(%s)", comp.Name, comp.Type),
			VarName:     generateVarName(comp.Name),
			ClusterName: "main",
			Metadata:    comp.Metadata,
		}
		data.Components = append(data.Components, diagComp)
		clusterComponents = append(clusterComponents, diagComp.VarName)
	}
	
	// Create main cluster
	if len(clusterComponents) > 0 {
		data.Clusters = append(data.Clusters, DiagramCluster{
			Name:       fmt.Sprintf("%s Framework", analysis.ProjectName),
			Components: clusterComponents,
		})
	}
	
	// Convert connections
	for _, conn := range analysis.Connections {
		data.Connections = append(data.Connections, DiagramConnection{
			From:      generateVarName(conn.From),
			To:        generateVarName(conn.To),
			Label:     conn.Description,
			Style:     "solid",
			Direction: ">>",
		})
	}
	
	// Add user connection to main component if available
	if len(data.Components) > 1 {
		data.Connections = append(data.Connections, DiagramConnection{
			From:      "user",
			To:        data.Components[1].VarName, // First non-user component
			Label:     "interacts with",
			Style:     "solid",
			Direction: ">>",
		})
	}
	
	return data
}

// generateVarName is a package-level helper function.
func generateVarName(name string) string {
	// Remove common prefixes and suffixes that mess up variable names
	name = strings.TrimPrefix(name, "- name: ")
	name = strings.TrimPrefix(name, "name: ")
	name = strings.TrimSuffix(name, "_")
	
	// Clean the name
	reg := regexp.MustCompile(`[^a-zA-Z0-9_]`)
	varName := reg.ReplaceAllString(name, "_")
	
	// Remove multiple underscores
	multiUnderscore := regexp.MustCompile(`_+`)
	varName = multiUnderscore.ReplaceAllString(varName, "_")
	
	// Remove leading/trailing underscores
	varName = strings.Trim(varName, "_")
	
	if len(varName) > 0 && varName[0] >= '0' && varName[0] <= '9' {
		varName = "comp_" + varName
	}
	
	varName = strings.ToLower(varName)
	
	if varName == "" || varName == "_" {
		varName = "component"
	}
	
	return varName
}

// sanitizeFilename creates a safe filename from project name.
func sanitizeFilename(projectName string) string {
	// Remove common prefixes
	name := strings.TrimSpace(projectName)
	
	// Replace spaces and special characters with underscores
	reg := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	filename := reg.ReplaceAllString(name, "_")
	
	// Remove multiple underscores/dashes
	multiChars := regexp.MustCompile(`[_-]+`)
	filename = multiChars.ReplaceAllString(filename, "_")
	
	// Remove leading/trailing special chars
	filename = strings.Trim(filename, "_-")
	
	// Convert to lowercase
	filename = strings.ToLower(filename)
	
	// Ensure it's not empty
	if filename == "" {
		filename = "architecture"
	}
	
	// Limit length
	if len(filename) > 50 {
		filename = filename[:50]
	}
	
	return filename
}