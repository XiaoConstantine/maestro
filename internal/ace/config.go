package ace

import (
	"os"
	"strconv"
)

// Configuration environment variables.
const (
	EnvACEEnabled             = "MAESTRO_ACE_ENABLED"
	EnvACELearningsDir        = "MAESTRO_ACE_LEARNINGS_DIR"
	EnvACEAsyncReflection     = "MAESTRO_ACE_ASYNC"
	EnvACECurationFrequency   = "MAESTRO_ACE_CURATION_FREQ"
	EnvACEMinConfidence       = "MAESTRO_ACE_MIN_CONFIDENCE"
	EnvACEMaxTokens           = "MAESTRO_ACE_MAX_TOKENS"
	EnvACEPruneMinRatio       = "MAESTRO_ACE_PRUNE_RATIO"
	EnvACESimilarityThreshold = "MAESTRO_ACE_SIMILARITY"
)

// LoadConfigFromEnv loads ACE configuration from environment variables.
func LoadConfigFromEnv() *MaestroACEConfig {
	config := DefaultMaestroACEConfig()

	// Check if ACE is explicitly disabled
	if val := os.Getenv(EnvACEEnabled); val != "" {
		config.Enabled = parseBool(val, config.Enabled)
	}

	// Override learnings directory
	if val := os.Getenv(EnvACELearningsDir); val != "" {
		config.LearningsDir = val
	}

	// Async reflection setting
	if val := os.Getenv(EnvACEAsyncReflection); val != "" {
		config.AsyncReflection = parseBool(val, config.AsyncReflection)
	}

	// Curation frequency
	if val := os.Getenv(EnvACECurationFrequency); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil && intVal > 0 {
			config.CurationFrequency = intVal
		}
	}

	// Min confidence
	if val := os.Getenv(EnvACEMinConfidence); val != "" {
		if floatVal, err := strconv.ParseFloat(val, 64); err == nil && floatVal >= 0 && floatVal <= 1 {
			config.MinConfidence = floatVal
		}
	}

	// Max tokens
	if val := os.Getenv(EnvACEMaxTokens); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil && intVal > 0 {
			config.MaxTokens = intVal
		}
	}

	// Prune min ratio
	if val := os.Getenv(EnvACEPruneMinRatio); val != "" {
		if floatVal, err := strconv.ParseFloat(val, 64); err == nil && floatVal >= 0 && floatVal <= 1 {
			config.PruneMinRatio = floatVal
		}
	}

	// Similarity threshold
	if val := os.Getenv(EnvACESimilarityThreshold); val != "" {
		if floatVal, err := strconv.ParseFloat(val, 64); err == nil && floatVal >= 0 && floatVal <= 1 {
			config.SimilarityThreshold = floatVal
		}
	}

	return config
}

func parseBool(val string, defaultVal bool) bool {
	switch val {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return defaultVal
	}
}
