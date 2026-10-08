package acp_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
)

// layout creates  <base>/work (the session cwd) and <base>/secret.txt (outside it).
func layout(t *testing.T) (work, secret string) {
	t.Helper()
	base := t.TempDir()
	work = filepath.Join(base, "work")
	secret = filepath.Join(base, "secret.txt")
	if err := os.MkdirAll(filepath.Join(work, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "ok.txt"), []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return work, secret
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

func TestGuardAllowsInsidePaths(t *testing.T) {
	work, _ := layout(t)
	g, err := acp.NewGuardForTest(work)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for _, p := range []string{filepath.Join(work, "ok.txt"), "ok.txt", "./sub/../ok.txt"} {
		if s, err := g.Read(p, nil, nil); err != nil || s != "line1\nline2\nline3\n" {
			t.Fatalf("read %q: %q %v", p, s, err)
		}
	}
	l, n := 2, 1
	if s, err := g.Read("ok.txt", &l, &n); err != nil || s != "line2\n" {
		t.Fatalf("line/limit: %q %v", s, err)
	}
	if err := g.Write(filepath.Join(work, "sub", "deep", "new.txt"), "hello"); err != nil {
		t.Fatalf("write creating parents: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(work, "sub", "deep", "new.txt")); string(b) != "hello" {
		t.Fatalf("content %q", b)
	}
	if d, err := g.Dir("sub"); err != nil || !strings.HasSuffix(d, "sub") {
		t.Fatalf("dir %q %v", d, err)
	}
}

func TestGuardRejectsDotDotEscapes(t *testing.T) {
	work, secret := layout(t)
	g, _ := acp.NewGuardForTest(work)
	defer g.Close()
	bad := []string{
		"../secret.txt",
		"sub/../../secret.txt",
		filepath.Join(work, "..", "secret.txt"),
		filepath.Join(work, "sub", "..", "..", "secret.txt"),
		secret,
		"..",
		filepath.Dir(work),
		"",
		"ok.txt\x00../../x",
	}
	for _, p := range bad {
		if _, err := g.Read(p, nil, nil); err == nil || !errors.Is(err, acp.ErrOutsideCwd) {
			t.Errorf("read %q: err=%v (want ErrOutsideCwd)", p, err)
		}
		if err := g.Write(p, "pwned"); err == nil {
			t.Errorf("write %q succeeded", p)
		}
	}
	if b, _ := os.ReadFile(secret); string(b) != "TOP SECRET" {
		t.Fatalf("secret was modified: %q", b)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(work), "pwned")); err == nil {
		t.Fatal("file created outside the cwd")
	}
	if _, err := g.Dir("../"); err == nil {
		t.Fatal("dir escape")
	}
	// a sibling directory that merely shares the cwd as a name prefix is outside
	sib := work + "-evil"
	_ = os.MkdirAll(sib, 0o755)
	_ = os.WriteFile(filepath.Join(sib, "x"), []byte("x"), 0o644)
	if _, err := g.Read(filepath.Join(sib, "x"), nil, nil); err == nil {
		t.Fatal("prefix-sibling directory must be outside")
	}
}

func TestGuardRejectsSymlinkEscapes(t *testing.T) {
	work, secret := layout(t)
	outsideDir := filepath.Dir(secret)
	symlinkOrSkip(t, secret, filepath.Join(work, "link-file"))
	symlinkOrSkip(t, outsideDir, filepath.Join(work, "link-dir"))
	symlinkOrSkip(t, "../secret.txt", filepath.Join(work, "link-rel"))
	symlinkOrSkip(t, "ok.txt", filepath.Join(work, "link-inside")) // harmless: stays inside
	g, _ := acp.NewGuardForTest(work)
	defer g.Close()

	for _, p := range []string{"link-file", "link-dir/secret.txt", "link-rel", filepath.Join(work, "link-dir", "secret.txt")} {
		if s, err := g.Read(p, nil, nil); err == nil || !errors.Is(err, acp.ErrOutsideCwd) {
			t.Errorf("read through symlink %q returned %q, err=%v (want ErrOutsideCwd)", p, s, err)
		}
	}
	// writing through a symlink must not touch the target either
	for _, p := range []string{"link-file", "link-dir/secret.txt", "link-dir/created.txt", "link-rel"} {
		if err := g.Write(p, "pwned"); err == nil {
			t.Errorf("write through symlink %q succeeded", p)
		}
	}
	if b, _ := os.ReadFile(secret); string(b) != "TOP SECRET" {
		t.Fatalf("symlink write modified the file outside the cwd: %q", b)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "created.txt")); err == nil {
		t.Fatal("file created outside the cwd through a symlinked directory")
	}
	if _, err := g.Dir("link-dir"); err == nil {
		t.Fatal("terminal cwd through a symlink to the outside")
	}
	if s, err := g.Read("link-inside", nil, nil); err != nil || !strings.HasPrefix(s, "line1") {
		t.Fatalf("an inside symlink should work: %q %v", s, err)
	}
}

func TestGuardSymlinkedSessionCwdStillWorks(t *testing.T) {
	work, _ := layout(t)
	link := filepath.Join(filepath.Dir(work), "work-link")
	symlinkOrSkip(t, work, link)
	g, err := acp.NewGuardForTest(link)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	// the agent may name the file by the lexical path or by the resolved one
	for _, p := range []string{filepath.Join(link, "ok.txt"), filepath.Join(work, "ok.txt"), "ok.txt"} {
		if _, err := g.Read(p, nil, nil); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
}

func TestGuardRefusesWritesIntoGitDir(t *testing.T) {
	work, _ := layout(t)
	_ = os.MkdirAll(filepath.Join(work, ".git", "hooks"), 0o755)
	g, _ := acp.NewGuardForTest(work)
	defer g.Close()
	for _, p := range []string{".git/hooks/pre-commit", ".git/config", ".GIT/config", "sub/.git/x"} {
		if err := g.Write(p, "#!/bin/sh\nrm -rf /\n"); err == nil {
			t.Errorf("write into %q succeeded", p)
		}
	}
	if _, err := os.Stat(filepath.Join(work, ".git", "hooks", "pre-commit")); err == nil {
		t.Fatal("hook written")
	}
}

func TestGuardLimits(t *testing.T) {
	work, _ := layout(t)
	g, _ := acp.NewGuardForTest(work)
	defer g.Close()
	if err := g.Write("big.txt", strings.Repeat("x", acp.MaxWriteBytes+1)); err == nil {
		t.Fatal("oversized write")
	}
	if err := os.WriteFile(filepath.Join(work, "huge.txt"), make([]byte, acp.MaxReadBytes+10), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Read("huge.txt", nil, nil); err == nil {
		t.Fatal("oversized read")
	}
	if _, err := g.Read("sub", nil, nil); err == nil {
		t.Fatal("directory read")
	}
	if err := g.Write(".", "x"); err == nil {
		t.Fatal("write to the cwd itself")
	}
	if runtime.GOOS == "windows" {
		if _, err := g.Read("ok.txt:stream", nil, nil); err == nil {
			t.Fatal("alternate data stream")
		}
	}
}

// The same boundary, reached the way an agent reaches it: fs/read_text_file and fs/write_text_file requests over ACP.
func TestAgentFSRequestsAreConfined(t *testing.T) {
	work, secret := layout(t)
	steps := []fakeacp.Step{
		{FSRead: &fakeacp.FSOp{Path: filepath.Join(work, "ok.txt")}},
		{FSRead: &fakeacp.FSOp{Path: "../secret.txt"}},
		{FSRead: &fakeacp.FSOp{Path: secret}},
		{FSWrite: &fakeacp.FSOp{Path: "../planted.txt", Content: "x"}},
		{FSWrite: &fakeacp.FSOp{Path: filepath.Join(work, "inside.txt"), Content: "fine"}},
		{FSWrite: &fakeacp.FSOp{Path: ".git/hooks/post-checkout", Content: "x"}},
	}
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{steps}})
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a", Cwd: work}), "go")
	got := text(cs)
	for _, want := range []string{"fs.read OK line1", "fs.write OK"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if n := strings.Count(got, "fs.read ERR"); n != 2 {
		t.Fatalf("want 2 rejected reads, got %d in %q", n, got)
	}
	if n := strings.Count(got, "fs.write ERR"); n != 2 {
		t.Fatalf("want 2 rejected writes, got %d in %q", n, got)
	}
	if strings.Contains(got, "TOP SECRET") {
		t.Fatalf("content outside the cwd was returned to the agent: %q", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(work), "planted.txt")); err == nil {
		t.Fatal("agent wrote outside the cwd")
	}
	if b, _ := os.ReadFile(filepath.Join(work, "inside.txt")); string(b) != "fine" {
		t.Fatalf("inside write: %q", b)
	}
}

func TestFSCanBeDisabled(t *testing.T) {
	work, _ := layout(t)
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{FSRead: &fakeacp.FSOp{Path: "ok.txt"}}}}}, func(c *acp.Config) { c.NoFS = true })
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a", Cwd: work}), "go")
	if !strings.Contains(text(cs), "fs.read ERR") {
		t.Fatalf("fs served although disabled: %q", text(cs))
	}
}

func TestAgentTerminalIsOffByDefaultAndConfinedWhenOn(t *testing.T) {
	work, _ := layout(t)
	steps := []fakeacp.Step{
		{Term: &fakeacp.TermOp{Command: "go", Args: []string{"version"}}},
		{Term: &fakeacp.TermOp{Command: "go", Args: []string{"version"}, Cwd: "sub"}},
		{Term: &fakeacp.TermOp{Command: "go", Args: []string{"version"}, Cwd: ".."}},
		{Term: &fakeacp.TermOp{Command: "go", Args: []string{"version"}, Cwd: filepath.Dir(work)}},
	}
	off := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{steps}})
	cs := turn(t, startSession(t, off, agents.RunSpec{AgentID: "a", Cwd: work}), "go")
	if n := strings.Count(text(cs), "term.create ERR"); n != 4 || strings.Contains(text(cs), "term OK") {
		t.Fatalf("terminals must be off by default: %q", text(cs))
	}

	on := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{steps}}, func(c *acp.Config) { c.Terminal = true })
	cs = turn(t, startSession(t, on, agents.RunSpec{AgentID: "a", Cwd: work}), "go")
	got := text(cs)
	if n := strings.Count(got, "term OK exit=0 out=go version"); n != 2 {
		t.Fatalf("want 2 confined terminals to run, got %d in %q", n, got)
	}
	if n := strings.Count(got, "term.create ERR"); n != 2 || !strings.Contains(got, "outside the session working directory") {
		t.Fatalf("want 2 escaping cwds rejected, got %d in %q", n, got)
	}
}
