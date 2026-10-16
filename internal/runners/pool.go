package runners

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/safe"
)

// SlotConfig is one runner a person asked for: a repository and where its jobs run.
type SlotConfig struct {
	Repo    Repo      `json:"repo"`
	Kind    Kind      `json:"kind"`
	Enabled bool      `json:"enabled"`
	By      string    `json:"by,omitempty"`
	At      time.Time `json:"at,omitempty"`
}

// Key names the slot: "owner/name:windows".
func (s SlotConfig) Key() string { return s.Repo.String() + ":" + string(s.Kind) }

// State of a slot.
type State string

const (
	StatePaused   State = "paused"   // switched off by a person
	StateStarting State = "starting" // getting the runner ready or registering it
	StateIdle     State = "idle"     // registered and waiting for a job
	StateBusy     State = "busy"     // running a job
	StateBlocked  State = "blocked"  // cannot run now (not private, no engine, low disk, GitHub not connected); looked at again
	StateError    State = "error"    // something failed; tried again after a pause that grows
)

// JobResult is a job a runner finished.
type JobResult struct {
	Name   string    `json:"name"`
	Result string    `json:"result"` // Succeeded, Failed, Canceled
	At     time.Time `json:"at"`
}

// Status is what the Connections page shows for a slot.
type Status struct {
	Key     string     `json:"key"`
	Repo    string     `json:"repo"`
	Kind    Kind       `json:"kind"`
	Enabled bool       `json:"enabled"`
	State   State      `json:"state"`
	Reason  string     `json:"reason,omitempty"`
	Runner  string     `json:"runner,omitempty"`
	Job     string     `json:"job,omitempty"`
	LastJob *JobResult `json:"lastJob,omitempty"`
	Jobs    int        `json:"jobs"`
	Version string     `json:"version,omitempty"`
	Since   time.Time  `json:"since"`
}

// Pool keeps one ephemeral runner registered for each enabled slot: register, run one job, clean up, register again.
type Pool struct {
	// API is GitHub as the signed-in account (gh), asked again for every registration.
	API       func(ctx context.Context) (host.API, error)
	Launchers map[Kind]Launcher
	// Release gives the runner release to use; nil reads the newest from GitHub (kept for ReleaseTTL).
	Release    func(ctx context.Context, api host.API) (Release, error)
	ReleaseTTL time.Duration // default 1 hour
	MinFree    uint64        // jobs wait while less is free where they write; default 10 GiB
	Machine    string        // this PC's name in runner names; default the host name
	Publish    func(Status)  // called on every change
	Now        func() time.Time
	Wait       func(ctx context.Context, d time.Duration) // default a timer
	Retry      time.Duration                              // pause before looking again at a blocked slot; default 1 minute
	MaxBackoff time.Duration                              // default 10 minutes
	// RecheckEvery is how often a runner that is waiting for a job asks again whether its repository is still private (default
	// 3 minutes). A repository made public while a runner waits is unregistered and stopped at the next look, not at the next job.
	RecheckEvery time.Duration

	mu      sync.Mutex
	slots   map[string]*slot
	rel     Release
	relAt   time.Time
	swept   map[Kind]bool
	closing bool
}

type slot struct {
	cfg    SlotConfig
	st     Status
	cancel context.CancelFunc
	done   chan struct{}
	log    []string
}

// DefaultMinFree is the free space a job needs before it starts.
const DefaultMinFree = 10 << 30

func (p *Pool) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pool) wait(ctx context.Context, d time.Duration) {
	if p.Wait != nil {
		p.Wait(ctx, d)
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

var machineRe = regexp.MustCompile(`[^a-z0-9-]+`)

// MachineName is the host name as runner names use it: lower case letters, digits and dashes, at most 20.
func MachineName(name string) string {
	n := strings.Trim(machineRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(n) > 20 {
		n = strings.Trim(n[:20], "-")
	}
	if n == "" {
		n = "pc"
	}
	return n
}

func (p *Pool) machine() string {
	if p.Machine != "" {
		return MachineName(p.Machine)
	}
	h, _ := os.Hostname()
	return MachineName(h)
}

// NamePrefix is how this PC's runners of a kind are named on GitHub.
func (p *Pool) NamePrefix(k Kind) string { return "drydock-" + p.machine() + "-" + string(k) + "-" }

// Set makes the running slots match cfgs: new enabled slots start, removed or switched-off ones stop (their runner is
// stopped and unregistered).
func (p *Pool) Set(cfgs []SlotConfig) {
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		return
	}
	if p.slots == nil {
		p.slots = map[string]*slot{}
	}
	want := map[string]SlotConfig{}
	for _, c := range cfgs {
		want[c.Key()] = c
	}
	var stop []*slot
	for k, s := range p.slots {
		c, ok := want[k]
		if !ok || !c.Enabled {
			if s.cancel != nil {
				stop = append(stop, s)
			}
			if !ok {
				delete(p.slots, k)
			}
		}
	}
	var start []*slot
	for k, c := range want {
		s := p.slots[k]
		if s == nil {
			s = &slot{cfg: c, st: Status{Key: k, Repo: c.Repo.String(), Kind: c.Kind, State: StatePaused, Since: p.now()}}
			p.slots[k] = s
		}
		s.cfg = c
		s.st.Enabled = c.Enabled
		if c.Enabled && s.cancel == nil {
			start = append(start, s)
		}
		if !c.Enabled && s.cancel == nil {
			s.st.State, s.st.Reason, s.st.Job, s.st.Runner = StatePaused, "", "", ""
		}
	}
	p.mu.Unlock()
	for _, s := range stop {
		p.stopSlot(s)
	}
	for _, s := range start {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		p.mu.Lock()
		s.cancel, s.done = cancel, done
		p.mu.Unlock()
		safe.Go("runners.Pool.Set", func() {
			func(s *slot) {
				defer close(done)
				p.run(ctx, s)
			}(s)
		})
	}
}

func (p *Pool) stopSlot(s *slot) {
	p.mu.Lock()
	cancel, done := s.cancel, s.done
	p.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	p.mu.Lock()
	s.cancel, s.done = nil, nil
	enabled := false
	if cur, ok := p.slots[s.st.Key]; ok && cur == s {
		enabled = s.cfg.Enabled
	}
	if !enabled {
		s.st.State, s.st.Reason, s.st.Job, s.st.Runner, s.st.Since = StatePaused, "", "", "", p.now()
	}
	st := s.st
	p.mu.Unlock()
	p.publish(st)
}

// Close stops every runner and unregisters it.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closing = true
	var all []*slot
	for _, s := range p.slots {
		all = append(all, s)
	}
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Add(1)
		safe.Go("runners.Pool.Close", func() {
			func(s *slot) {
				defer wg.Done()
				p.stopSlot(s)
			}(s)
		})
	}
	wg.Wait()
}

// Status lists the slots, by repository then kind.
func (p *Pool) Status() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Status, 0, len(p.slots))
	for _, s := range p.slots {
		out = append(out, s.st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Log is the end of a slot's runner output (at most 200 lines).
func (p *Pool) Log(key string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.slots[key]; s != nil {
		return append([]string(nil), s.log...)
	}
	return nil
}

func (p *Pool) publish(st Status) {
	if p.Publish != nil {
		p.Publish(st)
	}
}

func (p *Pool) update(s *slot, f func(st *Status)) {
	p.mu.Lock()
	before := s.st
	f(&s.st)
	if s.st.State != before.State {
		s.st.Since = p.now()
	}
	st := s.st
	changed := st.State != before.State || st.Reason != before.Reason || st.Job != before.Job || st.Runner != before.Runner ||
		st.Jobs != before.Jobs || st.Version != before.Version
	p.mu.Unlock()
	if changed {
		p.publish(st)
	}
}

func (p *Pool) addLog(s *slot, line string) {
	p.mu.Lock()
	s.log = append(s.log, line)
	if len(s.log) > 200 {
		s.log = s.log[len(s.log)-200:]
	}
	p.mu.Unlock()
}

// blocked is a reason a slot cannot run right now, looked at again after Retry rather than counted as a failure.
type blocked struct{ msg string }

func (b blocked) Error() string { return b.msg }

func (p *Pool) run(ctx context.Context, s *slot) {
	fails := 0
	for ctx.Err() == nil {
		ran, err := p.once(ctx, s)
		if ctx.Err() != nil {
			return
		}
		var b blocked
		switch {
		case err == nil:
			fails = 0
			continue
		case errors.As(err, &b):
			p.update(s, func(st *Status) { st.State, st.Reason, st.Job, st.Runner = StateBlocked, b.msg, "", "" })
			p.wait(ctx, p.retry())
		default:
			if ran {
				fails = 0
			}
			fails++
			p.update(s, func(st *Status) { st.State, st.Reason, st.Job, st.Runner = StateError, err.Error(), "", "" })
			p.wait(ctx, p.backoff(fails))
		}
	}
}

func (p *Pool) retry() time.Duration {
	if p.Retry > 0 {
		return p.Retry
	}
	return time.Minute
}

func (p *Pool) backoff(n int) time.Duration {
	max := p.MaxBackoff
	if max <= 0 {
		max = 10 * time.Minute
	}
	d := 10 * time.Second
	for i := 1; i < n && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}

func (p *Pool) release(ctx context.Context, api host.API) (Release, error) {
	ttl := p.ReleaseTTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	p.mu.Lock()
	if p.rel.Version != "" && p.now().Sub(p.relAt) < ttl {
		r := p.rel
		p.mu.Unlock()
		return r, nil
	}
	p.mu.Unlock()
	get := p.Release
	if get == nil {
		get = LatestRelease
	}
	r, err := get(ctx, api)
	if err != nil {
		return Release{}, err
	}
	p.mu.Lock()
	p.rel, p.relAt = r, p.now()
	p.mu.Unlock()
	return r, nil
}

// sweep runs once per kind: it removes what an earlier run of Drydock left behind (folders, containers) and this PC's
// runners of the repository that GitHub still lists as offline.
func (p *Pool) sweep(ctx context.Context, api host.API, s *slot, l Launcher) {
	p.mu.Lock()
	if p.swept == nil {
		p.swept = map[Kind]bool{}
	}
	first := !p.swept[l.Kind()]
	p.swept[l.Kind()] = true
	p.mu.Unlock()
	if first {
		l.Sweep(ctx)
	}
	old, err := ListRunners(ctx, api, s.cfg.Repo, p.NamePrefix(s.cfg.Kind))
	if err != nil {
		return
	}
	for _, r := range old {
		if r.Status == "offline" {
			_ = RemoveRunner(ctx, api, s.cfg.Repo, r.ID)
		}
	}
}

var (
	runningJob = regexp.MustCompile(`Running job: (.+?)\s*$`)
	doneJob    = regexp.MustCompile(`Job (.+?) completed with result: ([A-Za-z]+)`)
)

func randomSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// once registers one runner, runs it until it exits, and cleans up. ran reports whether a job ran.
func (p *Pool) once(ctx context.Context, s *slot) (ran bool, err error) {
	cfg := s.cfg
	l := p.Launchers[cfg.Kind]
	if l == nil {
		return false, blocked{fmt.Sprintf("%s runners are not available on this PC", cfg.Kind)}
	}
	p.update(s, func(st *Status) { st.State, st.Reason = StateStarting, "" })
	if p.API == nil {
		return false, blocked{"GitHub is not connected: sign in with gh on the Connections page"}
	}
	api, err := p.API(ctx)
	if err != nil || api == nil {
		return false, blocked{"GitHub is not connected: sign in with gh on the Connections page"}
	}
	if _, err := CheckRepo(ctx, api, cfg.Repo); err != nil {
		if errors.Is(err, ErrPublic) {
			return false, blocked{err.Error()}
		}
		return false, err
	}
	if err := l.Available(ctx); err != nil {
		return false, blocked{err.Error()}
	}
	min := p.MinFree
	if min == 0 {
		min = DefaultMinFree
	}
	if free, ok := l.Free(); ok && free < min {
		return false, blocked{fmt.Sprintf("only %.1f GB is free where jobs run; jobs wait until %.0f GB is free", float64(free)/(1<<30), float64(min)/(1<<30))}
	}
	p.sweep(ctx, api, s, l)
	rel, err := p.release(ctx, api)
	if err != nil {
		return false, err
	}
	p.update(s, func(st *Status) { st.Version = rel.Version })
	if err := l.Prepare(ctx, rel); err != nil {
		return false, err
	}
	name := p.NamePrefix(cfg.Kind) + randomSuffix()
	osLabel := "Windows"
	if cfg.Kind == Linux {
		osLabel = "Linux"
	}
	jit, err := RegisterJIT(ctx, api, cfg.Repo, name, []string{"self-hosted", osLabel, l.Arch(), "drydock"})
	if err != nil {
		return false, err
	}
	job := Job{Slot: cfg.Key(), Name: name, JIT: jit, Version: rel.Version}
	unregister := func() {
		rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = RemoveRunner(rctx, api, cfg.Repo, jit.RunnerID)
	}
	started := p.now()
	run, err := l.Start(ctx, job)
	if err != nil {
		unregister()
		_ = l.Cleanup(job)
		return false, fmt.Errorf("could not start the runner: %w", err)
	}
	p.update(s, func(st *Status) { st.State, st.Reason, st.Runner, st.Job = StateIdle, "", name, "" })
	var last string
	handle := func(line string) {
		p.addLog(s, line)
		if t := strings.TrimSpace(line); t != "" {
			last = t
		}
		if m := doneJob.FindStringSubmatch(line); m != nil {
			ran = true
			res := &JobResult{Name: m[1], Result: m[2], At: p.now()}
			p.update(s, func(st *Status) { st.LastJob, st.Job, st.Jobs = res, "", st.Jobs+1 })
		} else if m := runningJob.FindStringSubmatch(line); m != nil {
			p.update(s, func(st *Status) { st.State, st.Job = StateBusy, m[1] })
		}
	}
	lines := run.Lines()
	exit := run.Wait()
	code, exited := 0, false
	every := p.RecheckEvery
	if every <= 0 {
		every = 3 * time.Minute
	}
	recheck := time.NewTicker(every)
	defer recheck.Stop()
	for !exited {
		select {
		case <-recheck.C:
			// A runner that waits for a job on a repository that has since become public would take a fork's pull request.
			// One that is busy finishes its job (it is ephemeral and is not registered again).
			p.mu.Lock()
			idle := s.st.State == StateIdle
			p.mu.Unlock()
			if !idle {
				continue
			}
			if _, cerr := CheckRepo(ctx, api, cfg.Repo); !errors.Is(cerr, ErrPublic) {
				continue
			} else {
				run.Stop()
				select {
				case <-exit:
				case <-time.After(30 * time.Second):
				}
				if lines != nil {
					for range lines {
					}
				}
				_ = l.Cleanup(job)
				unregister()
				return ran, blocked{cerr.Error()}
			}
		case <-ctx.Done():
			run.Stop()
			select {
			case <-exit:
			case <-time.After(30 * time.Second):
			}
			if lines != nil {
				for range lines {
				}
			}
			_ = l.Cleanup(job)
			unregister()
			return ran, ctx.Err()
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			handle(line)
		case code = <-exit:
			exited = true
		}
	}
	// nothing a job started outlives its runner (on Windows the process's job object already ended them)
	run.Stop()
	if lines != nil {
		for line := range lines {
			handle(line)
		}
	}
	cerr := l.Cleanup(job)
	unregister()
	if !ran && (code != 0 || p.now().Sub(started) < time.Minute) {
		if last == "" {
			last = fmt.Sprintf("exit code %d", code)
		}
		return false, fmt.Errorf("the runner stopped without taking a job: %s", clip(last, 300))
	}
	if cerr != nil {
		return ran, fmt.Errorf("could not remove the finished job's files: %w", cerr)
	}
	return ran, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
