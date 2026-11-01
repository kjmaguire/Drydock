package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/router"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/watchdog"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The team lead (decision P26) and the watchdog wiring it shares: the lead service with the core's agents, rooms, budgets and
// scanner; the watchdog's thresholds per node, handoff artifacts, resets and the stop on a rejected pause; the monitor loop
// for stalled and unassigned tasks; the methods lead.standup and lead.interventions.

// LeadMonitorEvery is how often the monitor looks for stalled and unassigned tasks.
var LeadMonitorEvery = time.Minute

func init() {
	RegisterExtension(Extension{Name: "lead", After: []string{"agents-dev"}, Start: startLead})
}

func startLead(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	l := pipeline.NewLead(pipeline.LeadDeps{
		Tasks: c.Tasks, Runtime: c.Runtime, Bus: c.Bus,
		Team:   c.leadTeam,
		Config: c.leadConfig,
		Cwd:    c.leadCwd,
		Assign: func(ctx context.Context, taskID, worker, reviewer string) error {
			_, err := c.AssignTask(ctx, taskID, worker, reviewer)
			return err
		},
		Post:   c.leadPost,
		Budget: c.leadBudget,
		// the lead's turns are routed on the lead's own list of models, like every other turn (P112)
		Route: func(ctx context.Context, t pipeline.LeadTurn, in workflow.NodeInput, run workflow.NodeHandler) (workflow.NodeOutput, error) {
			return EconomicsRouteTurn(ctx, c, DirectTurn{Kind: router.RunLead, AgentID: t.AgentID, ProjectID: t.ProjectID, TaskID: t.TaskID, RunID: t.RunID, NodeID: "lead"}, in, run)
		},
		Redact: c.Scanner.Redact,
		Brief: func(ctx context.Context, projectID string) string {
			if f := c.Deps.ProjectBrief; f != nil {
				return f(ctx, projectID)
			}
			return ""
		},
		Steer: func(ctx context.Context, taskID, msg string, escalate bool) bool {
			return c.Watch.Steer(ctx, taskID, msg, escalate)
		},
		Dev: func(ctx context.Context, projectID string) pipeline.DevSettings {
			return pipeline.LoadDevSettings(ctx, c.DB, projectID)
		},
	})
	c.lead.Store(l)
	loopCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	safe.Go("core.startLead", func() {
		defer wg.Done()
		l.Loop(loopCtx, LeadMonitorEvery, func(ctx context.Context) []string {
			ps, err := c.Projects(ctx)
			if err != nil {
				return nil
			}
			out := make([]string, len(ps))
			for i, p := range ps {
				out[i] = p.ID
			}
			return out
		})
	})
	methods := map[string]Method{
		"lead.standup": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			if _, err := c.GetProject(ctx, p.ProjectID); err != nil {
				return nil, err
			}
			if err := c.RequireLicensed(ctx, "lead_standup"); err != nil {
				return nil, err
			}
			return l.Standup(ctx, p.ProjectID)
		}),
		"lead.interventions": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			TaskID    string `json:"taskId"`
		}) (any, error) {
			return pipeline.Interventions(ctx, c.DB, p.ProjectID, p.TaskID)
		}),
		// a person asks the team lead to split a task (trigger requested): its follow-ups wait in the backlog
		"lead.split": bind(func(ctx context.Context, p struct {
			TaskID string `json:"taskId"`
		}) (any, error) {
			return c.RequestSplit(ctx, p.TaskID, c.Human())
		}),
		// the monitor's pass on demand (the loop runs it every LeadMonitorEvery)
		"lead.tick": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return l.Tick(ctx, p.ProjectID)
		}),
	}
	return methods, func() { cancel(); wg.Wait() }, nil
}

// SplitResult is what lead.split returns: the lead's decision (split, or escalate when it thinks the task should not be split,
// with its reason in message), the follow-up tasks it made (in BACKLOG), and the stored intervention.
type SplitResult struct {
	Action         string       `json:"action"`
	Message        string       `json:"message"`
	Reason         string       `json:"reason"`
	LeadID         string       `json:"leadId"`
	LeadName       string       `json:"leadName"`
	CreatedTasks   []tasks.Task `json:"createdTasks"`
	InterventionID string       `json:"interventionId"`
	// Invalid is true when the lead's answers could not be used twice and the decision became an escalation.
	Invalid bool `json:"invalid,omitempty"`
}

// RequestSplit is lead.split: a person asks the project's team lead to split a task into follow-ups (pipeline.Lead.RequestSplit,
// trigger requested). Refused without a lead on the team, and for a finished task.
func (c *Core) RequestSplit(ctx context.Context, taskID, by string) (SplitResult, error) {
	if !isPerson(by) {
		return SplitResult{}, errors.New("only a person asks the team lead to split a task")
	}
	if err := c.RequireLicensed(ctx, "start_work"); err != nil {
		return SplitResult{}, err
	}
	l := c.lead.Load()
	if l == nil {
		return SplitResult{}, pipeline.ErrNoLead
	}
	o, err := l.RequestSplit(ctx, taskID, by)
	if err != nil {
		return SplitResult{}, err
	}
	res := SplitResult{Action: o.Decision.Action, Message: o.Decision.Message, Reason: o.Decision.Reason, LeadID: o.LeadID, LeadName: o.LeadName,
		CreatedTasks: []tasks.Task{}, InterventionID: o.InterventionID, Invalid: o.Invalid}
	for _, id := range o.CreatedTasks {
		if t, err := c.Tasks.Get(ctx, id); err == nil {
			res.CreatedTasks = append(res.CreatedTasks, t)
		}
	}
	return res, nil
}

// ---- hooks the base wiring (core.go) points at ----

// leadHook is Deps.Lead: it forwards to the lead service once the extension has started (before that, and in a build
// without it, there is no lead and nothing changes).
type leadHook struct{ c *Core }

func (h leadHook) Supervise(ctx context.Context, tr pipeline.LeadTrigger) pipeline.LeadOutcome {
	if l := h.c.lead.Load(); l != nil {
		return l.Supervise(ctx, tr)
	}
	return pipeline.LeadOutcome{}
}

// briefStanding is the context engine's Standing: the project brief (Deps.ProjectBrief) for the generated AGENTS.md.
func (c *Core) briefStanding(ctx context.Context, projectID string) string {
	if f := c.Deps.ProjectBrief; f != nil {
		return pipeline.BriefForInstructions(f(ctx, projectID))
	}
	return ""
}

// resetWatch is Deps.ResetWatch: a human continued the task, or it moved on: the watchdog's count starts again.
func (c *Core) resetWatch(ctx context.Context, taskID string) {
	if c.Watch != nil {
		_ = c.Watch.Reset(ctx, taskID)
	}
}

// watchdogHooks connects the watchdog to the room, the handoff store, the lead and the task state.
func (c *Core) watchdogHooks() watchdog.Hooks {
	return watchdog.Hooks{
		Notify:  c.watchdogNotify,
		Handoff: c.watchdogHandoff,
		Lead: func(ctx context.Context, n workflow.NodeInfo) bool {
			l := c.lead.Load()
			return l != nil && n.TaskID != "" && l.HasLead(ctx, n.TaskID, string(n.RunID), n.Meta["agent"])
		},
		Supervise: func(ctx context.Context, n workflow.NodeInfo, tr watchdog.TripRecord) (watchdog.Steer, bool) {
			l := c.lead.Load()
			if l == nil {
				return watchdog.Steer{}, false
			}
			o := l.Supervise(ctx, pipeline.LeadTrigger{Kind: pipeline.TriggerWatchdog, TaskID: n.TaskID, RunID: string(n.RunID), NodeAgent: n.Meta["agent"], Evidence: tr.Evidence})
			return watchdog.Steer{Action: o.Decision.Action, Message: o.Decision.Message}, o.Intervened
		},
		Stop: c.watchdogStop,
	}
}

// watchdogStop cancels the task when a human rejects a watchdog pause.
func (c *Core) watchdogStop(ctx context.Context, n workflow.NodeInfo, by string) {
	if n.TaskID == "" {
		return
	}
	if !strings.HasPrefix(by, "human:") {
		by = c.Human()
	}
	if _, err := c.Tasks.Transition(ctx, n.TaskID, tasks.Cancelled, tasks.TransitionOpts{By: by, Reason: "stopped by the human at a watchdog pause"}); err != nil {
		c.logf("stop after a rejected watchdog pause: %v", err)
	}
}

// watchdogHandoff writes the handoff artifact of a stage-2 pause (what tripped, where, on which task) into handoffs, so the
// agent that picks the task up and the human can read it (mcp read_handoff, the run explanation). The store and the task part
// are the session handoffs' (pipeline.SaveHandoff, pipeline.HandoffTaskOf).
func (c *Core) watchdogHandoff(ctx context.Context, n workflow.NodeInfo, tr watchdog.TripRecord) (string, error) {
	art := map[string]any{"kind": "watchdog_pause", "signal": tr.Signal, "stage": tr.Stage, "node": string(n.NodeID), "attempt": n.Attempt, "workflow": n.Workflow}
	var ev map[string]any
	if json.Unmarshal([]byte(tr.Evidence), &ev) == nil {
		art["evidence"] = ev["evidence"]
	}
	agentID := ""
	if t, err := c.Tasks.Get(ctx, n.TaskID); err == nil {
		art["task"] = pipeline.HandoffTaskOf(t)
		agentID = t.Assignee
		if n.Meta["agent"] == "review" {
			agentID = t.Reviewer
		}
	}
	return pipeline.SaveHandoff(ctx, c.DB, c.Bus, c.Scanner.Redact, pipeline.HandoffRecord{ProjectID: n.ProjectID, TaskID: n.TaskID, RunID: string(n.RunID),
		AgentID: agentID, Reason: "watchdog", Artifact: art, Event: map[string]any{"signal": tr.Signal}})
}

// ---- the lead service's view of the core ----

// leadTeam lists a project's agents with their open-task load.
func (c *Core) leadTeam(ctx context.Context, projectID string) ([]pipeline.LeadMember, error) {
	ags, err := c.Agents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	open, _ := c.Tasks.List(ctx, tasks.Filter{ProjectID: projectID, Statuses: []tasks.Status{tasks.Ready, tasks.Claimed, tasks.InProgress, tasks.Blocked, tasks.WaitingHuman, tasks.Review, tasks.Verification}})
	load := map[string]int{}
	for _, t := range open {
		load[t.Assignee]++
	}
	out := make([]pipeline.LeadMember, len(ags))
	for i, a := range ags {
		out[i] = pipeline.LeadMember{ID: a.ID, Name: a.Name, Role: a.Role, Adapter: a.Adapter, Load: load[a.ID]}
	}
	return out, nil
}

// leadConfig is the lead's runtime config: its own adapter, model and effort, read-only.
func (c *Core) leadConfig(ctx context.Context, agentID string) (rt.Config, error) {
	a, err := c.agent(ctx, agentID)
	if err != nil || a.Archived {
		return rt.Config{}, fmt.Errorf("the team lead %s is not on the team", agentID)
	}
	return rt.Config{AgentID: a.ID, Adapter: a.Adapter, Spec: agents.RunSpec{AgentID: a.ID, Role: pipeline.RoleLead, Model: a.Model, Effort: a.Effort, PermissionMode: "plan", ReadOnly: true},
		Provider: providerLabel(a), Tier: c.agentTier(a)}, nil
}

// leadCwd is the task's worktree when it exists, else the project's repository.
func (c *Core) leadCwd(ctx context.Context, projectID, taskID string) string {
	if taskID != "" {
		if t, err := c.Tasks.Get(ctx, taskID); err == nil && t.Worktree != "" {
			if st, err := os.Stat(t.Worktree); err == nil && st.IsDir() {
				return t.Worktree
			}
		}
	}
	if p, err := c.GetProject(ctx, projectID); err == nil {
		return p.RepoPath
	}
	return ""
}

// leadPost posts as the lead in the task's channel, or in #general for a project-wide message; the lead joins the room first.
func (c *Core) leadPost(ctx context.Context, projectID, taskID, agentID, body string) (string, error) {
	var rm room.Room
	var err error
	if taskID != "" {
		rm, err = c.Room.RoomByTask(ctx, taskID)
	} else {
		rm, err = c.generalChannel(ctx, projectID)
	}
	if err != nil {
		return "", err
	}
	if ok, _ := c.Room.IsMember(ctx, rm.ID, room.Agent(agentID)); !ok {
		if err := c.Room.AddMember(ctx, rm.ID, room.Agent(agentID), pipeline.RoleLead, room.StateActive, room.System); err != nil {
			return "", err
		}
	}
	res, err := c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: room.TypeChat, Author: room.Agent(agentID), Body: body, TaskID: taskID, Payload: map[string]any{"via": "lead"}})
	if err != nil {
		return "", err
	}
	return res.Message.ID, nil
}

func (c *Core) generalChannel(ctx context.Context, projectID string) (room.Room, error) {
	rooms, err := c.Room.ListRooms(ctx, projectID, false)
	if err != nil {
		return room.Room{}, err
	}
	for _, r := range rooms {
		if r.Kind == room.KindChannel && r.Name == "general" {
			return r, nil
		}
	}
	return room.Room{}, errors.New("the project has no #general channel")
}

// leadBudget refuses the lead's turn when an applicable budget (project, task, the lead itself) is used up, as for any turn.
func (c *Core) leadBudget(ctx context.Context, projectID, taskID, agentID string) error {
	sts, err := (&budget.Store{DB: c.DB}).StatusFor(ctx, budget.ScopeIDs{Project: projectID, Task: taskID, Agent: agentID})
	if err != nil {
		return fmt.Errorf("budgets cannot be evaluated: %w", err) // fail closed, like the budget gate
	}
	for _, st := range sts {
		if st.State == budget.StateExceeded {
			return fmt.Errorf("budget limit reached: %s", st.Message)
		}
	}
	return nil
}
