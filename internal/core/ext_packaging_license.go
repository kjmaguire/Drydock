package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/license"
	"github.com/kjmaguire/drydock/internal/updatepolicy"
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
	"worktree": "creating worktrees", "merge": "merging", "pull_request": "creating pull requests", "chat": "starting chats and sending chat messages",
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

// maxLicenseFile is the largest .drydock-license file that is read.
const maxLicenseFile = 64 << 10

// keysBrokenMessage is said when the build's embedded keys are set and cannot be read: licensing is on, and nothing can be
// verified, so Drydock stays read-only instead of allowing everything as an unlicensed development build does.
const keysBrokenMessage = "This build of Drydock cannot check licenses: its built-in license keys are unreadable, so it is read-only. Reinstall Drydock from the official installer."

// configured reports whether licensing is on in this build: it has license keys, or it was given some it cannot read.
func (p *packaging) configured() bool { return len(p.keySet()) > 0 || p.keysBroken }

// appWindow says why a license does not cover this build ("" when it does): its optional min_app and max_app name the oldest
// and newest Drydock version it is good for (audit 2026-10-16, L3). A version that cannot be read is not held against it.
func appWindow(pl *license.Payload, version string) string {
	if pl == nil {
		return ""
	}
	if pl.MinApp != "" {
		if c, err := updatepolicy.CompareVersions(version, pl.MinApp); err == nil && c < 0 {
			return fmt.Sprintf("This license is for Drydock %s or later, and this is %s. Install a newer Drydock or import a license for this version.", pl.MinApp, version)
		}
	}
	if pl.MaxApp != "" {
		if c, err := updatepolicy.CompareVersions(version, pl.MaxApp); err == nil && c > 0 {
			return fmt.Sprintf("This license is for Drydock up to %s, and this is %s. Import a license for this version.", pl.MaxApp, version)
		}
	}
	return ""
}

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
		// no banner: nothing is restricted, so nothing on every screen needs saying; the License page and the startup log say it
		v.Channels = []string{"stable", "beta"}
		st.view = v
		return st
	}
	var payload *license.Payload
	var perr error
	if tk, ok := p.getSetting(ctx, keyLicenseToken); ok && tk != "" {
		payload, perr = p.parseCached(tk)
	}
	switch {
	case p.keysBroken:
		st.status = license.Status{State: license.StateNone, Message: keysBrokenMessage}
	case perr != nil:
		st.status = license.Status{State: license.StateNone, Message: "The installed license could not be verified (" + friendlyLicenseError(perr) + "). Import a valid license."}
	default:
		s, err := license.Evaluate(payload, now, maxSeen)
		if err != nil {
			st.status = license.Status{State: license.StateNone, Message: "The installed license is not valid yet. " + friendlyLicenseError(err)}
		} else if why := appWindow(payload, AppVersion); why != "" {
			st.status = license.Status{State: license.StateNone, Payload: payload, Message: why}
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
		if p.keysBroken || st.status.Payload != nil {
			v.Banner = st.status.Message // the reason is not "no license": the keys are unreadable, or the license is for other versions
		}
	}
	if v.ClockSuspect {
		v.Message += " Drydock last saw " + maxSeen.Local().Format("2 January 2006") + ", which is later than this computer's clock says now, so the license is judged by that date. " +
			"If the clock is wrong, set it right. If Drydock's record of the date is the mistake, reset the clock check on the License page."
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

// advanceMaxSeen records the highest time ever observed (never moves back) and persists it when it moved a minute or more. A step
// of more than clockStep is only taken once the clock has read that far ahead for clockHold; if it comes back first, nothing was
// recorded and the license was never judged by it.
func (p *packaging) advanceMaxSeen(ctx context.Context, now time.Time) {
	p.mu.Lock()
	var setSince *string
	if !p.maxSeen.IsZero() && now.Sub(p.maxSeen) > clockStep {
		if p.jumpSince.IsZero() {
			p.jumpSince = now
			v := strconv.FormatInt(now.Unix(), 10)
			setSince = &v
		}
		if now.Sub(p.jumpSince) < clockHold {
			p.mu.Unlock()
			if setSince != nil {
				_ = p.setSetting(ctx, keyLicenseJumpSince, *setSince)
			}
			return
		}
	}
	clearSince := !p.jumpSince.IsZero() && setSince == nil
	p.jumpSince = time.Time{}
	if now.After(p.maxSeen) {
		p.maxSeen = now
	}
	save := p.maxSeen.Sub(p.savedMax) >= time.Minute
	v := p.maxSeen
	if save {
		p.savedMax = v
	}
	p.mu.Unlock()
	if clearSince {
		_ = p.delSetting(ctx, keyLicenseJumpSince)
	}
	if save {
		_ = p.setSetting(ctx, keyLicenseMaxSeen, strconv.FormatInt(v.Unix(), 10))
	}
}

// resetClock makes the latest time Drydock has seen the time on this computer's clock. It is the way out when that record is
// ahead of a clock that is right (a clock that was wrong when it was recorded), and it is allowed only then: it cannot be used
// to move a license back in time while the clock has not been set back. It is written to the audit log with both times.
func (p *packaging) resetClock(ctx context.Context) (LicenseView, error) {
	if !p.configured() {
		return LicenseView{}, errors.New("licensing is not configured in this build, so there is no clock check to reset")
	}
	now := p.clock()
	p.mu.Lock()
	old := p.maxSeen
	if !old.After(now.Add(24 * time.Hour)) {
		p.mu.Unlock()
		return LicenseView{}, errors.New("there is nothing to reset: this computer's clock is not behind the latest date Drydock has seen")
	}
	p.maxSeen, p.savedMax, p.jumpSince = now, now, time.Time{}
	p.mu.Unlock()
	if err := p.setSetting(ctx, keyLicenseMaxSeen, strconv.FormatInt(now.Unix(), 10)); err != nil {
		p.mu.Lock()
		p.maxSeen, p.savedMax = old, old
		p.mu.Unlock()
		return LicenseView{}, err
	}
	_ = p.delSetting(ctx, keyLicenseJumpSince)
	p.audit(ctx, "license.clock_reset", "", map[string]any{"was": old.Unix(), "now": now.Unix()})
	p.c.publish(ctx, "license.clock_reset", "", map[string]any{"was": old.Unix(), "now": now.Unix()})
	p.reconcile(ctx)
	return p.evaluate(ctx).view, nil
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
		"%s: %s is turned off. You can still read everything, export, back up, restore and create a support bundle, and nothing has been deleted. Import a license on the License page to continue.", why, what)}
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
	"agent.create": "create_agent", "agent.update": "create_agent", "project.create": "create_project", "project.attachRepo": "create_project", "project.setup": "create_project", "github.clone": "create_project", "provider.create": "add_connection", "integration.connect": "add_connection",
	"chat.create": "chat", "chat.send": "chat",
	"recommend.start": "start_work", // a paid turn of the team lead (audit 2026-10-16, L1); Core.Recommend checks it too, for the automatic path
	"task.resume":     "start_task", // a paused run goes on; pausing only stops work, so it stays available
	"lead.split":      "start_work", // a paid turn of the team lead
}

// onlyReduces reports whether a call to a gated method can only stop or refuse work: rejecting a request, declining an
// outbound action, stopping a paused run. Those stay available while Drydock is read-only, because refusing them would leave a
// risky pending item stuck until a license is imported.
func onlyReduces(method string, params json.RawMessage) bool {
	var p struct {
		Decision string `json:"decision"`
		Approve  *bool  `json:"approve"`
	}
	if json.Unmarshal(params, &p) != nil {
		return false
	}
	switch method {
	case "run.respond", "budget.resume":
		return p.Decision == "rejected"
	case "integration.outbound.decide":
		return p.Approve != nil && !*p.Approve
	}
	return false
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
		name, action, inner := name, action, inner
		m[name] = func(ctx context.Context, params json.RawMessage) (any, error) {
			if !onlyReduces(name, params) {
				if err := c.RequireLicensed(ctx, action); err != nil {
					return nil, err
				}
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
			p.notify(ctx, fmt.Sprintf("license-suspended:%s:%s", st.status.State, ids.New("n")),
				fmt.Sprintf("The license is %s, so %d running workflow run(s) were stopped cleanly. Their checkpoints are kept and they resume when a valid license is imported.", st.status.State, n), false)
		}
	} else if c.Engine != nil {
		if n, err := c.Engine.ResumeSuspended(ctx); err != nil {
			c.logf("resume suspended runs: %v", err)
		} else if n > 0 {
			c.publish(ctx, "license.runs_resumed", "", map[string]any{"runs": n})
			p.notify(ctx, "license-resumed:"+ids.New("n"), fmt.Sprintf("The license is valid again: %d suspended workflow run(s) resumed from their checkpoints.", n), false)
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
	m["license.resetClock"] = bind(func(ctx context.Context, _ struct{}) (any, error) { return p.resetClock(ctx) })
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
	if p.keysBroken {
		return LicenseView{}, errors.New(keysBrokenMessage)
	}
	token := strings.TrimSpace(r.Token)
	if token == "" && r.Path != "" {
		f, err := os.Open(r.Path)
		if err != nil {
			return LicenseView{}, fmt.Errorf("could not read the license file: %w", err)
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, maxLicenseFile+1)) // never more than the limit is read, whatever the file is
		if err != nil {
			return LicenseView{}, fmt.Errorf("could not read the license file: %w", err)
		}
		if len(b) > maxLicenseFile {
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
	if why := appWindow(&pl, AppVersion); why != "" {
		return LicenseView{}, errors.New("this license was not accepted: " + why)
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
