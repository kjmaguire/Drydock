// Package applog is the application log: a small rotating file under <data>/logs. Every line passes through a scrubber
// before it is written, so a secret that reaches a log call never reaches the disk (docs/packaging.md s6 and s9).
package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Writer appends scrubbed, timestamped lines to Dir/drydock.log and rotates it at MaxBytes, keeping Keep old files
// (drydock.log.1 is the newest). The file is opened per write, so no handle stays open between lines.
type Writer struct {
	Dir      string
	MaxBytes int64 // default 1 MiB
	Keep     int   // default 3
	// Scrub cleans a line; when it returns an error the line is replaced by a notice and the content is dropped.
	Scrub func(line string) (string, error)
	// Now overrides the clock (tests).
	Now func() time.Time

	mu sync.Mutex
}

// FileName is the active log file name.
const FileName = "drydock.log"

// Printf writes one line. Errors writing the log are swallowed: logging must never break the app.
func (w *Writer) Printf(format string, a ...any) { w.Write(fmt.Sprintf(format, a...)) }

// Write writes one line (embedded newlines are folded to keep one entry per line).
func (w *Writer) Write(msg string) {
	if w == nil || w.Dir == "" {
		return
	}
	msg = strings.NewReplacer("\r\n", " | ", "\n", " | ", "\r", " ").Replace(msg)
	if w.Scrub != nil {
		clean, err := w.Scrub(msg)
		if err != nil {
			clean = "[log line withheld: it could not be scanned for secrets]"
		}
		msg = clean
	}
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	line := now().UTC().Format("2006-01-02T15:04:05.000Z") + " " + msg + "\n"

	w.mu.Lock()
	defer w.mu.Unlock()
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(w.Dir, FileName)
	limit := w.MaxBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	if st, err := os.Stat(path); err == nil && st.Size()+int64(len(line)) > limit {
		w.rotate(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}

func (w *Writer) rotate(path string) {
	keep := w.Keep
	if keep <= 0 {
		keep = 3
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", path, keep))
	for i := keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1))
	}
	_ = os.Rename(path, path+".1")
}

// Files lists the log files, newest first (the active file, then .1, .2...). Only files that exist are returned.
func Files(dir string, keep int) []string {
	if keep <= 0 {
		keep = 3
	}
	var out []string
	p := filepath.Join(dir, FileName)
	if _, err := os.Stat(p); err == nil {
		out = append(out, p)
	}
	for i := 1; i <= keep; i++ {
		q := fmt.Sprintf("%s.%d", p, i)
		if _, err := os.Stat(q); err == nil {
			out = append(out, q)
		}
	}
	return out
}
