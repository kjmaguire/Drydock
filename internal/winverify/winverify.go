// Package winverify builds and runs the Windows verification bundle: cmd/drydock-winbundle cross-compiles the test binaries
// of every package with Windows-specific code (plus storage, the Claude adapter, CLI detection, git worktrees and process
// control), the scripted e2e core and cmd/drydock-verify into one folder; drydock-verify runs them on the machine it is
// copied to and writes a report a person can send back. Nothing in the bundle needs Go installed, except the tests that
// call the Go toolchain themselves, which are skipped with that reason when it is missing.
//
// The package is cross-platform on purpose: the same bundle is built for Linux to prove the orchestration on the
// development host (docs/windows-runner.md). It is a verification tool, not part of the shipped application.
package winverify

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Status is the outcome of a step or of one test.
type Status string

const (
	Pass Status = "pass"
	Fail Status = "fail"
	Skip Status = "skip"
)

// Bundle layout, relative to the bundle folder.
const (
	ManifestName = "bundle.json"
	ReadmeName   = "README-VERIFY.txt"
	TestsDir     = "tests"
	ReportMD     = "drydock-verify-report.md"
	ReportJSON   = "drydock-verify-report.json"
)

// ModulePath is the module the builder and the UI step expect to find in a source checkout.
const ModulePath = "github.com/kjmaguire/drydock"

// exeSuffix is ".exe" for Windows targets.
func exeSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// redactor removes what must never leave the machine in a report or log: tokens and keys in their common shapes, the
// value of any "token=" style parameter, and the home folder (replaced by ~, so paths stay readable without the user name).
type redactor struct {
	home string
}

var secretShapes = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{8,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`xox[abprs]-[A-Za-z0-9\-]{10,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/\-]{16,}=*`),
}

var secretParam = regexp.MustCompile(`(?i)\b(token|api[_-]?key|apikey|secret|password|passwd)=([^\s&"'<>]+)`)

func newRedactor() redactor {
	home, _ := os.UserHomeDir()
	if len(home) < 4 { // never replace "/" or "C:\" everywhere
		home = ""
	}
	return redactor{home: home}
}

func (r redactor) text(s string) string {
	for _, re := range secretShapes {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			if sub := re.FindStringSubmatch(m); len(sub) > 1 {
				return sub[1] + "[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	s = secretParam.ReplaceAllString(s, "$1=[REDACTED]")
	if r.home != "" {
		s = replaceFold(s, r.home, "~")
		if runtime.GOOS == "windows" { // the same folder written with forward slashes
			s = replaceFold(s, filepath.ToSlash(r.home), "~")
		}
	}
	return s
}

// replaceFold replaces old in s, ignoring case on Windows (paths are case-insensitive there).
func replaceFold(s, old, repl string) string {
	if runtime.GOOS != "windows" {
		return strings.ReplaceAll(s, old, repl)
	}
	lower, lowOld := strings.ToLower(s), strings.ToLower(old)
	if len(lower) != len(s) { // non-ASCII case folding changed the length: fall back to an exact replace
		return strings.ReplaceAll(s, old, repl)
	}
	var b strings.Builder
	for {
		i := strings.Index(lower, lowOld)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(repl)
		s, lower = s[i+len(old):], lower[i+len(old):]
	}
}

// lastLines returns at most n lines from the end of s (and at most max bytes), noting how many lines were left out.
func lastLines(s string, n, max int) string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	cut := 0
	if len(lines) > n {
		cut = len(lines) - n
		lines = lines[cut:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > max {
		out = "..." + out[len(out)-max:]
	}
	if cut > 0 {
		out = "[" + itoa(cut) + " earlier lines are in the log]\n" + out
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
