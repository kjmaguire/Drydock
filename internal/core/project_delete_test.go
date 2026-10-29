package core_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/scan"
)

// Deleting a project: the name must be typed, work that has not ended refuses it, and then the project's rows, its task
// worktree (with an uncommitted change), its run folder and its attachments go, while the repository, the task's branch,
// the other project, the event log and the audit log stay.
func TestDeleteProjectRemovesItsWorkButNeverTheRepository(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	a := open(t, data)
	repo := newRepo(t)
	ctx := context.Background()
	db := a.c.DB.Writer()
	x := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}

	var p, other core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	a.call("project.create", map[string]any{"name": "keep me"}, &other)

	// a task with a real worktree that holds a change nobody committed, and a run that waits for the human
	x(`INSERT INTO tasks(id,project_id,seq,title,status,created_at,updated_at) VALUES('task_del1','` + p.ID + `',1,'Subtract','BACKLOG',1,1)`)
	r, err := git.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.CreateWorktree(ctx, git.WorktreeReq{TaskID: "task_del1", Title: "Subtract", Parent: r.WorktreeParent(filepath.Join(data, "worktrees"), p.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "wip.go"), []byte("package gocalc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	x(`UPDATE tasks SET worktree=?, branch=? WHERE id='task_del1'`, wt.Path, wt.Branch)
	x(`INSERT INTO workflow_runs(id,workflow,status,project_id,task_id,topology_hash,created_at,updated_at) VALUES('run_del1','iterate-until-verified','WAITING_HUMAN','` + p.ID + `','task_del1','h',1,1)`)
	runDir := filepath.Join(data, "runs", "run_del1")
	os.MkdirAll(runDir, 0o755)
	os.WriteFile(filepath.Join(runDir, "stream.jsonl"), []byte("{}\n"), 0o644)
	var roomID string
	if err := a.c.DB.Reader().QueryRow(`SELECT id FROM rooms WHERE project_id=? LIMIT 1`, p.ID).Scan(&roomID); err != nil {
		t.Fatal(err)
	}
	attDir := filepath.Join(data, "attachments", roomID, "att1")
	os.MkdirAll(attDir, 0o755)
	os.WriteFile(filepath.Join(attDir, "notes.txt"), []byte("hi"), 0o644)
	// a downloaded attachment of one of the project's integration items (decision P114)
	x(`INSERT INTO external_items(id,integration_id,assignment_id,source,project_id,external_id,kind,body_ref,content_hash,fetched_at) VALUES('item_del1','int_del1','','linear','` + p.ID + `','LIN-1','issue','','h',1)`)
	x(`INSERT INTO integration_attachments(id,item_id,integration_id,external_id,local_path,listed_at) VALUES('iat_del1','item_del1','int_del1','a1','int_del1/attachments/item_del1/iat_del1.txt',1)`)
	itemAtt := filepath.Join(data, "integrations", "int_del1", "attachments", "item_del1")
	os.MkdirAll(itemAtt, 0o755)
	os.WriteFile(filepath.Join(itemAtt, "iat_del1.txt"), []byte("hi"), 0o644)

	// the name must be typed exactly
	if _, err := a.c.DeleteProject(ctx, p.ID, "gocal"); err == nil || !strings.Contains(err.Error(), "type its name") {
		t.Fatalf("wrong name: %v", err)
	}
	// a run that has not ended refuses the delete, and says which task
	_, err = a.c.DeleteProject(ctx, p.ID, "gocalc")
	if !errors.Is(err, core.ErrProjectBusy) || !strings.Contains(err.Error(), "task #1 Subtract is waiting human") {
		t.Fatalf("busy: %v", err)
	}
	if _, err := a.c.GetProject(ctx, p.ID); err != nil {
		t.Fatalf("a refused delete removed the project: %v", err)
	}

	x(`UPDATE workflow_runs SET status='CANCELLED' WHERE id='run_del1'`)
	var out core.ProjectDeleted
	a.call("project.delete", map[string]any{"id": p.ID, "confirmName": " gocalc "}, &out)
	if out.Name != "gocalc" || out.WorktreesRemoved != 1 || len(out.Warnings) != 0 {
		t.Fatalf("result %+v", out)
	}

	var list []core.Project
	a.call("project.list", nil, &list)
	if len(list) != 1 || list[0].ID != other.ID {
		t.Fatalf("projects after delete: %+v", list)
	}
	for _, gone := range []string{wt.Path, filepath.Join(data, "worktrees", p.ID), runDir, filepath.Join(data, "attachments", roomID), itemAtt} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there (%v)", gone, err)
		}
	}
	// the repository is untouched: its files, its branch with the task's name, and git no longer lists the worktree
	if _, err := os.Stat(filepath.Join(repo, "go.mod")); err != nil {
		t.Errorf("the repository lost a file: %v", err)
	}
	if b, err := exec.Command("git", "-C", repo, "branch", "--list", wt.Branch).Output(); err != nil || !strings.Contains(string(b), wt.Branch) {
		t.Errorf("the task branch %s is gone: %q %v", wt.Branch, b, err)
	}
	if b, _ := exec.Command("git", "-C", repo, "worktree", "list").Output(); strings.Contains(string(b), wt.Path) {
		t.Errorf("git still lists the worktree:\n%s", b)
	}
	// no row of the project is left in its main tables; the history stays
	for _, q := range []string{`SELECT COUNT(*) FROM rooms WHERE project_id=?`, `SELECT COUNT(*) FROM tasks WHERE project_id=?`, `SELECT COUNT(*) FROM workflow_runs WHERE project_id=?`} {
		var n int
		a.c.DB.Reader().QueryRow(q, p.ID).Scan(&n)
		if n != 0 {
			t.Errorf("%s: %d rows", q, n)
		}
	}
	var events, audits int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM events WHERE type='project.deleted' AND project_id=?`, p.ID).Scan(&events)
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='project.delete' AND subject=?`, p.ID).Scan(&audits)
	if events != 1 || audits != 1 {
		t.Errorf("project.deleted events %d, audit entries %d", events, audits)
	}
	// deleting again is an error
	if _, err := a.c.DeleteProject(ctx, p.ID, "gocalc"); err == nil {
		t.Error("a deleted project was deleted again")
	}
}

// When the repository cannot be opened (moved, or its WSL distribution is not running), the project is still deleted: a
// worktree in Drydock's data folder goes with that folder, and one kept beside the repository (where WSL repositories keep
// theirs) stays, named in the warnings with the reason.
func TestDeleteProjectNamesTheWorktreesItCouldNotRemove(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	a := open(t, data)
	repo := newRepo(t)
	ctx := context.Background()
	var p core.Project
	a.call("project.create", map[string]any{"name": "moved", "repoPath": repo}, &p)
	inData := filepath.Join(data, "worktrees", p.ID, "task_in")
	beside := filepath.Join(filepath.Dir(repo), ".drydock-worktrees", p.ID, "task_beside")
	for _, d := range []string{inData, beside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(d, "wip.go"), []byte("package wip\n"), 0o644)
	}
	db := a.c.DB.Writer()
	for i, wt := range []string{inData, beside} {
		if _, err := db.Exec(`INSERT INTO tasks(id,project_id,seq,title,status,created_at,updated_at,worktree,branch) VALUES(?,?,?,?,'BACKLOG',1,1,?,?)`,
			filepath.Base(wt), p.ID, i+1, "t", wt, "drydock/"+filepath.Base(wt)); err != nil {
			t.Fatal(err)
		}
	}
	// a fresh core (no repository opened yet), and the repository is gone
	a.c.Close()
	a = open(t, data)
	if err := os.Rename(repo, repo+"-moved"); err != nil {
		t.Fatal(err)
	}

	out, err := a.c.DeleteProject(ctx, p.ID, "moved")
	if err != nil {
		t.Fatal(err)
	}
	if out.WorktreesRemoved != 1 || len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], beside) || !strings.Contains(out.Warnings[0], "could not be opened") {
		t.Fatalf("result %+v", out)
	}
	if _, err := os.Stat(inData); !os.IsNotExist(err) {
		t.Errorf("the worktree in the data folder is still there (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(beside, "wip.go")); err != nil {
		t.Errorf("the worktree beside the repository was touched: %v", err)
	}
	var audits int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='project.delete' AND subject=?`, p.ID).Scan(&audits)
	if audits != 1 {
		t.Errorf("audit entries %d", audits)
	}
}

// A security scan that ends after its project was deleted stores nothing: the report would outlive the project.
func TestAScanReportOfADeletedProjectIsNotStored(t *testing.T) {
	a := open(t, "")
	ctx := context.Background()
	var keep, gone core.Project
	a.call("project.create", map[string]any{"name": "keep"}, &keep)
	a.call("project.create", map[string]any{"name": "gone"}, &gone)
	if _, err := a.c.DeleteProject(ctx, gone.ID, "gone"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{keep.ID, gone.ID} {
		if err := a.c.TestStoreScanReport(ctx, scan.Report{ID: "scn_" + id, ProjectID: id, Status: scan.StatusDone, By: "human:kyle", StartedAt: 1, FinishedAt: 2}); err != nil {
			t.Fatal(err)
		}
	}
	var kept, orphans int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM security_scans WHERE project_id=?`, keep.ID).Scan(&kept)
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM security_scans WHERE project_id=?`, gone.ID).Scan(&orphans)
	if kept != 1 || orphans != 0 {
		t.Fatalf("reports of the live project %d, of the deleted one %d", kept, orphans)
	}
}
