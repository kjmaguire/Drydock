//go:build windows

package app

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

// OpenEventName is the Wails event that tells a running page to open a route (ui/app/lib/shell-open.ts): a click on a
// notification.
const OpenEventName = "drydock:open"

// toasts is Drydock's Windows notifications (decision G1): Wails's notifications service, started with the app. It is wrapped so
// that none of the service's own methods (SendNotification and the rest) are bound for the page: only the shell raises a
// notification, from a needsyou.added event's words. It is registered only in the Windows build, so a Linux build or test never
// needs D-Bus.
//
// What the service writes, all per user (HKCU, no administrator rights), and the uninstaller removes
// (packaging/windows/drydock.nsi): Software\Classes\AppUserModelId\Drydock (DisplayName, IconUri, CustomActivator: the GUID of
// the app's activation class, made once) and Software\Classes\CLSID\{that GUID}\LocalServer32 (drydock.exe, so a click on a
// notification left in the notification centre starts Drydock). The toasts' identity is the app's Name, "Drydock", which the
// registry entry gives Drydock's name and icon (the icon is written to %TEMP%\Drydock{GUID}.png); the window's taskbar identity
// stays AppUserModelID. The service's shutdown is not called: it would only write an empty list of notification categories under
// HKCU\Software\Drydock, which Drydock does not use.
type toasts struct {
	ns *notifications.NotificationService

	mu    sync.Mutex
	ready bool
}

func newToasts() *toasts { return &toasts{ns: notifications.New()} }

// ServiceName names the service in Wails's logs.
func (t *toasts) ServiceName() string { return "drydock-notifications" }

// ServiceStartup starts Wails's service. A failure (a registry write refused, COM not answering) is said and is not a reason not
// to start: the taskbar button still asks for attention.
func (t *toasts) ServiceStartup(ctx context.Context, o application.ServiceOptions) error {
	if err := t.ns.ServiceStartup(ctx, o); err != nil {
		fmt.Fprintf(os.Stderr, "drydock: Windows notifications: %v (the taskbar button still asks for attention)\n", err)
		return nil
	}
	t.mu.Lock()
	t.ready = true
	t.mu.Unlock()
	return nil
}

// show raises one notification; false when the service did not start or Windows refused it.
func (t *toasts) show(n toastNote) bool {
	t.mu.Lock()
	ready := t.ready
	t.mu.Unlock()
	if !ready {
		return false
	}
	opts := notifications.NotificationOptions{ID: n.ID, Title: n.Title, Body: n.Body, Data: map[string]any{routeKey: n.Route}}
	if n.Silent {
		opts.Sound = &notifications.NotificationSound{Silent: true}
	}
	if err := t.ns.SendNotification(opts); err != nil {
		fmt.Fprintf(os.Stderr, "drydock: Windows notification: %v\n", err)
		return false
	}
	return true
}

// onClick calls open with the page a click on a notification asks for (clickRoute: checked, the Inbox when unreadable).
func (t *toasts) onClick(open func(route string)) {
	t.ns.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			open(inboxRoute)
			return
		}
		open(clickRoute(r.Response.UserInfo))
	})
}

// watchNeedsYou tells the person when something new needs them while the window is behind (decision G1, toastFor): the taskbar
// button asks for attention (Windows stops the flash when the window comes to the front) and a Windows notification shows the
// event's words, with Windows's sound unless the person turned "Play a sound" off.
func watchNeedsYou(ctx context.Context, c *core.Core, window *application.WebviewWindow, t *toasts) {
	events.Watch(ctx, c.Bus, events.Filter{Types: []string{core.EventNeedsYouAdded}}, func(e events.Event) {
		if window == nil {
			return
		}
		n, ok := toastFor(e, c.NotifyPrefs(ctx), window.IsFocused())
		if !ok {
			return
		}
		window.Flash(true)
		if t != nil {
			t.show(n)
		}
	})
}

// frontOf is the window as windowToFront sees it (Wails's Show returns the window, which windowToFront does not need).
type frontOf struct{ *application.WebviewWindow }

func (w frontOf) Show() { w.WebviewWindow.Show() }

// openFromNotification brings the window to the front and opens route: a running page is told on OpenEventName (the router
// opens it without a reload); a page that is not running yet (Drydock was started by the click) is given the address instead.
func openFromNotification(wa *application.App, window *application.WebviewWindow, bridge *Bridge, route string) {
	if window == nil {
		return
	}
	windowToFront(frontOf{window})
	if bridge != nil && bridge.subscribed() {
		wa.Event.Emit(OpenEventName, route)
		return
	}
	window.SetURL(route)
}
