package main

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/winverify"
)

func TestFlagsReachTheVerifier(t *testing.T) {
	dir := t.TempDir()
	if err := winverify.WriteManifest(dir, &winverify.Manifest{Commit: "abc", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Built: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run(context.Background(), []string{"--list", "--bundle", dir}, &out); code != 0 || !strings.Contains(out.String(), "core-smoke\n") {
		t.Fatalf("--list: %d\n%s", code, out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"--bundle", dir, "--steps", " nonesuch "}, &out); code != 2 || !strings.Contains(out.String(), `"nonesuch"`) {
		t.Fatalf("--steps is split and trimmed: %d\n%s", code, out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"--no-such-flag"}, &out); code != 2 || !strings.Contains(out.String(), "-with-claude") {
		t.Fatalf("an unknown flag prints the usage: %d\n%s", code, out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"with-claude"}, &out); code != 2 || !strings.Contains(out.String(), "unexpected argument") {
		t.Fatalf("a word without dashes is refused, not ignored: %d\n%s", code, out.String())
	}
}
