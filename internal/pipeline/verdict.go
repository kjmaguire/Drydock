package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// A gate that none of its checks could judge (audit 2026-10-16, M28). A check that was skipped or could not run gives no
// verdict, and is recorded as evidence without one; when a blocking gate (the verification run, a security scan gate) ends with
// no check that gave a verdict at all, passing it would send the work on as verified when nothing verified it. The node asks a
// person instead, and runs again after the answer: "answered" checks again (after the tool was installed or the setting fixed),
// {"waive_verdict": true} goes on without a result this time (recorded as evidence, covering only this exact content, and only a
// person can), "rejected" stops the run. As with the CI question, the answer is read from the approvals row, because the
// watchdog's check middleware takes an answer delivered into a check node's inbox as the answer to its own pause.

const verdictGate = "verdict"

// unjudged is a check that gave no verdict, as the question shows it.
type unjudged struct {
	Name   string `json:"name"`
	Status string `json:"status"` // skipped | error
	Reason string `json:"reason"`
}

func (u unjudged) String() string {
	if u.Reason == "" {
		return u.Name + " " + u.Status
	}
	return fmt.Sprintf("%s %s: %s", u.Name, u.Status, u.Reason)
}

// verdictAnswer is the human's answer to the no-verdict question about the content the node is looking at now.
type verdictAnswer struct {
	decision, by string
	waive        bool // {"waive_verdict": true}: go on without a result this time
}

// latestGateAnswer reads the answer to the latest question of the named gate that this node asked in this run: the decision,
// who decided, the response data and the question's payload. ok is false when no question of the gate was answered yet.
func latestGateAnswer(ctx context.Context, d Deps, in workflow.NodeInput, gate string) (decision, by string, resp, payload []byte, ok bool) {
	var status string
	err := d.Tasks.DB.Reader().QueryRowContext(ctx, `SELECT status, decision, decided_by, response, payload FROM approvals
		WHERE run_id=? AND node_id=? AND CAST(payload AS TEXT) LIKE ? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		string(in.RunID), string(in.NodeID), `%"gate":"`+gate+`"%`).Scan(&status, &decision, &by, &resp, &payload)
	if err != nil || status != "APPROVED" && status != "REJECTED" {
		return "", "", nil, nil, false
	}
	return decision, by, resp, payload, true
}

// verdictAnswerFor reads the answer to this node's latest no-verdict question, when that question was about the content the
// worktree has now (diff is its fingerprint). Without a fingerprint nothing can be shown to be about it, so no answer counts.
func verdictAnswerFor(ctx context.Context, d Deps, in workflow.NodeInput, diff string) verdictAnswer {
	if diff == "" {
		return verdictAnswer{}
	}
	decision, by, resp, payload, ok := latestGateAnswer(ctx, d, in, verdictGate)
	if !ok {
		return verdictAnswer{}
	}
	var q struct {
		Diff string `json:"diff"`
	}
	if json.Unmarshal(payload, &q) != nil || q.Diff != diff {
		return verdictAnswer{} // the question was about other content
	}
	var data struct {
		Waive bool `json:"waive_verdict"`
	}
	_ = json.Unmarshal(resp, &data)
	return verdictAnswer{decision: decision, by: by, waive: data.Waive && decision != "rejected"}
}

// stopOnVerdictAnswer ends the run when a person stopped it at the node's earlier question.
func stopOnVerdictAnswer(ans verdictAnswer, what string) error {
	if ans.decision == "rejected" && isHuman(ans.by) {
		return fmt.Errorf("%s gave no result and %s stopped the run", what, ans.by)
	}
	return nil
}

// noVerdict is the end of a blocking gate none of whose checks gave a verdict: a person's waiver for this content lets it
// pass (and says so in the evidence); otherwise a person is asked, and the node runs again after the answer.
func noVerdict(ctx context.Context, d Deps, in workflow.NodeInput, t tasks.Task, what, diff string, rows []unjudged, ans verdictAnswer) (workflow.NodeOutput, error) {
	var list []string
	for _, r := range rows {
		list = append(list, r.String())
	}
	shown := strings.Join(list, "; ")
	if shown == "" {
		shown = "there was nothing to check"
	}
	var notes []string
	if ans.waive {
		if isHuman(ans.by) {
			summary := d.redact(truncateStr(fmt.Sprintf("%s gave no result (%s); %s let the task go on without one", what, shown, ans.by), 400))
			data, _ := json.Marshal(map[string]any{"node": string(in.NodeID), "by": ans.by, "diff": diff, "checks": rows})
			if _, err := d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "waiver", Summary: summary, Data: data}); err != nil {
				return workflow.NodeOutput{}, err
			}
			d.emit(ctx, "task.verdict_waived", t, map[string]any{"node": string(in.NodeID), "by": ans.by})
			b, _ := json.Marshal(workflow.CheckResult{Name: string(in.NodeID), Passed: true, DiffHash: diff, Output: summary})
			return workflow.NodeOutput{Value: b, Route: "pass"}, nil
		}
		notes = append(notes, fmt.Sprintf("%s asked to go on without a result; only a person can decide that, so the checks were run again", ans.by))
	}
	p, _ := json.Marshal(map[string]any{"gate": verdictGate, "node": string(in.NodeID), "what": what, "diff": diff, "checks": nonNilUnjudged(rows), "notes": notes})
	return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "question", Retry: true, Payload: p,
		Title: d.redact(truncateStr("No check gave a result for "+what+": "+shown+". Fix what stopped them and check again, go on without a result this time, or stop.", 700))}}, nil
}

func nonNilUnjudged(r []unjudged) []unjudged {
	if r == nil {
		return []unjudged{}
	}
	return r
}
