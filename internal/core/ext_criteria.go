package core

import (
	"context"

	"github.com/kjmaguire/drydock/internal/pipeline"
)

// Each acceptance criterion's own status (decision P125, pipeline.CriteriaStatus): a person confirms a criterion met or not
// met, waives it with a reason, or reopens it, from the task page. The caller's identity is the core's person, never a value
// from the client, so an agent cannot confirm or waive through this method; an agent claims a criterion met only through the
// handoff tool, and a claim never sets the status.

func init() {
	RegisterExtension(Extension{Name: "criteria", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"task.criterion.mark": bind(func(ctx context.Context, p struct {
				TaskID string `json:"taskId"`
				Index  int    `json:"index"`
				Mark   string `json:"mark"`
				Note   string `json:"note"`
			}) (any, error) {
				return c.MarkCriterion(ctx, p.TaskID, p.Index, p.Mark, p.Note, c.Human())
			}),
		}, nil, nil
	}})
}

// MarkCriterion records a person's word on one acceptance criterion and returns every criterion's status after it.
func (c *Core) MarkCriterion(ctx context.Context, taskID string, index int, mark, note, by string) ([]pipeline.CriterionStatus, error) {
	if _, err := c.Tasks.MarkCriterion(ctx, taskID, index, mark, by, note); err != nil {
		return nil, err
	}
	return c.criteriaOf(ctx, taskID)
}

// criteriaOf is each of a task's acceptance criteria's status now.
func (c *Core) criteriaOf(ctx context.Context, taskID string) ([]pipeline.CriterionStatus, error) {
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}
	ev, err := c.Tasks.Evidence(ctx, taskID)
	if err != nil {
		return nil, err
	}
	rv, err := c.Tasks.Reviews(ctx, taskID)
	if err != nil {
		return nil, err
	}
	marks, err := c.Tasks.CriterionMarks(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return nonNilCriteria(pipeline.CriteriaStatus(t.Acceptance, ev, rv, marks)), nil
}

func nonNilCriteria(cs []pipeline.CriterionStatus) []pipeline.CriterionStatus {
	if cs == nil {
		return []pipeline.CriterionStatus{}
	}
	return cs
}
