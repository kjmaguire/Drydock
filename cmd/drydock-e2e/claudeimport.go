package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeImportSession is the session id of the fixture Claude Code chat the e2e imports (ui/e2e/chat-import.spec.ts).
const ClaudeImportSession = "e2e0c0de-0000-4000-8000-000000000001"

// claudeImportFixture writes a Claude Code config folder holding one session transcript, in the format Claude Code writes
// (one JSON object per line), that ran in folder; and Claude Code's sign-in beside it, which the import must never read.
func claudeImportFixture(config, folder string) error {
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return err
	}
	line := func(v map[string]any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	lines := []string{
		line(map[string]any{"type": "summary", "summary": "Notes from Claude Code", "leafUuid": "e2e-2"}),
		line(map[string]any{"type": "user", "sessionId": ClaudeImportSession, "cwd": folder, "timestamp": "2026-09-01T10:00:00.000Z", "uuid": "e2e-1",
			"message": map[string]any{"role": "user", "content": "What is in my notes folder?"}}),
		line(map[string]any{"type": "assistant", "sessionId": ClaudeImportSession, "cwd": folder, "timestamp": "2026-09-01T10:01:00.000Z", "uuid": "e2e-2",
			"message": map[string]any{"id": "msg_e2e", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "A shopping list and a draft."}}}}),
	}
	dir := filepath.Join(config, "projects", "-cc-notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ClaudeImportSession+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(config, ".credentials.json"), []byte(`{"stand-in":"never read"}`), 0o600)
}
