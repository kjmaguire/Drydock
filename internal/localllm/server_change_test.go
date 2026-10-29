package localllm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/proc"
)

// routerOf starts the router on models with the stand-in's options, and gives the launch it was started with.
func routerOf(t *testing.T, srv *localllm.Server, opts []string, models ...localllm.Model) localllm.Launch {
	t.Helper()
	tmp := t.TempDir()
	l := localllm.Launch{Program: os.Args[0], Args: llamatest.Args(opts...), Models: models,
		PresetFile: filepath.Join(tmp, "router", "models.ini"), CacheDir: filepath.Join(tmp, "router", "cache"),
		Port: freePort(t), CacheType: "q8_0", APIKey: "k", Timeout: 30 * time.Second}
	if err := srv.Start(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	return l
}

// listed is the router's listing: each model's state, by its id.
func listed(t *testing.T, srv *localllm.Server) map[string]string {
	t.Helper()
	base := strings.TrimSuffix(srv.State().BaseURL, "/v1")
	code, v := call(t, "GET", base+"/v1/models", "", nil)
	if code != 200 {
		t.Fatalf("%d %v", code, v)
	}
	out := map[string]string{}
	for _, d := range v["data"].([]any) {
		row := d.(map[string]any)
		out[row["id"].(string)] = row["status"].(map[string]any)["value"].(string)
	}
	return out
}

func chatTo(t *testing.T, srv *localllm.Server, id string) int {
	t.Helper()
	base := strings.TrimSuffix(srv.State().BaseURL, "/v1")
	code, _ := call(t, "POST", base+"/v1/chat/completions", "k", map[string]any{"model": id, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	return code
}

// One model is loaded beside the others, and one unloaded, without the server starting again (decision P120): the router
// unloads what goes (POST /models/unload), reads its preset again (GET /models?reload=1), loads what comes (POST /models/load)
// and Change waits for it, while the models kept go on answering. Down to one model the router stays one, so a model loaded
// next is again loaded beside it.
func TestOneModelIsLoadedOrUnloadedWhileTheOthersKeepRunning(t *testing.T) {
	srv := &localllm.Server{PM: proc.New()}
	calls := filepath.Join(t.TempDir(), "calls.txt")
	a, b, c := fileModel(t, "acme/a-GGUF:Q4_0", false), fileModel(t, "acme/b-GGUF:Q4_0", false), fileModel(t, "acme/c-GGUF:Q4_0", true)
	l := routerOf(t, srv, []string{"--load-ms=700", "--calls=" + calls}, a, b)
	pid, started := srv.State().PID, srv.State().StartedAt

	// c comes: a and b keep answering while it loads
	l.Models = []localllm.Model{a, b, c}
	l.APIKey = "another key, not used: the server keeps its own"
	done := make(chan error, 1)
	go func() { done <- srv.Change(context.Background(), l) }()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(srv.State().Loading, c.ID) {
		if time.Now().After(deadline) {
			t.Fatalf("c was never loading: %+v", srv.State())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := srv.State(); !st.Serves(c.ID) || st.State != "running" {
		t.Fatalf("while c loads: %+v", st)
	}
	if code := chatTo(t, srv, a.ID); code != 200 {
		t.Fatalf("a while c loads: %d", code)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	st := srv.State()
	if st.PID != pid || !st.StartedAt.Equal(started) || strings.Join(st.Models, ",") != a.ID+","+b.ID+","+c.ID || len(st.Loading) != 0 || st.Note != "" {
		t.Fatalf("the server started again, or says otherwise: %+v", st)
	}
	if got := listed(t, srv); got[a.ID] != "loaded" || got[b.ID] != "loaded" || got[c.ID] != "loaded" {
		t.Fatalf("%v", got)
	}
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if code := chatTo(t, srv, id); code != 200 {
			t.Fatalf("%s: %d", id, code)
		}
	}
	if preset, _ := os.ReadFile(l.PresetFile); !strings.Contains(string(preset), "["+c.ID+"]\nmodel = "+c.Files[0]+"\nmmproj = "+c.Projector) {
		t.Fatalf("the preset lacks c:\n%s", preset)
	}

	// a goes: unloaded, and out of the listing once the preset was read again
	l.Models = []localllm.Model{b, c}
	if err := srv.Change(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if got := listed(t, srv); len(got) != 2 || got[b.ID] != "loaded" || got[c.ID] != "loaded" {
		t.Fatalf("%v", got)
	}
	if code := chatTo(t, srv, a.ID); code != 400 {
		t.Fatalf("a request for a, unloaded: %d", code)
	}
	// down to one: the router stays, and loads another beside it again
	l.Models = []localllm.Model{c}
	if err := srv.Change(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if st := srv.State(); !st.Router || strings.Join(st.Models, ",") != c.ID || st.ModelID != c.ID {
		t.Fatalf("%+v", st)
	}
	l.Models = []localllm.Model{c, a}
	if err := srv.Change(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if st := srv.State(); st.PID != pid || strings.Join(st.Models, ",") != c.ID+","+a.ID {
		t.Fatalf("%+v", st)
	}
	got, _ := os.ReadFile(calls)
	want := "reload *\nload " + c.ID + "\nunload " + a.ID + "\nreload *\nunload " + b.ID + "\nreload *\nreload *\nload " + a.ID + "\n"
	if string(got) != want {
		t.Fatalf("the router's calls:\n%s\nwant:\n%s", got, want)
	}
	// the same set again calls nothing
	if err := srv.Change(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(calls); string(again) != want {
		t.Fatalf("%s", again)
	}
}

// What cannot be changed on the running server is a *NeedsRestart: a server of one model (no router), a setting of the router's
// command line, a kept model whose options changed; and a server that does not answer the router's calls, which says so
// (Unanswered) and changes nothing. A model that fails to load is an error and leaves the others loaded.
func TestAChangeTheServerCannotMakeNeedsARestart(t *testing.T) {
	a, b, c := fileModel(t, "acme/a-GGUF:Q4_0", false), fileModel(t, "acme/b-GGUF:Q4_0", false), fileModel(t, "acme/c-GGUF:Q4_0", false)
	draft := fileModel(t, "acme/a-draft-GGUF:Q4_0", false)

	one := &localllm.Server{PM: proc.New()}
	if err := one.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: llamatest.Args(), Model: a, Port: freePort(t), Timeout: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	defer one.Stop()
	var nr *localllm.NeedsRestart
	if err := one.Change(context.Background(), localllm.Launch{Program: os.Args[0], Args: llamatest.Args(), Models: []localllm.Model{a, b}}); !errors.As(err, &nr) || nr.Unanswered {
		t.Fatalf("one model: %v", err)
	}

	srv := &localllm.Server{PM: proc.New()}
	l := routerOf(t, srv, nil, a, b)
	for name, edit := range map[string]func(*localllm.Launch){
		"cache":   func(x *localllm.Launch) { x.CacheType = "q4_0" },
		"context": func(x *localllm.Launch) { x.ContextSize = 8192 },
		"draft":   func(x *localllm.Launch) { x.Drafts = map[string]localllm.Model{a.ID: draft} },
	} {
		x := l
		x.Models = []localllm.Model{a, b, c}
		edit(&x)
		if err := srv.Change(context.Background(), x); !errors.As(err, &nr) || nr.Unanswered {
			t.Errorf("%s: %v", name, err)
		}
	}
	if st := srv.State(); strings.Join(st.Models, ",") != a.ID+","+b.ID {
		t.Fatalf("%+v", st)
	}

	mute := &localllm.Server{PM: proc.New()}
	ml := routerOf(t, mute, []string{"--no-router-calls"}, a, b)
	ml.Models = []localllm.Model{a, b, c}
	err := mute.Change(context.Background(), ml)
	if !errors.As(err, &nr) || !nr.Unanswered || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a server without the calls: %v", err)
	}
	ml.Models = []localllm.Model{a}
	if err := mute.Change(context.Background(), ml); !errors.As(err, &nr) || !nr.Unanswered || !strings.Contains(err.Error(), "/models/unload") {
		t.Fatalf("a server without the calls: %v", err)
	}
	if st := mute.State(); st.State != "running" || strings.Join(st.Models, ",") != a.ID+","+b.ID {
		t.Fatalf("%+v", st)
	}

	failing := &localllm.Server{PM: proc.New()}
	fl := routerOf(t, failing, []string{"--load-ms=200", "--fail-model=" + c.ID}, a, b)
	fl.Models = []localllm.Model{a, b, c}
	if err := failing.Change(context.Background(), fl); err == nil || errors.As(err, &nr) || !strings.Contains(err.Error(), c.ID+" could not be loaded") {
		t.Fatalf("%v", err)
	}
	if st := failing.State(); st.State != "running" || strings.Join(st.Models, ",") != a.ID+","+b.ID || len(st.Loading) != 0 {
		t.Fatalf("%+v", st)
	}
	if code := chatTo(t, failing, b.ID); code != 200 {
		t.Fatalf("b after c failed: %d", code)
	}
}
