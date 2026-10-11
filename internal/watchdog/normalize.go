package watchdog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	reANSI     = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	reTime     = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?|\b\d{1,2}:\d{2}:\d{2}(?:[.,]\d+)?\b`)
	reDuration = regexp.MustCompile(`\b\d+(?:\.\d+)?\s?(?:ms|µs|us|ns|s|sec|secs|seconds|m|min|mins|minutes)\b`)
	reHexAddr  = regexp.MustCompile(`\b0x[0-9a-fA-F]{4,}\b`)
	reTempWin  = regexp.MustCompile(`(?i)[a-z]:\\[^\s"']*\\(?:temp|tmp)\\[^\s"']*`)
	reTempNix  = regexp.MustCompile(`/(?:tmp|var/folders)/[^\s"']*`)
	reUUID     = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	rePID      = regexp.MustCompile(`(?i)\bpid[ =:]\d+\b`)
)

// maxHashed is how much of a normalized result is hashed (brief section 7).
const maxHashed = 4096

// Normalize strips volatile content (timestamps, durations, temp paths, hex addresses, ANSI, trailing whitespace)
// so that two outputs that differ only in such content hash equal. At most 4 KB is kept: a longer text keeps its first and
// last 2 KB and its length. The end matters: `go test ./...` prints its stable "ok" lines first and the failure that changes
// as the agent works last, so the first 4 KB alone would read progress as a spin.
func Normalize(s string) string {
	s = reANSI.ReplaceAllString(s, "")
	s = reUUID.ReplaceAllString(s, "<uuid>")
	s = reTime.ReplaceAllString(s, "<time>")
	s = reDuration.ReplaceAllString(s, "<dur>")
	s = reHexAddr.ReplaceAllString(s, "<addr>")
	s = reTempWin.ReplaceAllString(s, "<tmp>")
	s = reTempNix.ReplaceAllString(s, "<tmp>")
	s = rePID.ReplaceAllString(s, "pid <n>")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	s = strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if len(s) > maxHashed {
		half := maxHashed / 2
		s = s[:half] + "\n<" + strconv.Itoa(len(s)) + " bytes>\n" + s[len(s)-half:]
	}
	return s
}

// CanonicalJSON re-encodes JSON with sorted keys and no insignificant whitespace; non-JSON input is returned trimmed.
func CanonicalJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return strings.TrimSpace(string(raw))
	}
	b, _ := json.Marshal(v) // map keys are sorted by encoding/json
	return string(b)
}

// ToolKey is the tool-spin identity: (tool name, canonical arguments, normalized result). The result is part of the
// key on purpose: re-reading a file after editing it, or re-running tests after a change, yields a different result
// and is progress.
func ToolKey(name string, args json.RawMessage, result string) string {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write([]byte(CanonicalJSON(args)))
	h.Write([]byte{0})
	h.Write([]byte(Normalize(result)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// FailureSignature identifies a failing check: normalized output plus the sorted failing test names.
func FailureSignature(output string, failing []string) string {
	names := append([]string(nil), failing...)
	sort.Strings(names)
	h := sha256.New()
	h.Write([]byte(Normalize(output)))
	h.Write([]byte{0})
	h.Write([]byte(strings.Join(names, "\n")))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// bigrams returns the set of character bigrams of the whitespace-normalized lowercase text.
func bigrams(s string) map[string]struct{} {
	r := []rune(strings.ToLower(strings.Join(strings.Fields(s), " ")))
	m := make(map[string]struct{}, len(r))
	for i := 0; i+1 < len(r); i++ {
		m[string(r[i:i+2])] = struct{}{}
	}
	return m
}

// Jaccard returns the bigram Jaccard similarity of a and b in [0,1].
func Jaccard(a, b string) float64 {
	x, y := bigrams(a), bigrams(b)
	if len(x) == 0 && len(y) == 0 {
		return 1
	}
	inter := 0
	for k := range x {
		if _, ok := y[k]; ok {
			inter++
		}
	}
	union := len(x) + len(y) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
