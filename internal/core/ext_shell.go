package core

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Opening an address in the person's own browser (audit 2026-10-16, M23). The window is a WebView2 page, and a link in it must not
// open inside the app or take the window itself somewhere: the page asks the shell to open the address in the default browser
// (shell.openExternal), and the shell hands it to the operating system only after this check. What the system opens for an address
// is anything it has a handler for (a program path, a file, a protocol such as ms-msdt:), so the only addresses that pass are a
// web address with a host and no password in it, and a plain mail address.

// MaxExternalURL is the longest address the shell is asked to open.
const MaxExternalURL = 2048

var mailAddressRe = regexp.MustCompile(`^[A-Za-z0-9._%+\-]{1,64}@[A-Za-z0-9.\-]{1,255}$`)

// ExternalURL checks an address a page asks to have opened and returns it in the form to hand over. Only http, https and a plain
// mailto: address pass.
func ExternalURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return "", errors.New("there is no address to open")
	case len(raw) > MaxExternalURL:
		return "", errors.New("the address is too long to open")
	case !utf8.ValidString(raw):
		return "", errors.New("the address is not text")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return "", errors.New("the address has characters that cannot be in one")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("the address cannot be read")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Hostname() == "" || u.User != nil || u.Opaque != "" {
			return "", errors.New("only a web address with a host name, without a user name or password in it, can be opened")
		}
		u.Scheme = strings.ToLower(u.Scheme)
		return u.String(), nil
	case "mailto":
		if u.Opaque == "" || u.RawQuery != "" || u.Host != "" || !mailAddressRe.MatchString(u.Opaque) {
			return "", errors.New("only a plain mail address can be opened")
		}
		return "mailto:" + u.Opaque, nil
	}
	return "", errors.New("only http, https and mail addresses can be opened from here")
}

// OpenExternal asks the shell to open a web or mail address in the person's own browser or mail program. Only a person may; a
// build with no window (the browser harness) says so, and the page falls back to the browser's own link.
func (c *Core) OpenExternal(ctx context.Context, raw, by string) error {
	if c.cfg.OpenURL == nil {
		return errors.New("this window cannot open another program: copy the address and open it in a browser")
	}
	if !isHuman(by) {
		return errors.New("only a person may open an address")
	}
	u, err := ExternalURL(raw)
	if err != nil {
		return err
	}
	return c.cfg.OpenURL(ctx, u)
}

// Restart starts Drydock again, so that a staged restore is applied now (decision P116). Only a person may; a build with no
// window says to quit and reopen instead.
func (c *Core) Restart(ctx context.Context, by string) error {
	if c.cfg.Restart == nil {
		return errors.New("this window cannot restart Drydock: quit and reopen it")
	}
	if !isHuman(by) {
		return errors.New("only a person may restart Drydock")
	}
	return c.cfg.Restart(ctx)
}

func init() {
	RegisterExtension(Extension{Name: "shell", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"shell.openExternal": bind(func(ctx context.Context, p struct {
				URL string `json:"url"`
			}) (any, error) {
				if err := c.OpenExternal(ctx, p.URL, c.Human()); err != nil {
					return nil, err
				}
				return map[string]bool{"opened": true}, nil
			}),
			"shell.restart": bind(func(ctx context.Context, _ struct{}) (any, error) {
				if err := c.Restart(ctx, c.Human()); err != nil {
					return nil, err
				}
				return map[string]bool{"restarting": true}, nil
			}),
		}, nil, nil
	}})
}
