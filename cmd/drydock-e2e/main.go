// Command drydock-e2e is a TEST HARNESS for the browser end-to-end tests in ui/e2e. It is not part of the product and is
// never shipped.
//
// What is real: the Drydock core (SQLite, event bus, workflow engine, tasks, room, pipeline, git worktrees, the checks that
// run `go test`) and the development HTTP/SSE transport that serves the built ui/dist. What is a test double: the agent
// CLI. The harness installs the repository's scripted fakeagent (internal/agents/fakeagent) as the "claude" adapter by
// re-executing itself when DRYDOCK_FAKE_AGENT=1, exactly as internal/core/core_test.go does. The fake worker really edits
// files in its worktree and the fake reviewer answers SHIP, so everything between those two is the real code path. Nothing
// printed by this harness or shown by the UI may be presented as a real agent.
//
// Output, one line each, on stdout (the Playwright global setup parses them):
//
//	DRYDOCK_E2E_REPO <path>              a fresh git repository copied from testdata/fixtures/gocalc (repeated -repos times)
//	DRYDOCK_E2E_GREEN_REPO <path>        (-green-repos N) baseline tests pass, and a repository skill is committed
//	DRYDOCK_E2E_DOCS <path>              a folder with one Markdown page, for an expert pack of type local_dir
//	DRYDOCK_E2E_OAUTH_MCP <url>          a fake MCP server that wants an OAuth sign-in (internal/mcp/oauthfake, a test double)
//	DRYDOCK_E2E_SKILL_REGISTRY <url>     a fake GitHub for the skill registries (cloudflare/skills holds three stand-in skills; decision P48)
//	DRYDOCK_E2E_LINEAR_API <url>         a Linear GraphQL protocol fixture (internal/integrations/itfixture, a test double)
//	DRYDOCK_E2E_LICENSE_ACTIVE <token>   (-licensed only) a license signed by this run's throwaway key, valid for 30 days
//	DRYDOCK_E2E_LICENSE_EXPIRED <token>  (-licensed only) a license signed by the same key that expired 30 days ago
//	DRYDOCK_E2E http://127.0.0.1:PORT/?token=TOKEN
//
// -licensed is a TEST HARNESS MODE, not a product feature. It generates a throwaway Ed25519 key pair for this process only
// and sets the build-time variable core.LicensePublicKeys to its public half, so the real licensing code runs in a build
// that "has keys": the app starts with NO license (read-only) until the e2e imports one of the tokens printed above. The
// private key is never written anywhere. Without -licensed the harness is a build with no keys, which allows everything.
//
// The harness also installs an in-memory credential store (a test double) so provider keys can be entered in the e2e.
// The DRYDOCK_E2E line is printed last, after the server listens.
//
// cmd/drydock-verify drives the same harness over HTTP for its core smoke test (no browser) and starts it with
// -exit-on-stdin-close, so closing the pipe stops it cleanly on Windows too.
package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
	"github.com/kjmaguire/drydock/internal/license"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/mcp/mcptestserver"
	"github.com/kjmaguire/drydock/internal/mcp/oauthfake"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
	"github.com/kjmaguire/drydock/internal/scrub"
	"github.com/kjmaguire/drydock/internal/transport/httpapi"
	"github.com/kjmaguire/drydock/internal/wsl"
	"github.com/kjmaguire/drydock/internal/wsl/wsltest"
)

const (
	// implemented Subtract, without and with a doc comment (the second is what the worker writes after human feedback)
	implPlain = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Subtract(a, b int) int { return a - b }\n"
	// the fixture with Subtract already implemented, so a green baseline exists for the test-first templates
	greenCalc    = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\n// Subtract returns a minus b.\nfunc Subtract(a, b int) int { return a - b }\n"
	calcMul      = greenCalc + "\n// Multiply returns a times b.\nfunc Multiply(a, b int) int { return a * b }\n"
	mulTests     = "package gocalc\n\nimport \"testing\"\n\n// AC1: Given 3 and 4, when Multiply is called, then it returns 12\nfunc TestAC1_Multiply(t *testing.T) {\n\tif got := Multiply(3, 4); got != 12 {\n\t\tt.Fatalf(\"Multiply(3,4) = %d, want 12\", got)\n\t}\n}\n\n// AC2: Given 0 and 9, when Multiply is called, then it returns 0\nfunc TestAC2_MultiplyByZero(t *testing.T) {\n\tif got := Multiply(0, 9); got != 0 {\n\t\tt.Fatalf(\"Multiply(0,9) = %d, want 0\", got)\n\t}\n}\n"
	mulSpec      = "# Multiply\n## Problem\nCallers of the gocalc package cannot multiply two integers.\n## Scope and non-goals\nIntegers only.\n## Acceptance criteria\n- Given 3 and 4, when Multiply is called, then it returns 12\n- Given 0 and 9, when Multiply is called, then it returns 0\n## Open questions\nnone"
	skillMD      = "---\nname: go-style\ndescription: Go style for this repository\nmetadata:\n  drydock.globs: \"**/*.go\"\n  drydock.roles: \"Backend\"\n---\nKeep functions small.\n"
	planProposal = `{"tasks":[{"key":"mul","title":"Add Multiply","description":"Add Multiply to gocalc","role":"backend","priority":3,"dependsOn":[],"acceptance":["Given 3 and 4, when Multiply is called, then it returns 12"]},{"key":"div","title":"Add Divide","description":"Add Divide to gocalc","role":"backend","dependsOn":["mul"],"acceptance":["Given 8 and 2, when Divide is called, then it returns 4"]}],"notes":"multiply first"}`
	implDoc      = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\n// Subtract returns a minus b.\nfunc Subtract(a, b int) int { return a - b }\n"
)

func main() {
	// TEST DOUBLES of the security scan's external tools (internal/scan/scantest): the core starts this binary again as the
	// stand-in osv-scanner and opengrep, which print canned, documented output shapes
	if scantest.IsStandIn(os.Args) {
		os.Exit(scantest.Main(os.Args))
	}
	// TEST DOUBLES of the WSL run mode (internal/wsl/wsltest): the stand-in wsl.exe re-executes this binary, and the stand-in
	// claude inside its distro may start this binary as its MCP server command to check what reached it
	if wsltest.IsStandIn() {
		os.Exit(wsltest.StandIn(os.Args[1:]))
	}
	if wsltest.IsInteropCheck() {
		os.Exit(wsltest.InteropCheckMain())
	}
	// an agent CLI starts `<this binary> mcp-shim` as its MCP server; it must not start the harness
	if code, ok := mcp.MaybeRunShim(os.Args); ok {
		os.Exit(code)
	}
	// Re-exec as a real stdio MCP server (a test fixture) for the MCP servers page: the page adds this binary as a server.
	if os.Getenv("DRYDOCK_E2E_MCP_SERVER") == "1" {
		os.Exit(mcptestserver.Run())
	}
	// Re-exec as the scripted agent: the claude adapter runs this same binary with DRYDOCK_FAKE_AGENT=1.
	if os.Getenv("DRYDOCK_FAKE_AGENT") == "1" {
		os.Exit(fakeagent.Run(os.Args[1:], os.Stdin, os.Stdout))
	}
	ui := flag.String("ui", "ui/dist", "built UI to serve")
	fixture := flag.String("fixture", "testdata/fixtures/gocalc", "fixture project copied into each fresh repository")
	repos := flag.Int("repos", 1, "number of fresh git repositories to create and print")
	green := flag.Int("green-repos", 0, "number of fresh repositories whose baseline tests pass and that carry a repository skill (for the test-first templates and the Skills page)")
	port := flag.Int("port", 0, "port on 127.0.0.1 (0 = any)")
	licensed := flag.Bool("licensed", false, "TEST HARNESS MODE: embed a throwaway license key so the app starts read-only until a printed test token is imported")
	stdinStop := flag.Bool("exit-on-stdin-close", false, "also stop cleanly when standard input closes (cmd/drydock-verify uses this: on Windows a parent cannot send a child SIGTERM)")
	diag := flag.String("diag", "", "TEST HARNESS: when it stops, write <diag>.goroutines.txt and <diag>.events.txt (what the core was doing and what it published) for a failed run")
	flag.Parse()

	if err := run(*ui, *fixture, *repos, *green, *port, *licensed, *stdinStop, *diag); err != nil {
		log.Fatal(err)
	}
}

func run(uiDir, fixture string, nRepos, nGreen, port int, licensed, stdinStop bool, diag string) error {
	root, err := os.MkdirTemp("", "drydock-e2e-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

	self, err := os.Executable()
	if err != nil {
		return err
	}
	script, err := json.Marshal(fakeagent.Script{
		Rules: []fakeagent.Rule{
			// Chats (ui/e2e/chats.spec.ts): the scripted agent answers a chat message that carries the marker text, refuses a
			// Bash call in the default permission mode, and runs it after the human allowed the suggested rule and pressed Retry
			// (the retry text comes from the UI). It is a TEST DOUBLE, never presented as Claude.
			{Match: "drydock-e2e chat", Steps: []fakeagent.Step{{Say: "Hello from the scripted chat stand-in (a test double). I read: drydock-e2e chat."}}},
			{Match: "drydock-e2e touch", Steps: []fakeagent.Step{{Tool: "Bash", Input: json.RawMessage(`{"command":"touch chat_probe.txt"}`), Ask: true, Result: ""}, {Say: "I was not allowed to run that."}}},
			{Match: "the tool is now allowed", Steps: []fakeagent.Step{{Tool: "Bash", Input: json.RawMessage(`{"command":"touch chat_probe.txt"}`), Ask: true, Result: ""}, {Say: "Done: chat_probe.txt is there now."}}},
			// the independent reviewer (prompt text from internal/pipeline/review.go) always ships
			{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}},
			// after a human "request changes" the worker's prompt carries "Changes requested by the human:"
			{Match: "Changes requested by the human", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: implDoc}}, {Say: "addressed the feedback: added the doc comment"}}},
			// the Feature template (spec gate, tests first) and PlanTeam: each Drydock prompt starts with a phase line
			{Match: "Drydock phase: plan", Steps: []fakeagent.Step{{Say: planProposal}}},
			{Match: "Drydock phase: spec", Steps: []fakeagent.Step{{Say: mulSpec}}},
			{Match: "Drydock phase: write-tests", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "multiply_test.go", Content: mulTests}}, {Say: "wrote the tests first: done multiply_test.go"}}},
			{Match: "currently fail", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: calcMul}}, {Say: "implemented Multiply"}}},
		},
		Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: implPlain}}, {Say: "implemented Subtract"}},
	})
	if err != nil {
		return err
	}
	scriptPath := filepath.Join(root, "script.json")
	if err := os.WriteFile(scriptPath, script, 0o644); err != nil {
		return err
	}
	// children (the agent processes) inherit these; this process already passed the re-exec check above
	os.Setenv("DRYDOCK_FAKE_AGENT", "1")
	os.Setenv("DRYDOCK_FAKE_SCRIPT", scriptPath)

	for i := 0; i < nRepos; i++ {
		dir := filepath.Join(root, fmt.Sprintf("repo%d", i), "gocalc")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := copyTree(fixture, dir); err != nil {
			return fmt.Errorf("copy fixture: %w", err)
		}
		if _, err := git.Init(context.Background(), dir, "main"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
		fmt.Printf("DRYDOCK_E2E_REPO %s\n", dir)
	}

	for i := 0; i < nGreen; i++ {
		dir := filepath.Join(root, fmt.Sprintf("green%d", i), "gocalc")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := copyTree(fixture, dir); err != nil {
			return fmt.Errorf("copy fixture: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "calc.go"), []byte(greenCalc), 0o644); err != nil {
			return err
		}
		sk := filepath.Join(dir, ".aet", "skills", "go-style")
		if err := os.MkdirAll(sk, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sk, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
			return err
		}
		if _, err := git.Init(context.Background(), dir, "main"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
		fmt.Printf("DRYDOCK_E2E_GREEN_REPO %s\n", dir)
	}
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(docs, "channels.md"), []byte("# Channels\n\nA goroutine communicates over a channel. Close a channel from the sender.\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("DRYDOCK_E2E_DOCS %s\n", docs)

	// a TEST DOUBLE for a hosted MCP server that wants an OAuth sign-in (discovery, registration, PKCE, tokens); the real
	// Drydock sign-in flow runs against it
	oauth := oauthfake.New()
	defer oauth.Close()
	fmt.Printf("DRYDOCK_E2E_OAUTH_MCP %s\n", oauth.URL())
	catalog, err := e2eCatalog(oauth.URL())
	if err != nil {
		return err
	}

	// a PROTOCOL FIXTURE (test double) for Linear's GraphQL API, written from Linear's documented shapes
	// (internal/integrations/itfixture): the Integrations page connects to it without a project, then assigns it
	lin := itfixture.NewLinear("") // any key is accepted; the suite types a made-up one
	now := time.Now()
	for i, title := range []string{"Login fails on Safari", "Export to CSV", "Rotate the certificates"} {
		team := "ENG"
		if i == 2 {
			team = "OPS"
		}
		lin.Put(itfixture.LinearIssue{ID: fmt.Sprintf("e2e-%d", i), Identifier: fmt.Sprintf("%s-%d", team, 10+i), Title: title, Description: "From the Linear fixture.",
			State: "Todo", Team: team, Priority: 2, Created: now.Add(-time.Hour), Updated: now.Add(-time.Duration(i) * time.Minute)})
	}
	lints, linURL := itfixture.Serve(lin.Server())
	defer lints.Close()
	fmt.Printf("DRYDOCK_E2E_LINEAR_API %s\n", linURL)

	if licensed {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		core.LicensePublicKeys = "e2e=" + base64.StdEncoding.EncodeToString(pub)
		now := time.Now()
		mint := func(jti string, iat, exp time.Time) string {
			return license.Sign(priv, license.Payload{KID: "e2e", JTI: jti, Kind: "paid", Sub: "e2e@example.invalid", Name: "E2E test license", Plan: "test",
				IAT: iat.Unix(), NBF: iat.Unix(), EXP: exp.Unix()})
		}
		fmt.Printf("DRYDOCK_E2E_LICENSE_ACTIVE %s\n", mint("lic_e2e_active", now.Add(-time.Hour), now.Add(30*24*time.Hour)))
		fmt.Printf("DRYDOCK_E2E_LICENSE_EXPIRED %s\n", mint("lic_e2e_expired", now.Add(-60*24*time.Hour), now.Add(-30*24*time.Hour)))
	}

	// TEST DOUBLE: on a Linux host (the e2e job of the development host) a stand-in WSL with one distro, "Ubuntu", whose
	// stand-in claude answers in stream-json, so the Connections page and the agent forms show the WSL run mode end to end.
	// Windows runs the e2e without it (the stand-in is a POSIX shell script); the real wsl.exe is proven by CI's wsl job.
	var wslHost *wsl.Host
	if runtime.GOOS != "windows" {
		f, err := wsltest.Setup(filepath.Join(root, "wsl"), self, true, "Ubuntu")
		if err != nil {
			return fmt.Errorf("stand-in WSL: %w", err)
		}
		wslHost = &wsl.Host{Exe: f.Exe}
		fmt.Printf("DRYDOCK_E2E_WSL %s\n", f.Distros[0])
	} else {
		wslHost = &wsl.Host{Exe: filepath.Join(root, "no-wsl-in-the-e2e")} // never the machine's own distros
	}
	// TEST DOUBLES: the security scan's osv-scanner and opengrep are this binary's stand-ins (scantest), and the Code engine
	// gets a rules folder with one placeholder file; the product reads both tools from PATH and ships no rules
	scanRules := filepath.Join(root, "opengrep-rules")
	if err := os.MkdirAll(scanRules, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(scanRules, "placeholder.yaml"), []byte("# a placeholder rules file for the stand-in opengrep (a test double)\nrules: []\n"), 0o644); err != nil {
		return err
	}
	c, err := core.Open(context.Background(), core.Config{
		DataDir:  filepath.Join(root, "data"),
		User:     "e2e",
		LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: self, WSL: wslHost}},
		WSL:      wslHost,
		ScanTools: scan.Paths{OSV: scan.Tool{Path: self, Args: scantest.Args("osv-scanner", "--sleep-ms=1500")},
			Opengrep: scan.Tool{Path: self, Args: scantest.Args("opengrep", "--sleep-ms=2500")}},
		ScanRulesDir: scanRules,
		// TEST DOUBLE: an in-memory credential store so the Providers page can be exercised with a fake API key. The shipped
		// app uses the Windows Credential Manager; nothing entered here is persisted anywhere.
		Credentials: providers.NewMemoryCredentialStoreForTests(),
		// TEST HARNESS: the built-in MCP catalog plus one entry for the fake OAuth server above, so the connector gallery's
		// Connect runs the real sign-in against a test double.
		MCPCatalog: catalog,
	})
	if err != nil {
		return err
	}
	defer c.Close()

	// a PROTOCOL FIXTURE for the skill registries (decision P48): the scheduled sync and "Sync now" read it instead of GitHub
	reg, err := startSkillRegistryFixture()
	if err != nil {
		return err
	}
	defer reg.Close()
	if k := c.Knowledge(); k != nil {
		k.Skills.Fetcher = reg.fetcher()
	}
	fmt.Printf("DRYDOCK_E2E_SKILL_REGISTRY %s\n", reg.url)

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	s := &httpapi.Server{Core: c, Token: hex.EncodeToString(b), StaticDir: uiDir}
	addr, shutdown, err := s.Listen(port)
	if err != nil {
		return err
	}
	fmt.Printf("DRYDOCK_E2E http://%s/?token=%s\n", addr, s.Token)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	if stdinStop {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			sig <- os.Interrupt
		}()
	}
	<-sig
	if diag != "" {
		if err := writeDiagnostics(c, diag); err != nil {
			fmt.Fprintf(os.Stderr, "drydock-e2e: diagnostics: %v\n", err)
		}
	}
	return shutdown(context.Background())
}

// writeDiagnostics records, for a run that failed in CI, what the core was doing when the suite ended (every goroutine's
// stack: a turn or run that never finished shows where it waits) and what it published (the event log, oldest first, each
// event's data cut short and passed through the secret scrubber like the application log). The harness holds only test
// doubles and throwaway values, and nothing here holds its access token.
func writeDiagnostics(c *core.Core, prefix string) error {
	if dir := filepath.Dir(prefix); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	g, err := os.Create(prefix + ".goroutines.txt")
	if err != nil {
		return err
	}
	werr := pprof.Lookup("goroutine").WriteTo(g, 2)
	if cerr := g.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	f, err := os.Create(prefix + ".events.txt")
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	err = writeEvents(context.Background(), c, w)
	if ferr := w.Flush(); err == nil {
		err = ferr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// writeEvents writes the whole event log, oldest first, one line per event.
func writeEvents(ctx context.Context, c *core.Core, w io.Writer) error {
	var after uint64
	for {
		evs, err := c.Bus.Replay(ctx, after, events.Filter{}, 1000)
		if err != nil {
			return err
		}
		for _, e := range evs {
			after = e.Seq
			if _, err := fmt.Fprintf(w, "%d %s %s project=%s run=%s task=%s agent=%s %s\n", e.Seq, e.Time.Format("15:04:05.000"), e.Type, e.ProjectID, e.RunID, e.TaskID, e.AgentID, diagData(ctx, c, e)); err != nil {
				return err
			}
		}
		if len(evs) < 1000 {
			return nil
		}
	}
}

// diagData is an event's data for the diagnostics file: short (streamed agent output shorter still) and scrubbed.
func diagData(ctx context.Context, c *core.Core, e events.Event) string {
	limit := 600
	if e.Type == "agent.output" {
		limit = 160
	}
	s := string(e.Data)
	if len(s) > limit {
		s = strings.ToValidUTF8(s[:limit], "") + "..."
	}
	if c.Scanner == nil {
		return s
	}
	shown, _, err := scrub.Text(ctx, c.Scanner, s)
	if err != nil {
		return "[withheld: could not be scanned for secrets]"
	}
	return shown
}

// e2eCatalog is the built-in MCP catalog with one more card: "Hosted tracker (test double)", the fake OAuth server. It is labelled
// a test double on its card and exists only in this harness.
func e2eCatalog(oauthURL string) (*mcp.Catalog, error) {
	base, err := mcp.LoadBuiltin()
	if err != nil {
		return nil, err
	}
	cat := &mcp.Catalog{Schema: base.Schema, Servers: append([]mcp.ServerSpec(nil), base.Servers...)}
	cat.Servers = append(cat.Servers, mcp.ServerSpec{
		ID: "faketrk", Display: "Hosted tracker (test double)", Publisher: "Drydock tests", Brand: "linear",
		Summary:     "A fake hosted MCP server that wants an OAuth sign-in (internal/mcp/oauthfake).",
		Description: "TEST DOUBLE for the browser end-to-end tests: OAuth discovery, dynamic client registration, PKCE and one whoami tool.",
		Transport:   mcp.TransportHTTP, URL: oauthURL,
		Auth:          mcp.AuthSpec{Kind: mcp.AuthDCR, Keyring: "mcp/faketrk"},
		Trust:         mcp.TrustPrivateData,
		ReadOnlyTools: []string{"whoami"},
		Identity:      &mcp.IdentitySpec{Tool: "whoami"},
		License:       mcp.LicenseSpec{Server: "test-double"},
	})
	if err := cat.Validate(); err != nil {
		return nil, err
	}
	return cat, nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
