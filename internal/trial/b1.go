package trial

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/proc"
)

// B1 is plain Claude Code (docs/evals.md 5, "one premium agent, no team"): one headless session in the task's repository with
// the task as its prompt, file edits accepted and every Bash command allowed on this machine, as a user who runs
// `claude -p` with those permissions gets it. No reviewer, no checks run for it, no sandbox. It gets the same trimmed
// environment as Drydock's agents and, like them, waits for long commands instead of moving them to the background.
type B1 struct {
	Bin        string // default "claude"
	Model      string
	CeilingUSD float64
	Wall       time.Duration
	// Root is the Drydock repository: a tool call that names its hidden checks marks the run.
	Root   string
	Hidden []string
	// ExtraEnv is added to the CLI's environment (the harness's tests pass their stand-in's script this way).
	ExtraEnv []string
}

// Detail describes the arm for the record.
func (b B1) Detail() string {
	return "plain Claude Code: one headless session (claude -p), --permission-mode acceptEdits, --allowedTools Bash, on this machine"
}

// Args are the CLI arguments of a run.
func (b B1) Args(t Task) []string {
	return []string{"-p", t.Title + "\n\n" + t.Prompt, "--model", b.Model, "--output-format", "stream-json", "--verbose",
		"--permission-mode", "acceptEdits", "--allowedTools", "Bash", "--max-budget-usd", strconv.FormatFloat(b.CeilingUSD, 'f', 2, 64),
		"--no-session-persistence"}
}

// Env is the CLI's environment: what Drydock gives its own Claude agents, outside the session that started the harness.
func (b B1) Env(parent []string) []string {
	env := proc.ScrubEnv(claude.WithoutEnclosingSession(parent), proc.BaseEnvNames, []string{"CLAUDE_", "ANTHROPIC_"})
	env = append(env, "CLAUDE_AUTO_BACKGROUND_TASKS=false", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1")
	return append(env, b.ExtraEnv...)
}

// Run runs the session in repo, saving the CLI's stream to transcript, and returns the record's measured part (the caller
// adds the acceptance verdict).
func (b B1) Run(ctx context.Context, t Task, repo string, transcript io.Writer) Record {
	bin := b.Bin
	if bin == "" {
		bin = "claude"
	}
	rec := Record{Arm: ArmB1, ArmDetail: b.Detail(), TaskID: t.ID, Model: b.Model, CeilingUSD: b.CeilingUSD, Sessions: 1}
	ctx, cancel := context.WithTimeout(ctx, b.Wall)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, bin, b.Args(t)...)
	cmd.Dir = repo
	cmd.Env = b.Env(os.Environ())
	cmd.Stdin = nil
	cmd.WaitDelay = 10 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		rec.Outcome, rec.Reason = Failed, err.Error()
		return rec
	}
	if err := cmd.Start(); err != nil {
		rec.Outcome, rec.Reason = Failed, "claude did not start: "+err.Error()
		return rec
	}
	st := ReadStream(io.TeeReader(stdout, transcript), b.Root, b.Hidden)
	err = cmd.Wait()
	rec.WallMs = time.Since(start).Milliseconds()
	st.apply(&rec)
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		rec.Outcome, rec.Reason = Failed, fmt.Sprintf("the wall-clock cap of %s ended it", b.Wall)
	case st.result == nil:
		rec.Outcome, rec.Reason = Failed, "claude ended without a result: "+errText(err, stderr.String())
	case strings.Contains(st.result.Subtype, "budget"):
		rec.Outcome, rec.Reason = Ceiling, "claude stopped at the dollar ceiling ("+st.result.Subtype+")"
	case st.result.IsError || strings.HasPrefix(st.result.Subtype, "error"):
		rec.Outcome, rec.Reason = Failed, "claude reported an error: "+st.result.Subtype+" "+strings.Join(st.result.Errors, "; ")
	}
	return rec
}

func errText(err error, stderr string) string {
	s := strings.TrimSpace(stderr)
	if len(s) > 500 {
		s = s[len(s)-500:]
	}
	if err != nil {
		return strings.TrimSpace(err.Error() + " " + s)
	}
	return s
}

// Stream is what a stream-json transcript of one session says.
type Stream struct {
	result   *streamResult
	models   map[string]bool
	tools    map[string]int
	sawFiles bool
}

type streamResult struct {
	Subtype    string   `json:"subtype"`
	IsError    bool     `json:"is_error"`
	NumTurns   int      `json:"num_turns"`
	TotalCost  *float64 `json:"total_cost_usd"`
	Errors     []string `json:"errors"`
	DurationMs int64    `json:"duration_ms"`
	Usage      *struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	ModelUsage map[string]struct {
		InputTokens              int64 `json:"inputTokens"`
		OutputTokens             int64 `json:"outputTokens"`
		CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
	} `json:"modelUsage"`
}

// ReadStream reads Claude Code's stream-json output: the final result, the models that answered, the tool calls by name, and
// whether a tool call named root's hidden paths.
func ReadStream(r io.Reader, root string, hidden []string) Stream {
	st := Stream{models: map[string]bool{}, tools: map[string]int{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var ev struct {
			Type    string `json:"type"`
			Message *struct {
				Model   string `json:"model"`
				Content []struct {
					Type  string          `json:"type"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			if ev.Message == nil {
				continue
			}
			if ev.Message.Model != "" && ev.Message.Model != "<synthetic>" {
				st.models[ev.Message.Model] = true
			}
			for _, c := range ev.Message.Content {
				if c.Type != "tool_use" {
					continue
				}
				st.tools[c.Name]++
				if namesHidden(string(c.Input), root, hidden) {
					st.sawFiles = true
				}
			}
		case "result":
			var res streamResult
			if json.Unmarshal(line, &res) == nil {
				st.result = &res
			}
		}
	}
	return st
}

func namesHidden(input, root string, hidden []string) bool {
	if root != "" && strings.Contains(input, root) {
		return true
	}
	for _, h := range hidden {
		if h != "" && strings.Contains(input, h) {
			return true
		}
	}
	return false
}

func (st Stream) apply(rec *Record) {
	rec.ModelsUsed = sortedKeys(st.models)
	if len(st.tools) > 0 {
		rec.ToolUses = st.tools
	}
	rec.SawHiddenFiles = st.sawFiles
	res := st.result
	if res == nil {
		rec.CallsWithoutCost = 1
		return
	}
	rec.ModelTurns = res.NumTurns
	if res.TotalCost != nil {
		rec.CostUSD = *res.TotalCost
	} else {
		rec.CallsWithoutCost = 1
	}
	for model, mu := range res.ModelUsage {
		rec.Tokens.Input += mu.InputTokens
		rec.Tokens.Output += mu.OutputTokens
		rec.Tokens.CacheRead += mu.CacheReadInputTokens
		rec.Tokens.CacheWrite += mu.CacheCreationInputTokens
		if !st.models[model] {
			rec.ModelsUsed = append(rec.ModelsUsed, model)
		}
	}
	if len(res.ModelUsage) == 0 && res.Usage != nil {
		rec.Tokens = Tokens{Input: res.Usage.InputTokens, Output: res.Usage.OutputTokens, CacheRead: res.Usage.CacheReadInputTokens, CacheWrite: res.Usage.CacheCreationInputTokens}
	}
	sort.Strings(rec.ModelsUsed)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CLIVersion is `claude --version`.
func CLIVersion(ctx context.Context, bin string) (string, error) {
	if bin == "" {
		bin = "claude"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", errors.New("claude --version: " + err.Error())
	}
	return strings.TrimSpace(string(out)), nil
}
