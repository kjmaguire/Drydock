package updatepolicy

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Release signatures (audit 2026-10-16, H6).
//
// The Wails updater verifies a signature over the artifact's digest when the release carries one, and installs a release that
// carries none after the digest check alone: an unsigned manifest is accepted even when a public key is pinned. Its signature
// also covers nothing but the digest, so an old, correctly signed build can be published as a newer version. Drydock therefore
// audits the release itself before anything is downloaded: it must be signed by the pinned key, over the digest and over a
// statement that ties the digest to the version, the channel, the file name and the size, and its URLs must be https.

// StatementKey is the metadata key of the manifest that holds the base64 signature of the statement.
const StatementKey = "drydockStatementSig"

// Facts are the fields of a release that its signature ties together.
type Facts struct {
	Version  string
	Channel  string
	Filename string
	Size     int64
	Digest   []byte // SHA-256 of the artifact
}

// Statement is the exact text the release key signs, one fact a line. The text facts are quoted, so a newline or a quote inside
// one cannot move a value into another line; the digest is in hex.
func Statement(f Facts) []byte {
	return []byte(fmt.Sprintf("drydock-update-v1\nversion=%q\nchannel=%q\nfile=%q\nsize=%d\nsha256=%s\n", f.Version, f.Channel, f.Filename, f.Size, hex.EncodeToString(f.Digest)))
}

// SignStatement signs f with the release key (the release tooling's side).
func SignStatement(key ed25519.PrivateKey, f Facts) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, Statement(f)))
}

// ParsePublicKey reads the build's pinned key: base64 of a raw 32-byte Ed25519 public key. Anything else is refused, not
// passed on, so a mistyped key is a build error and not an updater that quietly trusts nothing or the wrong thing.
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("update public key is not base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("update public key is %d bytes; a raw Ed25519 public key is %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// RequireHTTPS refuses a feed or artifact address that is not https with a host (and no credentials in it): the update is
// authenticated by its signature, but the manifest also carries the version and the notes, and a cleartext feed is a cleartext feed.
func RequireHTTPS(raw string) error {
	// the feed address may hold placeholders such as {{channel}}; they are not part of the scheme or the host
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("update address %q is not a URL: %w", raw, err)
	}
	if !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("update address %q must be https://host/... with no credentials", raw)
	}
	return nil
}

// Offered is a release as a provider describes it, in the plain terms the audit needs.
type Offered struct {
	Version, Channel, Filename, ArtifactURL string
	Size                                    int64
	DigestAlgo                              string
	Digest                                  []byte
	SignatureAlgo                           string
	Signature                               []byte
	Metadata                                map[string]any
}

// Audit decides whether a release may be downloaded and installed. wantChannel is the channel this build follows.
func Audit(o Offered, pub ed25519.PublicKey, wantChannel string) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("updates: no valid public key is pinned in this build")
	}
	if o.Version == "" {
		return errors.New("updates: the release has no version")
	}
	if !strings.EqualFold(o.Channel, wantChannel) {
		return fmt.Errorf("updates: the release is for the %q channel, this build follows %q", o.Channel, wantChannel)
	}
	if err := RequireHTTPS(o.ArtifactURL); err != nil {
		return fmt.Errorf("updates: the artifact: %w", err)
	}
	if o.DigestAlgo != "" && !strings.EqualFold(o.DigestAlgo, "sha256") {
		return fmt.Errorf("updates: the digest algorithm %q is not sha256", o.DigestAlgo)
	}
	if len(o.Digest) != 32 {
		return errors.New("updates: the release has no SHA-256 digest")
	}
	if !strings.EqualFold(o.SignatureAlgo, "ed25519") || len(o.Signature) != ed25519.SignatureSize {
		return errors.New("updates: the release is not signed with Ed25519; unsigned releases are never installed")
	}
	if !ed25519.Verify(pub, o.Digest, o.Signature) {
		return errors.New("updates: the release signature does not verify under the key pinned in this build")
	}
	stmt, _ := o.Metadata[StatementKey].(string)
	sig, err := base64.StdEncoding.DecodeString(stmt)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("updates: the release does not carry the signed statement of its version, channel and file")
	}
	facts := Facts{Version: o.Version, Channel: o.Channel, Filename: o.Filename, Size: o.Size, Digest: o.Digest}
	if !ed25519.Verify(pub, Statement(facts), sig) {
		return errors.New("updates: the release's version, channel, file or size is not what the release key signed")
	}
	return nil
}
