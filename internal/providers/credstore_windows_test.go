//go:build windows

package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/storage"
)

// TestWindowsCredentialManagerRoundTrip runs only on Windows against the real Credential Manager: a small secret
// stays in the store, a secret over 2560 bytes goes through the encrypted SQLite fallback.
func TestWindowsCredentialManagerRoundTrip(t *testing.T) {
	cred, err := NewCredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v := NewVault(cred, db)
	ctx := context.Background()
	ref := "test/drydock-wincred-roundtrip"
	t.Cleanup(func() { _ = v.Delete(ctx, ref); _ = cred.Delete(credService, kekUser) })
	for _, size := range []int{32, MaxCredentialBytes, MaxCredentialBytes + 1, 6000} {
		val := strings.Repeat("k", size)
		if err := v.Put(ctx, ref, val); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		got, err := v.Get(ctx, ref)
		if err != nil || got != val {
			t.Fatalf("size %d: round trip failed: %v", size, err)
		}
	}
}
