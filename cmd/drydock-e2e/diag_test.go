package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

// The diagnostics a failed CI run keeps: every goroutine's stack, and the event log with each event's data cut short and
// scrubbed of secrets.
func TestWriteDiagnosticsKeepsStacksAndAScrubbedEventLog(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "d"), User: "e2e"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	secret := "ghp_" + strings.Repeat("A1b2C3d4E5", 4)[:36]
	data, _ := json.Marshal(map[string]string{"note": "token " + secret})
	if _, err := c.Bus.Publish(context.Background(), events.Event{Type: "diag.secret", ProjectID: "p1", Data: data}); err != nil {
		t.Fatal(err)
	}
	long, _ := json.Marshal(map[string]string{"text": strings.Repeat("x", 5000)})
	if _, err := c.Bus.Publish(context.Background(), events.Event{Type: "agent.output", Data: long}); err != nil {
		t.Fatal(err)
	}

	prefix := filepath.Join(dir, "out", "core")
	if err := writeDiagnostics(c, prefix); err != nil {
		t.Fatal(err)
	}
	g, err := os.ReadFile(prefix + ".goroutines.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(g), "goroutine ") || !strings.Contains(string(g), "writeDiagnostics") {
		t.Fatalf("goroutine dump looks wrong:\n%.500s", g)
	}
	ev, err := os.ReadFile(prefix + ".events.txt")
	if err != nil {
		t.Fatal(err)
	}
	s := string(ev)
	if !strings.Contains(s, " diag.secret project=p1 ") {
		t.Fatalf("event missing:\n%s", s)
	}
	if strings.Contains(s, secret) {
		t.Fatalf("a secret reached the diagnostics:\n%s", s)
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, " agent.output ") && len(line) > 400 {
			t.Fatalf("streamed output is not cut short (%d bytes)", len(line))
		}
	}
}
