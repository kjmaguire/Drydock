package core

import "testing"

func TestParseCloneProgress(t *testing.T) {
	for _, c := range []struct {
		line  string
		phase string
		pct   int
		ok    bool
	}{
		{"Receiving objects:  42% (21/50), 1.20 MiB | 3.00 MiB/s", "Receiving objects", 42, true},
		{"remote: Counting objects: 100% (50/50), done.", "Counting objects", 100, true},
		{"Resolving deltas:   7% (1/14)", "Resolving deltas", 7, true},
		{"Updating files: 100% (12/12), done.", "Updating files", 100, true},
		{"Cloning into 'gocalc'...", "", 0, false},
		{"fatal: repository not found", "", 0, false},
		{"Receiving objects: 420% (1/1)", "", 0, false},
	} {
		phase, pct, ok := parseCloneProgress(c.line)
		if phase != c.phase || pct != c.pct || ok != c.ok {
			t.Fatalf("%q: %q %d %v", c.line, phase, pct, ok)
		}
	}
	var lines []string
	data := []byte("Receiving objects:  10% (1/10)\rReceiving objects:  20% (2/10)\rdone\n")
	for len(data) > 0 {
		n, tok, _ := splitProgress(data, true)
		lines = append(lines, string(tok))
		data = data[n:]
	}
	if len(lines) != 3 || lines[1] != "Receiving objects:  20% (2/10)" {
		t.Fatalf("%q", lines)
	}
}
