package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var aliases = map[string]string{"@playwright/test": "Playwright", "go": "Go", "npm:react": "React"}

// docDrift compares the versions docs/decisions.md names with the ones the code pins, so a stale sentence in the documentation
// shows up here instead of misleading the next reader.
func docDrift(items []Item) []DocDrift {
	b, err := os.ReadFile(filepath.Join(root, "docs", "decisions.md"))
	if err != nil {
		return nil
	}
	text := string(b)
	var out []DocDrift
	for _, it := range items {
		if it.Kind != "direct" && it.Kind != "dev" && it.ID != "go" {
			continue
		}
		name := it.Name
		if a, ok := aliases[it.ID]; ok {
			name = a
		} else if a, ok := aliases[name]; ok {
			name = a
		}
		name = strings.TrimPrefix(name, "npm:")
		// last path element without a trailing /vN
		parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
		last := parts[len(parts)-1]
		if regexp.MustCompile(`^v\d+$`).MatchString(last) && len(parts) > 1 {
			last = parts[len(parts)-2]
		}
		re := regexp.MustCompile("(?i)(?:^|[^\\w-])" + regexp.QuoteMeta(last) + "[` ]{1,3}v?(\\d+\\.\\d+\\.\\d+)")
		m := re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		if m[1] != strings.TrimPrefix(it.Used, "v") && !strings.HasPrefix(it.Used, m[1]) {
			out = append(out, DocDrift{Name: it.Name, Docs: m[1], Code: it.Used, Ref: "docs/decisions.md"})
		}
	}
	return out
}
