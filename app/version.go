package app

// Version is the build's version: what the updater compares, what support bundles and project exports name (core.AppVersion),
// and the FileVersion and ProductVersion of drydock.exe's version resource. A release build sets all of them from one value:
// `task build:windows VERSION=1.2.3` passes it to go-winres (--product-version, --file-version) and to the linker (-ldflags
// "-X github.com/kjmaguire/drydock/app.Version=1.2.3"). A build given none has this default, which the committed resource
// definition (build/windows/winres.json) carries too; a test keeps the two equal (docs/windows-identity.md).
var Version = "0.0.0-dev"
