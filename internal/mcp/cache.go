package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

// cacheStore is the exact-match result cache (docs/mcp.md section 4): rows in mcp_cache, redacted raw results on disk.
type cacheStore struct {
	db  *storage.DB
	dir string // <app data>/mcp-cache
	now func() time.Time
}

type cacheEntry struct {
	Key       string
	Server    string
	Tool      string
	Scope     string
	Version   string
	ArgsHash  string
	Text      string // redacted raw result text
	IsError   bool
	CreatedAt time.Time
	ExpiresAt time.Time
	Hits      int
}

type cacheFile struct {
	Text    string `json:"text"`
	IsError bool   `json:"is_error,omitempty"`
}

// cacheKey is sha256(scope || server_id || server_ident || tool || canonical_json(args)); server_ident (a hash of the
// server config, its reported version and the credential identity) is what makes a server upgrade or a credential change
// miss.
func cacheKey(scope, serverID, serverIdent, tool string, canonArgs []byte) string {
	return sha256Hex([]byte(scope), []byte(serverID), []byte(serverIdent), []byte(tool), canonArgs)
}

func (c *cacheStore) enabled() bool { return c != nil && c.db != nil && c.dir != "" }

// Get returns a live entry. An expired row is deleted (evicted=true) and reported as a miss.
func (c *cacheStore) Get(ctx context.Context, key string) (e *cacheEntry, evicted bool, err error) {
	if !c.enabled() {
		return nil, false, nil
	}
	var (
		row      cacheEntry
		created  int64
		expires  int64
		rawRef   string
		isErrInt int
	)
	err = c.db.Reader().QueryRowContext(ctx,
		`SELECT key, server_id, tool, scope, server_version, args_hash, raw_ref, is_error, created_at, expires_at, hits FROM mcp_cache WHERE key = ?`, key).
		Scan(&row.Key, &row.Server, &row.Tool, &row.Scope, &row.Version, &row.ArgsHash, &rawRef, &isErrInt, &created, &expires, &row.Hits)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	now := c.now()
	if expires <= now.UnixMilli() {
		_ = c.delete(ctx, key, rawRef)
		return nil, true, nil
	}
	b, rerr := os.ReadFile(filepath.Join(c.dir, filepath.Base(rawRef)))
	var cf cacheFile
	if rerr != nil || json.Unmarshal(b, &cf) != nil {
		_ = c.delete(ctx, key, rawRef) // the file is gone or corrupt: a miss, never an error towards the agent
		return nil, false, nil
	}
	row.Text, row.IsError = cf.Text, cf.IsError
	row.CreatedAt, row.ExpiresAt = time.UnixMilli(created), time.UnixMilli(expires)
	if err := c.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE mcp_cache SET hits = hits + 1 WHERE key = ?`, key)
		return e
	}); err != nil {
		return nil, false, err
	}
	row.Hits++
	return &row, false, nil
}

// Put stores (or replaces) an entry that expires ttl from now.
func (c *cacheStore) Put(ctx context.Context, e *cacheEntry, ttl time.Duration) error {
	if !c.enabled() || ttl <= 0 {
		return nil
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	ref := e.Key + ".json"
	b, err := json.Marshal(cacheFile{Text: e.Text, IsError: e.IsError})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, "put-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(c.dir, ref)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	now := c.now()
	return c.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, e2 := tx.ExecContext(ctx, `INSERT INTO mcp_cache(key, server_id, tool, scope, server_version, args_hash, raw_ref, bytes, is_error, created_at, expires_at, hits)
VALUES (?,?,?,?,?,?,?,?,?,?,?,0)
ON CONFLICT(key) DO UPDATE SET server_version=excluded.server_version, raw_ref=excluded.raw_ref, bytes=excluded.bytes, is_error=excluded.is_error,
  created_at=excluded.created_at, expires_at=excluded.expires_at, hits=0`,
			e.Key, e.Server, e.Tool, e.Scope, e.Version, e.ArgsHash, ref, len(e.Text), boolInt(e.IsError), now.UnixMilli(), now.Add(ttl).UnixMilli())
		return e2
	})
}

func (c *cacheStore) delete(ctx context.Context, key, ref string) error {
	_ = os.Remove(filepath.Join(c.dir, filepath.Base(ref)))
	return c.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `DELETE FROM mcp_cache WHERE key = ?`, key)
		return e
	})
}

// Purge deletes every expired entry and returns how many were removed.
func (c *cacheStore) Purge(ctx context.Context) (int, error) {
	if !c.enabled() {
		return 0, nil
	}
	rows, err := c.db.Reader().QueryContext(ctx, `SELECT key, raw_ref FROM mcp_cache WHERE expires_at <= ?`, c.now().UnixMilli())
	if err != nil {
		return 0, err
	}
	type kr struct{ k, r string }
	var dead []kr
	for rows.Next() {
		var x kr
		if err := rows.Scan(&x.k, &x.r); err != nil {
			rows.Close()
			return 0, err
		}
		dead = append(dead, x)
	}
	rows.Close()
	for _, x := range dead {
		if err := c.delete(ctx, x.k, x.r); err != nil {
			return 0, fmt.Errorf("purge %s: %w", x.k, err)
		}
	}
	return len(dead), nil
}

// InvalidateServer removes every entry of a server (config change, pack refresh).
func (c *cacheStore) InvalidateServer(ctx context.Context, serverID string) (int, error) {
	if !c.enabled() {
		return 0, nil
	}
	rows, err := c.db.Reader().QueryContext(ctx, `SELECT key, raw_ref FROM mcp_cache WHERE server_id = ?`, serverID)
	if err != nil {
		return 0, err
	}
	type kr struct{ k, r string }
	var dead []kr
	for rows.Next() {
		var x kr
		if err := rows.Scan(&x.k, &x.r); err != nil {
			rows.Close()
			return 0, err
		}
		dead = append(dead, x)
	}
	rows.Close()
	for _, x := range dead {
		if err := c.delete(ctx, x.k, x.r); err != nil {
			return 0, err
		}
	}
	return len(dead), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
