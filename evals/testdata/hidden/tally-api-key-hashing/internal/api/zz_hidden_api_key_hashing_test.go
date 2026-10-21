package api_test

// Hidden acceptance test of the trial task tally-api-key-hashing (evals/tasks). The agents never see it.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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

var hiddenAKFormat = regexp.MustCompile(`^tly_[A-Za-z0-9]{40}$`)

func hiddenAKHashes(token string) []string {
	var out []string
	for _, pre := range []string{token, strings.TrimPrefix(token, "tly_")} {
		sum := sha256.Sum256([]byte(pre))
		h := hex.EncodeToString(sum[:])
		out = append(out, h, strings.ToUpper(h))
	}
	return out
}

// hiddenAKNoToken fails when text shows any of the tokens.
func hiddenAKNoToken(t *testing.T, where, text string, tokens ...string) {
	t.Helper()
	for _, tok := range tokens {
		if strings.Contains(text, tok) {
			t.Errorf("%s shows the token %s: %s", where, tok, hiddenAKShort(text))
		}
	}
}

// hiddenAKNoSecrets fails when text shows any of the tokens or the SHA-256 of one (GET /v1/api-keys shows neither).
func hiddenAKNoSecrets(t *testing.T, where, text string, tokens ...string) {
	t.Helper()
	hiddenAKNoToken(t, where, text, tokens...)
	for _, tok := range tokens {
		for _, h := range hiddenAKHashes(tok) {
			if strings.Contains(text, h) {
				t.Errorf("%s shows the hash of the token %s: %s", where, tok, hiddenAKShort(text))
			}
		}
	}
}

func hiddenAKShort(s string) string {
	if len(s) > 3000 {
		return s[:3000] + "..."
	}
	return s
}

type hiddenAKNewKey struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Token  string `json:"token"`
}

// hiddenAKCreate is POST /v1/api-keys: the new key, with its token.
func hiddenAKCreate(t *testing.T, e *apitest.Env, key, name string) hiddenAKNewKey {
	t.Helper()
	k, _ := hiddenAKCreateAnswer(t, e, key, name)
	return k
}

// hiddenAKCreateAnswer is hiddenAKCreate that also returns the answer.
func hiddenAKCreateAnswer(t *testing.T, e *apitest.Env, key, name string) (hiddenAKNewKey, *apitest.Response) {
	t.Helper()
	r := e.Do("POST", "/v1/api-keys", key, map[string]string{"name": name})
	if r.Code != http.StatusCreated {
		t.Fatalf("POST /v1/api-keys: want 201, got %s", r)
	}
	var k hiddenAKNewKey
	r.JSON(t, &k)
	if k.ID == "" || k.Name != name || k.Token == "" {
		t.Fatalf("POST /v1/api-keys: the answer lacks the key's id, name or token: %s", r)
	}
	return k, r
}

// hiddenAKWantFormat fails unless the token the answer created has the new form.
func hiddenAKWantFormat(t *testing.T, where, token string, answer *apitest.Response) {
	t.Helper()
	if !hiddenAKFormat.MatchString(token) {
		t.Fatalf("%s: the token %q is not \"tly_\" and 40 characters from [A-Za-z0-9]: %s", where, token, answer)
	}
}

type hiddenAKListed struct {
	raw        map[string]json.RawMessage
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	UserID     string     `json:"user_id"`
	CreatedAt  *time.Time `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	Last4      string     `json:"last4"`
}

// hiddenAKList is GET /v1/api-keys sent with do: the keys by ID, and the raw body.
func hiddenAKList(t *testing.T, do func(method, path, key string, body any) *apitest.Response, key string) (map[string]hiddenAKListed, string) {
	t.Helper()
	r := do("GET", "/v1/api-keys", key, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET /v1/api-keys: want 200, got %s", r)
	}
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	r.JSON(t, &page)
	out := map[string]hiddenAKListed{}
	for _, raw := range page.Items {
		var k hiddenAKListed
		if err := json.Unmarshal(raw, &k); err != nil {
			t.Fatalf("GET /v1/api-keys: an item is not the JSON expected (%v): %s", err, raw)
		}
		if err := json.Unmarshal(raw, &k.raw); err != nil {
			t.Fatal(err)
		}
		out[k.ID] = k
	}
	return out, string(r.Body)
}

func TestHiddenAPIKeyTokens(t *testing.T) {
	e := apitest.New(t)
	seen := map[string]bool{}
	counts := map[rune]int{}
	for i := 0; i < 50; i++ {
		k, r := hiddenAKCreateAnswer(t, e, e.Acme.Key, "Key "+string(rune('A'+i%26)))
		hiddenAKWantFormat(t, "POST /v1/api-keys", k.Token, r)
		if seen[k.Token] {
			t.Fatalf("the token %s was given out twice", k.Token)
		}
		seen[k.Token] = true
		for _, c := range strings.TrimPrefix(k.Token, "tly_") {
			counts[c]++
		}
	}
	upper, lower, digit := 0, 0, 0
	for c := range counts {
		switch {
		case c >= 'A' && c <= 'Z':
			upper++
		case c >= 'a' && c <= 'z':
			lower++
		default:
			digit++
		}
	}
	// 2000 characters drawn from 62: every class shows up and nearly every character does
	if upper == 0 || lower == 0 || digit == 0 || len(counts) < 55 {
		t.Fatalf("50 tokens use %d different characters (%d upper case, %d lower case, %d digits): they are not random characters from [A-Za-z0-9]", len(counts), upper, lower, digit)
	}

	// a new key works, as the member who created it
	k := hiddenAKCreate(t, e, e.Acme.Key, "CI")
	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		APIKeyID string `json:"api_key_id"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/me", k.Token, nil, &me)
	if me.User.ID != e.Acme.Owner.ID || me.APIKeyID != k.ID {
		t.Fatalf("GET /v1/me with the new key = %+v, want the owner and key %s", me, k.ID)
	}
	if k.UserID != e.Acme.Owner.ID {
		t.Fatalf("the new key's user_id = %q, want %s", k.UserID, e.Acme.Owner.ID)
	}

	// the key given when an invite is accepted
	e.MustDo(http.StatusCreated, "POST", "/v1/invites", e.Acme.Key, map[string]string{"email": "new@people.example", "role": "member"}, nil)
	msgs := e.Mail.Messages()
	m := regexp.MustCompile(`token=([^\s&]+)`).FindStringSubmatch(msgs[len(msgs)-1].Body)
	if m == nil {
		t.Fatalf("no invite link in %q", msgs[len(msgs)-1].Body)
	}
	invite, _ := url.QueryUnescape(m[1])
	r := e.Do("POST", "/v1/invites/accept", "", map[string]string{"token": invite, "name": "New Person"})
	if r.Code != http.StatusCreated {
		t.Fatalf("POST /v1/invites/accept: want 201, got %s", r)
	}
	var accepted struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		APIKey hiddenAKNewKey `json:"api_key"`
	}
	r.JSON(t, &accepted)
	if accepted.APIKey.ID == "" {
		t.Fatalf("POST /v1/invites/accept: the answer has no api_key.id: %s", r)
	}
	hiddenAKWantFormat(t, "POST /v1/invites/accept", accepted.APIKey.Token, r)
	e.MustDo(http.StatusOK, "GET", "/v1/me", accepted.APIKey.Token, nil, &me)
	if me.User.ID != accepted.User.ID || me.APIKeyID != accepted.APIKey.ID {
		t.Fatalf("GET /v1/me with the key from the invite = %+v", me)
	}
}

func TestHiddenAPIKeyNeverShownAgain(t *testing.T) {
	e := apitest.New(t)
	k := hiddenAKCreate(t, e, e.Acme.Key, "Deploy")
	_, memberKey := e.AddMember(e.Acme, "member@acme.example", store.RoleMember)
	tokens := []string{k.Token, e.Acme.Key, memberKey}
	e.MustDo(http.StatusOK, "GET", "/v1/org", k.Token, nil, nil) // used once, so it has a last use

	keys, body := hiddenAKList(t, e.Do, e.Acme.Key)
	hiddenAKNoSecrets(t, "GET /v1/api-keys", body, tokens...)
	if len(keys) != 3 {
		t.Fatalf("GET /v1/api-keys lists %d keys, want 3: %s", len(keys), body)
	}
	for id, got := range keys {
		for _, field := range []string{"id", "name", "user_id", "created_at", "last4"} {
			if _, ok := got.raw[field]; !ok {
				t.Errorf("GET /v1/api-keys: the key %s has no %q: %s", id, field, body)
			}
		}
		if tok, ok := got.raw["token"]; ok && string(tok) != `""` && string(tok) != "null" {
			t.Errorf("GET /v1/api-keys: the key %s has a token: %s", id, body)
		}
	}
	got := keys[k.ID]
	if got.Name != "Deploy" || got.UserID != e.Acme.Owner.ID || got.Last4 != k.Token[len(k.Token)-4:] || got.CreatedAt == nil || !got.CreatedAt.Equal(apitest.Start) {
		t.Errorf("GET /v1/api-keys shows the new key with name %q, user_id %q, last4 %q, created_at %v; want Deploy, %s, %q, %s",
			got.Name, got.UserID, got.Last4, got.CreatedAt, e.Acme.Owner.ID, k.Token[len(k.Token)-4:], apitest.Start)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(apitest.Start) || got.RevokedAt != nil {
		t.Errorf("GET /v1/api-keys shows the new key's last_used_at %v and revoked_at %v; want %s and none", got.LastUsedAt, got.RevokedAt, apitest.Start)
	}
	for _, tok := range []string{e.Acme.Key, memberKey} {
		found := false
		for _, kk := range keys {
			found = found || kk.Last4 == tok[len(tok)-4:]
		}
		if !found {
			t.Errorf("GET /v1/api-keys: no key shows last4 %q", tok[len(tok)-4:])
		}
	}

	// nothing else shows a token either
	for _, path := range []string{"/v1/me", "/v1/org", "/v1/members"} {
		r := e.MustDo(http.StatusOK, "GET", path, k.Token, nil, nil)
		hiddenAKNoToken(t, "GET "+path, string(r.Body), tokens...)
	}
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/api-keys/"+k.ID, e.Acme.Key, nil, nil)
	keys, body = hiddenAKList(t, e.Do, e.Acme.Key)
	hiddenAKNoSecrets(t, "GET /v1/api-keys after a revocation", body, tokens...)
	if keys[k.ID].RevokedAt == nil || !keys[k.ID].RevokedAt.Equal(apitest.Start) {
		t.Errorf("GET /v1/api-keys shows the revoked key's revoked_at as %v, want %s", keys[k.ID].RevokedAt, apitest.Start)
	}
}

func TestHiddenAPIKeyAuthentication(t *testing.T) {
	e := apitest.New(t)
	k := hiddenAKCreate(t, e, e.Acme.Key, "Auth")
	e.MustDo(http.StatusOK, "GET", "/v1/org", k.Token, nil, nil)
	tail := k.Token[len(k.Token)-1:]
	other := "A"
	if tail == "A" {
		other = "B"
	}
	for _, bad := range append([]string{
		k.Token[:len(k.Token)-1] + other,
		k.Token + "x",
		k.Token[:len(k.Token)-1],
		"tly_" + hiddenAKHashes(k.Token)[0],
		k.Token[len(k.Token)-4:],
	}, hiddenAKHashes(k.Token)...) {
		if r := e.Do("GET", "/v1/org", bad, nil); r.Code != http.StatusUnauthorized {
			t.Errorf("GET /v1/org with %q (not the token): want 401, got %s", bad, r)
		}
	}
	// revoked keys and keys of removed members get 401, as before
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/api-keys/"+k.ID, e.Acme.Key, nil, nil)
	if r := e.Do("GET", "/v1/org", k.Token, nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked key: want 401, got %s", r)
	}
	u, memberKey := e.AddMember(e.Acme, "leaving@acme.example", store.RoleMember)
	e.MustDo(http.StatusOK, "GET", "/v1/org", memberKey, nil, nil)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/members/"+u.ID, e.Acme.Key, nil, nil)
	if r := e.Do("GET", "/v1/org", memberKey, nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("a removed member's key: want 401, got %s", r)
	}
	e.MustDo(http.StatusOK, "GET", "/v1/org", e.Acme.Key, nil, nil)
}

// hiddenAKServe builds an API server over st (a store loaded from a snapshot) and a function sending requests to it.
func hiddenAKServe(st *store.Store, log *slog.Logger) func(method, path, key string, body any) *apitest.Response {
	clk := clock.NewFake(apitest.Start.Add(24 * time.Hour))
	pay := &payments.Service{Store: st, Gateway: &payments.Fake{}, Clock: clk}
	subs := &subscriptions.Service{Store: st, Payments: pay, Clock: clk}
	srv := api.New(st, api.Options{Clock: clk, Payments: pay, Subscriptions: subs, Logger: log})
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

// hiddenAKSave saves st to path and returns the file's text.
func hiddenAKSave(t *testing.T, st *store.Store, path string) string {
	t.Helper()
	if err := snapshot.Save(st, path, apitest.Start); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var hiddenAKAnyToken = regexp.MustCompile(`tly_[A-Za-z0-9]{32,}`)

// hiddenAKWantHashes fails unless the snapshot keeps the hex SHA-256 of each token (of the whole token, or of the part
// after "tly_").
func hiddenAKWantHashes(t *testing.T, where, text string, tokens ...string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, tok := range tokens {
		if !strings.Contains(lower, hiddenAKHashes(tok)[0]) && !strings.Contains(lower, hiddenAKHashes(tok)[2]) {
			t.Errorf("%s does not keep the token's SHA-256 hash (hex) of %s", where, tok)
		}
	}
}

func TestHiddenAPIKeySnapshots(t *testing.T) {
	e := apitest.New(t)
	k := hiddenAKCreate(t, e, e.Acme.Key, "Snap")
	revoked := hiddenAKCreate(t, e, e.Acme.Key, "Revoked")
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/api-keys/"+revoked.ID, e.Acme.Key, nil, nil)
	gone, goneKey := e.AddMember(e.Acme, "gone@acme.example", store.RoleMember)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/members/"+gone.ID, e.Acme.Key, nil, nil)
	tokens := []string{k.Token, revoked.Token, goneKey, e.Acme.Key, e.Globex.Key}

	path := filepath.Join(t.TempDir(), "tally.json")
	text := hiddenAKSave(t, e.Store, path)
	for _, tok := range tokens {
		if strings.Contains(text, tok) {
			t.Fatalf("the snapshot holds the plain-text token %s:\n%s", tok, hiddenAKShort(text))
		}
	}
	if m := hiddenAKAnyToken.FindString(text); m != "" {
		t.Fatalf("the snapshot holds a plain-text token %s", m)
	}
	hiddenAKWantHashes(t, "the snapshot", text, k.Token, e.Acme.Key, e.Globex.Key) // the keys that work

	st, err := snapshot.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	do := hiddenAKServe(st, nil)
	for _, tok := range []string{k.Token, e.Acme.Key, e.Globex.Key} {
		if r := do("GET", "/v1/org", tok, nil); r.Code != http.StatusOK {
			t.Errorf("after a reload, a key: want 200, got %s", r)
		}
	}
	for _, tok := range []string{revoked.Token, goneKey} {
		if r := do("GET", "/v1/org", tok, nil); r.Code != http.StatusUnauthorized {
			t.Errorf("after a reload, a revoked key or a removed member's key: want 401, got %s", r)
		}
	}
	keys, body := hiddenAKList(t, do, k.Token)
	hiddenAKNoSecrets(t, "GET /v1/api-keys after a reload", body, tokens...)
	if keys[k.ID].Last4 != k.Token[len(k.Token)-4:] {
		t.Errorf("after a reload, GET /v1/api-keys shows last4 %q for a key whose token ends in %q", keys[k.ID].Last4, k.Token[len(k.Token)-4:])
	}
}

// hiddenAKOldSnapshot is a snapshot written before tokens were hashed (version 2, plain tokens in state.api_keys[].token):
// a working key, a revoked one and one of a user who is no longer a member.
const hiddenAKOldSnapshot = `{
  "version": 2,
  "saved_at": "2025-12-01T08:00:00Z",
  "state": {
    "seq": 20,
    "orgs": [{"id": "org_000001", "name": "Old Co", "plan": "team", "time_zone": "UTC", "tax_rate_bp": 0, "created_at": "2025-06-01T08:00:00Z"}],
    "users": [
      {"id": "usr_000002", "email": "owner@old.example", "name": "Old Owner", "created_at": "2025-06-01T08:00:00Z"},
      {"id": "usr_000003", "email": "gone@old.example", "name": "Gone", "created_at": "2025-06-01T08:00:00Z"}
    ],
    "members": [{"org_id": "org_000001", "user_id": "usr_000002", "role": "owner", "created_at": "2025-06-01T08:00:00Z"}],
    "api_keys": [
      {"id": "key_000004", "org_id": "org_000001", "user_id": "usr_000002", "name": "Old key", "token": "tly_5f2b9c0e4a7d4e3fa1c2b3d4e5f6a7b8", "created_at": "2025-06-01T08:00:00Z", "last_used_at": "2025-11-30T08:00:00Z"},
      {"id": "key_000005", "org_id": "org_000001", "user_id": "usr_000002", "name": "Revoked key", "token": "tly_0a1b2c3d4e5f60718293a4b5c6d7e8f9", "created_at": "2025-06-01T08:00:00Z", "revoked_at": "2025-07-01T08:00:00Z"},
      {"id": "key_000006", "org_id": "org_000001", "user_id": "usr_000003", "name": "Gone key", "token": "tly_ffeeddccbbaa99887766554433221100", "created_at": "2025-06-01T08:00:00Z"}
    ],
    "invites": [],
    "customers": [{"id": "cus_000007", "org_id": "org_000001", "name": "Jane", "email": "jane@example.com", "created_at": "2025-06-02T08:00:00Z"}]
  }
}
`

// hiddenAKOldV1Snapshot is a version 1 snapshot (Tally 0.x), also with a plain token.
const hiddenAKOldV1Snapshot = `{
  "version": 1,
  "seq": 3,
  "orgs": [{"id": "org_000001", "name": "Older Co", "tax_rate_bp": 0, "created_at": "2024-05-01T09:00:00Z"}],
  "users": [{"id": "usr_000002", "email": "owner@older.example", "name": "Older Owner", "created_at": "2024-05-01T09:00:00Z"}],
  "members": [{"org_id": "org_000001", "user_id": "usr_000002", "role": "owner", "created_at": "2024-05-01T09:00:00Z"}],
  "api_keys": [{"id": "key_000003", "org_id": "org_000001", "user_id": "usr_000002", "name": "Owner key", "token": "tly_99999999999999999999999999999999", "created_at": "2024-05-01T09:00:00Z"}]
}
`

func TestHiddenAPIKeyOldSnapshots(t *testing.T) {
	dir := t.TempDir()
	const (
		live    = "tly_5f2b9c0e4a7d4e3fa1c2b3d4e5f6a7b8"
		revoked = "tly_0a1b2c3d4e5f60718293a4b5c6d7e8f9"
		gone    = "tly_ffeeddccbbaa99887766554433221100"
		v1      = "tly_99999999999999999999999999999999"
	)
	old := filepath.Join(dir, "old.json")
	if err := os.WriteFile(old, []byte(hiddenAKOldSnapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := snapshot.Load(old)
	if err != nil {
		t.Fatalf("loading a version 2 snapshot written before tokens were hashed: %v", err)
	}
	do := hiddenAKServe(st, nil)
	var me struct {
		APIKeyID string `json:"api_key_id"`
	}
	r := do("GET", "/v1/me", live, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("a key from an old snapshot: want 200, got %s", r)
	}
	r.JSON(t, &me)
	if me.APIKeyID != "key_000004" {
		t.Fatalf("a key from an old snapshot acts as key %q, want key_000004", me.APIKeyID)
	}
	for _, tok := range []string{revoked, gone} {
		if r := do("GET", "/v1/org", tok, nil); r.Code != http.StatusUnauthorized {
			t.Errorf("a revoked key or a removed member's key from an old snapshot: want 401, got %s", r)
		}
	}
	keys, body := hiddenAKList(t, do, live)
	hiddenAKNoSecrets(t, "GET /v1/api-keys after loading an old snapshot", body, live, revoked, gone)
	for id, tok := range map[string]string{"key_000004": live, "key_000005": revoked, "key_000006": gone} {
		if keys[id].Last4 != tok[len(tok)-4:] {
			t.Errorf("GET /v1/api-keys after loading an old snapshot shows last4 %q for %s, whose token ends in %q", keys[id].Last4, id, tok[len(tok)-4:])
		}
	}

	// the next snapshot holds no plain-text token, and the keys still work from it
	next := filepath.Join(dir, "next.json")
	text := hiddenAKSave(t, st, next)
	if m := hiddenAKAnyToken.FindString(text); m != "" {
		t.Fatalf("the snapshot written after loading an old one holds the plain-text token %s:\n%s", m, hiddenAKShort(text))
	}
	hiddenAKWantHashes(t, "the snapshot written after loading an old one", text, live)
	st2, err := snapshot.Load(next)
	if err != nil {
		t.Fatal(err)
	}
	do2 := hiddenAKServe(st2, nil)
	if r := do2("GET", "/v1/org", live, nil); r.Code != http.StatusOK {
		t.Fatalf("a key of an old snapshot after saving and loading again: want 200, got %s", r)
	}
	for _, tok := range []string{revoked, gone} {
		if r := do2("GET", "/v1/org", tok, nil); r.Code != http.StatusUnauthorized {
			t.Errorf("a revoked key or a removed member's key after saving and loading again: want 401, got %s", r)
		}
	}

	// a version 1 snapshot too
	oldV1 := filepath.Join(dir, "v1.json")
	if err := os.WriteFile(oldV1, []byte(hiddenAKOldV1Snapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	st3, err := snapshot.Load(oldV1)
	if err != nil {
		t.Fatalf("loading a version 1 snapshot: %v", err)
	}
	if r := hiddenAKServe(st3, nil)("GET", "/v1/org", v1, nil); r.Code != http.StatusOK {
		t.Fatalf("a key of a version 1 snapshot: want 200, got %s", r)
	}
	if text := hiddenAKSave(t, st3, filepath.Join(dir, "v1-next.json")); strings.Contains(text, v1) {
		t.Fatalf("the snapshot written after loading a version 1 one holds its plain-text token:\n%s", hiddenAKShort(text))
	}
}

func TestHiddenAPIKeyNotLogged(t *testing.T) {
	var logs bytes.Buffer
	e := apitest.New(t)
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	do := hiddenAKServe(e.Store, log)
	r := do("POST", "/v1/api-keys", e.Acme.Key, map[string]string{"name": "Logged?"})
	if r.Code != http.StatusCreated {
		t.Fatalf("POST /v1/api-keys: want 201, got %s", r)
	}
	var k hiddenAKNewKey
	r.JSON(t, &k)
	do("GET", "/v1/org", k.Token, nil)
	do("DELETE", "/v1/api-keys/"+k.ID, e.Acme.Key, nil)
	do("GET", "/v1/org", k.Token, nil)
	do("GET", "/v1/api-keys", e.Acme.Key, nil)
	do("POST", "/v1/invites/accept", "", map[string]string{"token": "nope", "name": "X"})
	hiddenAKNoToken(t, "the server's log", logs.String(), k.Token, e.Acme.Key)
}

func TestHiddenAPIKeyLastUsedThrottle(t *testing.T) {
	e := apitest.New(t)
	k := hiddenAKCreate(t, e, e.Acme.Key, "Busy")
	idle := hiddenAKCreate(t, e, e.Acme.Key, "Idle")
	lastUsed := func() *time.Time {
		t.Helper()
		keys, _ := hiddenAKList(t, e.Do, e.Acme.Key) // a different key
		if got := keys[idle.ID].LastUsedAt; got != nil {
			t.Fatalf("a key that was never used shows last_used_at %s", got)
		}
		return keys[k.ID].LastUsedAt
	}
	use := func() {
		t.Helper()
		e.MustDo(http.StatusOK, "GET", "/v1/org", k.Token, nil, nil)
	}
	want := func(step string, at time.Time) {
		t.Helper()
		got := lastUsed()
		if got == nil || !got.Equal(at) {
			t.Fatalf("%s: last_used_at = %v, want %s", step, got, at.Format(time.RFC3339Nano))
		}
	}
	if got := lastUsed(); got != nil {
		t.Fatalf("a new key shows last_used_at %s", got)
	}
	t0 := apitest.Start
	e.Clock.Set(t0.Add(30 * time.Second))
	use()
	want("the first use", t0.Add(30*time.Second))
	e.Clock.Set(t0.Add(75 * time.Second)) // 45 s after the last use, in the next minute of the clock
	use()
	want("a use 45 seconds after the recorded last use", t0.Add(30*time.Second))
	e.Clock.Set(t0.Add(30*time.Second + 59*time.Second + 999*time.Millisecond))
	use()
	want("a use 59.999 seconds after the recorded last use", t0.Add(30*time.Second))
	e.Clock.Set(t0.Add(91 * time.Second))
	use()
	want("a use 61 seconds after the recorded last use", t0.Add(91*time.Second))
	e.Clock.Set(t0.Add(91*time.Second + 59*time.Second))
	use()
	use()
	want("uses 59 seconds after the recorded last use", t0.Add(91*time.Second))
	e.Clock.Set(t0.Add(91*time.Second + 2*time.Minute))
	use()
	want("a use two minutes after the recorded last use", t0.Add(91*time.Second+2*time.Minute))
}
