package contract_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/mcp/mcptestserver"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// mcpServerEnv makes the test binary a real stdio MCP server when a contract check points claude at it: "1" is
// internal/mcp/mcptestserver (a labelled test fixture with the tools echo, envlen and post_note), "slow" is slowMCPServer.
const mcpServerEnv = "DRYDOCK_CONTRACT_MCP_SERVER"

func TestMain(m *testing.M) {
	switch os.Getenv(mcpServerEnv) {
	case "1":
		os.Exit(mcptestserver.Run())
	case "slow":
		os.Exit(slowMCPServer())
	}
	os.Exit(m.Run())
}

// slowMCPServer is a real stdio MCP server, a labelled test fixture: its one tool, wait_then_mark, waits for
// DRYDOCK_CONTRACT_MCP_WAIT (a Go duration) and then returns DRYDOCK_CONTRACT_MCP_MARKER, like a long sandboxed command.
func slowMCPServer() int {
	wait, err := time.ParseDuration(os.Getenv("DRYDOCK_CONTRACT_MCP_WAIT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "slow mcp server:", err)
		return 2
	}
	marker := os.Getenv("DRYDOCK_CONTRACT_MCP_MARKER")
	srv := sdk.NewServer(&sdk.Implementation{Name: "drydock-contract-slow", Version: "1"}, nil)
	type noInput struct{}
	sdk.AddTool(srv, &sdk.Tool{Name: "wait_then_mark", Description: "Runs a long job: waits a few minutes, then returns its completion marker."},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ noInput) (*sdk.CallToolResult, any, error) {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "job finished after " + wait.String() + ": " + marker}}}, nil, nil
		})
	if err := srv.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "slow mcp server:", err)
		return 1
	}
	return 0
}

// childEnv is the environment of a claude a check starts: the process environment without the enclosing Claude Code session
// (see contract.EnclosingSessionEnv) or the repository's fake-agent switches, plus extra.
func childEnv(extra ...string) []string {
	env := contract.WithoutVars(contract.CleanEnv(), "DRYDOCK_FAKE_AGENT", "DRYDOCK_FAKE_SCRIPT", mcpServerEnv)
	return append(env, extra...)
}

// drydockClaude is the Claude adapter configured as the app builds it (the manifest registry turns on UserSettingsOnly with
// the scrubbed environment), so a check that uses its argv runs claude the way Drydock does.
func drydockClaude() *claude.Adapter { return &claude.Adapter{UserSettingsOnly: true} }

// installedClaude skips unless claude is installed. It is enough for the free checks, which never send a prompt.
func installedClaude(t *testing.T) string {
	t.Helper()
	if os.Getenv(contract.EnvNoRealAgents) == "1" {
		t.Skip(contract.EnvNoRealAgents + "=1")
	}
	exe, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed here")
	}
	return exe
}

var (
	loginOnce sync.Once
	loginErr  string
)

// paidClaude skips unless the paid checks are switched on (DRYDOCK_REAL_CLAUDE=1, which the runner sets), claude is installed
// and signed in, and the run's budget still has est dollars. It returns the executable and the cap for this one process
// (--max-budget-usd), so no single check can spend more than what is left.
func paidClaude(t *testing.T, est float64) (exe string, maxUSD float64) {
	t.Helper()
	if os.Getenv(contract.EnvRealClaude) != "1" {
		t.Skip("set " + contract.EnvRealClaude + "=1 (or run cmd/drydock-contract) for the paid checks against the real claude")
	}
	exe = installedClaude(t)
	loginOnce.Do(func() {
		cmd := exec.Command(exe, "auth", "status")
		cmd.Env = childEnv()
		out, err := cmd.Output()
		var st struct {
			LoggedIn bool `json:"loggedIn"`
		}
		switch {
		case err != nil:
			loginErr = "claude auth status: " + err.Error()
		case json.Unmarshal(bytes.TrimSpace(out), &st) != nil || !st.LoggedIn:
			loginErr = "claude is not signed in here"
		}
	})
	if loginErr != "" {
		t.Skip(loginErr)
	}
	return exe, budgetFor(t, est)
}

// budgetFor skips when the run's budget cannot cover est more dollars, and returns the cap for one process.
func budgetFor(t *testing.T, est float64) float64 {
	t.Helper()
	const perProcess = 0.50 // a haiku turn costs a fraction of a cent; this only stops a runaway
	budget, ok, err := contract.Budget()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return perProcess
	}
	spent, err := contract.LedgerTotal(os.Getenv(contract.EnvLedger))
	if err != nil {
		t.Fatal(err)
	}
	left := budget - spent
	if left < est {
		t.Skipf("budget: $%.4f of $%.2f is spent and this check needs about $%.2f", spent, budget, est)
	}
	return min(left, perProcess)
}

// recordSpend writes what a claude process reported spending to the ledger and the test log.
func recordSpend(t *testing.T, id string, usd float64) {
	t.Helper()
	if usd <= 0 {
		return
	}
	t.Logf("%s%s $%.6f", contract.SpendPrefix, id, usd)
	if err := contract.AppendSpend(os.Getenv(contract.EnvLedger), contract.Spend{Test: t.Name(), Contract: id, USD: usd}); err != nil {
		t.Errorf("ledger: %v", err)
	}
}

func usd(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

// randomWord is a random marker the model cannot know unless the check gave it to it.
func randomWord(prefix string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s-%06d", prefix, n.Int64()+100000)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// gitInit makes dir a repository when git is installed (Claude Code treats a repository's folders differently).
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// startOnly runs claude with exactly args and an empty stdin. Started with stream-json input (streamArgs, or the adapter's
// argv) it parses its arguments, starts and exits without a model turn, so a check of flags and values costs nothing.
func startOnly(t *testing.T, exe, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(exe, args...)
	cmd.Dir, cmd.Env, cmd.Stdin = dir, childEnv(), strings.NewReader("")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		code = 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("claude %s: %v", strings.Join(args, " "), err)
		}
	case <-time.After(90 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("claude %s did not exit with an empty input within 90s", strings.Join(args, " "))
	}
	return code, out.String(), errb.String()
}

// streamArgs are the flags of a stream-json session, as the adapter passes them, followed by extra.
func streamArgs(extra ...string) []string {
	return append([]string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}, extra...)
}

// event is one stream-json line, decoded loosely so a check can look at exactly the fields it is about.
type event map[string]any

func (e event) str(k string) string          { s, _ := e[k].(string); return s }
func (e event) obj(k string) event           { m, _ := e[k].(map[string]any); return event(m) }
func (e event) list(k string) []any          { l, _ := e[k].([]any); return l }
func (e event) num(k string) (float64, bool) { f, ok := e[k].(float64); return f, ok }

// blocks are the content blocks of an assistant or user message (a plain string content is one text block).
func (e event) blocks() []event {
	msg := e.obj("message")
	switch c := msg["content"].(type) {
	case string:
		return []event{{"type": "text", "text": c}}
	case []any:
		var out []event
		for _, b := range c {
			if m, ok := b.(map[string]any); ok {
				out = append(out, event(m))
			}
		}
		return out
	}
	return nil
}

// stringList keeps the strings of a JSON array.
func stringList(l []any) []string {
	var out []string
	for _, v := range l {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

type toolUse struct {
	ID, Name string
	Input    event
}

// turn is what one prompt produced, up to and including its result event.
type turn struct {
	events []event
	init   event
	result event
	text   string // result.result
	uses   []toolUse
}

func (tr turn) usesTool() bool { return len(tr.uses) > 0 }

// claudeProc is one claude process a check drives over stream-json, as Drydock's adapter does.
type claudeProc struct {
	t      *testing.T
	id     string // the contract the spend is booked to
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte
	stderr *lockedBuffer
	parser *claude.Parser
	chunks []workflow.Chunk
	init   event
	cost   float64 // the last result's total_cost_usd: what the process has spent so far
	closed bool
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// startClaude starts claude in dir with the given argv (after the executable) and environment. The process is stopped and its
// spend booked to contract id when the test ends, if the test did not close it first.
func startClaude(t *testing.T, id, exe, dir string, env []string, args ...string) *claudeProc {
	t.Helper()
	cmd := exec.Command(exe, args...)
	cmd.Dir, cmd.Env = dir, env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &claudeProc{t: t, id: id, cmd: cmd, stdin: stdin, lines: make(chan []byte, 1024), stderr: &lockedBuffer{}, parser: claude.NewParser()}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			p.lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(p.lines)
	}()
	t.Cleanup(p.close)
	return p
}

// turn sends one user message and reads until its result (at most three minutes).
func (p *claudeProc) turn(prompt string) turn {
	p.t.Helper()
	return p.turnWithin(prompt, 180*time.Second)
}

// turnWithin is turn with a longer wait, for a turn that runs a long tool call.
func (p *claudeProc) turnWithin(prompt string, limit time.Duration) turn {
	p.t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}})
	if _, err := p.stdin.Write(append(b, '\n')); err != nil {
		p.t.Fatalf("writing the prompt: %v (stderr: %s)", err, tail(p.stderr.String()))
	}
	var tr turn
	timeout := time.After(limit)
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				p.t.Fatalf("claude ended before the turn did; stderr: %s", tail(p.stderr.String()))
			}
			cs, _ := p.parser.Parse(line)
			p.chunks = append(p.chunks, cs...)
			var e event
			if json.Unmarshal(line, &e) != nil {
				continue
			}
			tr.events = append(tr.events, e)
			switch e.str("type") {
			case "system":
				if e.str("subtype") == "init" {
					tr.init, p.init = e, e
				}
			case "assistant":
				for _, b := range e.blocks() {
					if b.str("type") == "tool_use" {
						tr.uses = append(tr.uses, toolUse{ID: b.str("id"), Name: b.str("name"), Input: b.obj("input")})
					}
				}
			case "result":
				tr.result, tr.text = e, e.str("result")
				if c, ok := e.num("total_cost_usd"); ok {
					p.cost = c
				}
				return tr
			}
		case <-timeout:
			p.t.Fatalf("no result within %s; stderr: %s", limit, tail(p.stderr.String()))
		}
	}
}

// close ends the process (stdin closed, then killed if it lingers) and books its spend.
func (p *claudeProc) close() {
	if p.closed {
		return
	}
	p.closed = true
	_ = p.stdin.Close()
	drained := make(chan struct{})
	go func() {
		for line := range p.lines {
			cs, _ := p.parser.Parse(line)
			p.chunks = append(p.chunks, cs...)
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(30 * time.Second):
		_ = p.cmd.Process.Kill()
		<-drained
	}
	_ = p.cmd.Wait()
	recordSpend(p.t, p.id, p.cost)
}

// chunksOf returns the parser's chunks of one kind.
func (p *claudeProc) chunksOf(kind string) []workflow.Chunk {
	var out []workflow.Chunk
	for _, c := range p.chunks {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		s = "..." + s[len(s)-600:]
	}
	return s
}

// modelUsageKeys lists the models a result's modelUsage names.
func modelUsageKeys(result event) []string {
	var out []string
	for k := range result.obj("modelUsage") {
		out = append(out, k)
	}
	return out
}
