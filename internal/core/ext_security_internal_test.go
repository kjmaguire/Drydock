package core

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/scan"
)

// The rules folder decides which rules read the whole project: only a person chooses it (decision P44).
func TestOnlyAPersonChoosesTheRulesFolder(t *testing.T) {
	s := &securityExt{}
	for _, by := range []string{"", "human:", "agent:bot", "system"} {
		if _, err := s.setRulesDir(context.Background(), "prj", "C:/rules", by); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("%q: %v", by, err)
		}
	}
}

// Only a person starts a scan, of a project or of a folder (decision P61), and chooses the folder scans' rules folder.
func TestOnlyAPersonStartsAScan(t *testing.T) {
	s := &securityExt{}
	for _, by := range []string{"", "human:", "agent:bot", "system"} {
		for _, p := range []scanTarget{{Folder: "C:/code/shop"}, {ProjectID: "prj"}} {
			if _, err := s.start(context.Background(), p, by); err == nil || !strings.Contains(err.Error(), "only a person") {
				t.Fatalf("%q %+v: %v", by, p, err)
			}
		}
		if _, err := s.setWorkspaceRulesDir(context.Background(), "C:/rules", by); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("%q: %v", by, err)
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
