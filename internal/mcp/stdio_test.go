package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStdioUpstreamRoundTripThroughRealProcess(t *testing.T) {
	e := newEnv(t, nil)
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-stdio", ProjectID: "p"}, Servers: []ServerSpec{stdioSpec("sx", nil)}, Policy: allow(map[string][]string{"sx": {"*"}})})
	cs := e.connect(ep)
	res := callTool(t, cs, "sx__echo", map[string]any{"text": "over stdio"})
	if res.IsError || !strings.Contains(textOf(res), "echo: over stdio") {
		t.Fatalf("%s", textOf(res))
	}
	pids := e.g.UpstreamPIDs()
	if pids["sx"] <= 0 || !pidAlive(pids["sx"]) {
		t.Fatalf("pids %v", pids)
	}
	// cached across the process boundary too
	callTool(t, cs, "sx__counter", map[string]any{})
	r2 := callTool(t, cs, "sx__counter", map[string]any{})
	if b, _ := ExtractBody(textOf(r2)); b != "n=1" {
		t.Fatalf("cache over stdio: %q", b)
	}
	started := e.events(EventServerStarted)
	if len(started) != 1 {
		t.Fatalf("started events %d", len(started))
	}
	d := eventData(t, started[0])
	if d["server"] != "sx" || d["transport"] != "stdio" || int(d["pid"].(float64)) != pids["sx"] || d["restart_count"].(float64) != 0 {
		t.Fatalf("started payload %v", d)
	}
}

func TestStdioUpstreamEnvironmentIsAnAllowlist(t *testing.T) {
	t.Setenv("DRYDOCK_SECRET_PROBE", "should-not-be-inherited")
	t.Setenv("GITHUB_TOKEN", "ghp_should_not_be_inherited")
	e := newEnv(t, nil)
	spec := stdioSpec("sx", func(s *ServerSpec) { s.Cache.TTL = 0; s.Command.Env["EXPLICIT_VAR"] = "1" })
	cs := e.connect(e.startRun(RunConfig{Info: RunInfo{RunID: "r-env"}, Servers: []ServerSpec{spec}, Policy: allow(map[string][]string{"sx": {"*"}})}))
	for name, want := range map[string]string{"DRYDOCK_SECRET_PROBE": "unset", "GITHUB_TOKEN": "unset", "EXPLICIT_VAR": "set", "PATH": "set"} {
		body, _ := ExtractBody(textOf(callTool(t, cs, "sx__getenv", map[string]any{"text": name})))
		if body != want {
			t.Errorf("%s in the upstream: %q, want %q", name, body, want)
		}
	}
}

func TestStdioUpstreamCrashIsReportedAndRestartedLazily(t *testing.T) {
	e := newEnv(t, nil)
	spec := stdioSpec("sx", func(s *ServerSpec) { s.Cache.TTL = 0 })
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-crash"}, Servers: []ServerSpec{spec}, Policy: allow(map[string][]string{"sx": {"*"}})})
	cs := e.connect(ep)
	callTool(t, cs, "sx__echo", map[string]any{"text": "1"})
	pid1 := e.g.UpstreamPIDs()["sx"]
	// The server dies in the middle of the call: the agent gets a structured, non-fatal error.
	r := callTool(t, cs, "sx__crash", map[string]any{})
	if !r.IsError || !strings.Contains(textOf(r), `"error":"upstream_unavailable"`) {
		t.Fatalf("crash call: %s", textOf(r))
	}
	waitFor(t, "mcp.server.failed", 10*time.Second, func() bool { return len(e.events(EventServerFailed)) == 1 })
	d := eventData(t, e.events(EventServerFailed)[0])
	if d["server"] != "sx" || d["exit_code"].(float64) != 3 || int(d["pid"].(float64)) != pid1 {
		t.Fatalf("failed payload: %v", d)
	}
	if pidAlive(pid1) {
		t.Fatal("crashed server still alive")
	}
	// The run is unharmed: tools/list works and the next call restarts the server.
	if len(toolNames(t, cs)) == 0 {
		t.Fatal("tools/list broke after the crash")
	}
	r = callTool(t, cs, "sx__echo", map[string]any{"text": "2"})
	if r.IsError || !strings.Contains(textOf(r), "echo: 2") {
		t.Fatalf("restart: %s", textOf(r))
	}
	if pid2 := e.g.UpstreamPIDs()["sx"]; pid2 == 0 || pid2 == pid1 {
		t.Fatalf("pid after restart %d (before %d)", pid2, pid1)
	}
	started := e.events(EventServerStarted)
	if len(started) != 2 || eventData(t, started[1])["restart_count"].(float64) != 1 {
		t.Fatalf("restart_count not recorded: %d events", len(started))
	}
}

func TestStdioUpstreamThatCannotStartTripsTheBreaker(t *testing.T) {
	e := newEnv(t, nil)
	spec := stdioSpec("sx", func(s *ServerSpec) { s.Command.Env["DRYDOCK_MCP_TEST_FAIL_START"] = "1"; s.Cache.TTL = 0 })
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-brk"}, Servers: []ServerSpec{spec}, Policy: allow(map[string][]string{"sx": {"echo"}})})
	cs := e.connect(ep)
	attempt := func() string {
		r := callTool(t, cs, "sx__echo", map[string]any{"text": "x"})
		if !r.IsError {
			t.Fatal("must fail")
		}
		return textOf(r)
	}
	for i := 0; i < 3; i++ {
		if m := attempt(); strings.Contains(m, "circuit breaker open") || !strings.Contains(m, "upstream_unavailable") {
			t.Fatalf("attempt %d: %s", i, m)
		}
	}
	if m := attempt(); !strings.Contains(m, "circuit breaker open") {
		t.Fatalf("breaker did not open after 3 failures: %s", m)
	}
	// While open nothing is started, so the process manager is not hammered.
	before := len(e.events(EventServerFailed))
	attempt()
	if len(e.events(EventServerFailed)) != before {
		t.Fatal("an open breaker must not start the process")
	}
	// After 60 s the breaker lets one attempt through (it fails again, for real this time).
	e.clock.Advance(61 * time.Second)
	if m := attempt(); strings.Contains(m, "circuit breaker open") {
		t.Fatalf("breaker should half-open after 60s: %s", m)
	}
	if m := attempt(); !strings.Contains(m, "circuit breaker open") {
		// threshold is 3 consecutive failures; one more failure after the half-open attempt keeps counting up
		t.Logf("second post-reset attempt: %s", m)
	}
}

func TestCriticalUpstreamFailurePausesRun(t *testing.T) {
	var mu sync.Mutex
	var got []string
	e := newEnv(t, func(o *Options) {
		o.OnCritical = func(run RunInfo, server string, err error) {
			mu.Lock()
			got = append(got, run.RunID+"/"+server)
			mu.Unlock()
		}
	})
	mk := func(id string, critical bool) ServerSpec {
		return stdioSpec(id, func(s *ServerSpec) { s.Command.Env["DRYDOCK_MCP_TEST_FAIL_START"] = "1"; s.Critical = critical })
	}
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-crit"}, Servers: []ServerSpec{mk("must", true), mk("nice", false)}, Policy: allow(map[string][]string{"must": {"echo"}, "nice": {"echo"}})})
	cs := e.connect(ep)
	r := callTool(t, cs, "nice__echo", map[string]any{"text": "x"})
	if !r.IsError || r.StructuredContent.(map[string]any)["critical"] != false {
		t.Fatalf("non-critical: %s", textOf(r))
	}
	mu.Lock()
	if len(got) != 0 {
		t.Fatalf("a non-critical failure must not pause the run: %v", got)
	}
	mu.Unlock()
	r = callTool(t, cs, "must__echo", map[string]any{"text": "x"})
	if r.StructuredContent.(map[string]any)["critical"] != true {
		t.Fatalf("critical flag missing: %s", textOf(r))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "r-crit/must" {
		t.Fatalf("OnCritical: %v", got)
	}
	if n := len(e.events(EventCriticalFailure)); n != 1 {
		t.Fatalf("mcp.upstream_critical events: %d", n)
	}
}

var grandchildRe = regexp.MustCompile(`server=(\d+) grandchild=(\d+)`)

func TestStdioUpstreamProcessTreeIsKilled(t *testing.T) {
	for _, how := range []string{"StopUpstream", "Close"} {
		t.Run(how, func(t *testing.T) {
			e := newEnv(t, nil)
			spec := stdioSpec("sx", func(s *ServerSpec) { s.Cache.TTL = 0 })
			ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-tree"}, Servers: []ServerSpec{spec}, Policy: allow(map[string][]string{"sx": {"*"}})})
			cs := e.connect(ep)
			body, _ := ExtractBody(textOf(callTool(t, cs, "sx__spawn", map[string]any{})))
			m := grandchildRe.FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("spawn output: %q", body)
			}
			server, _ := strconv.Atoi(m[1])
			grand, _ := strconv.Atoi(m[2])
			t.Cleanup(func() { killPID(grand) })
			if !pidAlive(server) || !pidAlive(grand) {
				t.Fatalf("server %d / grandchild %d should be alive", server, grand)
			}
			if got := e.g.UpstreamPIDs()["sx"]; got != server {
				t.Fatalf("gateway pid %d != server pid %d", got, server)
			}
			if how == "StopUpstream" {
				e.g.StopUpstream("sx")
			} else {
				e.g.Close()
			}
			waitFor(t, "server process to die", 10*time.Second, func() bool { return !pidAlive(server) })
			if treeKillAssertable {
				// Windows: the Job Object reaps the grandchild even though the server had already exited.
				waitFor(t, "grandchild to die (Job Object tree kill)", 10*time.Second, func() bool { return !pidAlive(grand) })
			} else {
				t.Logf("grandchild %d alive=%v: tree kill is asserted on Windows only (decisions.md P1); the dev-host fallback kills the direct child", grand, pidAlive(grand))
			}
			if len(e.events(EventServerStopped)) == 0 {
				t.Fatal("mcp.server.stopped missing")
			}
		})
	}
}

// ---- the stdio shim, spawned exactly as a CLI spawns it from the config entry ----

func shimCommandFromConfig(t *testing.T, cfg ClientConfig) *exec.Cmd {
	t.Helper()
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
	ent, ok := doc.Servers[ServerName]
	if !ok || ent.Type != "stdio" || len(doc.Servers) != 1 {
		t.Fatalf("config must hold exactly one stdio entry named drydock: %s", cfg.JSON)
	}
	cmd := exec.Command(ent.Command, ent.Args...)
	cmd.Env = append(os.Environ(), modeEnv+"=shim")
	for k, v := range ent.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	return cmd
}

func TestStdioShimThroughRealSubprocess(t *testing.T) {
	ts, st := startHTTPUpstream(t, "", nil)
	self, _ := os.Executable()
	e := newEnv(t, func(o *Options) { o.ShimCommand = []string{self, "mcp-shim"} })
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-shim", ProjectID: "p"}, Servers: []ServerSpec{httpSpec("srv", ts.URL, nil)}, Policy: allow(map[string][]string{"srv": {"echo", "big"}})})
	cfg, err := ConfigFor("claude", *ep)
	if err != nil {
		t.Fatal(err)
	}
	cmd := shimCommandFromConfig(t, cfg)
	client := sdk.NewClient(&sdk.Implementation{Name: "cli", Version: "1"}, nil)
	cs, err := client.Connect(t.Context(), &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect through the shim: %v", err)
	}
	defer cs.Close()
	names := toolNames(t, cs)
	if strings.Join(names, ",") != "fetch_range,srv__big,srv__echo" {
		t.Fatalf("tools through the shim: %v", names)
	}
	if lr, err := cs.ListTools(t.Context(), nil); err != nil || lr.CacheScope != "private" {
		t.Fatalf("the shim's tools/list must carry cacheScope (Claude Code rejects it otherwise): %v %+v", err, lr)
	}
	res := callTool(t, cs, "srv__echo", map[string]any{"text": "via shim"})
	if body, _ := ExtractBody(textOf(res)); body != "echo: via shim" {
		t.Fatalf("shim result: %q", textOf(res))
	}
	if r := callTool(t, cs, "srv__counter", map[string]any{}); !r.IsError {
		t.Fatal("the allowlist applies through the shim too")
	}
	// A truncated result continues through the shim.
	big := callTool(t, cs, "srv__big", map[string]any{"bytes": 30000})
	h := big.Meta["drydock"].(map[string]any)["handle"].(string)
	fr := callTool(t, cs, "fetch_range", map[string]any{"handle": h, "offset": 8000, "length": 100})
	if fr.IsError {
		t.Fatal(textOf(fr))
	}
	_ = st
	// The shim presents the token itself: the same run token appears in no CLI-visible tool output.
	if strings.Contains(textOf(res), ep.Token) {
		t.Fatal("token in output")
	}
	cs.Close()
	waitFor(t, "shim exit", 10*time.Second, func() bool { return cmd.ProcessState != nil || cmd.Process == nil || !pidAlive(cmd.Process.Pid) })
}

func TestStdioShimWithWrongTokenFailsClosed(t *testing.T) {
	e := newEnv(t, nil)
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-shim2"}, Policy: allow(map[string][]string{"x": {"*"}})})
	bad := *ep
	bad.Token = strings.Repeat("0", 64)
	bad.ShimCommand = []string{func() string { p, _ := os.Executable(); return p }(), "mcp-shim"}
	cfg, err := ConfigFor("claude", bad)
	if err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "cli", Version: "1"}, nil)
	cs, err := client.Connect(t.Context(), &sdk.CommandTransport{Command: shimCommandFromConfig(t, cfg)}, nil)
	if err != nil {
		t.Fatalf("the shim itself starts without contacting the gateway: %v", err)
	}
	defer cs.Close()
	if _, err := cs.ListTools(t.Context(), nil); err == nil {
		t.Fatal("tools/list with a wrong token must fail")
	}
	if r, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: "x__echo"}); err == nil && !r.IsError {
		t.Fatal("tools/call with a wrong token must fail")
	}
	waitFor(t, "mcp.auth_failed", 5*time.Second, func() bool { return len(e.events(EventAuthFailed)) > 0 })
}

func TestShimRefusesToSendTheTokenAnywhereButLoopback(t *testing.T) {
	cases := []struct{ url, tok string }{
		{"https://example.com/mcp/r1", "t"},
		{"http://example.com/mcp/r1", "t"},
		{"http://192.168.0.2:80/mcp/r1", "t"},
		{"http://127.0.0.1:1/other", "t"},
		{"http://127.0.0.1:1/mcp/r1", ""},
		{"", "t"},
	}
	for _, c := range cases {
		if err := checkShimTarget(c.url, c.tok); err == nil {
			t.Errorf("checkShimTarget(%q, %q) accepted", c.url, c.tok)
		}
	}
	if err := checkShimTarget("http://127.0.0.1:5555/mcp/run-1", "t"); err != nil {
		t.Error(err)
	}
	if err := checkShimTarget(fmt.Sprintf("http://[::1]:%d/mcp/r", 80), "t"); err != nil {
		t.Error(err)
	}
}

// A client on the 2026-07-28 protocol (Claude Code 2.1.294) talks to the shim: list results carry cacheScope and call results carry
// resultType, otherwise the CLI discards them. Found by running the real Claude Code against the generated config.
func TestStdioShimServesNewProtocolClients(t *testing.T) {
	ts, _ := startHTTPUpstream(t, "", nil)
	self, _ := os.Executable()
	e := newEnv(t, func(o *Options) { o.ShimCommand = []string{self, "mcp-shim"} })
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-shim3", ProjectID: "p"}, Servers: []ServerSpec{httpSpec("srv", ts.URL, nil)}, Policy: allow(map[string][]string{"srv": {"echo"}})})
	cfg, err := ConfigFor("claude", *ep)
	if err != nil {
		t.Fatal(err)
	}
	cmd := shimCommandFromConfig(t, cfg)
	client := sdk.NewClient(&sdk.Implementation{Name: "cli", Version: "1"}, nil)
	cs, err := client.Connect(t.Context(), &sdk.CommandTransport{Command: cmd}, &sdk.ClientSessionOptions{ProtocolVersion: protocolNew})
	if err != nil {
		t.Fatalf("connect through the shim: %v", err)
	}
	defer cs.Close()
	if v := cs.InitializeResult().ProtocolVersion; v != protocolNew {
		t.Fatalf("negotiated %q", v)
	}
	lr, err := cs.ListTools(t.Context(), nil)
	if err != nil || lr.CacheScope != "private" || len(lr.Tools) == 0 {
		t.Fatalf("tools/list through the shim: %v %+v", err, lr)
	}
	for _, c := range []struct {
		tool string
		args map[string]any
	}{{"srv__echo", map[string]any{"text": "x"}}, {"srv__counter", map[string]any{}} /* refused by the allowlist */} {
		raw, _ := json.Marshal(callTool(t, cs, c.tool, c.args))
		if !strings.Contains(string(raw), `"resultType":"complete"`) {
			t.Fatalf("%s through the shim lacks resultType: %s", c.tool, raw)
		}
	}
}
