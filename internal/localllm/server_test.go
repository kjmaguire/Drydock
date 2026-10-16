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
