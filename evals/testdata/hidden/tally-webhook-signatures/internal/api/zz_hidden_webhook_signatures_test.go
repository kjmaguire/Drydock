package api_test

// Hidden acceptance test of the trial task tally-webhook-signatures (evals/tasks). The agents never see it.
// It only uses the HTTP API and the fixture's existing Go API, and computes every expected signature itself.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
)

// hiddenSigHMAC is the hex HMAC-SHA256 of "<ts>.<body>" keyed with secret.
func hiddenSigHMAC(secret string, ts int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10) + "."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// hiddenSigWant is the Tally-Signature header the task specifies for an attempt at `at`: t=<unix>,v1=<sig>[,v1=<sig>].
func hiddenSigWant(at time.Time, body []byte, secrets ...string) string {
	ts := at.Unix()
	h := "t=" + strconv.FormatInt(ts, 10)
	for _, s := range secrets {
		h += ",v1=" + hiddenSigHMAC(s, ts, body)
	}
	return h
}

type hiddenSigEndpoint struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Secret  string `json:"secret"`
	Enabled bool   `json:"enabled"`
}

var hiddenSigSecretRe = regexp.MustCompile(`^whsec_[0-9a-f]{32}$`)

func hiddenSigCreateEndpoint(t *testing.T, e *apitest.Env, url string) hiddenSigEndpoint {
	t.Helper()
	var ep hiddenSigEndpoint
	e.MustDo(http.StatusCreated, "POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": url, "events": []string{"*"}}, &ep)
	if ep.ID == "" || ep.Secret == "" {
		t.Fatalf("created endpoint = %+v", ep)
	}
	return ep
}

func hiddenSigRotate(t *testing.T, e *apitest.Env, ep hiddenSigEndpoint) hiddenSigEndpoint {
	t.Helper()
	r := e.Do("POST", "/v1/webhook-endpoints/"+ep.ID+"/rotate-secret", e.Acme.Key, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("POST /v1/webhook-endpoints/%s/rotate-secret: want 200 with the endpoint, got %s", ep.ID, r)
	}
	var got hiddenSigEndpoint
	r.JSON(t, &got)
	if got.ID != ep.ID {
		t.Fatalf("rotate-secret answered another object: want the endpoint %s, got %s", ep.ID, r)
	}
	if !hiddenSigSecretRe.MatchString(got.Secret) {
		t.Fatalf("rotate-secret: want a new secret whsec_ followed by 32 hex characters, got %q (%s)", got.Secret, r)
	}
	if got.Secret == ep.Secret {
		t.Fatalf("rotate-secret answered the old secret %q, want a new one", got.Secret)
	}
	return got
}

// hiddenSigDeliver publishes an event at the clock's current time, runs the deliveries and returns the Tally-Signature
// header and the body that the endpoint at url received.
func hiddenSigDeliver(t *testing.T, e *apitest.Env, url string) (string, []byte) {
	t.Helper()
	ctx := context.Background()
	before := len(e.HTTP.Requests())
	if err := e.Webhooks.Publish(ctx, e.Acme.ID, "customer.updated", map[string]any{"id": "cus_hidden", "at": e.Clock.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Webhooks.RunDue(ctx); err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	var found []apitest.Request
	for _, r := range e.HTTP.Requests()[before:] {
		if r.URL == url {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one delivery to %s, got %d", url, len(found))
	}
	return found[0].Header.Get("Tally-Signature"), found[0].Body
}

func hiddenSigExpect(t *testing.T, what, got string, at time.Time, body []byte, secrets ...string) {
	t.Helper()
	if want := hiddenSigWant(at, body, secrets...); got != want {
		t.Fatalf("%s: Tally-Signature\n got %q\nwant %q\n(t is the attempt time %s, then one v1 per secret: %d expected)", what, got, want, at.Format(time.RFC3339), len(secrets))
	}
}

func TestHiddenWebhookSignatureHeaderFormat(t *testing.T) {
	e := apitest.New(t)
	ep := hiddenSigCreateEndpoint(t, e, "https://hooks.acme.example/sig-format")

	// The event is published now and attempted 90 seconds later: t is the time of the attempt.
	ctx := context.Background()
	if err := e.Webhooks.Publish(ctx, e.Acme.ID, "customer.updated", map[string]string{"id": "cus_early"}); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(90 * time.Second)
	if _, err := e.Webhooks.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	reqs := e.HTTP.Requests()
	if len(reqs) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(reqs))
	}
	hiddenSigExpect(t, "a delivery attempted 90 seconds after its event", reqs[0].Header.Get("Tally-Signature"), e.Clock.Now(), reqs[0].Body, ep.Secret)

	e.Clock.Advance(3*time.Hour + 7*time.Second)
	got, body := hiddenSigDeliver(t, e, ep.URL)
	hiddenSigExpect(t, "a later delivery", got, e.Clock.Now(), body, ep.Secret)
}

func TestHiddenWebhookSecretRotationWindow(t *testing.T) {
	e := apitest.New(t)
	ep := hiddenSigCreateEndpoint(t, e, "https://hooks.acme.example/sig-rotate")
	s0 := ep.Secret

	e.Clock.Advance(time.Minute)
	rotatedAt := e.Clock.Now()
	ep = hiddenSigRotate(t, e, ep)
	s1 := ep.Secret

	var shown hiddenSigEndpoint
	e.MustDo(http.StatusOK, "GET", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, nil, &shown)
	if shown.Secret != s1 {
		t.Fatalf("GET after the rotation shows the secret %q, want the new secret %q", shown.Secret, s1)
	}

	got, body := hiddenSigDeliver(t, e, ep.URL)
	hiddenSigExpect(t, "right after a rotation (new secret first, previous second)", got, e.Clock.Now(), body, s1, s0)

	e.Clock.Set(rotatedAt.Add(23*time.Hour + 30*time.Minute))
	got, body = hiddenSigDeliver(t, e, ep.URL)
	hiddenSigExpect(t, "23.5 hours after a rotation", got, e.Clock.Now(), body, s1, s0)

	e.Clock.Set(rotatedAt.Add(24*time.Hour + time.Minute))
	got, body = hiddenSigDeliver(t, e, ep.URL)
	hiddenSigExpect(t, "24 hours and a minute after a rotation (only the new secret)", got, e.Clock.Now(), body, s1)

	// Two rotations an hour apart: the oldest secret is dropped at once, and the second rotation starts its own 24 hours.
	e.Clock.Advance(time.Hour)
	ep = hiddenSigRotate(t, e, ep)
	s2 := ep.Secret
	got, body = hiddenSigDeliver(t, e, ep.URL)
	hiddenSigExpect(t, "after the second rotation", got, e.Clock.Now(), body, s2, s1)

	e.Clock.Advance(time.Hour)
	latestAt := e.Clock.Now()
	ep = hiddenSigRotate(t, e, ep)
	s3 := ep.Secret
	if s3 == s1 || s3 == s2 {
		t.Fatalf("rotate-secret reused an old secret: %q", s3)
	}
	got, body = hiddenSigDeliver(t, e, ep.URL)
	hiddenSigExpect(t, "a rotation an hour after the previous one (the oldest secret is dropped)", got, e.Clock.Now(), body, s3, s2)

	e.Clock.Set(latestAt.Add(23 * time.Hour))
	got, body = hiddenSigDeliver(t, e, ep.URL)
	hiddenSigExpect(t, "23 hours after the latest rotation (24 hours after the one before)", got, e.Clock.Now(), body, s3, s2)

	e.Clock.Set(latestAt.Add(24*time.Hour + time.Minute))
	got, body = hiddenSigDeliver(t, e, ep.URL)
	hiddenSigExpect(t, "24 hours and a minute after the latest rotation", got, e.Clock.Now(), body, s3)
}

func TestHiddenWebhookRotateSecretIsTheOrganizations(t *testing.T) {
	e := apitest.New(t)
	ep := hiddenSigCreateEndpoint(t, e, "https://hooks.acme.example/sig-tenant")
	if r := e.Do("POST", "/v1/webhook-endpoints/"+ep.ID+"/rotate-secret", e.Globex.Key, nil); r.Code != http.StatusNotFound {
		t.Fatalf("Globex rotates Acme's endpoint: want 404, got %s", r)
	}
	if r := e.Do("POST", "/v1/webhook-endpoints/we_hidden_missing/rotate-secret", e.Acme.Key, nil); r.Code != http.StatusNotFound {
		t.Fatalf("rotate an endpoint that does not exist: want 404, got %s", r)
	}
	got, body := hiddenSigDeliver(t, e, ep.URL)
	hiddenSigExpect(t, "after refused rotations", got, e.Clock.Now(), body, ep.Secret)
	if strings.Count(got, "v1=") != 1 {
		t.Fatalf("a refused rotation changed the signing secrets: %s", got)
	}
}
