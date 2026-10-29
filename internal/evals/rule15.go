package evals

import (
	"context"
	"strings"

	"github.com/kjmaguire/drydock/internal/storage"
)

// Rule 15, no LLM where rules suffice (docs/token-control.md, decision P119), as a runtime assertion over the eval corpus: no
// model_usage row in the window may carry the purpose of a job that code does. The import-boundary test
// (TestDeterministicPackagesDoNotImportLLM in internal/router) keeps those packages from calling a model; this catches a call
// made for such a job from anywhere else.

// DeterministicPurposes are the model_usage purposes of jobs rules do: routing by rule, the watchdog, the marker parser, minutes
// export, inbox derivation, check summarizing, diff stats, turn scheduling, handoff verification and package assembly. The
// sanctioned model uses (token-control section 9: consult, digests, the optional minutes summary, the router's tiebreak
// "route_tiebreak", handoff drafting by the outgoing agent) have purposes of their own and are not on the list.
var DeterministicPurposes = []string{"route", "routing", "watchdog", "markers", "minutes", "inbox", "check_summary", "diff_stat", "turn_schedule",
	"handoff_verify", "package_assembly"}

// RulesSection is the rule 15 assertion of a report.
type RulesSection struct {
	DeterministicPurposes []string       `json:"deterministicPurposes"`
	ModelCalls            int            `json:"modelCalls"` // usage rows in the window with one of those purposes
	ByPurpose             map[string]int `json:"byPurpose"`
	Holds                 bool           `json:"holds"`
	Note                  string         `json:"note"`
}

func (r *Report) computeRules(ctx context.Context, db *storage.DB, o Options, until int64) error {
	s := &r.Rules
	s.DeterministicPurposes = append([]string(nil), DeterministicPurposes...)
	s.ByPurpose = map[string]int{}
	list := "'" + strings.Join(DeterministicPurposes, "','") + "'"
	rows, err := db.Reader().QueryContext(ctx, `SELECT mu.purpose, COUNT(*) FROM model_usage mu WHERE mu.ts>=? AND mu.ts<=? AND mu.purpose IN (`+list+`)
		AND (?='' OR mu.task_id IN (SELECT id FROM tasks WHERE project_id=?) OR (mu.task_id='' AND mu.agent_id IN (SELECT id FROM agents WHERE project_id=?)))
		GROUP BY mu.purpose`, o.SinceMS, until, o.ProjectID, o.ProjectID, o.ProjectID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			rows.Close()
			return err
		}
		s.ByPurpose[p] = n
		s.ModelCalls += n
	}
	rows.Close()
	s.Holds = s.ModelCalls == 0
	s.Note = "A model call recorded with one of these purposes was made for a job Drydock does in code (routing by rule, the watchdog, markers, minutes, the inbox, check summaries, diff stats, turn scheduling, handoff checks, package assembly). The rule holds when there is none. It reads the purpose the caller gave: a call made for such a job under another purpose is not seen here; the import-boundary test guards the packages that do these jobs."
	return rows.Err()
}
