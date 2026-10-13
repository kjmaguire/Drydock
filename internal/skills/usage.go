package skills

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Decision P49: what a skill costs and whether it is used. Deliveries (deliver.go) record what reached a run: the full text
// (fidelity exact or run_start) or only an index line to read on demand (fidelity on_demand). Uses record the on-demand reads
// that followed: Claude opening the skill's file, or an agent fetching it through the skills tool. Together they say what a
// skill cost in tokens and whether anyone read it. Whether a skill raised the work's quality is not in these numbers; that is
// what a trial measures (docs/evals.md section 14), and the recommendation says so.

// EventUsed is published when an agent opens a skill on demand.
const EventUsed = "skill.used"

// How a skill was used.
const (
	UseFileRead = "file_read"
	UseTool     = "tool"
)

// Recommendations.
const (
	RecommendRemove = "remove"
	RecommendNarrow = "narrow"
	RecommendKeep   = "keep"
)

// usageMinTasks is how many tasks a skill must have reached before a recommendation is made.
const usageMinTasks = 5

// RecordUse records that an agent opened a skill on demand.
func (s *Service) RecordUse(ctx context.Context, t Target, runID, skill, how string) {
	if skill == "" {
		return
	}
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO skill_uses(ts,project_id,task_id,agent_id,run_id,skill_name,how) VALUES(?,?,?,?,?,?,?)`,
			s.now().UnixMilli(), t.ProjectID, t.TaskID, t.AgentID, runID, skill, how)
		return err
	})
	if err != nil {
		return
	}
	s.emit(ctx, EventUsed, t.ProjectID, t.TaskID, t.AgentID, map[string]any{"skill": skill, "how": how, "run": runID})
}

// SkillUsage is one skill's cost and use in a project.
type SkillUsage struct {
	Name       string `json:"name"`
	RegistryID string `json:"registryId"`
	Origin     string `json:"origin"`
	Status     string `json:"status"`
	// Loaded counts deliveries of the full text; Listed counts deliveries of an index line only; Opened counts on-demand
	// reads and tool fetches; Tasks counts the distinct tasks any delivery reached.
	Loaded int `json:"loaded"`
	Listed int `json:"listed"`
	Opened int `json:"opened"`
	Tasks  int `json:"tasks"`
	// TokensLoaded and TokensListed estimate the tokens spent (about four characters per token).
	TokensLoaded   int        `json:"tokensLoaded"`
	TokensListed   int        `json:"tokensListed"`
	LastUsed       *time.Time `json:"lastUsed,omitempty"`
	Recommendation string     `json:"recommendation"`
	Reason         string     `json:"reason"`
}

func tokens(chars int) int { return (chars + 3) / 4 }

// Usage reports every skill the project has (any status), with its deliveries and uses so far.
func (s *Service) Usage(ctx context.Context, projectID string) ([]SkillUsage, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT name,registry_id,origin,status,LENGTH(body),LENGTH(description),globs FROM skills WHERE project_id=? ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	type meta struct {
		body, desc int
		globs      string
	}
	out := []SkillUsage{}
	metas := map[string]meta{}
	for rows.Next() {
		var u SkillUsage
		var m meta
		if err := rows.Scan(&u.Name, &u.RegistryID, &u.Origin, &u.Status, &m.body, &m.desc, &m.globs); err != nil {
			rows.Close()
			return nil, err
		}
		metas[u.Name] = m
		out = append(out, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, u := range out {
		idx[u.Name] = i
	}
	drows, err := s.DB.Reader().QueryContext(ctx, `SELECT skill_name,fidelity,COUNT(*),COUNT(DISTINCT task_id) FROM skill_deliveries WHERE project_id=? GROUP BY skill_name,fidelity`, projectID)
	if err != nil {
		return nil, err
	}
	taskSets := map[string]map[string]bool{}
	for drows.Next() {
		var name, fidelity string
		var n, tasks int
		if err := drows.Scan(&name, &fidelity, &n, &tasks); err != nil {
			drows.Close()
			return nil, err
		}
		i, ok := idx[name]
		if !ok {
			continue
		}
		m := metas[name]
		if fidelity == FidelityOnDemand {
			out[i].Listed += n
			out[i].TokensListed += n * tokens(len(name)+m.desc+8)
		} else {
			out[i].Loaded += n
			out[i].TokensLoaded += n * tokens(m.body)
		}
		_ = tasks
		_ = taskSets
	}
	drows.Close()
	trows, err := s.DB.Reader().QueryContext(ctx, `SELECT skill_name,COUNT(DISTINCT task_id) FROM skill_deliveries WHERE project_id=? GROUP BY skill_name`, projectID)
	if err != nil {
		return nil, err
	}
	for trows.Next() {
		var name string
		var tasks int
		if err := trows.Scan(&name, &tasks); err != nil {
			trows.Close()
			return nil, err
		}
		if i, ok := idx[name]; ok {
			out[i].Tasks = tasks
		}
	}
	trows.Close()
	urows, err := s.DB.Reader().QueryContext(ctx, `SELECT skill_name,COUNT(*),MAX(ts) FROM skill_uses WHERE project_id=? GROUP BY skill_name`, projectID)
	if err != nil {
		return nil, err
	}
	for urows.Next() {
		var name string
		var n int
		var last int64
		if err := urows.Scan(&name, &n, &last); err != nil {
			urows.Close()
			return nil, err
		}
		if i, ok := idx[name]; ok {
			out[i].Opened = n
			t := time.UnixMilli(last).UTC()
			out[i].LastUsed = &t
		}
	}
	urows.Close()
	for i := range out {
		out[i].Recommendation, out[i].Reason = recommend(out[i], metas[out[i].Name].globs)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i].Recommendation), rank(out[j].Recommendation)
		if ri != rj {
			return ri < rj
		}
		return out[i].TokensLoaded+out[i].TokensListed > out[j].TokensLoaded+out[j].TokensListed
	})
	return out, nil
}

func rank(r string) int {
	switch r {
	case RecommendRemove:
		return 0
	case RecommendNarrow:
		return 1
	case RecommendKeep:
		return 2
	}
	return 3
}

// recommend writes the recommendation from the numbers alone, and says what the numbers cannot tell.
func recommend(u SkillUsage, globs string) (string, string) {
	total := u.TokensLoaded + u.TokensListed
	if u.Tasks < usageMinTasks {
		return "", fmt.Sprintf("Too few tasks to judge (%d of %d).", u.Tasks, usageMinTasks)
	}
	hasGlobs := globs != "" && globs != "[]" && globs != "null"
	switch {
	case u.Opened > 0:
		return RecommendKeep, fmt.Sprintf("Opened %d times on demand across %d tasks: it is being used (about %s tokens so far).", u.Opened, u.Tasks, thousands(total))
	case u.Loaded == 0 && u.Listed > 0:
		return RecommendRemove, fmt.Sprintf("Listed in %d tasks and never opened: it costs its index line each time (about %s tokens) and gave nothing here. Remove it, or keep it for a task that will need it.", u.Tasks, thousands(total))
	case u.Loaded > 0 && !hasGlobs:
		return RecommendNarrow, fmt.Sprintf("Loaded in full into %d tasks (about %s tokens) because it names no paths, so every task pays for it. Give it paths so it loads only for the files it is about; whether it helps those tasks needs a trial.", u.Tasks, thousands(total))
	case u.Loaded > 0:
		return RecommendKeep, fmt.Sprintf("Loaded for matching files in %d tasks (about %s tokens). Whether it raised the work's quality is not in these numbers: a trial measures that (docs/evals.md section 14).", u.Tasks, thousands(total))
	}
	return "", "Never delivered yet."
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
