package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/license"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// ErrReadOnly is wrapped by every refusal that comes from the license state, so callers can recognize it with errors.Is.
var ErrReadOnly = errors.New("drydock is read-only")

// LicenseError is the plain-language refusal returned for work that the current license state does not allow.
type LicenseError struct {
	Action  string
	State   license.State
	Message string
}

func (e *LicenseError) Error() string { return e.Message }

// Is lets errors.Is(err, ErrReadOnly) recognize it.
func (e *LicenseError) Is(target error) bool { return target == ErrReadOnly }

var actionPhrases = map[string]string{
	"start_task": "starting tasks", "start_template": "starting tasks", "start_work": "starting agents and workflows", "approve": "approving agent actions",
	"create_agent": "adding agents", "create_project": "creating projects", "add_connection": "adding connections", "room_reply": "agent replies in the room",
	"worktree": "creating worktrees", "merge": "merging", "pull_request": "creating pull requests",
}

// licState is one evaluation of the license.
type licState struct {
	configured bool
	status     license.Status
	view       LicenseView
}

// allows reports whether an action may proceed. A build without licensing keys allows everything.
func (s licState) allows(action string) bool {
	if !s.configured {
		return true
	}
	return s.status.Allows(action)
}

// LicenseView is what license.status returns. It never contains the token.
type LicenseView struct {
	Configured   bool          `json:"configured"`
	DevMode      bool          `json:"devMode"`
	State        license.State `json:"state"`
	ReadOnly     bool          `json:"readOnly"`
	Message      string        `json:"message"`
	Banner       string        `json:"banner,omitempty"` // non-empty when the UI shows a persistent banner
	DaysLeft     int           `json:"daysLeft"`
	ExpiresAt    int64         `json:"expiresAt,omitempty"` // unix seconds
	GraceDays    int           `json:"graceDays,omitempty"`
	Kind         string        `json:"kind,omitempty"`
	Plan         string        `json:"plan,omitempty"`
	Name         string        `json:"name,omitempty"`
	Subject      string        `json:"subject,omitempty"`
	ID           string        `json:"licenseId,omitempty"`
	Channels     []string      `json:"channels"`
	Features     []string      `json:"features,omitempty"`
	ClockSuspect bool          `json:"clockSuspect"`
	LastSeen     int64         `json:"lastSeen,omitempty"` // highest time the app has observed, unix seconds
}

func (p *packaging) configured() bool { return len(p.keySet()) > 0 }

func (p *packaging) keySet() license.Keys {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.keys
}

func (p *packaging) clock() time.Time {
	p.mu.Lock()
	f := p.now
	p.mu.Unlock()
	return f()
}

// evaluate derives the license state now. It advances and persists the highest-seen time (the clock guard).
func (p *packaging) evaluate(ctx context.Context) licState {
	now := p.clock()
	p.mu.Lock()
	maxSeen := p.maxSeen
	p.mu.Unlock()
	st := licState{configured: p.configured()}
	v := LicenseView{Configured: st.configured, DevMode: !st.configured, Channels: []string{"stable"}}
	if maxSeen.After(now.Add(24 * time.Hour)) {
		v.ClockSuspect = true
	}
	if !st.configured {
		v.State = license.StateNone
		v.Message = "Licensing is not configured in this build. This is a development or unlicensed build, so every feature is allowed."
		v.Banner = "LICENSING NOT CONFIGURED: development build, nothing is restricted."
		v.Channels = []string{"stable", "beta"}
		st.view = v
		return st
	}
	var payload *license.Payload
	var perr error
	if tk, ok := p.getSetting(ctx, keyLicenseToken); ok && tk != "" {
		payload, perr = p.parseCached(tk)
	}
	if perr != nil {
		st.status = license.Status{State: license.StateNone, Message: "The installed license could not be verified (" + friendlyLicenseError(perr) + "). Import a valid license."}
	} else {
		s, err := license.Evaluate(payload, now, maxSeen)
		if err != nil {
			st.status = license.Status{State: license.StateNone, Message: "The installed license is not valid yet. " + friendlyLicenseError(err)}
		} else {
			st.status = s
		}
	}
	p.advanceMaxSeen(ctx, now)
	v.State, v.Message, v.ReadOnly, v.DaysLeft, v.ExpiresAt = st.status.State, st.status.Message, st.status.ReadOnly(), st.status.DaysLeft, st.status.ExpiresAt
	if pl := st.status.Payload; pl != nil {
		v.Kind, v.Plan, v.Name, v.Subject, v.ID, v.GraceDays, v.Features = pl.Kind, pl.Plan, pl.Name, pl.Sub, pl.JTI, pl.GraceDays, pl.Features
		if !st.status.ReadOnly() {
			v.Channels = nil
			for _, ch := range []string{"stable", "beta"} {
				if pl.ChannelAllowed(ch) {
					v.Channels = append(v.Channels, ch)
				}
			}
		}
	}
	switch st.status.State {
	case license.StateGrace:
		v.Banner = st.status.Message
	case license.StateExpired:
		v.Banner = "License expired: Drydock is read-only. You can read, export and back up everything; nothing has been deleted. Import a renewed license to continue."
	case license.StateNone:
		v.Banner = "No valid license: Drydock is read-only. Import a license or trial to start work."
	}
	if v.ClockSuspect {
		v.Message += " The system clock appears to have been set back; the license is judged by the latest time Drydock has seen."
	}
	v.LastSeen = maxSeen.Unix()
	st.view = v
	return st
}

func (p *packaging) parseCached(tk string) (*license.Payload, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cachedTk == tk {
		return p.cachedP, p.cachedE
	}
	pl, err := license.Parse(tk, p.keys)
	p.cachedTk = tk
	if err != nil {
		p.cachedP, p.cachedE = nil, err
	} else {
		p.cachedP, p.cachedE = &pl, nil
	}
	return p.cachedP, p.cachedE
}

// advanceMaxSeen records the highest time ever observed (never moves back) and persists it when it moved a minute or more.
func (p *packaging) advanceMaxSeen(ctx context.Context, now time.Time) {
	p.mu.Lock()
	if now.After(p.maxSeen) {
		p.maxSeen = now
	}
	save := p.maxSeen.Sub(p.savedMax) >= time.Minute
	v := p.maxSeen
	if save {
		p.savedMax = v
	}
	p.mu.Unlock()
	if save {
		_ = p.setSetting(ctx, keyLicenseMaxSeen, strconv.FormatInt(v.Unix(), 10))
	}
}

func friendlyLicenseError(err error) string {
	switch {
	case errors.Is(err, license.ErrSignature):
		return "its signature does not match any key built into this version of Drydock"
	case errors.Is(err, license.ErrFormat):
		return "this is not a Drydock license token"
	case errors.Is(err, license.ErrNotYet):
		return "its start date is in the future; check your system clock"
	}
	return err.Error()
}

// RequireLicensed returns a plain-language error (wrapping ErrReadOnly) when the license state does not allow the action.
// Reading, export, backup, restore, support bundles and license import are always allowed; in the expired state (and with no
// license in a build that has licensing keys) starting work is not. A build without licensing keys allows everything.
func (c *Core) RequireLicensed(ctx context.Context, action string) error {
	p := c.pkg()
	if p == nil {
		return nil
	}
	return p.require(ctx, action)
}

func (p *packaging) require(ctx context.Context, action string) error {
	st := p.evaluate(ctx)
	if st.allows(action) {
		return nil
	}
	what := actionPhrases[action]
	if what == "" {
		what = "that action"
	}
	var why string
	switch st.status.State {
	case license.StateExpired:
		why = "Your license has expired, so Drydock is read-only"
	default:
		why = "There is no valid license, so Drydock is read-only"
	}
	return &LicenseError{Action: action, State: st.status.State, Message: fmt.Sprintf(
		"%s: %s is turned off. You can still read everything, export, back up, restore and create a support bundle, and nothing has been deleted. Import a license in Settings > License to continue.", why, what)}
}

// RetentionAllowed reports whether data may be deleted by retention (docs/packaging.md s6 and s10, decision P12). The
// retention compactor and deleter (raw output and checkpoints of completed runs: compacted after 30 days, deleted after 90)
// MUST call this before every compaction or deletion and do nothing when it returns false: while the license is expired, or
// absent in a build that has licensing keys, nothing is deleted because of the license state. There is no compactor yet; this
// is the contract it plugs into.
func (c *Core) RetentionAllowed() bool {
	p := c.pkg()
	if p == nil {
		return true
	}
	return p.evaluate(context.Background()).allows("retention")
}

// licenseChannelAllowed is Core.ChannelAllowed: the license's channels; stable only when there is no usable license; every
// channel in a build without licensing keys.
func (c *Core) licenseChannelAllowed(ch string) bool {
	p := c.pkg()
	if p == nil {
		return ch == "stable"
	}
	st := p.evaluate(context.Background())
	if !st.configured {
		return true
	}
	if st.status.Payload == nil || st.status.ReadOnly() {
		return ch == "stable"
	}
	return st.status.Payload.ChannelAllowed(ch)
}

// gatedMethods are the registry methods that start work or approve it, and the action each is checked as.
var gatedMethods = map[string]string{
	"task.start": "start_task", "task.startTemplate": "start_template", "plan.propose": "start_work", "plan.accept": "start_work",
	"scheduler.tick": "start_work", "room.reply": "room_reply", "check.e2e": "start_work", "preview.start": "start_work", "budget.resume": "start_work",
	"run.respond": "approve", "integration.outbound.decide": "approve", "integration.outbound.execute": "approve",
	"agent.create": "create_agent", "project.create": "create_project", "provider.create": "add_connection", "integration.connect": "add_connection",
}

// gateMethods wraps the gated methods of a registry so an expired license refuses them before they run.
func (c *Core) gateMethods(m map[string]Method) {
	if c.pkg() == nil {
		return
	}
	for name, action := range gatedMethods {
		inner, ok := m[name]
		if !ok {
			continue
		}
		action, inner := action, inner
		m[name] = func(ctx context.Context, params json.RawMessage) (any, error) {
			if err := c.RequireLicensed(ctx, action); err != nil {
				return nil, err
			}
			return inner(ctx, params)
		}
	}
}

// nodeGate is workflow middleware for agent turns: a node that would start while the license is read-only waits (and asks
// for the run to be suspended) instead of running. The suspension cancels its context.
func (p *packaging) nodeGate(next workflow.NodeHandler) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		for {
			if p.c.RequireLicensed(ctx, "start_work") == nil {
				return next(ctx, in)
			}
			p.kick()
			select {
			case <-ctx.Done():
				return workflow.NodeOutput{}, ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
}

func (p *packaging) kick() {
	select {
	case p.kickCh <- struct{}{}:
	default:
	}
}

func (p *packaging) licenseLoop(ctx context.Context) {
	t := time.NewTicker(p.licenseTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-p.kickCh:
		}
		p.reconcile(ctx)
	}
}

// reconcile applies the license state to the running app: expired work is parked, renewed work resumes, and a change of state
// is announced once.
func (p *packaging) reconcile(ctx context.Context) {
	st := p.evaluate(ctx)
	if !st.configured {
		return
	}
	c := p.c
	if st.status.ReadOnly() {
		n := p.suspendRuns(ctx)
		if n > 0 {
			c.publish(ctx, "license.runs_suspended", "", map[string]any{"runs": n, "state": st.status.State})
			p.notify(ctx, fmt.Sprintf("license-suspended:%s:%d", st.status.State, time.Now().UnixNano()),
				fmt.Sprintf("The license is %s, so %d running workflow run(s) were stopped cleanly. Their checkpoints are kept and they resume when a valid license is imported.", st.status.State, n), false)
		}
	} else if c.Engine != nil {
		if n, err := c.Engine.ResumeSuspended(ctx); err != nil {
			c.logf("resume suspended runs: %v", err)
		} else if n > 0 {
			c.publish(ctx, "license.runs_resumed", "", map[string]any{"runs": n})
			p.notify(ctx, fmt.Sprintf("license-resumed:%d", time.Now().UnixNano()), fmt.Sprintf("The license is valid again: %d suspended workflow run(s) resumed from their checkpoints.", n), false)
		}
	}
	p.announce(ctx, st)
}

// suspendRuns parks every executing workflow run (agent processes are stopped with the run's context, which kills their
// process tree). Runs waiting for a human are left alone: they keep their pending request and survive.
func (p *packaging) suspendRuns(ctx context.Context) int {
	c := p.c
	if c.Engine == nil {
		return 0
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id FROM workflow_runs WHERE status IN ('RUNNING','RECOVERING','PENDING')`)
	if err != nil {
		return 0
	}
	var runs []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			runs = append(runs, id)
		}
	}
	rows.Close()
	n := 0
	for _, id := range runs {
		ok, err := c.Engine.Suspend(ctx, workflow.RunID(id), "the license is read-only; the run resumes when a valid license is imported")
		if err != nil {
			c.logf("suspend run %s: %v", id, err)
			continue
		}
		if ok {
			n++
		}
	}
	return n
}

// announce posts a notice and publishes an event when the license state changed since it was last announced.
func (p *packaging) announce(ctx context.Context, st licState) {
	key := string(st.status.State)
	if st.status.Payload != nil {
		key += ":" + st.status.Payload.JTI
	}
	if last, _ := p.getSetting(ctx, keyLicenseAnnounced); last == key {
		return
	}
	if err := p.setSetting(ctx, keyLicenseAnnounced, key); err != nil {
		return
	}
	p.c.publish(ctx, "license.state_changed", "", map[string]any{"state": st.status.State, "readOnly": st.status.ReadOnly(), "message": st.status.Message})
	switch st.status.State {
	case license.StateGrace, license.StateExpired, license.StateNone:
		p.notify(ctx, "license:"+key, st.view.Banner, true)
	}
}

// ---- methods ----

type licenseImportReq struct {
	Token string `json:"token"`
	Path  string `json:"path"` // a .drydock-license file, instead of a pasted token
}

func (p *packaging) licenseMethods(m map[string]Method) {
	m["license.status"] = bind(func(ctx context.Context, _ struct{}) (any, error) { return p.evaluate(ctx).view, nil })
	m["license.import"] = bind(func(ctx context.Context, r licenseImportReq) (any, error) { return p.importLicense(ctx, r) })
	m["license.clear"] = bind(func(ctx context.Context, _ struct{}) (any, error) {
		if !p.configured() {
			return nil, errors.New("licensing is not configured in this build, so there is no license to clear")
		}
		if err := p.delSetting(ctx, keyLicenseToken); err != nil {
			return nil, err
		}
		p.audit(ctx, "license.clear", "", nil)
		p.reconcile(ctx)
		return p.evaluate(ctx).view, nil
	})
}

// importLicense verifies a pasted token (or a .drydock-license file) against the embedded keys and installs it. A newer valid
// token replaces an older one; an older or already expired token never replaces a working license.
func (p *packaging) importLicense(ctx context.Context, r licenseImportReq) (LicenseView, error) {
	if !p.configured() {
		return LicenseView{}, errors.New("licensing is not configured in this build, so a license cannot be imported. This build allows every feature")
	}
	token := strings.TrimSpace(r.Token)
	if token == "" && r.Path != "" {
		b, err := os.ReadFile(r.Path)
		if err != nil {
			return LicenseView{}, fmt.Errorf("could not read the license file: %w", err)
		}
		if len(b) > 64<<10 {
			return LicenseView{}, errors.New("that file is too large to be a license")
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return LicenseView{}, errors.New("paste a license token or choose a .drydock-license file")
	}
	pl, err := license.Parse(token, p.keySet())
	if err != nil {
		return LicenseView{}, errors.New("this license was not accepted: " + friendlyLicenseError(err))
	}
	p.mu.Lock()
	maxSeen := p.maxSeen
	p.mu.Unlock()
	st, err := license.Evaluate(&pl, p.clock(), maxSeen)
	if err != nil {
		return LicenseView{}, errors.New("this license was not accepted: " + friendlyLicenseError(err))
	}
	if cur := p.evaluate(ctx); cur.status.Payload != nil {
		if pl.IAT < cur.status.Payload.IAT {
			return LicenseView{}, errors.New("this license is older than the one installed (issued earlier), so it was not used")
		}
		if st.State == license.StateExpired && !cur.status.ReadOnly() {
			return LicenseView{}, errors.New("this license has already expired, and the installed one is still working, so it was not used")
		}
	}
	if err := p.setSetting(ctx, keyLicenseToken, token); err != nil {
		return LicenseView{}, err
	}
	_ = p.c.Scanner.RegisterSecret("license", token)
	p.audit(ctx, "license.import", pl.JTI, map[string]any{"kind": pl.Kind, "plan": pl.Plan, "exp": pl.EXP, "state": st.State})
	p.reconcile(ctx)
	return p.evaluate(ctx).view, nil
}
