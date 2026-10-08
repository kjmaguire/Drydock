package skills

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// MaxGlobs bounds the globs of one skill.
const MaxGlobs = 50

// SplitGlobs splits a comma separated glob list, keeping commas that sit inside {braces}.
func SplitGlobs(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				if t := strings.TrimSpace(s[start:i]); t != "" {
					out = append(out, t)
				}
				start = i + 1
			}
		}
	}
	if t := strings.TrimSpace(s[start:]); t != "" {
		out = append(out, t)
	}
	return out
}

// ValidateGlob checks a glob: relative, no "..", no NUL, balanced braces, at most 200 characters.
func ValidateGlob(g string) error {
	if g == "" || len(g) > 200 {
		return errors.New("glob is empty or longer than 200 characters")
	}
	if strings.ContainsAny(g, "\x00\n\r") {
		return errors.New("glob contains a control character")
	}
	n := strings.ReplaceAll(g, `\`, "/")
	if strings.HasPrefix(n, "/") && !strings.HasPrefix(n, "//") {
		// a leading slash anchors at the repository root (gitignore style); that is fine
		n = n[1:]
	}
	if strings.HasPrefix(n, "/") || (len(n) >= 2 && n[1] == ':') {
		return errors.New("glob must be relative to the repository root")
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." {
			return errors.New("glob must not contain ..")
		}
	}
	depth := 0
	for _, r := range g {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return errors.New("unbalanced braces in glob")
			}
		}
	}
	if depth != 0 {
		return errors.New("unbalanced braces in glob")
	}
	if len(expandBraces(g)) > 32 {
		return errors.New("glob expands to more than 32 alternatives")
	}
	for _, alt := range expandBraces(g) {
		if _, err := path.Match(strings.ReplaceAll(strings.ReplaceAll(alt, "**", "*"), "/", "x"), "x"); err != nil {
			return fmt.Errorf("invalid glob syntax: %v", err)
		}
	}
	return nil
}

// expandBraces expands {a,b} alternatives (nested braces included).
func expandBraces(g string) []string {
	open := strings.IndexByte(g, '{')
	if open < 0 {
		return []string{g}
	}
	depth, closeAt := 0, -1
	for i := open; i < len(g); i++ {
		switch g[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				closeAt = i
			}
		}
		if closeAt >= 0 {
			break
		}
	}
	if closeAt < 0 {
		return []string{g}
	}
	var alts []string
	d, start := 0, open+1
	for i := open + 1; i < closeAt; i++ {
		switch g[i] {
		case '{':
			d++
		case '}':
			d--
		case ',':
			if d == 0 {
				alts = append(alts, g[start:i])
				start = i + 1
			}
		}
	}
	alts = append(alts, g[start:closeAt])
	var out []string
	for _, a := range alts {
		for _, rest := range expandBraces(g[closeAt+1:]) {
			out = append(out, expandBraces(g[:open]+a+rest)...)
			if len(out) > 64 {
				return out
			}
		}
	}
	return out
}

// NormalizePath turns a worktree-relative path into the form globs match against: forward slashes, no leading "./".
// It reports false for absolute paths and paths that leave the root.
func NormalizePath(p string) (string, bool) {
	p = strings.ReplaceAll(p, `\`, "/")
	if p == "" || strings.ContainsRune(p, 0) || strings.HasPrefix(p, "/") || (len(p) >= 2 && p[1] == ':') {
		return "", false
	}
	var out []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
		case "..":
			return "", false
		default:
			out = append(out, seg)
		}
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, "/"), true
}

// GlobMatch reports whether a worktree-relative path matches a glob. Rules (gitignore style): "*" and "?" do not cross "/",
// "**" as a whole segment matches any number of directories, a pattern with no "/" matches the file name at any depth,
// a trailing "/" means everything below that directory, {a,b} alternatives are expanded, matching is case-sensitive.
func GlobMatch(glob, p string) bool {
	np, ok := NormalizePath(p)
	if !ok {
		return false
	}
	for _, alt := range expandBraces(glob) {
		alt = strings.ReplaceAll(alt, `\`, "/")
		alt = strings.TrimPrefix(alt, "./")
		anchored := strings.HasPrefix(alt, "/")
		alt = strings.TrimPrefix(alt, "/")
		if strings.HasSuffix(alt, "/") {
			alt += "**"
		}
		if alt == "" {
			continue
		}
		if !anchored && !strings.Contains(alt, "/") {
			if m, _ := path.Match(alt, path.Base(np)); m {
				return true
			}
			continue
		}
		if matchSegs(strings.Split(alt, "/"), strings.Split(np, "/")) {
			return true
		}
	}
	return false
}

func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for len(pat) > 1 && pat[1] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 1 {
				return len(segs) > 0 // "dir/**" needs something below dir
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegs(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if m, err := path.Match(pat[0], segs[0]); err != nil || !m {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// AnchorDir returns the static directory a glob is rooted in ("src/api/**" -> "src/api", "migrations/**/*.sql" -> "migrations"),
// the directory whose nested instruction file would carry the skill for CLIs that load such files just in time. It reports
// false for globs with no static directory prefix ("**/*.ts", "wrangler.jsonc").
func AnchorDir(glob string) (string, bool) {
	g := strings.TrimPrefix(strings.ReplaceAll(glob, `\`, "/"), "./")
	g = strings.TrimPrefix(g, "/")
	if strings.ContainsAny(g, "{}") {
		return "", false
	}
	segs := strings.Split(g, "/")
	var dir []string
	for i, s := range segs {
		if strings.ContainsAny(s, "*?[") {
			break
		}
		if i == len(segs)-1 && !strings.HasSuffix(glob, "/") {
			break // the last static segment is a file name, not a directory
		}
		if s != "" {
			dir = append(dir, s)
		}
	}
	if len(dir) == 0 {
		return "", false
	}
	return strings.Join(dir, "/"), true
}
