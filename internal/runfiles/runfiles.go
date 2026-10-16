// Package runfiles reads and compacts the raw output kept under <data>/runs (brief s18). A run's agent streams, check logs and
// saved prompts are plain files while the run is recent; retention compresses them to <name>.gz after 30 days, so every reader
// goes through this package and finds a file in either form.
package runfiles

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Ext is the suffix of a compacted file.
const Ext = ".gz"

// MaxRead bounds what is decompressed into memory for one read: a compacted file larger than this is refused rather than
// filling memory (a run's stream is normally a few megabytes).
const MaxRead = 256 << 20

// Stat reports whether path exists in either form and its size as text (for a compacted file, the size gzip recorded).
func Stat(path string) (size int64, compacted, ok bool) {
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
		return st.Size(), false, true
	}
	f, err := os.Open(path + Ext)
	if err != nil {
		return 0, false, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return 0, false, false
	}
	if st.Size() < 4 {
		return 0, true, true
	}
	var tail [4]byte
	if _, err := f.ReadAt(tail[:], st.Size()-4); err != nil {
		return 0, true, true
	}
	return int64(binary.LittleEndian.Uint32(tail[:])), true, true
}

// Reader is a run file opened for random reads.
type Reader interface {
	io.ReaderAt
	io.Closer
	Size() int64
}

type plain struct{ *os.File }

func (p plain) Size() int64 {
	st, err := p.Stat()
	if err != nil {
		return 0
	}
	return st.Size()
}

type unpacked struct{ *bytes.Reader }

func (unpacked) Close() error { return nil }

// Open opens path, or its compacted form, for reads at any offset. The error wraps os.ErrNotExist when neither exists.
func Open(path string) (Reader, error) {
	f, err := os.Open(path)
	if err == nil {
		return plain{f}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b, gerr := readGzip(path + Ext)
	if gerr != nil {
		if errors.Is(gerr, os.ErrNotExist) {
			return nil, err
		}
		return nil, gerr
	}
	return unpacked{bytes.NewReader(b)}, nil
}

// ReadAll reads path, or its compacted form, whole.
func ReadAll(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return b, err
	}
	b, gerr := readGzip(path + Ext)
	if errors.Is(gerr, os.ErrNotExist) {
		return nil, err
	}
	return b, gerr
}

func readGzip(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("runfiles: %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = zr.Close() }()
	b, err := io.ReadAll(io.LimitReader(zr, MaxRead+1))
	if err != nil {
		return nil, fmt.Errorf("runfiles: %s: %w", filepath.Base(path), err)
	}
	if len(b) > MaxRead {
		return nil, fmt.Errorf("runfiles: %s is larger than %d MB uncompressed", filepath.Base(path), MaxRead>>20)
	}
	return b, nil
}

// Compactable reports whether a file under a run's folder is raw output that retention compresses: agent streams, check and
// command logs, and saved prompts. Step outputs that a later step reads back stay as they are.
func Compactable(name string) bool {
	return strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".log") || name == "prompt.md"
}

// Compress replaces path with path.gz and returns the sizes before and after. The compressed file is complete on disk before
// the original is removed, so a crash leaves one readable form (both, at worst, and Open prefers the original).
func Compress(path string) (before, after int64, err error) {
	src, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return 0, 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return 0, 0, err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	zw, err := gzip.NewWriterLevel(tmp, gzip.BestCompression)
	if err != nil {
		tmp.Close()
		return 0, 0, err
	}
	if _, err := io.Copy(zw, src); err != nil {
		tmp.Close()
		return 0, 0, err
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return 0, 0, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return 0, 0, err
	}
	zst, err := tmp.Stat()
	if err != nil {
		tmp.Close()
		return 0, 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, 0, err
	}
	if err := os.Rename(tmp.Name(), path+Ext); err != nil {
		return 0, 0, err
	}
	src.Close()
	if err := os.Remove(path); err != nil {
		return 0, 0, err
	}
	return st.Size(), zst.Size(), nil
}

// ErrOutsideRuns is returned for a reference that is not a file inside <data>/runs.
var ErrOutsideRuns = errors.New("runfiles: the reference is not a file inside the run folders")

// InRuns reports whether ref is an absolute path inside <dataDir>/runs once ".." is resolved. Evidence rows and node records
// store such paths and the readers open them; a record that came from an imported archive, or was edited, must not make a reader
// open C:\\Users\\you\\.ssh\\id_ed25519 or a \\\\host\\share path.
func InRuns(dataDir, ref string) bool {
	if dataDir == "" || ref == "" || !filepath.IsAbs(ref) {
		return false
	}
	runs := filepath.Clean(filepath.Join(dataDir, "runs"))
	clean := filepath.Clean(ref)
	return strings.HasPrefix(clean, runs+string(filepath.Separator))
}

// ReadConfined reads ref (or its compacted form) when InRuns says it is a run file, else ErrOutsideRuns.
func ReadConfined(dataDir, ref string) ([]byte, error) {
	if !InRuns(dataDir, ref) {
		return nil, ErrOutsideRuns
	}
	return ReadAll(filepath.Clean(ref))
}
