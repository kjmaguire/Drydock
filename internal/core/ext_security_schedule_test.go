package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/scan"
)

func scanFindingItems(a *app, projectID string) []room.NeedsYouItem {
	var needs []room.NeedsYouItem
	a.call("needsyou.list", map[string]string{"projectId": projectID}, &needs)
	var out []room.NeedsYouItem
	for _, it := range needs {
		if it.Kind == room.NeedsSecurityFindings {
			out = append(out, it)
		}
	}
	return out
}

// H5: a person's rule starts the project's scan after a merge (in their name, with the rule), its new high findings reach the
// Inbox once, and dismissing them clears the item. Every rule is off until the person turns it on.
func TestARuleStartsTheScanAfterAMergeAndItsNewHighFindingsReachTheInbox(t *testing.T) {
	a, _ := securityCore(t)
	repo := newRepo(t)
	os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("key = "+plantedKey+"\n"), 0o644)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	ctx := context.Background()
	merged := func() {
		a.c.Bus.Publish(ctx, events.Event{Type: "task.completed", ProjectID: p.ID, Data: json.RawMessage(`{}`)})
	}

	// off by default: a merge starts nothing
	var sch core.ScanSchedule
	a.call("security.schedule.get", map[string]any{"projectId": p.ID}, &sch)
	if sch.AfterMerge || sch.AfterSync || sch.Weekly || sch.Last != nil {
		t.Fatalf("%+v", sch)
	}
	var list []scan.Summary

	a.call("security.schedule.set", map[string]any{"projectId": p.ID, "afterMerge": true}, &sch)
	if !sch.AfterMerge || sch.SetBy != "human:kyle" || sch.SetAt == 0 {
		t.Fatalf("%+v", sch)
	}
	// a sync that changed nothing starts nothing, and neither does the sync rule while it is off
	a.c.Bus.Publish(ctx, events.Event{Type: "repo.synced", ProjectID: p.ID, Data: json.RawMessage(`{"outcome":"updated"}`)})
	merged()
	a.waitFor("the scan after the merge", func() bool {
		a.call("security.scan.list", map[string]any{"projectId": p.ID}, &list)
		return len(list) == 1 && list[0].Status == scan.StatusDone
	})
	if list[0].Trigger != core.TriggerAfterMerge || list[0].By != "human:kyle" {
		t.Fatalf("%+v", list[0])
	}
	a.call("security.schedule.get", map[string]any{"projectId": p.ID}, &sch)
	if sch.Last == nil || sch.Last.ID != list[0].ID {
		t.Fatalf("%+v", sch)
	}

	// the stand-ins find three high findings, all new: one Inbox item, in the project, with no file or secret in it
	var items []room.NeedsYouItem
	a.waitFor("the Inbox item", func() bool { items = scanFindingItems(a, p.ID); return len(items) == 1 })
	it := items[0]
	if it.ProjectID != p.ID || !strings.HasPrefix(it.Title, "3 new high findings in gocalc") || !strings.Contains(it.Detail, "after a merge") ||
		strings.Contains(it.Title+it.Detail, "notes.txt") || strings.Contains(it.Title+it.Detail, plantedKey) {
		t.Fatalf("%+v", it)
	}

	// dismissing the high findings clears it
	var rep scan.Report
	a.call("security.scan.report", map[string]any{"id": list[0].ID}, &rep)
	for _, f := range rep.Findings {
		if f.Severity == "high" || f.Severity == "critical" {
			a.call("security.finding.dismiss", map[string]any{"projectId": p.ID, "fingerprint": f.Fingerprint, "reason": "accepted_risk"}, nil)
		}
	}
	if items = scanFindingItems(a, p.ID); len(items) != 0 {
		t.Fatalf("%+v", items)
	}

	// a second rule's scan with the same findings has nothing new: no item even after the dismissals are restored
	merged()
	a.waitFor("the second scan", func() bool {
		a.call("security.scan.list", map[string]any{"projectId": p.ID}, &list)
		return len(list) == 2 && list[0].Status == scan.StatusDone
	})
	for _, f := range rep.Findings {
		if f.Severity != "high" && f.Severity != "critical" {
			continue
		}
		a.call("security.finding.restore", map[string]any{"projectId": p.ID, "fingerprint": f.Fingerprint}, nil)
	}
	if items = scanFindingItems(a, p.ID); len(items) != 0 {
		t.Fatalf("findings the scan before had are not new: %+v", items)
	}

	// turned off, a merge starts nothing again
	sch = core.ScanSchedule{}
	a.call("security.schedule.set", map[string]any{"projectId": p.ID}, &sch)
	merged()
	time.Sleep(500 * time.Millisecond) // the watcher handles the event on its own goroutine
	a.call("security.scan.list", map[string]any{"projectId": p.ID}, &list)
	if len(list) != 2 || sch.AfterMerge || sch.SetBy != "" {
		t.Fatalf("%+v %+v", list, sch)
	}
}

// H5: the sync rule starts a scan only for a sync that brought new commits.
func TestTheSyncRuleScansOnlyWhenTheSyncBroughtNewCommits(t *testing.T) {
	a, _ := securityCore(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var sch core.ScanSchedule
	a.call("security.schedule.set", map[string]any{"projectId": p.ID, "afterSync": true}, &sch)
	ctx := context.Background()
	a.c.Bus.Publish(ctx, events.Event{Type: "repo.synced", ProjectID: p.ID, Data: json.RawMessage(`{"outcome":"up_to_date"}`)})
	a.c.Bus.Publish(ctx, events.Event{Type: "task.completed", ProjectID: p.ID, Data: json.RawMessage(`{}`)})
	a.c.Bus.Publish(ctx, events.Event{Type: "repo.synced", ProjectID: p.ID, Data: json.RawMessage(`{"outcome":"updated"}`)})
	var list []scan.Summary
	a.waitFor("the scan after the sync", func() bool {
		a.call("security.scan.list", map[string]any{"projectId": p.ID}, &list)
		return len(list) == 1 && list[0].Status == scan.StatusDone
	})
	if list[0].Trigger != core.TriggerAfterSync {
		t.Fatalf("%+v", list)
	}
	// a person's own scan carries no rule, and an unknown project is refused
	if err := a.callErr("security.schedule.set", map[string]any{"projectId": "prj_none", "weekly": true}); err == nil {
		t.Fatal("a rule for no project was kept")
	}
}

// H5: turning the scan check on from the scan rules is announced, so the Verification checks panel shows it at once.
func TestSavingTheChecksIsAnnounced(t *testing.T) {
	a, _ := securityCore(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	sub, stop := a.c.Bus.Subscribe(events.Filter{Types: []string{"checks.settings_changed"}})
	defer stop()
	a.call("checks.settings.set", map[string]any{"projectId": p.ID, "secrets": true, "scan": true}, nil)
	select {
	case e := <-sub:
		if e.ProjectID != p.ID || !strings.Contains(string(e.Data), `"scan":true`) {
			t.Fatalf("%+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no checks.settings_changed")
	}
}
