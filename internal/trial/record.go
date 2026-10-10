package trial

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Arms.
const (
	ArmDrydock = "D"
	ArmB1      = "B1"
)

// Outcomes (docs/evals.md 6.2).
const (
	Accepted = "accepted" // the hidden acceptance check passed
	Rejected = "rejected" // the arm finished and the hidden check failed
	Failed   = "failed"   // the arm did not finish: it stopped, crashed, ran out of time or waited for a human
	Ceiling  = "ceiling"  // the dollar ceiling stopped it
	Skipped  = "skipped"  // it could not run here; Reason says why
)

// Record is one run's run.json (docs/evals.md 6.2). Every figure comes from the CLI's own report, the core's database or the
// harness's measurement.
type Record struct {
	Schema int    `json:"schema"`
	Trial  string `json:"trial"`
	// Commit is Drydock's commit, with "+dirty" when its tree had uncommitted changes.
	Commit    string `json:"commit"`
	Arm       string `json:"arm"`
	ArmDetail string `json:"armDetail"`
	TaskID    string `json:"taskId"`
	// TaskClass is the task's class (docs/evals.md 6.1), for the report's per-class table.
	TaskClass string `json:"taskClass,omitempty"`
	Repeat    int    `json:"repeat"`
	// Model is the model both arms were asked to use; ModelsUsed is what the CLI reported using.
	Model       string    `json:"model"`
	ModelsUsed  []string  `json:"modelsUsed"`
	CLI         string    `json:"cli"`
	Host        string    `json:"host"`
	StartCommit string    `json:"startCommit"`
	StartedAt   time.Time `json:"startedAt"`
	WallMs      int64     `json:"wallMs"`
	CeilingUSD  float64   `json:"ceilingUsd"`
	Outcome     string    `json:"outcome"`
	Reason      string    `json:"reason,omitempty"`
	// EvalAccept is the hidden check's verdict on the arm's result (the human accept of a product run is replaced by it).
	EvalAccept *AcceptResult `json:"evalAccept,omitempty"`
	// CostUSD is the sum of Claude Code's own total_cost_usd over the arm's CLI sessions: the CLI's estimate at API prices,
	// not what a subscription costs. CallsWithoutCost counts sessions that reported none, which makes CostUSD a lower bound.
	CostUSD          float64 `json:"costUsd"`
	CallsWithoutCost int     `json:"callsWithoutCost"`
	Tokens           Tokens  `json:"tokens"`
	// Sessions is the number of CLI processes (agent turns): 1 for B1, one per worker and reviewer turn for D.
	Sessions int `json:"sessions"`
	// ModelTurns is the CLI's num_turns (B1 only; the core does not store it).
	ModelTurns int      `json:"modelTurns,omitempty"`
	Reviews    []Review `json:"reviews,omitempty"`
	// ToolUses counts B1's tool calls by tool name.
	ToolUses map[string]int `json:"toolUses,omitempty"`
	// SandboxCommands counts the commands D's worker ran in its sandbox.
	SandboxCommands int `json:"sandboxCommands,omitempty"`
	// Skills is how D's agents received the skills of the run's bundle: "" (no skills), "all" or "relevant" (DevSettings.
	// SkillDelivery), with the bundle's skill names in SkillNames. SkillReads counts the times an agent opened a skill's
	// on-demand file with Read (only "relevant" has those files).
	Skills     string   `json:"skills,omitempty"`
	SkillNames []string `json:"skillNames,omitempty"`
	SkillReads int      `json:"skillReads,omitempty"`
	// CoreTaskID is D's task in its core (its events and usage files name it).
	CoreTaskID string `json:"coreTaskId,omitempty"`
	// SawHiddenFiles is set when one of B1's tool calls named the harness's hidden checks (it runs with this machine's whole
	// file system); such a run is reported but its acceptance does not count.
	SawHiddenFiles bool     `json:"sawHiddenFiles,omitempty"`
	Notes          []string `json:"notes,omitempty"`
}

// Tokens are summed over the arm's CLI sessions, each as the CLI reported it (modelUsage, or usage when absent).
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// Review is one of D's review verdicts, with what the reviewer said.
type Review struct {
	Verdict  string `json:"verdict"`
	By       string `json:"by"`
	Feedback string `json:"feedback,omitempty"`
}

// Counted reports whether the run counts towards acceptance figures.
func (r Record) Counted() bool { return r.Outcome != Skipped && !r.SawHiddenFiles }

// Dir is the run's directory under out: <date>_<trial>_<arm>_<task>_<k> (docs/evals.md 6.2).
func (r Record) Dir(out string) string {
	return filepath.Join(out, fmt.Sprintf("%s_%s_%s_%s_%d", r.StartedAt.UTC().Format("2006-01-02"), r.Trial, r.Arm, r.TaskID, r.Repeat))
}

// Write saves run.json and the named extra files into the run's directory.
func (r Record) Write(out string, extra map[string][]byte) (string, error) {
	dir := r.Dir(out)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	names := make([]string, 0, len(extra))
	for n := range extra {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), extra[n], 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// LoadRecords reads every run.json under dir.
func LoadRecords(dir string) ([]Record, error) {
	var out []Record
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "run.json" {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var r Record
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, r)
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Trial != b.Trial {
			return a.Trial < b.Trial
		}
		if a.TaskID != b.TaskID {
			return a.TaskID < b.TaskID
		}
		if a.Arm != b.Arm {
			return a.Arm < b.Arm
		}
		return a.Repeat < b.Repeat
	})
	return out, err
}
