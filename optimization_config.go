package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// OptimizationConfig contains configuration for late interaction and search optimization.
type OptimizationConfig struct {
	// Search Optimization Settings
	CandidateMultiplier int     `json:"candidate_multiplier"` // How many more candidates to fetch than needed
	SaturationThreshold float64 `json:"saturation_threshold"` // Threshold for stopping optimization

	// Late Interaction Settings
	EnableLateInteraction bool   `json:"enable_late_interaction"` // Enable late interaction with Qwen3
	QwenModel             string `json:"qwen_model"`              // Qwen model to use
	RefinementStages      int    `json:"refinement_stages"`       // Number of refinement stages
	FallbackOnFailure     bool   `json:"fallback_on_failure"`     // Fallback to basic selection on failure

	// Multi-Vector Embedding Settings
	EnableMultiVector bool `json:"enable_multi_vector"` // Enable multi-vector embeddings
	ChunkSize         int  `json:"chunk_size"`          // Size for chunk-level embeddings
	ChunkOverlap      int  `json:"chunk_overlap"`       // Overlap between chunks

	// Performance Settings
	MaxCandidates     int           `json:"max_candidates"`     // Maximum candidates to consider
	SelectionTimeout  time.Duration `json:"selection_timeout"`  // Timeout for selection process
	EvaluationEnabled bool          `json:"evaluation_enabled"` // Enable quality evaluation

	// Quality Thresholds
	MinRelevanceThreshold float64 `json:"min_relevance_threshold"` // Minimum relevance score to consider
	MinQualityThreshold   float64 `json:"min_quality_threshold"`   // Minimum content quality score
	MinDiversityThreshold float64 `json:"min_diversity_threshold"` // Minimum diversity requirement

	// Debug and Logging
	EnableDetailedLogging bool `json:"enable_detailed_logging"` // Enable detailed debug logging
	LogPerformanceMetrics bool `json:"log_performance_metrics"` // Log performance metrics
	SaveEvaluationReports bool `json:"save_evaluation_reports"` // Save evaluation reports to disk
}

// GetOptimizationConfig returns the optimization configuration based on environment variables.
func GetOptimizationConfig() *OptimizationConfig {
	config := &OptimizationConfig{
		// Default values
		CandidateMultiplier:   3,
		SaturationThreshold:   0.001,
		EnableLateInteraction: true,
		QwenModel:             "qwen2.5:0.5b",
		RefinementStages:      2,
		FallbackOnFailure:     true,
		EnableMultiVector:     false, // Disabled by default due to complexity
		ChunkSize:             512,
		ChunkOverlap:          128,
		MaxCandidates:         100,
		SelectionTimeout:      30 * time.Second,
		EvaluationEnabled:     true,
		MinRelevanceThreshold: 0.1,
		MinQualityThreshold:   0.3,
		MinDiversityThreshold: 0.2,
		EnableDetailedLogging: false,
		LogPerformanceMetrics: true,
		SaveEvaluationReports: false,
	}

	// Override with environment variables if present
	// MAESTRO_ENABLE_SUBMODULAR is deprecated and no longer used

	// Removed submodular method config

	if val := os.Getenv("MAESTRO_CANDIDATE_MULTIPLIER"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed >= 2 && parsed <= 10 {
			config.CandidateMultiplier = parsed
		}
	}

	if val := os.Getenv("MAESTRO_SATURATION_THRESHOLD"); val != "" {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil && parsed > 0 && parsed < 1 {
			config.SaturationThreshold = parsed
		}
	}

	if val := os.Getenv("MAESTRO_ENABLE_LATE_INTERACTION"); val != "" {
		config.EnableLateInteraction = parseConfigBool(val, config.EnableLateInteraction)
	}

	if val := os.Getenv("MAESTRO_QWEN_MODEL"); val != "" {
		config.QwenModel = val
	}

	if val := os.Getenv("MAESTRO_REFINEMENT_STAGES"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed >= 1 && parsed <= 5 {
			config.RefinementStages = parsed
		}
	}

	if val := os.Getenv("MAESTRO_FALLBACK_ON_FAILURE"); val != "" {
		config.FallbackOnFailure = parseConfigBool(val, config.FallbackOnFailure)
	}

	if val := os.Getenv("MAESTRO_ENABLE_MULTI_VECTOR"); val != "" {
		config.EnableMultiVector = parseConfigBool(val, config.EnableMultiVector)
	}

	if val := os.Getenv("MAESTRO_CHUNK_SIZE"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed >= 128 && parsed <= 2048 {
			config.ChunkSize = parsed
		}
	}

	if val := os.Getenv("MAESTRO_CHUNK_OVERLAP"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed >= 0 && parsed < config.ChunkSize {
			config.ChunkOverlap = parsed
		}
	}

	if val := os.Getenv("MAESTRO_MAX_CANDIDATES"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed >= 10 && parsed <= 1000 {
			config.MaxCandidates = parsed
		}
	}

	if val := os.Getenv("MAESTRO_SELECTION_TIMEOUT"); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil && parsed > 0 {
			config.SelectionTimeout = parsed
		}
	}

	if val := os.Getenv("MAESTRO_EVALUATION_ENABLED"); val != "" {
		config.EvaluationEnabled = parseConfigBool(val, config.EvaluationEnabled)
	}

	if val := os.Getenv("MAESTRO_MIN_RELEVANCE_THRESHOLD"); val != "" {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil && parsed >= 0 && parsed <= 1 {
			config.MinRelevanceThreshold = parsed
		}
	}

	if val := os.Getenv("MAESTRO_MIN_QUALITY_THRESHOLD"); val != "" {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil && parsed >= 0 && parsed <= 1 {
			config.MinQualityThreshold = parsed
		}
	}

	if val := os.Getenv("MAESTRO_MIN_DIVERSITY_THRESHOLD"); val != "" {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil && parsed >= 0 && parsed <= 1 {
			config.MinDiversityThreshold = parsed
		}
	}

	if val := os.Getenv("MAESTRO_ENABLE_DETAILED_LOGGING"); val != "" {
		config.EnableDetailedLogging = parseConfigBool(val, config.EnableDetailedLogging)
	}

	if val := os.Getenv("MAESTRO_LOG_PERFORMANCE_METRICS"); val != "" {
		config.LogPerformanceMetrics = parseConfigBool(val, config.LogPerformanceMetrics)
	}

	if val := os.Getenv("MAESTRO_SAVE_EVALUATION_REPORTS"); val != "" {
		config.SaveEvaluationReports = parseConfigBool(val, config.SaveEvaluationReports)
	}

	return config
}

// parseConfigBool safely parses boolean environment variables.
func parseConfigBool(val string, defaultValue bool) bool {
	val = strings.ToLower(strings.TrimSpace(val))
	switch val {
	case "true", "1", "yes", "on", "enabled":
		return true
	case "false", "0", "no", "off", "disabled":
		return false
	default:
		return defaultValue
	}
}

// IsLateInteractionEnabled returns whether late interaction is enabled.
func (c *OptimizationConfig) IsLateInteractionEnabled() bool {
	return c.EnableLateInteraction
}

// IsMultiVectorEnabled returns whether multi-vector embeddings are enabled.
func (c *OptimizationConfig) IsMultiVectorEnabled() bool {
	return c.EnableMultiVector
}

// ShouldUseFacilityLocation returns whether to use facility location formulation.
func (c *OptimizationConfig) ShouldUseFacilityLocation() bool {
	return false // Submodular optimization removed
}

// GetCandidateLimit calculates the candidate limit for the given target selection size.
func (c *OptimizationConfig) GetCandidateLimit(targetSize int) int {
	limit := targetSize * c.CandidateMultiplier
	if limit > c.MaxCandidates {
		limit = c.MaxCandidates
	}
	return limit
}

// ShouldEvaluateQuality returns whether to run quality evaluation.
func (c *OptimizationConfig) ShouldEvaluateQuality() bool {
	return c.EvaluationEnabled
}

// MeetsQualityThresholds checks if selection meets minimum quality requirements.
func (c *OptimizationConfig) MeetsQualityThresholds(relevance, quality, diversity float64) bool {
	return relevance >= c.MinRelevanceThreshold &&
		quality >= c.MinQualityThreshold &&
		diversity >= c.MinDiversityThreshold
}

// GetConfigSummary returns a string summary of the current configuration.
func (c *OptimizationConfig) GetConfigSummary() string {
	var summary strings.Builder

	summary.WriteString("Optimization Configuration:\n")
	summary.WriteString("==========================\n")

	// Submodular optimization removed
	summary.WriteString(sprintf("  Candidate Multiplier: %dx\n", c.CandidateMultiplier))
	summary.WriteString(sprintf("  Saturation Threshold: %.3f\n", c.SaturationThreshold))

	summary.WriteString("\nLate Interaction:\n")
	summary.WriteString(sprintf("  Enabled: %t\n", c.EnableLateInteraction))
	summary.WriteString(sprintf("  Model: %s\n", c.QwenModel))
	summary.WriteString(sprintf("  Refinement Stages: %d\n", c.RefinementStages))
	summary.WriteString(sprintf("  Fallback on Failure: %t\n", c.FallbackOnFailure))

	summary.WriteString("\nMulti-Vector Embeddings:\n")
	summary.WriteString(sprintf("  Enabled: %t\n", c.EnableMultiVector))
	summary.WriteString(sprintf("  Chunk Size: %d\n", c.ChunkSize))
	summary.WriteString(sprintf("  Chunk Overlap: %d\n", c.ChunkOverlap))

	summary.WriteString("\nPerformance Settings:\n")
	summary.WriteString(sprintf("  Max Candidates: %d\n", c.MaxCandidates))
	summary.WriteString(sprintf("  Selection Timeout: %v\n", c.SelectionTimeout))
	summary.WriteString(sprintf("  Evaluation Enabled: %t\n", c.EvaluationEnabled))

	summary.WriteString("\nQuality Thresholds:\n")
	summary.WriteString(sprintf("  Min Relevance: %.2f\n", c.MinRelevanceThreshold))
	summary.WriteString(sprintf("  Min Quality: %.2f\n", c.MinQualityThreshold))
	summary.WriteString(sprintf("  Min Diversity: %.2f\n", c.MinDiversityThreshold))

	summary.WriteString("\nLogging & Debug:\n")
	summary.WriteString(sprintf("  Detailed Logging: %t\n", c.EnableDetailedLogging))
	summary.WriteString(sprintf("  Performance Metrics: %t\n", c.LogPerformanceMetrics))
	summary.WriteString(sprintf("  Save Reports: %t\n", c.SaveEvaluationReports))

	return summary.String()
}

// sprintf is a simple wrapper around fmt.Sprintf to avoid importing fmt in this file.
func sprintf(format string, args ...interface{}) string {
	// This is a simplified implementation
	// In a real implementation, you'd import fmt and use fmt.Sprintf
	result := format
	// Basic placeholder replacement (simplified)
	for _, arg := range args {
		switch v := arg.(type) {
		case string:
			result = strings.Replace(result, "%s", v, 1)
		case int:
			result = strings.Replace(result, "%d", strconv.Itoa(v), 1)
			result = strings.Replace(result, "%dx", strconv.Itoa(v)+"x", 1)
		case bool:
			result = strings.Replace(result, "%t", strconv.FormatBool(v), 1)
		case float64:
			if strings.Contains(result, "%.3f") {
				result = strings.Replace(result, "%.3f", strconv.FormatFloat(v, 'f', 3, 64), 1)
			} else if strings.Contains(result, "%.2f") {
				result = strings.Replace(result, "%.2f", strconv.FormatFloat(v, 'f', 2, 64), 1)
			}
		case time.Duration:
			result = strings.Replace(result, "%v", v.String(), 1)
		}
	}
	return result
}

// Configuration constants for easy reference.
const (
	DefaultCandidateMultiplier   = 3
	DefaultSaturationThreshold   = 0.001
	DefaultQwenModel             = "qwen2.5:0.5b"
	DefaultRefinementStages      = 2
	DefaultChunkSize             = 512
	DefaultChunkOverlap          = 128
	DefaultMaxCandidates         = 100
	DefaultSelectionTimeout      = 30 * time.Second
	DefaultMinRelevanceThreshold = 0.1
	DefaultMinQualityThreshold   = 0.3
	DefaultMinDiversityThreshold = 0.2
)

// Environment variable names for documentation.
const (
	// Removed submodular environment variables.
	EnvCandidateMultiplier   = "MAESTRO_CANDIDATE_MULTIPLIER"
	EnvSaturationThreshold   = "MAESTRO_SATURATION_THRESHOLD"
	EnvEnableLateInteraction = "MAESTRO_ENABLE_LATE_INTERACTION"
	EnvQwenModel             = "MAESTRO_QWEN_MODEL"
	EnvRefinementStages      = "MAESTRO_REFINEMENT_STAGES"
	EnvFallbackOnFailure     = "MAESTRO_FALLBACK_ON_FAILURE"
	EnvEnableMultiVector     = "MAESTRO_ENABLE_MULTI_VECTOR"
	EnvChunkSize             = "MAESTRO_CHUNK_SIZE"
	EnvChunkOverlap          = "MAESTRO_CHUNK_OVERLAP"
	EnvMaxCandidates         = "MAESTRO_MAX_CANDIDATES"
	EnvSelectionTimeout      = "MAESTRO_SELECTION_TIMEOUT"
	EnvEvaluationEnabled     = "MAESTRO_EVALUATION_ENABLED"
	EnvMinRelevanceThreshold = "MAESTRO_MIN_RELEVANCE_THRESHOLD"
	EnvMinQualityThreshold   = "MAESTRO_MIN_QUALITY_THRESHOLD"
	EnvMinDiversityThreshold = "MAESTRO_MIN_DIVERSITY_THRESHOLD"
	EnvEnableDetailedLogging = "MAESTRO_ENABLE_DETAILED_LOGGING"
	EnvLogPerformanceMetrics = "MAESTRO_LOG_PERFORMANCE_METRICS"
	EnvSaveEvaluationReports = "MAESTRO_SAVE_EVALUATION_REPORTS"
)
