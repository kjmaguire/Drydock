package core_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/repomap"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// policyCatalog is a catalog of two test doubles served in this process: a documentation server (public docs, the kind a live
// pack names) and a server under the id gh, so the table's GitHub column governs it.
func policyCatalog(t *testing.T, docsCalls *atomic.Int32) *mcp.Catalog {
	t.Helper()
	docs := sdk.NewServer(&sdk.Implementation{Name: "docs-double", Version: "1"}, nil)
	type q struct {
		Query string `json:"query"`
	}
	sdk.AddTool(docs, &sdk.Tool{Name: "search", Description: "search the docs"}, func(_ context.Context, _ *sdk.CallToolRequest, in q) (*sdk.CallToolResult, any, error) {
		docsCalls.Add(1)
		body := fmt.Sprintf(`{"results":[{"url":"https://docs.example/kv","title":"KV limits","text":"A value is at most 25 MiB (asked: %s)."}]}`, in.Query)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: body}}}, nil, nil
	})
	gh := sdk.NewServer(&sdk.Implementation{Name: "gh-double", Version: "1"}, nil)
	sdk.AddTool(gh, &sdk.Tool{Name: "search_code", Description: "search code"}, func(context.Context, *sdk.CallToolRequest, q) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "no code"}}}, nil, nil
	})
	doc := fmt.Sprintf(`schema: 1
servers:
  - {id: docs, display: Docs double, publisher: Tests, brand: test, summary: docs, transport: http, url: %q, auth: {kind: none}, trust: public_docs, read_only_tools: [search], cache: {ttl: 24h, scope: global}, license: {server: MIT}}
  - {id: gh, display: GitHub double, publisher: Tests, brand: test, summary: gh, transport: http, url: %q, auth: {kind: none}, trust: private_data, read_only_tools: [search_code], license: {server: MIT}}
`, serveSDK(t, docs, nil), serveSDK(t, gh, nil))
	cat, err := mcp.ParseCatalog([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

// spawnTools starts a session of the agent the way every turn starts (Runtime.Spawn), connects a real MCP client to the entry
// it was given and returns the client and the tools the gateway lists.
func spawnTools(t *testing.T, a *app, ag core.Agent, projectID, taskID, cwd string) (*sdk.ClientSession, []string) {
	t.Helper()
	m, err := a.c.Runtime.Spawn(context.Background(), rt.Config{AgentID: ag.ID, ProjectID: projectID, TaskID: taskID, Adapter: "claude", Provider: "anthropic", Tier: "premium",
		Spec: agents.RunSpec{AgentID: ag.ID, Role: ag.Role, Cwd: cwd, Model: "fake-1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	raw, err := os.ReadFile(filepath.Join(a.dataDir(), "runs", m.RunID, "mcp.json"))
	if err != nil {
		t.Fatalf("%s got no gateway entry: %v", ag.Name, err)
	}
	url, tok := parseSingleEntry(t, raw)
	s, err := connectMCP(context.Background(), url, tok)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, toolNames(t, s)
}

// exactly reports whether got holds the names of want and nothing else, in any order.
func exactly(got []string, want ...string) bool {
	a, b := slices.Clone(got), slices.Clone(want)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func withRepoMap(tools ...string) []string {
	if repomap.Available() {
		return append(tools, "repo_map")
	}
	return tools
}

// The table of docs/mcp.md section 3 decides what each role reaches (decision P115): the Workspace tools of its row, the
// servers granted to the agent as far as the row's columns go, and the docs server of a live pack attached for it, through the
// gateway's pipeline (cache, untrusted marking, the send guard). A person changes a cell for the project, a CEL rule takes tools
// away, and a task narrows its agent's tools; none of it gives a tool the table, the grant or the pack does not.
func TestRoleTableDecidesEveryRunsToolsAndLivePackServersReachAgents(t *testing.T) {
	var docsCalls atomic.Int32
	a := openGalleryApp(t, policyCatalog(t, &docsCalls))
	p, repo := a.setup("policy")
	mk := func(name, role string) core.Agent {
		var ag core.Agent
		a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: name, Adapter: "claude", Role: role, Model: "fake-1"}, &ag)
		return ag
	}
	pm, backend, devops, reviewer := mk("Planner", "pm"), mk("Backend", "backend"), mk("Ops", "devops"), mk("Reviewer", "reviewer")

	// gh, connected from the catalog, put into the project and granted to three of them
	var res core.MCPConnectResult
	a.call("mcplib.connect", map[string]any{"catalogId": "gh"}, &res)
	a.call("mcpserver.add", map[string]string{"projectId": p.ID, "serverId": "gh"}, nil)
	a.call("mcpserver.access.set", map[string]any{"projectId": p.ID, "serverId": "gh", "agents": []core.MCPAccess{{AgentID: pm.ID}, {AgentID: backend.ID}, {AgentID: devops.ID}}}, nil)
	// a live pack on the docs double, attached for the whole project
	a.call("pack.install", map[string]string{"yaml": "schema: 1\nid: kvdocs\ntitle: KV docs\nkind: live\nvisibility: public\nlicense: {content: CC-BY-4.0}\nlive: {mcp: docs, tool: search}\n"}, nil)
	a.call("pack.attach", map[string]string{"projectId": p.ID, "packId": "kvdocs"}, nil)

	_, got := spawnTools(t, a, pm, p.ID, "", repo)
	// PM: requirements, memory, packs; trackers but no docs servers or GitHub; no file reads or skills
	if want := withRepoMap("fetch_range", "propose_memory", "search_memory", "search_pack", "search_requirements"); !sameSet(got, want...) {
		t.Fatalf("pm tools: %v, want %v", got, want)
	}
	bs, got := spawnTools(t, a, backend, p.ID, "", repo)
	if want := withRepoMap("fetch_range", "propose_memory", "read_range", "search_memory", "search_pack", "search_requirements", "skills", "docs__search"); !sameSet(got, want...) {
		t.Fatalf("backend tools (docs server yes, GitHub no): %v, want %v", got, want)
	}
	_, got = spawnTools(t, a, devops, p.ID, "", repo)
	if !slices.Contains(got, "gh__search_code") || !slices.Contains(got, "docs__search") {
		t.Fatalf("devops reaches GitHub and the docs server: %v", got)
	}
	_, got = spawnTools(t, a, reviewer, p.ID, "", repo)
	if slices.Contains(got, "docs__search") || slices.Contains(got, "gh__search_code") {
		t.Fatalf("a reviewer has no docs servers, and gh was not granted to it: %v", got)
	}

	// the live pack's server answers the agent through the gateway: marked untrusted, cached the second time
	out, isErr := callTool(t, bs, "docs__search", map[string]any{"query": "kv value size"})
	if isErr || !strings.Contains(out, "25 MiB") || !strings.Contains(out, "<untrusted") {
		t.Fatalf("docs__search: err=%v %s", isErr, out)
	}
	before := docsCalls.Load()
	if out, isErr := callTool(t, bs, "docs__search", map[string]any{"query": "kv value size"}); isErr || !strings.Contains(out, "25 MiB") {
		t.Fatalf("again: %v %s", isErr, out)
	}
	if docsCalls.Load() != before {
		t.Errorf("the same question to a public docs server is served from the cache (calls %d -> %d)", before, docsCalls.Load())
	}
	if out, isErr := callTool(t, bs, "docs__search", map[string]any{"query": "```go\nfunc main() {}\n```"}); !isErr {
		t.Errorf("the send guard keeps code from a docs server: %s", out)
	}
	// only the pack's tool of that server, and the GitHub double stays closed to the backend
	if out, isErr := callTool(t, bs, "gh__search_code", map[string]any{"query": "x"}); !isErr || !strings.Contains(out, "denied") {
		t.Errorf("backend gh__search_code must be refused: %s", out)
	}

	// the project's table: a person opens GitHub to the backend and file reads to the PM, then puts the PM's cell back
	var view core.RolePolicyView
	a.call("mcp.rolePolicy.get", map[string]string{"projectId": p.ID}, &view)
	if len(view.Rows) != len(mcp.PolicyRoles) || len(view.Columns) != len(mcp.RoleColumns) || view.Rows[0].Cells[mcp.ColReadRange].Allowed {
		t.Fatalf("view %+v", view.Rows[0])
	}
	on, off := true, false
	a.call("mcp.rolePolicy.set", map[string]any{"projectId": p.ID, "role": "backend", "column": mcp.ColGitHub, "allowed": on}, &view)
	a.call("mcp.rolePolicy.set", map[string]any{"projectId": p.ID, "role": "pm", "column": mcp.ColReadRange, "allowed": on}, &view)
	a.call("mcp.rolePolicy.set", map[string]any{"projectId": p.ID, "role": "devops", "column": mcp.ColDocs, "allowed": off}, &view)
	if c := view.Rows[0].Cells[mcp.ColReadRange]; !c.Allowed || c.Default || !c.Changed {
		t.Fatalf("pm read_range cell %+v", c)
	}
	_, got = spawnTools(t, a, backend, p.ID, "", repo)
	if !slices.Contains(got, "gh__search_code") {
		t.Fatalf("backend with GitHub opened: %v", got)
	}
	_, got = spawnTools(t, a, pm, p.ID, "", repo)
	if !slices.Contains(got, "read_range") || slices.Contains(got, "gh__search_code") {
		t.Fatalf("pm with read_range opened (GitHub still closed): %v", got)
	}
	_, got = spawnTools(t, a, devops, p.ID, "", repo)
	if slices.Contains(got, "docs__search") {
		t.Fatalf("devops with docs servers closed: %v", got)
	}
	a.call("mcp.rolePolicy.set", map[string]any{"projectId": p.ID, "role": "pm", "column": mcp.ColReadRange, "allowed": nil}, &view)
	if c := view.Rows[0].Cells[mcp.ColReadRange]; c.Allowed || c.Changed {
		t.Fatalf("reset cell %+v", c)
	}
	for _, bad := range []map[string]any{{"projectId": p.ID, "role": "boss", "column": mcp.ColDocs, "allowed": on}, {"projectId": p.ID, "role": "pm", "column": "everything", "allowed": on}, {"projectId": "prj_none", "role": "pm", "column": mcp.ColDocs, "allowed": on}} {
		if err := a.callErr("mcp.rolePolicy.set", bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}

	// a CEL rule takes the docs server from the backend; one that does not compile is refused with the reason
	if err := a.callErr("mcp.policyRule.add", map[string]any{"projectId": p.ID, "name": "broken", "expr": `column ==`}); err == nil {
		t.Fatal("a rule that does not compile was stored")
	}
	if err := a.callErr("mcp.policyRule.add", map[string]any{"projectId": p.ID, "name": "not boolean", "expr": `role`}); err == nil || !strings.Contains(err.Error(), "true or false") {
		t.Fatalf("a rule that is not boolean: %v", err)
	}
	a.call("mcp.policyRule.add", map[string]any{"projectId": p.ID, "name": "no docs for the backend", "expr": `role == "backend" && column == "docs"`}, &view)
	if len(view.Rules) != 1 || view.Rules[0].Name != "no docs for the backend" {
		t.Fatalf("rules %+v", view.Rules)
	}
	bs, got = spawnTools(t, a, backend, p.ID, "", repo)
	if slices.Contains(got, "docs__search") || !slices.Contains(got, "read_range") {
		t.Fatalf("the rule takes the docs server only: %v", got)
	}
	if out, isErr := callTool(t, bs, "docs__search", map[string]any{"query": "kv"}); !isErr || !strings.Contains(out, "denied") {
		t.Errorf("a call to a tool a rule took away is refused: %s", out)
	}
	a.call("mcp.policyRule.remove", map[string]any{"projectId": p.ID, "id": view.Rules[0].ID}, &view)
	if len(view.Rules) != 0 {
		t.Fatalf("rules after remove %+v", view.Rules)
	}

	// a task narrows its agent's tools; a name the agent does not have stays out (narrowing never widens)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Read only", Description: "Look, do not touch."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": backend.ID}, &task)
	var tv core.TaskToolsView
	a.call("task.mcpTools.set", map[string]any{"taskId": task.ID, "tools": []string{"read_range", "docs__*", "task"}}, &tv)
	if strings.Join(tv.Narrowed, ",") != "docs__*,read_range,task" || tv.AgentID != backend.ID {
		t.Fatalf("narrowed view %+v", tv)
	}
	if !exactly(tv.AgentTools, "docs__search", "fetch_range", "read_range") {
		t.Fatalf("what the agent gets for the task: %v", tv.AgentTools)
	}
	choice := map[string]bool{}
	for _, c := range tv.Choices {
		choice[c.Name] = true
	}
	if !choice["read_range"] || !choice["gh__*"] || !choice["docs__*"] || !choice["propose_memory"] {
		t.Fatalf("choices %+v", tv.Choices)
	}
	ts, got := spawnTools(t, a, backend, p.ID, task.ID, repo)
	if !exactly(got, "docs__search", "fetch_range", "read_range") {
		t.Fatalf("the task's turn: %v (task is not given: act tools are off)", got)
	}
	if out, isErr := callTool(t, ts, "search_memory", map[string]any{"query": "x"}); !isErr || !strings.Contains(out, "denied") {
		t.Errorf("a tool outside the narrowing is refused: %s", out)
	}
	_, got = spawnTools(t, a, backend, p.ID, "", repo)
	if !slices.Contains(got, "search_memory") {
		t.Fatalf("a run without the task is not narrowed: %v", got)
	}
	if err := a.callErr("task.mcpTools.set", map[string]any{"taskId": task.ID, "tools": []string{"read range; rm"}}); err == nil {
		t.Error("a name that is not a tool name was accepted")
	}
	a.call("task.mcpTools.set", map[string]any{"taskId": task.ID, "tools": nil}, &tv)
	if tv.Narrowed != nil || !slices.Contains(tv.AgentTools, "search_memory") {
		t.Fatalf("narrowing removed: %+v", tv)
	}

	// every change is a person's, in the audit log
	for _, action := range []string{"mcp.role_policy", "mcp.policy_rule_added", "mcp.policy_rule_removed", "task.mcp_tools"} {
		var n int
		a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action=? AND actor='human:kyle'`, action).Scan(&n)
		if n == 0 {
			t.Errorf("no audit entry %s", action)
		}
	}
}
