// Package pipeline assembles the workflow nodes that turn a task into reviewed, evidenced work in an isolated worktree:
// prepare (worktree, task transitions), work (an agent turn with minimal iteration context), verify (deterministic
// checks), finalize (commit, diff and test evidence). Templates (graphs) in this package compose them.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/checks"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Deps are the services the nodes need.
type Deps struct {
	Tasks   *tasks.Service
	Runtime *rt.Runtime
	PM      proc.Manager
	Bus     events.Bus
	// Repo resolves a project's repository.
	Repo func(ctx context.Context, projectID string) (*git.Repo, error)
	// WorktreeRoot is the parent directory of task worktrees (<app data>/worktrees).
	WorktreeRoot string
	// DataDir holds per-run artifacts (full check logs, diffs).
	DataDir string
	// AgentConfig returns the runtime config for the task's agent in a role ("work" or "review").
	AgentConfig func(t tasks.Task, role string) rt.Config
	// Checks overrides check detection (default checks.Detect on the worktree).
	Checks func(dir string) []checks.Spec
	// OnCheck is called after each individual check (post the tool-member message). May be nil.
	OnCheck func(ctx context.Context, t tasks.Task, r checks.Result)
	// OnTurn is called with an agent's final text after each work or review turn (the room posts it in the task channel).
	OnTurn func(ctx context.Context, t tasks.Task, agentID, role, text string)
	// Context, if set, owns the generated instruction files and the per-attempt context manifest (brief section 9).
	Context *ctxeng.Engine
}

// Input is the run input of pipeline workflows.
type Input struct {
	TaskID string `json:"taskId"`
}

func taskFromInput(ctx context.Context, d Deps, in workflow.NodeInput) (tasks.Task, error) {
	var inp Input
	if err := json.Unmarshal(in.Input, &inp); err != nil || inp.TaskID == "" {
		return tasks.Task{}, fmt.Errorf("pipeline: run input must be {\"taskId\": ...}: %v", err)
	}
	return d.Tasks.Get(ctx, inp.TaskID)
}

func worktreeOf(t tasks.Task) git.Worktree {
	return git.Worktree{Path: t.Worktree, Branch: t.Branch, Base: t.BaseBranch, TaskID: t.ID}
}

// SafePointHook returns the engine Config.SafePoint implementation: a hidden commit of the task worktree.
func SafePointHook(d Deps) func(ctx context.Context, n workflow.NodeInfo) (string, error) {
	return func(ctx context.Context, n workflow.NodeInfo) (string, error) {
		if n.TaskID == "" {
			return "", nil
		}
		t, err := d.Tasks.Get(ctx, n.TaskID)
		if err != nil || t.Worktree == "" {
			return "", nil // the prepare node has not run yet: nothing to record
		}
		repo, err := d.Repo(ctx, t.ProjectID)
		if err != nil {
			return "", err
		}
		return repo.SafePoint(ctx, worktreeOf(t), n.Step*100+n.Attempt)
	}
}

// Prepare creates the worktree and branch (idempotently), assigns the task to its agent and moves it to IN_PROGRESS.
func Prepare(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		repo, err := d.Repo(ctx, t.ProjectID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		w, err := repo.CreateWorktree(ctx, git.WorktreeReq{TaskID: t.ID, Title: t.Title, Base: t.BaseBranch, Parent: filepath.Join(d.WorktreeRoot, t.ProjectID)})
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("create worktree: %w", err)
		}
		if t, err = d.Tasks.Patch(ctx, t.ID, tasks.Update{Worktree: &w.Path, Branch: &w.Branch, Base: &w.Base}); err != nil {
			return workflow.NodeOutput{}, err
		}
		// advance idempotently to IN_PROGRESS
		for _, to := range []tasks.Status{tasks.Ready, tasks.Claimed, tasks.InProgress} {
			if tasks.CanTransition(t.Status, to) && stageIndex(t.Status) < stageIndex(to) {
				if t, err = d.Tasks.Transition(ctx, t.ID, to, tasks.TransitionOpts{By: "system", Reason: "pipeline prepare"}); err != nil {
					return workflow.NodeOutput{}, err
				}
			}
		}
		var instructionFiles []string
		if d.Context != nil {
			res, _, err := d.Context.Write(ctx, t.ProjectID, w.Path)
			if err != nil {
				return workflow.NodeOutput{}, fmt.Errorf("instruction files: %w", err)
			}
			// generated files belong to Drydock: keep them out of status, diffs and commits
			if err := repo.LocalIgnore(ctx, w, append(append([]string(nil), res.Written...), res.Edited...)); err != nil {
				return workflow.NodeOutput{}, fmt.Errorf("ignore generated files: %w", err)
			}
			instructionFiles = res.Written
		}
		b, _ := json.Marshal(map[string]any{"taskId": t.ID, "worktree": w.Path, "branch": w.Branch, "base": w.Base, "instructionFiles": instructionFiles})
		return workflow.NodeOutput{Value: b}, nil
	}
}

func stageIndex(s tasks.Status) int {
	switch s {
	case tasks.Backlog:
		return 0
	case tasks.Ready:
		return 1
	case tasks.Claimed:
		return 2
	case tasks.InProgress:
		return 3
	}
	return 4
}

// Work runs one agent turn in the task worktree. The prompt is the task spec on the first pass and, on later passes,
// only the latest failure or review feedback (token rule 16: never the previous session's history).
func Work(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		cfg := d.AgentConfig(t, "work")
		cfg.TaskID, cfg.ProjectID = t.ID, t.ProjectID
		cfg.Spec.Cwd = t.Worktree
		prompt := WorkPrompt(t, in)
		d.saveManifest(ctx, in, t, cfg.AgentID, prompt, "work")
		h := d.Runtime.AgentTask(rt.TaskSpec{Config: cfg, Prompt: func(workflow.NodeInput) string { return prompt }})
		out, err := h(ctx, in)
		if err != nil {
			return out, err
		}
		var res rt.TurnResult
		if json.Unmarshal(out.Value, &res) == nil {
			var cost float64
			for _, u := range res.Usage {
				cost += u.CostUSD
			}
			_, _ = d.Tasks.Patch(ctx, t.ID, tasks.Update{AddCost: cost})
			if d.OnTurn != nil && strings.TrimSpace(res.Text) != "" {
				d.OnTurn(ctx, t, cfg.AgentID, "work", res.Text)
			}
		}
		return out, nil
	}
}

// saveManifest records what this attempt was given and what was deliberately left out.
func (d Deps) saveManifest(ctx context.Context, in workflow.NodeInput, t tasks.Task, agentID, prompt, role string) {
	if d.Context == nil {
		return
	}
	var m ctxeng.Manifest
	m.Add("prompt ("+role+")", true, len(prompt), "task spec on the first pass; only the latest failure or feedback on later passes (token rule 16)", 1)
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		if st, err := os.Stat(filepath.Join(t.Worktree, name)); err == nil {
			m.Add("file:"+name, true, int(st.Size()), "generated instruction file read natively by the CLI (cache-stable prefix)", 5)
		}
	}
	m.Add("repo map", false, 0, "not built: tree-sitter is gated until the Windows CGO job is green (decision D5)", 9)
	m.Add("project memory", false, 0, "retrieved on demand (search_memory), not preloaded; accepted memory is already in AGENTS.md", 8)
	m.Add("room history", false, 0, "no messages selected for this task channel", 7)
	m.Add("previous session transcript", false, 0, "fresh session per attempt; handoff artifacts only (token rules 5 and 16)", 6)
	_ = d.Context.SaveManifest(ctx, string(in.RunID), string(in.NodeID), in.Attempt, t.ID, agentID, m)
}

// WorkPrompt builds the turn prompt. It contains only what the agent needs now.
func WorkPrompt(t tasks.Task, in workflow.NodeInput) string {
	var sb strings.Builder
	// feedback from the last failing check, a review, a watchdog nudge, or a human answer arrives through the inbox
	var feedback []string
	for _, m := range in.Inbox {
		var cr workflow.CheckResult
		if json.Unmarshal(m.Value, &cr) == nil && cr.Name != "" && !cr.Passed {
			f := fmt.Sprintf("The %q check failed (exit %d).", cr.Name, cr.ExitCode)
			if len(cr.FailingTests) > 0 {
				f += " Failing tests: " + strings.Join(cr.FailingTests, ", ") + "."
			}
			f += "\n\n" + cr.Output
			feedback = append(feedback, f)
			continue
		}
		var o map[string]any
		if json.Unmarshal(m.Value, &o) == nil {
			if w, ok := o["watchdog"].(string); ok {
				feedback = append(feedback, w)
			} else if fb, ok := o["feedback"].(string); ok {
				feedback = append(feedback, "Reviewer feedback:\n"+fb)
			} else if d, ok := o["data"]; ok {
				if b, _ := json.Marshal(d); len(b) > 0 && string(b) != "null" {
					feedback = append(feedback, "Guidance from the human:\n"+string(b))
				}
			}
		}
	}
	if len(feedback) == 0 {
		fmt.Fprintf(&sb, "Task: %s\n\n%s\n", t.Title, t.Description)
		if len(t.Acceptance) > 0 {
			sb.WriteString("\nAcceptance criteria:\n")
			for _, a := range t.Acceptance {
				fmt.Fprintf(&sb, "- %s\n", a)
			}
		}
		sb.WriteString("\nWork in the current directory. Make the change, keep it minimal, and stop when the task is done. Do not commit.")
		return sb.String()
	}
	fmt.Fprintf(&sb, "Task: %s\n\nYour previous attempt needs changes. Fix the following and stop:\n\n%s", t.Title, strings.Join(feedback, "\n\n---\n\n"))
	return sb.String()
}

// Verify runs the project's checks in order (build, test, lint, typecheck as detected), stopping at the first failure. It
// records evidence for each and returns the failing (or final passing) CheckResult with Route pass or fail.
func Verify(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		repo, err := d.Repo(ctx, t.ProjectID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		detect := d.Checks
		if detect == nil {
			detect = checks.Detect
		}
		specs := detect(t.Worktree)
		if len(specs) == 0 {
			return workflow.NodeOutput{}, errors.New("no checks detected for this project: add a Taskfile with build/test targets or configure checks")
		}
		logDir := filepath.Join(d.DataDir, "runs", string(in.RunID), "checks")
		diffHash, _ := repo.DiffHash(ctx, worktreeOf(t))
		var last checks.Result
		for _, spec := range specs {
			spec.LogDir = logDir
			res, err := checks.Run(ctx, d.PM, spec, func(line string) {
				in.Emit(workflow.NewChunk(workflow.ChunkLog, workflow.TextDelta{Text: line}))
			})
			if err != nil {
				return workflow.NodeOutput{}, err
			}
			res.DiffHash = diffHash
			last = res
			passed := res.Passed
			kind := spec.Name
			if kind == "build" || kind == "test" || kind == "lint" || kind == "typecheck" {
				// "test" evidence gates REVIEW; the others are recorded under their own kind
			}
			data, _ := json.Marshal(map[string]any{"argv": res.Argv, "exitCode": res.ExitCode, "failing": res.FailingTests, "durationMs": res.Duration.Milliseconds()})
			if _, err := d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: kind, Passed: &passed, Summary: summarize(res), Ref: res.LogRef, Data: data}); err != nil {
				return workflow.NodeOutput{}, err
			}
			if d.OnCheck != nil {
				d.OnCheck(ctx, t, res)
			}
			if !res.Passed {
				break
			}
		}
		b, _ := json.Marshal(last.CheckResult)
		route := "pass"
		if !last.Passed {
			route = "fail"
		}
		return workflow.NodeOutput{Value: b, Route: route}, nil
	}
}

func summarize(r checks.Result) string {
	if r.Passed {
		return fmt.Sprintf("%s passed in %s", r.Name, r.Duration.Round(10_000_000))
	}
	s := fmt.Sprintf("%s failed (exit %d) in %s", r.Name, r.ExitCode, r.Duration.Round(10_000_000))
	if len(r.FailingTests) > 0 {
		s += ": " + strings.Join(r.FailingTests, ", ")
	}
	return s
}

// Finalize commits the work, records diff and commit evidence, and updates the task's file list. It moves the task to
// REVIEW when a reviewer is assigned and the guards allow it.
func Finalize(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		repo, err := d.Repo(ctx, t.ProjectID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		w := worktreeOf(t)
		// never commit agent-owned instruction files generated by Drydock
		commit, err := repo.Commit(ctx, w, fmt.Sprintf("%s\n\nDrydock task %s", t.Title, t.ID))
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("commit: %w", err)
		}
		diff, err := repo.Diff(ctx, w, w.Base)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		stat, _ := repo.DiffStat(ctx, w, w.Base)
		dir := filepath.Join(d.DataDir, "runs", string(in.RunID), "evidence")
		_ = os.MkdirAll(dir, 0o755)
		diffPath := filepath.Join(dir, "task.diff")
		if err := os.WriteFile(diffPath, []byte(diff), 0o644); err != nil {
			return workflow.NodeOutput{}, err
		}
		statJSON, _ := json.Marshal(stat)
		if _, err := d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "diff", Summary: fmt.Sprintf("%d file(s), +%d -%d", stat.Files, stat.Insertions, stat.Deletions), Ref: diffPath, Data: statJSON}); err != nil {
			return workflow.NodeOutput{}, err
		}
		if _, err := d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "commit", Summary: "commit " + shortID(commit), Ref: commit}); err != nil {
			return workflow.NodeOutput{}, err
		}
		files := changedFiles(diff)
		_, _ = d.Tasks.Patch(ctx, t.ID, tasks.Update{Files: &files})
		out := map[string]any{"taskId": t.ID, "commit": commit, "diff": diffPath, "files": files, "status": string(t.Status)}
		if cur, err := d.Tasks.Get(ctx, t.ID); err == nil && cur.Reviewer != "" && cur.Reviewer != cur.Assignee && cur.Status == tasks.InProgress {
			if nt, err := d.Tasks.Transition(ctx, t.ID, tasks.Review, tasks.TransitionOpts{By: "system", Reason: "checks passed with evidence"}); err == nil {
				out["status"] = string(nt.Status)
			} else {
				out["reviewBlocked"] = err.Error()
			}
		}
		b, _ := json.Marshal(out)
		return workflow.NodeOutput{Value: b}, nil
	}
}

func shortID(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func changedFiles(diff string) []string {
	var files []string
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "+++ b/") {
			files = append(files, strings.TrimPrefix(l, "+++ b/"))
		}
	}
	return files
}

// TaskPipeline is the single-agent pipeline: prepare, work, verify (loop back to work on failure), finalize.
// MaxLoops bounds the work/verify loop; when exhausted the task goes to a human (WAITING_HUMAN) with a summary.
func TaskPipeline(d Deps, maxLoops int) *workflow.Graph {
	if maxLoops <= 0 {
		maxLoops = 5
	}
	return workflow.NewGraph("task-pipeline").
		Add(
			workflow.Route("prepare", Prepare(d)),
			workflow.AgentTask("work", Work(d)).With("agent", "work"),
			workflow.Check("verify", Verify(d)),
			workflow.Route("finalize", Finalize(d)),
			workflow.Request("limit", workflow.RequestSpec{Kind: "question", Title: "Iteration limit reached: checks still failing"}),
		).
		Edge("prepare", "work").
		Edge("work", "verify").
		EdgeWhen("verify", "finalize", "pass").
		Loop("verify", "work", "fail", maxLoops, "limit").
		Start("prepare")
}

var _ = agents.StatusReady
