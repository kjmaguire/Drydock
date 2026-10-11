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

// DB wraps a writer (MaxOpenConns=1, BEGIN IMMEDIATE) and a reader pool (query_only).
type DB struct {
	path string
	w    *sql.DB
	r    *sql.DB
}

// Open opens (creating if needed) the database at path and applies all migrations.
func Open(ctx context.Context, path string) (*DB, error) {
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
	w, err := sql.Open("sqlite", dsn(path, true))
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
	if err := d.Migrate(ctx); err != nil {
		_ = w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn(path, false))
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

func dsn(path string, writer bool) string {
	var p string
	if path == ":memory:" {
		p = "file::memory:?cache=shared"
	} else {
		p = fileURI(path)
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
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
