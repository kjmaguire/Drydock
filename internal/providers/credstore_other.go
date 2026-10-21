//go:build !windows

package providers

import "errors"

// NewCredentialStore is Windows-only: Drydock ships for Windows (docs/decisions.md P1) and the other platforms have
// no supported credential store here. Development hosts use NewMemoryCredentialStoreForTests in tests.
func NewCredentialStore() (CredentialStore, error) {
	return nil, errors.New("providers: the Windows Credential Manager store is not available on this platform")
}
