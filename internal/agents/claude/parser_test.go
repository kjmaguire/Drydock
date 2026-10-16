package claude

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

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
	for _, l := range []string{``, `not json`, `{"type":"assistant","message":"oops"}`, `[1,2]`, `{"type":"tool_progress","x":1}`, `{"type":"autocompact_state"}`} {
		if cs, ack := p.Parse([]byte(l)); len(cs) != 0 || ack != nil {
			t.Fatalf("line %q produced output", l)
		}
	}
}

// An event type the parser does not know is drift: the first one of each type in a session is a warning (the turn goes on),
// and every one is counted.
func TestUnknownEventTypesAreReportedAsDriftOncePerType(t *testing.T) {
	p := NewParser()
	cs, _ := p.Parse([]byte(`{"type":"turn_result","x":1}`))
	if len(cs) != 1 || cs[0].Kind != workflow.ChunkError {
		t.Fatalf("first unknown event: %v", kinds(cs))
	}
	var e workflow.ErrorChunk
	if cs[0].Decode(&e) != nil || e.Fatal || !strings.Contains(e.Message, `"turn_result"`) || !strings.Contains(e.Message, "adapter drift") || e.Code != workflow.CodeAdapterDrift || e.Detail != "turn_result" {
		t.Fatalf("warning %+v", e)
	}
	if cs, _ := p.Parse([]byte(`{"type":"turn_result","x":2}`)); len(cs) != 0 {
		t.Fatalf("the second one warned again: %v", kinds(cs))
	}
	p.Parse([]byte(`{"type":"other_new"}`))
	if got := p.UnknownEvents(); got["turn_result"] != 2 || got["other_new"] != 1 || len(got) != 2 {
		t.Fatalf("counts %v", got)
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

// Claude Code 2.1.296 reports total_cost_usd and modelUsage as running totals of the process and usage as the turn's own
// (contract CLAUDE-USAGE-02): the numbers below are the shape of a real two-turn process (8,854 then 8,924 tokens, the second
// result's modelUsage saying 17,778). Each turn must be recorded once.
func TestEachResultOfAProcessIsRecordedAsItsOwnTurn(t *testing.T) {
	p := NewParser()
	usageOf := func(line string) workflow.UsageChunk {
		t.Helper()
		cs, _ := p.Parse([]byte(line))
		for _, c := range cs {
			if c.Kind == workflow.ChunkUsage {
				var u workflow.UsageChunk
				if err := c.Decode(&u); err != nil {
					t.Fatal(err)
				}
				return u
			}
		}
		t.Fatalf("no usage in %s", line)
		return workflow.UsageChunk{}
	}
	first := usageOf(`{"type":"result","subtype":"success","total_cost_usd":0.0040,"usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":0,"cache_creation_input_tokens":8840},
		"modelUsage":{"claude-haiku-5-5":{"inputTokens":10,"outputTokens":4,"cacheReadInputTokens":0,"cacheCreationInputTokens":8840,"costUSD":0.0040,"contextWindow":200000}}}`)
	second := usageOf(`{"type":"result","subtype":"success","total_cost_usd":0.0050,"usage":{"input_tokens":12,"output_tokens":4,"cache_read_input_tokens":8840,"cache_creation_input_tokens":68},
		"modelUsage":{"claude-haiku-5-5":{"inputTokens":22,"outputTokens":8,"cacheReadInputTokens":8840,"cacheCreationInputTokens":8908,"costUSD":0.0050,"contextWindow":200000}}}`)
	if first.InputTokens+first.OutputTokens+first.CachedTokens+first.CacheWriteTokens != 8854 || first.CostUSD != 0.0040 {
		t.Fatalf("first turn %+v", first)
	}
	if second.InputTokens != 12 || second.OutputTokens != 4 || second.CachedTokens != 8840 || second.CacheWriteTokens != 68 || second.Model != "claude-haiku-5-5" {
		t.Fatalf("the second turn is counted with the first: %+v", second)
	}
	if d := second.CostUSD - 0.0010; d > 1e-12 || d < -1e-12 {
		t.Fatalf("second turn cost %v, want 0.0010", second.CostUSD)
	}
	// a total that went down cannot be a running total: it is taken as the turn's own
	third := usageOf(`{"type":"result","subtype":"success","total_cost_usd":0.0007,"usage":{"input_tokens":3,"output_tokens":2},
		"modelUsage":{"claude-haiku-5-5":{"inputTokens":3,"outputTokens":2,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.0007,"contextWindow":200000}}}`)
	if third.CostUSD != 0.0007 || third.InputTokens != 3 || third.OutputTokens != 2 {
		t.Fatalf("third %+v", third)
	}
	// a new process starts from zero
	if fresh := NewParser(); fresh.turnCost(0.0050) != 0.0050 {
		t.Fatal("a new parser has no earlier turns")
	}
}

// Claude Code 2.1.296 starts a session even when an MCP server fails, and says so only in system/init. A session that was
// given Drydock's gateway must not work on without it.
func TestAGatewayThatFailedToConnectStopsTheTurn(t *testing.T) {
	initWith := func(status string) string {
		return `{"type":"system","subtype":"init","session_id":"s1","model":"claude-haiku-5-5","tools":["Read"],"mcp_servers":[{"name":"drydock","status":"` + status + `"},{"name":"plugin:pdf","status":"connected"}]}`
	}
	fatal := func(p *Parser, line string) *workflow.ErrorChunk {
		cs, _ := p.Parse([]byte(line))
		for _, c := range cs {
			if c.Kind == workflow.ChunkError {
				var e workflow.ErrorChunk
				if err := c.Decode(&e); err != nil {
					t.Fatal(err)
				}
				return &e
			}
		}
		return nil
	}
	p := NewParser()
	p.RequireGateway(GatewayServer)
	if e := fatal(p, initWith("failed")); e == nil || !e.Fatal || !strings.Contains(e.Message, "gateway did not connect") {
		t.Fatalf("a failed gateway: %+v", e)
	}
	p = NewParser()
	p.RequireGateway(GatewayServer)
	if e := fatal(p, initWith("connected")); e != nil {
		t.Fatalf("a connected gateway is not an error: %+v", e)
	}
	if e := fatal(NewParser(), initWith("failed")); e != nil {
		t.Fatalf("a session without the gateway's configuration does not check it: %+v", e)
	}
}

// One tool result of tens of megabytes was kept whole in the run's events, the UI's ring and the window's messages
// (audit 2026-10-16, M22). Its start and its end are kept, and the cut says how much it is.
func TestAHugeToolResultKeepsItsStartAndItsEnd(t *testing.T) {
	small := strings.Repeat("a", 1000)
	if clipOutput(small) != small {
		t.Fatal("a small result changed")
	}
	big := "START" + strings.Repeat("é", 400_000) + "END"
	out := clipOutput(big)
	if len(out) > maxToolOutput+200 || !strings.HasPrefix(out, "START") || !strings.HasSuffix(out, "END") || !strings.Contains(out, "bytes left out of this result") {
		t.Fatalf("%d bytes: %.40q ... %.40q", len(out), out, out[len(out)-40:])
	}
	if !utf8.ValidString(out) {
		t.Fatal("cut inside a character")
	}
}
