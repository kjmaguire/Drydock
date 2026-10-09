package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
func DefaultDevSettings() DevSettings { return DevSettings{MaxParallel: 2, Respond: true} }

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
// a project command is a human decision).
func SaveDevSettings(ctx context.Context, db *storage.DB, projectID string, s DevSettings, by string) error {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return errors.New("devloop: only a human: identity may change a project's development settings")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	b, _ := json.Marshal(s)
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, devKey(projectID), string(b))
		return err
	})
}
