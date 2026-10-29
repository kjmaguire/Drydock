package localllm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Loading or unloading one model while the others keep running (decision P120): llama-server's router loads and unloads one
// model on request (POST /models/load and /models/unload, tools/server/README.md at build b11936 and the TurboQuant fork's at
// b10298-2.0.0), and reads its preset file again on GET /models?reload=1, listing a model new in it as unloaded. Change uses
// those calls, so a model added to the set or taken out of it leaves the others loaded and answering.

// NeedsRestart is what Change answers when the change cannot be made on the running server: the caller starts the server again
// on the new set instead. Unanswered says the server did not answer the router's calls (a build without them, or one that
// answered them with an error), which the person is told; otherwise the restart is the expected way (no router runs, or a
// setting or a kept model's options differ from what the server runs with).
type NeedsRestart struct {
	Reason     string
	Unanswered bool
}

func (e *NeedsRestart) Error() string { return e.Reason }

// Change makes the set of models the running router keeps loaded l.Models, without starting it again: it unloads the models
// no longer wanted, writes the preset with the new set and has the router read it again, loads each new model, and waits until
// the router's listing says each is loaded. The models kept stay loaded throughout. The server keeps the key, the port and the
// options it was started with; l must name the same ones (its APIKey is not used). A model that cannot be loaded is an error
// and leaves the others loaded; anything the server does not answer as documented is a *NeedsRestart.
func (s *Server) Change(ctx context.Context, l Launch) error {
	if len(l.Models) == 0 {
		return errors.New("no model to keep loaded")
	}
	if len(l.Models) > MaxLoaded {
		return fmt.Errorf("%d models at once is more than the %d the server keeps loaded", len(l.Models), MaxLoaded)
	}
	ids := make([]string, 0, len(l.Models))
	for _, m := range l.Models {
		if len(m.Files) == 0 {
			return errors.New("the model has no file")
		}
		ids = append(ids, m.ID)
	}
	s.mu.Lock()
	cur, gen, st := s.cur, s.gen, s.st
	s.mu.Unlock()
	if st.State != "running" || !st.Router || !cur.Router() {
		return &NeedsRestart{Reason: "no router runs: a server of one model loads no other beside it"}
	}
	if why := sameServer(cur, l); why != "" {
		return &NeedsRestart{Reason: why}
	}
	// a model kept loaded must keep its options: the router would unload it when its section of the preset changed
	old := map[string]bool{}
	for _, m := range cur.Models {
		old[m.ID] = true
		for _, n := range l.Models {
			if n.ID != m.ID {
				continue
			}
			a, errA := section(cur, m)
			b, errB := section(l, n)
			if errB != nil {
				return errB
			}
			if errA != nil || a != b {
				return &NeedsRestart{Reason: m.ID + " is kept with other options (its draft, projector or file changed)"}
			}
		}
	}
	var add, remove []string
	for _, id := range ids {
		if !old[id] {
			add = append(add, id)
		}
	}
	for _, m := range cur.Models {
		if !slices.Contains(ids, m.ID) {
			remove = append(remove, m.ID)
		}
	}
	preset, err := Preset(l)
	if err != nil {
		return err
	}
	l.APIKey, l.RouterMode, l.Note, l.Autostarted = cur.APIKey, true, "", cur.Autostarted
	if len(add) == 0 && len(remove) == 0 {
		// the same models (perhaps in another order): nothing to ask the router
		s.mu.Lock()
		same := s.gen == gen
		if same {
			s.cur = l
		}
		s.mu.Unlock()
		if same {
			s.set(func(st *ServerState) { st.Models, st.ModelID = append([]string{}, ids...), ids[0] })
		}
		return nil
	}
	base := "http://127.0.0.1:" + strconv.Itoa(cur.Port)
	tr := &http.Transport{Proxy: nil}
	defer tr.CloseIdleConnections()
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second, Transport: tr}
	}
	current := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.gen == gen && s.st.State == "running"
	}
	for _, id := range remove {
		if err := s.routerCall(ctx, hc, base, cur.APIKey, "/models/unload", id, "model is not running"); err != nil {
			return err
		}
		s.addLine("drydock: unloaded " + id + " (the others stay loaded)")
	}
	if err := os.WriteFile(cur.PresetFile, []byte(preset), 0o644); err != nil {
		return err
	}
	if err := s.reloadList(ctx, hc, base, cur.APIKey); err != nil {
		return err
	}
	if !current() {
		return errStopped
	}
	// the models kept, then the ones loading: Serves sees them all, so none can be removed meanwhile
	s.set(func(st *ServerState) {
		st.Models, st.ModelID, st.Loading = append([]string{}, ids...), ids[0], append([]string{}, add...)
	})
	for _, id := range add {
		if err := s.routerCall(ctx, hc, base, cur.APIKey, "/models/load", id, "model is already running"); err != nil {
			// nothing new was loaded: the server keeps the models it had but the ones unloaded
			var kept []string
			for _, k := range ids {
				if !slices.Contains(add, k) {
					kept = append(kept, k)
				}
			}
			s.mu.Lock()
			if s.gen == gen {
				s.cur.Models = modelsOf(cur.Models, kept)
			}
			s.mu.Unlock()
			s.set(func(st *ServerState) {
				if len(kept) > 0 {
					st.Models, st.ModelID = kept, kept[0]
				}
				st.Loading = nil
			})
			return err
		}
		s.addLine("drydock: loading " + id + " beside the models loaded")
	}
	timeout := l.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		if !current() {
			return errStopped
		}
		ready, ferr := routerReady(wctx, hc, base, cur.APIKey, ids)
		if ferr != nil || wctx.Err() != nil {
			if ferr == nil {
				ferr = fmt.Errorf("the models were not loaded after %s", timeout)
				if ctx.Err() != nil {
					ferr = errStopped
				}
			}
			// what failed is not kept: the server keeps the models it had and the ones that did load
			kept := s.loadedOf(context.WithoutCancel(ctx), hc, base, cur.APIKey, ids)
			s.mu.Lock()
			if s.gen == gen {
				s.cur.Models = modelsOf(l.Models, kept)
				s.cur.Drafts, s.cur.SpecTypes, s.cur.RouterMode = l.Drafts, l.SpecTypes, true
			}
			s.mu.Unlock()
			s.set(func(st *ServerState) {
				if len(kept) > 0 {
					st.Models, st.ModelID = kept, kept[0]
				}
				st.Loading = nil
			})
			return ferr
		}
		if ready {
			break
		}
		select {
		case <-wctx.Done():
		case <-tick.C:
		}
	}
	s.mu.Lock()
	stopped := s.gen != gen
	if !stopped {
		s.cur = l
	}
	s.mu.Unlock()
	if stopped {
		return errStopped
	}
	s.set(func(st *ServerState) {
		st.Models, st.ModelID, st.Loading, st.Router, st.Note = append([]string{}, ids...), ids[0], nil, true, ""
	})
	return nil
}

// sameServer says why a change cannot be made on the server started with cur ("" when it can): the program, the port, the
// cache, the context and the GPU layers are the router's command line, which its instances inherit.
func sameServer(cur, l Launch) string {
	switch {
	case l.Embeddings:
		return "an embedding model is served on its own"
	case cur.Program != l.Program || !slices.Equal(cur.Args, l.Args):
		return "the server program changed"
	case cur.Port != l.Port:
		return "the port changed"
	case cur.CacheType != l.CacheType:
		return "the cache type changed"
	case cur.ContextSize != l.ContextSize:
		return "the context size changed"
	case cur.GPULayers != l.GPULayers:
		return "the GPU layers changed"
	case cur.PresetFile == "" || cur.PresetFile != l.PresetFile:
		return "the router's model list moved"
	}
	return ""
}

// routerCall posts one of the router's calls for a model. 200 is done; a 400 whose message says what is already true (the model
// is not running, or already running) is done too. Anything else, a 404 for the call itself above all, means the server does
// not do it as documented: a *NeedsRestart the person is told about.
func (s *Server) routerCall(ctx context.Context, hc *http.Client, base, key, path, model, already string) error {
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return errStopped
		}
		return &NeedsRestart{Reason: fmt.Sprintf("the server did not answer POST %s: %v", path, err), Unanswered: true}
	}
	defer resp.Body.Close()
	msg := errorMessage(resp.Body)
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusBadRequest && strings.Contains(msg, already):
		return nil
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return &NeedsRestart{Reason: fmt.Sprintf("the server answered POST %s for %s with %d (%s)", path, model, resp.StatusCode, msg), Unanswered: true}
}

// reloadList has the router read its preset again (GET /models?reload=1): the models taken out are dropped from its listing,
// the new ones listed as unloaded. A server that does not answer it so is a *NeedsRestart.
func (s *Server) reloadList(ctx context.Context, hc *http.Client, base, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models?reload=1", nil)
	if err != nil {
		return err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return errStopped
		}
		return &NeedsRestart{Reason: "the server did not answer GET /models?reload=1: " + err.Error(), Unanswered: true}
	}
	defer resp.Body.Close()
	var listing struct {
		Data []json.RawMessage `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&listing) != nil {
		return &NeedsRestart{Reason: fmt.Sprintf("the server answered GET /models?reload=1 with %d", resp.StatusCode), Unanswered: true}
	}
	return nil
}

// loadedOf are the models of ids the router's listing says are loaded (or asleep), in the order of ids.
func (s *Server) loadedOf(ctx context.Context, hc *http.Client, base, key string, ids []string) []string {
	var out []string
	for _, id := range ids {
		if ok, _ := routerReady(ctx, hc, base, key, []string{id}); ok {
			out = append(out, id)
		}
	}
	return out
}

// modelsOf are the models of list whose ids are in ids.
func modelsOf(list []Model, ids []string) []Model {
	var out []Model
	for _, m := range list {
		if slices.Contains(ids, m.ID) {
			out = append(out, m)
		}
	}
	return out
}

// errorMessage reads llama-server's error answer ({"error": {"message": ...}}), or the body's first line.
func errorMessage(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 64<<10))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}
