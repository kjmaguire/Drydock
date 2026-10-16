package core_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/core"
)

// Test connection from the app sends the tiny prompt when asked (on haiku) and its outcome survives a restart: before, the
// UI never prompted and a restart put the CLI back to "version untested". Runs only with DRYDOCK_REAL_CLAUDE=1 and a
// signed-in real claude.
func TestRealTestConnectionPromptsAndIsKeptAcrossARestart(t *testing.T) {
	if why := contract.RealClaudeSkip(); why != "" {
		t.Skip(why)
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "")
	if det, err := claude.New().Detect(context.Background()); err != nil || det.Status != agents.StatusReady {
		t.Skipf("real claude not usable here: status=%s problems=%v err=%v", det.Status, det.Problems, err)
	}
	data := filepath.Join(t.TempDir(), "data")
	open := func() *core.Core {
		c, err := core.Open(context.Background(), core.Config{DataDir: data, User: "kyle", LeaseTTL: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := open()
	rep, err := c.TestConnection(context.Background(), "claude", true)
	if err != nil {
		t.Fatal(err)
	}
	var prompt connections.Step
	for _, s := range rep.Steps {
		if s.ID == "T3" {
			prompt = s
		}
	}
	if prompt.Status != connections.StepPass || !rep.OK || rep.TokensUsed == 0 {
		t.Fatalf("the tiny prompt did not run or pass: %+v (ok %v, tokens %d)", prompt, rep.OK, rep.TokensUsed)
	}
	for _, s := range rep.Steps {
		if s.ID == "T6" && strings.Contains(s.Reason, "not part of this build") {
			t.Fatalf("T6 says the gateway is not built: %q", s.Reason)
		}
	}
	c.Close()
	c = open()
	defer c.Close()
	conn, err := c.Connections.Check(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if conn.Reason == connections.ReasonVersionUntested || strings.HasPrefix(conn.Reason, connections.ReasonTestFailed) {
		t.Fatalf("the passing Test was forgotten by the restart: %s %v", conn.Reason, conn.Problems)
	}
}
