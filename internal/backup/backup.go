// Package backup makes consistent copies of the Drydock database and restores them without ever destroying data
// (docs/packaging.md section 8). A backup is a VACUUM INTO snapshot taken on a reader connection (consistent under WAL),
// verified with PRAGMA integrity_check, and recorded with its SHA-256. A restore moves the current database aside first.
package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/inuse"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Info describes a backup file.
type Info struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
	Created time.Time `json:"created"`
	Label   string    `json:"label"` // scheduled | pre-update-<old>-to-<new> | manual
	Keep    bool      `json:"keep"`  // exempt from pruning
}

// Create writes a verified snapshot of db into dir. label is part of the file name. A pre-update backup is exempt from
// pruning until Release is called on it (after the update proved healthy).
func Create(ctx context.Context, db *storage.DB, dir, label string, now time.Time) (info Info, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Info{}, err
	}
	sweepStale(dir, now)
	label = sanitize(label)
	name := fmt.Sprintf("drydock-%s-%s.db", now.UTC().Format("20060102T150405Z"), label)
	final := filepath.Join(dir, name)
	tmp := final + ".partial"
	_ = os.Remove(tmp)
	// A snapshot that fails leaves nothing behind: the temporary file is as large as the database, and a failure that repeats
	// every hour used to leave one each time (audit 2026-10-16, M13). After the rename the snapshot itself goes if its hash cannot
	// be recorded, so a backup is either whole with its hash or absent.
	renamed := false
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
			if renamed {
				_ = os.Remove(final)
				_ = os.Remove(final + ".sha256")
			}
		}
	}()
	// The pooled readers are opened read-only and VACUUM INTO refuses those, so the snapshot uses its own connection. Under
	// WAL it holds only a read transaction on the source, so the single writer is never blocked.
	src, err := sql.Open("sqlite", "file:"+filepath.ToSlash(db.Path())+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = src.Close() }()
	if _, err := src.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		return Info{}, fmt.Errorf("snapshot: %w", err)
	}
	if err := integrity(ctx, tmp); err != nil {
		_ = os.Remove(tmp)
		return Info{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return Info{}, err
	}
	renamed = true
	sum, size, err := hashFile(final)
	if err != nil {
		return Info{}, err
	}
	// written through a temporary file: a crash must not leave an empty or cut sidecar next to a good backup
	if err := writeAtomic(final+".sha256", []byte(sum+"  "+name+"\n")); err != nil {
		return Info{}, err
	}
	info = Info{Path: final, Name: name, Size: size, SHA256: sum, Created: now.UTC(), Label: label}
	if strings.HasPrefix(label, "pre-update") {
		info.Keep = true
		_ = writeAtomic(final+".keep", nil)
	}
	return info, nil
}

// writeAtomic writes data to path so that a crash leaves the old file or the whole new one, never part of it: the data goes to a
// temporary file in the same folder, is flushed, and is renamed over path.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// staleAfter is how old a temporary file of a snapshot must be before it is taken for the leftover of a failed one.
const staleAfter = 15 * time.Minute

// sweepStale removes the temporary files of snapshots and sidecars that were never finished.
func sweepStale(dir string, now time.Time) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		n := e.Name()
		leftover := (strings.HasPrefix(n, "drydock-") && strings.HasSuffix(n, ".db.partial")) ||
			(strings.HasPrefix(n, ".drydock-") && strings.HasSuffix(n, ".tmp"))
		if !leftover || e.IsDir() {
			continue
		}
		if st, err := e.Info(); err == nil && now.Sub(st.ModTime()) > staleAfter {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
}

// MaxKeepAge is how long a pre-update backup stays exempt from pruning when nothing has released it.
const MaxKeepAge = 30 * 24 * time.Hour

// Release removes the prune exemption of a pre-update backup once the new version is confirmed healthy.
func Release(path string) error {
	err := os.Remove(path + ".keep")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func sanitize(s string) string {
	if s == "" {
		return "manual"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func integrity(ctx context.Context, path string) error {
	c, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	var res string
	if err := c.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&res); err != nil {
		return fmt.Errorf("integrity check could not run: %w", err)
	}
	if res != "ok" {
		return fmt.Errorf("backup failed its integrity check: %s", res)
	}
	return nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// List returns the backups in dir, newest first.
func List(dir string) ([]Info, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, e := range ents {
		n := e.Name()
		if !strings.HasPrefix(n, "drydock-") || !strings.HasSuffix(n, ".db") {
			continue
		}
		parts := strings.SplitN(strings.TrimSuffix(n, ".db"), "-", 3)
		if len(parts) < 3 {
			continue
		}
		ts, err := time.Parse("20060102T150405Z", parts[1])
		if err != nil {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		in := Info{Path: filepath.Join(dir, n), Name: n, Size: st.Size(), Created: ts, Label: parts[2]}
		if b, err := os.ReadFile(in.Path + ".sha256"); err == nil {
			// a sidecar a power cut left empty or cut has no hash to read: the backup is listed without one (Verify then only
			// runs the integrity check) instead of the listing panicking at every start
			if f := strings.Fields(string(b)); len(f) > 0 && len(f[0]) == 64 {
				in.SHA256 = f[0]
			}
		}
		_, kerr := os.Stat(in.Path + ".keep")
		in.Keep = kerr == nil
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// Verify checks a backup against its recorded hash and runs the integrity check.
func Verify(ctx context.Context, in Info) error {
	sum, _, err := hashFile(in.Path)
	if err != nil {
		return err
	}
	if in.SHA256 != "" && sum != in.SHA256 {
		return fmt.Errorf("backup %s does not match its recorded SHA-256 (it was modified or corrupted)", in.Name)
	}
	return integrity(ctx, in.Path)
}

// ScheduledLabel is the label of a backup the scheduler took; any other label is a backup someone asked for.
const ScheduledLabel = "scheduled"

// Prune keeps the newest `daily` backups of different days and the newest `weekly` of different ISO weeks, every backup someone
// asked for (any label but ScheduledLabel) for `daily` days, plus anything
// marked Keep that is younger than MaxKeepAge at `now`, and deletes the rest. It returns the deleted names. Deleting is limited
// to files this package created. `now` is the caller's clock, the same one its backups are stamped with, so the exemption
// is measured on the time line the backups were made on.
func Prune(dir string, daily, weekly int, now time.Time) ([]string, error) {
	if now.IsZero() { // a zero time would keep every marked backup for ever
		return nil, errors.New("prune needs the current time")
	}
	all, err := List(dir)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	days := map[string]bool{}
	weeks := map[string]bool{}
	for _, b := range all { // newest first
		// A pre-update backup is exempt until the update proves itself, but not for good: markers that nothing ever released
		// (every update before this was wired) would otherwise hold a database-sized file each, for ever (audit 2026-10-16, M13).
		if b.Keep && now.Sub(b.Created) < MaxKeepAge {
			keep[b.Name] = true
			continue
		}
		// A backup the person started (Back up now, before a risky change) is theirs: it stays for as many days as daily
		// backups are kept, whatever else is taken that day. It used to share its day's one place with the scheduled backup, so
		// the next backup the same day deleted it (review 2026-10-27).
		if b.Label != ScheduledLabel && now.Sub(b.Created) < time.Duration(daily)*24*time.Hour {
			keep[b.Name] = true
			continue
		}
		d := b.Created.Format("2006-01-02")
		if !days[d] && len(days) < daily {
			days[d] = true
			keep[b.Name] = true
			continue
		}
		y, w := b.Created.ISOWeek()
		wk := fmt.Sprintf("%d-%02d", y, w)
		if !weeks[wk] && len(weeks) < weekly {
			weeks[wk] = true
			keep[b.Name] = true
		}
	}
	var removed []string
	for _, b := range all {
		if keep[b.Name] {
			continue
		}
		for _, suffix := range []string{"", ".sha256", ".keep"} {
			if err := os.Remove(b.Path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, err
			}
		}
		removed = append(removed, b.Name)
	}
	return removed, nil
}

// Restore replaces dbPath with the backup. The caller must have closed every connection to dbPath (the app stops its agents
// and closes the database first). The current database is moved to backupsDir/replaced-<time>.db, never deleted, and a
// stale -wal/-shm pair is moved with it so the restored file is not mixed with old write-ahead data.
func Restore(ctx context.Context, in Info, dbPath, backupsDir string, now time.Time) (replaced string, err error) {
	if err := Verify(ctx, in); err != nil {
		return "", err
	}
	if err := os.MkdirAll(backupsDir, 0o755); err != nil {
		return "", err
	}
	stamp := now.UTC().Format("20060102T150405Z")
	if _, err := os.Stat(dbPath); err == nil {
		replaced = filepath.Join(backupsDir, "replaced-"+stamp+".db")
		if err := inuse.Do(func() error { return os.Rename(dbPath, replaced) }); err != nil {
			return "", fmt.Errorf("move the current database aside: %w", err)
		}
		for _, sfx := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(dbPath + sfx); err == nil {
				if err := inuse.Do(func() error { return os.Rename(dbPath+sfx, replaced+sfx) }); err != nil {
					return replaced, err
				}
			}
		}
	}
	src, err := os.Open(in.Path)
	if err != nil {
		return replaced, err
	}
	defer src.Close()
	tmp := dbPath + ".restoring"
	dst, err := os.Create(tmp)
	if err != nil {
		return replaced, err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return replaced, err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return replaced, err
	}
	if err := dst.Close(); err != nil {
		return replaced, err
	}
	if err := inuse.Do(func() error { return os.Rename(tmp, dbPath) }); err != nil {
		return replaced, err
	}
	return replaced, nil
}
