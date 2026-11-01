package app

// AppUserModelID is Drydock's Windows application identity (docs/windows-identity.md section 6). Windows groups a running
// window with the taskbar button and Start menu shortcut that carry the same ID, and needs one before it shows the app's own
// notifications or jump list. The app sets it on its process at start (appid_windows.go) and the installer writes the same
// value on the Start menu shortcut (packaging/windows/drydock.nsi, APPID); a test keeps the two equal, because a pinned
// shortcut and a running window with different IDs show as two taskbar buttons. The form is Microsoft's
// CompanyName.ProductName; it must never change once released, or pinned shortcuts stop grouping with the app.
const AppUserModelID = "Drydock.Drydock"
