package claude

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/workflow"
)

// Fixtures are real `claude -p --output-format stream-json --verbose` output captured 2026-10-08 (Claude Code 2.1.294),
// with the large init payload, signatures and identifiers removed.
func parseFile(t *testing.T, path string) ([]workflow.Chunk, *Parser) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p := NewParser()
	var all []workflow.Chunk
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		cs, _ := p.Parse(sc.Bytes())
		all = append(all, cs...)
	}
	return all, p
}

func kinds(cs []workflow.Chunk) []string {
	var k []string
	for _, c := range cs {
		k = append(k, c.Kind)
	}
	return k
}

func TestParsePong(t *testing.T) {
	cs, p := parseFile(t, "testdata/pong.jsonl")
	got := kinds(cs)
	want := []string{"rate_limit", "session", "message_end", "usage", "done"}
	if len(got) != len(want) {
		t.Fatalf("kinds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds %v, want %v", got, want)
		}
	}
	if p.SessionID() != "SESSION" {
		t.Fatalf("session %q", p.SessionID())
	}
	var txt workflow.TextDelta
	cs[2].Decode(&txt)
	if txt.Text != "pong" {
		t.Fatalf("text %q", txt.Text)
	}
	var u workflow.UsageChunk
	cs[3].Decode(&u)
	if u.Source != "reported" || u.Model != "claude-haiku-5-5" || u.CostUSD <= 0 || u.OutputTokens == 0 || u.ContextSize == 0 {
		t.Fatalf("usage %+v", u)
	}
	var d workflow.DoneChunk
	cs[4].Decode(&d)
	if d.StopReason != "end_turn" {
		t.Fatalf("stop %q", d.StopReason)
	}
}

func TestParseToolUseAndResultCarryNameAndArgs(t *testing.T) {
	cs, _ := parseFile(t, "testdata/read_tool.jsonl")
	var call workflow.ToolCall
	var res workflow.ToolResult
	var calls, results int
	for _, c := range cs {
		switch c.Kind {
		case workflow.ChunkToolCall:
			calls++
			c.Decode(&call)
		case workflow.ChunkToolResult:
			results++
			c.Decode(&res)
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("calls=%d results=%d (%v)", calls, results, kinds(cs))
	}
	if call.Name != "Read" || res.Name != "Read" || res.ID != call.ID || string(res.Args) != string(call.Args) {
		t.Fatalf("call %+v result %+v", call, res)
	}
	if res.Output == "" || res.IsError {
		t.Fatalf("result %+v", res)
	}
	last := cs[len(cs)-1]
	if last.Kind != workflow.ChunkDone {
		t.Fatalf("stream must end with done, got %s", last.Kind)
	}
}

func TestInterruptedResultIsReportedAsInterrupted(t *testing.T) {
	p := NewParser()
	p.MarkInterrupted()
	cs, _ := p.Parse([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"S","total_cost_usd":0.001,"usage":{"input_tokens":5,"output_tokens":2}}`))
	k := kinds(cs)
	if len(k) != 2 || k[0] != "usage" || k[1] != "done" {
		t.Fatalf("an interrupt is not an error: %v", k)
	}
	var d workflow.DoneChunk
	cs[1].Decode(&d)
	if d.StopReason != "interrupted" {
		t.Fatalf("stop %q", d.StopReason)
	}
	// without a prior interrupt the same result is an error
	cs, _ = NewParser().Parse([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom"}`))
	if k := kinds(cs); len(k) != 3 || k[1] != "error" {
		t.Fatalf("want usage,error,done got %v", k)
	}
}

func TestControlAck(t *testing.T) {
	_, ack := NewParser().Parse([]byte(`{"type":"control_response","response":{"subtype":"success","request_id":"r1","response":{"still_queued":[]}}}`))
	if ack == nil || ack.RequestID != "r1" || !ack.Success {
		t.Fatalf("ack %+v", ack)
	}
}

func TestUnknownAndMalformedLinesAreTolerated(t *testing.T) {
	p := NewParser()
	for _, l := range []string{``, `not json`, `{"type":"brand_new_event","x":1}`, `{"type":"assistant","message":"oops"}`, `[1,2]`} {
		if cs, ack := p.Parse([]byte(l)); len(cs) != 0 || ack != nil {
			t.Fatalf("line %q produced output", l)
		}
	}
}

func TestStreamEventTextDeltas(t *testing.T) {
	cs, _ := NewParser().Parse([]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hel"}}}`))
	if len(cs) != 1 || cs[0].Kind != workflow.ChunkText {
		t.Fatalf("%v", kinds(cs))
	}
}

func TestVersionParsing(t *testing.T) {
	v, ok := ParseVersion("2.1.294 (Claude Code)")
	if !ok || v != [3]int{2, 1, 294} {
		t.Fatal(v)
	}
	if !Less([3]int{2, 1, 204}, MinVersion) || Less([3]int{2, 1, 205}, MinVersion) {
		t.Fatal("min version comparison")
	}
}

// permission_denied.jsonl is real output of claude 2.1.295 (haiku, --permission-mode default) asked to run `touch` through Bash,
// captured 2026-10-09 with paths and the session id replaced: headless claude refuses the call without asking and lists it in
// the result's permission_denials.
func TestPermissionDenialsAreReportedBeforeUsageAndDone(t *testing.T) {
	cs, p := parseFile(t, "testdata/permission_denied.jsonl")
	if p.SessionID() != "SESSION" {
		t.Fatalf("session %q", p.SessionID())
	}
	var den []workflow.PermissionDenial
	at := -1
	for i, c := range cs {
		if c.Kind == workflow.ChunkPermissionDenied {
			var d workflow.PermissionDenial
			if err := c.Decode(&d); err != nil {
				t.Fatal(err)
			}
			den = append(den, d)
			at = i
		}
	}
	if len(den) != 1 || den[0].ToolName != "Bash" || den[0].ToolUseID == "" || !strings.Contains(string(den[0].Input), `"touch probe_file.txt"`) {
		t.Fatalf("denials %+v", den)
	}
	k := kinds(cs)
	if at < 0 || at+2 >= len(k) || k[at+1] != workflow.ChunkUsage || k[len(k)-1] != workflow.ChunkDone {
		t.Fatalf("order %v", k)
	}
	for _, c := range cs {
		if c.Kind == workflow.ChunkError {
			t.Fatalf("a denial is not an error of the turn: %s", c.Data)
		}
	}
}

// resume_missing.jsonl is the only line claude 2.1.295 prints for --resume of a session id it does not have (it then exits 1):
// an error result with the reason in "errors" and no init event.
func TestMissingResumeSessionReportsTheCLIReason(t *testing.T) {
	cs, p := parseFile(t, "testdata/resume_missing.jsonl")
	if p.SessionID() != "" {
		t.Fatalf("no init was printed, session must stay unknown: %q", p.SessionID())
	}
	var msg string
	for _, c := range cs {
		if c.Kind == workflow.ChunkError {
			var e workflow.ErrorChunk
			_ = c.Decode(&e)
			msg = e.Message
		}
	}
	if !strings.Contains(msg, "No conversation found with session ID: MISSING-SESSION") {
		t.Fatalf("error %q (kinds %v)", msg, kinds(cs))
	}
}

// A turn served by two models (claude 2.1.296 answering a plan-mode `--model haiku` session with Sonnet; the shape is that
// run's result line with the numbers kept) is labeled with the model that cost the most, whatever Go's map order is.
func TestAResultWithTwoModelsIsLabeledWithTheCostliest(t *testing.T) {
	line := `{"type":"result","subtype":"success","is_error":false,"result":"OK","stop_reason":"end_turn","total_cost_usd":0.1639,` +
		`"modelUsage":{"claude-haiku-5-5":{"inputTokens":9,"outputTokens":0,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.00001,"contextWindow":200000},` +
		`"claude-sonnet-5-5":{"inputTokens":3,"outputTokens":5,"cacheReadInputTokens":0,"cacheCreationInputTokens":43000,"costUSD":0.1639,"contextWindow":1000000}}}`
	for i := 0; i < 20; i++ { // map iteration order varies from run to run
		cs, _ := NewParser().Parse([]byte(line))
		var u workflow.UsageChunk
		for _, c := range cs {
			if c.Kind == workflow.ChunkUsage {
				if err := c.Decode(&u); err != nil {
					t.Fatal(err)
				}
			}
		}
		if u.Model != "claude-sonnet-5-5" || u.InputTokens != 12 || u.OutputTokens != 5 || u.CacheWriteTokens != 43000 {
			t.Fatalf("usage %+v", u)
		}
	}
}
