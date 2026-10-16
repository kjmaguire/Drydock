package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
)

// Audit 2026-10-16, H10: a second process on a data folder used to open the whole core before anything said it should not be
// there, and opening the core marks the runs the last stop left open as failed. To the core a second launch is a restart after a
// crash, so it did that to the first one's live runs.
func TestASecondCoreOnTheSameDataFolderIsRefusedAndLeavesTheFirstsRunsAlone(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	p, _ := a.setup("gocalc")
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &ag)
	a.sql(`INSERT INTO agent_runs(id,agent_id,status,started_at) VALUES('arun_live',?,'RUNNING',?)`, ag.ID, time.Now().UnixMilli())

	_, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err == nil || !strings.Contains(err.Error(), "already running") || !strings.Contains(err.Error(), dataDir) {
		t.Fatalf("a second core on the same folder: %v", err)
	}
	var status string
	var ended int64
	if err := a.c.DB.Reader().QueryRow(`SELECT status, ended_at FROM agent_runs WHERE id='arun_live'`).Scan(&status, &ended); err != nil {
		t.Fatal(err)
	}
	if status != "RUNNING" || ended != 0 {
		t.Fatalf("the first core's live run was touched by the refused one: %s ended=%d", status, ended)
	}

	// once the first core closes the folder is free again (and the run it left open is then a stale one, as after any stop)
	a.c.Close()
	c2, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err != nil {
		t.Fatalf("the folder was not free after Close: %v", err)
	}
	defer c2.Close()
	if err := c2.DB.Reader().QueryRow(`SELECT status FROM agent_runs WHERE id='arun_live'`).Scan(&status); err != nil || status != "FAILED" {
		t.Fatalf("after a real stop the open run is closed: %s %v", status, err)
	}
}
