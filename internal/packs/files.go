package packs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/docparse"
	"github.com/kjmaguire/drydock/internal/security"
	"gopkg.in/yaml.v3"
)

// A pack made from a person's own files (decision P110): the files are kept in packs/<id>/files and the pack's one source is
// `{type: local_dir, path: files}`, so a refresh reads them like any other folder (Markdown, text, HTML, PDF, DOCX). The pack
// page adds and removes files; nothing else writes there.

// FilesDir is the folder of a pack's own files, relative to the pack's folder.
const FilesDir = "files"

// EventFilesChanged is published when a person adds or removes a pack's files.
const EventFilesChanged = "pack.files_changed"

// Bounds for a pack's files.
const (
	MaxUploadFiles     = 50        // in one call
	MaxPackFilesBytes  = 512 << 20 // all of a pack's files together
	maxUploadNameBytes = 120
)

var windowsDevice = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

var uploadExts = map[string]bool{".md": true, ".markdown": true, ".mdx": true, ".txt": true, ".html": true, ".htm": true, ".pdf": true, ".docx": true}

// UploadFile is one file a person adds to a pack.
type UploadFile struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

// PackFile is one of a pack's own files.
type PackFile struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// NewFilesPack is what a pack made from files is called and who may see what it holds.
type NewFilesPack struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Visibility string `json:"visibility"` // private (the default) or public
	License    string `json:"license"`    // SPDX id; a public pack needs one ("unknown" is accepted)
}

// FilesManifest is the pack.yaml of a pack made from files.
func FilesManifest(n NewFilesPack) ([]byte, error) {
	vis := firstNonEmpty(n.Visibility, VisibilityPrivate)
	type src struct {
		Type string `yaml:"type"`
		Path string `yaml:"path"`
	}
	doc := map[string]any{
		"schema": SchemaVersion, "id": strings.TrimSpace(n.ID), "title": strings.TrimSpace(n.Title), "kind": KindIndexed, "visibility": vis,
		"sources": []src{{Type: SourceLocalDir, Path: FilesDir}},
	}
	if lic := strings.TrimSpace(n.License); lic != "" {
		doc["license"] = map[string]string{"content": lic}
	} else if vis == VisibilityPublic {
		doc["license"] = map[string]string{"content": "unknown"}
	}
	return yaml.Marshal(doc)
}

// CreateFromFiles installs a pack whose source is its own files folder, then adds the files. The first refresh is left to
// the caller (the page starts it), as with any install. Human only.
func (s *Store) CreateFromFiles(ctx context.Context, by string, n NewFilesPack, files []UploadFile) (Pack, error) {
	if err := security.RequireHuman(by); err != nil {
		return Pack{}, err
	}
	if len(files) == 0 {
		return Pack{}, errors.New("packs: choose at least one file")
	}
	if _, err := s.Get(ctx, strings.TrimSpace(n.ID)); err == nil {
		return Pack{}, fmt.Errorf("packs: a pack called %s exists already; add the files to it instead", strings.TrimSpace(n.ID))
	}
	if err := checkUploads(files); err != nil {
		return Pack{}, err
	}
	y, err := FilesManifest(n)
	if err != nil {
		return Pack{}, err
	}
	p, err := s.Install(ctx, by, y)
	if err != nil {
		return Pack{}, err
	}
	if _, err := s.AddFiles(ctx, by, p.ID, files); err != nil {
		_ = s.Remove(ctx, by, p.ID) // a pack without its files is not what was asked for
		return Pack{}, err
	}
	return s.Get(ctx, p.ID)
}

// filesDir is the pack's own files folder, or an error when the pack does not read one.
func (s *Store) filesDir(ctx context.Context, packID string) (string, error) {
	_, m, err := s.loadRow(ctx, packID)
	if err != nil {
		return "", err
	}
	for _, src := range m.Sources {
		if src.Type == SourceLocalDir && filepath.Clean(filepath.FromSlash(src.Path)) == FilesDir {
			return filepath.Join(s.o.Dir, packID, FilesDir), nil
		}
	}
	return "", fmt.Errorf("packs: %s does not read its own files folder (its manifest names no local_dir source with path %q)", packID, FilesDir)
}

// Files lists a pack's own files. A pack without a files folder has none.
func (s *Store) Files(ctx context.Context, packID string) ([]PackFile, error) {
	dir, err := s.filesDir(ctx, packID)
	if err != nil {
		return []PackFile{}, nil
	}
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []PackFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []PackFile{}
	for _, e := range ents {
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, PackFile{Name: e.Name(), Size: info.Size(), Modified: info.ModTime().UTC()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AddFiles writes files into the pack's files folder; a file of the same name is replaced. The next refresh indexes them.
// Every file is checked before any is written: its name (the last element only), its kind (by extension, and for a PDF or a
// DOCX by its first bytes), its size, and the folder's total. Human only.
func (s *Store) AddFiles(ctx context.Context, by, packID string, files []UploadFile) ([]PackFile, error) {
	if err := security.RequireHuman(by); err != nil {
		return nil, err
	}
	dir, err := s.filesDir(ctx, packID)
	if err != nil {
		return nil, err
	}
	if err := checkUploads(files); err != nil {
		return nil, err
	}
	existing, err := s.Files(ctx, packID)
	if err != nil {
		return nil, err
	}
	sizes := map[string]int64{}
	var total int64
	for _, f := range existing {
		sizes[f.Name] = f.Size
		total += f.Size
	}
	for _, f := range files {
		name := uploadName(f.Name)
		total += int64(len(f.Data)) - sizes[name]
		sizes[name] = int64(len(f.Data))
	}
	if total > MaxPackFilesBytes {
		return nil, fmt.Errorf("packs: a pack's files may hold %d MB in all; these would make %d MB", MaxPackFilesBytes>>20, total>>20)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var names []string
	for _, f := range files {
		name := uploadName(f.Name)
		tmp, err := os.CreateTemp(dir, ".upload-*")
		if err != nil {
			return nil, err
		}
		_, werr := tmp.Write(f.Data)
		cerr := tmp.Close()
		if werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = os.Rename(tmp.Name(), filepath.Join(dir, name))
		}
		if werr != nil {
			_ = os.Remove(tmp.Name())
			return nil, werr
		}
		names = append(names, name)
	}
	s.emit(ctx, EventFilesChanged, map[string]any{"pack": packID, "added": names, "by": by})
	return s.Files(ctx, packID)
}

// RemoveFile deletes one of a pack's files; the next refresh retires its passages. Human only.
func (s *Store) RemoveFile(ctx context.Context, by, packID, name string) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	dir, err := s.filesDir(ctx, packID)
	if err != nil {
		return err
	}
	clean := uploadName(name)
	if clean != name {
		return fmt.Errorf("packs: %q is not one of the pack's files", name)
	}
	if err := os.Remove(filepath.Join(dir, clean)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("packs: %q is not one of the pack's files", name)
		}
		return err
	}
	s.emit(ctx, EventFilesChanged, map[string]any{"pack": packID, "removed": []string{clean}, "by": by})
	return nil
}

func checkUploads(files []UploadFile) error {
	if len(files) > MaxUploadFiles {
		return fmt.Errorf("packs: at most %d files at a time", MaxUploadFiles)
	}
	seen := map[string]bool{}
	for _, f := range files {
		name := uploadName(f.Name)
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("packs: %s is chosen twice", name)
		}
		seen[strings.ToLower(name)] = true
		ext := strings.ToLower(filepath.Ext(name))
		if !uploadExts[ext] {
			return fmt.Errorf("packs: %s is not a document a pack reads (Markdown, text, HTML, PDF or DOCX)", name)
		}
		if len(f.Data) == 0 {
			return fmt.Errorf("packs: %s is empty", name)
		}
		switch ext {
		case ".pdf", ".docx":
			if int64(len(f.Data)) > docparse.DefaultMaxBytes {
				return fmt.Errorf("packs: %s is %d MB; a PDF or DOCX may be %d MB", name, len(f.Data)>>20, docparse.DefaultMaxBytes>>20)
			}
			if kind := docparse.KindOf(name, f.Data); kind != strings.TrimPrefix(ext, ".") {
				return fmt.Errorf("packs: %s is not a %s file inside", name, strings.ToUpper(strings.TrimPrefix(ext, ".")))
			}
		default:
			if len(f.Data) > maxPageBytesCap {
				return fmt.Errorf("packs: %s is %d MB; a text document may be %d MB", name, len(f.Data)>>20, maxPageBytesCap>>20)
			}
			if bytes.IndexByte(f.Data, 0) >= 0 || !utf8.Valid(f.Data) {
				return fmt.Errorf("packs: %s is not text", name)
			}
		}
	}
	return nil
}

// uploadName keeps the last path element of a file name, without control characters or characters Windows refuses in a
// name, never a Windows device name, and at most 120 bytes; a name that is left empty becomes "file".
func uploadName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, `\`, "/"))
	n = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, n)
	n = strings.TrimSpace(strings.TrimLeft(n, "."))
	n = strings.TrimRight(n, ". ")
	if n == "" {
		n = "file"
	}
	if stem := strings.ToUpper(strings.TrimSuffix(n, filepath.Ext(n))); windowsDevice[stem] {
		n = "_" + n // CON.pdf would open the console on Windows, not a file
	}
	for len(n) > maxUploadNameBytes {
		ext := filepath.Ext(n)
		base := strings.TrimSuffix(n, ext)
		_, size := utf8.DecodeLastRuneInString(base)
		if size == 0 {
			n = n[:maxUploadNameBytes]
			break
		}
		n = base[:len(base)-size] + ext
	}
	return n
}
