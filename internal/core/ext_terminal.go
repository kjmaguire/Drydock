package core

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/kjmaguire/drydock/internal/terminal"
)

// The human's terminals for the bottom dock (internal/terminal). A terminal opens in a task's worktree or a project's
// repository, never at a path the caller names. Output is read by offset with a long poll, so both transports (the Wails
// bridge and the development HTTP server) carry it with the ordinary call path, and none of it reaches the event log.

func init() {
	RegisterExtension(Extension{Name: "terminal", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
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
