package localllm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Model is a model on this PC: a repository's variant, downloaded and checked.
type Model struct {
	// ID names it everywhere, llama.cpp style: "owner/repo:QUANT".
	ID           string    `json:"id"`
	Repo         string    `json:"repo"`
	Variant      string    `json:"variant"`
	Files        []string  `json:"files"` // full paths, the first is the one llama-server loads
	Size         uint64    `json:"sizeBytes"`
	DownloadedAt time.Time `json:"downloadedAt"`
	Fit          *Fit      `json:"fit,omitempty"`
}

// ModelID is "owner/repo:VARIANT".
func ModelID(repo, variant string) string { return repo + ":" + variant }

// SplitModelID is the inverse of ModelID.
func SplitModelID(id string) (repo, variant string, ok bool) {
	repo, variant, ok = strings.Cut(id, ":")
	return repo, variant, ok && ValidRepo(repo) && variant != "" && !strings.ContainsAny(variant, `/\`)
}

// Store keeps downloaded models under Dir/<owner>/<repo>/<variant>/ with a model.json beside the files.
type Store struct {
	Dir string
}

type manifest struct {
	Repo         string    `json:"repo"`
	Variant      string    `json:"variant"`
	Files        []string  `json:"files"` // base names
	Size         uint64    `json:"sizeBytes"`
	DownloadedAt time.Time `json:"downloadedAt"`
}

// Dir is where a variant's files live.
func (s *Store) VariantDir(repo, variant string) string {
	return filepath.Join(s.Dir, filepath.FromSlash(repo), safeName(variant))
}

// Put records a downloaded variant (its files are already in VariantDir).
func (s *Store) Put(repo string, v Variant) (Model, error) {
	dir := s.VariantDir(repo, v.Name)
	m := manifest{Repo: repo, Variant: v.Name, Size: v.Size, DownloadedAt: time.Now().UTC()}
	for _, f := range v.Files {
		m.Files = append(m.Files, path.Base(f.Path))
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "model.json"), b, 0o644); err != nil {
		return Model{}, err
	}
	return m.model(dir), nil
}

func (m manifest) model(dir string) Model {
	out := Model{ID: ModelID(m.Repo, m.Variant), Repo: m.Repo, Variant: m.Variant, Size: m.Size, DownloadedAt: m.DownloadedAt}
	for _, f := range m.Files {
		out.Files = append(out.Files, filepath.Join(dir, f))
	}
	return out
}

// List is every downloaded model whose files are all there, newest first.
func (s *Store) List() []Model {
	var out []Model
	_ = filepath.WalkDir(s.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "model.json" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		var m manifest
		if json.Unmarshal(b, &m) != nil || !ValidRepo(m.Repo) || m.Variant == "" || len(m.Files) == 0 {
			return nil
		}
		model := m.model(filepath.Dir(p))
		for _, f := range model.Files {
			if st, err := os.Stat(f); err != nil || !st.Mode().IsRegular() {
				return nil
			}
		}
		out = append(out, model)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].DownloadedAt.After(out[j].DownloadedAt) })
	if out == nil {
		out = []Model{}
	}
	return out
}

// Get finds one downloaded model.
func (s *Store) Get(id string) (Model, error) {
	for _, m := range s.List() {
		if m.ID == id {
			return m, nil
		}
	}
	return Model{}, fmt.Errorf("%w: %s", ErrNotDownloaded, id)
}

// Remove deletes a model's files (and a download of it that was never finished).
func (s *Store) Remove(id string) error {
	repo, variant, ok := SplitModelID(id)
	if !ok {
		return fmt.Errorf("%q is not a model id (owner/repo:VARIANT)", id)
	}
	dir := s.VariantDir(repo, variant)
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotDownloaded, id)
		}
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// empty repository folders go too
	for p := filepath.Dir(dir); p != s.Dir && strings.HasPrefix(p, s.Dir); p = filepath.Dir(p) {
		if os.Remove(p) != nil {
			break
		}
	}
	return nil
}

// Used is the bytes of every downloaded model.
func (s *Store) Used() uint64 {
	var n uint64
	for _, m := range s.List() {
		n += m.Size
	}
	return n
}
