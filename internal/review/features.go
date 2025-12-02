// Package review - Enhanced feature management
package review

import (
	"os"
	"sync"

	"github.com/XiaoConstantine/maestro/internal/types"
)

// globalFeatures holds the global feature configuration.
var globalFeatures *types.EnhancedFeatures
var globalMetrics *FeatureUsageMetrics
var featuresMu sync.RWMutex

// FeatureUsageMetrics tracks feature usage for analytics.
type FeatureUsageMetrics struct {
	mu    sync.Mutex
	usage map[string]int
}

// TrackFeatureUsage records usage of a feature.
func (m *FeatureUsageMetrics) TrackFeatureUsage(features *types.EnhancedFeatures, featureName string) {
	if m == nil || features == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.usage == nil {
		m.usage = make(map[string]int)
	}
	m.usage[featureName]++
}

// InitializeEnhancedFeatures initializes the global feature configuration.
func InitializeEnhancedFeatures() {
	featuresMu.Lock()
	defer featuresMu.Unlock()

	profile := os.Getenv("MAESTRO_FEATURE_PROFILE")
	switch profile {
	case "minimal":
		globalFeatures = &types.EnhancedFeatures{
			AdvancedReasoning:   false,
			ConsensusValidation: false,
			CommentRefinement:   false,
			DeclarativeWorkflows: false,
		}
	case "standard":
		globalFeatures = &types.EnhancedFeatures{
			AdvancedReasoning:   true,
			ConsensusValidation: true,
			CommentRefinement:   true,
			DeclarativeWorkflows: false,
		}
	case "full":
		globalFeatures = &types.EnhancedFeatures{
			AdvancedReasoning:             true,
			ConsensusValidation:           true,
			CommentRefinement:             true,
			DeclarativeWorkflows:          true,
			IntelligentParallelProcessing: true,
			AdvancedMemory:                true,
			AdaptiveStrategy:              true,
			ResourceMonitoring:            true,
		}
	default:
		// Default to standard features
		globalFeatures = &types.EnhancedFeatures{
			AdvancedReasoning:   true,
			ConsensusValidation: true,
			CommentRefinement:   true,
			DeclarativeWorkflows: false,
		}
	}

	// Check environment variable overrides
	if os.Getenv("MAESTRO_DECLARATIVE_WORKFLOWS") == "true" {
		globalFeatures.DeclarativeWorkflows = true
	}
	if os.Getenv("MAESTRO_PARALLEL_PROCESSING") == "true" {
		globalFeatures.IntelligentParallelProcessing = true
	}

	globalMetrics = &FeatureUsageMetrics{
		usage: make(map[string]int),
	}
}

// GetGlobalFeatures returns the global feature configuration.
func GetGlobalFeatures() *types.EnhancedFeatures {
	featuresMu.RLock()
	defer featuresMu.RUnlock()

	if globalFeatures == nil {
		featuresMu.RUnlock()
		InitializeEnhancedFeatures()
		featuresMu.RLock()
	}
	return globalFeatures
}

// SetGlobalFeatures sets the global feature configuration (for testing).
func SetGlobalFeatures(features *types.EnhancedFeatures) {
	featuresMu.Lock()
	defer featuresMu.Unlock()
	globalFeatures = features
}
