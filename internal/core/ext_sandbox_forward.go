package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/sandbox"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Port forwarding (decision P134). The person asks, on the task page, for a port of the task's sandbox; a forwarder container
// publishes it on 127.0.0.1 (internal/sandbox/forward.go) and "Open" shows it in a window of Drydock's own, in a process of its own
// with a storage partition of its own (app/sandboxview_windows.go). The page an agent wrote is never opened in the person's
// browser, which holds the cookies of every other local app on 127.0.0.1. Everything here is the person's own act: an agent has no
// way to open a port or a window.

// Events and audit actions of forwarding.
const (
	EventForwardOpened = "sandbox.forward.opened"
	EventForwardClosed = "sandbox.forward.closed"
	EventForwardViewed = "sandbox.forward.viewed"
)

// SandboxApp is a forwarded port to be shown: the address (on the loopback, from the engine's own record) and what the window is
// called.
type SandboxApp struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	TaskID string `json:"taskId"`
	Port   int    `json:"port"`
}

// SandboxForwards is what sandbox.forward.list answers.
type SandboxForwards struct {
	// Running is false when the task has no running sandbox (it has not run a sandboxed turn yet, or it ended).
	Running  bool              `json:"running"`
	Forwards []sandbox.Forward `json:"forwards"`
	// CanView is false when this window cannot open Drydock's viewer (the browser harness): the page then says so.
	CanView bool   `json:"canView"`
	Max     int    `json:"max"`
	Error   string `json:"error,omitempty"`
}

// forwardTask returns the task, or an error for one that does not exist.
func (c *Core) forwardTask(ctx context.Context, taskID string) (tasks.Task, error) {
	return c.Tasks.Get(ctx, taskID)
}

// forwardCacheTTL is how long an answer of sandbox.forward.list is reused. Listing asks the engine several times (the sandbox, its
// containers, one port each), and the task page asks on every view and again on every sandbox event, most of them for a task that
// has no sandbox or the same one as a moment ago.
const forwardCacheTTL = 3 * time.Second

// forwardCache holds the last good answer of the list for each task. Opening or closing a port drops that task's answer.
type forwardCache struct {
	mu sync.Mutex
	m  map[string]cachedForwards
}

type cachedForwards struct {
	at  time.Time
	val SandboxForwards
}

func (fc *forwardCache) get(taskID string, now time.Time) (SandboxForwards, bool) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	e, ok := fc.m[taskID]
	if !ok || now.Sub(e.at) >= forwardCacheTTL {
		return SandboxForwards{}, false
	}
	return e.val, true
}

func (fc *forwardCache) put(taskID string, now time.Time, v SandboxForwards) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.m == nil {
		fc.m = map[string]cachedForwards{}
	}
	for k, e := range fc.m { // a task page at a time: what is old goes
		if now.Sub(e.at) >= forwardCacheTTL {
			delete(fc.m, k)
		}
	}
	fc.m[taskID] = cachedForwards{at: now, val: v}
}

func (fc *forwardCache) drop(taskID string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	delete(fc.m, taskID)
}

// SandboxForwardList lists the task's forwarded ports.
func (c *Core) SandboxForwardList(ctx context.Context, taskID string) (SandboxForwards, error) {
	out := SandboxForwards{Forwards: []sandbox.Forward{}, CanView: c.cfg.OpenSandboxApp != nil, Max: sandbox.MaxForwards}
	if _, err := c.forwardTask(ctx, taskID); err != nil {
		return out, err
	}
	if v, ok := c.fwdCache.get(taskID, time.Now()); ok {
		return v, nil
	}
	v, err := c.listForwards(ctx, taskID, out)
	if err == nil && v.Error == "" {
		c.fwdCache.put(taskID, time.Now(), v) // an engine that did not answer is asked again at the next view
	}
	return v, err
}

func (c *Core) listForwards(ctx context.Context, taskID string, out SandboxForwards) (SandboxForwards, error) {
	ectx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	m := c.sandboxes()
	if _, err := m.Existing(ectx, taskID); err != nil {
		if errors.Is(err, sandbox.ErrNoSandbox) {
			return out, nil
		}
		out.Error = err.Error() // no engine answers: the page says so, it is not a failure of the call
		return out, nil
	}
	out.Running = true
	fw, err := m.Forwards(ectx, taskID)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	if fw != nil {
		out.Forwards = fw
	}
	return out, nil
}

// SandboxForwardOpen forwards a port of the task's sandbox to this machine's loopback address. Only a person may.
func (c *Core) SandboxForwardOpen(ctx context.Context, taskID string, port int, by string) (sandbox.Forward, error) {
	if !isHuman(by) {
		return sandbox.Forward{}, errors.New("only a person may forward a port")
	}
	t, err := c.forwardTask(ctx, taskID)
	if err != nil {
		return sandbox.Forward{}, err
	}
	octx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	defer c.fwdCache.drop(taskID)
	f, err := c.sandboxes().OpenForward(octx, taskID, port)
	if err != nil {
		return sandbox.Forward{}, err
	}
	c.recordForward(ctx, EventForwardOpened, t, by, f)
	return f, nil
}

// SandboxForwardClose stops forwarding a port. Only a person may.
func (c *Core) SandboxForwardClose(ctx context.Context, taskID string, port int, by string) error {
	if !isHuman(by) {
		return errors.New("only a person may stop a forwarded port")
	}
	t, err := c.forwardTask(ctx, taskID)
	if err != nil {
		return err
	}
	if err := sandbox.ValidForwardPort(port); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	defer c.fwdCache.drop(taskID)
	if err := c.sandboxes().CloseForward(cctx, taskID, port); err != nil {
		return err
	}
	c.recordForward(ctx, EventForwardClosed, t, by, sandbox.Forward{TaskID: taskID, Port: port})
	return nil
}

// SandboxForwardView shows a forwarded port in Drydock's viewer window. The address comes from the engine's record of the
// forward, never from the caller. Only a person may; a build with no window says so.
func (c *Core) SandboxForwardView(ctx context.Context, taskID string, port int, by string) error {
	if c.cfg.OpenSandboxApp == nil {
		return errors.New("this window cannot open the viewer for a sandbox's app: it only opens in Drydock's own window, never in a browser, which would share its cookies with your other local apps")
	}
	if !isHuman(by) {
		return errors.New("only a person may open a forwarded port")
	}
	t, err := c.forwardTask(ctx, taskID)
	if err != nil {
		return err
	}
	vctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	fw, err := c.sandboxes().Forwards(vctx, taskID)
	if err != nil {
		return err
	}
	for _, f := range fw {
		if f.Port != port {
			continue
		}
		app := SandboxApp{URL: "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(f.HostPort)) + "/", Title: fmt.Sprintf("Port %d of the task's sandbox", f.Port), TaskID: taskID, Port: f.Port}
		if err := c.cfg.OpenSandboxApp(ctx, app); err != nil {
			return err
		}
		c.recordForward(ctx, EventForwardViewed, t, by, f)
		return nil
	}
	return fmt.Errorf("port %d of this task is not forwarded", port)
}

// recordForward keeps what the person did in the audit log and the task's events: the port and where it went, nothing the app
// served.
func (c *Core) recordForward(ctx context.Context, action string, t tasks.Task, by string, f sandbox.Forward) {
	detail := map[string]any{"port": f.Port}
	if f.HostPort != 0 {
		detail["hostPort"] = f.HostPort
	}
	if _, err := security.NewAudit(c.DB, c.Scanner).Append(ctx, security.AuditEntry{Actor: by, Action: action, Subject: t.ID, Detail: detail}); err != nil {
		c.logf("audit %s: %v", action, err)
	}
	c.publishTask(ctx, action, t, "", detail)
}
