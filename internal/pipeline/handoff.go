package pipeline

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/safefs"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Verified agent handoffs (docs/token-control.md rule 6 and section 3, decision P119). A worker that has the handoff tool is asked
// to leave a handoff when it stops with work remaining. When the next work turn starts a fresh session, Drydock checks the newest
// handoff that session's agent wrote during the previous turn before the fresh session is seeded with it: the required fields
// are there, it is within its size bounds, the secret scanner finds nothing in it, every file it names is in the worktree (or
// was changed, for a deletion), and what it says about each check matches the check's latest recorded result. A handoff that
// passes gives the fresh session its account of the work, next to Drydock's own facts; one that fails is not used, the session
// gets Drydock's handoff, and the reasons are recorded on both handoffs, in the manifest and in the agent.session event.

// Bounds of an agent-written handoff.
const (
	maxAgentSummary = 3000 // characters of the summary
	maxAgentItems   = 40   // entries of one list
	maxAgentItem    = 600  // characters of one entry
	maxHandoffFiles = 20   // files a Drydock handoff lists with their hashes
	// maxHandoffReasons is how many reasons a failed check keeps.
	maxHandoffReasons = 8
)

// AgentHandoff is a handoff an agent wrote with the handoff tool (mcp.Handoff; the JSON names are the tool's).
type AgentHandoff struct {
	ID        string       `json:"id,omitempty"`
	Goal      string       `json:"goal,omitempty"`
	Summary   string       `json:"summary,omitempty"`
	Done      []string     `json:"done,omitempty"`
	Remaining []string     `json:"remaining,omitempty"`
	Open      []string     `json:"open,omitempty"`
	Decisions []string     `json:"decisions,omitempty"`
	Files     []string     `json:"files,omitempty"`
	Commands  []string     `json:"commands,omitempty"`
	NextStep  string       `json:"next_step,omitempty"`
	Checks    []CheckClaim `json:"checks,omitempty"`
	// Criteria are what it says about the task's acceptance criteria (P125).
	Criteria []CriterionReport `json:"criteria,omitempty"`
}

// CriterionReport is what an agent's handoff says about one acceptance criterion (mcp.HandoffCriterion).
type CriterionReport struct {
	ID       string `json:"id"`
	Met      bool   `json:"met"`
	Evidence string `json:"evidence,omitempty"`
}

// CheckClaim is what an agent's handoff says about one check.
type CheckClaim struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// AgentHandoffCheck is the result of checking an agent's handoff.
type AgentHandoffCheck struct {
	ID       string   `json:"id"`
	Accepted bool     `json:"accepted"`
	Reasons  []string `json:"reasons,omitempty"`
	// Handoff is the checked handoff when it passed. It is not stored with Drydock's artifact (the agent's handoff has its own row,
	// which ID names), so the artifact stays within its cap.
	Handoff *AgentHandoff `json:"-"`
}

// HandoffFacts is what an agent's handoff is checked against.
type HandoffFacts struct {
	// Exists reports whether a worktree-relative path is in the worktree.
	Exists func(path string) bool
	// Changed are the paths the work changed against the base (a deleted file is one).
	Changed map[string]bool
	// Checks is each check's latest recorded result (passed or not); a check that gave no verdict is absent.
	Checks map[string]bool
	// Redact is the secret scanner's redaction.
	Redact func(string) string
	// Acceptance is the task's acceptance list and Criteria each criterion's status (CriteriaStatus): a criterion the handoff
	// claims met must exist and must not stand not met by a check, a review or a person.
	Acceptance []string
	Criteria   []CriterionStatus
}

// VerifyAgentHandoff checks an agent's handoff (raw is the stored JSON) against the facts and returns why it fails, nothing when
// it passes.
func VerifyAgentHandoff(raw string, facts HandoffFacts) (AgentHandoff, []string) {
	var h AgentHandoff
	var why []string
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		return h, []string{"it is not a handoff the handoff tool writes"}
	}
	if strings.TrimSpace(h.Summary) == "" {
		why = append(why, "it has no summary")
	}
	if strings.TrimSpace(h.NextStep) == "" {
		why = append(why, "it has no next step")
	}
	if len(facts.Changed) > 0 && len(h.Files) == 0 {
		why = append(why, "it lists no files although the working folder has changes")
	}
	if len(raw) > MaxHandoffBytes {
		why = append(why, fmt.Sprintf("it is %d bytes, more than %d", len(raw), MaxHandoffBytes))
	}
	if n := utf8.RuneCountInString(h.Summary); n > maxAgentSummary {
		why = append(why, fmt.Sprintf("its summary is %d characters, more than %d", n, maxAgentSummary))
	}
	for name, list := range map[string][]string{"done": h.Done, "remaining": h.Remaining, "open": h.Open, "decisions": h.Decisions, "files": h.Files, "commands": h.Commands} {
		if len(list) > maxAgentItems {
			why = append(why, fmt.Sprintf("its %s list has %d entries, more than %d", name, len(list), maxAgentItems))
		}
		for _, it := range list {
			if utf8.RuneCountInString(it) > maxAgentItem {
				why = append(why, fmt.Sprintf("an entry of its %s list is longer than %d characters", name, maxAgentItem))
				break
			}
		}
	}
	if facts.Redact != nil && facts.Redact(raw) != raw {
		why = append(why, "the secret scanner found a secret in it")
	}
	for _, f := range h.Files {
		p := cleanRel(f)
		switch {
		case p == "":
			why = append(why, fmt.Sprintf("it names %q, which is not a path in the working folder", f))
		case facts.Changed[p]:
		case facts.Exists == nil || !facts.Exists(p):
			why = append(why, fmt.Sprintf("it names %s, which is not in the working folder and was not changed", p))
		}
	}
	for _, c := range h.Checks {
		rec, ok := facts.Checks[c.Name]
		switch {
		case !ok:
			why = append(why, fmt.Sprintf("it says the %s check %s, but Drydock has no recorded result for that check", c.Name, passWord(c.Passed)))
		case rec != c.Passed:
			why = append(why, fmt.Sprintf("it says the %s check %s, but its latest recorded result is that it %s", c.Name, passWord(c.Passed), passWord(rec)))
		}
	}
	for _, c := range h.Criteria {
		i := CriterionIndex(facts.Acceptance, c.ID)
		switch {
		case i < 0:
			why = append(why, fmt.Sprintf("it reports on criterion %s, which the task does not have", c.ID))
		case c.Met && strings.TrimSpace(c.Evidence) == "":
			why = append(why, fmt.Sprintf("it says criterion %s is met but not what shows it", c.ID))
		case c.Met && i < len(facts.Criteria) && facts.Criteria[i].Status == CriterionNotMet:
			st := facts.Criteria[i]
			why = append(why, fmt.Sprintf("it says criterion %s is met, but the %s says it is not (%s)", c.ID, sourceWord(st.Source), st.Evidence))
		}
	}
	sort.Strings(why)
	return h, why
}

func sourceWord(source string) string {
	switch source {
	case SourceCheck:
		return "newest test run"
	case SourceReview:
		return "newest review"
	}
	return "person's word"
}

func passWord(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}

// checkAgentHandoff finds the handoff the previous turn's agent wrote during that turn and checks it; nil when there is none.
func (d Deps) checkAgentHandoff(ctx context.Context, t tasks.Task, prev *WorkOutput, art HandoffArtifact) *AgentHandoffCheck {
	if prev == nil || prev.StartedAt == 0 || prev.Agent == "" || d.Tasks == nil || d.Tasks.DB == nil {
		return nil
	}
	var id, raw string
	err := d.Tasks.DB.Reader().QueryRowContext(ctx, `SELECT id, artifact FROM handoffs WHERE task_id=? AND agent_id=? AND reason='agent'
		AND created_at>=? AND verification='' ORDER BY created_at DESC, rowid DESC LIMIT 1`, t.ID, prev.Agent, prev.StartedAt).Scan(&id, &raw)
	if err != nil {
		return nil
	}
	facts := HandoffFacts{Changed: map[string]bool{}, Checks: map[string]bool{}, Redact: d.redact,
		Exists: func(p string) bool { _, err := safefs.Lstat(t.Worktree, p); return t.Worktree != "" && err == nil }}
	if d.Repo != nil && t.Worktree != "" {
		if repo, err := d.Repo(ctx, t.ProjectID); err == nil {
			if paths, err := repo.ChangedPaths(ctx, worktreeOf(t), t.BaseBranch); err == nil {
				for _, p := range paths {
					facts.Changed[p] = true
				}
			}
		}
	}
	if ev, err := d.Tasks.Evidence(ctx, t.ID); err == nil {
		facts.Checks = latestChecks(ev)
	}
	facts.Acceptance, facts.Criteria = t.Acceptance, d.criteriaStatus(ctx, t)
	h, why := VerifyAgentHandoff(raw, facts)
	c := &AgentHandoffCheck{ID: id, Accepted: len(why) == 0, Reasons: why}
	if len(why) > maxHandoffReasons { // the reasons are stored in Drydock's artifact too, which has a cap
		c.Reasons = append(why[:maxHandoffReasons:maxHandoffReasons], fmt.Sprintf("and %d more", len(why)-maxHandoffReasons))
	}
	if c.Accepted {
		h.ID = id
		c.Handoff = &h
	}
	return c
}

// recordHandoffCheck stores the result on the agent's handoff, with the handoff Drydock wrote for that session.
func (d Deps) recordHandoffCheck(ctx context.Context, c *AgentHandoffCheck, usedBy string) {
	if c == nil {
		return
	}
	status := "rejected"
	if c.Accepted {
		status = "accepted"
	}
	b, _ := json.Marshal(map[string]any{"status": status, "reasons": c.Reasons, "checkedAt": storage.Now(), "handoff": usedBy})
	_ = d.Tasks.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE handoffs SET verification=? WHERE id=?`, string(b), c.ID)
		return err
	})
}

// checkEvidence reports whether an evidence item is a check's result (Verify records the argv with each).
func checkEvidence(e tasks.Evidence) (argv []string, ok bool) {
	var data struct {
		Argv []string `json:"argv"`
	}
	if len(e.Data) == 0 || json.Unmarshal(e.Data, &data) != nil || data.Argv == nil {
		return nil, false
	}
	return data.Argv, true
}

// latestChecks is each check's latest result with a verdict.
func latestChecks(ev []tasks.Evidence) map[string]bool {
	out := map[string]bool{}
	seen := map[string]bool{}
	for i := len(ev) - 1; i >= 0; i-- {
		e := ev[i]
		if _, ok := checkEvidence(e); !ok || seen[e.Kind] {
			continue
		}
		seen[e.Kind] = true
		if e.Passed != nil {
			out[e.Kind] = *e.Passed
		}
	}
	return out
}

// checkCommandsAndRefs are the commands of the checks Drydock ran (latest per check) and the references to their logs and to the
// diff, never their content.
func checkCommandsAndRefs(ev []tasks.Evidence) (cmds, refs []string) {
	seen := map[string]bool{}
	diffRef := ""
	for i := len(ev) - 1; i >= 0; i-- {
		e := ev[i]
		if e.Kind == "diff" && diffRef == "" && e.Ref != "" {
			diffRef = "diff: " + e.Ref
		}
		argv, ok := checkEvidence(e)
		if !ok || seen[e.Kind] {
			continue
		}
		seen[e.Kind] = true
		if len(argv) > 0 && len(cmds) < 8 {
			cmds = append(cmds, clip(strings.Join(argv, " "), 200))
		}
		if e.Ref != "" && len(refs) < 9 {
			refs = append(refs, e.Kind+": "+e.Ref)
		}
	}
	sort.Strings(cmds)
	sort.Strings(refs)
	if diffRef != "" {
		refs = append(refs, diffRef)
	}
	return cmds, refs
}

// acceptanceStatus says where the acceptance criteria stand by the recorded facts. Drydock does not judge a criterion itself:
// the reviewer does, after the checks pass.
func acceptanceStatus(t tasks.Task, failing bool) string {
	switch {
	case len(t.Acceptance) == 0:
		return "the task has no acceptance criteria: the worker's definition of done stands in for them"
	case failing:
		return "not met yet: checks are failing"
	case strings.EqualFold(t.Verdict, "REVISE"):
		return "not met yet: the reviewer asked for changes"
	}
	return "not judged yet: the reviewer judges them once the checks pass"
}

// criteriaStatus is each of the task's acceptance criteria's status now (CriteriaStatus over its evidence, reviews and marks).
func (d Deps) criteriaStatus(ctx context.Context, t tasks.Task) []CriterionStatus {
	if len(t.Acceptance) == 0 || d.Tasks == nil {
		return nil
	}
	ev, _ := d.Tasks.Evidence(ctx, t.ID)
	rv, _ := d.Tasks.Reviews(ctx, t.ID)
	marks, _ := d.Tasks.CriterionMarks(ctx, t.ID)
	return CriteriaStatus(t.Acceptance, ev, rv, marks)
}

// HandoffCriterion is one acceptance criterion as a handoff carries it: its status and what showed it, without the evidence's
// text (the task page has it), so the artifact stays within its cap.
type HandoffCriterion struct {
	Index   int    `json:"index"`
	ID      string `json:"id,omitempty"`
	Status  string `json:"status"`
	Source  string `json:"source,omitempty"`
	By      string `json:"by,omitempty"`
	ClaimBy string `json:"claimBy,omitempty"` // an agent claims it met, unconfirmed
}

func handoffCriteria(cs []CriterionStatus) []HandoffCriterion {
	out := make([]HandoffCriterion, 0, len(cs))
	for _, c := range cs {
		hc := HandoffCriterion{Index: c.Index, ID: c.ID, Status: c.Status, Source: c.Source, By: c.By}
		if c.Claim != nil {
			hc.ClaimBy = c.Claim.By
		}
		out = append(out, hc)
	}
	return out
}

// nextStep is the next step by the recorded facts.
func nextStep(h HandoffArtifact) string {
	switch {
	case len(h.FailingChecks) > 0:
		return "make the failing check pass: " + clip(h.FailingChecks[0], 200)
	case len(h.Remaining) > 0:
		first, _, _ := strings.Cut(h.Remaining[0], "\n")
		return "address the feedback that follows the handoff (" + clip(strings.TrimSpace(first), 160) + ")"
	}
	return "continue the task from the current state of the working folder; it is not finished"
}

// fileHashes hashes the content of the touched files now (at most n).
func fileHashes(root string, paths []string, n int) []HandoffFile {
	var out []HandoffFile
	for _, p := range paths {
		if len(out) == n {
			break
		}
		f := HandoffFile{Path: p}
		b, err := safefs.ReadFile(root, p, maxFileRead)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			f.SHA256 = "deleted"
		case err != nil:
			f.SHA256 = "unreadable"
		default:
			sum := sha256.Sum256(b)
			f.SHA256 = hex.EncodeToString(sum[:])[:12]
		}
		out = append(out, f)
	}
	return out
}

// safePointOf is the safe point the engine recorded before this attempt.
func (d Deps) safePointOf(ctx context.Context, n workflow.NodeInfo) string {
	if d.Tasks == nil || d.Tasks.DB == nil || n.RunID == "" {
		return ""
	}
	var ref string
	_ = d.Tasks.DB.Reader().QueryRowContext(ctx, `SELECT safe_point_ref FROM node_executions WHERE run_id=? AND node_id=? AND step=? AND attempt=?`,
		string(n.RunID), string(n.NodeID), n.Step, n.Attempt).Scan(&ref)
	return ref
}

// taskDecisions are the task's recorded decisions (DECISION messages of its room, newest first, superseded ones left out).
func (d Deps) taskDecisions(ctx context.Context, taskID string) []string {
	if d.Tasks == nil || d.Tasks.DB == nil {
		return nil
	}
	rows, err := d.Tasks.DB.Reader().QueryContext(ctx, `SELECT COALESCE(json_extract(payload,'$.status'),''), COALESCE(json_extract(payload,'$.title'),''), body
		FROM messages WHERE task_id=? AND type='DECISION' ORDER BY seq DESC LIMIT 20`, taskID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() && len(out) < 5 {
		var status, title, body string
		if rows.Scan(&status, &title, &body) != nil || status == "superseded" {
			continue
		}
		if strings.TrimSpace(title) == "" {
			title, _, _ = strings.Cut(strings.TrimSpace(body), "\n")
		}
		if title = clip(oneLine(title), 200); title != "" {
			out = append(out, "["+nonEmptyStr(status, "proposed")+"] "+title)
		}
	}
	return out
}

// packageCeiling is the project's package ceiling in tokens.
func (d Deps) packageCeiling(ctx context.Context, t tasks.Task) int {
	if d.Tasks == nil || d.Tasks.DB == nil {
		return ctxeng.DefaultCeiling
	}
	return LoadDevSettings(ctx, d.Tasks.DB, t.ProjectID).PackageCeiling()
}

// instructionPieces is what every turn in the worktree carries from its files: AGENTS.md (CLAUDE.md and GEMINI.md import it) and
// .claude/rules. They are counted against the ceiling and never cut here: the context engine keeps AGENTS.md within its share.
func (d Deps) instructionPieces(t tasks.Task) []ctxeng.Piece {
	if t.Worktree == "" {
		return nil
	}
	n := 0
	if st, err := os.Stat(filepath.Join(t.Worktree, "AGENTS.md")); err == nil {
		n += int(st.Size())
	}
	for _, f := range mdFiles(filepath.Join(t.Worktree, ".claude", "rules")) {
		n += f.size
	}
	if n == 0 {
		return nil
	}
	return []ctxeng.Piece{{Kind: ctxeng.KindProjectInstructions, Source: "instruction files", Counted: true, Bytes: n,
		Reason: "AGENTS.md and .claude/rules, read by the CLI in every turn (never cut by the turn; the context engine keeps AGENTS.md within its share of the ceiling)"}}
}
