package claude

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
	"github.com/kjmaguire/drydock/internal/wsl"
	"github.com/kjmaguire/drydock/internal/wsl/wsltest"
)

func TestTranslateArgsMapsEveryPathTheCLIOpens(t *testing.T) {
	p := wsl.Paths{Distro: "Ubuntu", MountRoot: "/mnt/"}
	in := []string{"-p", "--mcp-config", `C:\Users\k\AppData\Roaming\Drydock\runs\r1\mcp-wsl.json`, "--strict-mcp-config",
		"--append-system-prompt-file", `C:\Users\k\AppData\Local\Temp\drydock-claude-prompt-1.md`, "--add-dir", `\\wsl.localhost\Ubuntu\home\k\shared`,
		"--settings=D:\\cfg\\s.json", "--model", "haiku", "--allowedTools", `Bash(go test ./...)`}
	got, err := translateArgs(in, p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-p", "--mcp-config", "/mnt/c/Users/k/AppData/Roaming/Drydock/runs/r1/mcp-wsl.json", "--strict-mcp-config",
		"--append-system-prompt-file", "/mnt/c/Users/k/AppData/Local/Temp/drydock-claude-prompt-1.md", "--add-dir", "/home/k/shared",
		"--settings=/mnt/d/cfg/s.json", "--model", "haiku", "--allowedTools", `Bash(go test ./...)`}
	if !slices.Equal(got, want) {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if _, err := translateArgs([]string{"--add-dir", `\\fileserver\share`}, p); err == nil {
		t.Fatal("a path the distro cannot reach must refuse the start, not be passed as it is")
	}
}

func TestWSLEnvLetsOnlyWhatClaudeNeedsCrossIntoTheDistro(t *testing.T) {
	a := &Adapter{PassEnv: func() []string { return []string{"AWS_PROFILE"} }}
	env := []string{"PATH=C:\\Windows", "HOME=C:\\Users\\k", "USERPROFILE=C:\\Users\\k", "TEMP=C:\\t", "ANTHROPIC_API_KEY=sk-x", "CLAUDE_CODE_DISABLE_CRON=1",
		"CLAUDE_CONFIG_DIR=C:\\Users\\k\\.claude", "HTTPS_PROXY=http://proxy:8080", "NODE_EXTRA_CA_CERTS=C:\\certs\\ca.pem", "AWS_PROFILE=work", "MCP_TOOL_TIMEOUT=5"}
	got := a.wslEnv(env, agents.RunSpec{Env: []string{"MCP_TOOL_TIMEOUT=5"}})
	var wslenv string
	for _, kv := range got {
		if v, ok := strings.CutPrefix(kv, "WSLENV="); ok {
			wslenv = v
		}
	}
	names := strings.Split(wslenv, ":")
	for _, want := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_DISABLE_CRON", "HTTPS_PROXY", "NODE_EXTRA_CA_CERTS/p", "AWS_PROFILE", "MCP_TOOL_TIMEOUT"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s does not cross: WSLENV=%s", want, wslenv)
		}
	}
	for _, not := range []string{"PATH", "HOME", "USERPROFILE", "TEMP", "CLAUDE_CONFIG_DIR"} {
		for _, n := range names {
			if strings.SplitN(n, "/", 2)[0] == not {
				t.Errorf("%s must not cross into the distro (the distro has its own; CLAUDE_CONFIG_DIR would share the Windows sign-in)", not)
			}
		}
	}
}

func TestWSLMCPConfigStartsTheWindowsShimThroughInteropWithItsVariablesNamed(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "mcp.json")
	doc := `{"mcpServers":{"drydock":{"type":"stdio","command":"C:\\Program Files\\Drydock\\drydock.exe","args":["mcp-shim"],"env":{"DRYDOCK_MCP_URL":"http://127.0.0.1:5000/mcp/r1","DRYDOCK_MCP_TOKEN":"tok"}}}}`
	if err := os.WriteFile(src, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	out, win, err := wslMCPConfig(src, wsl.Paths{Distro: "Ubuntu", MountRoot: "/mnt/"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	var got struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	s := got.MCPServers["drydock"]
	if !win {
		t.Error("drydock.exe is a Windows program: the start needs interop")
	}
	if s.Command != "/mnt/c/Program Files/Drydock/drydock.exe" || !slices.Equal(s.Args, []string{"mcp-shim"}) || s.Env["WSLENV"] != "DRYDOCK_MCP_TOKEN:DRYDOCK_MCP_URL" || s.Env["DRYDOCK_MCP_TOKEN"] != "tok" {
		t.Fatalf("%s", b)
	}
	if st, err := os.Stat(out); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) {
		t.Fatalf("the copy holds the run token: owner-only (%v)", err)
	}
}

func TestTheWSLRunModeIsOfferedOnlyWhereWSLExists(t *testing.T) {
	if slices.Contains((&Adapter{}).Capabilities().RunModes, agents.RunModeWSL) {
		t.Fatal("no WSL host: native only")
	}
	if runtime.GOOS != "windows" && slices.Contains((&Adapter{WSL: &wsl.Host{}}).Capabilities().RunModes, agents.RunModeWSL) {
		t.Fatal("a development host has no wsl.exe")
	}
	if _, err := (&Adapter{}).Start(context.Background(), proc.New(), agents.RunSpec{Cwd: t.TempDir(), RunMode: agents.RunModeWSL, WSLDistro: "Ubuntu"}); err == nil {
		t.Fatal("started in WSL without a WSL host")
	}
}

// recPM records what was started.
type recPM struct {
	proc.Manager
	mu    sync.Mutex
	specs []proc.Spec
}

func (r *recPM) Start(ctx context.Context, s proc.Spec) (proc.Proc, error) {
	r.mu.Lock()
	r.specs = append(r.specs, s)
	r.mu.Unlock()
	return r.Manager.Start(ctx, s)
}

// readTurn collects the assistant text of one turn.
func readTurn(t *testing.T, s agents.Session) string {
	t.Helper()
	var b strings.Builder
	timeout := time.After(90 * time.Second)
	for {
		select {
		case c, ok := <-s.Output():
			if !ok {
				t.Fatalf("output ended: %s (stderr %q)", b.String(), s.Stderr())
			}
			switch c.Kind {
			case workflow.ChunkMessageEnd:
				var d workflow.TextDelta
				_ = c.Decode(&d)
				b.WriteString(d.Text)
			case workflow.ChunkDone:
				return b.String()
			}
		case <-timeout:
			t.Fatalf("no turn end: %s (stderr %q)", b.String(), s.Stderr())
		}
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting: " + what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// wslScenario starts the stand-in claude in distro through the adapter, with the working folder cwd (a Windows path, or on a
// development host a Linux one), and proves: the start crosses no secret on the command line and only the chosen variables
// through WSLENV; the CLI runs in the translated folder with the distro's HOME, reads the instructions and MCP config Drydock
// wrote on the Windows side; the MCP server command (this test binary, a Windows program on Windows) starts from inside the
// distro with the gateway's variables; stream-json flows both ways over wsl.exe's pipes for two turns; and Stop leaves no
// process of the run alive inside the distro, including grandchildren in a session of their own.
func wslScenario(t *testing.T, host *wsl.Host, distro, cwd, wantCwd string) {
	ctx := context.Background()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mcp.json")
	token := strings.Repeat("ab", 32)
	doc, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"drydock": map[string]any{"type": "stdio", "command": self, "args": []string{"mcp-shim"},
		"env": map[string]string{"DRYDOCK_MCP_URL": "http://127.0.0.1:1/mcp/run1", "DRYDOCK_MCP_TOKEN": token}}}})
	if err := os.WriteFile(cfg, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	const key = "sk-ant-test-0123456789" // 22 characters; a fake, never a real key
	t.Setenv("ANTHROPIC_API_KEY", key)
	t.Setenv("GITHUB_TOKEN", "ghp_must_not_cross")
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	a := &Adapter{ScrubEnv: true, UserSettingsOnly: true, WSL: host}
	pm := &recPM{Manager: proc.New()}
	sess, err := a.Start(ctx, pm, agents.RunSpec{AgentID: "wsl1", Cwd: cwd, RunMode: agents.RunModeWSL, WSLDistro: distro, MCPConfigPath: cfg,
		SystemAppend: "Be brief.", Env: []string{"MCP_TOOL_TIMEOUT=1000"}})
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = sess.Stop(ctx)
		}
	}()
	if err := sess.Send(ctx, "hello spawn interop"); err != nil {
		t.Fatal(err)
	}
	text := readTurn(t, sess)
	info, _ := host.Cached(distro, "claude")
	for _, want := range []string{"standin turn=1", "cwd=" + wantCwd + " ", "home=" + info.Home + " ", "run=set", "keylen=22", "prompt=yes", "mcp=yes", "mcpcmdok=yes",
		"quiet=1", "spawned=yes", "interop=[interop-ok tokenlen=64 url=http://127.0.0.1:1/mcp/run1]"} {
		if !strings.Contains(text, want) {
			t.Errorf("the agent's answer lacks %q:\n%s", want, text)
		}
	}
	pm.mu.Lock()
	spec := pm.specs[0]
	pm.mu.Unlock()
	marker := ""
	for _, arg := range spec.Args {
		if strings.Contains(arg, key) || strings.Contains(arg, token) || strings.Contains(arg, "ghp_") {
			t.Fatalf("a secret is on wsl.exe's command line: %q", arg)
		}
		if v, ok := strings.CutPrefix(arg, wsl.RunEnv+"="); ok {
			marker = v
		}
	}
	var wslenv string
	for _, kv := range spec.Env {
		if v, ok := strings.CutPrefix(kv, "WSLENV="); ok {
			wslenv = v
		}
	}
	names := strings.Split(wslenv, ":")
	if !slices.Contains(names, "ANTHROPIC_API_KEY") || !slices.Contains(names, "MCP_TOOL_TIMEOUT") || slices.Contains(names, "GITHUB_TOKEN") || slices.Contains(names, "CLAUDE_CONFIG_DIR") {
		t.Fatalf("WSLENV=%s", wslenv)
	}
	if marker == "" {
		t.Fatal("no run marker on the command line")
	}
	waitUntil(t, "the grandchildren to be running", func() bool { p, _ := host.Marked(ctx, distro, marker); return len(p) >= 3 })

	if err := sess.Send(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	if text := readTurn(t, sess); !strings.Contains(text, "standin turn=2") {
		t.Fatalf("the session did not continue: %s", text)
	}

	_ = sess.Stop(ctx)
	stopped = true
	select {
	case <-sess.Wait():
	case <-time.After(60 * time.Second):
		t.Fatal("the session did not end")
	}
	// Wait answers only after the sweep: nothing of the run is left inside the distro
	if p, err := host.Marked(ctx, distro, marker); err != nil || len(p) != 0 {
		t.Fatalf("processes of the run outlived Stop inside the distro: %v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mcp-wsl.json")); !os.IsNotExist(err) {
		t.Fatal("the distro's copy of the MCP config (it holds the run token) was not removed")
	}
}

func TestAWSLSessionRunsInTheDistroStreamsAndStopKillsItsLinuxProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows the real wsl.exe is driven by TestRealWSLSession (CI's wsl job)")
	}
	for _, tool := range []string{"/usr/bin/setsid", "/usr/bin/bash", "/usr/bin/getent"} {
		if _, err := os.Stat(tool); err != nil {
			t.Skipf("%s is missing on this host", tool)
		}
	}
	f := wsltest.NewFake(t, true, "Ubuntu")
	cwd := t.TempDir()
	wslScenario(t, &wsl.Host{Exe: f.Exe}, "Ubuntu", cwd, cwd)
}

// TestRealWSLSession runs the same proof against a real distro: CI's wsl job sets DRYDOCK_WSL_TEST_DISTRO after installing
// one (it never contains a real Claude Code: the stand-in is installed into ~/.local/bin and refuses to replace a real one).
// It covers a working folder on a Windows drive (read through the mount root) and one inside the distro (\\wsl.localhost).
func TestRealWSLSession(t *testing.T) {
	distro := os.Getenv("DRYDOCK_WSL_TEST_DISTRO")
	if runtime.GOOS != "windows" || distro == "" {
		t.Skip("needs Windows with a WSL distro named in DRYDOCK_WSL_TEST_DISTRO (CI's wsl job)")
	}
	host := &wsl.Host{}
	exe, err := host.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := wsltest.InstallClaude(exe, distro); err != nil {
		t.Fatal(err)
	}
	info, err := host.Probe(context.Background(), distro, "claude", WSLVersionArgs, WSLAuthArgs, true)
	if err != nil || info.Tool == "" {
		t.Fatalf("detection: %+v %v", info, err)
	}
	t.Logf("distro %s: WSL2=%v kernel=%s interop=%v netmode=%q mount root %q, claude at %s", distro, info.WSL2, info.Kernel, info.Interop, info.NetMode, info.Paths.MountRoot, info.Tool)
	t.Run("windows drive", func(t *testing.T) {
		cwd := t.TempDir()
		lin, err := info.Paths.ToLinux(cwd)
		if err != nil {
			t.Fatal(err)
		}
		wslScenario(t, host, distro, cwd, lin)
	})
	t.Run("inside the distro", func(t *testing.T) {
		lin := "/tmp/drydock-wsl-test-" + wsl.NewMarker()
		if out, err := exec.Command(exe, "-d", distro, "--exec", "mkdir", "-p", lin).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		defer func() { _ = exec.Command(exe, "-d", distro, "--exec", "rm", "-rf", lin).Run() }()
		wslScenario(t, host, distro, wsl.UNC(distro, lin), lin)
	})
}

func TestANativeStartWithoutWindowsClaudeNamesTheDistroWhereItIsInstalled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs the stand-in wsl.exe of a development host")
	}
	for _, tool := range []string{"/usr/bin/bash", "/usr/bin/getent"} {
		if _, err := os.Stat(tool); err != nil {
			t.Skipf("%s is missing on this host", tool)
		}
	}
	f := wsltest.NewFake(t, true, "Ubuntu")
	host := &wsl.Host{Exe: f.Exe}
	a := &Adapter{WSL: host}
	t.Setenv("PATH", t.TempDir()) // no claude on this "Windows"
	t.Setenv("HOME", t.TempDir())
	spec := agents.RunSpec{AgentID: "lead", Cwd: t.TempDir()}
	// before the Connections page probed the distro, the plain error stays
	if _, err := a.Start(context.Background(), proc.New(), spec); err == nil || strings.Contains(err.Error(), "WSL") {
		t.Fatalf("no probe yet: %v", err)
	}
	if _, err := host.Probe(context.Background(), "Ubuntu", "claude", WSLVersionArgs, WSLAuthArgs, true); err != nil {
		t.Fatal(err)
	}
	_, err := a.Start(context.Background(), proc.New(), spec)
	if err == nil || !strings.Contains(err.Error(), "installed in the WSL distro Ubuntu") || !strings.Contains(err.Error(), "Where agents run") {
		t.Fatalf("%v", err)
	}
}
