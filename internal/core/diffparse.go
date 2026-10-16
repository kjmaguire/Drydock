package core

import (
	"strconv"
	"strings"
)

// DiffFile is one file of a unified diff, as the diff viewer lists it.
type DiffFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"` // added | deleted | modified | renamed
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
	hunks     []string
}

// parseUnifiedDiff splits the output of `git diff` into files. It reads only what git writes (diff --git headers, mode,
// rename and binary lines, hunks); anything it does not recognise is skipped, never guessed.
func parseUnifiedDiff(patch string) []DiffFile {
	var out []DiffFile
	var cur *DiffFile
	var hunk *strings.Builder
	flush := func() {
		if cur != nil && hunk != nil {
			cur.hunks = append(cur.hunks, hunk.String())
		}
		hunk = nil
	}
	for _, l := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			flush()
			out = append(out, DiffFile{Status: "modified"})
			cur = &out[len(out)-1]
			a, b := splitGitPaths(strings.TrimPrefix(l, "diff --git "))
			cur.OldPath, cur.Path = a, b
		case cur == nil:
		case hunk == nil && strings.HasPrefix(l, "new file mode"):
			cur.Status = "added"
		case hunk == nil && strings.HasPrefix(l, "deleted file mode"):
			cur.Status = "deleted"
		case hunk == nil && strings.HasPrefix(l, "rename from "):
			cur.Status, cur.OldPath = "renamed", unquotePath(strings.TrimPrefix(l, "rename from "))
		case hunk == nil && strings.HasPrefix(l, "rename to "):
			cur.Path = unquotePath(strings.TrimPrefix(l, "rename to "))
		case hunk == nil && strings.HasPrefix(l, "Binary files ") && strings.HasSuffix(l, " differ"):
			cur.Binary = true
		case hunk == nil && strings.HasPrefix(l, "--- "):
			if p := stripPrefix(strings.TrimPrefix(l, "--- "), "a/"); p != "" {
				cur.OldPath = p
			}
		case hunk == nil && strings.HasPrefix(l, "+++ "):
			if p := stripPrefix(strings.TrimPrefix(l, "+++ "), "b/"); p != "" {
				cur.Path = p
			}
		case strings.HasPrefix(l, "@@"):
			flush()
			hunk = &strings.Builder{}
			hunk.WriteString(l + "\n")
		case hunk != nil:
			hunk.WriteString(l + "\n")
			if strings.HasPrefix(l, "+") {
				cur.Additions++
			} else if strings.HasPrefix(l, "-") {
				cur.Deletions++
			}
		}
	}
	flush()
	for i := range out {
		f := &out[i]
		if f.Status != "renamed" && f.OldPath == f.Path {
			f.OldPath = ""
		}
		if f.Status == "deleted" && f.Path == "" {
			f.Path = f.OldPath
		}
	}
	return out
}

// splitGitPaths splits the "a/x b/y" of a diff --git header. Names with spaces are ambiguous there, so the ---/+++ and rename
// lines, which follow, override this guess.
func splitGitPaths(s string) (string, string) {
	if strings.HasPrefix(s, `"`) {
		if a, rest, ok := cutQuoted(s); ok {
			return stripPrefix(a, "a/"), stripPrefix(strings.TrimSpace(rest), "b/")
		}
	}
	if i := strings.Index(s, " b/"); i >= 0 {
		return stripPrefix(s[:i], "a/"), stripPrefix(s[i+1:], "b/")
	}
	return s, s
}

func cutQuoted(s string) (string, string, bool) {
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			u, err := strconv.Unquote(s[:i+1])
			return u, s[i+1:], err == nil
		}
	}
	return "", "", false
}

func unquotePath(s string) string {
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

func stripPrefix(s, prefix string) string {
	s = unquotePath(strings.TrimSpace(s))
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, prefix)
}

// fromHunks rebuilds the two sides of a file from its hunks alone, for a recorded diff whose commits git no longer has. Only
// the changed regions and their context are known; the gaps are marked, never filled in.
func fromHunks(hunks []string) (original, modified string) {
	var o, m strings.Builder
	for i, h := range hunks {
		lines := strings.Split(strings.TrimSuffix(h, "\n"), "\n")
		if i > 0 {
			o.WriteString("⋯\n")
			m.WriteString("⋯\n")
		}
		for _, l := range lines[1:] {
			switch {
			case strings.HasPrefix(l, "+"):
				m.WriteString(l[1:] + "\n")
			case strings.HasPrefix(l, "-"):
				o.WriteString(l[1:] + "\n")
			case strings.HasPrefix(l, `\`): // "\ No newline at end of file"
			default:
				if len(l) > 0 {
					l = l[1:]
				}
				o.WriteString(l + "\n")
				m.WriteString(l + "\n")
			}
		}
	}
	return o.String(), m.String()
}
