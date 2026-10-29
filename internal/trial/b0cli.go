package trial

// B0 with a CLI other than Claude Code, and on a provider other than the CLI's own (decision P121). The broadcast rules do not
// change: each turn is a new session given the whole room, with no router, cheap tier, handoff, cache or watchdog. What changes
// is how a turn runs. Claude Code keeps its own path (`claude -p`, stream-json, --max-budget-usd); any other CLI is driven
// through the adapter Drydock builds from its manifest (an ACP agent such as Codex, Gemini CLI, OpenCode, Goose or Cline, or a
// headless one), which normalizes what the CLI prints into text, tool calls, tool results and usage. A provider is given as its
// base URL and the name of the environment variable that holds its key; only CLIs Drydock knows how to point at another
// endpoint take one (Claude Code at an Anthropic-compatible endpoint, OpenCode at an OpenAI-compatible one).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// CLIClaude is the default CLI of the broadcast team.
const CLIClaude = "claude"

// B0Provider is the provider a broadcast team runs on instead of its CLI's own sign-in: a cheap one, typically.
type B0Provider struct {
	// ID names the provider in the record and, for OpenCode, in its configuration (letters, digits, dots, dashes).
	ID string `json:"id"`
	// BaseURL is the provider's API endpoint: Anthropic-compatible for Claude Code, OpenAI-compatible (…/v1) for OpenCode.
	BaseURL string `json:"baseUrl"`
	// KeyEnv names the environment variable of the harness that holds the API key. The key is passed to the CLI's environment
	// and is never written to the record or the transcript (they are redacted, and the record only names the variable).
	KeyEnv string `json:"keyEnv"`
	// InputPerMTok and OutputPerMTok are the provider's prices in USD per million tokens. Given, a turn's cost is estimated from
	// its tokens (cached tokens at the input price, so an upper bound) instead of taken from the CLI, whose own figure is at
	// its vendor's prices.
	InputPerMTok  float64 `json:"inputPerMTok,omitempty"`
	OutputPerMTok float64 `json:"outputPerMTok,omitempty"`
}

var providerID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,39}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func (p *B0Provider) priced() bool { return p != nil && (p.InputPerMTok > 0 || p.OutputPerMTok > 0) }

// estimate is a turn's cost at the provider's prices.
func (p *B0Provider) estimate(t Tokens) float64 {
	return float64(t.Input+t.CacheRead+t.CacheWrite)*p.InputPerMTok/1e6 + float64(t.Output)*p.OutputPerMTok/1e6
}

func (b B0) cli() string {
	if b.CLI == "" {
		return CLIClaude
	}
	return b.CLI
}

// Check says why the team cannot run as configured, or nil: a CLI other than Claude Code needs its adapter, and a provider is
// taken only by a CLI that can be pointed at one, with an https endpoint (or one on this computer) and a key in the environment.
func (b B0) Check() error {
	if b.cli() != CLIClaude && b.Adapter == nil {
		return fmt.Errorf("B0 with %s needs its adapter (the CLI's manifest)", b.cli())
	}
	p := b.Provider
	if p == nil {
		return nil
	}
	if !providerID.MatchString(p.ID) {
		return fmt.Errorf("the provider's id %q must be letters, digits, dots and dashes", p.ID)
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) || u.User != nil {
		return fmt.Errorf("the provider's base URL must be https (or http on this computer), without credentials: %q", p.BaseURL)
	}
	if !envName.MatchString(p.KeyEnv) {
		return fmt.Errorf("name the environment variable that holds the provider's key, not %q", p.KeyEnv)
	}
	if os.Getenv(p.KeyEnv) == "" {
		return fmt.Errorf("the environment variable %s that should hold the provider's key is empty", p.KeyEnv)
	}
	if p.InputPerMTok < 0 || p.OutputPerMTok < 0 {
		return errors.New("prices cannot be negative")
	}
	switch b.cli() {
	case CLIClaude, "opencode":
		return nil
	}
	return fmt.Errorf("B0 does not know how to point %s at another provider (Claude Code and OpenCode only); run it on its own sign-in", b.cli())
}

// ProviderEnv is the environment that points the team's CLI at the provider (none without one). For Claude Code: its documented
// gateway variables, the base URL and the key as a bearer token, with every model alias set to the run's model so no call
// goes to another one. For OpenCode: an inline configuration (OPENCODE_CONFIG_CONTENT, which replaces the manifest's, so its
// compaction setting is kept) with the provider as an OpenAI-compatible one whose key OpenCode reads from KeyEnv, and the key
// variable itself.
func (b B0) ProviderEnv() []string {
	p := b.Provider
	if p == nil {
		return nil
	}
	key := os.Getenv(p.KeyEnv)
	switch b.cli() {
	case CLIClaude:
		return []string{"ANTHROPIC_BASE_URL=" + p.BaseURL, "ANTHROPIC_AUTH_TOKEN=" + key, "ANTHROPIC_API_KEY=",
			"ANTHROPIC_MODEL=" + b.Model, "ANTHROPIC_DEFAULT_OPUS_MODEL=" + b.Model, "ANTHROPIC_DEFAULT_SONNET_MODEL=" + b.Model,
			"ANTHROPIC_DEFAULT_HAIKU_MODEL=" + b.Model, "ANTHROPIC_SMALL_FAST_MODEL=" + b.Model}
	case "opencode":
		cfg, _ := json.Marshal(map[string]any{
			"compaction": map[string]any{"auto": true},
			"provider": map[string]any{p.ID: map[string]any{"npm": "@ai-sdk/openai-compatible", "name": p.ID,
				"options": map[string]any{"baseURL": p.BaseURL, "apiKey": "{env:" + p.KeyEnv + "}"}, "models": map[string]any{b.Model: map[string]any{}}}},
			"model": p.ID + "/" + b.Model,
		})
		return []string{"OPENCODE_CONFIG_CONTENT=" + string(cfg), p.KeyEnv + "=" + key}
	}
	return nil
}

// sessionModel is the model a CLI other than Claude Code is started with: OpenCode names a provider's model provider/model.
func (b B0) sessionModel() string {
	if b.Provider != nil && b.cli() == "opencode" {
		return b.Provider.ID + "/" + b.Model
	}
	return b.Model
}

// b0Turn is what one agent's turn left: its stream for the transcript, what it adds to the room, its figures, and, when the run
// must stop, why.
type b0Turn struct {
	stream  []byte
	entries []RoomEntry
	rec     Record
	outcome string // Failed or Ceiling when stop is set
	stop    string // "" (the turn ended normally), error, ceiling
	reason  string
}

// runTurn runs one agent's turn with the team's CLI and, when the provider has prices, puts the turn's cost at them.
func (b B0) runTurn(ctx context.Context, repo string, round int, role B0Role, prompt string, remainingUSD float64) b0Turn {
	var tr b0Turn
	if b.cli() == CLIClaude {
		tr = b.claudeTurn(ctx, repo, round, role, prompt, remainingUSD)
	} else {
		tr = b.adapterTurn(ctx, repo, round, role, prompt, remainingUSD)
	}
	if b.Provider.priced() {
		tr.rec.CostUSD, tr.rec.CallsWithoutCost = b.Provider.estimate(tr.rec.Tokens), 0
	}
	return tr
}

// claudeTurn is Claude Code's turn: one `claude -p` session whose stream-json the harness reads itself.
func (b B0) claudeTurn(ctx context.Context, repo string, round int, role B0Role, prompt string, remainingUSD float64) b0Turn {
	out, err := b.turn(ctx, repo, prompt, remainingUSD)
	st := ReadStream(bytes.NewReader(out), b.Root, b.Hidden)
	tr := b0Turn{stream: out, entries: roomEntries(out, round, role.Name)}
	st.apply(&tr.rec)
	switch res := st.result; {
	case ctx.Err() == context.DeadlineExceeded: // the run's wall clock: Run says so
	case res == nil:
		tr.outcome, tr.stop, tr.reason = Failed, "error", fmt.Sprintf("the %s's session ended without a result: %s", role.Name, errText(err, ""))
	case strings.Contains(res.Subtype, "budget"):
		tr.outcome, tr.stop, tr.reason = Ceiling, "ceiling", "claude stopped at the dollar ceiling ("+res.Subtype+")"
	case res.IsError || strings.HasPrefix(res.Subtype, "error"):
		tr.outcome, tr.stop, tr.reason = Failed, "error", fmt.Sprintf("the %s's session reported an error: %s %s", role.Name, res.Subtype, strings.Join(res.Errors, "; "))
	}
	return tr
}

// adapterTurn is any other CLI's turn: a new session through its adapter, the prompt as one message, every chunk read until the
// turn is done, then the session stopped. The CLI's permission requests are all allowed (the team may do anything in its
// repository, as B1's Claude Code may). There is no per-session budget flag, so the dollar ceiling is checked against each
// usage report as it comes and ends the turn once reached.
func (b B0) adapterTurn(ctx context.Context, repo string, round int, role B0Role, prompt string, remainingUSD float64) b0Turn {
	tr := b0Turn{}
	fail := func(f string, a ...any) b0Turn {
		tr.outcome, tr.stop, tr.reason = Failed, "error", fmt.Sprintf(f, a...)
		return tr
	}
	spec := agents.RunSpec{AgentID: "b0-" + role.Name, Role: role.Name, Cwd: repo, Model: b.sessionModel(), Env: append(b.ProviderEnv(), b.ExtraEnv...),
		AskPermission: func(context.Context, agents.ToolAsk) (bool, string) { return true, "" }}
	s, err := b.Adapter.Start(ctx, proc.New(), spec)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return tr
		}
		return fail("the %s's %s session did not start: %v", role.Name, b.cli(), err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Stop(sctx)
	}()
	if err := s.Send(ctx, prompt); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return tr
		}
		return fail("the %s's %s session did not take the prompt: %v", role.Name, b.cli(), err)
	}
	var stream bytes.Buffer
	var text strings.Builder
	models := map[string]bool{}
	tools := map[string]int{}
	flush := func(full string) {
		if full == "" {
			full = text.String()
		}
		text.Reset()
		if strings.TrimSpace(full) != "" {
			tr.entries = append(tr.entries, RoomEntry{Round: round, Agent: role.Name, Kind: "said", Text: full})
		}
	}
	finish := func() b0Turn {
		tr.stream = stream.Bytes()
		tr.rec.ModelsUsed = sortedKeys(models)
		if len(tools) > 0 {
			tr.rec.ToolUses = tools
		}
		return tr
	}
	reported := false
	lastErr := ""
	for {
		var c workflow.Chunk
		var ok bool
		select {
		case c, ok = <-s.Output():
		case <-ctx.Done():
			flush("")
			tr = finish()
			if ctx.Err() == context.DeadlineExceeded {
				return tr // the run's wall clock: Run says so
			}
			return fail("the %s's %s turn was stopped: %v", role.Name, b.cli(), ctx.Err())
		}
		if !ok {
			flush("")
			if !reported {
				tr.rec.CallsWithoutCost = 1
			}
			tr = finish()
			return fail("the %s's %s session ended before its turn did: %s", role.Name, b.cli(), errText(nil, s.Stderr()))
		}
		line, _ := json.Marshal(map[string]any{"type": "b0_chunk", "cli": b.cli(), "agent": role.Name, "round": round, "kind": c.Kind, "data": c.Data})
		stream.Write(append(line, '\n'))
		switch c.Kind {
		case workflow.ChunkText:
			var d workflow.TextDelta
			if c.Decode(&d) == nil {
				text.WriteString(d.Text)
			}
		case workflow.ChunkMessageEnd:
			var d workflow.TextDelta
			_ = c.Decode(&d)
			flush(d.Text)
		case workflow.ChunkToolCall:
			flush("")
			var call workflow.ToolCall
			if c.Decode(&call) == nil {
				tools[call.Name]++
				if namesHidden(string(call.Args), b.Root, b.Hidden) {
					tr.rec.SawHiddenFiles = true
				}
				tr.entries = append(tr.entries, RoomEntry{Round: round, Agent: role.Name, Kind: "tool_call", Tool: call.Name, Text: string(call.Args)})
			}
		case workflow.ChunkToolResult:
			flush("")
			var res workflow.ToolResult
			if c.Decode(&res) == nil {
				tr.entries = append(tr.entries, RoomEntry{Round: round, Agent: role.Name, Kind: "tool_output", Tool: res.Name, Text: res.Output})
			}
		case workflow.ChunkSession:
			var sc workflow.SessionChunk
			if c.Decode(&sc) == nil && sc.Model != "" {
				models[sc.Model] = true
			}
		case workflow.ChunkUsage:
			var u workflow.UsageChunk
			if c.Decode(&u) != nil {
				continue
			}
			if u.Model != "" {
				models[u.Model] = true
			}
			tr.rec.Tokens.Input += u.InputTokens
			tr.rec.Tokens.Output += u.OutputTokens
			tr.rec.Tokens.CacheRead += u.CachedTokens
			tr.rec.Tokens.CacheWrite += u.CacheWriteTokens
			if u.CostUSD > 0 {
				tr.rec.CostUSD += u.CostUSD
				reported = true
			}
			cost := tr.rec.CostUSD
			if b.Provider.priced() {
				cost, reported = b.Provider.estimate(tr.rec.Tokens), true
			}
			if cost >= remainingUSD {
				flush("")
				tr.outcome, tr.stop = Ceiling, "ceiling"
				tr.reason = fmt.Sprintf("the team reached the dollar ceiling of $%s during the %s's turn in round %d", strconv.FormatFloat(b.CeilingUSD, 'f', -1, 64), role.Name, round)
				return finish()
			}
		case workflow.ChunkError:
			var e workflow.ErrorChunk
			if c.Decode(&e) != nil {
				continue
			}
			if e.Fatal {
				flush("")
				tr = finish()
				return fail("the %s's %s session reported an error: %s", role.Name, b.cli(), e.Message)
			}
			if e.Code == "" {
				lastErr = e.Message // a turn that then ends with stop reason "error" failed for this reason
			}
		case workflow.ChunkDone:
			flush("")
			if !reported {
				tr.rec.CallsWithoutCost = 1
			}
			var d workflow.DoneChunk
			_ = c.Decode(&d)
			switch d.StopReason {
			case "error":
				tr = finish()
				return fail("the %s's %s session reported an error: %s", role.Name, b.cli(), lastErr)
			case "cancelled", "refusal":
				tr = finish()
				return fail("the %s's %s turn ended with %s", role.Name, b.cli(), d.StopReason)
			}
			return finish()
		}
	}
}
