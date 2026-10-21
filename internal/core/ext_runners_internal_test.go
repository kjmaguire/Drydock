package core

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/runners"
)

// Only a person changes CI runners: an agent's identity (or none) is refused before anything is read or changed.
func TestOnlyAPersonChangesCIRunners(t *testing.T) {
	r := &runnersExt{} // no pool, no database: a refusal must come first
	ctx := context.Background()
	for _, by := range []string{"agent:a1", "human:", ""} {
		if _, err := r.add(ctx, "kjmaguire/shop", runners.Linux, true, by); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("%q: %v", by, err)
		}
		if _, err := r.change(ctx, "kjmaguire/shop", runners.Linux, by, nil); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("%q: %v", by, err)
		}
		if _, err := r.setRoot(ctx, `D:\x`, by); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("%q: %v", by, err)
		}
	}
}
