package headless_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/headless"
	"github.com/kjmaguire/drydock/internal/agents/headless/fakeagy"
	"github.com/kjmaguire/drydock/internal/agents/headless/fakecustom"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// TestMain runs this test binary as one of the LABELLED stand-ins when asked to: agy written from its documentation
// (fakeagy), or a person's prompt-and-exit CLI (fakecustom). Both are real processes; neither is a real CLI.
func TestMain(m *testing.M) {
	switch {
	case os.Getenv("DRYDOCK_FAKE_AGY") == "1":
		os.Exit(fakeagy.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	case os.Getenv("DRYDOCK_FAKE_CUSTOM") == "1":
		os.Exit(fakecustom.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func script(t *testing.T, env string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	p := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(env, p)
}

// turn sends one message and reads to the turn's done chunk.
func turn(t *testing.T, s agents.Session, msg string) []workflow.Chunk {
	t.Helper()
	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	var out []workflow.Chunk
	deadline := time.After(20 * time.Second)
	for {
		select {
		case c, ok := <-s.Output():
			if !ok {
				t.Fatalf("output closed after %v (stderr %q)", kinds(out), s.Stderr())
			}
			out = append(out, c)
			if c.Kind == workflow.ChunkDone {
				return out
			}
		case <-deadline:
			t.Fatalf("no done chunk: %v", kinds(out))
		}
	}
}

func kinds(cs []workflow.Chunk) []string {
	var k []string
	for _, c := range cs {
		k = append(k, c.Kind)
	}
	return k
}

func of(cs []workflow.Chunk, kind string) []workflow.Chunk {
	var out []workflow.Chunk
	for _, c := range cs {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

func stopReason(t *testing.T, cs []workflow.Chunk) string {
	t.Helper()
	var d workflow.DoneChunk
	_ = of(cs, workflow.ChunkDone)[0].Decode(&d)
	return d.StopReason
}

// What print mode refused reaches the person as permission_denied (decision D11), a tool step is a call and its result, the
// environment keeps the CLI's own variables but not another tool's, and an agy failure (exit 3, AGY_ERROR) ends the turn
// with agy's message.
func TestAntigravityDeniedActionsToolStepsAndFailures(t *testing.T) {
	t.Setenv("DRYDOCK_FAKE_AGY", "1")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("DRYDOCK_FAKE_AGY_ARGS", argsFile)
	t.Setenv("GEMINI_API_KEY", "kept-for-agy")
	t.Setenv("OTHER_TOOL_TOKEN", "not-for-agy")
	script(t, "DRYDOCK_FAKE_AGY_SCRIPT", fakeagy.Script{Rules: []fakeagy.Rule{
		{Match: "edit", Steps: []fakeagy.Step{{Tool: &fakeagy.Tool{Name: "read_file", Args: map[string]any{"path": "a.go"}, Output: "package a"}}, {Deny: &fakeagy.Tool{Name: "write_file", Args: map[string]any{"path": "a.go"}}}, {Say: "I could not write a.go."}, {Unknown: "telemetry_ping"}, {Unknown: "telemetry_ping"}}},
		{Match: "crash", Steps: []fakeagy.Step{{Say: "starting "}, {Fail: "the model is overloaded"}}},
	}, Default: []fakeagy.Step{{Say: "OK"}}})
	// the stand-in switch is passed on by name, as a person passes a variable on (Settings): everything else is scrubbed
	pass := func() []string {
		return []string{"DRYDOCK_FAKE_AGY", "DRYDOCK_FAKE_AGY_SCRIPT", "DRYDOCK_FAKE_AGY_ARGS"}
	}
	ad := headless.NewAntigravity(headless.AntigravityConfig{Exe: os.Args[0], ScrubEnv: true, PassEnv: pass, Caps: agents.Capabilities{Headless: true}})
	s, err := ad.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a1", Cwd: t.TempDir(), Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	cs := turn(t, s, "please edit a.go")
	if s.ID() != "agy-conv-0001" {
		t.Errorf("session id %q", s.ID())
	}
	calls, results, denied := of(cs, workflow.ChunkToolCall), of(cs, workflow.ChunkToolResult), of(cs, workflow.ChunkPermissionDenied)
	if len(calls) != 1 || len(results) != 1 || len(denied) != 1 {
		t.Fatalf("chunks %v", kinds(cs))
	}
	var tr workflow.ToolResult
	_ = results[0].Decode(&tr)
	if tr.Name != "read_file" || tr.Output != "package a" || !strings.Contains(string(tr.Args), "a.go") {
		t.Errorf("tool result %+v", tr)
	}
	var d workflow.PermissionDenial
	_ = denied[0].Decode(&d)
	if d.ToolName != "write_file" || !strings.Contains(string(d.Input), "a.go") || !strings.Contains(d.Reason, "D11") {
		t.Errorf("denial %+v", d)
	}
	drift := 0
	for _, c := range of(cs, workflow.ChunkError) {
		var e workflow.ErrorChunk
		_ = c.Decode(&e)
		if e.Code == workflow.CodeAdapterDrift && e.Detail == "telemetry_ping" {
			drift++
		}
	}
	if drift != 1 {
		t.Errorf("an unknown event is a drift warning, once: %d", drift)
	}
	args, _ := os.ReadFile(argsFile)
	if got := strings.Join(strings.Fields(string(args)), " "); got != "-p --input-format stream-json --output-format stream-json --mode default --effort low" {
		t.Errorf("argv %q", got)
	}
	env, _ := os.ReadFile(argsFile + ".env")
	if !strings.Contains(string(env), "GEMINI_API_KEY=kept-for-agy") || strings.Contains(string(env), "not-for-agy") {
		t.Errorf("the scrubbed environment keeps agy's own variables only")
	}
	if err := s.Interrupt(context.Background()); err == nil {
		t.Error("interrupt is not claimed")
	}

	cs = turn(t, s, "crash now")
	var e workflow.ErrorChunk
	for _, c := range of(cs, workflow.ChunkError) {
		if _ = c.Decode(&e); e.Fatal {
			break
		}
	}
	if !e.Fatal || !strings.Contains(e.Message, "the model is overloaded (MODEL_ERROR)") || stopReason(t, cs) != "error" {
		t.Fatalf("failure: %+v %v", e, kinds(cs))
	}
	select {
	case ei := <-s.Wait():
		if ei.Code != 3 {
			t.Errorf("exit %d", ei.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the process did not end")
	}
	if _, err := ad.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a1", Cwd: t.TempDir(), ResumeID: "x"}); err == nil {
		t.Error("resume is not claimed")
	}
}

func customAdapter(t *testing.T, prompt string, d headless.Dialect, args func(map[string]string) []string) *headless.CustomAdapter {
	t.Helper()
	ad, err := headless.NewCustom(headless.CustomConfig{ID: "mycli", Exe: os.Args[0], Args: args, Prompt: prompt, Dialect: d, ExitCodes: map[int]string{0: "success", 1: "failure"}})
	if err != nil {
		t.Fatal(err)
	}
	return ad
}

var standInDialect = headless.Dialect{Format: headless.FormatJSONL, TypeField: "type", Map: map[string]map[string]string{
	"assistant_text": {"type": "message", "text": "$.text"},
	"tool_call":      {"type": "tool", "id": "$.id", "name": "$.tool", "args": "$.input"},
	"tool_result":    {"type": "tool_result", "id": "$.id", "ok": "$.ok", "output": "$.output"},
	"usage":          {"type": "usage", "input": "$.in", "output": "$.out"},
	"result":         {"type": "done", "status": "$.status"},
	"session":        {"type": "session", "id": "$.id", "model": "$.model"},
}}

// argvOf is the manifest's argv as HeadlessArgs builds it: the base, then a group only when its placeholder has a value.
func argvOf(vars map[string]string) []string {
	out := []string{"run", "--json", "--cwd", vars["cwd"]}
	if vars["session_id"] != "" {
		out = append(out, "--session", vars["session_id"])
	}
	if vars["model"] != "" {
		out = append(out, "--model", vars["model"])
	}
	return out
}

// One process per turn: the session id the CLI printed goes to the next turn, a failing exit ends the turn with the reason,
// a line the dialect does not know is a drift warning once, and a turn's process ending does not end the session.
func TestCustomRunnerContinuesTheSessionAndReportsFailures(t *testing.T) {
	t.Setenv("DRYDOCK_FAKE_CUSTOM", "1")
	runs := filepath.Join(t.TempDir(), "runs")
	t.Setenv("DRYDOCK_FAKE_CUSTOM_ARGS", runs)
	script(t, "DRYDOCK_FAKE_CUSTOM_SCRIPT", fakecustom.Script{Rules: []fakecustom.Rule{
		{Match: "fail", Steps: []fakecustom.Step{{Say: "trying "}, {Fail: "no credits left"}}},
		{Match: "odd", Steps: []fakecustom.Step{{Raw: `{"type":"progress","pct":50}`}, {Raw: `not json`}, {Raw: `{"type":"progress","pct":90}`}, {Say: "fine"}}},
	}, Default: []fakecustom.Step{{Say: "OK"}}})
	ad := customAdapter(t, headless.PromptStdin, standInDialect, argvOf)
	cwd := t.TempDir()
	s, err := ad.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a1", Cwd: cwd, Model: "small"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	if s.PID() != 0 {
		t.Error("nothing runs before the first message")
	}
	first := turn(t, s, "hello")
	id := s.ID()
	if !strings.HasPrefix(id, "my-cli-session-") || stopReason(t, first) != "ok" {
		t.Fatalf("session %q, stop %q", id, stopReason(t, first))
	}
	turn(t, s, "again")
	b, _ := os.ReadFile(runs)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "--session") || !strings.Contains(lines[1], "--session "+id) || !strings.Contains(lines[0], "--model small") || !strings.Contains(lines[0], "--cwd "+cwd) {
		t.Fatalf("argv of the turns: %q", lines)
	}

	cs := turn(t, s, "fail please")
	var e workflow.ErrorChunk
	_ = of(cs, workflow.ChunkError)[0].Decode(&e)
	if !strings.Contains(e.Message, "exit code 1") || !strings.Contains(e.Message, "no credits left") || stopReason(t, cs) != "error" {
		t.Fatalf("failed turn %+v %v", e, kinds(cs))
	}
	select {
	case <-s.Wait():
		t.Fatal("a turn's process ending must not end the session")
	case <-time.After(200 * time.Millisecond):
	}
	cs = turn(t, s, "odd output")
	drift := map[string]int{}
	for _, c := range of(cs, workflow.ChunkError) {
		var e workflow.ErrorChunk
		_ = c.Decode(&e)
		drift[e.Detail]++
	}
	if drift["progress"] != 1 || drift["(unreadable line)"] != 1 {
		t.Errorf("drift %v", drift)
	}
	var end workflow.TextDelta
	_ = of(cs, workflow.ChunkMessageEnd)[0].Decode(&end)
	if end.Text != "fine" {
		t.Errorf("answer %q", end.Text)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Wait():
	case <-time.After(5 * time.Second):
		t.Fatal("Stop ends the session")
	}
	if err := s.Send(context.Background(), "after stop"); err == nil {
		t.Error("a stopped session takes no message")
	}
}

// The prompt as an argument, plain text out, a second message while a turn runs refused, and Stop during a turn ends its
// process.
func TestCustomRunnerPromptArgumentPlainTextAndStopDuringATurn(t *testing.T) {
	t.Setenv("DRYDOCK_FAKE_CUSTOM", "1")
	script(t, "DRYDOCK_FAKE_CUSTOM_SCRIPT", fakecustom.Script{Rules: []fakecustom.Rule{
		{Match: "slow", Steps: []fakecustom.Step{{Say: "thinking"}, {SleepMS: 60000}}},
	}, Default: []fakecustom.Step{{Say: "line one"}, {Say: "line two"}}})
	ad := customAdapter(t, headless.PromptArg, headless.Dialect{Format: headless.FormatText}, func(v map[string]string) []string {
		return []string{"run", "--text", "--prompt", v["prompt"]}
	})
	s, err := ad.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a1", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	cs := turn(t, s, "say two lines")
	var end workflow.TextDelta
	_ = of(cs, workflow.ChunkMessageEnd)[0].Decode(&end)
	if end.Text != "line one\nline two\n" || len(of(cs, workflow.ChunkUsage)) != 0 {
		t.Fatalf("plain text: %q %v", end.Text, kinds(cs))
	}
	if err := s.Send(context.Background(), "slow one"); err != nil {
		t.Fatal(err)
	}
	for c := range s.Output() {
		if c.Kind == workflow.ChunkText {
			break
		}
	}
	if err := s.Send(context.Background(), "another"); err == nil {
		t.Error("a second message while a turn runs is refused")
	}
	pid := s.PID()
	start := time.Now()
	if err := s.Stop(context.Background()); err != nil {
		t.Logf("stop: %v", err)
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("Stop waited for the turn instead of ending it")
	}
	for range s.Output() { // closed once the killed turn's reader is done
	}
	if pid <= 0 {
		t.Errorf("the running turn had no process id")
	}
}

func TestDialectValidation(t *testing.T) {
	if p := headless.ValidateDialect(standInDialect); len(p) != 0 {
		t.Fatalf("the stand-in's dialect: %v", p)
	}
	bad := headless.Dialect{Format: headless.FormatJSONL, TypeField: "type", Map: map[string]map[string]string{
		"assistant_text": {"type": "message", "text": "$.text"},
		"usage":          {"type": "message", "input": "$..in"},
	}}
	p := strings.Join(headless.ValidateDialect(bad), "; ")
	for _, want := range []string{"both use type \"message\"", "is not a field path"} {
		if !strings.Contains(p, want) {
			t.Errorf("%q lacks %q", p, want)
		}
	}
	if _, err := headless.NewCustom(headless.CustomConfig{ID: "x", Prompt: "telepathy", Dialect: headless.Dialect{Format: headless.FormatText}}); err == nil {
		t.Error("a prompt route that is neither stdin nor arg")
	}
}
