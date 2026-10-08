package requirements

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Event types (brief section 10; docs/expert-packs.md section 10 plus lifecycle extensions).
const (
	EventAdded    = "requirement.added"
	EventChanged  = "requirement.changed"
	EventLinked   = "requirement.linked"
	EventArchived = "requirement.archived"
	EventReleased = "requirement.chunk_released"

	EventMilestoneCreated = "milestone.created"
	EventMilestoneUpdated = "milestone.updated"
	EventMilestoneClosed  = "milestone.closed"
	EventMilestoneOpened  = "milestone.reopened"
	EventMilestoneDeleted = "milestone.deleted"
	EventMilestoneTask    = "milestone.task_changed"
)

// Errors.
var (
	ErrNotFound = errors.New("requirements: not found")
	ErrNotHuman = security.ErrNotHuman
)

// Service stores requirements, versions and chunks, links criteria to tasks and reports traceability.
type Service struct {
	DB      *storage.DB
	Bus     events.Bus
	Scanner security.SecretScanner // required: secrets are redacted before anything is stored
	Now     func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) emit(ctx context.Context, typ, projectID, taskID string, data any) {
	if s.Bus == nil {
		return
	}
	_, _ = events.Emit(ctx, s.Bus, typ, events.Event{ProjectID: projectID, TaskID: taskID}, data)
}

// Requirement is a document as listed.
type Requirement struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	Title       string    `json:"title"`
	Source      string    `json:"source"` // upload | paste | sync
	SourceRef   string    `json:"sourceRef,omitempty"`
	Version     int       `json:"version"`
	ContentHash string    `json:"contentHash"`
	Archived    bool      `json:"archived"`
	CreatedBy   string    `json:"createdBy,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
	Chunks      int       `json:"chunks"`
	Quarantined int       `json:"quarantined"`
	Links       int       `json:"links"`
	StaleLinks  int       `json:"staleLinks"`
}

// AddReq creates a document.
type AddReq struct {
	ProjectID string `json:"projectId"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Format    string `json:"format"` // markdown (default) | text | pdftext
	Source    string `json:"source"` // upload | paste (default upload)
	By        string `json:"by"`
}

// SyncReq creates or updates the document synced from an external item.
type SyncReq struct {
	ProjectID string
	SourceRef string // stable id of the external item, for example "notion:page-id"
	Title     string
	Text      string
	Format    string
	By        string // human:, integration:<id> or system
}

func allowedSyncer(by string) error {
	if by == "system" || strings.HasPrefix(by, "integration:") && len(by) > len("integration:") {
		return nil
	}
	return security.RequireHuman(by)
}

type prepared struct {
	text        string
	hash        string
	chunks      []packs.Chunk
	scans       []packs.Scanned
	redactions  int
	title       string
}

func (s *Service) prepare(format, title, text string) (prepared, error) {
	if s.Scanner == nil {
		return prepared{}, packs.ErrNoScanner
	}
	md, err := Normalize(format, text)
	if err != nil {
		return prepared{}, err
	}
	if strings.TrimSpace(md) == "" {
		return prepared{}, errors.New("requirements: the document is empty")
	}
	red := packs.RedactSecrets(s.Scanner, md)
	stored, _ := packs.StripHidden(red.Text) // hidden-character marks are for chunk scanning only; they are never stored
	p := prepared{text: stored, redactions: red.Count, hash: packs.HashHex(md)}
	t, chunks := packs.ChunkMarkdown(red.Text, strings.TrimSpace(title), packs.DefaultChunkOptions())
	p.title = strings.TrimSpace(title)
	if p.title == "" {
		p.title = t
	}
	if p.title == "" {
		return prepared{}, errors.New("requirements: a title is required (the document has no heading to use)")
	}
	for _, c := range chunks {
		sc := packs.ScanChunk(c.Body)
		c.Body = sc.Text
		c.TokenEst = packs.EstimateTokens(c.Body)
		c.Hash = packs.HashHex(c.HeadingPath, c.Body)
		p.chunks = append(p.chunks, c)
		p.scans = append(p.scans, sc)
	}
	if len(p.chunks) == 0 {
		return prepared{}, errors.New("requirements: the document has no content")
	}
	return p, nil
}

// Add stores a new document (version 1). Human only.
func (s *Service) Add(ctx context.Context, r AddReq) (Requirement, error) {
	if err := security.RequireHuman(r.By); err != nil {
		return Requirement{}, err
	}
	src := r.Source
	if src == "" {
		src = "upload"
	}
	if src != "upload" && src != "paste" {
		return Requirement{}, errors.New("requirements: source must be upload or paste")
	}
	return s.create(ctx, r.ProjectID, src, "", r.Title, r.Text, r.Format, r.By)
}

func (s *Service) create(ctx context.Context, projectID, source, sourceRef, title, text, format, by string) (Requirement, error) {
	p, err := s.prepare(format, title, text)
	if err != nil {
		return Requirement{}, err
	}
	id := ids.New("req")
	now := s.now().UnixMilli()
	err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, projectID).Scan(&n); err != nil || n == 0 {
			return fmt.Errorf("requirements: project %q not found", projectID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO requirements(id,project_id,title,source,source_ref,current_version,content_hash,created_by,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?,?,?)`,
			id, projectID, p.title, source, sourceRef, p.hash, by, now, now); err != nil {
			return err
		}
		return s.insertVersion(ctx, tx, id, 1, p, by, now)
	})
	if err != nil {
		return Requirement{}, err
	}
	s.emit(ctx, EventAdded, projectID, "", map[string]any{"id": id, "title": p.title, "version": 1, "source": source, "chunks": len(p.chunks), "redactions": p.redactions, "by": by})
	return s.Get(ctx, id)
}

func (s *Service) insertVersion(ctx context.Context, tx *sql.Tx, id string, version int, p prepared, by string, now int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO requirement_versions(requirement_id,version,content_hash,text,chunks,redactions,created_by,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		id, version, p.hash, p.text, len(p.chunks), p.redactions, by, now); err != nil {
		return err
	}
	for i, c := range p.chunks {
		q := 0
		if p.scans[i].Quarantined {
			q = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO requirement_chunks(requirement_id,version,ordinal,heading_path,body,token_est,chunk_hash,risk,flags,quarantined,current) VALUES(?,?,?,?,?,?,?,?,?,?,1)`,
			id, version, c.Ordinal, c.HeadingPath, c.Body, c.TokenEst, c.Hash, p.scans[i].Risk, strings.Join(p.scans[i].Flags, ","), q); err != nil {
			return err
		}
	}
	return nil
}

// Update stores new text as the next version when its content hash differs (otherwise nothing changes). Human only.
func (s *Service) Update(ctx context.Context, by, id, text, format string) (Requirement, bool, error) {
	if err := security.RequireHuman(by); err != nil {
		return Requirement{}, false, err
	}
	return s.update(ctx, by, id, "", text, format)
}

func (s *Service) update(ctx context.Context, by, id, title, text, format string) (Requirement, bool, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return Requirement{}, false, err
	}
	if title == "" {
		title = cur.Title
	}
	p, err := s.prepare(format, title, text)
	if err != nil {
		return Requirement{}, false, err
	}
	if p.hash == cur.ContentHash && p.title == cur.Title {
		return cur, false, nil
	}
	changedBody := p.hash != cur.ContentHash
	now := s.now().UnixMilli()
	next := cur.Version
	if changedBody {
		next = cur.Version + 1
	}
	var stale int
	err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if changedBody {
			if _, err := tx.ExecContext(ctx, `UPDATE requirement_chunks SET current=0 WHERE requirement_id=?`, id); err != nil {
				return err
			}
			if err := s.insertVersion(ctx, tx, id, next, p, by, now); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE requirements SET title=?,current_version=?,content_hash=?,updated_at=? WHERE id=?`, p.title, next, p.hash, now, id); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT task_id) FROM requirement_links WHERE requirement_id=? AND version < ?`, id, next).Scan(&stale)
	})
	if err != nil {
		return Requirement{}, false, err
	}
	if changedBody {
		s.emit(ctx, EventChanged, cur.ProjectID, "", map[string]any{"id": id, "title": p.title, "version": next, "previous": cur.Version, "chunks": len(p.chunks), "stale_tasks": stale, "redactions": p.redactions, "by": by})
	}
	out, err := s.Get(ctx, id)
	return out, changedBody, err
}

// Sync creates the document for an external item or, when its content hash changed, a new version (requirement.changed).
// It returns whether anything changed. Callers are humans, integrations ("integration:<id>") or "system".
func (s *Service) Sync(ctx context.Context, r SyncReq) (Requirement, bool, error) {
	if err := allowedSyncer(r.By); err != nil {
		return Requirement{}, false, err
	}
	if strings.TrimSpace(r.SourceRef) == "" {
		return Requirement{}, false, errors.New("requirements: a synced document needs a source_ref")
	}
	var id string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT id FROM requirements WHERE project_id=? AND source_ref=?`, r.ProjectID, r.SourceRef).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		out, err := s.create(ctx, r.ProjectID, "sync", r.SourceRef, r.Title, r.Text, r.Format, r.By)
		return out, err == nil, err
	}
	if err != nil {
		return Requirement{}, false, err
	}
	return s.update(ctx, r.By, id, r.Title, r.Text, r.Format)
}

const reqCols = `r.id,r.project_id,r.title,r.source,r.source_ref,r.current_version,r.content_hash,r.archived,r.created_by,r.created_at,r.updated_at,
	(SELECT COUNT(*) FROM requirement_chunks c WHERE c.requirement_id=r.id AND c.current=1),
	(SELECT COUNT(*) FROM requirement_chunks c WHERE c.requirement_id=r.id AND c.current=1 AND c.quarantined=1),
	(SELECT COUNT(DISTINCT l.task_id) FROM requirement_links l WHERE l.requirement_id=r.id),
	(SELECT COUNT(DISTINCT l.task_id) FROM requirement_links l WHERE l.requirement_id=r.id AND l.version < r.current_version)`

func scanReq(sc interface{ Scan(...any) error }) (Requirement, error) {
	var r Requirement
	var archived int
	var c, u int64
	err := sc.Scan(&r.ID, &r.ProjectID, &r.Title, &r.Source, &r.SourceRef, &r.Version, &r.ContentHash, &archived, &r.CreatedBy, &c, &u, &r.Chunks, &r.Quarantined, &r.Links, &r.StaleLinks)
	r.Archived = archived == 1
	r.Created, r.Updated = time.UnixMilli(c).UTC(), time.UnixMilli(u).UTC()
	return r, err
}

// Get returns one document.
func (s *Service) Get(ctx context.Context, id string) (Requirement, error) {
	r, err := scanReq(s.DB.Reader().QueryRowContext(ctx, `SELECT `+reqCols+` FROM requirements r WHERE r.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return r, err
}

// List returns a project's documents, newest first.
func (s *Service) List(ctx context.Context, projectID string, includeArchived bool) ([]Requirement, error) {
	q := `SELECT ` + reqCols + ` FROM requirements r WHERE r.project_id=?`
	if !includeArchived {
		q += ` AND r.archived=0`
	}
	rows, err := s.DB.Reader().QueryContext(ctx, q+` ORDER BY r.updated_at DESC, r.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Requirement{}
	for rows.Next() {
		r, err := scanReq(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// VersionInfo is one version of a document.
type VersionInfo struct {
	Version     int       `json:"version"`
	ContentHash string    `json:"contentHash"`
	Chunks      int       `json:"chunks"`
	Redactions  int       `json:"redactions"`
	CreatedBy   string    `json:"createdBy,omitempty"`
	Created     time.Time `json:"created"`
}

// Versions lists a document's versions, newest first.
func (s *Service) Versions(ctx context.Context, id string) ([]VersionInfo, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT version,content_hash,chunks,redactions,created_by,created_at FROM requirement_versions WHERE requirement_id=? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VersionInfo{}
	for rows.Next() {
		var v VersionInfo
		var ts int64
		if err := rows.Scan(&v.Version, &v.ContentHash, &v.Chunks, &v.Redactions, &v.CreatedBy, &ts); err != nil {
			return nil, err
		}
		v.Created = time.UnixMilli(ts).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

// Text returns the stored (redacted) Markdown of a version; version 0 means the current one.
func (s *Service) Text(ctx context.Context, id string, version int) (string, error) {
	if version == 0 {
		r, err := s.Get(ctx, id)
		if err != nil {
			return "", err
		}
		version = r.Version
	}
	var t string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT text FROM requirement_versions WHERE requirement_id=? AND version=?`, id, version).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %s@%d", ErrNotFound, id, version)
	}
	return t, err
}

// Archive hides a document from search and lists (its versions, citations and links stay). Human only.
func (s *Service) Archive(ctx context.Context, by, id string, archived bool) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	r, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	v := 0
	if archived {
		v = 1
	}
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE requirements SET archived=?, updated_at=? WHERE id=?`, v, s.now().UnixMilli(), id)
		return err
	}); err != nil {
		return err
	}
	s.emit(ctx, EventArchived, r.ProjectID, "", map[string]any{"id": id, "archived": archived, "by": by})
	return nil
}

// QuarantinedChunk is a chunk held back by the injection scan.
type QuarantinedChunk struct {
	ChunkID     int64    `json:"chunkId"`
	Version     int      `json:"version"`
	HeadingPath string   `json:"headingPath"`
	Body        string   `json:"body"`
	Risk        int      `json:"risk"`
	Flags       []string `json:"flags"`
}

// Quarantined lists the current version's chunks held back from search.
func (s *Service) Quarantined(ctx context.Context, id string) ([]QuarantinedChunk, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id,version,heading_path,body,risk,flags FROM requirement_chunks WHERE requirement_id=? AND current=1 AND quarantined=1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuarantinedChunk{}
	for rows.Next() {
		var q QuarantinedChunk
		var flags string
		if err := rows.Scan(&q.ChunkID, &q.Version, &q.HeadingPath, &q.Body, &q.Risk, &flags); err != nil {
			return nil, err
		}
		if flags != "" {
			q.Flags = strings.Split(flags, ",")
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// ReleaseChunk lets a quarantined chunk into search. Human only.
func (s *Service) ReleaseChunk(ctx context.Context, by string, chunkID int64) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	var proj, req string
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT r.project_id, r.id FROM requirement_chunks c JOIN requirements r ON r.id=c.requirement_id WHERE c.id=?`, chunkID).Scan(&proj, &req); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: chunk %d", ErrNotFound, chunkID)
			}
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE requirement_chunks SET quarantined=0 WHERE id=?`, chunkID)
		return err
	})
	if err == nil {
		s.emit(ctx, EventReleased, proj, "", map[string]any{"id": req, "chunk": chunkID, "by": by})
	}
	return err
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
