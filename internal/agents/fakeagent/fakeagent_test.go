package fakeagent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The stand-in refuses a Write the session's flags forbid, as Claude Code does, so a session Drydock starts with the wrong
// flags fails a test: a reviewer that is not read-only would otherwise write as freely as a worker.
func TestWritesFollowTheSessionsFlags(t *testing.T) {
	sc, _ := json.Marshal(Script{Default: []Step{{Write: &WriteSpec{Path: "out.txt", Content: "x"}}, {Say: "done"}}})
	sp := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(sp, sc, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	run := func(flags ...string) (written bool, stream string) {
		dir := t.TempDir()
		wd, _ := os.Getwd()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(wd)
		var out bytes.Buffer
		Run(append([]string{"-p", "write it"}, flags...), strings.NewReader(""), &out)
		_, err := os.Stat(filepath.Join(dir, "out.txt"))
		return err == nil, out.String()
	}
	if w, _ := run("--permission-mode", "acceptEdits"); !w {
		t.Fatal("a worker session did not write")
	}
	for _, flags := range [][]string{{"--disallowedTools", "Edit,Write,MultiEdit"}, {"--permission-mode", "plan"}} {
		w, stream := run(flags...)
		if w || !strings.Contains(stream, `"is_error":true`) {
			t.Fatalf("%v: written=%v\n%s", flags, w, stream)
		}
	}
}
