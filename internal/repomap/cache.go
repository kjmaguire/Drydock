package repomap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// The disk cache keeps, per mapped folder, the tags of the files it had at the last map, keyed by content hash. It is one
// file per folder, rewritten only when a map parsed something new, and it holds only the folder's current files, so it
// does not grow with the folder's history. A cache written by another parser or query version is ignored.

type diskCache struct {
	Version string           `json:"version"`
	Tags    map[string][]Tag `json:"tags"`
}

// memoryCap bounds the in-memory tag cache: past it, a map keeps only the entries of the files it just read.
const memoryCap = 50000

func cacheFile(dir, root string) string {
	if dir == "" {
		return ""
	}
	h := sha256.Sum256([]byte(root))
	return filepath.Join(dir, hex.EncodeToString(h[:8])+".json")
}

// loadDisk reads a folder's cache file once per Mapper.
func (m *Mapper) loadDisk(dir, root string) {
	f := cacheFile(dir, root)
	m.mu.Lock()
	defer m.mu.Unlock()
	if f == "" || m.loaded[f] {
		return
	}
	m.loaded[f] = true
	b, err := os.ReadFile(f)
	if err != nil {
		return
	}
	var dc diskCache
	if json.Unmarshal(b, &dc) != nil || dc.Version != m.version {
		return
	}
	for h, t := range dc.Tags {
		if _, ok := m.tags[h]; !ok {
			m.tags[h] = t
		}
	}
	m.saved[f] = keysPrint(dc.Tags)
}

// saveDisk writes the tags of a folder's current files (live: their content hashes) when they differ from the file's, and
// trims the memory cache when it is over memoryCap.
func (m *Mapper) saveDisk(dir, root string, live map[string]bool) {
	m.mu.Lock()
	snap := make(map[string][]Tag, len(live))
	for h := range live {
		if t, ok := m.tags[h]; ok && h != "" {
			snap[h] = t
		}
	}
	if len(m.tags) > memoryCap {
		m.tags = make(map[string][]Tag, len(snap))
		for h, t := range snap {
			m.tags[h] = t
		}
	}
	m.mu.Unlock()
	f := cacheFile(dir, root)
	if f == "" {
		return
	}
	fp := keysPrint(snap)
	m.mu.Lock()
	same := m.saved[f] == fp
	m.saved[f] = fp
	m.mu.Unlock()
	if same {
		return
	}
	b, err := json.Marshal(diskCache{Version: m.version, Tags: snap})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
		return
	}
	tmp := f + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, f)
	}
}

// keysPrint is a fingerprint of a cache's set of content hashes.
func keysPrint(t map[string][]Tag) string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
	}
	return hex.EncodeToString(h.Sum(nil))
}
