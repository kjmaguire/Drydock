//go:build windows

package app

import (
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"

	"github.com/kjmaguire/drydock/internal/winident"
)

// The window's icons (docs/windows-identity.md). Wails gives every window the icon group with resource ID 3 of the executable,
// loaded once with LoadIcon at the system's large-icon size and set as the big icon only; the title bar then shows that icon
// shrunk by Windows, not the drawn 16 and 20 pixel images, and a window on a monitor with another scale keeps the size of the
// first. So when the window is shown, and again when its DPI changes, both of its icons are loaded from the same resource at
// the sizes that DPI asks for: the small one for the title bar, the big one for the taskbar button and Alt-Tab. A build with
// no resources (go run, a test build) has no icon 3 and keeps what Wails gave it.
var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	procLoadImageW             = user32.NewProc("LoadImageW")
	procSendMessageW           = user32.NewProc("SendMessageW")
	procDestroyIcon            = user32.NewProc("DestroyIcon")
	procGetDpiForWindow        = user32.NewProc("GetDpiForWindow")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procGetSystemMetricsForDpi = user32.NewProc("GetSystemMetricsForDpi")
)

const (
	wmSetIcon   = 0x0080
	iconSmall   = 0
	iconBig     = 1
	imageIcon   = 1
	smCxIcon    = 11
	smCxSmIcon  = 49
	defaultDPI  = 96
	lrDefault   = 0
	noIconError = 0
)

// windowIcons holds the icons this package loaded for one window, destroyed once they are replaced.
type windowIcons struct {
	mu         sync.Mutex
	small, big uintptr
}

// attachWindowIcons sets the window's icons when it is shown and whenever its DPI changes.
func attachWindowIcons(w *application.WebviewWindow) {
	icons := &windowIcons{}
	apply := func(*application.WindowEvent) {
		// window event listeners run on their own goroutine; the window belongs to the main thread
		application.InvokeSync(func() { icons.apply(uintptr(w.NativeWindow())) })
	}
	w.OnWindowEvent(events.Windows.WindowShow, apply)
	w.OnWindowEvent(events.Windows.WindowDPIChanged, apply)
}

func (ic *windowIcons) apply(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	ic.mu.Lock()
	defer ic.mu.Unlock()
	dpi := uintptr(defaultDPI)
	if procGetDpiForWindow.Find() == nil {
		if d, _, _ := procGetDpiForWindow.Call(hwnd); d != 0 {
			dpi = d
		}
	}
	if small := loadAppIcon(metric(smCxSmIcon, dpi)); small != noIconError {
		procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, small) //nolint:errcheck // SendMessage reports through its result, which WM_SETICON does not have
		destroyIcon(ic.small)
		ic.small = small
	}
	if big := loadAppIcon(metric(smCxIcon, dpi)); big != noIconError {
		procSendMessageW.Call(hwnd, wmSetIcon, iconBig, big) //nolint:errcheck // as above
		destroyIcon(ic.big)
		ic.big = big
	}
}

// metric is a system icon size in pixels at the window's DPI (GetSystemMetricsForDpi is Windows 10 1607 and newer).
func metric(index, dpi uintptr) uintptr {
	if procGetSystemMetricsForDpi.Find() == nil {
		if v, _, _ := procGetSystemMetricsForDpi.Call(index, dpi); v != 0 {
			return v
		}
	}
	v, _, _ := procGetSystemMetrics.Call(index)
	return v
}

// loadAppIcon loads the executable's icon group winident.IconResourceID at size x size pixels; Windows picks the nearest drawn
// size in the group. It returns 0 when the executable has no such icon.
func loadAppIcon(size uintptr) uintptr {
	var module windows.Handle
	// the executable itself (no name), without taking a reference on it
	if err := windows.GetModuleHandleEx(windows.GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT, nil, &module); err != nil || size == 0 {
		return noIconError
	}
	// MAKEINTRESOURCE: the ID in place of a name pointer
	h, _, _ := procLoadImageW.Call(uintptr(module), uintptr(winident.IconResourceID), imageIcon, size, size, lrDefault)
	return h
}

func destroyIcon(h uintptr) {
	if h != 0 {
		procDestroyIcon.Call(h) //nolint:errcheck // nothing to do when an icon cannot be freed
	}
}
