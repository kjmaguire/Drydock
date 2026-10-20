package core_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/repomap"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// These tests cover the MCP wiring of agent runs (docs/mcp.md): the shared gateway, the per-session endpoint and config file, the
// per-role allowlist, and the real MCP client path. Nothing here fakes the gateway: tools are called through its HTTP endpoint
// with the go-sdk client, exactly as the shim and a CLI would.

// ---- helpers ----

type bearerRT struct{ tok string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

func connectMCP(ctx context.Context, url, tok string) (*sdk.ClientSession, error) {
	c := sdk.NewClient(&sdk.Implementation{Name: "core-test", Version: "1"}, nil)
	return c.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: bearerRT{tok}}, DisableStandaloneSSE: true, MaxRetries: 1}, nil)
}

// status posts to the endpoint with the token and returns the HTTP status (401 once the run has ended).
func endpointStatus(url, tok string) int {
	req, _ := http.NewRequest("POST", url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

type mcpConfigFile struct {
	MCPServers map[string]struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	} `json:"mcpServers"`
}

// parseSingleEntry asserts the file holds exactly the one "drydock" stdio-shim entry and returns the URL and token in it.
func parseSingleEntry(t *testing.T, raw []byte) (url, tok string) {
	t.Helper()
	var f mcpConfigFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("config is not JSON: %v\n%s", err, raw)
	}
	if len(f.MCPServers) != 1 {
		t.Fatalf("the CLI must see exactly one MCP server, got %d: %s", len(f.MCPServers), strings.ReplaceAll(string(raw), tokenOf(raw), "<token>"))
	}
	e, ok := f.MCPServers["drydock"]
	if !ok {
		t.Fatal("the single entry must be named drydock")
	}
	if e.Type != "stdio" || len(e.Args) != 1 || e.Args[0] != "mcp-shim" || e.Command == "" || e.URL != "" || len(e.Headers) != 0 {
		t.Fatalf("entry is not the loopback stdio shim: %+v", e)
	}
	if len(e.Env) != 2 || e.Env["DRYDOCK_MCP_URL"] == "" || e.Env["DRYDOCK_MCP_TOKEN"] == "" {
		t.Fatalf("shim env must be exactly the url and token: %v", mapKeys(e.Env))
	}
	if !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/mcp/arun_[0-9a-f]+$`).MatchString(e.Env["DRYDOCK_MCP_URL"]) {
		t.Fatalf("endpoint %q", e.Env["DRYDOCK_MCP_URL"])
	}
	return e.Env["DRYDOCK_MCP_URL"], e.Env["DRYDOCK_MCP_TOKEN"]
}

func tokenOf(raw []byte) string {
	m := regexp.MustCompile(`"DRYDOCK_MCP_TOKEN":\s*"([0-9a-f]+)"`).FindSubmatch(raw)
	if m == nil {
		return "\x00none"
	}
	return string(m[1])
}

func mapKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func toolNames(t *testing.T, s *sdk.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var out []string
	for _, tl := range res.Tools {
		out = append(out, tl.Name)
	}
	return out
}

func callTool(t *testing.T, s *sdk.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("tools/call %s: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

// sameSet compares tool lists. A set that has the read tools (read_range) also has repo_map in a build with the tree-sitter
// parser (CGO), which gives it to every role with the other read tools (decision P62).
func sameSet(got []string, want ...string) bool {
	if repomap.Available() && slices.Contains(want, "read_range") && !slices.Contains(want, "repo_map") {
		want = append(append([]string(nil), want...), "repo_map")
	}
	if len(got) != len(want) {
		return false
	}
	m := map[string]bool{}
	for _, g := range got {
		m[g] = true
	}
	for _, w := range want {
		if !m[w] {
			return false
		}
	}
	return true
}

// captureAdapter wraps an adapter and records, at Start, the spec it was given and the config file the spec points at. With
// probe set it also acts as an MCP client of the run's endpoint while the session is alive.
type captureAdapter struct {
	agents.Adapter
	mu     sync.Mutex
	starts []*capturedStart
	probe  bool
}

type capturedStart struct {
	Spec      agents.RunSpec
	Cfg       []byte
	URL, Tok  string
	Tools     []string
	ReadRange string
	TaskCall  string
	TaskErr   bool
	ProbeErr  string
}

func (a *captureAdapter) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	cs := &capturedStart{Spec: spec}
	if spec.MCPConfigPath != "" {
		raw, err := os.ReadFile(spec.MCPConfigPath)
		if err != nil {
			cs.ProbeErr = "read config: " + err.Error()
		} else {
			cs.Cfg = raw
			var f mcpConfigFile
			if json.Unmarshal(raw, &f) == nil {
				cs.URL, cs.Tok = f.MCPServers["drydock"].Env["DRYDOCK_MCP_URL"], f.MCPServers["drydock"].Env["DRYDOCK_MCP_TOKEN"]
			}
		}
	}
	if a.probe && cs.URL != "" {
		pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		s, err := connectMCP(pctx, cs.URL, cs.Tok)
		if err != nil {
			cs.ProbeErr = err.Error()
		} else {
			if lt, err := s.ListTools(pctx, nil); err == nil {
				for _, tl := range lt.Tools {
					cs.Tools = append(cs.Tools, tl.Name)
				}
			}
			if r, err := s.CallTool(pctx, &sdk.CallToolParams{Name: "read_range", Arguments: map[string]any{"path": "calc.go"}}); err == nil {
				for _, c := range r.Content {
					if tc, ok := c.(*sdk.TextContent); ok {
						cs.ReadRange += tc.Text
					}
				}
			}
			if r, err := s.CallTool(pctx, &sdk.CallToolParams{Name: "task", Arguments: map[string]any{"action": "get"}}); err == nil {
				cs.TaskErr = r.IsError
				for _, c := range r.Content {
					if tc, ok := c.(*sdk.TextContent); ok {
						cs.TaskCall += tc.Text
					}
				}
			}
			_ = s.Close()
		}
		cancel()
	}
	a.mu.Lock()
	a.starts = append(a.starts, cs)
	a.mu.Unlock()
	return a.Adapter.Start(ctx, pm, spec)
}

func (a *captureAdapter) all() []*capturedStart {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*capturedStart(nil), a.starts...)
}

// openWith is open() with extra adapters (the scripted fake agent backs "claude" unless overridden).
func openWith(t *testing.T, extra map[string]agents.Adapter) *app {
	return openScripted(t, extra, fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}},
	})
}

func openScripted(t *testing.T, extra map[string]agents.Adapter, sc fakeagent.Script) *app {
	t.Helper()
	dir := t.TempDir()
	script, _ := json.Marshal(sc)
	sp := filepath.Join(dir, "script.json")
	os.WriteFile(sp, script, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	ad := map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}
	for k, v := range extra {
		ad[k] = v
	}
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", LeaseTTL: 3 * time.Second, Adapters: ad})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}
}

func (a *app) dataDir() string {
	var info struct {
		DataDir string `json:"dataDir"`
	}
	a.call("app.info", nil, &info)
	return info.DataDir
}

func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&buf, r); close(done) }()
	var once sync.Once
	stop := func() string {
		once.Do(func() { w.Close(); os.Stderr = old; <-done })
		return buf.String()
	}
	t.Cleanup(func() { stop() })
	return stop
}

// leaks reports where the secret appears: in any database table (text of every column), in any file under the data directory.
func leaks(t *testing.T, a *app, secret string) []string {
	t.Helper()
	var where []string
	rows, err := a.c.DB.Reader().Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	for _, tb := range tables {
		rs, err := a.c.DB.Reader().Query(`SELECT * FROM "` + tb + `"`)
		if err != nil {
			continue // an FTS shadow table the reader cannot select from
		}
		cols, _ := rs.Columns()
		for rs.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if rs.Scan(ptrs...) != nil {
				continue
			}
			for _, v := range vals {
				if strings.Contains(v.String, secret) {
					where = append(where, "table "+tb)
				}
			}
		}
		rs.Close()
	}
	filepath.Walk(a.dataDir(), func(p string, info os.FileInfo, err error) error {
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte(secret)) {
			where = append(where, "file "+p)
		}
		return nil
	})
	return where
}

func (a *app) eventsOf(typ string) []map[string]any {
	a.t.Helper()
	rows, err := a.c.DB.Reader().Query(`SELECT run_id, task_id, agent_id, data FROM events WHERE type=? ORDER BY seq`, typ)
	if err != nil {
		a.t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var runID, taskID, agentID string
		var data []byte
		rows.Scan(&runID, &taskID, &agentID, &data)
		m := map[string]any{}
		json.Unmarshal(data, &m)
		m["_run"], m["_task"], m["_agent"] = runID, taskID, agentID
		out = append(out, m)
	}
	return out
}

func (a *app) setup(name string) (core.Project, string) {
	repo := newRepo(a.t)
	var p core.Project
	a.call("project.create", map[string]any{"name": name, "repoPath": repo}, &p)
	return p, repo
}

// ---- (a) the real pipeline gets the entry, and the run ends with the turn ----

func TestPipelineTurnsGetOneGatewayEntryAndTheRunEndsWithTheTurn(t *testing.T) {
	stderr := captureStderr(t)
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}, probe: true}
	a := openWith(t, map[string]agents.Adapter{"claude": capt})
	p, _ := a.setup("gocalc")
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})

	starts := capt.all()
	if len(starts) != 2 {
		t.Fatalf("a work turn and a review turn were expected, got %d starts", len(starts))
	}
	dataDir := a.dataDir()
	for i, s := range starts {
		who := []string{"work", "review"}[i]
		if s.Spec.MCPConfigPath == "" {
			t.Fatalf("%s turn: RunSpec.MCPConfigPath was not set", who)
		}
		if s.ProbeErr != "" {
			t.Fatalf("%s turn: %s", who, s.ProbeErr)
		}
		url, tok := parseSingleEntry(t, s.Cfg)
		if s.URL != url || s.Tok != tok {
			t.Fatal("capture mismatch")
		}
		runDir := regexp.MustCompile(`/mcp/(arun_[0-9a-f]+)$`).FindStringSubmatch(url)[1]
		if want := filepath.Join(dataDir, "runs", runDir, "mcp.json"); s.Spec.MCPConfigPath != want {
			t.Fatalf("config path %q, want %q", s.Spec.MCPConfigPath, want)
		}
		if st, err := os.Stat(s.Spec.MCPConfigPath); err == nil {
			t.Fatalf("the config file must be gone once the turn ended (it holds the token): %v", st.Mode())
		}
		// while the session was alive: the allowlist was the read-only default (plus, for the worker outside a sandbox, the
		// permission tool its CLI asks: ext_permprompt.go), and a tool outside it was refused
		want := []string{"fetch_range", "read_range", "search_memory", "search_requirements", "skills"}
		if who == "work" {
			want = append(want, "permission_prompt")
		}
		if !sameSet(s.Tools, want...) {
			t.Fatalf("%s turn tools: %v", who, s.Tools)
		}
		if !strings.Contains(s.ReadRange, "package gocalc") || !strings.Contains(s.ReadRange, "calc.go") {
			t.Fatalf("%s turn: read_range through the gateway returned %q", who, s.ReadRange)
		}
		if !s.TaskErr || !strings.Contains(s.TaskCall, "denied") {
			t.Fatalf("%s turn: the task tool is outside the default allowlist and must be refused, got err=%v %q", who, s.TaskErr, s.TaskCall)
		}
		// after the turn: the endpoint no longer accepts the token
		if code := endpointStatus(url, tok); code != http.StatusUnauthorized {
			t.Fatalf("%s turn: the ended run's token must be refused, got HTTP %d", who, code)
		}
		if !strings.Contains(strings.Join(s.Spec.AllowedTools, ","), "mcp__drydock__*") {
			t.Fatalf("Claude needs mcp__drydock__* allowed to call the tools without a prompt: %v", s.Spec.AllowedTools)
		}
	}
	// the audit trail: read_range allowed, task denied, per run, with the agent and task on the event
	calls, denied := a.eventsOf("mcp.call"), a.eventsOf("mcp.denied")
	var okRead, deniedTask int
	for _, e := range calls {
		if e["tool"] == "read_range" && e["decision"] == "allowed" && e["_task"] == task.ID && (e["_agent"] == worker.ID || e["_agent"] == reviewer.ID) {
			okRead++
		}
	}
	for _, e := range denied {
		if e["tool"] == "task" && e["_task"] == task.ID {
			deniedTask++
		}
	}
	if okRead != 2 || deniedTask != 2 {
		t.Fatalf("audit events: %d allowed read_range, %d denied task (want 2 and 2)\ncalls=%v\ndenied=%v", okRead, deniedTask, calls, denied)
	}
	// agent.mcp records the attachment (no token in it)
	var attached int
	for _, e := range a.eventsOf("agent.mcp") {
		if e["attached"] == true && e["transport"] == "stdio-shim" {
			attached++
		}
	}
	if attached != 2 {
		t.Fatalf("agent.mcp events: %d", attached)
	}
	// the context manifest lists the entry
	rows, err := a.c.DB.Reader().Query(`SELECT agent_id, items FROM context_manifests WHERE task_id=?`, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for rows.Next() {
		var ag, items string
		rows.Scan(&ag, &items)
		var its []struct {
			Source   string
			Included bool
			Reason   string
		}
		json.Unmarshal([]byte(items), &its)
		for _, it := range its {
			if it.Source == "mcp:drydock" {
				if !it.Included || !strings.Contains(it.Reason, "search_requirements") || strings.Contains(it.Reason, "task, ") {
					t.Fatalf("manifest entry: %+v", it)
				}
				seen[ag] = it.Reason
			}
		}
	}
	rows.Close()
	if seen[worker.ID] == "" || seen[reviewer.ID] == "" {
		t.Fatalf("both turns' manifests must list mcp:drydock: %v", seen)
	}

	a.call("run.respond", map[string]any{"runId": runID, "requestId": detail.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Completed
	})

	// the bearer tokens are in nothing the app keeps: not in events, not in any table, not in a file, not in the logs
	logs := stderr()
	for i, s := range starts {
		if w := leaks(t, a, s.Tok); len(w) > 0 {
			t.Fatalf("turn %d: the run token leaked into %v", i, w)
		}
		if strings.Contains(logs, s.Tok) {
			t.Fatalf("turn %d: the run token is in the app log", i)
		}
	}
}

// An adapter whose generated MCP configuration is unverified gets no MCP at all, and the manifest says why.
func TestUnverifiedAdapterGetsNoMCPAndTheManifestSaysWhy(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}, probe: true}
	a := openWith(t, map[string]agents.Adapter{"codex": capt})
	p, _ := a.setup("gocalc")
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Codex worker", Adapter: "codex", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})
	starts := capt.all()
	if len(starts) != 1 || starts[0].Spec.MCPConfigPath != "" || len(starts[0].Spec.AllowedTools) != 0 {
		t.Fatalf("an unverified adapter must be started without MCP: %+v", starts)
	}
	var reason string
	rows, _ := a.c.DB.Reader().Query(`SELECT items FROM context_manifests WHERE task_id=? AND agent_id=?`, task.ID, worker.ID)
	for rows.Next() {
		var items string
		rows.Scan(&items)
		var its []struct {
			Source   string
			Included bool
			Reason   string
		}
		json.Unmarshal([]byte(items), &its)
		for _, it := range its {
			if it.Source == "mcp:drydock" {
				if it.Included {
					t.Fatal("must be excluded")
				}
				reason = it.Reason
			}
		}
	}
	rows.Close()
	if !strings.Contains(reason, "codex") || !strings.Contains(reason, "unverified") {
		t.Fatalf("the manifest must record that MCP was not attached and why: %q", reason)
	}
	var notAttached int
	for _, e := range a.eventsOf("agent.mcp") {
		if e["attached"] == false && e["_agent"] == worker.ID && strings.Contains(fmt.Sprint(e["reason"]), "unverified") {
			notAttached++
		}
	}
	if notAttached != 1 {
		t.Fatalf("agent.mcp must record the exclusion: %d", notAttached)
	}
	var st struct {
		Running  bool
		Adapters []struct {
			Adapter  string
			Attached bool
		}
	}
	a.call("mcp.status", nil, &st)
	got := map[string]bool{}
	for _, r := range st.Adapters {
		got[r.Adapter] = r.Attached
	}
	if !st.Running || !got["claude"] || got["codex"] || got["gemini"] || got["antigravity"] {
		t.Fatalf("status %+v", st)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": detail.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Completed
	})
}

// ---- (b) a real MCP client against the shared gateway, over real data ----

const packPhrase = "Hold the flange torque wrench at QK-7731-ALPHA newton metres during calibration."

func installLocalPack(t *testing.T, a *app, projectID string) {
	t.Helper()
	docs := filepath.Join(t.TempDir(), "docs")
	os.MkdirAll(docs, 0o755)
	os.WriteFile(filepath.Join(docs, "flange.md"), []byte("# Flange\n\n## Calibration\n\n"+packPhrase+" The procedure is repeated yearly.\n"), 0o644)
	a.call("pack.install", map[string]string{"yaml": "schema: 1\nid: flange\ntitle: Flange manual\nkind: indexed\nvisibility: private\nsources:\n  - {type: local_dir, path: \"" + strings.ReplaceAll(docs, `\`, `/`) + "\"}\nattach: {instructions: \"Flange manual notes.\"}\n"}, nil)
	a.call("pack.refresh", map[string]string{"id": "flange"}, nil)
	a.call("pack.attach", map[string]string{"projectId": projectID, "packId": "flange", "role": "backend"}, nil)
}

func TestRealMCPClientAgainstTheSharedGatewayOverRealData(t *testing.T) {
	a := openWith(t, nil)
	repo := newRepo(t)
	os.MkdirAll(filepath.Join(repo, ".aet", "skills", "go-style"), 0o755)
	os.WriteFile(filepath.Join(repo, ".aet", "skills", "go-style", "SKILL.md"), []byte("---\nname: go-style\ndescription: Go style rules\nmetadata:\n  drydock.globs: \"**/*.go\"\n---\nKeep functions small.\n"), 0o644)
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "skills")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	installLocalPack(t, a, p.ID)
	var req struct{ ID string }
	a.call("requirement.add", map[string]any{"projectId": p.ID, "title": "Calculator PRD", "text": "# Calc\n\n## Subtract\n\nThe calculator shall subtract two integers using wraparound arithmetic.\n"}, &req)
	var mem struct{ ID string }
	a.call("memory.propose", map[string]any{"projectId": p.ID, "kind": "convention", "title": "Use gofmt", "body": "Always run gofmt before committing Go code."}, &mem)
	a.call("memory.accept", map[string]string{"id": mem.ID}, nil)
	a.call("skill.scan", map[string]string{"projectId": p.ID}, nil)
	a.call("skill.accept", map[string]string{"projectId": p.ID, "name": "go-style"}, nil)

	var backend, frontend core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend", Adapter: "claude", Role: "backend", Model: "fake-1"}, &backend)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Frontend", Adapter: "claude", Role: "frontend", Model: "fake-1"}, &frontend)
	a.call("skill.assign", map[string]any{"projectId": p.ID, "name": "go-style", "agentId": backend.ID, "on": true}, nil) // skills are given to agents (P76)
	dataDir := a.dataDir()

	spawn := func(ag core.Agent, taskID string) (*rt.Managed, *sdk.ClientSession, string, string) {
		t.Helper()
		m, err := a.c.Runtime.Spawn(context.Background(), rt.Config{AgentID: ag.ID, ProjectID: p.ID, TaskID: taskID, Adapter: "claude", Provider: "anthropic", Tier: "premium",
			Spec: agents.RunSpec{AgentID: ag.ID, Role: ag.Role, Cwd: repo, Model: "fake-1"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.Stop(context.Background()) })
		raw, err := os.ReadFile(filepath.Join(dataDir, "runs", m.RunID, "mcp.json"))
		if err != nil {
			t.Fatalf("config file of the session: %v", err)
		}
		url, tok := parseSingleEntry(t, raw)
		s, err := connectMCP(context.Background(), url, tok)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return m, s, url, tok
	}

	bm, bs, burl, btok := spawn(backend, "")
	fm, fs, furl, _ := spawn(frontend, "")

	// the allowlist is per agent: the pack is attached to the backend role only
	if got := toolNames(t, bs); !sameSet(got, "fetch_range", "read_range", "search_memory", "search_pack", "search_requirements", "skills") {
		t.Fatalf("backend tools: %v", got)
	}
	if got := toolNames(t, fs); !sameSet(got, "fetch_range", "read_range", "search_memory", "search_requirements", "skills") {
		t.Fatalf("frontend tools (no pack attached): %v", got)
	}

	// search_pack: cited results from the installed local pack, marked as untrusted data
	out, isErr := callTool(t, bs, "search_pack", map[string]any{"query": "flange torque wrench calibration"})
	if isErr {
		t.Fatalf("search_pack: %s", out)
	}
	for _, want := range []string{"QK-7731-ALPHA", `"pack":"flange"`, `"version":"`, `"url":"`, `"chunk_id":"`, `"fetched_at":"`, "Calibration", "<untrusted"} {
		if !strings.Contains(out, want) {
			t.Errorf("search_pack result lacks %q:\n%s", want, out)
		}
	}
	if out, _ := callTool(t, bs, "search_pack", map[string]any{"query": "kubernetes ingress controller"}); !strings.Contains(out, "nothing found") {
		t.Errorf("an unmatched query must say so: %s", out)
	}
	// a pack that is not attached cannot be named
	if out, isErr := callTool(t, bs, "search_pack", map[string]any{"query": "flange", "pack_ids": []string{"secret-pack"}}); !isErr || !strings.Contains(out, "not attached") {
		t.Errorf("unattached pack: err=%v %s", isErr, out)
	}
	// the frontend agent has no search_pack: refused, and audited
	if out, isErr := callTool(t, fs, "search_pack", map[string]any{"query": "flange"}); !isErr || !strings.Contains(out, "denied") {
		t.Errorf("frontend search_pack must be denied: %s", out)
	}
	// real data answers from requirements, memory and skills
	if out, isErr := callTool(t, bs, "search_requirements", map[string]any{"query": "subtract integers wraparound"}); isErr || !strings.Contains(out, "wraparound") || !strings.Contains(out, "REQ:") {
		t.Errorf("search_requirements: err=%v %s", isErr, out)
	}
	if out, isErr := callTool(t, bs, "search_memory", map[string]any{"query": "gofmt"}); isErr || !strings.Contains(out, "Always run gofmt") {
		t.Errorf("search_memory: err=%v %s", isErr, out)
	}
	if out, isErr := callTool(t, bs, "skills", map[string]any{"action": "list"}); isErr || !strings.Contains(out, "go-style") {
		t.Errorf("skills list: err=%v %s", isErr, out)
	}
	if out, isErr := callTool(t, bs, "skills", map[string]any{"action": "get", "name": "go-style"}); isErr || !strings.Contains(out, "Keep functions small") {
		t.Errorf("skills get: err=%v %s", isErr, out)
	}
	if out, isErr := callTool(t, bs, "read_range", map[string]any{"path": "calc.go", "start_line": 1, "end_line": 3}); isErr || !strings.Contains(out, "package gocalc") {
		t.Errorf("read_range: err=%v %s", isErr, out)
	}
	if out, isErr := callTool(t, bs, "read_range", map[string]any{"path": "../../etc/passwd"}); !isErr || strings.Contains(out, "root:") {
		t.Errorf("read_range must stay inside the worktree: err=%v %s", isErr, out)
	}
	if out, isErr := callTool(t, bs, "read_range", map[string]any{"path": ".git/config"}); !isErr {
		t.Errorf("git internals are never served: %s", out)
	}

	// repo_map: in a CGO build every role has it (the map of the worktree); without CGO it is not offered (decision P62)
	if out, isErr := callTool(t, bs, "repo_map", map[string]any{"token_budget": 512}); repomap.Available() {
		if isErr || !strings.Contains(out, "calc.go") || !strings.Contains(out, "Repo map:") {
			t.Errorf("repo_map: err=%v %s", isErr, out)
		}
	} else if !isErr || !strings.Contains(out, "denied") {
		t.Errorf("repo_map without a parser must be refused: %s", out)
	}
	// outside the allowlist: refused with an audit event, and the act tools are absent from the default policy
	for _, tool := range []string{"task", "room", "handoff"} {
		if out, isErr := callTool(t, bs, tool, map[string]any{"action": "get"}); !isErr || !strings.Contains(out, "denied") {
			t.Errorf("%s must be refused: %s", tool, out)
		}
	}
	// a name that does not exist at all
	if out, isErr := callTool(t, bs, "gh__create_pull_request", map[string]any{}); !isErr || !strings.Contains(out, "denied") {
		t.Errorf("unknown tool: %s", out)
	}
	deniedTools := map[string]bool{}
	for _, e := range a.eventsOf("mcp.denied") {
		if e["_run"] == bm.RunID {
			deniedTools[fmt.Sprint(e["tool"])] = true
		}
		if e["_run"] == fm.RunID && e["tool"] == "search_pack" {
			deniedTools["frontend:search_pack"] = true
		}
	}
	for _, w := range []string{"task", "room", "handoff"} {
		if !deniedTools[w] {
			t.Errorf("no mcp.denied audit event for %s: %v", w, deniedTools)
		}
	}
	if !deniedTools["frontend:search_pack"] {
		t.Errorf("no mcp.denied audit event for the frontend's search_pack")
	}
	var allowedSearch bool
	for _, e := range a.eventsOf("mcp.call") {
		if e["_run"] == bm.RunID && e["tool"] == "search_pack" && e["decision"] == "allowed" && e["_agent"] == backend.ID && e["_task"] == "" {
			allowedSearch = true
		}
	}
	if !allowedSearch {
		t.Error("no mcp.call audit event for the allowed search_pack")
	}

	// a run's token opens only its own endpoint
	if code := endpointStatus(furl, btok); code != http.StatusUnauthorized {
		t.Errorf("backend token on the frontend endpoint: HTTP %d", code)
	}
	if code := endpointStatus(burl, btok); code == http.StatusUnauthorized || code == -1 {
		t.Errorf("backend token on its own endpoint: HTTP %d", code)
	}
	if len(a.eventsOf("mcp.auth_failed")) == 0 {
		t.Error("a refused token must leave an mcp.auth_failed event")
	}

	// stopping the session ends the run: the endpoint stops accepting its token
	if err := bm.Stop(context.Background()); err != nil {
		t.Logf("stop: %v", err)
	}
	if code := endpointStatus(burl, btok); code != http.StatusUnauthorized {
		t.Errorf("after Stop the token must be refused, HTTP %d", code)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "runs", bm.RunID, "mcp.json")); err == nil {
		t.Error("the config file must be removed with the run")
	}
	if w := leaks(t, a, btok); len(w) > 0 {
		t.Errorf("token leaked into %v", w)
	}
}

// The stateful tools exist and work over the real services, but only when a human turned them on for the project.
func TestActToolsOverTaskRoomAndHandoffServices(t *testing.T) {
	a := openWith(t, nil)
	p, repo := a.setup("gocalc")
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b.", Acceptance: []string{"Given 5 and 3 When Subtract Then 2"}}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var other tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Another task"}, &other)
	dataDir := a.dataDir()

	spawn := func(ag core.Agent) (*sdk.ClientSession, *rt.Managed) {
		m, err := a.c.Runtime.Spawn(context.Background(), rt.Config{AgentID: ag.ID, ProjectID: p.ID, TaskID: task.ID, Adapter: "claude", Provider: "anthropic", Tier: "premium",
			Spec: agents.RunSpec{AgentID: ag.ID, Role: ag.Role, Cwd: repo, Model: "fake-1"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.Stop(context.Background()) })
		raw, err := os.ReadFile(filepath.Join(dataDir, "runs", m.RunID, "mcp.json"))
		if err != nil {
			t.Fatal(err)
		}
		url, tok := parseSingleEntry(t, raw)
		s, err := connectMCP(context.Background(), url, tok)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s, m
	}

	// off by default
	s0, _ := spawn(worker)
	if got := toolNames(t, s0); !sameSet(got, "fetch_range", "read_range", "search_memory", "search_requirements", "skills") {
		t.Fatalf("default tools: %v", got)
	}
	// a human turns them on for the project
	a.call("mcp.actTools.set", map[string]any{"projectId": p.ID, "enabled": true}, nil)
	var chk struct{ Enabled bool }
	a.call("mcp.actTools.get", map[string]string{"projectId": p.ID}, &chk)
	if !chk.Enabled {
		t.Fatal("setting")
	}
	var n int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='mcp.act_tools' AND actor='human:kyle'`).Scan(&n)
	if n != 1 {
		t.Fatalf("turning on state-changing tools is an audited human decision: %d rows", n)
	}

	ws, wm := spawn(worker)
	if got := toolNames(t, ws); !sameSet(got, "fetch_range", "read_range", "search_memory", "search_requirements", "skills", "task", "room", "handoff") {
		t.Fatalf("worker tools: %v", got)
	}
	rs, _ := spawn(reviewer)
	if got := toolNames(t, rs); !sameSet(got, "fetch_range", "read_range", "search_memory", "search_requirements", "skills", "room", "handoff") {
		t.Fatalf("a reviewer gets room and handoff, not task: %v", got)
	}

	// task: get, note, proposal (never a status change), the run's own task only
	out, isErr := callTool(t, ws, "task", map[string]any{"action": "get"})
	if isErr || !strings.Contains(out, "Implement Subtract") || !strings.Contains(out, "Given 5 and 3") {
		t.Fatalf("task get: err=%v %s", isErr, out)
	}
	if out, isErr := callTool(t, ws, "task", map[string]any{"action": "get", "task_id": other.ID}); !isErr || !strings.Contains(out, "this run's task") {
		t.Fatalf("another task: %s", out)
	}
	if out, isErr := callTool(t, ws, "task", map[string]any{"action": "propose_status", "status": "COMPLETED"}); !isErr {
		t.Fatalf("COMPLETED is not a proposable status: %s", out)
	}
	if out, isErr := callTool(t, ws, "task", map[string]any{"action": "add_note", "note": "Found the bug in Subtract: it returns zero."}); isErr {
		t.Fatalf("add_note: %s", out)
	}
	if out, isErr := callTool(t, ws, "task", map[string]any{"action": "propose_status", "status": "BLOCKED", "evidence_refs": []string{"log:abc"}}); isErr {
		t.Fatalf("propose_status: %s", out)
	}
	var d core.TaskDetail
	a.call("task.get", map[string]string{"id": task.ID}, &d)
	if d.Task.Status != tasks.Backlog && d.Task.Status != tasks.Ready {
		t.Fatalf("a proposal must not move the task: %s", d.Task.Status)
	}
	var msgs []room.Message
	a.call("room.messages", map[string]any{"roomId": d.RoomID, "limit": 50}, &msgs)
	var note, prop bool
	for _, m := range msgs {
		if m.Author.Key() == worker.ID && m.Type == room.TypeStatus && strings.Contains(m.Body, "Found the bug") {
			note = true
		}
		if m.Author.Key() == worker.ID && m.Type == room.TypeQuestion && strings.Contains(m.Body, "BLOCKED") && strings.Contains(m.Body, "log:abc") {
			prop = true
		}
	}
	if !note || !prop {
		t.Fatalf("note=%v proposal=%v in %d messages", note, prop, len(msgs))
	}
	var needs []room.NeedsYouItem
	a.call("needsyou.list", map[string]string{"projectId": p.ID}, &needs)
	if len(needs) == 0 {
		t.Error("a proposal to stop must reach Needs you")
	}
	if len(a.eventsOf("task.status_proposed")) != 1 {
		t.Error("task.status_proposed event")
	}

	// room: read the task room, post into it (typed, redacted), channels of the project by name, DMs never
	if out, isErr := callTool(t, ws, "room", map[string]any{"action": "read"}); isErr || !strings.Contains(out, "Found the bug") {
		t.Fatalf("room read: err=%v %s", isErr, out)
	}
	secret := "ghp_1a2B3c4D5e6F7g8H9i0J1k2L3m4N5o6P7q8R"
	if out, isErr := callTool(t, ws, "room", map[string]any{"action": "post", "kind": "STATUS", "body": "Subtract fixed. token " + secret}); isErr || !strings.Contains(out, "posted msg_") {
		t.Fatalf("room post: err=%v %s", isErr, out)
	}
	a.call("room.messages", map[string]any{"roomId": d.RoomID, "limit": 50}, &msgs)
	var posted bool
	for _, m := range msgs {
		if strings.Contains(m.Body, "Subtract fixed") {
			posted = true
			if strings.Contains(m.Body, secret) || m.Author.Key() != worker.ID || m.Trust != room.TrustUntrusted {
				t.Fatalf("posted message: %+v", m)
			}
		}
	}
	if !posted {
		t.Fatal("the posted message is missing")
	}
	if out, isErr := callTool(t, ws, "room", map[string]any{"action": "post", "kind": "CHAT", "channel": "no-such-channel", "body": "x"}); !isErr || !strings.Contains(out, "no channel") {
		t.Fatalf("unknown channel: %s", out)
	}
	if out, isErr := callTool(t, ws, "room", map[string]any{"action": "read", "channel": "general", "limit": 5}); isErr {
		t.Fatalf("channel by name: %s", out)
	}
	var dm room.Room
	a.call("room.createDM", map[string]any{"projectId": p.ID, "agentId": worker.ID}, &dm)
	if dm.ID == "" {
		t.Fatal("DM was not created")
	}
	if out, isErr := callTool(t, ws, "room", map[string]any{"action": "read", "channel": dm.Name}); !isErr {
		t.Fatalf("a DM is never reachable from a tool: %s", out)
	}

	// handoff: create and read back; a different project cannot read it
	out, isErr = callTool(t, ws, "handoff", map[string]any{"action": "create", "summary": "Fixed Subtract; tests still to run.", "done": []string{"fix"}, "open": []string{"run tests"}, "files": []string{"calc.go"}})
	if isErr || !strings.Contains(out, "handoff created: hnd_") {
		t.Fatalf("handoff create: err=%v %s", isErr, out)
	}
	hid := regexp.MustCompile(`hnd_[0-9a-f]+`).FindString(out)
	if out, isErr := callTool(t, rs, "handoff", map[string]any{"action": "get", "handoff_id": hid}); isErr || !strings.Contains(out, "tests still to run") || !strings.Contains(out, "run tests") {
		t.Fatalf("handoff get by the reviewer of the same task: err=%v %s", isErr, out)
	}
	if out, isErr := callTool(t, ws, "handoff", map[string]any{"action": "create", "summary": "x", "files": []string{"/etc/passwd"}}); !isErr {
		t.Fatalf("a file outside the worktree is refused: %s", out)
	}
	var stored string
	a.c.DB.Reader().QueryRow(`SELECT artifact FROM handoffs WHERE id=?`, hid).Scan(&stored)
	if !strings.Contains(stored, "Fixed Subtract") {
		t.Fatalf("stored handoff %q", stored)
	}
	if len(a.eventsOf("handoff.created")) != 1 {
		t.Error("handoff.created event")
	}

	// the session ends: all of it stops
	wm.Stop(context.Background())
	if _, err := ws.ListTools(context.Background(), nil); err == nil {
		t.Error("the ended run still answers")
	}
}

// ---- (c) a real Claude turn with the generated config ----

func TestRealClaudeCallsTheSearchPackToolThroughTheGeneratedConfig(t *testing.T) {
	if why := contract.RealClaudeSkip(); why != "" {
		t.Skip(why)
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "")
	det, err := claude.New().Detect(context.Background())
	if err != nil || det.Status != agents.StatusReady {
		t.Skipf("real claude not usable here: status=%s problems=%v err=%v", det.Status, det.Problems, err)
	}
	t.Logf("real claude %s (%s)", det.Version, det.AuthDetail)
	for _, mode := range []string{"acceptEdits", "plan"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", LeaseTTL: 3 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(c.Close)
			a := &app{t: t, c: c}
			p, repo := a.setup("gocalc")
			installLocalPack(t, a, p.ID)
			var ag core.Agent
			a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Haiku", Adapter: "claude", Role: "backend", Model: "haiku", PermissionMode: mode}, &ag)

			ctx, cancel := context.WithTimeout(context.Background(), 170*time.Second)
			defer cancel()
			m, err := c.Runtime.Spawn(ctx, rt.Config{AgentID: ag.ID, ProjectID: p.ID, Adapter: "claude", Provider: "anthropic", Tier: "premium",
				Spec: agents.RunSpec{AgentID: ag.ID, Role: "backend", Cwd: repo, Model: "haiku", PermissionMode: mode, ExtraArgs: []string{"--no-session-persistence"}}})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Stop(context.Background())
			prompt := "Call the search_pack tool of the MCP server named drydock with the query \"flange torque wrench calibration\". " +
				"Then reply in one line with the exact torque value from the result and the citation: pack name, version and URL. Do not guess; use only the tool result."
			res, err := m.RunTurn(ctx, prompt, nil, nil)
			if err != nil {
				t.Fatalf("turn: %v", err)
			}
			var cost float64
			for _, u := range res.Usage {
				cost += u.CostUSD
			}
			t.Logf("[%s] reply: %s", mode, res.Text)
			t.Logf("[%s] tool calls seen by the runtime: %d, cost $%.4f", mode, res.ToolCalls, cost)
			var arrived bool
			for _, e := range a.eventsOf("mcp.call") {
				if e["_run"] == m.RunID {
					t.Logf("[%s] gateway audit: tool=%v decision=%v returned_bytes=%v", mode, e["tool"], e["decision"], e["returned_bytes"])
					if e["tool"] == "search_pack" && e["decision"] == "allowed" {
						arrived = true
					}
				}
			}
			for _, e := range a.eventsOf("mcp.denied") {
				if e["_run"] == m.RunID {
					t.Logf("[%s] gateway denied: %v", mode, e)
				}
			}
			if !arrived {
				t.Fatalf("[%s] the search_pack call never reached the gateway (reply: %s)", mode, res.Text)
			}
			if !strings.Contains(res.Text, "QK-7731-ALPHA") || !strings.Contains(strings.ToLower(res.Text), "flange") {
				t.Fatalf("[%s] the reply does not carry the pack's value and citation: %s", mode, res.Text)
			}
		})
	}
}

// Room replies are agent turns too: the responder spawns through the same Runtime, so the reply gets the read-only entry, scoped
// to the project root (there is no task worktree in a channel), and its run ends with the reply.
func TestRoomReplyTurnGetsTheGatewayEntryToo(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}, probe: true}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, fakeagent.Script{Default: []fakeagent.Step{{Say: "I looked at it"}}})
	p, repo := a.setup("gocalc")
	var dev map[string]any
	a.call("projectdev.set", map[string]any{"projectId": p.ID, "settings": map[string]any{"MaxParallel": 1, "Respond": true}}, &dev)
	var alice core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Alice", Adapter: "claude", Role: "backend", Model: "fake-1"}, &alice)
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": p.ID}, &rooms)
	var general room.Room
	for _, r := range rooms {
		if r.Name == "general" {
			general = r
		}
	}
	a.call("room.post", map[string]any{"roomId": general.ID, "body": "@" + alice.ID + " what does Add do?"}, nil)
	a.waitFor("the reply", func() bool {
		var ms []room.Message
		a.call("room.messages", map[string]any{"roomId": general.ID, "limit": 50}, &ms)
		for _, m := range ms {
			if m.Author.Key() == alice.ID && strings.Contains(m.Body, "I looked at it") {
				return true
			}
		}
		return false
	})
	starts := capt.all()
	if len(starts) != 1 {
		t.Fatalf("%d starts", len(starts))
	}
	s := starts[0]
	if s.Spec.MCPConfigPath == "" || s.ProbeErr != "" {
		t.Fatalf("the reply turn has no MCP entry: %q %s", s.Spec.MCPConfigPath, s.ProbeErr)
	}
	parseSingleEntry(t, s.Cfg)
	if s.Spec.PermissionMode != "plan" || !s.Spec.ReadOnly || s.Spec.Cwd != repo {
		t.Fatalf("a reply is read-only at the project root: %+v", s.Spec)
	}
	// the workspace tools are allowed; Read is not allowed tool-wide (that would reach files outside the project): inside
	// its working folder Claude reads without a rule
	if allowed := strings.Join(s.Spec.AllowedTools, ","); allowed != "mcp__drydock__*" {
		t.Fatalf("allowed tools %v", s.Spec.AllowedTools)
	}
	if !sameSet(s.Tools, "fetch_range", "read_range", "search_memory", "search_requirements", "skills") {
		t.Fatalf("tools %v", s.Tools)
	}
	if !strings.Contains(s.ReadRange, "package gocalc") {
		t.Fatalf("read_range at the project root: %q", s.ReadRange)
	}
	a.waitFor("the run to end", func() bool { return endpointStatus(s.URL, s.Tok) == http.StatusUnauthorized })
}

// A run on a trainable provider route gets no Workspace tools at all (security.md section 9): it is not given a gateway entry, and the
// event says why.
func TestTrainableProviderRouteGetsNoMCP(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	dir := t.TempDir()
	script, _ := json.Marshal(fakeagent.Script{Default: []fakeagent.Step{{Say: "ok"}}})
	sp := filepath.Join(dir, "script.json")
	os.WriteFile(sp, script, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", Credentials: providers.NewMemoryCredentialStoreForTests(),
		Adapters: map[string]agents.Adapter{"claude": capt}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	p, repo := a.setup("gocalc")
	var tp providers.Provider
	a.call("provider.create", map[string]any{"Name": "Trainable", "Kind": "direct", "BaseURL": "http://127.0.0.1:9/v1", "APIKey": "sk-other-key-1234567890", "DataClass": "trainable", "Tier": "free"}, &tp)
	var free, priv core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Free", Adapter: "claude", Role: "backend", Model: "m", ProviderID: tp.ID}, &free)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Own", Adapter: "claude", Role: "backend", Model: "m"}, &priv)
	for _, ag := range []core.Agent{free, priv} {
		m, err := c.Runtime.Spawn(context.Background(), rt.Config{AgentID: ag.ID, ProjectID: p.ID, Adapter: "claude", Spec: agents.RunSpec{AgentID: ag.ID, Role: "backend", Cwd: repo, Model: "m"}})
		if err != nil {
			t.Fatal(err)
		}
		m.Stop(context.Background())
	}
	starts := capt.all()
	if len(starts) != 2 || starts[0].Spec.MCPConfigPath != "" || starts[1].Spec.MCPConfigPath == "" {
		t.Fatalf("only the private-route agent gets MCP: %+v", starts)
	}
	var why string
	for _, e := range a.eventsOf("agent.mcp") {
		if e["_agent"] == free.ID {
			why = fmt.Sprint(e["reason"])
		}
	}
	if !strings.Contains(why, "trainable") {
		t.Fatalf("reason %q", why)
	}
}
