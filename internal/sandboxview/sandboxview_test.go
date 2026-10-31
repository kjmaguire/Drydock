package sandboxview

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOnlyTheLoopbackAddressOfAForwardCanBeShown(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:49153/", "http://127.0.0.1:1/", "http://127.0.0.1:65535", "http://127.0.0.1:8080/"} {
		if err := ValidURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "127.0.0.1:8080", "https://127.0.0.1:8080/", "file:///C:/Windows/win.ini", "javascript:alert(1)", "about:blank",
		"http://localhost:8080/", "http://127.0.0.2:8080/", "http://[::1]:8080/", "http://0.0.0.0:8080/", "http://10.0.0.5:8080/",
		"http://example.com/", "http://127.0.0.1/", "http://127.0.0.1:0/", "http://127.0.0.1:65536/", "http://127.0.0.1:080/",
		"http://127.0.0.1:8080/admin", "http://127.0.0.1:8080/?a=1", "http://127.0.0.1:8080/#x", "http://user:pw@127.0.0.1:8080/",
		"http://127.0.0.1:8080@evil.example/", "http://evil.example#@127.0.0.1:8080/", "http://127.0.0.1:8080\\@evil.example/",
		"http://127.0.0.1.evil.example:8080/", "http://127.1:8080/", "http://2130706433:8080/", "http://0x7f.0.0.1:8080/",
	} {
		if err := ValidURL(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestArgsAndParseRoundTrip(t *testing.T) {
	args, err := Args(Request{URL: "http://127.0.0.1:49153/", Title: "Port 3000 of the task's sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	r, ok, err := Parse(append([]string{"drydock.exe"}, args...))
	if err != nil || !ok || r.URL != "http://127.0.0.1:49153/" || !strings.HasPrefix(r.Title, "Port 3000 of the task's sandbox") {
		t.Fatalf("%+v %v %v", r, ok, err)
	}
	if _, err := Args(Request{URL: "http://example.com/"}); err == nil {
		t.Fatal("an address that is not a forward gets no arguments")
	}
}

func TestParseLeavesTheAppAlone(t *testing.T) {
	for _, args := range [][]string{{"drydock.exe"}, {"drydock.exe", "mcp-shim"}, {"drydock.exe", "--url", "x"}} {
		if _, ok, err := Parse(args); ok || err != nil {
			t.Errorf("%v is not a viewer: %v %v", args, ok, err)
		}
	}
}

func TestParseRefusesWhatIsNotAViewerRequest(t *testing.T) {
	for _, args := range [][]string{
		{"drydock.exe", Arg},
		{"drydock.exe", Arg, "--url"},
		{"drydock.exe", Arg, "--url", "http://example.com/"},
		{"drydock.exe", Arg, "--url", "http://127.0.0.1:8080/", "--devtools"},
		{"drydock.exe", Arg, "--url", "http://127.0.0.1:8080/", "--title"},
		{"drydock.exe", Arg, "--title", "no address"},
	} {
		if _, ok, err := Parse(args); !ok || err == nil {
			t.Errorf("%v: ok=%v err=%v", args, ok, err)
		}
	}
}

func TestTitleIsOneShortMarkedLine(t *testing.T) {
	got := Title("a\r\nb\x00c\t" + strings.Repeat("x", 200))
	if strings.ContainsAny(got, "\r\n\x00\t") || !strings.HasSuffix(got, " - Drydock isolated window") || len([]rune(got)) > MaxTitle+len(" - Drydock isolated window") {
		t.Fatalf("%q", got)
	}
	if Title("") != "Sandbox app - Drydock isolated window" {
		t.Fatalf("%q", Title(""))
	}
}

func TestEachViewerGetsAFreshProfileAndLeftoversGo(t *testing.T) {
	dir := t.TempDir()
	a, err := NewProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewProfile(dir)
	if err != nil || a == b {
		t.Fatalf("two viewers share a profile: %q %q %v", a, b, err)
	}
	if rel, err := filepath.Rel(filepath.Join(dir, DirName), a); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("%q is not under the viewers' folder", a)
	}
	if ents, _ := os.ReadDir(a); len(ents) != 0 {
		t.Fatalf("a new profile is empty: %v", ents)
	}
	if err := os.WriteFile(filepath.Join(a, "Cookies"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanLeftovers(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Fatal("the leftovers are gone")
	}
	if err := CleanLeftovers(dir); err != nil {
		t.Fatalf("nothing to clean is not an error: %v", err)
	}
}

func TestAProfileHeldByTheClosingBrowserIsRemovedOnceItLetsGo(t *testing.T) {
	dir, err := NewProfile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	// the first two attempts fail the way Windows does while msedgewebview2.exe still has a file open
	remove := func(p string) error {
		attempts++
		if attempts <= 2 {
			return errors.New("The process cannot access the file because it is being used by another process.")
		}
		return os.RemoveAll(p)
	}
	if err := RemoveProfile(dir, 10, time.Millisecond, remove); err != nil || attempts != 3 {
		t.Fatalf("attempts %d: %v", attempts, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the profile is gone")
	}
}

func TestAProfileThatStaysHeldIsReportedAndLeftForTheNextStart(t *testing.T) {
	data := t.TempDir()
	dir, err := NewProfile(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveProfile(dir, 3, time.Millisecond, func(string) error { return errors.New("in use") }); err == nil {
		t.Fatal("a profile that cannot be removed is reported")
	}
	if err := CleanLeftovers(data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the next start removes it")
	}
}

func TestTheTitleIsMadeOnceWhateverPathItTakes(t *testing.T) {
	args, err := Args(Request{URL: "http://127.0.0.1:49153/", Title: "Port 3000 of the task's sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := Parse(append([]string{"drydock.exe"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	// what the window shows is Title of what Parse returned
	if got, want := Title(r.Title), "Port 3000 of the task's sandbox - Drydock isolated window"; got != want {
		t.Fatalf("%q, want %q", got, want)
	}
	long := Title(strings.Repeat("x", 300))
	if Title(long) != long || len([]rune(long)) > MaxTitle+len([]rune(titleSuffix)) {
		t.Fatalf("%q", long)
	}
	if Title("proof") != "proof - Drydock isolated window" {
		t.Fatalf("%q", Title("proof"))
	}
}

func TestAProfileIsOnlyOneTheAppMade(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidProfile(dir, p); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	for _, bad := range []string{"", "relative", dir, filepath.Join(dir, DirName), filepath.Join(dir, DirName, "p-missing"), filepath.Join(dir, DirName, "..", "p-x"), other, filepath.Join(dir, DirName, "x-1")} {
		if err := ValidProfile(dir, bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	// it travels in the arguments
	args, err := Args(Request{URL: "http://127.0.0.1:8080/", Title: "t", Profile: p})
	if err != nil {
		t.Fatal(err)
	}
	r, ok, err := Parse(append([]string{"drydock.exe"}, args...))
	if err != nil || !ok || r.Profile != p {
		t.Fatalf("%+v %v %v", r, ok, err)
	}
}

func TestTheViewersAssetServerAnswersNothing(t *testing.T) {
	called := false
	h := ClosedAssets(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	for _, path := range []string{"/wails/runtime", "/wails/runtime.js", "/", "/index.html", "/wails/events/payload"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://wails.localhost"+path, strings.NewReader(`{"object":9,"method":0}`)))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if called {
		t.Fatal("a request reached the handler behind it")
	}
}
