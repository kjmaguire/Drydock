package localllm

import (
	"bufio"
	"context"
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
	// Timeout bounds the wait for the model to load (default 15 minutes: a large model from a slow disk).
	Timeout time.Duration
}

// ServerState is what the page shows about the server.
type ServerState struct {
	State     string    `json:"state"` // stopped | starting | running | failed
	ModelID   string    `json:"modelId,omitempty"`
	Port      int       `json:"port,omitempty"`
	BaseURL   string    `json:"baseUrl,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"startedAt,omitempty"`
	PID       int       `json:"pid,omitempty"`
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

// State is the current state.
func (s *Server) State() ServerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
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
		*st = ServerState{State: "starting", ModelID: l.Model.ID, Port: l.Port, BaseURL: base + "/v1", StartedAt: time.Now().UTC()}
	})
	if err := portFree(l.Port); err != nil {
		s.set(func(st *ServerState) { st.State, st.Error = "failed", err.Error() })
		return err
	}
	env := proc.ScrubEnv(os.Environ(), proc.BaseEnvNames, nil)
	// the server outlives this call: the person stops it
	p, err := s.PM.Start(context.Background(), proc.Spec{Path: l.Program, Args: Arguments(l), Dir: dirOf(l.Program), Env: env, Label: "llama-server"})
	if err != nil {
		s.set(func(st *ServerState) { st.State, st.Error = "failed", "could not start llama-server: "+err.Error() })
		return err
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.p, s.done = p, done
	s.mu.Unlock()
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
	go pump(p.Stdout())
	go pump(p.Stderr())
	exited := make(chan proc.ExitInfo, 1)
	go func() {
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
	}()
	s.set(func(st *ServerState) { st.PID = p.PID() })
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case ei := <-exited:
			err := fmt.Errorf("llama-server ended with code %d: %s", ei.Code, s.tail())
			s.set(func(st *ServerState) { st.State, st.Error = "failed", err.Error() })
			return err
		case <-wctx.Done():
			_ = p.Kill(true)
			err := fmt.Errorf("llama-server was not ready after %s: %s", timeout, s.tail())
			if ctx.Err() != nil {
				err = errors.New("stopped before llama-server was ready")
			}
			<-done
			s.set(func(st *ServerState) { st.State, st.Error = "failed", err.Error() })
			return err
		case <-tick.C:
			if s.healthy(wctx, base) {
				s.set(func(st *ServerState) { st.State, st.Error = "running", "" })
				return nil
			}
		}
	}
}

func (s *Server) healthy(ctx context.Context, base string) bool {
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	}
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
