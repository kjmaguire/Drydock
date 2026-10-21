// Command drydock-winbundle builds the verification bundle that cmd/drydock-verify runs: the test binaries of every
// package with Windows-specific code (plus storage, the Claude adapter, CLI detection, git worktrees, process control and
// the packages with real-Claude tests), cross-compiled without CGO and laid out in their package folders with the testdata
// they read, the scripted e2e core, drydock-verify itself, optionally the Go toolchain, a README and bundle.json, zipped.
//
//	go run ./cmd/drydock-winbundle -goos windows -out dist-windows/verify    (writes dist-windows/drydock-windows-verify.zip)
//	go run ./cmd/drydock-winbundle -goos linux -out dist-windows/linux       (the same bundle for the development host)
//
// It runs on Linux or Windows from a source checkout. dist-windows/ is git-ignored; bundles are never committed.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/kjmaguire/drydock/internal/winverify"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("drydock-winbundle", flag.ContinueOnError)
	fl.SetOutput(stderr)
	goos := fl.String("goos", "windows", "target system")
	goarch := fl.String("goarch", "amd64", "target architecture")
	out := fl.String("out", filepath.Join("dist-windows", "verify"), "bundle folder (replaced if it holds an earlier bundle)")
	zipPath := fl.String("zip", "", `zip to write (default: drydock-<goos>-verify.zip next to -out; "-" for none)`)
	withGo := fl.Bool("with-go", true, "copy the Go toolchain into the bundle so the tests that run go work on a machine without Go (about 75 MB zipped)")
	jobs := fl.Int("j", 2, "test binaries linked at once")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	m, err := winverify.Build(ctx, winverify.BuildOptions{GOOS: *goos, GOARCH: *goarch, Out: *out, Zip: *zipPath, Jobs: *jobs, WithGo: *withGo, Log: stderr})
	if err != nil {
		fmt.Fprintln(stderr, "drydock-winbundle:", err)
		return 1
	}
	fmt.Fprintf(stdout, "bundle for %s/%s from commit %s: %d test binaries, %s in %s\n", m.GOOS, m.GOARCH, m.Commit, len(m.Packages), size(dirSize(*out)), *out)
	for _, p := range m.Packages {
		fmt.Fprintf(stdout, "  %-32s %s\n", p.Path, size(fileSize(filepath.Join(*out, filepath.FromSlash(p.Binary)))))
	}
	if len(m.Untested) > 0 {
		fmt.Fprintf(stdout, "  Windows-specific but without tests (compiled, not run): %v\n", m.Untested)
	}
	if z := *zipPath; z != "-" {
		if z == "" {
			z = filepath.Join(filepath.Dir(filepath.Clean(*out)), "drydock-"+m.GOOS+"-verify.zip")
		}
		fmt.Fprintf(stdout, "zip: %s (%s)\n", z, size(fileSize(z)))
	}
	return 0
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func size(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }
