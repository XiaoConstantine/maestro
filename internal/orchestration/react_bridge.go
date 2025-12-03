package orchestration

import (
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/maestro/internal/agent"
	"github.com/XiaoConstantine/maestro/internal/search"
)

func createReActAgent(id string, searchTool *search.SimpleSearchTool, logger *logging.Logger) (*agent.UnifiedReActAgent, error) {
	return agent.NewUnifiedReActAgent(id, searchTool, logger)
}
