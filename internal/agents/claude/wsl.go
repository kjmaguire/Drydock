package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// The WSL run mode (decision P41): Claude Code installed inside a WSL distro, started through wsl.exe instead of claude.exe.
// The distro's install is its own Connection with its own sign-in; Drydock never reads, copies or moves a credential between
// Windows and the distro (D1). What changes against the native start:
//
//   - every path Drydock hands the CLI (the working folder, the instructions file, the MCP config and the shim command in it,
//     any other path-taking flag) is translated to the distro's form (a drive under its mount root, or its own files);
//   - the CLI gets the distro's own HOME and PATH (a login shell's, found by the probe), never the Windows ones, and from the
//     Windows environment only what WSLENV names: the variables Claude Code reads (CLAUDE_*, ANTHROPIC_*, without the
//     Windows-only ones), the proxy and certificate variables, Drydock's own per-run variables and the names the person chose
//     to pass. Values travel in the environment, never on the command line, so a key is not in any process list;
//   - every process of the run carries a marker, and stopping the run (or its end) kills every marked process and the process
//     groups they lead inside the distro, because killing wsl.exe's Job Object does not reliably end them.

// wslBinary is the name the manifest declares for the wsl run mode (binaries.wsl).
const wslBinary = "claude"

// WSLProbeArgs are the version and sign-in status arguments the probe runs claude with inside a distro (the manifest's
// detect.version.argv and auth.probe.argv).
var (
	WSLVersionArgs = []string{"--version"}
	WSLAuthArgs    = []string{"auth", "status"}
)

// windowsOnlyEnv are CLAUDE_* variables that name Windows things; inside the distro they would point the CLI at the Windows
// install's files (CLAUDE_CONFIG_DIR would even share the Windows sign-in, which the distro's own Connection must not do).
var windowsOnlyEnv = map[string]bool{"CLAUDE_CONFIG_DIR": true, "CLAUDE_CODE_GIT_BASH_PATH": true, "CLAUDE_CODE_TMPDIR": true, "CLAUDE_CODE_SHELL": true}

// wslNetEnv are the network variables a CLI inside the distro needs as much as one on Windows; the certificate ones are paths.
var wslNetEnv = map[string]bool{"HTTP_PROXY": false, "HTTPS_PROXY": false, "NO_PROXY": false, "ALL_PROXY": false,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "NODE_EXTRA_CA_CERTS": true, "CURL_CA_BUNDLE": true, "REQUESTS_CA_BUNDLE": true,
	"LANG": false, "LC_ALL": false, "TZ": false, "NO_COLOR": false}

// pathFlags are the flags whose value is a path the CLI opens.
var pathFlags = map[string]bool{"--append-system-prompt-file": true, "--system-prompt-file": true, "--mcp-config": true,
	"--settings": true, "--add-dir": true, "--plugin-dir": true, "--debug-file": true}

// WSLAvailable reports whether this adapter can start the WSL run mode on this machine (wsl.exe exists).
func (a *Adapter) WSLAvailable() bool {
	if a.WSL == nil {
		return false
	}
	_, err := a.WSL.Path()
	return err == nil
}

// NativeInstalled reports whether Claude Code is installed on this machine itself (agents.NativeLocator): the configured
// program, claude on PATH, or the native installer's folders. It starts nothing.
func (a *Adapter) NativeInstalled() bool {
	_, err := a.executable()
	return err == nil
}

// wslInstall names a WSL distro where an earlier probe (the Connections page's) found Claude Code, "" when none did. It only
// reads the probe cache and the distro list, so a native start that failed does not wait for a probe of every distro.
func (a *Adapter) wslInstall(ctx context.Context) string {
	if a.WSL == nil {
		return ""
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ds, _, err := a.WSL.Distros(lctx)
	if err != nil {
		return ""
	}
	for _, d := range ds {
		if !wsl.UserDistro(d.Name) {
			continue
		}
		if in, ok := a.WSL.Cached(d.Name, wslBinary); ok && in.Tool != "" {
			return d.Name
		}
	}
	return ""
}

// translateArgs rewrites the value of every path-taking flag (both "--flag value" and "--flag=value") to the distro's form.
func translateArgs(args []string, p wsl.Paths) ([]string, error) {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		a := out[i]
		if k, v, ok := strings.Cut(a, "="); ok && pathFlags[k] {
			lin, err := p.ToLinux(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[i] = k + "=" + lin
			continue
		}
		if pathFlags[a] && i+1 < len(out) {
			lin, err := p.ToLinux(out[i+1])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", a, err)
			}
			out[i+1] = lin
			i++
		}
	}
	return out, nil
}

// wslMCPConfig writes the distro's copy of the gateway config: each stdio server's command (drydock.exe mcp-shim, a Windows
// program the distro starts through interop) as the distro sees it, and WSLENV in the server's env naming its variables, so the
// run's URL and token reach the Windows shim (interop copies only what WSLENV names). The token stays in the file (owner-only,
// removed when the run ends) and the shim's environment, as natively. The shim talks to the gateway on the Windows loopback,
// so this works in NAT and mirrored networking alike. windowsCmd reports whether a command is a Windows program (.exe), which
// needs the distro's interop.
func wslMCPConfig(path string, p wsl.Paths) (dst string, windowsCmd bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", false, fmt.Errorf("the MCP config is not JSON: %w", err)
	}
	for name, srv := range doc.MCPServers {
		cmd, _ := srv["command"].(string)
		if cmd == "" {
			continue // an http entry: the distro would need the Windows loopback, which NAT networking does not give
		}
		lin, err := p.ToLinux(cmd)
		if err != nil {
			return "", false, fmt.Errorf("the MCP server %s cannot be started from the distro: %w", name, err)
		}
		windowsCmd = windowsCmd || strings.HasSuffix(strings.ToLower(lin), ".exe")
		srv["command"] = lin
		env := map[string]any{}
		if e, ok := srv["env"].(map[string]any); ok {
			env = e
		}
		var names []string
		for k := range env {
			if k != "WSLENV" {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		env["WSLENV"] = strings.Join(names, ":")
		srv["env"] = env
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", false, err
	}
	dst = filepath.Join(filepath.Dir(path), "mcp-wsl.json")
	if err := os.WriteFile(dst, out, 0o600); err != nil {
		return "", false, err
	}
	return dst, windowsCmd, nil
}

// wslEnv is the Windows-side environment of wsl.exe for a WSL session: env as for a native start, with WSLENV naming what
// may cross into the distro (see the comment at the top of this file). spec.Env names and the person's PassEnv names cross.
func (a *Adapter) wslEnv(env []string, spec agents.RunSpec) []string {
	extra := map[string]bool{}
	for _, kv := range spec.Env {
		k, _, _ := strings.Cut(kv, "=")
		extra[strings.ToUpper(k)] = true
	}
	if a.PassEnv != nil {
		for _, n := range a.PassEnv() {
			extra[strings.ToUpper(n)] = true
		}
	}
	keep := func(name string) bool {
		u := strings.ToUpper(name)
		switch {
		case windowsOnlyEnv[u], u == "PATH", u == "HOME", u == "WSLENV":
			return false
		case strings.HasPrefix(u, "CLAUDE_"), strings.HasPrefix(u, "ANTHROPIC_"), extra[u]:
			return true
		}
		_, ok := wslNetEnv[u]
		return ok
	}
	return wsl.Forward(env, keep, func(name string) bool { return wslNetEnv[strings.ToUpper(name)] })
}

// startWSL starts claude inside spec.WSLDistro. env is the native start's environment and text the instructions to append.
func (a *Adapter) startWSL(ctx context.Context, pm proc.Manager, spec agents.RunSpec, env []string, text string) (agents.Session, error) {
	if a.WSL == nil {
		return nil, errors.New("claude: the WSL run mode is not available in this build")
	}
	exe, err := a.WSL.Path()
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	distro := strings.TrimSpace(spec.WSLDistro)
	if distro == "" {
		return nil, errors.New("claude: the agent runs in WSL but no distro is chosen: pick one in the agent's settings")
	}
	info, err := a.WSL.Probe(ctx, distro, wslBinary, WSLVersionArgs, nil, false)
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if info.Tool == "" {
		return nil, fmt.Errorf("claude: Claude Code was not found in the WSL distro %s (looked on a login shell's PATH and in ~/.local/bin): install it inside the distro and sign in there with `claude auth login`", distro)
	}
	if info.Paths.MountRoot == "" {
		return nil, fmt.Errorf("claude: the WSL distro %s did not say where it mounts Windows drives (wslpath failed and /etc/wsl.conf names no [automount] root)", distro)
	}
	cwd, err := info.Paths.ToLinux(spec.Cwd)
	if err != nil {
		return nil, fmt.Errorf("claude: the working folder cannot be reached from the WSL distro %s: %w", distro, err)
	}
	var cleanup []string
	clean := func() {
		for _, f := range cleanup {
			_ = os.Remove(f)
		}
	}
	if spec.MCPConfigPath != "" {
		cfg, windowsCmd, err := wslMCPConfig(spec.MCPConfigPath, info.Paths)
		if err != nil {
			return nil, fmt.Errorf("claude: %w", err)
		}
		if windowsCmd && !info.Interop {
			_ = os.Remove(cfg)
			return nil, fmt.Errorf("claude: Windows interop is off in the WSL distro %s, and Drydock's tools reach an agent there through a Windows program the distro starts (drydock.exe mcp-shim). Turn interop on ([interop] enabled=true in /etc/wsl.conf, then wsl --terminate %s), or run this agent natively", distro, distro)
		}
		cleanup = append(cleanup, cfg)
		spec.MCPConfigPath = cfg
	}
	promptFile, err := writePromptFile(text)
	if err != nil {
		clean()
		return nil, fmt.Errorf("claude: %w", err)
	}
	if promptFile != "" {
		cleanup = append(cleanup, promptFile)
	}
	args, err := translateArgs(a.argsWith(spec, promptFile), info.Paths)
	if err != nil {
		clean()
		return nil, fmt.Errorf("claude: a path cannot be reached from the WSL distro %s: %w", distro, err)
	}
	marker := wsl.NewMarker()
	launch := wsl.Launch{Distro: distro, Dir: cwd, Marker: marker, Env: []string{"HOME=" + info.Home, "PATH=" + info.Path}, Argv: append([]string{info.Tool}, args...)}
	p, err := pm.Start(ctx, proc.Spec{Path: exe, Args: launch.Args(), Dir: os.TempDir(), Env: a.wslEnv(env, spec), Label: "agent:" + spec.AgentID})
	if err != nil {
		clean()
		return nil, err
	}
	host := a.WSL
	sweep := func() {
		kctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = host.Kill(kctx, distro, marker)
	}
	return newSession(p, spec, sweep, clean), nil
}
