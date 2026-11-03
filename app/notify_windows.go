//go:build windows

package app

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

// watchNeedsYou makes the taskbar button ask for attention when something new needs the person while the window is behind
// (decision G1, wantsAttention). Windows stops the flash when the window comes to the front. A Windows notification (a toast)
// is not raised: Wails's notifications service brings in a module Drydock does not carry yet.
func watchNeedsYou(ctx context.Context, c *core.Core, window *application.WebviewWindow) {
	events.Watch(ctx, c.Bus, events.Filter{Types: []string{core.EventNeedsYouAdded}}, func(e events.Event) {
		if window == nil {
			return
		}
		if wantsAttention(e, c.NotifyPrefs(ctx), window.IsFocused()) {
			window.Flash(true)
		}
	})
}
