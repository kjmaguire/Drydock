package projectio

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Errors a caller can recognize.
var (
	ErrNewerSchema = errors.New("projectio: this archive was made by a newer Drydock (its database schema is newer than this version's); update Drydock to import it")
	ErrNewerFormat = errors.New("projectio: this archive uses a newer archive format than this version reads; update Drydock to import it")
	ErrBadArchive  = errors.New("projectio: not a valid Drydock project archive")
)

const (
	maxManifest   = 8 << 20
	maxEntries    = 200000
	maxTotalBytes = 8 << 30
)

// Preview is what the human sees before importing; it is computed without touching the live database.
type Preview struct {
	Manifest       Manifest       `json:"manifest"`
	AppSchema      int64          `json:"appSchema"`
	NeedsMigration bool           `json:"needsMigration"`
	Counts         map[string]int `json:"counts"`
	// Pending is what the import would hold back or switch off until a human decides.
	Pending Stats `json:"pending"`
}

// Inspect validates an archive (manifest, entry names, SHA-256 of every file, schema version) and describes what importing
// it would do.
func Inspect(ctx context.Context, archive string) (Preview, error) {
	a, err := openArchive(ctx, archive)
	if err != nil {
		return Preview{}, err
	}
	defer a.close()
	db, migrated, err := a.openDB(ctx)
	if err != nil {
		return Preview{}, err
	}
	defer db.Close()
	im := &ImportEnv{Source: filepath.Base(archive), Human: "human:you"}
	counts := map[string]int{}
	for _, t := range Tables {
		n := 0
		err := readTable(ctx, db, t, func(r Row) error {
			n++
			if t.Import != nil {
				t.Import(r, im)
			}
			return nil
		})
		if err != nil {
			return Preview{}, err
		}
		counts[t.Name] = n
	}
	return Preview{Manifest: a.m, AppSchema: storage.LatestVersion(), NeedsMigration: migrated, Counts: counts, Pending: im.Stats}, nil
}

// Target is the live installation an import writes to.
type Target struct {
	DB      *storage.DB
	DataDir string
	// Human is the importing user's member id ("human:<name>").
	Human string
}

// ImportOptions are the human's choices. RepoPath is required: the archive never carries a repository.
type ImportOptions struct {
	Archive  string
	RepoPath string // an existing local repository root, already validated by the caller
	Branch   string // its checked-out branch
	Name     string // optional; defaults to the exported name
	Now      int64  // ms
}

// ImportResult describes the new project.
type ImportResult struct {
	ProjectID    string         `json:"projectId"`
	Name         string         `json:"name"`
	SourceID     string         `json:"sourceProjectId"`
	Counts       map[string]int `json:"counts"`
	IDsRemapped  int            `json:"idsRemapped"`
	RunFiles     int            `json:"runFilesRestored"`
	Migrated     bool           `json:"migratedForward"`
	Pending      Stats          `json:"pending"`
	SourceSchema int64          `json:"sourceSchema"`
}

// Import creates a NEW project from the archive with every id remapped. It never overwrites or merges with an existing
// project. Everything it writes is one transaction; raw run files are restored first and removed again if it fails.
func Import(ctx context.Context, t Target, o ImportOptions) (ImportResult, error) {
	if strings.TrimSpace(o.RepoPath) == "" {
		return ImportResult{}, errors.New("projectio: a local repository path is required to import a project")
	}
	a, err := openArchive(ctx, o.Archive)
	if err != nil {
		return ImportResult{}, err
	}
	defer a.close()
	src, migrated, err := a.openDB(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer src.Close()

	// pass 1: every id-shaped token in the archive gets a fresh id (consistently, in every column and in JSON)
	idmap := map[string]string{}
	for _, tb := range Tables {
		err := readTable(ctx, src, tb, func(r Row) error {
			for _, v := range r {
				for _, s := range valueStrings(v) {
					for _, id := range idToken.FindAllString(s, -1) {
						if _, ok := idmap[id]; !ok {
							idmap[id] = ids.New(id[:strings.IndexByte(id, '_')])
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			return ImportResult{}, err
		}
	}
	newProject, ok := idmap[a.m.ProjectID]
	if !ok {
		return ImportResult{}, fmt.Errorf("%w: the project row is missing", ErrBadArchive)
	}
	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = a.m.ProjectName
	}
	im := &ImportEnv{DataDir: t.DataDir, Human: t.Human, Now: o.Now, Source: filepath.Base(o.Archive), ProjectName: name, RepoPath: o.RepoPath, Branch: o.Branch}
	rep := newRemapper(idmap, t.DataDir)

	restored, err := a.restoreRuns(t.DataDir, idmap)
	if err != nil {
		return ImportResult{}, err
	}
	counts := map[string]int{}
	err = t.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		im.Stats = Stats{}
		for k := range counts {
			delete(counts, k)
		}
		for _, tb := range Tables {
			var stmt *sql.Stmt
			var stmtCols string
			err := readTable(ctx, src, tb, func(r Row) error {
				for k, v := range r {
					r[k] = rep.value(v)
				}
				if tb.Import != nil {
					tb.Import(r, im)
				}
				cols := make([]string, 0, len(r))
				for k := range r {
					cols = append(cols, k)
				}
				sort.Strings(cols)
				key := strings.Join(cols, ",")
				if stmt == nil || key != stmtCols {
					if stmt != nil {
						stmt.Close()
					}
					verb := "INSERT"
					if tb.IgnoreDup {
						verb = "INSERT OR IGNORE"
					}
					var err error
					stmt, err = tx.PrepareContext(ctx, fmt.Sprintf(`%s INTO "%s"(%s) VALUES(%s)`, verb, tb.Name, quoteCols(cols), strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")))
					if err != nil {
						return fmt.Errorf("import %s: %w", tb.Name, err)
					}
					stmtCols = key
				}
				args := make([]any, len(cols))
				for i, c := range cols {
					args[i] = r[c]
				}
				if _, err := stmt.ExecContext(ctx, args...); err != nil {
					return fmt.Errorf("import %s: %w", tb.Name, err)
				}
				counts[tb.Name]++
				return nil
			})
			if stmt != nil {
				stmt.Close()
			}
			if err != nil {
				return err
			}
		}
		return quietInbox(ctx, tx, newProject, o.Now)
	})
	if err != nil {
		for _, d := range restored.dirs {
			_ = os.RemoveAll(d)
		}
		return ImportResult{}, err
	}
	return ImportResult{ProjectID: newProject, Name: name, SourceID: a.m.ProjectID, Counts: counts, IDsRemapped: len(idmap), RunFiles: restored.files,
		Migrated: migrated, Pending: im.Stats, SourceSchema: a.m.SchemaVersion}, nil
}

// quietInbox dismisses the Needs-you items the imported history would otherwise raise (stage-2 watchdog trips, failed
// checks and blocking questions that were already history on the exporting machine).
func quietInbox(ctx context.Context, tx *sql.Tx, projectID string, now int64) error {
	stmts := []string{
		`INSERT OR IGNORE INTO room_inbox_acks(item_key,acked_by,acked_at)
		 SELECT 'watchdog:'||w.id,'system:import',? FROM watchdog_trips w WHERE w.stage=2 AND w.task_id IN (SELECT id FROM tasks WHERE project_id=?)`,
		`INSERT OR IGNORE INTO room_inbox_acks(item_key,acked_by,acked_at)
		 SELECT 'check:'||m.id,'system:import',? FROM messages m WHERE m.room_id IN (SELECT id FROM rooms WHERE project_id=?)
		   AND m.author LIKE 'tool:%' AND json_extract(m.payload,'$.check_status') IN ('fail','error')`,
		`INSERT OR IGNORE INTO room_inbox_acks(item_key,acked_by,acked_at)
		 SELECT 'question:'||m.id,'system:import',? FROM messages m WHERE m.room_id IN (SELECT id FROM rooms WHERE project_id=?)
		   AND m.type='QUESTION'`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s, now, projectID); err != nil {
			return err
		}
	}
	return nil
}

// ---- id and path remapping ----

// idToken matches the ids Drydock generates: a short lowercase prefix, an underscore and 12 hex digits.
var idToken = regexp.MustCompile(`\b[a-z]{1,6}_[0-9a-f]{12}\b`)

type remapper struct {
	ids     map[string]string
	dataRaw string
	dataEsc string
}

func newRemapper(m map[string]string, dataDir string) *remapper {
	esc, _ := json.Marshal(dataDir)
	return &remapper{ids: m, dataRaw: dataDir, dataEsc: strings.Trim(string(esc), `"`)}
}

func (r *remapper) str(s string) string {
	if strings.Contains(s, "$DATA") {
		s = strings.ReplaceAll(s, tokDataJSON, r.dataEsc)
		s = strings.ReplaceAll(s, tokData, r.dataRaw)
	}
	return idToken.ReplaceAllStringFunc(s, func(id string) string {
		if n, ok := r.ids[id]; ok {
			return n
		}
		return id
	})
}

func (r *remapper) value(v any) any {
	switch t := v.(type) {
	case string:
		return r.str(t)
	case []byte:
		if utf8.Valid(t) {
			return []byte(r.str(string(t)))
		}
	}
	return v
}

func valueStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []byte:
		if utf8.Valid(t) {
			return []string{string(t)}
		}
	}
	return nil
}

// readTable streams the rows of one exported table from a project.db. A table missing from an archive made by an older
// build is skipped.
func readTable(ctx context.Context, db *storage.DB, t Table, f func(Row) error) error {
	order := "rowid"
	q := fmt.Sprintf(`SELECT %s FROM "%s" ORDER BY %s`, quoteCols(t.Columns), t.Name, order)
	rows, err := db.Reader().QueryContext(ctx, q)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return fmt.Errorf("read %s: %w", t.Name, err)
	}
	defer rows.Close()
	vals := make([]any, len(t.Columns))
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		r := Row{}
		for i, c := range t.Columns {
			r[c] = vals[i]
		}
		for k, v := range t.Const {
			r[k] = v
		}
		if err := f(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ---- opening and validating an archive ----

type archive struct {
	zr    *zip.ReadCloser
	m     Manifest
	stage string
	files map[string]*zip.File
}

func (a *archive) close() {
	if a.zr != nil {
		a.zr.Close()
	}
	if a.stage != "" {
		os.RemoveAll(a.stage)
	}
}

// safeName reports whether an archive entry name is a plain relative slash path without traversal.
func safeName(n string) bool {
	if n == "" || len(n) > 512 || strings.ContainsAny(n, "\\:\x00") || strings.HasPrefix(n, "/") {
		return false
	}
	return path.Clean(n) == n && !strings.HasPrefix(n, "../") && n != ".."
}

func openArchive(ctx context.Context, p string) (*archive, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	a := &archive{zr: zr, files: map[string]*zip.File{}}
	ok := false
	defer func() {
		if !ok {
			a.close()
		}
	}()
	if len(zr.File) > maxEntries {
		return nil, fmt.Errorf("%w: too many entries", ErrBadArchive)
	}
	var total uint64
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		if !safeName(f.Name) {
			return nil, fmt.Errorf("%w: unsafe entry name %q", ErrBadArchive, f.Name)
		}
		if _, dup := a.files[f.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate entry %q", ErrBadArchive, f.Name)
		}
		total += f.UncompressedSize64
		a.files[f.Name] = f
	}
	if total > maxTotalBytes {
		return nil, fmt.Errorf("%w: unpacks to more than %d GiB", ErrBadArchive, maxTotalBytes>>30)
	}
	mf, found := a.files[ManifestName]
	if !found {
		return nil, fmt.Errorf("%w: manifest.json is missing", ErrBadArchive)
	}
	if mf.UncompressedSize64 > maxManifest {
		return nil, fmt.Errorf("%w: manifest is too large", ErrBadArchive)
	}
	rc, err := mf.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	b, err := io.ReadAll(io.LimitReader(rc, maxManifest+1))
	rc.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	if err := json.Unmarshal(b, &a.m); err != nil {
		return nil, fmt.Errorf("%w: manifest: %v", ErrBadArchive, err)
	}
	if a.m.Kind != ArchiveKind {
		return nil, fmt.Errorf("%w: this is not a project archive", ErrBadArchive)
	}
	if a.m.Format > FormatVersion {
		return nil, ErrNewerFormat
	}
	if a.m.Format < 1 {
		return nil, fmt.Errorf("%w: unknown format", ErrBadArchive)
	}
	// the refusal of a newer schema must not depend on opening the database, and comes before anything is extracted
	if a.m.SchemaVersion > storage.LatestVersion() {
		return nil, ErrNewerSchema
	}
	listed := map[string]bool{}
	a.stage, err = os.MkdirTemp("", "drydock-import-*")
	if err != nil {
		return nil, err
	}
	for _, f := range a.m.Files {
		if !safeName(f.Path) || f.Path == ManifestName || listed[f.Path] {
			return nil, fmt.Errorf("%w: bad file list entry %q", ErrBadArchive, f.Path)
		}
		listed[f.Path] = true
		zf, present := a.files[f.Path]
		if !present {
			return nil, fmt.Errorf("%w: %s is listed in the manifest but missing from the archive", ErrBadArchive, f.Path)
		}
		if err := a.verify(zf, f); err != nil {
			return nil, err
		}
	}
	for n := range a.files {
		if n != ManifestName && !listed[n] {
			return nil, fmt.Errorf("%w: %s is in the archive but not in the manifest", ErrBadArchive, n)
		}
	}
	if !listed[DBName] {
		return nil, fmt.Errorf("%w: project.db is missing", ErrBadArchive)
	}
	ok = true
	return a, nil
}

// verify streams one entry through SHA-256, bounded by the size the manifest declares; project.db is also kept in the stage.
func (a *archive) verify(zf *zip.File, want FileEntry) error {
	rc, err := zf.Open()
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrBadArchive, want.Path, err)
	}
	defer rc.Close()
	h := sha256.New()
	var w io.Writer = h
	var out *os.File
	if want.Path == DBName {
		if out, err = os.Create(filepath.Join(a.stage, DBName)); err != nil {
			return err
		}
		defer out.Close()
		w = io.MultiWriter(h, out)
	}
	n, err := io.Copy(w, io.LimitReader(rc, want.Size+1))
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrBadArchive, want.Path, err)
	}
	if n != want.Size || hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
		return fmt.Errorf("%w: %s does not match its SHA-256 in the manifest (the file was modified or corrupted)", ErrBadArchive, want.Path)
	}
	return nil
}

// openDB opens the extracted project.db after checking its real schema version against the manifest. A database made by
// an older build is migrated forward in the extracted copy (the archive is never modified).
func (a *archive) openDB(ctx context.Context) (*storage.DB, bool, error) {
	p := filepath.Join(a.stage, DBName)
	v, err := storage.FileSchemaVersion(ctx, p)
	if err != nil {
		return nil, false, fmt.Errorf("%w: project.db: %v", ErrBadArchive, err)
	}
	if v > storage.LatestVersion() {
		return nil, false, ErrNewerSchema
	}
	if v != a.m.SchemaVersion {
		return nil, false, fmt.Errorf("%w: project.db is at schema %d but the manifest says %d", ErrBadArchive, v, a.m.SchemaVersion)
	}
	db, err := storage.Open(ctx, p)
	if err != nil {
		return nil, false, fmt.Errorf("%w: project.db: %v", ErrBadArchive, err)
	}
	return db, v < storage.LatestVersion(), nil
}

type restoredRuns struct {
	dirs  []string
	files int
}

// restoreRuns writes the archive's raw run output under <data>/runs/<new run id>/. Existing files are never overwritten.
func (a *archive) restoreRuns(dataDir string, idmap map[string]string) (restoredRuns, error) {
	var out restoredRuns
	dirSeen := map[string]bool{}
	cleanup := func() {
		for _, d := range out.dirs {
			_ = os.RemoveAll(d)
		}
	}
	for _, f := range a.m.Files {
		parts := strings.Split(f.Path, "/")
		if len(parts) < 3 || parts[0] != "runs" {
			continue
		}
		nid, ok := idmap[parts[1]]
		if !ok || strings.ContainsAny(nid, `/\`) {
			continue
		}
		rel := append([]string{nid}, parts[2:]...)
		dst := filepath.Join(append([]string{dataDir, "runs"}, rel...)...)
		root := filepath.Join(dataDir, "runs", nid)
		if !strings.HasPrefix(dst, root+string(filepath.Separator)) {
			cleanup()
			return restoredRuns{}, fmt.Errorf("%w: unsafe run path %q", ErrBadArchive, f.Path)
		}
		if !dirSeen[root] {
			if _, err := os.Stat(root); err == nil {
				cleanup()
				return restoredRuns{}, fmt.Errorf("projectio: %s already exists; not overwriting", root)
			}
			dirSeen[root] = true
			out.dirs = append(out.dirs, root)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			cleanup()
			return restoredRuns{}, err
		}
		rc, err := a.files[f.Path].Open()
		if err != nil {
			cleanup()
			return restoredRuns{}, err
		}
		w, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			rc.Close()
			cleanup()
			return restoredRuns{}, err
		}
		_, err = io.Copy(w, io.LimitReader(rc, f.Size+1))
		rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			cleanup()
			return restoredRuns{}, err
		}
		out.files++
	}
	return out, nil
}
