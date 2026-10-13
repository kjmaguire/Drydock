package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/skills"
)

// The connector gallery (decision P43) shows every catalog server as a card with what a human needs to decide: publisher,
// what it gives agents, how it signs in, its license, and whether it is connected. Listing contacts no server.
func TestMCPGalleryShowsEveryCatalogServerAsACard(t *testing.T) {
	a := open(t, "")
	var cards []core.MCPGalleryCard
	a.call("mcplib.gallery", nil, &cards)
	byID := map[string]core.MCPGalleryCard{}
	var ids []string
	for _, c := range cards {
		byID[c.ID] = c
		ids = append(ids, c.ID)
		if c.Status != core.GalleryNotConnected || c.LibraryID != "" || c.Publisher == "" || c.Brand == "" || c.Summary == "" || !strings.HasPrefix(c.DocsURL, "https://") {
			t.Errorf("%s: %+v", c.ID, c)
		}
	}
	if strings.Join(ids, ",") != "mslearn,cfdocs,cfapi,gh,ctx7,linear,notion,sentry" {
		t.Fatalf("cards: %v", ids)
	}
	want := map[string]string{"mslearn": "none", "cfdocs": "none", "cfapi": "oauth", "gh": "bearer", "ctx7": "bearer",
		"linear": "oauth", "notion": "oauth", "sentry": "oauth"}
	for id, auth := range want {
		if byID[id].Auth != auth {
			t.Errorf("%s signs in with %q, want %q", id, byID[id].Auth, auth)
		}
	}
	// decision P45: no local-program card in the shipped gallery (templates are still connected from a test catalog below)
	for _, c := range cards {
		if c.Template || len(c.Command) > 0 || c.SecretEnv != "" {
			t.Errorf("%s is a local program; the shipped gallery offers hosted servers only (P45): %+v", c.ID, c)
		}
	}
	if c := byID["gh"]; c.Publisher != "GitHub" || c.Brand != "github" || c.Integration != "github" || c.AuthOptional || c.TokenHelp == "" {
		t.Errorf("gh: %+v", c)
	}
	if !byID["ctx7"].AuthOptional || byID["cfdocs"].Group != "Cloudflare" || byID["cfapi"].Group != "Cloudflare" || byID["mslearn"].Publisher != "Microsoft" {
		t.Error("ctx7 optional token, Cloudflare group, Microsoft publisher")
	}
	// decision P47: a vendor's skill registries ride on its cards, and every id names a registry the Skills page knows
	if strings.Join(byID["cfdocs"].SkillRegistries, ",") != "cloudflare-skills" || strings.Join(byID["cfapi"].SkillRegistries, ",") != "cloudflare-skills" ||
		strings.Join(byID["mslearn"].SkillRegistries, ",") != "msdocs-mcp-skills,msdocs-agent-skills,microsoft-skills" || len(byID["gh"].SkillRegistries) != 0 {
		t.Errorf("skill registries: cfdocs %v mslearn %v gh %v", byID["cfdocs"].SkillRegistries, byID["mslearn"].SkillRegistries, byID["gh"].SkillRegistries)
	}
	regs, err := skills.CuratedRegistries()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, r := range regs {
		known[r.ID] = true
	}
	for _, c := range cards {
		for _, id := range c.SkillRegistries {
			if !known[id] {
				t.Errorf("%s names an unknown skill registry %q", c.ID, id)
			}
		}
	}
	if c := byID["mslearn"]; len(c.Tools) != 3 || c.ToolsListed || !c.Tools[0].ReadOnlyHint {
		t.Errorf("declared tools before connecting: %+v", c.Tools)
	}
}

// galleryCatalog is a catalog of test doubles (servers in this process and this test binary as a stdio server).
func galleryCatalog(t *testing.T, docsURL, tokURL, oauthURL string) *mcp.Catalog {
	self, _ := os.Executable()
	argv, _ := json.Marshal([]string{self})
	doc := fmt.Sprintf(`schema: 1
servers:
  - {id: docs, display: Docs double, publisher: Tests, brand: test, summary: "docs", transport: http, url: %q, auth: {kind: none}, trust: public_docs, license: {server: MIT}}
  - id: tok
    display: Token double
    publisher: Tests
    brand: test
    summary: tok
    transport: http
    url: %q
    auth: {kind: bearer, keyring: "mcp/tok"}
    trust: private_data
    read_only_tools: [me]
    identity: {tool: me, fields: [login]}
    license: {server: MIT}
  - id: oa
    display: OAuth double
    publisher: Tests
    brand: test
    summary: oa
    transport: http
    url: %q
    auth: {kind: oauth_dcr, keyring: "mcp/oa"}
    trust: private_data
    read_only_tools: [whoami]
    identity: {tool: whoami}
    license: {server: MIT}
  - id: prog
    display: Program double
    publisher: Tests
    brand: test
    summary: prog
    transport: stdio
    command: {argv: %s, env: {%s: "1"}, secret_env: {API_KEY: "mcp/prog"}}
    auth: {kind: none}
    trust: private_data
    license: {server: MIT, bundled: false}
    template: true
  - {id: down, display: Gone double, publisher: Tests, brand: test, summary: down, transport: http, url: "http://127.0.0.1:1/mcp", auth: {kind: none}, trust: public_docs, license: {server: MIT}}
`, docsURL, tokURL, oauthURL, argv, testMCPEnv)
	cat, err := mcp.ParseCatalog([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func openGalleryApp(t *testing.T, cat *mcp.Catalog) *app {
	t.Helper()
	dir := t.TempDir()
	script, _ := json.Marshal(fakeagent.Script{Default: []fakeagent.Step{{Say: "ok"}}})
	sp := filepath.Join(dir, "script.json")
	os.WriteFile(sp, script, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Credentials: providers.NewMemoryCredentialStoreForTests(), Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}, MCPCatalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}
}

func serveSDK(t *testing.T, srv *sdk.Server, check func(r *http.Request) bool) string {
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil && !check(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp"
}

func (a *app) card(id string) core.MCPGalleryCard {
	a.t.Helper()
	var cards []core.MCPGalleryCard
	a.call("mcplib.gallery", nil, &cards)
	for _, c := range cards {
		if c.ID == id {
			return c
		}
	}
	a.t.Fatalf("no card %s", id)
	return core.MCPGalleryCard{}
}

// Connect, as other apps do it: no project needed, OAuth in the browser or a token, and the card then says Connected, as whom,
// and which tools the server offers. A connected server is an ordinary library server: it goes into a project and to agents
// the library's way. A server that cannot be reached is connected but its card says why.
func TestMCPGalleryConnectsByOAuthTokenAndProgram(t *testing.T) {
	docs := sdk.NewServer(&sdk.Implementation{Name: "docs-double", Title: "Docs Double", Version: "2.1.0"}, &sdk.ServerOptions{Instructions: "Search first."})
	sdk.AddTool(docs, &sdk.Tool{Name: "search", Description: "search the docs"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "hit"}}}, nil, nil
	})
	const token = "tok-gallery-0123456789abcdef"
	tokSrv := sdk.NewServer(&sdk.Implementation{Name: "tok-double", Version: "1"}, nil)
	sdk.AddTool(tokSrv, &sdk.Tool{Name: "me", Description: "who"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: `{"login":"octo","id":7}`}}}, nil, nil
	})
	f := newOAuthFake(t)
	cat := galleryCatalog(t, serveSDK(t, docs, nil), serveSDK(t, tokSrv, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+token }), f.URL())
	a := openGalleryApp(t, cat)

	// no account needed: connected at once, with the server's own identity and its tools
	var res core.MCPConnectResult
	a.call("mcplib.connect", core.MCPConnectReq{CatalogID: "docs"}, &res)
	c := res.Card
	if c.Status != core.GalleryConnected || c.LibraryID != "docs" || !c.ToolsListed || len(c.Tools) != 1 || c.Tools[0].Name != "search" || res.OAuth != nil {
		t.Fatalf("docs: %+v", res)
	}
	if c.ServerInfo == nil || c.ServerInfo.Title != "Docs Double" || c.ServerInfo.Version != "2.1.0" || c.ServerInfo.Instructions != "Search first." {
		t.Fatalf("serverInfo: %+v", c.ServerInfo)
	}

	// a token server: refused without the token, connected with it, and the account comes from its identity tool
	if err := a.callErr("mcplib.connect", core.MCPConnectReq{CatalogID: "tok"}); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("connect without a token: %v", err)
	}
	a.call("mcplib.connect", core.MCPConnectReq{CatalogID: "tok", Token: token}, &res)
	if res.Card.Status != core.GalleryConnected || res.Card.Account != "octo" {
		t.Fatalf("tok: %+v", res.Card)
	}
	if w := leaks(t, a, token); len(w) > 0 {
		t.Fatalf("the token leaked into %v", w)
	}

	// OAuth: Connect hands back the sign-in page; the card says Needs sign-in until the browser comes back
	a.call("mcplib.connect", core.MCPConnectReq{CatalogID: "oa"}, &res)
	if res.OAuth == nil || res.OAuth.State != "waiting" || !strings.Contains(res.OAuth.URL, "/authorize") || res.Card.Status != core.GalleryNeedsSignIn {
		t.Fatalf("oauth connect: %+v %+v", res.OAuth, res.Card)
	}
	resp, err := http.Get(res.OAuth.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	a.waitFor("the gallery card to show the sign-in", func() bool {
		c := a.card("oa")
		return c.Status == core.GalleryConnected && c.Account == "alice"
	})
	if c := a.card("oa"); c.ServerInfo == nil || c.ServerInfo.Title != "OAuth test double" || !c.ToolsListed {
		t.Fatalf("oa after sign-in: %+v", c)
	}
	if w := leaks(t, a, f.Access()); len(w) > 0 {
		t.Fatalf("the access token leaked into %v", w)
	}

	// a program the human runs: its secret goes to the credential store, and the program's identity comes back
	if err := a.callErr("mcplib.connect", core.MCPConnectReq{CatalogID: "prog"}); err == nil || !strings.Contains(err.Error(), "API_KEY") {
		t.Fatalf("connect a template without its secret: %v", err)
	}
	const secret = "prog-secret-abcdef0123456789"
	a.call("mcplib.connect", core.MCPConnectReq{CatalogID: "prog", Token: secret}, &res)
	if res.Card.Status != core.GalleryConnected || res.Card.ServerInfo == nil || res.Card.ServerInfo.Name != "drydock-test-server" ||
		res.Card.ServerInfo.Instructions == "" || !res.Card.ToolsListed {
		t.Fatalf("prog: %+v", res.Card)
	}
	if w := leaks(t, a, secret); len(w) > 0 {
		t.Fatalf("the program's secret leaked into %v", w)
	}

	// a server that is not there: connected in the library, Error on the card with the reason
	a.call("mcplib.connect", core.MCPConnectReq{CatalogID: "down"}, &res)
	if res.Card.Status != core.GalleryError || !strings.Contains(res.Card.Detail, "could not") || res.Card.LibraryID == "" {
		t.Fatalf("down: %+v", res.Card)
	}

	// connecting twice is the same connection
	a.call("mcplib.connect", core.MCPConnectReq{CatalogID: "docs"}, &res)
	var lib []core.MCPLibraryServer
	a.call("mcplib.list", nil, &lib)
	if len(lib) != 5 {
		t.Fatalf("library after connecting five servers (one twice): %d", len(lib))
	}

	// Add to project and Grant to agents are the library's own
	p, _ := a.setup("gocalc")
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend", Adapter: "claude", Role: "backend", Model: "fake-1"}, &ag)
	a.call("mcpserver.add", map[string]string{"projectId": p.ID, "serverId": "docs"}, nil)
	a.call("mcpserver.grant", map[string]any{"projectId": p.ID, "serverId": "tok", "agentId": ag.ID, "on": true}, nil)
	if c := a.card("docs"); len(c.Projects) != 1 || c.Projects[0] != p.ID {
		t.Fatalf("projects on the card: %+v", c.Projects)
	}
	var view []core.MCPServerView
	a.call("mcpserver.list", map[string]string{"projectId": p.ID}, &view)
	if len(view) != 2 {
		t.Fatalf("project servers: %+v", view)
	}

	// Disconnect forgets the connection, and the card is back to Not connected
	a.call("mcplib.delete", map[string]string{"id": "tok"}, nil)
	if c := a.card("tok"); c.Status != core.GalleryNotConnected || c.Account != "" {
		t.Fatalf("after disconnect: %+v", c)
	}
	// a new token forgets the old account until the server is reached again
	a.call("mcplib.connect", core.MCPConnectReq{CatalogID: "tok", Token: token}, &res)
	if res.Card.Account != "octo" {
		t.Fatalf("reconnected: %+v", res.Card)
	}
}

// A server the human describes is identified by what it says about itself: its serverInfo and instructions, kept with the
// library server after Find tools, and forgotten when its address changes.
func TestCustomServerIsIdentifiedByItsServerInfo(t *testing.T) {
	self, _ := os.Executable()
	a := openGalleryApp(t, galleryCatalog(t, "http://127.0.0.1:1/mcp", "http://127.0.0.1:1/mcp", "http://127.0.0.1:1/mcp"))
	var lib core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "mine", Display: "My server", Transport: "stdio", Argv: []string{self}, Env: map[string]string{testMCPEnv: "1"}}, &lib)
	if lib.ServerInfo != nil || lib.CatalogID != "" {
		t.Fatalf("before connecting: %+v", lib)
	}
	a.call("mcplib.discover", map[string]string{"id": "mine"}, &lib)
	if lib.ServerInfo == nil || lib.ServerInfo.Name != "drydock-test-server" || lib.ServerInfo.Title != "Drydock test server" || lib.ServerInfo.Version != "1" ||
		!strings.Contains(lib.ServerInfo.Instructions, "test double") || lib.LastError != "" {
		t.Fatalf("identity: %+v", lib.ServerInfo)
	}
	// a changed command is another server: what the old one said is forgotten
	lib = core.MCPLibraryServer{}
	a.call("mcplib.save", core.MCPServerInput{ID: "mine", Display: "My server", Transport: "stdio", Argv: []string{self, "--other"}, Env: map[string]string{testMCPEnv: "1"}}, &lib)
	if lib.ServerInfo != nil || len(lib.Tools) != 0 {
		t.Fatalf("after a changed command: %+v", lib)
	}
	// a failure is kept on the server for the page, and a success clears it
	a.call("mcplib.save", core.MCPServerInput{ID: "gone", Display: "Gone", Transport: "http", URL: "http://127.0.0.1:1/mcp"}, nil)
	if err := a.callErr("mcplib.discover", map[string]string{"id": "gone"}); err == nil {
		t.Fatal("an unreachable server was discovered")
	}
	var all []core.MCPLibraryServer
	a.call("mcplib.list", nil, &all)
	for _, s := range all {
		if s.ID == "gone" && (s.LastError == "" || s.LastErrorAt == 0) {
			t.Fatalf("the failure was not kept: %+v", s)
		}
	}
}
