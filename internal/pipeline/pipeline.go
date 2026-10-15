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
	"slices"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/checks"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/skills"
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
	// Scanner scans the lines a task added for secrets (the `secrets` check). Default: checks.DefaultScanner.
	Scanner security.SecretScanner
	// Redact, if set, removes secrets from a handoff artifact before it is stored and given to an agent (the core passes its
	// scanner, which also knows the stored provider keys). Nil: Scanner's Redact.
	Redact func(string) string
	// DepsBinary overrides the osv-scanner executable of the `deps` check (tests; default osv-scanner on PATH).
	DepsBinary string
	// MCPStatus, if set, says whether (and with which tools) the agent's turn gets the MCP gateway entry, so the context
	// manifest can list it as included or excluded with the reason. The wiring itself is rt.Runtime.MCP, which sees every
	// session; this is only the read side for the manifest. Nil: the manifest says MCP is not wired in this build.
	MCPStatus func(ctx context.Context, t tasks.Task, agentID, phase string) MCPStatus
	// WorkerChecks, if set, reports whether a worker turn on this task may run the project's check commands (the project
	// switched that on and the agent's CLI honors exact allow rules). Never consulted for a reviewer. Nil: never.
	WorkerChecks func(ctx context.Context, t tasks.Task, cfg rt.Config) bool
	// Sandbox, if set, runs the commands of a project that switched its sandbox on in a container per task (docs/sandbox.md):
	// worker turns get the sandbox tools instead of a shell, and the checks run in the container. Nil: never.
	Sandbox SandboxHook
	// GuardPoll is how often a task waiting for an earlier task looks again (default 2 seconds).
	GuardPoll time.Duration
	// ProjectBrief, if set, returns the brief the human wrote for the project. It is put, bounded and labelled as the human's,
	// into the worker, spec, design, test-writing and planner prompts (and the core feeds the same text into the generated
	// AGENTS.md). Nil, or an empty brief: nothing is added.
	ProjectBrief func(ctx context.Context, projectID string) string
	// PlanDir, if set, is the folder a read-only planning turn runs in (the repository, or a private folder for a project that
	// has no repository yet). Nil: the repository root.
	PlanDir func(ctx context.Context, projectID string) (string, error)
	// Lead, if set, runs the team lead's supervision turn when a deterministic trigger fires (an iteration limit, repeated
	// identical review feedback). Nil, or a project without a lead agent: the behaviour is exactly as without a lead.
	Lead LeadSupervisor
	// ResetWatch, if set, is called when a human continues a task past a limit and when a task moves on successfully (its
	// checks passed and it entered review): the watchdog's trip count for the task starts again (watchdog.Watchdog.Reset).
	ResetWatch func(ctx context.Context, taskID string)
}

// MCPStatus is the manifest view of one turn's MCP attachment.
type MCPStatus struct {
	Attached bool
	Reason   string   // why not, when not attached
	Tools    []string // exposed tool names the run may call, when attached
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
		w, err := repo.CreateWorktree(ctx, git.WorktreeReq{TaskID: t.ID, Title: t.Title, Base: t.BaseBranch, Parent: repo.WorktreeParent(d.WorktreeRoot, t.ProjectID)})
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

// Work runs one agent turn in the task worktree. Every pass carries the task (title, description, acceptance criteria) and the
// project brief; a later pass adds only the latest failure, review feedback or guidance (token rule 16: the prompt never
// carries the previous session's history). A later pass continues the previous turn's session while its context occupancy is
// below ContextHandoffPct; otherwise it starts a fresh session seeded with a handoff artifact (session.go).
func Work(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		cfg := d.AgentConfig(t, "work")
		applyRoute(ctx, &cfg)
		cfg.TaskID, cfg.ProjectID = t.ID, t.ProjectID
		cfg.Spec.Cwd = t.Worktree
		if q, err := d.prepareWorker(ctx, in, t, &cfg); q != nil || err != nil {
			if q == nil {
				return workflow.NodeOutput{}, err
			}
			return *q, err
		}
		return d.runWorkTurn(ctx, in, t, cfg, WorkPrompt(t, in, d.promptContext(ctx, t, false)))
	}
}

// promptContext gathers what a builder's turn carries besides the task: the brief, the accepted spec and design notes (when the
// task went through the spec gate) and the worker's own definition of done for a task without criteria.
func (d Deps) promptContext(ctx context.Context, t tasks.Task, withSpec bool) PromptContext {
	pc := PromptContext{Brief: d.taskBrief(ctx, t)}
	if withSpec {
		pc.Spec = d.acceptedSpec(ctx, t.ID)
		pc.Design = d.designNotes(ctx, t.ID)
	}
	if len(t.Acceptance) == 0 {
		if ev, err := d.Tasks.Evidence(ctx, t.ID); err == nil {
			pc.DoD = statedDoD(ev)
		}
	}
	return pc
}

// addWorkerTools lets a worker turn run the project's check commands, when the project allows it: the exact command lines
// of checks.WorkerRules become allow rules, and the system prompt lists the ones Drydock will run after this turn, found with
// the same detection Verify uses, so the worker types what is allowed rather than a variant the CLI refuses.
func (d Deps) addWorkerTools(ctx context.Context, t tasks.Task, cfg *rt.Config) {
	if d.WorkerChecks == nil || t.Worktree == "" || !d.WorkerChecks(ctx, t, *cfg) {
		return
	}
	cfg.Spec.AllowedTools = append(append([]string(nil), cfg.Spec.AllowedTools...), checks.WorkerRules()...)
	cfg.Spec.SystemAppend = strings.TrimSpace(cfg.Spec.SystemAppend + "\n\n" + WorkerChecksText(checks.WorkerCommands(d.detect()(t.Worktree))))
}

// detect is Deps.Checks, or checks.Detect when it is not set.
func (d Deps) detect() func(dir string) []checks.Spec {
	if d.Checks != nil {
		return d.Checks
	}
	return checks.Detect
}

// WorkerChecksText tells a worker which commands it may run itself. now is what Drydock will run after the turn.
func WorkerChecksText(now []string) string {
	var b strings.Builder
	b.WriteString("You may run this project's checks yourself with the Bash tool while you work. Type each command exactly as shown, ")
	b.WriteString("run from the working folder, with nothing added: no other arguments, no pipes, no redirection, no cd. Any other ")
	b.WriteString("command will be refused.\n")
	if len(now) > 0 {
		b.WriteString("Drydock runs these after your turn:\n")
		for _, c := range now {
			b.WriteString("- " + c + "\n")
		}
	} else {
		b.WriteString("No checks are set up in this project yet.\n")
	}
	b.WriteString("If you add a go.mod, a package.json script or a Taskfile target, these forms are also allowed: go build ./..., ")
	b.WriteString("go test ./..., go vet ./..., npm run <build|test|lint|typecheck> --silent, task <build|test|lint|typecheck>.")
	return b.String()
}

// InstructionBudget is how much instruction text (AGENTS.md and .claude/rules) may go into every Claude turn before the
// manifest says it is over budget: about 8,000 tokens, close to the whole rest of a quiet session's first request (P36).
const InstructionBudget = 32 << 10

type mdFile struct {
	name string
	size int
}

// repoMapSectionBytes is the size of the repo map section of a generated AGENTS.md (0: it has none).
func repoMapSectionBytes(agentsMD string) int {
	b, err := os.ReadFile(agentsMD)
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.Index(s, "\n## "+ctxeng.RepoMapSectionTitle+"\n")
	if i < 0 {
		return 0
	}
	rest := s[i+1:]
	if j := strings.Index(rest[3:], "\n## "); j >= 0 {
		return j + 4
	}
	return len(rest)
}

// mdFiles lists the .md files directly in dir, sorted by name.
func mdFiles(dir string) []mdFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []mdFile
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".md") {
			if fi, err := e.Info(); err == nil {
				out = append(out, mdFile{e.Name(), int(fi.Size())})
			}
		}
	}
	return out
}

// saveManifest records what this attempt was given and what was deliberately left out.
// sess is the session decision of a work turn (nil for the other turns, which always start a fresh session).
func (d Deps) saveManifest(ctx context.Context, in workflow.NodeInput, t tasks.Task, agentID, prompt, role string, sess *SessionDecision) {
	if d.Context == nil {
		return
	}
	var m ctxeng.Manifest
	m.Add("prompt ("+role+")", true, len(prompt), "the task (compact on later passes) and the project brief on every pass; later passes add only the latest failure, feedback or guidance (token rule 16)", 1)
	every := 0 // what Claude gets in every turn: AGENTS.md (CLAUDE.md imports it) and .claude/rules
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		if st, err := os.Stat(filepath.Join(t.Worktree, name)); err == nil {
			m.Add("file:"+name, true, int(st.Size()), "generated instruction file: read natively by CLIs that load it; Claude Code gets its text in the system prompt (Drydock starts it with the human's own settings only, which skips project files)", 5)
			if name == "AGENTS.md" {
				every += int(st.Size())
			}
		}
	}
	for _, f := range mdFiles(filepath.Join(t.Worktree, ".claude", "rules")) {
		m.Add("file:.claude/rules/"+f.name, true, f.size, "skill or rule Claude Code gets in every turn", 5)
		every += f.size
	}
	for _, f := range mdFiles(filepath.Join(t.Worktree, filepath.FromSlash(skills.OnDemandDir))) {
		m.Add("file:"+skills.OnDemandDir+"/"+f.name, false, f.size, "skill listed in AGENTS.md: the agent reads it with Read when the work needs it (skill delivery: relevant)", 5)
	}
	if every > InstructionBudget {
		m.Add("budget:instructions", true, every, fmt.Sprintf("OVER BUDGET: %d KB of instructions go into every Claude turn, more than the %d KB budget; with skill delivery all, set it to relevant; otherwise give this agent fewer skills, shorten the repository's own skills (.aet/skills), or give them paths so they stay out of tasks that name other files", every>>10, InstructionBudget>>10), 0)
	} else {
		m.Add("budget:instructions", true, every, fmt.Sprintf("within budget: %d KB of instructions go into every Claude turn (budget %d KB)", (every+1023)>>10, InstructionBudget>>10), 0)
	}
	repoMapTool := false
	if d.MCPStatus == nil {
		m.Add("mcp:drydock", false, 0, "MCP gateway not wired in this build: the agent has no search_pack, search_requirements, search_memory, skills or read_range tools", 4)
	} else if st := d.MCPStatus(ctx, t, agentID, role); st.Attached {
		m.Add("mcp:drydock", true, 0, "one gateway entry (loopback, per-run token): tools "+strings.Join(st.Tools, ", ")+"; results are fetched on demand, nothing is preloaded", 4)
		repoMapTool = slices.Contains(st.Tools, "repo_map")
	} else {
		m.Add("mcp:drydock", false, 0, "not attached: "+st.Reason, 4)
	}
	if n := repoMapSectionBytes(filepath.Join(t.Worktree, "AGENTS.md")); n > 0 {
		m.Add("repo_map", true, n, "token-budgeted repo map in AGENTS.md (section "+ctxeng.RepoMapSectionTitle+"); a bigger or focused one through the repo_map tool (token rule 9)", 9)
	} else if repoMapTool {
		m.Add("repo_map", false, 0, "not in AGENTS.md (a small repository, or the map could not be made in time); on demand through the repo_map tool", 9)
	} else {
		m.Add("repo_map", false, 0, "no repo map for this turn: this build has no tree-sitter parser (built without CGO), or the agent has no MCP gateway", 9)
	}
	m.Add("project memory", false, 0, "the newest accepted entries are in AGENTS.md up to a budget; the rest is retrieved on demand (search_memory)", 8)
	m.Add("room history", false, 0, "no messages selected for this task channel", 7)
	switch {
	case sess == nil:
		m.Add("previous session transcript", false, 0, "fresh session per attempt; handoff artifacts only (token rules 5 and 16)", 6)
	case sess.Action == SessionContinue:
		m.Add("previous session transcript", true, 0, "continued session "+sess.PrevSession+" (--resume): "+sess.Reason+" (token rule 6)", 6)
	case sess.Action == SessionHandoff:
		m.Add("previous session transcript", false, 0, "not continued: "+sess.Reason+" (token rules 5 and 6)", 6)
		m.Add("handoff:"+nonEmptyStr(sess.Handoff, "unsaved"), true, 0, "handoff artifact ("+sess.Trigger+") put before the prompt: the task, its criteria, what the previous session did, the diff stat, failing checks and open questions", 2)
	default:
		m.Add("previous session transcript", false, 0, sess.Reason+" (token rules 5 and 16)", 6)
	}
	_ = d.Context.SaveManifest(ctx, string(in.RunID), string(in.NodeID), in.Attempt, t.ID, agentID, m)
	// the exact prompt goes next to the turn's stream on disk (never into the database) for the run explanation
	if p := PromptPath(d.DataDir, in.NodeInfo); p != "" {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
			_ = os.WriteFile(p, []byte(prompt), 0o600)
		}
	}
}

// PromptPath is where a turn's prompt is kept: beside the stream.jsonl of the same node execution.
func PromptPath(dataDir string, n workflow.NodeInfo) string {
	if dataDir == "" || n.RunID == "" {
		return ""
	}
	return filepath.Join(dataDir, "runs", string(n.RunID), string(n.NodeID), fmt.Sprintf("%d-%d", n.Step, n.Attempt), "prompt.md")
}

// WorkPrompt builds the turn prompt. It contains only what the agent needs now: the project brief, the task (title,
// description, acceptance criteria; compact on a later pass), the accepted spec and design notes when there are any, and on a
// later pass the latest failure, review feedback or guidance. A task without acceptance criteria asks the worker to state the
// definition of done it works to (or reminds it of the one it stated).
func WorkPrompt(t tasks.Task, in workflow.NodeInput, pc PromptContext) string {
	feedback := inboxFeedback(in)
	var sb strings.Builder
	sb.WriteString(briefBlock(pc.Brief))
	sb.WriteString(taskBlock(t, len(feedback) > 0))
	if strings.TrimSpace(pc.Spec) != "" {
		sb.WriteString("\n" + quoted("The spec a human accepted for this task (problem, scope and non-goals)", pc.Spec))
	}
	if strings.TrimSpace(pc.Design) != "" {
		sb.WriteString("\n" + quoted("Design notes from the architect", pc.Design))
	}
	if len(t.Acceptance) == 0 {
		if strings.TrimSpace(pc.DoD) != "" {
			sb.WriteString("\nNo acceptance criteria were given. The definition of done you stated earlier (keep to it, and start your final summary with it):\n" + pc.DoD + "\n")
		} else {
			sb.WriteString("\n" + dodAsk)
		}
	}
	if len(feedback) == 0 {
		sb.WriteString("\nWork in the current directory. Make the change, keep it minimal, and stop when the task is done. Do not commit.")
		return sb.String()
	}
	fmt.Fprintf(&sb, "\nYour previous attempt needs changes. Fix the following and stop. Do not commit.\n\n%s", strings.Join(feedback, "\n\n---\n\n"))
	return sb.String()
}

// inboxFeedback collects what a later pass must fix or follow: a failing check, a merge conflict, a watchdog notice, review
// feedback, or guidance from a human or the team lead (rendered as readable text, never raw JSON).
func inboxFeedback(in workflow.NodeInput) []string {
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
			if c, ok := o["conflict"].(map[string]any); ok {
				var files []string
				if fl, ok := c["files"].([]any); ok {
					for _, f := range fl {
						if s, ok := f.(string); ok {
							files = append(files, s)
						}
					}
				}
				target, _ := c["target"].(string)
				feedback = append(feedback, conflictPrompt(files, target))
			} else if w, ok := o["watchdog"].(string); ok {
				feedback = append(feedback, w)
			} else if g, ok := o["guidance"].(string); ok {
				if strings.TrimSpace(g) != "" {
					feedback = append(feedback, guidanceLabel(o["from"])+"\n"+clip(strings.TrimSpace(g), maxGuidance))
				}
			} else if fb, ok := o["feedback"].(string); ok {
				label := "Reviewer feedback:" // the reviewer agent's REVISE
				if from, _ := o["from"].(string); from == "human" {
					label = "Changes requested by the human:" // the human's "Request changes" at the merge approval
				}
				feedback = append(feedback, label+"\n"+fb)
			} else if d, ok := o["data"]; ok {
				raw, _ := json.Marshal(d)
				if g := HumanText(raw); g != "" {
					feedback = append(feedback, "Guidance from the human:\n"+g)
				}
			}
		}
	}
	return feedback
}

func guidanceLabel(from any) string {
	if s, _ := from.(string); s == "lead" {
		return "Guidance from the team lead:"
	}
	return "Guidance from the human:"
}

// Verify runs the project's checks in order (build, test, lint, typecheck as detected, then the settings-controlled
// `secrets` and `deps` checks), stopping at the first failure. It records evidence for each and returns the failing (or
// final passing) CheckResult with Route pass or fail. A check that could not be evaluated (osv-scanner not installed, a
// tool that broke) is recorded as evidence with no verdict and an inconclusive event; it is never a pass and never sends
// the work back to an agent.
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
		specs := d.detect()(t.Worktree)
		if len(specs) == 0 {
			return workflow.NodeOutput{}, errors.New("no checks detected for this project: add a Taskfile with build/test targets or configure checks")
		}
		specs = append(specs, d.extraChecks(ctx, repo, t)...)
		if specs, err = d.sandboxChecks(ctx, t, specs); err != nil {
			return sandboxTrouble(in, err)
		}
		// a merge that brought the base branch in and was not resolved cannot pass: markers are not code
		if repo.MergeInProgress(ctx, worktreeOf(t)) {
			if left := repo.UnresolvedConflicts(ctx, worktreeOf(t)); len(left) > 0 {
				cr := workflow.CheckResult{Name: "merge-conflicts", Passed: false, ExitCode: 1,
					Output: "Conflict markers are still in: " + strings.Join(left, ", ") + ". Edit each file so it keeps what both changes were for and remove every <<<<<<<, ======= and >>>>>>> line."}
				failedFlag := false
				_, _ = d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "merge-conflicts", Passed: &failedFlag, Summary: "conflict markers remain in " + strings.Join(left, ", ")})
				b, _ := json.Marshal(cr)
				return workflow.NodeOutput{Value: b, Route: "fail"}, nil
			}
		}
		logDir := filepath.Join(d.DataDir, "runs", string(in.RunID), "checks")
		diffHash, _ := repo.DiffHash(ctx, worktreeOf(t))
		var last checks.Result
		failed := false
		for _, spec := range specs {
			spec.LogDir = logDir
			res, err := checks.Run(ctx, d.PM, spec, func(line string) {
				in.Emit(workflow.NewChunk(workflow.ChunkLog, workflow.TextDelta{Text: line}))
			})
			if err != nil {
				return workflow.NodeOutput{}, err
			}
			res.DiffHash = diffHash
			kind := spec.Name
			data, _ := json.Marshal(map[string]any{"argv": res.Argv, "exitCode": res.ExitCode, "failing": res.FailingTests, "durationMs": res.Duration.Milliseconds(),
				"skipped": res.Skipped, "errored": res.Errored})
			if res.Inconclusive() {
				status := "skipped"
				if res.Errored {
					status = "error"
				}
				summary := fmt.Sprintf("%s %s: %s", kind, status, nonEmptyStr(res.SkipReason, res.Output))
				if _, err := d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: kind, Passed: nil, Summary: truncateStr(summary, 300), Ref: res.LogRef, Data: data}); err != nil {
					return workflow.NodeOutput{}, err
				}
				d.emit(ctx, "task.check_inconclusive", t, map[string]any{"kind": kind, "status": status, "reason": truncateStr(nonEmptyStr(res.SkipReason, res.Output), 300), "logRef": res.LogRef, "diffHash": diffHash})
				continue
			}
			last = res
			passed := res.Passed
			if _, err := d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: kind, Passed: &passed, Summary: summarize(res), Ref: res.LogRef, Data: data}); err != nil {
				return workflow.NodeOutput{}, err
			}
			if d.OnCheck != nil {
				d.OnCheck(ctx, t, res)
			}
			if !res.Passed {
				failed = true
				break
			}
		}
		b, _ := json.Marshal(last.CheckResult)
		route := "pass"
		if failed {
			route = "fail"
		}
		return workflow.NodeOutput{Value: b, Route: route}, nil
	}
}

// extraChecks returns the project's enabled `secrets` and `deps` checks (per-project settings; secrets default on, deps off).
func (d Deps) extraChecks(ctx context.Context, repo *git.Repo, t tasks.Task) []checks.Spec {
	x := checks.Extras{Settings: checks.LoadSettings(ctx, d.Tasks.DB, t.ProjectID), Dir: t.Worktree,
		Diff: func(ctx context.Context) (string, error) { return repo.Diff(ctx, worktreeOf(t), "") }, Scanner: d.Scanner,
		Deps: checks.DepsOptions{Binary: d.DepsBinary}}
	if x.Settings.Secrets && x.Scanner == nil {
		sc, err := checks.DefaultScanner()
		if err != nil {
			// the scanner cannot start: say so as an inconclusive secrets check rather than silently not scanning
			broken := checks.Spec{Name: "secrets", Argv: []string{"drydock", "scan-added-lines"}, Dir: t.Worktree, InProcess: func(context.Context, proc.Manager, checks.Spec) (checks.Result, error) {
				var r checks.Result
				r.Errored, r.ExitCode, r.Output = true, 2, "secrets scanner unavailable: "+err.Error()
				return r, nil
			}}
			x.Settings.Secrets = false
			return append([]checks.Spec{broken}, checks.ExtraSpecs(x)...)
		}
		x.Scanner = sc
	}
	return checks.ExtraSpecs(x)
}

func nonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
		// base and commit let the diff viewer read both versions of every file from git (internal/core/ext_diffview.go)
		base, _ := repo.MergeBase(ctx, w, w.Base)
		statJSON, _ := json.Marshal(struct {
			git.DiffStat
			Base   string `json:"base,omitempty"`
			Commit string `json:"commit,omitempty"`
		}{stat, base, commit})
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
				if d.ResetWatch != nil {
					d.ResetWatch(ctx, t.ID) // the task moved on: an earlier watchdog trip no longer counts toward a pause
				}
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
// MaxLoops bounds the work/verify loop; when exhausted the escalation asks the team lead (if any), then a human, who can
// continue with guidance (a fresh loop budget) or stop the task.
func TaskPipeline(d Deps, maxLoops int) *workflow.Graph {
	if maxLoops <= 0 {
		maxLoops = 5
	}
	g := workflow.NewGraph("task-pipeline").
		Add(
			workflow.Route("prepare", Prepare(d)),
			workflow.AgentTask("work", Work(d)).With("agent", "work"),
			workflow.Check("verify", Verify(d)),
			workflow.Route("finalize", Finalize(d)),
		).
		Edge("prepare", "work").
		Edge("work", "verify").
		EdgeWhen("verify", "finalize", "pass").
		Loop("verify", "work", "fail", maxLoops, NodeEscalate)
	addEscalation(g, d, nil)
	return g.Start("prepare")
}

var _ = agents.StatusReady
