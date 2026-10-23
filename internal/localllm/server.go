package localllm

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Launch is how llama-server is started: the program (the installed build's, or a labelled stand-in with its leading
// arguments), one model and the settings a person chose.
type Launch struct {
	Program     string
	Args        []string
	Model       Model
	Port        int
	CacheType   string // q8_0 | q4_0 | f16 | turbo4 | turbo3
	ContextSize int    // 0: the model's own
	GPULayers   string // "auto" (default), "all", or a number
	LogDir      string
	// APIKey, when set, is the key the server demands on every request but /health and the model list: it reaches the server
	// through its environment (LLAMA_API_KEY), never its command line, which other programs on the machine can read. Any other
	// program on the machine could otherwise use the model and read its settings (audit 2026-10-16, L32). Start also checks the
	// answering server with it, so a different program that took the port is not taken for it.
	APIKey string
	// Timeout bounds the wait for the model to load (default 15 minutes: a large model from a slow disk).
	Timeout time.Duration
	// Autostarted says Drydock started the server by itself when it started (the Autostart setting), not a person; the state
	// says so.
	Autostarted bool
}

// NewAPIKey is a random key for one start of the server.
func NewAPIKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("localllm: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// childEnv is the environment llama-server and its probes start with: the allowlist that agents get, none of the app's tokens.
func childEnv() []string { return proc.ScrubEnv(os.Environ(), proc.BaseEnvNames, nil) }

// ServerState is what the page shows about the server.
type ServerState struct {
	State     string    `json:"state"` // stopped | starting | running | failed
	ModelID   string    `json:"modelId,omitempty"`
	Port      int       `json:"port,omitempty"`
	BaseURL   string    `json:"baseUrl,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"startedAt,omitempty"`
	PID       int       `json:"pid,omitempty"`
	// Autostarted says Drydock started the server when it started (Launch.Autostarted); false when a person did. CacheType and
	// ContextSize are what the server was started with (ContextSize 0: the model's own).
	Autostarted bool   `json:"autostarted"`
	CacheType   string `json:"cacheType,omitempty"`
	ContextSize int    `json:"contextSize,omitempty"`
}

// Server runs one llama-server at a time.
type Server struct {
	PM   proc.Manager
	HTTP *http.Client
	// OnChange is told every state change (the core publishes it).
	OnChange func(ServerState)

	mu    sync.Mutex
	st    ServerState
	p     proc.Proc
	done  chan struct{} // closed when the current process has ended and its output is drained
	lines []string
	gen   int
}

const keepLines = 200

// errStopped is what a Start answers when a Stop (or a newer Start) took over while it waited, or while its program was starting.
var errStopped = errors.New("the server was stopped before llama-server was ready")

// State is the current state ("stopped" before the first start).
func (s *Server) State() ServerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	if st.State == "" {
		st.State = "stopped"
	}
	return st
}

// Log is the server's last lines.
func (s *Server) Log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.lines...)
}

func (s *Server) set(mutate func(*ServerState)) ServerState {
	s.mu.Lock()
	mutate(&s.st)
	st := s.st
	s.mu.Unlock()
	if s.OnChange != nil {
		s.OnChange(st)
	}
	return st
}

func (s *Server) addLine(l string) {
	s.mu.Lock()
	s.lines = append(s.lines, l)
	if len(s.lines) > keepLines {
		s.lines = s.lines[len(s.lines)-keepLines:]
	}
	s.mu.Unlock()
}

// Arguments is the command line Start uses, for tests and the page.
func Arguments(l Launch) []string {
	args := append([]string{}, l.Args...)
	args = append(args, "--host", "127.0.0.1", "--port", strconv.Itoa(l.Port), "-m", l.Model.Files[0], "--alias", l.Model.ID, "--jinja", "--no-webui")
	ngl := l.GPULayers
	if ngl == "" {
		ngl = "auto"
	}
	args = append(args, "-ngl", ngl)
	if l.CacheType != "" {
		args = append(args, "-ctk", l.CacheType, "-ctv", l.CacheType)
	}
	if l.ContextSize > 0 {
		args = append(args, "-c", strconv.Itoa(l.ContextSize))
	}
	return args
}

// Start stops a server that runs, starts llama-server with the model and waits until /health answers 200 (the model is
// loaded). A program that ends first, or one that is not ready in time, is reported as failed with its last lines.
func (s *Server) Start(ctx context.Context, l Launch) error {
	if len(l.Model.Files) == 0 {
		return errors.New("the model has no file")
	}
	if l.Port <= 0 || l.Port > 65535 {
		return fmt.Errorf("%d is not a port", l.Port)
	}
	s.Stop()
	if s.PM == nil {
		s.PM = proc.New()
	}
	timeout := l.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	s.mu.Lock()
	s.gen++
	gen := s.gen
	s.lines = nil
	s.mu.Unlock()
	base := "http://127.0.0.1:" + strconv.Itoa(l.Port)
	s.set(func(st *ServerState) {
		*st = ServerState{State: "starting", ModelID: l.Model.ID, Port: l.Port, BaseURL: base + "/v1", StartedAt: time.Now().UTC(),
			Autostarted: l.Autostarted, CacheType: l.CacheType, ContextSize: l.ContextSize}
	})
	if err := portFree(l.Port); err != nil {
		s.set(func(st *ServerState) { st.State, st.Error = "failed", err.Error() })
		return err
	}
	env := childEnv()
	if l.APIKey != "" {
		env = append(env, "LLAMA_API_KEY="+l.APIKey)
	}
	// the server outlives this call: the person stops it
	p, err := s.PM.Start(context.Background(), proc.Spec{Path: l.Program, Args: Arguments(l), Dir: dirOf(l.Program), Env: env, Label: "llama-server"})
	if err != nil {
		s.set(func(st *ServerState) { st.State, st.Error = "failed", "could not start llama-server: "+err.Error() })
		return err
	}
	done := make(chan struct{})
	s.mu.Lock()
	// A Stop (or a newer Start) that came while the program was being started found nothing to end: the server held no process yet.
	// This Start is no longer the current one, so it ends the program itself (and lets it go, so the port is free again when this
	// returns) instead of leaving a llama-server that the page says is stopped.
	superseded := s.gen != gen
	if !superseded {
		s.p, s.done = p, done
	}
	s.mu.Unlock()
	if superseded {
		_ = p.Kill(true)
		select {
		case <-p.Wait():
		case <-time.After(10 * time.Second):
		}
		return errStopped
	}
	_ = p.Stdin().Close()
	var logf *os.File
	if l.LogDir != "" && os.MkdirAll(l.LogDir, 0o755) == nil {
		logf, _ = os.Create(filepath.Join(l.LogDir, "llama-server.log"))
	}
	var wg sync.WaitGroup
	pump := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			s.addLine(sc.Text())
			if logf != nil {
				fmt.Fprintln(logf, sc.Text())
			}
		}
		_, _ = io.Copy(io.Discard, r)
	}
	wg.Add(2)
	stdout, stderr := p.Stdout(), p.Stderr()
	safe.Go("localllm.Server.Start.stdout", func() { pump(stdout) })
	safe.Go("localllm.Server.Start.stderr", func() { pump(stderr) })
	exited := make(chan proc.ExitInfo, 1)
	safe.Go("localllm.Server.Start#3", func() {
		ei := <-p.Wait()
		wg.Wait()
		if logf != nil {
			logf.Close()
		}
		exited <- ei
		close(done)
		s.mu.Lock()
		current := s.gen == gen
		s.mu.Unlock()
		if current {
			msg := fmt.Sprintf("llama-server ended with code %d: %s", ei.Code, s.tail())
			s.set(func(st *ServerState) {
				if st.State == "running" || st.State == "starting" {
					st.State, st.Error = "failed", msg
				}
			})
		}
	})
	s.set(func(st *ServerState) { st.PID = p.PID() })
	// one client for the whole wait: a new transport per poll would leave an idle connection behind each
	hc := s.HTTP
	if hc == nil {
		tr := &http.Transport{Proxy: nil}
		defer tr.CloseIdleConnections()
		hc = &http.Client{Timeout: 2 * time.Second, Transport: tr}
	}
	// a Stop (or a newer Start) during the wait moves the generation on and owns the state from then on
	current := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.gen == gen
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case ei := <-exited:
			if !current() {
				return errStopped
			}
			err := fmt.Errorf("llama-server ended with code %d: %s", ei.Code, s.tail())
			s.set(func(st *ServerState) { st.State, st.Error = "failed", err.Error() })
			return err
		case <-wctx.Done():
			_ = p.Kill(true)
			err := fmt.Errorf("llama-server was not ready after %s: %s", timeout, s.tail())
			if ctx.Err() != nil {
				err = errStopped
			}
			<-done
			if !current() {
				return errStopped
			}
			s.set(func(st *ServerState) { st.State, st.Error = "failed", err.Error() })
			return err
		case <-tick.C:
			if !current() {
				return errStopped
			}
			if s.healthy(wctx, hc, base) && s.ours(wctx, hc, base, l.APIKey) {
				var stopped bool
				s.set(func(st *ServerState) {
					if stopped = s.gen != gen; !stopped {
						st.State, st.Error = "running", ""
					}
				})
				if stopped {
					return errStopped
				}
				return nil
			}
		}
	}
}

func (s *Server) healthy(ctx context.Context, hc *http.Client, base string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ours checks that what answers on the port is the server just started: with a key, /props (which the server protects with it)
// must answer 200 to the key as JSON. Without one nothing can be told apart, and /health is the whole check.
func (s *Server) ours(ctx context.Context, hc *http.Client, base, key string) bool {
	if key == "" {
		return true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/props", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var v map[string]json.RawMessage
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&v) == nil
}

func (s *Server) tail() string {
	lines := s.Log()
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.TrimSpace(strings.Join(lines, " | "))
}

// Stop ends the server, if one runs.
func (s *Server) Stop() {
	s.mu.Lock()
	p, done := s.p, s.done
	s.p, s.done = nil, nil
	s.gen++
	running := s.st.State == "running" || s.st.State == "starting"
	s.mu.Unlock()
	if p != nil {
		_ = p.Kill(true)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	if running || p != nil {
		s.set(func(st *ServerState) { *st = ServerState{State: "stopped"} })
	}
}

func portFree(port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("port %d is in use (another program listens on it; choose another port in the settings)", port)
	}
	return ln.Close()
}

func dirOf(program string) string {
	if d := filepath.Dir(program); d != "." {
		return d
	}
	return ""
}
