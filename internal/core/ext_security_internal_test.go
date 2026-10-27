package core

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/scan"
)

// Only a person starts a scan, of a project or of a folder (decision P61).
func TestOnlyAPersonStartsAScan(t *testing.T) {
	s := &securityExt{}
	for _, by := range []string{"", "human:", "agent:bot", "system"} {
		for _, p := range []scanTarget{{Folder: "C:/code/shop"}, {ProjectID: "prj"}} {
			if _, err := s.start(context.Background(), p, by); err == nil || !strings.Contains(err.Error(), "only a person") {
				t.Fatalf("%q %+v: %v", by, p, err)
			}
		}
	}
}

// Only a person dismisses a finding, restores one, or makes a task from one (decision P97); refused before anything is read.
func TestOnlyAPersonDismissesRestoresOrMakesATaskFromAFinding(t *testing.T) {
	s := &securityExt{}
	req := findingDismissReq{scanTarget: scanTarget{ProjectID: "prj"}, Fingerprint: strings.Repeat("a", 24), Reason: scan.ReasonNotReal}
	for _, by := range []string{"", "human:", "agent:bot", "system"} {
		if _, err := s.dismiss(context.Background(), req, by); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("dismiss %q: %v", by, err)
		}
		if _, err := s.restore(context.Background(), req, by); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("restore %q: %v", by, err)
		}
		if _, err := s.findingTask(context.Background(), findingTaskReq{ScanID: "scn", Fingerprint: req.Fingerprint}, by); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("task %q: %v", by, err)
		}
	}
}

// A folder scan that has just finished is stored a moment before it leaves the running scans: the recent folder scans list it
// once, as its stored row (with its end and summary), not twice (seen on the Windows CI, 2026-10-21: the scan's ID twice).
func TestAFolderScanThatJustFinishedIsListedOnceAsItsStoredRow(t *testing.T) {
	running := []scan.Summary{
		{ID: "scn_older_running", Folder: "C:/a", Status: scan.StatusRunning, StartedAt: 100},
		{ID: "scn_just_done", Folder: "C:/b", Status: scan.StatusDone, StartedAt: 300},
		{ID: "scn_newer_running", Folder: "C:/c", Status: scan.StatusRunning, StartedAt: 200},
	}
	stored := []scan.Summary{
		{ID: "scn_just_done", Folder: "C:/b", Status: scan.StatusDone, StartedAt: 300, FinishedAt: 350, Summary: "Scanned in 50 ms: no secrets."},
		{ID: "scn_earlier", Folder: "C:/a", Status: scan.StatusDone, StartedAt: 50, FinishedAt: 60},
	}
	got := mergeFolderScans(running, stored, 10)
	var ids []string
	for _, sm := range got {
		ids = append(ids, sm.ID)
	}
	if strings.Join(ids, ",") != "scn_newer_running,scn_older_running,scn_just_done,scn_earlier" {
		t.Fatalf("%v", ids)
	}
	if got[2].FinishedAt != 350 || got[2].Summary == "" {
		t.Fatalf("the stored row is the one shown: %+v", got[2])
	}
	if got := mergeFolderScans(running, stored, 2); len(got) != 2 || got[0].ID != "scn_newer_running" {
		t.Fatalf("limit: %+v", got)
	}
}

// A report the database refused is kept, and every read gets its own copy: the dismissals a read applies never reach the kept
// one, so a later read counts the finding exactly once (audit 2026-10-27).
func TestAKeptUnstoredReportIsNotChangedByItsReaders(t *testing.T) {
	kept := scan.Report{ID: "scn_1", ProjectID: "prj", Findings: []scan.Finding{{Engine: "secrets", Severity: scan.High, Fingerprint: strings.Repeat("a", 24)}},
		Engines: []scan.EngineResult{{ID: "secrets", Findings: 1}}}
	kept.Counts.High = 1
	s := &securityExt{running: map[string]*scan.Run{}, unstored: map[string]scan.Report{"prj": kept}}
	d := map[string]scan.Dismissal{strings.Repeat("a", 24): {By: "human:kyle", Reason: scan.ReasonNotReal}}
	for i := 0; i < 2; i++ {
		rep, err := s.lastReport(context.Background(), target{Key: "prj", ProjectID: "prj"})
		if err != nil || rep == nil {
			t.Fatalf("read %d: %v %v", i, rep, err)
		}
		scan.ApplyDismissals(rep, d)
		if rep.Counts.High != 0 || !rep.Findings[0].Dismissed || rep.Engines[0].Findings != 0 {
			t.Fatalf("read %d: counts %+v, engines %+v, dismissed %v", i, rep.Counts, rep.Engines, rep.Findings[0].Dismissed)
		}
		byID, _, err := s.scanByID(context.Background(), "scn_1")
		if err != nil {
			t.Fatal(err)
		}
		scan.ApplyDismissals(byID, d)
	}
	if got := s.unstored["prj"]; got.Findings[0].Dismissed || got.Counts.High != 1 || got.Engines[0].Findings != 1 {
		t.Fatalf("the kept report was changed: %+v", got)
	}
}
