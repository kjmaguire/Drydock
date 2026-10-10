package api_test

// Hidden acceptance test of the trial task tally-invite-tokens (evals/tasks). The agents never see it.
//
// The invite token is read from the email the outbox kept (e.Mail), the only place it may appear.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"example.com/tally/internal/api"
	"example.com/tally/internal/apitest"
	"example.com/tally/internal/clock"
	"example.com/tally/internal/payments"
	"example.com/tally/internal/snapshot"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
)

var (
	hiddenITLink   = regexp.MustCompile(`token=([^\s&"'<>]+)`)
	hiddenITFormat = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

const hiddenITWeek = 7 * 24 * time.Hour

// hiddenITInvite sends POST /v1/invites with key and returns the new invite's ID and the token of the email it sent to the
// address.
func hiddenITInvite(t *testing.T, e *apitest.Env, key, email, role string) (string, string) {
	t.Helper()
	id, token, _ := hiddenITInviteAnswer(t, e, key, email, role)
	return id, token
}

// hiddenITInviteAnswer is hiddenITInvite that also returns the answer to POST /v1/invites.
func hiddenITInviteAnswer(t *testing.T, e *apitest.Env, key, email, role string) (string, string, *apitest.Response) {
	t.Helper()
	before := len(e.Mail.Messages())
	r := e.Do("POST", "/v1/invites", key, map[string]string{"email": email, "role": role})
	if r.Code != http.StatusCreated {
		t.Fatalf("POST /v1/invites for %s: want 201, got %s", email, r)
	}
	var inv struct {
		ID string `json:"id"`
	}
	r.JSON(t, &inv)
	token := ""
	for _, m := range e.Mail.Messages()[before:] {
		if strings.EqualFold(strings.TrimSpace(m.To), email) {
			if sub := hiddenITLink.FindStringSubmatch(m.Body); sub != nil {
				token, _ = url.QueryUnescape(sub[1])
			}
		}
	}
	if token == "" {
		t.Fatalf("POST /v1/invites for %s sent no email with a link holding the token parameter; emails: %+v", email, e.Mail.Messages()[before:])
	}
	if inv.ID == "" {
		t.Fatalf("the answer to POST /v1/invites has no id: %s", r)
	}
	return inv.ID, token, r
}

func hiddenITAccept(e *apitest.Env, token, name string) *apitest.Response {
	return e.Do("POST", "/v1/invites/accept", "", map[string]string{"token": token, "name": name})
}

func hiddenITWant(t *testing.T, what string, r *apitest.Response, status int, code string) {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(r.Body, &body)
	if r.Code != status || (code != "" && body.Error != code) {
		if code == "" {
			t.Fatalf("%s: want %d, got %s", what, status, r)
		}
		t.Fatalf("%s: want %d {\"error\":%q}, got %s", what, status, code, r)
	}
}

// hiddenITRole is the role of the member with this email address in org, "" when there is none.
func hiddenITRole(t *testing.T, e *apitest.Env, org apitest.Org, email string) string {
	t.Helper()
	var page struct {
		Items []struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/members", org.Key, nil, &page)
	for _, m := range page.Items {
		if strings.EqualFold(m.Email, email) {
			return m.Role
		}
	}
	return ""
}

type hiddenITPending struct {
	ID        string     `json:"id"`
	Email     string     `json:"email"`
	Role      string     `json:"role"`
	CreatedAt *time.Time `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// hiddenITList is GET /v1/invites with key, and its raw body.
func hiddenITList(t *testing.T, e *apitest.Env, key string) ([]hiddenITPending, string) {
	t.Helper()
	r := e.Do("GET", "/v1/invites", key, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET /v1/invites: want 200, got %s", r)
	}
	var page struct {
		Items []hiddenITPending `json:"items"`
	}
	r.JSON(t, &page)
	return page.Items, string(r.Body)
}

func hiddenITIDs(items []hiddenITPending) []string {
	ids := []string{}
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	sort.Strings(ids)
	return ids
}

// hiddenITServe builds an API server over st (a store loaded from a snapshot) with its clock at now.
func hiddenITServe(st *store.Store, now time.Time) func(method, path, key string, body any) *apitest.Response {
	clk := clock.NewFake(now)
	pay := &payments.Service{Store: st, Gateway: &payments.Fake{}, Clock: clk}
	subs := &subscriptions.Service{Store: st, Payments: pay, Clock: clk}
	srv := api.New(st, api.Options{Clock: clk, Payments: pay, Subscriptions: subs})
	return func(method, path, key string, body any) *apitest.Response {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, rd)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return &apitest.Response{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
	}
}

func TestHiddenInviteTokenOnlyInEmail(t *testing.T) {
	e := apitest.New(t)
	_, viewerKey := e.AddMember(e.Acme, "viewer@acme.example", store.RoleViewer)
	tokens := map[string]string{} // token -> invite ID
	var ids []string
	for i := 0; i < 10; i++ {
		id, tok, r := hiddenITInviteAnswer(t, e, e.Acme.Key, fmt.Sprintf("person%d@people.example", i), "member")
		if raw, err := base64.RawURLEncoding.DecodeString(tok); !hiddenITFormat.MatchString(tok) || err != nil || len(raw) != 32 {
			t.Fatalf("the invite token %q is not 32 bytes in unpadded base64url (43 characters)", tok)
		}
		if strings.Contains(string(r.Body), tok) {
			t.Fatalf("the answer to POST /v1/invites shows the token: %s", r)
		}
		if _, dup := tokens[tok]; dup {
			t.Fatalf("the token %s was given out twice", tok)
		}
		tokens[tok] = id
		ids = append(ids, id)
	}
	noTokens := func(where, text string) {
		t.Helper()
		for tok := range tokens {
			if strings.Contains(text, tok) {
				t.Fatalf("%s shows an invite token (%s): %s", where, tok, text)
			}
		}
	}
	// no API answer shows a token, to any member
	for _, key := range []string{e.Acme.Key, viewerKey} {
		for _, path := range []string{"/v1/invites", "/v1/members", "/v1/org", "/v1/me"} {
			r := e.MustDo(http.StatusOK, "GET", path, key, nil, nil)
			noTokens("GET "+path, string(r.Body))
		}
	}
	items, _ := hiddenITList(t, e, viewerKey)
	sort.Strings(ids)
	if got := hiddenITIDs(items); strings.Join(got, ",") != strings.Join(ids, ",") {
		t.Fatalf("GET /v1/invites lists %v, want the 10 pending invites %v", got, ids)
	}

	// the accepting answer does not show it either
	var first, revoked, used string
	for tok, id := range tokens {
		switch {
		case first == "":
			first = tok
		case revoked == "":
			revoked = tok
			e.MustDo(http.StatusNoContent, "DELETE", "/v1/invites/"+id, e.Acme.Key, nil, nil)
		case used == "":
			used = tok
		}
	}
	r := hiddenITAccept(e, used, "Used Person")
	hiddenITWant(t, "accepting a pending invite", r, http.StatusCreated, "")
	noTokens("the answer to POST /v1/invites/accept", string(r.Body))

	// snapshots hold no token; the invites work as before after a reload
	path := filepath.Join(t.TempDir(), "tally.json")
	if err := snapshot.Save(e.Store, path, e.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	noTokens("the snapshot", string(b))
	st, err := snapshot.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	do := hiddenITServe(st, e.Clock.Now())
	hiddenITWant(t, "after a reload, accepting a revoked invite", do("POST", "/v1/invites/accept", "", map[string]string{"token": revoked, "name": "R"}), http.StatusNotFound, "invite_not_found")
	hiddenITWant(t, "after a reload, accepting a used invite", do("POST", "/v1/invites/accept", "", map[string]string{"token": used, "name": "U"}), http.StatusGone, "invite_used")
	hiddenITWant(t, "after a reload, accepting a pending invite", do("POST", "/v1/invites/accept", "", map[string]string{"token": first, "name": "First"}), http.StatusCreated, "")
}

func TestHiddenInviteAcceptOutcomes(t *testing.T) {
	e := apitest.New(t)
	start := e.Clock.Now()

	// unknown tokens (of the right form)
	for i := 0; i < 2; i++ {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		tok := base64.RawURLEncoding.EncodeToString(b)
		hiddenITWant(t, fmt.Sprintf("accepting the unknown token %q", tok), hiddenITAccept(e, tok, "X"), http.StatusNotFound, "invite_not_found")
	}

	// revoked
	id, tok := hiddenITInvite(t, e, e.Acme.Key, "revoked@people.example", "member")
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/invites/"+id, e.Acme.Key, nil, nil)
	hiddenITWant(t, "accepting a revoked invite", hiddenITAccept(e, tok, "Revoked"), http.StatusNotFound, "invite_not_found")
	if role := hiddenITRole(t, e, e.Acme, "revoked@people.example"); role != "" {
		t.Fatalf("a revoked invite made its address a member (%s)", role)
	}

	// already accepted: used once, never again, even after the member was removed
	_, tok = hiddenITInvite(t, e, e.Acme.Key, "used@people.example", "billing")
	r := hiddenITAccept(e, tok, "Used")
	hiddenITWant(t, "accepting a pending invite", r, http.StatusCreated, "")
	var accepted struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		APIKey struct {
			Token string `json:"token"`
		} `json:"api_key"`
	}
	r.JSON(t, &accepted)
	if role := hiddenITRole(t, e, e.Acme, "used@people.example"); role != "billing" {
		t.Fatalf("after accepting an invite as billing, the member's role is %q", role)
	}
	e.MustDo(http.StatusOK, "GET", "/v1/org", accepted.APIKey.Token, nil, nil)
	hiddenITWant(t, "accepting the same invite again", hiddenITAccept(e, tok, "Used"), http.StatusGone, "invite_used")
	hiddenITWant(t, "accepting the same invite again under another name", hiddenITAccept(e, tok, "Someone Else"), http.StatusGone, "invite_used")
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/members/"+accepted.User.ID, e.Acme.Key, nil, nil)
	hiddenITWant(t, "accepting a used invite after the member was removed", hiddenITAccept(e, tok, "Used"), http.StatusGone, "invite_used")
	if role := hiddenITRole(t, e, e.Acme, "used@people.example"); role != "" {
		t.Fatalf("a used invite made its address a member again (%s)", role)
	}

	// expiry: 7 days on the service's clock
	_, early := hiddenITInvite(t, e, e.Acme.Key, "early@people.example", "member")
	_, late := hiddenITInvite(t, e, e.Acme.Key, "late@people.example", "member")
	e.Clock.Set(start.Add(hiddenITWeek - time.Second))
	hiddenITWant(t, "accepting an invite 7 days less a second after it was created", hiddenITAccept(e, early, "Early"), http.StatusCreated, "")
	e.Clock.Set(start.Add(hiddenITWeek + time.Second))
	hiddenITWant(t, "accepting an invite 7 days and a second after it was created", hiddenITAccept(e, late, "Late"), http.StatusGone, "invite_expired")
	if role := hiddenITRole(t, e, e.Acme, "late@people.example"); role != "" {
		t.Fatalf("an expired invite made its address a member (%s)", role)
	}
	e.Clock.Set(start.Add(30 * hiddenITWeek))
	hiddenITWant(t, "accepting an invite 30 weeks after it was created", hiddenITAccept(e, late, "Late"), http.StatusGone, "invite_expired")
}

func TestHiddenInviteReinvite(t *testing.T) {
	e := apitest.New(t)
	_, globexTok := hiddenITInvite(t, e, e.Globex.Key, "alice@people.example", "viewer") // another organization's invite
	_, first := hiddenITInvite(t, e, e.Acme.Key, "Alice@People.example", "viewer")
	e.Clock.Advance(time.Hour)
	second, newest := hiddenITInvite(t, e, e.Acme.Key, "alice@PEOPLE.example", "billing")
	if newest == first {
		t.Fatal("inviting the address again sent the same token")
	}
	items, body := hiddenITList(t, e, e.Acme.Key)
	if len(items) != 1 || items[0].ID != second || items[0].Role != "billing" {
		t.Fatalf("after inviting the address again, GET /v1/invites must list only the newest invite (%s, billing): %s", second, body)
	}
	hiddenITWant(t, "accepting the replaced invite", hiddenITAccept(e, first, "Alice"), http.StatusNotFound, "invite_not_found")
	hiddenITWant(t, "accepting the newest invite", hiddenITAccept(e, newest, "Alice"), http.StatusCreated, "")
	if role := hiddenITRole(t, e, e.Acme, "alice@people.example"); role != "billing" {
		t.Fatalf("after accepting the newest invite (billing), the role is %q", role)
	}
	// another organization's invite to the address is not touched
	hiddenITWant(t, "accepting Globex's invite to the same address", hiddenITAccept(e, globexTok, "Alice"), http.StatusCreated, "")
	if role := hiddenITRole(t, e, e.Globex, "alice@people.example"); role != "viewer" {
		t.Fatalf("after accepting Globex's invite, the role in Globex is %q", role)
	}

	// inviting again after an invite expired: the new one works, the old one does not
	start := e.Clock.Now()
	_, old := hiddenITInvite(t, e, e.Acme.Key, "bob@people.example", "member")
	e.Clock.Set(start.Add(hiddenITWeek + time.Hour))
	_, fresh := hiddenITInvite(t, e, e.Acme.Key, "Bob@people.example", "member")
	if r := hiddenITAccept(e, old, "Bob"); r.Code != http.StatusNotFound && r.Code != http.StatusGone {
		t.Fatalf("accepting an expired invite that was replaced: want 404 or 410, got %s", r)
	}
	hiddenITWant(t, "accepting the new invite that replaced an expired one", hiddenITAccept(e, fresh, "Bob"), http.StatusCreated, "")
}

func TestHiddenInviteListsPendingOnly(t *testing.T) {
	e := apitest.New(t)
	_, viewerKey := e.AddMember(e.Acme, "viewer@acme.example", store.RoleViewer)
	start := e.Clock.Now()
	expiring, _ := hiddenITInvite(t, e, e.Acme.Key, "expiring@people.example", "member")
	revoked, _ := hiddenITInvite(t, e, e.Acme.Key, "revoked@people.example", "member")
	_, acceptedTok := hiddenITInvite(t, e, e.Acme.Key, "accepted@people.example", "member")
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/invites/"+revoked, e.Acme.Key, nil, nil)
	hiddenITWant(t, "accepting a pending invite", hiddenITAccept(e, acceptedTok, "Accepted"), http.StatusCreated, "")
	e.Clock.Set(start.Add(3 * 24 * time.Hour))
	pending1, _ := hiddenITInvite(t, e, e.Acme.Key, "pending1@people.example", "viewer")
	hiddenITInvite(t, e, e.Globex.Key, "globex@people.example", "viewer") // another organization's

	check := func(when string, want map[string]time.Time) {
		t.Helper()
		for _, key := range []string{e.Acme.Key, viewerKey} {
			items, body := hiddenITList(t, e, key)
			var wantIDs []string
			for id := range want {
				wantIDs = append(wantIDs, id)
			}
			sort.Strings(wantIDs)
			if got := hiddenITIDs(items); strings.Join(got, ",") != strings.Join(wantIDs, ",") {
				t.Fatalf("%s, GET /v1/invites lists %v, want the pending invites %v: %s", when, got, wantIDs, body)
			}
			for _, it := range items {
				if it.ExpiresAt == nil || !it.ExpiresAt.Equal(want[it.ID]) {
					t.Fatalf("%s, GET /v1/invites shows %s with expires_at %v, want %s (7 days after it was created): %s", when, it.ID, it.ExpiresAt, want[it.ID].Format(time.RFC3339), body)
				}
			}
		}
	}
	check("3 days after the first invites", map[string]time.Time{expiring: start.Add(hiddenITWeek), pending1: start.Add(3*24*time.Hour + hiddenITWeek)})
	e.Clock.Set(start.Add(hiddenITWeek - time.Second))
	check("7 days less a second after the first invites", map[string]time.Time{expiring: start.Add(hiddenITWeek), pending1: start.Add(3*24*time.Hour + hiddenITWeek)})
	e.Clock.Set(start.Add(hiddenITWeek + time.Second))
	pending2, _ := hiddenITInvite(t, e, e.Acme.Key, "pending2@people.example", "billing")
	check("7 days and a second after the first invites", map[string]time.Time{pending1: start.Add(3*24*time.Hour + hiddenITWeek), pending2: start.Add(2*hiddenITWeek + time.Second)})
}
