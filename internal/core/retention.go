package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/runfiles"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Retention (brief s18, docs/packaging.md). Events and usage are kept indefinitely, except agent.output: an agent's streamed
// output, kept so the team lead can read what a worker did on a task while it works, deleted after agentOutputRetention (its
// durable copy is the run's stream file). The raw output and checkpoints of a run that ended (completed, failed or cancelled)
// are compacted after rawCompactAfter (every checkpoint but the run's latest is deleted; streams, logs and saved prompts are
// gzip-compressed in place, and runfiles reads either form) and deleted after rawDeleteAfter (all of the run's checkpoints and
// its folder under <data>/runs, except the files a task's evidence still names). The run's row, its node executions, events, usage and evidence rows stay. Compaction and
// deletion do nothing while the license is read-only (decision P13).
const (
	agentOutputRetention = 30 * 24 * time.Hour
	rawCompactAfter      = 30 * 24 * time.Hour
	rawDeleteAfter       = 90 * 24 * time.Hour
	retentionFirstPass   = 5 * time.Minute
)

// RetentionReport is what one retention pass did.
type RetentionReport struct {
	AgentOutputDeleted int64 `json:"agentOutputDeleted"`
	// Suspended is true when the license is read-only: nothing was compacted or deleted.
	Suspended          bool  `json:"suspended,omitempty"`
	RunsCompacted      int   `json:"runsCompacted"`
	CheckpointsDeleted int64 `json:"checkpointsDeleted"`
	FilesCompressed    int   `json:"filesCompressed"`
	BytesBefore        int64 `json:"bytesBefore"`
	BytesAfter         int64 `json:"bytesAfter"`
	RunsDeleted        int   `json:"runsDeleted"`
	BytesDeleted       int64 `json:"bytesDeleted"`
}

func (r RetentionReport) acted() bool {
	return r.RunsCompacted > 0 || r.RunsDeleted > 0
}

func init() {
	RegisterExtension(Extension{Name: "retention", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		ctx, cancel := context.WithCancel(ctx)
		safe.Go("core.init", func() {
			// the first pass waits a little, so starting the app does not begin with disk work
			select {
			case <-ctx.Done():
				return
			case <-time.After(retentionFirstPass):
			}
			t := time.NewTicker(24 * time.Hour)
			defer t.Stop()
			for {
				rep, err := c.RunRetention(ctx, time.Now())
				if err != nil && ctx.Err() == nil {
					c.logf("retention: %v", err)
				}
				if rep.AgentOutputDeleted > 0 {
					c.logf("retention: deleted %d agent output events older than %s", rep.AgentOutputDeleted, agentOutputRetention)
				}
				if rep.acted() {
					c.logf("retention: compacted %d runs (%d checkpoints deleted, %d files %d -> %d bytes), deleted %d runs (%d bytes)",
						rep.RunsCompacted, rep.CheckpointsDeleted, rep.FilesCompressed, rep.BytesBefore, rep.BytesAfter, rep.RunsDeleted, rep.BytesDeleted)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		})
		return nil, cancel, nil
	}})
}

// RunRetention runs one retention pass as of now.
func (c *Core) RunRetention(ctx context.Context, now time.Time) (RetentionReport, error) {
	var rep RetentionReport
	n, err := c.pruneAgentOutput(ctx, now)
	rep.AgentOutputDeleted = n
	if err != nil {
		return rep, err
	}
	// recorded telemetry older than its retention window goes with the daily pass; nothing else ever deleted it (audit 2026-10-16, L5)
	c.purgeTelemetry(ctx)
	if !c.RetentionAllowed() {
		rep.Suspended = true
		return rep, nil
	}
	if err := c.compactRuns(ctx, now.Add(-rawDeleteAfter).UnixMilli(), now.Add(-rawCompactAfter).UnixMilli(), &rep); err != nil {
		return rep, err
	}
	if err := c.deleteRuns(ctx, now.Add(-rawDeleteAfter).UnixMilli(), &rep); err != nil {
		return rep, err
	}
	if rep.acted() {
		c.publish(ctx, "retention.ran", "", map[string]any{"runsCompacted": rep.RunsCompacted, "checkpointsDeleted": rep.CheckpointsDeleted,
			"filesCompressed": rep.FilesCompressed, "bytesBefore": rep.BytesBefore, "bytesAfter": rep.BytesAfter, "runsDeleted": rep.RunsDeleted, "bytesDeleted": rep.BytesDeleted})
	}
	return rep, nil
}

// pruneAgentOutput deletes agent.output events older than agentOutputRetention before now, in batches so the single writer
// connection is never held for long.
func (c *Core) pruneAgentOutput(ctx context.Context, now time.Time) (int64, error) {
	cutoff := now.Add(-agentOutputRetention).UnixMilli()
	var total int64
	for {
		res, err := c.DB.Writer().ExecContext(ctx, `DELETE FROM events WHERE seq IN (SELECT seq FROM events WHERE type='agent.output' AND ts < ? LIMIT 5000)`, cutoff)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < 5000 || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}

type endedRun struct {
	id, latest string
}

// endedRuns lists the runs that ended (completed, failed or cancelled) no later than before and after after. Each pass looks
// at every such run and does only what is left to do, so a run imported with an old date is handled like any other.
func (c *Core) endedRuns(ctx context.Context, after, before int64) ([]endedRun, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id, latest_checkpoint_id FROM workflow_runs
		WHERE status IN ('COMPLETED','FAILED','CANCELLED') AND updated_at > ? AND updated_at <= ? ORDER BY updated_at`, after, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []endedRun
	for rows.Next() {
		var r endedRun
		if err := rows.Scan(&r.id, &r.latest); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// runDir is the run's folder under <data>/runs, or "" for an id that is not a plain name.
func (c *Core) runDir(id string) string {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return ""
	}
	return filepath.Join(c.cfg.DataDir, "runs", id)
}

// compactRuns compacts the runs that ended between the deletion and the compaction cutoffs.
func (c *Core) compactRuns(ctx context.Context, deleteCutoff, cutoff int64, rep *RetentionReport) error {
	runs, err := c.endedRuns(ctx, deleteCutoff, cutoff)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if err := ctx.Err(); err != nil {
			return err
		}
		did := false
		if r.latest != "" {
			res, err := c.DB.Writer().ExecContext(ctx, `DELETE FROM workflow_checkpoints WHERE run_id=? AND id<>?`, r.id, r.latest)
			if err != nil {
				return fmt.Errorf("compact %s: %w", r.id, err)
			}
			n, _ := res.RowsAffected()
			rep.CheckpointsDeleted += n
			did = n > 0
		}
		if dir := c.runDir(r.id); dir != "" {
			err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						return nil
					}
					return err
				}
				if d.IsDir() || !d.Type().IsRegular() || !runfiles.Compactable(d.Name()) {
					return nil
				}
				before, after, err := runfiles.Compress(p)
				if err != nil {
					return err
				}
				rep.FilesCompressed++
				rep.BytesBefore += before
				rep.BytesAfter += after
				did = true
				return nil
			})
			if err != nil {
				return fmt.Errorf("compact %s: %w", r.id, err)
			}
		}
		if did {
			rep.RunsCompacted++
		}
	}
	return nil
}

// deleteRuns deletes the checkpoints and the folder of every run that ended before cutoff and still has either.
func (c *Core) deleteRuns(ctx context.Context, cutoff int64, rep *RetentionReport) error {
	runs, err := c.endedRuns(ctx, -1, cutoff)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := c.DB.Writer().ExecContext(ctx, `DELETE FROM workflow_checkpoints WHERE run_id=?`, r.id)
		if err != nil {
			return fmt.Errorf("delete %s: %w", r.id, err)
		}
		n, _ := res.RowsAffected()
		rep.CheckpointsDeleted += n
		did := n > 0
		if dir := c.runDir(r.id); dir != "" {
			if _, err := os.Stat(dir); err == nil {
				freed, removed, err := c.removeRunFiles(ctx, dir)
				if err != nil {
					return fmt.Errorf("delete %s: %w", r.id, err)
				}
				rep.BytesDeleted += freed
				did = did || removed
			}
		}
		if did {
			rep.RunsDeleted++
		}
	}
	return nil
}

// removeRunFiles deletes what a finished run left in its folder, except the files in its evidence folder that a task's evidence
// still names (the recorded diff, the accepted spec): the task's page shows them as long as the task exists, and they used to be
// deleted from under their rows with the rest of the folder (audit 2026-10-16, L23). The raw output (streams, prompts, check
// logs) is what retention is for and goes. It reports the bytes freed and whether it removed anything; a folder that is left
// holds nothing but those files.
func (c *Core) removeRunFiles(ctx context.Context, dir string) (freed int64, removed bool, err error) {
	keep, err := c.evidenceFilesIn(ctx, dir)
	if err != nil {
		return 0, false, err
	}
	var walk func(d string) (kept bool, err error)
	walk = func(d string) (bool, error) {
		ents, err := os.ReadDir(d)
		if err != nil {
			return false, err
		}
		kept := false
		for _, e := range ents {
			p := filepath.Join(d, e.Name())
			switch {
			case e.IsDir():
				k, err := walk(p)
				if err != nil {
					return false, err
				}
				if k {
					kept = true
					continue
				}
				if err := os.Remove(p); err != nil { // empty now
					return false, err
				}
				removed = true
			case keep[p] || keep[strings.TrimSuffix(p, runfiles.Ext)]:
				kept = true
			default:
				if info, err := e.Info(); err == nil {
					freed += info.Size()
				}
				if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return false, err
				}
				removed = true
			}
		}
		return kept, nil
	}
	if kept, err := walk(dir); err != nil {
		return freed, removed, err
	} else if !kept {
		if err := os.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return freed, removed, err
		}
		removed = true
	}
	return freed, removed, nil
}

// evidenceFilesIn is the set of files in the evidence folder of a run's folder dir that a task's evidence names (by the path
// written in the row; a file that was compacted to <name>.gz is the same file).
func (c *Core) evidenceFilesIn(ctx context.Context, dir string) (map[string]bool, error) {
	prefix := filepath.Join(filepath.Clean(dir), "evidence") + string(filepath.Separator)
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT ref FROM task_evidence WHERE ref<>'' AND instr(ref, ?) > 0`, filepath.Join(filepath.Clean(dir), "evidence"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keep := map[string]bool{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		if ref = filepath.Clean(ref); strings.HasPrefix(ref, prefix) {
			keep[ref] = true
		}
	}
	return keep, rows.Err()
}

// purgeTelemetry deletes recorded spans and metrics older than the sink's retention window.
func (c *Core) purgeTelemetry(ctx context.Context) {
	econMu.Lock()
	e := econByCore[c]
	econMu.Unlock()
	if e == nil || e.sink == nil {
		return
	}
	if err := e.sink.Purge(ctx, false); err != nil {
		c.logf("retention: telemetry: %v", err)
	}
}
