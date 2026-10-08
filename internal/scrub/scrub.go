// Package scrub removes secrets and identifying local names from text that leaves the app's own storage: support bundles,
// project exports and the on-disk application log. It builds on the secret scanner (gitleaks rules plus the exact values
// of secrets the app holds). Scrubbing fails closed: when a scan cannot complete the text is not returned.
package scrub

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/security"
)

// Scanner is what the scrubber needs from the secret scanner (*security.Scanner satisfies it).
type Scanner interface {
	Scan(ctx context.Context, text string) ([]security.Finding, error)
}

// Text returns text with every finding replaced by its "[REDACTED:<rule>]" mask, and the findings. A finding whose
// location is unknown redacts the whole text. A scan error (for example text over the scanner's size cap) is returned
// with no text: the caller must leave the content out.
func Text(ctx context.Context, sc Scanner, text string) (string, []security.Finding, error) {
	if text == "" {
		return "", nil, nil
	}
	fs, err := sc.Scan(ctx, text)
	if err != nil {
		return "", nil, err
	}
	if len(fs) == 0 {
		return text, nil, nil
	}
	type rng struct {
		s, e int
		mask string
	}
	var rs []rng
	for _, f := range fs {
		if f.End <= f.Start || f.Start < 0 || f.End > len(text) {
			return f.Mask, fs, nil
		}
		rs = append(rs, rng{f.Start, f.End, f.Mask})
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].s < rs[j].s })
	var b strings.Builder
	pos := 0
	for i := 0; i < len(rs); {
		r := rs[i]
		end := r.e
		j := i + 1
		for j < len(rs) && rs[j].s < end {
			if rs[j].e > end {
				end = rs[j].e
			}
			j++
		}
		if r.s > pos {
			b.WriteString(text[pos:r.s])
		}
		b.WriteString(r.mask)
		pos = end
		i = j
	}
	b.WriteString(text[pos:])
	return b.String(), fs, nil
}

// Rules counts findings per rule id.
func Rules(fs []security.Finding) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[f.RuleID]++
	}
	return m
}

// Identity replaces the local user's identifying names with fixed tokens: the user profile path, the machine name and
// the user name.
type Identity struct {
	profile []string
	host    *regexp.Regexp
	user    *regexp.Regexp
}

// Tokens used in scrubbed text.
const (
	TokenProfile = "<USERPROFILE>"
	TokenMachine = "<MACHINE>"
	TokenUser    = "<USER>"
)

// NewIdentity builds the replacer. The user names are all the names the person is known by (the OS account, the display
// name). Empty values, and names shorter than three characters (which would shred ordinary words), are skipped. Profile paths are matched in both slash styles, case-insensitively.
func NewIdentity(profileDir, machine string, users ...string) *Identity {
	id := &Identity{}
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[strings.ToLower(p)] {
			seen[strings.ToLower(p)] = true
			id.profile = append(id.profile, p)
		}
	}
	if len(profileDir) >= 3 {
		p := strings.TrimRight(profileDir, `/\`)
		add(p)
		add(strings.ReplaceAll(p, `\`, `/`))
		add(strings.ReplaceAll(p, `/`, `\`))
		add(strings.ReplaceAll(strings.ReplaceAll(p, `\`, `\\`), `/`, `\\`)) // JSON-escaped
	}
	sort.SliceStable(id.profile, func(i, j int) bool { return len(id.profile[i]) > len(id.profile[j]) })
	word := func(s string) *regexp.Regexp {
		if len(s) < 3 {
			return nil
		}
		return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(s) + `\b`)
	}
	id.host = word(machine)
	var names []string
	for _, u := range users {
		if len(u) >= 3 {
			names = append(names, regexp.QuoteMeta(u))
		}
	}
	if len(names) > 0 {
		id.user = regexp.MustCompile(`(?i)\b(?:` + strings.Join(names, "|") + `)\b`)
	}
	return id
}

// Apply replaces the identifying names in s.
func (id *Identity) Apply(s string) string {
	out, _ := id.ApplyCount(s)
	return out
}

// ApplyCount is Apply that also counts the replacements per token.
func (id *Identity) ApplyCount(s string) (string, map[string]int) {
	if id == nil {
		return s, nil
	}
	counts := map[string]int{}
	for _, p := range id.profile {
		var n int
		s, n = replaceFold(s, p, TokenProfile)
		counts[TokenProfile] += n
	}
	if id.host != nil {
		counts[TokenMachine] = len(id.host.FindAllStringIndex(s, -1))
		s = id.host.ReplaceAllString(s, TokenMachine)
	}
	if id.user != nil {
		counts[TokenUser] = len(id.user.FindAllStringIndex(s, -1))
		s = id.user.ReplaceAllString(s, TokenUser)
	}
	for k, v := range counts {
		if v == 0 {
			delete(counts, k)
		}
	}
	return s, counts
}

func replaceFold(s, old, repl string) (string, int) {
	if old == "" {
		return s, 0
	}
	ls, lo := strings.ToLower(s), strings.ToLower(old)
	if len(ls) != len(s) || len(lo) != len(old) { // case folding changed byte lengths: match exactly instead
		n := strings.Count(s, old)
		return strings.ReplaceAll(s, old, repl), n
	}
	var b strings.Builder
	pos, n := 0, 0
	for {
		i := strings.Index(ls[pos:], lo)
		if i < 0 {
			break
		}
		b.WriteString(s[pos : pos+i])
		b.WriteString(repl)
		pos += i + len(old)
		n++
	}
	if n == 0 {
		return s, 0
	}
	b.WriteString(s[pos:])
	return b.String(), n
}

// SensitiveName reports whether a setting or variable name suggests a secret value (token, secret, password, key...).
func SensitiveName(name string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"token", "secret", "password", "passwd", "pass", "pwd", "apikey", "api_key", "api-key", "credential", "private", "bearer", "auth", "license", "cookie", "session"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return strings.HasSuffix(n, "_key") || strings.HasSuffix(n, ".key") || strings.HasSuffix(n, "-key") || n == "key"
}
