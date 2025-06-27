package main

import (
	"os"
	"strings"
)

// EnhancedFeatures manages feature flags for advanced DSPy-Go capabilities
type EnhancedFeatures struct {
	// Phase 1 Features
	AdvancedReasoning    bool
	ConsensusValidation  bool
	CommentRefinement    bool
	
	// Phase 2 Features
	DeclarativeWorkflows        bool
	IntelligentParallelProcessing bool // Phase 2.2 Intelligent Parallel Processing
	AdvancedMemory              bool
	AdaptiveStrategy            bool
	ResourceMonitoring          bool
	AdaptiveResourceManagement  bool // Adaptive resource monitoring
	LoadBalancing               bool // Intelligent load balancing
	CircuitBreaking             bool // Circuit breaker patterns
	
	// Phase 3 Features (for future use)
	ReactiveProcessing   bool
	ConversationalMode   bool
	LearningAdaptation   bool
	
	// Fallback settings
	FallbackToLegacy     bool
	EnableMetrics        bool
	EnableDebugLogging   bool
}

// DefaultEnhancedFeatures returns the default feature configuration
func DefaultEnhancedFeatures() *EnhancedFeatures {
	return &EnhancedFeatures{
		// Phase 1 - enabled by default for gradual rollout
		AdvancedReasoning:    true,
		ConsensusValidation:  true,
		CommentRefinement:    true,
		
		// Phase 2 - enabled for advanced users
		DeclarativeWorkflows:        true,
		IntelligentParallelProcessing: true,
		AdvancedMemory:              true,
		AdaptiveStrategy:            true,
		ResourceMonitoring:          true,
		AdaptiveResourceManagement:  true,
		LoadBalancing:               true,
		CircuitBreaking:             true,
		
		// Phase 3 - disabled until implemented
		ReactiveProcessing:   false,
		ConversationalMode:   false,
		LearningAdaptation:   false,
		
		// Safety features
		FallbackToLegacy:     true,
		EnableMetrics:        true,
		EnableDebugLogging:   false,
	}
}

// LoadEnhancedFeaturesFromEnv loads feature flags from environment variables
func LoadEnhancedFeaturesFromEnv() *EnhancedFeatures {
	features := DefaultEnhancedFeatures()
	
	// Phase 1 Features
	if val := os.Getenv("MAESTRO_ADVANCED_REASONING"); val != "" {
		features.AdvancedReasoning = parseBool(val, features.AdvancedReasoning)
	}
	if val := os.Getenv("MAESTRO_CONSENSUS_VALIDATION"); val != "" {
		features.ConsensusValidation = parseBool(val, features.ConsensusValidation)
	}
	if val := os.Getenv("MAESTRO_COMMENT_REFINEMENT"); val != "" {
		features.CommentRefinement = parseBool(val, features.CommentRefinement)
	}
	
	// Phase 2 Features
	if val := os.Getenv("MAESTRO_DECLARATIVE_WORKFLOWS"); val != "" {
		features.DeclarativeWorkflows = parseBool(val, features.DeclarativeWorkflows)
	}
	if val := os.Getenv("MAESTRO_INTELLIGENT_PARALLEL_PROCESSING"); val != "" {
		features.IntelligentParallelProcessing = parseBool(val, features.IntelligentParallelProcessing)
	}
	if val := os.Getenv("MAESTRO_ADVANCED_MEMORY"); val != "" {
		features.AdvancedMemory = parseBool(val, features.AdvancedMemory)
	}
	if val := os.Getenv("MAESTRO_ADAPTIVE_STRATEGY"); val != "" {
		features.AdaptiveStrategy = parseBool(val, features.AdaptiveStrategy)
	}
	if val := os.Getenv("MAESTRO_RESOURCE_MONITORING"); val != "" {
		features.ResourceMonitoring = parseBool(val, features.ResourceMonitoring)
	}
	if val := os.Getenv("MAESTRO_ADAPTIVE_RESOURCE_MANAGEMENT"); val != "" {
		features.AdaptiveResourceManagement = parseBool(val, features.AdaptiveResourceManagement)
	}
	if val := os.Getenv("MAESTRO_LOAD_BALANCING"); val != "" {
		features.LoadBalancing = parseBool(val, features.LoadBalancing)
	}
	if val := os.Getenv("MAESTRO_CIRCUIT_BREAKING"); val != "" {
		features.CircuitBreaking = parseBool(val, features.CircuitBreaking)
	}
	
	// Phase 3 Features
	if val := os.Getenv("MAESTRO_REACTIVE_PROCESSING"); val != "" {
		features.ReactiveProcessing = parseBool(val, features.ReactiveProcessing)
	}
	if val := os.Getenv("MAESTRO_CONVERSATIONAL_MODE"); val != "" {
		features.ConversationalMode = parseBool(val, features.ConversationalMode)
	}
	if val := os.Getenv("MAESTRO_LEARNING_ADAPTATION"); val != "" {
		features.LearningAdaptation = parseBool(val, features.LearningAdaptation)
	}
	
	// Safety and debugging features
	if val := os.Getenv("MAESTRO_FALLBACK_LEGACY"); val != "" {
		features.FallbackToLegacy = parseBool(val, features.FallbackToLegacy)
	}
	if val := os.Getenv("MAESTRO_ENABLE_METRICS"); val != "" {
		features.EnableMetrics = parseBool(val, features.EnableMetrics)
	}
	if val := os.Getenv("MAESTRO_DEBUG_LOGGING"); val != "" {
		features.EnableDebugLogging = parseBool(val, features.EnableDebugLogging)
	}
	
	return features
}

// GetFeatureProfile returns a predefined feature profile
func GetFeatureProfile(profile string) *EnhancedFeatures {
	switch strings.ToLower(profile) {
	case "conservative":
		return &EnhancedFeatures{
			AdvancedReasoning:             false,
			ConsensusValidation:           false,
			CommentRefinement:             false,
			DeclarativeWorkflows:          false,
			IntelligentParallelProcessing: false,
			AdvancedMemory:                false,
			AdaptiveStrategy:              false,
			ResourceMonitoring:            false,
			AdaptiveResourceManagement:    false,
			LoadBalancing:                 false,
			CircuitBreaking:               false,
			ReactiveProcessing:            false,
			ConversationalMode:            false,
			LearningAdaptation:            false,
			FallbackToLegacy:              true,
			EnableMetrics:                 true,
			EnableDebugLogging:            false,
		}
		
	case "experimental":
		return &EnhancedFeatures{
			AdvancedReasoning:             true,
			ConsensusValidation:           true,
			CommentRefinement:             true,
			DeclarativeWorkflows:          true,
			IntelligentParallelProcessing: true,
			AdvancedMemory:                true,
			AdaptiveStrategy:              true,
			ResourceMonitoring:            true,
			AdaptiveResourceManagement:    true,
			LoadBalancing:                 true,
			CircuitBreaking:               true,
			ReactiveProcessing:            false, // Still too experimental
			ConversationalMode:            false,
			LearningAdaptation:            false,
			FallbackToLegacy:              true,
			EnableMetrics:                 true,
			EnableDebugLogging:            true,
		}
		
	case "phase1":
		return &EnhancedFeatures{
			AdvancedReasoning:             true,
			ConsensusValidation:           true,
			CommentRefinement:             true,
			DeclarativeWorkflows:          false,
			IntelligentParallelProcessing: false,
			AdvancedMemory:                false,
			AdaptiveStrategy:              false,
			ResourceMonitoring:            false,
			AdaptiveResourceManagement:    false,
			LoadBalancing:                 false,
			CircuitBreaking:               false,
			ReactiveProcessing:            false,
			ConversationalMode:            false,
			LearningAdaptation:            false,
			FallbackToLegacy:              true,
			EnableMetrics:                 true,
			EnableDebugLogging:            false,
		}
		
	case "phase2":
		return &EnhancedFeatures{
			AdvancedReasoning:             true,
			ConsensusValidation:           true,
			CommentRefinement:             true,
			DeclarativeWorkflows:          true,
			IntelligentParallelProcessing: true,
			AdvancedMemory:                true,
			AdaptiveStrategy:              true,
			ResourceMonitoring:            true,
			AdaptiveResourceManagement:    true,
			LoadBalancing:                 true,
			CircuitBreaking:               true,
			ReactiveProcessing:            false,
			ConversationalMode:            false,
			LearningAdaptation:            false,
			FallbackToLegacy:              true,
			EnableMetrics:                 true,
			EnableDebugLogging:            false,
		}
		
	case "all":
		return &EnhancedFeatures{
			AdvancedReasoning:             true,
			ConsensusValidation:           true,
			CommentRefinement:             true,
			DeclarativeWorkflows:          true,
			IntelligentParallelProcessing: true,
			AdvancedMemory:                true,
			AdaptiveStrategy:              true,
			ResourceMonitoring:            true,
			AdaptiveResourceManagement:    true,
			LoadBalancing:                 true,
			CircuitBreaking:               true,
			ReactiveProcessing:            true,
			ConversationalMode:            true,
			LearningAdaptation:            true,
			FallbackToLegacy:              true,
			EnableMetrics:                 true,
			EnableDebugLogging:            true,
		}
		
	default:
		return DefaultEnhancedFeatures()
	}
}

// IsEnhancedProcessingEnabled checks if any enhanced features are enabled
func (f *EnhancedFeatures) IsEnhancedProcessingEnabled() bool {
	return f.AdvancedReasoning || f.ConsensusValidation || f.CommentRefinement ||
		   f.DeclarativeWorkflows || f.IntelligentParallelProcessing || f.AdvancedMemory ||
		   f.ReactiveProcessing || f.ConversationalMode || f.LearningAdaptation
}

// GetPhaseLevel returns the highest enabled phase level
func (f *EnhancedFeatures) GetPhaseLevel() int {
	if f.ReactiveProcessing || f.ConversationalMode || f.LearningAdaptation {
		return 3
	}
	if f.DeclarativeWorkflows || f.IntelligentParallelProcessing || f.AdvancedMemory {
		return 2
	}
	if f.AdvancedReasoning || f.ConsensusValidation || f.CommentRefinement {
		return 1
	}
	return 0
}

// ValidateConfiguration checks for valid feature combinations
func (f *EnhancedFeatures) ValidateConfiguration() []string {
	var warnings []string
	
	// Check for dependencies
	if f.ConsensusValidation && !f.AdvancedReasoning {
		warnings = append(warnings, "Consensus validation works best with advanced reasoning enabled")
	}
	
	if f.CommentRefinement && !f.ConsensusValidation {
		warnings = append(warnings, "Comment refinement works best with consensus validation enabled")
	}
	
	if f.ReactiveProcessing && !f.DeclarativeWorkflows {
		warnings = append(warnings, "Reactive processing requires declarative workflows")
	}
	
	if f.ConversationalMode && !f.AdvancedMemory {
		warnings = append(warnings, "Conversational mode requires advanced memory management")
	}
	
	// Check for experimental combinations
	phaseLevel := f.GetPhaseLevel()
	if phaseLevel >= 3 && !f.FallbackToLegacy {
		warnings = append(warnings, "Phase 3 features are experimental - consider enabling fallback to legacy")
	}
	
	return warnings
}

// String returns a human-readable description of enabled features
func (f *EnhancedFeatures) String() string {
	var enabled []string
	
	if f.AdvancedReasoning {
		enabled = append(enabled, "Advanced Reasoning")
	}
	if f.ConsensusValidation {
		enabled = append(enabled, "Consensus Validation")
	}
	if f.CommentRefinement {
		enabled = append(enabled, "Comment Refinement")
	}
	if f.DeclarativeWorkflows {
		enabled = append(enabled, "Declarative Workflows")
	}
	if f.IntelligentParallelProcessing {
		enabled = append(enabled, "Intelligent Parallel")
	}
	if f.AdvancedMemory {
		enabled = append(enabled, "Advanced Memory")
	}
	if f.AdaptiveStrategy {
		enabled = append(enabled, "Adaptive Strategy")
	}
	if f.ResourceMonitoring {
		enabled = append(enabled, "Resource Monitoring")
	}
	if f.ReactiveProcessing {
		enabled = append(enabled, "Reactive Processing")
	}
	if f.ConversationalMode {
		enabled = append(enabled, "Conversational Mode")
	}
	if f.LearningAdaptation {
		enabled = append(enabled, "Learning Adaptation")
	}
	
	if len(enabled) == 0 {
		return "Legacy Mode (No Enhanced Features)"
	}
	
	return "Enhanced Features: " + strings.Join(enabled, ", ")
}

// FeatureUsageMetrics tracks which features are being used
type FeatureUsageMetrics struct {
	AdvancedReasoningUsage    int64
	ConsensusValidationUsage  int64
	CommentRefinementUsage    int64
	LegacyFallbackUsage       int64
	TotalProcessingAttempts   int64
}

// TrackFeatureUsage increments usage counters
func (m *FeatureUsageMetrics) TrackFeatureUsage(features *EnhancedFeatures, usedFeature string) {
	m.TotalProcessingAttempts++
	
	switch usedFeature {
	case "advanced_reasoning":
		m.AdvancedReasoningUsage++
	case "consensus_validation":
		m.ConsensusValidationUsage++
	case "comment_refinement":
		m.CommentRefinementUsage++
	case "legacy_fallback":
		m.LegacyFallbackUsage++
	}
}

// GetUsageReport returns a usage report
func (m *FeatureUsageMetrics) GetUsageReport() map[string]interface{} {
	if m.TotalProcessingAttempts == 0 {
		return map[string]interface{}{
			"message": "No processing attempts recorded",
		}
	}
	
	return map[string]interface{}{
		"total_attempts":             m.TotalProcessingAttempts,
		"advanced_reasoning_usage":   float64(m.AdvancedReasoningUsage) / float64(m.TotalProcessingAttempts),
		"consensus_validation_usage": float64(m.ConsensusValidationUsage) / float64(m.TotalProcessingAttempts),
		"comment_refinement_usage":   float64(m.CommentRefinementUsage) / float64(m.TotalProcessingAttempts),
		"legacy_fallback_usage":      float64(m.LegacyFallbackUsage) / float64(m.TotalProcessingAttempts),
	}
}

// Helper functions

func parseBool(value string, defaultValue bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on", "enabled":
		return true
	case "false", "0", "no", "off", "disabled":
		return false
	default:
		return defaultValue
	}
}

// Global feature configuration
var globalFeatures *EnhancedFeatures
var globalMetrics *FeatureUsageMetrics

// InitializeEnhancedFeatures initializes the global feature configuration
func InitializeEnhancedFeatures() {
	// Check for profile environment variable first
	if profile := os.Getenv("MAESTRO_FEATURE_PROFILE"); profile != "" {
		globalFeatures = GetFeatureProfile(profile)
	} else {
		globalFeatures = LoadEnhancedFeaturesFromEnv()
	}
	
	globalMetrics = &FeatureUsageMetrics{}
	
	// Initialize Phase 2 integration if any Phase 2 features are enabled
	if globalFeatures.DeclarativeWorkflows || globalFeatures.IntelligentParallelProcessing || globalFeatures.AdvancedMemory {
		// Note: RAG system needs to be passed from the main application
		// This will be handled in the main.go initialization
		println("INFO: Phase 2 features enabled - will initialize advanced workflows")
	}
	
	// Validate configuration and log warnings
	if warnings := globalFeatures.ValidateConfiguration(); len(warnings) > 0 {
		for _, warning := range warnings {
			// Log warning - in real implementation, use proper logging
			println("WARNING: " + warning)
		}
	}
}

// GetGlobalFeatures returns the global feature configuration
func GetGlobalFeatures() *EnhancedFeatures {
	if globalFeatures == nil {
		InitializeEnhancedFeatures()
	}
	return globalFeatures
}

// GetGlobalMetrics returns the global feature usage metrics
func GetGlobalMetrics() *FeatureUsageMetrics {
	if globalMetrics == nil {
		InitializeEnhancedFeatures()
	}
	return globalMetrics
}

// Convenience functions for feature checks (used by processors)
// Note: Individual processors may have their own versions of these functions

func shouldFallbackToLegacy() bool {
	return GetGlobalFeatures().FallbackToLegacy
}

func isMetricsEnabled() bool {
	return GetGlobalFeatures().EnableMetrics
}

func isDebugLoggingEnabled() bool {
	return GetGlobalFeatures().EnableDebugLogging
}