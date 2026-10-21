package packs

import "github.com/kjmaguire/drydock/internal/mcp"

type mcpRun struct{ ProjectID, AgentID, Role string }

func (r mcpRun) info() mcp.RunInfo {
	return mcp.RunInfo{RunID: "run1", ProjectID: r.ProjectID, AgentID: r.AgentID, Role: r.Role}
}

type policyFragment struct{ p mcp.RunPolicy }
