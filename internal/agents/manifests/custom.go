package manifests

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents"
)

// A person's own CLIs (decision P115): manifests kept in <app data>/adapters/custom/<id>.yaml, one file per CLI, in the schema
// of the embedded template (cli/custom.yaml). They are validated more strictly than the embedded ones: a custom manifest only
// (meta.custom, the headless contract, a dialect, no Go parser, no ACP), an id that is not a built-in adapter's and that names
// the file, and an acknowledgement of its own (gate id custom-<id>), because each one runs a program the person chose.

// MaxCustomBytes is the largest manifest file read.
const MaxCustomBytes = 64 << 10

// UserGateID is the acknowledgement gate a person's CLI must carry.
func UserGateID(id string) string { return "custom-" + id }

// ParseUserCustom parses and validates a person's own manifest.
func ParseUserCustom(data []byte) (*Manifest, error) {
	if len(data) > MaxCustomBytes {
		return nil, fmt.Errorf("the manifest is larger than %d KiB", MaxCustomBytes>>10)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, err
	}
	var p []string
	if !m.Meta.Custom {
		p = append(p, "meta.custom must be true: only custom manifests can be added")
	}
	if m.Meta.ID == TemplateID {
		p = append(p, "meta.id must be your CLI's own id, not the template's")
	}
	if m.Gate == nil || m.Gate.ID != UserGateID(m.Meta.ID) {
		p = append(p, fmt.Sprintf("gate.id must be %s (each custom CLI is acknowledged on its own)", UserGateID(m.Meta.ID)))
	}
	if m.Gate != nil && strings.TrimSpace(m.Gate.Text) == "" {
		p = append(p, "gate.text must say what the acknowledgement is about")
	}
	if len(p) > 0 {
		return nil, &ValidationError{ID: m.Meta.ID, Problems: p}
	}
	return m, nil
}

// CustomTemplate is the embedded template's text, to start a person's own manifest from.
func CustomTemplate() ([]byte, error) { return fs.ReadFile(embedded, "cli/"+TemplateID+".yaml") }

// CustomFile is one file of the custom folder as the loader found it.
type CustomFile struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Error string `json:"error,omitempty"` // why it was not loaded
	// SHA256 is the hash of the bytes read, when they loaded: what an acknowledgement of this CLI is bound to (decision P117).
	SHA256 string `json:"sha256,omitempty"`
}

// CustomSum is the SHA-256 (hex) of a custom manifest file's bytes.
func CustomSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// LoadUserCustom reads every *.yaml directly in dir (missing dir: none). A file that does not validate, or whose name is not
// its id, is reported and not loaded.
func LoadUserCustom(dir string) ([]*Manifest, []CustomFile) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var ms []*Manifest
	var files []CustomFile
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		f := CustomFile{ID: strings.TrimSuffix(e.Name(), ".yaml"), Path: filepath.Join(dir, e.Name())}
		info, err := e.Info()
		switch {
		case err != nil:
			f.Error = err.Error()
		case !info.Mode().IsRegular():
			f.Error = "not a regular file"
		case info.Size() > MaxCustomBytes:
			f.Error = fmt.Sprintf("larger than %d KiB", MaxCustomBytes>>10)
		}
		if f.Error == "" {
			b, err := os.ReadFile(f.Path)
			if err != nil {
				f.Error = err.Error()
			} else if m, err := ParseUserCustom(b); err != nil {
				f.Error = err.Error()
			} else if m.Meta.ID != f.ID {
				f.Error = fmt.Sprintf("meta.id %q must match the file name", m.Meta.ID)
			} else {
				f.SHA256 = CustomSum(b)
				ms = append(ms, m)
			}
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ID < files[j].ID })
	return ms, files
}

// ErrBuiltinID refuses a custom CLI whose id is a built-in adapter's.
var ErrBuiltinID = errors.New("this id belongs to a built-in adapter")

// AddCustom puts a person's validated manifest in the set and builds its adapter. builtin says which ids are the embedded
// manifests' (they can never be replaced).
func (r *Registry) AddCustom(m *Manifest, builtin func(id string) bool) (agents.Adapter, error) {
	if builtin(m.Meta.ID) {
		return nil, fmt.Errorf("%q: %w", m.Meta.ID, ErrBuiltinID)
	}
	if err := r.Set.Add(m); err != nil {
		return nil, err
	}
	a, err := r.Build(m.Meta.ID)
	if err != nil {
		r.Set.Remove(m.Meta.ID)
		return nil, err
	}
	return a, nil
}

// RemoveCustom takes a person's CLI out of the set (a built-in id is refused).
func (r *Registry) RemoveCustom(id string, builtin func(id string) bool) error {
	if builtin(id) {
		return fmt.Errorf("%q: %w", id, ErrBuiltinID)
	}
	m, ok := r.Set.Get(id)
	if !ok || !m.Meta.Custom {
		return fmt.Errorf("no custom CLI %q", id)
	}
	r.Set.Remove(id)
	return nil
}
