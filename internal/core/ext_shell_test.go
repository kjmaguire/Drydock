package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
)

// What the shell may be asked to open (audit 2026-10-16, M23): the system opens anything it has a handler for, so only a web
// address with a host and no password, and a plain mail address, get through. Every address below is one a link in agent text or
// a page could carry.
func TestOnlyAWebOrMailAddressIsHandedToTheShell(t *testing.T) {
	good := map[string]string{
		"https://example.com/a?b=c#d":     "https://example.com/a?b=c#d",
		"  HTTP://Example.com:8080/x  ":   "http://Example.com:8080/x",
		"https://127.0.0.1:3000/":         "https://127.0.0.1:3000/",
		"mailto:someone@example.com":      "mailto:someone@example.com",
		"https://example.com/a%20b":       "https://example.com/a%20b",
		"https://example.com/ü":           "https://example.com/%C3%BC",
		"https://[::1]:8080/ok":           "https://[::1]:8080/ok",
		"https://example.com/p?x=1&y=2#z": "https://example.com/p?x=1&y=2#z",
	}
	for in, want := range good {
		got, err := core.ExternalURL(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "   ",
		`C:\Windows\System32\calc.exe`, `\\server\share\run.exe`, "/etc/passwd", "calc.exe", "file:///C:/Windows/System32/calc.exe",
		"ms-msdt:/id PCWDiagnostic", "javascript:alert(1)", "data:text/html,<script>alert(1)</script>", "vbscript:x", "ftp://example.com/x",
		"search-ms:query=x", "shell:startup", "steam://run/1", "tel:+15551234", "sms:+15551234",
		"https://user:pass@example.com/", "https://user@example.com/", "https:///nohost", "https://", "http:example.com", "//example.com/x",
		"https://example.com/\x00", "https://example.com/a\nb", "https://example.com/a\tb", "https://exa\x7fmple.com/", `https://example.com\evil.exe`,
		"mailto:", "mailto:a@b.com?attach=C:\\secret.txt", "mailto:a@b.com?subject=x", "mailto:a b@c.com", "mailto://x@y.com", "mailto:<a>@b.com",
		"https://example.com/" + strings.Repeat("a", core.MaxExternalURL),
		"https://example.com/\xff\xfe",
	}
	for _, in := range bad {
		if got, err := core.ExternalURL(in); err == nil {
			t.Errorf("%q was accepted as %q", in, got)
		}
	}
}

// The core hands an address to the shell (Config.OpenURL) only for a person, only after the check, and says plainly when the
// build has no shell to hand it to.
func TestAnAddressIsOpenedInThePersonsBrowserOnlyAfterTheCheck(t *testing.T) {
	var opened []string
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		// TEST DOUBLE: the shell's way of opening an address in the default browser
		OpenURL: func(_ context.Context, u string) error { opened = append(opened, u); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var got map[string]bool
	a.call("shell.openExternal", map[string]string{"url": "https://example.com/docs"}, &got)
	if !got["opened"] || len(opened) != 1 || opened[0] != "https://example.com/docs" {
		t.Fatalf("%v %v", got, opened)
	}
	for _, bad := range []string{`C:\Windows\System32\cmd.exe`, "ms-msdt:/id x", "https://user:pw@example.com/", ""} {
		if err := a.callErr("shell.openExternal", map[string]string{"url": bad}); err == nil {
			t.Errorf("%q was handed to the shell", bad)
		}
	}
	if len(opened) != 1 {
		t.Fatalf("the shell was asked to open %v", opened)
	}
	if err := c.OpenExternal(context.Background(), "https://example.com/", "agent:agt_1"); err == nil || len(opened) != 1 {
		t.Fatalf("an agent opened an address: %v %v", err, opened)
	}

	b := open(t, "")
	if err := b.callErr("shell.openExternal", map[string]string{"url": "https://example.com/"}); err == nil || !strings.Contains(err.Error(), "cannot open another program") {
		t.Fatalf("without a shell: %v", err)
	}
}

// "Restart now" (decision P116) asks the shell to start Drydock again, only for a person; app.info says whether this window can,
// and a build without a shell says to quit and reopen. The support bundle carries the window's WebView2 version.
func TestRestartNowAsksTheShellOnlyForAPersonAndTheBundleNamesWebView2(t *testing.T) {
	restarts := 0
	dataDir := filepath.Join(t.TempDir(), "data")
	c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		// TEST DOUBLES: the shell's restart and the window's WebView2 version
		Restart:         func(context.Context) error { restarts++; return nil },
		WebView2Version: func() string { return "131.0.2903.70" },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var info map[string]any
	a.call("app.info", map[string]any{}, &info)
	if info["canRestart"] != true {
		t.Fatalf("app.info %v", info)
	}
	var got map[string]bool
	a.call("shell.restart", map[string]any{}, &got)
	if !got["restarting"] || restarts != 1 {
		t.Fatalf("%v, %d restarts", got, restarts)
	}
	if err := c.Restart(context.Background(), "agent:agt_1"); err == nil || restarts != 1 {
		t.Fatalf("an agent restarted Drydock: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	a.call("supportbundle.create", map[string]any{"dest": dest}, nil)
	found := false
	for _, body := range readZipFiles(t, dest) {
		found = found || strings.Contains(body, `"webview2": "131.0.2903.70"`) || strings.Contains(body, `"webview2":"131.0.2903.70"`)
	}
	if !found {
		t.Fatal("the support bundle does not name the WebView2 version")
	}

	b := open(t, "")
	b.call("app.info", map[string]any{}, &info)
	if info["canRestart"] != false {
		t.Fatalf("without a shell app.info says %v", info)
	}
	if err := b.callErr("shell.restart", map[string]any{}); err == nil || !strings.Contains(err.Error(), "quit and reopen") {
		t.Fatalf("without a shell: %v", err)
	}
}

// Settings > About (decision P129): app.info names the build's version, and app.licenses reads the third-party notices the
// build ships (the installer puts THIRD_PARTY_NOTICES.md beside the executable); a build without them, or a file that is not
// them, says so instead.
func TestAboutNamesTheVersionAndReadsTheShippedNotices(t *testing.T) {
	dir := t.TempDir()
	notices := filepath.Join(dir, "THIRD_PARTY_NOTICES.md")
	if err := os.WriteFile(notices, []byte("# Third-party notices\n\n| Module | License |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	open := func(file string) *app {
		c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second, NoticesFile: file})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Close)
		return &app{t: t, c: c}
	}
	a := open(notices)
	var info map[string]any
	a.call("app.info", nil, &info)
	if info["version"] != core.AppVersion || info["version"] == "" {
		t.Fatalf("app.info %v", info)
	}
	var l core.Licenses
	a.call("app.licenses", nil, &l)
	if !l.Shipped || !strings.HasPrefix(l.Text, "# Third-party notices") || l.File != notices || l.Note != "" {
		t.Fatalf("%+v", l)
	}
	l = core.Licenses{}
	open(filepath.Join(dir, "none.md")).call("app.licenses", nil, &l)
	if l.Shipped || l.Text != "" || !strings.Contains(l.Note, "no third-party notices") {
		t.Fatalf("without the file: %+v", l)
	}
	bin := filepath.Join(dir, "bin.md")
	_ = os.WriteFile(bin, []byte{0xff, 0xfe, 0x00}, 0o644)
	l = core.Licenses{}
	open(bin).call("app.licenses", nil, &l)
	if l.Shipped || !strings.Contains(l.Note, "not its third-party notices") {
		t.Fatalf("a file that is not text: %+v", l)
	}
}
