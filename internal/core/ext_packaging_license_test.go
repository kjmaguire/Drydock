package core_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/license"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// ---- shared helpers for the packaging tests ----

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

type licenseKit struct {
	priv ed25519.PrivateKey
	keys license.Keys
	clk  *testClock
}

func newKit(t *testing.T) *licenseKit {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &licenseKit{priv: priv, keys: license.Keys{"k1": pub}, clk: &testClock{t: time.Now()}}
}

// attach turns licensing on for a running Core, as a build with embedded keys would have it.
func (k *licenseKit) attach(c *core.Core) {
	c.TestSetLicenseKeys(k.keys)
	c.TestSetLicenseClock(k.clk.now)
}

func (k *licenseKit) token(kind, jti string, iat, exp time.Time, graceDays int, channels ...string) string {
	return license.Sign(k.priv, license.Payload{KID: "k1", JTI: jti, Kind: kind, Sub: "cust-1", Name: "Test Co", Plan: "team", IAT: iat.Unix(), NBF: iat.Unix(), EXP: exp.Unix(),
		GraceDays: graceDays, Channels: channels})
}

func (a *app) status() core.LicenseView {
	a.t.Helper()
	var v core.LicenseView
	a.call("license.status", nil, &v)
	return v
}

func (a *app) importLicense(token string) (core.LicenseView, error) {
	raw, err := a.c.Call(context.Background(), "license.import", mustJSON(map[string]string{"token": token}))
	if err != nil {
		return core.LicenseView{}, err
	}
	var v core.LicenseView
	json.Unmarshal(raw, &v)
	return v, nil
}

func (a *app) mustBeReadOnlyError(what string, err error) {
	a.t.Helper()
	if err == nil {
		a.t.Fatalf("%s was allowed while the license is read-only", what)
	}
	if !errors.Is(err, core.ErrReadOnly) {
		a.t.Fatalf("%s: error does not wrap ErrReadOnly: %v", what, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "read-only") || !strings.Contains(msg, "nothing has been deleted") || !strings.Contains(msg, "Import a license") {
		a.t.Fatalf("%s: the message is not plain enough: %q", what, msg)
	}
}

func gitProject(t *testing.T, a *app, name string) core.Project {
	var p core.Project
	a.call("project.create", map[string]any{"name": name, "repoPath": newRepo(t)}, &p)
	return p
}

// ---- licensing ----

func TestLicensingNotConfiguredAllowsEverythingAndSaysSoLoudly(t *testing.T) {
	a := open(t, "")
	v := a.status()
	if v.Configured || !v.DevMode || v.State != license.StateNone || v.ReadOnly {
		t.Fatalf("status %+v", v)
	}
	if !strings.Contains(strings.ToUpper(v.Banner), "NOT CONFIGURED") || !strings.Contains(v.Message, "every feature is allowed") {
		t.Fatalf("the unlicensed build does not say so: %q / %q", v.Banner, v.Message)
	}
	// nothing is restricted: a project, an agent and a task start
	p := gitProject(t, a, "dev")
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &ag)
	if err := a.c.RequireLicensed(context.Background(), "start_task"); err != nil {
		t.Fatal(err)
	}
	if !a.c.RetentionAllowed() || !a.c.ChannelAllowed("beta") || !a.c.ChannelAllowed("stable") {
		t.Error("an unlicensed build must keep everything allowed")
	}
	err := a.callErr("license.import", map[string]string{"token": "x.y"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("import in a build without keys: %v", err)
	}
	if err := a.callErr("license.clear", nil); err == nil {
		t.Fatal("clear with nothing configured must say so")
	}
}

func TestLicenseStateMachineAndReadOnlyEnforcement(t *testing.T) {
	kit := newKit(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	p := gitProject(t, a, "gocalc") // created while the build is unlicensed
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "R", Adapter: "claude", Role: "reviewer"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "T", Description: "d"}, &task)
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": p.ID}, &rooms)
	general := ""
	for _, rm := range rooms {
		if rm.Name == "general" {
			general = rm.ID
		}
	}

	kit.attach(a.c)
	t0 := kit.clk.now()

	// no token in a licensed build: read-only, with the reason in plain words
	v := a.status()
	if !v.Configured || v.DevMode || v.State != license.StateNone || !v.ReadOnly || !strings.Contains(v.Banner, "read-only") {
		t.Fatalf("no license: %+v", v)
	}
	a.mustBeReadOnlyError("project.create", a.callErr("project.create", map[string]any{"name": "x", "repoPath": newRepo(t)}))
	if a.c.RetentionAllowed() || a.c.ChannelAllowed("beta") {
		t.Error("without a license retention must be suspended and only stable allowed")
	}
	a.call("room.post", map[string]any{"roomId": general, "body": "posting is allowed in every state"}, nil)

	// a token signed by another key, and a malformed one, are refused
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	forged := license.Sign(otherPriv, license.Payload{KID: "k1", JTI: "forged", Kind: "paid", Sub: "x", IAT: t0.Unix(), NBF: t0.Unix(), EXP: t0.Add(time.Hour * 24 * 400).Unix()})
	if _, err := a.importLicense(forged); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("forged token: %v", err)
	}
	if _, err := a.importLicense("not-a-token"); err == nil || !strings.Contains(err.Error(), "not a Drydock license") {
		t.Fatalf("garbage token: %v", err)
	}
	future := kit.token("paid", "future", t0.Add(72*time.Hour), t0.Add(500*time.Hour), 0)
	if _, err := a.importLicense(future); err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("nbf in the future: %v", err)
	}
	if a.status().State != license.StateNone {
		t.Fatal("a refused token changed the state")
	}

	// trial -> active (paid replaces trial) -> grace -> expired -> renewed
	trial := kit.token("trial", "trial-1", t0.Add(-2*time.Hour), t0.Add(14*24*time.Hour), 0)
	v, err := a.importLicense(trial)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != license.StateTrial || v.ReadOnly || v.Kind != "trial" || v.DaysLeft < 12 {
		t.Fatalf("trial: %+v", v)
	}
	if a.c.ChannelAllowed("beta") {
		t.Error("a token without a beta channel must not allow beta")
	}
	exp := t0.Add(30 * 24 * time.Hour)
	paid := kit.token("paid", "paid-1", t0, exp, 7, "stable", "beta")
	if v, err = a.importLicense(paid); err != nil || v.State != license.StateActive || v.Plan != "team" || v.Subject != "cust-1" {
		t.Fatalf("paid: %+v %v", v, err)
	}
	if !a.c.ChannelAllowed("beta") {
		t.Error("the license's channels are not used")
	}
	if _, err := a.importLicense(trial); err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("an older token replaced a newer one: %v", err)
	}
	if err := a.c.RequireLicensed(context.Background(), "start_task"); err != nil {
		t.Fatal(err)
	}
	// the token is never part of the status
	raw, _ := json.Marshal(a.status())
	if strings.Contains(string(raw), paid) || strings.Contains(string(raw), strings.Split(paid, ".")[1]) {
		t.Fatal("license.status leaks the token")
	}

	// grace: full functionality plus a persistent banner and an inbox item
	kit.clk.set(exp.Add(3 * 24 * time.Hour))
	a.c.TestReconcileLicense(context.Background())
	v = a.status()
	if v.State != license.StateGrace || v.ReadOnly || v.Banner == "" || !strings.Contains(v.Banner, "grace") {
		t.Fatalf("grace: %+v", v)
	}
	if err := a.c.RequireLicensed(context.Background(), "start_task"); err != nil {
		t.Fatalf("grace must keep full functionality: %v", err)
	}
	var needs []room.NeedsYouItem
	a.waitFor("grace inbox item", func() bool {
		a.call("needsyou.list", map[string]string{"projectId": p.ID}, &needs)
		for _, n := range needs {
			if strings.Contains(strings.ToLower(n.Title), "grace") {
				return true
			}
		}
		return false
	})

	// expired: read-only, every mutation refused with a plain message, everything else still works
	kit.clk.set(exp.Add(8 * 24 * time.Hour))
	a.c.TestReconcileLicense(context.Background())
	v = a.status()
	if v.State != license.StateExpired || !v.ReadOnly {
		t.Fatalf("expired: %+v", v)
	}
	a.mustBeReadOnlyError("task.assign then task.start", func() error {
		a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task) // assigning is planning, allowed
		return a.callErr("task.start", map[string]string{"id": task.ID})
	}())
	a.mustBeReadOnlyError("task.startTemplate", a.callErr("task.startTemplate", map[string]string{"taskId": task.ID, "template": "feature"}))
	a.mustBeReadOnlyError("agent.create", a.callErr("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "X", Adapter: "claude", Role: "backend"}))
	a.mustBeReadOnlyError("project.create", a.callErr("project.create", map[string]any{"name": "x", "repoPath": newRepo(t)}))
	a.mustBeReadOnlyError("run.respond", a.callErr("run.respond", map[string]any{"runId": "run_x", "requestId": "req_x", "decision": "approved"}))
	// refusing work stays available while read-only (reject, decline, stop a paused run): these must get past the license gate
	// and fail only because the run and approval do not exist
	for name, params := range map[string]map[string]any{
		"run.respond":                 {"runId": "run_x", "requestId": "req_x", "decision": "rejected"},
		"budget.resume":               {"runId": "run_x", "requestId": "req_x", "decision": "rejected"},
		"integration.outbound.decide": {"id": "out_x", "approve": false},
	} {
		if err := a.callErr(name, params); err != nil && strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s (a refusal) was blocked by read-only mode: %v", name, err)
		}
	}
	a.mustBeReadOnlyError("plan.propose", a.callErr("plan.propose", map[string]any{"projectId": p.ID, "goal": "g"}))
	a.mustBeReadOnlyError("scheduler.tick", a.callErr("scheduler.tick", map[string]any{"projectId": p.ID}))
	a.mustBeReadOnlyError("provider.create", a.callErr("provider.create", map[string]any{"name": "x"}))
	a.mustBeReadOnlyError("integration.connect", a.callErr("integration.connect", map[string]any{"kind": "linear"}))
	a.mustBeReadOnlyError("Core.StartTask", func() error { _, err := a.c.StartTask(context.Background(), task.ID); return err }())
	a.mustBeReadOnlyError("Core.CreateAgent", func() error {
		_, err := a.c.CreateAgent(context.Background(), core.AgentSpec{ProjectID: p.ID, Name: "X", Adapter: "claude", Role: "r"})
		return err
	}())
	// the table of gated methods really is wired into the registry
	for name := range core.GatedMethodsForTest() {
		if _, ok := a.c.Methods()[name]; !ok {
			t.Errorf("gated method %s is not in the registry (renamed?)", name)
			continue
		}
		a.mustBeReadOnlyError(name, a.callErr(name, map[string]any{}))
	}
	if a.c.RetentionAllowed() {
		t.Error("retention must be suspended while expired")
	}
	if rep, err := a.c.RunRetention(context.Background(), time.Now().Add(400*24*time.Hour)); err != nil || !rep.Suspended || rep.RunsCompacted+rep.RunsDeleted != 0 {
		t.Errorf("a retention pass while expired: %+v %v", rep, err)
	}
	if a.c.ChannelAllowed("beta") {
		t.Error("an expired license must not allow beta")
	}
	// still works: reading, posting, exporting, backing up, restoring, support bundle, license import
	var got core.TaskDetail
	a.call("task.get", map[string]string{"id": task.ID}, &got)
	a.call("room.post", map[string]any{"roomId": general, "body": "still can talk"}, nil)
	a.call("project.exportPreview", map[string]any{"projectId": p.ID}, nil)
	var exp1 map[string]any
	a.call("project.export", map[string]any{"projectId": p.ID, "dest": filepath.Join(t.TempDir(), "x.drydock-project.zip")}, &exp1)
	a.call("backup.settings.set", map[string]any{"dir": filepath.Join(t.TempDir(), "bk")}, nil)
	a.call("backup.now", nil, nil)
	a.call("supportbundle.preview", map[string]any{}, nil)
	a.call("license.status", nil, nil)
	var banner core.LicenseView
	a.call("license.status", nil, &banner)
	if !strings.Contains(banner.Banner, "read-only") || !strings.Contains(banner.Banner, "nothing has been deleted") {
		t.Errorf("expired banner: %q", banner.Banner)
	}
	// the expiry produced a Needs-you item as well
	a.waitFor("expired inbox item", func() bool {
		a.call("needsyou.list", map[string]string{"projectId": p.ID}, &needs)
		for _, n := range needs {
			if strings.Contains(strings.ToLower(n.Title), "expired") {
				return true
			}
		}
		return false
	})

	// the system clock set back does not extend validity: it is judged by the latest time ever seen
	kit.clk.set(exp.Add(-10 * 24 * time.Hour))
	v = a.status()
	if v.State != license.StateExpired || !v.ClockSuspect || !strings.Contains(v.Message, "clock") {
		t.Fatalf("clock set back: %+v", v)
	}
	a.mustBeReadOnlyError("task.start with the clock set back", a.callErr("task.start", map[string]string{"id": task.ID}))
	// ... and that memory survives a restart
	a.c.Close()
	b := &app{t: t}
	c2, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c2.Close)
	b.c = c2
	kit.attach(c2)
	kit.clk.set(exp.Add(-10 * 24 * time.Hour)) // still set back after the restart; the real time of the run is far earlier than the persisted one
	v = b.status()
	if v.State != license.StateExpired || !v.ClockSuspect {
		t.Fatalf("after restart with the clock set back: %+v", v)
	}

	// renewal: a newer token brings everything back, and the old one cannot be used to go back
	kit.clk.set(exp.Add(8 * 24 * time.Hour))
	renewed := kit.token("paid", "paid-2", t0.Add(26*24*time.Hour), exp.Add(365*24*time.Hour), 7, "stable", "beta")
	v, err = b.importLicense(renewed)
	if err != nil || v.State != license.StateActive || v.ReadOnly {
		t.Fatalf("renewal: %+v %v", v, err)
	}
	if err := b.c.RequireLicensed(context.Background(), "start_task"); err != nil {
		t.Fatalf("after renewal: %v", err)
	}
	if !b.c.RetentionAllowed() {
		t.Error("retention must resume after renewal")
	}
	// clearing returns to the no-license state; nothing is deleted
	var cleared core.LicenseView
	b.call("license.clear", nil, &cleared)
	if cleared.State != license.StateNone || !cleared.ReadOnly {
		t.Fatalf("clear: %+v", cleared)
	}
	var again core.TaskDetail
	b.call("task.get", map[string]string{"id": task.ID}, &again)
	if again.Task.ID != task.ID {
		t.Fatal("clearing the license lost data")
	}
}

// The license token is never written to the application log, an event, or a support bundle.
func TestLicenseTokenNeverLeaks(t *testing.T) {
	kit := newKit(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	kit.attach(a.c)
	t0 := kit.clk.now()
	token := kit.token("paid", "paid-1", t0, t0.Add(30*24*time.Hour), 7)
	if _, err := a.importLicense(token); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t)
	a.c.TestLog("debugging: user pasted %s into the box", token) // even if some code path logged it, the scrubbed log must not keep it
	if out := stderr(); !strings.Contains(out, "debugging: user pasted") || strings.Contains(out, strings.Split(token, ".")[0]) || strings.Contains(out, strings.Split(token, ".")[1]) {
		t.Fatalf("stderr must show the line without the token: %q", out)
	}
	logs, _ := filepath.Glob(filepath.Join(dataDir, "logs", "drydock.log*"))
	if len(logs) == 0 {
		t.Fatal("no application log was written")
	}
	for _, f := range logs {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), strings.Split(token, ".")[0]) || strings.Contains(string(b), strings.Split(token, ".")[1]) {
			t.Fatalf("the token reached %s", f)
		}
	}
	var evs []struct{ Data json.RawMessage }
	a.call("events.replay", map[string]any{"after": 0, "limit": 2000}, &evs)
	for _, e := range evs {
		if strings.Contains(string(e.Data), strings.Split(token, ".")[1]) {
			t.Fatal("the token reached an event payload")
		}
	}
	var out struct {
		Path string `json:"path"`
	}
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	a.call("supportbundle.create", map[string]any{"dest": dest}, &out)
	for name, body := range readZipFiles(t, dest) {
		if strings.Contains(body, strings.Split(token, ".")[1]) {
			t.Fatalf("the token reached the support bundle file %s", name)
		}
	}
}

var _ = fakeagent.Script{}
