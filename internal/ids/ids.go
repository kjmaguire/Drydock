// Package ids generates short prefixed random identifiers ("task_3f9a1c2b7d40").
package ids

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns prefix + "_" + 12 hex characters of randomness.
func New(prefix string) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failure is not recoverable
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
