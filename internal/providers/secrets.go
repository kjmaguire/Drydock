package providers

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"

	"github.com/kjmaguire/drydock/internal/storage"
)

// SecretStore holds secret values behind opaque references; providers.api_key_ref is such a reference and the
// value never reaches SQLite columns, logs or events. A missing or locked store is an error, never a plaintext
// fallback (docs/security.md s8).
type SecretStore interface {
	Put(ctx context.Context, ref, value string) error
	Get(ctx context.Context, ref string) (string, error) // ErrSecretNotFound when absent
	Delete(ctx context.Context, ref string) error        // idempotent
}

// MaxCredentialBytes is the Windows Credential Manager blob limit (CRED_MAX_CREDENTIAL_BLOB_SIZE, 5*512 bytes),
// enforced by go-keyring on Windows (docs/phase0/go-platform.md s3).
const MaxCredentialBytes = 2560

// Errors from a CredentialStore.
var (
	ErrCredNotFound = errors.New("providers: credential not found")
	ErrCredTooBig   = errors.New("providers: credential exceeds the credential store limit")
)

// CredentialStore is the OS credential store: small values only. The Windows implementation wraps
// github.com/zalando/go-keyring (Credential Manager); see credstore_windows.go.
type CredentialStore interface {
	Set(service, user, secret string) error
	Get(service, user string) (string, error) // ErrCredNotFound when absent
	Delete(service, user string) error        // ErrCredNotFound when absent
}

const (
	credService = "drydock"
	kekUser     = "drydock/secret-kek/v1"
)

// Vault is the product SecretStore: values up to MaxCredentialBytes live in the credential store; larger values are
// AES-256-GCM encrypted into the secret_blobs table with the key held in the credential store.
type Vault struct {
	cred CredentialStore
	db   *storage.DB
	mu   sync.Mutex
}

// NewVault builds the product secret store over cred and db.
func NewVault(cred CredentialStore, db *storage.DB) *Vault { return &Vault{cred: cred, db: db} }

// Put stores value under ref, replacing any earlier value in either location.
func (v *Vault) Put(ctx context.Context, ref, value string) error {
	if ref == "" {
		return fmt.Errorf("%w: empty secret reference", ErrInvalid)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(value) <= MaxCredentialBytes {
		if err := v.cred.Set(credService, ref, value); err != nil {
			if errors.Is(err, ErrCredTooBig) {
				return v.putBlobLocked(ctx, ref, value)
			}
			return fmt.Errorf("credential store: %w", err)
		}
		return v.deleteBlobLocked(ctx, ref)
	}
	return v.putBlobLocked(ctx, ref, value)
}

func (v *Vault) putBlobLocked(ctx context.Context, ref, value string) error {
	gcm, err := v.aeadLocked(true)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ct := gcm.Seal(nil, nonce, []byte(value), []byte(ref)) // the reference is bound as additional data
	err = v.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO secret_blobs(ref,nonce,ciphertext,created_at) VALUES(?,?,?,?)
			ON CONFLICT(ref) DO UPDATE SET nonce=excluded.nonce, ciphertext=excluded.ciphertext, created_at=excluded.created_at`,
			ref, nonce, ct, storage.Now())
		return err
	})
	if err != nil {
		return err
	}
	if err := v.cred.Delete(credService, ref); err != nil && !errors.Is(err, ErrCredNotFound) {
		return fmt.Errorf("credential store: %w", err)
	}
	return nil
}

// Get returns the value for ref.
func (v *Vault) Get(ctx context.Context, ref string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, err := v.cred.Get(credService, ref)
	if err == nil {
		return s, nil
	}
	if !errors.Is(err, ErrCredNotFound) {
		return "", fmt.Errorf("credential store: %w", err)
	}
	var nonce, ct []byte
	err = v.db.Reader().QueryRowContext(ctx, `SELECT nonce,ciphertext FROM secret_blobs WHERE ref=?`, ref).Scan(&nonce, &ct)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %s", ErrSecretNotFound, ref)
	}
	if err != nil {
		return "", err
	}
	gcm, err := v.aeadLocked(false)
	if err != nil {
		return "", err
	}
	pt, err := gcm.Open(nil, nonce, ct, []byte(ref))
	if err != nil {
		return "", fmt.Errorf("providers: secret %s cannot be decrypted (wrong key or tampered value): %w", ref, err)
	}
	return string(pt), nil
}

// Delete removes ref from both locations.
func (v *Vault) Delete(ctx context.Context, ref string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.cred.Delete(credService, ref); err != nil && !errors.Is(err, ErrCredNotFound) {
		return fmt.Errorf("credential store: %w", err)
	}
	return v.deleteBlobLocked(ctx, ref)
}

func (v *Vault) deleteBlobLocked(ctx context.Context, ref string) error {
	return v.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM secret_blobs WHERE ref=?`, ref)
		return err
	})
}

// aeadLocked returns the AES-256-GCM instance keyed by the key-encryption key in the credential store; create
// generates the key when none exists.
func (v *Vault) aeadLocked(create bool) (cipher.AEAD, error) {
	enc, err := v.cred.Get(credService, kekUser)
	var key []byte
	switch {
	case err == nil:
		key, err = base64.StdEncoding.DecodeString(enc)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("providers: stored secret key is malformed")
		}
	case errors.Is(err, ErrCredNotFound):
		if !create {
			return nil, fmt.Errorf("providers: the secret key is missing from the credential store; large secrets cannot be decrypted")
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := v.cred.Set(credService, kekUser, base64.StdEncoding.EncodeToString(key)); err != nil {
			return nil, fmt.Errorf("credential store: %w", err)
		}
	default:
		return nil, fmt.Errorf("credential store: %w", err)
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

// MemoryCredentialStore is a stand-in for the OS credential store, TEST ONLY: values vanish at exit. It enforces
// the Windows blob limit so tests exercise the over-limit fallback path in Vault; it proves nothing about the real
// Credential Manager (that is credstore_windows_test.go, which runs only on Windows).
type MemoryCredentialStore struct {
	mu    sync.Mutex
	limit int
	m     map[string]string
}

// NewMemoryCredentialStoreForTests returns a MemoryCredentialStore with the Windows 2560-byte limit.
func NewMemoryCredentialStoreForTests() *MemoryCredentialStore {
	return &MemoryCredentialStore{limit: MaxCredentialBytes, m: map[string]string{}}
}

func (s *MemoryCredentialStore) Set(service, user, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(secret) > s.limit {
		return ErrCredTooBig
	}
	s.m[service+"\x00"+user] = secret
	return nil
}

func (s *MemoryCredentialStore) Get(service, user string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[service+"\x00"+user]
	if !ok {
		return "", ErrCredNotFound
	}
	return v, nil
}

func (s *MemoryCredentialStore) Delete(service, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := service + "\x00" + user
	if _, ok := s.m[k]; !ok {
		return ErrCredNotFound
	}
	delete(s.m, k)
	return nil
}

// Keys lists the stored (service, user) pairs, for tests that assert what reached the credential store.
func (s *MemoryCredentialStore) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.m {
		out = append(out, k)
	}
	return out
}

// MemorySecretStore is an in-memory SecretStore, TEST ONLY: never used by the product, secrets are lost at exit
// and nothing is encrypted.
type MemorySecretStore struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemorySecretStoreForTests returns an empty MemorySecretStore.
func NewMemorySecretStoreForTests() *MemorySecretStore {
	return &MemorySecretStore{m: map[string]string{}}
}

func (s *MemorySecretStore) Put(_ context.Context, ref, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[ref] = value
	return nil
}

func (s *MemorySecretStore) Get(_ context.Context, ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[ref]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrSecretNotFound, ref)
	}
	return v, nil
}

func (s *MemorySecretStore) Delete(_ context.Context, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, ref)
	return nil
}
