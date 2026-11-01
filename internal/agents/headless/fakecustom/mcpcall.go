package fakecustom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPCall asks the stand-in to use a server of its MCP configuration.
type MCPCall struct {
	Server string         `json:"server"`         // the name under mcpServers
	Tool   string         `json:"tool,omitempty"` // empty: list the tools
	Args   map[string]any `json:"args,omitempty"`
}

// mcpServer is one entry of the mcpServers JSON (Claude Code's shape: a stdio command or an http endpoint).
type mcpServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
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

// callMCP does what a CLI does with an MCP configuration file: read the mcpServers JSON (from the --mcp-config path, else MCPFile
// in the working folder), start the stdio server with the env it names (or open the http endpoint with its headers), initialize,
// then list the tools or call one. It returns one line of text.
func callMCP(configPath string, c *MCPCall) string {
	if configPath == "" {
		configPath = MCPFile
	}
	b, err := os.ReadFile(configPath)
	if err != nil {
		return "mcp ERR no configuration: " + err.Error()
	}
	var doc struct {
		MCPServers map[string]mcpServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "mcp ERR unreadable configuration: " + err.Error()
	}
	srv, ok := doc.MCPServers[c.Server]
	if !ok {
		var names []string
		for n := range doc.MCPServers {
			names = append(names, n)
		}
		sort.Strings(names)
		return fmt.Sprintf("mcp ERR no server %q in the configuration (it has %s)", c.Server, strings.Join(names, ","))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var tr sdk.Transport
	switch {
	case srv.Command != "":
		cmd := exec.Command(srv.Command, srv.Args...)
		cmd.Env = os.Environ()
		for k, v := range srv.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		tr = &sdk.CommandTransport{Command: cmd}
	case srv.URL != "":
		tr = &sdk.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: &http.Client{Transport: headerTransport{srv.Headers, http.DefaultTransport}}}
	default:
		return "mcp ERR the server entry has neither a command nor a url"
	}
	cl := sdk.NewClient(&sdk.Implementation{Name: "fakecustom", Version: "0"}, nil)
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
