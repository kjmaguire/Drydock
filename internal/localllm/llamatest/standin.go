// Package llamatest holds the LABELLED TEST DOUBLE of llama-server and PROTOCOL FIXTURES of the GitHub releases API and the
// Hugging Face Hub, so the local models feature is tested end to end without downloading or running llama.cpp. The stand-in
// is this test binary (or the e2e harness) started again as `<binary> drydock-standin-llama-server [options] <server args>`;
// it answers the server's own endpoints (/health, /v1/models, /props, /v1/chat/completions, /v1/embeddings) with canned,
// documented shapes and never presents itself as llama.cpp (its --version says so). It runs either of llama-server's two modes
// as llama.cpp's tools/server/README.md (build b11936) describes them: one model (-m), or the router (--models-preset, no -m),
// which lists every model of the preset with its state and routes each request by its "model" field (decision P111).
package llamatest

import (
	"bufio"
	"crypto/subtle"
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
//	--load-ms=N               answer /health with 503 (one model) or list the models as loading (the router) for N ms after start
//	--fail-start              exit 1 at once with a message (a build that cannot load the model)
//	--fail-model=ID           the router's instance of the model ID ends while it loads (its listing says failed, code 1)
//	--record=FILE             write the server arguments to FILE (a test checks what it was started with)
//	--record-env=FILE         write the router's cache folder (LLAMA_CACHE) and whether a key was given (never the key) to FILE
//
// Like the real server it refuses a projector (--mmproj) or a draft (--model-draft) whose file is not there. Unlike it, it also
// refuses a draft model without --spec-type draft-simple: llama.cpp would load the draft and never use it, which a test would
// not otherwise see.
func Args(opts ...string) []string { return append([]string{Marker}, opts...) }

// model is one model the stand-in serves.
type model struct {
	id, path, projector, draft string
}

// Main runs the stand-in named by os.Args and returns its exit code.
func Main(args []string) int {
	rest := args[2:]
	devices, loadMS, record, recordEnv, failModel := "cuda", 0, "", "", ""
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
		case strings.HasPrefix(opt, "--fail-model="):
			failModel = strings.TrimPrefix(opt, "--fail-model=")
		case strings.HasPrefix(opt, "--record="):
			record = strings.TrimPrefix(opt, "--record=")
		case strings.HasPrefix(opt, "--record-env="):
			recordEnv = strings.TrimPrefix(opt, "--record-env=")
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
	if recordEnv != "" {
		_ = os.WriteFile(recordEnv, []byte(fmt.Sprintf("LLAMA_CACHE=%s\nkey=%v\n", os.Getenv("LLAMA_CACHE"), os.Getenv("LLAMA_API_KEY") != "")), 0o644)
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
				// what a Vulkan build says on a PC whose driver it could not load (the words of llama.cpp's own lines)
				fmt.Fprintln(os.Stderr, "ggml_vulkan: No devices found.\nload_backend: loaded CPU backend from stand-in-cpu.dll")
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
	port, alias, ctx, maxModels := 0, "", 4096, 4
	cacheK, preset, spec := "f16", "", ""
	embeddings := false
	one := model{}
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
			one.path = next()
		case "-c", "--ctx-size":
			ctx, _ = strconv.Atoi(next())
		case "-ctk", "--cache-type-k":
			cacheK = next()
		case "-mm", "--mmproj":
			one.projector = next()
		case "-md", "--model-draft", "--spec-draft-model":
			one.draft = next()
		case "--spec-type":
			spec = next()
		case "--embedding", "--embeddings":
			embeddings = true
		case "--models-preset":
			preset = next()
		case "--models-max":
			maxModels, _ = strconv.Atoi(next())
		case "--host", "-ngl", "-ctv", "--cache-type-v", "--gpu-layers":
			next()
		}
	}
	var models []model
	router := preset != "" && one.path == ""
	if router {
		var err error
		if models, err = readPreset(preset); err != nil {
			fmt.Fprintf(os.Stderr, "stand-in llama-server: %v\n", err)
			return 1
		}
		if maxModels > 0 && len(models) > maxModels {
			fmt.Fprintf(os.Stderr, "stand-in llama-server: number of models to load on startup (%d) exceeds models_max (%d)\n", len(models), maxModels)
			return 1
		}
	} else {
		if one.path == "" {
			fmt.Fprintln(os.Stderr, "stand-in llama-server: no model (-m)")
			return 1
		}
		if one.draft != "" && spec != "draft-simple" {
			fmt.Fprintln(os.Stderr, "stand-in llama-server: a draft model without --spec-type draft-simple drafts nothing (a test double's check)")
			return 1
		}
		one.id = alias
		if one.id == "" {
			one.id = one.path
		}
		models = []model{one}
	}
	for _, m := range models {
		for what, p := range map[string]string{"model": m.path, "projector": m.projector, "draft model": m.draft} {
			if p == "" {
				continue
			}
			if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
				fmt.Fprintf(os.Stderr, "stand-in llama-server: cannot load the %s %s: %v\n", what, p, err)
				return 1
			}
		}
	}
	byID := map[string]model{}
	for _, m := range models {
		byID[m.id] = m
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "stand-in llama-server: %v\n", err)
		return 1
	}
	ready := time.Now().Add(time.Duration(loadMS) * time.Millisecond)
	if router {
		fmt.Printf("stand-in llama-server (a Drydock test double): router for %d models, cache %s, ctx %d\n", len(models), cacheK, ctx)
	} else {
		fmt.Printf("stand-in llama-server (a Drydock test double): loading %s, cache %s, ctx %d\n", one.path, cacheK, ctx)
	}
	fmt.Printf("main: server is listening on http://%s\n", ln.Addr())
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	errorJSON := func(w http.ResponseWriter, code int, kind, msg string) {
		writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg, "type": kind}})
	}
	loading := func(w http.ResponseWriter) bool {
		if !router && time.Now().Before(ready) {
			errorJSON(w, http.StatusServiceUnavailable, "unavailable_error", "Loading model")
			return true
		}
		return false
	}
	// a model's state in the router: loading until the load time has passed, then loaded (or failed, when told)
	status := func(m model) map[string]any {
		switch {
		case time.Now().Before(ready):
			return map[string]any{"value": "loading", "args": []string{"llama-server"}}
		case m.id == failModel:
			return map[string]any{"value": "unloaded", "args": []string{"llama-server"}, "failed": true, "exit_code": 1}
		}
		return map[string]any{"value": "loaded", "args": []string{"llama-server"}}
	}
	inputs := func(m model) []string {
		if m.projector != "" {
			return []string{"text", "image"}
		}
		return []string{"text"}
	}
	props := func(m model) map[string]any {
		return map[string]any{
			"default_generation_settings": map[string]any{"n_ctx": ctx, "params": map[string]any{"n_predict": -1}},
			"total_slots":                 1, "model_path": m.path, "model_alias": m.id, "chat_template": "", "build_info": "standin (a Drydock test double)",
			"modalities": map[string]any{"vision": m.projector != "", "audio": false},
		}
	}
	// the model a request names: in the router a missing or unknown one is refused, as the router does
	pick := func(w http.ResponseWriter, name string) (model, bool) {
		if !router {
			return one, true
		}
		if name == "" {
			errorJSON(w, http.StatusBadRequest, "invalid_request_error", "model name is missing from the request")
			return model{}, false
		}
		m, ok := byID[name]
		if !ok {
			errorJSON(w, http.StatusBadRequest, "invalid_request_error", "model not found: "+name)
			return model{}, false
		}
		if st := status(m); st["value"] != "loaded" {
			errorJSON(w, http.StatusServiceUnavailable, "unavailable_error", "model is not loaded: "+name)
			return model{}, false
		}
		return m, true
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if loading(w) {
			return
		}
		writeJSON(w, 200, map[string]any{"status": "ok"})
	})
	listModels := func(w http.ResponseWriter, _ *http.Request) {
		if loading(w) {
			return
		}
		data := []any{}
		for _, m := range models {
			row := map[string]any{"id": m.id, "object": "model", "created": time.Now().Unix(), "owned_by": "llamacpp",
				"architecture": map[string]any{"input_modalities": inputs(m), "output_modalities": []string{"text"}}}
			if router {
				row["status"] = status(m)
				row["context_length"] = 32768
			} else {
				row["meta"] = map[string]any{"vocab_type": 2, "n_vocab": 151936, "n_ctx_train": 32768, "n_embd": 2048, "n_params": 4022468096, "size": 2497281024}
			}
			data = append(data, row)
		}
		writeJSON(w, 200, map[string]any{"object": "list", "data": data})
	}
	mux.HandleFunc("/v1/models", listModels)
	mux.HandleFunc("/models", listModels)
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		if loading(w) {
			return
		}
		if router && r.URL.Query().Get("model") == "" {
			// the router's own properties: no model, no context (llama.cpp's server-models.cpp)
			writeJSON(w, 200, map[string]any{"role": "router", "max_instances": maxModels, "models_autoload": true, "model_alias": "llama-server",
				"model_path": "none", "default_generation_settings": map[string]any{"params": map[string]any{}, "n_ctx": 0}, "build_info": "standin router (a Drydock test double)"})
			return
		}
		m, ok := pick(w, r.URL.Query().Get("model"))
		if !ok {
			return
		}
		writeJSON(w, 200, props(m))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if loading(w) {
			return
		}
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if embeddings {
			errorJSON(w, http.StatusNotImplemented, "not_supported_error", "This server does not support completions. Start it without `--embeddings`")
			return
		}
		m, ok := pick(w, req.Model)
		if !ok {
			return
		}
		last := ""
		if n := len(req.Messages); n > 0 {
			if s, ok := req.Messages[n-1].Content.(string); ok {
				last = s
			}
		}
		text := "stand-in answer to: " + last
		if router {
			text = "stand-in answer from " + m.id + " to: " + last
		}
		usage := map[string]any{"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20}
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk := map[string]any{"id": "chatcmpl-standin", "object": "chat.completion.chunk", "model": m.id,
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": text}, "finish_reason": nil}}}
			b, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", b)
			end := map[string]any{"id": "chatcmpl-standin", "object": "chat.completion.chunk", "model": m.id,
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": usage}
			b, _ = json.Marshal(end)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			return
		}
		writeJSON(w, 200, map[string]any{"id": "chatcmpl-standin", "object": "chat.completion", "created": time.Now().Unix(), "model": m.id,
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}},
			"usage":   usage})
	})
	// embeddings only with --embeddings, as the real server: a vector of four numbers per input (OpenAI's shape)
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if loading(w) {
			return
		}
		if !embeddings {
			errorJSON(w, http.StatusNotImplemented, "not_supported_error", "This server does not support embeddings. Start it with `--embeddings`")
			return
		}
		var req struct {
			Input any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var inputs []string
		switch in := req.Input.(type) {
		case string:
			inputs = []string{in}
		case []any:
			for _, x := range in {
				s, _ := x.(string)
				inputs = append(inputs, s)
			}
		}
		data := []any{}
		for i, s := range inputs {
			data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": []float64{float64(len(s)), 0.5, 0.25, 0.125}})
		}
		writeJSON(w, 200, map[string]any{"object": "list", "model": one.id, "data": data, "usage": map[string]any{"prompt_tokens": len(inputs), "total_tokens": len(inputs)}})
	})
	// like the real server: with LLAMA_API_KEY set every endpoint but the public ones wants "Authorization: Bearer <key>"
	var handler http.Handler = mux
	if key := os.Getenv("LLAMA_API_KEY"); key != "" {
		public := map[string]bool{"/health": true, "/v1/health": true, "/models": true, "/v1/models": true, "/api/tags": true}
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !public[r.URL.Path] && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
				errorJSON(w, http.StatusUnauthorized, "authentication_error", "Invalid API Key")
				return
			}
			mux.ServeHTTP(w, r)
		})
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
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

// readPreset reads the router's preset file the way llama.cpp's common/preset.cpp does for the keys Drydock writes: sections,
// "key = value" lines, and comments that start with ";" or "#" (a value ends at either). Every section must name its model
// file, and a draft model needs spec-type draft-simple (the stand-in's own check, as for one model).
func readPreset(path string) ([]model, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("preset file does not exist: %s", path)
	}
	defer f.Close()
	var out []model
	var cur *model
	spec := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			out = append(out, model{id: strings.TrimSpace(line[1 : len(line)-1])})
			cur = &out[len(out)-1]
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("preset line %q is not key = value", line)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if i := strings.IndexAny(v, ";#"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if cur == nil {
			continue // version and the [*] defaults
		}
		switch k {
		case "model", "m":
			cur.path = v
		case "mmproj", "mm":
			cur.projector = v
		case "model-draft", "md", "spec-draft-model":
			cur.draft = v
		case "spec-type":
			spec[cur.id] = v
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("preset file %s lists no model", path)
	}
	for _, m := range out {
		if m.path == "" {
			return nil, fmt.Errorf("preset %s names no model file", m.id)
		}
		if m.draft != "" && spec[m.id] != "draft-simple" {
			return nil, fmt.Errorf("preset %s: a draft model without spec-type draft-simple drafts nothing (a test double's check)", m.id)
		}
	}
	return out, nil
}
