package checks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Settings are a project's choices about the verification checks that go beyond build, test and lint (brief s13a). The
// secrets check is on by default, the dependency check is off by default (it needs a tool and, unless offline, sends
// package metadata to osv.dev).
type Settings struct {
	Secrets     bool `json:"secrets"`
	Deps        bool `json:"deps"`
	DepsOffline bool `json:"depsOffline"`
}

// DefaultSettings applies to a project that never saved settings.
func DefaultSettings() Settings { return Settings{Secrets: true} }

// LoadSettings reads a project's settings, or the defaults when it has none (or the table is not there).
func LoadSettings(ctx context.Context, db *storage.DB, projectID string) Settings {
	s := DefaultSettings()
	if db == nil || projectID == "" {
		return s
	}
	var sec, deps, off int
	err := db.Reader().QueryRowContext(ctx, `SELECT secrets_enabled,deps_enabled,deps_offline FROM project_check_settings WHERE project_id=?`, projectID).Scan(&sec, &deps, &off)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return s
	}
	return Settings{Secrets: sec != 0, Deps: deps != 0, DepsOffline: off != 0}
}

// SaveSettings stores a project's settings. Turning a safety check off is a human decision.
func SaveSettings(ctx context.Context, db *storage.DB, projectID string, s Settings, by string) error {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return fmt.Errorf("checks: only a human: identity may change a project's checks")
	}
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO project_check_settings(project_id,secrets_enabled,deps_enabled,deps_offline,updated_at,updated_by) VALUES(?,?,?,?,?,?)
			ON CONFLICT(project_id) DO UPDATE SET secrets_enabled=excluded.secrets_enabled, deps_enabled=excluded.deps_enabled, deps_offline=excluded.deps_offline,
			updated_at=excluded.updated_at, updated_by=excluded.updated_by`, projectID, b2i(s.Secrets), b2i(s.Deps), b2i(s.DepsOffline), storage.Now(), by)
		return err
	})
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Extras are what the settings add to the detected checks.
type Extras struct {
	Settings Settings
	Dir      string
	// Diff returns the task's diff against its base (added lines are scanned for secrets).
	Diff    func(ctx context.Context) (string, error)
	Scanner security.SecretScanner
	Deps    DepsOptions
}

// ExtraSpecs returns the secrets and deps checks that are enabled, in that order. Both come after the detected
// build/test/lint checks: the cheap deterministic ones first (token rule 10), the broader ones when the work builds.
func ExtraSpecs(x Extras) []Spec {
	var out []Spec
	if x.Settings.Secrets && x.Scanner != nil && x.Diff != nil {
		out = append(out, SecretsSpec(x.Dir, x.Scanner, x.Diff))
	}
	if x.Settings.Deps {
		o := x.Deps
		o.Offline = o.Offline || x.Settings.DepsOffline
		out = append(out, DepsSpec(x.Dir, o))
	}
	return out
}

// DetectWith is Detect plus the settings-controlled extras. When no build/test/lint check is detected it returns nothing:
// a secrets scan alone is not verification of the work.
func DetectWith(dir string, x Extras) []Spec {
	base := Detect(dir)
	if len(base) == 0 {
		return nil
	}
	x.Dir = dir
	return append(base, ExtraSpecs(x)...)
}

var defaultScanner struct {
	once  sync.Once
	value *security.Scanner
	err   error
}

// DefaultScanner returns a process-wide gitleaks scanner for callers that were not handed one.
func DefaultScanner() (security.SecretScanner, error) {
	defaultScanner.once.Do(func() { defaultScanner.value, defaultScanner.err = security.NewScanner() })
	if defaultScanner.err != nil {
		return nil, defaultScanner.err
	}
	return defaultScanner.value, nil
}
