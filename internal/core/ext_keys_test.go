package core_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

// A person binds a shortcut to other keys (decision P132): the binding is kept for the workspace and survives a restart; one
// bound to its default keys is not kept; an unknown shortcut, a key the map cannot name, a plain key, the modifiers out of
// order, a key the window keeps and a combination another shortcut has (by default or bound) are each refused with the reason,
// and nothing changes; {} resets them all.
func TestAPersonBindsAShortcutAndTheCoreKeepsIt(t *testing.T) {
	a := open(t, "")
	var b core.KeyBindings
	b = core.KeyBindings{}
	a.call("keys.get", nil, &b)
	if len(b.Bindings) != 0 {
		t.Fatalf("before any binding: %+v", b)
	}
	b = core.KeyBindings{}
	a.call("keys.set", map[string]any{"bindings": map[string]string{"inbox": "Ctrl+Alt+I", "palette": "Ctrl+K", "terminal": "Alt+`"}}, &b)
	if len(b.Bindings) != 2 || b.Bindings["inbox"] != "Ctrl+Alt+I" || b.Bindings["terminal"] != "Alt+`" {
		t.Fatalf("kept %+v", b)
	}
	for name, c := range map[string]struct {
		bindings map[string]string
		want     string
	}{
		"an unknown shortcut":        {map[string]string{"launch": "Ctrl+L"}, `no shortcut "launch"`},
		"a key it cannot name":       {map[string]string{"inbox": "Ctrl+Space"}, "not a key the keyboard map can name"},
		"a plain key":                {map[string]string{"inbox": "Shift+I"}, "hold Ctrl or Alt"},
		"the modifiers reversed":     {map[string]string{"inbox": "Shift+Ctrl+I"}, "Ctrl, Alt and Shift, in that order"},
		"a key the window keeps":     {map[string]string{"inbox": "Ctrl+R"}, "kept by the window"},
		"another's default keys":     {map[string]string{"inbox": "Ctrl+B"}, "already the keys of sidebar"},
		"two on the same keys":       {map[string]string{"inbox": "Ctrl+Alt+X", "dock": "Ctrl+Alt+X"}, "Ctrl+Alt+X is already the keys of"},
		"a default taken by another": {map[string]string{"inbox": "Ctrl+J"}, "already the keys of dock"},
	} {
		if err := a.callErr("keys.set", map[string]any{"bindings": c.bindings}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	// a shortcut moved off its default frees those keys for another
	b = core.KeyBindings{}
	a.call("keys.set", map[string]any{"bindings": map[string]string{"dock": "Ctrl+Alt+J", "inbox": "Ctrl+J"}}, &b)
	if b.Bindings["inbox"] != "Ctrl+J" || b.Bindings["dock"] != "Ctrl+Alt+J" {
		t.Fatalf("swapped %+v", b)
	}

	dir := a.dataDir()
	a.c.Close()
	r := openAt(t, dir)
	b = core.KeyBindings{}
	r.call("keys.get", nil, &b)
	if len(b.Bindings) != 2 || b.Bindings["inbox"] != "Ctrl+J" {
		t.Fatalf("after a restart %+v", b)
	}
	b = core.KeyBindings{}
	r.call("keys.set", map[string]any{"bindings": map[string]string{}}, &b)
	b = core.KeyBindings{}
	r.call("keys.get", nil, &b)
	if len(b.Bindings) != 0 {
		t.Fatalf("after a reset %+v", b)
	}
}

// The core's shortcuts and defaults are the keyboard map's (ui/app/lib/keys.ts KEYMAP, the lines with an id), and its reserved
// keys the page's RESERVED: a shortcut added on one side only would be refused by the core or bound to keys the page never
// checks.
func TestTheCoresShortcutsAreTheKeyboardMaps(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "ui", "app", "lib", "keys.ts"))
	if err != nil {
		t.Fatal(err)
	}
	lines := regexp.MustCompile(`\{ id: '([a-z-]+)', keys: \[([^\]]*)\]`).FindAllStringSubmatch(string(src), -1)
	page := map[string]string{}
	for _, l := range lines {
		var keys []string
		for _, k := range regexp.MustCompile(`'((?:[^'\\]|\\.)*)'|"([^"]*)"`).FindAllStringSubmatch(l[2], -1) {
			keys = append(keys, strings.ReplaceAll(k[1]+k[2], `\\`, `\`))
		}
		page[l[1]] = strings.Join(keys, "+")
	}
	if got := core.ShortcutDefaults(); len(page) == 0 || len(got) != len(page) {
		t.Fatalf("the core has %d shortcuts, the map %d: %v %v", len(got), len(page), got, page)
	}
	for id, combo := range core.ShortcutDefaults() {
		if page[id] != combo {
			t.Errorf("%s: the core says %q, the map %q", id, combo, page[id])
		}
	}
	m := regexp.MustCompile(`(?s)export const RESERVED: readonly string\[\] = \[(.*?)\];`).FindStringSubmatch(string(src))
	if m == nil {
		t.Fatal("no RESERVED in keys.ts")
	}
	var reserved []string
	for _, k := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(m[1], -1) {
		reserved = append(reserved, k[1])
	}
	if strings.Join(reserved, ",") != strings.Join(core.ReservedCombos(), ",") {
		t.Fatalf("reserved: the page %v, the core %v", reserved, core.ReservedCombos())
	}
}
