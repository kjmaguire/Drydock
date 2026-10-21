package mcp

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/claude"
)

// The Claude adapter stops a session whose gateway failed to connect, looking for the server by this name.
func TestTheClaudeAdapterLooksForTheGatewayByItsName(t *testing.T) {
	if claude.GatewayServer != ServerName {
		t.Fatalf("claude.GatewayServer %q, the gateway writes %q", claude.GatewayServer, ServerName)
	}
}
