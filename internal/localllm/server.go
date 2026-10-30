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
// arguments), the models and the settings a person chose. One model is served by a server of its own (-m); several are served
// by llama-server's router mode (decision P111): the router starts one instance per model, listed in a preset file, routes each
// request by its "model" field, and lists every model with its state.
type Launch struct {
	Program string
	Args    []string
	// Model is the one model served; Models, when it holds more than one, are the models the router keeps loaded (Model is
	// then not used).
	Model  Model
	Models []Model
	// Drafts are the draft files of speculative decoding, by the id of the model each drafts for (--model-draft with
	// --spec-type draft-simple, or the type of a file that only drafts).
	Drafts map[string]Model
	// SpecTypes is the kind of speculative decoding of a model, by its id, when it is not draft-simple (decisions P120 and
	// P129): SpecEagle3, SpecDFlash or SpecDSpark for such a draft in Drafts, SpecMTP for a file of MTP heads in Drafts or, with
	// no draft, for a model that drafts with its own MTP heads.
	SpecTypes map[string]string
	// RouterMode runs the router even for one model: a router that kept one model loaded after the others were unloaded stays
	// one, so another can be loaded beside it again without a restart (decision P120).
	RouterMode bool
	// Note is why this start happened, shown with the server's state (ServerState.Note): set when a change of the models kept
	// loaded could not be made on the running server and it was started again instead.
	Note string
	// Embeddings serves an embedding model: --embeddings, which makes the server answer /v1/embeddings and nothing else.
	Embeddings bool
	// PresetFile is where the router's model list is written, and CacheDir the empty folder the router is given as its
	// download cache (LLAMA_CACHE), so it lists no model a person downloaded with llama.cpp by hand: both for router mode only.
	PresetFile  string
	CacheDir    string
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

// gpuEnvNames are what a GPU driver and its loader read, besides the allowlist agents get: Windows' own facts (a display driver
// looks for its files under DRIVERDATA and SYSTEMDRIVE) and the CPU's. None of them carries a credential.
var gpuEnvNames = []string{
	"SYSTEMDRIVE", "DRIVERDATA", "ALLUSERSPROFILE", "PROGRAMW6432", "COMMONPROGRAMFILES", "COMMONPROGRAMFILES(X86)", "COMMONPROGRAMW6432",
	"OS", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "PROCESSOR_IDENTIFIER", "PROCESSOR_LEVEL", "PROCESSOR_REVISION",
	"CUDA_PATH", "CUDA_VISIBLE_DEVICES", "HIP_PATH", "HIP_VISIBLE_DEVICES", "ROCR_VISIBLE_DEVICES",
}

// gpuEnvPrefixes are the families of settings of the Vulkan loader (VK_ICD_FILENAMES, VK_DRIVER_FILES), CUDA's paths by version
// (CUDA_PATH_V12_8) and ggml's own (GGML_VK_VISIBLE_DEVICES): the person's choices of which GPU the program uses.
var gpuEnvPrefixes = []string{"VK_", "CUDA_PATH_", "GGML_"}

// childEnv is the environment llama-server and its probes start with: the allowlist that agents get and what a GPU driver
// needs to be found (without them a Vulkan or CUDA build may see no GPU), none of the app's tokens.
func childEnv() []string {
	return proc.ScrubEnv(os.Environ(), append(append([]string{}, proc.BaseEnvNames...), gpuEnvNames...), gpuEnvPrefixes)
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
	// Autostarted says Drydock started the server when it started (Launch.Autostarted); false when a person did. CacheType and
	// ContextSize are what the server was started with (ContextSize 0: the model's own).
	Autostarted bool   `json:"autostarted"`
	CacheType   string `json:"cacheType,omitempty"`
	ContextSize int    `json:"contextSize,omitempty"`
	// Models are every model the server keeps loaded (ModelID is the first); Router says it runs llama-server's router mode
	// for them; Embeddings that it serves an embedding model (decision P111).
	Models     []string `json:"models,omitempty"`
	Router     bool     `json:"router,omitempty"`
	Embeddings bool     `json:"embeddings,omitempty"`
	// Loading are the models the router is loading beside the ones it keeps (Change; they are among Models meanwhile), Note why
	// the server was started again when a change could not be made on the running one (Launch.Note; decision P120).
	Loading []string `json:"loading,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// Serves reports whether the server keeps the model loaded, or is loading it.
func (st ServerState) Serves(id string) bool {
	if st.State != "running" && st.State != "starting" {
		return false
	}
	for _, m := range st.Models {
		if m == id {
			return true
		}
	}
	return st.ModelID == id
}

// MaxLoaded is how many models the router keeps loaded at once: llama.cpp's own default (--models-max 4), the most a PC that
// has room for them needs. The router is always started with it, not with the number it loads at the start, so a model loaded
// beside them later does not make it unload the one used least recently (decision P120).
const MaxLoaded = 4

// models are the chat models of the launch, the one model first.
func (l Launch) models() []Model {
	if len(l.Models) > 0 {
		return l.Models
	}
	return []Model{l.Model}
}

// Router reports whether the launch runs llama-server's router mode: two models or more, or one when RouterMode says so.
func (l Launch) Router() bool { return len(l.Models) > 1 || (l.RouterMode && len(l.Models) == 1) }

// Server runs one llama-server at a time.
type Server struct {
	PM   proc.Manager
	HTTP *http.Client
	// OnChange is told every state change (the core publishes it).
	OnChange func(ServerState)

	mu    sync.Mutex
	st    ServerState
	cur   Launch // what the current process was started with (or changed to since, by Change)
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

// The kinds of speculative decoding Drydock runs (llama-server --spec-type), named on every start: llama.cpp infers none from
// a plain draft model's header, and the TurboQuant build infers none at all (its common/arg.cpp at b10298-2.0.0 has no
// inference from the draft's header). Both builds Drydock installs have all five (decisions P120 and P129: the types in their
// common/speculative.cpp, the examples in their docs/speculative.md).
const (
	// SpecSimple is a smaller model of the same family drafting (--model-draft).
	SpecSimple = "draft-simple"
	// SpecEagle3 is an EAGLE-3 draft (--model-draft): a one-layer model trained for one target, reading its hidden states.
	SpecEagle3 = "draft-eagle3"
	// SpecMTP is multi-token prediction (MTP, NextN) heads drafting: the model's own (no draft file), or a file of heads alone
	// (--model-draft: the TurboQuant build's --mtp-head is another name for it, and the official build has only --model-draft).
	SpecMTP = "draft-mtp"
	// SpecDFlash is a DFlash draft (--model-draft): a small block-diffusion model trained for one target that drafts a whole
	// block of tokens in one pass. SpecDSpark is a DFlash draft with a Markov head.
	SpecDFlash = "draft-dflash"
	SpecDSpark = "draft-dspark"
)

// SpecOf is the --spec-type a draft-only kind of file runs with (GGUFInfo.DraftOnly); "" for a draft model (draft-simple).
func SpecOf(kind string) string {
	switch kind {
	case DraftOnlyEagle3:
		return SpecEagle3
	case DraftOnlyDFlash:
		return SpecDFlash
	case DraftOnlyDSpark:
		return SpecDSpark
	case DraftOnlyMTPHead:
		return SpecMTP
	}
	return ""
}

// spec is the kind of speculative decoding a model of the launch runs ("" for none) and the draft file it loads for it ("" for
// none: the model's own MTP heads).
func (l Launch) spec(id string) (sp, draft string) {
	if d, ok := l.Drafts[id]; ok && len(d.Files) > 0 {
		switch t := l.SpecTypes[id]; t {
		case SpecEagle3, SpecDFlash, SpecDSpark, SpecMTP:
			return t, d.Files[0]
		}
		return SpecSimple, d.Files[0]
	}
	if l.SpecTypes[id] == SpecMTP {
		return SpecMTP, ""
	}
	return "", ""
}

// Arguments is the command line Start uses, for tests and the page. One model: -m with its alias, its projector and its draft.
// Several: the router with the preset file (Preset), each model's own options in it; the options every model shares (the GPU
// layers, the cache, the context) are on the router's command line, which its instances inherit.
func Arguments(l Launch) []string {
	args := append([]string{}, l.Args...)
	args = append(args, "--host", "127.0.0.1", "--port", strconv.Itoa(l.Port))
	if l.Router() {
		args = append(args, "--models-preset", l.PresetFile, "--models-max", strconv.Itoa(MaxLoaded))
	} else {
		m := l.models()[0]
		args = append(args, "-m", m.Files[0], "--alias", m.ID)
		if l.Embeddings {
			args = append(args, "--embeddings")
		} else {
			if m.Projector != "" {
				args = append(args, "--mmproj", m.Projector)
			}
			sp, draft := l.spec(m.ID)
			if draft != "" {
				args = append(args, "--model-draft", draft)
			}
			if sp != "" {
				args = append(args, "--spec-type", sp)
			}
		}
	}
	if !l.Embeddings {
		args = append(args, "--jinja")
	}
	args = append(args, "--no-webui")
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

// Preset is the router's model list (llama-server --models-preset): a section per model, named by its id (which becomes the
// model's name in the router's listing and in requests), with its file, its projector and its draft, loaded at the start.
// llama.cpp's preset reader ends a value at a ";" or "#" and a line at a line break, so a path with one cannot be written: it
// is refused rather than cut short.
func Preset(l Launch) (string, error) {
	var b strings.Builder
	b.WriteString("; written by Drydock for llama-server's router mode: each section is a model it keeps loaded\nversion = 1\n")
	for _, m := range l.Models {
		sec, err := section(l, m)
		if err != nil {
			return "", err
		}
		b.WriteString(sec)
	}
	return b.String(), nil
}

// section is one model's section of the preset: its id, its file, its projector, its draft and kind of speculative decoding.
func section(l Launch, m Model) (string, error) {
	var b strings.Builder
	value := func(k, v string) error {
		if strings.ContainsAny(v, ";#\r\n") {
			return fmt.Errorf("the path %q holds a character (; # or a line break) llama-server's model list cannot carry: move Drydock's data folder", v)
		}
		fmt.Fprintf(&b, "%s = %s\n", k, v)
		return nil
	}
	if strings.ContainsAny(m.ID, "[]\r\n;#") || len(m.Files) == 0 {
		return "", fmt.Errorf("%q cannot be listed for llama-server's router", m.ID)
	}
	fmt.Fprintf(&b, "\n[%s]\n", m.ID)
	if err := value("model", m.Files[0]); err != nil {
		return "", err
	}
	if m.Projector != "" {
		if err := value("mmproj", m.Projector); err != nil {
			return "", err
		}
	}
	sp, draft := l.spec(m.ID)
	if draft != "" {
		if err := value("model-draft", draft); err != nil {
			return "", err
		}
	}
	if sp != "" {
		_ = value("spec-type", sp)
	}
	_ = value("load-on-startup", "true")
	return b.String(), nil
}

// Start stops a server that runs, starts llama-server with the model and waits until /health answers 200 (the model is
// loaded). A program that ends first, or one that is not ready in time, is reported as failed with its last lines.
func (s *Server) Start(ctx context.Context, l Launch) error {
	models := l.models()
	ids := make([]string, 0, len(models))
	for _, m := range models {
		if len(m.Files) == 0 {
			return errors.New("the model has no file")
		}
		ids = append(ids, m.ID)
	}
	if l.Port <= 0 || l.Port > 65535 {
		return fmt.Errorf("%d is not a port", l.Port)
	}
	if l.Router() {
		if len(l.Models) > MaxLoaded {
			return fmt.Errorf("%d models at once is more than the %d the server keeps loaded", len(l.Models), MaxLoaded)
		}
		if l.Embeddings {
			return errors.New("an embedding model is served on its own")
		}
		if l.PresetFile == "" {
			return errors.New("router mode needs a preset file")
		}
		preset, err := Preset(l)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(l.PresetFile), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(l.PresetFile, []byte(preset), 0o644); err != nil {
			return err
		}
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
	s.cur = l
	s.mu.Unlock()
	base := "http://127.0.0.1:" + strconv.Itoa(l.Port)
	s.set(func(st *ServerState) {
		*st = ServerState{State: "starting", ModelID: ids[0], Port: l.Port, BaseURL: base + "/v1", StartedAt: time.Now().UTC(),
			Autostarted: l.Autostarted, CacheType: l.CacheType, ContextSize: l.ContextSize, Models: ids, Router: l.Router(), Embeddings: l.Embeddings,
			Note: l.Note}
	})
	if err := portFree(l.Port); err != nil {
		s.set(func(st *ServerState) { st.State, st.Error = "failed", err.Error() })
		return err
	}
	env := childEnv()
	if l.APIKey != "" {
		env = append(env, "LLAMA_API_KEY="+l.APIKey)
	}
	if l.Router() && l.CacheDir != "" && os.MkdirAll(l.CacheDir, 0o755) == nil {
		env = append(env, "LLAMA_CACHE="+l.CacheDir)
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
				if l.Router() {
					// the router answers at once; the models are ready when its listing says each is loaded
					ready, ferr := routerReady(wctx, hc, base, l.APIKey, ids)
					if ferr != nil {
						_ = p.Kill(true)
						<-done
						if !current() {
							return errStopped
						}
						ferr = fmt.Errorf("%w: %s", ferr, s.tail())
						s.set(func(st *ServerState) { st.State, st.Error = "failed", ferr.Error() })
						return ferr
					}
					if !ready {
						continue
					}
				}
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

// routerReady reads the router's model listing: ready when every model is loaded; an error when one failed to load (its
// instance ended), since the router then waits for a request to try again, which Drydock would never send.
func routerReady(ctx context.Context, hc *http.Client, base, key string, ids []string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return false, nil
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	var listing struct {
		Data []struct {
			ID     string `json:"id"`
			Status struct {
				Value    string `json:"value"`
				Failed   bool   `json:"failed"`
				ExitCode int    `json:"exit_code"`
			} `json:"status"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&listing) != nil {
		return false, nil
	}
	state := map[string]string{}
	for _, d := range listing.Data {
		if d.Status.Failed {
			if contains(ids, d.ID) {
				return false, fmt.Errorf("%s could not be loaded (its instance ended with code %d)", d.ID, d.Status.ExitCode)
			}
			continue
		}
		state[d.ID] = d.Status.Value
	}
	for _, id := range ids {
		if v := state[id]; v != "loaded" && v != "sleeping" {
			return false, nil
		}
	}
	return true, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
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
