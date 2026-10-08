// Package updatepolicy holds the rules that decide whether and how an update may be applied (docs/packaging.md sections 4
// and 5). The Wails updater downloads, verifies and swaps the executable; this package decides when that is allowed, takes
// the pre-update database backup, keeps the previous executable for rollback, and refuses downgrades that were not asked for.
package updatepolicy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/backup"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Blocker is a reason an update must wait; the UI shows them to the human.
type Blocker string

// State is what the policy needs to know about the running app.
type State struct {
	Current, Candidate string
	Channel            string // channel the candidate was published on
	AllowedChannels    func(ch string) bool
	ActiveRuns         int  // workflow runs in RUNNING
	LiveAgents         int  // agent processes alive
	Rollback           bool // the human asked for this version explicitly
}

// Check returns every reason the update cannot be applied right now. An empty result means it may proceed.
func Check(s State) []Blocker {
	var out []Blocker
	if s.AllowedChannels != nil && !s.AllowedChannels(s.Channel) {
		out = append(out, Blocker(fmt.Sprintf("your license does not include the %q update channel", s.Channel)))
	}
	if !s.Rollback {
		if c, err := CompareVersions(s.Candidate, s.Current); err != nil {
			out = append(out, Blocker("cannot compare versions: "+err.Error()))
		} else if c <= 0 {
			out = append(out, Blocker(fmt.Sprintf("%s is not newer than the running %s (downgrades only happen through Roll back)", s.Candidate, s.Current)))
		}
	}
	if s.ActiveRuns > 0 {
		out = append(out, Blocker(fmt.Sprintf("%d workflow run(s) are running; stop them or let them finish", s.ActiveRuns)))
	}
	if s.LiveAgents > 0 {
		out = append(out, Blocker(fmt.Sprintf("%d agent process(es) are alive; they will be stopped cleanly before the restart", s.LiveAgents)))
	}
	return out
}

// Prepared is what Prepare leaves behind for a possible rollback.
type Prepared struct {
	Backup       backup.Info `json:"backup"`
	PreviousExe  string      `json:"previousExe"`
	PreviousInfo string      `json:"previousVersion"`
}

// Prepare takes the pre-update backup (exempt from pruning until Confirm) and keeps a copy of the running executable under
// previous\<version>\. If either step fails nothing has been changed and the update must not proceed.
func Prepare(ctx context.Context, db *storage.DB, dataDir, exePath, current, candidate string, now time.Time) (Prepared, error) {
	label := fmt.Sprintf("pre-update-%s-to-%s", current, candidate)
	b, err := backup.Create(ctx, db, filepath.Join(dataDir, "backups"), label, now)
	if err != nil {
		return Prepared{}, fmt.Errorf("pre-update backup failed, so the update was not started: %w", err)
	}
	dir := filepath.Join(dataDir, "previous", current)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Prepared{}, err
	}
	dst := filepath.Join(dir, filepath.Base(exePath))
	if err := copyFile(exePath, dst); err != nil {
		return Prepared{}, fmt.Errorf("could not keep the previous version for rollback, so the update was not started: %w", err)
	}
	return Prepared{Backup: b, PreviousExe: dst, PreviousInfo: current}, nil
}

// Confirm ends the pre-update backup's prune exemption once the new version ran healthy.
func Confirm(p Prepared) error { return backup.Release(p.Backup.Path) }

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// CompareVersions compares two semantic versions (major.minor.patch with an optional -prerelease). A prerelease sorts
// before its release. It returns -1, 0 or 1.
func CompareVersions(a, b string) (int, error) {
	pa, err := parse(a)
	if err != nil {
		return 0, err
	}
	pb, err := parse(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			if pa.n[i] < pb.n[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	switch {
	case pa.pre == pb.pre:
		return 0, nil
	case pa.pre == "":
		return 1, nil
	case pb.pre == "":
		return -1, nil
	}
	return comparePre(pa.pre, pb.pre), nil
}

type ver struct {
	n   [3]int
	pre string
}

func parse(s string) (ver, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v ver
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre, s = s[i+1:], s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, errors.New("version " + strconv.Quote(s) + " is not major.minor.patch")
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, errors.New("version " + strconv.Quote(s) + " is not major.minor.patch")
		}
		v.n[i] = n
	}
	return v, nil
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, xe := strconv.Atoi(as[i])
		y, ye := strconv.Atoi(bs[i])
		switch {
		case xe == nil && ye == nil:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		case xe == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case ye == nil:
			return 1
		default:
			if as[i] != bs[i] {
				if as[i] < bs[i] {
					return -1
				}
				return 1
			}
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}
