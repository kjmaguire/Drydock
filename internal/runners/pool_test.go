package runners_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/runners"
	"github.com/kjmaguire/drydock/internal/runners/runnerstest"
)

type poolRig struct {
	gh     *runnerstest.GitHub
	win    *runnerstest.Launcher
	lin    *runnerstest.Launcher
	pool   *runners.Pool
	mu     sync.Mutex
	events []runners.Status
	waits  []time.Duration
}

func newRig(t *testing.T, repos map[string]runnerstest.Repo) *poolRig {
	t.Helper()
	r := &poolRig{gh: runnerstest.NewGitHub(repos), win: &runnerstest.Launcher{K: runners.Windows}, lin: &runnerstest.Launcher{K: runners.Linux}}
	r.pool = &runners.Pool{
		API:       func(context.Context) (host.API, error) { return r.gh, nil },
		Launchers: map[runners.Kind]runners.Launcher{runners.Windows: r.win, runners.Linux: r.lin},
		Machine:   "Kyle-PC",
		Publish: func(s runners.Status) {
			r.mu.Lock()
			r.events = append(r.events, s)
			r.mu.Unlock()
		},
		// no real pauses in tests: record them and yield briefly
		Wait: func(ctx context.Context, d time.Duration) {
			r.mu.Lock()
			r.waits = append(r.waits, d)
			r.mu.Unlock()
			select {
			case <-ctx.Done():
			case <-time.After(20 * time.Millisecond):
			}
		},
	}
	t.Cleanup(r.pool.Close)
	return r
}

func (r *poolRig) status(t *testing.T, key string) runners.Status {
	t.Helper()
	for _, s := range r.pool.Status() {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("no slot %s", key)
	return runners.Status{}
}

func waitUntil(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func slotFor(repo string, k runners.Kind) runners.SlotConfig {
	rp, _ := runners.ParseRepo(repo)
	return runners.SlotConfig{Repo: rp, Kind: k, Enabled: true}
}

// One job, start to finish: a just-in-time registration with the OS labels, the runner waits, takes the job, its result
// is recorded, its files are removed, its registration is deleted, and a new registration waits for the next job.
func TestAPoolRunsOneJobPerRegistrationAndRegistersAgain(t *testing.T) {
	r := newRig(t, map[string]runnerstest.Repo{"kjmaguire/shop": {Private: true, Admin: true}})
	r.lin.JobTime = 50 * time.Millisecond
	r.pool.Set([]runners.SlotConfig{slotFor("kjmaguire/shop", runners.Linux)})
	key := "kjmaguire/shop:linux"
	waitUntil(t, "the first runner to wait for a job", func() bool { return r.status(t, key).State == runners.StateIdle })
	regs := r.gh.Registrations()
	if len(regs) != 1 || !strings.HasPrefix(regs[0].Name, "drydock-kyle-pc-linux-") ||
		!slices.Equal(regs[0].Labels, []string{"self-hosted", "Linux", "X64", "drydock"}) {
		t.Fatalf("%+v", regs)
	}
	if st := r.status(t, key); st.Runner != regs[0].Name || st.Version != "2.330.0" {
		t.Fatalf("%+v", st)
	}
	started := r.lin.StartedJobs()
	if len(started) != 1 || started[0].JIT.Encoded != regs[0].Config || started[0].Slot != key {
		t.Fatalf("the runner did not get its registration: %+v", started)
	}

	r.lin.Queue("build", "Succeeded")
	waitUntil(t, "the job to finish and a second runner to wait", func() bool {
		st := r.status(t, key)
		return st.Jobs == 1 && st.State == runners.StateIdle && len(r.gh.Registrations()) == 2
	})
	st := r.status(t, key)
	if st.LastJob == nil || st.LastJob.Name != "build" || st.LastJob.Result != "Succeeded" {
		t.Fatalf("%+v", st)
	}
	if c := r.lin.CleanedNames(); len(c) == 0 || c[0] != regs[0].Name {
		t.Fatalf("the finished job's files were not removed: %v", c)
	}
	if rm := r.gh.RemovedIDs(); !slices.Contains(rm, regs[0].ID) {
		t.Fatalf("the used registration was not deleted: %v", rm)
	}
	sawBusy := false
	r.mu.Lock()
	for _, e := range r.events {
		if e.State == runners.StateBusy && e.Job == "build" {
			sawBusy = true
		}
	}
	r.mu.Unlock()
	if !sawBusy {
		t.Fatal("no event said the job was running")
	}

	// switching the slot off stops the waiting runner and deletes its registration
	off := slotFor("kjmaguire/shop", runners.Linux)
	off.Enabled = false
	r.pool.Set([]runners.SlotConfig{off})
	if st := r.status(t, key); st.State != runners.StatePaused {
		t.Fatalf("%+v", st)
	}
	second := r.gh.Registrations()[1]
	if !slices.Contains(r.gh.RemovedIDs(), second.ID) {
		t.Fatal("the waiting runner's registration was left on GitHub")
	}
	n := len(r.gh.Registrations())
	time.Sleep(100 * time.Millisecond)
	if len(r.gh.Registrations()) != n {
		t.Fatal("a switched-off slot registered again")
	}
}

// Only private repositories: a public or internal one is never registered, and a repository made public later stops getting
// runners at its next registration.
func TestAPoolNeverRegistersARunnerOnAPublicRepository(t *testing.T) {
	r := newRig(t, map[string]runnerstest.Repo{
		"kjmaguire/drydock": {Private: false, Admin: true},
		"acme/internal":     {Private: true, Visibility: "internal", Admin: true},
		"kjmaguire/shop":    {Private: true, Admin: true},
	})
	r.pool.Set([]runners.SlotConfig{slotFor("kjmaguire/drydock", runners.Linux), slotFor("acme/internal", runners.Linux)})
	waitUntil(t, "both slots to be blocked", func() bool {
		return r.status(t, "kjmaguire/drydock:linux").State == runners.StateBlocked && r.status(t, "acme/internal:linux").State == runners.StateBlocked
	})
	if st := r.status(t, "kjmaguire/drydock:linux"); !strings.Contains(st.Reason, "public") || !strings.Contains(st.Reason, "fork") {
		t.Fatalf("%+v", st)
	}
	if len(r.gh.Registrations()) != 0 || len(r.lin.StartedJobs()) != 0 {
		t.Fatal("a runner was registered for a repository that is not private")
	}

	// a private repository that turns public: the job in progress ends, the next registration is refused
	r.pool.Set([]runners.SlotConfig{slotFor("kjmaguire/shop", runners.Linux)})
	waitUntil(t, "the runner to wait", func() bool { return r.status(t, "kjmaguire/shop:linux").State == runners.StateIdle })
	r.gh.SetRepo("kjmaguire/shop", runnerstest.Repo{Private: false, Admin: true})
	r.lin.Queue("build", "Succeeded")
	waitUntil(t, "the slot to be blocked", func() bool { return r.status(t, "kjmaguire/shop:linux").State == runners.StateBlocked })
	if len(r.gh.Registrations()) != 1 {
		t.Fatalf("registered again on a public repository: %+v", r.gh.Registrations())
	}
}

// A repository made public while its runner waits for a job loses the runner at the next look: stopped, its files removed, its
// registration deleted, the slot blocked. (Before this, the waiting runner stayed registered until the next job took it, and a
// fork's pull request could be that job.)
func TestAWaitingRunnerIsRemovedWhenItsRepositoryTurnsPublic(t *testing.T) {
	r := newRig(t, map[string]runnerstest.Repo{"kjmaguire/shop": {Private: true, Admin: true}})
	r.pool.RecheckEvery = 25 * time.Millisecond
	r.pool.Set([]runners.SlotConfig{slotFor("kjmaguire/shop", runners.Linux)})
	key := "kjmaguire/shop:linux"
	waitUntil(t, "the runner to wait", func() bool { return r.status(t, key).State == runners.StateIdle })
	reg := r.gh.Registrations()[0]
	time.Sleep(80 * time.Millisecond)
	if r.status(t, key).State != runners.StateIdle || len(r.gh.RemovedIDs()) != 0 {
		t.Fatal("a private repository's waiting runner must be left alone")
	}
	r.gh.SetRepo("kjmaguire/shop", runnerstest.Repo{Private: false, Admin: true})
	waitUntil(t, "the slot to be blocked", func() bool { return r.status(t, key).State == runners.StateBlocked })
	if !slices.Contains(r.gh.RemovedIDs(), reg.ID) {
		t.Fatal("the waiting runner's registration was left on GitHub")
	}
	if c := r.lin.CleanedNames(); len(c) == 0 || c[0] != reg.Name {
		t.Fatalf("the runner's files were not removed: %v", c)
	}
	if st := r.status(t, key); !strings.Contains(st.Reason, "public") {
		t.Fatalf("%+v", st)
	}
	if n := len(r.gh.Registrations()); n != 1 {
		t.Fatalf("registered again on a public repository: %d registrations", n)
	}
}

// The other reasons a slot waits: not enough free space where jobs run, no container engine, a repository the account does
// not administer, and GitHub not connected. None of them registers a runner.
func TestAPoolWaitsWhenItCannotRun(t *testing.T) {
	r := newRig(t, map[string]runnerstest.Repo{"kjmaguire/shop": {Private: true, Admin: true}, "acme/app": {Private: true, Admin: false}})
	r.win.FreeB = 3 << 30
	r.lin.Unavail = context.DeadlineExceeded
	r.pool.Set([]runners.SlotConfig{slotFor("kjmaguire/shop", runners.Windows), slotFor("kjmaguire/shop", runners.Linux), slotFor("acme/app", runners.Windows)})
	waitUntil(t, "all three to stop", func() bool {
		return r.status(t, "kjmaguire/shop:windows").State == runners.StateBlocked && r.status(t, "kjmaguire/shop:linux").State == runners.StateBlocked &&
			r.status(t, "acme/app:windows").State == runners.StateError
	})
	if st := r.status(t, "kjmaguire/shop:windows"); !strings.Contains(st.Reason, "3.0 GB is free") || !strings.Contains(st.Reason, "10 GB") {
		t.Fatalf("%+v", st)
	}
	if st := r.status(t, "acme/app:windows"); !strings.Contains(st.Reason, "does not administer") {
		t.Fatalf("%+v", st)
	}
	if len(r.gh.Registrations()) != 0 {
		t.Fatal("registered while it could not run")
	}
	// space is freed: the Windows slot carries on by itself
	r.win.FreeB = 50 << 30
	waitUntil(t, "the Windows runner to wait for a job", func() bool { return r.status(t, "kjmaguire/shop:windows").State == runners.StateIdle })

	gone := newRig(t, map[string]runnerstest.Repo{"kjmaguire/shop": {Private: true, Admin: true}})
	gone.pool.API = func(context.Context) (host.API, error) { return nil, context.Canceled }
	gone.pool.Set([]runners.SlotConfig{slotFor("kjmaguire/shop", runners.Linux)})
	waitUntil(t, "the slot to say GitHub is not connected", func() bool {
		return strings.Contains(gone.status(t, "kjmaguire/shop:linux").Reason, "sign in with gh")
	})
}

// A runner that stops at once without a job is a failure: its registration is deleted, its output's last line is the
// reason, and the pool waits longer each time before trying again.
func TestARunnerThatFailsBacksOffAndIsUnregistered(t *testing.T) {
	r := newRig(t, map[string]runnerstest.Repo{"kjmaguire/shop": {Private: true, Admin: true}})
	fail := &failingLauncher{Launcher: runnerstest.Launcher{K: runners.Linux}}
	r.pool.Launchers[runners.Linux] = fail
	r.pool.Set([]runners.SlotConfig{slotFor("kjmaguire/shop", runners.Linux)})
	// three failures, read while the slot shows the last one's reason: a new attempt clears the reason as it starts, so reading
	// right after the third registration could land in that moment (Windows CI did, 2026-10-20)
	var st runners.Status
	waitUntil(t, "three failures and the reason", func() bool {
		st = r.status(t, "kjmaguire/shop:linux")
		return len(r.gh.Registrations()) >= 3 && strings.Contains(st.Reason, "stopped without taking a job")
	})
	if !strings.Contains(st.Reason, "stopped without taking a job: An error occurred: bad configuration") {
		t.Fatalf("%+v", st)
	}
	for _, reg := range r.gh.Registrations()[:2] {
		if !slices.Contains(r.gh.RemovedIDs(), reg.ID) {
			t.Fatalf("registration %d was left", reg.ID)
		}
	}
	r.mu.Lock()
	waits := append([]time.Duration(nil), r.waits...)
	r.mu.Unlock()
	if len(waits) < 2 || waits[0] != 10*time.Second || waits[1] != 20*time.Second {
		t.Fatalf("the pauses do not grow: %v", waits)
	}
}

type failingLauncher struct{ runnerstest.Launcher }

type failed struct {
	lines chan string
	exit  chan int
}

func (f *failed) Lines() <-chan string { return f.lines }
func (f *failed) Wait() <-chan int     { return f.exit }
func (f *failed) Stop()                {}

func (l *failingLauncher) Start(context.Context, runners.Job) (runners.Running, error) {
	f := &failed{lines: make(chan string, 2), exit: make(chan int, 1)}
	f.lines <- "An error occurred: bad configuration"
	close(f.lines)
	f.exit <- 1
	return f, nil
}

// On its first registration for a repository the pool deletes this PC's runners GitHub still lists as offline (left when
// Drydock stopped without cleaning up), and nobody else's.
func TestAPoolRemovesThisPCsOfflineRunnersOnly(t *testing.T) {
	r := newRig(t, map[string]runnerstest.Repo{"kjmaguire/shop": {Private: true, Admin: true}})
	r.gh.Listed["kjmaguire/shop"] = []runners.Registered{
		{ID: 1, Name: "drydock-kyle-pc-linux-aaaaaa", Status: "offline"},
		{ID: 2, Name: "drydock-kyle-pc-linux-bbbbbb", Status: "online"},
		{ID: 3, Name: "drydock-other-pc-linux-cccccc", Status: "offline"},
		{ID: 4, Name: "build-box", Status: "offline"},
	}
	r.pool.Set([]runners.SlotConfig{slotFor("kjmaguire/shop", runners.Linux)})
	waitUntil(t, "the runner to wait", func() bool { return r.status(t, "kjmaguire/shop:linux").State == runners.StateIdle })
	if rm := r.gh.RemovedIDs(); !slices.Equal(rm, []int64{1}) {
		t.Fatalf("removed %v", rm)
	}
	if r.lin.Swept != 1 {
		t.Fatalf("the launcher's leftovers were swept %d times", r.lin.Swept)
	}
}
