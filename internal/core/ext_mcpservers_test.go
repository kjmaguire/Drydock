package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/mcp/mcptestserver"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// A real stdio MCP server (internal/mcp/mcptestserver) that this test binary re-executes (see TestMain).
const testMCPEnv = "DRYDOCK_CORE_TEST_MCP"

func serveTestMCPIfAsked() {
	if os.Getenv(testMCPEnv) == "1" {
		os.Exit(mcptestserver.Run())
	}
}

// scenarioAdapter wraps the scripted agent and, while a session is alive, acts as the agent CLI's MCP client of the run's endpoint.
type scenarioAdapter struct {
	agents.Adapter
	mu    sync.Mutex
	steps map[string]func(ctx context.Context, s *sdk.ClientSession) // by agent id
	out   map[string][]string
}

func (a *scenarioAdapter) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	a.mu.Lock()
	step := a.steps[spec.AgentID]
	a.mu.Unlock()
	if step != nil && spec.MCPConfigPath != "" {
		raw, err := os.ReadFile(spec.MCPConfigPath)
		if err == nil {
			var f mcpConfigFile
			if json.Unmarshal(raw, &f) == nil {
				e := f.MCPServers["drydock"]
				pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
				if s, err := connectMCP(pctx, e.Env["DRYDOCK_MCP_URL"], e.Env["DRYDOCK_MCP_TOKEN"]); err == nil {
					step(pctx, s)
					_ = s.Close()
				}
				cancel()
			}
		}
	}
	return a.Adapter.Start(ctx, pm, spec)
}

func (a *scenarioAdapter) note(agent, line string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.out[agent] = append(a.out[agent], line)
}

func openMCPApp(t *testing.T, ad agents.Adapter) *app {
	t.Helper()
	dir := t.TempDir()
	script, _ := json.Marshal(fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}},
	})
	sp := filepath.Join(dir, "script.json")
	os.WriteFile(sp, script, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Credentials: providers.NewMemoryCredentialStoreForTests(), Adapters: map[string]agents.Adapter{"claude": ad}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}
}

type mcpApproval struct {
	ID, Server, Tool, Args string
}

func (a *app) pendingMCP(project string) []mcpApproval {
	var got []mcpApproval
	a.call("mcpserver.approval.list", map[string]string{"projectId": project}, &got)
	return got
}

func TestUserMCPServerEndToEnd(t *testing.T) {
	self, _ := os.Executable()
	notes := filepath.Join(t.TempDir(), "notes.txt")
	const secret = "sekret-api-key-0123456789"

	sc := &scenarioAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}, steps: map[string]func(context.Context, *sdk.ClientSession){}, out: map[string][]string{}}
	a := openMCPApp(t, sc)
	p, _ := a.setup("gocalc")
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)

	// refused before anything is stored
	for name, in := range map[string]core.MCPServerInput{
		"credentials in the command line": {ID: "tst", Display: "x", Transport: "stdio", Argv: []string{self, "--api-key=abcdefghijkl"}},
		"plain http to a remote host":     {ID: "tst", Display: "x", Transport: "http", URL: "http://example.com/mcp"},
		"a reserved id":                   {ID: "drydock", Display: "x", Transport: "stdio", Argv: []string{self}},
		"a bearer token on a stdio one":   {ID: "tst", Display: "x", Transport: "stdio", Argv: []string{self}, BearerToken: "abc"},
		"a secret with no value":          {ID: "tst", Display: "x", Transport: "stdio", Argv: []string{self}, SecretEnv: map[string]string{"API_KEY": ""}},
		"a bad variable name":             {ID: "tst", Display: "x", Transport: "stdio", Argv: []string{self}, Env: map[string]string{"A B": "1"}},
	} {
		if err := a.callErr("mcplib.save", in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	var none []core.MCPLibraryServer
	a.call("mcplib.list", nil, &none)
	if len(none) != 0 {
		t.Fatalf("a refused server was stored: %+v", none)
	}

	// add the server, find its tools, declare echo and envlen read-only, grant it to the worker only
	var lib core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "tst", Display: "Test server", Transport: "stdio", Argv: []string{self},
		Env: map[string]string{testMCPEnv: "1", "NOTES_FILE": notes}, SecretEnv: map[string]string{"API_KEY": secret}, ReadOnlyTools: []string{"echo", "envlen"}}, &lib)
	if len(lib.SecretEnv) != 1 || lib.SecretEnv[0] != "API_KEY" || !lib.Guard || len(lib.Projects) != 0 {
		t.Fatalf("saved %+v", lib)
	}
	raw, _ := json.Marshal(lib)
	if strings.Contains(string(raw), secret) {
		t.Fatal("the view carries the secret")
	}
	a.call("mcplib.discover", map[string]string{"id": "tst"}, &lib)
	names := map[string]bool{}
	for _, tl := range lib.Tools {
		names[tl.Name] = true
	}
	if !names["echo"] || !names["envlen"] || !names["post_note"] || lib.DiscoveredAt == 0 {
		t.Fatalf("discovered %+v", lib.Tools)
	}
	// a project can only grant a server it has
	if err := a.callErr("mcpserver.access.set", map[string]any{"projectId": p.ID, "serverId": "tst", "agents": []core.MCPAccess{{AgentID: worker.ID}}}); err == nil {
		t.Fatal("a grant for a server the project does not have was accepted")
	}
	var srv core.MCPServerView
	a.call("mcpserver.add", map[string]string{"projectId": p.ID, "serverId": "tst"}, &srv)
	if !srv.Enabled || srv.ProjectID != p.ID {
		t.Fatalf("added %+v", srv)
	}
	a.call("mcpserver.access.set", map[string]any{"projectId": p.ID, "serverId": "tst", "agents": []core.MCPAccess{{AgentID: worker.ID}}}, &srv)
	if len(srv.Agents) != 1 || srv.Agents[0].AgentID != worker.ID || strings.Join(srv.Agents[0].Tools, ",") != "*" {
		t.Fatalf("access %+v", srv.Agents)
	}
	if err := a.callErr("mcpserver.access.set", map[string]any{"projectId": p.ID, "serverId": "tst", "agents": []core.MCPAccess{{AgentID: "agt_other"}}}); err == nil {
		t.Fatal("a grant to an unknown agent was accepted")
	}

	var toolsSeen []string
	defer func() {
		sc.mu.Lock()
		t.Logf("worker tools %v; worker calls %v", toolsSeen, sc.out[worker.ID])
		sc.mu.Unlock()
	}()
	// the worker's run: the server's tools are listed, the read-only tool runs, the secret reached the server but never the agent,
	// the egress guard stops code from leaving, and every write waits for the human
	sc.steps[worker.ID] = func(ctx context.Context, s *sdk.ClientSession) {
		lt, _ := s.ListTools(ctx, nil)
		for _, tl := range lt.Tools {
			toolsSeen = append(toolsSeen, tl.Name)
		}
		call := func(tool string, args map[string]any) string {
			res, err := s.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
			if err != nil {
				return "ERR " + err.Error()
			}
			var sb strings.Builder
			for _, c := range res.Content {
				if tc, ok := c.(*sdk.TextContent); ok {
					sb.WriteString(tc.Text)
				}
			}
			if res.IsError {
				return "TOOLERR " + sb.String()
			}
			return sb.String()
		}
		sc.note(worker.ID, "echo|"+call("tst__echo", map[string]any{"text": "hi"}))
		sc.note(worker.ID, "envlen|"+call("tst__envlen", map[string]any{"text": "API_KEY"}))
		sc.note(worker.ID, "fence|"+call("tst__echo", map[string]any{"text": "```go\nfunc main() {}\n```"}))
		sc.note(worker.ID, "note1|"+call("tst__post_note", map[string]any{"text": "first"}))
		sc.note(worker.ID, "note2|"+call("tst__post_note", map[string]any{"text": "second"}))
	}
	var reviewerTools []string
	sc.steps[reviewer.ID] = func(ctx context.Context, s *sdk.ClientSession) {
		lt, _ := s.ListTools(ctx, nil)
		for _, tl := range lt.Tools {
			reviewerTools = append(reviewerTools, tl.Name)
		}
	}

	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Make Subtract return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)

	// the human answers the two write calls: approve the first, reject the second
	decide := func(want string, approve bool) {
		var ap mcpApproval
		a.waitFor("an MCP approval for "+want, func() bool {
			for _, x := range a.pendingMCP(p.ID) {
				if strings.Contains(x.Args, want) {
					ap = x
					return true
				}
			}
			return false
		})
		if ap.Server != "tst" || ap.Tool != "post_note" {
			t.Fatalf("approval %+v", ap)
		}
		a.call("mcpserver.approval.decide", map[string]any{"approvalId": ap.ID, "approve": approve}, nil)
	}
	decide("first", true)
	decide("second", false)

	var detail core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})

	for _, want := range []string{"tst__echo", "tst__envlen", "tst__post_note", "read_range"} {
		found := false
		for _, n := range toolsSeen {
			found = found || n == want
		}
		if !found {
			t.Errorf("worker tools %v lack %s", toolsSeen, want)
		}
	}
	for _, n := range reviewerTools {
		if strings.HasPrefix(n, "tst__") {
			t.Errorf("the reviewer was never granted the server but sees %s", n)
		}
	}
	got := map[string]string{}
	for _, l := range sc.out[worker.ID] {
		k, v, _ := strings.Cut(l, "|")
		got[k] = v
	}
	if !strings.Contains(got["echo"], "echo: hi") {
		t.Errorf("read-only tool: %q", got["echo"])
	}
	if got["envlen"] != "API_KEY="+fmt.Sprint(len(secret)) && !strings.Contains(got["envlen"], fmt.Sprint(len(secret))) {
		t.Errorf("the secret did not reach the server's environment: %q", got["envlen"])
	}
	if !strings.Contains(got["fence"], "blocked before leaving the machine") {
		t.Errorf("the egress guard did not stop code: %q", got["fence"])
	}
	if !strings.Contains(got["note1"], "posted: first") {
		t.Errorf("approved write: %q", got["note1"])
	}
	if !strings.Contains(got["note2"], "was not approved") {
		t.Errorf("rejected write: %q", got["note2"])
	}
	body, _ := os.ReadFile(notes)
	if strings.TrimSpace(string(body)) != "first" {
		t.Errorf("only the approved call may have run, notes file = %q", body)
	}

	// the secret is nowhere: not in any table, event or file
	if where := leaks(t, a, secret); len(where) != 0 {
		t.Errorf("the secret leaked into %v", where)
	}

	// removal: the server and its grants are gone and the secret is deleted from the store
	a.call("mcplib.delete", map[string]string{"id": "tst"}, nil)
	a.call("mcplib.list", nil, &none)
	var inProject []core.MCPServerView
	a.call("mcpserver.list", map[string]string{"projectId": p.ID}, &inProject)
	if len(none) != 0 || len(inProject) != 0 {
		t.Fatalf("not deleted: library %+v project %+v", none, inProject)
	}
}

// The real Claude Code CLI, driven through Core.Runtime.Spawn exactly as a task turn is, uses a server the human added and
// receives the agent's purpose: both reach it only through what Drydock generates (the gateway entry and --append-system-prompt-file).
func TestRealClaudeUsesAUserAddedServerAndReceivesThePurpose(t *testing.T) {
	if os.Getenv("DRYDOCK_NO_REAL_AGENTS") == "1" {
		t.Skip("DRYDOCK_NO_REAL_AGENTS=1")
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "")
	det, err := claude.New().Detect(context.Background())
	if err != nil || det.Status != agents.StatusReady {
		t.Skipf("real claude not usable here: status=%s problems=%v err=%v", det.Status, det.Problems, err)
	}
	t.Logf("real claude %s (%s)", det.Version, det.AuthDetail)
	self, _ := os.Executable()
	dir := t.TempDir()
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", LeaseTTL: 3 * time.Second, Credentials: providers.NewMemoryCredentialStoreForTests()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	p, repo := a.setup("gocalc")
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Haiku", Adapter: "claude", Role: "backend", Model: "haiku", Effort: "low", PermissionMode: "acceptEdits",
		Purpose: "Begin every final reply with the single word PINEAPPLE."}, &ag)
	var lib core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "tst", Display: "Test", Transport: "stdio", Argv: []string{self}, Env: map[string]string{testMCPEnv: "1"}, ReadOnlyTools: []string{"echo"}}, &lib)
	var srv core.MCPServerView
	// dropping the server onto the agent is one call: it puts the server into the project and grants it
	a.call("mcpserver.grant", map[string]any{"projectId": p.ID, "serverId": "tst", "agentId": ag.ID, "on": true}, &srv)

	ctx, cancel := context.WithTimeout(context.Background(), 170*time.Second)
	defer cancel()
	m, err := c.Runtime.Spawn(ctx, rt.Config{AgentID: ag.ID, ProjectID: p.ID, Adapter: "claude", Provider: "anthropic", Tier: "premium",
		Spec: agents.RunSpec{AgentID: ag.ID, Role: "backend", Cwd: repo, Model: "haiku", Effort: "low", PermissionMode: "acceptEdits", ExtraArgs: []string{"--no-session-persistence"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	res, err := m.RunTurn(ctx, "Use the tool named tst__echo from the MCP server drydock with the text hello-from-claude, then tell me what the tool returned, quoting its text.", nil, nil)
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	t.Logf("reply: %s", res.Text)
	var reached bool
	for _, e := range a.eventsOf("mcp.call") {
		if e["_run"] == m.RunID {
			t.Logf("gateway audit: server=%v tool=%v decision=%v", e["server"], e["tool"], e["decision"])
			if e["server"] == "tst" && e["tool"] == "echo" && e["decision"] == "allowed" {
				reached = true
			}
		}
	}
	var withInstructions bool
	for _, e := range a.eventsOf("agent.started") {
		if e["_run"] == m.RunID || e["agentRun"] == m.RunID {
			withInstructions = e["instructions"] == true
		}
	}
	if !withInstructions {
		t.Errorf("agent.started did not report that the purpose was sent")
	}
	if !reached {
		t.Fatalf("the call to the user-added server never reached the gateway (reply: %s)", res.Text)
	}
	if !strings.Contains(res.Text, "hello-from-claude") {
		t.Errorf("the reply does not carry the tool result: %s", res.Text)
	}
	// Whether the MODEL obeys a soft standing instruction is its choice and varies from run to run, so it is reported, not asserted.
	// What is asserted above is deterministic: the purpose was sent (agent.started "instructions") and the tool call arrived.
	t.Logf("model followed the purpose (reply starts with PINEAPPLE): %v", strings.HasPrefix(strings.ToUpper(strings.TrimSpace(res.Text)), "PINEAPPLE"))
}

// Connect once, use in several projects: membership and grants are per project, a project never reaches a server it was not put
// into, an agent is granted only inside its own project, and disconnecting removes the server everywhere.
func TestMCPLibraryIsSharedButProjectsAndGrantsAreSeparate(t *testing.T) {
	self, _ := os.Executable()
	a := openMCPApp(t, &claude.Adapter{Exe: os.Args[0]})
	p1, _ := a.setup("one")
	p2, _ := a.setup("two")
	var a1, a2 core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p1.ID, Name: "A1", Adapter: "claude", Role: "backend"}, &a1)
	a.call("agent.create", core.AgentSpec{ProjectID: p2.ID, Name: "A2", Adapter: "claude", Role: "backend"}, &a2)
	var lib core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "shared", Display: "Shared", Transport: "stdio", Argv: []string{self}, Env: map[string]string{testMCPEnv: "1"}}, &lib)

	list := func(p string) []core.MCPServerView {
		var out []core.MCPServerView
		a.call("mcpserver.list", map[string]string{"projectId": p}, &out)
		return out
	}
	if len(list(p1.ID)) != 0 || len(list(p2.ID)) != 0 {
		t.Fatal("a connected server must not be in any project until it is put there")
	}

	// dropping the server on an agent puts it into that agent's project only
	var v core.MCPServerView
	a.call("mcpserver.grant", map[string]any{"projectId": p1.ID, "serverId": "shared", "agentId": a1.ID, "on": true}, &v)
	if !v.Enabled || len(v.Agents) != 1 || v.Agents[0].AgentID != a1.ID {
		t.Fatalf("grant %+v", v)
	}
	if len(list(p1.ID)) != 1 || len(list(p2.ID)) != 0 {
		t.Fatalf("membership leaked across projects: %d / %d", len(list(p1.ID)), len(list(p2.ID)))
	}
	// an agent of another project cannot be granted through this project
	if err := a.callErr("mcpserver.grant", map[string]any{"projectId": p1.ID, "serverId": "shared", "agentId": a2.ID, "on": true}); err == nil {
		t.Fatal("an agent of project two was granted a server through project one")
	}

	// a second project puts the same connected server in, with its own grants
	a.call("mcpserver.add", map[string]string{"projectId": p2.ID, "serverId": "shared"}, &v)
	if len(v.Agents) != 0 {
		t.Fatalf("grants carried over to a new project: %+v", v.Agents)
	}
	a.call("mcpserver.grant", map[string]any{"projectId": p2.ID, "serverId": "shared", "agentId": a2.ID, "on": true}, &v)
	a.call("mcplib.list", nil, &[]core.MCPLibraryServer{})
	var libs []core.MCPLibraryServer
	a.call("mcplib.list", nil, &libs)
	if len(libs) != 1 || strings.Join(libs[0].Projects, ",") != p1.ID+","+p2.ID && strings.Join(libs[0].Projects, ",") != p2.ID+","+p1.ID {
		t.Fatalf("library %+v", libs)
	}

	// switching it off in one project and taking it out of the other
	a.call("mcpserver.setEnabled", map[string]any{"projectId": p1.ID, "serverId": "shared", "enabled": false}, &v)
	if v.Enabled {
		t.Fatal("still enabled")
	}
	a.call("mcpserver.remove", map[string]string{"projectId": p1.ID, "serverId": "shared"}, nil)
	if len(list(p1.ID)) != 0 || len(list(p2.ID)) != 1 || len(list(p2.ID)[0].Agents) != 1 {
		t.Fatalf("taking it out of project one touched project two: %+v", list(p2.ID))
	}
	a.call("mcplib.list", nil, &libs)
	if len(libs) != 1 {
		t.Fatal("taking a server out of a project must keep it in the library")
	}

	// disconnecting removes it everywhere
	a.call("mcplib.delete", map[string]string{"id": "shared"}, nil)
	if len(list(p2.ID)) != 0 {
		t.Fatal("a disconnected server is still in a project")
	}
	var n int
	if err := a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM agent_mcp_access`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("grants left behind: %d %v", n, err)
	}
}
