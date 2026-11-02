package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/crashreport"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The task page's interrupted-turn request carries the crash report of that moment (X513): the newest one kept since the run
// started; an older one, or none, adds nothing, and other requests are left as they are.
func TestAnInterruptedTurnRequestCarriesTheCrashReportOfItsRun(t *testing.T) {
	start := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	reports := []crashreport.Report{ // newest first, as crashreport.List gives them
		{Name: "crash-20261102T101500Z-1.txt", Time: start.Add(15 * time.Minute), Summary: "fatal error: concurrent map writes"},
		{Name: "crash-20261102T095000Z-1.txt", Time: start.Add(-10 * time.Minute), Summary: "panic: older"},
	}
	got := newestCrashSince(reports, start.UnixMilli())
	if got == nil || got.Name != reports[0].Name || got.Summary != "fatal error: concurrent map writes" || got.Time != reports[0].Time.UnixMilli() {
		t.Fatalf("crash of the run: %+v", got)
	}
	if c := newestCrashSince(reports[1:], start.UnixMilli()); c != nil {
		t.Fatalf("a crash from before the run started is not its crash: %+v", c)
	}

	pending := []workflow.PendingRequest{
		{ID: "a1", Spec: workflow.RequestSpec{Kind: "approval", Payload: json.RawMessage(`{"gate":"interrupted","node":"code","step":3,"interruptions":2}`)}},
		{ID: "a2", Spec: workflow.RequestSpec{Kind: "approval", Payload: json.RawMessage(`{"gate":"budget","message":"x"}`)}},
	}
	out := withCrash(pending, got)
	var pl map[string]any
	_ = json.Unmarshal(out[0].Spec.Payload, &pl)
	cr, _ := pl["crashReport"].(map[string]any)
	if cr["name"] != reports[0].Name || cr["summary"] != reports[0].Summary || pl["interruptions"] != float64(2) {
		t.Fatalf("interrupted payload %s", out[0].Spec.Payload)
	}
	if string(out[1].Spec.Payload) != `{"gate":"budget","message":"x"}` {
		t.Fatalf("another request must be left as it is: %s", out[1].Spec.Payload)
	}
	before := string(pending[0].Spec.Payload)
	if string(withCrash(pending, nil)[0].Spec.Payload) != before {
		t.Fatal("no crash report adds nothing")
	}
}
