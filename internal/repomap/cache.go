package repomap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// The disk cache keeps, per cache key (a mapped folder, or one key for all checkouts of a repository), the tags of the
// files the last map had, keyed by content hash. It is one file per key, rewritten only when a map's set of files changed,
// and it holds only the last map's files, so it does not grow with the history. A cache written by another parser or query version is ignored.

type diskCache struct {
	Version string           `json:"version"`
	Tags    map[string][]Tag `json:"tags"`
}

// memoryCap bounds the in-memory tag cache: past it, a map keeps only the entries of the files it just read.
var memoryCap = 50000 // a variable so a test can lower it

func cacheFile(dir, key string) string {
	if dir == "" {
		return ""
	}
	h := sha256.Sum256([]byte(key))
	return filepath.Join(dir, hex.EncodeToString(h[:8])+".json")
}

// loadDisk reads a cache file into memory once, and again after the memory cache was trimmed (saveDisk).
func (m *Mapper) loadDisk(dir, key string) {
	f := cacheFile(dir, key)
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

// saveDisk writes the tags of a map's current files (live: their content hashes) when they differ from the cache file's,
// and bounds memory: past memoryCap the tag cache keeps only this map's entries (and every cache file is read again when
// next needed), and the file stats keep only this map's files (paths).
func (m *Mapper) saveDisk(dir, key string, live, paths map[string]bool) {
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
		m.loaded = map[string]bool{}
	}
	if len(m.stats) > memoryCap {
		kept := make(map[string]statInfo, len(paths))
		for p := range paths {
			if st, ok := m.stats[p]; ok {
				kept[p] = st
			}
		}
		m.stats = kept
	}
	f := cacheFile(dir, key)
	if f == "" {
		m.mu.Unlock()
		return
	}
	fp := keysPrint(snap)
	same := m.saved[f] == fp
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
	// a temporary file of its own, so two maps writing at once never mix their bytes; recorded as saved only once written
	tmp, err := os.CreateTemp(filepath.Dir(f), filepath.Base(f)+".*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), f)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	m.mu.Lock()
	m.saved[f] = fp
	m.mu.Unlock()
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
