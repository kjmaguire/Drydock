package packs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Event types published on the bus (docs/expert-packs.md section 7).
const (
	EventConsulted     = "pack.consulted"
	EventStale         = "pack.stale"
	EventRefreshed     = "pack.refreshed"
	EventPageChanged   = "pack.page_changed"
	EventRefreshFailed = "pack.refresh_failed"
	EventAttached      = "pack.attached"
	// Extensions: lifecycle events a UI needs.
	EventInstalled = "pack.installed"
	EventRemoved   = "pack.removed"
	EventDetached  = "pack.detached"
	EventReleased  = "pack.chunk_released"
)

// Errors.
var (
	ErrNotFound    = errors.New("packs: pack not found")
	ErrBusy        = errors.New("packs: a refresh is already running for this pack")
	ErrNotIndexed  = errors.New("packs: a live pack has nothing to refresh")
	ErrNoScanner   = errors.New("packs: a secret scanner is required")
	ErrNotHuman    = security.ErrNotHuman
	ErrUnknownPack = errors.New("packs: unknown pack")
)

// Options configures a Store.
type Options struct {
	DB  *storage.DB
	Bus events.Bus
	// Dir is <app data>/packs: packs/<id>/pack.yaml and the normalized pages.
	Dir string
	// Scanner redacts secrets from everything before it is indexed (gitleaks in production). Required.
	Scanner security.SecretScanner
	// Live reaches MCP docs servers through the gateway for live and hybrid packs. Nil: live packs report unavailable.
	Live LiveSearcher
	// Summarizer (optional) turns the deterministic changed-pages digest into a short summary with a cheap model.
	Summarizer Summarizer
	// Answerer (optional) synthesizes CONSULT answers from retrieved chunks. Without it CONSULT is retrieval-only.
	Answerer Answerer
	Now      func() time.Time
	// AllowLoopbackFetch lets manifests name http://127.0.0.1 sources and the fetcher dial loopback. Tests and local mirrors
	// only.
	AllowLoopbackFetch bool
	// FetchTransport replaces the fetcher's HTTP transport (tests).
	FetchTransport http.RoundTripper
	Logf           func(format string, a ...any)
}

// Store is the Expert Pack service.
type Store struct {
	o         Options
	locks     sync.Map // pack id -> *sync.Mutex
	staleSeen sync.Map
}

// New builds a Store.
func New(o Options) (*Store, error) {
	if o.DB == nil || o.Dir == "" {
		return nil, errors.New("packs: DB and Dir are required")
	}
	if o.Scanner == nil {
		return nil, ErrNoScanner
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{o: o}, nil
}

func (s *Store) now() time.Time { return s.o.Now().UTC() }

func (s *Store) emit(ctx context.Context, typ string, data any) {
	if s.o.Bus == nil {
		return
	}
	if _, err := events.Emit(ctx, s.o.Bus, typ, events.Event{}, data); err != nil {
		s.o.Logf("packs: event %s: %v", typ, err)
	}
}

func requireHumanOrSystem(by string) error {
	if by == "system" {
		return nil
	}
	return security.RequireHuman(by)
}

// Pack is a pack as the UI lists it.
type Pack struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Kind          string    `json:"kind"`
	Visibility    string    `json:"visibility"`
	Version       string    `json:"version"`
	Status        string    `json:"status"`
	LastRefreshAt time.Time `json:"lastRefreshAt"`
	LastSuccessAt time.Time `json:"lastSuccessAt"`
	LastError     string    `json:"lastError,omitempty"`
	Pages         int       `json:"pages"`
	Chunks        int       `json:"chunks"`
	Quarantined   int       `json:"quarantined"`
	// AgeDays is the age of the last successful refresh (-1 when the pack has no local copy or never refreshed).
	AgeDays int `json:"ageDays"`
	// Freshness is "fresh", "warn" or "stale" from the manifest's thresholds ("" for live packs).
	Freshness string `json:"freshness,omitempty"`
	// Offline is true when the pack answers without any network (an indexed pack with content).
	Offline       bool      `json:"offline"`
	ManifestDrift bool      `json:"manifestDrift,omitempty"` // pack.yaml on disk differs from the installed manifest
	NextRefreshAt time.Time `json:"nextRefreshAt,omitempty"`
	Manifest      *Manifest `json:"manifest"`
	Attribution   string    `json:"attribution,omitempty"`
}

type packRow struct {
	id, title, kind, vis, version, manifest, manifestHash, status, lastErr string
	seq                                                                    int64
	lastRefresh, lastOK                                                    int64
}

const packCols = `id,title,kind,visibility,version,version_seq,manifest,manifest_hash,status,last_refresh_at,last_success_at,last_error`

func scanPackRow(sc interface{ Scan(...any) error }) (packRow, error) {
	var r packRow
	err := sc.Scan(&r.id, &r.title, &r.kind, &r.vis, &r.version, &r.seq, &r.manifest, &r.manifestHash, &r.status, &r.lastRefresh, &r.lastOK, &r.lastErr)
	return r, err
}

func (s *Store) loadRow(ctx context.Context, id string) (packRow, *Manifest, error) {
	r, err := scanPackRow(s.o.DB.Reader().QueryRowContext(ctx, `SELECT `+packCols+` FROM packs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return r, nil, err
	}
	m, err := ParseManifest([]byte(r.manifest), ValidateOptions{AllowLoopbackHTTP: s.o.AllowLoopbackFetch})
	if err != nil {
		return r, nil, fmt.Errorf("stored manifest of %s no longer validates: %w", id, err)
	}
	return r, m, nil
}

func (s *Store) toPack(ctx context.Context, r packRow, m *Manifest) Pack {
	p := Pack{ID: r.id, Title: r.title, Kind: r.kind, Visibility: r.vis, Version: r.version, Status: r.status, LastError: r.lastErr, Manifest: m, AgeDays: -1}
	if r.lastRefresh > 0 {
		p.LastRefreshAt = time.UnixMilli(r.lastRefresh).UTC()
	}
	if r.lastOK > 0 {
		p.LastSuccessAt = time.UnixMilli(r.lastOK).UTC()
	}
	rd := s.o.DB.Reader()
	_ = rd.QueryRowContext(ctx, `SELECT COUNT(*) FROM pack_pages WHERE pack_id=? AND status='active'`, r.id).Scan(&p.Pages)
	_ = rd.QueryRowContext(ctx, `SELECT COUNT(*) FROM pack_chunks WHERE pack_id=? AND seq_removed IS NULL`, r.id).Scan(&p.Chunks)
	_ = rd.QueryRowContext(ctx, `SELECT COUNT(*) FROM pack_chunks WHERE pack_id=? AND seq_removed IS NULL AND quarantined=1`, r.id).Scan(&p.Quarantined)
	if m != nil {
		if m.License.AttributionRequired {
			p.Attribution = m.License.Attribution
		}
		if r.kind != KindLive && r.lastOK > 0 {
			age := s.now().Sub(time.UnixMilli(r.lastOK))
			p.AgeDays = int(age.Hours() / 24)
			switch {
			case age > m.Freshness.StaleAfter.D():
				p.Freshness = "stale"
			case age > m.Freshness.WarnAfter.D():
				p.Freshness = "warn"
			default:
				p.Freshness = "fresh"
			}
			if m.Refresh.Mode == "scheduled" {
				p.NextRefreshAt = time.UnixMilli(r.lastRefresh).Add(m.Refresh.Interval.D()).UTC()
			}
		}
		p.Offline = r.kind != KindLive && p.Chunks > 0
		if b, err := os.ReadFile(filepath.Join(s.o.Dir, r.id, "pack.yaml")); err == nil && HashHex(string(b)) != r.manifestHash {
			p.ManifestDrift = true
		}
	}
	return p
}

// List returns every installed pack by id.
func (s *Store) List(ctx context.Context) ([]Pack, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT `+packCols+` FROM packs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var rs []packRow
	for rows.Next() {
		r, err := scanPackRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	out := []Pack{}
	for _, r := range rs {
		m, err := ParseManifest([]byte(r.manifest), ValidateOptions{AllowLoopbackHTTP: s.o.AllowLoopbackFetch})
		if err != nil {
			m = nil
		}
		out = append(out, s.toPack(ctx, r, m))
	}
	return out, nil
}

// Get returns one pack.
func (s *Store) Get(ctx context.Context, id string) (Pack, error) {
	r, m, err := s.loadRow(ctx, id)
	if err != nil {
		return Pack{}, err
	}
	return s.toPack(ctx, r, m), nil
}

// Install validates a manifest and registers (or updates) the pack. It never fetches anything: the first refresh is a
// separate human action. The manifest text is kept as written (comments included) in packs/<id>/pack.yaml. Human only.
func (s *Store) Install(ctx context.Context, by string, manifest []byte) (Pack, error) {
	if err := security.RequireHuman(by); err != nil {
		return Pack{}, err
	}
	if len(manifest) > 64<<10 {
		return Pack{}, errors.New("packs: pack.yaml is larger than 64 KiB")
	}
	m, err := ParseManifest(manifest, ValidateOptions{AllowLoopbackHTTP: s.o.AllowLoopbackFetch})
	if err != nil {
		return Pack{}, err
	}
	now := s.now().UnixMilli()
	text := string(manifest)
	h := HashHex(text)
	var created bool
	err = s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var oldKind string
		e := tx.QueryRowContext(ctx, `SELECT kind FROM packs WHERE id=?`, m.ID).Scan(&oldKind)
		switch {
		case errors.Is(e, sql.ErrNoRows):
			created = true
			_, e = tx.ExecContext(ctx, `INSERT INTO packs(id,title,kind,visibility,manifest,manifest_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
				m.ID, m.Title, m.Kind, m.Visibility, text, h, "new", now, now)
		case e != nil:
		default:
			_, e = tx.ExecContext(ctx, `UPDATE packs SET title=?,kind=?,visibility=?,manifest=?,manifest_hash=?,updated_at=? WHERE id=?`, m.Title, m.Kind, m.Visibility, text, h, now, m.ID)
		}
		return e
	})
	if err != nil {
		return Pack{}, err
	}
	dir := filepath.Join(s.o.Dir, m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Pack{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.yaml"), manifest, 0o644); err != nil {
		return Pack{}, err
	}
	s.emit(ctx, EventInstalled, map[string]any{"pack": m.ID, "kind": m.Kind, "visibility": m.Visibility, "created": created, "by": by})
	return s.Get(ctx, m.ID)
}

// Remove deletes a pack, its index, its attachments and its stored pages. Human only.
func (s *Store) Remove(ctx context.Context, by, id string) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	var exists int
	if err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM packs WHERE id=?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	mu := s.lock(id)
	if !mu.TryLock() {
		return ErrBusy
	}
	defer mu.Unlock()
	err := s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		// chunks first, so the FTS delete trigger runs for every row
		for _, q := range []string{`DELETE FROM pack_chunks WHERE pack_id=?`, `DELETE FROM pack_pages WHERE pack_id=?`, `DELETE FROM pack_pins WHERE pack_id=?`,
			`DELETE FROM pack_attachments WHERE pack_id=?`, `DELETE FROM pack_versions WHERE pack_id=?`, `DELETE FROM packs WHERE id=?`} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(s.o.Dir, id))
	s.emit(ctx, EventRemoved, map[string]any{"pack": id, "by": by})
	return nil
}

func (s *Store) lock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Quarantined lists a pack's chunks held back from retrieval, with the reason (scan flags).
func (s *Store) Quarantined(ctx context.Context, packID string) ([]QuarantinedChunk, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT c.id,c.heading_path,c.body,c.risk,c.flags,p.url FROM pack_chunks c JOIN pack_pages p ON p.id=c.page_id
		WHERE c.pack_id=? AND c.seq_removed IS NULL AND c.quarantined=1 ORDER BY c.id`, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuarantinedChunk{}
	for rows.Next() {
		var q QuarantinedChunk
		var flags string
		if err := rows.Scan(&q.ChunkID, &q.HeadingPath, &q.Body, &q.Risk, &flags, &q.URL); err != nil {
			return nil, err
		}
		q.Flags = splitFlags(flags)
		out = append(out, q)
	}
	return out, rows.Err()
}

// QuarantinedChunk is a chunk the scan held back.
type QuarantinedChunk struct {
	ChunkID     int64    `json:"chunkId"`
	URL         string   `json:"url"`
	HeadingPath string   `json:"headingPath"`
	Body        string   `json:"body"`
	Risk        int      `json:"risk"`
	Flags       []string `json:"flags"`
}

func splitFlags(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// ReleaseChunk lets a quarantined chunk into retrieval. Human only; the chunk stays marked (released_by) and a later refresh
// that leaves the chunk unchanged keeps the release.
func (s *Store) ReleaseChunk(ctx context.Context, by string, chunkID int64) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	var pack string
	err := s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT pack_id FROM pack_chunks WHERE id=? AND seq_removed IS NULL`, chunkID).Scan(&pack); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: chunk %d", ErrNotFound, chunkID)
			}
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE pack_chunks SET quarantined=0, released_by=? WHERE id=?`, by, chunkID)
		return err
	})
	if err == nil {
		s.emit(ctx, EventReleased, map[string]any{"pack": pack, "chunk": chunkID, "by": by})
	}
	return err
}

// Pin keeps a version past the normal retention (a stored citation points at it).
func (s *Store) Pin(ctx context.Context, packID, version, reason string) error {
	return s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pack_versions WHERE pack_id=? AND version=?`, packID, version).Scan(&n); err != nil || n == 0 {
			return fmt.Errorf("%w: %s@%s", ErrNotFound, packID, version)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO pack_pins(pack_id,version,reason,created_at) VALUES(?,?,?,?) ON CONFLICT(pack_id,version) DO NOTHING`, packID, version, reason, s.now().UnixMilli())
		return err
	})
}

// VersionInfo is one entry of a pack's history.
type VersionInfo struct {
	Version   string    `json:"version"`
	Seq       int64     `json:"seq"`
	CreatedAt time.Time `json:"createdAt"`
	Pages     int       `json:"pages"`
	Chunks    int       `json:"chunks"`
	Added     int       `json:"added"`
	Changed   int       `json:"changed"`
	Removed   int       `json:"removed"`
	Skipped   int       `json:"skipped"`
	Failed    int       `json:"failed"`
	Digest    *Digest   `json:"digest,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Pinned    bool      `json:"pinned,omitempty"`
}

// Versions lists a pack's versions, newest first, with their changed-pages digests.
func (s *Store) Versions(ctx context.Context, packID string) ([]VersionInfo, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT v.version,v.seq,v.created_at,v.pages,v.chunks,v.added,v.changed,v.removed,v.skipped,v.failed,v.digest,v.summary,
		EXISTS(SELECT 1 FROM pack_pins n WHERE n.pack_id=v.pack_id AND n.version=v.version) FROM pack_versions v WHERE v.pack_id=? ORDER BY v.seq DESC`, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VersionInfo{}
	for rows.Next() {
		var v VersionInfo
		var created int64
		var digest string
		if err := rows.Scan(&v.Version, &v.Seq, &created, &v.Pages, &v.Chunks, &v.Added, &v.Changed, &v.Removed, &v.Skipped, &v.Failed, &digest, &v.Summary, &v.Pinned); err != nil {
			return nil, err
		}
		v.CreatedAt = time.UnixMilli(created).UTC()
		var d Digest
		if json.Unmarshal([]byte(digest), &d) == nil && d.PackID != "" {
			v.Digest = &d
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// packIDs returns every installed pack id, sorted.
func (s *Store) packIDs(ctx context.Context) ([]string, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT id FROM packs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, rows.Err()
}
