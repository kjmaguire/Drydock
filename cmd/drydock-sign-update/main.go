// Command drydock-sign-update is the release tooling's side of the update signature (docs/packaging.md section 4, audit
// 2026-10-16, H6). Given the installer it writes the update manifest the app's updater reads: the artifact's SHA-256, an
// Ed25519 signature over that digest, and the signed statement that ties the digest to the version, the channel, the file name
// and the size. Without that statement the app refuses the release (internal/updatepolicy.Audit).
//
//	drydock-sign-update -genkey
//	DRYDOCK_UPDATE_SIGNING_KEY=<base64 private key> drydock-sign-update -artifact drydock-1.2.0.exe -version 1.2.0 \
//	    -channel stable -url https://updates.example.com/stable/drydock-1.2.0.exe > manifest.json
//
// The private key is only ever read from the environment variable, never from the command line.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/updatepolicy"
)

type artifact struct {
	URL           string `json:"url"`
	Filename      string `json:"filename"`
	Filetype      string `json:"filetype,omitempty"`
	Size          int64  `json:"size"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	DigestAlgo    string `json:"digestAlgo"`
	Digest        string `json:"digest"`
	SignatureAlgo string `json:"signatureAlgo"`
	Signature     string `json:"signature"`
}

type manifest struct {
	SchemaVersion int            `json:"schemaVersion"`
	Version       string         `json:"version"`
	Channel       string         `json:"channel"`
	Name          string         `json:"name,omitempty"`
	Notes         string         `json:"notes,omitempty"`
	PublishedAt   string         `json:"publishedAt"`
	Artifacts     []artifact     `json:"artifacts"`
	Metadata      map[string]any `json:"metadata"`
}

// build reads the artifact and returns the manifest that announces it, signed with key.
func build(key ed25519.PrivateKey, path, version, channel, url, platform, arch, notes string, now time.Time) (manifest, error) {
	if err := updatepolicy.RequireHTTPS(url); err != nil {
		return manifest{}, err
	}
	if version == "" || channel == "" {
		return manifest{}, errors.New("-version and -channel are required")
	}
	f, err := os.Open(path)
	if err != nil {
		return manifest{}, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return manifest{}, err
	}
	digest := h.Sum(nil)
	name := filepath.Base(path)
	facts := updatepolicy.Facts{Version: version, Channel: channel, Filename: name, Size: size, Digest: digest}
	return manifest{
		SchemaVersion: 1, Version: version, Channel: channel, Notes: notes, PublishedAt: now.UTC().Format(time.RFC3339),
		Artifacts: []artifact{{URL: url, Filename: name, Filetype: strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."), Size: size, Platform: platform, Arch: arch,
			DigestAlgo: "sha256", Digest: base64.StdEncoding.EncodeToString(digest),
			SignatureAlgo: "ed25519", Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, digest))}},
		Metadata: map[string]any{updatepolicy.StatementKey: updatepolicy.SignStatement(key, facts)},
	}, nil
}

// parsePrivateKey reads base64 of a 64-byte Ed25519 private key, or of its 32-byte seed.
func parsePrivateKey(b64 string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("the signing key is not base64: %w", err)
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	}
	return nil, fmt.Errorf("the signing key is %d bytes; expected %d (seed) or %d", len(raw), ed25519.SeedSize, ed25519.PrivateKeySize)
}

func main() {
	genkey := flag.Bool("genkey", false, "print a new key pair: the public key to pin in the build (ldflags app.UpdatePublicKey) and the private key for DRYDOCK_UPDATE_SIGNING_KEY")
	art := flag.String("artifact", "", "the installer file")
	version := flag.String("version", "", "its version")
	channel := flag.String("channel", "stable", "the channel it is published on")
	url := flag.String("url", "", "where the app downloads it from (https)")
	platform := flag.String("platform", "windows", "platform")
	arch := flag.String("arch", "amd64", "architecture")
	notes := flag.String("notes", "", "release notes")
	flag.Parse()

	if *genkey {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("public (pin in the build):  ", base64.StdEncoding.EncodeToString(pub))
		fmt.Println("private (keep secret, DRYDOCK_UPDATE_SIGNING_KEY):", base64.StdEncoding.EncodeToString(priv))
		return
	}
	key, err := parsePrivateKey(os.Getenv("DRYDOCK_UPDATE_SIGNING_KEY"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "drydock-sign-update:", err)
		os.Exit(2)
	}
	m, err := build(key, *art, *version, *channel, *url, *platform, *arch, *notes, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "drydock-sign-update:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(m)
}
