package connections_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
)

// TestMain turns the test binary into one of three stand-in processes when asked to:
//
//   - DRYDOCK_FAKE_AGENT=1: the scripted Claude stream-json agent (internal/agents/fakeagent), a real process.
//   - DRYDOCK_FAKE_ACP=1: the scripted ACP agent (internal/agents/acp/fakeacp), a real process on the real SDK.
//   - DRYDOCK_STUB_CLI=1: a probe stub. The test copies this binary into a PATH directory under the name of a CLI (claude,
//     node, codex, ...); invoked under that name it answers each argv from a table (version strings, exit codes) and logs the
//     invocation. Detection tests use it to produce exact version/auth/exit-code situations that a single installed CLI cannot.
//
// All three are labelled stand-ins: they are real executables speaking the real protocols, never a replacement for a
// conformance run against the real CLI, which the *Real* and *Live* tests do when the CLI is present.
func TestMain(m *testing.M) {
	switch {
	case os.Getenv("DRYDOCK_STUB_CLI") == "1":
		os.Exit(stubMain(os.Args, os.Stdout, os.Stderr))
	case os.Getenv("DRYDOCK_FAKE_AGENT") == "1":
		os.Exit(fakeagent.Run(os.Args[1:], os.Stdin, os.Stdout))
	case os.Getenv("DRYDOCK_FAKE_ACP") == "1":
		os.Exit(fakeacp.Run(os.Args[1:], os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

// stubReply is the answer to one argv.
type stubReply struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Exit   int    `json:"exit,omitempty"`
}

// stubTable maps program name -> joined argv -> reply.
type stubTable map[string]map[string]stubReply

func stubMain(args []string, out, errw io.Writer) int {
	name := strings.TrimSuffix(filepath.Base(args[0]), ".exe")
	if log := os.Getenv("DRYDOCK_STUB_LOG"); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%s %s\n", name, strings.Join(args[1:], " "))
			f.Close()
		}
	}
	b, err := os.ReadFile(os.Getenv("DRYDOCK_STUB_TABLE"))
	if err != nil {
		fmt.Fprintln(errw, "stub: no table:", err)
		return 97
	}
	var tbl stubTable
	if err := json.Unmarshal(b, &tbl); err != nil {
		fmt.Fprintln(errw, "stub: bad table:", err)
		return 97
	}
	r, ok := tbl[name][strings.Join(args[1:], " ")]
	if !ok {
		fmt.Fprintf(errw, "stub: %s has no reply for %q\n", name, strings.Join(args[1:], " "))
		return 98
	}
	fmt.Fprint(out, r.Stdout)
	fmt.Fprint(errw, r.Stderr)
	return r.Exit
}

// stubEnv installs stubs for the given table into a fresh directory, makes it the whole PATH, and returns the directory and
// the log of invocations. The stubs are copies of this test binary.
func stubEnv(t *testing.T, tbl stubTable) (dir string, invocations func() []string) {
	t.Helper()
	dir = t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for name := range tbl {
		dst := filepath.Join(dir, name)
		if runtime.GOOS == "windows" {
			dst += ".exe" // Windows finds programs on PATH by extension (PATHEXT); the stub strips it from its name
		}
		if err := os.Symlink(self, dst); err != nil {
			if err := copyFile(self, dst); err != nil {
				t.Fatal(err)
			}
		}
	}
	tb, _ := json.Marshal(tbl)
	table := filepath.Join(t.TempDir(), "table.json")
	if err := os.WriteFile(table, tb, 0o644); err != nil {
		t.Fatal(err)
	}
	logf := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("DRYDOCK_STUB_CLI", "1")
	t.Setenv("DRYDOCK_STUB_TABLE", table)
	t.Setenv("DRYDOCK_STUB_LOG", logf)
	t.Setenv("PATH", dir)
	return dir, func() []string {
		b, _ := os.ReadFile(logf)
		s := strings.TrimSpace(string(b))
		if s == "" {
			return nil
		}
		return strings.Split(s, "\n")
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
