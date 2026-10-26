package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// scanned makes a project with the planted key on line 3 of notes.txt and waits for its first scan.
func scanned(t *testing.T) (*app, core.Project, scan.Report) {
	t.Helper()
	a, _ := securityCore(t)
	repo := newRepo(t)
	os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("# deploy notes\nregion = eu-west-1\nkey = "+plantedKey+"\nbucket = calc-builds\nowner = ops\n"), 0o644)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	return a, p, scanOnce(a, map[string]any{"projectId": p.ID})
}

func scanOnce(a *app, target map[string]any) scan.Report {
	a.t.Helper()
	var started scan.Report
	a.call("security.scan.start", target, &started)
	var cur *scan.Report
	a.waitFor("the scan to finish", func() bool {
		a.call("security.scan.get", target, &cur)
		return cur != nil && cur.ID == started.ID && cur.Status == scan.StatusDone
	})
	// a scan reads done a moment before it is stored (changed()); the history and a dismissal read the stored ones
	a.waitFor("the scan to be stored", func() bool {
		var hist []scan.Summary
		a.call("security.scan.list", target, &hist)
		return len(hist) > 0 && hist[0].ID == started.ID
	})
	return *cur
}

func secretOf(t *testing.T, rep scan.Report) scan.Finding {
	t.Helper()
	for _, f := range rep.Findings {
		if f.Engine == scan.Secrets {
			return f
		}
	}
	t.Fatalf("no secret in %+v", rep.Findings)
	return scan.Finding{}
}

// Decision P97: a person dismisses a finding ("Not a real key"); the report shows it dismissed (who, when, why) and leaves it
// out of the counts and the summary, every later scan of the project does the same, the dismissed list shows it, and
// restoring it counts it again.
func TestADismissedFindingIsLeftOutOfTheCountsOfEveryLaterScan(t *testing.T) {
	a, p, rep := scanned(t)
	if len(rep.Findings) != 4 || rep.Counts.Total != 4 {
		t.Fatalf("%+v", rep)
	}
	for _, f := range rep.Findings {
		if !scan.ValidFingerprint(f.Fingerprint) {
			t.Fatalf("a finding without a fingerprint: %+v", f)
		}
	}
	secret := secretOf(t, rep)
	target := map[string]any{"projectId": p.ID}

	sub, stop := a.c.Bus.Subscribe(events.Filter{Types: []string{"security.finding.*"}})
	defer stop()
	for _, bad := range []map[string]any{
		{"projectId": p.ID, "fingerprint": secret.Fingerprint, "reason": "boring"},
		{"projectId": p.ID, "fingerprint": "not-a-fingerprint", "reason": "not_real"},
		{"projectId": p.ID, "fingerprint": strings.Repeat("0", 24), "reason": "not_real"},
	} {
		if err := a.callErr("security.finding.dismiss", bad); err == nil {
			t.Fatalf("%v was accepted", bad)
		}
	}
	var got scan.Report
	a.call("security.finding.dismiss", map[string]any{"projectId": p.ID, "fingerprint": secret.Fingerprint, "reason": "not_real"}, &got)
	if got.Counts.Total != 3 || got.Counts.High != 2 || got.Dismissed != 1 || !strings.Contains(got.Summary, "no secrets") || !strings.Contains(got.Summary, "1 finding you dismissed") {
		t.Fatalf("%+v %s", got.Counts, got.Summary)
	}
	d := secretOf(t, got)
	if !d.Dismissed || d.DismissedBy != "human:kyle" || d.DismissedAt == 0 || d.DismissReason != "not_real" {
		t.Fatalf("%+v", d)
	}
	select {
	case e := <-sub:
		var data map[string]any
		json.Unmarshal(e.Data, &data)
		if e.Type != "security.finding.dismissed" || e.ProjectID != p.ID || data["fingerprint"] != secret.Fingerprint || strings.Contains(string(e.Data), plantedKey) {
			t.Fatalf("%+v %s", e, e.Data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no security.finding.dismissed event")
	}
	// the report read by id, and the dismissed list
	var byID scan.Report
	a.call("security.scan.report", map[string]any{"id": rep.ID}, &byID)
	if byID.Counts.Total != 3 || !secretOf(t, byID).Dismissed {
		t.Fatalf("%+v", byID.Counts)
	}
	var list []scan.Dismissal
	a.call("security.finding.dismissed", target, &list)
	if len(list) != 1 || list[0].Fingerprint != secret.Fingerprint || list[0].Engine != scan.Secrets || list[0].File != "notes.txt" || list[0].Line != 3 || list[0].ScanID != rep.ID || list[0].By != "human:kyle" {
		t.Fatalf("%+v", list)
	}

	// a later scan: the same finding is marked and not counted, in the stored report and the history
	next := scanOnce(a, target)
	if next.ID == rep.ID || next.Counts.Total != 3 || !secretOf(t, next).Dismissed || !strings.Contains(next.Summary, "no secrets") {
		t.Fatalf("%+v %s", next.Counts, next.Summary)
	}
	var hist []scan.Summary
	a.call("security.scan.list", map[string]any{"projectId": p.ID, "limit": 5}, &hist)
	if len(hist) != 2 || hist[0].ID != next.ID || hist[0].Counts.Total != 3 {
		t.Fatalf("%+v", hist)
	}

	// restored: counted again, gone from the list; a second restore says it is not dismissed
	got = scan.Report{} // decoded fresh: a field left out at zero must not keep the last value
	a.call("security.finding.restore", map[string]any{"projectId": p.ID, "fingerprint": secret.Fingerprint}, &got)
	if got.Counts.Total != 4 || got.Dismissed != 0 || secretOf(t, got).Dismissed || !strings.Contains(got.Summary, "1 secret") {
		t.Fatalf("%+v %s", got.Counts, got.Summary)
	}
	a.call("security.finding.dismissed", target, &list)
	if len(list) != 0 {
		t.Fatalf("%+v", list)
	}
	if err := a.callErr("security.finding.restore", map[string]any{"projectId": p.ID, "fingerprint": secret.Fingerprint}); err == nil || !strings.Contains(err.Error(), "not dismissed") {
		t.Fatal(err)
	}

	// a project's dismissals go with it
	a.call("security.finding.dismiss", map[string]any{"projectId": p.ID, "fingerprint": secret.Fingerprint, "reason": "accepted_risk"}, nil)
	a.call("project.delete", map[string]any{"id": p.ID, "confirmName": p.Name}, nil)
	var n int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM security_dismissals`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d dismissals outlived the project", n)
	}
}

// The code around a finding comes from the scanned folder at the time of the call: the planted key never appears in the
// answer, the lines around it do; a report whose finding names a file outside the folder (a path out of it) is refused.
func TestTheCodeAroundAFindingNeverCarriesTheKeyAndAPathOutOfTheFolderIsRefused(t *testing.T) {
	a, _, rep := scanned(t)
	secret := secretOf(t, rep)
	var cx scan.FindingContext
	a.call("security.finding.context", map[string]any{"scanId": rep.ID, "fingerprint": secret.Fingerprint}, &cx)
	raw, _ := json.Marshal(cx)
	if strings.Contains(string(raw), plantedKey) {
		t.Fatalf("the context carries the key: %s", raw)
	}
	if cx.ScanID != rep.ID || len(cx.Lines) != 5 || cx.Lines[0].Text != "# deploy notes" || !cx.Lines[2].Hit || !cx.Lines[2].Masked || !strings.HasPrefix(cx.Lines[2].Text, "key = [REDACTED:") || cx.Lines[4].Text != "owner = ops" {
		t.Fatalf("%s", raw)
	}
	if !strings.Contains(cx.Advice.Meaning, "AWS access key") || len(cx.Advice.Steps) == 0 {
		t.Fatalf("%+v", cx.Advice)
	}
	// by index, with one line around
	idx := -1
	for i, f := range rep.Findings {
		if f.Fingerprint == secret.Fingerprint {
			idx = i
		}
	}
	a.call("security.finding.context", map[string]any{"scanId": rep.ID, "index": idx, "around": 1}, &cx)
	if len(cx.Lines) != 3 || cx.Lines[0].N != 2 {
		t.Fatalf("%+v", cx.Lines)
	}
	// a code finding: its lines, and the rule's own words
	for _, f := range rep.Findings {
		if f.Engine == scan.SAST {
			a.call("security.finding.context", map[string]any{"scanId": rep.ID, "fingerprint": f.Fingerprint}, &cx)
			if len(cx.Lines) == 0 || cx.Advice.Meaning == "" {
				t.Fatalf("%+v", cx)
			}
		}
	}
	if err := a.callErr("security.finding.context", map[string]any{"scanId": rep.ID, "fingerprint": strings.Repeat("a", 24)}); err == nil {
		t.Fatal("an unknown finding")
	}

	// the stored report is changed to name a file outside the scanned folder: the call refuses to read it
	outside := filepath.Join(filepath.Dir(rep.Dir), "outside.txt")
	os.WriteFile(outside, []byte("not the repository's\n"), 0o644)
	for _, file := range []string{"../outside.txt", filepath.ToSlash(outside)} {
		if _, err := a.c.DB.Writer().Exec(`UPDATE security_scans SET report_json=json_set(report_json, '$.findings[0].file', ?) WHERE id=?`, file, rep.ID); err != nil {
			t.Fatal(err)
		}
		if err := a.callErr("security.finding.context", map[string]any{"scanId": rep.ID, "index": 0}); err == nil || !strings.Contains(err.Error(), "not inside the scanned folder") {
			t.Fatalf("%s: %v", file, err)
		}
	}
}

// "Open the file" (decision P97): security.file.read answers the finding's whole file from its first line, the finding's line
// marked and the key masked, a dependency finding's manifest whole; a file outside the scanned folder is refused as the context is.
func TestTheWholeFileOfAFindingIsReadWithTheKeyMaskedAndStaysInTheFolder(t *testing.T) {
	a, _, rep := scanned(t)
	secret := secretOf(t, rep)
	var fx scan.FindingContext
	a.call("security.file.read", map[string]any{"scanId": rep.ID, "fingerprint": secret.Fingerprint}, &fx)
	raw, _ := json.Marshal(fx)
	if strings.Contains(string(raw), plantedKey) {
		t.Fatalf("the file carries the key: %s", raw)
	}
	if fx.ScanID != rep.ID || fx.More || len(fx.Lines) != 5 || fx.Lines[0].N != 1 || fx.Lines[0].Text != "# deploy notes" || fx.Lines[0].Hit ||
		!fx.Lines[2].Hit || !fx.Lines[2].Masked || !strings.HasPrefix(fx.Lines[2].Text, "key = [REDACTED:") || fx.Lines[4].Text != "owner = ops" {
		t.Fatalf("%s", raw)
	}
	// a dependency finding names a package in its manifest: the manifest is read whole, no line is the finding's
	for _, f := range rep.Findings {
		if f.Engine != scan.Deps {
			continue
		}
		fx = scan.FindingContext{}
		a.call("security.file.read", map[string]any{"scanId": rep.ID, "fingerprint": f.Fingerprint}, &fx)
		text := ""
		for _, l := range fx.Lines {
			text += l.Text + "\n"
			if l.Hit {
				t.Fatalf("a manifest line marked as the finding: %+v", l)
			}
		}
		if f.File != "go.mod" || len(fx.Lines) == 0 || !strings.HasPrefix(text, "module gocalc\n") || fx.Note != "" {
			t.Fatalf("%+v", fx)
		}
	}
	if err := a.callErr("security.file.read", map[string]any{"scanId": rep.ID, "fingerprint": strings.Repeat("a", 24)}); err == nil {
		t.Fatal("an unknown finding")
	}
	// the stored report names a file outside the scanned folder: refused
	outside := filepath.Join(filepath.Dir(rep.Dir), "outside.txt")
	os.WriteFile(outside, []byte("not the repository's\n"), 0o644)
	if _, err := a.c.DB.Writer().Exec(`UPDATE security_scans SET report_json=json_set(report_json, '$.findings[0].file', ?) WHERE id=?`, "../outside.txt", rep.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.callErr("security.file.read", map[string]any{"scanId": rep.ID, "index": 0}); err == nil || !strings.Contains(err.Error(), "not inside the scanned folder") {
		t.Fatal(err)
	}
}

// A task is made from a finding in the scan's project: it names the rule, the file and the line and what to do, never the
// key, links back to the finding, and can be given to an agent (with another agent to review). A finding has one task, and a
// folder scan, with no project, says why it cannot have one.
func TestATaskIsMadeFromAFindingAndLinksBackToIt(t *testing.T) {
	a, p, rep := scanned(t)
	secret := secretOf(t, rep)
	builder := a.agent(p.ID, "Builder", "builder")
	reviewer := a.agent(p.ID, "Reviewer", "reviewer")
	var out core.FindingTask
	a.call("security.finding.task", map[string]any{"scanId": rep.ID, "fingerprint": secret.Fingerprint, "assignee": builder.ID}, &out)
	tk := out.Task
	if tk.ProjectID != p.ID || tk.IssueRef != "scan:"+rep.ID+":"+secret.Fingerprint || tk.Status != tasks.Backlog || tk.Assignee != builder.ID || tk.Reviewer != reviewer.ID {
		t.Fatalf("%+v", tk)
	}
	if tk.Title != "Remove the secret in notes.txt:3 and revoke it" || !strings.Contains(tk.Description, "Rule: aws-access-token") || !strings.Contains(tk.Description, "Where: notes.txt:3") ||
		!strings.Contains(tk.Description, "What to do:\n1. ") || strings.Contains(tk.Title+tk.Description, plantedKey) {
		t.Fatalf("%q\n%s", tk.Title, tk.Description)
	}
	if err := a.callErr("security.finding.task", map[string]any{"scanId": rep.ID, "fingerprint": secret.Fingerprint}); err == nil || !strings.Contains(err.Error(), "already made from this finding") {
		t.Fatal(err)
	}
	// unassigned, for a dependency finding: the version to upgrade to is in the title
	for _, f := range rep.Findings {
		if f.Engine == scan.Deps {
			out = core.FindingTask{}
			a.call("security.finding.task", map[string]any{"scanId": rep.ID, "fingerprint": f.Fingerprint}, &out)
			if out.Task.Assignee != "" || out.Task.Title != "Upgrade golang.org/x/text from 0.3.7 to 0.3.8 (known vulnerabilities)" {
				t.Fatalf("%+v", out.Task)
			}
		}
	}
	if err := a.callErr("security.finding.task", map[string]any{"scanId": rep.ID, "index": 0, "assignee": "agt_nobody"}); err == nil || !strings.Contains(err.Error(), "not on this project's team") {
		t.Fatal(err)
	}

	// a folder scan has no project
	folder := realDir(t, t.TempDir())
	os.WriteFile(filepath.Join(folder, "notes.txt"), []byte("key = "+plantedKey+"\n"), 0o644)
	frep := scanOnce(a, map[string]any{"folder": folder})
	if err := a.callErr("security.finding.task", map[string]any{"scanId": frep.ID, "fingerprint": secretOf(t, frep).Fingerprint}); err == nil || !strings.Contains(err.Error(), "folder, not a project") {
		t.Fatal(err)
	}
	// a folder scan's finding is dismissed under the folder
	var got scan.Report
	a.call("security.finding.dismiss", map[string]any{"folder": folder, "fingerprint": secretOf(t, frep).Fingerprint, "reason": "false_positive"}, &got)
	if got.ID != frep.ID || got.Counts.Total != frep.Counts.Total-1 {
		t.Fatalf("%+v", got.Counts)
	}
	var list []scan.Dismissal
	a.call("security.finding.dismissed", map[string]any{"projectId": p.ID}, &list)
	if len(list) != 0 {
		t.Fatalf("the folder's dismissal is not the project's: %+v", list)
	}
}
