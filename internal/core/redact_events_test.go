package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/events"
)

// What an agent, a check or a remote service printed reaches the event log through the events its failures publish. The bus the
// core builds scans those events with the secret scanner before they are stored (audit 2026-10-16, H7).
func TestEventsThatCarryToolOutputAreScannedBeforeTheyAreStored(t *testing.T) {
	a := open(t, "")
	ctx := context.Background()
	for _, typ := range []string{"agent.failed", "agent.stderr", "workflow.node_failed", "check.finished", "task.blocked", "mcp.call_failed", "github.error", "pipeline.failed", "watchdog.stuck", "lead.failed"} {
		data := json.RawMessage(`{"error":"curl -H 'Authorization: token ` + plantedToken + `' failed","nested":{"lines":["fine","` + plantedToken + `"]},"count":7}`)
		seq, err := a.c.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: "proj_x", Data: data})
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		got, err := a.c.Bus.Replay(ctx, seq-1, events.Filter{Types: []string{typ}}, 1)
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: %v %d", typ, err, len(got))
		}
		if strings.Contains(string(got[0].Data), plantedToken) {
			t.Errorf("%s was stored with the token: %s", typ, got[0].Data)
		}
		var v struct {
			Error string `json:"error"`
			Count int    `json:"count"`
		}
		if err := json.Unmarshal(got[0].Data, &v); err != nil || v.Count != 7 || !strings.HasPrefix(v.Error, "curl -H 'Authorization: ") || !strings.HasSuffix(v.Error, "failed") {
			t.Errorf("%s: the rest of the event must be as published: %v %s", typ, err, got[0].Data)
		}
	}

	// Drydock's own facts (ids, counts, states) are not scanned: the same bytes come back
	own := json.RawMessage(`{"id":"proj_x","name":"demo","count":3}`)
	seq, err := a.c.Bus.Publish(ctx, events.Event{Type: "project.renamed", ProjectID: "proj_x", Data: own})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.c.Bus.Replay(ctx, seq-1, events.Filter{Types: []string{"project.renamed"}}, 1)
	if err != nil || len(got) != 1 || string(got[0].Data) != string(own) {
		t.Fatalf("%v %+v", err, got)
	}
}
