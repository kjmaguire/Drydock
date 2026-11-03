package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Reviews posted while a pull request waits for the person (H12). The pr_feedback step reads a pull request's reviews once,
// before the person is asked to approve; what a collaborator writes on GitHub afterwards used to be read only after the person
// answered. This loop reads the reviews of every pull request whose review and merge waits for the person, every five minutes
// (pipeline.WaitingPRReviews: the same trusted and untrusted split, through the person's own GitHub login and nothing else, no
// model call): a collaborator's change request turns the waiting review into a change-request item in the Inbox
// (room.NeedsPRReview) and can be sent back with one click; nothing acts on it by itself.

// The loop's period and its first run after the app starts.
var (
	prWaitingEvery    = 5 * time.Minute
	prWaitingFirstRun = 2 * time.Minute
)

func init() {
	RegisterExtension(Extension{Name: "prwaiting", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		ctx, cancel := context.WithCancel(ctx)
		safe.Go("prwaiting.loop", func() { c.prWaitingLoop(ctx) })
		return nil, cancel, nil
	}})
}

func (c *Core) prWaitingLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(prWaitingFirstRun):
	}
	t := time.NewTicker(prWaitingEvery)
	defer t.Stop()
	for {
		_, _ = c.CheckWaitingPRReviews(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// CheckWaitingPRReviews reads the reviews of each pull request whose review and merge waits for the person, and returns how
// many change requests it found. A pull request GitHub cannot be read for is left for the next round (its error is logged
// without its text: it can carry a remote's address).
func (c *Core) CheckWaitingPRReviews(ctx context.Context) (int, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `
SELECT DISTINCT a.task_id FROM approvals a JOIN tasks t ON t.id = a.task_id JOIN projects p ON p.id = t.project_id
WHERE a.status = 'REQUESTED' AND a.kind = 'review' AND a.phase = 'merge' AND t.pr_ref <> '' AND p.mode = 'pr_flow'`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	found := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return found, ctx.Err()
		}
		t, err := c.Tasks.Get(ctx, id)
		if err != nil {
			continue
		}
		cfg, err := c.prSource(ctx, t.ProjectID)
		if err != nil {
			continue
		}
		r, err := pipeline.WaitingPRReviews(ctx, c.Deps, cfg, t)
		if err != nil {
			c.logf("prwaiting: task %s: the pull request's reviews could not be read", t.ID)
			continue
		}
		found += r.Requests
	}
	return found, nil
}

// PRReviewNote is a collaborator's change request that arrived while the review and merge waited (H12), as the person's
// answer shows it: the summary, and the request framed as feedback, which Send back with the GitHub review sends.
type PRReviewNote struct {
	Summary  string `json:"summary"`
	Author   string `json:"author,omitempty"`
	Feedback string `json:"feedback"`
}

// prReviewNote is the newest change request recorded for the task since the request was made (created, ms); nil when none.
func (c *Core) prReviewNote(ctx context.Context, taskID string, created int64) *PRReviewNote {
	if taskID == "" {
		return nil
	}
	var summary string
	var data []byte
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT summary, data FROM task_evidence WHERE task_id = ? AND kind = ? AND created_at >= ? ORDER BY id DESC LIMIT 1`,
		taskID, pipeline.EvidencePRReviewWaiting, created).Scan(&summary, &data)
	if err != nil {
		return nil
	}
	var d struct {
		Author   string `json:"author"`
		Feedback string `json:"feedback"`
	}
	if json.Unmarshal(data, &d) != nil || d.Feedback == "" {
		return nil
	}
	return &PRReviewNote{Summary: summary, Author: d.Author, Feedback: d.Feedback}
}
