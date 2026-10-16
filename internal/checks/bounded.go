package checks

import (
	"fmt"
	"io"
	"sort"
)

// What a command that prints without end may cost (audit 2026-10-16, M6): a test run that loops on a log line, a dev server that
// stays up for days. The output is shown and judged from its start and its end, so memory keeps those; the file keeps the first
// part; the failing tests are collected as the lines go by.
const (
	keepHead     = 2 << 20  // the first bytes of the output kept in memory
	keepTail     = 4 << 20  // and the last
	maxLogBytes  = 64 << 20 // the most of the output written to the log file
	maxFailNames = 500      // the most failing test names collected
)

// logFile writes a check's lines to its log until the file holds maxLogBytes, then says so once and writes nothing more.
type logFile struct {
	f    io.Writer
	max  int64
	n    int64
	full bool
}

func (l *logFile) line(s string) {
	if l.full {
		return
	}
	if l.n+int64(len(s))+1 > l.max {
		l.full = true
		_, _ = fmt.Fprintf(l.f, "[the log stops here: it holds the first %d MiB of the output]\n", l.max>>20)
		return
	}
	n, _ := fmt.Fprintln(l.f, s)
	l.n += int64(n)
}

// failing collects the names of failing tests as the lines of the output go by.
type failing struct {
	parser string
	set    map[string]bool
}

func (f *failing) add(line string) {
	if f.parser == "" || len(f.set) >= maxFailNames {
		return
	}
	for _, n := range ParseFailingTests(f.parser, line) {
		if f.set == nil {
			f.set = map[string]bool{}
		}
		if len(f.set) < maxFailNames {
			f.set[n] = true
		}
	}
}

func (f *failing) names() []string {
	if len(f.set) == 0 {
		return nil
	}
	out := make([]string, 0, len(f.set))
	for n := range f.set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// drain reads r to its end and throws it away: a pipe nobody reads blocks the command that writes to it.
func drain(r io.Reader) { _, _ = io.Copy(io.Discard, r) }
