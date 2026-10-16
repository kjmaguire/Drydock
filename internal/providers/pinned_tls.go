package providers

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// pinnedTLSConfig is the TLS configuration for a provider whose certificate a person pinned (TLSPinned): a self-signed
// certificate on a LAN host has no chain to verify, so the server's own certificate is compared with the one the person trusted.
// want is the SHA-256 of that certificate (its DER form, the first one the server sends: the leaf).
//
// This is the certificate pinning that Go's documentation describes for InsecureSkipVerify together with VerifyPeerCertificate:
// the standard chain check is switched off and replaced by a stricter one, in this file and nowhere else. The file is the only
// place in the product that does this, and CodeQL's query for it (go/disabled-certificate-check) cannot see the replacement, so
// the file is left out of code scanning by name in .github/codeql/codeql-config.yml (a test keeps that list to what was reviewed).
// VerifyPeerCertificate runs on every full handshake, and this client keeps no session cache, so no connection is resumed
// without it.
func pinnedTLSConfig(want []byte) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // pinned by SHA-256 in VerifyPeerCertificate
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("providers: no server certificate")
			}
			sum := sha256.Sum256(rawCerts[0])
			if subtle.ConstantTimeCompare(sum[:], want) != 1 {
				return fmt.Errorf("providers: server certificate does not match the pinned fingerprint")
			}
			return nil
		},
	}
}
