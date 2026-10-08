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
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/scrub"
	"github.com/kjmaguire/drydock/internal/storage"
)

// FormatVersion is the archive format this build writes and the newest it reads.
const FormatVersion = 1

// ArchiveKind marks the manifest.
const ArchiveKind = "drydock-project"

// Archive file names.
const (
	ManifestName = "manifest.json"
	DBName       = "project.db"
)

// Data directory tokens: the exporting machine's data directory is replaced by these so paths inside the archive mean
// something on the importing machine ($DATAJSON$ is the form inside JSON strings, where backslashes are doubled).
const (
	tokData     = "$DATA$"
	tokDataJSON = "$DATAJSON$"
)

// FileEntry is one archive file in the manifest.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Excluded names a file that was left out and why.
type Excluded struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ScanReport is the secret-scan result for what the archive contains. Findings never carry the secret text.
type ScanReport struct {
	DBRedactions     int            `json:"dbRedactions"`
	DBByRule         map[string]int `json:"dbByRule,omitempty"`
	RunsFilesScanned int            `json:"runsFilesScanned"`
	RunsRedactions   int            `json:"runsRedactions"`
	RunsByRule       map[string]int `json:"runsByRule,omitempty"`
	RunsExcluded     []Excluded     `json:"runsExcluded,omitempty"`
}

// Manifest describes an archive.
type Manifest struct {
	Format        int            `json:"format"`
	Kind          string         `json:"kind"`
	AppVersion    string         `json:"appVersion"`
	SchemaVersion int64          `json:"schemaVersion"`
	ProjectID     string         `json:"projectId"`
	ProjectName   string         `json:"projectName"`
	Created       int64          `json:"created"` // ms
	IncludesRuns  bool           `json:"includesRuns"`
	Files         []FileEntry    `json:"files"`
	Tables        map[string]int `json:"tables"`
	Scan          ScanReport     `json:"scan"`
	// Never lists what is not in the archive, so a reader sees the promise next to the content.
	NeverIncluded []string `json:"neverIncluded"`
}

// NeverIncluded is written into every manifest.
var neverIncluded = []string{"secrets and Credential Manager entries", "encrypted secret blobs", "agent CLI credentials", "provider API keys",
	"worktree contents", "the repository", "vendor Connections"}

// Source is the live installation an export reads.
type Source struct {
	DB         *storage.DB
	DataDir    string
	Scanner    scrub.Scanner
	AppVersion string
	// Profile is the user profile directory; paths under it are replaced by a token.
	Profile string
	Now     func() time.Time
}

// ExportOptions selects what to export.
type ExportOptions struct {
	ProjectID string
	// Dest is the archive path to write (Export only).
	Dest string
	// IncludeRuns adds the raw run output (runs/<run>/...) after scanning it for secrets. Off by default: raw output can
	// contain source code.
	IncludeRuns bool
}

// Result is what Export and Preview return.
type Result struct {
	Path     string   `json:"path,omitempty"`
	Size     int64    `json:"size"`
	Manifest Manifest `json:"manifest"`
}

// ErrNoProject is returned for an unknown project id.
var ErrNoProject = errors.New("projectio: no such project")

// Export writes the project archive.
func (s Source) Export(ctx context.Context, o ExportOptions) (Result, error) {
	if o.Dest == "" {
		return Result{}, errors.New("projectio: Dest is required")
	}
	stage, err := os.MkdirTemp("", "drydock-export-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	m, err := s.stage(ctx, o, stage)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(o.Dest), 0o755); err != nil {
		return Result{}, err
	}
	tmp := o.Dest + ".partial"
	if err := writeZip(tmp, stage, m); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	if err := os.Rename(tmp, o.Dest); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	st, err := os.Stat(o.Dest)
	if err != nil {
		return Result{}, err
	}
	return Result{Path: o.Dest, Size: st.Size(), Manifest: m}, nil
}

// Preview builds exactly what Export would write, reports the file list and the secret-scan result, and writes nothing
// that outlives the call.
func (s Source) Preview(ctx context.Context, o ExportOptions) (Result, error) {
	stage, err := os.MkdirTemp("", "drydock-export-preview-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	m, err := s.stage(ctx, o, stage)
	if err != nil {
		return Result{}, err
	}
	var total int64
	for _, f := range m.Files {
		total += f.Size
	}
	return Result{Size: total, Manifest: m}, nil
}

type exporter struct {
	s     Source
	ctx   context.Context
	opt   ExportOptions
	env   *ExportEnv
	tok   *strings.Replacer
	ident *scrub.Identity
	scan  ScanReport
	files []string // staged archive paths in order
	exp   map[string]bool
}

func (s Source) newExporter(ctx context.Context, o ExportOptions) *exporter {
	x := &exporter{s: s, ctx: ctx, opt: o, env: &ExportEnv{DataDir: s.DataDir, IncludeRuns: o.IncludeRuns}, exp: map[string]bool{}}
	x.scan.DBByRule, x.scan.RunsByRule = map[string]int{}, map[string]int{}
	if s.DataDir != "" {
		raw := s.DataDir
		esc := strings.ReplaceAll(raw, `\`, `\\`)
		pairs := []string{}
		if esc != raw {
			pairs = append(pairs, esc, tokDataJSON)
		}
		pairs = append(pairs, raw, tokData)
		if sl := filepath.ToSlash(raw); sl != raw {
			pairs = append(pairs, sl, tokData)
		}
		x.tok = strings.NewReplacer(pairs...)
	}
	if s.Profile != "" {
		x.ident = scrub.NewIdentity(s.Profile, "", "")
	}
	return x
}

// stage builds every archive file under dir and returns the manifest (without writing the zip).
func (s Source) stage(ctx context.Context, o ExportOptions, dir string) (Manifest, error) {
	x := s.newExporter(ctx, o)
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	tx, err := s.DB.Reader().BeginTx(ctx, nil) // one snapshot for every table
	if err != nil {
		return Manifest{}, err
	}
	defer tx.Rollback()

	var name string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM projects WHERE id=?`, o.ProjectID).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Manifest{}, fmt.Errorf("%w: %s", ErrNoProject, o.ProjectID)
		}
		return Manifest{}, err
	}
	dbPath := filepath.Join(dir, DBName)
	dest, err := storage.Open(ctx, dbPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("create project.db: %w", err)
	}
	counts := map[string]int{}
	for _, t := range Tables {
		n, err := x.exportTable(tx, dest, dir, t)
		if err != nil {
			dest.Close()
			return Manifest{}, err
		}
		counts[t.Name] = n
	}
	ver, err := dest.SchemaVersion(ctx)
	if err != nil {
		dest.Close()
		return Manifest{}, err
	}
	// a single self-contained file: no write-ahead log travels beside it
	if _, err := dest.Writer().ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		dest.Close()
		return Manifest{}, err
	}
	if err := dest.Close(); err != nil {
		return Manifest{}, err
	}
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	if o.IncludeRuns {
		ids, err := queryStrings(ctx, tx, `SELECT id FROM workflow_runs WHERE id IN `+runIDs, sql.Named("p", o.ProjectID))
		if err != nil {
			return Manifest{}, err
		}
		if err := x.stageRuns(dir, ids); err != nil {
			return Manifest{}, err
		}
	}
	m := Manifest{Format: FormatVersion, Kind: ArchiveKind, AppVersion: s.AppVersion, SchemaVersion: ver, ProjectID: o.ProjectID, ProjectName: name,
		Created: now().UTC().UnixMilli(), IncludesRuns: o.IncludeRuns, Tables: counts, Scan: x.scan, NeverIncluded: neverIncluded}
	paths := append([]string{DBName}, x.files...)
	for _, p := range paths {
		e, err := hashStaged(dir, p)
		if err != nil {
			return Manifest{}, err
		}
		m.Files = append(m.Files, e)
	}
	if len(m.Scan.DBByRule) == 0 {
		m.Scan.DBByRule = nil
	}
	if len(m.Scan.RunsByRule) == 0 {
		m.Scan.RunsByRule = nil
	}
	return m, nil
}

func queryStrings(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func quoteCols(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = `"` + c + `"`
	}
	return strings.Join(q, ",")
}

// exportTable copies one table's scoped rows into the new database, scrubbing every text value.
func (x *exporter) exportTable(tx *sql.Tx, dest *storage.DB, dir string, t Table) (int, error) {
	order := t.OrderBy
	if order == "" {
		order = "rowid"
	}
	q := fmt.Sprintf(`SELECT %s FROM "%s" WHERE (%s) ORDER BY %s`, quoteCols(t.Columns), t.Name, t.Where, order)
	rows, err := tx.QueryContext(x.ctx, q, sql.Named("p", x.opt.ProjectID))
	if err != nil {
		return 0, fmt.Errorf("export %s: %w", t.Name, err)
	}
	defer rows.Close()

	insCols := append([]string(nil), t.Columns...)
	var constKeys []string
	for k := range t.Const {
		constKeys = append(constKeys, k)
	}
	sort.Strings(constKeys)
	insCols = append(insCols, constKeys...)
	ph := strings.TrimSuffix(strings.Repeat("?,", len(insCols)), ",")
	ins := fmt.Sprintf(`INSERT INTO "%s"(%s) VALUES(%s)`, t.Name, quoteCols(insCols), ph)

	n := 0
	err = dest.WriteTx(x.ctx, func(wtx *sql.Tx) error {
		n = 0
		stmt, err := wtx.PrepareContext(x.ctx, ins)
		if err != nil {
			return err
		}
		defer stmt.Close()
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
			if t.Export != nil {
				t.Export(r, x.env)
			}
			args := make([]any, 0, len(insCols))
			for _, c := range t.Columns {
				v, err := x.clean(t.Name, c, r[c])
				if err != nil {
					return err
				}
				r[c] = v
				args = append(args, v)
			}
			for _, k := range constKeys {
				args = append(args, t.Const[k])
			}
			if _, err := stmt.ExecContext(x.ctx, args...); err != nil {
				return fmt.Errorf("export %s: %w", t.Name, err)
			}
			if t.Name == "room_exports" {
				if err := x.stageRoomExport(dir, r); err != nil {
					return err
				}
			}
			n++
		}
		return rows.Err()
	})
	return n, err
}

// clean tokenizes local paths and removes secrets from one value; it fails closed when a value cannot be scanned.
func (x *exporter) clean(table, col string, v any) (any, error) {
	var s string
	bytesVal := false
	switch t := v.(type) {
	case string:
		s = t
	case []byte:
		if !utf8.Valid(t) {
			return nil, fmt.Errorf("export %s.%s: unexpected binary data; refusing to export a value that cannot be scanned", table, col)
		}
		s, bytesVal = string(t), true
	default:
		return v, nil
	}
	if s == "" {
		return v, nil
	}
	if x.tok != nil {
		s = x.tok.Replace(s)
	}
	s = x.ident.Apply(s)
	if len(s) >= 16 {
		out, fs, err := scrub.Text(x.ctx, x.s.Scanner, s)
		if err != nil {
			return nil, fmt.Errorf("export %s.%s: the secret scan failed, so the value is not exported: %w", table, col, err)
		}
		if len(fs) > 0 {
			x.scan.DBRedactions += len(fs)
			for _, f := range fs {
				x.scan.DBByRule[f.RuleID]++
			}
			s = out
		}
	}
	if bytesVal {
		return []byte(s), nil
	}
	return s, nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// stageRoomExport writes an immutable minutes or PR-description export as a Markdown file for people to read.
func (x *exporter) stageRoomExport(dir string, r Row) error {
	name := "exports/" + unsafeName.ReplaceAllString(str(r, "kind")+"-"+str(r, "id"), "_") + ".md"
	if x.exp[name] {
		return nil
	}
	x.exp[name] = true
	if err := writeStaged(dir, name, []byte(str(r, "content"))); err != nil {
		return err
	}
	x.files = append(x.files, name)
	return nil
}

func writeStaged(dir, rel string, b []byte) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// maxScanFile is the largest raw-output file that is scanned; bigger files are left out rather than shipped unscanned.
const maxScanFile = 8<<20 - 1024

// stageRuns copies the raw output of the project's runs after scanning every file; findings are redacted, and a file that
// cannot be scanned (binary, too large, read error) is left out and listed.
func (x *exporter) stageRuns(dir string, runIDs []string) error {
	sort.Strings(runIDs)
	for _, id := range runIDs {
		if strings.ContainsAny(id, `/\`) || id == "" || id == "." || id == ".." {
			continue
		}
		root := filepath.Join(x.s.DataDir, "runs", id)
		if st, err := os.Lstat(root); err != nil || !st.IsDir() {
			continue
		}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(x.s.DataDir, p)
			arch := filepath.ToSlash(rel)
			if !d.Type().IsRegular() {
				x.excludeRun(arch, "not a regular file")
				return nil
			}
			info, err := d.Info()
			if err != nil {
				x.excludeRun(arch, "unreadable")
				return nil
			}
			if info.Size() > maxScanFile {
				x.excludeRun(arch, fmt.Sprintf("larger than the %d MiB scan limit", maxScanFile>>20))
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				x.excludeRun(arch, "unreadable")
				return nil
			}
			if !utf8.Valid(b) || strings.IndexByte(string(b[:min(len(b), 8192)]), 0) >= 0 {
				x.excludeRun(arch, "binary file; it cannot be scanned for secrets")
				return nil
			}
			s := x.ident.Apply(string(b))
			out, fs, err := scrub.Text(x.ctx, x.s.Scanner, s)
			if err != nil {
				x.excludeRun(arch, "the secret scan failed: "+err.Error())
				return nil
			}
			x.scan.RunsFilesScanned++
			if len(fs) > 0 {
				x.scan.RunsRedactions += len(fs)
				for _, f := range fs {
					x.scan.RunsByRule[f.RuleID]++
				}
			}
			if err := writeStaged(dir, arch, []byte(out)); err != nil {
				return err
			}
			x.files = append(x.files, arch)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (x *exporter) excludeRun(p, why string) {
	x.scan.RunsExcluded = append(x.scan.RunsExcluded, Excluded{Path: p, Reason: why})
}

func hashStaged(dir, rel string) (FileEntry, error) {
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return FileEntry{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return FileEntry{}, err
	}
	return FileEntry{Path: rel, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// writeZip writes the manifest first, then every listed file.
func writeZip(dest, dir string, m Manifest) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	fail := func(err error) error {
		zw.Close()
		out.Close()
		return err
	}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fail(err)
	}
	w, err := zw.Create(ManifestName)
	if err != nil {
		return fail(err)
	}
	if _, err := w.Write(mb); err != nil {
		return fail(err)
	}
	for _, f := range m.Files {
		hdr := &zip.FileHeader{Name: f.Path, Method: zip.Deflate}
		hdr.Modified = time.UnixMilli(m.Created).UTC()
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return fail(err)
		}
		src, err := os.Open(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			return fail(err)
		}
		_, err = io.Copy(w, src)
		src.Close()
		if err != nil {
			return fail(err)
		}
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
