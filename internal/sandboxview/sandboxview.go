// Package sandboxview is what the viewer for a sandbox's app is told and may be told (decision P134). The app a worker started in
// a sandbox runs code an agent wrote, so it is never opened in the person's browser, which holds the cookies of every other local
// app on 127.0.0.1. Drydock shows it in a second process of its own executable (`drydock.exe sandbox-view --url ... --title ...`):
// a window with no bridge to the core, no data of Drydock's, and a browser profile in a new folder that is deleted when the window
// closes, so what the page stores (cookies, local storage, cache) is its own and ends with it. This package holds the part that
// does not need a window: the arguments, what address may be shown, and the folder.
package sandboxview

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Arg is the first argument that makes the executable a viewer instead of the app.
const Arg = "sandbox-view"

// DirName is the folder of the data folder that holds the viewers' profiles.
const DirName = "sandbox-view"

// MaxTitle is the longest window title kept.
const MaxTitle = 80

// Request is what a viewer opens.
type Request struct {
	URL   string
	Title string
}

// Args are the arguments that start a viewer for a request. The address has been checked; a request that fails ValidURL has none.
func Args(r Request) ([]string, error) {
	if err := ValidURL(r.URL); err != nil {
		return nil, err
	}
	return []string{Arg, "--url", r.URL, "--title", Title(r.Title)}, nil
}

// Parse reads a viewer's command line (os.Args). ok is false when it is not one, which is the normal start of the app; an error
// means it is one and says what is wrong with it.
func Parse(args []string) (r Request, ok bool, err error) {
	if len(args) < 2 || args[1] != Arg {
		return Request{}, false, nil
	}
	rest := args[2:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--url", "--title":
			if i+1 >= len(rest) {
				return Request{}, true, fmt.Errorf("%s needs a value", rest[i])
			}
			if rest[i] == "--url" {
				r.URL = rest[i+1]
			} else {
				r.Title = rest[i+1]
			}
			i++
		default:
			return Request{}, true, fmt.Errorf("unknown argument %q", rest[i])
		}
	}
	if err := ValidURL(r.URL); err != nil {
		return Request{}, true, err
	}
	r.Title = Title(r.Title)
	return r, true, nil
}

// ValidURL accepts only http://127.0.0.1:<port>/ : the loopback address a forwarder publishes on, a port, the root path, and
// nothing else (no other host, name, scheme, credentials, query or fragment). The viewer is not a browser.
func ValidURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("the address is not valid")
	}
	if u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("only http://127.0.0.1:<port>/ can be shown")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host != "127.0.0.1" {
		return errors.New("only the loopback address 127.0.0.1 can be shown")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return errors.New("the address has no valid port")
	}
	return nil
}

// Title makes a window title from text: no control characters, one line, at most MaxTitle characters, and always marked as an
// isolated window so the person can tell it from Drydock's own.
func Title(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	t := strings.Join(strings.Fields(b.String()), " ")
	if rs := []rune(t); len(rs) > MaxTitle {
		t = string(rs[:MaxTitle-1]) + "…"
	}
	if t == "" {
		t = "Sandbox app"
	}
	return t + " - Drydock isolated window"
}

// NewProfile makes the empty folder a viewer's browser profile lives in, under the data folder. Remove it when the viewer ends.
func NewProfile(dataDir string) (string, error) {
	parent := filepath.Join(dataDir, DirName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "p-")
}

// RemoveProfile removes a viewer's profile folder, trying again for a few seconds: when the window closes, WebView2's own processes
// are still shutting down and hold files in it, so the first attempt can fail. What is left after the last attempt is removed by
// CleanLeftovers at the next start of the app. remove is os.RemoveAll except in a test.
func RemoveProfile(dir string, tries int, wait time.Duration, remove func(string) error) error {
	if remove == nil {
		remove = os.RemoveAll
	}
	var err error
	for i := 0; i < tries; i++ {
		if err = remove(dir); err == nil {
			if _, serr := os.Stat(dir); os.IsNotExist(serr) {
				return nil
			}
		}
		time.Sleep(wait)
	}
	if err == nil {
		err = errors.New("the folder is still there")
	}
	return err
}

// CleanLeftovers removes the profiles a crash or a killed viewer left. Call it at start of the app: a viewer is a child of the app
// and dies with it, so none is running then.
func CleanLeftovers(dataDir string) error {
	return os.RemoveAll(filepath.Join(dataDir, DirName))
}
