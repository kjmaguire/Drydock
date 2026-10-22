package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// What a person does with one finding on the Security page (decision P97): dismiss it (and restore it), read the code around
// it, and make a task from it. A finding is named by its scan and its fingerprint (scan.Fingerprint), which stays the same in
// later scans while its line reads the same. Dismissals are kept per project, or folder of a folder scan (security_dismissals,
// migration 00132): every report of the target that is read marks the findings dismissed now, and leaves them out of its
// counts and its summary; a finished scan stores its report so. Only a person dismisses, restores or makes a task; reading
// the code around a finding is read-only and reads the file at the time of the call, with every secret masked. Events:
// security.finding.dismissed, security.finding.restored (the fingerprint and the target, never a line of code).

func (s *securityExt) findingMethods() map[string]Method {
	c := s.c
	return map[string]Method{
		"security.finding.dismiss": bind(func(ctx context.Context, p findingDismissReq) (any, error) {
			return s.dismiss(ctx, p, c.Human())
		}),
		"security.finding.restore": bind(func(ctx context.Context, p findingDismissReq) (any, error) {
			return s.restore(ctx, p, c.Human())
		}),
		// the target's dismissed findings, newest first
		"security.finding.dismissed": bind(func(ctx context.Context, p scanTarget) (any, error) {
			t, err := s.target(ctx, p, false)
			if err != nil {
				return nil, err
			}
			return s.dismissedList(ctx, t.Key)
		}),
		"security.finding.context": bind(func(ctx context.Context, p findingRef) (any, error) {
			return s.findingContext(ctx, p)
		}),
		// the whole file of a finding (the Security page's "Open the file"): read-only, masked as the context is
		"security.file.read": bind(func(ctx context.Context, p findingRef) (any, error) {
			return s.findingFile(ctx, p)
		}),
		"security.finding.task": bind(func(ctx context.Context, p findingTaskReq) (any, error) {
			return s.findingTask(ctx, p, c.Human())
		}),
	}
}

// findingDismissReq is security.finding.dismiss and security.finding.restore: the project or folder, the finding's
// fingerprint, and (to dismiss) why.
type findingDismissReq struct {
	scanTarget
	Fingerprint string `json:"fingerprint"`
	Reason      string `json:"reason"`
}

// findingRef names a finding of a scan by its fingerprint, or by its index in the report's findings when no fingerprint is
// given (a report stored before findings had one).
type findingRef struct {
	ScanID      string `json:"scanId"`
	Fingerprint string `json:"fingerprint"`
	Index       *int   `json:"index"`
	// Around is the lines shown before and after the finding (default 2, at most 10).
	Around int `json:"around"`
}

// findingTaskReq is security.finding.task.
type findingTaskReq struct {
	ScanID      string `json:"scanId"`
	Fingerprint string `json:"fingerprint"`
	Index       *int   `json:"index"`
	// Assignee is an agent of the scan's project to give the task to; its reviewer is another agent of the team (a
	// reviewer-role one first). Empty leaves the task unassigned.
	Assignee string `json:"assignee"`
}

// FindingTask is what security.finding.task answers: the task made and the finding it names.
type FindingTask struct {
	Task    tasks.Task   `json:"task"`
	Finding scan.Finding `json:"finding"`
}

// dismissals are the target's dismissed findings by fingerprint.
func (s *securityExt) dismissals(ctx context.Context, key string) (map[string]scan.Dismissal, error) {
	list, err := s.dismissedList(ctx, key)
	if err != nil {
		return nil, err
	}
	out := make(map[string]scan.Dismissal, len(list))
	for _, d := range list {
		out[d.Fingerprint] = d
	}
	return out, nil
}

func (s *securityExt) dismissedList(ctx context.Context, key string) ([]scan.Dismissal, error) {
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT fingerprint,reason,dismissed_by,dismissed_at,scan_id,engine,rule,title,file,line FROM security_dismissals
		WHERE target_key=? ORDER BY dismissed_at DESC, fingerprint`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []scan.Dismissal{}
	for rows.Next() {
		var d scan.Dismissal
		var engine string
		if err := rows.Scan(&d.Fingerprint, &d.Reason, &d.By, &d.At, &d.ScanID, &engine, &d.Rule, &d.Title, &d.File, &d.Line); err != nil {
			return nil, err
		}
		d.Engine = scan.EngineID(engine)
		out = append(out, d)
	}
	return out, rows.Err()
}

// overlay marks a report's findings with the target's dismissals as they are now. A report that cannot read them is left as
// it was stored, and the failure is logged.
func (s *securityExt) overlay(ctx context.Context, key string, rep *scan.Report) {
	if rep == nil {
		return
	}
	d, err := s.dismissals(ctx, key)
	if err != nil {
		s.c.logf("security: the dismissed findings of %s could not be read: %v", key, err)
		return
	}
	scan.ApplyDismissals(rep, d)
}

// scanByID is a scan's report: the running one, one the database refused, or a stored one; with its target key.
func (s *securityExt) scanByID(ctx context.Context, id string) (*scan.Report, string, error) {
	s.mu.Lock()
	for key, r := range s.running {
		if rep := r.Snapshot(); rep.ID == id {
			s.mu.Unlock()
			return &rep, key, nil
		}
	}
	for key, rep := range s.unstored {
		if rep.ID == id {
			s.mu.Unlock()
			return &rep, key, nil
		}
	}
	s.mu.Unlock()
	var key, doc string
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT project_id, report_json FROM security_scans WHERE id=?`, id).Scan(&key, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("there is no scan %s", id)
	}
	if err != nil {
		return nil, "", err
	}
	rep, err := decodeReport(doc)
	return rep, key, err
}

// findingOf finds a finding of a report by its fingerprint, else by its index.
func findingOf(rep *scan.Report, fingerprint string, index *int) (scan.Finding, error) {
	if fingerprint != "" {
		for _, f := range rep.Findings {
			if f.Fingerprint == fingerprint {
				return f, nil
			}
		}
		return scan.Finding{}, fmt.Errorf("the scan %s has no finding %s", rep.ID, fingerprint)
	}
	if index == nil {
		return scan.Finding{}, errors.New("say which finding: its fingerprint, or its index in the report")
	}
	if *index < 0 || *index >= len(rep.Findings) {
		return scan.Finding{}, fmt.Errorf("the scan %s has %d findings; there is no finding %d", rep.ID, len(rep.Findings), *index)
	}
	return rep.Findings[*index], nil
}

// dismiss records that a person dismissed a finding of the target's last scan, and answers with that report as it reads now.
func (s *securityExt) dismiss(ctx context.Context, p findingDismissReq, by string) (any, error) {
	if !isPerson(by) {
		return nil, errors.New("only a person may dismiss a finding")
	}
	if !scan.ValidReason(p.Reason) {
		return nil, fmt.Errorf("say why the finding is dismissed: %q, %q or %q", scan.ReasonNotReal, scan.ReasonAcceptedRisk, scan.ReasonFalsePositive)
	}
	if !scan.ValidFingerprint(p.Fingerprint) {
		return nil, fmt.Errorf("%q is not a finding's fingerprint", p.Fingerprint)
	}
	t, err := s.target(ctx, p.scanTarget, false)
	if err != nil {
		return nil, err
	}
	rep, err := s.lastReport(ctx, t)
	if err != nil {
		return nil, err
	}
	if rep == nil {
		return nil, fmt.Errorf("this %s has not been scanned yet", t.noun())
	}
	f, err := findingOf(rep, p.Fingerprint, nil)
	if err != nil {
		return nil, fmt.Errorf("the last scan of this %s has no finding %s", t.noun(), p.Fingerprint)
	}
	now := storage.Now()
	err = s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO security_dismissals(target_key,fingerprint,reason,dismissed_by,dismissed_at,scan_id,engine,rule,title,file,line)
			VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(target_key,fingerprint) DO UPDATE SET reason=excluded.reason, dismissed_by=excluded.dismissed_by,
			dismissed_at=excluded.dismissed_at, scan_id=excluded.scan_id`,
			t.Key, p.Fingerprint, p.Reason, by, now, rep.ID, string(f.Engine), f.Rule, s.c.Scanner.Redact(f.Title), s.c.Scanner.Redact(f.File), f.Line)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish(ctx, t, "security.finding.dismissed", map[string]any{"fingerprint": p.Fingerprint, "reason": p.Reason, "by": by, "scanId": rep.ID})
	return s.current(ctx, t)
}

// restore takes a dismissal back: the finding counts again in the target's reports.
func (s *securityExt) restore(ctx context.Context, p findingDismissReq, by string) (any, error) {
	if !isPerson(by) {
		return nil, errors.New("only a person may restore a dismissed finding")
	}
	t, err := s.target(ctx, p.scanTarget, false)
	if err != nil {
		return nil, err
	}
	var n int64
	err = s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `DELETE FROM security_dismissals WHERE target_key=? AND fingerprint=?`, t.Key, p.Fingerprint)
		if err == nil {
			n, _ = r.RowsAffected()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("the finding %s of this %s is not dismissed", p.Fingerprint, t.noun())
	}
	s.publish(ctx, t, "security.finding.restored", map[string]any{"fingerprint": p.Fingerprint, "by": by})
	return s.current(ctx, t)
}

// lastReport is the target's running scan, else the one the database refused, else its last stored one (nil for none),
// without the dismissals applied.
func (s *securityExt) lastReport(ctx context.Context, t target) (*scan.Report, error) {
	s.mu.Lock()
	r, ok := s.running[t.Key]
	kept, held := s.unstored[t.Key]
	s.mu.Unlock()
	if ok {
		rep := r.Snapshot()
		return &rep, nil
	}
	if held {
		return &kept, nil
	}
	var doc string
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT report_json FROM security_scans WHERE project_id=? ORDER BY started_at DESC, id DESC LIMIT 1`, t.Key).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeReport(doc)
}

// scanDir is the folder a scan's report may be read from now: a folder scan's folder if it still passes the scan's folder
// rules (checkFolder), a project's scan's folder if it is still the project's repository.
func (s *securityExt) scanDir(ctx context.Context, rep *scan.Report) (string, error) {
	if rep.ProjectID == "" {
		return s.checkFolder(rep.Dir)
	}
	pr, err := s.c.GetProject(ctx, rep.ProjectID)
	if err != nil {
		return "", err
	}
	same := filepath.Clean(pr.RepoPath) == filepath.Clean(rep.Dir)
	if !same && pr.RepoPath != "" {
		a, errA := filepath.EvalSymlinks(pr.RepoPath)
		b, errB := filepath.EvalSymlinks(rep.Dir)
		same = errA == nil && errB == nil && withinFolder(a, b) && withinFolder(b, a)
	}
	if !same {
		return "", fmt.Errorf("the project's repository is no longer %s, the folder this scan read: scan it again", rep.Dir)
	}
	if st, err := os.Stat(rep.Dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("the repository folder %s is not there", rep.Dir)
	}
	return rep.Dir, nil
}

// findingContext is the code around a finding, read now from the folder its scan read, with every secret masked and every
// line redacted; and what the finding means and what to do.
func (s *securityExt) findingContext(ctx context.Context, p findingRef) (scan.FindingContext, error) {
	return s.readFinding(ctx, p, func(ctx context.Context, dir string, f scan.Finding) (scan.FindingContext, error) {
		return scan.ReadContext(ctx, dir, f, p.Around, s.c.Scanner.Redact)
	})
}

// findingFile is the whole file of a finding (its first scan.MaxFileLines lines), read now from the folder its scan read under
// the same folder rules, with every secret masked and every line redacted. Nothing in the folder runs and nothing is opened.
func (s *securityExt) findingFile(ctx context.Context, p findingRef) (scan.FindingContext, error) {
	return s.readFinding(ctx, p, func(ctx context.Context, dir string, f scan.Finding) (scan.FindingContext, error) {
		return scan.ReadFile(ctx, dir, f, s.c.Scanner.Redact)
	})
}

// readFinding finds a scan's finding and reads it with read, from the folder the scan read while that may still be read.
func (s *securityExt) readFinding(ctx context.Context, p findingRef, read func(context.Context, string, scan.Finding) (scan.FindingContext, error)) (scan.FindingContext, error) {
	rep, key, err := s.scanByID(ctx, p.ScanID)
	if err != nil {
		return scan.FindingContext{}, err
	}
	s.overlay(ctx, key, rep)
	f, err := findingOf(rep, p.Fingerprint, p.Index)
	if err != nil {
		return scan.FindingContext{}, err
	}
	dir, err := s.scanDir(ctx, rep)
	if err != nil {
		return scan.FindingContext{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := read(cctx, dir, f)
	out.ScanID = rep.ID
	return out, err
}

// findingTask makes a task in the scan's project that names the finding (rule, file and line, never a secret), says what to
// do, and links back to it (issueRef "scan:<scanId>:<fingerprint>"); optionally assigned. A finding has one task: a second is
// refused and names the first.
func (s *securityExt) findingTask(ctx context.Context, p findingTaskReq, by string) (FindingTask, error) {
	if !isPerson(by) {
		return FindingTask{}, errors.New("only a person may make a task from a finding")
	}
	rep, key, err := s.scanByID(ctx, p.ScanID)
	if err != nil {
		return FindingTask{}, err
	}
	if rep.ProjectID == "" {
		return FindingTask{}, errors.New("this scan is of a folder, not a project, and a task belongs to a project: make the folder a project (or attach it to one), scan it there, then make the task")
	}
	s.overlay(ctx, key, rep)
	f, err := findingOf(rep, p.Fingerprint, p.Index)
	if err != nil {
		return FindingTask{}, err
	}
	if f.Fingerprint == "" {
		return FindingTask{}, errors.New("this finding is from a scan made before findings had fingerprints: scan again, then make the task")
	}
	ref := "scan:" + rep.ID + ":" + f.Fingerprint
	var existing, existingTitle string
	err = s.c.DB.Reader().QueryRowContext(ctx, `SELECT id, title FROM tasks WHERE project_id=? AND issue_ref LIKE 'scan:%:' || ? AND status NOT IN (?,?) LIMIT 1`,
		rep.ProjectID, f.Fingerprint, string(tasks.Failed), string(tasks.Cancelled)).Scan(&existing, &existingTitle)
	if err == nil {
		return FindingTask{}, fmt.Errorf("the task %q (%s) was already made from this finding", existingTitle, existing)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return FindingTask{}, err
	}
	var reviewer string
	if p.Assignee != "" {
		if reviewer, err = s.reviewerFor(ctx, rep.ProjectID, p.Assignee); err != nil {
			return FindingTask{}, err
		}
	}
	title, desc := findingTaskText(rep, f)
	t, err := s.c.CreateTask(ctx, tasks.CreateReq{ProjectID: rep.ProjectID, Title: s.c.Scanner.Redact(title), Description: s.c.Scanner.Redact(desc), IssueRef: ref})
	if err != nil {
		return FindingTask{}, err
	}
	if p.Assignee != "" {
		if t, err = s.c.AssignTask(ctx, t.ID, p.Assignee, reviewer); err != nil {
			return FindingTask{}, fmt.Errorf("the task %s was made but could not be assigned: %w", t.ID, err)
		}
	}
	return FindingTask{Task: t, Finding: f}, nil
}

// reviewerFor checks that the assignee is on the project's team and picks another agent of it to review: a reviewer-role
// agent first, never a team lead.
func (s *securityExt) reviewerFor(ctx context.Context, projectID, assignee string) (string, error) {
	team, err := s.c.Agents(ctx, projectID)
	if err != nil {
		return "", err
	}
	found := false
	for _, a := range team {
		if a.ID == assignee {
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("agent %s is not on this project's team", assignee)
	}
	for _, pass := range []bool{true, false} {
		for _, a := range team {
			if a.ID == assignee || pipeline.IsLeadRole(a.Role) || (pass && pipeline.NormalizePlanRole(a.Role) != "reviewer") {
				continue
			}
			return a.ID, nil
		}
	}
	return "", errors.New("no other agent on the team can review the work: add one, or make the task without an assignee")
}

// findingTaskText is the task's title and description: the rule, the file and line, what it means and what to do.
func findingTaskText(rep *scan.Report, f scan.Finding) (string, string) {
	where := f.File
	if f.Line > 0 {
		where = fmt.Sprintf("%s:%d", f.File, f.Line)
		if f.EndLine > f.Line {
			where += fmt.Sprintf("-%d", f.EndLine)
		}
	}
	var title string
	switch f.Engine {
	case scan.Secrets:
		title = "Remove the secret in " + where + " and revoke it"
	case scan.Deps:
		title = fmt.Sprintf("Upgrade %s %s (known vulnerabilities)", f.Package, f.Version)
		if f.Fixed != "" {
			title = fmt.Sprintf("Upgrade %s from %s to %s (known vulnerabilities)", f.Package, f.Version, f.Fixed)
		}
	default:
		title = "Fix a security finding in " + where + ": " + f.Title
	}
	title = clip(title, 200)
	adv := scan.AdviceFor(f)
	var b strings.Builder
	engine := string(f.Engine)
	for _, e := range scan.Engines {
		if e.ID == f.Engine {
			engine = e.Name + " (" + e.Tool + ")"
		}
	}
	fmt.Fprintf(&b, "The security scan of %s found this.\n\n", time.UnixMilli(rep.StartedAt).UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "Finding: %s\nEngine: %s\nRule: %s\nSeverity: %s\n", f.Title, engine, f.Rule, f.Severity)
	if where != "" {
		fmt.Fprintf(&b, "Where: %s\n", where)
	}
	if f.Package != "" {
		fmt.Fprintf(&b, "Package: %s %s\n", f.Package, f.Version)
	}
	fmt.Fprintf(&b, "\nWhat it means: %s\n\nWhat to do:\n", adv.Meaning)
	for i, st := range adv.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, st)
	}
	if len(adv.Refs) > 0 {
		fmt.Fprintf(&b, "\nReferences: %s\n", strings.Join(adv.Refs, ", "))
	}
	fmt.Fprintf(&b, "\nMade from the finding %s of the scan %s on the Security page. The finding names a rule, a file and a line; it never carries a secret's value. Scan again when this is done: the finding should be gone.\n", f.Fingerprint, rep.ID)
	return title, b.String()
}
