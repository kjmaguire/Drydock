package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

func scheduleCore(t *testing.T, opts ...string) (*Core, *securityExt, Project) {
	t.Helper()
	ctx := context.Background()
	exe := os.Args[0]
	c, err := Open(ctx, Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		ScanTools: scan.Paths{OSV: scan.Tool{Path: exe, Args: scantest.Args("osv-scanner", opts...)}, Opengrep: scan.Tool{Path: exe, Args: scantest.Args("opengrep", opts...)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644)
	if _, err := git.Init(ctx, repo, "main"); err != nil {
		t.Fatal(err)
	}
	p, err := c.CreateProject(ctx, "gocalc", repo)
	if err != nil {
		t.Fatal(err)
	}
	return c, c.security(), p
}

func waitScans(t *testing.T, s *securityExt, projectID string, n int) []scan.Summary {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, running := s.running[projectID]
		s.mu.Unlock()
		list, err := s.summaries(context.Background(), `project_id=? ORDER BY started_at DESC, id DESC`, projectID)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) >= n && !running {
			return list
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("not %d scans", n)
	return nil
}

// H5: the weekly rule starts a scan when the project has none, then not again until a week after the last one.
func TestTheWeeklyRuleScansAWeekAfterTheLastScan(t *testing.T) {
	ctx := context.Background()
	_, s, p := scheduleCore(t)
	now := time.Now()
	s.tick(ctx, now) // no rule: nothing
	if list := waitScans(t, s, p.ID, 0); len(list) != 0 {
		t.Fatalf("%+v", list)
	}
	if _, err := s.setSchedule(ctx, p.ID, ScanRule{Weekly: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	s.tick(ctx, now)
	list := waitScans(t, s, p.ID, 1)
	if list[0].Trigger != TriggerWeekly || list[0].By != "human:kyle" {
		t.Fatalf("%+v", list)
	}
	s.tick(ctx, time.Now())
	if list = waitScans(t, s, p.ID, 1); len(list) != 1 {
		t.Fatalf("a second weekly scan within the week: %+v", list)
	}
	v, _ := s.scheduleView(ctx, p.ID)
	if v.NextWeeklyAt < list[0].StartedAt+scanWeek.Milliseconds() || v.Last == nil || v.Last.ID != list[0].ID {
		t.Fatalf("%+v", v)
	}
	s.tick(ctx, time.Now().Add(scanWeek+time.Hour))
	if list = waitScans(t, s, p.ID, 2); len(list) != 2 || list[0].Trigger != TriggerWeekly {
		t.Fatalf("%+v", list)
	}
}

// H5: a rule's scan waits while an agent works in the project's folder and starts at the next tick once it stopped; a rule
// set by no person starts nothing.
func TestARulesScanWaitsWhileAnAgentWorksInTheFolder(t *testing.T) {
	ctx := context.Background()
	c, s, p := scheduleCore(t)
	if _, err := s.setSchedule(ctx, p.ID, ScanRule{AfterMerge: true}, "agent:dana"); err == nil {
		t.Fatal("an agent set the rule")
	}
	if _, err := s.setSchedule(ctx, p.ID, ScanRule{AfterMerge: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Writer().Exec(`INSERT INTO chats(id,project_id,adapter,status,created_at,updated_at) VALUES('chat_busy',?,'claude','running',0,0)`, p.ID); err != nil {
		t.Fatal(err)
	}
	s.due(ctx, p.ID, TriggerAfterMerge)
	s.due(ctx, p.ID, TriggerWeekly) // the weekly rule is off: it changes nothing
	v, _ := s.scheduleView(ctx, p.ID)
	if v.Waiting != TriggerAfterMerge || !strings.Contains(v.WaitingWhy, "agent is working") {
		t.Fatalf("%+v", v)
	}
	if list := waitScans(t, s, p.ID, 0); len(list) != 0 {
		t.Fatalf("scanned while an agent works: %+v", list)
	}
	if _, err := c.DB.Writer().Exec(`UPDATE chats SET status='idle' WHERE id='chat_busy'`); err != nil {
		t.Fatal(err)
	}
	s.tick(ctx, time.Now())
	list := waitScans(t, s, p.ID, 1)
	if list[0].Trigger != TriggerAfterMerge {
		t.Fatalf("%+v", list)
	}
	if v, _ = s.scheduleView(ctx, p.ID); v.Waiting != "" {
		t.Fatalf("%+v", v)
	}

	// a stored rule whose setter is not a person (written by hand) is dropped, not run
	if err := c.setSetting(ctx, settingScanSchedule+p.ID, `{"afterMerge":true,"setBy":"agent:dana"}`); err != nil {
		t.Fatal(err)
	}
	s.due(ctx, p.ID, TriggerAfterMerge)
	if list = waitScans(t, s, p.ID, 1); len(list) != 1 {
		t.Fatalf("%+v", list)
	}
}

func TestNextWeeklyAndTheAgentsNoteSayTheRule(t *testing.T) {
	now := time.UnixMilli(10 * scanWeek.Milliseconds())
	if nextWeekly(0, now) != now.UnixMilli() || nextWeekly(now.UnixMilli()-scanWeek.Milliseconds()-1, now) != now.UnixMilli() {
		t.Fatal("a project never scanned or scanned over a week ago is due now")
	}
	if got := nextWeekly(now.UnixMilli()-1000, now); got != now.UnixMilli()-1000+scanWeek.Milliseconds() {
		t.Fatal(got)
	}
	if w := scheduleWords(ScanRule{}); !strings.Contains(w, "Only a person starts a scan") {
		t.Fatal(w)
	}
	if w := scheduleWords(ScanRule{AfterMerge: true, Weekly: true}); !strings.Contains(w, "after a merge, weekly") {
		t.Fatal(w)
	}
}

// H5 review: a tick while the weekly scan runs does not mark it due again (the running scan is not stored yet), and a weekly
// scan that waited is dropped when another scan started after it was marked: one scan, not two in a row.
func TestTheWeeklyRuleDoesNotRunTwiceWhileItsScanRuns(t *testing.T) {
	ctx := context.Background()
	c, s, p := scheduleCore(t, "--sleep-ms=700")
	if _, err := s.setSchedule(ctx, p.ID, ScanRule{Weekly: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	s.tick(ctx, time.Now())
	s.mu.Lock()
	_, running := s.running[p.ID]
	s.mu.Unlock()
	if !running {
		t.Fatal("the weekly scan did not start")
	}
	s.tick(ctx, time.Now()) // a minute later on a big repository: the scan still runs
	s.mu.Lock()
	waiting := s.pending[p.ID]
	s.mu.Unlock()
	if waiting != "" {
		t.Fatalf("the weekly scan was marked due while it ran: %q", waiting)
	}
	if list := waitScans(t, s, p.ID, 1); len(list) != 1 || list[0].Trigger != TriggerWeekly {
		t.Fatalf("%+v", list)
	}
	time.Sleep(200 * time.Millisecond) // a follow-up after the scan ended would have started by now
	if list := waitScans(t, s, p.ID, 1); len(list) != 1 {
		t.Fatalf("a second weekly scan: %+v", list)
	}

	// the weekly scan waits for an agent; meanwhile the person starts their own scan: the weekly one is no longer due
	if _, err := c.DB.Writer().Exec(`INSERT INTO chats(id,project_id,adapter,status,created_at,updated_at) VALUES('chat_busy',?,'claude','running',0,0)`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Writer().Exec(`UPDATE security_scans SET started_at=? WHERE project_id=?`, time.Now().Add(-scanWeek-time.Hour).UnixMilli(), p.ID); err != nil {
		t.Fatal(err)
	}
	s.tick(ctx, time.Now())
	s.mu.Lock()
	waiting = s.pending[p.ID]
	s.mu.Unlock()
	if waiting != TriggerWeekly {
		t.Fatalf("%q", waiting)
	}
	if _, err := s.startAs(ctx, scanTarget{ProjectID: p.ID}, "human:kyle", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Writer().Exec(`UPDATE chats SET status='idle' WHERE id='chat_busy'`); err != nil {
		t.Fatal(err)
	}
	list := waitScans(t, s, p.ID, 2)
	s.tick(ctx, time.Now()) // the agent stopped: a tick would start a weekly scan that is still due
	time.Sleep(200 * time.Millisecond)
	if list = waitScans(t, s, p.ID, 2); len(list) != 2 || list[0].Trigger != "" {
		t.Fatalf("the weekly scan ran after the person's: %+v", list)
	}
	s.mu.Lock()
	waiting = s.pending[p.ID]
	s.mu.Unlock()
	if waiting != "" {
		t.Fatalf("%q", waiting)
	}
}

// H5 review: a weekly scan that cannot start (the repository folder is gone) is not tried again at every tick; it tries
// again after weeklyRetry and runs once the folder is back.
func TestAWeeklyScanThatCannotStartWaitsBeforeTryingAgain(t *testing.T) {
	ctx := context.Background()
	_, s, p := scheduleCore(t)
	if _, err := s.setSchedule(ctx, p.ID, ScanRule{Weekly: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	away := p.RepoPath + "-away"
	if err := os.Rename(p.RepoPath, away); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if s.tick(ctx, now); len(waitScans(t, s, p.ID, 0)) != 0 {
		t.Fatal("scanned a missing folder")
	}
	s.mu.Lock()
	failedAt := s.weekly[p.ID].failedAt
	s.mu.Unlock()
	if failedAt == 0 {
		t.Fatal("the failure was not kept")
	}
	if err := os.Rename(away, p.RepoPath); err != nil {
		t.Fatal(err)
	}
	s.tick(ctx, now.Add(time.Minute))
	if list := waitScans(t, s, p.ID, 0); len(list) != 0 {
		t.Fatalf("tried again a minute later: %+v", list)
	}
	s.tick(ctx, time.UnixMilli(failedAt).Add(weeklyRetry+time.Minute))
	if list := waitScans(t, s, p.ID, 1); len(list) != 1 || list[0].Trigger != TriggerWeekly {
		t.Fatalf("%+v", list)
	}
	s.mu.Lock()
	failedAt = s.weekly[p.ID].failedAt
	s.mu.Unlock()
	if failedAt != 0 {
		t.Fatal("the failure outlived the scan that started")
	}
}
