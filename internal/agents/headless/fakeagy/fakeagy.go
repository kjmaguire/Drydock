// Package fakeagy is a LABELLED TEST DOUBLE of the Antigravity CLI (`agy`), written from its documentation, not from a
// captured stream: no agy binary was available (docs/connections.md section 16). It is a real process (tests re-execute their
// own binary) that speaks the headless mode the docs describe: `-p --input-format stream-json --output-format stream-json`,
// a user message per stdin line, `init`, `step_update` and one `result` per turn on stdout, `denied_actions` for what print mode
// refused, and `AGY_ERROR: {...}` on stderr with exit code 3 when it fails. It proves Drydock's side only.
package fakeagy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"
)

// Script drives the stand-in. The first rule whose Match is in the message wins; Default answers the rest.
type Script struct {
	ConversationID string `json:"conversationId,omitempty"`
	Rules          []Rule `json:"rules,omitempty"`
	Default        []Step `json:"default,omitempty"`
}

// Rule picks steps by message content.
type Rule struct {
	Match string `json:"match"`
	Steps []Step `json:"steps"`
}

// Step is one thing the agent does in a turn.
type Step struct {
	Say   string `json:"say,omitempty"`
	Think string `json:"think,omitempty"`
	Tool  *Tool  `json:"tool,omitempty"`
	// Deny is a tool action print mode refused (reported in the result's denied_actions).
	Deny *Tool `json:"deny,omitempty"`
	// Fail ends the process the way agy fails: AGY_ERROR on stderr, exit 3.
	Fail  string    `json:"fail,omitempty"`
	Usage *[3]int64 `json:"usage,omitempty"` // input, output, cached tokens of the step
	// Unknown prints an event of a type agy's docs do not name (drift).
	Unknown string `json:"unknown,omitempty"`
}

// Tool is a tool use.
type Tool struct {
	Name   string         `json:"name"`
	Args   map[string]any `json:"args,omitempty"`
	Output string         `json:"output,omitempty"`
}

// Run is the stand-in's main. Environment: DRYDOCK_FAKE_AGY_SCRIPT (a Script as JSON), DRYDOCK_FAKE_AGY_ARGS (a file the argv
// is written to, one argument per line, and next to it <file>.env with the environment).
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if f := os.Getenv("DRYDOCK_FAKE_AGY_ARGS"); f != "" {
		_ = os.WriteFile(f, []byte(strings.Join(args, "\n")+"\n"), 0o644)
		_ = os.WriteFile(f+".env", []byte(strings.Join(os.Environ(), "\n")+"\n"), 0o644)
	}
	switch {
	case slices.Equal(args, []string{"--version"}):
		fmt.Fprintln(stdout, "agy 1.3.1 (Drydock test stand-in)")
		return 0
	case len(args) >= 2 && args[0] == "-p" && args[1] == "/usage":
		fmt.Fprintln(stdout, "Quota resets 2026-11-01T00:00:00Z")
		return 0
	}
	for _, want := range [][2]string{{"--input-format", "stream-json"}, {"--output-format", "stream-json"}} {
		if !hasPair(args, want[0], want[1]) {
			fmt.Fprintf(stderr, "agy stand-in: %s %s is required\n", want[0], want[1])
			return 2
		}
	}
	if !slices.Contains(args, "-p") || !hasFlag(args, "--mode") {
		fmt.Fprintln(stderr, "agy stand-in: -p and --mode are required")
		return 2
	}
	var sc Script
	if p := os.Getenv("DRYDOCK_FAKE_AGY_SCRIPT"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(b, &sc) != nil {
			fmt.Fprintln(stderr, "agy stand-in: unreadable script")
			return 2
		}
	}
	if sc.ConversationID == "" {
		sc.ConversationID = "agy-conv-0001"
	}
	mode := valueOf(args, "--mode")
	enc := json.NewEncoder(stdout)
	in := bufio.NewScanner(stdin)
	in.Buffer(make([]byte, 1<<20), 16<<20)
	started := false
	for in.Scan() {
		var msg struct {
			Type    string `json:"type"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(in.Bytes(), &msg) != nil || msg.Type != "user" {
			fmt.Fprintln(stderr, `AGY_ERROR: {"message":"stream-json input must be user messages","code":"INVALID_INPUT"}`)
			return 3
		}
		if !started {
			started = true
			_ = enc.Encode(map[string]any{"type": "init", "conversation_id": sc.ConversationID, "init": map[string]any{"cwd": mustWd(), "tools": []string{"read_file", "write_file", "run_shell_command"}, "permission_mode": mode}})
		}
		steps := sc.Default
		for _, r := range sc.Rules {
			if strings.Contains(msg.Message.Content, r.Match) {
				steps = r.Steps
				break
			}
		}
		var said strings.Builder
		var denied []map[string]any
		for i, st := range steps {
			id := fmt.Sprintf("step-%d", i+1)
			switch {
			case st.Fail != "":
				b, _ := json.Marshal(map[string]any{"message": st.Fail, "code": "MODEL_ERROR"})
				fmt.Fprintf(stderr, "AGY_ERROR: %s\n", b)
				return 3
			case st.Say != "":
				for _, w := range strings.SplitAfter(st.Say, " ") {
					if w == "" {
						continue
					}
					_ = enc.Encode(map[string]any{"type": "step_update", "step_id": id, "step_type": "text", "state": "running", "text_delta": w})
				}
				said.WriteString(st.Say)
			case st.Think != "":
				_ = enc.Encode(map[string]any{"type": "step_update", "step_id": id, "step_type": "thinking", "state": "running", "text_delta": st.Think})
			case st.Tool != nil:
				_ = enc.Encode(map[string]any{"type": "step_update", "step_id": id, "step_type": "tool_call", "state": "running", "tool": map[string]any{"id": id, "name": st.Tool.Name, "args": st.Tool.Args}})
				time.Sleep(5 * time.Millisecond)
				_ = enc.Encode(map[string]any{"type": "step_update", "step_id": id, "step_type": "tool_call", "state": "done", "duration": 5, "tool": map[string]any{"id": id, "name": st.Tool.Name, "output": st.Tool.Output}})
			case st.Deny != nil:
				denied = append(denied, map[string]any{"tool": st.Deny.Name, "args": st.Deny.Args})
			case st.Unknown != "":
				_ = enc.Encode(map[string]any{"type": st.Unknown})
			}
			if st.Usage != nil {
				_ = enc.Encode(map[string]any{"type": "step_update", "step_id": id, "step_type": "usage", "state": "done", "usage": map[string]any{"input_tokens": st.Usage[0], "output_tokens": st.Usage[1], "cached_tokens": st.Usage[2]}})
			}
		}
		res := map[string]any{"type": "result", "status": "SUCCESS", "conversation_id": sc.ConversationID, "response": said.String()}
		if len(denied) > 0 {
			res["denied_actions"] = denied
		}
		_ = enc.Encode(res)
	}
	return 0
}

func hasFlag(args []string, f string) bool { return slices.Contains(args, f) }

func hasPair(args []string, f, v string) bool { return valueOf(args, f) == v }

func valueOf(args []string, f string) string {
	for i, a := range args {
		if a == f && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, f+"="); ok {
			return v
		}
	}
	return ""
}

func mustWd() string { wd, _ := os.Getwd(); return wd }
