package core_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/runners"
	"github.com/kjmaguire/drydock/internal/runners/runnerstest"
)

type runnerRig struct {
	gh       *runnerstest.GitHub
	win, lin *runnerstest.Launcher
}

// runnersCore is a core whose CI runners talk to the TEST DOUBLE GitHub and start TEST DOUBLE runners, on a Windows and a
// Linux "host" alike.
func runnersCore(t *testing.T, dataDir string, rig *runnerRig) *app {
	t.Helper()
	c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: "claude-not-used"}},
		Runners: func(p *runners.Pool) {
			p.API = func(context.Context) (host.API, error) { return rig.gh, nil }
			p.Launchers = map[runners.Kind]runners.Launcher{runners.Windows: rig.win, runners.Linux: rig.lin}
			p.Machine = "kyle-pc"
			p.Wait = func(ctx context.Context, d time.Duration) {
				select {
				case <-ctx.Done():
				case <-time.After(20 * time.Millisecond):
				}
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}
}

func newRunnerRig() *runnerRig {
	return &runnerRig{
		gh: runnerstest.NewGitHub(map[string]runnerstest.Repo{
			"kjmaguire/shop":    {Private: true, Admin: true},
			"kjmaguire/drydock": {Private: false, Admin: true},
			"acme/app":          {Private: true, Admin: false},
		}),
		win: &runnerstest.Launcher{K: runners.Windows, FreeB: 200 << 30},
		lin: &runnerstest.Launcher{K: runners.Linux},
	}
}

func slotOf(v core.RunnersView, key string) *core.RunnerSlot {
	for i := range v.Slots {
		if v.Slots[i].Key == key {
			return &v.Slots[i]
		}
	}
	return nil
}

// Decision P56: a person adds a Linux runner for a private repository; it registers with GitHub and waits, runs a job and
// registers again; switching it off unregisters it, the setting outlives a restart, and removing it forgets it.
func TestAPersonAddsACIRunnerForAPrivateRepository(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	rig := newRunnerRig()
	a := runnersCore(t, dir, rig)
	var v core.RunnersView
	a.call("runners.get", struct{}{}, &v)
	if len(v.Slots) != 0 || len(v.Kinds) != 2 || !v.Kinds[0].Available || v.Kinds[0].RunsOn != "[self-hosted, Windows, drydock]" ||
		v.Kinds[1].RunsOn != "[self-hosted, Linux, drydock]" || v.Max != 8 {
		t.Fatalf("%+v", v)
	}
	a.call("runners.add", map[string]any{"repo": "https://github.com/kjmaguire/shop", "kind": "linux"}, &v)
	key := "kjmaguire/shop:linux"
	if s := slotOf(v, key); s == nil || s.By != "human:kyle" || !s.Enabled {
		t.Fatalf("%+v", v.Slots)
	}
	a.waitFor("the runner to wait for a job", func() bool {
		a.call("runners.get", struct{}{}, &v)
		s := slotOf(v, key)
		return s != nil && s.State == runners.StateIdle
	})
	regs := rig.gh.Registrations()
	if len(regs) != 1 || !strings.HasPrefix(regs[0].Name, "drydock-kyle-pc-linux-") {
		t.Fatalf("%+v", regs)
	}
	rig.lin.Queue("build", "Succeeded")
	a.waitFor("the job to be recorded", func() bool {
		a.call("runners.get", struct{}{}, &v)
		s := slotOf(v, key)
		return s != nil && s.Jobs == 1 && s.LastJob != nil && s.LastJob.Result == "Succeeded"
	})
	var log struct{ Lines []string }
	a.call("runners.log", map[string]any{"repo": "kjmaguire/shop", "kind": "linux"}, &log)
	if !slices.ContainsFunc(log.Lines, func(l string) bool { return strings.Contains(l, "Running job: build") }) {
		t.Fatalf("%v", log.Lines)
	}
	if len(a.eventsOf("runners.changed")) == 0 {
		t.Fatal("no runners.changed event")
	}
	for _, e := range a.eventsOf("runners.changed") {
		for _, reg := range rig.gh.Registrations() {
			if strings.Contains(string(mustJSON(e)), reg.Config) {
				t.Fatal("an event carried a runner's one-job configuration")
			}
		}
	}

	a.call("runners.enable", map[string]any{"repo": "kjmaguire/shop", "kind": "linux", "enabled": false}, &v)
	if s := slotOf(v, key); s == nil || s.State != runners.StatePaused || s.Enabled {
		t.Fatalf("%+v", v.Slots)
	}
	last := rig.gh.Registrations()[len(rig.gh.Registrations())-1]
	if !slices.Contains(rig.gh.RemovedIDs(), last.ID) {
		t.Fatal("switching the runner off left its registration on GitHub")
	}

	// the runners are the workspace's settings: a restart brings them back as they were
	a.c.Close()
	b := runnersCore(t, dir, rig)
	b.call("runners.get", struct{}{}, &v)
	if s := slotOf(v, key); s == nil || s.Enabled || s.State != runners.StatePaused {
		t.Fatalf("after a restart: %+v", v.Slots)
	}
	// counted before switching it on: the runner can register before the call returns, and then waits for a job
	n := len(rig.gh.Registrations())
	b.call("runners.enable", map[string]any{"repo": "kjmaguire/shop", "kind": "linux", "enabled": true}, &v)
	b.waitFor("the runner to register again", func() bool { return len(rig.gh.Registrations()) > n })
	b.call("runners.remove", map[string]any{"repo": "kjmaguire/shop", "kind": "linux"}, &v)
	if len(v.Slots) != 0 {
		t.Fatalf("%+v", v.Slots)
	}
	final := rig.gh.Registrations()[len(rig.gh.Registrations())-1]
	if !slices.Contains(rig.gh.RemovedIDs(), final.ID) {
		t.Fatal("removing the runner left its registration on GitHub")
	}
}

// What is refused, each with its reason: a public repository (anyone's fork could run code on this PC), a repository the
// account does not administer, a Windows runner the person has not accepted runs as them, a second runner of the same kind,
// a ninth runner, and things that are not repositories or kinds.
func TestCIRunnersRefuseWhatIsNotSafeOrNotPossible(t *testing.T) {
	rig := newRunnerRig()
	a := runnersCore(t, filepath.Join(t.TempDir(), "data"), rig)
	for _, c := range []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"repo": "kjmaguire/drydock", "kind": "linux"}, "fork"},
		{map[string]any{"repo": "acme/app", "kind": "linux"}, "does not administer"},
		{map[string]any{"repo": "kjmaguire/shop", "kind": "windows"}, "as you"},
		{map[string]any{"repo": "kjmaguire/missing", "kind": "linux"}, "does not show"},
		{map[string]any{"repo": "not a repo", "kind": "linux"}, "owner/name"},
		{map[string]any{"repo": "kjmaguire/shop", "kind": "macos"}, "not a runner kind"},
	} {
		if err := a.callErr("runners.add", c.params); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: %v", c.params, err)
		}
	}
	if len(rig.gh.Registrations()) != 0 {
		t.Fatal("a refused runner registered")
	}
	var v core.RunnersView
	a.call("runners.add", map[string]any{"repo": "kjmaguire/shop", "kind": "windows", "acceptRunsAsYou": true}, &v)
	if err := a.callErr("runners.add", map[string]any{"repo": "KJMaguire/Shop", "kind": "windows", "acceptRunsAsYou": true}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		name := "kjmaguire/r" + string(rune('a'+i))
		rig.gh.SetRepo(name, runnerstest.Repo{Private: true, Admin: true})
		a.call("runners.add", map[string]any{"repo": name, "kind": "linux"}, &v)
	}
	rig.gh.SetRepo("kjmaguire/ninth", runnerstest.Repo{Private: true, Admin: true})
	if err := a.callErr("runners.add", map[string]any{"repo": "kjmaguire/ninth", "kind": "linux"}); err == nil || !strings.Contains(err.Error(), "at most 8") {
		t.Fatal(err)
	}
	if err := a.callErr("runners.remove", map[string]any{"repo": "kjmaguire/none", "kind": "linux"}); err == nil {
		t.Fatal("removed a runner that does not exist")
	}
	// the folder for Windows jobs cannot change under a running Windows runner
	if err := a.callErr("runners.root.set", map[string]any{"path": t.TempDir()}); err == nil || !strings.Contains(err.Error(), "Windows runners run on Windows only") &&
		!strings.Contains(err.Error(), "switch the Windows runners off") {
		t.Fatal(err)
	}
}

// When no Dev Drive is found, the CI runners' view and the folder hints say why (the Dev Drive check needs no administrator
// rights; the reason comes from the volumes themselves on Windows, here from the TEST DOUBLE), and when one is found there is
// no reason to give.
func TestCIRunnersSayWhyNoDevDriveWasFound(t *testing.T) {
	reason := "the Dev Drive check is not available on this Windows version (the volume state query for D: failed: The parameter is incorrect.), and fsutil devdrv query, which needs administrator rights, said \"Error:  Access is denied.\""
	open := func(scan runners.DevDriveScan) *app {
		c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
			Adapters:  map[string]agents.Adapter{"claude": &claude.Adapter{Exe: "claude-not-used"}},
			DevDrives: func(context.Context) runners.DevDriveScan { return scan }, // TEST DOUBLE: what the volumes said
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Close)
		return &app{t: t, c: c}
	}
	a := open(runners.DevDriveScan{Reason: reason})
	var v core.RunnersView
	a.call("runners.get", struct{}{}, &v)
	if len(v.DevDrives) != 0 || v.DevDriveReason != reason {
		t.Fatalf("%+v", v)
	}
	var h core.FolderHints
	a.call("fs.folderHints", struct{}{}, &h)
	if len(h.DevDrives) != 0 || h.DevDriveReason != reason {
		t.Fatalf("%+v", h)
	}
	b := open(runners.DevDriveScan{Drives: []runners.Drive{{Letter: "E:", Trusted: true}}})
	v, h = core.RunnersView{}, core.FolderHints{} // omitted fields would otherwise keep the first answer
	b.call("runners.get", struct{}{}, &v)
	if len(v.DevDrives) != 1 || v.DevDrives[0].Letter != "E:" || v.DevDriveReason != "" {
		t.Fatalf("%+v", v)
	}
	b.call("fs.folderHints", struct{}{}, &h)
	if len(h.DevDrives) != 1 || h.DevDriveReason != "" {
		t.Fatalf("%+v", h)
	}
}
