package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
)

// A turn whose session never started (Claude Code refusing --resume of a session it does not have answers with an error result
// and nothing else; here the scripted stand-in does the same) is marked as such, so a caller can tell it from a turn that ran
// and start a fresh session instead. A turn that ran is marked started, and so is a continued session that resumed.
func TestATurnWhoseSessionNeverStartedIsMarked(t *testing.T) {
	h := newHarness(t, fakeagent.Script{MissingSessions: []string{"gone-1"}, Default: []fakeagent.Step{{Say: "hello"}}})
	ctx := context.Background()
	turn := func(resume string) (rt.TurnResult, error) {
		cfg := h.config()
		cfg.Spec.ResumeID = resume
		m, err := h.rt.Spawn(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = m.Stop(ctx) }()
		return m.RunTurn(ctx, "hi", nil, nil)
	}
	res, err := turn("gone-1")
	if err == nil && res.Started {
		t.Fatalf("a refused resume was reported as a started turn: %+v", res)
	}
	if err != nil && !errors.Is(err, rt.ErrNotStarted) {
		t.Fatalf("a refused resume must wrap ErrNotStarted: %v", err)
	}
	res, err = turn("")
	if err != nil || !res.Started || res.SessionID == "" || res.Text != "hello" {
		t.Fatalf("a turn that ran: %+v %v", res, err)
	}
	res, err = turn(res.SessionID)
	if err != nil || !res.Started {
		t.Fatalf("a resumed turn that ran: %+v %v", res, err)
	}
	h.settle(t)
}
