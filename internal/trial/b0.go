package trial

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/safe"
)

// B0 is the naive broadcast baseline of docs/evals.md section 5 (decision P113): what an AI team costs with none of Drydock's
// controls. A team of agents with the Drydock arm's roles (a worker and a reviewer), all Claude Code on the one model the run
// names, take turns in a free-for-all: each turn, an agent is given the task and everything said and done in the room so far, in
// full (every message, every tool call and every tool output, nothing cut, summarized or reordered for a cache), and may do
// anything it likes in the one shared repository. There is no router, no cheap tier, no handoff (the whole transcript crosses
// every session), no response cache and no watchdog. It stops when, in one round, every agent says it is done; after Rounds
// rounds (the Drydock arm's five iterations); at the wall-clock cap; or at the run's dollar ceiling, which is a recorded outcome
// (`ceiling`), not a discarded run. A prompt that outgrows the model's context window ends the run as `failed` with the CLI's
// reason: that is where a broadcast team runs out.
//
// It is a harness arm, not part of the product, and it runs on this machine with Bash allowed, as B1 does.
//
// Since decision P121 the CLI and the provider are parameters (b0cli.go): the team can be any CLI Drydock drives (CLI and Adapter;
// Claude Code by default) and can run on a provider other than the CLI's own (Provider).
type B0 struct {
	Bin   string // default "claude"
	Model string
	// CLI is the agent CLI every member runs: "claude" (the default) or the id of another CLI's manifest, driven by Adapter.
	CLI     string
	Adapter agents.Adapter
	// Provider, when set, is where the team's model calls go instead of the CLI's own sign-in.
	Provider   *B0Provider
	CeilingUSD float64
	Wall       time.Duration
	// Roles are the team in turn order; default the Drydock arm's worker and reviewer.
	Roles []B0Role
	// Rounds bounds the free-for-all (default 5).
	Rounds int
	// Root is the Drydock repository: a tool call that names its hidden checks marks the run.
	Root   string
	Hidden []string
	// ExtraEnv is added to the CLI's environment (the harness's tests pass their stand-in's script this way).
	ExtraEnv []string
}

// B0Role is one member of the broadcast team.
type B0Role struct {
	Name  string `json:"name"`
	Brief string `json:"brief"`
}

// DoneWord is what an agent ends its reply with when it thinks nothing more is needed from it.
const DoneWord = "DONE"

// DefaultB0Roles are the Drydock arm's roles: the worker who implements and the reviewer who checks it.
var DefaultB0Roles = []B0Role{
	{Name: "worker", Brief: "You implement the task: change the code in this repository and run its tests."},
	{Name: "reviewer", Brief: "You review the worker's change against the task and fix or ask for anything that is wrong or missing."},
}

func (b B0) roles() []B0Role {
	if len(b.Roles) > 0 {
		return b.Roles
	}
	return DefaultB0Roles
}

func (b B0) rounds() int {
	if b.Rounds > 0 {
		return b.Rounds
	}
	return 5
}

// Detail describes the arm for the record.
func (b B0) Detail() string {
	var names []string
	for _, r := range b.roles() {
		names = append(names, r.Name)
	}
	who := "Claude Code"
	if b.cli() != CLIClaude {
		who = b.cli()
	}
	on := ""
	if b.Provider != nil {
		on = " on the provider " + b.Provider.ID
		if b.Provider.priced() {
			on += fmt.Sprintf(" (cost estimated at $%s/$%s per million input/output tokens)", strconv.FormatFloat(b.Provider.InputPerMTok, 'f', -1, 64),
				strconv.FormatFloat(b.Provider.OutputPerMTok, 'f', -1, 64))
		}
	}
	return fmt.Sprintf("naive broadcast: %d %s agents (%s), one model%s, every room message and tool output to every agent each turn, "+
		"free-for-all until all say %s or %d rounds; no router, cheap tier, handoff, cache or watchdog; on this machine with Bash",
		len(names), who, strings.Join(names, ", "), on, DoneWord, b.rounds())
}

// Args are the CLI arguments of one turn; the prompt goes on standard input (it holds the whole room, which can outgrow a
// command line).
func (b B0) Args(remainingUSD float64) []string {
	return []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--model", b.Model,
		"--permission-mode", "acceptEdits", "--allowedTools", "Bash", "--max-budget-usd", strconv.FormatFloat(remainingUSD, 'f', 4, 64),
		"--no-session-persistence"}
}

// Env is Claude Code's environment, the same as B1's, pointed at the provider when there is one.
func (b B0) Env(parent []string) []string {
	return B1{ExtraEnv: append(b.ProviderEnv(), b.ExtraEnv...)}.Env(parent)
}

// RoomEntry is one thing that happened in the room: an agent said something, called a tool, or a tool answered.
type RoomEntry struct {
	Round int    `json:"round"`
	Agent string `json:"agent"`
	Kind  string `json:"kind"` // said | tool_call | tool_output
	Tool  string `json:"tool,omitempty"`
	Text  string `json:"text"`
}

// Prompt is what an agent is given for its turn: who it is, the task, and the whole room in full.
func (b B0) Prompt(t Task, role B0Role, room []RoomEntry) string {
	var names []string
	for _, r := range b.roles() {
		names = append(names, r.Name)
	}
	var s strings.Builder
	fmt.Fprintf(&s, "You are the %s on a team of %d agents (%s) working on one task in this repository. %s\n", role.Name, len(names), strings.Join(names, ", "), role.Brief)
	fmt.Fprintf(&s, "Everything the team has said and done so far is below, in full. Do what your role should do next. "+
		"When you think the task is finished and nothing more is needed from you, end your reply with a line that says only %s.\n\n", DoneWord)
	fmt.Fprintf(&s, "# Task: %s\n\n%s\n\n# The room so far\n", t.Title, t.Prompt)
	if len(room) == 0 {
		s.WriteString("\n(nothing yet: you are first)\n")
	}
	for _, e := range room {
		switch e.Kind {
		case "tool_call":
			fmt.Fprintf(&s, "\n[round %d, %s ran %s]\n%s\n", e.Round, e.Agent, e.Tool, e.Text)
		case "tool_output":
			fmt.Fprintf(&s, "\n[round %d, output of %s's %s]\n%s\n", e.Round, e.Agent, e.Tool, e.Text)
		default:
			fmt.Fprintf(&s, "\n[round %d, %s said]\n%s\n", e.Round, e.Agent, e.Text)
		}
	}
	return s.String()
}

// saidDone reports whether an agent's last words end with the done line.
func saidDone(text string) bool {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1]) == DoneWord
}

// Run runs the team in repo, saving every session's stream to transcript (one after the other), and returns the record's
// measured part (the caller adds the acceptance verdict) and the room as it ended.
func (b B0) Run(ctx context.Context, t Task, repo string, transcript io.Writer) (Record, []RoomEntry) {
	roles := b.roles()
	rec := Record{Arm: ArmB0, ArmDetail: b.Detail(), TaskID: t.ID, Model: b.Model, CeilingUSD: b.CeilingUSD, Agents: len(roles), AgentCLI: b.cli()}
	if b.Provider != nil {
		rec.Provider = b.Provider.ID
		if b.Provider.priced() {
			rec.CostSource = "estimated"
		}
	}
	if err := b.Check(); err != nil {
		rec.Outcome, rec.Reason, rec.Stop = Failed, err.Error(), "error"
		return rec, nil
	}
	if b.cli() != CLIClaude {
		rec.Notes = append(rec.Notes, "the dollar ceiling is checked before each turn and against each usage report: "+b.cli()+" has no per-session budget flag")
	}
	ctx, cancel := context.WithTimeout(ctx, b.Wall)
	defer cancel()
	start := time.Now()
	defer func() { rec.WallMs = time.Since(start).Milliseconds() }()
	var room []RoomEntry
	models := map[string]bool{}
	for round := 1; round <= b.rounds(); round++ {
		rec.Rounds = round
		allDone := true
		for _, role := range roles {
			switch {
			case ctx.Err() != nil:
				rec.Outcome, rec.Reason, rec.Stop = Failed, fmt.Sprintf("the wall-clock cap of %s ended it", b.Wall), "wall"
				return finishB0(rec, models), room
			case rec.CostUSD >= b.CeilingUSD:
				rec.Outcome, rec.Reason, rec.Stop = Ceiling, fmt.Sprintf("the team reached the dollar ceiling of $%s before the %s's turn in round %d", strconv.FormatFloat(b.CeilingUSD, 'f', -1, 64), role.Name, round), "ceiling"
				return finishB0(rec, models), room
			}
			prompt := b.Prompt(t, role, room)
			rec.BroadcastBytes += int64(len(prompt))
			rec.MaxPromptBytes = max(rec.MaxPromptBytes, int64(len(prompt)))
			tr := b.runTurn(ctx, repo, round, role, prompt, b.CeilingUSD-rec.CostUSD)
			_, _ = transcript.Write(tr.stream)
			one := tr.rec
			rec.Sessions++
			rec.CostUSD += one.CostUSD
			rec.CallsWithoutCost += one.CallsWithoutCost
			rec.ModelTurns += one.ModelTurns
			rec.Tokens.Input += one.Tokens.Input
			rec.Tokens.Output += one.Tokens.Output
			rec.Tokens.CacheRead += one.Tokens.CacheRead
			rec.Tokens.CacheWrite += one.Tokens.CacheWrite
			rec.SawHiddenFiles = rec.SawHiddenFiles || one.SawHiddenFiles
			for _, m := range one.ModelsUsed {
				models[m] = true
			}
			for k, v := range one.ToolUses {
				if rec.ToolUses == nil {
					rec.ToolUses = map[string]int{}
				}
				rec.ToolUses[k] += v
			}
			said := tr.entries
			room = append(room, said...)
			switch {
			case ctx.Err() == context.DeadlineExceeded:
				rec.Outcome, rec.Reason, rec.Stop = Failed, fmt.Sprintf("the wall-clock cap of %s ended it", b.Wall), "wall"
				return finishB0(rec, models), room
			case tr.stop != "":
				rec.Outcome, rec.Reason, rec.Stop = tr.outcome, tr.reason, tr.stop
				return finishB0(rec, models), room
			}
			last := ""
			for _, e := range said {
				if e.Kind == "said" {
					last = e.Text
				}
			}
			allDone = allDone && saidDone(last)
		}
		if allDone {
			rec.Stop = "consensus"
			return finishB0(rec, models), room
		}
	}
	rec.Stop = "rounds"
	rec.Notes = append(rec.Notes, fmt.Sprintf("the team was still talking after %d rounds; the hidden check judges what the repository holds", b.rounds()))
	return finishB0(rec, models), room
}

func finishB0(rec Record, models map[string]bool) Record {
	rec.ModelsUsed = sortedKeys(models)
	return rec
}

// turn runs one agent's session: the prompt as one stream-json user message, the stream read until its result, then standard
// input closed so the CLI exits. It returns the session's whole stream.
func (b B0) turn(ctx context.Context, repo, prompt string, remainingUSD float64) ([]byte, error) {
	bin := b.Bin
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, b.Args(remainingUSD)...)
	cmd.Dir = repo
	cmd.Env = b.Env(os.Environ())
	cmd.WaitDelay = 10 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude did not start: %w", err)
	}
	msg, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}})
	// written beside the read below: a prompt larger than the pipe must not wait for a reader that is waiting for us
	safe.Go("trial.B0.turn", func() { _, _ = stdin.Write(append(msg, '\n')) })
	var out bytes.Buffer
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	closed := false
	for sc.Scan() {
		line := sc.Bytes()
		out.Write(line)
		out.WriteByte('\n')
		var ev struct {
			Type string `json:"type"`
		}
		if !closed && json.Unmarshal(line, &ev) == nil && ev.Type == "result" {
			_ = stdin.Close() // the turn is over: no more input, so the CLI exits
			closed = true
		}
	}
	if !closed {
		_ = stdin.Close()
	}
	err = cmd.Wait()
	if err != nil {
		err = fmt.Errorf("%s", errText(err, stderr.String()))
	}
	return out.Bytes(), err
}

// roomEntries is what a session's stream adds to the room: the agent's text, its tool calls with their input, and each tool's
// output, all in full.
func roomEntries(stream []byte, round int, agent string) []RoomEntry {
	var out []RoomEntry
	tools := map[string]string{} // tool_use id -> tool name
	sc := bufio.NewScanner(bytes.NewReader(stream))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Type    string `json:"type"`
			Message *struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Message == nil || (ev.Type != "assistant" && ev.Type != "user") {
			continue
		}
		var blocks []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(ev.Message.Content, &blocks) != nil {
			continue
		}
		for _, c := range blocks {
			switch {
			case ev.Type == "assistant" && c.Type == "text" && strings.TrimSpace(c.Text) != "":
				out = append(out, RoomEntry{Round: round, Agent: agent, Kind: "said", Text: c.Text})
			case ev.Type == "assistant" && c.Type == "tool_use":
				tools[c.ID] = c.Name
				out = append(out, RoomEntry{Round: round, Agent: agent, Kind: "tool_call", Tool: c.Name, Text: string(c.Input)})
			case ev.Type == "user" && c.Type == "tool_result":
				out = append(out, RoomEntry{Round: round, Agent: agent, Kind: "tool_output", Tool: tools[c.ToolUseID], Text: toolText(c.Content)})
			}
		}
	}
	return out
}

// toolText is a tool result's content: a string, or the text of its blocks.
func toolText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}
