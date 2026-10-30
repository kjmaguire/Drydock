// Package llamatest holds the LABELLED TEST DOUBLE of llama-server and PROTOCOL FIXTURES of the GitHub releases API and the
// Hugging Face Hub, so the local models feature is tested end to end without downloading or running llama.cpp. The stand-in
// is this test binary (or the e2e harness) started again as `<binary> drydock-standin-llama-server [options] <server args>`;
// it answers the server's own endpoints (/health, /v1/models, /props, /v1/chat/completions, /v1/embeddings) with canned,
// documented shapes and never presents itself as llama.cpp (its --version says so). It runs either of llama-server's two modes
// as llama.cpp's tools/server/README.md (build b11936) describes them: one model (-m), or the router (--models-preset, no -m),
// which lists every model of the preset with its state and routes each request by its "model" field (decision P111), and
// loads and unloads one model while the others keep running (POST /models/load and /models/unload, GET /models?reload=1 to
// read the preset again; decision P120), as the README and tools/server/server-models.cpp at that build answer them.
package llamatest

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
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
//	--no-router-calls         the router answers neither /models/load nor /models/unload (404, as a build without them would),
//	                          and reads its preset only once
//	--calls=FILE              append each router call (load, unload, reload, with its model) to FILE, one per line
//
// Like the real server it refuses a projector (--mmproj) or a draft (--model-draft) whose file is not there. Unlike it, it also
// refuses a draft model without a --spec-type that uses one (draft-simple, draft-eagle3, draft-dflash, draft-dspark, or
// draft-mtp for a file of MTP heads), and a type that needs a draft file without one: llama.cpp would load the draft and never
// use it, or fail later, which a test would not otherwise see. --spec-type draft-mtp without a draft uses the model's own MTP
// heads, as llama.cpp's does.
func Args(opts ...string) []string { return append([]string{Marker}, opts...) }

// model is one model the stand-in serves, with the kind of speculative decoding it runs ("" for none).
type model struct {
	id, path, projector, draft, spec string
}

// draftFileSpecs are the spec types that load a draft file; draft-mtp may, or may use the model's own heads.
var draftFileSpecs = map[string]bool{"draft-simple": true, "draft-eagle3": true, "draft-dflash": true, "draft-dspark": true}

// draftSpecOK is the stand-in's check of a model's speculative decoding: a draft file needs a type that uses one, a type that
// needs a draft file has one, and draft-mtp is either (the model's own heads, or a file of heads alone).
func draftSpecOK(m model) error {
	switch {
	case m.draft != "" && !draftFileSpecs[m.spec] && m.spec != "draft-mtp":
		return fmt.Errorf("a draft model without a --spec-type that uses it drafts nothing (a test double's check)")
	case m.draft == "" && draftFileSpecs[m.spec]:
		return fmt.Errorf("--spec-type %s needs a draft model (a test double's check)", m.spec)
	case m.spec != "" && !draftFileSpecs[m.spec] && m.spec != "draft-mtp":
		return fmt.Errorf("the stand-in knows no --spec-type %s", m.spec)
	}
	return nil
}

// Main runs the stand-in named by os.Args and returns its exit code.
func Main(args []string) int {
	rest := args[2:]
	devices, loadMS, record, recordEnv, failModel, calls := "cuda", 0, "", "", "", ""
	fail, noRouterCalls := false, false
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
		case opt == "--no-router-calls":
			noRouterCalls = true
		case strings.HasPrefix(opt, "--calls="):
			calls = strings.TrimPrefix(opt, "--calls=")
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
	cacheK, preset := "f16", ""
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
			one.spec = next()
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
		if err := draftSpecOK(one); err != nil {
			fmt.Fprintf(os.Stderr, "stand-in llama-server: %v\n", err)
			return 1
		}
		one.id = alias
		if one.id == "" {
			one.id = one.path
		}
		models = []model{one}
	}
	for _, m := range models {
		if err := filesOf(m); err != nil {
			fmt.Fprintf(os.Stderr, "stand-in llama-server: %v\n", err)
			return 1
		}
	}
	// the router's models with their state: what the preset lists, each loaded at the start; /models/load, /models/unload and a
	// reload of the preset change them while the others keep running (decision P120)
	var mu sync.Mutex
	type inst struct {
		model
		loaded  bool      // running: loading until readyAt, then loaded
		readyAt time.Time //
		failed  bool      // its instance ends while it loads
		order   int       // when it was loaded last, for the least recently loaded one the router unloads past --models-max
	}
	insts := map[string]*inst{}
	var names []string // the listing's order
	seq := 0
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "stand-in llama-server: %v\n", err)
		return 1
	}
	ready := time.Now().Add(time.Duration(loadMS) * time.Millisecond)
	for _, m := range models {
		insts[m.id] = &inst{model: m, loaded: true, readyAt: ready, failed: m.id == failModel, order: seq}
		names = append(names, m.id)
		seq++
	}
	logCall := func(what, id string) {
		if calls == "" {
			return
		}
		if f, err := os.OpenFile(calls, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%s %s\n", what, id)
			f.Close()
		}
	}
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
	// a model's state in the router (under mu): unloaded, loading until its load time has passed, then loaded (or failed, when
	// told), in the shapes of the README's GET /models
	status := func(in *inst) map[string]any {
		switch {
		case in.loaded && time.Now().Before(in.readyAt):
			return map[string]any{"value": "loading", "args": []string{"llama-server"}}
		case in.failed:
			in.loaded = false
			return map[string]any{"value": "unloaded", "args": []string{"llama-server"}, "failed": true, "exit_code": 1}
		case !in.loaded:
			return map[string]any{"value": "unloaded"}
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
		mu.Lock()
		defer mu.Unlock()
		in, ok := insts[name]
		if !ok {
			errorJSON(w, http.StatusBadRequest, "invalid_request_error", "model not found: "+name)
			return model{}, false
		}
		if st := status(in); st["value"] != "loaded" {
			errorJSON(w, http.StatusServiceUnavailable, "unavailable_error", "model is not loaded: "+name)
			return model{}, false
		}
		return in.model, true
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if loading(w) {
			return
		}
		writeJSON(w, 200, map[string]any{"status": "ok"})
	})
	// reload reads the preset again, as GET /models?reload=1 does (under mu): a model no longer in it is dropped, one whose
	// options changed is unloaded and takes them, a new one is listed unloaded (load-on-startup is not honored then)
	reload := func() error {
		fresh, err := readPreset(preset)
		if err != nil {
			return err
		}
		keep := map[string]bool{}
		names = names[:0]
		for _, m := range fresh {
			keep[m.id] = true
			names = append(names, m.id)
			if in, ok := insts[m.id]; ok {
				if in.model != m {
					in.model, in.loaded, in.failed = m, false, false
				}
				continue
			}
			insts[m.id] = &inst{model: m}
		}
		for id := range insts {
			if !keep[id] {
				delete(insts, id)
			}
		}
		return nil
	}
	listModels := func(w http.ResponseWriter, r *http.Request) {
		if loading(w) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if router && !noRouterCalls && r.URL.Query().Get("reload") != "" {
			logCall("reload", "*")
			if err := reload(); err != nil {
				errorJSON(w, http.StatusInternalServerError, "server_error", err.Error())
				return
			}
		}
		data := []any{}
		for _, id := range names {
			in := insts[id]
			row := map[string]any{"id": in.id, "object": "model", "created": time.Now().Unix(), "owned_by": "llamacpp",
				"architecture": map[string]any{"input_modalities": inputs(in.model), "output_modalities": []string{"text"}}}
			if router {
				row["status"] = status(in)
				row["context_length"] = 32768
			} else {
				row["meta"] = map[string]any{"vocab_type": 2, "n_vocab": 151936, "n_ctx_train": 32768, "n_embd": 2048, "n_params": 4022468096, "size": 2497281024}
			}
			data = append(data, row)
		}
		writeJSON(w, 200, map[string]any{"object": "list", "data": data})
	}
	// the router's load and unload of one model (server-models.cpp's post_router_models_load and _unload): the others keep
	// running; past --models-max the least recently loaded one is unloaded, as the router does
	if router && !noRouterCalls {
		named := func(r *http.Request) string {
			var req struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			return req.Model
		}
		mux.HandleFunc("POST /models/load", func(w http.ResponseWriter, r *http.Request) {
			id := named(r)
			mu.Lock()
			defer mu.Unlock()
			logCall("load", id)
			in, ok := insts[id]
			if !ok {
				errorJSON(w, http.StatusNotFound, "not_found_error", "model is not found")
				return
			}
			if in.loaded {
				errorJSON(w, http.StatusBadRequest, "invalid_request_error", "model is already running")
				return
			}
			running, oldest := 0, ""
			for _, x := range insts {
				if x.loaded {
					running++
					if oldest == "" || x.order < insts[oldest].order {
						oldest = x.id
					}
				}
			}
			if maxModels > 0 && running >= maxModels {
				insts[oldest].loaded = false
			}
			in.loaded, in.failed, in.order = true, in.id == failModel || filesOf(in.model) != nil, seq
			in.readyAt = time.Now().Add(time.Duration(loadMS) * time.Millisecond)
			seq++
			writeJSON(w, 200, map[string]any{"success": true})
		})
		mux.HandleFunc("POST /models/unload", func(w http.ResponseWriter, r *http.Request) {
			id := named(r)
			mu.Lock()
			defer mu.Unlock()
			logCall("unload", id)
			in, ok := insts[id]
			if !ok {
				errorJSON(w, http.StatusBadRequest, "invalid_request_error", "model is not found")
				return
			}
			if !in.loaded {
				errorJSON(w, http.StatusBadRequest, "invalid_request_error", "model is not running")
				return
			}
			in.loaded = false
			writeJSON(w, 200, map[string]any{"success": true})
		})
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
	// embeddings only with --embeddings, as the real server, in OpenAI's shape: a vector per input (WordVector: texts that share
	// words are near, so a test can tell a ranking by meaning from another; it is no model)
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
			data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": WordVector(s)})
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

// WordVector is the stand-in's embedding of a text: 64 counts of its lower-case words, each word counted at a place its FNV
// hash picks, plus 1 in the last place so no vector is all zeros. Texts that share words are near by cosine similarity; it
// knows nothing of meaning (a test double).
func WordVector(s string) []float64 {
	v := make([]float64, 65)
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[h.Sum32()%64]++
	}
	v[64] = 1
	return v
}

// filesOf checks that a model's files are there, as llama-server does when it loads one.
func filesOf(m model) error {
	for what, p := range map[string]string{"model": m.path, "projector": m.projector, "draft model": m.draft} {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("cannot load the %s %s: %v", what, p, err)
		}
	}
	return nil
}

// readPreset reads the router's preset file the way llama.cpp's common/preset.cpp does for the keys Drydock writes: sections,
// "key = value" lines, and comments that start with ";" or "#" (a value ends at either). Every section must name its model
// file, and its draft and spec type must go together (the stand-in's own check, as for one model).
func readPreset(path string) ([]model, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("preset file does not exist: %s", path)
	}
	defer f.Close()
	var out []model
	var cur *model
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
			cur.spec = v
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("preset file %s lists no model", path)
	}
	for _, m := range out {
		if m.path == "" {
			return nil, fmt.Errorf("preset %s names no model file", m.id)
		}
		if err := draftSpecOK(m); err != nil {
			return nil, fmt.Errorf("preset %s: %v", m.id, err)
		}
	}
	return out, nil
}
