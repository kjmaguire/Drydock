package runners

import (
	"archive/tar"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/sandbox"
)

// Kind is where a runner's jobs run: "windows" (on this PC, in a folder of its own) or "linux" (in a fresh container on the
// container engine).
type Kind string

const (
	Windows Kind = "windows"
	Linux   Kind = "linux"
)

// JITEnv is the environment variable that hands the runner its one-job configuration. The runner reads command-line
// options from ACTIONS_RUNNER_INPUT_<NAME> and removes them from its environment before it starts a job, so the
// credential is never on a command line where a job's processes could read it.
const JITEnv = "ACTIONS_RUNNER_INPUT_JITCONFIG"

// Job is one runner registration and the process that serves it.
type Job struct {
	Slot    string
	Name    string
	JIT     JIT
	Version string
}

// Running is a started runner.
type Running interface {
	// Lines yields the runner's output, line by line, and is closed when the output ends.
	Lines() <-chan string
	// Wait yields the exit code once.
	Wait() <-chan int
	// Stop ends the runner and everything it started.
	Stop()
}

// Launcher prepares and starts runners of one kind.
type Launcher interface {
	Kind() Kind
	// Available says why runners of this kind cannot run here (nil when they can).
	Available(ctx context.Context) error
	// Prepare makes the release's runner ready to start (downloaded and checked, unpacked or built into an image).
	Prepare(ctx context.Context, rel Release) error
	Start(ctx context.Context, job Job) (Running, error)
	// Cleanup removes what the job left: its folder, or its container.
	Cleanup(job Job) error
	// Free is the free space where jobs write, when it can be measured.
	Free() (uint64, bool)
	// Arch is the runner's architecture label ("X64" or "ARM64").
	Arch() string
	// Sweep removes what an earlier run of Drydock left (job folders, containers).
	Sweep(ctx context.Context)
}

// procRunning adapts a proc.Proc.
type procRunning struct {
	p     proc.Proc
	lines chan string
	exit  chan int
	stop  func()
}

func newProcRunning(p proc.Proc, stop func()) *procRunning {
	r := &procRunning{p: p, lines: make(chan string, 64), exit: make(chan int, 1), stop: stop}
	var wg sync.WaitGroup
	for _, src := range []io.Reader{p.Stdout(), p.Stderr()} {
		if src == nil {
			continue
		}
		wg.Add(1)
		safe.Go("runners.newProcRunning", func() {
			func(src io.Reader) {
				defer wg.Done()
				sc := bufio.NewScanner(src)
				sc.Buffer(make([]byte, 64<<10), 1<<20)
				for sc.Scan() {
					r.lines <- sc.Text()
				}
				_, _ = io.Copy(io.Discard, src)
			}(src)
		})
	}
	safe.Go("runners.newProcRunning#2", func() {
		wg.Wait()
		close(r.lines)
	})
	safe.Go("runners.newProcRunning#3", func() {
		ei := <-p.Wait()
		r.exit <- ei.Code
	})
	return r
}

func (r *procRunning) Lines() <-chan string { return r.lines }
func (r *procRunning) Wait() <-chan int     { return r.exit }
func (r *procRunning) Stop() {
	if r.stop != nil {
		r.stop()
	}
	_ = r.p.Kill(true)
}

// runnerEnv is the environment a runner starts with: the scrubbed base every agent process gets (no app variables, no
// other tools' tokens) and its one-job configuration.
func runnerEnv(base []string, jit string) []string {
	if base == nil {
		base = proc.ScrubEnv(os.Environ(), proc.BaseEnvNames, nil)
	}
	env := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, JITEnv) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, JITEnv+"="+jit)
}

// HostLauncher runs Windows runners on this PC: each job gets a fresh copy of the runner in Root/jobs/<runner name>, deleted
// when the job ends, so nothing a job leaves (checkouts, tool downloads, caches, logs) reaches the next one.
type HostLauncher struct {
	Root     string   // where jobs run (a Dev Drive folder when there is one)
	Cache    string   // downloaded archives and the unpacked runner
	Platform Platform // win-x64 unless set
	HTTP     *http.Client
	PM       proc.Manager
	// Program is the command that starts the runner in its folder: run.cmd on Windows (run.sh elsewhere) unless a test
	// replaces it with a labelled stand-in.
	Program func(dir string) (string, []string)
	Env     []string // base environment; nil means the scrubbed environment of this process

	mu   sync.Mutex
	dist string
	prep sync.Mutex // one Prepare at a time: two runners starting together must not unpack into the same folder
}

func (h *HostLauncher) Kind() Kind { return Windows }

func (h *HostLauncher) platform() Platform {
	if h.Platform != "" {
		return h.Platform
	}
	if runtime.GOARCH == "arm64" {
		return WinARM64
	}
	return WinX64
}

func (h *HostLauncher) Available(context.Context) error { return nil }

func (h *HostLauncher) Prepare(ctx context.Context, rel Release) error {
	h.prep.Lock()
	defer h.prep.Unlock()
	a, ok := rel.Assets[h.platform()]
	if !ok {
		return fmt.Errorf("runner %s has no %s archive", rel.Version, h.platform())
	}
	dist := filepath.Join(h.Cache, "dist", rel.Version+"-"+string(h.platform()))
	h.mu.Lock()
	ready := h.dist == dist
	h.mu.Unlock()
	if ready {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dist, ".drydock-checked")); err != nil {
		archive, err := Download(ctx, h.HTTP, a, filepath.Join(h.Cache, "downloads"))
		if err != nil {
			return err
		}
		if !strings.HasSuffix(a.Name, ".zip") {
			return fmt.Errorf("the %s runner comes as %s; only zip archives are unpacked on this PC", h.platform(), a.Name)
		}
		_ = os.RemoveAll(dist)
		if err := Unzip(archive, dist); err != nil {
			return fmt.Errorf("could not unpack %s: %w", a.Name, err)
		}
		if err := os.WriteFile(filepath.Join(dist, ".drydock-checked"), []byte(a.SHA256+"\n"), 0o644); err != nil {
			return err
		}
		prune(filepath.Join(h.Cache, "dist"), filepath.Base(dist))
		prune(filepath.Join(h.Cache, "downloads"), a.Name)
	}
	h.mu.Lock()
	h.dist = dist
	h.mu.Unlock()
	return nil
}

// prune removes everything in dir but keep.
func prune(dir, keep string) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.Name() != keep {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

func (h *HostLauncher) jobDir(job Job) string { return filepath.Join(h.Root, "jobs", job.Name) }

func (h *HostLauncher) Start(ctx context.Context, job Job) (Running, error) {
	h.mu.Lock()
	dist := h.dist
	h.mu.Unlock()
	if dist == "" {
		return nil, errors.New("the runner is not prepared")
	}
	dir := h.jobDir(job)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if err := CopyTree(dist, dir); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("could not copy the runner for the job: %w", err)
	}
	prog, args := h.program(dir)
	pm := h.PM
	if pm == nil {
		pm = proc.New()
	}
	p, err := pm.Start(ctx, proc.Spec{Path: prog, Args: args, Dir: dir, Env: runnerEnv(h.Env, job.JIT.Encoded), Label: "runner:" + job.Name})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return newProcRunning(p, nil), nil
}

func (h *HostLauncher) program(dir string) (string, []string) {
	if h.Program != nil {
		return h.Program(dir)
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(dir, "run.cmd"), nil
	}
	return filepath.Join(dir, "run.sh"), nil
}

// Cleanup deletes the job's folder, trying again for a few seconds while Windows still holds its files.
func (h *HostLauncher) Cleanup(job Job) error {
	dir := h.jobDir(job)
	var err error
	for i := 0; i < 10; i++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

// Sweep deletes job folders left by an earlier run of Drydock (it stopped before cleaning up). The folder is one a person chose
// and may be shared with other things, so only entries named like a job of this app are deleted, never everything in "jobs"
// (audit 2026-10-16, L35).
func (h *HostLauncher) Sweep(context.Context) {
	dir := filepath.Join(h.Root, "jobs")
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), jobNameStem) {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

func (h *HostLauncher) Arch() string {
	if h.platform() == WinARM64 {
		return "ARM64"
	}
	return "X64"
}

func (h *HostLauncher) Free() (uint64, bool) {
	if err := os.MkdirAll(h.Root, 0o755); err != nil {
		return 0, false
	}
	n, err := FreeBytes(h.Root)
	return n, err == nil
}

// Container labels of runner images and containers.
const (
	LabelRole   = "runner"
	LabelRunner = "drydock.runner"
)

// Dockerfile is the runner image: Ubuntu with the tools most workflows expect, the runner's own dependencies (its
// installdependencies.sh), and an unprivileged user that may use sudo inside the container (jobs install packages). The
// runner archive is the checked download, unpacked by ADD.
const Dockerfile = `FROM ubuntu:26.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl git jq sudo unzip zip xz-utils tar gzip && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1001 -s /bin/bash runner && echo 'runner ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/runner && chmod 0440 /etc/sudoers.d/runner
ADD runner.tar.gz /home/runner/actions-runner/
RUN /home/runner/actions-runner/bin/installdependencies.sh && rm -rf /var/lib/apt/lists/* && chown -R runner:runner /home/runner/actions-runner
USER runner
WORKDIR /home/runner/actions-runner
ENTRYPOINT ["./run.sh"]
`

// ContainerLauncher runs Linux runners, each job in a new container that is removed when it ends. The container gets no
// mount of the PC's drives and no access to the container engine (so a job cannot start containers next to it), and has
// limits on processors, memory and processes.
type ContainerLauncher struct {
	Engine *sandbox.Engine
	Cache  string
	HTTP   *http.Client
	PM     proc.Manager
	Env    []string // base environment of the engine client; nil means the scrubbed environment of this process
	CPUs   string   // default "4"
	Memory string   // default "8g"
	Pids   int      // default 4096

	mu       sync.Mutex
	image    string
	platform Platform
	prep     sync.Mutex // one Prepare at a time: the image is built once
}

func (c *ContainerLauncher) Kind() Kind { return Linux }

func (c *ContainerLauncher) Available(ctx context.Context) error {
	info, err := c.Engine.Info(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.platform = LinuxX64
	if info.Arch == "arm64" || info.Arch == "aarch64" {
		c.platform = LinuxARM64
	}
	c.mu.Unlock()
	return nil
}

// ImageTag is the runner image of a version.
func ImageTag(version string) string { return "drydock-runner:" + version }

func (c *ContainerLauncher) Prepare(ctx context.Context, rel Release) error {
	c.prep.Lock()
	defer c.prep.Unlock()
	if err := c.Available(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	plat := c.platform
	ready := c.image == ImageTag(rel.Version)
	c.mu.Unlock()
	if ready {
		return nil
	}
	tag := ImageTag(rel.Version)
	if !c.Engine.HasImage(ctx, tag) {
		a, ok := rel.Assets[plat]
		if !ok {
			return fmt.Errorf("runner %s has no %s archive", rel.Version, plat)
		}
		archive, err := Download(ctx, c.HTTP, a, filepath.Join(c.Cache, "downloads"))
		if err != nil {
			return err
		}
		if err := c.build(ctx, tag, archive); err != nil {
			return err
		}
		prune(filepath.Join(c.Cache, "downloads"), a.Name)
	}
	c.removeOldImages(ctx, tag)
	c.mu.Lock()
	c.image = tag
	c.mu.Unlock()
	return nil
}

// build sends the engine a build context of two files, the Dockerfile and the checked runner archive, so the archive is
// unpacked inside Linux and never on the PC.
func (c *ContainerLauncher) build(ctx context.Context, tag, archive string) error {
	pr, pw := io.Pipe()
	safe.Go("runners.ContainerLauncher.build", func() {
		pw.CloseWithError(BuildContext(pw, archive))
	})
	_, err := c.Engine.Run(ctx, pr, "build", "--quiet", "--label", sandbox.LabelOwner+"=drydock", "--label", sandbox.LabelRole+"="+LabelRole, "-t", tag, "-")
	_ = pr.Close()
	if err != nil {
		return fmt.Errorf("could not build the runner image: %w", err)
	}
	return nil
}

// BuildContext writes the image's build context as a tar stream: Dockerfile and runner.tar.gz.
func BuildContext(w io.Writer, archive string) error {
	tw := tar.NewWriter(w)
	if err := tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(Dockerfile)), ModTime: time.Unix(0, 0)}); err != nil {
		return err
	}
	if _, err := io.WriteString(tw, Dockerfile); err != nil {
		return err
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "runner.tar.gz", Mode: 0o644, Size: st.Size(), ModTime: time.Unix(0, 0)}); err != nil {
		return err
	}
	if _, err := io.Copy(tw, f); err != nil {
		return err
	}
	return tw.Close()
}

// removeOldImages deletes Drydock's runner images of other versions (one still used by a container stays).
func (c *ContainerLauncher) removeOldImages(ctx context.Context, keep string) {
	out, err := c.Engine.Run(ctx, nil, "image", "ls", "--filter", "label="+sandbox.LabelOwner+"=drydock", "--filter", "label="+sandbox.LabelRole+"="+LabelRole, "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		return
	}
	var old []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && l != keep && strings.HasPrefix(l, "drydock-runner:") {
			old = append(old, l)
		}
	}
	sort.Strings(old)
	for _, img := range old {
		_, _ = c.Engine.Run(ctx, nil, "rmi", img)
	}
}

// ContainerArgs are the engine client's arguments for a job's container. The configuration is passed by name only (-e
// NAME): its value comes from the client's environment, never from its command line.
func (c *ContainerLauncher) ContainerArgs(job Job, image string) []string {
	cpus, mem, pids := c.CPUs, c.Memory, c.Pids
	if cpus == "" {
		cpus = "4"
	}
	if mem == "" {
		mem = "8g"
	}
	if pids <= 0 {
		pids = 4096
	}
	return []string{"run", "--rm", "--init", "--name", job.Name,
		"--label", sandbox.LabelOwner + "=drydock", "--label", sandbox.LabelRole + "=" + LabelRole, "--label", LabelRunner + "=" + job.Slot,
		"--cpus", cpus, "--memory", mem, "--pids-limit", fmt.Sprint(pids),
		"-e", JITEnv, image}
}

func (c *ContainerLauncher) Start(ctx context.Context, job Job) (Running, error) {
	c.mu.Lock()
	image := c.image
	c.mu.Unlock()
	if image == "" {
		return nil, errors.New("the runner image is not built")
	}
	pm := c.PM
	if pm == nil {
		pm = proc.New()
	}
	base := c.Env
	if base == nil {
		// the engine client keeps its own settings (DOCKER_HOST, DOCKER_CONTEXT, DOCKER_CONFIG)
		base = proc.ScrubEnv(os.Environ(), proc.BaseEnvNames, []string{"DOCKER_"})
	}
	p, err := pm.Start(ctx, proc.Spec{Path: c.Engine.Bin, Args: c.ContainerArgs(job, image), Env: runnerEnv(base, job.JIT.Encoded), Label: "runner:" + job.Name})
	if err != nil {
		return nil, err
	}
	return newProcRunning(p, func() { _ = c.Cleanup(job) }), nil
}

// Cleanup removes the job's container if it is still there.
func (c *ContainerLauncher) Cleanup(job Job) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := c.Engine.Run(ctx, nil, "rm", "-f", job.Name)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "no such container") {
		return nil
	}
	return err
}

func (c *ContainerLauncher) Arch() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.platform == LinuxARM64 {
		return "ARM64"
	}
	return "X64"
}

// Sweep removes runner containers an earlier run of Drydock left.
func (c *ContainerLauncher) Sweep(ctx context.Context) {
	out, err := c.Engine.Run(ctx, nil, "ps", "-aq", "--filter", "label="+sandbox.LabelOwner+"=drydock", "--filter", "label="+sandbox.LabelRole+"="+LabelRole)
	if err != nil {
		return
	}
	for _, id := range strings.Fields(out) {
		_, _ = c.Engine.Run(ctx, nil, "rm", "-f", id)
	}
}

func (c *ContainerLauncher) Free() (uint64, bool) { return 0, false }
