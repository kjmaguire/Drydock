package providers

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/storage"
)

func newVault(t *testing.T) (*Vault, *MemoryCredentialStore, *storage.DB) {
	t.Helper()
	db, err := storage.OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// The in-memory credential store stands in for Credential Manager ONLY so the over-limit fallback logic can run
	// here; the real wincred path is credstore_windows_test.go.
	cred := NewMemoryCredentialStoreForTests()
	return NewVault(cred, db), cred, db
}

func TestVaultSmallSecretStaysInCredentialStore(t *testing.T) {
	v, cred, db := newVault(t)
	ctx := context.Background()
	val := strings.Repeat("k", MaxCredentialBytes) // exactly at the limit
	if err := v.Put(ctx, "provider/a/api-key", val); err != nil {
		t.Fatal(err)
	}
	got, err := v.Get(ctx, "provider/a/api-key")
	if err != nil || got != val {
		t.Fatalf("get: %v", err)
	}
	var n int
	db.Reader().QueryRow(`SELECT count(*) FROM secret_blobs`).Scan(&n)
	if n != 0 {
		t.Fatalf("a secret at the limit must not be written to SQLite, got %d blobs", n)
	}
	if len(cred.Keys()) != 1 {
		t.Fatalf("credential store keys %v", cred.Keys())
	}
}

func TestVaultLargeSecretFallsBackToEncryptedSQLite(t *testing.T) {
	v, cred, db := newVault(t)
	ctx := context.Background()
	plain := strings.Repeat("TOP-SECRET-", 400) // 4400 bytes, over 2560
	if len(plain) <= MaxCredentialBytes {
		t.Fatal("test value too small")
	}
	// The stand-in store refuses it exactly as go-keyring does on Windows.
	if err := cred.Set("x", "y", plain); !errors.Is(err, ErrCredTooBig) {
		t.Fatalf("stand-in must reject over-limit values: %v", err)
	}
	if err := v.Put(ctx, "oauth/bundle", plain); err != nil {
		t.Fatal(err)
	}
	got, err := v.Get(ctx, "oauth/bundle")
	if err != nil || got != plain {
		t.Fatalf("round trip failed: %v", err)
	}
	// Only the key-encryption key sits in the credential store; the value is not there.
	keys := cred.Keys()
	if len(keys) != 1 || !strings.Contains(keys[0], kekUser) {
		t.Fatalf("credential store should hold only the encryption key, has %v", keys)
	}
	// The SQLite row is ciphertext: no plaintext fragment on disk.
	var ct, nonce []byte
	if err := db.Reader().QueryRow(`SELECT ciphertext, nonce FROM secret_blobs WHERE ref='oauth/bundle'`).Scan(&ct, &nonce); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("TOP-SECRET")) || len(nonce) != 12 {
		t.Fatal("blob is not encrypted")
	}
	db.Writer().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	for _, f := range []string{db.Path(), db.Path() + "-wal"} {
		if b, err := os.ReadFile(f); err == nil && bytes.Contains(b, []byte("TOP-SECRET")) {
			t.Fatalf("plaintext found in %s", f)
		}
	}
	// Replacing a large value with a small one moves it back and drops the blob.
	if err := v.Put(ctx, "oauth/bundle", "small"); err != nil {
		t.Fatal(err)
	}
	if g, _ := v.Get(ctx, "oauth/bundle"); g != "small" {
		t.Fatal("small replacement not read back")
	}
	var n int
	db.Reader().QueryRow(`SELECT count(*) FROM secret_blobs`).Scan(&n)
	if n != 0 {
		t.Fatal("stale blob left after shrinking the value")
	}
	// And the other direction, then delete clears both locations.
	if err := v.Put(ctx, "oauth/bundle", plain); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete(ctx, "oauth/bundle"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get(ctx, "oauth/bundle"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestVaultBoundary(t *testing.T) {
	v, _, db := newVault(t)
	ctx := context.Background()
	for _, size := range []int{1, MaxCredentialBytes, MaxCredentialBytes + 1} {
		ref := "r" + strings.Repeat("x", size%7)
		val := strings.Repeat("v", size)
		if err := v.Put(ctx, ref, val); err != nil {
			t.Fatal(err)
		}
		got, err := v.Get(ctx, ref)
		if err != nil || got != val {
			t.Fatalf("size %d: %v", size, err)
		}
		var n int
		db.Reader().QueryRow(`SELECT count(*) FROM secret_blobs WHERE ref=?`, ref).Scan(&n)
		if (size > MaxCredentialBytes) != (n == 1) {
			t.Fatalf("size %d: blob rows %d", size, n)
		}
	}
}

func TestVaultCiphertextIsBoundToItsReferenceAndKey(t *testing.T) {
	v, cred, db := newVault(t)
	ctx := context.Background()
	plain := strings.Repeat("s", 3000)
	if err := v.Put(ctx, "ref/one", plain); err != nil {
		t.Fatal(err)
	}
	// Copy the blob to another reference: the reference is AEAD additional data, so decryption must fail.
	db.Writer().Exec(`INSERT INTO secret_blobs(ref,nonce,ciphertext,created_at) SELECT 'ref/two',nonce,ciphertext,created_at FROM secret_blobs WHERE ref='ref/one'`)
	if _, err := v.Get(ctx, "ref/two"); err == nil {
		t.Fatal("a blob moved to another reference decrypted")
	}
	// Lose the key: large secrets are unreadable and say so, never a plaintext fallback.
	cred.Delete(credService, kekUser)
	if _, err := v.Get(ctx, "ref/one"); err == nil || !strings.Contains(err.Error(), "key is missing") {
		t.Fatalf("missing key: %v", err)
	}
}

func TestVaultCredentialStoreFailureIsAnError(t *testing.T) {
	db, _ := storage.OpenTemp(context.Background(), t.TempDir())
	defer db.Close()
	v := NewVault(brokenCred{}, db)
	if err := v.Put(context.Background(), "a", "b"); err == nil {
		t.Fatal("Put must fail when the credential store is unavailable (no plaintext fallback)")
	}
	if err := v.Put(context.Background(), "a", strings.Repeat("b", 4000)); err == nil {
		t.Fatal("large Put must fail when the credential store (key holder) is unavailable")
	}
	var n int
	db.Reader().QueryRow(`SELECT count(*) FROM secret_blobs`).Scan(&n)
	if n != 0 {
		t.Fatal("something was written to SQLite without a key")
	}
}

// brokenCred is a credential store that always fails, like a locked or missing OS store.
type brokenCred struct{}

func (brokenCred) Set(string, string, string) error      { return errors.New("store locked") }
func (brokenCred) Get(string, string) (string, error)    { return "", errors.New("store locked") }
func (brokenCred) Delete(string, string) error           { return errors.New("store locked") }
