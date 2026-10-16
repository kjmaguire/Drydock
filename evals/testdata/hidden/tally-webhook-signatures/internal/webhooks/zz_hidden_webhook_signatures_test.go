package webhooks_test

// Hidden acceptance test of the trial task tally-webhook-signatures (evals/tasks). The agents never see it.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/webhooks"
)

func hiddenVerifyHMAC(secret string, msg []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(msg)
	return hex.EncodeToString(m.Sum(nil))
}

// hiddenVerifySig is the v1 signature of body at the Unix time ts: HMAC-SHA256 of "<ts>.<body>".
func hiddenVerifySig(secret string, ts int64, body []byte) string {
	return hiddenVerifyHMAC(secret, append([]byte(strconv.FormatInt(ts, 10)+"."), body...))
}

var hiddenVerifyErrors = []struct {
	name string
	err  error
}{
	{"ErrMalformedHeader", webhooks.ErrMalformedHeader},
	{"ErrNoTimestamp", webhooks.ErrNoTimestamp},
	{"ErrTimestampTolerance", webhooks.ErrTimestampTolerance},
	{"ErrNoSignature", webhooks.ErrNoSignature},
	{"ErrSignatureMismatch", webhooks.ErrSignatureMismatch},
}

func TestHiddenSignKeepsItsMeaning(t *testing.T) {
	const want = "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
	if got := webhooks.Sign("key", []byte("The quick brown fox jumps over the lazy dog")); got != want {
		t.Fatalf("Sign(key, fox) = %s, want %s (the hex HMAC-SHA256 of the body)", got, want)
	}
	body := []byte(`{"id":"evt_1"}`)
	if got, want := webhooks.Sign("whsec_x", body), hiddenVerifyHMAC("whsec_x", body); got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
}

func TestHiddenSignatureHeaderBuildsTheHeader(t *testing.T) {
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ts := at.Unix()
	body := []byte(`{"id":"evt_1","type":"invoice.paid"}`)
	one := "t=" + strconv.FormatInt(ts, 10) + ",v1=" + hiddenVerifySig("whsec_current", ts, body)
	if got := webhooks.SignatureHeader([]string{"whsec_current"}, at, body); got != one {
		t.Fatalf("SignatureHeader(one secret)\n got %q\nwant %q", got, one)
	}
	two := one + ",v1=" + hiddenVerifySig("whsec_previous", ts, body)
	if got := webhooks.SignatureHeader([]string{"whsec_current", "whsec_previous"}, at, body); got != two {
		t.Fatalf("SignatureHeader(current, previous)\n got %q\nwant %q", got, two)
	}
	// The same instant in another time zone is the same Unix time.
	if got := webhooks.SignatureHeader([]string{"whsec_current"}, at.In(time.FixedZone("UTC+5", 5*3600)), body); got != one {
		t.Fatalf("SignatureHeader with a time in another zone\n got %q\nwant %q", got, one)
	}
}

func TestHiddenVerifyAccepts(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ts := now.Unix()
	tss := strconv.FormatInt(ts, 10)
	body := []byte(`{"id":"evt_42","type":"customer.created"}`)
	secret := "whsec_hidden_current"
	good := hiddenVerifySig(secret, ts, body)
	at := func(d time.Duration) (string, string) {
		x := now.Add(d).Unix()
		return strconv.FormatInt(x, 10), hiddenVerifySig(secret, x, body)
	}
	early, earlySig := at(-4*time.Minute - 59*time.Second)
	late, lateSig := at(4*time.Minute + 59*time.Second)
	nineAgo, nineAgoSig := at(-9 * time.Minute)
	nineAhead, nineAheadSig := at(9 * time.Minute)

	for _, c := range []struct {
		name      string
		header    string
		secrets   []string
		tolerance time.Duration
	}{
		{"the header SignatureHeader builds", webhooks.SignatureHeader([]string{secret}, now, body), []string{secret}, 0},
		{"t then v1", "t=" + tss + ",v1=" + good, []string{secret}, 0},
		{"v1 then t", "v1=" + good + ",t=" + tss, []string{secret}, 0},
		{"spaces around each pair", " t=" + tss + " , v1=" + good + " ", []string{secret}, 0},
		{"spaces after the commas", "t=" + tss + ", v1=" + good, []string{secret}, 0},
		{"unknown keys are ignored", "t=" + tss + ",v0=" + hiddenVerifyHMAC(secret, body) + ",v1=" + good + ",scheme=x", []string{secret}, 0},
		{"several v1, the second matches", "t=" + tss + ",v1=" + hiddenVerifySig("whsec_other", ts, body) + ",v1=" + good, []string{secret}, 0},
		{"several v1, the first matches", "t=" + tss + ",v1=" + good + ",v1=00", []string{secret}, 0},
		{"the second of several secrets", "t=" + tss + ",v1=" + good, []string{"whsec_new_one", secret}, 0},
		{"the first of several secrets", "t=" + tss + ",v1=" + good, []string{secret, "whsec_new_one"}, 0},
		{"a rotation header verified with the previous secret only", webhooks.SignatureHeader([]string{"whsec_new_one", secret}, now, body), []string{secret}, 0},
		{"tolerance 0 is 5 minutes: 4m59s old", "t=" + early + ",v1=" + earlySig, []string{secret}, 0},
		{"tolerance 0 is 5 minutes: 4m59s ahead", "t=" + late + ",v1=" + lateSig, []string{secret}, 0},
		{"tolerance 10m: 9m old", "t=" + nineAgo + ",v1=" + nineAgoSig, []string{secret}, 10 * time.Minute},
		{"tolerance 10m: 9m ahead", "t=" + nineAhead + ",v1=" + nineAheadSig, []string{secret}, 10 * time.Minute},
	} {
		if err := webhooks.Verify(body, c.header, c.secrets, c.tolerance, now); err != nil {
			t.Errorf("%s: Verify(%q, secrets %q, tolerance %v) = %v, want nil", c.name, c.header, c.secrets, c.tolerance, err)
		}
	}
}

func TestHiddenVerifyErrorsInOrder(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ts := now.Unix()
	tss := strconv.FormatInt(ts, 10)
	body := []byte(`{"id":"evt_42","type":"customer.created"}`)
	secret := "whsec_hidden_current"
	good := hiddenVerifySig(secret, ts, body)
	at := func(d time.Duration) (string, string) {
		x := now.Add(d).Unix()
		return strconv.FormatInt(x, 10), hiddenVerifySig(secret, x, body)
	}
	old, oldSig := at(-5*time.Minute - time.Second)
	ahead, aheadSig := at(5*time.Minute + time.Second)
	twoAgo, twoAgoSig := at(-2 * time.Minute)
	elevenAhead, elevenAheadSig := at(11 * time.Minute)

	for _, c := range []struct {
		name      string
		header    string
		body      []byte
		secrets   []string
		tolerance time.Duration
		want      error
	}{
		{"an empty header", "", body, []string{secret}, 0, webhooks.ErrMalformedHeader},
		{"a pair without =", "t=" + tss + ",v1", body, []string{secret}, 0, webhooks.ErrMalformedHeader},
		{"a pair without = next to a good signature", "t=" + tss + ",v1=" + good + ",garbage", body, []string{secret}, 0, webhooks.ErrMalformedHeader},
		{"a t that is not a number", "t=yesterday,v1=" + good, body, []string{secret}, 0, webhooks.ErrMalformedHeader},
		{"a t that is not an integer", "t=" + tss + ".5,v1=" + good, body, []string{secret}, 0, webhooks.ErrMalformedHeader},
		{"an empty t", "t=,v1=" + good, body, []string{secret}, 0, webhooks.ErrMalformedHeader},
		{"malformed before no timestamp", "v1=" + good + ",junk", body, []string{secret}, 0, webhooks.ErrMalformedHeader},
		{"a bad t without v1", "t=abc", body, []string{secret}, 0, webhooks.ErrMalformedHeader},
		{"malformed before tolerance", "t=" + old + ",v1=" + oldSig + ",junk", body, []string{secret}, 0, webhooks.ErrMalformedHeader},

		{"no t", "v1=" + good, body, []string{secret}, 0, webhooks.ErrNoTimestamp},
		{"no t, only unknown keys and v1", "v0=abc,v1=" + good, body, []string{secret}, 0, webhooks.ErrNoTimestamp},

		{"5m01s old with tolerance 0", "t=" + old + ",v1=" + oldSig, body, []string{secret}, 0, webhooks.ErrTimestampTolerance},
		{"5m01s ahead with tolerance 0", "t=" + ahead + ",v1=" + aheadSig, body, []string{secret}, 0, webhooks.ErrTimestampTolerance},
		{"2m old with a tolerance of 1m", "t=" + twoAgo + ",v1=" + twoAgoSig, body, []string{secret}, time.Minute, webhooks.ErrTimestampTolerance},
		{"11m ahead with a tolerance of 10m", "t=" + elevenAhead + ",v1=" + elevenAheadSig, body, []string{secret}, 10 * time.Minute, webhooks.ErrTimestampTolerance},
		{"tolerance before no signature", "t=" + old, body, []string{secret}, 0, webhooks.ErrTimestampTolerance},
		{"tolerance before mismatch", "t=" + old + ",v1=00ff", body, []string{secret}, 0, webhooks.ErrTimestampTolerance},

		{"no v1", "t=" + tss, body, []string{secret}, 0, webhooks.ErrNoSignature},
		{"only v0", "t=" + tss + ",v0=" + good, body, []string{secret}, 0, webhooks.ErrNoSignature},

		{"a wrong signature", "t=" + tss + ",v1=" + hiddenVerifySig("whsec_someone_else", ts, body), body, []string{secret}, 0, webhooks.ErrSignatureMismatch},
		{"another payload", "t=" + tss + ",v1=" + good, []byte(`{"id":"evt_43","type":"customer.created"}`), []string{secret}, 0, webhooks.ErrSignatureMismatch},
		{"the signature of another t", "t=" + strconv.FormatInt(ts+1, 10) + ",v1=" + good, body, []string{secret}, 0, webhooks.ErrSignatureMismatch},
		{"the old scheme (HMAC of the body alone)", "t=" + tss + ",v1=" + hiddenVerifyHMAC(secret, body), body, []string{secret}, 0, webhooks.ErrSignatureMismatch},
		{"none of the secrets", "t=" + tss + ",v1=" + good, body, []string{"whsec_a", "whsec_b"}, 0, webhooks.ErrSignatureMismatch},
		{"several v1, none matches", "t=" + tss + ",v1=00,v1=" + hiddenVerifySig("whsec_a", ts, body), body, []string{secret}, 0, webhooks.ErrSignatureMismatch},
	} {
		err := webhooks.Verify(c.body, c.header, c.secrets, c.tolerance, now)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: Verify(%q, tolerance %v) = %v, want an error that errors.Is %v", c.name, c.header, c.tolerance, err, c.want)
			continue
		}
		for _, other := range hiddenVerifyErrors {
			if other.err != c.want && errors.Is(err, other.err) {
				t.Errorf("%s: Verify(%q) = %v, which also matches %s; the errors are checked in order and only the first applies", c.name, c.header, err, other.name)
			}
		}
	}
}

// TestHiddenVerifyARealDelivery checks a delivery the service sent, before and after a rotation, with Verify.
func TestHiddenVerifyARealDelivery(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	var ep struct {
		ID     string `json:"id"`
		URL    string `json:"url"`
		Secret string `json:"secret"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": "https://hooks.acme.example/verify", "events": []string{"*"}}, &ep)
	deliver := func() apitest.Request {
		t.Helper()
		before := len(e.HTTP.Requests())
		if err := e.Webhooks.Publish(ctx, e.Acme.ID, "customer.updated", map[string]string{"id": "cus_v"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Webhooks.RunDue(ctx); err != nil {
			t.Fatal(err)
		}
		reqs := e.HTTP.Requests()[before:]
		if len(reqs) != 1 {
			t.Fatalf("want 1 delivery, got %d", len(reqs))
		}
		return reqs[0]
	}
	r := deliver()
	h := r.Header.Get("Tally-Signature")
	if err := webhooks.Verify(r.Body, h, []string{ep.Secret}, 0, e.Clock.Now()); err != nil {
		t.Fatalf("Verify(the delivery, its endpoint's secret) = %v, want nil (header %q)", err, h)
	}
	if err := webhooks.Verify(r.Body, h, []string{ep.Secret}, 0, e.Clock.Now().Add(6*time.Minute)); !errors.Is(err, webhooks.ErrTimestampTolerance) {
		t.Fatalf("Verify 6 minutes later = %v, want ErrTimestampTolerance", err)
	}

	var rotated struct {
		Secret string `json:"secret"`
	}
	e.MustDo(http.StatusOK, "POST", "/v1/webhook-endpoints/"+ep.ID+"/rotate-secret", e.Acme.Key, nil, &rotated)
	e.Clock.Advance(time.Hour)
	r = deliver()
	h = r.Header.Get("Tally-Signature")
	for _, secrets := range [][]string{{rotated.Secret}, {ep.Secret}, {"whsec_unrelated", ep.Secret}} {
		if err := webhooks.Verify(r.Body, h, secrets, 0, e.Clock.Now()); err != nil {
			t.Fatalf("an hour after a rotation, Verify(secrets %q) = %v, want nil (header %q)", secrets, err, h)
		}
	}
	if err := webhooks.Verify(r.Body, h, []string{"whsec_unrelated"}, 0, e.Clock.Now()); !errors.Is(err, webhooks.ErrSignatureMismatch) {
		t.Fatalf("Verify with an unrelated secret = %v, want ErrSignatureMismatch", err)
	}

	e.Clock.Advance(24 * time.Hour)
	r = deliver()
	h = r.Header.Get("Tally-Signature")
	if err := webhooks.Verify(r.Body, h, []string{rotated.Secret}, 0, e.Clock.Now()); err != nil {
		t.Fatalf("25 hours after a rotation, Verify(new secret) = %v, want nil (header %q)", err, h)
	}
	if err := webhooks.Verify(r.Body, h, []string{ep.Secret}, 0, e.Clock.Now()); !errors.Is(err, webhooks.ErrSignatureMismatch) {
		t.Fatalf("25 hours after a rotation, Verify(previous secret) = %v, want ErrSignatureMismatch (header %q)", err, h)
	}
}
