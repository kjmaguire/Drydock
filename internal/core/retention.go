package core

import (
	"context"
	"time"
)

// Retention (brief s18, docs/packaging.md). The bulk of the events table is agent.output: an agent's streamed output, kept so
// the team lead can read what a worker did on a task while it works. After agentOutputRetention it is deleted; the run's
// stream files under <data>/runs and every other event (task, workflow, approval, room, security) stay. Nothing else is
// compacted or deleted yet.
const agentOutputRetention = 30 * 24 * time.Hour

func init() {
	RegisterExtension(Extension{Name: "retention", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		ctx, cancel := context.WithCancel(ctx)
		go func() {
			t := time.NewTicker(24 * time.Hour)
			defer t.Stop()
			for {
				if n, err := c.pruneAgentOutput(ctx, time.Now()); err != nil {
					c.logf("retention: %v", err)
				} else if n > 0 {
					c.logf("retention: deleted %d agent output events older than %s", n, agentOutputRetention)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
		return nil, cancel, nil
	}})
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
