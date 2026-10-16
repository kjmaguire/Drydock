//go:build windows

package providers

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// keyringStore is the Windows Credential Manager through github.com/zalando/go-keyring. The Windows backend rejects
// values over 2560 bytes (ErrSetDataTooBig); Vault then encrypts them into SQLite instead.
type keyringStore struct{}

// NewCredentialStore returns the Windows Credential Manager store. UNPROVEN until the Windows job runs
// (docs/decisions.md P2): see credstore_windows_test.go.
func NewCredentialStore() (CredentialStore, error) { return keyringStore{}, nil }

func (keyringStore) Set(service, user, secret string) error {
	err := keyring.Set(service, user, secret)
	if errors.Is(err, keyring.ErrSetDataTooBig) {
		return ErrCredTooBig
	}
	return err
}

func (keyringStore) Get(service, user string) (string, error) {
	s, err := keyring.Get(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrCredNotFound
	}
	return s, err
}

func (keyringStore) Delete(service, user string) error {
	err := keyring.Delete(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrCredNotFound
	}
	return err
}
