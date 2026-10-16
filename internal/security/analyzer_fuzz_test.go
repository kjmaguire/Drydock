package security

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"
)

var fuzzTokens = []string{
	"rm", "-rf", "-Recurse", "-Force", "Remove-Item", "del", "/s", "/q", "git", "push", "--force", "x", "..", "/", "\\", "C:\\", "~", "$x", "${x}", "$(", ")", "(", "{", "}",
	"`", "'", "\"", "|", "||", "&&", "&", ";", "\n", ">", ">>", "<", "<<", "<<<", "2>&1", "$", "@", "@(", "@{", "@'\n", "\n'@", "%", "!", "^", "=", " ", "  ", "\t", "eval",
	"bash", "-c", "powershell", "-Command", "-enc", "cmd", "/c", "env", "sudo", "xargs", "find", "-exec", "{}", "\\;", "+", "*", "?", "[", "]", "#", "<#", "#>", "$null",
	"if", "then", "fi", "for", "in", "do", "done", "function", "iex", "&", ".", "call", ":label", "set", "PATH=", "cat", ".env", "wrangler", "deploy", "\u2018", "\u201c", "\x00", "é",
}

func randomCommand(r *rand.Rand) string {
	n := 1 + r.Intn(14)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(fuzzTokens[r.Intn(len(fuzzTokens))])
		if r.Intn(3) > 0 {
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

// Random token soup in every flavor: the analyzer must terminate, never panic, and never allow an Opaque report.
func TestAnalyzerRobustOnTokenSoup(t *testing.T) {
	r := rand.New(rand.NewSource(20261008))
	deadline := time.Now().Add(40 * time.Second)
	for i := 0; i < 6000 && time.Now().Before(deadline); i++ {
		cmd := randomCommand(r)
		for _, sh := range []ShellKind{bash, psh, cmdx, ShellUnknown} {
			an, root := posixAn, posixRoot
			if sh == psh || sh == cmdx {
				an, root = winAn, winRoot
			}
			done := make(chan CommandReport, 1)
			go func() {
				rep, _ := an.Analyze(context.Background(), CommandInput{Raw: cmd, Shell: sh, Root: root})
				done <- rep
			}()
			select {
			case rep := <-done:
				d := Decide(rep)
				if rep.Confidence == Opaque && d.Verdict == VerdictAllow {
					t.Fatalf("[%s] %q: opaque report allowed", sh, cmd)
				}
				if len(rep.HardDeny) > 0 && d.Verdict != VerdictDeny {
					t.Fatalf("[%s] %q: hard deny not denied", sh, cmd)
				}
				if rep.Digest == "" {
					t.Fatalf("[%s] %q: no digest", sh, cmd)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("[%s] %q: analysis did not terminate", sh, cmd)
			}
		}
	}
}

func FuzzAnalyze(f *testing.F) {
	for _, s := range []string{"ls", "rm -rf x", "Remove-Item -Recurse x", "del /s x", "bash -c 'x'", "$x = 1", "echo %X%", "cat <<EOF\nx\nEOF", "iex $x"} {
		f.Add(s, uint8(0))
	}
	f.Fuzz(func(t *testing.T, cmd string, sh uint8) {
		shells := []ShellKind{bash, psh, cmdx, ShellUnknown}
		s := shells[int(sh)%len(shells)]
		an, root := posixAn, posixRoot
		if s == psh || s == cmdx {
			an, root = winAn, winRoot
		}
		rep, err := an.Analyze(context.Background(), CommandInput{Raw: cmd, Shell: s, Root: root})
		if err != nil {
			t.Fatal(err)
		}
		if d := Decide(rep); rep.Confidence == Opaque && d.Verdict == VerdictAllow {
			t.Fatalf("opaque allowed: %q", cmd)
		}
	})
}
