package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/projectio"
	"github.com/kjmaguire/drydock/internal/security"
)

// Deleting a project (decision P51). The person types the project's name; the core checks it again and refuses while work
// runs in the project (a workflow run that has not ended, an agent answering in a room, a security scan). The rows go in one
// transaction (projectio.DeleteSteps; the event log, the audit log and the usage stay), after which Drydock removes what it
// made on disk for the project: the task worktrees, sandboxes, run folders, attachments and caches. The repository folder,
// its branches and its commits are never touched; only git's own record of the removed worktrees is pruned.

// ProjectDeleted is what deleting a project did.
type ProjectDeleted struct {
	Name string `json:"name"`
	// WorktreesRemoved counts Drydock's task worktrees that were removed.
	WorktreesRemoved int `json:"worktreesRemoved"`
	// Warnings name what could not be cleaned up (the project itself is gone). They never carry secrets.
	Warnings []string `json:"warnings"`
}

// ErrProjectBusy is returned while work runs in the project.
var ErrProjectBusy = errors.New("work is running in this project")

// projectHooks are what extensions add to project deletion: a busy check (a non-empty answer names the work that runs and
// refuses the delete) and a cleanup that runs after the rows are gone.
type projectHooks struct {
	busy    []func(projectID string) string
	cleanup []func(ctx context.Context, projectID string, taskIDs []string)
}

// onProjectBusy registers a busy check for project deletion (called from an extension's Start).
func (c *Core) onProjectBusy(f func(projectID string) string) {
	c.mu.Lock()
	c.projHooks.busy = append(c.projHooks.busy, f)
	c.mu.Unlock()
}

// onProjectDeleted registers a cleanup that runs after a project's rows were deleted.
func (c *Core) onProjectDeleted(f func(ctx context.Context, projectID string, taskIDs []string)) {
	c.mu.Lock()
	c.projHooks.cleanup = append(c.projHooks.cleanup, f)
	c.mu.Unlock()
}

// deletedTask is what the disk cleanup needs of a task.
type deletedTask struct{ id, worktree, branch string }

// DeleteProject deletes a project. confirmName must be the project's name.
func (c *Core) DeleteProject(ctx context.Context, id, confirmName string) (ProjectDeleted, error) {
	p, err := c.GetProject(ctx, id)
	if err != nil {
		return ProjectDeleted{}, err
	}
	if strings.TrimSpace(confirmName) != p.Name {
		return ProjectDeleted{}, fmt.Errorf("to delete the project, type its name exactly: %s", p.Name)
	}
	c.mu.Lock()
	hooks := c.projHooks
	c.mu.Unlock()
	var busy []string
	for _, f := range hooks.busy {
		if b := f(id); b != "" {
			busy = append(busy, b)
		}
	}
	if len(busy) > 0 {
		return ProjectDeleted{}, fmt.Errorf("%w: %s. Stop it, then delete the project", ErrProjectBusy, strings.Join(busy, "; "))
	}
	// the repository is opened before its project row goes (c.repo reads the row); a missing repository is no obstacle, but
	// the worktrees only git can remove are then named in the warnings
	var (
		repo    *git.Repo
		repoErr error
	)
	if p.HasRepo {
		repo, repoErr = c.repo(ctx, id)
	}

	out := ProjectDeleted{Name: p.Name, Warnings: []string{}}
	var (
		tasks   []deletedTask
		runs    []string
		roomIDs []string
		sandbox bool
		res     projectio.DeleteResult
	)
	// the busy check and the delete are one write transaction: no run can start between them (every start writes a run row,
	// and the writer is a single connection), and a run that started before is seen and refuses the delete
	err = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if b, err := runningWork(ctx, tx, id); err != nil {
			return err
		} else if len(b) > 0 {
			return fmt.Errorf("%w: %s. Cancel it (or wait for it to end), then delete the project", ErrProjectBusy, strings.Join(b, "; "))
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, worktree, branch FROM tasks WHERE project_id=?`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var t deletedTask
			if err := rows.Scan(&t.id, &t.worktree, &t.branch); err != nil {
				rows.Close()
				return err
			}
			tasks = append(tasks, t)
		}
		rows.Close()
		if runs, err = strings1(ctx, tx, `SELECT id FROM workflow_runs WHERE project_id=?1 OR task_id IN (SELECT id FROM tasks WHERE project_id=?1)
			UNION SELECT id FROM agent_runs WHERE agent_id IN (SELECT id FROM agents WHERE project_id=?1)`, id); err != nil {
			return err
		}
		if roomIDs, err = strings1(ctx, tx, `SELECT id FROM rooms WHERE project_id=?`, id); err != nil {
			return err
		}
		var v string
		_ = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingSandbox+id).Scan(&v)
		sandbox = strings.Contains(strings.ReplaceAll(v, " ", ""), `"enabled":true`)
		if res, err = projectio.Delete(ctx, tx, id); err != nil {
			return err
		}
		// audited in the same transaction: the project is never gone without its entry
		worktrees := 0
		for _, t := range tasks {
			if t.worktree != "" {
				worktrees++
			}
		}
		_, err = security.NewAudit(c.DB, c.Scanner).AppendTx(ctx, tx, security.AuditEntry{Actor: c.Human(), Action: "project.delete", Subject: id,
			Detail: map[string]any{"name": p.Name, "rows": res.Rows, "worktrees": worktrees}})
		return err
	})
	if err != nil {
		return ProjectDeleted{}, err
	}
	c.mu.Lock()
	delete(c.repos, id)
	c.mu.Unlock()

	warn := func(format string, a ...any) { out.Warnings = append(out.Warnings, fmt.Sprintf(format, a...)) }
	taskIDs := make([]string, 0, len(tasks))
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.id)
	}
	for _, f := range hooks.cleanup {
		f(ctx, id, taskIDs)
	}

	// the task worktrees: git removes each one (with any changes not committed in it; the branch and its commits stay), then
	// Drydock's own folder of the project's worktrees goes
	dataWorktrees := filepath.Join(c.cfg.DataDir, "worktrees", id)
	if repo == nil {
		// without the repository only the worktrees in Drydock's data folder can go (with that folder, below); one kept
		// elsewhere (beside a WSL repository) stays, and the person is told where
		for _, t := range tasks {
			if t.worktree == "" {
				continue
			}
			if !within(t.worktree, dataWorktrees) {
				warn("the worktree %s was left: the repository could not be opened (%v)", t.worktree, repoErr)
			} else if _, err := os.Lstat(t.worktree); err == nil {
				out.WorktreesRemoved++
			}
		}
	} else {
		for _, t := range tasks {
			if t.worktree == "" {
				continue
			}
			if err := repo.DeleteWorktree(ctx, git.Worktree{Path: t.worktree, Branch: t.branch, TaskID: t.id}, true); err != nil {
				warn("the worktree %s: %v", t.worktree, err)
				continue
			}
			out.WorktreesRemoved++
		}
		if dir := repo.WorktreeParent(filepath.Join(c.cfg.DataDir, "worktrees"), id); filepath.Base(dir) == id {
			removeTree(dir, warn)
		}
		// git forgets worktrees whose folder is gone
		if err := repo.PruneWorktrees(ctx); err != nil {
			warn("pruning the repository's worktree list: %v", err)
		}
	}
	removeTree(dataWorktrees, warn)

	// containers of the project's tasks, when the project used a sandbox
	if sandbox {
		for _, t := range taskIDs {
			sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if err := c.sandboxes().Remove(sctx, t); err != nil {
				warn("the sandbox of a task: %v", err)
			}
			cancel()
		}
	}

	// Drydock's files of the project
	for _, r := range runs {
		if dir := c.runDir(r); dir != "" {
			removeTree(dir, warn)
		}
	}
	for _, t := range taskIDs {
		if !plainName(t) {
			continue
		}
		removeTree(filepath.Join(c.cfg.DataDir, "runs", "previews", t), warn)
		removeTree(filepath.Join(c.cfg.DataDir, "runs", "e2e", t), warn)
	}
	for _, r := range roomIDs {
		if plainName(r) {
			removeTree(filepath.Join(c.cfg.DataDir, "attachments", r), warn)
		}
	}
	if plainName(id) {
		removeTree(filepath.Join(c.cfg.DataDir, "projects", id), warn)
	}
	for _, f := range res.MCPCache {
		if plainName(f) {
			removeFile(filepath.Join(c.cfg.DataDir, "mcp-cache", f), warn)
		}
	}
	for _, f := range res.ItemBodies {
		if a, b, ok := strings.Cut(f, "/"); ok && plainName(a) && plainName(b) {
			removeFile(filepath.Join(c.cfg.DataDir, "integrations", a, b), warn)
		}
	}

	c.publish(ctx, "project.deleted", id, map[string]any{"name": p.Name, "worktreesRemoved": out.WorktreesRemoved, "warnings": len(out.Warnings)})
	return out, nil
}

// runningWork names the work that is running in the project: workflow runs that have not ended (a task's, a plan's), agents
// answering in its rooms and a recommendation turn.
func runningWork(ctx context.Context, tx *sql.Tx, projectID string) ([]string, error) {
	var out []string
	rows, err := tx.QueryContext(ctx, `SELECT r.status, COALESCE(t.seq, 0), COALESCE(t.title, ''), r.workflow FROM workflow_runs r LEFT JOIN tasks t ON t.id = r.task_id
		WHERE (r.project_id=?1 OR t.project_id=?1) AND r.status NOT IN ('COMPLETED','FAILED','CANCELLED') ORDER BY t.seq, r.created_at`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var status, title, wf string
		var seq int
		if err := rows.Scan(&status, &seq, &title, &wf); err != nil {
			rows.Close()
			return nil, err
		}
		what := strings.ToLower(strings.ReplaceAll(status, "_", " "))
		if seq > 0 {
			out = append(out, fmt.Sprintf("task #%d %s is %s", seq, title, what))
		} else {
			out = append(out, fmt.Sprintf("a %s run is %s", wf, what))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var turns int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_turns WHERE project_id=? AND status='running'`, projectID).Scan(&turns); err != nil {
		return nil, err
	}
	if turns > 0 {
		out = append(out, "an agent is answering in a channel")
	}
	// a recommendation turn writes its result into the project when it ends (recommend.go); a row left running by a crash
	// is marked failed at start, so this never refuses for one that is not running
	var recs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM recommendations WHERE project_id=? AND status='running'`, projectID).Scan(&recs); err != nil {
		return nil, err
	}
	if recs > 0 {
		out = append(out, "an agent is preparing recommendations")
	}
	return out, nil
}

func strings1(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// plainName reports whether s is one path element (an id), so joining it never leaves the folder it is joined to.
func plainName(s string) bool {
	return s != "" && s != "." && s != ".." && filepath.Base(s) == s && !strings.ContainsAny(s, `/\:`)
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func removeTree(dir string, warn func(string, ...any)) {
	if _, err := os.Lstat(dir); err != nil {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		warn("the folder %s: %v", dir, err)
	}
}

func removeFile(path string, warn func(string, ...any)) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		warn("the file %s: %v", path, err)
	}
}
