// Package fakecustom is a LABELLED TEST DOUBLE of a person's own agent CLI: a prompt-and-exit program that meets the headless
// contract the way the `custom` manifest template describes it (docs/connections.md section 9). Each run reads the prompt (from
// stdin, or after --prompt), prints its turn as JSON lines (`message`, `tool`, `tool_result`, `usage`, `session`, `done`) or,
// with --text, as plain text, and exits 0 (1 when the script fails the turn). It is a real process (tests re-execute their own
// binary); it stands for no real CLI.
package fakecustom

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"
)

// Script drives the stand-in: the first rule whose Match is in the prompt wins, Default answers the rest.
type Script struct {
	Rules   []Rule `json:"rules,omitempty"`
	Default []Step `json:"default,omitempty"`
}

// Rule picks steps by prompt content.
type Rule struct {
	Match string `json:"match"`
	Steps []Step `json:"steps"`
}

// Step is one thing the CLI does.
type Step struct {
	Say  string         `json:"say,omitempty"`
	Tool string         `json:"tool,omitempty"`
	Args map[string]any `json:"args,omitempty"`
	Out  string         `json:"out,omitempty"`
	// SleepMS waits (a long answer to interrupt).
	SleepMS int `json:"sleepMs,omitempty"`
	// Fail prints the message on stderr and exits 1.
	Fail string `json:"fail,omitempty"`
	// Raw prints the line as it is (a line the dialect does not know).
	Raw string `json:"raw,omitempty"`
	// MCP uses a server of the MCP configuration this run was given (--mcp-config <file>, else MCPFile in the working folder)
	// as a real MCP client would, and says what it got as a message ("mcp tools ...", "mcp OK ...", "mcp ERR ...").
	MCP *MCPCall `json:"mcp,omitempty"`
}

// MCPFile is where the stand-in looks for its MCP configuration when no --mcp-config names one: the template's file route.
const MCPFile = ".my-cli/mcp.json"

// Run is the stand-in's main. Environment: DRYDOCK_FAKE_CUSTOM_SCRIPT (a Script as JSON), DRYDOCK_FAKE_CUSTOM_ARGS (a file each
// run appends its argv to, one run per line).
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if f := os.Getenv("DRYDOCK_FAKE_CUSTOM_ARGS"); f != "" {
		if fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(fh, strings.Join(args, " "))
			fh.Close()
		}
	}
	if slices.Equal(args, []string{"--version"}) {
		fmt.Fprintln(stdout, "my-cli 0.4.2 (Drydock test stand-in)")
		return 0
	}
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(stderr, "usage: my-cli run [--json|--text] [--cwd dir] [--session id] [--model m] [--prompt text]")
		return 2
	}
	text := slices.Contains(args, "--text")
	prompt := valueOf(args, "--prompt")
	if !slices.Contains(args, "--prompt") {
		b, _ := io.ReadAll(stdin)
		prompt = string(b)
	}
	if cwd := valueOf(args, "--cwd"); cwd != "" {
		if wd, _ := os.Getwd(); wd != cwd {
			fmt.Fprintf(stderr, "--cwd %s is not the working directory %s\n", cwd, wd)
			return 2
		}
	}
	var sc Script
	if p := os.Getenv("DRYDOCK_FAKE_CUSTOM_SCRIPT"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(b, &sc) != nil {
			fmt.Fprintln(stderr, "stand-in: unreadable script")
			return 2
		}
	}
	steps := sc.Default
	for _, r := range sc.Rules {
		if strings.Contains(prompt, r.Match) {
			steps = r.Steps
			break
		}
	}
	session := valueOf(args, "--session")
	if session == "" {
		session = fmt.Sprintf("my-cli-session-%d", os.Getpid())
	}
	enc := json.NewEncoder(stdout)
	if !text {
		_ = enc.Encode(map[string]any{"type": "session", "id": session, "model": valueOf(args, "--model")})
	}
	for i, st := range steps {
		switch {
		case st.Fail != "":
			fmt.Fprintln(stderr, st.Fail)
			return 1
		case st.SleepMS > 0:
			time.Sleep(time.Duration(st.SleepMS) * time.Millisecond)
		case st.Raw != "":
			fmt.Fprintln(stdout, st.Raw)
		case st.MCP != nil:
			line := callMCP(valueOf(args, "--mcp-config"), st.MCP)
			if text {
				fmt.Fprintln(stdout, line)
			} else {
				_ = enc.Encode(map[string]any{"type": "message", "text": line + "\n"})
			}
		case st.Say != "" && text:
			fmt.Fprintln(stdout, st.Say)
		case st.Say != "":
			for _, w := range strings.SplitAfter(st.Say, " ") {
				if w == "" {
					continue
				}
				_ = enc.Encode(map[string]any{"type": "message", "text": w})
			}
		case st.Tool != "" && !text:
			id := fmt.Sprintf("t%d", i+1)
			_ = enc.Encode(map[string]any{"type": "tool", "id": id, "tool": st.Tool, "input": st.Args})
			_ = enc.Encode(map[string]any{"type": "tool_result", "id": id, "ok": true, "output": st.Out})
		}
	}
	if !text {
		_ = enc.Encode(map[string]any{"type": "usage", "in": 12 + len(prompt)/4, "out": 3})
		_ = enc.Encode(map[string]any{"type": "done", "status": "ok"})
	}
	return 0
}

func valueOf(args []string, f string) string {
	for i, a := range args {
		if a == f && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
