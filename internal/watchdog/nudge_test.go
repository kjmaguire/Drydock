package watchdog_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/watchdog"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// turnCtl is a labelled test double of the agent runtime's turn controller: it records the interrupt and the message.
type turnCtl struct {
	mu        sync.Mutex
	cancelled int
	sent      []string
	got       chan struct{}
}

func (c *turnCtl) CancelTurn() error { c.mu.Lock(); c.cancelled++; c.mu.Unlock(); return nil }
func (c *turnCtl) Send(_ context.Context, msg string) error {
	c.mu.Lock()
	c.sent = append(c.sent, msg)
	c.mu.Unlock()
	close(c.got)
	return nil
}

// A person's message (task.steer): with no turn running it waits for the next turn, added to what is already queued rather
// than replacing it; with a turn running, the turn is interrupted and the message sent into the same session.
func TestNudgeQueuesWithoutATurnAndInterruptsARunningOne(t *testing.T) {
	r := newRig(t)
	if got := r.wd.Nudge(context.Background(), "n1", "Use the existing helper."); got != watchdog.SteerQueued {
		t.Fatalf("no turn is running: %s", got)
	}
	if got := r.wd.Nudge(context.Background(), "n1", "And keep the tests."); got != watchdog.SteerQueued {
		t.Fatalf("no turn is running: %s", got)
	}
	s := &spinner{}
	r.await(r.run("wd-nudge-q", s.handler, "n1"), workflow.StatusCompleted)
	s.mu.Lock()
	if len(s.seen) != 1 || len(s.seen[0]) != 1 || s.seen[0][0] != "Use the existing helper.\n\nAnd keep the tests." {
		t.Fatalf("queued messages %q", s.seen)
	}
	s.mu.Unlock()

	ctl := &turnCtl{got: make(chan struct{})}
	started := make(chan struct{})
	h := func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		in.Turn.Set(ctl)
		close(started)
		select {
		case <-ctl.got:
		case <-ctx.Done():
			return workflow.NodeOutput{}, ctx.Err()
		}
		b, _ := json.Marshal("done")
		return workflow.NodeOutput{Value: b}, nil
	}
	id := r.run("wd-nudge-live", h, "n2")
	<-started
	deadline := time.Now().Add(5 * time.Second)
	for r.wd.Nudge(context.Background(), "n2", "Stop editing the README.") != watchdog.SteerDelivered {
		if time.Now().After(deadline) {
			t.Fatal("the running turn was not found")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.await(id, workflow.StatusCompleted)
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if ctl.cancelled != 1 || len(ctl.sent) != 1 || !strings.Contains(ctl.sent[0], "README") {
		t.Fatalf("interrupts %d, sent %q", ctl.cancelled, ctl.sent)
	}
	if n := count(t, r.db, "n2"); n != 0 {
		t.Fatalf("a person's message is not a watchdog trip, count %d", n)
	}
}
