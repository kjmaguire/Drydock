package integrations

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"sync"
)

// The OAuth apps the owner registers (2026-10-16). The vendors' own APIs have no dynamic client registration (their hosted MCP
// servers do, docs/mcp.md): a sign-in needs an OAuth app registered with the vendor, whose public client id Drydock sends.
// A release build can carry one (Options.OAuth, from the build or the environment); otherwise the person registers an app with
// the vendor and pastes its client id on the Integrations page, and Drydock keeps it in the settings table
// ("integrations.oauth.<kind>.client_id", and ".base_url" for a self-hosted Sentry). A client id is public, not a secret, but
// it is never logged or put in an event all the same. A pasted client id wins over the build's.

// OAuthAppInfo describes a kind's OAuth app to the Integrations page: the client id in use (public), the self-hosted base URL
// it belongs to, where to register one, and where the client id came from ("setting": pasted on the page, "build": the
// release or the environment, "": none, so the sign-in is off).
type OAuthAppInfo struct {
	Kind        string `json:"kind"`
	ClientID    string `json:"clientId,omitempty"`
	BaseURL     string `json:"baseUrl,omitempty"`
	RegisterURL string `json:"registerUrl"`
	Source      string `json:"source,omitempty"`
	// BaseURLSetting says the vendor can be self-hosted, so the page offers a base URL beside the client id (Sentry).
	BaseURLSetting bool `json:"baseUrlSetting,omitempty"`
	// RedirectURI is the loopback redirect a registered app must allow (the code flow); "" for the device flow.
	RedirectURI string `json:"redirectUri,omitempty"`
	// Scopes the registered app is asked for, in words for the registration page.
	Scopes []string `json:"scopes,omitempty"`
}

// OAuthAppSetting is what the owner pasted for a kind.
type OAuthAppSetting struct {
	ClientID string
	BaseURL  string
}

// oauthAppSettings is the in-memory copy of the pasted registrations (the settings table holds the durable one).
type oauthAppSettings struct {
	mu sync.RWMutex
	m  map[string]OAuthAppSetting
}

const oauthAppSettingPrefix = "integrations.oauth."

// EventOAuthAppChanged says a kind's OAuth app registration changed (data: kind only; never the client id).
const EventOAuthAppChanged = "integration.oauth_app_changed"

// linearRedirectPort is the loopback port Drydock asks for first for Linear's code flow, so the redirect URI a registered app
// must allow is known in advance (http://127.0.0.1:41739/callback). When the port is taken, any free one is used.
const linearRedirectPort = 41739

// registerURL is the vendor page where an OAuth app is registered.
func registerURL(kind string) string {
	switch kind {
	case KindLinear:
		return "https://linear.app/settings/api/applications/new"
	case KindSentry:
		return "https://sentry.io/settings/developer-settings/"
	case KindGitHub:
		return "https://github.com/settings/applications/new"
	}
	return ""
}

// ReadOAuthApps reads the pasted registrations from the settings table.
func (s *Store) ReadOAuthApps(ctx context.Context) (map[string]OAuthAppSetting, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT key, value FROM settings WHERE key LIKE ?`, oauthAppSettingPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]OAuthAppSetting{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		rest := strings.TrimPrefix(k, oauthAppSettingPrefix)
		kind, field, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}
		a := out[kind]
		switch field {
		case "client_id":
			a.ClientID = v
		case "base_url":
			a.BaseURL = v
		}
		out[kind] = a
	}
	return out, rows.Err()
}

// WriteOAuthApp stores (or, with an empty client id, removes) a kind's pasted registration.
func (s *Store) WriteOAuthApp(ctx context.Context, kind string, a OAuthAppSetting) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for field, v := range map[string]string{"client_id": a.ClientID, "base_url": a.BaseURL} {
			key := oauthAppSettingPrefix + kind + "." + field
			if v == "" {
				if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// loadOAuthApps reads the pasted registrations once (New).
func (s *Service) loadOAuthApps(ctx context.Context) {
	m, err := s.st.ReadOAuthApps(ctx)
	if err != nil {
		s.o.Logf("integrations: reading the OAuth app settings: %v", err)
		m = map[string]OAuthAppSetting{}
	}
	s.oauthApps.mu.Lock()
	s.oauthApps.m = m
	s.oauthApps.mu.Unlock()
}

// oauthAppSetting returns what the owner pasted for a kind.
func (s *Service) oauthAppSetting(kind string) (OAuthAppSetting, bool) {
	s.oauthApps.mu.RLock()
	defer s.oauthApps.mu.RUnlock()
	a, ok := s.oauthApps.m[kind]
	return a, ok && a.ClientID != ""
}

// OAuthAppInfo describes a kind's OAuth app for the page (nothing secret: the client id is public).
func (s *Service) OAuthAppInfo(kind string) (OAuthAppInfo, bool) {
	def, ok := DefaultOAuthApp(kind, "")
	if !ok {
		return OAuthAppInfo{}, false
	}
	info := OAuthAppInfo{Kind: kind, RegisterURL: registerURL(kind), BaseURLSetting: kind == KindSentry, Scopes: def.Scopes}
	if def.Method() == "pkce" {
		info.RedirectURI = "http://127.0.0.1:" + strconv.Itoa(linearRedirectPort) + "/callback"
	}
	if a, ok := s.oauthAppSetting(kind); ok {
		info.ClientID, info.BaseURL, info.Source = a.ClientID, a.BaseURL, "setting"
	} else if o := s.o.OAuth[kind]; o.ClientID != "" {
		info.ClientID, info.Source = o.ClientID, "build"
	}
	return info, true
}

// SetOAuthApp records the OAuth app the owner registered with a vendor (its public client id; for Sentry also the self-hosted
// base URL it belongs to). An empty client id removes the registration, so the sign-in falls back to the build's app or is off.
// Only a human can change it; the client id is never logged or published.
func (s *Service) SetOAuthApp(ctx context.Context, by, kind, clientID, baseURL string) (OAuthAppInfo, error) {
	if err := requireHuman(by); err != nil {
		return OAuthAppInfo{}, err
	}
	if _, ok := DefaultOAuthApp(kind, ""); !ok {
		return OAuthAppInfo{}, errf(ErrBadRequest, "%s has no OAuth sign-in Drydock can do; paste a %s instead", vendor(kind), tokenNoun(kind))
	}
	clientID = strings.TrimSpace(clientID)
	if clientID != "" && !clientIDRe.MatchString(clientID) {
		return OAuthAppInfo{}, errf(ErrBadRequest, "the client id must be printable characters without spaces or line breaks (at most 200)")
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != "" {
		if kind != KindSentry {
			return OAuthAppInfo{}, errf(ErrBadRequest, "only Sentry has a self-hosted URL for its OAuth app")
		}
		b, err := CheckAPIBase(baseURL)
		if err != nil {
			return OAuthAppInfo{}, err
		}
		baseURL = b
	}
	if clientID == "" {
		baseURL = ""
	}
	a := OAuthAppSetting{ClientID: clientID, BaseURL: baseURL}
	if err := s.st.WriteOAuthApp(ctx, kind, a); err != nil {
		return OAuthAppInfo{}, err
	}
	s.oauthApps.mu.Lock()
	if s.oauthApps.m == nil {
		s.oauthApps.m = map[string]OAuthAppSetting{}
	}
	if clientID == "" {
		delete(s.oauthApps.m, kind)
	} else {
		s.oauthApps.m[kind] = a
	}
	s.oauthApps.mu.Unlock()
	s.emit(ctx, EventOAuthAppChanged, "", "", map[string]any{"kind": kind, "registered": clientID != ""})
	info, _ := s.OAuthAppInfo(kind)
	return info, nil
}

// usesGH reports that a GitHub connection goes through the person's gh sign-in (the default); one made with the OAuth device
// flow holds its own token and calls GitHub's REST API directly.
func usesGH(c Connection) bool { return c.Kind == KindGitHub && c.AuthKind != AuthOAuth }
