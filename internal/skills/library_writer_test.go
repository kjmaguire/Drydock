package skills

import (
	"context"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// writerProbe is the real scanner with one addition: each scan first makes a write of its own on the app's one writer
// connection, and records it if the write could not get the connection within two seconds (a scan inside a write transaction).
type writerProbe struct {
	security.SecretScanner
	db    *storage.DB
	scans int
	err   error
}

func (p *writerProbe) probe() {
	p.scans++
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := p.db.Writer().ExecContext(ctx, `UPDATE projects SET name=name WHERE id=''`); err != nil && p.err == nil {
		p.err = err
	}
}

func (p *writerProbe) Scan(ctx context.Context, text string) ([]security.Finding, error) {
	p.probe()
	return p.SecretScanner.Scan(ctx, text)
}

func (p *writerProbe) Redact(text string) string {
	p.probe()
	return p.SecretScanner.Redact(text)
}

// A registry's skills are scanned before the library is written: the scan of a whole registry inside the write transaction
// held the app's one writer connection, and every other write (a message sent, an event published) waited for it.
func TestLibrarySyncScansBeforeHoldingTheWriter(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	p := &writerProbe{SecretScanner: e.s.Scanner, db: e.db}
	e.s.Scanner = p
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(nil))
	e.useFixture(gh)
	if _, err := e.s.CheckRegistry(ctx, "cloudflare-skills"); err != nil {
		t.Fatal(err)
	}
	if p.scans == 0 {
		t.Fatal("the registry's skills were not scanned")
	}
	if p.err != nil {
		t.Fatalf("a write waited for the library sync's transaction while it scanned (%d scans): %v", p.scans, p.err)
	}
	var n int
	if err := e.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_library WHERE registry_id='cloudflare-skills'`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("the library holds %d skills: %v", n, err)
	}
}
