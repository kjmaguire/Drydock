package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/scan"
)

// Scheduled security scans (lane B batch B7, item H5): a person sets, per project, a rule by which the project's scan starts
// without the Scan button: after a task's work is merged (task.completed; for a pull request merged on GitHub, at the next sync
// that brings new commits, since only then is the merge in the folder), after Sync now brings new commits into the working
// tree (repo.synced with the outcome "updated"), and once a week. Every rule is off by default: the scan's dependency engine
// sends package names and versions to osv.dev, which the person agrees to by turning a rule on. The rule is stored with the
// person who set it, and a scan it starts is started in that person's name (scan.Report.By) with the rule in Trigger, so "only a
// person starts a scan" (decision P44) still holds: the person started it by the rule.
//
// A rule's scan waits while an agent works in the project's folder (agentWorksIn, the rule Sync now follows) or while a scan
// of the project runs, and starts at the next tick (scheduleTick) or when that scan ends. Nothing leaves the PC but the disclosed
// osv.dev query, and a finding never makes a task. A rule's scan that finds high or critical findings the scan before did not
// have puts one item in the Inbox (scanFindingItems); a later rule's scan that still has them keeps it. Dismissing those
// findings, a scan the person starts, a later scan without them, or marking the item done clears it. The weekly rule counts a
// scan that is running as the last scan, and one whose scan could not start tries again after weeklyRetry.

const (
	settingScanSchedule = "security:schedule:"     // + project id: the rule (ScanRule as JSON)
	settingScanNew      = "security:schedule-new:" // + project id: the new high findings of the last rule's scan (newFindings)
)

// The rules a scan can start by (scan.Report.Trigger).
const (
	TriggerAfterMerge = "after-merge"
	TriggerAfterSync  = "after-sync"
	TriggerWeekly     = "weekly"
)

// scanWeek is how long after the project's last scan (of any kind) the weekly rule starts one.
const scanWeek = 7 * 24 * time.Hour

// scheduleTick is how often waiting scans and the weekly rule are looked at.
var scheduleTick = time.Minute

// weeklyRetry is how long the weekly rule waits after its scan could not start (the repository folder is gone, say) before it
// tries again, so a missing folder is not tried, and logged, at every tick.
var weeklyRetry = time.Hour

// weeklyState is the weekly rule's memory of a project (securityExt.weekly): when it last marked the scan as due, and when
// its start last failed (unix ms; 0: never).
type weeklyState struct {
	markedAt, failedAt int64
}

// ScanRule is a project's rule as stored.
type ScanRule struct {
	AfterMerge bool   `json:"afterMerge"`
	AfterSync  bool   `json:"afterSync"`
	Weekly     bool   `json:"weekly"`
	SetBy      string `json:"setBy,omitempty"`
	SetAt      int64  `json:"setAt,omitempty"`
}

func (r ScanRule) on() bool { return r.AfterMerge || r.AfterSync || r.Weekly }

func (r ScanRule) wants(trigger string) bool {
	switch trigger {
	case TriggerAfterMerge:
		return r.AfterMerge
	case TriggerAfterSync:
		return r.AfterSync
	case TriggerWeekly:
		return r.Weekly
	}
	return false
}

// ScanSchedule is security.schedule.get's answer: the rule, the rule's scan that waits (and why), the newest scan a rule
// started, and when the weekly one is next due.
type ScanSchedule struct {
	ProjectID string `json:"projectId"`
	ScanRule
	Waiting      string        `json:"waiting,omitempty"`
	WaitingWhy   string        `json:"waitingWhy,omitempty"`
	Last         *scan.Summary `json:"last,omitempty"`
	NextWeeklyAt int64         `json:"nextWeeklyAt,omitempty"`
}

func (s *securityExt) scheduleMethods() map[string]Method {
	return map[string]Method{
		"security.schedule.get": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return s.scheduleView(ctx, p.ProjectID)
		}),
		// only a person sets the rule; it is kept with their name and each scan it starts is in their name
		"security.schedule.set": bind(func(ctx context.Context, p struct {
			ProjectID  string `json:"projectId"`
			AfterMerge bool   `json:"afterMerge"`
			AfterSync  bool   `json:"afterSync"`
			Weekly     bool   `json:"weekly"`
		}) (any, error) {
			return s.setSchedule(ctx, p.ProjectID, ScanRule{AfterMerge: p.AfterMerge, AfterSync: p.AfterSync, Weekly: p.Weekly}, s.c.Human())
		}),
	}
}

// rule reads a project's rule; none stored is every rule off.
func (s *securityExt) rule(ctx context.Context, projectID string) ScanRule {
	var r ScanRule
	if v := readSetting(ctx, s.c, settingScanSchedule+projectID); v != "" {
		_ = json.Unmarshal([]byte(v), &r)
	}
	return r
}

func (s *securityExt) setSchedule(ctx context.Context, projectID string, r ScanRule, by string) (ScanSchedule, error) {
	if !isPerson(by) {
		return ScanSchedule{}, errors.New("only a person sets when the security scan runs")
	}
	if projectID == "" {
		return ScanSchedule{}, errors.New("say which project's scan this is")
	}
	if _, err := s.c.GetProject(ctx, projectID); err != nil {
		return ScanSchedule{}, err
	}
	if r.on() {
		r.SetBy, r.SetAt = by, time.Now().UnixMilli()
		b, _ := json.Marshal(r)
		if err := s.c.setSetting(ctx, settingScanSchedule+projectID, string(b)); err != nil {
			return ScanSchedule{}, err
		}
	} else if err := s.deleteSetting(ctx, settingScanSchedule+projectID); err != nil {
		return ScanSchedule{}, err
	}
	s.mu.Lock()
	if w, ok := s.pending[projectID]; ok && !r.wants(w) {
		delete(s.pending, projectID) // the rule that waited was turned off
	}
	delete(s.weekly, projectID) // a rule set again tries at once, even after a scan that could not start
	if !r.AfterMerge {
		delete(s.awaitSync, projectID)
	}
	s.mu.Unlock()
	s.c.publish(ctx, "security.schedule", projectID, map[string]any{"afterMerge": r.AfterMerge, "afterSync": r.AfterSync, "weekly": r.Weekly, "by": by})
	return s.scheduleView(ctx, projectID)
}

func (s *securityExt) deleteSetting(ctx context.Context, key string) error {
	return s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key)
		return err
	})
}

// forgetSchedule removes a deleted project's rule and its Inbox note.
func (s *securityExt) forgetSchedule(ctx context.Context, projectID string) {
	_ = s.deleteSetting(ctx, settingScanSchedule+projectID)
	_ = s.deleteSetting(ctx, settingScanNew+projectID)
}

func (s *securityExt) scheduleView(ctx context.Context, projectID string) (ScanSchedule, error) {
	if projectID == "" {
		return ScanSchedule{}, errors.New("say which project's scan this is")
	}
	v := ScanSchedule{ProjectID: projectID, ScanRule: s.rule(ctx, projectID)}
	s.mu.Lock()
	v.Waiting = s.pending[projectID]
	s.mu.Unlock()
	if v.Waiting != "" {
		v.WaitingWhy = "An agent is working in the project's folder, or a scan of it is running. It starts when that ends."
	}
	last, err := s.summaries(ctx, `project_id=? AND COALESCE(json_extract(report_json,'$.trigger'),'')<>'' ORDER BY started_at DESC, id DESC LIMIT 1`, projectID)
	if err != nil {
		return v, err
	}
	if len(last) > 0 {
		v.Last = &last[0]
	}
	if v.Weekly {
		at, err := s.lastScanAt(ctx, projectID)
		if err != nil {
			return v, err
		}
		v.NextWeeklyAt = nextWeekly(at, time.Now())
	}
	return v, nil
}

// lastScanAt is when the project's newest stored scan started (0: never scanned).
func (s *securityExt) lastScanAt(ctx context.Context, projectID string) (int64, error) {
	var at sql.NullInt64
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT MAX(started_at) FROM security_scans WHERE project_id=?`, projectID).Scan(&at)
	return at.Int64, err
}

// nextWeekly is when the weekly rule starts a scan: a week after the last scan of any kind, or now when there was none or the
// week is over.
func nextWeekly(lastAt int64, now time.Time) int64 {
	next := lastAt + scanWeek.Milliseconds()
	if lastAt == 0 || next < now.UnixMilli() {
		return now.UnixMilli()
	}
	return next
}

// watchSchedule starts the watcher of merges and syncs and the tick of waiting and weekly scans; both end with ctx.
func (s *securityExt) watchSchedule(ctx context.Context) {
	events.Watch(ctx, s.c.Bus, events.Filter{Types: []string{"task.completed", "repo.synced"}}, func(e events.Event) {
		if trigger := triggerOf(e); trigger != "" && e.ProjectID != "" {
			s.onTrigger(ctx, e, trigger)
		}
	})
	safe.Go("security.schedule", func() {
		t := time.NewTicker(scheduleTick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.tick(ctx, time.Now())
			}
		}
	})
}

// triggerOf names the rule an event may start: a task completed (its work merged), a sync that fast-forwarded the branch.
func triggerOf(e events.Event) string {
	switch e.Type {
	case "task.completed":
		return TriggerAfterMerge
	case "repo.synced":
		var d struct {
			Outcome string `json:"outcome"`
		}
		if json.Unmarshal(e.Data, &d) == nil && d.Outcome == string(git.SyncUpdated) {
			return TriggerAfterSync
		}
	}
	return ""
}

// onTrigger handles a merge or a sync. A task merged through a pull request was merged on GitHub: the project's folder does
// not have the change yet, so the rule "after a merge" waits for the next sync that brings new commits and scans then.
func (s *securityExt) onTrigger(ctx context.Context, e events.Event, trigger string) {
	pid := e.ProjectID
	switch trigger {
	case TriggerAfterMerge:
		if e.TaskID != "" && s.mergedOnGitHub(ctx, e.TaskID) {
			if s.rule(ctx, pid).AfterMerge {
				s.mu.Lock()
				s.awaitSync[pid] = true
				s.mu.Unlock()
			}
			return
		}
	case TriggerAfterSync:
		s.mu.Lock()
		merged := s.awaitSync[pid]
		delete(s.awaitSync, pid)
		s.mu.Unlock()
		if merged && s.rule(ctx, pid).AfterMerge {
			trigger = TriggerAfterMerge // the sync brought the merge in: one scan, for the merge
		}
	}
	s.due(ctx, pid, trigger)
}

// mergedOnGitHub reports whether the task's work was merged through a pull request (github.pr_merged, pipeline/prflow.go).
func (s *securityExt) mergedOnGitHub(ctx context.Context, taskID string) bool {
	var one int
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT 1 FROM events WHERE task_id=? AND type='github.pr_merged' LIMIT 1`, taskID).Scan(&one)
	return err == nil
}

// due marks the rule's scan as waiting when the project's rule wants it, then tries to start it.
func (s *securityExt) due(ctx context.Context, projectID, trigger string) {
	if !s.rule(ctx, projectID).wants(trigger) {
		return
	}
	s.mu.Lock()
	s.pending[projectID] = trigger
	s.mu.Unlock()
	s.runScheduled(ctx, projectID)
}

// tick starts the waiting scans that can start now and marks the weekly scans that are due.
func (s *securityExt) tick(ctx context.Context, now time.Time) {
	for _, pid := range s.weeklyRules(ctx) {
		at, err := s.lastScanAt(ctx, pid)
		if err != nil || nextWeekly(at, now) > now.UnixMilli() {
			continue
		}
		s.mu.Lock()
		_, running := s.running[pid] // a running scan is not in lastScanAt yet: it is the last scan
		w := s.weekly[pid]
		retry := w.failedAt != 0 && now.UnixMilli() < w.failedAt+weeklyRetry.Milliseconds()
		if _, ok := s.pending[pid]; !ok && !running && !retry {
			s.pending[pid] = TriggerWeekly
			w.markedAt = now.UnixMilli()
			s.weekly[pid] = w
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	waiting := make([]string, 0, len(s.pending))
	for pid := range s.pending {
		waiting = append(waiting, pid)
	}
	s.mu.Unlock()
	sort.Strings(waiting)
	for _, pid := range waiting {
		if ctx.Err() != nil {
			return
		}
		s.runScheduled(ctx, pid)
	}
}

// weeklyRules lists the projects whose rule has the weekly scan on.
func (s *securityExt) weeklyRules(ctx context.Context) []string {
	rows, err := s.c.DB.Reader().QueryContext(ctx, `SELECT substr(key,?), value FROM settings WHERE substr(key,1,?)=?`,
		len(settingScanSchedule)+1, len(settingScanSchedule), settingScanSchedule)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var pid, v string
		var r ScanRule
		if rows.Scan(&pid, &v) == nil && json.Unmarshal([]byte(v), &r) == nil && r.Weekly {
			out = append(out, pid)
		}
	}
	return out
}

// runScheduled starts the project's waiting scan unless an agent works in its folder or a scan of it runs; then it keeps
// waiting. A rule turned off, a person's name that is not one, or a project without its repository drops it.
func (s *securityExt) runScheduled(ctx context.Context, projectID string) bool {
	s.mu.Lock()
	trigger, ok := s.pending[projectID]
	_, running := s.running[projectID]
	s.mu.Unlock()
	if !ok || running {
		return false
	}
	drop := func() {
		s.mu.Lock()
		if s.pending[projectID] == trigger {
			delete(s.pending, projectID)
		}
		s.mu.Unlock()
	}
	// failed drops a weekly scan that could not start and holds the rule back for weeklyRetry; it says whether this is the
	// first failure in a row (only that one is logged)
	failed := func() bool {
		drop()
		if trigger != TriggerWeekly {
			return true
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		w := s.weekly[projectID]
		first := w.failedAt == 0
		w.failedAt = time.Now().UnixMilli()
		s.weekly[projectID] = w
		return first
	}
	r := s.rule(ctx, projectID)
	if !r.wants(trigger) || !isPerson(r.SetBy) {
		drop()
		return false
	}
	if trigger == TriggerWeekly {
		// a scan that started after the rule marked this one (a person's, or the weekly one itself while it ran) is the week's
		// scan: this one is no longer due
		s.mu.Lock()
		marked := s.weekly[projectID].markedAt
		s.mu.Unlock()
		if at, err := s.lastScanAt(ctx, projectID); err != nil || (marked != 0 && at >= marked) {
			drop()
			return false
		}
	}
	p, err := s.c.GetProject(ctx, projectID)
	if err != nil || p.RepoPath == "" {
		failed()
		return false
	}
	if busy, err := s.c.agentWorksIn(ctx, projectID, p.RepoPath); err != nil || busy {
		return false // waits for the next tick
	}
	if _, err := s.startAs(ctx, scanTarget{ProjectID: projectID}, r.SetBy, trigger); err != nil {
		s.mu.Lock()
		_, running = s.running[projectID]
		s.mu.Unlock()
		if running {
			return false // another scan started a moment ago: this one starts when it ends
		}
		if failed() {
			s.c.logf("security: the %s scan of %s did not start: %v", trigger, projectID, err)
		}
		return false
	}
	drop()
	if trigger == TriggerWeekly {
		s.mu.Lock()
		w := s.weekly[projectID]
		w.failedAt = 0
		s.weekly[projectID] = w
		s.mu.Unlock()
	}
	return true
}

// newFindings is what the rules' scans found that the scan before each did not: the high and critical findings not dismissed.
// A later rule's scan that still reports them, not dismissed, keeps them.
type newFindings struct {
	ScanID       string   `json:"scanId"`
	Trigger      string   `json:"trigger"`
	At           int64    `json:"at"`
	Fingerprints []string `json:"fingerprints"`
}

// noteNewFindings keeps, for the Inbox, the new high findings of a rule's scan that finished, with the ones the note had that
// this scan still reports and nobody dismissed: a merge after a merge does not clear an alert the person has not seen. A
// person's own scan (they watch its findings on the page) clears the note, and so does a rule's scan without any of them. A
// scan that did not finish leaves it.
func (s *securityExt) noteNewFindings(ctx context.Context, projectID string, rep scan.Report) {
	if rep.Status != scan.StatusDone {
		return
	}
	key := settingScanNew + projectID
	if rep.Trigger == "" {
		_ = s.deleteSetting(ctx, key)
		return
	}
	before := map[string]bool{}
	var doc string
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT report_json FROM security_scans WHERE project_id=? AND id<>? AND status=? ORDER BY started_at DESC, id DESC LIMIT 1`,
		projectID, rep.ID, scan.StatusDone).Scan(&doc)
	if err == nil {
		if prev, err := decodeReport(doc); err == nil {
			for _, f := range prev.Findings {
				before[f.Fingerprint] = true
			}
		}
	}
	var old newFindings
	if v := readSetting(ctx, s.c, key); v != "" {
		_ = json.Unmarshal([]byte(v), &old)
	}
	noted := map[string]bool{}
	for _, fp := range old.Fingerprints {
		noted[fp] = true
	}
	var kept, fresh []string
	for _, f := range rep.Findings {
		if f.Dismissed || (f.Severity != "critical" && f.Severity != "high") {
			continue
		}
		switch {
		case noted[f.Fingerprint]:
			kept = append(kept, f.Fingerprint)
		case !before[f.Fingerprint]:
			fresh = append(fresh, f.Fingerprint)
		}
	}
	// nothing new: the earlier note stays as it was (its scan, rule and Inbox key) with the findings still there
	n := newFindings{ScanID: old.ScanID, Trigger: old.Trigger, At: old.At, Fingerprints: kept}
	if len(fresh) > 0 {
		n = newFindings{ScanID: rep.ID, Trigger: rep.Trigger, At: rep.FinishedAt, Fingerprints: append(kept, fresh...)}
	}
	if len(n.Fingerprints) == 0 {
		_ = s.deleteSetting(ctx, key)
		return
	}
	b, _ := json.Marshal(n)
	if err := s.c.setSetting(ctx, key, string(b)); err != nil {
		s.c.logf("security: the new findings of %s could not be kept for the Inbox: %v", rep.ID, err)
	}
}

// triggerWords says what started a rule's scan, in the Inbox's words.
func triggerWords(trigger string) string {
	switch trigger {
	case TriggerAfterMerge:
		return "the scan after a merge"
	case TriggerAfterSync:
		return "the scan after a sync"
	case TriggerWeekly:
		return "the weekly scan"
	}
	return "a scheduled scan"
}

// scanFindingItems are the Inbox items of the rules' scans: one per project (of projectID, or all with "") whose last rule's
// scan found high or critical findings that the scan before did not have and that are not dismissed since. The words name the
// project, a count and the rule: never a finding's file, code or secret.
func (c *Core) scanFindingItems(ctx context.Context, projectID string) []room.NeedsYouItem {
	s := c.security()
	if s == nil {
		return nil
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT substr(key,?), value FROM settings WHERE substr(key,1,?)=?`,
		len(settingScanNew)+1, len(settingScanNew), settingScanNew)
	if err != nil {
		return nil
	}
	notes := map[string]newFindings{}
	for rows.Next() {
		var pid, v string
		var n newFindings
		if rows.Scan(&pid, &v) == nil && json.Unmarshal([]byte(v), &n) == nil && (projectID == "" || pid == projectID) {
			notes[pid] = n
		}
	}
	rows.Close()
	var out []room.NeedsYouItem
	for pid, n := range notes {
		p, err := c.GetProject(ctx, pid)
		if err != nil {
			continue
		}
		dismissed, err := s.dismissals(ctx, pid)
		if err != nil {
			continue
		}
		left := 0
		for _, fp := range n.Fingerprints {
			if _, ok := dismissed[fp]; !ok {
				left++
			}
		}
		if left == 0 {
			continue
		}
		what := "new high finding"
		if left > 1 {
			what = "new high findings"
		}
		out = append(out, room.NeedsYouItem{Key: "security-findings:" + pid + ":" + n.ScanID, Kind: room.NeedsSecurityFindings, Severity: 1, ProjectID: pid,
			Title:   inboxLine(fmt.Sprintf("%d %s in %s", left, what, p.Name), 160),
			Detail:  "Found by " + triggerWords(n.Trigger) + ". Open Security to read them; dismissing them, a scan you start, or a scan without them clears this.",
			Subject: n.ScanID, CreatedAt: time.UnixMilli(n.At).UTC()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// scheduleWords is how the agents' tool says a project was never scanned.
func scheduleWords(r ScanRule) string {
	if !r.on() {
		return "This project has not been scanned yet. Only a person starts a scan (the project's Security page) or sets it to run after a merge, a sync or weekly (Project settings)."
	}
	var when []string
	if r.AfterMerge {
		when = append(when, "after a merge")
	}
	if r.AfterSync {
		when = append(when, "after a sync with new commits")
	}
	if r.Weekly {
		when = append(when, "weekly")
	}
	return "This project has not been scanned yet. A person set its scan to run " + strings.Join(when, ", ") + "; none has run yet."
}
