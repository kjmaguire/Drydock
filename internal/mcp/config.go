package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Environment variables the stdio shim reads (docs/mcp.md section 2, transport A).
const (
	EnvMCPURL   = "DRYDOCK_MCP_URL"
	EnvMCPToken = "DRYDOCK_MCP_TOKEN"
)

// Transport choices for ConfigFor.
const (
	// TransportShim: the CLI spawns the stdio shim (default, works for every CLI, needs no header support).
	TransportShim = "stdio-shim"
	// TransportDirectHTTP: the CLI connects to the loopback endpoint itself with a bearer header or env var.
	TransportDirectHTTP = "http"
)

// ErrMCPUnknown is returned for CLIs whose per-run MCP injection is unproven (Antigravity): the Connection records
// "mcp: unknown" and the gateway is not offered (docs/mcp.md section 10).
var ErrMCPUnknown = errors.New("mcp: per-run MCP configuration for this CLI is unverified; the gateway is not offered")

// String masks the token so that an Endpoint can be logged by accident.
func (e Endpoint) String() string {
	return fmt.Sprintf("Endpoint{run=%s url=%s token=***}", e.RunID, e.URL)
}

// GoString implements fmt.GoStringer (%#v).
func (e Endpoint) GoString() string { return e.String() }

// LogValue implements slog.LogValuer.
func (e Endpoint) LogValue() slog.Value {
	return slog.GroupValue(slog.String("run", e.RunID), slog.String("url", e.URL))
}

// ConfigOptions tunes ConfigFor.
type ConfigOptions struct {
	// Transport is TransportShim (default) or TransportDirectHTTP.
	Transport string
	// Tools lists the exposed tool names a CLI-side filter should allow (Codex enabled_tools, Gemini includeTools). The
	// gateway enforces the real allowlist regardless; CLI-side filters are defence in depth only (docs/mcp.md section 3).
	Tools []string
}

// ClientConfig is everything a CLI needs to reach the single "drydock" server of one run. It contains the run token: write
// it only with WriteConfig (mode 0600) or hand Env to the process; never log it.
type ClientConfig struct {
	Provider  string
	Transport string
	// JSON is the Claude-style {"mcpServers":{"drydock":{...}}} document. Claude takes it as --mcp-config <file>; the
	// ACP adapter (internal/agents/acp) reads the same file and converts it to session/new.mcpServers for Codex, Gemini,
	// OpenCode and Goose.
	JSON []byte
	// Args are native CLI arguments. Use ArgsFor to substitute the config file path.
	Args []string
	// Env are extra KEY=VALUE variables for the CLI process (never put the token on a command line).
	Env []string
	// Inline is a provider-specific inline configuration document (OpenCode OPENCODE_CONFIG_CONTENT value, or the Gemini
	// settings.json fragment), when the provider's mechanism is a config string.
	Inline string
	// AllowedTools are CLI-side tool allow patterns in the CLI's own naming (Claude: mcp__drydock__*).
	AllowedTools []string
	// Verified is true only where the mechanism is confirmed by Phase 0 evidence. Even then no live run has exercised
	// the gateway end to end in this repository's tests.
	Verified bool
	Notes    []string
}

const configPathToken = "{config}"

// ArgsFor returns Args with the config file path substituted.
func (c ClientConfig) ArgsFor(configPath string) []string {
	out := make([]string, len(c.Args))
	for i, a := range c.Args {
		out[i] = strings.ReplaceAll(a, configPathToken, configPath)
	}
	return out
}

// ClaudeToolName is the name Claude Code gives a gateway tool (docs/phase0/agent-clis.md section 1).
func ClaudeToolName(exposedTool string) string { return "mcp__" + ServerName + "__" + exposedTool }

// ConfigFor returns the single config entry a CLI needs to reach the run's gateway endpoint. provider is the adapter id:
// claude, codex, gemini, opencode, goose, antigravity, custom.
func ConfigFor(provider string, ep Endpoint, opts ...ConfigOptions) (ClientConfig, error) {
	var o ConfigOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Transport == "" {
		o.Transport = TransportShim
	}
	if o.Transport != TransportShim && o.Transport != TransportDirectHTTP {
		return ClientConfig{}, fmt.Errorf("mcp: unknown transport %q", o.Transport)
	}
	if ep.URL == "" || ep.Token == "" {
		return ClientConfig{}, errors.New("mcp: endpoint has no url or token")
	}
	if o.Transport == TransportShim && len(ep.ShimCommand) == 0 {
		return ClientConfig{}, errors.New("mcp: endpoint has no shim command")
	}
	cfg := ClientConfig{Provider: provider, Transport: o.Transport}
	entry := func() map[string]any {
		if o.Transport == TransportShim {
			return map[string]any{
				"type": "stdio", "command": ep.ShimCommand[0], "args": append([]string{}, ep.ShimCommand[1:]...),
				"env": map[string]string{EnvMCPURL: ep.URL, EnvMCPToken: ep.Token},
			}
		}
		return map[string]any{"type": "http", "url": ep.URL, "headers": map[string]string{"Authorization": "Bearer " + ep.Token}}
	}
	file := func() ([]byte, error) {
		return json.MarshalIndent(map[string]any{"mcpServers": map[string]any{ServerName: entry()}}, "", "  ")
	}
	var err error
	switch provider {
	case "claude":
		if cfg.JSON, err = file(); err != nil {
			return cfg, err
		}
		cfg.Args = []string{"--mcp-config", configPathToken, "--strict-mcp-config"}
		cfg.AllowedTools = []string{ClaudeToolName("*")}
		cfg.Verified = true
		cfg.Notes = append(cfg.Notes,
			"flags and the mcp__<server>__<tool> naming are from the Claude Code help and docs (agent-clis section 1); no live claude run has exercised the gateway yet",
			"-p mode waits at most MCP_TIMEOUT (30 s) for servers: the gateway answers initialize and tools/list without contacting upstreams",
			"--permission-prompt-tool mcp__drydock__permission_prompt is not provided by this package (the permission prompt tool is a workflow concern)")
		if o.Transport == TransportDirectHTTP {
			cfg.Verified = false
			cfg.Notes = append(cfg.Notes, "UNVERIFIED: whether Claude sends a configured Authorization header to an http MCP server; the shim is the default for that reason")
		}
	case "codex":
		if cfg.JSON, err = file(); err != nil {
			return cfg, err
		}
		cfg.Notes = append(cfg.Notes, "UNVERIFIED: no live Codex run; mechanism from agent-clis section 2: ACP session/new.mcpServers (JSON file) or -c mcp_servers.drydock.*")
		cfg.Args = []string{"-c", `mcp_servers.drydock.url="` + ep.URL + `"`, "-c", `mcp_servers.drydock.bearer_token_env_var="` + EnvMCPToken + `"`}
		cfg.Env = []string{EnvMCPToken + "=" + ep.Token}
		if len(o.Tools) > 0 {
			b, _ := json.Marshal(o.Tools)
			cfg.Args = append(cfg.Args, "-c", "mcp_servers.drydock.enabled_tools="+string(b))
		}
		cfg.Transport = TransportDirectHTTP // the -c route is direct HTTP; the JSON file keeps the requested transport
		cfg.Notes = append(cfg.Notes, "the Args/Env route is direct streamable HTTP (bearer_token_env_var); the JSON file uses the requested transport")
	case "gemini":
		if cfg.JSON, err = file(); err != nil {
			return cfg, err
		}
		frag := map[string]any{"httpUrl": ep.URL, "headers": map[string]string{"Authorization": "Bearer " + ep.Token}, "trust": false}
		if len(o.Tools) > 0 {
			frag["includeTools"] = o.Tools
		}
		b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{ServerName: frag}})
		cfg.Inline = string(b)
		cfg.Args = []string{"--allowed-mcp-server-names", ServerName}
		cfg.Notes = append(cfg.Notes, "UNVERIFIED: no live Gemini run; Inline is a settings.json fragment (httpUrl is streamable HTTP, url would be SSE); prefer ACP session/new with the JSON file")
	case "opencode":
		if cfg.JSON, err = file(); err != nil {
			return cfg, err
		}
		var oc map[string]any
		if o.Transport == TransportShim {
			oc = map[string]any{"type": "local", "command": ep.ShimCommand, "environment": map[string]string{EnvMCPURL: ep.URL, EnvMCPToken: ep.Token}, "enabled": true, "timeout": 30000}
		} else {
			oc = map[string]any{"type": "remote", "url": ep.URL, "headers": map[string]string{"Authorization": "Bearer " + ep.Token}, "enabled": true, "timeout": 30000}
		}
		b, _ := json.Marshal(map[string]any{"mcp": map[string]any{ServerName: oc}})
		cfg.Inline = string(b)
		cfg.Env = []string{"OPENCODE_CONFIG_CONTENT=" + string(b)}
		cfg.Notes = append(cfg.Notes, "UNVERIFIED: no live OpenCode run; mechanism from agent-clis section 4 (OPENCODE_CONFIG_CONTENT, or ACP session/new). Its MCP timeout defaults to 5000 ms: raised here to 30000")
	case "goose":
		cfg.Transport = TransportShim
		if len(ep.ShimCommand) == 0 {
			return cfg, errors.New("mcp: endpoint has no shim command")
		}
		o.Transport = TransportShim
		if cfg.JSON, err = file(); err != nil {
			return cfg, err
		}
		cfg.Args = []string{"--with-extension", strings.Join(ep.ShimCommand, " ")}
		cfg.Env = []string{EnvMCPURL + "=" + ep.URL, EnvMCPToken + "=" + ep.Token}
		cfg.Notes = append(cfg.Notes, "UNVERIFIED: no live Goose run; Goose's HTTP flag header support is unverified, so the stdio shim is used; whether the extension inherits these env vars is unverified; the ACP session/new route (JSON file) is preferred; a shim path containing spaces needs adapter-side quoting")
	case "custom":
		if cfg.JSON, err = file(); err != nil {
			return cfg, err
		}
		cfg.Notes = append(cfg.Notes, "UNVERIFIED: custom CLI; the manifest's mcp.inject decides whether this file is used (config_file | acp_session_new | env)")
	case "antigravity":
		return ClientConfig{Provider: provider}, ErrMCPUnknown
	default:
		return ClientConfig{}, fmt.Errorf("mcp: unknown provider %q", provider)
	}
	return cfg, nil
}

// WriteConfig writes the config JSON to path with owner-only permissions, creating the directory. The file is deleted at
// run end (Gateway.EndRun removes <run dir>/mcp.json) and must be excluded from support bundles (docs/mcp.md section 2).
func WriteConfig(path string, cfg ClientConfig) error {
	if len(cfg.JSON) == 0 {
		return errors.New("mcp: provider has no config file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, cfg.JSON, 0o600)
}
