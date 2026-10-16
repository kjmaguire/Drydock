package updatepolicy

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func signedRelease(t *testing.T) (Offered, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("the installer"))
	f := Facts{Version: "1.2.0", Channel: "stable", Filename: "drydock-1.2.0.exe", Size: 123, Digest: sum[:]}
	return Offered{Version: f.Version, Channel: f.Channel, Filename: f.Filename, Size: f.Size, ArtifactURL: "https://updates.example.com/drydock-1.2.0.exe",
		DigestAlgo: "sha256", Digest: f.Digest, SignatureAlgo: "ed25519", Signature: ed25519.Sign(priv, f.Digest),
		Metadata: map[string]any{StatementKey: SignStatement(priv, f)}}, pub, priv
}

func TestAReleaseSignedOverItsFactsPasses(t *testing.T) {
	o, pub, _ := signedRelease(t)
	if err := Audit(o, pub, "stable"); err != nil {
		t.Fatal(err)
	}
}

func TestAReleaseThatIsNotSignedByThePinnedKeyOverEveryFactIsRefused(t *testing.T) {
	_, otherPub, _ := signedRelease(t)
	for name, mutate := range map[string]func(o *Offered){
		"no signature":             func(o *Offered) { o.Signature, o.SignatureAlgo = nil, "" },
		"signature, no algo":       func(o *Offered) { o.SignatureAlgo = "" },
		"another algorithm":        func(o *Offered) { o.SignatureAlgo = "ecdsa-p256" },
		"a short signature":        func(o *Offered) { o.Signature = o.Signature[:10] },
		"a flipped signature":      func(o *Offered) { s := append([]byte(nil), o.Signature...); s[0] ^= 1; o.Signature = s },
		"a digest of another file": func(o *Offered) { d := sha256.Sum256([]byte("other")); o.Digest = d[:] },
		"no digest":                func(o *Offered) { o.Digest = nil },
		"sha512 named":             func(o *Offered) { o.DigestAlgo = "sha512" },
		"a newer version":          func(o *Offered) { o.Version = "9.9.9" }, // an old signed build relabelled as newer
		"another channel":          func(o *Offered) { o.Channel = "beta" },
		"another file name":        func(o *Offered) { o.Filename = "evil.exe" },
		"another size":             func(o *Offered) { o.Size = 999 },
		"no statement":             func(o *Offered) { o.Metadata = nil },
		"a garbage statement":      func(o *Offered) { o.Metadata = map[string]any{StatementKey: "not base64!"} },
		"a statement of a different release": func(o *Offered) {
			_, _, priv := signedRelease(t)
			o.Metadata = map[string]any{StatementKey: SignStatement(priv, Facts{Version: o.Version, Channel: o.Channel, Filename: o.Filename, Size: o.Size, Digest: o.Digest})}
		},
		"an http artifact":             func(o *Offered) { o.ArtifactURL = "http://updates.example.com/drydock.exe" },
		"an artifact with credentials": func(o *Offered) { o.ArtifactURL = "https://user:pw@updates.example.com/x.exe" },
		"a file artifact":              func(o *Offered) { o.ArtifactURL = "file:///c:/x.exe" },
		"no version":                   func(o *Offered) { o.Version = "" },
	} {
		o, pub, _ := signedRelease(t)
		mutate(&o)
		if err := Audit(o, pub, "stable"); err == nil {
			t.Errorf("%s: the release was accepted", name)
		}
	}
	o, _, _ := signedRelease(t)
	if err := Audit(o, otherPub, "stable"); err == nil {
		t.Error("a release signed by another key was accepted")
	}
	if err := Audit(o, nil, "stable"); err == nil {
		t.Error("an audit with no key passed")
	}
	if err := Audit(o, otherPub[:5], "stable"); err == nil {
		t.Error("an audit with a short key passed")
	}
}

func TestThePinnedKeyAndTheFeedAddressAreChecked(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	good := base64.StdEncoding.EncodeToString(pub)
	if _, err := ParsePublicKey(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "###", base64.StdEncoding.EncodeToString(pub[:31]), base64.StdEncoding.EncodeToString(append([]byte(pub), 0))} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("key %q accepted", bad)
		}
	}
	for _, ok := range []string{"https://updates.example.com/{{channel}}/manifest.json", "https://u.example.com:8443/m.json?x=1"} {
		if err := RequireHTTPS(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://u.example.com/m.json", "ftp://u.example.com/x", "https:///m.json", "https://user:pw@u.example.com/m.json", "//u.example.com/m.json", "", "u.example.com/m.json"} {
		if err := RequireHTTPS(bad); err == nil || !strings.Contains(err.Error(), "https") {
			t.Errorf("%q accepted (%v)", bad, err)
		}
	}
}

func TestTheStatementCannotBeConfusedByNewlinesInFacts(t *testing.T) {
	a := Statement(Facts{Version: "1", Channel: "stable\nfile=x", Filename: "y", Size: 1, Digest: make([]byte, 32)})
	b := Statement(Facts{Version: "1", Channel: "stable", Filename: "x\nfile=y", Size: 1, Digest: make([]byte, 32)})
	if string(a) == string(b) {
		t.Fatal("two different releases have the same statement")
	}
}
