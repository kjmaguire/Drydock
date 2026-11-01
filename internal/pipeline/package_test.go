package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The package a work turn starts with (decision P119): the files the task names as ranges, a ceiling on the whole package, and
// the previous session's own handoff checked before a fresh session is seeded with it. The agent is the scripted Claude stand-in
// (internal/agents/fakeagent, a labelled test double).

// commitFile adds a file to the fixture repository before the run, so the task's worktree has it.
func commitFile(t *testing.T, e *env, name, content string) {
	t.Helper()
	dir := filepath.Join(e.dir, "repo")
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", name}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "add " + name}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

// bigGo is a Go file of n lines, each about width characters, with Subtract mentioned at line mark.
func bigGo(n, width, mark int) string {
	var b strings.Builder
	b.WriteString("package gocalc\n")
	for i := 2; i <= n; i++ {
		if i == mark {
			b.WriteString("// SubtractTable is where the subtraction cases live\n")
			continue
		}
		fmt.Fprintf(&b, "// line %d %s\n", i, strings.Repeat("x", width))
	}
	return b.String()
}

func setFiles(t *testing.T, e *env, files ...string) {
	t.Helper()
	tk, err := e.tasks.Patch(context.Background(), e.task.ID, tasks.Update{Files: &files})
	if err != nil {
		t.Fatal(err)
	}
	e.task = tk
}

func manifestItems(t *testing.T, e *env, runID workflow.RunID, node string, attempt int) map[string]ctxeng.Item {
	t.Helper()
	m, err := e.deps.Context.LoadManifest(context.Background(), string(runID), node, attempt)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ctxeng.Item{}
	for _, it := range m.Items {
		out[it.Source] = it
	}
	return out
}

// A short file the task names enters whole; a long one only as the range around the line that mentions the task's identifier,
// with a read_range pointer, and never whole; a file that is not there is listed as left out. A file a failing check points at
// enters on the next pass.
func TestFilesTheTaskNamesEnterAsRangesNeverWhole(t *testing.T) {
	e := sessionEnv(t, onePassScript())
	commitFile(t, e, "table.go", bigGo(600, 40, 400))
	if _, err := e.db.Writer().Exec(`UPDATE tasks SET description=description||' See SubtractTable.' WHERE id=?`, e.task.ID); err != nil {
		t.Fatal(err)
	}
	setFiles(t, e, "calc.go", "table.go", "gone.go")
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	p := promptLog()
	if len(p) != 1 {
		t.Fatalf("turns %d", len(p))
	}
	first := p[0]
	for _, want := range []string{"File calc.go (all ", "func Subtract", "File table.go, lines 380-420 of 600", "\n400| // SubtractTable is where",
		`The rest of table.go: read_range(path "table.go", start_line, end_line).`} {
		if !strings.Contains(first, want) {
			t.Errorf("first prompt lacks %q", want)
		}
	}
	if strings.Contains(first, "// line 2 ") || strings.Contains(first, "// line 599 ") {
		t.Error("a long file entered whole")
	}
	items := manifestItems(t, e, runID, "work", 1)
	if it := items["file:table.go [380-420]"]; !it.Included || it.Kind != ctxeng.KindFileRange || !strings.Contains(it.Reason, "600 lines, more than 150") {
		t.Fatalf("table.go: %+v", it)
	}
	if it := items["file:calc.go"]; !it.Included || it.Kind != ctxeng.KindFileWhole {
		t.Fatalf("calc.go: %+v", it)
	}
	if it := items["file:gone.go"]; it.Included || !strings.Contains(it.Reason, "not a file in the worktree") {
		t.Fatalf("gone.go: %+v", it)
	}
	if it := items["budget:package"]; !strings.Contains(it.Reason, "of a package ceiling of 20000") || !strings.Contains(it.Reason, "nothing cut") {
		t.Fatalf("package: %+v", it)
	}
}

// onePassScript writes the right Subtract at once: the checks pass and the run has one work turn (a manifest is kept per attempt
// number, so a later pass would replace the first pass's).
func onePassScript() fakeagent.Script {
	return fakeagent.Script{Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "implemented Subtract"}}}}
}

// A later pass carries only what changed (token rule 16): the file the failing check points at, around the lines it names, and
// not the task's files again.
func TestALaterPassGetsOnlyTheFilesTheFailingCheckNames(t *testing.T) {
	e := sessionEnv(t, twoPassScript(10_000))
	commitFile(t, e, "table.go", bigGo(600, 40, 400))
	setFiles(t, e, "calc.go", "table.go")
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	p := promptLog()
	if len(p) != 2 || !strings.Contains(p[0], "File table.go, lines 1-40 of 600") {
		t.Fatalf("turns %d:\n%s", len(p), p[0])
	}
	if !strings.Contains(p[1], "File calc_test.go (all ") || strings.Contains(p[1], "File table.go") || strings.Contains(p[1], "File calc.go") {
		t.Fatalf("second pass:\n%s", p[1])
	}
	items := manifestItems(t, e, runID, "work", 1)
	if it := items["file:calc_test.go"]; !it.Included || it.Kind != ctxeng.KindFileWhole {
		t.Fatalf("calc_test.go: %+v", it)
	}
}

// Over the project's ceiling, file excerpts go first: the manifest lists the cut or left-out file with its reason, the prompt
// carries at most a cut excerpt whose data block is still closed, and the task and the instructions are whole.
func TestAPackageOverTheCeilingLeavesOutFilesFirstAndSaysSo(t *testing.T) {
	e := sessionEnv(t, onePassScript())
	commitFile(t, e, "wide.go", bigGo(600, 380, 0))
	commitFile(t, e, "wide2.go", bigGo(600, 120, 0))
	setFiles(t, e, "wide.go", "wide2.go")
	if err := pipeline.SaveDevSettings(context.Background(), e.db, "p1", pipeline.DevSettings{MaxParallel: 2, Respond: true, ContextCeiling: ctxeng.MinCeiling}, "human:me"); err != nil {
		t.Fatal(err)
	}
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	p := promptLog()
	if !strings.Contains(p[0], "Task: Implement Subtract") || !strings.Contains(p[0], "Given 5 and 3") || !strings.Contains(p[0], "Work in the current directory.") {
		t.Fatalf("first prompt:\n%s", p[0])
	}
	for i, pr := range p {
		if strings.Count(pr, "<<<DATA") != strings.Count(pr, "DATA>>>") {
			t.Fatalf("prompt %d has an unclosed data block:\n%s", i, pr)
		}
	}
	items := manifestItems(t, e, runID, "work", 1)
	// the later file goes first and whole; the earlier one is cut to what fits
	gone, cut := items["file:wide2.go [1-40]"], items["file:wide.go [1-40]"]
	if gone.Included || gone.Exclusion != ctxeng.ExclusionOverBudget || !strings.Contains(gone.Reason, "left out: over the package ceiling of 4000 tokens") || !strings.Contains(gone.Reason, "read_range on wide2.go") {
		t.Fatalf("wide2.go: %+v", gone)
	}
	if !cut.Included || cut.Exclusion != ctxeng.ExclusionOverBudget || !strings.Contains(cut.Reason, "cut from") {
		t.Fatalf("wide.go: %+v", cut)
	}
	if strings.Contains(p[0], "File wide2.go") || !strings.Contains(p[0], "[cut to fit the context ceiling; read_range on wide.go") {
		t.Fatalf("first prompt:\n%s", p[0])
	}
	if task := items["task"]; !task.Included || task.Exclusion != "" {
		t.Fatalf("task: %+v", task)
	}
	if pkg := items["budget:package"]; !strings.Contains(pkg.Reason, "of a package ceiling of 4000") || !strings.Contains(pkg.Reason, "2 piece(s) cut or left out") {
		t.Fatalf("package: %+v", pkg)
	}
	// the setting is bounded
	for _, bad := range []int{1, ctxeng.MinCeiling - 1, ctxeng.MaxCeiling + 1} {
		if err := pipeline.SaveDevSettings(context.Background(), e.db, "p1", pipeline.DevSettings{MaxParallel: 2, ContextCeiling: bad}, "human:me"); err == nil {
			t.Errorf("ceiling %d accepted", bad)
		}
	}
	if got := pipeline.DefaultDevSettings().PackageCeiling(); got != ctxeng.DefaultCeiling {
		t.Errorf("default ceiling %d", got)
	}
}

// agentHandoffEnv runs the two-pass script (the first turn ends at 75 %, so the second is a fresh session) with a handoff the
// first turn's agent "wrote" with the handoff tool (stored the way the tool stores it, during the turn).
func agentHandoffEnv(t *testing.T, h map[string]any) (*env, workflow.RunID, string) {
	t.Helper()
	e := sessionEnv(t, twoPassScript(150_000))
	raw, _ := json.Marshal(h)
	id := "hnd_agent0000001"
	turns := 0
	e.deps.OnTurn = func(ctx context.Context, tk tasks.Task, agentID, role, text string) {
		if turns++; turns == 1 {
			if _, err := e.db.Writer().Exec(`INSERT INTO handoffs(id,task_id,run_id,agent_id,artifact,reason,created_at) VALUES(?,?,?,?,?,?,?)`,
				id, tk.ID, "", agentID, string(raw), "agent", storage.Now()); err != nil {
				t.Error(err)
			}
		}
	}
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	return e, runID, id
}

func verification(t *testing.T, e *env, id string) map[string]any {
	t.Helper()
	var v string
	if err := e.db.Reader().QueryRow(`SELECT verification FROM handoffs WHERE id=?`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		t.Fatalf("verification %q: %v", v, err)
	}
	return m
}

// A handoff the agent wrote that passes the checks seeds the fresh session with its account of the work and its next step, next
// to Drydock's facts; the check is recorded on it.
func TestAVerifiedAgentHandoffSeedsTheFreshSession(t *testing.T) {
	e, runID, id := agentHandoffEnv(t, map[string]any{"summary": "Wrote Subtract; the sign is still wrong.", "remaining": []string{"flip the sign in Subtract"},
		"files": []string{"calc.go"}, "next_step": "change a + b to a - b in Subtract", "checks": []map[string]any{{"name": "test", "passed": false}}})
	second := promptLog()[1]
	for _, want := range []string{"The previous session's own handoff (" + id, "Wrote Subtract; the sign is still wrong.", "- flip the sign in Subtract",
		"Next step: change a + b to a - b in Subtract", "Failing checks: test (exit 1)"} {
		if !strings.Contains(second, want) {
			t.Errorf("the seeded prompt lacks %q:\n%s", want, second)
		}
	}
	if strings.Contains(second, "What the previous session said it did") {
		t.Error("an accepted handoff takes the place of the final text")
	}
	v := verification(t, e, id)
	outs := workOutputs(t, e, runID)
	if v["status"] != "accepted" || v["handoff"] != outs[1].Session.Handoff {
		t.Fatalf("verification %v", v)
	}
	if it := manifestItems(t, e, runID, "work", 1)["agent handoff:"+id]; it.Included || !strings.Contains(it.Reason, "passed Drydock's checks") {
		t.Fatalf("manifest %+v", it)
	}
	var art pipeline.HandoffArtifact
	for _, h := range handoffRows(t, e) {
		if h.id == outs[1].Session.Handoff {
			_ = json.Unmarshal([]byte(h.artifact), &art)
		}
	}
	if art.AgentHandoff == nil || !art.AgentHandoff.Accepted || art.NextStep != "change a + b to a - b in Subtract" {
		t.Fatalf("artifact %+v", art.AgentHandoff)
	}
}

// A handoff whose claims do not hold (a check it says passed failed, a file that is not there) is not used: the fresh session
// gets Drydock's handoff, and the reasons are on the agent's handoff, in Drydock's, the manifest and the agent.session event.
func TestAnAgentHandoffThatFailsItsChecksFallsBackToDrydocks(t *testing.T) {
	e, runID, id := agentHandoffEnv(t, map[string]any{"summary": "All done, tests pass.", "files": []string{"calc.go", "nothere.go"},
		"next_step": "nothing", "checks": []map[string]any{{"name": "test", "passed": true}}})
	second := promptLog()[1]
	if strings.Contains(second, "All done, tests pass.") || !strings.Contains(second, "What the previous session said it did:\nimplemented Subtract.") {
		t.Fatalf("the rejected handoff reached the session:\n%s", second)
	}
	v := verification(t, e, id)
	reasons, _ := v["reasons"].([]any)
	joined := fmt.Sprint(reasons...)
	if v["status"] != "rejected" || !strings.Contains(joined, "it says the test check passed, but its latest recorded result is that it failed") ||
		!strings.Contains(joined, "nothere.go, which is not in the working folder") {
		t.Fatalf("verification %v", v)
	}
	it := manifestItems(t, e, runID, "work", 1)["agent handoff:"+id]
	if it.Included || !strings.Contains(it.Reason, "was not used, Drydock's handoff was") || !strings.Contains(it.Reason, "nothere.go") {
		t.Fatalf("manifest %+v", it)
	}
	evs := sessionEvents(t, e)
	if evs[1]["agentHandoff"] != id || evs[1]["agentHandoffAccepted"] != false || evs[1]["agentHandoffReasons"] == nil {
		t.Fatalf("event %v", evs[1])
	}
	// the Drydock handoff carries the fields P39 did not write
	outs := workOutputs(t, e, runID)
	var art pipeline.HandoffArtifact
	for _, h := range handoffRows(t, e) {
		if h.id == outs[1].Session.Handoff {
			_ = json.Unmarshal([]byte(h.artifact), &art)
		}
	}
	if art.Goal != "Implement Subtract" || art.AcceptanceStatus != "not met yet: checks are failing" || art.WorktreeRef == nil || art.WorktreeRef.Head == "" ||
		art.WorktreeRef.SafePoint == "" || len(art.Files) != 1 || art.Files[0].Path != "calc.go" || len(art.Files[0].SHA256) != 12 ||
		!strings.HasPrefix(art.FailingCheckSignature, "test:") || len(art.CommandsOfNote) == 0 || len(art.EvidenceRefs) == 0 ||
		!strings.HasPrefix(art.NextStep, "make the failing check pass: test") || art.AgentHandoff == nil || art.AgentHandoff.Accepted {
		t.Fatalf("artifact %+v", art)
	}
}

// Each check of an agent's handoff on its own.
func TestVerifyAgentHandoff(t *testing.T) {
	facts := pipeline.HandoffFacts{
		Exists:  func(p string) bool { return p == "calc.go" || p == "README.md" },
		Changed: map[string]bool{"calc.go": true, "old.go": true},
		Checks:  map[string]bool{"test": false, "build": true},
		Redact:  func(s string) string { return strings.ReplaceAll(s, "sk-live-123", "[REDACTED]") },
	}
	ok := map[string]any{"summary": "s", "next_step": "n", "files": []string{"calc.go", "old.go"}, "checks": []map[string]any{{"name": "build", "passed": true}, {"name": "test", "passed": false}}}
	with := func(k string, v any) string {
		m := map[string]any{}
		for kk, vv := range ok {
			m[kk] = vv
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	if _, why := pipeline.VerifyAgentHandoff(with("x", nil), facts); len(why) != 0 {
		t.Fatalf("a good handoff failed: %v", why)
	}
	for _, tc := range []struct {
		raw, want string
	}{
		{"not json", "not a handoff"},
		{with("summary", nil), "no summary"},
		{with("next_step", nil), "no next step"},
		{with("files", nil), "lists no files although the working folder has changes"},
		{with("summary", strings.Repeat("a", 3001)), "summary is 3001 characters"},
		{with("summary", strings.Repeat("a", 9000)), "more than 8192"},
		{with("remaining", make([]string, 41)), "remaining list has 41 entries"},
		{with("done", []string{strings.Repeat("d", 601)}), "an entry of its done list is longer than 600"},
		{with("summary", "the key is sk-live-123"), "secret scanner found a secret"},
		{with("files", []string{"calc.go", "../outside.go"}), `"../outside.go", which is not a path`},
		{with("files", []string{"calc.go", "missing.go"}), "missing.go, which is not in the working folder and was not changed"},
		{with("checks", []map[string]any{{"name": "test", "passed": true}}), "it says the test check passed, but its latest recorded result is that it failed"},
		{with("checks", []map[string]any{{"name": "lint", "passed": true}}), "no recorded result for that check"},
	} {
		_, why := pipeline.VerifyAgentHandoff(tc.raw, facts)
		if !strings.Contains(strings.Join(why, "; "), tc.want) {
			t.Errorf("%.60s: %v, want %q", tc.raw, why, tc.want)
		}
	}
}
