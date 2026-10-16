// Command drydock-verify runs a verification bundle (built by cmd/drydock-winbundle) on the machine it is copied to:
// machine facts, the core smoke through the scripted e2e core, each bundled package's tests, and on request the real
// Claude Code tests and the UI suites. It writes drydock-verify-report.md and drydock-verify-report.json and zips them with
// the step logs into drydock-verify-<date>.zip next to itself. See README-VERIFY.txt in the bundle and
// docs/windows-runner.md. It is a verification tool, not part of the shipped application.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/kjmaguire/drydock/internal/winverify"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout)
	stop()
	if winverify.OwnConsole() { // started by a double-click: keep the window open so the result can be read
		fmt.Print("\nPress Enter to close this window.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("drydock-verify", flag.ContinueOnError)
	fs.SetOutput(stdout)
	withClaude := fs.Bool("with-claude", false, "also run the tests that use the real Claude Code CLI (spends real usage on the signed-in account, roughly one dollar)")
	withUI := fs.Bool("with-ui", false, "also run the UI suites (npm ci, npm test, npm run build, npx playwright test) from a source checkout with Node 24")
	steps := fs.String("steps", "", "run only these steps, comma-separated (an entry ending in * matches a prefix, e.g. tests:*)")
	list := fs.Bool("list", false, "list the steps and what each proves, then exit")
	out := fs.String("out", "", "folder for the results and their zip (default: the bundle folder)")
	bundle := fs.String("bundle", "", "the bundle folder (default: the folder this program is in)")
	repo := fs.String("repo", "", "the Drydock source checkout for --with-ui (default: found from the working folder)")
	keep := fs.Bool("keep", false, "keep the scratch folders (for debugging)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stdout, "drydock-verify: unexpected argument %q (flags start with --; see --help)\n", fs.Arg(0))
		return 2
	}
	var names []string
	for _, s := range strings.Split(*steps, ",") {
		if s = strings.TrimSpace(s); s != "" {
			names = append(names, s)
		}
	}
	return winverify.Verify(ctx, winverify.Options{
		Bundle: *bundle, Out: *out, WithClaude: *withClaude, WithUI: *withUI, Repo: *repo, Steps: names, Keep: *keep, List: *list,
		Args: args,
	}, stdout)
}
