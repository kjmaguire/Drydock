package claude

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Claude Code started with --setting-sources user (Adapter.UserSettingsOnly, decision P16: a repository's own settings could
// run hooks or allow tools the human never approved) also skips the project's memory files: CLAUDE.md, the AGENTS.md it
// imports and .claude/rules (measured with 2.1.296: asked for a codename in AGENTS.md it answered UNKNOWN, with
// --setting-sources user,project it answered). Those files carry what Drydock generates for the agent (accepted memory,
// standing instructions, attached packs, skills as rules) and the repository's own instructions, so the adapter reads them
// from the working folder and appends them to the system prompt instead. Settings files (hooks, permissions) are never read.

const (
	maxMemoryFile  = 64 << 10  // a larger instruction file is cut with a note
	maxMemoryTotal = 256 << 10 // all of them together
	maxImportDepth = 4         // CLAUDE.md -> @AGENTS.md -> ... (Claude Code's own limit is 5)
)

// projectMemory returns the project's instruction files in the working folder as one text for the system prompt, or "".
func projectMemory(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	root, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	r := &memReader{root: root, seen: map[string]bool{}}
	var parts []string
	for _, name := range []string{"CLAUDE.md", filepath.Join(".claude", "CLAUDE.md"), "CLAUDE.local.md"} {
		if t := r.file(filepath.Join(root, name), 0); t != "" {
			parts = append(parts, "## "+filepath.ToSlash(name)+"\n\n"+t)
		}
	}
	if rules, err := filepath.Glob(filepath.Join(root, ".claude", "rules", "*.md")); err == nil {
		sort.Strings(rules)
		for _, f := range rules {
			if t := r.file(f, 0); t != "" {
				rel, _ := filepath.Rel(root, f)
				parts = append(parts, "## "+filepath.ToSlash(rel)+"\n\n"+t)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "# Project instructions\n\nThe project's own instruction files from your working folder follow (Drydock passes them here because it starts " +
		"Claude Code with your user settings only). Follow them as you would a CLAUDE.md.\n\n" + strings.Join(parts, "\n\n")
}

type memReader struct {
	root  string
	seen  map[string]bool
	total int
}

// file reads one instruction file, replacing each line that is only an @import of a file inside the working folder with
// that file's text. Imports outside the folder (a home directory, ../) are left as they are: a repository must not pull in
// files from elsewhere on the machine.
func (r *memReader) file(path string, depth int) string {
	clean := filepath.Clean(path)
	if r.seen[clean] || r.total >= maxMemoryTotal || !r.inside(clean) {
		return ""
	}
	st, err := os.Stat(clean)
	if err != nil || !st.Mode().IsRegular() {
		return ""
	}
	r.seen[clean] = true
	b, err := os.ReadFile(clean)
	if err != nil {
		return ""
	}
	text := string(b)
	if len(text) > maxMemoryFile {
		text = text[:maxMemoryFile] + "\n[cut: the file is longer than 64 KB]"
	}
	if room := maxMemoryTotal - r.total; len(text) > room {
		text = text[:room] + "\n[cut: the instruction files are longer than 256 KB together]"
	}
	r.total += len(text)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if depth >= maxImportDepth || !strings.HasPrefix(t, "@") || strings.ContainsAny(t, " \t") || len(t) < 2 {
			continue
		}
		target := filepath.Join(filepath.Dir(clean), filepath.FromSlash(t[1:]))
		if !r.inside(target) {
			continue
		}
		if sub := r.file(target, depth+1); sub != "" {
			lines[i] = sub
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (r *memReader) inside(p string) bool {
	rel, err := filepath.Rel(r.root, filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
