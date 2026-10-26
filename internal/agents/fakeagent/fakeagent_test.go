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

// A turn the stand-in fails ends the way Claude Code 2.1.310 reports a failed API call, so the real parser sees what it would
// see from the CLI: the API-error assistant message (is_api_error_message on the event), then a result that says is_error with
// the last message's stop reason and the terminal reason api_error. ModelErrors fails the turn before any step (nothing said:
// "tool_use", as the CLI's result reads); a Fail step fails it after an answer ("end_turn").
func TestAFailedTurnHasTheShapeOfAFailedAPICall(t *testing.T) {
	const apiErr = "API Error: 529 overloaded"
	type line struct {
		Type           string `json:"type"`
		Subtype        string `json:"subtype"`
		IsError        bool   `json:"is_error"`
		Result         string `json:"result"`
		StopReason     string `json:"stop_reason"`
		TerminalReason string `json:"terminal_reason"`
		APIErrorFlag   bool   `json:"is_api_error_message"`
		Message        struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	run := func(script Script, flags ...string) []line {
		t.Helper()
		sc, _ := json.Marshal(script)
		sp := filepath.Join(t.TempDir(), "script.json")
		if err := os.WriteFile(sp, sc, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
		var out bytes.Buffer
		Run(append([]string{"-p", "go"}, flags...), strings.NewReader(""), &out)
		var lines []line
		for _, raw := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			var l line
			if err := json.Unmarshal([]byte(raw), &l); err != nil {
				t.Fatalf("%v: %s", err, raw)
			}
			lines = append(lines, l)
		}
		return lines
	}
	check := func(name string, lines []line, stop string, saidBefore int) {
		t.Helper()
		if len(lines) != 3+saidBefore {
			t.Fatalf("%s: %d lines, want init, %d said, the API-error message and the result", name, len(lines), saidBefore)
		}
		msg, res := lines[len(lines)-2], lines[len(lines)-1]
		if msg.Type != "assistant" || !msg.APIErrorFlag || len(msg.Message.Content) != 1 || msg.Message.Content[0].Text != apiErr {
			t.Fatalf("%s: the API-error message %+v", name, msg)
		}
		if res.Type != "result" || res.Subtype != "success" || !res.IsError || res.Result != apiErr || res.StopReason != stop || res.TerminalReason != "api_error" {
			t.Fatalf("%s: the result %+v", name, res)
		}
		for _, l := range lines[:len(lines)-2] {
			if l.APIErrorFlag {
				t.Fatalf("%s: an ordinary message carries the flag: %+v", name, l)
			}
		}
	}
	check("a model that fails its first call", run(Script{Default: []Step{{Say: "never said"}}, ModelErrors: map[string]string{"m1": apiErr}}, "--model", "m1"), "tool_use", 0)
	// a Say streams its words as deltas and then the whole message: "an answer" is two deltas and one assistant line
	check("a call that fails after an answer", run(Script{Default: []Step{{Say: "an answer"}, {Fail: apiErr}, {Say: "never said"}}}), "end_turn", 3)
}
