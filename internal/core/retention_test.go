package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/runfiles"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Agent output older than the retention period is deleted; newer output and every other event stay.
func TestRetentionDeletesOnlyOldAgentOutput(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, Config{DataDir: t.TempDir(), User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	now := time.Now()
	old, recent := now.Add(-agentOutputRetention-time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli()
	for _, row := range []struct {
		ts  int64
		typ string
	}{{old, "agent.output"}, {old, "agent.output"}, {old, "task.completed"}, {recent, "agent.output"}} {
		if _, err := c.DB.Writer().ExecContext(ctx, `INSERT INTO events(ts,type,project_id,run_id,task_id,agent_id,data) VALUES(?,?,?,?,?,?,'{}')`, row.ts, row.typ, "p", "", "t", ""); err != nil {
			t.Fatal(err)
		}
	}
	n, err := c.pruneAgentOutput(ctx, now)
	if err != nil || n != 2 {
		t.Fatalf("deleted %d (%v), want the 2 old agent outputs", n, err)
	}
	count := func(q string) int {
		var k int
		_ = c.DB.Reader().QueryRowContext(ctx, q).Scan(&k)
		return k
	}
	if count(`SELECT COUNT(*) FROM events WHERE type='task.completed' AND task_id='t'`) != 1 || count(`SELECT COUNT(*) FROM events WHERE type='agent.output' AND task_id='t'`) != 1 {
		t.Fatal("retention deleted more than old agent output")
	}
}

// A run that ended more than 30 days ago keeps only its latest checkpoint and has its streams, logs and prompts compressed;
// after 90 days its checkpoints and folder are gone. Recent runs, running runs and step outputs are not touched, a pass does
// not redo a run, and nothing is compacted or deleted while the license is read-only.
func TestRetentionCompactsThenDeletesTheRawOutputOfEndedRuns(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c, err := Open(ctx, Config{DataDir: dir, User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	now := time.Now()
	day := 24 * time.Hour
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := c.DB.Writer().ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"node/1-1/stream.jsonl": strings.Repeat(`{"kind":"text","text":"working"}`+"\n", 500),
		"node/1-1/prompt.md": strings.Repeat("Task: implement Subtract.\n", 200), "checks/test.log": strings.Repeat("ok  gocalc\n", 300),
		"node/step-1/output.json": `{"big":true}`}
	mkRun := func(id, status string, ended time.Duration) {
		at := now.Add(-ended).UnixMilli()
		exec(`INSERT INTO workflow_runs(id,workflow,status,topology_hash,latest_checkpoint_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			id, "loop", status, "h", id+"-c3", at, at)
		for i := 1; i <= 3; i++ {
			exec(`INSERT INTO workflow_checkpoints(id,run_id,workflow,step,kind,state,created_at) VALUES(?,?,?,?,?,?,?)`,
				fmt.Sprintf("%s-c%d", id, i), id, "loop", i, "step", []byte(`{}`), at)
		}
		for rel, text := range files {
			p := filepath.Join(dir, "runs", id, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	mkRun("run_old", "COMPLETED", 40*day)
	mkRun("run_older", "FAILED", 100*day)
	mkRun("run_recent", "COMPLETED", 2*day)
	mkRun("run_live", "RUNNING", 50*day)
	checkpoints := func(id string) int {
		var n int
		_ = c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_checkpoints WHERE run_id=?`, id).Scan(&n)
		return n
	}
	exists := func(id, rel string) bool {
		_, err := os.Stat(filepath.Join(dir, "runs", id, filepath.FromSlash(rel)))
		return err == nil
	}

	// (a read-only license suspends compaction and deletion: TestLicenseStateMachineAndReadOnlyEnforcement)
	rep, err := c.RunRetention(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RunsCompacted != 1 || rep.RunsDeleted != 1 || rep.FilesCompressed != 3 || rep.BytesAfter >= rep.BytesBefore/5 {
		t.Fatalf("report %+v", rep)
	}
	// compacted: the latest checkpoint stays, raw output is compressed, the step output is as it was
	if checkpoints("run_old") != 1 || !exists("run_old", "node/1-1/stream.jsonl.gz") || exists("run_old", "node/1-1/stream.jsonl") ||
		!exists("run_old", "checks/test.log.gz") || !exists("run_old", "node/1-1/prompt.md.gz") || !exists("run_old", "node/step-1/output.json") {
		t.Fatal("run_old was not compacted as it should be")
	}
	if b, err := runfiles.ReadAll(filepath.Join(dir, "runs", "run_old", "checks", "test.log")); err != nil || string(b) != files["checks/test.log"] {
		t.Fatalf("a compacted log does not read back: %v", err)
	}
	// deleted after 90 days: no checkpoint, no folder; the run row stays
	var status string
	if checkpoints("run_older") != 0 || exists("run_older", "") || c.DB.Reader().QueryRowContext(ctx, `SELECT status FROM workflow_runs WHERE id='run_older'`).Scan(&status) != nil {
		t.Fatal("run_older was not deleted as it should be")
	}
	// a recent run and a run still going are untouched
	for _, id := range []string{"run_recent", "run_live"} {
		if checkpoints(id) != 3 || !exists(id, "node/1-1/stream.jsonl") {
			t.Fatalf("%s was touched", id)
		}
	}
	// the next pass has nothing new to do
	if rep, err := c.RunRetention(ctx, now.Add(time.Hour)); err != nil || rep.acted() {
		t.Fatalf("second pass %+v %v", rep, err)
	}
	// and the compacted run is deleted once it is 90 days old
	if rep, err := c.RunRetention(ctx, now.Add(51*day)); err != nil || rep.RunsDeleted != 1 || exists("run_old", "") || checkpoints("run_old") != 0 {
		t.Fatalf("later pass %+v %v", rep, err)
	}
}

// Recorded telemetry older than its window is deleted by the daily pass; nothing else ever deleted it (audit 2026-10-16, L5).
func TestTheRetentionPassPurgesOldTelemetry(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, Config{DataDir: t.TempDir(), User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	now := time.Now()
	old, fresh := now.Add(-40*24*time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli()
	for _, ts := range []int64{old, fresh} {
		if _, err := c.DB.Writer().Exec(`INSERT INTO telemetry_spans(trace_id,span_id,name,start_ms,end_ms) VALUES('t','s','node',?,?)`, ts, ts+5); err != nil {
			t.Fatal(err)
		}
		if _, err := c.DB.Writer().Exec(`INSERT INTO telemetry_metrics(ts,name,kind,value) VALUES(?,'m','counter',1)`, ts); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.RunRetention(ctx, now); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"telemetry_spans", "telemetry_metrics"} {
		var n int
		if err := c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s holds %d rows after the pass, want only the fresh one: %v", table, n, err)
		}
	}
}

// The deletion of an old run's folder leaves the files in its evidence folder that a task's evidence still names (audit
// 2026-10-16, L23): the task's page shows the recorded diff for as long as the task exists, and the folder's deletion took it from
// under its row. Everything else in the folder goes (the raw output is what retention is for, check logs included), and a folder
// with nothing to keep goes whole.
func TestRetentionKeepsTheFilesATasksEvidenceStillNames(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c, err := Open(ctx, Config{DataDir: dir, User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	now := time.Now()
	day := 24 * time.Hour
	p, err := c.CreateProject(ctx, "kept", "")
	if err != nil {
		t.Fatal(err)
	}
	task, err := c.Tasks.Create(ctx, tasks.CreateReq{ProjectID: p.ID, Title: "Work with evidence"})
	if err != nil {
		t.Fatal(err)
	}
	mkRun := func(id string, files map[string]string) {
		at := now.Add(-100 * day).UnixMilli()
		if _, err := c.DB.Writer().ExecContext(ctx, `INSERT INTO workflow_runs(id,workflow,status,topology_hash,latest_checkpoint_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			id, "loop", "COMPLETED", "h", "", at, at); err != nil {
			t.Fatal(err)
		}
		for rel, text := range files {
			p := filepath.Join(dir, "runs", id, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := func(id, rel string) string { return filepath.Join(dir, "runs", id, filepath.FromSlash(rel)) }
	exists := func(id, rel string) bool { _, err := os.Stat(path(id, rel)); return err == nil }
	mkRun("run_withevidence", map[string]string{"evidence/task.diff": "the patch\n", "evidence/scratch.txt": "not named by any row\n",
		"checks/test.log": "ok gocalc\n", "checks/old.log": "not named\n", "node/1-1/stream.jsonl": strings.Repeat("x", 4096)})
	if err := os.MkdirAll(path("run_withevidence", "empty/dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkRun("run_unreferenced", map[string]string{"evidence/task.diff": "a patch nobody names\n", "node/1-1/stream.jsonl": "x"})
	// an evidence file already compacted to .gz is the same file
	mkRun("run_compacted", map[string]string{"evidence/summary.txt.gz": "pretend gzip", "evidence/other.txt": "x"})
	for _, ev := range []tasks.Evidence{
		{Kind: "diff", Ref: path("run_withevidence", "evidence/task.diff")},
		{Kind: "test", Ref: path("run_withevidence", "checks/test.log")}, // raw output: retention is for these, they go
		{Kind: "check", Ref: path("run_compacted", "evidence/summary.txt")},
		{Kind: "commit", Ref: "0123456789abcdef0123456789abcdef01234567"}, // not a path
	} {
		if _, err := c.Tasks.AddEvidence(ctx, task.ID, ev); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := c.RunRetention(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RunsDeleted != 3 {
		t.Fatalf("report %+v", rep)
	}
	for _, f := range []struct {
		run, rel string
		want     bool
	}{
		{"run_withevidence", "evidence/task.diff", true}, {"run_withevidence", "checks/test.log", false},
		{"run_withevidence", "evidence/scratch.txt", false}, {"run_withevidence", "checks/old.log", false},
		{"run_withevidence", "node", false}, {"run_withevidence", "empty", false},
		{"run_unreferenced", "", false},
		{"run_compacted", "evidence/summary.txt.gz", true}, {"run_compacted", "evidence/other.txt", false},
	} {
		if got := exists(f.run, f.rel); got != f.want {
			t.Errorf("%s/%s exists %v, want %v", f.run, f.rel, got, f.want)
		}
	}
	if b, err := os.ReadFile(path("run_withevidence", "evidence/task.diff")); err != nil || string(b) != "the patch\n" {
		t.Fatalf("the kept diff: %q %v", b, err)
	}
	// the next pass has nothing left to do for them
	if rep, err := c.RunRetention(ctx, now.Add(time.Hour)); err != nil || rep.BytesDeleted != 0 {
		t.Fatalf("second pass %+v %v", rep, err)
	}
	// deleting the task takes its evidence rows; the files are the project's to remove when the project goes (project_delete)
}
