package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

// DevSettings are a project's choices for autonomous development (Slice 4). They are stored as one JSON value per project
// in the settings table under "devloop:<project id>". Nothing in them is a lock: templates and agents read them as
// defaults, and only a human identity may change them.
type DevSettings struct {
	// MaxParallel is the most agent turns running at once in the project (scheduler admission and the per-node gate).
	MaxParallel int `json:"maxParallel"`
	// AutoSchedule lets the scheduler start READY tasks by itself. Off by default: starting work is a human decision until
	// the human switches it on for the project. Merging stays a human action either way.
	AutoSchedule bool `json:"autoSchedule"`
	// Respond switches agent replies in the Engineering Room on or off for the project (default on).
	Respond bool         `json:"respond"`
	E2E     E2ESettings  `json:"e2e"`
	Preview PreviewSetup `json:"preview"`
	// CollisionGuard makes a task wait while an earlier task is changing the same code (docs/parallel-work.md). On unless
	// the human switches it off; nil means on.
	CollisionGuard *bool `json:"collisionGuard,omitempty"`
	// WorkerChecks lets a Claude worker run this project's own checks (build, test, lint, typecheck) during its turn, as exact
	// command lines (checks.WorkerRules; docs/worker-commands.md, tier 1). Those run code the worker can change, so in practice
	// it can then run anything. Off by default, off after an import; never applies to a reviewer.
	WorkerChecks bool `json:"workerChecks,omitempty"`
	// LeadStallMinutes is how long a RUNNING task may go without progress events before the team lead is asked about it
	// (trigger "stalled"). 0 means the default, 15 minutes. Only matters when the project has an agent with the lead role.
	LeadStallMinutes int `json:"leadStallMinutes,omitempty"`
	// CIMode is the pull-request flow's CI gate before a merge (docs/github.md section 5). CIRequired, the default: CI must
	// pass on the reviewed commit, and when none reports the human is asked (check again, merge without CI this time, or
	// stop). CINone: a human declared that the repository has no CI, so a commit nothing reports on passes after the review
	// alone (checks that do report still count). Required after an import.
	CIMode string `json:"ciMode"`
	// CINoneBy and CINoneAt (Unix milliseconds) record who declared that the repository has no CI, and when. SaveDevSettings
	// sets them from its caller's identity, never from the settings it is given; empty while CIMode is CIRequired.
	CINoneBy string `json:"ciNoneBy,omitempty"`
	CINoneAt int64  `json:"ciNoneAt,omitempty"`
	// SkillDelivery is how a Claude agent receives the project's skills: "all" puts every skill that reaches it into every
	// turn; "relevant" puts in the repository's own skills (.aet/skills; one with globs unless the task names files and none
	// matches) and the imported ones whose globs match the task's files, and lists the others in AGENTS.md for the agent
	// to read when the work needs them
	// (skills.DeliveryAll, skills.DeliveryRelevant). Empty means the default (skills.DefaultDelivery, relevant). Other CLIs
	// always get the matching ones at the start and the rest from the skills tool.
	SkillDelivery string `json:"skillDelivery,omitempty"`
}

// The CI modes (DevSettings.CIMode).
const (
	CIRequired = "required"
	CINone     = "none"
)

// DefaultLeadStallMinutes is the stall time when LeadStallMinutes is not set.
const DefaultLeadStallMinutes = 15

// LeadStall is the stall time in force.
func (s DevSettings) LeadStall() time.Duration {
	if s.LeadStallMinutes <= 0 {
		return DefaultLeadStallMinutes * time.Minute
	}
	return time.Duration(s.LeadStallMinutes) * time.Minute
}

// GuardOn reports whether the collision guard is in force.
func (s DevSettings) GuardOn() bool { return s.CollisionGuard == nil || *s.CollisionGuard }

// E2ESettings gate the `e2e` check (brief section 13a).
type E2ESettings struct {
	Enabled    bool     `json:"enabled"`
	Command    []string `json:"command,omitempty"` // argv, no shell; empty = detect (Taskfile test:e2e/e2e, package.json script, Playwright config)
	TimeoutSec int      `json:"timeoutSec,omitempty"`
}

// PreviewSetup gates the `preview` check.
type PreviewSetup struct {
	Enabled    bool     `json:"enabled"`
	Command    []string `json:"command,omitempty"` // argv, no shell; empty = Taskfile target / package.json script named preview
	URL        string   `json:"url,omitempty"`     // health URL; empty = the first URL the command prints
	TimeoutSec int      `json:"timeoutSec,omitempty"`
}

// DefaultDevSettings applies to a project that never saved settings.
func DefaultDevSettings() DevSettings {
	return DevSettings{MaxParallel: 2, Respond: true, CIMode: CIRequired}
}

// Validate normalizes and checks the settings.
func (s *DevSettings) Validate() error {
	if s.MaxParallel <= 0 {
		s.MaxParallel = 2
	}
	if s.MaxParallel > 16 {
		return fmt.Errorf("devloop: maxParallel %d is more than 16", s.MaxParallel)
	}
	for _, c := range [][]string{s.E2E.Command, s.Preview.Command} {
		for _, a := range c {
			if strings.ContainsAny(a, "\x00\r\n") {
				return errors.New("devloop: a command argument contains a control character")
			}
		}
	}
	if len(s.E2E.Command) > 64 || len(s.Preview.Command) > 64 {
		return errors.New("devloop: a command has more than 64 arguments")
	}
	if s.E2E.TimeoutSec < 0 || s.Preview.TimeoutSec < 0 || s.E2E.TimeoutSec > 7200 || s.Preview.TimeoutSec > 3600 {
		return errors.New("devloop: timeout out of range")
	}
	if s.LeadStallMinutes < 0 || s.LeadStallMinutes > 1440 {
		return errors.New("devloop: leadStallMinutes must be 0 (default 15) to 1440")
	}
	switch s.CIMode {
	case "":
		s.CIMode = CIRequired
	case CIRequired, CINone:
	default:
		return fmt.Errorf("devloop: ciMode must be %q or %q, not %q", CIRequired, CINone, s.CIMode)
	}
	if s.CIMode == CIRequired {
		s.CINoneBy, s.CINoneAt = "", 0
	}
	switch s.SkillDelivery {
	case "", "all", "relevant":
	default:
		return fmt.Errorf("devloop: skillDelivery must be \"all\" or \"relevant\", not %q", s.SkillDelivery)
	}
	return nil
}

func devKey(projectID string) string { return "devloop:" + projectID }

// LoadDevSettings reads a project's settings, or the defaults.
func LoadDevSettings(ctx context.Context, db *storage.DB, projectID string) DevSettings {
	s := DefaultDevSettings()
	if db == nil || projectID == "" {
		return s
	}
	var raw string
	if err := db.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, devKey(projectID)).Scan(&raw); err != nil {
		return s
	}
	if json.Unmarshal([]byte(raw), &s) != nil {
		return DefaultDevSettings()
	}
	if s.Validate() != nil {
		return DefaultDevSettings()
	}
	return s
}

// SaveDevSettings stores a project's settings. Only a human identity may change them (turning on automatic scheduling or
// a project command is a human decision, and so is declaring that the repository has no CI). Declaring "no CI" records
// who declared it (by) and when; saving the declaration again keeps the original record.
func SaveDevSettings(ctx context.Context, db *storage.DB, projectID string, s DevSettings, by string) error {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return errors.New("devloop: only a human: identity may change a project's development settings")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		if s.CIMode == CINone {
			prev := DefaultDevSettings()
			var raw string
			if tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, devKey(projectID)).Scan(&raw) == nil {
				_ = json.Unmarshal([]byte(raw), &prev)
			}
			if prev.CIMode == CINone && prev.CINoneBy != "" {
				s.CINoneBy, s.CINoneAt = prev.CINoneBy, prev.CINoneAt
			} else {
				s.CINoneBy, s.CINoneAt = by, storage.Now()
			}
		}
		b, _ := json.Marshal(s)
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, devKey(projectID), string(b))
		return err
	})
}
