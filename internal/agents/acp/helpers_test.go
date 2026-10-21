package acp_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// TestMain turns the test binary into the scripted ACP agent (internal/agents/acp/fakeacp) when asked to: a real separate
// process speaking ACP on the real SDK. It also owns the temp dir the SDK's example agent is built into.
func TestMain(m *testing.M) {
	if os.Getenv("DRYDOCK_FAKE_ACP") == "1" {
		os.Exit(fakeacp.Run(os.Args[1:], os.Stdin, os.Stdout))
	}
	code := m.Run()
	if exampleDir != "" {
		_ = os.RemoveAll(exampleDir)
	}
	os.Exit(code)
}

var (
	exampleOnce sync.Once
	exampleDir  string
	exampleBin  string
	exampleErr  error
)

// exampleAgent builds github.com/coder/acp-go-sdk/example/agent (the SDK's own example agent, a real ACP agent with no
// model behind it) into a temp dir and returns the binary.
func exampleAgent(t *testing.T) string {
	t.Helper()
	exampleOnce.Do(func() {
		exampleDir, exampleErr = os.MkdirTemp("", "acp-example-agent-")
		if exampleErr != nil {
			return
		}
		exampleBin = filepath.Join(exampleDir, "example-agent")
		if goexe := os.Getenv("GOEXE"); goexe != "" {
			exampleBin += goexe
		} else if filepath.Separator == '\\' {
			exampleBin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", exampleBin, "github.com/coder/acp-go-sdk/example/agent")
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=readonly")
		if out, err := cmd.CombinedOutput(); err != nil {
			exampleErr = &buildError{err, string(out)}
		}
	})
	if exampleErr != nil {
		t.Skipf("cannot build the SDK example agent: %v", exampleErr)
	}
	return exampleBin
}

type buildError struct {
	err error
	out string
}

func (b *buildError) Error() string { return b.err.Error() + ": " + strings.TrimSpace(b.out) }

// fakeAdapter returns an adapter whose command is this test binary re-executed as the scripted ACP agent.
func fakeAdapter(t *testing.T, script fakeacp.Script, mut ...func(*acp.Config)) *acp.Adapter {
	t.Helper()
	dir := t.TempDir()
	b, _ := json.Marshal(script)
	sp := filepath.Join(dir, "script.json")
	if err := os.WriteFile(sp, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRYDOCK_FAKE_ACP", "1")
	t.Setenv("DRYDOCK_FAKE_ACP_SCRIPT", sp)
	cfg := acp.Config{ID: "fakeacp", Command: os.Args[0]}
	for _, m := range mut {
		m(&cfg)
	}
	return acp.New(cfg)
}

func startSession(t *testing.T, ad agents.Adapter, spec agents.RunSpec) agents.Session {
	t.Helper()
	if spec.Cwd == "" {
		spec.Cwd = t.TempDir()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s, err := ad.Start(ctx, proc.New(), spec)
	if err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		sctx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer c2()
		_ = s.Stop(sctx)
		cancel()
	})
	return s
}

// turn sends msg and returns every chunk up to and including the turn's done chunk.
func turn(t *testing.T, s agents.Session, msg string) []workflow.Chunk {
	t.Helper()
	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	return readUntilDone(t, s, 30*time.Second)
}

func readUntilDone(t *testing.T, s agents.Session, d time.Duration) []workflow.Chunk {
	t.Helper()
	var chunks []workflow.Chunk
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case c, ok := <-s.Output():
			if !ok {
				t.Fatalf("output closed before done; chunks=%v stderr=%q", kinds(chunks), s.Stderr())
			}
			chunks = append(chunks, c)
			if c.Kind == workflow.ChunkDone {
				return chunks
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for done; chunks=%v stderr=%q", kinds(chunks), s.Stderr())
		}
	}
}

func kinds(cs []workflow.Chunk) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Kind)
	}
	return out
}

func ofKind(cs []workflow.Chunk, kind string) []workflow.Chunk {
	var out []workflow.Chunk
	for _, c := range cs {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// text is the concatenation of all text deltas.
func text(cs []workflow.Chunk) string {
	var sb strings.Builder
	for _, c := range ofKind(cs, workflow.ChunkText) {
		var d workflow.TextDelta
		_ = c.Decode(&d)
		sb.WriteString(d.Text)
	}
	return sb.String()
}

func decode[T any](t *testing.T, c workflow.Chunk) T {
	t.Helper()
	var v T
	if err := c.Decode(&v); err != nil {
		t.Fatalf("decode %s: %v", c.Kind, err)
	}
	return v
}

func stopReason(t *testing.T, cs []workflow.Chunk) string {
	t.Helper()
	d := ofKind(cs, workflow.ChunkDone)
	if len(d) == 0 {
		t.Fatal("no done chunk")
	}
	return decode[workflow.DoneChunk](t, d[len(d)-1]).StopReason
}

func alive(s agents.Session) bool {
	select {
	case <-s.Wait():
		return false
	case <-time.After(150 * time.Millisecond):
		return true
	}
}
