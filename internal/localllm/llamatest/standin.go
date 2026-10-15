// Package llamatest holds the LABELLED TEST DOUBLE of llama-server and PROTOCOL FIXTURES of the GitHub releases API and the
// Hugging Face Hub, so the local models feature is tested end to end without downloading or running llama.cpp. The stand-in
// is this test binary (or the e2e harness) started again as `<binary> drydock-standin-llama-server [options] <server args>`;
// it answers the server's own endpoints (/health, /v1/models, /props, /v1/chat/completions) with canned, documented shapes and
// never presents itself as llama.cpp (its --version says so).
package llamatest

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

// Marker is the first argument that tells a binary to run the stand-in instead of itself.
const Marker = "drydock-standin-llama-server"

// IsStandIn reports whether args (os.Args) ask for the stand-in.
func IsStandIn(args []string) bool { return len(args) > 1 && args[1] == Marker }

// Args are the leading arguments (Launch.Args) that make a binary run the stand-in. Options come before the server's own
// arguments:
//
//	--devices=cuda|none|fail  what --list-devices prints (default cuda: one GPU with 16 GB, 14 GB free)
//	--load-ms=N               answer /health with 503 for N ms after start (the model loading)
//	--fail-start              exit 1 at once with a message (a build that cannot load the model)
//	--record=FILE             write the server arguments to FILE (a test checks what it was started with)
func Args(opts ...string) []string { return append([]string{Marker}, opts...) }

// Main runs the stand-in named by os.Args and returns its exit code.
func Main(args []string) int {
	rest := args[2:]
	devices, loadMS, record := "cuda", 0, ""
	fail := false
	for len(rest) > 0 {
		opt := rest[0]
		switch {
		case strings.HasPrefix(opt, "--devices="):
			devices = strings.TrimPrefix(opt, "--devices=")
		case strings.HasPrefix(opt, "--load-ms="):
			loadMS, _ = strconv.Atoi(strings.TrimPrefix(opt, "--load-ms="))
		case opt == "--fail-start":
			fail = true
		case strings.HasPrefix(opt, "--record="):
			record = strings.TrimPrefix(opt, "--record=")
		default:
			opt = ""
		}
		if opt == "" {
			break
		}
		rest = rest[1:]
	}
	if record != "" {
		_ = os.WriteFile(record, []byte(strings.Join(rest, "\n")), 0o644)
	}
	for _, a := range rest {
		switch a {
		case "--version":
			fmt.Println("version: 0 (standin) (a Drydock test double, not llama.cpp)")
			return 0
		case "--list-devices":
			switch devices {
			case "fail":
				fmt.Fprintln(os.Stderr, "stand-in: told not to list devices")
				return 1
			case "none":
				fmt.Println("Available devices:\n  (none)")
			default:
				fmt.Println("Available devices:\n  CUDA0: Stand-in GPU (a Drydock test double) (16384 MiB, 14336 MiB free)")
			}
			return 0
		}
	}
	if fail {
		fmt.Fprintln(os.Stderr, "stand-in llama-server: told to fail at start (a test double)")
		return 1
	}
	port, alias, model, ctx := 0, "", "", 4096
	cacheK := "f16"
	for i := 0; i < len(rest); i++ {
		next := func() string {
			if i+1 < len(rest) {
				i++
				return rest[i]
			}
			return ""
		}
		switch rest[i] {
		case "--port":
			port, _ = strconv.Atoi(next())
		case "--alias", "-a":
			alias = next()
		case "-m", "--model":
			model = next()
		case "-c", "--ctx-size":
			ctx, _ = strconv.Atoi(next())
		case "-ctk", "--cache-type-k":
			cacheK = next()
		case "--host", "-ngl", "-ctv", "--cache-type-v", "--gpu-layers":
			next()
		}
	}
	if model == "" {
		fmt.Fprintln(os.Stderr, "stand-in llama-server: no model (-m)")
		return 1
	}
	if st, err := os.Stat(model); err != nil || !st.Mode().IsRegular() {
		fmt.Fprintf(os.Stderr, "stand-in llama-server: cannot load %s: %v\n", model, err)
		return 1
	}
	if alias == "" {
		alias = model
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "stand-in llama-server: %v\n", err)
		return 1
	}
	ready := time.Now().Add(time.Duration(loadMS) * time.Millisecond)
	fmt.Printf("stand-in llama-server (a Drydock test double): loading %s, cache %s, ctx %d\n", model, cacheK, ctx)
	fmt.Printf("main: server is listening on http://%s\n", ln.Addr())
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	loading := func(w http.ResponseWriter) bool {
		if time.Now().Before(ready) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": 503, "message": "Loading model", "type": "unavailable_error"}})
			return true
		}
		return false
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if loading(w) {
			return
		}
		writeJSON(w, 200, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		if loading(w) {
			return
		}
		writeJSON(w, 200, map[string]any{"object": "list", "data": []any{map[string]any{
			"id": alias, "object": "model", "created": time.Now().Unix(), "owned_by": "llamacpp",
			"meta": map[string]any{"vocab_type": 2, "n_vocab": 151936, "n_ctx_train": 32768, "n_embd": 2048, "n_params": 4022468096, "size": 2497281024},
		}}})
	})
	mux.HandleFunc("/props", func(w http.ResponseWriter, _ *http.Request) {
		if loading(w) {
			return
		}
		writeJSON(w, 200, map[string]any{
			"default_generation_settings": map[string]any{"n_ctx": ctx, "params": map[string]any{"n_predict": -1}},
			"total_slots":                 1, "model_path": model, "model_alias": alias, "chat_template": "", "build_info": "standin (a Drydock test double)",
			"modalities": map[string]any{"vision": false, "audio": false},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if loading(w) {
			return
		}
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		last := ""
		if n := len(req.Messages); n > 0 {
			if s, ok := req.Messages[n-1].Content.(string); ok {
				last = s
			}
		}
		text := "stand-in answer to: " + last
		usage := map[string]any{"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20}
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk := map[string]any{"id": "chatcmpl-standin", "object": "chat.completion.chunk", "model": alias,
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": text}, "finish_reason": nil}}}
			b, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", b)
			end := map[string]any{"id": "chatcmpl-standin", "object": "chat.completion.chunk", "model": alias,
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": usage}
			b, _ = json.Marshal(end)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			return
		}
		writeJSON(w, 200, map[string]any{"id": "chatcmpl-standin", "object": "chat.completion", "created": time.Now().Unix(), "model": alias,
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}},
			"usage":   usage})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	go func() {
		<-stop
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "stand-in llama-server: %v\n", err)
		return 1
	}
	return 0
}
