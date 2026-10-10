package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Engine runs the container engine's command-line client. Every container, network and volume Drydock creates carries the
// label LabelOwner=drydock, so cleanup never touches anything else on the machine.
type Engine struct {
	// Bin is the client executable: "docker" unless DRYDOCK_CONTAINER_CLI names another Docker-compatible client.
	Bin string
}

// Labels on everything a sandbox creates.
const (
	LabelOwner   = "drydock.owner"
	LabelTask    = "drydock.task"
	LabelProject = "drydock.project"
	LabelRole    = "drydock.role" // workspace | egress | service
	LabelConfig  = "drydock.config"
)

// ErrNoEngine means no container engine answered: not installed, not running, or in Windows-containers mode.
var ErrNoEngine = errors.New("no container engine is reachable")

// NewEngine returns the engine client, honouring DRYDOCK_CONTAINER_CLI.
func NewEngine() *Engine {
	bin := strings.TrimSpace(os.Getenv("DRYDOCK_CONTAINER_CLI"))
	if bin == "" {
		bin = "docker"
	}
	return &Engine{Bin: bin}
}

// Run runs the client with args and returns its standard output. A failure carries the client's error text.
func (e *Engine) Run(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	hideWindow(cmd)
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("%w: %s is not installed or not on PATH", ErrNoEngine, e.Bin)
		}
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 2000 {
			msg = msg[:2000] + "..."
		}
		if msg == "" {
			msg = err.Error()
		}
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		return out.String(), fmt.Errorf("%s %s: %s", e.Bin, name, msg)
	}
	return out.String(), nil
}

// Info is what the engine reports about itself.
type Info struct {
	Client  string `json:"client"`
	Version string `json:"version"`
	OS      string `json:"os"`   // must be linux
	Arch    string `json:"arch"` // amd64 | arm64
}

// Info asks the engine for its version and platform. The engine must run Linux containers.
func (e *Engine) Info(ctx context.Context) (Info, error) {
	out, err := e.Run(ctx, nil, "version", "--format", "{{json .Server}}")
	if err != nil {
		if errors.Is(err, ErrNoEngine) {
			return Info{}, err
		}
		return Info{}, fmt.Errorf("%w: %v", ErrNoEngine, err)
	}
	var s struct {
		Version string `json:"Version"`
		Os      string `json:"Os"`
		Arch    string `json:"Arch"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &s); err != nil || s.Version == "" {
		return Info{}, fmt.Errorf("%w: the engine did not report its version", ErrNoEngine)
	}
	info := Info{Client: e.Bin, Version: s.Version, OS: s.Os, Arch: s.Arch}
	if s.Os != "linux" {
		return info, fmt.Errorf("the container engine runs %s containers; switch it to Linux containers", s.Os)
	}
	return info, nil
}

// HasImage reports whether the image is present locally.
func (e *Engine) HasImage(ctx context.Context, image string) bool {
	_, err := e.Run(ctx, nil, "image", "inspect", "--format", "{{.Id}}", image)
	return err == nil
}

// Pull fetches an image.
func (e *Engine) Pull(ctx context.Context, image string) error {
	_, err := e.Run(ctx, nil, "pull", "--quiet", image)
	return err
}

// ensureImage pulls the image when it is not present.
func (e *Engine) ensureImage(ctx context.Context, image string) error {
	if e.HasImage(ctx, image) {
		return nil
	}
	if err := e.Pull(ctx, image); err != nil {
		return fmt.Errorf("could not get image %s: %w", image, err)
	}
	return nil
}

// list returns the ids of objects of a kind ("container", "network", "volume") carrying a label (key=value).
func (e *Engine) list(ctx context.Context, kind, label string) ([]string, error) {
	var args []string
	switch kind {
	case "container":
		args = []string{"ps", "-a", "-q", "--filter", "label=" + label}
	case "network":
		args = []string{"network", "ls", "-q", "--filter", "label=" + label}
	case "volume":
		args = []string{"volume", "ls", "-q", "--filter", "label=" + label}
	default:
		return nil, fmt.Errorf("sandbox: unknown kind %q", kind)
	}
	out, err := e.Run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// labelsOf returns the labels of containers carrying a label, keyed by container id.
func (e *Engine) labelsOf(ctx context.Context, label string) (map[string]map[string]string, error) {
	ids, err := e.list(ctx, "container", label)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	out, err := e.Run(ctx, nil, append([]string{"inspect", "--format", "{{.Id}} {{json .Config.Labels}}"}, ids...)...)
	if err != nil {
		return nil, err
	}
	res := map[string]map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		id, js, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		var l map[string]string
		if json.Unmarshal([]byte(js), &l) == nil {
			res[id] = l
		}
	}
	return res, nil
}
