package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// ---- helpers shared by the template nodes ----

type turnOpts struct {
	Role     string // template role: pm, tests, architect, security
	ReadOnly bool
	Prompt   string
	Phase    string // manifest label
	PostAs   string // role name passed to OnTurn ("review" makes the room post a RESULT)
}

type turnOut struct {
	Text    string
	AgentID string
}

// roleTurn runs one agent turn for a template role in the task worktree, records the context manifest and the cost on the
// task, and lets the room post the agent's text. The agent is the one the run input pinned for the role.
func (td TemplateDeps) roleTurn(ctx context.Context, in workflow.NodeInput, t tasks.Task, o turnOpts) (turnOut, error) {
	if td.RoleConfig == nil {
		return turnOut{}, errors.New("pipeline: the template has no way to resolve agents by role")
	}
	cfg, err := td.RoleConfig(ctx, t, o.Role, td.templateInput(in).Roles[o.Role])
	if err != nil {
		return turnOut{}, err
	}
	cfg.TaskID, cfg.ProjectID = t.ID, t.ProjectID
	cfg.Spec.Cwd = t.Worktree
	if o.ReadOnly {
		cfg.Spec.PermissionMode, cfg.Spec.ReadOnly = "plan", true
	} else if td.sandboxed(ctx, t) {
		// a role that writes in the worktree (the test writer, the spec writer) runs sandboxed like the implementer
		if err := td.sandboxTurn(ctx, t, &cfg); err != nil {
			return turnOut{}, fmt.Errorf("the task's sandbox: %w", err)
		}
	}
	td.saveManifest(ctx, in, t, cfg.AgentID, o.Prompt, o.Phase)
	prompt := o.Prompt
	out, err := td.Runtime.AgentTask(rt.TaskSpec{Config: cfg, Prompt: func(workflow.NodeInput) string { return prompt }})(ctx, in)
	if err != nil {
		return turnOut{}, err
	}
	var res rt.TurnResult
	_ = json.Unmarshal(out.Value, &res)
	var cost float64
	for _, u := range res.Usage {
		cost += u.CostUSD
	}
	_, _ = td.Tasks.Patch(ctx, t.ID, tasks.Update{AddCost: cost})
	post := o.PostAs
	if post == "" {
		post = o.Role
	}
	if td.OnTurn != nil && strings.TrimSpace(res.Text) != "" {
		td.OnTurn(ctx, t, cfg.AgentID, post, res.Text)
	}
	return turnOut{Text: res.Text, AgentID: cfg.AgentID}, nil
}

// feedbackFromInbox collects {"feedback": ..., "previous": ...} messages (from a failed check, the human, a watchdog).
func feedbackFromInbox(in workflow.NodeInput) (feedback, previous string) {
	var fb []string
	for _, m := range in.Inbox {
		var o struct {
			Feedback string `json:"feedback"`
			Previous string `json:"previous"`
		}
		if json.Unmarshal(m.Value, &o) == nil {
			if o.Feedback != "" {
				fb = append(fb, o.Feedback)
			}
			if o.Previous != "" {
				previous = o.Previous
			}
		}
	}
	return strings.Join(fb, "\n\n"), previous
}

// humanBody is the response the engine delivers to the node after a request: {"decision","data","by","request"}.
type humanBody struct {
	Decision string          `json:"decision"`
	By       string          `json:"by"`
	Data     json.RawMessage `json:"data"`
}

func humanBodyOf(in workflow.NodeInput) (humanBody, bool) {
	for _, m := range in.Inbox {
		var h humanBody
		if json.Unmarshal(m.Value, &h) == nil && h.Decision != "" {
			return h, true
		}
	}
	return humanBody{}, false
}

func isHuman(by string) bool { return strings.HasPrefix(by, "human:") && len(by) > len("human:") }

func latestEvidence(ev []tasks.Evidence, kind string) (tasks.Evidence, bool) {
	for i := len(ev) - 1; i >= 0; i-- {
		if ev[i].Kind == kind {
			return ev[i], true
		}
	}
	return tasks.Evidence{}, false
}

// requireAccepted is the spec gate's second lock (the first is the graph): no test-writing, design or implementation turn
// may start for a task whose spec a human has not accepted.
func (td TemplateDeps) requireAccepted(ctx context.Context, t tasks.Task) error {
	ev, err := td.Tasks.Evidence(ctx, t.ID)
	if err != nil {
		return err
	}
	if e, ok := latestEvidence(ev, "spec_accept"); ok && e.Passed != nil && *e.Passed {
		var d struct {
			By string `json:"by"`
		}
		_ = json.Unmarshal(e.Data, &d)
		if isHuman(d.By) {
			return nil
		}
	}
	return fmt.Errorf("nothing is implemented until a human accepts the spec: task %s has no accepted spec", t.ID)
}

func (td TemplateDeps) evidenceDir(in workflow.NodeInput) string {
	dir := filepath.Join(td.DataDir, "runs", string(in.RunID), "evidence")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// ---- the spec gate ----

// specDraft: the PM-role agent writes the spec (read-only turn).
func (td TemplateDeps) specDraft() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		feedback, previous := feedbackFromInbox(in)
		out, err := td.roleTurn(ctx, in, t, turnOpts{Role: RolePM, ReadOnly: true, Phase: "spec", Prompt: SpecPrompt(t, previous, feedback, td.taskBrief(ctx, t))})
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(map[string]any{"text": out.Text, "agent": out.AgentID})
		return workflow.NodeOutput{Value: b}, nil
	}
}

// specCheck parses the spec. A spec without Given/When/Then criteria goes back to the PM and never reaches the human.
func (td TemplateDeps) specCheck() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var drafted struct {
			Text string `json:"text"`
		}
		for _, m := range in.Inbox {
			_ = json.Unmarshal(m.Value, &drafted)
		}
		spec, problems := ParseSpec(drafted.Text)
		if len(problems) > 0 {
			b, _ := json.Marshal(map[string]any{"feedback": "The spec cannot be used:\n- " + strings.Join(problems, "\n- "), "previous": drafted.Text})
			return workflow.NodeOutput{Value: b, Route: "invalid"}, nil
		}
		sum := sha256.Sum256([]byte(spec.Body))
		sha := hex.EncodeToString(sum[:])
		path := filepath.Join(td.evidenceDir(in), "spec-"+sha[:12]+".md")
		if err := os.WriteFile(path, []byte(spec.Body+"\n"), 0o644); err != nil {
			return workflow.NodeOutput{}, err
		}
		data, _ := json.Marshal(map[string]any{"sha": sha, "criteria": spec.Criteria})
		if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "spec", Summary: fmt.Sprintf("spec %q with %d acceptance criteria (Given/When/Then)", spec.Title, len(spec.Criteria)), Ref: path, Data: data}); err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(map[string]any{"spec": spec, "sha": sha, "ref": path})
		return workflow.NodeOutput{Value: b, Route: "valid"}, nil
	}
}

// SpecReviewPayload is what the human sees when asked to accept a spec.
type SpecReviewPayload struct {
	Phase    string      `json:"phase"` // "spec"
	TaskID   string      `json:"taskId"`
	Title    string      `json:"title"`
	Spec     string      `json:"spec"`
	Criteria []Criterion `json:"criteria"`
	SHA      string      `json:"sha"`
	Ref      string      `json:"ref"`
	Note     string      `json:"note"`
}

// specReview puts the spec to the human as a `review` request. The task waits for the human (WAITING_HUMAN) and nothing
// else runs: no node is reachable from here except through specaccept.
func (td TemplateDeps) specReview() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var v struct {
			Spec Spec   `json:"spec"`
			SHA  string `json:"sha"`
			Ref  string `json:"ref"`
		}
		for _, m := range in.Inbox {
			_ = json.Unmarshal(m.Value, &v)
		}
		if len(v.Spec.Criteria) == 0 {
			return workflow.NodeOutput{}, errors.New("spec review without a parsed spec")
		}
		if cur, err := td.Tasks.Get(ctx, t.ID); err == nil && cur.Status == tasks.InProgress {
			if _, err := td.Tasks.Transition(ctx, t.ID, tasks.WaitingHuman, tasks.TransitionOpts{By: "system", Reason: "waiting for the human to accept the spec"}); err != nil {
				return workflow.NodeOutput{}, err
			}
		}
		payload, _ := json.Marshal(SpecReviewPayload{Phase: "spec", TaskID: t.ID, Title: v.Spec.Title, Spec: clip(v.Spec.Body, 40000), Criteria: v.Spec.Criteria, SHA: v.SHA, Ref: v.Ref,
			Note: "Approve to accept the spec and start building. Reject with feedback to send it back to the PM. Optionally answer with data.criteria (a list of Given/When/Then lines) to replace the criteria."})
		return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "review", Title: "Accept the spec: " + t.Title, Payload: payload}}, nil
	}
}

// specAccept records the human's acceptance. Only a human identity passes; the criteria become the task's acceptance
// criteria (with their ids), and the task goes back to IN_PROGRESS.
func (td TemplateDeps) specAccept() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		h, ok := humanBodyOf(in)
		if !ok || !isHuman(h.By) {
			return workflow.NodeOutput{}, fmt.Errorf("the spec can only be accepted by a human; got %q", h.By)
		}
		var v struct {
			Spec Spec   `json:"spec"`
			SHA  string `json:"sha"`
			Ref  string `json:"ref"`
		}
		if raw, ok := in.Shared["out/speccheck"]; ok {
			_ = json.Unmarshal(raw, &v)
		}
		if len(v.Spec.Criteria) == 0 {
			return workflow.NodeOutput{}, errors.New("no checked spec to accept")
		}
		criteria := v.Spec.Criteria
		var edit struct {
			Criteria []string `json:"criteria"`
		}
		if len(h.Data) > 0 && json.Unmarshal(h.Data, &edit) == nil && len(edit.Criteria) > 0 {
			cs, problems := ParseCriteriaLines(edit.Criteria)
			if len(problems) > 0 {
				b, _ := json.Marshal(map[string]any{"feedback": "The criteria you edited are not usable:\n- " + strings.Join(problems, "\n- "), "previous": v.Spec.Body})
				return workflow.NodeOutput{Value: b, Route: "revise"}, nil
			}
			criteria = cs
		}
		acc := CriteriaStrings(criteria)
		if _, err := td.Tasks.Patch(ctx, t.ID, tasks.Update{Acceptance: &acc}); err != nil {
			return workflow.NodeOutput{}, err
		}
		pass := true
		data, _ := json.Marshal(map[string]any{"by": h.By, "sha": v.SHA, "criteria": criteria})
		if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "spec_accept", Passed: &pass, Summary: fmt.Sprintf("spec accepted by %s with %d acceptance criteria", h.By, len(criteria)), Ref: v.Ref, Data: data}); err != nil {
			return workflow.NodeOutput{}, err
		}
		if _, err := td.Tasks.Transition(ctx, t.ID, tasks.InProgress, tasks.TransitionOpts{By: h.By, Reason: "spec accepted"}); err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(map[string]any{"accepted": criteria, "by": h.By})
		return workflow.NodeOutput{Value: b, Route: "ok"}, nil
	}
}

// specRevise sends a rejected spec (or invalid human edits) back to the PM with the feedback.
func (td TemplateDeps) specRevise() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		fb, prev := feedbackFromInbox(in)
		by := "system"
		if h, ok := humanBodyOf(in); ok {
			if !isHuman(h.By) {
				return workflow.NodeOutput{}, fmt.Errorf("a spec can only be sent back by a human; got %q", h.By)
			}
			by = h.By
			hh := humanResponse(in)
			fb = strings.TrimSpace(hh.feedback)
			if fb == "" {
				fb = "The human rejected the spec without comment; tighten the scope and make every criterion observable."
			}
			fb = "The human reviewer sent the spec back: " + fb
		}
		if raw, ok := in.Shared["out/speccheck"]; ok && prev == "" {
			var v struct {
				Spec Spec `json:"spec"`
			}
			if json.Unmarshal(raw, &v) == nil {
				prev = v.Spec.Body
			}
		}
		if cur, err := td.Tasks.Get(ctx, t.ID); err == nil && cur.Status == tasks.WaitingHuman {
			if _, err := td.Tasks.Transition(ctx, t.ID, tasks.InProgress, tasks.TransitionOpts{By: by, Reason: "spec sent back"}); err != nil {
				return workflow.NodeOutput{}, err
			}
		}
		b, _ := json.Marshal(map[string]any{"feedback": fb, "previous": prev})
		return workflow.NodeOutput{Value: b}, nil
	}
}

// design: the optional architect step of the SaaS template. Without an architect on the team it passes through.
func (td TemplateDeps) design() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		if err := td.requireAccepted(ctx, t); err != nil {
			return workflow.NodeOutput{}, err
		}
		if td.templateInput(in).Roles[RoleArchitect] == "" {
			return workflow.NodeOutput{Value: json.RawMessage(`{"design":"skipped: no architect agent on the team"}`)}, nil
		}
		cur, _ := td.Tasks.Get(ctx, t.ID)
		out, err := td.roleTurn(ctx, in, cur, turnOpts{Role: RoleArchitect, ReadOnly: true, Phase: "design", Prompt: DesignPrompt(cur, td.acceptedSpec(ctx, t.ID), td.taskBrief(ctx, t))})
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		path := filepath.Join(td.evidenceDir(in), "design.md")
		if err := os.WriteFile(path, []byte(out.Text+"\n"), 0o644); err != nil {
			return workflow.NodeOutput{}, err
		}
		if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "design", Summary: fmt.Sprintf("design notes by the architect (%d bytes)", len(out.Text)), Ref: path}); err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(map[string]any{"design": clip(out.Text, 4000)})
		return workflow.NodeOutput{Value: b}, nil
	}
}

// ---- test first ----

// writeTests: an agent writes the tests (or the reproducing test of a bug) before any implementation exists.
func (td TemplateDeps) writeTests(bug bool) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		if !bug {
			if err := td.requireAccepted(ctx, t); err != nil {
				return workflow.NodeOutput{}, err
			}
		}
		feedback, _ := feedbackFromInbox(in)
		phase := "write-tests"
		if bug {
			phase = "reproduce"
		}
		spec := ""
		if !bug {
			spec = td.acceptedSpec(ctx, t.ID)
		}
		out, err := td.roleTurn(ctx, in, t, turnOpts{Role: RoleTests, Phase: phase, Prompt: TestsPrompt(t, bug, feedback, spec, td.taskBrief(ctx, t))})
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(map[string]any{"text": clip(out.Text, 2000), "agent": out.AgentID})
		return workflow.NodeOutput{Value: b}, nil
	}
}

var testPathRE = regexp.MustCompile(`(?i)(^|/)(tests?|__tests__|e2e|specs?|testdata|fixtures|__snapshots__)/|(_test\.[a-z0-9]+$)|(\.(test|spec)\.[a-z0-9]+$)|(^|/)test_[^/]*$|(_spec\.[a-z0-9]+$)|(^|/)conftest\.py$`)

// IsTestPath reports whether a repo-relative path is a test file or test data by the usual conventions.
func IsTestPath(p string) bool { return testPathRE.MatchString(filepath.ToSlash(p)) }

// testScope checks what the test-writing turn changed: only test files, at least one, and (for a feature) every accepted
// criterion id is referenced by a changed test, so the criteria are really "turned into tests".
func (td TemplateDeps) testScope(trace bool) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		repo, err := td.Repo(ctx, t.ProjectID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		st, err := repo.Status(ctx, worktreeOf(t))
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var testFiles, other []string
		for _, f := range st.Files() {
			if IsTestPath(f) {
				testFiles = append(testFiles, f)
			} else {
				other = append(other, f)
			}
		}
		var problems []string
		if len(testFiles) == 0 {
			problems = append(problems, "no test file was created or changed")
		}
		if len(other) > 0 {
			problems = append(problems, "these non-test files changed, and only tests may change at this step (revert them): "+strings.Join(other, ", "))
		}
		if trace && len(testFiles) > 0 {
			var content strings.Builder
			for _, f := range testFiles {
				if b, err := os.ReadFile(filepath.Join(t.Worktree, f)); err == nil && len(b) < 1<<20 {
					content.Write(b)
					content.WriteByte('\n')
				}
			}
			var missing []string
			for _, a := range t.Acceptance {
				id := criterionID(a)
				if id == "" {
					continue
				}
				if !regexp.MustCompile(regexp.QuoteMeta(id) + `([^0-9]|$)`).MatchString(content.String()) {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				problems = append(problems, "no test refers to "+strings.Join(missing, ", ")+": every acceptance criterion needs a test that carries its id in its name or in a comment")
			}
		}
		if len(problems) > 0 {
			b, _ := json.Marshal(map[string]any{"feedback": "- " + strings.Join(problems, "\n- ")})
			return workflow.NodeOutput{Value: b, Route: "badscope"}, nil
		}
		b, _ := json.Marshal(map[string]any{"testFiles": testFiles})
		return workflow.NodeOutput{Value: b, Route: "ok"}, nil
	}
}

var acID = regexp.MustCompile(`^\s*(AC\d+)\b`)

func criterionID(s string) string {
	if m := acID.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// detectTest returns the project's `test` check, the same command red and green both use.
func (td TemplateDeps) detectTest(t tasks.Task) (checks.Spec, bool) {
	for _, s := range td.detect()(t.Worktree) {
		if s.Name == "test" {
			return s, true
		}
	}
	return checks.Spec{}, false
}

// redCheck runs the project's test command after the tests were written and before any implementation: for new behaviour
// it must FAIL (red). A passing run means the tests prove nothing and goes back to the test writer.
func (td TemplateDeps) redCheck() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		spec, ok := td.detectTest(t)
		if !ok {
			return workflow.NodeOutput{}, errors.New("no test command detected for this project: red/green evidence needs one (add a Taskfile test target, go.mod or a package.json test script)")
		}
		spec.LogDir = filepath.Join(td.DataDir, "runs", string(in.RunID), "checks")
		wrapped, err := td.sandboxChecks(ctx, t, []checks.Spec{spec})
		if err != nil {
			return sandboxTrouble(in, err)
		}
		spec = wrapped[0]
		res, err := checks.Run(ctx, td.PM, spec, func(line string) {
			in.Emit(workflow.NewChunk(workflow.ChunkLog, workflow.TextDelta{Text: line}))
		})
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		if res.TimedOut {
			return workflow.NodeOutput{}, fmt.Errorf("the test command timed out while establishing the red state: %s", truncateStr(res.Output, 400))
		}
		data, _ := json.Marshal(map[string]any{"argv": res.Argv, "exitCode": res.ExitCode, "failing": res.FailingTests, "durationMs": res.Duration.Milliseconds()})
		if res.Passed {
			b, _ := json.Marshal(map[string]any{"feedback": "All tests pass before the implementation exists, so the new tests do not test the new behaviour. Make them assert the specified behaviour so that they fail until it is built."})
			return workflow.NodeOutput{Value: b, Route: "notred"}, nil
		}
		summary := fmt.Sprintf("RED as required: the test command fails before any implementation (exit %d)", res.ExitCode)
		if len(res.FailingTests) > 0 {
			summary += ": " + strings.Join(res.FailingTests, ", ")
		}
		if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "red_test", Summary: truncateStr(summary, 300), Ref: res.LogRef, Data: data}); err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(map[string]any{"red": true, "failing": res.FailingTests, "output": res.Output})
		return workflow.NodeOutput{Value: b, Route: "red"}, nil
	}
}

// implement is the work node of the test-first and security templates: the task's worker in the worktree, with the failing
// tests, the design notes or the scan findings added to the first prompt. Later passes get only the failure or feedback.
func (td TemplateDeps) implement() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		if td.gated(in) {
			if err := td.requireAccepted(ctx, t); err != nil {
				return workflow.NodeOutput{}, err
			}
		}
		var red struct {
			Red     bool     `json:"red"`
			Failing []string `json:"failing"`
			Output  string   `json:"output"`
		}
		var scan struct {
			Summary string `json:"scanSummary"`
		}
		clean := in
		clean.Inbox = nil
		for _, m := range in.Inbox {
			var probe struct {
				Red     bool   `json:"red"`
				Summary string `json:"scanSummary"`
			}
			if json.Unmarshal(m.Value, &probe) == nil && probe.Red {
				_ = json.Unmarshal(m.Value, &red)
				continue
			}
			if json.Unmarshal(m.Value, &probe) == nil && probe.Summary != "" {
				scan.Summary = probe.Summary
				continue
			}
			var body humanBody
			if json.Unmarshal(m.Value, &body) == nil && body.Decision != "" {
				if d := strings.TrimSpace(string(body.Data)); d == "" || d == "null" || d == "{}" || d == `""` {
					continue // a bare "continue" from the human is not guidance
				}
			}
			clean.Inbox = append(clean.Inbox, m)
		}
		cfg := td.AgentConfig(t, "work")
		applyRoute(ctx, &cfg)
		cfg.TaskID, cfg.ProjectID = t.ID, t.ProjectID
		cfg.Spec.Cwd = t.Worktree
		if q, err := td.prepareWorker(ctx, in, t, &cfg); q != nil || err != nil {
			if q == nil {
				return workflow.NodeOutput{}, err
			}
			return *q, err
		}
		// the accepted spec and the architect's notes go with every implementation pass, not only the first
		prompt := WorkPrompt(t, clean, td.promptContext(ctx, t, td.gated(in)))
		if red.Red {
			prompt += ImplementSuffix(red.Failing, red.Output)
		}
		if scan.Summary != "" {
			prompt += "\n\nA security scan of the tree found the following. Fix every finding that is real, and do not just silence the scanner:\n" + quoted("Scan results", clip(scan.Summary, 4000))
		}
		td.saveManifest(ctx, in, t, cfg.AgentID, prompt, "work")
		out, err := td.Runtime.AgentTask(rt.TaskSpec{Config: cfg, Prompt: func(workflow.NodeInput) string { return prompt }})(ctx, in)
		if err != nil {
			return out, err
		}
		var res rt.TurnResult
		if json.Unmarshal(out.Value, &res) == nil {
			var cost float64
			for _, u := range res.Usage {
				cost += u.CostUSD
			}
			_, _ = td.Tasks.Patch(ctx, t.ID, tasks.Update{AddCost: cost})
			td.recordDoD(ctx, t, res.Text)
			if td.OnTurn != nil && strings.TrimSpace(res.Text) != "" {
				td.OnTurn(ctx, t, cfg.AgentID, "work", res.Text)
			}
		}
		return out, nil
	}
}

// gated reports whether this run's workflow has the spec gate (so the implementation requires an accepted spec). The pull
// request variants (feature-pr, full-stack-saas-pr) have the same gate.
func (td TemplateDeps) gated(in workflow.NodeInput) bool {
	return IsGated(in.Workflow)
}

// IsGated reports whether a workflow (local or pull request variant) has the spec gate.
func IsGated(workflowName string) bool {
	w := strings.TrimSuffix(workflowName, PRSuffix)
	return w == WorkflowFeature || w == WorkflowSaaS
}

// verifyTDD is Verify plus what the test-first templates add: after the project's checks pass, the e2e check (when the
// project enables it and defines it), and the red-to-green evidence that proves the tests written first now pass.
func (td TemplateDeps) verifyTDD() workflow.NodeHandler {
	base := Verify(td.Deps)
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		out, err := base(ctx, in)
		if err != nil || out.Route != "pass" {
			return out, err
		}
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		dev := td.dev(ctx, t.ProjectID)
		if dev.E2E.Enabled && td.sandboxed(ctx, t) {
			// the e2e check would run the project's code on this machine; in a sandboxed project the worker runs end-to-end tests
			// in the container itself (sandbox_run), and this check says it was not run
			reason := "the project runs its commands in a sandbox, and this e2e check does not run there yet; the worker runs end-to-end tests in the sandbox with sandbox_run"
			if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "e2e", Summary: "e2e skipped: " + reason}); err != nil {
				return workflow.NodeOutput{}, err
			}
			td.emit(ctx, "task.check_inconclusive", t, map[string]any{"kind": "e2e", "status": "skipped", "reason": reason})
			return out, nil
		}
		res, err := checks.RunE2E(ctx, td.PM, t.Worktree, checks.E2EOptions{Enabled: dev.E2E.Enabled, Command: dev.E2E.Command,
			Timeout: time.Duration(dev.E2E.TimeoutSec) * time.Second, LogDir: filepath.Join(td.DataDir, "runs", string(in.RunID), "checks"),
			Env: []string{"PLAYWRIGHT_JSON_OUTPUT_NAME=test-results/e2e-report.json"}}, func(line string) {
			in.Emit(workflow.NewChunk(workflow.ChunkLog, workflow.TextDelta{Text: line}))
		})
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		data, _ := json.Marshal(map[string]any{"argv": res.Argv, "source": res.Source, "exitCode": res.ExitCode, "failing": res.FailingTests, "summary": res.Summary, "skipped": res.Skipped})
		switch {
		case res.Inconclusive():
			reason := nonEmptyStr(res.SkipReason, res.Output)
			if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "e2e", Summary: truncateStr("e2e skipped: "+reason, 300), Data: data}); err != nil {
				return workflow.NodeOutput{}, err
			}
			td.emit(ctx, "task.check_inconclusive", t, map[string]any{"kind": "e2e", "status": "skipped", "reason": truncateStr(reason, 300)})
			if td.OnInconclusive != nil {
				td.OnInconclusive(ctx, t, res.Result)
			}
		default:
			passed := res.Passed
			summary := summarize(res.Result)
			if res.Summary != nil {
				summary += fmt.Sprintf(" (%d passed, %d failed, %d skipped)", res.Summary.Expected, res.Summary.Unexpected, res.Summary.Skipped)
			}
			if _, err := td.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "e2e", Passed: &passed, Summary: truncateStr(summary, 300), Ref: res.LogRef, Data: data}); err != nil {
				return workflow.NodeOutput{}, err
			}
			for _, s := range res.Screenshots {
				if _, err := td.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "screenshot", Summary: filepath.Base(s), Ref: s}); err != nil {
					return workflow.NodeOutput{}, err
				}
			}
			if td.OnCheck != nil {
				td.OnCheck(ctx, t, res.Result)
			}
			if !res.Passed {
				b, _ := json.Marshal(res.CheckResult)
				return workflow.NodeOutput{Value: b, Route: "fail"}, nil
			}
		}
		if ev, err := td.Tasks.Evidence(ctx, t.ID); err == nil {
			if red, ok := latestEvidence(ev, "red_test"); ok {
				var d struct {
					Failing []string `json:"failing"`
				}
				_ = json.Unmarshal(red.Data, &d)
				pass := true
				summary := "GREEN: the test command that failed before the implementation now passes"
				if len(d.Failing) > 0 {
					summary = fmt.Sprintf("GREEN: %d test(s) that failed before the implementation now pass (%s)", len(d.Failing), strings.Join(d.Failing, ", "))
				}
				if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "green_test", Passed: &pass, Summary: truncateStr(summary, 300), Data: red.Data}); err != nil {
					return workflow.NodeOutput{}, err
				}
			}
		}
		return out, nil
	}
}

// previewNode starts the project's preview before the human approves, when the project enables it. A preview that does
// not start is recorded and shown but does not block the human (the diff, the checks and the review are still there).
func (td TemplateDeps) previewNode() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		dev := td.dev(ctx, t.ProjectID)
		var res checks.PreviewResult
		if td.Preview == nil {
			res.Result = checks.SkippedResult("preview", "this build has no preview runner")
		} else if res, err = td.Preview(ctx, t, dev); err != nil {
			res.Result = checks.SkippedResult("preview", "")
			res.Skipped, res.Errored, res.SkipReason = false, true, "the preview could not start: "+err.Error()
			res.Output = res.SkipReason
		}
		data, _ := json.Marshal(map[string]any{"url": res.URL, "source": res.Source, "skipped": res.Skipped})
		switch {
		case res.Inconclusive():
			reason := nonEmptyStr(res.SkipReason, res.Output)
			if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "preview", Summary: truncateStr("preview not available: "+reason, 300), Data: data}); err != nil {
				return workflow.NodeOutput{}, err
			}
			if td.OnInconclusive != nil {
				td.OnInconclusive(ctx, t, res.Result)
			}
		case res.Passed:
			pass := true
			if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "preview", Passed: &pass, Summary: "preview ready at " + res.URL, Ref: res.URL, Data: data}); err != nil {
				return workflow.NodeOutput{}, err
			}
			if td.OnCheck != nil {
				td.OnCheck(ctx, t, res.Result)
			}
		default:
			fail := false
			if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "preview", Passed: &fail, Summary: truncateStr("preview did not start: "+res.Output, 300), Ref: res.LogRef, Data: data}); err != nil {
				return workflow.NodeOutput{}, err
			}
			if td.OnCheck != nil {
				td.OnCheck(ctx, t, res.Result)
			}
		}
		return workflow.NodeOutput{Value: json.RawMessage(`{"preview":true}`)}, nil
	}
}

// securityReview is the independent reviewer with the security role: it sees the scan evidence and the diff, and returns
// SHIP or REVISE like the generic reviewer.
func (td TemplateDeps) securityReview() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		if t.Status != tasks.Review {
			return workflow.NodeOutput{}, fmt.Errorf("task is %s, not REVIEW: nothing to review (checks must pass with evidence first)", t.Status)
		}
		ev, err := td.Tasks.Evidence(ctx, t.ID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var diff string
		if d, ok := latestEvidence(ev, "diff"); ok {
			b, _ := os.ReadFile(d.Ref)
			diff = string(b)
		}
		agentID := td.templateInput(in).Roles[RoleSecurity]
		if agentID == t.Assignee {
			return workflow.NodeOutput{}, errors.New("the security reviewer must be a different agent from the one that did the work")
		}
		out, err := td.roleTurn(ctx, in, t, turnOpts{Role: RoleSecurity, ReadOnly: true, Phase: "security-review", PostAs: "review", Prompt: SecurityReviewPrompt(t, ev, diff)})
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		v := ParseVerdict(out.Text)
		model := ""
		if cfg, err := td.RoleConfig(ctx, t, RoleSecurity, agentID); err == nil {
			model = cfg.Spec.Model
		}
		if _, err := td.Tasks.AddReview(ctx, t.ID, tasks.ReviewRecord{Attempt: t.Iteration, Reviewer: out.AgentID, Verdict: v.Verdict, Feedback: v.Feedback, Model: model}); err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(v)
		if v.Verdict == "SHIP" {
			if _, err := td.Tasks.Transition(ctx, t.ID, tasks.Verification, tasks.TransitionOpts{By: out.AgentID, Reason: "the security reviewer returned SHIP"}); err != nil {
				return workflow.NodeOutput{}, err
			}
			return workflow.NodeOutput{Value: b, Route: "ship"}, nil
		}
		if _, err := td.Tasks.Transition(ctx, t.ID, tasks.InProgress, tasks.TransitionOpts{By: out.AgentID, Reason: "the security reviewer returned REVISE", Feedback: v.Feedback}); err != nil {
			return workflow.NodeOutput{Value: b, Route: "limit"}, nil
		}
		return td.reviseOutput(ctx, t, v, b), nil
	}
}
