package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// LatestVersion is the highest migration version embedded in this build (the schema version a database has after Open).
func LatestVersion() int64 {
	ents, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return 0
	}
	var max int64
	for _, e := range ents {
		name := e.Name()
		i := strings.IndexByte(name, '_')
		if i <= 0 || !strings.HasSuffix(name, ".sql") {
			continue
		}
		if n, err := strconv.ParseInt(name[:i], 10, 64); err == nil && n > max {
			max = n
		}
	}
	return max
}

// SchemaVersion is the highest migration applied to this database.
func (d *DB) SchemaVersion(ctx context.Context) (int64, error) {
	return schemaVersion(ctx, d.r)
}

// FileSchemaVersion reads the schema version of a database file without migrating or modifying it (read-only open). A file
// that is not a Drydock database yields an error; version 0 means no migration was ever applied.
func FileSchemaVersion(ctx context.Context, path string) (int64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	q := url.Values{}
	q.Set("mode", "ro")
	db, err := sql.Open("sqlite", fileURI(abs)+"?"+q.Encode())
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return schemaVersion(ctx, db)
}

func schemaVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MAX(version_id) FROM goose_db_version WHERE is_applied=1`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return v.Int64, nil
}
