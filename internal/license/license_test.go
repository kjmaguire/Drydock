package license_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/license"
)

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

var now = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func payload(exp time.Time, grace int) license.Payload {
	return license.Payload{KID: "k1", JTI: "lic_1", Kind: "paid", Sub: "cus_1", NBF: now.Add(-time.Hour).Unix(), EXP: exp.Unix(), GraceDays: grace, IAT: now.Unix()}
}

func TestSignParseAndTamperDetection(t *testing.T) {
	pub, priv := keypair(t)
	keys := license.Keys{"k1": pub}
	tok := license.Sign(priv, payload(now.AddDate(0, 1, 0), 7))
	p, err := license.Parse(tok, keys)
	if err != nil || p.Sub != "cus_1" {
		t.Fatalf("%+v %v", p, err)
	}
	// changing a single payload byte invalidates the signature
	parts := strings.Split(tok, ".")
	forged := parts[0][:len(parts[0])-2] + "AA" + "." + parts[1]
	if _, err := license.Parse(forged, keys); err == nil {
		t.Fatal("forged payload verified")
	}
	// a token signed by another key, or with an unknown kid, is refused
	_, other := keypair(t)
	if _, err := license.Parse(license.Sign(other, payload(now.AddDate(0, 1, 0), 0)), keys); !errors.Is(err, license.ErrSignature) {
		t.Fatalf("foreign signature: %v", err)
	}
	p2 := payload(now.AddDate(0, 1, 0), 0)
	p2.KID = "unknown"
	if _, err := license.Parse(license.Sign(priv, p2), keys); !errors.Is(err, license.ErrSignature) {
		t.Fatalf("unknown kid: %v", err)
	}
	for _, bad := range []string{"", "x", "a.b.c", "!!!.???"} {
		if _, err := license.Parse(bad, keys); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestKeyRotationAcceptsBothKeys(t *testing.T) {
	pubOld, privOld := keypair(t)
	pubNew, privNew := keypair(t)
	keys := license.Keys{"old": pubOld, "new": pubNew}
	for kid, priv := range map[string]ed25519.PrivateKey{"old": privOld, "new": privNew} {
		p := payload(now.AddDate(1, 0, 0), 0)
		p.KID = kid
		if _, err := license.Parse(license.Sign(priv, p), keys); err != nil {
			t.Fatalf("%s: %v", kid, err)
		}
	}
}

func TestStatesAndReadOnlyBehaviour(t *testing.T) {
	cases := []struct {
		name     string
		p        *license.Payload
		at       time.Time
		want     license.State
		readOnly bool
	}{
		{"none", nil, now, license.StateNone, true},
		{"active", ptr(payload(now.AddDate(0, 0, 10), 7)), now, license.StateActive, false},
		{"trial", ptr(func() license.Payload { p := payload(now.AddDate(0, 0, 10), 0); p.Kind = "trial"; return p }()), now, license.StateTrial, false},
		{"grace", ptr(payload(now.AddDate(0, 0, -3), 7)), now, license.StateGrace, false},
		{"expired", ptr(payload(now.AddDate(0, 0, -10), 7)), now, license.StateExpired, true},
	}
	for _, c := range cases {
		st, err := license.Evaluate(c.p, c.at, time.Time{})
		if err != nil || st.State != c.want || st.ReadOnly() != c.readOnly {
			t.Errorf("%s: %+v %v", c.name, st, err)
		}
	}
	exp, _ := license.Evaluate(ptr(payload(now.AddDate(0, 0, -10), 0)), now, time.Time{})
	for _, a := range []string{"read", "export", "backup", "restore", "support_bundle", "import_license"} {
		if !exp.Allows(a) {
			t.Errorf("expired state must still allow %s", a)
		}
	}
	for _, a := range []string{"start_agent", "start_task", "merge", "create_pr", "add_connection", "approve"} {
		if exp.Allows(a) {
			t.Errorf("expired state must refuse %s", a)
		}
	}
}

func TestClockSetBackDoesNotExtendValidity(t *testing.T) {
	p := ptr(payload(now.AddDate(0, 0, 1), 0))
	// the app has seen a time 30 days after expiry; the system clock now says it is still valid
	st, _ := license.Evaluate(p, now, now.AddDate(0, 0, 31))
	if st.State != license.StateExpired {
		t.Fatalf("clock rollback extended the license: %+v", st)
	}
	// small skew is tolerated
	st, _ = license.Evaluate(p, now, now.Add(2*time.Hour))
	if st.State != license.StateActive {
		t.Fatalf("small skew rejected: %+v", st)
	}
	// nbf in the future is an explicit error
	future := payload(now.AddDate(0, 1, 0), 0)
	future.NBF = now.AddDate(0, 0, 3).Unix()
	if _, err := license.Evaluate(&future, now, time.Time{}); !errors.Is(err, license.ErrNotYet) {
		t.Fatalf("future nbf: %v", err)
	}
}

func TestChannelEntitlement(t *testing.T) {
	p := payload(now.AddDate(1, 0, 0), 0)
	if !p.ChannelAllowed("stable") || p.ChannelAllowed("beta") {
		t.Fatal("default entitlement is stable only")
	}
	p.Channels = []string{"stable", "beta"}
	if !p.ChannelAllowed("beta") {
		t.Fatal("beta entitled")
	}
}

func ptr[T any](v T) *T { return &v }
