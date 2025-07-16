package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
)

// ArchitectureAnalysisSignature defines the signature for repository architecture analysis.
var ArchitectureAnalysisSignature = core.NewSignature(
	[]core.InputField{
		{Field: core.Field{Name: "repository_context", Description: "Repository code context and structure information"}},
		{Field: core.Field{Name: "analysis_focus", Description: "What aspect to focus on (overview, detailed, microservices, etc.)"}},
	},
	[]core.OutputField{
		{Field: core.NewField("project_name")},
		{Field: core.NewField("project_type")},
		{Field: core.NewField("language")},
		{Field: core.NewField("framework")},
		{Field: core.NewField("components")},
		{Field: core.NewField("connections")},
		{Field: core.NewField("summary")},
		{Field: core.NewField("recommendations")},
	},
).WithInstruction(`Analyze the repository structure and identify the software architecture.

Identify:
1. Project name and type (web_service, microservices, cli_tool, library, etc.)
2. Primary programming language and framework
3. Key components with their types (service, database, api, frontend, cache, etc.)
4. Component dependencies and connections
5. Communication patterns (http, database, file, event, grpc)
6. Overall architecture summary and recommendations

Provide structured output for each field with clear, actionable information.`)

// DiagramDataGenerationSignature defines the signature for generating structured diagram data.
var DiagramDataGenerationSignature = core.NewSignature(
	[]core.InputField{
		{Field: core.Field{Name: "architecture_analysis", Description: "Structured architecture analysis of the project"}},
		{Field: core.Field{Name: "diagram_type", Description: "Type of diagram to generate (overview, detailed, microservices, etc.)"}},
		{Field: core.Field{Name: "project_name", Description: "Name of the project for file naming"}},
	},
	[]core.OutputField{
		{Field: core.NewField("diagram_title")},
		{Field: core.NewField("components")},
		{Field: core.NewField("clusters")},
		{Field: core.NewField("connections")},
		{Field: core.NewField("explanation")},
	},
).WithInstruction(`Generate structured diagram data for architecture visualization.

Based on the architecture analysis, identify:

1. **Components**: List each component with:
   - name: component identifier
   - type: technology type (go, database, user, api, etc.)
   - label: display text for the diagram
   - cluster: which group it belongs to (optional)

2. **Clusters**: Logical groupings like:
   - "Framework Core" for main components
   - "Data Layer" for databases
   - "External Services" for third-party integrations

3. **Connections**: Relationships between components:
   - from: source component name
   - to: target component name  
   - label: description of the relationship
   - style: solid, dashed, or dotted

Focus on the most important architectural relationships for the specified diagram type.
Return structured data, not code.`)

// ArchitectureAnalysisProcessor processes architecture analysis using DSPy signatures.
type ArchitectureAnalysisProcessor struct {
	agent         ReviewAgent
	analysisCache map[string]*ArchitectureAnalysis
	cacheKeyFunc  func(string) string
}

// NewArchitectureAnalysisProcessor creates a new processor.
func NewArchitectureAnalysisProcessor(agent ReviewAgent) *ArchitectureAnalysisProcessor {
	return &ArchitectureAnalysisProcessor{
		agent:         agent,
		analysisCache: make(map[string]*ArchitectureAnalysis),
		cacheKeyFunc: func(context string) string {
			// Simple cache key based on context length and first/last chars
			if len(context) < 10 {
				return context
			}
			return fmt.Sprintf("%s_%d_%s", context[:5], len(context), context[len(context)-5:])
		},
	}
}

// AnalyzeArchitecture performs architecture analysis using DSPy signature.
func (p *ArchitectureAnalysisProcessor) AnalyzeArchitecture(ctx context.Context, repoContext, analysisType string) (*ArchitectureAnalysis, error) {
	// Check cache first
	cacheKey := p.cacheKeyFunc(repoContext + analysisType)
	if cached, exists := p.analysisCache[cacheKey]; exists {
		return cached, nil
	}

	// Create prediction module
	predict := modules.NewPredict(ArchitectureAnalysisSignature).WithName("ArchitectureAnalyzer")
	
	// Prepare inputs
	inputs := map[string]interface{}{
		"repository_context": repoContext,
		"analysis_focus":     analysisType,
	}

	// Execute prediction
	result, err := predict.Process(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("architecture analysis failed: %w", err)
	}

	// Extract and parse results
	analysis, err := p.parseAnalysisResult(result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse analysis result: %w", err)
	}

	// Cache result
	p.analysisCache[cacheKey] = analysis
	return analysis, nil
}

// GenerateDiagramData generates structured diagram data using DSPy signature.
func (p *ArchitectureAnalysisProcessor) GenerateDiagramData(ctx context.Context, analysis *ArchitectureAnalysis, diagramType string) (*DiagramData, error) {
	// Create prediction module
	predict := modules.NewPredict(DiagramDataGenerationSignature).WithName("DiagramDataGenerator")
	
	// Serialize analysis to string
	analysisJSON, err := json.Marshal(analysis)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize analysis: %w", err)
	}

	// Prepare inputs
	inputs := map[string]interface{}{
		"architecture_analysis": string(analysisJSON),
		"diagram_type":          diagramType,
		"project_name":          analysis.ProjectName,
	}

	// Execute prediction
	result, err := predict.Process(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("diagram data generation failed: %w", err)
	}

	// Parse the result into DiagramData
	diagramData, err := p.parseDiagramDataResult(result, analysis, diagramType)
	if err != nil {
		return nil, fmt.Errorf("failed to parse diagram data: %w", err)
	}

	return diagramData, nil
}

// GeneratePythonCode generates Python code using the template system.
func (p *ArchitectureAnalysisProcessor) GeneratePythonCode(ctx context.Context, analysis *ArchitectureAnalysis, diagramType string) (string, error) {
	// Try to generate structured diagram data from LLM
	diagramData, err := p.GenerateDiagramData(ctx, analysis, diagramType)
	if err != nil {
		// If LLM fails, fall back to default diagram data
		diagramData = CreateDefaultDiagramData(analysis, diagramType)
	}

	// Create template generator
	generator, err := NewDiagramTemplateGenerator()
	if err != nil {
		return "", fmt.Errorf("failed to create template generator: %w", err)
	}

	// Generate Python code from template
	pythonCode, err := generator.GeneratePythonCode(diagramData)
	if err != nil {
		return "", fmt.Errorf("failed to generate Python code from template: %w", err)
	}

	return pythonCode, nil
}

// parseAnalysisResult parses the DSPy result into ArchitectureAnalysis struct.
func (p *ArchitectureAnalysisProcessor) parseAnalysisResult(result map[string]interface{}) (*ArchitectureAnalysis, error) {
	analysis := &ArchitectureAnalysis{}

	// Extract basic fields
	if projectName, ok := result["project_name"].(string); ok {
		analysis.ProjectName = projectName
	}
	if projectType, ok := result["project_type"].(string); ok {
		analysis.ProjectType = projectType
	}
	if language, ok := result["language"].(string); ok {
		analysis.Language = language
	}
	if framework, ok := result["framework"].(string); ok {
		analysis.Framework = framework
	}
	if summary, ok := result["summary"].(string); ok {
		analysis.Summary = summary
	}

	// Parse components (expecting JSON string or structured data)
	if componentsRaw, ok := result["components"]; ok {
		if err := p.parseComponents(componentsRaw, &analysis.Components); err != nil {
			// If parsing fails, create a default component
			analysis.Components = []ArchitectureComponent{
				{
					Name:        analysis.ProjectName,
					Type:        "service",
					Technology:  analysis.Language,
					Description: fmt.Sprintf("Main %s component", analysis.ProjectType),
					Dependencies: []string{},
					Metadata:    make(map[string]string),
				},
			}
		}
	}

	// Parse connections
	if connectionsRaw, ok := result["connections"]; ok {
		if err := p.parseConnections(connectionsRaw, &analysis.Connections); err != nil {
			analysis.Connections = []ArchitectureConnection{}
		}
	}

	// Parse recommendations
	if recommendationsRaw, ok := result["recommendations"]; ok {
		if err := p.parseRecommendations(recommendationsRaw, &analysis.Recommendations); err != nil {
			analysis.Recommendations = []string{}
		}
	}

	return analysis, nil
}

// parseDiagramDataResult parses the DSPy result into DiagramData struct.
func (p *ArchitectureAnalysisProcessor) parseDiagramDataResult(result map[string]interface{}, analysis *ArchitectureAnalysis, diagramType string) (*DiagramData, error) {
	// Start with default diagram data
	diagramData := CreateDefaultDiagramData(analysis, diagramType)
	
	// Override with LLM-generated data if available
	if title, ok := result["diagram_title"].(string); ok && title != "" {
		diagramData.Title = title
	}
	
	// Parse components from LLM output
	if componentsRaw, ok := result["components"]; ok {
		if components, err := p.parseComponentsFromLLM(componentsRaw); err == nil {
			// Merge with default components, keeping user component
			userComp := diagramData.Components[0] // User component
			diagramData.Components = append([]DiagramComponent{userComp}, components...)
		}
	}
	
	// Parse clusters from LLM output
	if clustersRaw, ok := result["clusters"]; ok {
		if clusters, err := p.parseClustersFromLLM(clustersRaw); err == nil {
			diagramData.Clusters = clusters
		}
	}
	
	// Parse connections from LLM output
	if connectionsRaw, ok := result["connections"]; ok {
		if connections, err := p.parseConnectionsFromLLM(connectionsRaw); err == nil {
			// Keep user connection and add LLM connections
			userConn := diagramData.Connections[0] // User connection
			diagramData.Connections = append([]DiagramConnection{userConn}, connections...)
		}
	}
	
	return diagramData, nil
}

// parseComponentsFromLLM parses components from LLM output.
func (p *ArchitectureAnalysisProcessor) parseComponentsFromLLM(raw interface{}) ([]DiagramComponent, error) {
	var components []DiagramComponent
	
	switch v := raw.(type) {
	case string:
		// Try to parse as JSON
		if err := json.Unmarshal([]byte(v), &components); err != nil {
			// If JSON parsing fails, create components from text description
			return p.parseComponentsFromText(v), nil
		}
	case []interface{}:
		// Handle slice of interfaces
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				comp := DiagramComponent{}
				if name, ok := itemMap["name"].(string); ok {
					comp.Name = name
					comp.VarName = generateVarName(name)
				}
				if compType, ok := itemMap["type"].(string); ok {
					comp.Type = compType
				}
				if label, ok := itemMap["label"].(string); ok {
					comp.Label = label
				} else {
					comp.Label = comp.Name
				}
				if cluster, ok := itemMap["cluster"].(string); ok {
					comp.ClusterName = cluster
				}
				components = append(components, comp)
			}
		}
	}
	
	return components, nil
}

// parseComponentsFromText creates components from text description.
func (p *ArchitectureAnalysisProcessor) parseComponentsFromText(text string) []DiagramComponent {
	var components []DiagramComponent
	
	// Simple text parsing - look for patterns like "ComponentName (type)"
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		// Extract component name and type from patterns like "DSPy-Go (go framework)"
		if matches := regexp.MustCompile(`([^(]+)\s*\(([^)]+)\)`).FindStringSubmatch(line); len(matches) == 3 {
			name := strings.TrimSpace(matches[1])
			typeInfo := strings.TrimSpace(matches[2])
			
			comp := DiagramComponent{
				Name:    name,
				Type:    typeInfo,
				Label:   name,
				VarName: generateVarName(name),
			}
			components = append(components, comp)
		} else if line != "" {
			// Fallback: treat the whole line as a component name
			comp := DiagramComponent{
				Name:    line,
				Type:    "service",
				Label:   line,
				VarName: generateVarName(line),
			}
			components = append(components, comp)
		}
	}
	
	return components
}

// parseClustersFromLLM parses clusters from LLM output.
func (p *ArchitectureAnalysisProcessor) parseClustersFromLLM(raw interface{}) ([]DiagramCluster, error) {
	var clusters []DiagramCluster
	
	switch v := raw.(type) {
	case string:
		if err := json.Unmarshal([]byte(v), &clusters); err != nil {
			// Create a default cluster from text
			clusters = []DiagramCluster{
				{
					Name:       "Main Components",
					Components: []string{}, // Will be populated later
				},
			}
		}
	case []interface{}:
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				cluster := DiagramCluster{}
				if name, ok := itemMap["name"].(string); ok {
					cluster.Name = name
				}
				if components, ok := itemMap["components"].([]interface{}); ok {
					for _, comp := range components {
						if compStr, ok := comp.(string); ok {
							cluster.Components = append(cluster.Components, compStr)
						}
					}
				}
				clusters = append(clusters, cluster)
			}
		}
	}
	
	return clusters, nil
}

// parseConnectionsFromLLM parses connections from LLM output.
func (p *ArchitectureAnalysisProcessor) parseConnectionsFromLLM(raw interface{}) ([]DiagramConnection, error) {
	var connections []DiagramConnection
	
	switch v := raw.(type) {
	case string:
		if err := json.Unmarshal([]byte(v), &connections); err != nil {
			// Parse from text description
			return p.parseConnectionsFromText(v), nil
		}
	case []interface{}:
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				conn := DiagramConnection{}
				if from, ok := itemMap["from"].(string); ok {
					conn.From = generateVarName(from)
				}
				if to, ok := itemMap["to"].(string); ok {
					conn.To = generateVarName(to)
				}
				if label, ok := itemMap["label"].(string); ok {
					conn.Label = label
				}
				if style, ok := itemMap["style"].(string); ok {
					conn.Style = style
				} else {
					conn.Style = "solid"
				}
				conn.Direction = ">>"
				connections = append(connections, conn)
			}
		}
	}
	
	return connections, nil
}

// parseConnectionsFromText creates connections from text description.
func (p *ArchitectureAnalysisProcessor) parseConnectionsFromText(text string) []DiagramConnection {
	var connections []DiagramConnection
	
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		// Look for patterns like "A -> B (label)" or "A connects to B"
		if matches := regexp.MustCompile(`([^->\s]+)\s*->\s*([^(]+)\s*\(([^)]+)\)`).FindStringSubmatch(line); len(matches) == 4 {
			conn := DiagramConnection{
				From:      generateVarName(strings.TrimSpace(matches[1])),
				To:        generateVarName(strings.TrimSpace(matches[2])),
				Label:     strings.TrimSpace(matches[3]),
				Style:     "solid",
				Direction: ">>",
			}
			connections = append(connections, conn)
		}
	}
	
	return connections
}

// Helper methods for parsing different result types.
func (p *ArchitectureAnalysisProcessor) parseComponents(raw interface{}, components *[]ArchitectureComponent) error {
	switch v := raw.(type) {
	case string:
		// Try to parse as JSON
		return json.Unmarshal([]byte(v), components)
	case []interface{}:
		// Handle slice of interfaces
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				comp := ArchitectureComponent{
					Metadata: make(map[string]string),
				}
				if name, ok := itemMap["name"].(string); ok {
					comp.Name = name
				}
				if cType, ok := itemMap["type"].(string); ok {
					comp.Type = cType
				}
				if tech, ok := itemMap["technology"].(string); ok {
					comp.Technology = tech
				}
				if desc, ok := itemMap["description"].(string); ok {
					comp.Description = desc
				}
				*components = append(*components, comp)
			}
		}
		return nil
	}
	return fmt.Errorf("unsupported components format: %T", raw)
}

func (p *ArchitectureAnalysisProcessor) parseConnections(raw interface{}, connections *[]ArchitectureConnection) error {
	switch v := raw.(type) {
	case string:
		return json.Unmarshal([]byte(v), connections)
	case []interface{}:
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				conn := ArchitectureConnection{}
				if from, ok := itemMap["from"].(string); ok {
					conn.From = from
				}
				if to, ok := itemMap["to"].(string); ok {
					conn.To = to
				}
				if cType, ok := itemMap["type"].(string); ok {
					conn.Type = cType
				}
				if desc, ok := itemMap["description"].(string); ok {
					conn.Description = desc
				}
				*connections = append(*connections, conn)
			}
		}
		return nil
	}
	return fmt.Errorf("unsupported connections format: %T", raw)
}

func (p *ArchitectureAnalysisProcessor) parseRecommendations(raw interface{}, recommendations *[]string) error {
	switch v := raw.(type) {
	case string:
		// Split by newlines or parse as JSON
		if strings.Contains(v, "[") {
			return json.Unmarshal([]byte(v), recommendations)
		}
		*recommendations = strings.Split(v, "\n")
		return nil
	case []interface{}:
		for _, item := range v {
			if str, ok := item.(string); ok {
				*recommendations = append(*recommendations, str)
			}
		}
		return nil
	}
	return fmt.Errorf("unsupported recommendations format: %T", raw)
}