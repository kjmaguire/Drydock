package trial

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// An init event as the CLI prints it, with the account's details made up: an organization id in the plugin folder, connectors,
// commands and a scratch folder named after the session.
const orgID = "11111111-2222-3333-4444-555555555555"

func initEvent() string {
	return `{"type":"system","subtype":"init","cwd":"/tmp/claude-0/-home-user-Drydock/927585e5-898b-57de-a294-91a4b635e67b/scratchpad/trial-work/saasapi-B1",` +
		`"session_id":"5cc2b097-2d3a-44d2-8298-979177da3dcf","tools":["Bash","Read","Edit","mcp__Slack__slack_send_message","plugin:data:query"],` +
		`"mcp_servers":[{"name":"plugin:productivity:slack","status":"failed"}],"model":"claude-haiku-5-5","permissionMode":"acceptEdits",` +
		`"slash_commands":["deep-research","pdf-viewer:annotate"],"apiKeySource":"none","claude_code_version":"2.1.296","agents":["claude","Explore"],` +
		`"plugins":[{"name":"design","path":"/root/.claude/plugins/synced/` + orgID + `_30e81146-54e9-4e71-a3ee-5365a2a9b73c/design","version":"0.2.0"}],` +
		`"skills":["design:ux-copy"],"uuid":"dc73127a-6468-435a-9230-6f2ac1742a47","output_style":"default"}`
}

func TestATranscriptKeepsNothingOfTheAccountInItsInitEvent(t *testing.T) {
	assistant := `{"type":"assistant","message":{"content":[{"type":"text","text":"I will edit the file."}]},"session_id":"5cc2b097-2d3a-44d2-8298-979177da3dcf"}`
	result := `{"type":"result","subtype":"success","total_cost_usd":0.0123,"usage":{"input_tokens":10}}`
	raw := initEvent() + "\n" + assistant + "\n" + result + "\n"
	got := string(RedactTranscript([]byte(raw)))
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 3 || !strings.HasSuffix(got, "\n") {
		t.Fatalf("%d lines:\n%s", len(lines), got)
	}
	if lines[1] != assistant || lines[2] != result {
		t.Fatalf("an event that is not the init event must be unchanged:\n%s\n%s", lines[1], lines[2])
	}
	var init map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &init); err != nil {
		t.Fatal(err)
	}
	if init["type"] != "system" || init["subtype"] != "init" || init["model"] != "claude-haiku-5-5" || init["permissionMode"] != "acceptEdits" ||
		init["claude_code_version"] != "2.1.296" || init["apiKeySource"] != "none" || init["output_style"] != "default" {
		t.Errorf("what a reader needs of the setup is gone: %v", init)
	}
	if tools, _ := init["tools"].([]any); len(tools) != 3 || tools[0] != "Bash" || init["toolsLeftOut"] != float64(2) {
		t.Errorf("tools: %v (left out %v)", init["tools"], init["toolsLeftOut"])
	}
	for _, leak := range []string{orgID, "plugin:", "slack", "Slack", "pdf-viewer", "deep-research", "5cc2b097", "dc73127a", "927585e5", "/root/", "claude-0", "design", "Explore", "mcp_servers", "slash_commands"} {
		if strings.Contains(lines[0], leak) {
			t.Errorf("the init event still holds %q: %s", leak, lines[0])
		}
	}
	if init["redacted"] == nil {
		t.Error("the reader is not told something was left out")
	}
	// again changes nothing
	if again := RedactTranscript([]byte(got)); !bytes.Equal(again, []byte(got)) {
		t.Errorf("not idempotent:\n%s\n%s", got, again)
	}
}

func TestRedactingAMissingFinalNewlineAndOddLinesChangesNothingElse(t *testing.T) {
	raw := "not json at all\n\n" + `{"type":"user","message":"hello"}` // no newline at the end, a blank line, a line that is not JSON
	if got := RedactTranscript([]byte(raw)); string(got) != raw {
		t.Fatalf("%q", got)
	}
	if got := RedactTranscript(nil); len(got) != 0 {
		t.Fatalf("%q", got)
	}
	// a line that merely mentions an init event is not one
	mention := `{"type":"assistant","message":"the system init event says \"init\""}`
	if got := RedactTranscript([]byte(mention + "\n")); string(got) != mention+"\n" {
		t.Fatalf("%q", got)
	}
}

func TestMachinePathsAreMadeGenericWhereverTheyAre(t *testing.T) {
	for in, want := range map[string]string{
		`{"cmd":"ls /tmp/claude-0/-home-user-Drydock/927585e5-898b-57de-a294-91a4b635e67b/scratchpad/trial-work/x"}`:       `{"cmd":"ls <scratchpad>/trial-work/x"}`,
		`{"o":"saved to: /root/.claude/projects/-tmp-claude-0--home-user-Drydock-927585e5/tool-results/abc.txt\n"}`:        `{"o":"saved to: <home>/.claude/<redacted>\n"}`,
		`{"p":"/home/someone/.claude/plugins/synced/` + orgID + `_x/design"}`:                                              `{"p":"<home>/.claude/<redacted>"}`,
		`{"t":"/tmp/claude-0/-home-user-Drydock/927585e5-898b-57de-a294-91a4b635e67b/tasks/b1.output"}`:                    `{"t":"<session-folder>/tasks/b1.output"}`,
		`{"keep":"/tmp/drydock-trial-123/repo and /usr/local/go/bin and C:\\Users\\a\\go and example.com/tally/.claudex"}`: `{"keep":"/tmp/drydock-trial-123/repo and /usr/local/go/bin and C:\\Users\\a\\go and example.com/tally/.claudex"}`,
	} {
		got := string(RedactPaths([]byte(in)))
		if got != want {
			t.Errorf("RedactPaths(%s)\n got  %s\n want %s", in, got, want)
		}
		if !json.Valid([]byte(got)) {
			t.Errorf("no longer valid JSON: %s", got)
		}
	}
}

// Whoever writes a run directory gets the redacted files: the directory is committed.
func TestARunDirectoryIsWrittenRedacted(t *testing.T) {
	out := t.TempDir()
	r := Record{Schema: 1, Trial: "t", Arm: ArmB1, TaskID: "x", Repeat: 1, StartedAt: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)}
	diff := "+ // looks in /root/.claude/ on purpose\n"
	dir, err := r.Write(out, map[string][]byte{
		"transcript.jsonl": []byte(initEvent() + "\n"),
		"events.json":      []byte(`{"p":"/root/.claude/projects/x/y.txt"}`),
		"diff.patch":       []byte(diff),
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if tr := read("transcript.jsonl"); strings.Contains(tr, orgID) || strings.Contains(tr, "slack") || strings.Contains(tr, "/root/") {
		t.Errorf("transcript: %s", tr)
	}
	if ev := read("events.json"); ev != `{"p":"<home>/.claude/<redacted>"}` {
		t.Errorf("events: %s", ev)
	}
	if got := read("diff.patch"); got != diff {
		t.Errorf("an arm's own diff is its work, not the machine's state: %q", got)
	}
}

func TestRedactResultsRewritesEarlierRunsOnceAndOnlyWhatChanges(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a/transcript.jsonl": initEvent() + "\n" + `{"type":"result"}` + "\n",
		"b/transcript.jsonl": `{"type":"result"}` + "\n", // nothing to take out
		"b/events.json":      `{"p":"/tmp/claude-0/-home-user-Drydock/927585e5-898b-57de-a294-91a4b635e67b/scratchpad/x"}`,
		"b/run.json":         `{"note":"/root/.claude/stays: the record is the harness's own"}`,
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	n, err := RedactResults(dir)
	if err != nil || n != 2 {
		t.Fatalf("%d files rewritten, %v", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b", "run.json")); string(b) != files["b/run.json"] {
		t.Errorf("run.json was touched: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b", "events.json")); string(b) != `{"p":"<scratchpad>/x"}` {
		t.Errorf("events.json: %s", b)
	}
	if info, err := os.Stat(filepath.Join(dir, "a", "transcript.jsonl")); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o640) {
		t.Errorf("the file mode must be kept: %v %v", info, err) // Windows keeps no such bits
	}
	if n, err := RedactResults(dir); err != nil || n != 0 {
		t.Fatalf("a second pass rewrote %d files, %v", n, err)
	}
}
