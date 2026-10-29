package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/proc"
)

// Decision P60: a chat's properties. The skills chosen from the workspace library and the library MCP servers granted to the
// chat really reach the chat's Claude Code session (the scripted real-process agent, a TEST DOUBLE that speaks Claude Code's
// stream-json and calls MCP tools through the --mcp-config entry the way Claude Code does), and nothing that was not chosen
// does. The MCP server is the real stdio test server (internal/mcp/mcptestserver), re-executed from this test binary.

// specProbe wraps the scripted Claude adapter: it keeps every spec a session starts with and, when the session has a gateway
// entry, the tools the gateway lists for it.
type specProbe struct {
	agents.Adapter
	mu    sync.Mutex
	specs []agents.RunSpec
	tools [][]string
}

func (p *specProbe) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	var tools []string
	if spec.MCPConfigPath != "" {
		raw, _ := os.ReadFile(spec.MCPConfigPath)
		var f mcpConfigFile
		_ = json.Unmarshal(raw, &f)
		ent := f.MCPServers["drydock"]
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		playAgentIn(pm)
		if s, err := connectMCP(cctx, ent.Env["DRYDOCK_MCP_URL"], ent.Env["DRYDOCK_MCP_TOKEN"]); err == nil {
			if lt, err := s.ListTools(cctx, nil); err == nil {
				for _, tl := range lt.Tools {
					tools = append(tools, tl.Name)
				}
			}
			_ = s.Close()
		}
		cancel()
	}
	p.mu.Lock()
	p.specs = append(p.specs, spec)
	p.tools = append(p.tools, tools)
	p.mu.Unlock()
	return p.Adapter.Start(ctx, pm, spec)
}

func (p *specProbe) last() (agents.RunSpec, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.specs[len(p.specs)-1], p.tools[len(p.tools)-1]
}

// chatPropsScript: what the scripted agent does for each message of these tests.
var chatPropsScript = fakeagent.Script{
	Rules: []fakeagent.Rule{
		{Match: "read the wrangler skill", Steps: []fakeagent.Step{{MCP: "mcp__drydock__skills", Input: json.RawMessage(`{"action":"get","name":"wrangler"}`)}}},
		{Match: "read the durable objects skill", Steps: []fakeagent.Step{{MCP: "mcp__drydock__skills", Input: json.RawMessage(`{"action":"get","name":"durable-objects"}`)}}},
		{Match: "echo through the server", Steps: []fakeagent.Step{{MCP: "mcp__drydock__tst__echo", Input: json.RawMessage(`{"text":"hello from the chat"}`)}}},
	},
	Default: []fakeagent.Step{{Say: "ok"}},
}

// libraryWithSkills fills the workspace skill library from the registry fixture (a PROTOCOL FIXTURE standing in for GitHub)
// and connects the stdio test server to the MCP library, declaring echo read-only.
func libraryWithSkills(t *testing.T, a *app) {
	t.Helper()
	f := newWatchFixture(t)
	f.publish("cloudflare/skills", watchSha1, map[string]string{
		"skills/wrangler/SKILL.md":        fixtureSkill("wrangler", "", "WRANGLER-RULE: use the project's Wrangler version."),
		"skills/durable-objects/SKILL.md": fixtureSkill("durable-objects", "", "DO-RULE: one object per room."),
	})
	f.use(a)
	if _, err := a.c.RunSkillWatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	a.call("mcplib.save", core.MCPServerInput{ID: "tst", Display: "Test server", Transport: "stdio", Argv: []string{self},
		Env: map[string]string{testMCPEnv: "1"}, ReadOnlyTools: []string{"echo"}}, nil)
}

func lastAssistant(ms []chats.Message) string {
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Role == chats.RoleAssistant {
			return ms[i].Body
		}
	}
	return ""
}

func TestAChatsChosenSkillsAndMCPServersReachItsClaudeSessionAndNothingElseDoes(t *testing.T) {
	probe := &specProbe{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	e := openChats(t, chatPropsScript, map[string]agents.Adapter{"claude": probe})
	libraryWithSkills(t, e.app)

	// a chat without a project, in its private folder, with one library skill and the test server
	c := e.create(map[string]any{
		"skills":      []map[string]string{{"registryId": "cloudflare-skills", "name": "wrangler"}},
		"mcpServers":  []map[string]any{{"serverId": "tst"}},
		"readHistory": false,
	})
	if c.ProjectID != "" || !strings.HasPrefix(c.Cwd, filepath.Join(e.dataDir(), "chats")) {
		t.Fatalf("a chat without a project runs in its private folder: %+v", c)
	}
	if len(c.Skills) != 1 || c.Skills[0] != (chats.SkillRef{RegistryID: "cloudflare-skills", Name: "wrangler"}) {
		t.Fatalf("skills %+v", c.Skills)
	}
	// granted by name, as an agent's grant: the poisoned tool is left out
	if len(c.MCPServers) != 1 || c.MCPServers[0].ServerID != "tst" || strings.Join(c.MCPServers[0].Tools, ",") != "echo,envlen,post_note" {
		t.Fatalf("mcp servers %+v", c.MCPServers)
	}
	if got := e.get(c.ID).Chat; len(got.Skills) != 1 || len(got.MCPServers) != 1 {
		t.Fatalf("the properties are stored with the chat: %+v", got)
	}

	// the skill: listed in the session's instructions, its text read through the skills tool
	e.send(c.ID, "please read the wrangler skill")
	spec, tools := probe.last()
	for _, want := range []string{"Skills chosen for this chat", "**wrangler**", "Stand-in skill wrangler for the test.", "mcp__drydock__skills"} {
		if !strings.Contains(spec.SystemAppend, want) {
			t.Fatalf("the instructions must carry %q:\n%s", want, spec.SystemAppend)
		}
	}
	if strings.Contains(spec.SystemAppend, "WRANGLER-RULE") || strings.Contains(spec.SystemAppend, "durable-objects") {
		t.Fatalf("only the index is in every request, and only the chosen skill:\n%s", spec.SystemAppend)
	}
	if strings.Join(tools, ",") != "fetch_range,skills,tst__echo,tst__envlen,tst__post_note" {
		t.Fatalf("the session gets the skills tool and the granted server's tools, nothing else: %v", tools)
	}
	argv := e.argv()[len(e.argv())-1]
	if _, ok := flagOf(argv, "--mcp-config"); !ok {
		t.Fatalf("no gateway entry: %v", argv)
	}
	// the gateway's tools are callable without a prompt (the entry's own rule, mcp__drydock__*): what the gateway lists is
	// what the session can reach
	if allowed, _ := flagOf(argv, "--allowedTools"); !strings.Contains(allowed, "mcp__drydock__") {
		t.Fatalf("claude may call the gateway's tools without a prompt: %q", allowed)
	}
	// the quiet environment stays (decision P36)
	if !slices.Contains(argv, "--disable-slash-commands") {
		t.Fatalf("the chat's session is still quiet: %v", argv)
	}
	d := e.get(c.ID)
	if got := lastAssistant(d.Messages); !strings.Contains(got, "mcp__drydock__skills answered") || !strings.Contains(got, "WRANGLER-RULE") {
		t.Fatalf("the skill's text reached the session: %q", got)
	}

	// a skill that was not chosen is not served
	e.send(c.ID, "now read the durable objects skill")
	if got := lastAssistant(e.get(c.ID).Messages); !strings.Contains(got, "failed") || strings.Contains(got, "DO-RULE") {
		t.Fatalf("a skill not chosen must not reach the session: %q", got)
	}

	// the granted server: its read-only tool answers through the gateway
	e.send(c.ID, "echo through the server")
	// through the gateway's usual pipeline: the result is marked untrusted data from the server
	if got := lastAssistant(e.get(c.ID).Messages); !strings.Contains(got, "mcp__drydock__tst__echo answered") || !strings.Contains(got, "echo: hello from the chat") ||
		!strings.Contains(got, `<untrusted source="tst"`) {
		t.Fatalf("the server's tool reached the session: %q", got)
	}
	// the next turns continued the same session
	if v, ok := flagOf(e.argv()[len(e.argv())-1], "--resume"); !ok || v == "" {
		t.Fatal("the turns continue the CLI's session")
	}

	// taken away: from the next message the session has neither, and the change is in the chat's history
	var u chats.Chat
	e.call("chat.update", map[string]any{"id": c.ID, "skills": []any{}, "mcpServers": []any{}}, &u)
	if len(u.Skills) != 0 || len(u.MCPServers) != 0 {
		t.Fatalf("update %+v", u)
	}
	e.send(c.ID, "echo through the server")
	spec, _ = probe.last()
	if spec.MCPConfigPath != "" || strings.Contains(spec.SystemAppend, "Skills chosen") {
		t.Fatalf("nothing chosen, nothing given: config %q, instructions %q", spec.MCPConfigPath, spec.SystemAppend)
	}
	if got := lastAssistant(e.get(c.ID).Messages); !strings.Contains(got, "failed") || strings.Contains(got, "echo: hello") {
		t.Fatalf("the server is not reachable once taken away: %q", got)
	}
	if v, ok := flagOf(e.argv()[len(e.argv())-1], "--resume"); !ok || v == "" {
		t.Fatal("a change of skills or servers keeps the session")
	}
	var notes []string
	for _, m := range e.get(c.ID).Messages {
		if m.Role == chats.RoleSystem {
			notes = append(notes, m.Body)
		}
	}
	if !slices.Contains(notes, "No skills for this chat from the next message.") || !slices.Contains(notes, "No MCP servers for this chat from the next message.") {
		t.Fatalf("notes %q", notes)
	}
}

// A chat that only reads history gets the chats tool and no server, even when the library has one and another chat was
// granted it.
func TestAChatIsNotGrantedServersOrSkillsItWasNotGiven(t *testing.T) {
	probe := &specProbe{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	e := openChats(t, chatPropsScript, map[string]agents.Adapter{"claude": probe})
	libraryWithSkills(t, e.app)
	e.create(map[string]any{"mcpServers": []map[string]any{{"serverId": "tst"}}, "skills": []map[string]string{{"registryId": "cloudflare-skills", "name": "wrangler"}}})

	plain := e.create(map[string]any{})
	e.send(plain.ID, "echo through the server")
	spec, tools := probe.last()
	if strings.Join(tools, ",") != "chats,fetch_range" || strings.Contains(spec.SystemAppend, "Skills chosen") {
		t.Fatalf("a chat given nothing gets only the chats tool: %v / %q", tools, spec.SystemAppend)
	}
	if got := lastAssistant(e.get(plain.ID).Messages); !strings.Contains(got, "failed") {
		t.Fatalf("an MCP tool not granted is refused: %q", got)
	}
	e.send(plain.ID, "please read the wrangler skill")
	if got := lastAssistant(e.get(plain.ID).Messages); !strings.Contains(got, "failed") || strings.Contains(got, "WRANGLER-RULE") {
		t.Fatalf("a skill not chosen is not read: %q", got)
	}
}

func TestChatPropertiesAreCheckedAndAMoveStartsANewSessionWithTheConversation(t *testing.T) {
	sc, _ := json.Marshal(fakeacp.Script{Default: []fakeacp.Step{{Say: "ok"}}})
	sp := filepath.Join(t.TempDir(), "acp.json")
	os.WriteFile(sp, sc, 0o644)
	e := openChats(t, chatPropsScript, map[string]agents.Adapter{
		// a scripted ACP agent (a TEST DOUBLE) with no known per-run MCP mechanism: it can never be given the gateway
		"acpchat": acp.New(acp.Config{ID: "acpchat", Command: os.Args[0], Env: []string{"DRYDOCK_FAKE_ACP=1", "DRYDOCK_FAKE_ACP_SCRIPT=" + sp}}),
	})
	libraryWithSkills(t, e.app)
	p, _ := e.setup("props")
	for name, bad := range map[string]map[string]any{
		"an unknown skill":                     {"skills": []map[string]string{{"registryId": "cloudflare-skills", "name": "nope"}}},
		"a skill without registry":             {"skills": []map[string]string{{"name": "wrangler"}}},
		"an unknown server":                    {"mcpServers": []map[string]any{{"serverId": "nope"}}},
		"a bad tool name":                      {"mcpServers": []map[string]any{{"serverId": "tst", "tools": []string{"a b"}}}},
		"skills for a CLI without the gateway": {"adapter": "acpchat", "skills": []map[string]string{{"registryId": "cloudflare-skills", "name": "wrangler"}}},
		"a folder for a project chat":          {"projectId": p.ID, "cwd": t.TempDir()},
	} {
		if err := e.callErr("chat.create", bad); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// a named tool list is kept as given (the person narrowed the grant)
	c := e.create(map[string]any{"mcpServers": []map[string]any{{"serverId": "tst", "tools": []string{"echo"}}}})
	if strings.Join(c.MCPServers[0].Tools, ",") != "echo" {
		t.Fatalf("%+v", c.MCPServers)
	}
	e.send(c.ID, "hello")
	if v, ok := flagOf(e.argv()[len(e.argv())-1], "--resume"); ok && v != "" {
		t.Fatal("the first turn starts a session")
	}
	e.send(c.ID, "hello again")
	if v, ok := flagOf(e.argv()[len(e.argv())-1], "--resume"); !ok || v == "" {
		t.Fatal("the second turn continues it")
	}

	// moving the chat to another folder: the session belongs to the old folder, so the next turn starts a new one with the
	// conversation so far
	dir := t.TempDir()
	var moved chats.Chat
	e.call("chat.update", map[string]any{"id": c.ID, "cwd": dir}, &moved)
	if moved.Cwd != filepath.Clean(dir) || moved.SessionID != "" {
		t.Fatalf("moved %+v", moved)
	}
	ev := e.send(c.ID, "and now?")
	if ev["fallback"] != true || ev["resumed"] == true {
		t.Fatalf("the next turn is a new session given the transcript: %v", ev)
	}
	if v, ok := flagOf(e.argv()[len(e.argv())-1], "--resume"); ok && v != "" {
		t.Fatal("no resume after a move")
	}
	if ps := e.prompts(); !strings.Contains(ps[len(ps)-1], "hello again") {
		t.Fatalf("the new session is given the conversation: %q", ps[len(ps)-1])
	}
	if b, err := os.Stat(dir); err != nil || !b.IsDir() {
		t.Fatal(err)
	}
	// back to its private folder
	e.call("chat.update", map[string]any{"id": c.ID, "cwd": ""}, &moved)
	if !strings.HasPrefix(moved.Cwd, filepath.Join(e.dataDir(), "chats")) {
		t.Fatalf("private folder %q", moved.Cwd)
	}
	// a project chat takes no folder, and a folder that does not exist is refused
	pc := e.create(map[string]any{"projectId": p.ID})
	if err := e.callErr("chat.update", map[string]any{"id": pc.ID, "cwd": t.TempDir()}); err == nil {
		t.Fatal("a project chat runs in the project's folder")
	}
	if err := e.callErr("chat.update", map[string]any{"id": c.ID, "cwd": filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("a missing folder is refused")
	}
	// a fork keeps the properties
	var f chats.Chat
	e.call("chat.fork", map[string]any{"id": c.ID}, &f)
	if len(f.MCPServers) != 1 || f.MCPServers[0].ServerID != "tst" {
		t.Fatalf("fork %+v", f)
	}
	// another agent CLI without the gateway cannot take the chat's server
	if err := e.callErr("chat.update", map[string]any{"id": c.ID, "adapter": "acpchat"}); err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("moving a chat with servers to a CLI without the gateway: %v", err)
	}
}
