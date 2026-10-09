package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestBuiltinCatalogLoadsAndMatchesTheDocs(t *testing.T) {
	c, err := LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	if c.Schema != 1 {
		t.Fatal(c.Schema)
	}
	get := func(id string) ServerSpec {
		s, ok := c.Get(id)
		if !ok {
			t.Fatalf("catalog lacks %s", id)
		}
		return s
	}
	tools := func(s ServerSpec) string {
		var n []string
		for _, d := range s.Tools {
			n = append(n, d.Name)
		}
		return strings.Join(n, ",")
	}
	ml := get("mslearn")
	if ml.URL != "https://learn.microsoft.com/api/mcp" || ml.Transport != "http" || ml.Auth.Kind != "none" || ml.Trust != TrustPublicDocs ||
		ml.Cache.TTL.D() != 24*time.Hour || ml.Shape.URLParams["maxTokenBudget"] != 2000 || ml.License.Content != "CC-BY-4.0" || ml.License.Attribution == "" {
		t.Fatalf("mslearn: %+v", ml)
	}
	if tools(ml) != "microsoft_docs_search,microsoft_docs_fetch,microsoft_code_sample_search" {
		t.Fatalf("mslearn tools: %s", tools(ml))
	}
	if u, _ := upstreamEndpoint(ml); u != "https://learn.microsoft.com/api/mcp?maxTokenBudget=2000" {
		t.Fatalf("endpoint with ?maxTokenBudget: %s", u)
	}
	cf := get("cfdocs")
	if cf.URL != "https://docs.mcp.cloudflare.com/mcp" || cf.Auth.Kind != "none" || cf.Cache.TTL.D() != 24*time.Hour || !strings.Contains(tools(cf), "search_cloudflare_documentation") {
		t.Fatalf("cfdocs: %+v", cf)
	}
	gh := get("gh")
	if gh.URL != "https://api.githubcopilot.com/mcp/" || gh.Auth.Kind != AuthBearer || gh.Auth.Keyring == "" || gh.Trust != TrustPrivateData || gh.DataClass != ContentPrivate ||
		gh.Cache.Scope != "project" || gh.Cache.TTL.D() != 60*time.Second {
		t.Fatalf("gh: %+v", gh)
	}
	for _, w := range []string{"pull_request_review_write", "create_pull_request", "merge_pull_request"} {
		if !matchAny(gh.SideEffectsTools, w) {
			t.Errorf("gh: %s must be side-effecting", w)
		}
	}
	for _, r := range []string{"pull_request_read", "actions_get", "actions_list", "get_job_logs", "list_issues"} {
		if !matchAny(gh.ReadOnlyTools, r) || gh.isSideEffect(r, false, false) {
			t.Errorf("gh: %s must be read-only", r)
		}
	}
	if !gh.isSideEffect("brand_new_tool", false, false) {
		t.Error("an unknown tool of a private_data server must be treated as write-class")
	}
	cx := get("ctx7")
	if cx.URL != "https://mcp.context7.com/mcp" || cx.Auth.Kind != AuthBearer || !cx.Auth.Optional || cx.Trust != TrustThirdPartyDocs || tools(cx) != "resolve-library-id,query-docs" {
		t.Fatalf("ctx7: %+v", cx)
	}
	for _, id := range []string{"linear", "notion", "sentry"} {
		s := get(id)
		if s.Transport != "http" || s.Trust != TrustPrivateData || s.Cache.TTL != 0 || s.License.Bundled || s.TermsStatus != "unverified" {
			t.Errorf("%s: %+v", id, s)
		}
	}
	if get("linear").URL != "https://mcp.linear.app/mcp/readonly" || get("notion").URL != "https://mcp.notion.com/mcp" || get("sentry").URL != "https://mcp.sentry.dev/mcp" {
		t.Error("hosted urls")
	}
	if get("sentry").Auth.Scheme != "Sentry-Bearer" {
		t.Error("sentry auth scheme")
	}
	// D14: the stdio servers are templates, never bundled, never started by default
	for _, s := range c.Servers {
		if s.Transport == TransportStdio && (!s.Template || s.License.Bundled) {
			t.Errorf("%s: stdio entries in the shipped catalog must be non-bundled templates (D14)", s.ID)
		}
		if len(s.ID) > 8 {
			t.Errorf("%s: id longer than 8", s.ID)
		}
	}
	if !get("aikido").Template || !strings.HasPrefix(get("aikido").License.Server, "AGPL") {
		t.Error("aikido template")
	}
	// the local Notion server is not in the catalog at all
	for _, s := range c.Servers {
		if s.Transport == TransportStdio && strings.Contains(strings.Join(s.Command.Argv, " "), "notion") {
			t.Error("notion local server must not be offered (D14)")
		}
	}
	// tool name length: <=8 id + "__" + tool stays well under typical API limits
	for _, s := range c.Servers {
		for _, d := range s.Tools {
			if n := len(exposed(s.ID, d.Name)); n > 48 {
				t.Errorf("%s: exposed name %d chars", exposed(s.ID, d.Name), n)
			}
		}
	}
}

func TestCatalogValidationRejectsBadEntries(t *testing.T) {
	base := func() ServerSpec {
		s := ServerSpec{ID: "ok1", Display: "Ok", Transport: "http", URL: "https://example.com/mcp", Trust: TrustPublicDocs, License: LicenseSpec{Server: "MIT"}}
		s.applyDefaults()
		return s
	}
	if err := func() error { s := base(); return s.Validate(false) }(); err != nil {
		t.Fatalf("baseline must be valid: %v", err)
	}
	cases := map[string]func(*ServerSpec){
		"id too long":             func(s *ServerSpec) { s.ID = "toolongid" },
		"id uppercase":            func(s *ServerSpec) { s.ID = "Ab" },
		"id with underscore":      func(s *ServerSpec) { s.ID = "a__b" },
		"reserved id":             func(s *ServerSpec) { s.ID = "workspace" },
		"no display":              func(s *ServerSpec) { s.Display = "" },
		"plain http to the world": func(s *ServerSpec) { s.URL = "http://example.com/mcp" },
		"url credentials":         func(s *ServerSpec) { s.URL = "https://u:p@example.com/mcp" },
		"ftp":                     func(s *ServerSpec) { s.URL = "ftp://example.com" },
		"http with command":       func(s *ServerSpec) { s.Command = &CommandSpec{Argv: []string{"x"}} },
		"stdio without command":   func(s *ServerSpec) { s.Transport = "stdio"; s.URL = "" },
		"stdio with url":          func(s *ServerSpec) { s.Transport = "stdio"; s.Command = &CommandSpec{Argv: []string{"x"}} },
		"secret in argv": func(s *ServerSpec) {
			s.Transport = "stdio"
			s.URL = ""
			s.Command = &CommandSpec{Argv: []string{"x", "--token=abcdef123456"}}
		},
		"bad transport":            func(s *ServerSpec) { s.Transport = "websocket" },
		"bad trust":                func(s *ServerSpec) { s.Trust = "trusted" },
		"bearer without keyring":   func(s *ServerSpec) { s.Auth = AuthSpec{Kind: AuthBearer} },
		"bad auth":                 func(s *ServerSpec) { s.Auth = AuthSpec{Kind: "basic"} },
		"private data public data": func(s *ServerSpec) { s.Trust = TrustPrivateData; s.DataClass = ContentPublicDocs },
		"private global cache": func(s *ServerSpec) {
			s.Trust = TrustPrivateData
			s.DataClass = ContentPrivate
			s.Cache = CacheSpec{TTL: Duration(time.Hour), Scope: "global"}
		},
		"bad cache scope":         func(s *ServerSpec) { s.Cache.Scope = "universe" },
		"negative shape":          func(s *ServerSpec) { s.Shape.MaxInlineBytes = -1 },
		"bad terms":               func(s *ServerSpec) { s.TermsStatus = "maybe" },
		"unknown role":            func(s *ServerSpec) { s.RecommendedRoles = []string{"ceo"} },
		"duplicate tool decl":     func(s *ServerSpec) { s.Tools = []ToolDecl{{Name: "a"}, {Name: "a"}} },
		"tool with separator":     func(s *ServerSpec) { s.Tools = []ToolDecl{{Name: "a__b"}} },
		"cc-by needs attribution": func(s *ServerSpec) { s.License.Content = "CC-BY-4.0" },
		"bundle FSL":              func(s *ServerSpec) { s.License = LicenseSpec{Server: "FSL-1.1-Apache-2.0", Bundled: true} },
		"bundle AGPL":             func(s *ServerSpec) { s.License = LicenseSpec{Server: "AGPL-3.0", Bundled: true} },
	}
	for name, mod := range cases {
		s := base()
		mod(&s)
		s.applyDefaultsKeep()
		if err := s.Validate(false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	s := base()
	s.Transport, s.URL = "stdio", ""
	s.Command = &CommandSpec{Argv: []string{"x"}}
	s.License.Bundled = true
	if err := s.Validate(true); err == nil {
		t.Error("the shipped catalog may not bundle a stdio server")
	}
}

// applyDefaultsKeep re-derives defaults without overriding fields a case set on purpose.
func (s *ServerSpec) applyDefaultsKeep() {
	if s.DataClass == "" {
		s.applyDefaults()
	}
}

func TestParseCatalogStrictness(t *testing.T) {
	if _, err := ParseCatalog([]byte("schema: 2\nservers: []\n")); err == nil {
		t.Error("schema 2 accepted")
	}
	if _, err := ParseCatalog([]byte("schema: 1\nservers:\n  - id: a\n    bogus_field: 1\n")); err == nil {
		t.Error("unknown field accepted")
	}
	dup := "schema: 1\nservers:\n" + strings.Repeat("  - {id: a, display: A, transport: http, url: 'https://x.example/mcp', trust: public_docs, license: {server: MIT}}\n", 2)
	if _, err := ParseCatalog([]byte(dup)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate ids: %v", err)
	}
	if _, err := ParseCatalog([]byte("schema: 1\nservers:\n  - {id: a, display: A, transport: http, url: 'https://x.example/mcp', trust: public_docs, cache: {ttl: soon}, license: {server: MIT}}\n")); err == nil {
		t.Error("bad duration accepted")
	}
	c, err := ParseCatalog([]byte("schema: 1\nservers:\n  - {id: a, display: A, transport: http, url: 'https://x.example/mcp', trust: public_docs, cache: {ttl: 0}, license: {server: MIT}}\n"))
	if err != nil || c.Servers[0].Cache.TTL != 0 || c.Servers[0].DataClass != ContentPublicDocs || c.Servers[0].Cache.Scope != "global" {
		t.Errorf("defaults: %v %+v", err, c)
	}
}

func TestProjectEnableDisableAndCustomServers(t *testing.T) {
	c, _ := LoadBuiltin()
	ids := func(ss []ServerSpec) string {
		var o []string
		for _, s := range ss {
			o = append(o, s.ID)
		}
		return strings.Join(o, ",")
	}
	def, err := c.Resolve(nil)
	if err != nil || ids(def) != "cfdocs,mslearn" {
		t.Fatalf("defaults: %v %v", ids(def), err)
	}
	pc, err := ParseProjectConfig([]byte(`
enabled: [gh, ctx7, sentryx]
disabled: [cfdocs]
overrides:
  gh: {cache_ttl: 5m, allow_tools: [pull_request_read], critical: true}
  ctx7: {side_effects_tools: ["query-*"]}
custom:
  - id: wiki
    display: Team wiki
    transport: http
    url: https://wiki.example.com/mcp
    trust: private_data
    allow_tools: [search, read_page]
    read_only_tools: [search, read_page]
    cache: {ttl: 2m, scope: project}
    license: {server: LicenseRef-Internal}
  - id: loc
    display: Local tool
    transport: stdio
    command: {argv: ["C:\\tools\\mytool.exe", "--mcp"], env: {MODE: ro}, secret_env: {TOOL_KEY: "mcp/loc"}}
    trust: private_data
    allow_tools: ["get_*"]
    cache: {ttl: 0s, scope: project}
    license: {server: LicenseRef-Internal}
`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Resolve(pc)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "ctx7,gh,loc,mslearn,sentryx,wiki" {
		t.Fatalf("resolved: %s", ids(got))
	}
	by := map[string]ServerSpec{}
	for _, s := range got {
		by[s.ID] = s
	}
	if by["gh"].Cache.TTL.D() != 5*time.Minute || !by["gh"].Critical || len(by["gh"].AllowTools) != 1 {
		t.Errorf("gh override: %+v", by["gh"])
	}
	ctx7 := by["ctx7"]
	if !ctx7.isSideEffect("query-docs", true, false) {
		t.Error("project made query-docs side-effecting")
	}
	if cat, _ := c.Get("gh"); cat.Cache.TTL.D() != 60*time.Second || cat.Critical {
		t.Error("overrides must not mutate the catalog")
	}
	w := by["wiki"]
	if w.Cache.TTL.D() != 2*time.Minute || !w.toolAllowedBySpec("search") || w.toolAllowedBySpec("delete_page") || w.DataClass != ContentPrivate {
		t.Errorf("custom http: %+v", w)
	}
	l := by["loc"]
	if l.Transport != "stdio" || l.Command.SecretEnv["TOOL_KEY"] != "mcp/loc" || !l.toolAllowedBySpec("get_x") || l.toolAllowedBySpec("set_x") {
		t.Errorf("custom stdio: %+v", l)
	}
	// templates are never started unless named
	if _, err := c.Resolve(&ProjectConfig{Enabled: []string{"nosuch"}}); err == nil {
		t.Error("unknown catalog id accepted")
	}
	if _, err := c.Resolve(&ProjectConfig{Overrides: map[string]ServerOverride{"nosuch": {}}}); err == nil {
		t.Error("unknown override id accepted")
	}
	if _, err := c.Resolve(&ProjectConfig{Custom: []ServerSpec{{ID: "gh", Display: "x", Transport: "http", URL: "https://x.example", Trust: TrustPublicDocs, License: LicenseSpec{Server: "MIT"}}}}); err == nil {
		t.Error("custom id colliding with the catalog accepted")
	}
	// invalid custom servers are rejected at parse time
	for name, doc := range map[string]string{
		"http to the world": "custom:\n  - {id: bad, display: B, transport: http, url: 'http://evil.example/mcp', trust: public_docs, license: {server: MIT}}\n",
		"unknown field":     "custom:\n  - {id: bad, display: B, transport: http, url: 'https://x.example/mcp', trust: public_docs, flavour: spicy, license: {server: MIT}}\n",
		"long id":           "custom:\n  - {id: waytoolongid, display: B, transport: http, url: 'https://x.example/mcp', trust: public_docs, license: {server: MIT}}\n",
	} {
		if _, err := ParseProjectConfig([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if pc, err := ParseProjectConfig(nil); err != nil || pc == nil {
		t.Errorf("empty project config: %v", err)
	}
}

func TestCustomServerRunsThroughTheSamePipeline(t *testing.T) {
	// A custom stdio server (the real test MCP server process) with its own allowlist and TTL, enabled through the project
	// configuration, goes through the allowlist, cache, shaping and egress pipeline like a catalog server.
	self := stdioSpec("tmp", nil).Command.Argv[0]
	pc, err := ParseProjectConfig([]byte(`
custom:
  - id: mine
    display: My tool
    transport: stdio
    command: {argv: ["` + strings.ReplaceAll(self, `\`, `\\`) + `"], env: {` + modeEnv + `: server}}
    trust: public_docs
    allow_tools: [echo, counter]
    read_only_tools: [echo, counter]
    cache: {ttl: 30s, scope: global}
    shape: {max_inline_bytes: 512}
    license: {server: LicenseRef-Internal}
`))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := LoadBuiltin()
	servers, err := c.Resolve(pc)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, nil)
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-custom", ProjectID: "p"}, Servers: servers, Policy: allow(map[string][]string{"mine": {"*"}})})
	cs := e.connect(ep)
	if names := toolNames(t, cs); strings.Join(names, ",") != "fetch_range,mine__counter,mine__echo" {
		t.Fatalf("custom server's own allowlist: %v", names)
	}
	callTool(t, cs, "mine__counter", map[string]any{})
	if b, _ := ExtractBody(textOf(callTool(t, cs, "mine__counter", map[string]any{}))); b != "n=1" {
		t.Fatalf("custom TTL cache: %q", b)
	}
	e.clock.Advance(31 * time.Second)
	if b, _ := ExtractBody(textOf(callTool(t, cs, "mine__counter", map[string]any{}))); b != "n=2" {
		t.Fatalf("custom TTL expiry: %q", b)
	}
	if r := callTool(t, cs, "mine__big", map[string]any{"bytes": 10}); !r.IsError {
		t.Fatal("tool outside the custom allowlist")
	}
}

// Every catalog entry says in plain words what it gives an agent: the library shows it when a human adds a server from the catalog.
func TestCatalogEntriesHaveShortDescriptions(t *testing.T) {
	c, err := LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range c.Servers {
		if d := strings.TrimSpace(s.Description); d == "" || len(d) > 300 || strings.Contains(d, "\n") {
			t.Errorf("%s: description %q must be one short paragraph", s.ID, d)
		}
	}
}
