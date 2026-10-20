package manifests_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

func embeddedSet(t *testing.T) *manifests.Set {
	t.Helper()
	s, err := manifests.Embedded()
	if err != nil {
		t.Fatalf("embedded manifests: %v", err)
	}
	return s
}

func get(t *testing.T, id string) *manifests.Manifest {
	t.Helper()
	m, ok := embeddedSet(t).Get(id)
	if !ok {
		t.Fatalf("no manifest %q", id)
	}
	return m
}

func TestEmbeddedManifestsLoadAndValidate(t *testing.T) {
	s := embeddedSet(t)
	want := []string{"antigravity", "claude", "cline", "codex", "custom", "gemini", "goose", "opencode"}
	if !reflect.DeepEqual(s.IDs(), want) {
		t.Fatalf("ids %v, want %v", s.IDs(), want)
	}
	for _, m := range s.All() {
		if err := m.Validate(); err != nil {
			t.Errorf("%s: %v", m.Meta.ID, err)
		}
		if m.InstallHint() == "" && !m.Meta.Custom {
			t.Errorf("%s: no install guidance", m.Meta.ID)
		}
	}
}

// The commands, flags and pins below are the ones recorded in docs/phase0/agent-clis.md; a change there must change both.
func TestManifestFactsMatchTheEvidence(t *testing.T) {
	type acpWant struct {
		argv []string
	}
	for id, w := range map[string]acpWant{
		"codex":    {[]string{"npx", "-y", "@agentclientprotocol/codex-acp@2.2.2"}},
		"gemini":   {[]string{"gemini", "--acp"}},
		"opencode": {[]string{"opencode", "acp"}},
		"goose":    {[]string{"goose", "acp"}},
	} {
		m := get(t, id)
		if m.Transports.Primary != manifests.TransportACP || !m.ACPEnabled() {
			t.Errorf("%s: primary %q enabled=%v", id, m.Transports.Primary, m.ACPEnabled())
			continue
		}
		if !reflect.DeepEqual(m.Transports.ACP.Argv, w.argv) {
			t.Errorf("%s: acp argv %v, want %v", id, m.Transports.ACP.Argv, w.argv)
		}
	}
	if m := get(t, "opencode"); m.Transports.Headless != nil {
		t.Error("opencode has no headless transport (decision D11)")
	}
	cl := get(t, "claude")
	if cl.Transports.Primary != manifests.TransportHeadless || cl.Transports.Headless.Parser != manifests.ParserClaude {
		t.Errorf("claude must be headless (D1): %+v", cl.Transports)
	}
	if cl.ACPEnabled() {
		t.Error("the Claude ACP bridge must be disabled (D1)")
	}
	if a := cl.Transports.ACP; a == nil || !reflect.DeepEqual(a.Argv, []string{"npx", "-y", "@agentclientprotocol/claude-agent-acp@0.94.0"}) || a.EnableWhen != "written_confirmation_recorded" {
		t.Errorf("claude acp block: %+v", a)
	}
	if !reflect.DeepEqual(cl.Auth.ForbiddenFlags, []string{"--bare"}) || !reflect.DeepEqual(cl.Auth.WarnEnv, []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}) {
		t.Errorf("claude auth: %+v", cl.Auth)
	}
	if p := cl.Auth.Probe; p == nil || !reflect.DeepEqual(p.Argv, []string{"auth", "status", "--json"}) || p.Parse != "json" {
		t.Errorf("claude probe %+v", p)
	}
	if cl.Versions.Min != "2.1.205" || cl.Versions.Recommended != "2.1.305" {
		t.Errorf("claude versions %+v", cl.Versions)
	}
	cx := get(t, "codex")
	if p := cx.Auth.Probe; p == nil || !reflect.DeepEqual(p.Argv, []string{"login", "status"}) || p.Parse != "exit_code" || p.ReadyWhen != "exit == 0" {
		t.Errorf("codex probe %+v", p)
	}
	if cx.Versions.Min != "0.159.1" || len(cx.Transports.ACP.Requires) != 1 || cx.Transports.ACP.Requires[0].Tool != "node" || cx.Transports.ACP.Requires[0].Min != "22" {
		t.Errorf("codex: %+v %+v", cx.Versions, cx.Transports.ACP.Requires)
	}
	for _, id := range []string{"gemini", "opencode", "goose"} {
		if v := get(t, id).Versions; v.Min != map[string]string{"gemini": "0.63.0", "opencode": "1.18.35", "goose": "1.53.0"}[id] {
			t.Errorf("%s min %s", id, v.Min)
		}
	}
	// no auth probe for gemini and goose (nothing exists), and the manifest says so
	for _, id := range []string{"gemini", "goose"} {
		if get(t, id).Auth.Probe != nil {
			t.Errorf("%s: probe: none expected", id)
		}
	}
	g := get(t, "gemini")
	if g.Terms == nil || !strings.Contains(g.Terms.Warning, "API key") || !strings.Contains(g.Terms.Warning, "2026-06-18") {
		t.Errorf("gemini D2 warning: %+v", g.Terms)
	}
	if !reflect.DeepEqual(g.Auth.BlockedKinds, []string{"oauth-personal"}) || g.Transports.ACP.Env["GEMINI_CLI_TRUST_WORKSPACE"] != "true" {
		t.Errorf("gemini auth/env: %+v %+v", g.Auth, g.Transports.ACP.Env)
	}
	if len(g.Requires) != 1 || g.Requires[0].Tool != "node" {
		t.Errorf("gemini requires node: %+v", g.Requires)
	}
	if gm := get(t, "goose").Transports.ACP.Env["GOOSE_MODE"]; gm != "approve" {
		t.Errorf("goose mode %q", gm)
	}
	ag := get(t, "antigravity")
	if ag.Gate == nil || !ag.Gate.Required || ag.Gate.ID != "antigravity-third-party-v1" || !strings.Contains(ag.Gate.Text, "no Google statement") {
		t.Errorf("antigravity gate: %+v", ag.Gate)
	}
	if !ag.Transports.Headless.Unverified {
		t.Error("the antigravity transport must be marked unverified")
	}
	if ag.Transports.ACP == nil || ag.ACPEnabled() {
		t.Error("antigravity acp is a later evaluation and must stay disabled")
	}
	cu := get(t, "custom")
	if !cu.Meta.Custom || cu.Transports.Headless.Dialect == nil || cu.Gate == nil || !cu.Gate.Required || cu.Transports.Headless.Parser != "" {
		t.Errorf("custom: %+v", cu)
	}
	// tiers
	for id, tier := range map[string]string{"claude": "premium", "codex": "premium", "gemini": "premium", "opencode": "cheap", "goose": "cheap", "antigravity": "premium"} {
		if got := get(t, id).Meta.Tier; got != tier {
			t.Errorf("%s tier %q, want %q", id, got, tier)
		}
	}
}

func TestEveryBridgePackageIsPinnedExactly(t *testing.T) {
	for _, m := range embeddedSet(t).All() {
		a := m.Transports.ACP
		if a == nil || a.Argv[0] != "npx" {
			continue
		}
		pkg := a.Argv[len(a.Argv)-1]
		if !strings.HasPrefix(pkg, "@agentclientprotocol/") || strings.Contains(pkg, "latest") || strings.Contains(pkg, "^") {
			t.Errorf("%s: bridge %q", m.Meta.ID, pkg)
		}
	}
}

func TestDeclaredCapabilityMapping(t *testing.T) {
	c := manifests.DeclaredCapabilities(get(t, "claude"))
	if !c.Streaming || !c.Interrupt || !c.CostReporting || c.ImageInput || !c.ToolCallVisibility || !c.Headless {
		t.Errorf("claude declared: %+v", c) // cost_reporting: estimate counts as reported, image_input: unknown is false
	}
	g := manifests.DeclaredCapabilities(get(t, "gemini"))
	if g.EffortControl || g.CostReporting {
		t.Errorf("gemini has no effort flag and reports no dollars: %+v", g)
	}
	if a := manifests.DeclaredCapabilities(get(t, "antigravity")); a.ModelSelection || a.SessionResume || a.ToolCallVisibility {
		t.Errorf("unknown must not read as true: %+v", a)
	}
}

// HeadlessArgs must reproduce what the Claude adapter really passes, so the manifest cannot drift from the code.
func TestClaudeHeadlessArgsMatchTheAdapter(t *testing.T) {
	m := get(t, "claude")
	ad := claude.New()
	cases := []struct {
		spec agents.RunSpec
		vars map[string]string
	}{
		{agents.RunSpec{}, map[string]string{}},
		{agents.RunSpec{Model: "haiku", Effort: "low"}, map[string]string{"model": "haiku", "effort": "low"}},
		{agents.RunSpec{Model: "sonnet", Effort: "high", PermissionMode: "plan", MCPConfigPath: "/run/mcp.json", ResumeID: "abc", SystemAppend: "be brief", AllowedTools: []string{"Read", "Grep"}},
			map[string]string{"model": "sonnet", "effort": "high", "mode": "plan", "mcp.config_path": "/run/mcp.json", "session_id": "abc", "system_append": "be brief", "allowed_tools": "Read,Grep"}},
		{agents.RunSpec{ResumeID: "r1"}, map[string]string{"session_id": "r1"}},
	}
	for i, c := range cases {
		if got, want := m.HeadlessArgs(c.vars), ad.Args(c.spec); !reflect.DeepEqual(got, want) {
			t.Errorf("case %d:\nmanifest %v\nadapter  %v", i, got, want)
		}
	}
	for _, a := range m.HeadlessArgs(cases[2].vars) {
		if a == "--bare" {
			t.Fatal("--bare is forbidden")
		}
	}
}

func TestHeadlessArgsSkipIncompleteGroupsAndKeepOrder(t *testing.T) {
	m := get(t, "codex")
	got := m.HeadlessArgs(map[string]string{"cwd": "/w", "effort": "high"})
	want := []string{"exec", "--json", "-C", "/w", "-s", "workspace-write", "-", "-c", `model_reasoning_effort="high"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	// a value with shell metacharacters stays one argv element
	got = m.HeadlessArgs(map[string]string{"cwd": "/w; rm -rf /", "model": "a b"})
	if got[3] != "/w; rm -rf /" || got[len(got)-1] != "a b" {
		t.Fatalf("substitution must be per element, got %v", got)
	}
}

func TestCondition(t *testing.T) {
	c, err := manifests.ParseCondition("$.loggedIn == true")
	if err != nil {
		t.Fatal(err)
	}
	for doc, want := range map[string][2]bool{`{"loggedIn": true}`: {true, true}, `{"loggedIn": false}`: {false, true}, `{"x": 1}`: {false, false}, `not json`: {false, false}, `{"loggedIn":"true"}`: {true, true}} {
		if got, ok := c.EvalJSON([]byte(doc)); got != want[0] || ok != want[1] {
			t.Errorf("%s: got %v,%v want %v", doc, got, ok, want)
		}
	}
	n, _ := manifests.ParseCondition(`$.a.b != "x"`)
	if got, ok := n.EvalJSON([]byte(`{"a":{"b":"y"}}`)); !got || !ok {
		t.Error("nested not-equal")
	}
	e, err := manifests.ParseCondition("exit == 0")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.EvalExit(0); !ok {
		t.Error("exit 0")
	}
	if ok, _ := e.EvalExit(1); ok {
		t.Error("exit 1")
	}
	if _, err := e.EvalExit(0); err != nil {
		t.Error(err)
	}
	if _, ok := e.EvalJSON([]byte(`{}`)); ok {
		t.Error("exit condition on json")
	}
	for _, bad := range []string{"", "loggedIn == true", "$.x ~= 1", "exit == true", "$.x == y", "$ == 1", "exit == 0; rm"} {
		if _, err := manifests.ParseCondition(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestVersionParsingAndOrder(t *testing.T) {
	for in, want := range map[string]manifests.Version{"2.1.294 (Claude Code)": {2, 1, 294}, "codex-cli 0.161.0": {0, 161, 0}, " 1.53.0": {1, 53, 0}, "v22.22.0": {22, 22, 0}, "22": {22, 0, 0}, "1.2": {1, 2, 0}} {
		if v, ok := manifests.ParseVersion(in); !ok || v != want {
			t.Errorf("%q: %v %v", in, v, ok)
		}
	}
	if _, ok := manifests.ParseVersion("none"); ok {
		t.Error("no digits must not parse")
	}
	a, _ := manifests.ParseVersion("2.1.9")
	b, _ := manifests.ParseVersion("2.1.10")
	if !a.Less(b) || b.Less(a) || a.Compare(a) != 0 || a.String() != "2.1.9" {
		t.Error("ordering is numeric, not lexical")
	}
}
