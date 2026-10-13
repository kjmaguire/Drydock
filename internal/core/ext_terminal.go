package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
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
			// connection.signIn starts a Connection's own sign-in (claude auth login, inside the distro for a WSL install) in a
			// terminal of the dock, where the person answers it (decision P50). Drydock runs the unmodified CLI and never reads
			// what the sign-in stores (D1); the Connections page re-probes afterwards like after any change.
			"connection.signIn": bind(func(ctx context.Context, p struct {
				Adapter string `json:"adapter"`
				Distro  string `json:"distro"` // the install inside this WSL distro instead of the native one
			}) (any, error) {
				conn, err := c.connectionByKey(ctx, p.Adapter, p.Distro)
				if err != nil {
					return nil, err
				}
				wslExe := ""
				if conn.RunMode == "wsl" && c.wsl != nil {
					if wslExe, err = c.wsl.Path(); err != nil {
						return nil, fmt.Errorf("sign in inside %s: %w", conn.Distro, err)
					}
				}
				prog, args, err := connections.SignInCommand(conn, wslExe)
				if err != nil {
					return nil, err
				}
				home, err := os.UserHomeDir()
				if err != nil {
					return nil, fmt.Errorf("sign in: %w", err)
				}
				title := "Sign in: " + conn.Adapter
				if conn.RunMode == "wsl" {
					title += " in " + conn.Distro
				}
				info, err := m.OpenProgram(home, title, 100, 24, prog, args...)
				if err != nil {
					return nil, err
				}
				c.publish(ctx, "connection.sign_in_started", "", map[string]any{"key": conn.Key, "by": c.Human()})
				// when the sign-in ends, the Connections page re-probes (it re-reads on every connection.* event)
				go func(key, id string) {
					if code, err := m.Exited(extCtx, id); err == nil {
						c.publish(extCtx, "connection.sign_in_finished", "", map[string]any{"key": key, "exitCode": code})
					}
				}(conn.Key, info.ID)
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

// connectionByKey is the detected Connection of an adapter's native install, or of its install inside a WSL distro.
func (c *Core) connectionByKey(ctx context.Context, adapter, distro string) (connections.Connection, error) {
	key := adapter
	if distro != "" {
		key = adapter + "@wsl:" + distro
	}
	for _, conn := range c.Connections.DetectAll(ctx) {
		if conn.Key == key {
			return conn, nil
		}
	}
	if distro != "" {
		return connections.Connection{}, fmt.Errorf("no Connection of %s inside the WSL distro %s: the distro is not listed, or %s is not installed in it", adapter, distro, adapter)
	}
	return connections.Connection{}, fmt.Errorf("no Connection named %s", adapter)
}
