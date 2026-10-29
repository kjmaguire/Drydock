package localllm

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/proc"
)

// Manager bundles the pieces the core drives: the build on disk, the Hub, the model store and the one server. Tests and the
// e2e harness replace GitHub, the Hub, the download client and the server program with labelled doubles through the core's
// Config.LocalModels hook.
type Manager struct {
	Root string
	PM   proc.Manager
	// GitHub reads the release listings (no sign-in: the repositories are public). Default: api.github.com directly.
	GitHub host.API
	// HTTP downloads the build archives. Default: a client with a 30-minute timeout per archive.
	HTTP    *http.Client
	Hub     *Hub
	Runtime *Runtime
	Store   *Store
	Server  *Server
	// Embed serves the embedding model, on its own port (decision P111): llama-server's --embeddings makes a server answer
	// embeddings and nothing else, so it is never the chat server.
	Embed *Server
	// Program, when set, replaces the installed build's server program (the stand-in of internal/localllm/llamatest); the
	// leading arguments it returns go before the server's own.
	Program func(installed Installed) (string, []string)
	// Memory reads the PC's RAM (default SystemMemory).
	Memory func() Memory
}

// NewManager wires the default pieces under root.
func NewManager(root string, pm proc.Manager) *Manager {
	if pm == nil {
		pm = proc.New()
	}
	hc := &http.Client{Timeout: 30 * time.Minute}
	m := &Manager{Root: root, PM: pm, GitHub: host.HTTPAPI{BaseURL: "https://api.github.com"}, HTTP: hc, Hub: &Hub{}, Memory: SystemMemory}
	m.Runtime = &Runtime{Root: root, HTTP: hc}
	m.Store = &Store{Dir: filepath.Join(root, "models")}
	m.Server = &Server{PM: pm}
	m.Embed = &Server{PM: pm}
	return m
}

// Finish applies the defaults a hook may have changed (the download client to the runtime).
func (m *Manager) Finish() {
	m.Runtime.HTTP = m.HTTP
	if m.Server.PM == nil {
		m.Server.PM = m.PM
	}
	if m.Embed == nil {
		m.Embed = &Server{}
	}
	if m.Embed.PM == nil {
		m.Embed.PM = m.PM
	}
	if m.Memory == nil {
		m.Memory = SystemMemory
	}
}

// Supported says whether builds can run here: Windows, or anywhere a stand-in program is injected.
func (m *Manager) Supported() bool { return runtime.GOOS == "windows" || m.Program != nil }

// ServerProgram is the program to start for the installed build and its leading arguments.
func (m *Manager) ServerProgram(in Installed) (string, []string) {
	if m.Program != nil {
		return m.Program(in)
	}
	return in.Server, nil
}

// LogDir is where the server's log goes (the embedding server's in its embeddings folder).
func (m *Manager) LogDir() string { return filepath.Join(m.Root, "logs") }

// PresetFile is where the router's model list is written, and RouterCache the empty folder the router takes as its cache.
func (m *Manager) PresetFile() string  { return filepath.Join(m.Root, "router", "models.ini") }
func (m *Manager) RouterCache() string { return filepath.Join(m.Root, "router", "cache") }

// SuggestBackend picks the backend for this PC: CUDA when NVIDIA's driver tool is installed, else Vulkan (every recent GPU
// has a Vulkan driver; a PC without one falls back to the CPU inside the build).
func SuggestBackend() Backend {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("nvidia-smi"); err == nil {
			return CUDA
		}
		if _, err := exec.LookPath(`C:\Windows\System32\nvidia-smi.exe`); err == nil {
			return CUDA
		}
	}
	return Vulkan
}
