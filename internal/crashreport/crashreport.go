// Package crashreport keeps what Go prints when the process dies (docs/packaging.md section 9, crash reports; review 2026-10-27).
//
// A panic in a goroutine Drydock starts is contained and logged (internal/safe, core/panics.go), but some failures still end the
// process: a panic on a goroutine a library started, a runtime fatal error (a concurrent map write, out of memory, a stack
// overflow). Go prints the reason and every goroutine's stack to standard error, and drydock.exe is a GUI program with no
// console: before this, a crash left nothing behind. Arm points Go's crash output (debug.SetCrashOutput) at a file of this
// process's own in <data>/crashes; at the next start Collect turns any such file that has text into a crash report, with its
// secrets redacted, and removes the empty ones a normal exit left. Reports stay on this machine: the Support page lists them and
// a support bundle includes them (scrubbed again), and nothing is sent anywhere.
package crashreport

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

// Dir is where crash output and reports are kept, under the data folder.
func Dir(dataDir string) string { return filepath.Join(dataDir, "crashes") }

// Keep is how many reports are kept; older ones are deleted by Collect.
const Keep = 10

// MaxBytes is how much of one crash's output a report keeps (the reason and the first goroutines; a crash of a busy process
// can print megabytes of stacks).
const MaxBytes = 256 << 10

const (
	pendingPrefix = "pending-"
	pendingExt    = ".crash"
	reportPrefix  = "crash-"
	reportExt     = ".txt"
)

// Armed is a process's crash output file.
type Armed struct {
	f    *os.File
	path string
}

// Path is the file Go writes this process's crash output to.
func (a *Armed) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// Arm makes Go write this process's crash output to a new file in Dir(dataDir) as well as to standard error. Call Disarm on a
// normal exit; the file is then empty and is removed.
func Arm(dataDir string) (*Armed, error) {
	dir := Dir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%s%s-%d%s", pendingPrefix, time.Now().UTC().Format("20060102T150405Z"), os.Getpid(), pendingExt)
	p := filepath.Join(dir, name)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		f.Close()
		os.Remove(p)
		return nil, err
	}
	return &Armed{f: f, path: p}, nil
}

// Disarm stops writing crash output to the file and removes it when nothing was written.
func (a *Armed) Disarm() {
	if a == nil || a.f == nil {
		return
	}
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	st, err := a.f.Stat()
	a.f.Close()
	a.f = nil
	if err == nil && st.Size() == 0 {
		os.Remove(a.path)
	}
}

// Report is one kept crash report.
type Report struct {
	Name    string    `json:"name"`
	Time    time.Time `json:"time"`
	Size    int64     `json:"size"`
	Summary string    `json:"summary"` // the line that says why (panic: ..., fatal error: ...), redacted
}

// Collect turns every crash output file in Dir(dataDir) other than skip (this process's own, from Arm) that has text into a
// report, redacting it with redact and naming the version that was running (the one that wrote it is not known for sure: the
// version of this start is the best guess, and the report says so), removes the empty ones, and keeps the newest Keep reports.
// It returns the reports it made.
func Collect(dataDir, skip string, redact func(string) string, version string) ([]Report, error) {
	dir := Dir(dataDir)
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var made []Report
	var errs []error
	for _, e := range ents {
		n := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(n, pendingPrefix) || !strings.HasSuffix(n, pendingExt) {
			continue
		}
		p := filepath.Join(dir, n)
		if skip != "" && sameFile(p, skip) {
			continue
		}
		r, err := collectOne(dir, p, n, redact, version)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n, err))
			continue
		}
		if r != nil {
			made = append(made, *r)
		}
	}
	if err := prune(dir); err != nil {
		errs = append(errs, err)
	}
	return made, errors.Join(errs...)
}

func collectOne(dir, p, name string, redact func(string) string, version string) (*Report, error) {
	b, err := readAtMost(p, MaxBytes)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, os.Remove(p) // a normal exit, or one that left no output (killed, power loss)
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, pendingPrefix), pendingExt)
	when, err := time.Parse("20060102T150405Z", strings.SplitN(stamp, "-", 2)[0])
	if err != nil {
		when = time.Time{}
	}
	text := string(b)
	if redact != nil {
		text = redact(text)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Drydock crash report\nstarted: %s\nfound by version: %s (the crashed run was most likely the same)\n\n", when.Format(time.RFC3339), version)
	sb.WriteString(text)
	if len(b) == MaxBytes {
		sb.WriteString("\n[cut: the crash output was longer]\n")
	}
	out := filepath.Join(dir, reportPrefix+stamp+reportExt)
	if err := os.WriteFile(out, []byte(sb.String()), 0o600); err != nil {
		return nil, err
	}
	if err := os.Remove(p); err != nil {
		return nil, err
	}
	st, _ := os.Stat(out)
	r := &Report{Name: filepath.Base(out), Time: when, Summary: summary(text)}
	if st != nil {
		r.Size = st.Size()
	}
	return r, nil
}

// List returns the kept reports, newest first.
func List(dataDir string) ([]Report, error) {
	dir := Dir(dataDir)
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Report
	for _, e := range ents {
		n := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(n, reportPrefix) || !strings.HasSuffix(n, reportExt) {
			continue
		}
		p := filepath.Join(dir, n)
		st, err := e.Info()
		if err != nil {
			continue
		}
		b, err := readAtMost(p, 64<<10)
		if err != nil {
			continue
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(n, reportPrefix), reportExt)
		when, _ := time.Parse("20060102T150405Z", strings.SplitN(stamp, "-", 2)[0])
		out = append(out, Report{Name: n, Time: when, Size: st.Size(), Summary: summary(string(b))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// Read returns one report's text. name must be a report's own name (as List gives it), never a path.
func Read(dataDir, name string) (string, error) {
	if name != filepath.Base(name) || !strings.HasPrefix(name, reportPrefix) || !strings.HasSuffix(name, reportExt) {
		return "", fmt.Errorf("crashreport: %q is not a crash report", name)
	}
	b, err := readAtMost(filepath.Join(Dir(dataDir), name), MaxBytes+4096)
	return string(b), err
}

// Delete removes one report.
func Delete(dataDir, name string) error {
	if _, err := Read(dataDir, name); err != nil {
		return err
	}
	return os.Remove(filepath.Join(Dir(dataDir), name))
}

func prune(dir string) error {
	rs, err := List(filepath.Dir(dir))
	if err != nil {
		return err
	}
	var errs []error
	for i := Keep; i < len(rs); i++ {
		if err := os.Remove(filepath.Join(dir, rs[i].Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// summary is the line of a crash's output that says why: Go starts it with "panic: " or "fatal error: ".
func summary(text string) string {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		for _, p := range []string{"panic: ", "fatal error: ", "runtime: ", "unexpected fault"} {
			if strings.HasPrefix(l, p) {
				if len(l) > 300 {
					l = l[:300] + "..."
				}
				return l
			}
		}
	}
	return "Drydock stopped unexpectedly"
}

func readAtMost(p string, n int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, n))
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return os.SameFile(sa, sb)
}
