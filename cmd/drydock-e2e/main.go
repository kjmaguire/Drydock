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
//	DRYDOCK_E2E http://127.0.0.1:PORT/?token=TOKEN
//
// The harness also installs an in-memory credential store (a test double) so provider keys can be entered in the e2e.
// The second line is printed last, after the server listens.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/transport/httpapi"
)

const (
	// implemented Subtract, without and with a doc comment (the second is what the worker writes after human feedback)
	implPlain = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Subtract(a, b int) int { return a - b }\n"
	implDoc   = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\n// Subtract returns a minus b.\nfunc Subtract(a, b int) int { return a - b }\n"
)

func main() {
	// Re-exec as the scripted agent: the claude adapter runs this same binary with DRYDOCK_FAKE_AGENT=1.
	if os.Getenv("DRYDOCK_FAKE_AGENT") == "1" {
		os.Exit(fakeagent.Run(os.Args[1:], os.Stdin, os.Stdout))
	}
	ui := flag.String("ui", "ui/dist", "built UI to serve")
	fixture := flag.String("fixture", "testdata/fixtures/gocalc", "fixture project copied into each fresh repository")
	repos := flag.Int("repos", 1, "number of fresh git repositories to create and print")
	port := flag.Int("port", 0, "port on 127.0.0.1 (0 = any)")
	flag.Parse()

	if err := run(*ui, *fixture, *repos, *port); err != nil {
		log.Fatal(err)
	}
}

func run(uiDir, fixture string, nRepos, port int) error {
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
			// the independent reviewer (prompt text from internal/pipeline/review.go) always ships
			{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}},
			// after a human "request changes" the worker's prompt carries "Reviewer feedback:"
			{Match: "Reviewer feedback", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: implDoc}}, {Say: "addressed the feedback: added the doc comment"}}},
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

	c, err := core.Open(context.Background(), core.Config{
		DataDir:  filepath.Join(root, "data"),
		User:     "e2e",
		LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: self}},
		// TEST DOUBLE: an in-memory credential store so the Providers page can be exercised with a fake API key. The shipped
		// app uses the Windows Credential Manager; nothing entered here is persisted anywhere.
		Credentials: providers.NewMemoryCredentialStoreForTests(),
	})
	if err != nil {
		return err
	}
	defer c.Close()

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
	<-sig
	return shutdown(context.Background())
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
