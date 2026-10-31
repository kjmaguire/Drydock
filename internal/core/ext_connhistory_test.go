package core_test

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/core"
)

// An install's conformance history and capability matrix (decision P132): before any test the matrix is what the adapter
// declares (its permission modes among it) and there are no runs; each Test connection run is kept, newest first, with its
// steps counted and whether the tiny prompt was sent; the matrix then carries the newest run's cells; another install of the
// same CLI keeps its own history; the history survives a restart.
func TestTestConnectionRunsAreKeptAsTheInstallsHistory(t *testing.T) {
	a := open(t, "")
	var h core.ConnectionHistory
	a.call("connection.history", map[string]string{"adapter": "claude"}, &h)
	if h.Key != "claude" || len(h.Runs) != 0 || h.MatrixAt != 0 || len(h.Matrix) == 0 {
		t.Fatalf("before any test: %+v", h)
	}
	if c, ok := h.Matrix["permission_modes"]; !ok || c.Provenance != "declared" || c.Value == "" {
		t.Fatalf("the permission modes: %+v", h.Matrix)
	}
	for k, c := range h.Matrix {
		if c.Provenance == "observed" {
			t.Fatalf("%s observed before any test: %+v", k, c)
		}
	}
	var first, second connections.Report
	a.call("connection.test", map[string]any{"adapter": "claude"}, &first)
	a.call("connection.test", map[string]any{"adapter": "claude", "prompt": true}, &second)
	a.call("connection.history", map[string]string{"adapter": "claude"}, &h)
	if len(h.Runs) != 2 || h.Runs[0].ID <= h.Runs[1].ID || !h.Runs[0].Prompt || h.Runs[1].Prompt || h.Runs[0].At == 0 {
		t.Fatalf("the runs: %+v", h.Runs)
	}
	p, f, s := second.Counts()
	if r := h.Runs[0]; r.Passed != p || r.Failed != f || r.Skipped != s || r.OK != second.OK || r.CLIVersion != second.CLIVersion || r.Transport != second.Transport {
		t.Fatalf("the newest run %+v, the report %d/%d/%d ok %v", r, p, f, s, second.OK)
	}
	if !second.OK && h.Runs[0].Reason == "" {
		t.Fatalf("a failed run without its reason: %+v", h.Runs[0])
	}
	if len(second.Matrix) > 0 {
		if h.MatrixAt != h.Runs[0].At {
			t.Fatalf("the matrix is not the newest run's: %d, runs %+v", h.MatrixAt, h.Runs)
		}
		for k, c := range second.Matrix {
			if got := h.Matrix[k]; got.Provenance != c.Provenance {
				t.Fatalf("%s: %+v, the report's %+v", k, got, c)
			}
		}
	}
	var wsl core.ConnectionHistory
	a.call("connection.history", map[string]string{"adapter": "claude", "distro": "Ubuntu"}, &wsl)
	if wsl.Key != connections.WSLKey("claude", "Ubuntu") || len(wsl.Runs) != 0 {
		t.Fatalf("the WSL install's history: %+v", wsl)
	}
	if err := a.callErr("connection.history", map[string]string{"adapter": "no-such-cli"}); err == nil {
		t.Fatal("a history for a CLI that is not there")
	}

	dir := a.dataDir()
	a.c.Close()
	b := openAt(t, dir)
	b.call("connection.history", map[string]string{"adapter": "claude"}, &h)
	if len(h.Runs) != 2 {
		t.Fatalf("after a restart: %+v", h.Runs)
	}
}
