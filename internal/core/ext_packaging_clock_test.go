package core_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/license"
)

const day = 24 * time.Hour

// licensedApp is an app with a year-long paid license that was installed at the kit's clock.
func licensedApp(t *testing.T, dataDir string) (*licenseKit, *app, time.Time) {
	t.Helper()
	kit := newKit(t)
	a := open(t, dataDir)
	kit.attach(a.c)
	t0 := kit.clk.now()
	if v, err := a.importLicense(kit.token("paid", "paid-clock", t0, t0.Add(365*day), 7)); err != nil || v.State != license.StateActive {
		t.Fatalf("license: %+v %v", v, err)
	}
	return kit, a, t0
}

// One reading of a clock that is years ahead (a wrong date for an hour, a clock chip that lost its battery) used to be written
// down as the latest time Drydock had seen, and the license was judged by it from then on, even after the clock was right
// again (audit 2026-10-16, M16).
func TestAForwardClockJumpThatDoesNotHoldIsNeverRemembered(t *testing.T) {
	kit, a, t0 := licensedApp(t, "")
	kit.clk.set(t0.Add(800 * day))
	if v := a.status(); v.LastSeen > t0.Add(time.Hour).Unix() {
		t.Fatalf("a reading 800 days ahead was recorded at once: last seen %s", time.Unix(v.LastSeen, 0))
	}
	kit.clk.set(t0.Add(2 * time.Hour)) // the clock is put right
	v := a.status()
	if v.State != license.StateActive || v.ClockSuspect || v.ReadOnly {
		t.Fatalf("after the clock was put right the license is still judged by the wrong date: %+v", v)
	}
	if err := a.c.RequireLicensed(context.Background(), "start_task"); err != nil {
		t.Fatalf("work is refused after the clock was put right: %v", err)
	}
}

// A clock that really is far ahead and stays there is the date: after three days it is remembered, and setting the clock back
// afterwards is what the guard is for.
func TestAForwardClockJumpThatHoldsForDaysIsRemembered(t *testing.T) {
	kit, a, t0 := licensedApp(t, "")
	far := t0.Add(400 * day) // past the end of the license and its grace
	kit.clk.set(far)
	a.status()
	kit.clk.set(far.Add(2*day + 20*time.Hour))
	if v := a.status(); v.LastSeen > t0.Add(time.Hour).Unix() {
		t.Fatalf("remembered before it held for three days: last seen %s", time.Unix(v.LastSeen, 0))
	}
	kit.clk.set(far.Add(3*day + time.Hour))
	a.status() // this call takes the step; the view it returns still shows the record from before it
	if v := a.status(); v.LastSeen != far.Add(3*day+time.Hour).Unix() {
		t.Fatalf("a jump that held for three days is not remembered: last seen %s", time.Unix(v.LastSeen, 0))
	}
	kit.clk.set(t0.Add(day)) // set back: judged by what was seen
	if v := a.status(); !v.ClockSuspect || v.State != license.StateExpired {
		t.Fatalf("a clock set back after a held jump: %+v", v)
	}
}

// The three days are counted across restarts: an app that is closed every evening still gets there.
func TestAPendingForwardJumpIsKeptAcrossARestart(t *testing.T) {
	dataDir := t.TempDir() + "/data"
	kit, a, t0 := licensedApp(t, dataDir)
	far := t0.Add(60 * day)
	kit.clk.set(far)
	a.status()
	a.c.Close()
	b := openAt(t, dataDir)
	kit.attach(b.c)
	kit.clk.set(far.Add(3*day + time.Hour))
	b.status()
	if v := b.status(); v.LastSeen != far.Add(3*day+time.Hour).Unix() {
		t.Fatalf("the pending jump was forgotten by the restart: last seen %s", time.Unix(v.LastSeen, 0))
	}
}

// A date that was wrong when it was written down can be undone, by a person, on the record.
func TestResetClockCorrectsTheRecordAndIsAudited(t *testing.T) {
	dataDir := t.TempDir() + "/data"
	kit, a, t0 := licensedApp(t, dataDir)
	a.c.Close()
	// as an older version left it: the latest time seen is far ahead of the real one
	setSetting(t, dataDir, "license.maxseen", jsonInt(t0.Add(900*day).Unix()))
	b := openAt(t, dataDir)
	kit.attach(b.c)
	v := b.status()
	if !v.ClockSuspect || !v.ReadOnly {
		t.Fatalf("the record ahead of the clock should lock the license: %+v", v)
	}
	if !strings.Contains(v.Message, "reset the clock check") {
		t.Fatalf("the message does not say how to get out: %q", v.Message)
	}
	var after core.LicenseView
	b.call("license.resetClock", nil, &after)
	if after.ClockSuspect || after.ReadOnly || after.State != license.StateActive {
		t.Fatalf("after the reset: %+v", after)
	}
	if after.LastSeen != kit.clk.now().Unix() {
		t.Fatalf("the record should be the clock now: %s", time.Unix(after.LastSeen, 0))
	}
	var n int
	var detail string
	if err := b.c.DB.Reader().QueryRow(`SELECT COUNT(*), COALESCE(MAX(detail),'') FROM audit_log WHERE action='license.clock_reset'`).Scan(&n, &detail); err != nil || n != 1 {
		t.Fatalf("the reset was not audited once: %d %v", n, err)
	}
	var d map[string]int64
	if json.Unmarshal([]byte(detail), &d) != nil || d["was"] != t0.Add(900*day).Unix() || d["now"] != kit.clk.now().Unix() {
		t.Fatalf("the audit entry does not hold both times: %s", detail)
	}
	if err := b.c.RequireLicensed(context.Background(), "start_task"); err != nil {
		t.Fatalf("work is refused after the reset: %v", err)
	}
	// with nothing to reset it refuses: the reset cannot be used to move a license back in time while the clock is right
	if err := b.callErr("license.resetClock", nil); err == nil || !strings.Contains(err.Error(), "nothing to reset") {
		t.Fatalf("a second reset: %v", err)
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

var _ = agents.Adapter(nil)
var _ = claude.Adapter{}
var _ = os.Args
