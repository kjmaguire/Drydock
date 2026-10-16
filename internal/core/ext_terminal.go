package core

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/terminal"
)

// The human's terminals for the bottom dock (internal/terminal). A terminal opens in a task's worktree or a project's
// repository, never at a path the caller names. Output is read by offset with a long poll, so both transports (the Wails
// bridge and the development HTTP server) carry it with the ordinary call path, and none of it reaches the event log.

func init() {
	RegisterExtension(Extension{Name: "terminal", Start: func(extCtx context.Context, c *Core) (map[string]Method, func(), error) {
		m := &terminal.Manager{}
		var signMu sync.Mutex
		signIns := map[string]string{} // Connection key -> the terminal of its running sign-in
		return map[string]Method{
			"terminal.open": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				TaskID    string `json:"taskId"`
				Cols      int    `json:"cols"`
				Rows      int    `json:"rows"`
			}) (any, error) {
				dir, title, err := c.terminalDir(ctx, p.ProjectID, p.TaskID)
				if err != nil {
					return nil, err
				}
				return m.Open(dir, title, p.Cols, p.Rows)
			}),
			"terminal.read": bind(func(ctx context.Context, p struct {
				ID     string `json:"id"`
				From   int64  `json:"from"`
				WaitMs int    `json:"waitMs"`
			}) (any, error) {
				wait := time.Duration(min(max(p.WaitMs, 0), 20000)) * time.Millisecond
				return m.Read(ctx, p.ID, p.From, wait)
			}),
			"terminal.write": bind(func(ctx context.Context, p struct {
				ID   string `json:"id"`
				Data string `json:"data"`
			}) (any, error) {
				if len(p.Data) > 64<<10 {
					return nil, errors.New("terminal: input over 64 KB in one write")
				}
				return nil, m.Write(p.ID, []byte(p.Data))
			}),
			"terminal.resize": bind(func(ctx context.Context, p struct {
				ID   string `json:"id"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}) (any, error) {
				return nil, m.Resize(p.ID, p.Cols, p.Rows)
			}),
			"terminal.close": bind(func(ctx context.Context, p idParam) (any, error) { return nil, m.Close(p.ID) }),
			"terminal.list":  bind(func(ctx context.Context, _ struct{}) (any, error) { return m.List(), nil }),
			// connection.signIn starts a Connection's own sign-in (its manifest's auth.login: claude auth login, inside the distro
			// for a WSL install) in a terminal the person answers (decision P50). Drydock runs the unmodified CLI and never reads
			// what the sign-in stores (D1). One sign-in per Connection at a time: asking again while it runs returns the same
			// terminal. When it ends, whatever of it is left inside a distro is killed, the core checks the Connection again (so no
			// cached "signed out" outlives the sign-in), and connection.sign_in_finished, with the new state, makes the Connections
			// page re-check.
			"connection.signIn": bind(func(ctx context.Context, p struct {
				Adapter string `json:"adapter"`
				Distro  string `json:"distro"` // the install inside this WSL distro instead of the native one
			}) (any, error) {
				key := p.Adapter
				if p.Distro != "" {
					key = connections.WSLKey(p.Adapter, p.Distro)
				}
				signMu.Lock()
				defer signMu.Unlock()
				if in, ok := runningSignIn(m, signIns, key); ok {
					return in, nil
				}
				conn, si, err := c.Connections.SignIn(ctx, key)
				if err != nil {
					return nil, err
				}
				if in, ok := runningSignIn(m, signIns, conn.Key); ok {
					return in, nil
				}
				title := "Sign in: " + conn.Adapter
				if conn.RunMode == "wsl" {
					title += " in " + conn.Distro
				}
				var id string
				ready := make(chan struct{})
				info, err := m.OpenProgram(si.Dir, title, 100, 24, terminal.Program{Path: si.Program, Args: si.Args, Env: si.Env, OnExit: func(code int) {
					<-ready
					signMu.Lock()
					if signIns[conn.Key] == id {
						delete(signIns, conn.Key)
					}
					signMu.Unlock()
					if si.Marker != "" && c.wsl != nil {
						// killing wsl.exe does not end what it started inside the distro (internal/wsl/launch.go)
						kctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						if n, err := c.wsl.Kill(kctx, si.Distro, si.Marker); err != nil {
							c.logf("sign-in in %s: clean-up: %v", si.Distro, err)
						} else if n > 0 {
							c.logf("sign-in in %s: %d processes were left and are killed", si.Distro, n)
						}
						cancel()
					}
					if extCtx.Err() == nil { // not while the core shuts down
						// the sign-in changed what the CLI answers: check this Connection again now, past the probe cache (a distro's
						// probe is kept for minutes), so whatever reads the sign-in state next (the card, the agent forms, a chat)
						// sees the new one, and the event says it
						data := map[string]any{"key": conn.Key, "exitCode": code}
						cctx, cancel := context.WithTimeout(extCtx, 60*time.Second)
						if now, err := c.Connections.CheckKey(cctx, conn.Key); err != nil {
							c.logf("sign-in of %s: checking it again: %v", conn.Key, err)
						} else if now.Authenticated != nil {
							data["signedIn"] = *now.Authenticated
						}
						cancel()
						c.publish(extCtx, "connection.sign_in_finished", "", data)
					}
				}})
				if err != nil {
					return nil, err
				}
				id = info.ID
				signIns[conn.Key] = id
				close(ready)
				c.publish(ctx, "connection.sign_in_started", "", map[string]any{"key": conn.Key, "by": c.Human()})
				return info, nil
			}),
		}, m.CloseAll, nil
	}})
}

// terminalDir is a task's worktree while it exists, else its project's repository (or, before one is attached, the project's
// private workspace folder).
func (c *Core) terminalDir(ctx context.Context, projectID, taskID string) (dir, title string, err error) {
	if taskID != "" {
		t, err := c.Tasks.Get(ctx, taskID)
		if err != nil {
			return "", "", err
		}
		if worktreeExists(t) {
			return t.Worktree, "task #" + strconv.Itoa(t.Seq), nil
		}
		projectID = t.ProjectID
	}
	if projectID == "" {
		return "", "", errors.New("terminal: name a project or a task")
	}
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return "", "", err
	}
	dir, err = c.projectDir(p)
	return dir, p.Name, err
}

// runningSignIn is the terminal of key's sign-in while its program runs (keys compare as Windows compares distro names).
func runningSignIn(m *terminal.Manager, signIns map[string]string, key string) (terminal.Info, bool) {
	for k, id := range signIns {
		if !strings.EqualFold(k, key) {
			continue
		}
		for _, in := range m.List() {
			if in.ID == id && !in.Exited {
				return in, true
			}
		}
	}
	return terminal.Info{}, false
}
