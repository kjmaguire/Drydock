package trial

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
)

// The naive broadcast arm (B0) with the repository's scripted stand-in for Claude Code (fakeagent, a TEST DOUBLE) as every agent
// of the team. The stand-in logs each prompt it is given, so the tests read exactly what was broadcast; it reports $0.0002 and
// 10 input tokens per turn, which is what the record must add up. Real runs need a signed-in Claude Code (docs/evals.md 5).

// prompts reads the stand-in's prompt log.
func prompts(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var p string
		if err := json.Unmarshal(sc.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func b0Rig(t *testing.T, script fakeagent.Script) (B0, Task, string, string, string) {
	t.Helper()
	root := repoRoot(t)
	x := task(t, "saasapi-invoice-rules")
	repo := t.TempDir()
	if _, err := Prepare(context.Background(), root, x, repo); err != nil {
		t.Fatal(err)
	}
	logs := t.TempDir()
	promptLog, argsLog := filepath.Join(logs, "prompts.log"), filepath.Join(logs, "args.log")
	env := append(fakeScript(t, script), "DRYDOCK_FAKE_PROMPT_LOG="+promptLog, "DRYDOCK_FAKE_ARGS_LOG="+argsLog)
	b := B0{Bin: os.Args[0], Model: "haiku", CeilingUSD: 2, Wall: time.Minute, Root: root, Hidden: []string{"evals/testdata", "zz_hidden_"}, ExtraEnv: env}
	return b, x, repo, promptLog, argsLog
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestB0BroadcastsEveryMessageAndToolOutputToEveryAgent(t *testing.T) {
	x := task(t, "saasapi-invoice-rules")
	b, x, repo, promptLog, argsLog := b0Rig(t, fakeagent.Script{Rules: []fakeagent.Rule{
		{Match: "You are the worker", Steps: []fakeagent.Step{
			{Tool: "Bash", Input: json.RawMessage(`{"command":"go test ./internal/billing/"}`), Result: "TOOL-OUTPUT-7Q: --- FAIL: TestRounding (0.00s)"},
			{Write: &fakeagent.WriteSpec{Path: "internal/billing/invoice.go", Content: solution(t, x.ID, "internal/billing/invoice.go")}},
			{Say: "Fixed the rounding rules.\nDONE"},
		}},
		{Match: "You are the reviewer", Steps: []fakeagent.Step{{Say: "The change matches the task.\nDONE"}}},
	}})
	var transcript bytes.Buffer
	rec, room := b.Run(context.Background(), x, repo, &transcript)
	if rec.Outcome != "" || rec.Reason != "" {
		t.Fatalf("the team did not finish: %s %s", rec.Outcome, rec.Reason)
	}
	if rec.Arm != ArmB0 || rec.Stop != "consensus" || rec.Rounds != 1 || rec.Agents != 2 || rec.Sessions != 2 || !near(rec.CostUSD, 0.0004) ||
		rec.Tokens.Input != 20 || rec.Tokens.CacheRead != 200 || rec.ToolUses["Bash"] != 1 || rec.ToolUses["Write"] != 1 ||
		strings.Join(rec.ModelsUsed, ",") != "fake-1" || rec.CallsWithoutCost != 0 || rec.SawHiddenFiles {
		t.Fatalf("record %+v", rec)
	}
	ps := prompts(t, promptLog)
	if len(ps) != 2 {
		t.Fatalf("one prompt per turn: %d", len(ps))
	}
	worker, reviewer := ps[0], ps[1]
	if !strings.HasPrefix(worker, "You are the worker on a team of 2 agents (worker, reviewer)") || !strings.Contains(worker, "(nothing yet: you are first)") ||
		!strings.Contains(worker, "# Task: "+x.Title) || !strings.Contains(worker, x.Prompt) {
		t.Fatalf("the worker's prompt:\n%s", worker)
	}
	// the reviewer is given everything, in full: what the worker said, the command it ran, the command's output and the file it wrote
	for _, want := range []string{"You are the reviewer", "[round 1, worker ran Bash]", `"command":"go test ./internal/billing/"`,
		"[round 1, output of worker's Bash]\nTOOL-OUTPUT-7Q: --- FAIL: TestRounding (0.00s)", "[round 1, worker ran Write]",
		"[round 1, worker said]\nFixed the rounding rules.\nDONE", x.Prompt} {
		if !strings.Contains(reviewer, want) {
			t.Errorf("the reviewer's prompt lacks %q:\n%s", want, reviewer)
		}
	}
	if rec.BroadcastBytes != int64(len(worker)+len(reviewer)) || rec.MaxPromptBytes != int64(len(reviewer)) {
		t.Fatalf("broadcast %d (max %d), prompts %d and %d", rec.BroadcastBytes, rec.MaxPromptBytes, len(worker), len(reviewer))
	}
	if len(room) != 6 || room[5].Agent != "reviewer" || room[5].Kind != "said" {
		t.Fatalf("room %+v", room)
	}
	// one model for every agent, the remaining ceiling for each turn, the prompt on standard input
	args, _ := os.ReadFile(argsLog)
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"--max-budget-usd","2.0000"`) || !strings.Contains(lines[1], `"--max-budget-usd","1.9998"`) {
		t.Fatalf("argv %s", args)
	}
	for _, l := range lines {
		for _, want := range []string{`"--model","haiku"`, `"--input-format","stream-json"`, `"--allowedTools","Bash"`, `"--no-session-persistence"`} {
			if !strings.Contains(l, want) {
				t.Errorf("argv lacks %s: %s", want, l)
			}
		}
	}
	if strings.Count(transcript.String(), `"type":"result"`) != 2 {
		t.Fatalf("the transcript holds both sessions: %s", transcript.String())
	}
	if res := Accept(context.Background(), b.Root, x, repo); !res.Passed {
		t.Fatalf("the worker wrote the reference solution, the hidden check must pass: %+v", res)
	}
}

func TestB0KeepsTalkingUntilTheRoundLimitAndEveryPromptCarriesTheWholeRoom(t *testing.T) {
	b, x, repo, promptLog, _ := b0Rig(t, fakeagent.Script{Rules: []fakeagent.Rule{
		{Match: "You are the worker", Steps: []fakeagent.Step{{Tool: "Bash", Input: json.RawMessage(`{"command":"ls"}`), Result: "go.mod internal"}, {Say: "Still looking."}}},
		{Match: "You are the reviewer", Steps: []fakeagent.Step{{Say: "Nothing to review yet.\nDONE"}}},
	}})
	b.Rounds = 3
	rec, _ := b.Run(context.Background(), x, repo, &bytes.Buffer{})
	if rec.Outcome != "" || rec.Stop != "rounds" || rec.Rounds != 3 || rec.Sessions != 6 || !near(rec.CostUSD, 0.0012) || len(rec.Notes) != 1 {
		t.Fatalf("record %+v", rec)
	}
	ps := prompts(t, promptLog)
	if len(ps) != 6 {
		t.Fatalf("%d prompts", len(ps))
	}
	// each turn's prompt holds the room of the one before it and more: nothing is summarized or cut
	room := func(p string) string {
		return strings.Replace(p[strings.Index(p, "# The room so far"):], "\n(nothing yet: you are first)\n", "", 1)
	}
	for i := 1; i < len(ps); i++ {
		if !strings.HasPrefix(room(ps[i]), room(ps[i-1])) || len(room(ps[i])) <= len(room(ps[i-1])) {
			t.Fatalf("prompt %d does not carry prompt %d's room in full:\n%s\n---\n%s", i, i-1, ps[i-1], ps[i])
		}
	}
	if strings.Count(ps[5], "[round ") != 11 || !strings.Contains(ps[5], "[round 3, output of worker's Bash]\ngo.mod internal") {
		t.Fatalf("the last prompt:\n%s", ps[5])
	}
	var total int64
	for _, p := range ps {
		total += int64(len(p))
	}
	if rec.BroadcastBytes != total || rec.MaxPromptBytes != int64(len(ps[5])) {
		t.Fatalf("broadcast %d, want %d", rec.BroadcastBytes, total)
	}
}

func TestB0StopsAtTheDollarCeilingAndRecordsIt(t *testing.T) {
	b, x, repo, promptLog, _ := b0Rig(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "thinking"}}})
	b.CeilingUSD = 0.0005 // the third turn takes the team to $0.0006
	rec, _ := b.Run(context.Background(), x, repo, &bytes.Buffer{})
	if rec.Outcome != Ceiling || rec.Stop != "ceiling" || rec.Sessions != 3 || rec.Rounds != 2 || !strings.Contains(rec.Reason, "dollar ceiling of $0.0005 before the reviewer's turn in round 2") {
		t.Fatalf("record %+v", rec)
	}
	if n := len(prompts(t, promptLog)); n != 3 {
		t.Fatalf("no turn starts past the ceiling: %d prompts", n)
	}
}

func TestB0EndsOnAFailedSessionOrTheWallClock(t *testing.T) {
	t.Run("a session that fails", func(t *testing.T) {
		b, x, repo, _, _ := b0Rig(t, fakeagent.Script{ModelErrors: map[string]string{"haiku": "API Error: 529 overloaded"}})
		rec, _ := b.Run(context.Background(), x, repo, &bytes.Buffer{})
		if rec.Outcome != Failed || rec.Stop != "error" || rec.Sessions != 1 || !strings.Contains(rec.Reason, "the worker's session reported an error") {
			t.Fatalf("record %+v", rec)
		}
	})
	t.Run("the wall clock", func(t *testing.T) {
		b, x, repo, _, _ := b0Rig(t, fakeagent.Script{Default: []fakeagent.Step{{SleepMS: 20000}, {Say: "late"}}})
		b.Wall = time.Second
		began := time.Now()
		rec, _ := b.Run(context.Background(), x, repo, &bytes.Buffer{})
		if rec.Outcome != Failed || rec.Stop != "wall" || !strings.Contains(rec.Reason, "wall-clock cap of 1s") || time.Since(began) > 15*time.Second {
			t.Fatalf("record %+v after %s", rec, time.Since(began))
		}
	})
}

func TestB0DoneIsTheLastLineAlone(t *testing.T) {
	for text, want := range map[string]bool{"Fixed.\nDONE": true, "DONE\n": true, "  done\n DONE  ": true, "DONE.": false, "I am DONE": false, "": false, "DONE\nbut one more thing": false} {
		if saidDone(text) != want {
			t.Errorf("%q: %v", text, !want)
		}
	}
	if d := (B0{Model: "haiku"}).Detail(); !strings.Contains(d, "2 Claude Code agents (worker, reviewer)") || !strings.Contains(d, "5 rounds") {
		t.Fatal(d)
	}
}

// A B0 run's record survives run.json and the report shows what only the broadcast team has.
func TestB0RecordsAreWrittenAndReported(t *testing.T) {
	out := t.TempDir()
	r := Record{Schema: 1, Trial: "b0-test", Arm: ArmB0, TaskID: "saasapi-invoice-rules", Repeat: 1, Outcome: Ceiling, Reason: "the team reached the dollar ceiling",
		StartedAt: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC), Agents: 2, Rounds: 3, Stop: "ceiling", Sessions: 5, BroadcastBytes: 300 << 10, MaxPromptBytes: 90 << 10, CostUSD: 2.01}
	if _, err := r.Write(out, map[string][]byte{"transcript.jsonl": []byte("{}\n")}); err != nil {
		t.Fatal(err)
	}
	recs, err := LoadRecords(out)
	if err != nil || len(recs) != 1 || recs[0].Stop != "ceiling" || recs[0].BroadcastBytes != 300<<10 || recs[0].Agents != 2 {
		t.Fatalf("%+v %v", recs, err)
	}
	md := Report(recs)
	for _, want := range []string{"| B0 | 1 | 0 |", "The broadcast team (B0), every run:", "| saasapi-invoice-rules | 1 | 2 | 3 | ceiling | 5 | 300.0 KiB | 90.0 KiB |"} {
		if !strings.Contains(md, want) {
			t.Errorf("the report lacks %q:\n%s", want, md)
		}
	}
}
