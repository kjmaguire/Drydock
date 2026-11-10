package core

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/scan"
)

// Once the security extension's close has run, no scan or tool install starts: one started then (the scan a rule starts when
// another ends, an install asked for during the stop) would outlive the core and write to its data folder after Close.
func TestSecurityStartsNothingOnceClosed(t *testing.T) {
	c, err := Open(context.Background(), Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := securityRegistry.Load(c)
	if !ok {
		c.Close()
		t.Fatal("the security extension did not start")
	}
	s := v.(*securityExt)
	c.Close()
	if _, err := s.startAs(context.Background(), scanTarget{ProjectID: "proj_x"}, c.Human(), TriggerWeekly); !errors.Is(err, errClosingScan) {
		t.Fatalf("a scan started after close: %v", err)
	}
	src, _ := scan.SourceOf("osv-scanner")
	if s.startInstall("", src, c.Human()) {
		t.Fatal("an install started after close")
	}
	if s.life.Err() == nil {
		t.Fatal("close did not end the extension's background work")
	}
}
