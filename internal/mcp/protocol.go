package mcp

import (
	"encoding/json"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// protocolNew is the first MCP protocol revision in which every result must carry resultType ("complete") and list results must
// carry cacheScope. Claude Code 2.1.294 speaks it, and rejects results that omit either field: a tools/call result without
// resultType is discarded with "missing required resultType", so the agent sees an error instead of the tool's answer.
const protocolNew = "2026-07-28"

// completeResult sets resultType "complete" on a tools/call result for a request that follows the new protocol. The SDK does this
// only for results returned by its own tool handlers; the gateway and the shim answer tools/call from middleware, so they must do
// it themselves. The field is unexported in the SDK, but its JSON form is public: round-trip the result through JSON.
// Requests on older protocol revisions get the result unchanged, as the SDK itself does.
func completeResult(req *sdk.CallToolRequest, res *sdk.CallToolResult) *sdk.CallToolResult {
	if res == nil || req == nil || req.ProtocolVersion() < protocolNew {
		return res
	}
	b, err := json.Marshal(res)
	if err != nil {
		return res
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return res
	}
	m["resultType"] = json.RawMessage(`"complete"`)
	b, err = json.Marshal(m)
	if err != nil {
		return res
	}
	var out sdk.CallToolResult
	if json.Unmarshal(b, &out) != nil {
		return res
	}
	return &out
}
