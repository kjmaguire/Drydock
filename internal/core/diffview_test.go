package core_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The diff viewer reads a task's recorded diff as files with both versions from git, and the live worktree on request.
func TestDiffViewerShowsBothVersionsOfEveryFile(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("gocalc")
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b"}, &task)
	var none core.DiffFiles
	a.call("task.diffFiles", map[string]any{"id": task.ID}, &none)
	if none.Source != "none" || len(none.Files) != 0 || none.LiveAvailable {
		t.Fatalf("before any work: %+v", none)
	}
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var d core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return len(d.Pending) == 1
	})

	var files core.DiffFiles
	a.call("task.diffFiles", map[string]any{"id": task.ID}, &files)
	if files.Source != "recorded" || files.Base == "" || files.Head == "" || !files.LiveAvailable || len(files.Files) != 1 {
		t.Fatalf("recorded diff: %+v", files)
	}
	f := files.Files[0]
	if f.Path != "calc.go" || f.Status != "modified" || f.Additions == 0 {
		t.Fatalf("file %+v", f)
	}
	var c core.DiffFileContent
	a.call("task.diffFile", map[string]any{"id": task.ID, "path": "calc.go"}, &c)
	if c.Partial || c.Binary || c.TooLarge || !strings.Contains(c.Original, "return 0") || !strings.Contains(c.Modified, "return a - b") {
		t.Fatalf("content %+v", c)
	}

	// the live diff includes what is in the worktree now
	if err := os.WriteFile(filepath.Join(d.Task.Worktree, "notes.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var live core.DiffFiles
	a.call("task.diffFiles", map[string]any{"id": task.ID, "live": true}, &live)
	var added *core.DiffFile
	for i := range live.Files {
		if live.Files[i].Path == "notes.md" {
			added = &live.Files[i]
		}
	}
	if live.Source != "live" || added == nil || added.Status != "added" {
		t.Fatalf("live diff %+v", live)
	}
	a.call("task.diffFile", map[string]any{"id": task.ID, "path": "notes.md", "live": true}, &c)
	if c.Original != "" || c.Modified != "hello\n" {
		t.Fatalf("live content %+v", c)
	}
	for _, bad := range []string{"../../etc/passwd", "missing.go"} {
		if _, err := a.c.Call(t.Context(), "task.diffFile", mustJSON(map[string]any{"id": task.ID, "path": bad, "live": true})); err == nil {
			t.Errorf("%q was read", bad)
		}
	}
}

// A recorded diff whose commits git no longer has still opens, from the patch alone, marked partial.
func TestDiffViewerFallsBackToThePatchWhenTheCommitsAreGone(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	p, _ := a.setup("gocalc")
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Old work"}, &task)
	patch := "diff --git a/calc.go b/calc.go\n--- a/calc.go\n+++ b/calc.go\n@@ -1,2 +1,2 @@\n ctx\n-old\n+new\n"
	// the patch is a run file, kept where the readers are allowed to open evidence from (a reference elsewhere is refused)
	ref := filepath.Join(dataDir, "runs", "run_aaaaaaaaaaaa", "evidence", "task.diff")
	if err := os.MkdirAll(filepath.Dir(ref), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref, []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := strings.Repeat("ab", 20)
	if _, err := a.c.Tasks.AddEvidence(t.Context(), task.ID, tasks.Evidence{Kind: "diff", Ref: ref, Data: []byte(`{"base":"` + gone + `","commit":"` + gone + `"}`)}); err != nil {
		t.Fatal(err)
	}
	var c core.DiffFileContent
	a.call("task.diffFile", map[string]any{"id": task.ID, "path": "calc.go"}, &c)
	if !c.Partial || c.Original != "ctx\nold\n" || c.Modified != "ctx\nnew\n" {
		t.Fatalf("fallback %+v", c)
	}
}

// task.diff answers the evidence a screen asked for (audit 2026-10-16, L36): a patch recorded after the card was keyed on an earlier
// one is not shown as that one, and an id that is not a diff of the task is refused.
func TestTaskDiffAnswersTheEvidenceItWasAskedFor(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	p, _ := a.setup("gocalc")
	var task, other tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Work"}, &task)
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Another"}, &other)
	record := func(taskID, name, body string) tasks.Evidence {
		ref := filepath.Join(dataDir, "runs", "run_aaaaaaaaaaaa", "evidence", name)
		if err := os.MkdirAll(filepath.Dir(ref), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ref, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ev, err := a.c.Tasks.AddEvidence(t.Context(), taskID, tasks.Evidence{Kind: "diff", Ref: ref})
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	first := record(task.ID, "first.diff", "first patch\n")
	second := record(task.ID, "second.diff", "second patch\n")
	foreign := record(other.ID, "foreign.diff", "another task's patch\n")

	var got string
	a.call("task.diff", map[string]any{"id": task.ID}, &got)
	if got != "second patch\n" {
		t.Fatalf("the newest by default: %q", got)
	}
	a.call("task.diff", map[string]any{"id": task.ID, "evidenceId": first.ID}, &got)
	if got != "first patch\n" {
		t.Fatalf("the evidence asked for: %q", got)
	}
	a.call("task.diff", map[string]any{"id": task.ID, "evidenceId": second.ID}, &got)
	if got != "second patch\n" {
		t.Fatalf("the newest by id: %q", got)
	}
	for _, id := range []int64{foreign.ID, 999999} {
		if err := a.callErr("task.diff", map[string]any{"id": task.ID, "evidenceId": id}); err == nil {
			t.Errorf("evidence %d is not a diff of this task, and was answered", id)
		}
	}
}

// A file too large to show is reported as such without being read (audit 2026-10-16, L22): both versions used to be read into memory
// whole (and the viewer's own limit applied only afterwards). The recorded diff names real commits, one of which holds a file
// larger than the viewer's hard limit.
func TestDiffViewerReportsAHugeFileWithoutReadingIt(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	p, repo := a.setup("gocalc")
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Big file"}, &task)
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	base := run("rev-parse", "HEAD")
	const size = 25 << 20 // above the viewer's hard limit of 20 MiB
	if err := os.WriteFile(filepath.Join(repo, "big.txt"), []byte(strings.Repeat("0123456789abcdef\n", size/17+1)[:size]), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "big.txt")
	run("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "a big file")
	head := run("rev-parse", "HEAD")

	patch := "diff --git a/big.txt b/big.txt\nnew file mode 100644\n--- /dev/null\n+++ b/big.txt\n@@ -0,0 +1 @@\n+0123456789abcdef\n"
	ref := filepath.Join(dataDir, "runs", "run_aaaaaaaaaaaa", "evidence", "task.diff")
	if err := os.MkdirAll(filepath.Dir(ref), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref, []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.Tasks.AddEvidence(t.Context(), task.ID, tasks.Evidence{Kind: "diff", Ref: ref, Data: []byte(`{"base":"` + base + `","commit":"` + head + `"}`)}); err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	for _, force := range []bool{false, true} {
		runtime.GC()
		runtime.ReadMemStats(&before)
		var c core.DiffFileContent
		a.call("task.diffFile", map[string]any{"id": task.ID, "path": "big.txt", "force": force}, &c)
		runtime.ReadMemStats(&after)
		if !c.TooLarge || c.Size != size || c.Original != "" || c.Modified != "" || c.Binary || c.Partial {
			t.Fatalf("force %v: %+v", force, c)
		}
		if grown := after.TotalAlloc - before.TotalAlloc; grown > 8<<20 {
			t.Fatalf("force %v: the file was read into memory (%d bytes allocated for a %d byte file)", force, grown, size)
		}
	}
}
