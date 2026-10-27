package runtime

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The agent.output payload of a tool result fits an event: a result within the cap is published as it is, one whose output is
// too long loses the middle of the output, one whose arguments are the bulk loses the middle of the arguments, and every cut
// lands on a character boundary.
func TestAToolResultIsCutToFitAnEvent(t *testing.T) {
	fits := func(t *testing.T, ev map[string]any) workflow.ToolResult {
		t.Helper()
		b, err := json.Marshal(ev)
		if err != nil || len(b) > events.MaxEventData {
			t.Fatalf("the payload is %d bytes (%v)", len(b), err)
		}
		if ev["kind"] != workflow.ChunkToolResult {
			t.Fatalf("kind %v", ev["kind"])
		}
		var tr workflow.ToolResult
		if err := json.Unmarshal(ev["data"].(json.RawMessage), &tr); err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(tr.Output) || !json.Valid(tr.Args) {
			t.Fatalf("the cut broke a character or the arguments: %.80q %.80s", tr.Output, tr.Args)
		}
		return tr
	}

	small := workflow.NewChunk(workflow.ChunkToolResult, workflow.ToolResult{ID: "t1", Name: "Read", Output: "package a"})
	if ev := outputEvent(small); string(ev["data"].(json.RawMessage)) != string(small.Data) {
		t.Fatalf("a small result must be published as it is: %s", ev["data"])
	}

	// multibyte text over the cap: the start and the end stay, the middle says how much is gone, no character is split
	long := strings.Repeat("héllo wörld ", 30<<10)
	tr := fits(t, outputEvent(workflow.NewChunk(workflow.ChunkToolResult, workflow.ToolResult{ID: "t1", Name: "Read", Args: json.RawMessage(`{"path":"a.txt"}`), Output: long})))
	if !strings.HasPrefix(tr.Output, "héllo wörld ") || !strings.HasSuffix(tr.Output, "héllo wörld ") || !strings.Contains(tr.Output, "bytes left out of the middle of this result by Drydock") || string(tr.Args) != `{"path":"a.txt"}` {
		t.Fatalf("output %.60q ... %.60q args %s", tr.Output, tr.Output[len(tr.Output)-60:], tr.Args)
	}
	if len(tr.Output) > events.MaxEventData || len(tr.Output) < events.MaxEventData/2 {
		t.Fatalf("the cut kept %d bytes", len(tr.Output))
	}

	// the arguments are the bulk (a Write of a long file): they are cut the same way and become a string, the output stays
	args, _ := json.Marshal(map[string]any{"path": "big.txt", "content": strings.Repeat("0123456789", 30<<10)})
	tr = fits(t, outputEvent(workflow.NewChunk(workflow.ChunkToolResult, workflow.ToolResult{ID: "t2", Name: "Write", Args: args, Output: "ok"})))
	var cut string
	if json.Unmarshal(tr.Args, &cut) != nil || !strings.HasPrefix(cut, `{"content":"0123456789`) || !strings.Contains(cut, "left out of the middle of this result's arguments by Drydock") || tr.Output != "ok" {
		t.Fatalf("args %.80s output %q", tr.Args, tr.Output)
	}
}

func TestCutMiddleKeepsThreeQuartersThenAQuarter(t *testing.T) {
	s := strings.Repeat("a", 100) + strings.Repeat("b", 100)
	got := cutMiddle(s, 40, "result")
	if !strings.HasPrefix(got, strings.Repeat("a", 30)+"\n[... 160 bytes left out of the middle of this result by Drydock ...]\n") || !strings.HasSuffix(got, strings.Repeat("b", 10)) || strings.Count(got, "a") != 30 {
		t.Fatalf("%q", got)
	}
	if cutMiddle("short", 10, "result") != "short" {
		t.Fatal("a string that fits is not cut")
	}
	if got := cutMiddle("ééé", 1, "result"); !utf8.ValidString(got) || strings.Count(got, "é") > 1 {
		t.Fatalf("%q", got)
	}
}
