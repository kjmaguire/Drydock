package core_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

// copyProgram puts a copy of this test binary at dir/name (a stand-in tool program the person "chooses").
func copyProgram(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(dir, name)
	if err := os.Link(os.Args[0], dst); err == nil {
		return dst
	}
	in, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

// toolsCore is a core whose osv-scanner and opengrep are not installed (their configured programs do not exist); a chosen
// program keeps the configured leading arguments, so the copy of this binary a test chooses runs as the labelled stand-in.
func toolsCore(t *testing.T, pick func(context.Context, core.FilePick) (string, error)) *app {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "not-installed")
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
		ScanTools: scan.Paths{OSV: scan.Tool{Path: missing, Args: scantest.Args("osv-scanner")},
			Opengrep: scan.Tool{Path: missing, Args: scantest.Args("opengrep")}},
		PickFile: pick})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}
}

// The Security page's "Choose file...": the window's own dialog picks osv-scanner's program, the core remembers it for the
// whole workspace, the tools panel says it is the chosen one, and the next scan runs it. Cancelling the dialog changes
// nothing; "Use PATH" forgets the choice.
func TestAPersonChoosesAScanToolsProgramWithTheFileDialog(t *testing.T) {
	prog := copyProgram(t, t.TempDir(), "osv-scanner")
	var asked []core.FilePick
	answer := prog
	a := toolsCore(t, func(_ context.Context, p core.FilePick) (string, error) {
		asked = append(asked, p)
		return answer, nil
	})
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)

	var tools scan.Tools
	a.call("security.tools", map[string]any{"projectId": p.ID}, &tools)
	if tools.OSV.Found || !tools.CanChoose || tools.OSV.Install == "" {
		t.Fatalf("before: %+v", tools)
	}
	a.call("security.tool.choose", map[string]any{"projectId": p.ID, "tool": "osv-scanner"}, &tools)
	if len(asked) != 1 || asked[0].FilterPattern != "*.exe" || !strings.Contains(asked[0].Title, "osv-scanner") {
		t.Fatalf("the dialog was asked %+v", asked)
	}
	if !tools.OSV.Found || tools.OSV.Source != "chosen" || tools.OSV.Chosen != prog || !strings.Contains(tools.OSV.Version, "standin") {
		t.Fatalf("after choosing: %+v", tools.OSV)
	}
	// the choice is the workspace's: another project sees it too
	var other core.Project
	a.call("project.create", map[string]any{"name": "other", "repoPath": newRepo(t)}, &other)
	a.call("security.tools", map[string]any{"projectId": other.ID}, &tools)
	if tools.OSV.Source != "chosen" {
		t.Fatalf("another project: %+v", tools.OSV)
	}

	// the scan runs the chosen program: the dependency engine is not skipped as "not installed"
	var started scan.Report
	a.call("security.scan.start", map[string]any{"projectId": p.ID}, &started)
	var rep *scan.Report
	a.waitFor("the scan to finish", func() bool {
		a.call("security.scan.get", map[string]any{"projectId": p.ID}, &rep)
		return rep != nil && rep.Status != scan.StatusRunning
	})
	for _, e := range rep.Engines {
		if e.ID == scan.Deps && (e.Status == scan.Skipped || strings.Contains(e.Reason, "not installed")) {
			t.Fatalf("the chosen osv-scanner did not run: %+v", e)
		}
	}

	// cancelling the dialog keeps the choice
	answer = ""
	a.call("security.tool.choose", map[string]any{"projectId": p.ID, "tool": "osv-scanner"}, &tools)
	if tools.OSV.Chosen != prog {
		t.Fatalf("cancel changed the choice: %+v", tools.OSV)
	}
	// "Use PATH": an empty path forgets it
	tools = scan.Tools{}
	a.call("security.tool.set", map[string]any{"projectId": p.ID, "tool": "osv-scanner", "path": ""}, &tools)
	if tools.OSV.Found || tools.OSV.Chosen != "" {
		t.Fatalf("after forgetting: %+v", tools.OSV)
	}
}

// Typing the path works where there is no file dialog (the browser in development); what cannot be a program is refused
// with the reason, and a tool Drydock does not run cannot be set.
func TestAScanToolsProgramCanBeTypedAndBadPathsAreRefused(t *testing.T) {
	a := toolsCore(t, nil)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	ctx := context.Background()
	call := func(method, params string) error {
		_, err := a.c.Call(ctx, method, []byte(params))
		return err
	}
	var tools scan.Tools
	a.call("security.tools", map[string]any{"projectId": p.ID}, &tools)
	if tools.CanChoose {
		t.Fatal("a core without a file dialog says it can open one")
	}
	if err := call("security.tool.choose", `{"projectId":"`+p.ID+`","tool":"opengrep"}`); err == nil || !strings.Contains(err.Error(), "type the full path") {
		t.Fatal(err)
	}
	for _, c := range []struct{ params, want string }{
		{`{"projectId":"` + p.ID + `","tool":"opengrep","path":"opengrep.exe"}`, "full path"},
		{`{"projectId":"` + p.ID + `","tool":"opengrep","path":"` + filepath.ToSlash(filepath.Join(t.TempDir(), "nothing.exe")) + `"}`, "no file at"},
		{`{"projectId":"` + p.ID + `","tool":"semgrep","path":""}`, "not a scan tool"},
	} {
		if err := call("security.tool.set", c.params); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", c.params, err)
		}
	}
	if runtime.GOOS == "windows" {
		txt := filepath.Join(t.TempDir(), "opengrep.txt")
		os.WriteFile(txt, []byte("x"), 0o644)
		if err := call("security.tool.set", `{"projectId":"`+p.ID+`","tool":"opengrep","path":"`+filepath.ToSlash(txt)+`"}`); err == nil || !strings.Contains(err.Error(), "not a program") {
			t.Fatal(err)
		}
	}
	prog := copyProgram(t, t.TempDir(), "opengrep")
	// a path pasted with quotes, as Windows' "Copy as path" gives it
	a.call("security.tool.set", map[string]any{"projectId": p.ID, "tool": "opengrep", "path": `"` + prog + `"`}, &tools)
	if !tools.Opengrep.Found || tools.Opengrep.Source != "chosen" || tools.Opengrep.Chosen != prog {
		t.Fatalf("%+v", tools.Opengrep)
	}
	// a chosen program that is deleted afterwards shows as chosen but missing
	tools = scan.Tools{}
	if err := os.Remove(prog); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	a.call("security.tools", map[string]any{"projectId": p.ID}, &tools)
	if tools.Opengrep.Found || tools.Opengrep.Chosen != prog {
		t.Fatalf("%+v", tools.Opengrep)
	}
}
