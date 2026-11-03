package app

import (
	"encoding/json"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

// wantsAttention says whether an event should make the taskbar button ask for attention (decision G1): a new Inbox item
// (core.EventNeedsYouAdded), the person's switch on, and the window not in front (in front, the window's own count and toast say
// it). Pure, so it is tested on any OS; notify_windows.go acts on it.
func wantsAttention(e events.Event, prefs core.NotifyPrefs, focused bool) bool {
	if e.Type != core.EventNeedsYouAdded || !prefs.NeedsYou || focused {
		return false
	}
	var n core.NeedsYouNote
	return json.Unmarshal(e.Data, &n) == nil && n.Key != ""
}
