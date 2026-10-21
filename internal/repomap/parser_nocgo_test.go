//go:build !cgo

package repomap

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Run with CGO_ENABLED=0: the build without the C parser has no repo map and says why.
func TestABuildWithoutCGOHasNoParser(t *testing.T) {
	if Available() || len(Languages()) != 0 {
		t.Fatal("a build without CGO reports a parser")
	}
	m := New(Options{List: func(context.Context, string) ([]string, error) { t.Fatal("nothing is listed"); return nil, nil }})
	_, err := m.Map(context.Background(), Request{Root: t.TempDir()})
	if !errors.Is(err, ErrNoParser) || !strings.Contains(err.Error(), "built without the C parser") {
		t.Fatalf("Map = %v, want ErrNoParser", err)
	}
}
