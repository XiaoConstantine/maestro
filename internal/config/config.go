// Package config provides centralized configuration management for Maestro.
package config

import (
	"os"
	"strconv"
)

// Default configuration values.
const (
	DefaultReviewWorkers    = 120
	DefaultIndexWorkers     = 4
	DefaultVectorDimensions = 768
)

// GetReviewWorkers returns the number of concurrent workers for review.
// Configurable via MAESTRO_REVIEW_WORKERS environment variable.
func GetReviewWorkers() int {
	return getEnvInt("MAESTRO_REVIEW_WORKERS", DefaultReviewWorkers)
}

// GetIndexWorkers returns the number of concurrent workers for indexing.
// Configurable via MAESTRO_INDEX_WORKERS environment variable.
func GetIndexWorkers() int {
	return getEnvInt("MAESTRO_INDEX_WORKERS", DefaultIndexWorkers)
}

// GetVectorDimensions returns the embedding vector dimensions.
// Configurable via MAESTRO_VECTOR_DIMENSIONS environment variable.
func GetVectorDimensions() int {
	dims := getEnvInt("MAESTRO_VECTOR_DIMENSIONS", DefaultVectorDimensions)
	// Validate range
	if dims < 1 || dims > 4096 {
		return DefaultVectorDimensions
	}
	return dims
}

// getEnvInt reads an integer from environment variable with a default fallback.
func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil && intVal > 0 {
			return intVal
		}
	}
	return defaultVal
}
