package trial

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/sandbox"
	"github.com/kjmaguire/drydock/internal/skills"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Drydock modes for the worker's commands.
const (
	ModeSandbox = "sandbox" // the per-task container (decision P30)
	ModeChecks  = "checks"  // the project's own checks on this machine (decision P24)
)

// Drydock is arm D: the product's own loop through the methods the UI calls, on a fresh core and database. A worker and an
// independent reviewer (both Claude Code, the same model as B1) work the task in Drydock's plain workflow: work, the
// project's checks, review, and up to five iterations, then the merge approval. There the hidden acceptance check stands in
// for the human: it passes, and the harness approves the merge; it fails, and the harness cancels the task. Any other
// question Drydock asks the human ends the run (no human answers in a trial). A project budget is the dollar ceiling.
type Drydock struct {
	Root       string // the Drydock repository (task paths are relative to it)
	DataDir    string // where the run's core keeps its database (fresh per run)
	Model      string
	CeilingUSD float64
	Wall       time.Duration
	Mode       string
	// SandboxImage is the workspace image of ModeSandbox.
	SandboxImage string
	// Poll is how often the harness looks at the task (default 2s).
	Poll time.Duration
	// Adapters replace the core's agent adapters: test doubles only (the harness's own tests use the scripted stand-in).
	Adapters map[string]agents.Adapter
	// SkillsDir (optional) holds skills as <name>/SKILL.md. They are committed into the task's repository under .aet/skills
	// before the run, accepted by the harness as the human would, and reach both agents; Skills says how (skills.DeliveryAll or
	// skills.DeliveryRelevant). Without SkillsDir the project has no skills.
	SkillsDir string
	Skills    string
}

// Detail describes the arm for the record.
func (d Drydock) Detail() string {
	s := "Drydock: worker + independent reviewer (Claude Code), plain workflow, commands in the per-task sandbox (P30, image " + d.SandboxImage + ")"
	if d.Mode == ModeChecks {
		s = "Drydock: worker + independent reviewer (Claude Code), plain workflow, the worker may run the project's checks on this machine (P24)"
	}
	if d.SkillsDir != "" {
		s += ", the bundle's skills delivered " + skills.DeliveryOf(d.Skills)
	}
	return s
}

// Artifacts are the files a D run leaves next to its run.json.
type Artifacts struct {
	Events []byte
	Usage  []byte
	// Evidence is the task's check evidence (what ran, where, and whether it passed).
	Evidence []byte
}

// Run works the task in repo (already prepared at its start commit) and returns the record and its artifacts. accept is
// the hidden check, run on the worker's result at the merge approval.
func (d Drydock) Run(ctx context.Context, t Task, repo string, accept func(tree string) AcceptResult) (Record, Artifacts) {
	rec := Record{Arm: ArmDrydock, ArmDetail: d.Detail(), TaskID: t.ID, Model: d.Model, CeilingUSD: d.CeilingUSD}
	var art Artifacts
	start := time.Now()
	fail := func(outcome, reason string) (Record, Artifacts) {
		rec.Outcome, rec.Reason = outcome, reason
		rec.WallMs = time.Since(start).Milliseconds()
		return rec, art
	}
	if d.Poll <= 0 {
		d.Poll = 2 * time.Second
	}
	if d.Mode == ModeSandbox {
		def, _ := json.MarshalIndent(map[string]any{"image": d.SandboxImage, "network": "none",
			"env": map[string]string{"CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=mod", "GOPROXY": "off", "GOCACHE": "/tmp/gocache", "GOPATH": "/tmp/gopath"}}, "", "  ")
		if err := os.MkdirAll(filepath.Join(repo, ".drydock"), 0o755); err != nil {
			return fail(Failed, err.Error())
		}
		if err := os.WriteFile(filepath.Join(repo, ".drydock", "sandbox.json"), append(def, '\n'), 0o644); err != nil {
			return fail(Failed, err.Error())
		}
		if err := commitAll(ctx, repo, "Drydock sandbox definition"); err != nil {
			return fail(Failed, err.Error())
		}
	}
	if d.SkillsDir != "" {
		names, err := copySkills(d.SkillsDir, filepath.Join(repo, ".aet", "skills"))
		if err != nil {
			return fail(Failed, "the skills: "+err.Error())
		}
		if err := commitAll(ctx, repo, "The team's skills"); err != nil {
			return fail(Failed, err.Error())
		}
		rec.Skills, rec.SkillNames = skills.DeliveryOf(d.Skills), names
	}
	c, err := core.Open(ctx, core.Config{DataDir: d.DataDir, User: "trial", LeaseTTL: 30 * time.Second, Adapters: d.Adapters})
	if err != nil {
		return fail(Failed, "the core did not open: "+err.Error())
	}
	var coreTask string
	defer func() {
		// the core removes the task's sandbox itself when the task ends; give it the time to finish before closing it (a
		// close cancels that work), then remove whatever is still there
		if coreTask != "" && d.Mode == ModeSandbox {
			d.waitSandboxGone(coreTask, time.Minute)
		}
		c.Close()
		if coreTask != "" {
			d.cleanup(coreTask)
		}
	}()
	call := func(method string, params, out any) error {
		b, _ := json.Marshal(params)
		res, err := c.Call(ctx, method, b)
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		if out != nil {
			return json.Unmarshal(res, out)
		}
		return nil
	}
	var p core.Project
	if err := call("project.create", map[string]any{"name": t.ID, "repoPath": repo}, &p); err != nil {
		return fail(Failed, err.Error())
	}
	switch d.Mode {
	case ModeSandbox:
		if err := call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}, nil); err != nil {
			return fail(Failed, err.Error())
		}
	case ModeChecks:
		var s map[string]any
		if err := call("projectdev.get", map[string]any{"projectId": p.ID}, &s); err != nil {
			return fail(Failed, err.Error())
		}
		s["workerChecks"] = true
		if err := call("projectdev.set", map[string]any{"projectId": p.ID, "settings": s}, nil); err != nil {
			return fail(Failed, err.Error())
		}
	}
	if d.SkillsDir != "" {
		var res skills.ScanResult
		if err := call("skill.scan", map[string]string{"projectId": p.ID}, &res); err != nil {
			return fail(Failed, err.Error())
		}
		if res.Offered != len(rec.SkillNames) || len(res.Errors) > 0 {
			return fail(Failed, fmt.Sprintf("the skills scan offered %d of %d skills (blocked %d): %s", res.Offered, len(rec.SkillNames), res.Blocked, strings.Join(res.Errors, "; ")))
		}
		for _, name := range rec.SkillNames {
			if err := call("skill.accept", map[string]string{"projectId": p.ID, "name": name}, nil); err != nil {
				return fail(Failed, err.Error())
			}
		}
		var s map[string]any
		if err := call("projectdev.get", map[string]any{"projectId": p.ID}, &s); err != nil {
			return fail(Failed, err.Error())
		}
		s["skillDelivery"] = rec.Skills
		if err := call("projectdev.set", map[string]any{"projectId": p.ID, "settings": s}, nil); err != nil {
			return fail(Failed, err.Error())
		}
	}
	limit := d.CeilingUSD
	if err := call("budget.set", map[string]any{"scope": "project", "scopeId": p.ID, "limitUsd": limit, "warnPct": 0.8, "note": "trial ceiling"}, nil); err != nil {
		return fail(Failed, err.Error())
	}
	var w, r core.Agent
	if err := call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Worker", Adapter: "claude", Role: "backend", Model: d.Model}, &w); err != nil {
		return fail(Failed, err.Error())
	}
	if err := call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer", Adapter: "claude", Role: "reviewer", Model: d.Model}, &r); err != nil {
		return fail(Failed, err.Error())
	}
	var task tasks.Task
	if err := call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: t.Title, Description: t.Prompt}, &task); err != nil {
		return fail(Failed, err.Error())
	}
	if err := call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task); err != nil {
		return fail(Failed, err.Error())
	}
	rec.CoreTaskID, coreTask = task.ID, task.ID
	var runID string
	if err := call("task.start", map[string]string{"id": task.ID}, &runID); err != nil {
		return fail(Failed, err.Error())
	}
	deadline := start.Add(d.Wall)
	var det core.TaskDetail
	cancelTask := func() { _ = call("task.cancel", map[string]string{"id": task.ID}, nil) }
	for {
		if err := call("task.get", map[string]string{"id": task.ID}, &det); err != nil {
			rec.Outcome, rec.Reason = Failed, err.Error()
			break
		}
		if len(det.Pending) > 0 {
			pr := det.Pending[0]
			if pr.Spec.Kind == "review" && strings.HasPrefix(pr.Spec.Title, "Review and merge") {
				res := accept(det.Task.Worktree)
				rec.EvalAccept = &res
				if res.Passed {
					rec.Outcome = Accepted
					if err := call("run.respond", map[string]any{"runId": runID, "requestId": pr.ID, "decision": "approved", "data": map[string]string{}}, nil); err != nil {
						rec.Notes = append(rec.Notes, "the merge approval was not taken: "+err.Error())
					} else if !d.waitCompleted(ctx, call, task.ID) {
						rec.Notes = append(rec.Notes, "the task did not reach COMPLETED within two minutes of the approval")
					}
				} else {
					rec.Outcome, rec.Reason = Rejected, "the hidden acceptance check failed"
					cancelTask()
				}
				break
			}
			if strings.HasPrefix(pr.Spec.Title, "Budget limit reached") {
				rec.Outcome, rec.Reason = Ceiling, pr.Spec.Title
			} else {
				rec.Outcome, rec.Reason = Failed, "Drydock asked the human ("+pr.Spec.Kind+"): "+pr.Spec.Title
			}
			cancelTask()
			break
		}
		if det.Run != nil && det.Run.Status.Terminal() {
			rec.Outcome, rec.Reason = Failed, fmt.Sprintf("the run ended %s: %s", det.Run.Status, det.Run.FailedReason)
			break
		}
		if time.Now().After(deadline) {
			rec.Outcome, rec.Reason = Failed, fmt.Sprintf("the wall-clock cap of %s ended it", d.Wall)
			cancelTask()
			break
		}
		select {
		case <-ctx.Done():
			rec.Outcome, rec.Reason = Failed, "interrupted: "+ctx.Err().Error()
			cancelTask()
			rec.WallMs = time.Since(start).Milliseconds()
			return rec, art
		case <-time.After(d.Poll):
		}
	}
	rec.WallMs = time.Since(start).Milliseconds()
	if len(det.Evidence) > 0 {
		art.Evidence, _ = json.MarshalIndent(det.Evidence, "", "  ")
	}
	for _, rv := range det.Reviews {
		rec.Reviews = append(rec.Reviews, Review{Verdict: rv.Verdict, By: rv.Reviewer, Feedback: clip(rv.Feedback, 4000)})
	}
	var usage []core.UsageRow
	if err := call("usage.list", map[string]any{"taskId": task.ID, "limit": 1000}, &usage); err == nil {
		d.addUsage(&rec, usage)
		art.Usage, _ = json.MarshalIndent(usage, "", "  ")
	}
	// every event of the task, in pages: the core returns at most 2,000 per call (and 500 when asked for more)
	var evs []json.RawMessage
	for after := uint64(0); ; {
		var page []json.RawMessage
		if err := call("events.replay", map[string]any{"taskId": task.ID, "after": after, "limit": eventPage}, &page); err != nil {
			rec.Notes = append(rec.Notes, "the task's events could not be read in full: "+err.Error())
			break
		}
		evs = append(evs, page...)
		if len(page) < eventPage {
			break
		}
		var last struct {
			Seq uint64 `json:"seq"`
		}
		if json.Unmarshal(page[len(page)-1], &last) != nil || last.Seq <= after {
			break
		}
		after = last.Seq
	}
	art.Events, _ = json.MarshalIndent(evs, "", "  ")
	for _, e := range evs {
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(e, &head) == nil && head.Type == "sandbox.command" {
			rec.SandboxCommands++
		}
		if head.Type == "agent.output" && readsSkill(e) {
			rec.SkillReads++
		}
	}
	return rec, art
}

func (d Drydock) addUsage(rec *Record, rows []core.UsageRow) {
	models := map[string]bool{}
	for _, u := range rows {
		if u.CacheHit {
			continue
		}
		rec.Sessions++
		if u.CostUSD > 0 {
			rec.CostUSD += u.CostUSD
		} else {
			rec.CallsWithoutCost++
		}
		rec.Tokens.Input += int64(u.Input)
		rec.Tokens.Output += int64(u.Output)
		rec.Tokens.CacheRead += int64(u.Cached)
		rec.Tokens.CacheWrite += int64(u.CacheWrite)
		if u.Model != "" {
			models[u.Model] = true
		}
	}
	rec.ModelsUsed = sortedKeys(models)
}

func (d Drydock) waitCompleted(ctx context.Context, call func(string, any, any) error, id string) bool {
	until := time.Now().Add(2 * time.Minute)
	for time.Now().Before(until) {
		var det core.TaskDetail
		if call("task.get", map[string]string{"id": id}, &det) == nil && det.Task.Status == tasks.Completed {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	return false
}

// eventPage is how many events the harness asks the core for at a time (the core's own maximum; tests make it small).
var eventPage = 2000

// nameLine is the first name: line of a SKILL.md front matter.
var nameLine = regexp.MustCompile(`(?m)\A(---\r?\n(?:.*\r?\n)*?name:[ \t]*)\S.*$`)

// readsSkill reports whether an agent.output event is a Read of a skill's on-demand file.
func readsSkill(e json.RawMessage) bool {
	var ev struct {
		Data struct {
			Kind string `json:"kind"`
			Data struct {
				Name string `json:"name"`
				Args struct {
					FilePath string `json:"file_path"`
				} `json:"args"`
			} `json:"data"`
		} `json:"data"`
	}
	if json.Unmarshal(e, &ev) != nil || ev.Data.Kind != "tool_call" || ev.Data.Data.Name != "Read" {
		return false
	}
	return strings.Contains(filepath.ToSlash(ev.Data.Data.Args.FilePath), "/"+skills.OnDemandDir+"/")
}

// copySkills copies each <name>/SKILL.md of src into dst and returns the names, sorted. A repository skill's front matter
// name must be its directory's name; registry skills are stored under their directory's name whatever they declare
// (skills.ParseRegistrySkill), so a different declared name is replaced by the directory's.
func copySkills(src, dst string) ([]string, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name(), "SKILL.md"))
		if err != nil {
			return nil, err
		}
		b = []byte(nameLine.ReplaceAllString(string(b), "${1}"+e.Name()))
		if err := os.MkdirAll(filepath.Join(dst, e.Name()), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name(), "SKILL.md"), b, 0o644); err != nil {
			return nil, err
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%s has no <name>/SKILL.md", src)
	}
	sort.Strings(names)
	return names, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// waitSandboxGone waits until no container of the task is left, or for at most limit.
func (d Drydock) waitSandboxGone(taskID string, limit time.Duration) {
	eng := sandbox.NewEngine()
	until := time.Now().Add(limit)
	for time.Now().Before(until) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		out, err := eng.Run(ctx, nil, "ps", "-aq", "--filter", "label="+sandbox.LabelTask+"="+taskID)
		cancel()
		if err == nil && strings.TrimSpace(out) == "" {
			return
		}
		time.Sleep(time.Second)
	}
}

// cleanup removes what the task's sandbox left, should the core have closed before its own cleanup ran.
func (d Drydock) cleanup(taskID string) {
	if d.Mode != ModeSandbox {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	m := &sandbox.Manager{Engine: sandbox.NewEngine()}
	_ = m.Remove(ctx, taskID)
}
