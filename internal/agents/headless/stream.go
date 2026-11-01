// Package headless drives agent CLIs over the headless contract (docs/connections.md section 9, brief section 5) where Drydock
// has no protocol client: Antigravity's documented stream-json mode (decisions D2, D11) and the declarative dialect of a
// person's own manifest (`custom`). What is declared in a manifest stays data (argv, output format, field paths); this package
// is the Go that reads it. Neither has been run against the real program it describes: the tests use labelled stand-ins written
// from the documentation and from the dialect a manifest declares.
package headless

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// stream is the output side of a session: a channel the reader may leave, and the stderr tail for error reports.
type stream struct {
	out      chan workflow.Chunk
	stopped  chan struct{} // closed by Stop: nobody reads Output after it
	stopOnce sync.Once
	errTail  tail
}

func newStream() stream {
	return stream{out: make(chan workflow.Chunk, 256), stopped: make(chan struct{})}
}

// emit hands a chunk to whoever reads Output(); it gives up (false) once the session was stopped, so a reader that left with a
// full buffer does not hold the process behind it.
func (s *stream) emit(c workflow.Chunk) bool {
	select {
	case s.out <- c:
		return true
	default:
	}
	select {
	case s.out <- c:
		return true
	case <-s.stopped:
		return false
	}
}

func (s *stream) markStopped() { s.stopOnce.Do(func() { close(s.stopped) }) }

type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 8192 {
		t.b = t.b[len(t.b)-8192:]
	}
	return len(p), nil
}

func (t *tail) String() string { t.mu.Lock(); defer t.mu.Unlock(); return string(t.b) }

// childEnv is the environment of a CLI: everything with scrub off; with it on the base allowlist (proc.BaseEnvNames), the
// variables whose names start with one of prefixes (the CLI's own settings and keys) and the names passed on by the person.
func childEnv(scrub bool, passEnv func() []string, prefixes []string, extra []string) []string {
	parent := os.Environ()
	var env []string
	if scrub {
		names := append([]string(nil), proc.BaseEnvNames...)
		var passed []string
		if passEnv != nil {
			passed = passEnv()
			names = append(names, passed...)
		}
		env = proc.WithoutProxyCredentials(proc.ScrubEnv(parent, names, prefixes), passed)
	} else {
		env = append(env, parent...)
	}
	return append(env, extra...)
}

// findProgram looks the manifest's program names up on PATH, as detection does (internal/connections): each name as written,
// then without its Windows extension (agy.exe is agy on the development host). A full path is used as it is.
func findProgram(names []string) (string, error) {
	cands := append([]string(nil), names...)
	for _, n := range names {
		switch strings.ToLower(filepath.Ext(n)) {
		case ".exe", ".cmd", ".bat", ".com":
			cands = append(cands, strings.TrimSuffix(n, filepath.Ext(n)))
		}
	}
	for _, n := range cands {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s was not found on PATH", strings.Join(names, " or "))
}

func chunk(kind string, v any) workflow.Chunk { return workflow.NewChunk(kind, v) }

func rawOrNil(b []byte) json.RawMessage {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return json.RawMessage(b)
}

// drift is a warning that the CLI printed something the reader does not know: a line that is not the declared format, or an
// event of a type nobody mapped. It is said once per kind (internal/core/adapterdrift.go records it on the Connection).
type drift struct {
	seen map[string]bool
}

func (d *drift) once(kind string) (workflow.Chunk, bool) {
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	if d.seen[kind] {
		return workflow.Chunk{}, false
	}
	d.seen[kind] = true
	return chunk(workflow.ChunkError, workflow.ErrorChunk{Message: "the CLI printed an event Drydock does not read: " + kind, Code: workflow.CodeAdapterDrift, Detail: kind}), true
}

// firstLine is the first non-empty line of s, at most 300 characters.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 300 {
				l = l[:300]
			}
			return l
		}
	}
	return ""
}
