// Package ace provides ACE (Agentic Context Engineering) integration for Maestro.
// ACE enables self-improving agents by maintaining a playbook of strategies,
// patterns, and mistakes that evolves over time.
package ace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/ace"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// MaestroACEManager wraps the dspy-go ACE Manager with Maestro-specific functionality.
// It coordinates learning across review and search agents.
type MaestroACEManager struct {
	manager    *ace.Manager
	config     *MaestroACEConfig
	logger     *logging.Logger
	adapters   []ace.Adapter
	mu         sync.RWMutex
	isEnabled  bool
	basePath   string
}

// MaestroACEConfig holds ACE configuration for Maestro.
type MaestroACEConfig struct {
	// Enabled controls whether ACE learning is active
	Enabled bool `json:"enabled"`

	// LearningsDir is the directory for storing learnings files
	LearningsDir string `json:"learnings_dir"`

	// ReviewLearningsFile is the filename for review-specific learnings
	ReviewLearningsFile string `json:"review_learnings_file"`

	// SearchLearningsFile is the filename for search-specific learnings
	SearchLearningsFile string `json:"search_learnings_file"`

	// AsyncReflection enables background trajectory processing
	AsyncReflection bool `json:"async_reflection"`

	// CurationFrequency is how many trajectories to batch before curating
	CurationFrequency int `json:"curation_frequency"`

	// MinConfidence is the minimum confidence for adding new insights
	MinConfidence float64 `json:"min_confidence"`

	// MaxTokens is the token budget for learnings files
	MaxTokens int `json:"max_tokens"`

	// PruneMinRatio is the success rate below which learnings are pruned
	PruneMinRatio float64 `json:"prune_min_ratio"`

	// SimilarityThreshold is the Jaccard threshold for deduplication
	SimilarityThreshold float64 `json:"similarity_threshold"`
}

// DefaultMaestroACEConfig returns sensible defaults for Maestro.
func DefaultMaestroACEConfig() *MaestroACEConfig {
	return &MaestroACEConfig{
		Enabled:             true,
		LearningsDir:        "learnings",
		ReviewLearningsFile: "reviews.md",
		SearchLearningsFile: "search.md",
		AsyncReflection:     true,
		CurationFrequency:   5,
		MinConfidence:       0.6,
		MaxTokens:           50000,
		PruneMinRatio:       0.25,
		SimilarityThreshold: 0.80,
	}
}

// NewMaestroACEManager creates a new ACE manager for Maestro.
func NewMaestroACEManager(basePath string, config *MaestroACEConfig, logger *logging.Logger) (*MaestroACEManager, error) {
	if config == nil {
		config = DefaultMaestroACEConfig()
	}

	if !config.Enabled {
		return &MaestroACEManager{
			config:    config,
			logger:    logger,
			isEnabled: false,
			basePath:  basePath,
		}, nil
	}

	// Create learnings directory
	learningsDir := filepath.Join(basePath, config.LearningsDir)
	if err := os.MkdirAll(learningsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create learnings directory: %w", err)
	}

	return &MaestroACEManager{
		config:    config,
		logger:    logger,
		adapters:  make([]ace.Adapter, 0),
		isEnabled: true,
		basePath:  basePath,
	}, nil
}

// RegisterAdapter adds an adapter for extracting insights from existing systems.
func (m *MaestroACEManager) RegisterAdapter(adapter ace.Adapter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.adapters = append(m.adapters, adapter)
}

// GetReviewManager returns an ACE manager configured for code review.
func (m *MaestroACEManager) GetReviewManager(ctx context.Context) (*ace.Manager, error) {
	if !m.isEnabled {
		return nil, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.manager != nil {
		return m.manager, nil
	}

	aceConfig := ace.DefaultConfig()
	aceConfig.Enabled = true
	aceConfig.LearningsPath = filepath.Join(m.basePath, m.config.LearningsDir, m.config.ReviewLearningsFile)
	aceConfig.AsyncReflection = m.config.AsyncReflection
	aceConfig.CurationFrequency = m.config.CurationFrequency
	aceConfig.MinConfidence = m.config.MinConfidence
	aceConfig.MaxTokens = m.config.MaxTokens
	aceConfig.PruneMinRatio = m.config.PruneMinRatio
	aceConfig.SimilarityThreshold = m.config.SimilarityThreshold

	// Create unified reflector with adapters
	reflector := ace.NewUnifiedReflector(m.adapters, ace.NewSimpleReflector())

	manager, err := ace.NewManager(aceConfig, reflector)
	if err != nil {
		return nil, fmt.Errorf("failed to create ACE manager: %w", err)
	}

	m.manager = manager
	m.logger.Info(ctx, "ACE review manager initialized with learnings at %s", aceConfig.LearningsPath)

	return manager, nil
}

// GetSearchManager returns an ACE manager configured for search operations.
func (m *MaestroACEManager) GetSearchManager(ctx context.Context) (*ace.Manager, error) {
	if !m.isEnabled {
		return nil, nil
	}

	aceConfig := ace.DefaultConfig()
	aceConfig.Enabled = true
	aceConfig.LearningsPath = filepath.Join(m.basePath, m.config.LearningsDir, m.config.SearchLearningsFile)
	aceConfig.AsyncReflection = m.config.AsyncReflection
	aceConfig.CurationFrequency = m.config.CurationFrequency
	aceConfig.MinConfidence = m.config.MinConfidence
	aceConfig.MaxTokens = m.config.MaxTokens
	aceConfig.PruneMinRatio = m.config.PruneMinRatio
	aceConfig.SimilarityThreshold = m.config.SimilarityThreshold

	// Create unified reflector with adapters
	reflector := ace.NewUnifiedReflector(m.adapters, ace.NewSimpleReflector())

	manager, err := ace.NewManager(aceConfig, reflector)
	if err != nil {
		return nil, fmt.Errorf("failed to create ACE search manager: %w", err)
	}

	m.logger.Info(ctx, "ACE search manager initialized with learnings at %s", aceConfig.LearningsPath)

	return manager, nil
}

// GetLearningsContext returns formatted learnings for context injection.
func (m *MaestroACEManager) GetLearningsContext() string {
	if !m.isEnabled || m.manager == nil {
		return ""
	}
	return m.manager.LearningsContext()
}

// StartTrajectory begins recording a new execution trajectory.
func (m *MaestroACEManager) StartTrajectory(agentID, taskType, query string) *ace.TrajectoryRecorder {
	if !m.isEnabled || m.manager == nil {
		return nil
	}
	return m.manager.StartTrajectory(agentID, taskType, query)
}

// EndTrajectory finalizes a trajectory with the given outcome.
func (m *MaestroACEManager) EndTrajectory(ctx context.Context, recorder *ace.TrajectoryRecorder, outcome ace.Outcome) {
	if !m.isEnabled || m.manager == nil || recorder == nil {
		return
	}
	m.manager.EndTrajectory(ctx, recorder, outcome)
}

// Metrics returns current ACE performance metrics.
func (m *MaestroACEManager) Metrics() map[string]int64 {
	if !m.isEnabled || m.manager == nil {
		return map[string]int64{}
	}
	return m.manager.Metrics()
}

// IsEnabled returns whether ACE is enabled.
func (m *MaestroACEManager) IsEnabled() bool {
	return m.isEnabled
}

// Close shuts down the ACE manager and flushes pending work.
func (m *MaestroACEManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.manager != nil {
		return m.manager.Close()
	}
	return nil
}
