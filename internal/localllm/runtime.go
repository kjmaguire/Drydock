package localllm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/runners"
)

// MaxRuntimeArchive is the largest build archive Drydock downloads (the CUDA runtime archives are about 400 MB).
const MaxRuntimeArchive = 1 << 30

// Installed is the build on this PC.
type Installed struct {
	Source      string    `json:"source"`
	Tag         string    `json:"tag"`
	Backend     Backend   `json:"backend"`
	Dir         string    `json:"dir"`
	Server      string    `json:"server"` // full path of llama-server
	InstalledAt time.Time `json:"installedAt"`
}

// Progress is one step of an install, for the page.
type Progress struct {
	Phase string `json:"phase"` // downloading | unpacking | done
	File  string `json:"file,omitempty"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

const installedFile = "installed.json"

// Runtime keeps the builds under Root: Root/cache holds the checked archives, Root/<source>-<tag>-<backend> an unpacked build,
// Root/installed.json which one is current.
type Runtime struct {
	Root string
	HTTP *http.Client
}

// Installed is the current build, if any (its server program must still be there).
func (r *Runtime) Installed() (Installed, bool) {
	b, err := os.ReadFile(filepath.Join(r.Root, installedFile))
	if err != nil {
		return Installed{}, false
	}
	var in Installed
	if json.Unmarshal(b, &in) != nil || in.Server == "" {
		return Installed{}, false
	}
	if st, err := os.Stat(in.Server); err != nil || !st.Mode().IsRegular() {
		return Installed{}, false
	}
	return in, true
}

// Install downloads the release's archive (and its CUDA runtime), checks each against its digest, unpacks them into one
// folder, finds llama-server in it and makes it the current build. Older builds are removed, except the one that was
// current until now when the new one failed.
func (r *Runtime) Install(ctx context.Context, rel Release, report func(Progress)) (Installed, error) {
	src, ok := SourceByID(rel.Source)
	if !ok {
		return Installed{}, fmt.Errorf("%q is not a build source", rel.Source)
	}
	if report == nil {
		report = func(Progress) {}
	}
	cache := filepath.Join(r.Root, "cache")
	opts := func(name string) runners.DownloadOptions {
		return runners.DownloadOptions{Prefix: src.DownloadPrefix(), MaxBytes: MaxRuntimeArchive, Progress: func(done, total int64) {
			report(Progress{Phase: "downloading", File: name, Done: done, Total: total})
		}}
	}
	archive, err := runners.DownloadWith(ctx, r.HTTP, rel.Archive, cache, opts(rel.Archive.Name))
	if err != nil {
		return Installed{}, err
	}
	var cudart string
	if rel.Cudart != nil {
		if cudart, err = runners.DownloadWith(ctx, r.HTTP, *rel.Cudart, cache, opts(rel.Cudart.Name)); err != nil {
			return Installed{}, err
		}
	}
	dir := filepath.Join(r.Root, safeName(rel.Source+"-"+rel.Tag+"-"+string(rel.Backend)))
	report(Progress{Phase: "unpacking", File: rel.Archive.Name})
	_ = os.RemoveAll(dir)
	if err := runners.Unzip(archive, dir); err != nil {
		return Installed{}, fmt.Errorf("unpack %s: %w", rel.Archive.Name, err)
	}
	server, err := findServer(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return Installed{}, err
	}
	if cudart != "" {
		// the CUDA runtime's DLLs go beside llama-server, where Windows looks for them
		report(Progress{Phase: "unpacking", File: rel.Cudart.Name})
		tmp := dir + ".cudart"
		_ = os.RemoveAll(tmp)
		if err := runners.Unzip(cudart, tmp); err != nil {
			_ = os.RemoveAll(dir)
			return Installed{}, fmt.Errorf("unpack %s: %w", rel.Cudart.Name, err)
		}
		if err := mergeFiles(tmp, filepath.Dir(server)); err != nil {
			_ = os.RemoveAll(dir)
			_ = os.RemoveAll(tmp)
			return Installed{}, err
		}
		_ = os.RemoveAll(tmp)
	}
	in := Installed{Source: rel.Source, Tag: rel.Tag, Backend: rel.Backend, Dir: dir, Server: server, InstalledAt: time.Now().UTC()}
	b, _ := json.Marshal(in)
	if err := os.WriteFile(filepath.Join(r.Root, installedFile), b, 0o644); err != nil {
		return Installed{}, err
	}
	r.prune(dir)
	report(Progress{Phase: "done"})
	return in, nil
}

// Remove forgets the current build and deletes every unpacked build and cached archive.
func (r *Runtime) Remove() error {
	entries, err := os.ReadDir(r.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var first error
	for _, e := range entries {
		if e.Name() == "models" || e.Name() == "logs" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.Root, e.Name())); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// prune removes unpacked builds other than keep and archives in the cache other than the newest two.
func (r *Runtime) prune(keep string) {
	entries, _ := os.ReadDir(r.Root)
	for _, e := range entries {
		p := filepath.Join(r.Root, e.Name())
		if !e.IsDir() || p == keep || e.Name() == "cache" || e.Name() == "models" || e.Name() == "logs" {
			continue
		}
		_ = os.RemoveAll(p)
	}
	cache := filepath.Join(r.Root, "cache")
	files, _ := os.ReadDir(cache)
	type f struct {
		name string
		at   time.Time
	}
	var zips []f
	for _, e := range files {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		zips = append(zips, f{e.Name(), info.ModTime()})
	}
	sort.Slice(zips, func(i, j int) bool { return zips[i].at.After(zips[j].at) })
	for i, z := range zips {
		if i >= 2 && !strings.HasPrefix(z.name, "cudart-") {
			_ = os.Remove(filepath.Join(cache, z.name))
		}
	}
}

// findServer finds llama-server in an unpacked build (the official archives keep it at the top; older ones under build/bin).
func findServer(dir string) (string, error) {
	var found string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && isServer(d.Name()) {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("the archive has no llama-server program in it")
	}
	return found, nil
}

// mergeFiles moves every file under src into dst (flat: the CUDA runtime archives hold DLLs only), replacing what is there.
func mergeFiles(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		target := filepath.Join(dst, d.Name())
		_ = os.Remove(target)
		return os.Rename(p, target)
	})
}

// safeName turns s into one folder name: letters, digits, '-', '.' and '_' stay, anything else becomes '_'. The result is never
// empty, never made only of dots ("." and ".." are folders that lead elsewhere) and never ends in a dot (Windows drops it, so
// two names would become one) (audit 2026-10-16, L33).
func safeName(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.', c == '_':
			b.WriteRune(c)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if strings.Trim(out, ".") == "" {
		return "_"
	}
	if strings.HasSuffix(out, ".") {
		out = strings.TrimRight(out, ".") + "_"
	}
	return out
}
