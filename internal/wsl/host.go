package wsl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
)

// ErrNoWSL means wsl.exe is not on this machine (or this is not Windows and no stand-in was configured).
var ErrNoWSL = errors.New("wsl: WSL is not installed on this machine (wsl.exe was not found)")

// Host reaches the distros of this machine through wsl.exe. Every call is an argv array through the Process Manager, with a
// timeout that kills the whole tree; nothing is ever passed through a Windows shell. The zero value finds wsl.exe itself.
type Host struct {
	// Exe is wsl.exe. Empty: %SystemRoot%\System32\wsl.exe on Windows, nothing elsewhere (a development host has no WSL;
	// tests set a labelled stand-in here).
	Exe string
	// PM runs wsl.exe (nil: a process manager of its own).
	PM proc.Manager
	// Env is the environment wsl.exe gets for lists and probes (nil: the base allowlist of the app's environment, without any
	// WSLENV, so nothing of Windows is copied into the distro).
	Env func() []string
	// TTL is how long a probe result is reused (default 5 minutes). Detection on the Connections page refreshes it.
	TTL time.Duration

	mu    sync.Mutex
	cache map[string]Info
}

// Exe resolves wsl.exe.
func (h *Host) exe() (string, error) {
	if h.Exe != "" {
		if _, err := exec.LookPath(h.Exe); err != nil {
			return "", fmt.Errorf("%w: %v", ErrNoWSL, err)
		}
		return h.Exe, nil
	}
	if runtime.GOOS != "windows" {
		return "", ErrNoWSL
	}
	if root := os.Getenv("SystemRoot"); root != "" {
		p := filepath.Join(root, "System32", "wsl.exe")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("wsl.exe"); err == nil {
		return p, nil
	}
	return "", ErrNoWSL
}

// Path returns wsl.exe's path, or ErrNoWSL.
func (h *Host) Path() (string, error) { return h.exe() }

func (h *Host) pm() proc.Manager {
	if h.PM == nil {
		return proc.New()
	}
	return h.PM
}

// BaseEnv is the environment wsl.exe gets when nothing of Windows should reach the distro: the base allowlist of env, with
// WSL_UTF8=1 (wsl.exe's own messages in UTF-8 where it supports it; DecodeOutput reads both) and an empty WSLENV.
func BaseEnv(env []string) []string {
	out := proc.ScrubEnv(env, proc.BaseEnvNames, nil)
	return append(out, "WSL_UTF8=1", "WSLENV=")
}

// Environ is the environment this host starts wsl.exe with (BaseEnv of Drydock's own unless the host was given another).
func (h *Host) Environ() []string { return h.env() }

func (h *Host) env() []string {
	if h.Env != nil {
		return h.Env()
	}
	return BaseEnv(os.Environ())
}

func (h *Host) run(ctx context.Context, args []string, timeout time.Duration) (proc.ProbeResult, error) {
	exe, err := h.exe()
	if err != nil {
		return proc.ProbeResult{}, err
	}
	return proc.Probe(ctx, h.pm(), proc.Spec{Path: exe, Args: args, Env: h.env(), Label: "probe:wsl"}, timeout), nil
}

// Distros lists the installed distributions (`wsl.exe -l -q`, then `-l -v` for the default, state and WSL version where it
// answers). No distro is not an error: the list is empty and the reason says what wsl.exe printed.
func (h *Host) Distros(ctx context.Context) ([]Distro, string, error) {
	res, err := h.run(ctx, []string{"-l", "-q"}, 20*time.Second)
	if err != nil {
		return nil, "", err
	}
	if res.TimedOut {
		return nil, "", errors.New("wsl: wsl.exe -l -q did not answer within 20 seconds")
	}
	out := DecodeOutput([]byte(res.Stdout))
	if res.Exit != 0 {
		msg := strings.TrimSpace(firstLine(out + "\n" + DecodeOutput([]byte(res.Stderr))))
		if msg == "" {
			msg = fmt.Sprintf("wsl.exe -l -q exited %d", res.Exit)
		}
		return nil, msg, nil
	}
	names := ParseList(out)
	verbose := map[string]Distro{}
	if vr, err := h.run(ctx, []string{"-l", "-v"}, 20*time.Second); err == nil && vr.Exit == 0 {
		for _, d := range ParseVerbose(DecodeOutput([]byte(vr.Stdout))) {
			verbose[strings.ToLower(d.Name)] = d
		}
	}
	ds := make([]Distro, 0, len(names))
	for _, n := range names {
		d, ok := verbose[strings.ToLower(n)]
		if !ok {
			d = Distro{}
		}
		d.Name = n
		ds = append(ds, d)
	}
	return ds, "", nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

func cacheKey(distro, tool string) string { return strings.ToLower(distro) + "\x00" + tool }

// Probe runs the probe script for tool in distro through the person's login shell (versionArgs and authArgs come from the
// adapter manifest). A result younger than TTL is reused unless fresh is set. Without authArgs the sign-in status is not
// asked again: the result carries the last one probed for the same install (Info.Tool), or none (AuthExit -1).
func (h *Host) Probe(ctx context.Context, distro, tool string, versionArgs, authArgs []string, fresh bool) (Info, error) {
	ttl := h.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if !fresh {
		if in, ok := h.Cached(distro, tool); ok && time.Since(in.At) < ttl {
			return in, nil
		}
	}
	res, err := h.run(ctx, ProbeArgs(distro, tool, versionArgs, authArgs), 90*time.Second)
	if err != nil {
		return Info{}, err
	}
	if res.TimedOut {
		return Info{}, fmt.Errorf("wsl: the distro %s did not answer within 90 seconds", distro)
	}
	in := ParseProbe(distro, res.Stdout)
	in.At = time.Now()
	if !in.Complete {
		why := strings.TrimSpace(firstLine(DecodeOutput([]byte(res.Stderr)) + "\n" + DecodeOutput([]byte(res.Stdout))))
		if why == "" {
			why = fmt.Sprintf("exit %d", res.Exit)
		}
		return in, fmt.Errorf("wsl: probing the distro %s failed: %s", distro, why)
	}
	h.mu.Lock()
	if h.cache == nil {
		h.cache = map[string]Info{}
	}
	key := cacheKey(distro, tool)
	if len(authArgs) == 0 {
		// a probe that did not ask for the sign-in status (a launch's or a repository's) keeps the last one known for the same
		// install, so it does not turn the Connections page's "signed in" into "unknown"
		if old, ok := h.cache[key]; ok && old.AuthExit >= 0 && in.Tool != "" && old.Tool == in.Tool {
			in.Auth, in.AuthExit, in.AuthErr = old.Auth, old.AuthExit, old.AuthErr
		}
	}
	h.cache[key] = in
	h.mu.Unlock()
	return in, nil
}

// Cached returns the last probe result for tool in distro, whatever its age.
func (h *Host) Cached(distro, tool string) (Info, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	in, ok := h.cache[cacheKey(distro, tool)]
	return in, ok
}

// Kill kills every process of the run marked marker inside distro (KillArgs) and returns how many it killed.
func (h *Host) Kill(ctx context.Context, distro, marker string) (int, error) {
	if marker == "" {
		return 0, errors.New("wsl: no run marker")
	}
	res, err := h.run(ctx, KillArgs(distro, marker), 30*time.Second)
	if err != nil {
		return 0, err
	}
	n, ok := field(res.Stdout, "drydock-killed=")
	if !ok {
		return 0, fmt.Errorf("wsl: stopping the run's Linux processes failed (exit %d): %s", res.Exit, strings.TrimSpace(firstLine(DecodeOutput([]byte(res.Stderr)))))
	}
	k, _ := strconv.Atoi(strings.TrimSpace(n))
	return k, nil
}

// dirScript checks a folder inside the distro, making it first (with its parents) when asked. Exit 3: it could not be made;
// exit 4: it is not a folder. The path is an argument, never part of the script.
const dirScript = `if [ "$2" = make ]; then mkdir -p -- "$1" 2>/dev/null || exit 3; fi
[ -d "$1" ] || exit 4
echo drydock-dir=ok
`

// DirArgs is the argv after wsl.exe that checks (and with create set, first creates) the folder linux inside distro.
func DirArgs(distro, linux string, create bool) []string {
	mode := "check"
	if create {
		mode = "make"
	}
	return []string{"-d", distro, "--exec", "/bin/sh", "-c", dirScript, "drydock-dir", linux, mode}
}

// Dir checks that the absolute Linux path linux is a folder inside distro, creating it first (mkdir -p) when create is set. It
// runs as the distro's default user, so a folder it makes belongs to that user (the one an agent runs as).
func (h *Host) Dir(ctx context.Context, distro, linux string, create bool) error {
	if !strings.HasPrefix(linux, "/") {
		return fmt.Errorf("wsl: %q is not an absolute Linux path", linux)
	}
	res, err := h.run(ctx, DirArgs(distro, linux, create), 60*time.Second)
	if err != nil {
		return err
	}
	if res.TimedOut {
		return fmt.Errorf("wsl: the distro %s did not answer within 60 seconds", distro)
	}
	if _, ok := field(res.Stdout, "drydock-dir="); ok && res.Exit == 0 {
		return nil
	}
	switch res.Exit {
	case 3:
		return fmt.Errorf("the folder %s could not be created in the WSL distro %s", linux, distro)
	case 4:
		return fmt.Errorf("the folder %s does not exist in the WSL distro %s", linux, distro)
	}
	why := strings.TrimSpace(firstLine(DecodeOutput([]byte(res.Stdout)) + "\n" + DecodeOutput([]byte(res.Stderr))))
	if why == "" {
		why = fmt.Sprintf("exit %d", res.Exit)
	}
	return fmt.Errorf("the WSL distro %s could not be reached: %s", distro, why)
}

// Marked lists the processes of the run marked marker that are alive inside distro.
func (h *Host) Marked(ctx context.Context, distro, marker string) ([]int, error) {
	res, err := h.run(ctx, ListArgs(distro, marker), 30*time.Second)
	if err != nil {
		return nil, err
	}
	v, ok := field(res.Stdout, "drydock-marked=")
	if !ok {
		return nil, fmt.Errorf("wsl: listing the run's Linux processes failed (exit %d)", res.Exit)
	}
	var pids []int
	for _, f := range strings.Fields(v) {
		if n, err := strconv.Atoi(f); err == nil {
			pids = append(pids, n)
		}
	}
	return pids, nil
}

func field(out, key string) (string, bool) {
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if v, ok := strings.CutPrefix(l, key); ok {
			return v, true
		}
	}
	return "", false
}

// NetworkingMode reads networkingMode from the person's %USERPROFILE%\.wslconfig ([wsl2] section): "mirrored", "nat", ... or
// "" when the file does not set it (WSL's default is NAT). It reads only that one setting.
func NetworkingMode(home string) string {
	b, err := os.ReadFile(filepath.Join(home, ".wslconfig"))
	if err != nil {
		return ""
	}
	v, _ := ConfValue(strings.Split(DecodeOutput(b), "\n"), "wsl2", "networkingMode")
	return strings.ToLower(v)
}
