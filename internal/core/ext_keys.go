package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// A person's keyboard shortcuts (decision P132). The UI's keyboard map (ui/app/lib/keys.ts KEYMAP) names the shortcuts and
// their default keys; a person binds one to other keys in Settings, and the core keeps the bindings that differ from the
// defaults for the workspace, in the settings table, so every window of it uses them. The same rules as the page's: a key the
// map can name, held with Ctrl or Alt (a plain key would fire while typing), nothing the window or the text fields keep, and
// never two shortcuts on one combination.

const settingKeyBindings = "ui.keybindings"

// shortcutDefaults are the shortcuts a person can bind, with their default keys: the lines of KEYMAP that carry an id
// (TestTheCoresShortcutsAreTheKeyboardMaps checks the two against each other).
var shortcutDefaults = map[string]string{
	"palette": "Ctrl+K", "inbox": "Ctrl+Shift+K", "sidebar": "Ctrl+B",
	"focus-sidebar": "Ctrl+1", "focus-main": "Ctrl+2", "focus-details": "Ctrl+3", "focus-dock": "Ctrl+4",
	"dock": "Ctrl+J", "terminal": "Ctrl+`", "diff": "Ctrl+Shift+D", "inspect": "Ctrl+.",
	"room-prev": "Alt+Up", "room-next": "Alt+Down", "new-chat": "Ctrl+N",
}

// reservedCombos are the window's own keys (WebView2 acts on them before the page: docs/ui.md 14), the editing keys every field
// needs, and the keys the message boxes send with (lib/keys.ts RESERVED).
var reservedCombos = []string{"Ctrl+R", "Ctrl+Shift+R", "F5", "F12", "Ctrl+Shift+I", "Ctrl+Shift+J", "Ctrl+P", "Ctrl+F", "Ctrl+W", "Alt+F4",
	"Ctrl+A", "Ctrl+C", "Ctrl+V", "Ctrl+X", "Ctrl+Y", "Ctrl+Z", "Ctrl+Enter"}

// comboKey is a key the map can name: a letter, a digit, a punctuation key, an arrow, Home, End, Page Up or Down, F1 to F12.
var comboKey = regexp.MustCompile(`^([A-Z0-9` + "`" + `.,/;'\[\]\-=\\]|Up|Down|Left|Right|Home|End|PageUp|PageDown|F([1-9]|1[0-2]))$`)

// KeyBindings is what keys.get and keys.set answer: the shortcuts bound to other keys than their defaults.
type KeyBindings struct {
	Bindings map[string]string `json:"bindings"`
}

func init() {
	RegisterExtension(Extension{Name: "keys", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"keys.get": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.KeyBindings(ctx), nil }),
			// keys.set replaces the bindings (an id left out goes back to its default; {} resets them all)
			"keys.set": bind(func(ctx context.Context, p KeyBindings) (any, error) { return c.SetKeyBindings(ctx, p.Bindings) }),
		}, nil, nil
	}})
}

// KeyBindings is the workspace's bindings (none when nothing was bound, or what is kept does not read).
func (c *Core) KeyBindings(ctx context.Context) KeyBindings {
	out := KeyBindings{Bindings: map[string]string{}}
	var v string
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingKeyBindings).Scan(&v); err != nil {
		return out
	}
	var kept map[string]string
	if json.Unmarshal([]byte(v), &kept) != nil {
		return out
	}
	for id, combo := range kept {
		if def, ok := shortcutDefaults[id]; ok && combo != def {
			out.Bindings[id] = combo
		}
	}
	return out
}

// SetKeyBindings checks and keeps the bindings, and tells the other windows (keys.changed).
func (c *Core) SetKeyBindings(ctx context.Context, b map[string]string) (KeyBindings, error) {
	clean := map[string]string{}
	for id, combo := range b {
		def, ok := shortcutDefaults[id]
		if !ok {
			return KeyBindings{}, fmt.Errorf("there is no shortcut %q to bind", id)
		}
		if combo = strings.TrimSpace(combo); combo != "" && combo != def {
			clean[id] = combo
		}
	}
	effective := map[string]string{}
	for id, def := range shortcutDefaults {
		effective[id] = def
	}
	for id, combo := range clean {
		effective[id] = combo
	}
	for _, id := range sortedKeys(clean) {
		if why := comboProblem(clean[id]); why != "" {
			return KeyBindings{}, fmt.Errorf("%s: %s", id, why)
		}
		for _, other := range sortedKeys(effective) {
			if other != id && effective[other] == clean[id] {
				return KeyBindings{}, fmt.Errorf("%s: %s is already the keys of %s", id, clean[id], other)
			}
		}
	}
	v, err := json.Marshal(clean)
	if err != nil {
		return KeyBindings{}, err
	}
	if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingKeyBindings, string(v))
		return err
	}); err != nil {
		return KeyBindings{}, err
	}
	c.publish(ctx, "keys.changed", "", map[string]any{"count": len(clean), "by": c.Human()})
	return KeyBindings{Bindings: clean}, nil
}

// comboProblem says why a combination cannot be a binding ("" when it can): its modifiers in the map's order (Ctrl, Alt,
// Shift), Ctrl or Alt among them, a key the map can name, nothing reserved.
func comboProblem(combo string) string {
	parts := strings.Split(combo, "+")
	if strings.HasSuffix(combo, "+") || len(parts) < 2 {
		return "hold Ctrl or Alt with a letter, a digit, a punctuation key, an arrow or F1 to F12"
	}
	key, mods := parts[len(parts)-1], parts[:len(parts)-1]
	order := []string{"Ctrl", "Alt", "Shift"}
	last := -1
	for _, m := range mods {
		i := slices.Index(order, m)
		if i <= last {
			return fmt.Sprintf("%s is not written as Ctrl, Alt and Shift, in that order, then the key", combo)
		}
		last = i
	}
	switch {
	case !comboKey.MatchString(key):
		return fmt.Sprintf("%s is not a key the keyboard map can name", key)
	case !slices.Contains(mods, "Ctrl") && !slices.Contains(mods, "Alt"):
		return "hold Ctrl or Alt with it, so it never fires while typing"
	case slices.Contains(reservedCombos, combo):
		return fmt.Sprintf("%s is kept by the window or by the text fields", combo)
	}
	return ""
}
