//go:build windows

package app

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// The key the uninstaller removes (TestTheUninstallerRemovesTheNotificationRegistration) is the one Wails's service writes.
func TestTheNotificationIdentityIsWhereWailsWritesIt(t *testing.T) {
	if got, want := notifications.ToastRegistryPath+AppName, `Software\Classes\AppUserModelId\`+AppName; got != want {
		t.Fatalf("Wails writes %q, the uninstaller removes %q", got, want)
	}
	if notifications.ToastRegistryGuidKey != "CustomActivator" {
		t.Fatalf("Wails keeps the activation class in %q, the uninstaller reads CustomActivator", notifications.ToastRegistryGuidKey)
	}
}
