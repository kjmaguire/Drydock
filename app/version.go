package app

// Version is the build's version: what the updater compares, what support bundles and project exports name (core.AppVersion),
// and the FileVersion and ProductVersion of drydock.exe's version resource. A release build sets all of them from one value:
// `task build:windows VERSION=1.2.3` passes it to go-winres (--product-version, --file-version) and to the linker (-ldflags
// "-X github.com/kjmaguire/drydock/app.Version=1.2.3"). A build given none has this default, which the committed resource
// definition (build/windows/winres.json) carries too; a test keeps the two equal (docs/windows-identity.md).
var Version = "0.0.0-dev"

// DebugPort, when a build sets it (-ldflags "-X github.com/kjmaguire/drydock/app.DebugPort=9333"), opens WebView2's remote
// debugging on 127.0.0.1 at that port, so the release workflow's tests can drive the installed app's own window
// (ui/e2e-installed). Release builds never set it: Wails clears WebView2's own environment switches
// (WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS), so without this no other program can attach to the window, and the release
// workflow checks that the shipped exe opens no debugging port.
var DebugPort = ""
