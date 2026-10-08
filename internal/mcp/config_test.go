package mcp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testEndpoint() Endpoint {
	return Endpoint{RunID: "run-9", URL: "http://127.0.0.1:4567/mcp/run-9", Token: strings.Repeat("ab", 32), ShimCommand: []string{`C:\Program Files\Drydock\drydock.exe`, "mcp-shim"}}
}

func TestConfigForClaude(t *testing.T) {
	ep := testEndpoint()
	cfg, err := ConfigFor("claude", ep)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Verified || cfg.Transport != TransportShim {
		t.Fatalf("%+v", cfg)
	}
	var doc struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfg.JSON, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Servers) != 1 {
		t.Fatalf("exactly one entry expected: %s", cfg.JSON)
	}
	e := doc.Servers["drydock"]
	if e.Type != "stdio" || e.Command != ep.ShimCommand[0] || len(e.Args) != 1 || e.Args[0] != "mcp-shim" || e.Env[EnvMCPURL] != ep.URL || e.Env[EnvMCPToken] != ep.Token {
		t.Fatalf("entry: %+v", e)
	}
	if got := strings.Join(cfg.ArgsFor("/runs/r/mcp.json"), " "); got != "--mcp-config /runs/r/mcp.json --strict-mcp-config" {
		t.Fatalf("args: %s", got)
	}
	if len(cfg.AllowedTools) != 1 || cfg.AllowedTools[0] != "mcp__drydock__*" || ClaudeToolName("read_range") != "mcp__drydock__read_range" || ClaudeToolName("gh__list_issues") != "mcp__drydock__gh__list_issues" {
		t.Fatalf("allowed tools: %v", cfg.AllowedTools)
	}
	// direct HTTP: the header route is unverified for Claude
	h, err := ConfigFor("claude", ep, ConfigOptions{Transport: TransportDirectHTTP})
	if err != nil || h.Verified || !strings.Contains(string(h.JSON), `"type": "http"`) || !strings.Contains(string(h.JSON), "Bearer "+ep.Token) {
		t.Fatalf("http: %v %s", err, h.JSON)
	}
	// the file works as the acp adapter's input too: same Claude-style document
	path := filepath.Join(t.TempDir(), "runs", "r", "mcp.json")
	if err := WriteConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != string(cfg.JSON) {
		t.Fatal("written file differs")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("config file mode %v", st.Mode().Perm())
		}
		ds, _ := os.Stat(filepath.Dir(path))
		if ds.Mode().Perm() != 0o700 {
			t.Fatalf("config dir mode %v", ds.Mode().Perm())
		}
	}
}

func TestConfigForOthersAreMarkedUnverified(t *testing.T) {
	ep := testEndpoint()
	for _, p := range []string{"codex", "gemini", "opencode", "goose", "custom"} {
		cfg, err := ConfigFor(p, ep, ConfigOptions{Tools: []string{"read_range"}})
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if cfg.Verified {
			t.Errorf("%s must be marked unverified", p)
		}
		if !strings.Contains(strings.Join(cfg.Notes, " "), "UNVERIFIED") {
			t.Errorf("%s: notes must say UNVERIFIED: %v", p, cfg.Notes)
		}
		if len(cfg.JSON) == 0 {
			t.Errorf("%s: no ACP-route JSON", p)
		}
	}
	codex, _ := ConfigFor("codex", ep, ConfigOptions{Tools: []string{"read_range"}})
	joined := strings.Join(codex.Args, " ")
	if !strings.Contains(joined, "mcp_servers.drydock.url=") || !strings.Contains(joined, "bearer_token_env_var=\"DRYDOCK_MCP_TOKEN\"") || !strings.Contains(joined, `enabled_tools=["read_range"]`) {
		t.Fatalf("codex args: %s", joined)
	}
	for _, a := range codex.Args {
		if strings.Contains(a, ep.Token) {
			t.Fatal("the token must never be on a command line")
		}
	}
	if len(codex.Env) != 1 || codex.Env[0] != "DRYDOCK_MCP_TOKEN="+ep.Token {
		t.Fatalf("codex env: %v", codex.Env)
	}
	gem, _ := ConfigFor("gemini", ep)
	if !strings.Contains(gem.Inline, `"httpUrl"`) || strings.Contains(gem.Inline, `"url"`) {
		t.Fatalf("gemini must use httpUrl (streamable HTTP; url is SSE): %s", gem.Inline)
	}
	oc, _ := ConfigFor("opencode", ep)
	if !strings.HasPrefix(oc.Env[0], "OPENCODE_CONFIG_CONTENT=") || !strings.Contains(oc.Inline, `"type":"local"`) {
		t.Fatalf("opencode: %v", oc.Env)
	}
	if _, err := ConfigFor("antigravity", ep); err != ErrMCPUnknown {
		t.Fatalf("antigravity: %v", err)
	}
	if _, err := ConfigFor("nosuchcli", ep); err == nil {
		t.Fatal("unknown provider")
	}
	if _, err := ConfigFor("claude", Endpoint{URL: "u"}); err == nil {
		t.Fatal("missing token")
	}
	if _, err := ConfigFor("claude", ep, ConfigOptions{Transport: "carrier-pigeon"}); err == nil {
		t.Fatal("bad transport")
	}
}

func TestEndpointNeverPrintsItsToken(t *testing.T) {
	ep := testEndpoint()
	for _, s := range []string{
		fmt.Sprint(ep), fmt.Sprintf("%v", ep), fmt.Sprintf("%+v", ep), fmt.Sprintf("%#v", ep), fmt.Sprintf("%s", ep),
	} {
		if strings.Contains(s, ep.Token) {
			t.Fatalf("token printed: %s", s)
		}
	}
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("run", "endpoint", ep)
	if strings.Contains(sb.String(), ep.Token) {
		t.Fatalf("token logged: %s", sb.String())
	}
}
