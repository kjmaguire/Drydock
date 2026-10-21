package fakeacp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPCall asks the scripted agent to use an MCP server the client attached to the session.
type MCPCall struct {
	Server string         `json:"server"`         // the name given in session/new.mcpServers
	Tool   string         `json:"tool,omitempty"` // empty: list the tools
	Args   map[string]any `json:"args,omitempty"`
}

type headerTransport struct {
	headers map[string]string
	next    http.RoundTripper
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return h.next.RoundTrip(r)
}

// callMCP does what an ACP agent does with session/new.mcpServers: start the stdio server (with the env the client gave) or
// open the HTTP endpoint (with the headers it gave), initialize, then list tools or call one. It returns one line of text.
func callMCP(ctx context.Context, servers []acp.McpServer, c *MCPCall) string {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var tr sdk.Transport
	found := false
	for _, m := range servers {
		switch {
		case m.Stdio != nil && m.Stdio.Name == c.Server:
			cmd := exec.Command(m.Stdio.Command, m.Stdio.Args...)
			cmd.Env = os.Environ()
			for _, e := range m.Stdio.Env {
				cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
			}
			tr, found = &sdk.CommandTransport{Command: cmd}, true
		case m.Http != nil && m.Http.Name == c.Server:
			hdr := map[string]string{}
			for _, h := range m.Http.Headers {
				hdr[h.Name] = h.Value
			}
			tr, found = &sdk.StreamableClientTransport{Endpoint: m.Http.Url, HTTPClient: &http.Client{Transport: headerTransport{hdr, http.DefaultTransport}}}, true
		}
	}
	if !found {
		return fmt.Sprintf("mcp ERR no server %q was attached to this session", c.Server)
	}
	cl := sdk.NewClient(&sdk.Implementation{Name: "fakeacp", Version: "0"}, nil)
	cs, err := cl.Connect(ctx, tr, nil)
	if err != nil {
		return "mcp ERR connect: " + err.Error()
	}
	defer func() { _ = cs.Close() }()
	if c.Tool == "" {
		var names []string
		for t, err := range cs.Tools(ctx, nil) {
			if err != nil {
				return "mcp ERR list: " + err.Error()
			}
			names = append(names, t.Name)
		}
		return "mcp tools " + strings.Join(names, ",")
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: c.Tool, Arguments: c.Args})
	if err != nil {
		return "mcp ERR call: " + err.Error()
	}
	var out []string
	for _, ct := range res.Content {
		if t, ok := ct.(*sdk.TextContent); ok {
			out = append(out, t.Text)
		}
	}
	status := "OK"
	if res.IsError {
		status = "TOOLERR"
	}
	return "mcp " + status + " " + strings.Join(out, " ")
}
