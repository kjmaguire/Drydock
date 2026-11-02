package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/crashreport"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The task page's interrupted-turn request carries the crash report of that moment (X513): the newest one kept between the run's
// start and the time the request was raised; one from before the run, or from while the request waited, is not its crash; none
// adds nothing, and other requests are left as they are.
func TestAnInterruptedTurnRequestCarriesTheCrashReportOfItsRun(t *testing.T) {
	start := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	reports := []crashreport.Report{ // newest first, as crashreport.List gives them
		{Name: "crash-20261102T101500Z-1.txt", Time: start.Add(15 * time.Minute), Summary: "fatal error: concurrent map writes"},
		{Name: "crash-20261102T095000Z-1.txt", Time: start.Add(-10 * time.Minute), Summary: "panic: older"},
	}
	raised := start.Add(20 * time.Minute).UnixMilli()
	got := newestCrashBetween(reports, start.UnixMilli(), raised)
	if got == nil || got.Name != reports[0].Name || got.Summary != "fatal error: concurrent map writes" || got.Time != reports[0].Time.UnixMilli() {
		t.Fatalf("crash of the run: %+v", got)
	}
	if c := newestCrashBetween(reports[1:], start.UnixMilli(), raised); c != nil {
		t.Fatalf("a crash from before the run started is not its crash: %+v", c)
	}
	// Drydock crashed again at 11:00, while the request (raised at 10:20) waited: that crash did not cut the turn off
	later := append([]crashreport.Report{{Name: "crash-20261102T110000Z-1.txt", Time: start.Add(time.Hour), Summary: "panic: unrelated"}}, reports...)
	if c := newestCrashBetween(later, start.UnixMilli(), raised); c == nil || c.Name != reports[0].Name {
		t.Fatalf("a crash after the request was raised is not what cut the turn off: %+v", c)
	}
	if c := newestCrashBetween(later[:1], start.UnixMilli(), raised); c != nil {
		t.Fatalf("only a crash from while the request waited: none is shown, got %+v", c)
	}

	pending := []workflow.PendingRequest{
		{ID: "a1", Spec: workflow.RequestSpec{Kind: "approval", Payload: json.RawMessage(`{"gate":"interrupted","node":"code","step":3,"interruptions":2}`)}},
		{ID: "a2", Spec: workflow.RequestSpec{Kind: "approval", Payload: json.RawMessage(`{"gate":"budget","message":"x"}`)}},
	}
	out := []workflow.PendingRequest{withCrash(pending[0], got), withCrash(pending[1], got)}
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
	if string(withCrash(pending[0], nil).Spec.Payload) != before {
		t.Fatal("no crash report adds nothing")
	}
}
