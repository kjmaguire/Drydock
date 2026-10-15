package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/runners"
)

// Local models (decision P65, docs/local-models.md): llama.cpp on this PC, kept by internal/localllm. A person installs an
// official build, downloads GGUF models from Hugging Face and starts llama-server on one of them; the server is registered
// as a provider (kind host, flavor llamacpp) so the router and the agents use it like any other. Installs and downloads run in
// the background here, their progress is read with local.get and announced with the event local.changed. Only a person
// installs, downloads, starts or stops: these bring programs and gigabytes onto the PC and take its GPU.

const (
	settingLocal      = "local:settings"
	defaultLocalPort  = 9933 // one above llama.cpp's own default, so a llama-server a person runs by hand keeps its port
	localProviderName = "Models on this PC (llama.cpp)"
)

// LocalSettings are the person's choices.
type LocalSettings struct {
	Source      string           `json:"source"`      // official | turboquant
	Backend     localllm.Backend `json:"backend"`     // "" until chosen: the suggested one is used
	CacheType   string           `json:"cacheType"`   // q8_0 (default) | q4_0 | f16 | turbo4 | turbo3 (fork only)
	ContextSize int              `json:"contextSize"` // 0: the model's own
	Port        int              `json:"port"`
	ModelID     string           `json:"modelId"`    // the model the server serves (the last one started)
	ProviderID  string           `json:"providerId"` // the provider row the server is registered as
	Autostart   bool             `json:"autostart"`  // start the server with Drydock when a model was served before
}

// LocalJob is an install or a download in progress, or how the last one ended.
type LocalJob struct {
	Kind    string    `json:"kind"` // install | download
	Target  string    `json:"target"`
	State   string    `json:"state"` // running | done | failed | cancelled
	Phase   string    `json:"phase,omitempty"`
	File    string    `json:"file,omitempty"`
	Done    int64     `json:"done"`
	Total   int64     `json:"total"`
	Error   string    `json:"error,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
}

// LocalRuntime is the build on this PC.
type LocalRuntime struct {
	Installed   bool             `json:"installed"`
	Source      string           `json:"source,omitempty"`
	SourceName  string           `json:"sourceName,omitempty"`
	Tag         string           `json:"tag,omitempty"`
	Backend     localllm.Backend `json:"backend,omitempty"`
	Server      string           `json:"server,omitempty"`
	InstalledAt time.Time        `json:"installedAt,omitempty"`
	Devices     localllm.Devices `json:"devices"`
	Job         *LocalJob        `json:"job,omitempty"`
}

// LocalView is the Providers page's "Models on this PC" section.
type LocalView struct {
	Supported        bool                   `json:"supported"`
	Reason           string                 `json:"reason,omitempty"`
	Runtime          LocalRuntime           `json:"runtime"`
	Sources          []localllm.Source      `json:"sources"`
	Backends         []localllm.BackendInfo `json:"backends"`
	SuggestedBackend localllm.Backend       `json:"suggestedBackend"`
	Models           []localllm.Model       `json:"models"`
	Downloads        []LocalJob             `json:"downloads"`
	Server           localllm.ServerState   `json:"server"`
	Settings         LocalSettings          `json:"settings"`
	Memory           localllm.Memory        `json:"memory"`
	FreeBytes        uint64                 `json:"freeBytes"`
	ModelsBytes      uint64                 `json:"modelsBytes"`
	Log              []string               `json:"log"`
}

func init() {
	RegisterExtension(Extension{Name: "localmodels", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		l := &localExt{c: c, downloads: map[string]*localDownload{}}
		l.m = localllm.NewManager(filepath.Join(c.cfg.DataDir, "local-models"), c.Deps.PM)
		if c.cfg.LocalModels != nil {
			c.cfg.LocalModels(l.m)
		}
		l.m.Finish()
		l.m.Server.OnChange = func(st localllm.ServerState) {
			c.publish(context.Background(), "local.changed", "", map[string]any{"what": "server", "state": st.State})
		}
		l.autostart(ctx)
		return map[string]Method{
			"local.get": bind(func(ctx context.Context, _ struct{}) (any, error) {
				return l.view(ctx), nil
			}),
			"local.install": bind(func(ctx context.Context, p struct {
				Source  string           `json:"source"`
				Backend localllm.Backend `json:"backend"`
			}) (any, error) {
				return l.install(ctx, p.Source, p.Backend, c.Human())
			}),
			"local.runtime.remove": bind(func(ctx context.Context, _ struct{}) (any, error) {
				return l.removeRuntime(ctx, c.Human())
			}),
			"local.settings.set": bind(func(ctx context.Context, p LocalSettingsEdit) (any, error) {
				return l.setSettings(ctx, p, c.Human())
			}),
			"local.models.suggested": bind(func(ctx context.Context, _ struct{}) (any, error) {
				return l.m.Hub.Suggested(ctx)
			}),
			"local.models.search": bind(func(ctx context.Context, p struct {
				Query string `json:"query"`
			}) (any, error) {
				return l.m.Hub.Search(ctx, p.Query)
			}),
			"local.model.variants": bind(func(ctx context.Context, p struct {
				Repo string `json:"repo"`
			}) (any, error) {
				return l.variants(ctx, p.Repo)
			}),
			"local.model.download": bind(func(ctx context.Context, p struct {
				Repo    string `json:"repo"`
				Variant string `json:"variant"`
			}) (any, error) {
				return l.download(ctx, p.Repo, p.Variant, c.Human())
			}),
			"local.model.download.cancel": bind(func(ctx context.Context, p struct {
				ID string `json:"id"`
			}) (any, error) {
				return l.cancelDownload(ctx, p.ID)
			}),
			"local.model.remove": bind(func(ctx context.Context, p struct {
				ID string `json:"id"`
			}) (any, error) {
				return l.removeModel(ctx, p.ID, c.Human())
			}),
			"local.server.start": bind(func(ctx context.Context, p struct {
				ModelID string `json:"modelId"`
			}) (any, error) {
				return l.startServer(ctx, p.ModelID, c.Human())
			}),
			"local.server.stop": bind(func(ctx context.Context, _ struct{}) (any, error) {
				return l.stopServer(ctx, c.Human())
			}),
		}, l.close, nil
	}})
}

// LocalSettingsEdit changes the settings a person sends (nil leaves one as it is).
type LocalSettingsEdit struct {
	Source      *string           `json:"source"`
	Backend     *localllm.Backend `json:"backend"`
	CacheType   *string           `json:"cacheType"`
	ContextSize *int              `json:"contextSize"`
	Port        *int              `json:"port"`
	Autostart   *bool             `json:"autostart"`
}

type localExt struct {
	c *Core
	m *localllm.Manager

	mu        sync.Mutex
	job       *LocalJob // the install running or last run
	jobCancel context.CancelFunc
	downloads map[string]*localDownload
	devices   localllm.Devices
	devFor    string // the server program the devices were read for
	starting  bool
}

type localDownload struct {
	job    LocalJob
	cancel context.CancelFunc
}

func (l *localExt) close() {
	l.mu.Lock()
	if l.jobCancel != nil {
		l.jobCancel()
	}
	for _, d := range l.downloads {
		d.cancel()
	}
	l.mu.Unlock()
	l.m.Server.Stop()
}

func (l *localExt) changed(what string, more map[string]any) {
	data := map[string]any{"what": what}
	for k, v := range more {
		data[k] = v
	}
	l.c.publish(context.Background(), "local.changed", "", data)
}

// settings reads the person's choices with the defaults filled in.
func (l *localExt) settings(ctx context.Context) LocalSettings {
	s := LocalSettings{Source: localllm.SourceOfficial, CacheType: "q8_0", Port: defaultLocalPort, Autostart: true}
	var v string
	if err := l.c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingLocal).Scan(&v); err == nil {
		_ = json.Unmarshal([]byte(v), &s)
	}
	if _, ok := localllm.SourceByID(s.Source); !ok {
		s.Source = localllm.SourceOfficial
	}
	if s.Port <= 0 {
		s.Port = defaultLocalPort
	}
	if s.CacheType == "" {
		s.CacheType = "q8_0"
	}
	return s
}

func (l *localExt) save(ctx context.Context, s LocalSettings) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return l.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingLocal, string(b))
		return err
	})
}

func (l *localExt) setSettings(ctx context.Context, e LocalSettingsEdit, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may change the local models' settings")
	}
	s := l.settings(ctx)
	if e.Source != nil {
		if _, ok := localllm.SourceByID(*e.Source); !ok {
			return LocalView{}, fmt.Errorf("%q is not a build source", *e.Source)
		}
		s.Source = *e.Source
	}
	if e.Backend != nil {
		if *e.Backend != "" && !e.Backend.Valid() {
			return LocalView{}, fmt.Errorf("%q is not a backend", *e.Backend)
		}
		s.Backend = *e.Backend
	}
	if e.CacheType != nil {
		src, _ := localllm.SourceByID(s.Source)
		if !src.CacheType(*e.CacheType) {
			return LocalView{}, fmt.Errorf("%q is not a KV cache type of %s (one of %s)", *e.CacheType, src.Name, strings.Join(src.CacheTypes, ", "))
		}
		s.CacheType = *e.CacheType
	}
	if e.ContextSize != nil {
		if *e.ContextSize < 0 || *e.ContextSize > 4_000_000 || (*e.ContextSize > 0 && *e.ContextSize < 1024) {
			return LocalView{}, errors.New("the context size is tokens: 0 for the model's own, else at least 1024")
		}
		s.ContextSize = *e.ContextSize
	}
	if e.Port != nil {
		if *e.Port < 1024 || *e.Port > 65535 {
			return LocalView{}, errors.New("the port must be between 1024 and 65535")
		}
		s.Port = *e.Port
	}
	if e.Autostart != nil {
		s.Autostart = *e.Autostart
	}
	if err := l.save(ctx, s); err != nil {
		return LocalView{}, err
	}
	l.changed("settings", nil)
	return l.view(ctx), nil
}

func (l *localExt) view(ctx context.Context) LocalView {
	s := l.settings(ctx)
	v := LocalView{Supported: l.m.Supported(), Sources: localllm.Sources, SuggestedBackend: localllm.SuggestBackend(), Settings: s,
		Server: l.m.Server.State(), Memory: l.m.Memory(), Log: l.m.Server.Log(), Downloads: []LocalJob{}}
	if !v.Supported {
		v.Reason = "Local models run on Windows only."
	}
	for _, b := range localllm.Backends {
		v.Backends = append(v.Backends, localllm.Describe(b))
	}
	if in, ok := l.m.Runtime.Installed(); ok {
		src, _ := localllm.SourceByID(in.Source)
		v.Runtime = LocalRuntime{Installed: true, Source: in.Source, SourceName: src.Name, Tag: in.Tag, Backend: in.Backend, Server: in.Server, InstalledAt: in.InstalledAt}
		v.Runtime.Devices = l.devicesOf(ctx, in)
	}
	l.mu.Lock()
	if l.job != nil {
		j := *l.job
		v.Runtime.Job = &j
	}
	for _, d := range l.downloads {
		v.Downloads = append(v.Downloads, d.job)
	}
	l.mu.Unlock()
	if v.Runtime.Devices.Devices == nil {
		v.Runtime.Devices.Devices = []localllm.Device{}
	}
	v.Models = l.m.Store.List()
	for i := range v.Models {
		f := localllm.FitOf(v.Models[i].Size, v.Runtime.Devices, v.Memory)
		v.Models[i].Fit = &f
		v.ModelsBytes += v.Models[i].Size
	}
	if free, err := runners.FreeBytes(l.m.Root); err == nil {
		v.FreeBytes = free
	} else if free, err := runners.FreeBytes(l.c.cfg.DataDir); err == nil {
		v.FreeBytes = free
	}
	return v
}

// devicesOf asks the installed build for its devices once per build (the answer is kept until the build changes).
func (l *localExt) devicesOf(ctx context.Context, in localllm.Installed) localllm.Devices {
	l.mu.Lock()
	if l.devFor == in.Server && l.devices.Probed {
		d := l.devices
		l.mu.Unlock()
		return d
	}
	l.mu.Unlock()
	prog, args := l.m.ServerProgram(in)
	d := localllm.ListDevices(ctx, l.m.PM, prog, args)
	l.mu.Lock()
	l.devices, l.devFor = d, in.Server
	l.mu.Unlock()
	return d
}

// install downloads and unpacks the newest build of the source for the backend, in the background; local.changed says how
// far it is. The server is stopped first when it runs on the build being replaced.
func (l *localExt) install(ctx context.Context, source string, backend localllm.Backend, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may install the local models' runtime")
	}
	if !l.m.Supported() {
		return LocalView{}, errors.New("local models run on Windows only")
	}
	s := l.settings(ctx)
	if source != "" {
		if _, ok := localllm.SourceByID(source); !ok {
			return LocalView{}, fmt.Errorf("%q is not a build source", source)
		}
		s.Source = source
	}
	if backend != "" {
		if !backend.Valid() {
			return LocalView{}, fmt.Errorf("%q is not a backend", backend)
		}
		s.Backend = backend
	}
	if s.Backend == "" {
		s.Backend = localllm.SuggestBackend()
	}
	src, _ := localllm.SourceByID(s.Source)
	if !src.Supports(s.Backend) {
		return LocalView{}, fmt.Errorf("%s has no Windows build for %s", src.Name, localllm.Describe(s.Backend).Label)
	}
	if !src.CacheType(s.CacheType) {
		s.CacheType = src.CacheTypes[0]
	}
	l.mu.Lock()
	if l.job != nil && l.job.State == "running" {
		l.mu.Unlock()
		return LocalView{}, errors.New("an install is already running")
	}
	jctx, cancel := context.WithCancel(context.Background())
	l.job = &LocalJob{Kind: "install", Target: src.Name + " for " + localllm.Describe(s.Backend).Label, State: "running", Phase: "looking up the newest build", Started: time.Now().UTC()}
	l.jobCancel = cancel
	l.mu.Unlock()
	if err := l.save(ctx, s); err != nil {
		cancel()
		return LocalView{}, err
	}
	l.changed("install", map[string]any{"state": "running"})
	go l.runInstall(jctx, src, s.Backend)
	return l.view(ctx), nil
}

func (l *localExt) runInstall(ctx context.Context, src localllm.Source, backend localllm.Backend) {
	finish := func(err error) {
		l.mu.Lock()
		l.job.Ended = time.Now().UTC()
		switch {
		case err == nil:
			l.job.State = "done"
			if !strings.HasPrefix(l.job.Phase, "already") {
				l.job.Phase = "done"
			}
		case errors.Is(err, context.Canceled):
			l.job.State, l.job.Error = "cancelled", "cancelled"
		default:
			l.job.State, l.job.Error = "failed", err.Error()
		}
		st := l.job.State
		l.jobCancel = nil
		l.mu.Unlock()
		l.changed("install", map[string]any{"state": st})
	}
	rel, err := localllm.LatestRelease(ctx, l.m.GitHub, src, backend)
	if err != nil {
		finish(err)
		return
	}
	if in, ok := l.m.Runtime.Installed(); ok && in.Source == rel.Source && in.Tag == rel.Tag && in.Backend == rel.Backend {
		l.mu.Lock()
		l.job.Phase = "already the newest build (" + rel.Tag + ")"
		l.mu.Unlock()
		finish(nil)
		return
	}
	// the server runs the build about to be replaced
	if st := l.m.Server.State(); st.State == "running" || st.State == "starting" {
		l.m.Server.Stop()
	}
	last := time.Time{}
	in, err := l.m.Runtime.Install(ctx, rel, func(p localllm.Progress) {
		l.mu.Lock()
		l.job.Phase, l.job.File, l.job.Done, l.job.Total = p.Phase+" "+rel.Tag, p.File, p.Done, p.Total
		l.mu.Unlock()
		if time.Since(last) > 700*time.Millisecond || p.Phase != "downloading" {
			last = time.Now()
			l.changed("install", map[string]any{"state": "running", "phase": p.Phase})
		}
	})
	if err != nil {
		finish(err)
		return
	}
	l.mu.Lock()
	l.devices, l.devFor = localllm.Devices{}, ""
	l.mu.Unlock()
	_ = in
	finish(nil)
}

func (l *localExt) removeRuntime(ctx context.Context, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may remove the local models' runtime")
	}
	l.mu.Lock()
	running := l.job != nil && l.job.State == "running"
	l.mu.Unlock()
	if running {
		return LocalView{}, errors.New("an install is running")
	}
	l.m.Server.Stop()
	if err := l.m.Runtime.Remove(); err != nil {
		return LocalView{}, err
	}
	l.mu.Lock()
	l.devices, l.devFor, l.job = localllm.Devices{}, "", nil
	l.mu.Unlock()
	l.changed("install", map[string]any{"state": "removed"})
	return l.view(ctx), nil
}

// variants lists a repository's downloadable files with whether each fits this PC.
func (l *localExt) variants(ctx context.Context, repo string) ([]localllm.Variant, error) {
	vs, err := l.m.Hub.Variants(ctx, repo)
	if err != nil {
		return nil, err
	}
	var dev localllm.Devices
	if in, ok := l.m.Runtime.Installed(); ok {
		dev = l.devicesOf(ctx, in)
	}
	mem := l.m.Memory()
	for i := range vs {
		f := localllm.FitOf(vs[i].Size, dev, mem)
		vs[i].Fit = &f
	}
	return vs, nil
}

// download fetches a repository's variant in the background. Several may run; the same one twice is refused.
func (l *localExt) download(ctx context.Context, repo, variant string, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may download a model")
	}
	if !localllm.ValidRepo(repo) {
		return LocalView{}, fmt.Errorf("%q is not a repository (owner/name)", repo)
	}
	vs, err := l.m.Hub.Variants(ctx, repo)
	if err != nil {
		return LocalView{}, err
	}
	var v *localllm.Variant
	for i := range vs {
		if vs[i].Name == variant {
			v = &vs[i]
		}
	}
	if v == nil {
		return LocalView{}, fmt.Errorf("%s has no file named %s", repo, variant)
	}
	id := localllm.ModelID(repo, variant)
	if free, err := runners.FreeBytes(l.m.Root); err == nil && v.Size+(2<<30) > free {
		return LocalView{}, fmt.Errorf("%s needs %d GB and the drive has %d GB free", id, v.Size>>30, free>>30)
	}
	l.mu.Lock()
	if d, ok := l.downloads[id]; ok && d.job.State == "running" {
		l.mu.Unlock()
		return LocalView{}, fmt.Errorf("%s is already downloading", id)
	}
	dctx, cancel := context.WithCancel(context.Background())
	d := &localDownload{job: LocalJob{Kind: "download", Target: id, State: "running", Total: int64(v.Size), Started: time.Now().UTC()}, cancel: cancel}
	l.downloads[id] = d
	l.mu.Unlock()
	l.changed("download", map[string]any{"id": id, "state": "running"})
	go l.runDownload(dctx, repo, *v, id)
	return l.view(ctx), nil
}

func (l *localExt) runDownload(ctx context.Context, repo string, v localllm.Variant, id string) {
	dir := l.m.Store.VariantDir(repo, v.Name)
	last := time.Time{}
	err := l.m.Hub.Download(ctx, repo, v, dir, func(p localllm.DownloadProgress) {
		l.mu.Lock()
		d := l.downloads[id]
		d.job.File, d.job.Done, d.job.Total = p.File, int64(p.Done), int64(p.Total)
		l.mu.Unlock()
		if time.Since(last) > 700*time.Millisecond {
			last = time.Now()
			l.changed("download", map[string]any{"id": id, "state": "running"})
		}
	})
	if err == nil {
		_, err = l.m.Store.Put(repo, v)
	}
	l.mu.Lock()
	d := l.downloads[id]
	d.job.Ended = time.Now().UTC()
	switch {
	case err == nil:
		d.job.State = "done"
		d.job.Done = d.job.Total
	case errors.Is(err, context.Canceled):
		d.job.State, d.job.Error = "cancelled", "cancelled"
	default:
		d.job.State, d.job.Error = "failed", err.Error()
	}
	st := d.job.State
	l.mu.Unlock()
	l.changed("download", map[string]any{"id": id, "state": st})
}

func (l *localExt) cancelDownload(ctx context.Context, id string) (LocalView, error) {
	l.mu.Lock()
	d, ok := l.downloads[id]
	if ok && d.job.State == "running" {
		d.cancel()
	} else if ok {
		delete(l.downloads, id) // clears a finished or failed row from the page
	}
	l.mu.Unlock()
	if !ok {
		return LocalView{}, fmt.Errorf("no download of %s", id)
	}
	return l.view(ctx), nil
}

func (l *localExt) removeModel(ctx context.Context, id string, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may remove a model")
	}
	if st := l.m.Server.State(); st.ModelID == id && (st.State == "running" || st.State == "starting") {
		return LocalView{}, fmt.Errorf("%s is being served: stop the server first", id)
	}
	l.mu.Lock()
	if d, ok := l.downloads[id]; ok {
		if d.job.State == "running" {
			d.cancel()
		}
		delete(l.downloads, id)
	}
	l.mu.Unlock()
	if err := l.m.Store.Remove(id); err != nil {
		return LocalView{}, err
	}
	s := l.settings(ctx)
	if s.ModelID == id {
		s.ModelID = ""
		_ = l.save(ctx, s)
	}
	l.changed("models", map[string]any{"id": id})
	return l.view(ctx), nil
}

// startServer serves a downloaded model: it waits for the model to load, then makes sure the provider row exists and reads
// its catalog, so the model is routable at once.
func (l *localExt) startServer(ctx context.Context, modelID string, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may start the local model server")
	}
	if err := l.serve(ctx, modelID); err != nil {
		return LocalView{}, err
	}
	return l.view(ctx), nil
}

func (l *localExt) serve(ctx context.Context, modelID string) error {
	in, ok := l.m.Runtime.Installed()
	if !ok {
		return errors.New("no llama.cpp build is installed: install one first")
	}
	s := l.settings(ctx)
	if modelID == "" {
		modelID = s.ModelID
	}
	m, err := l.m.Store.Get(modelID)
	if err != nil {
		return err
	}
	l.mu.Lock()
	if l.starting {
		l.mu.Unlock()
		return errors.New("the server is already starting")
	}
	l.starting = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.starting = false
		l.mu.Unlock()
	}()
	src, _ := localllm.SourceByID(in.Source)
	cache := s.CacheType
	if !src.CacheType(cache) {
		cache = src.CacheTypes[0]
	}
	prog, args := l.m.ServerProgram(in)
	launch := localllm.Launch{Program: prog, Args: args, Model: m, Port: s.Port, CacheType: cache, ContextSize: s.ContextSize, LogDir: l.m.LogDir()}
	if err := l.m.Server.Start(ctx, launch); err != nil {
		return err
	}
	s.ModelID = modelID
	if id, err := l.ensureProvider(ctx, s); err != nil {
		l.c.logf("local models: provider: %v", err)
	} else {
		s.ProviderID = id
		if _, err := l.c.Prober.Probe(ctx, id); err != nil {
			l.c.logf("local models: probe: %v", err)
		}
		_, _ = l.c.Health.CheckNow(ctx, id)
	}
	if err := l.save(ctx, s); err != nil {
		return err
	}
	l.changed("server", map[string]any{"state": "running", "modelId": modelID})
	return nil
}

// ensureProvider makes the server a provider: one row, kept at the server's address.
func (l *localExt) ensureProvider(ctx context.Context, s LocalSettings) (string, error) {
	base := fmt.Sprintf("http://127.0.0.1:%d/v1", s.Port)
	if s.ProviderID != "" {
		p, err := l.c.Providers.Get(ctx, s.ProviderID)
		if err == nil {
			if p.BaseURL != base || p.Flavor != providers.FlavorLlamaCpp {
				p.BaseURL, p.Flavor = base, providers.FlavorLlamaCpp
				if _, err := l.c.Providers.Update(ctx, p, ""); err != nil {
					return "", err
				}
			}
			if !p.Enabled {
				_ = l.c.Providers.SetEnabled(ctx, p.ID, true)
			}
			return p.ID, nil
		}
		if !errors.Is(err, providers.ErrNotFound) {
			return "", err
		}
	}
	// a row from before, found by its address (the settings were lost or the row was made by hand)
	if list, err := l.c.Providers.List(ctx); err == nil {
		for _, p := range list {
			if p.Kind == providers.KindHost && p.BaseURL == base {
				return p.ID, nil
			}
		}
	}
	p, err := l.c.Providers.Create(ctx, providers.NewProvider{Name: localProviderName, Kind: providers.KindHost, BaseURL: base,
		DataClass: providers.DataPrivate, Tier: providers.TierCheap, Flavor: providers.FlavorLlamaCpp, MaxConcurrent: 1})
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

func (l *localExt) stopServer(ctx context.Context, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may stop the local model server")
	}
	l.m.Server.Stop()
	s := l.settings(ctx)
	if s.ProviderID != "" {
		_, _ = l.c.Health.CheckNow(ctx, s.ProviderID)
	}
	l.changed("server", map[string]any{"state": "stopped"})
	return l.view(ctx), nil
}

// autostart serves the model served last time, when the person left that on.
func (l *localExt) autostart(ctx context.Context) {
	s := l.settings(ctx)
	if !s.Autostart || s.ModelID == "" || !l.m.Supported() {
		return
	}
	if _, ok := l.m.Runtime.Installed(); !ok {
		return
	}
	go func() {
		if err := l.serve(context.Background(), s.ModelID); err != nil {
			l.c.logf("local models: autostart: %v", err)
		}
	}()
}
