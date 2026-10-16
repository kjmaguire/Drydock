package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/updatepolicy"
)

// What the tool writes is exactly what the app's audit accepts, and nothing else is.
func TestTheManifestItWritesPassesTheAppsAudit(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	dir := t.TempDir()
	path := filepath.Join(dir, "drydock-1.2.0.exe")
	if err := os.WriteFile(path, []byte("installer bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := build(priv, path, "1.2.0", "stable", "https://updates.example.com/stable/drydock-1.2.0.exe", "windows", "amd64", "", time.Unix(1760000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	a := m.Artifacts[0]
	digest, _ := base64.StdEncoding.DecodeString(a.Digest)
	sig, _ := base64.StdEncoding.DecodeString(a.Signature)
	o := updatepolicy.Offered{Version: m.Version, Channel: m.Channel, Filename: a.Filename, ArtifactURL: a.URL, Size: a.Size, DigestAlgo: a.DigestAlgo, Digest: digest,
		SignatureAlgo: a.SignatureAlgo, Signature: sig, Metadata: m.Metadata}
	if err := updatepolicy.Audit(o, pub, "stable"); err != nil {
		t.Fatal(err)
	}
	o.Version = "9.9.9"
	if err := updatepolicy.Audit(o, pub, "stable"); err == nil {
		t.Fatal("the same signed release relabelled as a newer version passed")
	}
	if _, err := build(priv, path, "1.2.0", "stable", "http://updates.example.com/x.exe", "windows", "amd64", "", time.Now()); err == nil {
		t.Fatal("an http artifact address was signed")
	}
	if _, err := build(priv, filepath.Join(dir, "missing.exe"), "1.2.0", "stable", "https://u.example.com/x.exe", "windows", "amd64", "", time.Now()); err == nil {
		t.Fatal("a missing file was signed")
	}
}

func TestSigningKeysAreReadAsSeedOrFullKeyOnly(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	for _, ok := range []string{base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(priv.Seed())} {
		if _, err := parsePrivateKey(ok); err != nil {
			t.Errorf("%v", err)
		}
	}
	for _, bad := range []string{"", "###", base64.StdEncoding.EncodeToString(priv[:20])} {
		if _, err := parsePrivateKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
