package claude

import (
	"os"
	"strings"
	"testing"
)

// TestMain lets the process tests run this test binary as the claude CLI (a LABELED TEST DOUBLE, the stand-in): with
// DRYDOCK_CLAUDE_STANDIN=1 in its environment it records what it was started with into files named after $OUT and exits.
// It replaces POSIX shell scripts, so the same tests run on Windows, where the CLI's argument passing matters most.
func TestMain(m *testing.M) {
	if os.Getenv("DRYDOCK_CLAUDE_STANDIN") == "1" {
		os.Exit(standin(os.Args[1:], os.Getenv("OUT")))
	}
	os.Exit(m.Run())
}

// standin writes $OUT.args (one argument per line), $OUT.env (the environment), $OUT.content and $OUT.path (the contents
// and path of --append-system-prompt-file), $OUT.allowed (--allowedTools) and $OUT.inline (when --append-system-prompt was
// passed inline).
func standin(args []string, out string) int {
	if out == "" {
		return 2
	}
	write := func(suffix, s string) { _ = os.WriteFile(out+suffix, []byte(s), 0o644) }
	write(".args", strings.Join(args, "\n")+"\n")
	write(".env", strings.Join(os.Environ(), "\n")+"\n")
	for i, a := range args {
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch a {
		case "--append-system-prompt-file":
			b, _ := os.ReadFile(next)
			write(".content", string(b))
			write(".path", next)
		case "--allowedTools":
			write(".allowed", next)
		case "--append-system-prompt":
			write(".inline", "inline")
		}
	}
	return 0
}

// standinSpecEnv is what a RunSpec adds so the started test binary acts as the stand-in.
func standinSpecEnv(out string) []string { return []string{"DRYDOCK_CLAUDE_STANDIN=1", "OUT=" + out} }
