package app

import (
	"encoding/json"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

func TestTheTaskbarAsksForAttentionOnlyForANewItemWhileTheWindowIsBehind(t *testing.T) {
	data, _ := json.Marshal(core.NeedsYouNote{Key: "watchdog:1", Kind: "watchdog_pause", Title: "A task was paused"})
	added := events.Event{Type: core.EventNeedsYouAdded, Data: data}
	on, off := core.NotifyPrefs{NeedsYou: true}, core.NotifyPrefs{}
	for _, c := range []struct {
		name    string
		e       events.Event
		prefs   core.NotifyPrefs
		focused bool
		want    bool
	}{
		{"a new item, the window behind", added, on, false, true},
		{"the window in front", added, on, true, false},
		{"the switch off", added, off, false, false},
		{"another event", events.Event{Type: "task.completed", Data: data}, on, false, false},
		{"an item without a key", events.Event{Type: core.EventNeedsYouAdded, Data: []byte(`{}`)}, on, false, false},
	} {
		if got := wantsAttention(c.e, c.prefs, c.focused); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
