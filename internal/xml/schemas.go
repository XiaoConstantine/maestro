// Package xml provides XML schema definitions and parsing utilities for maestro.
package xml

import (
	"fmt"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/interceptors"
)

// ToolCall represents the root structure for all tool calls in XML format.
type ToolCall struct {
	ToolName  string                 `xml:"tool_name"`
	Arguments map[string]interface{} `xml:"arguments"`
}

// SearchFilesArguments represents arguments for the search_files tool.
type SearchFilesArguments struct {
	Pattern string `xml:"pattern"`
}

// SearchContentArguments represents arguments for the search_content tool.
type SearchContentArguments struct {
	Query string `xml:"query"`
	Path  string `xml:"path,omitempty"`
}

// ReadFileArguments represents arguments for the read_file tool.
type ReadFileArguments struct {
	FilePath string `xml:"file_path"`
}

// FinishArguments represents arguments for the Finish tool (no arguments needed).
type FinishArguments struct{}

// ConfigFactory creates appropriate XML configurations for different tool types.
type ConfigFactory struct {
	// Performance metrics for optimization
	successRates  map[string]float64
	avgParseTimes map[string]time.Duration
	mu            sync.RWMutex
}

// NewConfigFactory creates a new XML configuration factory.
func NewConfigFactory() *ConfigFactory {
	return &ConfigFactory{
		successRates:  make(map[string]float64),
		avgParseTimes: make(map[string]time.Duration),
	}
}

// GetSearchToolConfig returns XML configuration optimized for search tools.
func (x *ConfigFactory) GetSearchToolConfig() interceptors.XMLConfig {
	return interceptors.XMLConfig{
		StrictParsing: false, // Allow flexible parsing for search queries
		MaxSize:       5000,  // Reasonable size limit for tool responses
		ParseTimeout:  10,    // 10 second timeout for XML parsing
	}
}

// GetFileToolConfig returns XML configuration optimized for file tools.
func (x *ConfigFactory) GetFileToolConfig() interceptors.XMLConfig {
	return interceptors.XMLConfig{
		StrictParsing: true,  // Strict parsing for file paths
		MaxSize:       10000, // Larger limit for file content
		ParseTimeout:  15,    // Longer timeout for file operations
	}
}

// GetGeneralConfig returns XML configuration for general tool usage.
func (x *ConfigFactory) GetGeneralConfig() interceptors.XMLConfig {
	return interceptors.XMLConfig{
		StrictParsing: false, // Flexible parsing by default
		MaxSize:       8000,  // Balanced size limit
		ParseTimeout:  12,    // Balanced timeout
	}
}

// GetAdaptiveConfig returns dynamically optimized XML configuration based on performance metrics.
func (x *ConfigFactory) GetAdaptiveConfig(toolName string, queryComplexity int) interceptors.XMLConfig {
	x.mu.RLock()
	successRate := x.successRates[toolName]
	avgParseTime := x.avgParseTimes[toolName]
	x.mu.RUnlock()

	// Base configuration
	config := interceptors.XMLConfig{
		StrictParsing: false,
		MaxSize:       8000,
		ParseTimeout:  10,
	}

	// Adjust based on success rate
	if successRate > 0.95 {
		// High success rate - can be stricter
		config.StrictParsing = true
		config.ParseTimeout = 8
	} else if successRate < 0.7 {
		// Low success rate - be more lenient
		config.MaxSize = 12000
		config.ParseTimeout = 15
	}

	// Adjust based on query complexity
	if queryComplexity >= 4 {
		config.MaxSize = config.MaxSize * 2
		config.ParseTimeout = config.ParseTimeout + 5
	}

	// Adjust based on average parse time
	if avgParseTime > 5*time.Second {
		// Increase timeout based on historical parse time
		config.ParseTimeout = config.ParseTimeout + 5
	}

	return config
}

// RecordPerformance records XML parsing performance metrics for optimization.
func (x *ConfigFactory) RecordPerformance(toolName string, success bool, parseTime time.Duration) {
	x.mu.Lock()
	defer x.mu.Unlock()

	// Update success rate (exponential moving average)
	currentRate := x.successRates[toolName]
	if success {
		x.successRates[toolName] = currentRate*0.9 + 0.1
	} else {
		x.successRates[toolName] = currentRate * 0.9
	}

	// Update average parse time (exponential moving average)
	currentAvg := x.avgParseTimes[toolName]
	x.avgParseTimes[toolName] = time.Duration(float64(currentAvg)*0.9 + float64(parseTime)*0.1)
}

// ToolCallParser handles parsing of XML tool calls.
type ToolCallParser struct {
	configFactory *ConfigFactory
}

// NewToolCallParser creates a new tool call parser.
func NewToolCallParser() *ToolCallParser {
	return &ToolCallParser{
		configFactory: NewConfigFactory(),
	}
}

// ParseToolCall extracts tool call information from XML.
func (tcp *ToolCallParser) ParseToolCall(xmlContent string) (*ToolCall, error) {
	// This will be implemented with dspy-go's native XML parsing
	// For now, we define the interface
	return nil, nil
}

// ValidateToolArguments validates tool arguments based on tool type.
func (tcp *ToolCallParser) ValidateToolArguments(toolName string, args map[string]interface{}) error {
	switch toolName {
	case "search_files":
		if _, ok := args["pattern"]; !ok {
			return fmt.Errorf("search_files requires 'pattern' argument")
		}
	case "search_content":
		if _, ok := args["query"]; !ok {
			return fmt.Errorf("search_content requires 'query' argument")
		}
	case "read_file":
		if _, ok := args["file_path"]; !ok {
			return fmt.Errorf("read_file requires 'file_path' argument")
		}
	case "Finish":
		// No arguments required for Finish
	default:
		return fmt.Errorf("unknown tool: %s", toolName)
	}
	return nil
}

// GetConfigFactory returns the config factory for external use.
func (tcp *ToolCallParser) GetConfigFactory() *ConfigFactory {
	return tcp.configFactory
}
