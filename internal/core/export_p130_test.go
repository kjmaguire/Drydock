package core

import (
	"context"

	"github.com/kjmaguire/drydock/internal/mcp"
)

// TestRequestJoin is the Workspace room tool's request_join as an agent's run calls it (decision P130).
func (c *Core) TestRequestJoin(ctx context.Context, projectID, agentID, channel, reason string) (string, error) {
	return mcpRoom{c: c}.RequestJoin(ctx, mcp.RunInfo{ProjectID: projectID, AgentID: agentID}, channel, reason)
}
