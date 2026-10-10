package contract_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Contract checks against the real Claude Code CLI. The free ones (flags and values) start claude with an empty stream-json
// input, which parses the arguments and exits without a model turn; they run wherever claude is installed. The paid ones send
// tiny prompts to the cheapest model (haiku) and run only with DRYDOCK_REAL_CLAUDE=1, which cmd/drydock-contract sets; each
// books what the CLI reported spending to the run's ledger and stops once the run's budget is spent.

// CLAUDE-ARGV-01 (free).
func TestClaudeAcceptsTheFlagsDrydockPasses(t *testing.T) {
	exe := installedClaude(t)
	dir := t.TempDir()
	mcp := filepath.Join(dir, "mcp.json")
	writeFile(t, mcp, `{"mcpServers":{}}`)
	prompt := filepath.Join(dir, "prompt.md")
	writeFile(t, prompt, "# Instructions\n\nline two\nline three\n")
	ad := drydockClaude()
	base := agents.RunSpec{AgentID: "contract", Cwd: dir, Model: "haiku", Effort: "low", MCPConfigPath: mcp, SystemAppend: "x", AllowedTools: []string{"Read", "Grep"}}
	type tc struct {
		name string
		spec agents.RunSpec
	}
	ro := base
	ro.ReadOnly, ro.PermissionMode = true, "plan"
	cases := []tc{{"read-only", ro}}
	modes := claude.New().Capabilities().PermissionModes
	for _, m := range modes {
		s := base
		s.PermissionMode = m
		cases = append(cases, tc{"mode " + m, s})
	}
	for _, c := range cases {
		args := ad.Args(c.spec)
		// Start passes the system prompt addition in a file; Args shows it inline
		if i := slices.Index(args, "--append-system-prompt"); i >= 0 {
			args[i], args[i+1] = "--append-system-prompt-file", prompt
		}
		args = append(args, "--no-session-persistence")
		code, _, stderr := startOnly(t, exe, dir, args...)
		if code != 0 || strings.Contains(stderr, "unknown option") || strings.Contains(stderr, "is invalid") || strings.Contains(stderr, "Unknown --") {
			t.Errorf("%s: claude refused Drydock's argv %v (exit %d): %s", c.name, args, code, tail(stderr))
		}
	}
	// a value the CLI does not know is refused, so the checks above can see a refusal
	if code, _, stderr := startOnly(t, exe, dir, streamArgs("--model", "haiku", "--permission-mode", "drydock-bogus")...); code == 0 {
		t.Errorf("an unknown permission mode was accepted (stderr %q): this check cannot see a refusal", tail(stderr))
	} else if m := regexp.MustCompile(`Allowed choices are ([^.\n]+)`).FindStringSubmatch(stderr); m != nil {
		listed := strings.Split(m[1], ", ")
		var extra, unlisted []string
		for _, mode := range listed {
			if !slices.Contains(modes, mode) {
				extra = append(extra, mode)
			}
		}
		for _, mode := range modes {
			if !slices.Contains(listed, mode) {
				unlisted = append(unlisted, mode)
			}
		}
		if len(extra) > 0 || len(unlisted) > 0 {
			t.Logf("%sthe CLI lists permission modes Drydock does not offer (%s) and no longer lists ones Drydock passes, which it still accepts (%s)",
				contract.NotePrefix, strings.Join(extra, ", "), strings.Join(unlisted, ", "))
		}
	}
}

// CLAUDE-EFFORT-01 (free).
func TestClaudeAcceptsEveryEffortDrydockOffers(t *testing.T) {
	exe := installedClaude(t)
	dir := t.TempDir()
	efforts := claude.New().Capabilities().Efforts
	if len(efforts) == 0 {
		t.Fatal("the adapter offers no efforts")
	}
	// control: an unknown value must be visible, as a warning (2.1.x ignores it and runs at the default) or as an error
	code, _, stderr := startOnly(t, exe, dir, streamArgs("--model", "haiku", "--effort", "drydock-bogus", "--no-session-persistence")...)
	warned := strings.Contains(stderr, "Unknown --effort value")
	if code == 0 && !warned {
		t.Fatalf("claude neither refused nor warned about an unknown effort (stderr %q): this check cannot tell an accepted effort from an ignored one", tail(stderr))
	}
	if m := regexp.MustCompile(`Valid values: ([^.\n]+)`).FindStringSubmatch(stderr); m != nil {
		var extra []string
		for _, v := range strings.Split(m[1], ", ") {
			if v = strings.TrimSpace(v); !slices.Contains(efforts, v) {
				extra = append(extra, v)
			}
		}
		if len(extra) > 0 {
			t.Logf("%sthe CLI offers efforts Drydock does not: %s", contract.NotePrefix, strings.Join(extra, ", "))
		}
	}
	for _, e := range efforts {
		code, _, stderr := startOnly(t, exe, dir, streamArgs("--model", "haiku", "--effort", e, "--no-session-persistence")...)
		if code != 0 || strings.Contains(stderr, "Unknown --effort value") || strings.Contains(stderr, "is invalid") {
			t.Errorf("--effort %s is not accepted (exit %d): %s", e, code, tail(stderr))
		}
	}
}

// CLAUDE-STREAM-01 (paid): one haiku turn with a Read tool call, started with the adapter's own argv.
func TestClaudeStreamJSONEventShapes(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.02)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hello.txt"), "hello from file\n")
	args := drydockClaude().Args(agents.RunSpec{Model: "haiku", Effort: "low", AllowedTools: []string{"Read"}})
	args = append(args, "--no-session-persistence", "--max-budget-usd", usd(maxUSD))
	p := startClaude(t, "CLAUDE-STREAM-01", exe, dir, childEnv(), args...)
	tr := p.turn("Use the Read tool to read hello.txt in the current directory, then reply with its exact content only.")
	p.close()

	seen := map[string]bool{}
	var sawDelta bool
	var useID string
	var resultOK, textOK bool
	for _, e := range tr.events {
		kind := e.str("type")
		if st := e.str("subtype"); st != "" {
			kind += "/" + st
		}
		seen[kind] = true
		switch e.str("type") {
		case "stream_event":
			ev := e.obj("event")
			if ev.str("type") == "content_block_delta" && ev.obj("delta").str("type") == "text_delta" {
				sawDelta = sawDelta || ev.obj("delta").str("text") != ""
			}
		case "assistant":
			for _, b := range e.blocks() {
				switch b.str("type") {
				case "tool_use":
					if b.str("name") == "Read" && b.str("id") != "" && b.obj("input").str("file_path") != "" {
						useID = b.str("id")
					}
				case "text":
					textOK = textOK || strings.Contains(b.str("text"), "hello from file")
				}
			}
		case "user":
			for _, b := range e.blocks() {
				if b.str("type") == "tool_result" && b.str("tool_use_id") == useID && useID != "" {
					resultOK = strings.Contains(fmt.Sprint(b["content"]), "hello from file")
				}
			}
		}
	}
	in := tr.init
	if in == nil || in.str("session_id") == "" || in.str("model") == "" || !slices.Contains(stringList(in.list("tools")), "Read") {
		t.Errorf("system/init lacks session_id, model or a tools list with Read: %v", keys(in))
	}
	if useID == "" {
		t.Errorf("no assistant tool_use block for Read with an id and input.file_path")
	}
	if !resultOK {
		t.Errorf("no user tool_result block carrying the file's text for the Read call %q", useID)
	}
	if !sawDelta {
		t.Errorf("no stream_event content_block_delta with a text_delta (--include-partial-messages): Drydock would stream nothing")
	}
	if !textOK {
		t.Errorf("no assistant text block with the answer")
	}
	r := tr.result
	if r.str("subtype") != "success" || r["is_error"] != false || r.str("stop_reason") != "end_turn" || !strings.Contains(r.str("result"), "hello from file") ||
		in == nil || r.str("session_id") != in.str("session_id") {
		t.Errorf("result: subtype %q is_error %v stop_reason %q result %q session %q (init %q)", r.str("subtype"), r["is_error"], r.str("stop_reason"), r.str("result"), r.str("session_id"), in.str("session_id"))
	}

	// what Drydock's parser makes of the same output
	var calls, results, texts int
	var sess workflow.SessionChunk
	var usage workflow.UsageChunk
	var done workflow.DoneChunk
	for _, c := range p.chunks {
		switch c.Kind {
		case workflow.ChunkSession:
			_ = c.Decode(&sess)
		case workflow.ChunkToolCall:
			var tc workflow.ToolCall
			if c.Decode(&tc) == nil && tc.Name == "Read" {
				calls++
			}
		case workflow.ChunkToolResult:
			var res workflow.ToolResult
			if c.Decode(&res) == nil && res.Name == "Read" && strings.Contains(res.Output, "hello from file") {
				results++
			}
		case workflow.ChunkText:
			texts++
		case workflow.ChunkUsage:
			_ = c.Decode(&usage)
		case workflow.ChunkDone:
			_ = c.Decode(&done)
		}
	}
	if in != nil && sess.SessionID != in.str("session_id") || calls != 1 || results != 1 || texts == 0 || usage.Source != "reported" || usage.CostUSD <= 0 ||
		!strings.Contains(usage.Model, "haiku") || done.StopReason != "end_turn" {
		t.Errorf("parser: session %q, Read calls %d, results %d, text deltas %d, usage %+v, done %q", sess.SessionID, calls, results, texts, usage, done.StopReason)
	}
	var kinds []string
	for k := range seen {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	t.Logf("%sevent types in one turn: %s", contract.NotePrefix, strings.Join(kinds, ", "))
}

// CLAUDE-USAGE-01 (paid).
func TestClaudeResultReportsCostAndModelUsage(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.01)
	dir := t.TempDir()
	args := drydockClaude().Args(agents.RunSpec{Model: "haiku", ExtraArgs: []string{"--tools", "", "--no-session-persistence", "--max-budget-usd", usd(maxUSD)}})
	p := startClaude(t, "CLAUDE-USAGE-01", exe, dir, childEnv(), args...)
	tr := p.turn("Reply with the single word OK.")
	p.close()
	r := tr.result
	total, ok := r.num("total_cost_usd")
	if !ok || total <= 0 {
		t.Fatalf("result.total_cost_usd is missing or not positive: %v", r["total_cost_usd"])
	}
	mu := r.obj("modelUsage")
	if len(mu) == 0 {
		t.Fatalf("result.modelUsage is missing: %v", keys(r))
	}
	var sum float64
	var haiku bool
	for model, raw := range mu {
		m, _ := raw.(map[string]any)
		entry := event(m)
		haiku = haiku || strings.Contains(model, "haiku")
		for _, f := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "costUSD", "contextWindow"} {
			if _, ok := entry.num(f); !ok {
				t.Errorf("modelUsage[%q] lacks the number %s the parser reads: %v", model, f, keys(entry))
			}
		}
		c, _ := entry.num("costUSD")
		sum += c
	}
	if !haiku {
		t.Errorf("modelUsage is not keyed by the model id that answered (asked for haiku): %v", modelUsageKeys(r))
	}
	if math.Abs(sum-total) > 1e-9+1e-6*total {
		t.Errorf("modelUsage costUSD adds up to %v, total_cost_usd is %v", sum, total)
	}
	u := r.obj("usage")
	if _, ok := u.num("input_tokens"); !ok {
		t.Errorf("result.usage lacks input_tokens: %v", keys(u))
	}
	us := p.chunksOf(workflow.ChunkUsage)
	var uc workflow.UsageChunk
	if len(us) != 1 || us[0].Decode(&uc) != nil || uc.CostUSD != total || uc.Source != "reported" || !strings.Contains(uc.Model, "haiku") {
		t.Errorf("parser usage %+v (%d chunks), total %v", uc, len(us), total)
	}
	t.Logf("%s%s answered, $%.6f", contract.NotePrefix, strings.Join(modelUsageKeys(r), ", "), total)
}

// CLAUDE-USAGE-02 (paid): two turns in one process.
func TestClaudeReportsEachTurnsOwnUsage(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.01)
	dir := t.TempDir()
	args := drydockClaude().Args(agents.RunSpec{Model: "haiku", ExtraArgs: []string{"--tools", "", "--no-session-persistence", "--max-budget-usd", usd(maxUSD)}})
	p := startClaude(t, "CLAUDE-USAGE-02", exe, dir, childEnv(), args...)
	first := p.turn("Reply with the single word ONE.")
	second := p.turn("Reply with the single word TWO.")
	p.close()
	tokens := func(e event, in, out, read, write string) float64 {
		var n float64
		for _, f := range []string{in, out, read, write} {
			v, _ := e.num(f)
			n += v
		}
		return n
	}
	modelTokens := func(r event) float64 {
		var n float64
		for _, raw := range r.obj("modelUsage") {
			m, _ := raw.(map[string]any)
			n += tokens(event(m), "inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens")
		}
		return n
	}
	turnTokens := func(r event) float64 {
		return tokens(r.obj("usage"), "input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens")
	}
	c1, _ := first.result.num("total_cost_usd")
	c2, _ := second.result.num("total_cost_usd")
	m1, m2, t1, t2 := modelTokens(first.result), modelTokens(second.result), turnTokens(first.result), turnTokens(second.result)
	t.Logf("%sturn 1: total_cost_usd $%.6f, modelUsage %v tokens, usage %v tokens; turn 2: $%.6f, modelUsage %v tokens, usage %v tokens", contract.NotePrefix, c1, m1, t1, c2, m2, t2)
	if m1 == 0 || t2 == 0 {
		t.Fatalf("the results carry no token counts to compare: %v / %v", keys(first.result), keys(second.result))
	}
	switch {
	case m2 == m1+t2 && c2 >= c1:
		// running totals, as measured with 2.1.296: the parser must record the difference
	case m2 == t2:
		t.Fatalf("the second result's modelUsage (%v tokens) is now the turn's own: the parser's running-total rule "+
			"(internal/agents/claude/parser.go, turnCost and turnModel) would under-count every later turn; change it before adopting this version", m2)
	default:
		t.Fatalf("the second result's modelUsage (%v tokens) is neither turn 2's usage (%v) nor the process total (%v)", m2, t2, m1+t2)
	}
	// Drydock's own parser, fed this real stream, records each turn once
	var us []workflow.UsageChunk
	for _, c := range p.chunks {
		if c.Kind == workflow.ChunkUsage {
			var u workflow.UsageChunk
			if err := c.Decode(&u); err != nil {
				t.Fatal(err)
			}
			us = append(us, u)
		}
	}
	if len(us) != 2 {
		t.Fatalf("the parser produced %d usage records for two turns", len(us))
	}
	got := float64(us[1].InputTokens + us[1].OutputTokens + us[1].CachedTokens + us[1].CacheWriteTokens)
	if got != t2 || math.Abs(us[1].CostUSD-(c2-c1)) > 1e-9 || math.Abs(us[0].CostUSD+us[1].CostUSD-c2) > 1e-9 {
		t.Errorf("the parser recorded turn 2 as %v tokens and $%.6f; the CLI's own figures are %v tokens and $%.6f (process total $%.6f)", got, us[1].CostUSD, t2, c2-c1, c2)
	}
}

// CLAUDE-USAGE-03 (paid): one session, two processes; the second resumes the first.
func TestClaudeResumedSessionReportsItsWholeTotals(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.01)
	dir := t.TempDir()
	session := uuidV4(t)
	extra := []string{"--tools", "", "--max-budget-usd", usd(maxUSD)}
	first := startClaude(t, "CLAUDE-USAGE-03", exe, dir, childEnv(), drydockClaude().Args(agents.RunSpec{Model: "haiku", ExtraArgs: append([]string{"--session-id", session}, extra...)})...)
	one := first.turn("Reply with the single word ONE.")
	first.close()
	baseline := lastTotals(t, first.chunks)
	second := startClaude(t, "CLAUDE-USAGE-03", exe, dir, childEnv(), drydockClaude().Args(agents.RunSpec{Model: "haiku", ResumeID: session, ExtraArgs: extra})...)
	second.parser.SetBaseline(baseline) // what the adapter does for a ResumeID session with a stored baseline
	two := second.turn("Reply with the single word TWO.")
	second.close()
	modelTokens := func(r event) float64 {
		var n float64
		for _, raw := range r.obj("modelUsage") {
			m, _ := raw.(map[string]any)
			for _, f := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens"} {
				v, _ := event(m).num(f)
				n += v
			}
		}
		return n
	}
	turnTokens := func(r event) float64 {
		var n float64
		for _, f := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
			v, _ := r.obj("usage").num(f)
			n += v
		}
		return n
	}
	c1, _ := one.result.num("total_cost_usd")
	c2, _ := two.result.num("total_cost_usd")
	m1, m2, t2 := modelTokens(one.result), modelTokens(two.result), turnTokens(two.result)
	t.Logf("%sprocess 1: $%.6f, %v tokens; process 2 (--resume): $%.6f, modelUsage %v tokens, its own usage %v tokens", contract.NotePrefix, c1, m1, c2, m2, t2)
	switch {
	case m2 == m1+t2 && c2 > c1:
		// the session's totals, as measured with 2.1.296: the baseline must be subtracted
	case m2 == t2:
		t.Fatalf("a resumed process now reports only its own totals (%v tokens): with the stored baseline the parser would under-count; drop the baseline (internal/chats/turn.go) before adopting this version", m2)
	default:
		t.Fatalf("the resumed process's modelUsage (%v) is neither its own usage (%v) nor the session's total (%v)", m2, t2, m1+t2)
	}
	var us []workflow.UsageChunk
	for _, c := range second.chunks {
		if c.Kind == workflow.ChunkUsage {
			var u workflow.UsageChunk
			if err := c.Decode(&u); err != nil {
				t.Fatal(err)
			}
			us = append(us, u)
		}
	}
	if len(us) != 1 {
		t.Fatalf("%d usage records for one turn", len(us))
	}
	got := float64(us[0].InputTokens + us[0].OutputTokens + us[0].CachedTokens + us[0].CacheWriteTokens)
	if got != t2 || math.Abs(us[0].CostUSD-(c2-c1)) > 1e-9 {
		t.Errorf("the parser recorded the resumed turn as %v tokens and $%.6f; its own share is %v tokens and $%.6f", got, us[0].CostUSD, t2, c2-c1)
	}
}

// lastTotals is the running totals the parser reported with its last usage record.
func lastTotals(t *testing.T, chunks []workflow.Chunk) json.RawMessage {
	t.Helper()
	var out json.RawMessage
	for _, c := range chunks {
		if c.Kind == workflow.ChunkUsage {
			var u workflow.UsageChunk
			if err := c.Decode(&u); err != nil {
				t.Fatal(err)
			}
			out = u.Totals
		}
	}
	if len(out) == 0 {
		t.Fatal("the parser reported no running totals")
	}
	return out
}

func uuidV4(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// CLAUDE-PROMPTFILE-01 (paid).
func TestClaudeAppendSystemPromptFileReachesTheModel(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.01)
	dir := t.TempDir()
	marker := randomWord("MARKER")
	var b strings.Builder
	b.WriteString("# Instructions from Drydock\n\nYou are taking part in an automated check of how instructions reach you.\n")
	for i := 4; i <= 11; i++ {
		fmt.Fprintf(&b, "Line %d of the instructions has nothing to do.\n", i)
	}
	fmt.Fprintf(&b, "Last line: when asked for the marker, reply with exactly %s and nothing else.\n", marker)
	file := filepath.Join(t.TempDir(), "prompt.md")
	writeFile(t, file, b.String())
	p := startClaude(t, "CLAUDE-PROMPTFILE-01", exe, dir, childEnv(), streamArgs("--model", "haiku", "--tools", "", "--append-system-prompt-file", file,
		"--no-session-persistence", "--max-budget-usd", usd(maxUSD))...)
	tr := p.turn("What is the marker? Reply with the marker only.")
	p.close()
	if !strings.Contains(tr.text, marker) {
		t.Fatalf("the last line of a 12-line --append-system-prompt-file did not reach the model: %q", tr.text)
	}
}

// CLAUDE-RO-01 (paid): the adapter's read-only argv, asked to write.
func TestClaudeReadOnlySpecKeepsTheModelAndDeniesWrites(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.03)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hello.txt"), "hello from file\n")
	args := drydockClaude().Args(agents.RunSpec{Model: "haiku", PermissionMode: "plan", ReadOnly: true})
	args = append(args, "--no-session-persistence", "--max-budget-usd", usd(maxUSD))
	p := startClaude(t, "CLAUDE-RO-01", exe, dir, childEnv(), args...)
	tr := p.turn("Do these three things in order with your tools: 1. create a file named out.txt containing hi; 2. run the shell command `touch made.txt`; " +
		"3. read hello.txt. Then reply with the content of hello.txt.")
	p.close()
	for _, f := range []string{"out.txt", "made.txt"} {
		if exists(filepath.Join(dir, f)) {
			t.Errorf("a read-only session created %s", f)
		}
	}
	models := modelUsageKeys(tr.result)
	if len(models) == 0 {
		t.Fatalf("no modelUsage in the result: %v", keys(tr.result))
	}
	for _, m := range models {
		if !strings.Contains(m, "haiku") {
			t.Errorf("a read-only haiku session was answered by %s too: %v", m, models)
		}
	}
	var denied []string
	for _, d := range tr.result.list("permission_denials") {
		if m, ok := d.(map[string]any); ok {
			denied = append(denied, event(m).str("tool_name"))
		}
	}
	tools := stringList(tr.init.list("tools"))
	t.Logf("%sargv %v; answered by %v; writing tools offered: %v; refused calls: %v", contract.NotePrefix, args[len(streamArgs()):], models,
		slices.DeleteFunc(slices.Clone(tools), func(s string) bool { return !slices.Contains(claude.ReadOnlyDenied, s) }), denied)
}

// CLAUDE-PLAN-01, the observation (paid): which model answers a plan-mode session started with --model haiku.
func TestClaudePlanModeHaikuObservation(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.10)
	dir := t.TempDir()
	p := startClaude(t, "CLAUDE-PLAN-01", exe, dir, childEnv(), streamArgs("--model", "haiku", "--permission-mode", "plan", "--tools", "",
		"--no-session-persistence", "--max-budget-usd", usd(maxUSD))...)
	tr := p.turn("Reply with the single word OK.")
	p.close()
	models := modelUsageKeys(tr.result)
	if len(models) == 0 {
		t.Fatalf("no modelUsage to observe: %v", keys(tr.result))
	}
	sort.Strings(models)
	cost, _ := tr.result.num("total_cost_usd")
	var other []string
	for _, m := range models {
		if !strings.Contains(m, "haiku") {
			other = append(other, m)
		}
	}
	if len(other) > 0 {
		t.Logf("%splan mode with --model haiku was answered by %s ($%.4f for one word; init said %q): the hazard is still there", contract.NotePrefix,
			strings.Join(models, ", "), cost, tr.init.str("model"))
	} else {
		t.Logf("%splan mode with --model haiku was answered by haiku only ($%.4f): the hazard did not show this time", contract.NotePrefix, cost)
	}
}

// CLAUDE-SETTINGS-01 (paid): a repository with its own hook and CLAUDE.md, without and with --setting-sources user.
func TestClaudeUserSettingSourcesSkipRepositoryHooksAndMemory(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.02)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)
	marker := filepath.Join(t.TempDir(), "hook-ran.txt")
	code := randomWord("HERON")
	writeFile(t, filepath.Join(repo, ".claude", "settings.json"),
		`{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo ran >> `+filepath.ToSlash(marker)+`"}]}]}}`)
	writeFile(t, filepath.Join(repo, "CLAUDE.md"), "# Project\n\nThe project codeword is "+code+".\n")
	ask := "Without using any tool: what is the project codeword? Answer with the codeword only, or UNKNOWN if you were not given one."
	run := func(id string, extra ...string) turn {
		p := startClaude(t, id, exe, repo, childEnv(), streamArgs(append([]string{"--model", "haiku", "--tools", "", "--no-session-persistence", "--max-budget-usd", usd(maxUSD)}, extra...)...)...)
		tr := p.turn(ask)
		p.close()
		return tr
	}
	control := run("CLAUDE-SETTINGS-01")
	hookRan, knewCode := exists(marker), strings.Contains(control.text, code)
	_ = os.Remove(marker)
	userOnly := run("CLAUDE-SETTINGS-01", "--setting-sources", "user")
	if hookRan {
		if exists(marker) {
			t.Errorf("with --setting-sources user the repository's own UserPromptSubmit hook still ran")
		}
	} else {
		t.Logf("%sthe control run without the flag did not run the repository's hook either", contract.NotePrefix)
	}
	if knewCode {
		if strings.Contains(userOnly.text, code) {
			t.Errorf("with --setting-sources user the project's CLAUDE.md is loaded natively (%q): Drydock appends it too, so it would appear twice", userOnly.text)
		}
	} else {
		t.Logf("%sthe control run without the flag did not see CLAUDE.md either (%q)", contract.NotePrefix, control.text)
	}
	t.Logf("%scontrol: hook ran %v, knew the codeword %v; --setting-sources user: hook ran %v, answer %q", contract.NotePrefix, hookRan, knewCode, exists(marker), userOnly.text)
}

// CLAUDE-RESTRICTED-01, the free half: restricted mode refuses bypassPermissions before it starts.
func TestClaudeRestrictedModeRefusesBypassPermissions(t *testing.T) {
	exe := installedClaude(t)
	dir := t.TempDir()
	if code, _, stderr := startOnly(t, exe, dir, streamArgs("--model", "haiku", "--restricted", "--permission-mode", "acceptEdits", "--no-session-persistence")...); code != 0 {
		t.Fatalf("--restricted with acceptEdits does not start (exit %d): %s", code, tail(stderr))
	}
	code, _, stderr := startOnly(t, exe, dir, streamArgs("--model", "haiku", "--restricted", "--permission-mode", "bypassPermissions", "--no-session-persistence")...)
	if code == 0 {
		t.Fatalf("--restricted accepted bypassPermissions: %s", tail(stderr))
	}
	t.Logf("%s%s", contract.NotePrefix, tail(stderr))
}

// CLAUDE-RESTRICTED-01, the paid half: what the file tools may write under --restricted in acceptEdits mode, and which tools
// are left.
func TestClaudeRestrictedModeConfinesTools(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.08)
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	gitInit(t, repo)
	allowed := []string{"package.json", "Makefile", "tsconfig.json", filepath.Join(".github", "workflows", "drydock-probe.yml"), ".gitignore"}
	refused := []string{filepath.Join(".claude", "settings.json"), filepath.Join(".git", "hooks", "pre-commit"), filepath.Join("..", "outside.txt")}
	abs := func(rel string) string { return filepath.Clean(filepath.Join(repo, rel)) }
	var all []string
	for _, f := range append(append([]string(nil), allowed...), refused...) {
		all = append(all, abs(f))
	}
	p := startClaude(t, "CLAUDE-RESTRICTED-01", exe, repo, childEnv(), streamArgs("--model", "haiku", "--restricted", "--permission-mode", "acceptEdits",
		"--no-session-persistence", "--max-budget-usd", usd(maxUSD))...)
	attempted := map[string]bool{}
	note := func(tr turn) {
		for _, u := range tr.uses {
			if fp := u.Input.str("file_path"); fp != "" {
				attempted[filepath.Clean(fp)] = true
			}
		}
	}
	tr := p.turn("This is an automated permissions check. Use the Write tool once for each of these files, in this order, each with the content " +
		"drydock-probe. Attempt every one even if an earlier write fails or is refused; do not use any other tool and do not ask questions. Then reply DONE.\n" +
		strings.Join(all, "\n"))
	note(tr)
	tools := stringList(tr.init.list("tools"))
	for round := 0; round < 2; round++ {
		var missing []string
		for _, f := range all {
			if !attempted[f] {
				missing = append(missing, f)
			}
		}
		if len(missing) == 0 {
			break
		}
		more := p.turn("You have not called the Write tool for these files yet. Call it now for each of them, with the content drydock-probe, even if you expect a refusal. Then reply DONE.\n" +
			strings.Join(missing, "\n"))
		note(more)
	}
	p.close()

	for _, name := range []string{"Bash", "PowerShell", "REPL", "WebFetch"} {
		if slices.Contains(tools, name) {
			t.Errorf("--restricted still offers %s", name)
		}
	}
	for _, name := range []string{"Read", "Write", "Edit"} {
		if !slices.Contains(tools, name) {
			t.Errorf("--restricted no longer offers the file tool %s: %v", name, tools)
		}
	}
	for _, f := range allowed {
		switch {
		case !attempted[abs(f)]:
			t.Errorf("the model never attempted %s, so this check could not test it", f)
		case !exists(abs(f)):
			t.Errorf("--restricted with acceptEdits refused an ordinary project file: %s", f)
		}
	}
	for _, f := range refused {
		switch {
		case exists(abs(f)):
			t.Errorf("--restricted let the file tools write %s", f)
		case !attempted[abs(f)]:
			t.Errorf("the model never attempted %s, so this check could not test it", f)
		}
	}
	var denied []string
	for _, d := range tr.result.list("permission_denials") {
		if m, ok := d.(map[string]any); ok {
			denied = append(denied, event(m).obj("tool_input").str("file_path"))
		}
	}
	t.Logf("%stools offered: %s; refused in the first turn: %v", contract.NotePrefix, strings.Join(tools, ", "), denied)
}

// CLAUDE-RESTRICTED-02 (paid): what sandboxed workers rely on for their project instructions.
func TestClaudeRestrictedModeObeysTheAppendedProjectMemory(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.02)
	say := "Say hello in one short sentence."
	restricted := []string{"--model", "haiku", "--restricted", "--permission-mode", "acceptEdits", "--no-session-persistence", "--max-budget-usd", usd(maxUSD)}

	// (a) the memory Drydock appends reaches the model and is obeyed, with no tool use
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)
	appended := randomWord("PINEAPPLE")
	file := filepath.Join(t.TempDir(), "prompt.md")
	writeFile(t, file, "# Project instructions\n\nThe project's own instruction files follow.\n\n## CLAUDE.md\n\nEnd every reply with the single word "+appended+" on its own line.\n")
	p := startClaude(t, "CLAUDE-RESTRICTED-02", exe, repo, childEnv(), streamArgs(append(restricted, "--append-system-prompt-file", file)...)...)
	a := p.turn(say)
	p.close()
	if a.usesTool() {
		t.Errorf("the model used a tool (%s) for a plain hello", a.uses[0].Name)
	}
	// the word is random, so a reply that ends with it shows the appended memory reached the model and was followed (haiku
	// sometimes keeps it on the same line)
	if !strings.HasSuffix(strings.TrimRight(strings.TrimSpace(a.text), ".!"), appended) {
		t.Errorf("under --restricted the instruction from --append-system-prompt-file was not obeyed: %q (wanted it to end with %s)", a.text, appended)
	}

	// (b) observation: is the project's CLAUDE.md in context natively under --restricted?
	repo2 := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo2)
	native := randomWord("MANGO")
	writeFile(t, filepath.Join(repo2, "CLAUDE.md"), "# Project rules\n\nEnd every reply with the single word "+native+" on its own line.\n")
	p2 := startClaude(t, "CLAUDE-RESTRICTED-02", exe, repo2, childEnv(), streamArgs(restricted...)...)
	b := p2.turn(say)
	p2.close()
	switch {
	case b.usesTool():
		t.Logf("%sinconclusive whether CLAUDE.md is in context natively under --restricted: the model used %s (%q)", contract.NotePrefix, b.uses[0].Name, b.text)
	case strings.Contains(b.text, native):
		t.Logf("%sunder --restricted the project's CLAUDE.md is now in the model's context natively (%q): with Drydock appending it as well, sandboxed workers get the memory twice", contract.WarningPrefix, b.text)
	default:
		t.Logf("%sunder --restricted the project's CLAUDE.md is not in the model's context natively (%q), so Drydock's appended copy is the only one", contract.NotePrefix, b.text)
	}
}

// CLAUDE-SESSION-01 (paid): Drydock started from inside a Claude Code session. The runner gives this test the enclosing
// session's variables when it was itself started inside one (TestRef.Enclosing); elsewhere a made-up CLAUDE_CODE_SESSION_ID
// stands in for the enclosing session.
func TestClaudeStartedByDrydockGetsItsOwnSession(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.02)
	dir := t.TempDir()
	outer := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if outer == "" {
		outer = "6f1d2c3b-4a59-4e6f-8a7b-" + strings.TrimPrefix(randomWord("x"), "x-") + "000000"
		t.Setenv("CLAUDE_CODE_SESSION_ID", outer)
		t.Setenv("CLAUDECODE", "1")
		t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	}
	var present []string
	for _, n := range contract.EnclosingSessionEnv {
		if _, ok := os.LookupEnv(n); ok {
			present = append(present, n)
		}
	}
	extra := []string{"--tools", "", "--no-session-persistence", "--max-budget-usd", usd(maxUSD)}

	// control: a child that inherits the environment as it is
	inherit := contract.WithoutVars(os.Environ(), "DRYDOCK_FAKE_AGENT", "DRYDOCK_FAKE_SCRIPT", mcpServerEnv)
	p := startClaude(t, "CLAUDE-SESSION-01", exe, dir, inherit, drydockClaude().Args(agents.RunSpec{Model: "haiku", ExtraArgs: extra})...)
	tr := p.turn("Reply with the single word OK.")
	p.close()
	t.Logf("%swith %s set, a claude that inherits them reports the enclosing session: %v", contract.NotePrefix, strings.Join(present, ", "),
		tr.result.str("session_id") == outer)

	// through the adapter, from the same environment
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	s, err := drydockClaude().Start(ctx, proc.New(), agents.RunSpec{AgentID: "contract", Cwd: dir, Model: "haiku", ExtraArgs: extra})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background())
	if err := s.Send(ctx, "Reply with the single word OK."); err != nil {
		t.Fatal(err)
	}
	var cost float64
	func() {
		for {
			select {
			case c, ok := <-s.Output():
				if !ok {
					return
				}
				switch c.Kind {
				case workflow.ChunkUsage:
					var u workflow.UsageChunk
					if c.Decode(&u) == nil {
						cost = u.CostUSD
					}
				case workflow.ChunkDone:
					return
				}
			case <-ctx.Done():
				t.Fatalf("no answer within 180s: %s", tail(s.Stderr()))
			}
		}
	}()
	recordSpend(t, "CLAUDE-SESSION-01", cost)
	if id := s.ID(); id == "" || id == outer {
		t.Fatalf("an agent Drydock started reported session %q, the enclosing one (%s set): the adapter passed the enclosing session on", id, strings.Join(present, ", "))
	}
}

// CLAUDE-MCP-01 (paid): a project .mcp.json and the user's own servers must not join a --strict-mcp-config session.
func TestClaudeStrictMCPConfigLoadsOnlyTheGivenServer(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.02)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	server := func(name string) string {
		return fmt.Sprintf(`{"mcpServers":{%q:{"type":"stdio","command":%q,"args":[],"env":{%q:"1"}}}}`, name, self, mcpServerEnv)
	}
	writeFile(t, filepath.Join(dir, ".mcp.json"), server("intruder"))
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	writeFile(t, cfg, server("drydock"))
	args := drydockClaude().Args(agents.RunSpec{Model: "haiku", MCPConfigPath: cfg})
	args = append(args, "--no-session-persistence", "--max-budget-usd", usd(maxUSD))
	p := startClaude(t, "CLAUDE-MCP-01", exe, dir, childEnv(), args...)
	tr := p.turn("Reply with the single word OK.")
	p.close()
	status := map[string]string{}
	var plugins, others []string
	for _, s := range tr.init.list("mcp_servers") {
		m, _ := s.(map[string]any)
		e := event(m)
		status[e.str("name")] = e.str("status")
		switch {
		case e.str("name") == "drydock":
		case e.str("source") == "plugin":
			plugins = append(plugins, e.str("name"))
		default:
			others = append(others, e.str("name")+" ("+e.str("source")+")")
		}
	}
	if status["drydock"] == "" {
		t.Errorf("the configured server is not in the session: %v", status)
	}
	if len(others) > 0 {
		t.Errorf("--strict-mcp-config let other configured MCP servers join (a repository's .mcp.json or the user's own): %v", others)
	}
	// installed plugins' servers still start (measured with 2.1.296); what matters is that none of their tools is offered,
	// which the tools check below asserts
	if len(plugins) > 0 {
		t.Logf("%s%d MCP servers from installed plugins started despite --strict-mcp-config (%s); their tools are denied by %s", contract.NotePrefix,
			len(plugins), strings.Join(plugins[:min(len(plugins), 5)], ", "), claude.PluginToolsDenied)
	}
	tools := stringList(tr.init.list("tools"))
	var foreign []string
	for _, tool := range tools {
		if strings.HasPrefix(tool, "mcp__") && !strings.HasPrefix(tool, "mcp__drydock__") {
			foreign = append(foreign, tool)
		}
	}
	if len(foreign) > 0 {
		t.Errorf("tools from other servers reached the session: %v", foreign)
	}
	if status["drydock"] == "connected" && !slices.Contains(tools, "mcp__drydock__echo") {
		t.Errorf("the configured server connected but its tool mcp__drydock__echo is not offered: %v", tools)
	}
	t.Logf("%sdrydock %s; other servers %v; plugin servers %d; foreign tools %v", contract.NotePrefix, status["drydock"], others, len(plugins), foreign)
}

// CLAUDE-DENIAL-01 (paid): a write the default mode refuses, reported in permission_denials.
func TestClaudeReportsRefusedToolCallsAsPermissionDenials(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.02)
	dir := t.TempDir()
	args := drydockClaude().Args(agents.RunSpec{Model: "haiku", PermissionMode: "default"})
	args = append(args, "--no-session-persistence", "--max-budget-usd", usd(maxUSD))
	p := startClaude(t, "CLAUDE-DENIAL-01", exe, dir, childEnv(), args...)
	ask := "Use the Write tool to create a file named denied.txt in the current directory containing the word hi. Do not use any other tool. Then reply DONE."
	tr := p.turn(ask)
	if !slices.ContainsFunc(tr.uses, func(u toolUse) bool { return u.Name == "Write" }) {
		tr = p.turn("Please call the Write tool now, exactly as asked, even if you expect it to be refused. " + ask)
	}
	p.close()
	if exists(filepath.Join(dir, "denied.txt")) {
		t.Fatal("the refused write happened")
	}
	var use toolUse
	for _, u := range tr.uses {
		if u.Name == "Write" {
			use = u
		}
	}
	if use.ID == "" {
		t.Fatalf("the model never called Write, so there was nothing to refuse: %q", tr.text)
	}
	var found bool
	for _, d := range tr.result.list("permission_denials") {
		m, _ := d.(map[string]any)
		e := event(m)
		if e.str("tool_name") == "Write" && e.str("tool_use_id") == use.ID && strings.HasSuffix(filepath.ToSlash(e.obj("tool_input").str("file_path")), "denied.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("result.permission_denials does not carry the refused Write %s: %v", use.ID, tr.result["permission_denials"])
	}
	var parsed bool
	for _, c := range p.chunksOf(workflow.ChunkPermissionDenied) {
		var d workflow.PermissionDenial
		parsed = parsed || (c.Decode(&d) == nil && d.ToolName == "Write" && d.ToolUseID == use.ID)
	}
	if !parsed {
		t.Fatalf("the parser did not report the refusal as a permission_denied chunk")
	}
}

// CLAUDE-MCPWAIT-01 (paid, slow: about two and a half minutes): a sandboxed worker's long MCP tool call is waited for.
func TestClaudeWaitsForALongMCPToolCall(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.03)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)
	marker := randomWord("LONG-COMMAND-DONE")
	const wait = 130 * time.Second // longer than the 120 seconds after which Claude Code moves a call to the background
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	writeFile(t, cfg, fmt.Sprintf(`{"mcpServers":{"slow":{"type":"stdio","command":%q,"args":[],"env":{%q:"slow","DRYDOCK_CONTRACT_MCP_WAIT":%q,"DRYDOCK_CONTRACT_MCP_MARKER":%q}}}}`,
		self, mcpServerEnv, wait.String(), marker))
	// what Drydock sets for sandboxed workers; the variables are replaced, not added, because a hosting session may export
	// CLAUDE_AUTO_BACKGROUND_TASKS=true
	waitEnv := []string{"CLAUDE_AUTO_BACKGROUND_TASKS=false", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1", "MCP_TOOL_TIMEOUT=3900000"}
	env := append(contract.WithoutVars(childEnv(), "CLAUDE_AUTO_BACKGROUND_TASKS", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", "MCP_TOOL_TIMEOUT"), waitEnv...)
	p := startClaude(t, "CLAUDE-MCPWAIT-01", exe, repo, env, streamArgs("--model", "haiku", "--restricted", "--permission-mode", "acceptEdits",
		"--mcp-config", cfg, "--strict-mcp-config", "--allowedTools", "mcp__slow__wait_then_mark", "--no-session-persistence", "--max-budget-usd", usd(maxUSD))...)
	began := time.Now()
	tr := p.turnWithin("Call the wait_then_mark tool of the MCP server named slow exactly once. The job takes a few minutes: wait for it. "+
		"Then reply with the exact text the tool returned.", 8*time.Minute)
	took := time.Since(began).Round(time.Second)
	p.close()
	if !slices.ContainsFunc(tr.uses, func(u toolUse) bool { return u.Name == "mcp__slow__wait_then_mark" }) {
		t.Fatalf("the model never called mcp__slow__wait_then_mark (%q), so nothing long ran", tr.text)
	}
	if !strings.Contains(tr.text, marker) {
		t.Fatalf("a %s MCP call did not reach the model in the same turn (the turn ended after %s): %q", wait, took, tr.text)
	}
	t.Logf("%sthe %s call's result arrived in the same turn, which took %s, with %s", contract.NotePrefix, wait, took, strings.Join(waitEnv, " "))
}

func keys(e event) []string {
	var out []string
	for k := range e {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// reviewedTools are the built-in tools a Drydock session is meant to have (some only on one platform, in plan mode or in
// another CLI version). CLAUDE-QUIET-01 warns about any other tool a session is offered, so each tool a new CLI version adds
// is either listed here or denied (claude.UnattendedDenied) before the version is adopted.
var reviewedTools = []string{
	"Bash", "PowerShell", "Edit", "MultiEdit", "Write", "Read", "Glob", "Grep", "LS", "NotebookEdit", "TodoWrite",
	"Task", "Agent", "TaskOutput", "TaskStop", "BashOutput", "KillShell", "ToolSearch", "WebFetch", "WebSearch", "LSP",
	"ListMcpResourcesTool", "ReadMcpResourceTool", "ReadMcpResourceDirTool", "EnterPlanMode", "ExitPlanMode",
}

// quietNames are the names claude.QuietEnv sets, for a check that wants the CLI's defaults.
func quietNames() []string {
	var out []string
	for _, kv := range claude.QuietEnv {
		k, _, _ := strings.Cut(kv, "=")
		out = append(out, k)
	}
	return out
}

// closedPort is the address of a local port nothing listens on (a listener is opened and closed again).
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// CLAUDE-QUIET-01 (free): started as Drydock starts it, Claude Code offers no skills or slash commands and none of the tools
// every Drydock session denies, and the quiet environment still removes the tools it names. The prompt is /context, which the
// CLI answers itself when slash commands are off; the API address is a closed local port, so a CLI that did send it to a model
// could not spend either.
func TestClaudeSessionsStartedByDrydockAreQuiet(t *testing.T) {
	exe := installedClaude(t)
	dir := t.TempDir()
	gitInit(t, dir)
	offline := "ANTHROPIC_BASE_URL=http://" + closedPort(t)
	look := func(env []string, args ...string) (tools, skills, commands []string) {
		p := startClaude(t, "CLAUDE-QUIET-01", exe, dir, append(env, offline), append(args, "--no-session-persistence")...)
		tr := p.turnWithin("/context", 120*time.Second)
		p.close()
		if c, _ := tr.result.num("total_cost_usd"); c != 0 {
			t.Errorf("/context cost %v: the CLI sent it to a model", c)
		}
		return stringList(tr.init.list("tools")), stringList(tr.init.list("skills")), stringList(tr.init.list("slash_commands"))
	}
	plainEnv := contract.WithoutVars(contract.CleanEnv(), append(quietNames(), "DRYDOCK_FAKE_AGENT", "DRYDOCK_FAKE_SCRIPT", mcpServerEnv)...)
	plainTools, plainSkills, plainCommands := look(plainEnv, streamArgs("--model", "haiku")...)
	quietTools, _, _ := look(claude.Quiet(plainEnv), streamArgs("--model", "haiku")...)
	tools, skills, commands := look(childEnv(), drydockClaude().Args(agents.RunSpec{Model: "haiku"})...)

	if len(skills) > 0 || len(commands) > 0 {
		t.Errorf("--disable-slash-commands no longer keeps the user's skills and commands out of a session: %d skills (%s), %d commands",
			len(skills), strings.Join(skills[:min(len(skills), 5)], ", "), len(commands))
	}
	for _, tool := range tools {
		if slices.Contains(claude.UnattendedDenied, tool) {
			t.Errorf("%s is offered although Drydock denies it", tool)
		}
	}
	// the variables whose effect shows in the tool list: when the CLI has the tools, the variable must remove them
	for name, removes := range map[string][]string{"CLAUDE_CODE_DISABLE_CRON": {"CronCreate", "CronDelete", "CronList"}, "CLAUDE_CODE_DISABLE_WORKFLOWS": {"Workflow"}} {
		for _, tool := range removes {
			switch {
			case !slices.Contains(plainTools, tool):
				t.Logf("%sthis CLI does not offer %s, so what %s does cannot be seen", contract.NotePrefix, tool, name)
			case slices.Contains(quietTools, tool):
				t.Errorf("%s=1 no longer removes %s: the variable was renamed or dropped (see claude.QuietEnv)", name, tool)
			}
		}
	}
	var unreviewed []string
	for _, tool := range tools {
		if !strings.HasPrefix(tool, "mcp__") && !slices.Contains(reviewedTools, tool) {
			unreviewed = append(unreviewed, tool)
		}
	}
	if len(unreviewed) > 0 {
		t.Logf("%sDrydock sessions are offered tools nobody reviewed: %s. Deny them (claude.UnattendedDenied) or list them in reviewedTools",
			contract.WarningPrefix, strings.Join(unreviewed, ", "))
	}
	t.Logf("%sa Drydock session is offered %d tools (%s), no skills and no commands; the CLI's defaults here: %d tools, %d skills, %d commands",
		contract.NotePrefix, len(tools), strings.Join(tools, ", "), len(plainTools), len(plainSkills), len(plainCommands))
}

// CLAUDE-TOKENS-01 (paid): what the first request of a session Drydock starts sends, against the CLI's defaults, and that a
// second session in another worktree of the same repository reads the shared part from the prompt cache. Measured with
// 2.1.296 on haiku with ten plugins installed: 9,846 tokens (27,463 before the quiet setup); the second worktree wrote 2,673
// new tokens and read the rest from the cache.
func TestClaudeSessionTokensAndTheCacheAcrossWorktrees(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.03)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)
	writeFile(t, filepath.Join(repo, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(repo, "CLAUDE.md"), "# Demo\n\nRun go test ./... before you finish.\n")
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=contract@drydock.invalid", "-c", "user.name=contract"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	git("add", ".")
	git("commit", "-qm", "start")
	wt := []string{filepath.Join(filepath.Dir(repo), "task-one"), filepath.Join(filepath.Dir(repo), "task-two")}
	git("worktree", "add", "-q", "-b", "task-one", wt[0])
	git("worktree", "add", "-q", "-b", "task-two", wt[1])
	type sent struct{ input, written, read float64 }
	first := func(dir string, env []string, args ...string) sent {
		p := startClaude(t, "CLAUDE-TOKENS-01", exe, dir, env, append(args, "--no-session-persistence", "--max-budget-usd", usd(maxUSD/3))...)
		tr := p.turn("Reply with the single word OK.")
		p.close()
		u := tr.result.obj("usage")
		in, _ := u.num("input_tokens")
		w, _ := u.num("cache_creation_input_tokens")
		r, _ := u.num("cache_read_input_tokens")
		return sent{in + w + r, w, r}
	}
	plainEnv := contract.WithoutVars(childEnv(), quietNames()...)
	plain := first(wt[0], plainEnv, streamArgs("--model", "haiku", "--setting-sources", "user", "--disallowedTools", claude.PluginToolsDenied)...)
	args := drydockClaude().Args(agents.RunSpec{Model: "haiku"})
	one := first(wt[0], childEnv(), args...)
	two := first(wt[1], childEnv(), args...)
	if one.input == 0 || plain.input == 0 {
		t.Fatalf("no usage reported: plain %+v, Drydock %+v", plain, one)
	}
	if one.input > plain.input {
		t.Errorf("a Drydock session's first request (%.0f tokens) is larger than the CLI's defaults (%.0f)", one.input, plain.input)
	}
	if two.read == 0 {
		t.Errorf("a second session in another worktree read nothing from the prompt cache (wrote %.0f of %.0f tokens): every task now pays its whole context again",
			two.written, two.input)
	}
	if one.input > 20000 {
		t.Logf("%sa Drydock session's first request grew to %.0f tokens (9,846 measured with 2.1.296): see what this version adds", contract.WarningPrefix, one.input)
	}
	t.Logf("%sfirst request: Drydock %.0f tokens, the CLI's defaults with the user's settings %.0f; a second worktree wrote %.0f and read %.0f from the cache",
		contract.NotePrefix, one.input, plain.input, two.written, two.read)
}
