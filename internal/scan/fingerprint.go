package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// A finding's fingerprint (decision P97) is its stable id: what the Security page dismisses, asks the code around, and makes a
// task from. It is a hash of the engine, the rule, the file and the text of the finding's line: the same finding keeps its
// fingerprint while its line reads the same, wherever the line moves in the file. The text is never a secret's value: the
// secrets engine hashes the line with the secret masked, and every line is redacted by the app's scanner before it is
// hashed. A finding with no line text (a dependency, a file that cannot be read) hashes what names it instead: the package
// and its advisories, or the line number. Two findings with the same fingerprint in one report are told apart by a suffix
// in report order ("-2", "-3").

// fingerprintLen is how many hex digits of the hash a fingerprint keeps (96 bits).
const fingerprintLen = 24

// fingerprintPattern is what a fingerprint looks like, with its suffix.
var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{24}(-[0-9]{1,5})?$`)

// ValidFingerprint reports whether s looks like a fingerprint.
func ValidFingerprint(s string) bool { return fingerprintPattern.MatchString(s) }

// normalizeText collapses every run of white space to one space, line by line, and drops the empty lines, so indentation
// and line endings do not change a fingerprint.
func normalizeText(s string) string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if f := strings.Join(strings.Fields(l), " "); f != "" {
			out = append(out, f)
		}
	}
	return strings.Join(out, "\n")
}

// Fingerprint is the fingerprint of a finding of engine and rule in file, from its line's text (redacted, any secret
// masked) or, when there is no text, from its line number.
func Fingerprint(engine EngineID, rule, file, text string, line int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", engine, canonicalRule(engine, rule), file)
	if t := normalizeText(text); t != "" {
		th := sha256.Sum256([]byte(t))
		fmt.Fprintf(h, "text:%x", th)
	} else {
		fmt.Fprintf(h, "line:%d", line)
	}
	return hex.EncodeToString(h.Sum(nil))[:fingerprintLen]
}

// BaseFingerprint is a fingerprint without its suffix.
func BaseFingerprint(fp string) string {
	if i := strings.IndexByte(fp, '-'); i >= 0 {
		return fp[:i]
	}
	return fp
}

// canonicalRule is the rule as the fingerprint names it. Opengrep names a rule by the folder it was read from and its id
// ("home.kyle.AppData.drydock.rules.builtin-1a2b3c.drydock.drydock.go.weak-hash"); a built-in rule is named by its own id,
// so a new version of the rules (a new folder) or another data folder keeps the fingerprint.
func canonicalRule(engine EngineID, rule string) string {
	if engine == SAST {
		if r, ok := builtinRule(rule); ok {
			return r.ID
		}
	}
	return rule
}

// numberFingerprints gives the second and later findings with the same fingerprint in a report a suffix, in report order.
func numberFingerprints(fs []Finding) {
	seen := map[string]int{}
	for i := range fs {
		fp := BaseFingerprint(fs[i].Fingerprint)
		if fp == "" {
			continue
		}
		seen[fp]++
		if n := seen[fp]; n > 1 {
			fs[i].Fingerprint = fp + "-" + strconv.Itoa(n)
		} else {
			fs[i].Fingerprint = fp
		}
	}
}

// maskSecret is the line text the secrets engine hashes: the line with every copy of the secret replaced by the mask the
// app's scanner uses, then redacted. ok is false when the secret is not in the line (it cannot be masked there): the line
// number is hashed instead.
func maskSecret(line, secret, rule string, redact func(string) string) (string, bool) {
	if secret == "" || !strings.Contains(line, secret) {
		return "", false
	}
	return redact(strings.ReplaceAll(line, secret, secretMask(rule))), true
}

// secretMask is what stands for a secret's value: the form of the app's scanner (security.Scanner.Redact).
func secretMask(rule string) string { return "[REDACTED:" + rule + "]" }

// maxHashedLines bounds the lines of one finding that its fingerprint reads.
const maxHashedLines = 20

// lineReader reads the lines of the scanned files for the fingerprints of one engine's findings, each file once. A file it
// cannot read safely (outside the folder, not a regular file, too big, binary) gives no text.
type lineReader struct {
	dir   string
	files map[string][]string
}

func newLineReader(dir string) *lineReader {
	return &lineReader{dir: dir, files: map[string][]string{}}
}

// text is the finding's lines (Line to EndLine, at most maxHashedLines), or "" when they cannot be read.
func (r *lineReader) text(f Finding) string {
	if f.File == "" || f.Line <= 0 {
		return ""
	}
	lines, ok := r.files[f.File]
	if !ok {
		lines = nil
		if p, err := resolveInside(r.dir, f.File); err == nil {
			if b, _, err := readSafeText(p); err == nil {
				lines = splitLines(string(b))
			}
		}
		r.files[f.File] = lines
	}
	end := f.Line
	if f.EndLine > end {
		end = min(f.EndLine, f.Line+maxHashedLines-1)
	}
	if f.Line > len(lines) {
		return ""
	}
	return strings.Join(lines[f.Line-1:min(end, len(lines))], "\n")
}

// fingerprintFindings sets the fingerprint of findings read from files (the Code engine's): from their lines in dir,
// redacted.
func fingerprintFindings(dir string, fs []Finding, redact func(string) string) {
	r := newLineReader(dir)
	for i := range fs {
		text := r.text(fs[i])
		if text != "" {
			text = redact(text)
		}
		fs[i].Fingerprint = Fingerprint(fs[i].Engine, fs[i].Rule, fs[i].File, text, fs[i].Line)
	}
}

// depsText is what names a dependency finding for its fingerprint: the package, its version and its advisories.
func depsText(f Finding) string {
	return f.Package + "@" + f.Version + " " + strings.Join(f.IDs, ",")
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// readSafeText reads a regular file that looks like text: at most maxSecretFile bytes, no NUL byte in its first 8 KiB.
// why says, in plain words, why a file was not read.
func readSafeText(p string) (b []byte, why string, err error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, "the file is not there any more", err
	}
	if !st.Mode().IsRegular() {
		return nil, "it is not a regular file", errNotText
	}
	if st.Size() > maxSecretFile {
		return nil, fmt.Sprintf("the file is larger than %d MiB", maxSecretFile>>20), errNotText
	}
	b, ok, readErr := readText(p)
	switch {
	case readErr:
		return nil, "the file could not be read", errNotText
	case !ok:
		return nil, "it is a binary file", errNotText
	}
	return b, "", nil
}

// within reports whether path is dir or inside it (case-insensitive on Windows).
func within(path, dir string) bool {
	if isWindows {
		path, dir = strings.ToLower(path), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
