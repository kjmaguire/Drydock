package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A key belongs to the address it was given for. Pointing the provider somewhere else without giving a key for the new place
// forgets the old one, so it is never sent there (audit 2026-10-16, M9).
func TestChangingAProvidersAddressForgetsItsKeyUnlessANewOneIsGiven(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "openai-compatible", BaseURL: "https://api.example.com/v1", APIKey: "sk-ONE", Tier: TierPremium})
	oldRef := p.KeyRef

	// the same place, another path: the key stays
	same := p
	same.BaseURL = "https://api.example.com/v2"
	up, err := e.reg.Update(e.ctx, same, "")
	if err != nil || up.KeyRef != oldRef {
		t.Fatalf("a path change lost the key: %v %+v", err, up)
	}
	if k, err := e.reg.APIKey(e.ctx, up); err != nil || k != "sk-ONE" {
		t.Fatalf("key after a path change: %q %v", k, err)
	}
	// the default port written out is the same place
	dflt := up
	dflt.BaseURL = "https://API.example.com:443/v2"
	if up, err = e.reg.Update(e.ctx, dflt, ""); err != nil || up.KeyRef != oldRef {
		t.Fatalf("an explicit default port lost the key: %v %+v", err, up)
	}

	// another host: the key is forgotten, in the store as well
	moved := up
	moved.BaseURL = "https://evil.example.net/v1"
	up, err = e.reg.Update(e.ctx, moved, "")
	if err != nil || up.KeyRef != "" {
		t.Fatalf("a new host kept the old key: %v %+v", err, up)
	}
	if _, err := e.sec.Get(e.ctx, oldRef); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("the old key is still in the store: %v", err)
	}
	if k, _ := e.reg.APIKey(e.ctx, up); k != "" {
		t.Fatalf("the provider still has a key: %q", k)
	}
	var cleared bool
	for _, ev := range e.events(t, "provider.updated") {
		var d struct {
			KeyCleared bool `json:"keyCleared"`
		}
		if json.Unmarshal(ev.Data, &d) == nil && d.KeyCleared {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("the event does not say the key was forgotten")
	}

	// a key given with the new address is kept for it
	back := up
	back.BaseURL = "https://api.example.com/v1"
	up, err = e.reg.Update(e.ctx, back, "sk-TWO")
	if err != nil || up.KeyRef == "" {
		t.Fatalf("a new key with the new address: %v %+v", err, up)
	}
	if k, _ := e.reg.APIKey(e.ctx, up); k != "sk-TWO" {
		t.Fatalf("key %q", k)
	}
	// and a different scheme or port is a different place too
	for _, u := range []string{"http://127.0.0.1:8080/v1", "https://api.example.com:8443/v1"} {
		other := up
		other.BaseURL = u
		got, err := e.reg.Update(e.ctx, other, "")
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		if got.KeyRef != "" {
			t.Fatalf("%s kept the key", u)
		}
		if _, err := e.reg.Update(e.ctx, func() Provider { b := got; b.BaseURL = "https://api.example.com/v1"; return b }(), "sk-BACK"); err != nil {
			t.Fatal(err)
		}
		up, _ = e.reg.Get(e.ctx, p.ID)
	}
}

func TestSameOrigin(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"https://a.com/x", "https://a.com/y", true},
		{"https://A.com", "https://a.com:443/v1", true},
		{"http://a.com", "http://a.com:80", true},
		{"https://a.com", "http://a.com", false},
		{"https://a.com", "https://b.com", false},
		{"https://a.com", "https://a.com:8443", false},
		{"https://a.com", "https://a.com.evil.com", false},
		{"https://a.com@evil.com/x", "https://a.com", false},
		{"", "", true},
		{"::bad", "::bad", true},
		{"::bad", "https://a.com", false},
	} {
		if got := SameOrigin(c.a, c.b); got != c.want {
			t.Errorf("SameOrigin(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// Go forwards Authorization to the same domain only, but a provider's key can travel in another header (x-api-key and the like),
// and those go wherever a redirect points. A redirect to another address is refused.
func TestAProvidersClientNeverFollowsARedirectToAnotherAddress(t *testing.T) {
	var elsewhereHits int
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhereHits++
	}))
	defer elsewhere.Close()
	var sameSeen string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/leak":
			http.Redirect(w, r, elsewhere.URL+"/collect", http.StatusFound)
		case "/within":
			http.Redirect(w, r, "/landed", http.StatusFound)
		case "/landed":
			sameSeen = r.Header.Get("x-api-key")
		}
	}))
	defer origin.Close()

	p := Provider{ID: "p", BaseURL: origin.URL, TLSMode: TLSNoneLoop}
	c, err := NewHTTPClient(p, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", origin.URL+"/leak", nil)
	req.Header.Set("x-api-key", "sk-SECRET")
	if resp, err := c.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("a redirect to another address was followed")
	}
	if elsewhereHits != 0 {
		t.Fatalf("the other address was contacted %d times", elsewhereHits)
	}
	req, _ = http.NewRequest("GET", origin.URL+"/within", nil)
	req.Header.Set("x-api-key", "sk-SECRET")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("a redirect within the address was refused: %v", err)
	}
	resp.Body.Close()
	if sameSeen != "sk-SECRET" {
		t.Fatalf("the redirect within the address lost the header: %q", sameSeen)
	}
}
