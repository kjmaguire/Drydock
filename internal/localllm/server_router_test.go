package localllm_test

import (
	"bytes"
	"context"
	"encoding/json"
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

// fileModel is a downloaded model of a test: a file, and a projector when asked.
func fileModel(t *testing.T, id string, projector bool) localllm.Model {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "model.gguf")
	_ = os.WriteFile(f, []byte("GGUF"), 0o644)
	repo, variant, _ := strings.Cut(id, ":")
	m := localllm.Model{ID: id, Repo: repo, Variant: variant, Files: []string{f}, Size: 4}
	if projector {
		m.Projector = filepath.Join(dir, "mmproj-F16.gguf")
		_ = os.WriteFile(m.Projector, []byte("GGUF"), 0o644)
	}
	return m
}

func call(t *testing.T, method, url, key string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rd)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return resp.StatusCode, v
}

// One model with a projector and a draft: the server is started with --mmproj, and --model-draft with --spec-type draft-simple
// (decision P111); the stand-in refuses a projector or a draft whose file is missing, and a draft without the spec type.
func TestOneModelIsServedWithItsProjectorAndItsDraft(t *testing.T) {
	srv := &localllm.Server{PM: proc.New()}
	record := filepath.Join(t.TempDir(), "args.txt")
	m := fileModel(t, "ggml-org/gemma-3-12b-it-GGUF:Q4_K_M", true)
	draft := fileModel(t, "ggml-org/gemma-3-1b-it-GGUF:Q4_K_M", false)
	l := localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--record=" + record), Model: m, Drafts: map[string]localllm.Model{m.ID: draft},
		Port: freePort(t), APIKey: "k", Timeout: 30 * time.Second}
	if err := srv.Start(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	args, _ := os.ReadFile(record)
	for _, want := range []string{"--mmproj\n" + m.Projector, "--model-draft\n" + draft.Files[0], "--spec-type\ndraft-simple", "--jinja", "-m\n" + m.Files[0]} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("arguments lack %q:\n%s", want, args)
		}
	}
	st := srv.State()
	if st.Router || strings.Join(st.Models, ",") != m.ID || !st.Serves(m.ID) || st.Serves(draft.ID) {
		t.Fatalf("%+v", st)
	}
	base := strings.TrimSuffix(st.BaseURL, "/v1")
	if code, props := call(t, "GET", base+"/props", "k", nil); code != 200 || props["modalities"].(map[string]any)["vision"] != true {
		t.Fatalf("%d %v", code, props)
	}
	srv.Stop()
	// the stand-in's own checks: a draft without its spec type, a projector that is not there
	m.Projector = ""
	for _, args := range [][]string{
		append(llamatest.Args(), "--model-draft", draft.Files[0]),
		append(llamatest.Args(), "--mmproj", filepath.Join(t.TempDir(), "gone.gguf")),
	} {
		err := srv.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: args, Model: m, Port: freePort(t), Timeout: 30 * time.Second})
		if err == nil || !strings.Contains(err.Error(), "code 1") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

// An embedding model is served with --embeddings and without the chat template: the server answers /v1/embeddings and refuses
// a chat, and a chat server refuses embeddings.
func TestAnEmbeddingModelIsServedOnItsOwn(t *testing.T) {
	srv := &localllm.Server{PM: proc.New()}
	record := filepath.Join(t.TempDir(), "args.txt")
	m := fileModel(t, "Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0", true)
	if err := srv.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--record=" + record), Model: m, Embeddings: true, Port: freePort(t), APIKey: "k", Timeout: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	args, _ := os.ReadFile(record)
	if !strings.Contains(string(args), "--embeddings") || strings.Contains(string(args), "--jinja") || strings.Contains(string(args), "--mmproj") {
		t.Fatalf("%s", args)
	}
	if st := srv.State(); !st.Embeddings || st.State != "running" {
		t.Fatalf("%+v", st)
	}
	base := strings.TrimSuffix(srv.State().BaseURL, "/v1")
	code, v := call(t, "POST", base+"/v1/embeddings", "k", map[string]any{"input": []string{"hello", "drydock"}})
	if code != 200 || len(v["data"].([]any)) != 2 {
		t.Fatalf("%d %v", code, v)
	}
	if code, _ := call(t, "POST", base+"/v1/chat/completions", "k", map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}); code != http.StatusNotImplemented {
		t.Fatalf("an embedding server answered a chat: %d", code)
	}
	if code, _ := call(t, "POST", base+"/v1/embeddings", "", map[string]any{"input": "x"}); code != http.StatusUnauthorized {
		t.Fatalf("embeddings without the key: %d", code)
	}
}

// Several models are served by the router (decision P111): the preset file lists each by its id with its file, its projector
// and its draft, loaded at the start; the router is started with --models-preset and --models-max, its own cache folder, the
// shared options on its command line; Start waits until the listing says every model is loaded; a request is routed by its
// model field, and one naming no model, or another, is refused.
func TestSeveralModelsAreServedByTheRouter(t *testing.T) {
	srv := &localllm.Server{PM: proc.New()}
	tmp := t.TempDir()
	record, recordEnv := filepath.Join(tmp, "args.txt"), filepath.Join(tmp, "env.txt")
	coder := fileModel(t, "ggml-org/Qwen3-Coder-30B-A3B-Instruct-GGUF:Q4_K_M", false)
	vision := fileModel(t, "ggml-org/gemma-3-4b-it-GGUF:Q4_K_M", true)
	draft := fileModel(t, "ggml-org/Qwen3-0.6B-GGUF:Q8_0", false)
	port := freePort(t)
	l := localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--load-ms=600", "--record="+record, "--record-env="+recordEnv),
		Models: []localllm.Model{coder, vision}, Drafts: map[string]localllm.Model{coder.ID: draft},
		PresetFile: filepath.Join(tmp, "router", "models.ini"), CacheDir: filepath.Join(tmp, "router", "cache"),
		Port: port, CacheType: "q8_0", ContextSize: 32768, APIKey: "k", LogDir: tmp, Timeout: 30 * time.Second}
	start := time.Now()
	if err := srv.Start(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	if time.Since(start) < 500*time.Millisecond {
		t.Fatal("Start did not wait for the models to load")
	}
	st := srv.State()
	if st.State != "running" || !st.Router || st.ModelID != coder.ID || strings.Join(st.Models, ",") != coder.ID+","+vision.ID || !st.Serves(vision.ID) {
		t.Fatalf("%+v", st)
	}
	args, _ := os.ReadFile(record)
	for _, want := range []string{"--models-preset\n" + l.PresetFile, "--models-max\n2", "--port\n" + strconv.Itoa(port), "-ctk\nq8_0", "-c\n32768", "--jinja"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("arguments lack %q:\n%s", want, args)
		}
	}
	if strings.Contains(string(args), "\n-m\n") || strings.Contains(string(args), "--alias") {
		t.Fatalf("the router is started without a model of its own:\n%s", args)
	}
	preset, _ := os.ReadFile(l.PresetFile)
	for _, want := range []string{"version = 1", "[" + coder.ID + "]\nmodel = " + coder.Files[0] + "\nmodel-draft = " + draft.Files[0] + "\nspec-type = draft-simple\nload-on-startup = true",
		"[" + vision.ID + "]\nmodel = " + vision.Files[0] + "\nmmproj = " + vision.Projector + "\nload-on-startup = true"} {
		if !strings.Contains(string(preset), want) {
			t.Fatalf("the preset lacks %q:\n%s", want, preset)
		}
	}
	if env, _ := os.ReadFile(recordEnv); !strings.Contains(string(env), "LLAMA_CACHE="+l.CacheDir) || !strings.Contains(string(env), "key=true") {
		t.Fatalf("the router's environment: %s", env)
	}
	base := strings.TrimSuffix(st.BaseURL, "/v1")
	code, listing := call(t, "GET", base+"/v1/models", "", nil)
	if code != 200 || len(listing["data"].([]any)) != 2 {
		t.Fatalf("%d %v", code, listing)
	}
	for _, d := range listing["data"].([]any) {
		if d.(map[string]any)["status"].(map[string]any)["value"] != "loaded" {
			t.Fatalf("%v", d)
		}
	}
	msg := map[string]any{"role": "user", "content": "hi"}
	for _, id := range []string{coder.ID, vision.ID} {
		code, v := call(t, "POST", base+"/v1/chat/completions", "k", map[string]any{"model": id, "messages": []any{msg}})
		if code != 200 || !strings.Contains(v["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"].(string), id) {
			t.Fatalf("%s: %d %v", id, code, v)
		}
	}
	if code, _ := call(t, "POST", base+"/v1/chat/completions", "k", map[string]any{"messages": []any{msg}}); code != 400 {
		t.Fatalf("a request naming no model: %d", code)
	}
	if code, _ := call(t, "POST", base+"/v1/chat/completions", "k", map[string]any{"model": draft.ID, "messages": []any{msg}}); code != 400 {
		t.Fatalf("a request for a model the router does not have: %d", code)
	}
	if code, props := call(t, "GET", base+"/props", "k", nil); code != 200 || props["role"] != "router" {
		t.Fatalf("%d %v", code, props)
	}
	if code, props := call(t, "GET", base+"/props?model="+vision.ID, "k", nil); code != 200 || props["model_alias"] != vision.ID {
		t.Fatalf("%d %v", code, props)
	}
}

// A model the router cannot load fails the start with its name; more models than the router keeps, an embedding model among
// several, and a path llama.cpp's preset reader would cut short are refused before anything starts.
func TestTheRouterRefusesWhatItCannotServe(t *testing.T) {
	srv := &localllm.Server{PM: proc.New()}
	tmp := t.TempDir()
	a, b := fileModel(t, "acme/a-GGUF:Q4_0", false), fileModel(t, "acme/b-GGUF:Q4_0", false)
	l := localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--load-ms=200", "--fail-model="+b.ID), Models: []localllm.Model{a, b},
		PresetFile: filepath.Join(tmp, "models.ini"), Port: freePort(t), Timeout: 30 * time.Second}
	err := srv.Start(context.Background(), l)
	if err == nil || !strings.Contains(err.Error(), b.ID+" could not be loaded") {
		t.Fatalf("%v", err)
	}
	if st := srv.State(); st.State != "failed" {
		t.Fatalf("%+v", st)
	}
	l.Args = llamatest.Args()
	five := []localllm.Model{a, b, fileModel(t, "acme/c-GGUF:Q4_0", false), fileModel(t, "acme/d-GGUF:Q4_0", false), fileModel(t, "acme/e-GGUF:Q4_0", false)}
	for name, bad := range map[string]localllm.Launch{
		"five":      {Program: l.Program, Models: five, PresetFile: l.PresetFile, Port: l.Port},
		"embedding": {Program: l.Program, Models: []localllm.Model{a, b}, Embeddings: true, PresetFile: l.PresetFile, Port: l.Port},
		"no preset": {Program: l.Program, Models: []localllm.Model{a, b}, Port: l.Port},
	} {
		if err := srv.Start(context.Background(), bad); err == nil {
			t.Errorf("%s was started", name)
		}
	}
	semi := a
	semi.Files = []string{filepath.Join(tmp, "a;b.gguf")}
	if _, err := localllm.Preset(localllm.Launch{Models: []localllm.Model{semi, b}}); err == nil || !strings.Contains(err.Error(), "cannot carry") {
		t.Fatalf("%v", err)
	}
}
