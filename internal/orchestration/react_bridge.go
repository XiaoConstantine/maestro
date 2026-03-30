package orchestration

import (
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/agent"
	"github.com/XiaoConstantine/maestro/internal/search"
)

// createReActAgent is a compatibility bridge for the explicit legacy fallback path.
// The primary interactive runtime in Maestro is native.Agent.
func createReActAgent(id string, searchTool *search.SimpleSearchTool, logger *logging.Logger) (*agent.UnifiedReActAgent, error) {
	return agent.NewUnifiedReActAgent(id, searchTool, logger)
}
