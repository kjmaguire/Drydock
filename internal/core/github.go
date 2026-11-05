package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kjmaguire/drydock/internal/integrations"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// UpdateProject changes how a project delivers work: local merge or a GitHub pull request, and the merge strategy.
func (c *Core) UpdateProject(ctx context.Context, id, mode, mergeStrategy string) (Project, error) {
	p, err := c.GetProject(ctx, id)
	if err != nil {
		return p, err
	}
	if mode == "" {
		mode = p.Mode
	}
	if mergeStrategy == "" {
		mergeStrategy = p.MergeStrategy
	}
	if mode != "local_merge" && mode != "pr_flow" {
		return p, fmt.Errorf("mode must be local_merge or pr_flow, not %q", mode)
	}
	switch mergeStrategy {
	case "merge", "squash", "rebase":
	default:
		return p, fmt.Errorf("merge strategy must be merge, squash or rebase, not %q", mergeStrategy)
	}
	if mode == "pr_flow" {
		// fail now, not halfway through a run, when the repository cannot do pull requests
		if _, err := c.prSource(ctx, id); err != nil {
			return p, fmt.Errorf("pr_flow is not available for this project: %w", err)
		}
	}
	if _, err := c.DB.Writer().ExecContext(ctx, `UPDATE projects SET mode=?, merge_strategy=?, version=version+1 WHERE id=?`, mode, mergeStrategy, id); err != nil {
		return p, err
	}
	return c.GetProject(ctx, id)
}

// GitHubIssues lists open issues of the project's repository. Their text is untrusted outside data.
func (c *Core) GitHubIssues(ctx context.Context, projectID, labels string) ([]host.Issue, error) {
	cfg, err := c.prSource(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return cfg.GitHub.Issues(ctx, labels, timeZero)
}

// ImportIssue turns an issue into a BACKLOG task. The human chose to import it, but its text still came from outside, so it
// gets the same cleaning as a connection's items (secrets redacted, invisible characters removed, sizes capped, an
// instruction-like title withheld) inside a quoted block that says it is data; an issue with a high injection risk is not
// imported at all. The pull request later says "Closes #n".
func (c *Core) ImportIssue(ctx context.Context, projectID string, number int) (tasks.Task, error) {
	r, err := c.ImportIssueWith(ctx, ImportIssueReq{ProjectID: projectID, Number: number})
	return r.Task, err
}

// ImportIssueReq is github.importIssue: the issue, and optionally its crew and its start in the same click (the Pull requests
// page's "Import and start", C11). Without them the task is a BACKLOG task, as an import always made (P37(4)/M27: imported
// work waits until a person moves it on; this per-issue click is that person). Start runs under task.create's checks (licence,
// repository), made before the task is, so a refused start imports nothing.
type ImportIssueReq struct {
	ProjectID string `json:"projectId"`
	Number    int    `json:"number"`
	Assignee  string `json:"assignee"`
	Reviewer  string `json:"reviewer"`
	Start     bool   `json:"start"`
}

// ImportIssueWith is github.importIssue: ImportIssue's cleaning and refusal, then task.create's assignment and start.
func (c *Core) ImportIssueWith(ctx context.Context, req ImportIssueReq) (CreateTaskResult, error) {
	projectID, number := req.ProjectID, req.Number
	cfg, err := c.prSource(ctx, projectID)
	if err != nil {
		return CreateTaskResult{}, err
	}
	issues, err := cfg.GitHub.Issues(ctx, "", timeZero)
	if err != nil {
		return CreateTaskResult{}, err
	}
	for _, is := range issues {
		if is.Number != number {
			continue
		}
		ref := fmt.Sprintf("#%d", is.Number)
		by := "the human, from an issue by " + nonEmpty(is.Author, "an unknown author")
		x := integrations.Sanitizer{Scanner: c.Scanner}.OutsideTask(ctx, "GitHub issue", ref, is.URL, by, is.Title, is.Body)
		if x.Risk >= integrations.RiskThreshold {
			return CreateTaskResult{}, fmt.Errorf("issue %s has a high injection risk (%s): it is not turned into a task; write the task yourself", ref, strings.Join(x.Flags, ", "))
		}
		return c.CreateTaskWith(ctx, CreateTaskReq{CreateReq: tasks.CreateReq{ProjectID: projectID, Title: x.Title, Description: x.Description, IssueRef: ref},
			Assignee: req.Assignee, Reviewer: req.Reviewer, Start: req.Start})
	}
	return CreateTaskResult{}, fmt.Errorf("issue #%d is not an open issue of this repository", number)
}

// PRState is the pull request view of a task.
type PRState struct {
	PR       host.PR           `json:"pr"`
	CI       host.CIState      `json:"ci"`
	Reviews  []reviewView      `json:"reviews"`
	Pipeline *workflow.RunInfo `json:"run,omitempty"`
}

type reviewView struct {
	host.ReviewEvent
	Trusted bool `json:"trusted"` // only collaborator-level authors can trigger rework; the rest is for the human to read
}

// PullRequest returns the live GitHub state of a task's pull request.
func (c *Core) PullRequest(ctx context.Context, taskID string) (PRState, error) {
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return PRState{}, err
	}
	if t.PRRef == "" {
		return PRState{}, errors.New("this task has no pull request yet")
	}
	cfg, err := c.prSource(ctx, t.ProjectID)
	if err != nil {
		return PRState{}, err
	}
	n, _ := strconv.Atoi(t.PRRef[strings.LastIndex(t.PRRef, "#")+1:])
	var st PRState
	if st.PR, _, err = cfg.GitHub.GetPR(ctx, n, ""); err != nil {
		return st, err
	}
	if st.CI, err = cfg.GitHub.Checks(ctx, st.PR.Head.SHA); err != nil {
		return st, err
	}
	evs, err := cfg.GitHub.Reviews(ctx, n, timeZero)
	if err != nil {
		return st, err
	}
	for _, e := range evs {
		st.Reviews = append(st.Reviews, reviewView{ReviewEvent: e, Trusted: e.Trusted()})
	}
	return st, nil
}

// ChannelAllowed reports whether the installed license entitles the user to an update channel (ext_packaging.go): the
// channels of the license token, stable only without a license, and every channel in a build that has no licensing keys.
func (c *Core) ChannelAllowed(ch string) bool { return c.licenseChannelAllowed(ch) }
