package localllm_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/proc"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func tinyModel(t *testing.T) localllm.Model {
	t.Helper()
	f := filepath.Join(t.TempDir(), "tiny-Q4_0.gguf")
	_ = os.WriteFile(f, []byte("GGUF"), 0o644)
	return localllm.Model{ID: "acme/tiny-GGUF:Q4_0", Repo: "acme/tiny-GGUF", Variant: "Q4_0", Files: []string{f}, Size: 4}
}

// Start waits for the model to load (/health 503 then 200), the server then lists the model under Drydock's id, and Stop
// ends it. The arguments carry the model, the alias, the cache type and the context size.
func TestTheServerIsStartedWithTheModelAndStopped(t *testing.T) {
	var states []string
	srv := &localllm.Server{PM: proc.New(), OnChange: func(st localllm.ServerState) { states = append(states, st.State) }}
	if st := srv.State(); st.State != "stopped" {
		t.Fatalf("before the first start: %+v", st)
	}
	port := freePort(t)
	record := filepath.Join(t.TempDir(), "args.txt")
	l := localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--load-ms=700", "--record="+record), Model: tinyModel(t), Port: port, CacheType: "q8_0", ContextSize: 16384, LogDir: t.TempDir(), Timeout: 30 * time.Second}
	if err := srv.Start(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	st := srv.State()
	if st.State != "running" || st.ModelID != "acme/tiny-GGUF:Q4_0" || st.BaseURL != "http://127.0.0.1:"+strconv.Itoa(port)+"/v1" || st.PID == 0 {
		t.Fatalf("%+v", st)
	}
	args, _ := os.ReadFile(record)
	for _, want := range []string{"--host\n127.0.0.1", "--port\n" + strconv.Itoa(port), "-m\n" + l.Model.Files[0], "--alias\nacme/tiny-GGUF:Q4_0", "-ctk\nq8_0", "-ctv\nq8_0", "-c\n16384", "-ngl\nauto", "--jinja"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("arguments lack %q:\n%s", want, args)
		}
	}
	resp, err := http.Get(st.BaseURL + "/models")
	if err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&listing)
	resp.Body.Close()
	if len(listing.Data) != 1 || listing.Data[0].ID != "acme/tiny-GGUF:Q4_0" {
		t.Fatalf("%+v", listing)
	}
	if lines := srv.Log(); len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "listening") {
		t.Fatalf("log %v", lines)
	}
	if b, _ := os.ReadFile(filepath.Join(l.LogDir, "llama-server.log")); !strings.Contains(string(b), "stand-in") {
		t.Fatal("no log file")
	}
	srv.Stop()
	if st := srv.State(); st.State != "stopped" {
		t.Fatalf("%+v", st)
	}
	if _, err := http.Get(st.BaseURL + "/models"); err == nil {
		t.Fatal("the server still answers after Stop")
	}
	if strings.Join(states, ",") != "starting,starting,running,stopped" {
		t.Fatalf("states %v", states)
	}
}

// With a key the server is started with it in its environment (LLAMA_API_KEY) and never on its command line, answers nothing
// but /health and the model list without it, and Start is satisfied only by a server that accepts the key (audit 2026-10-16,
// L32). A different program that holds the port is not taken for the server.
func TestTheServerDemandsAKeyThatIsNotOnItsCommandLine(t *testing.T) {
	srv := &localllm.Server{PM: proc.New()}
	port := freePort(t)
	record := filepath.Join(t.TempDir(), "args.txt")
	key := localllm.NewAPIKey()
	if len(key) < 32 || key == localllm.NewAPIKey() {
		t.Fatalf("a key must be long and new every time: %q", key)
	}
	l := localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--record=" + record), Model: tinyModel(t), Port: port, APIKey: key, LogDir: t.TempDir(), Timeout: 30 * time.Second}
	if err := srv.Start(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	if args, _ := os.ReadFile(record); strings.Contains(string(args), key) || strings.Contains(string(args), "api-key") {
		t.Fatalf("the key is on the command line, where any program on the machine can read it:\n%s", args)
	}
	get := func(path, auth string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.State().BaseURL[:len(srv.State().BaseURL)-len("/v1")]+path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := get("/props", ""); got != http.StatusUnauthorized {
		t.Fatalf("/props without the key: %d", got)
	}
	if got := get("/props", "Bearer wrong"); got != http.StatusUnauthorized {
		t.Fatalf("/props with a wrong key: %d", got)
	}
	if got := get("/props", "Bearer "+key); got != http.StatusOK {
		t.Fatalf("/props with the key: %d", got)
	}
	if got := get("/health", ""); got != http.StatusOK {
		t.Fatalf("/health is public: %d", got)
	}
}

// A port that another program holds is refused before anything is started (the keyed readiness check is in
// server_internal_test.go).
func TestAStartOnAPortAnotherProgramHoldsIsRefused(t *testing.T) {
	impostor := &localllm.Server{PM: proc.New()}
	port := freePort(t)
	if err := impostor.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: llamatest.Args(), Model: tinyModel(t), Port: port, Timeout: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	defer impostor.Stop()
	real := &localllm.Server{PM: proc.New()}
	err := real.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: llamatest.Args(), Model: tinyModel(t), Port: port, APIKey: localllm.NewAPIKey(), Timeout: 5 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("a start on a port that another program holds: %v", err)
	}
}

// A Stop while the model still loads ends in "stopped", never in "failed": the Start that was waiting answers that it was
// stopped and leaves the state alone, however the process's exit and the stop are ordered.
func TestAStopDuringTheLoadLeavesTheServerStopped(t *testing.T) {
	var mu sync.Mutex
	var states []string
	srv := &localllm.Server{PM: proc.New(), OnChange: func(st localllm.ServerState) {
		mu.Lock()
		states = append(states, st.State)
		mu.Unlock()
	}}
	started := make(chan error, 1)
	go func() {
		started <- srv.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--load-ms=60000"), Model: tinyModel(t), Port: freePort(t), Timeout: 30 * time.Second})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for srv.State().PID == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	srv.Stop()
	select {
	case err := <-started:
		if err == nil || !strings.Contains(err.Error(), "stopped") {
			t.Fatalf("Start answered %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
	time.Sleep(100 * time.Millisecond) // the exit goroutine, had it a say, would have had it by now
	if st := srv.State(); st.State != "stopped" || st.Error != "" {
		t.Fatalf("%+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(strings.Join(states, ","), "failed") || states[len(states)-1] != "stopped" {
		t.Fatalf("states %v", states)
	}
}

// stopInTheWindow is a process manager that calls Stop after the program has been started and before the server has taken hold of
// it: the moment in which a Stop finds nothing to end (audit 2026-10-16, L44: the page lets a person press Stop while a start waits).
// It also watches the program, so the test can tell whether it ended however the server reads its exit.
type stopInTheWindow struct {
	proc.Manager
	srv     *localllm.Server
	started chan *watched
}

func (m stopInTheWindow) Start(ctx context.Context, spec proc.Spec) (proc.Proc, error) {
	p, err := m.Manager.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	w := &watched{Proc: p, exited: make(chan struct{}), wait: make(chan proc.ExitInfo, 1)}
	go func() {
		ei := <-p.Wait()
		w.wait <- ei
		close(w.exited)
	}()
	m.started <- w
	m.srv.Stop()
	return w, nil
}

// watched is a program whose exit is announced twice: to whoever calls Wait, as always, and by closing exited.
type watched struct {
	proc.Proc
	exited chan struct{}
	wait   chan proc.ExitInfo
}

func (w *watched) Wait() <-chan proc.ExitInfo { return w.wait }

// A Stop that lands while the program is being started (after it exists, before the server holds it) must not leave the program
// running: the Start it came during ends it and answers that it was stopped.
func TestAStopThatLandsWhileTheProgramIsBeingStartedDoesNotLeaveItRunning(t *testing.T) {
	srv := &localllm.Server{}
	started := make(chan *watched, 1)
	srv.PM = stopInTheWindow{Manager: proc.New(), srv: srv, started: started}
	t.Cleanup(srv.Stop)
	err := srv.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--load-ms=60000"), Model: tinyModel(t), Port: freePort(t), Timeout: 30 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("Start answered %v", err)
	}
	if st := srv.State(); st.State != "stopped" || st.Error != "" {
		t.Fatalf("%+v", st)
	}
	w := <-started
	select {
	case <-w.exited:
	case <-time.After(5 * time.Second):
		_ = w.Kill(true) // the test's own cleanup: the server never held this program, so srv.Stop cannot end it
		t.Fatal("the program a Stop did not find is still running after Start returned")
	}
}

// A program that ends at start is reported as failed with its last lines; one that never becomes ready is killed.
func TestAServerThatCannotStartIsReported(t *testing.T) {
	srv := &localllm.Server{PM: proc.New()}
	err := srv.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--fail-start"), Model: tinyModel(t), Port: freePort(t), Timeout: 30 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "told to fail") {
		t.Fatal(err)
	}
	if st := srv.State(); st.State != "failed" || !strings.Contains(st.Error, "code 1") {
		t.Fatalf("%+v", st)
	}
	err = srv.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--load-ms=60000"), Model: tinyModel(t), Port: freePort(t), Timeout: 1500 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "not ready after") {
		t.Fatal(err)
	}
	if st := srv.State(); st.State != "failed" || st.PID == 0 {
		t.Fatalf("%+v", st)
	}
	srv.Stop()
	// a port another program holds is refused before anything starts
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	err = srv.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: llamatest.Args(), Model: tinyModel(t), Port: ln.Addr().(*net.TCPAddr).Port})
	if err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatal(err)
	}
}
