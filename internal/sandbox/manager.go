package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/sandbox/egress"
)

// Manager creates, reuses and removes task sandboxes. It is safe for concurrent use; work on one task is serialized.
type Manager struct {
	Engine *Engine
	// EgressBin returns the path of the Linux build of drydock-egress for an architecture (amd64, arm64). Default: EgressBinary.
	EgressBin func(arch string) (string, error)
	// LogDir, if set, receives the full output of every command (<LogDir>/<task>/<n>-<time>.log); the agent sees a bounded part.
	LogDir string
	// SetupTimeout bounds each setup command (default 20 minutes).
	SetupTimeout time.Duration

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	boxes map[string]*Box
	info  *Info
	seq   int
}

// Spec says which sandbox a task needs.
type Spec struct {
	TaskID    string
	ProjectID string
	// Worktree is the host path of the task worktree, mounted at Workdir.
	Worktree string
	Config   Config
}

// Box is a running sandbox.
type Box struct {
	Spec      Spec
	Base      string // name prefix of everything the box created
	Workspace string // workspace container name
	Egress    string // egress container name ("" unless the network is allowlist)
	Network   string // the task's private network
	Setup     []SetupResult
	Started   time.Time
	// hasTimeout reports that the image has a timeout command, which ends a command's whole process group in the container.
	hasTimeout bool
	reused     bool
}

// SetupResult is the outcome of one setup command.
type SetupResult struct {
	Command  string        `json:"command"`
	ExitCode int           `json:"exitCode"`
	Output   string        `json:"output"`
	Duration time.Duration `json:"duration"`
}

// NewManager returns a manager for the engine.
func NewManager(e *Engine, logDir string) *Manager {
	return &Manager{Engine: e, LogDir: logDir}
}

func (m *Manager) lock(taskID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locks == nil {
		m.locks = map[string]*sync.Mutex{}
		m.boxes = map[string]*Box{}
	}
	l := m.locks[taskID]
	if l == nil {
		l = &sync.Mutex{}
		m.locks[taskID] = l
	}
	return l
}

// Info returns the engine's information, asking it once.
func (m *Manager) Info(ctx context.Context) (Info, error) {
	m.mu.Lock()
	if m.info != nil {
		i := *m.info
		m.mu.Unlock()
		return i, nil
	}
	m.mu.Unlock()
	i, err := m.Engine.Info(ctx)
	if err != nil {
		return i, err
	}
	m.mu.Lock()
	m.info = &i
	m.mu.Unlock()
	return i, nil
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// BaseName is the name prefix of a task's sandbox objects.
func BaseName(taskID string) string {
	s := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(taskID), "-"), "-.")
	if len(s) > 40 {
		s = s[:40]
	}
	return "drydock-" + s
}

// Ensure returns the task's running sandbox, creating it when there is none or when its definition changed. Creating one
// pulls missing images, starts the services and runs the setup commands, so it can take minutes the first time.
func (m *Manager) Ensure(ctx context.Context, s Spec) (*Box, error) {
	if s.TaskID == "" || s.Worktree == "" {
		return nil, errors.New("sandbox: a task and its worktree are required")
	}
	if strings.ContainsAny(s.Worktree, ",\"\n") {
		return nil, fmt.Errorf("sandbox: the worktree path %q contains a comma or quote, which the engine's mount syntax cannot carry", s.Worktree)
	}
	if err := s.Config.Validate(); err != nil {
		return nil, err
	}
	l := m.lock(s.TaskID)
	l.Lock()
	defer l.Unlock()
	hash := s.Config.Hash() + "-" + shortHash(s.Worktree)
	m.mu.Lock()
	cached := m.boxes[s.TaskID]
	m.mu.Unlock()
	if cached != nil && cached.Spec.Config.Hash() == s.Config.Hash() && cached.Spec.Worktree == s.Worktree && m.running(ctx, cached.Workspace) {
		return cached, nil
	}
	info, err := m.Info(ctx)
	if err != nil {
		return nil, err
	}
	base := BaseName(s.TaskID)
	b := &Box{Spec: s, Base: base, Workspace: base + "-ws", Network: base, Started: time.Now()}
	if s.Config.Network == NetworkAllowlist {
		b.Egress = base + "-egress"
	}
	// a sandbox left from an earlier Drydock start, with the same definition: use it
	if out, err := m.Engine.Run(ctx, nil, "inspect", "--format", "{{.State.Running}}|{{index .Config.Labels \""+LabelConfig+"\"}}", b.Workspace); err == nil {
		running, label, _ := strings.Cut(strings.TrimSpace(out), "|")
		if running == "true" && label == hash {
			b.reused = true
			b.hasTimeout = m.hasCommand(ctx, b.Workspace, "timeout")
			m.mu.Lock()
			m.boxes[s.TaskID] = b
			m.mu.Unlock()
			return b, nil
		}
	}
	if err := m.remove(ctx, s.TaskID); err != nil {
		return nil, err
	}
	if err := m.create(ctx, b, info, hash); err != nil {
		_ = m.remove(context.WithoutCancel(ctx), s.TaskID)
		return nil, err
	}
	m.mu.Lock()
	m.boxes[s.TaskID] = b
	m.mu.Unlock()
	return b, nil
}

func (m *Manager) labels(b *Box, role, hash string) []string {
	out := []string{"--label", LabelOwner + "=drydock", "--label", LabelTask + "=" + b.Spec.TaskID, "--label", LabelProject + "=" + b.Spec.ProjectID, "--label", LabelRole + "=" + role}
	if hash != "" {
		out = append(out, "--label", LabelConfig+"="+hash)
	}
	return out
}

func (m *Manager) create(ctx context.Context, b *Box, info Info, hash string) error {
	c := b.Spec.Config
	e := m.Engine
	if err := e.ensureImage(ctx, c.Image); err != nil {
		return err
	}
	for _, name := range c.ServiceNames() {
		if err := e.ensureImage(ctx, c.Services[name].Image); err != nil {
			return fmt.Errorf("service %s: %w", name, err)
		}
	}
	netArgs := append([]string{"network", "create"}, m.labels(b, "network", "")...)
	if c.Network != NetworkOpen {
		netArgs = append(netArgs, "--internal")
	}
	if _, err := e.Run(ctx, nil, append(netArgs, b.Network)...); err != nil {
		return err
	}
	if b.Egress != "" {
		image, err := m.egressImage(ctx, info.Arch)
		if err != nil {
			return err
		}
		out := b.Base + "-out" // the egress container's own way out, shared with nothing else
		if _, err := e.Run(ctx, nil, append(append([]string{"network", "create"}, m.labels(b, "network", "")...), out)...); err != nil {
			return err
		}
		args := append([]string{"run", "-d", "--name", b.Egress, "--network", b.Network, "--network-alias", "egress"}, m.labels(b, "egress", "")...)
		args = append(args, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory", "128m", "--pids-limit", "256",
			"-e", "DRYDOCK_EGRESS_ALLOW="+strings.Join(c.Hosts(), ","), image)
		if _, err := e.Run(ctx, nil, args...); err != nil {
			return err
		}
		if _, err := e.Run(ctx, nil, "network", "connect", out, b.Egress); err != nil {
			return err
		}
	}
	var vols []string
	for i, v := range c.Volumes {
		name := fmt.Sprintf("%s-v%d", b.Base, i)
		if _, err := e.Run(ctx, nil, append(append([]string{"volume", "create"}, m.labels(b, "volume", "")...), name)...); err != nil {
			return err
		}
		vols = append(vols, "--mount", "type=volume,source="+name+",target="+v)
	}
	for _, name := range c.ServiceNames() {
		svc := c.Services[name]
		args := append([]string{"run", "-d", "--name", b.Base + "-svc-" + name, "--network", b.Network, "--network-alias", name}, m.labels(b, "service", "")...)
		args = append(args, "--security-opt", "no-new-privileges", "--pids-limit", "4096")
		for _, k := range sortedKeys(svc.Env) {
			args = append(args, "-e", k+"="+svc.Env[k])
		}
		args = append(args, strings.TrimSpace(svc.Image))
		args = append(args, svc.Command...)
		if _, err := e.Run(ctx, nil, args...); err != nil {
			return fmt.Errorf("service %s: %w", name, err)
		}
	}
	args := append([]string{"run", "-d", "--name", b.Workspace, "--hostname", "workspace", "--network", b.Network}, m.labels(b, "workspace", hash)...)
	args = append(args, "--init", "--security-opt", "no-new-privileges",
		"--cpus", strconv.FormatFloat(c.CPUs, 'f', -1, 64), "--memory", c.Memory, "--pids-limit", strconv.Itoa(c.Pids),
		"--mount", "type=bind,source="+b.Spec.Worktree+",target="+Workdir)
	args = append(args, vols...)
	args = append(args, "-w", Workdir)
	for _, kv := range b.proxyEnv() {
		args = append(args, "-e", kv)
	}
	for _, k := range sortedKeys(c.Env) {
		args = append(args, "-e", k+"="+c.Env[k])
	}
	if c.User != "" {
		args = append(args, "--user", c.User)
	}
	args = append(args, "--entrypoint", "sh", c.Image, "-c", `trap "exit 0" TERM INT; while :; do sleep 3600 & wait $!; done`)
	if _, err := e.Run(ctx, nil, args...); err != nil {
		return err
	}
	if c.User != "" {
		for _, v := range c.Volumes { // a fresh volume belongs to root: give it to the user that runs the commands
			_, _ = e.Run(ctx, nil, "exec", "-u", "0", b.Workspace, "chown", c.User, v)
		}
	}
	b.hasTimeout = m.hasCommand(ctx, b.Workspace, "timeout")
	setupTimeout := m.SetupTimeout
	if setupTimeout <= 0 {
		setupTimeout = 20 * time.Minute
	}
	for _, cmd := range c.Setup {
		res, err := m.exec(ctx, b, ExecRequest{Command: cmd, Timeout: setupTimeout}, "setup")
		sr := SetupResult{Command: cmd, ExitCode: res.ExitCode, Output: res.Output, Duration: res.Duration}
		if err != nil {
			sr.ExitCode, sr.Output = -1, err.Error()
		}
		b.Setup = append(b.Setup, sr)
		if err != nil || res.ExitCode != 0 {
			break // later steps usually depend on earlier ones; the agent sees what failed and can fix it
		}
	}
	return nil
}

// proxyEnv is the environment that points every tool at the egress proxy. Services are reached directly.
func (b *Box) proxyEnv() []string {
	if b.Egress == "" {
		return nil
	}
	noProxy := strings.Join(append([]string{"localhost", "127.0.0.1", "::1"}, b.Spec.Config.ServiceNames()...), ",")
	p := "http://egress:3128"
	return []string{"HTTP_PROXY=" + p, "HTTPS_PROXY=" + p, "http_proxy=" + p, "https_proxy=" + p, "NO_PROXY=" + noProxy, "no_proxy=" + noProxy}
}

func (m *Manager) running(ctx context.Context, container string) bool {
	out, err := m.Engine.Run(ctx, nil, "inspect", "--format", "{{.State.Running}}", container)
	return err == nil && strings.TrimSpace(out) == "true"
}

func (m *Manager) hasCommand(ctx context.Context, container, name string) bool {
	_, err := m.Engine.Run(ctx, nil, "exec", container, "sh", "-c", "command -v "+name)
	return err == nil
}

// Remove deletes everything a task's sandbox created: containers, networks and volumes.
func (m *Manager) Remove(ctx context.Context, taskID string) error {
	l := m.lock(taskID)
	l.Lock()
	defer l.Unlock()
	return m.remove(ctx, taskID)
}

func (m *Manager) remove(ctx context.Context, taskID string) error {
	m.mu.Lock()
	if m.boxes != nil {
		delete(m.boxes, taskID)
	}
	m.mu.Unlock()
	label := LabelTask + "=" + taskID
	var errs []error
	if ids, err := m.Engine.list(ctx, "container", label); err != nil {
		errs = append(errs, err)
	} else if len(ids) > 0 {
		if _, err := m.Engine.Run(ctx, nil, append([]string{"rm", "-f", "-v"}, ids...)...); err != nil {
			errs = append(errs, err)
		}
	}
	for _, kind := range []string{"network", "volume"} {
		ids, err := m.Engine.list(ctx, kind, label)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, id := range ids {
			if _, err := m.Engine.Run(ctx, nil, kind, "rm", id); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Prune removes the sandboxes of tasks keep does not want (finished or deleted tasks, left from an earlier run). It returns
// the task ids it removed.
func (m *Manager) Prune(ctx context.Context, keep func(taskID string) bool) ([]string, error) {
	tasks := map[string]bool{}
	labels, err := m.Engine.labelsOf(ctx, LabelOwner+"=drydock")
	if err != nil {
		return nil, err
	}
	for _, l := range labels {
		if t := l[LabelTask]; t != "" {
			tasks[t] = true
		}
	}
	for _, kind := range []string{"network", "volume"} {
		out, err := m.Engine.Run(ctx, nil, map[string][]string{
			"network": {"network", "ls", "--filter", "label=" + LabelOwner + "=drydock", "--format", `{{.Label "` + LabelTask + `"}}`},
			"volume":  {"volume", "ls", "--filter", "label=" + LabelOwner + "=drydock", "--format", `{{.Label "` + LabelTask + `"}}`},
		}[kind]...)
		if err != nil {
			continue
		}
		for _, t := range strings.Fields(out) {
			tasks[t] = true
		}
	}
	var removed []string
	var errs []error
	for t := range tasks {
		if keep(t) {
			continue
		}
		if err := m.Remove(ctx, t); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, t)
	}
	sort.Strings(removed)
	return removed, errors.Join(errs...)
}

// Denials returns the most recent requests the egress proxy refused (newest last, at most n).
func (m *Manager) Denials(ctx context.Context, b *Box, n int) []egress.Decision {
	if b.Egress == "" {
		return nil
	}
	out, err := m.Engine.Run(ctx, nil, "logs", "--tail", "2000", b.Egress)
	if err != nil {
		return nil
	}
	var refused []egress.Decision
	for _, d := range egress.ParseLog(strings.NewReader(out)) {
		if !d.Allowed {
			refused = append(refused, d)
		}
	}
	if len(refused) > n {
		refused = refused[len(refused)-n:]
	}
	return refused
}

// Describe tells an agent what its sandbox is: where its files are, which services it can reach, what the network allows and
// how setup went.
func (m *Manager) Describe(ctx context.Context, b *Box) string {
	c := b.Spec.Config
	var sb strings.Builder
	fmt.Fprintf(&sb, "Sandbox image: %s. Your working folder is mounted at %s; paths under %s in command output are your working folder.\n", c.Image, Workdir, Workdir)
	if len(c.Services) > 0 {
		sb.WriteString("Services (reach each by its name as the host name):\n")
		for _, n := range c.ServiceNames() {
			fmt.Fprintf(&sb, "- %s: %s\n", n, c.Services[n].Image)
		}
	}
	switch c.Network {
	case NetworkAllowlist:
		hosts := c.Hosts()
		fmt.Fprintf(&sb, "Network: only allowlisted hosts, through the proxy already set in HTTP_PROXY/HTTPS_PROXY (%d hosts: %s).\n", len(hosts), strings.Join(firstN(hosts, 40), ", "))
		if d := m.Denials(ctx, b, 10); len(d) > 0 {
			sb.WriteString("Recently refused (ask the human to add a host to allowHosts in " + ConfigPath + " if the project needs it):\n")
			for _, x := range d {
				fmt.Fprintf(&sb, "- %s:%d (%s)\n", x.Host, x.Port, x.Reason)
			}
		}
	case NetworkOpen:
		sb.WriteString("Network: open internet access.\n")
	case NetworkNone:
		sb.WriteString("Network: no internet access; the services are reachable.\n")
	}
	if len(c.Env) > 0 {
		fmt.Fprintf(&sb, "Environment set for every command: %s.\n", strings.Join(sortedKeys(c.Env), ", "))
	}
	if b.reused {
		sb.WriteString("Setup: ran when this sandbox was first created.\n")
	}
	for _, s := range b.Setup {
		status := "ok"
		if s.ExitCode != 0 {
			status = fmt.Sprintf("FAILED (exit %d)", s.ExitCode)
		}
		fmt.Fprintf(&sb, "Setup `%s`: %s in %s.\n", s.Command, status, s.Duration.Round(time.Millisecond))
		if s.ExitCode != 0 {
			fmt.Fprintf(&sb, "%s\n", s.Output)
		}
	}
	return sb.String()
}

// EgressBinary finds the Linux build of drydock-egress: DRYDOCK_EGRESS_BIN, or drydock-egress-linux-<arch> next to the
// running executable (the release puts it there).
func EgressBinary(arch string) (string, error) {
	if p := os.Getenv("DRYDOCK_EGRESS_BIN"); p != "" {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	p := filepath.Join(filepath.Dir(exe), "drydock-egress-linux-"+arch)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("the network allowlist needs %s (it ships next to Drydock); set \"network\" to \"open\" or \"none\" in %s to run without it", filepath.Base(p), ConfigPath)
	}
	return p, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return append(append([]string(nil), s[:n]...), fmt.Sprintf("and %d more", len(s)-n))
	}
	return s
}

// containerDir maps a working-folder-relative directory to the container path; it never leaves the working folder.
func containerDir(rel string) (string, error) {
	rel = strings.ReplaceAll(strings.TrimSpace(rel), `\`, "/")
	if rel == "" || rel == "." {
		return Workdir, nil
	}
	if strings.HasPrefix(rel, Workdir+"/") || rel == Workdir {
		rel = strings.TrimPrefix(strings.TrimPrefix(rel, Workdir), "/")
	}
	clean := path.Clean("/" + rel)
	if strings.HasPrefix(rel, "/") || strings.Contains("/"+rel+"/", "/../") {
		return "", fmt.Errorf("the folder %q is not inside the working folder", rel)
	}
	return path.Join(Workdir, clean), nil
}
