// Package storage owns the SQLite database: one write connection, a pool of read connections,
// WAL mode, and embedded migrations (applied with the pressly/goose migrations tool).
package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNewerDatabase is returned by Open for a database file a newer build of Drydock has migrated. Update Drydock, or restore a
// backup made before the update (the Backups page lists them).
var ErrNewerDatabase = errors.New("storage: this database was made by a newer Drydock; update Drydock, or restore a backup made before that update")

// DB wraps a writer (MaxOpenConns=1, BEGIN IMMEDIATE) and a reader pool (query_only).
type DB struct {
	path string
	w    *sql.DB
	r    *sql.DB
}

// Open opens (creating if needed) the database at path and applies all migrations.
func Open(ctx context.Context, path string) (*DB, error) { return open(ctx, path, nil) }

// OpenUntrusted opens a database file somebody else made (a project archive's project.db) the way Open does, with the
// connection settings that keep its schema from acting: trusted_schema off (a view, trigger, default or check constraint of the
// file may call only functions SQLite marks innocuous) and cell_size_check on (a corrupt page is an error, not a read past it).
func OpenUntrusted(ctx context.Context, path string) (*DB, error) {
	return open(ctx, path, []string{"trusted_schema(0)", "cell_size_check(1)"})
}

func open(ctx context.Context, path string, pragmas []string) (*DB, error) {
	if path != ":memory:" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		path = abs
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	w, err := sql.Open("sqlite", dsn(path, true, pragmas...))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	if err := w.PingContext(ctx); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("open writer: %w", err)
	}
	d := &DB{path: path, w: w}
	// a database a newer build migrated is not opened by this one: its tables have a shape this code does not know, and an older
	// build writing to it would corrupt what the newer one relies on
	if v, err := schemaVersion(ctx, w); err == nil && v > LatestVersion() {
		_ = w.Close()
		return nil, fmt.Errorf("%w (its schema is at %d, this build knows %d)", ErrNewerDatabase, v, LatestVersion())
	}
	if err := d.Migrate(ctx); err != nil {
		_ = w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn(path, false, pragmas...))
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	if path == ":memory:" {
		// an in-memory database is per-connection; share the writer for reads in tests that use it
		_ = r.Close()
		r = w
	}
	d.r = r
	return d, nil
}

// OpenTemp is a test helper: a fresh database file under dir.
func OpenTemp(ctx context.Context, dir string) (*DB, error) {
	return Open(ctx, filepath.Join(dir, "drydock.db"))
}

func dsn(path string, writer bool, extra ...string) string {
	var p string
	if path == ":memory:" {
		p = "file::memory:?cache=shared"
	} else {
		p = fileURI(path)
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	// NORMAL in WAL mode: a commit survives the app crashing or being killed, and the file survives anything; a power cut or an
	// operating system crash can lose the last commits (a decision given in the last seconds). FULL would sync the log at every
	// commit, which a stream of events cannot afford on a PC's disk (audit 2026-10-16, L21).
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	for _, e := range extra {
		q.Add("_pragma", e)
	}
	if writer {
		q.Add("_txlock", "immediate")
	} else {
		q.Add("_pragma", "query_only(1)")
	}
	sep := "?"
	if strings.Contains(p, "?") {
		sep = "&"
	}
	return p + sep + q.Encode()
}

// fileURI renders an absolute path as a SQLite URI: file:///tmp/x.db or file:///C:/x/y.db.
func fileURI(path string) string {
	s := filepath.ToSlash(path)
	if len(s) >= 2 && s[1] == ':' { // Windows drive path
		s = "/" + s
	}
	u := url.URL{Path: s}
	return "file://" + u.EscapedPath()
}

// Migrate applies embedded migrations.
func (d *DB) Migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, d.w, sub)
	if err != nil {
		return fmt.Errorf("migrations provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// MigrateTo moves the schema to exactly version (down or up). It exists for migration tests that start from an older
// shape, insert old rows and migrate up again; the app itself only ever calls Migrate.
func (d *DB) MigrateTo(ctx context.Context, version int64) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, d.w, sub)
	if err != nil {
		return fmt.Errorf("migrations provider: %w", err)
	}
	cur, err := p.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	if version < cur {
		_, err = p.DownTo(ctx, version)
	} else if version > cur {
		_, err = p.UpTo(ctx, version)
	}
	if err != nil {
		return fmt.Errorf("migrate to %d: %w", version, err)
	}
	return nil
}

// Path returns the database file path.
func (d *DB) Path() string { return d.path }

// Writer returns the single-connection writer pool. Use WriteTx for transactions.
func (d *DB) Writer() *sql.DB { return d.w }

// Reader returns the read pool.
func (d *DB) Reader() *sql.DB { return d.r }

// Close closes both pools.
func (d *DB) Close() error {
	var errs []error
	if d.r != nil && d.r != d.w {
		errs = append(errs, d.r.Close())
	}
	errs = append(errs, d.w.Close())
	return errors.Join(errs...)
}

// WriteTx runs fn in an immediate transaction on the writer, retrying on SQLITE_BUSY.
func (d *DB) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	var last error
	for attempt := 0; attempt < 6; attempt++ {
		err := d.writeTxOnce(ctx, fn)
		if err == nil {
			return nil
		}
		if !isBusy(err) {
			return err
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(10<<attempt) * time.Millisecond):
		}
	}
	return last
}

func (d *DB) writeTxOnce(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "SQLITE_BUSY") || strings.Contains(s, "database is locked")
}

// Now returns the current time as stored in the database (UTC, millisecond precision).
func Now() int64 { return time.Now().UTC().UnixMilli() }
