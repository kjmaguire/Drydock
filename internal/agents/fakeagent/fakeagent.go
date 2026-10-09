// Package fakeagent is a scripted stand-in for an agent CLI. It is a real process (see cmd/fakeagent and the tests'
// re-exec) that meets the headless contract by speaking Claude Code's actual stream-json dialect, so it exercises the real
// claude parser and adapter. It exists to test loop-breaking and failure boundaries (watchdog, interrupts, crashes)
// deterministically; it is never presented as a real agent.
package fakeagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Script drives the fake agent. Turn i handles the i-th user message; Default handles any further messages.
type Script struct {
	Rules   []Rule   `json:"rules,omitempty"` // first rule whose Match is a substring of the prompt wins
	Turns   [][]Step `json:"turns"`
	Default []Step   `json:"default,omitempty"`
}

// Rule selects steps by prompt content.
type Rule struct {
	Match string `json:"match"`
	Steps []Step `json:"steps"`
}

// Step is one scripted action. Exactly one of Say, Tool, SleepMS, Block, Exit is used.
type Step struct {
	Say     string          `json:"say,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Result  string          `json:"result,omitempty"`
	IsError bool            `json:"isError,omitempty"`
	Repeat  int             `json:"repeat,omitempty"` // repeat this step n times (0 or 1 = once)
	Vary    bool            `json:"vary,omitempty"`   // add "n": <repeat index> to the tool input so every call differs
	SleepMS int             `json:"sleepMs,omitempty"`
	Block   bool            `json:"block,omitempty"` // wait for an interrupt (or stdin close)
	// WaitFile waits (up to two minutes) until a file exists at this absolute path, so a test can order two scripted agents by what
	// one of them has done instead of by sleeping.
	WaitFile string `json:"waitFile,omitempty"`
	Exit     int    `json:"exit,omitempty"` // exit the process with this code (non-zero) mid-turn
	// Write makes the agent really write a file (relative to its working directory) and report it as a Write tool call.
	Write *WriteSpec `json:"write,omitempty"`
}

// WriteSpec is a scripted file write.
type WriteSpec struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// LoadScript reads the JSON script named by the DRYDOCK_FAKE_SCRIPT environment variable.
func LoadScript() (Script, error) {
	var sc Script
	path := os.Getenv("DRYDOCK_FAKE_SCRIPT")
	if path == "" {
		return sc, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return sc, err
	}
	return sc, json.Unmarshal(b, &sc)
}

// Run emulates `claude` for the given args. It returns the process exit code.
func Run(args []string, in io.Reader, out io.Writer) int {
	for _, a := range args {
		if a == "--version" || a == "-v" {
			fmt.Fprintln(out, "9.9.9 (Fake Claude)")
			return 0
		}
	}
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		fmt.Fprintln(out, `{"loggedIn": true, "authMethod": "fake"}`)
		return 0
	}
	sc, err := LoadScript()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeagent: bad script:", err)
		return 2
	}
	a := &agent{out: out, script: sc, session: "fake-session-0001", interrupt: make(chan struct{}, 1)}
	return a.loop(in)
}

type agent struct {
	mu        sync.Mutex
	out       io.Writer
	script    Script
	session   string
	turn      int
	toolSeq   int
	interrupt chan struct{}
	running   bool
	queue     []string
	closed    bool
	exit      chan int
}

func (a *agent) emit(v any) {
	b, _ := json.Marshal(v)
	a.mu.Lock()
	defer a.mu.Unlock()
	fmt.Fprintf(a.out, "%s\n", b)
	if f, ok := a.out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
}

func (a *agent) loop(in io.Reader) int {
	a.exit = make(chan int, 1)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sc.Scan() {
			var m struct {
				Type    string `json:"type"`
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
				RequestID string `json:"request_id"`
				Request   struct {
					Subtype string `json:"subtype"`
				} `json:"request"`
			}
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			switch m.Type {
			case "user":
				text := ""
				if len(m.Message.Content) > 0 && m.Message.Content[0] == '"' {
					_ = json.Unmarshal(m.Message.Content, &text)
				}
				a.mu.Lock()
				if a.running {
					a.queue = append(a.queue, text)
					a.mu.Unlock()
					continue
				}
				a.running = true
				a.mu.Unlock()
				go a.runTurn(text)
			case "control_request":
				if m.Request.Subtype == "interrupt" {
					select {
					case a.interrupt <- struct{}{}:
					default:
					}
					a.emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m.RequestID, "response": map[string]any{"still_queued": []string{}}}})
				}
			}
		}
	}()
	select {
	case code := <-a.exit:
		return code
	case <-done:
		a.mu.Lock()
		a.closed = true
		a.mu.Unlock()
		// let an in-flight turn finish its output
		time.Sleep(50 * time.Millisecond)
		return 0
	}
}

func (a *agent) steps(prompt string) []Step {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range a.script.Rules {
		if strings.Contains(prompt, r.Match) {
			return r.Steps
		}
	}
	i := a.turn
	a.turn++
	if i < len(a.script.Turns) {
		return a.script.Turns[i]
	}
	if len(a.script.Default) > 0 {
		return a.script.Default
	}
	return []Step{{Say: "done"}}
}

func (a *agent) runTurn(prompt string) {
	if path := os.Getenv("DRYDOCK_FAKE_PROMPT_LOG"); path != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			b, _ := json.Marshal(prompt)
			fmt.Fprintf(f, "%s\n", b)
			f.Close()
		}
	}
	steps := a.steps(prompt)
	a.emit(map[string]any{"type": "system", "subtype": "init", "session_id": a.session, "model": "fake-1", "tools": []string{"Read", "Bash", "Edit"}, "cwd": mustGetwd()})
	interrupted := false
	var lastText string
loop:
	for _, st := range steps {
		n := st.Repeat
		if n < 1 {
			n = 1
		}
		for i := 0; i < n; i++ {
			select {
			case <-a.interrupt:
				interrupted = true
				break loop
			default:
			}
			switch {
			case st.Exit != 0:
				a.exit <- st.Exit
				return
			case st.SleepMS > 0:
				select {
				case <-time.After(time.Duration(st.SleepMS) * time.Millisecond):
				case <-a.interrupt:
					interrupted = true
					break loop
				}
			case st.WaitFile != "":
				deadline := time.After(2 * time.Minute)
			wait:
				for {
					if _, err := os.Stat(st.WaitFile); err == nil {
						break wait
					}
					select {
					case <-time.After(25 * time.Millisecond):
					case <-deadline:
						break wait
					case <-a.interrupt:
						interrupted = true
						break loop
					}
				}
			case st.Block:
				<-a.interrupt
				interrupted = true
				break loop
			case st.Say != "":
				lastText = st.Say
				for _, w := range strings.SplitAfter(st.Say, " ") {
					a.emit(map[string]any{"type": "stream_event", "session_id": a.session, "event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": w}}})
				}
				a.emit(map[string]any{"type": "assistant", "session_id": a.session, "message": map[string]any{"model": "fake-1", "role": "assistant",
					"content": []any{map[string]any{"type": "text", "text": st.Say}}, "usage": a.usage()}})
			case st.Write != nil:
				a.mu.Lock()
				a.toolSeq++
				id := fmt.Sprintf("toolu_fake_%04d", a.toolSeq)
				a.mu.Unlock()
				full := st.Write.Path
				if !filepath.IsAbs(full) {
					full = filepath.Join(mustGetwd(), st.Write.Path)
				}
				werr := os.WriteFile(full, []byte(st.Write.Content), 0o644)
				in, _ := json.Marshal(map[string]any{"file_path": st.Write.Path, "content": st.Write.Content})
				res := "File written: " + st.Write.Path
				if werr != nil {
					res = werr.Error()
				}
				a.emit(map[string]any{"type": "assistant", "session_id": a.session, "message": map[string]any{"model": "fake-1", "role": "assistant",
					"content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Write", "input": json.RawMessage(in)}}, "usage": a.usage()}})
				a.emit(map[string]any{"type": "user", "session_id": a.session, "message": map[string]any{"role": "user",
					"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": res, "is_error": werr != nil}}}})
			case st.Tool != "":
				a.mu.Lock()
				a.toolSeq++
				id := fmt.Sprintf("toolu_fake_%04d", a.toolSeq)
				a.mu.Unlock()
				in := st.Input
				if len(in) == 0 {
					in = json.RawMessage(`{}`)
				}
				if st.Vary {
					var m map[string]any
					if json.Unmarshal(in, &m) == nil {
						m["n"] = i
						in, _ = json.Marshal(m)
					}
				}
				a.emit(map[string]any{"type": "assistant", "session_id": a.session, "message": map[string]any{"model": "fake-1", "role": "assistant",
					"content": []any{map[string]any{"type": "tool_use", "id": id, "name": st.Tool, "input": in}}, "usage": a.usage()}})
				a.emit(map[string]any{"type": "user", "session_id": a.session, "message": map[string]any{"role": "user",
					"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": st.Result, "is_error": st.IsError}}}})
			}
		}
	}
	if interrupted {
		a.emit(map[string]any{"type": "user", "session_id": a.session, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}}}})
		a.emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "session_id": a.session, "total_cost_usd": 0.0001, "usage": map[string]any{"input_tokens": 5, "output_tokens": 2}})
	} else {
		a.emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": lastText, "stop_reason": "end_turn", "session_id": a.session,
			"total_cost_usd": 0.0002, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "cache_read_input_tokens": 100},
			"modelUsage": map[string]any{"fake-1": map[string]any{"inputTokens": 10, "outputTokens": 5, "cacheReadInputTokens": 100, "cacheCreationInputTokens": 0, "costUSD": 0.0002, "contextWindow": 200000}}})
	}
	a.mu.Lock()
	a.running = false
	var next string
	has := len(a.queue) > 0
	if has {
		next = a.queue[0]
		a.queue = a.queue[1:]
		a.running = true
	}
	a.mu.Unlock()
	if has {
		a.runTurn(next)
	}
}

func (a *agent) usage() map[string]any {
	return map[string]any{"input_tokens": 3, "output_tokens": 2, "cache_read_input_tokens": 50, "cache_creation_input_tokens": 0}
}

func mustGetwd() string { d, _ := os.Getwd(); return d }
