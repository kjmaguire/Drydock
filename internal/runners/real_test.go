package runners

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/sandbox"
)

// The real runner release, opt-in (DRYDOCK_REAL_RUNNER_RELEASE=1; CI sets it): the newest release is read from GitHub, its
// archive for this machine is downloaded and checked against the SHA-256 in its release notes, and the runner is made ready
// the way the app does it (unpacked on Windows, built into the image on a Linux container engine). Then the runner itself
// shows it starts, and that it reads its configuration from ACTIONS_RUNNER_INPUT_JITCONFIG: with a configuration that is
// not valid it complains about that instead of saying it is not configured. Nothing is registered with GitHub.
func TestRealRunnerRelease(t *testing.T) {
	if os.Getenv("DRYDOCK_REAL_RUNNER_RELEASE") != "1" {
		t.Skip("set DRYDOCK_REAL_RUNNER_RELEASE=1 to download and check the real runner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	api := host.HTTPAPI{BaseURL: "https://api.github.com", Token: os.Getenv("GITHUB_TOKEN")}
	rel, err := LatestRelease(ctx, api)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("runner %s: %d archives with a published SHA-256", rel.Version, len(rel.Assets))
	for _, p := range []Platform{WinX64, LinuxX64} {
		if _, ok := rel.Assets[p]; !ok {
			t.Fatalf("no %s archive in %+v", p, rel.Assets)
		}
	}
	cache := t.TempDir()
	client := &http.Client{Timeout: 15 * time.Minute}
	const garbage = "bm90LWEtdmFsaWQtY29uZmln" // base64 of "not-a-valid-config"
	notConfigured := func(out string) bool { return strings.Contains(strings.ToLower(out), "not configured") }

	if runtime.GOOS == "windows" {
		h := &HostLauncher{Root: t.TempDir(), Cache: cache, HTTP: client}
		if err := h.Prepare(ctx, rel); err != nil {
			t.Fatal(err)
		}
		dist := filepath.Join(cache, "dist", rel.Version+"-"+string(h.platform()))
		out, err := exec.CommandContext(ctx, filepath.Join(dist, "bin", "Runner.Listener.exe"), "--version").CombinedOutput()
		if err != nil || !strings.Contains(string(out), rel.Version) {
			t.Fatalf("Runner.Listener --version: %v\n%s", err, out)
		}
		cmd := exec.CommandContext(ctx, filepath.Join(dist, "bin", "Runner.Listener.exe"), "run")
		cmd.Env = append(os.Environ(), JITEnv+"="+garbage)
		out, _ = cmd.CombinedOutput()
		t.Logf("with a configuration that is not valid:\n%s", out)
		if notConfigured(string(out)) {
			t.Fatalf("the runner did not read %s", JITEnv)
		}
		return
	}
	eng := sandbox.NewEngine()
	if _, err := eng.Info(ctx); err != nil {
		t.Skipf("no container engine: %v", err)
	}
	c := &ContainerLauncher{Engine: eng, Cache: cache, HTTP: client}
	if err := c.Prepare(ctx, rel); err != nil {
		t.Fatal(err)
	}
	tag := ImageTag(rel.Version)
	t.Cleanup(func() { _, _ = eng.Run(context.Background(), nil, "rmi", "-f", tag) })
	out, err := eng.Run(ctx, nil, "run", "--rm", "--entrypoint", "./bin/Runner.Listener", tag, "--version")
	if err != nil || !strings.Contains(out, rel.Version) {
		t.Fatalf("Runner.Listener --version in the image: %v\n%s", err, out)
	}
	cmd := exec.CommandContext(ctx, eng.Bin, "run", "--rm", "-e", JITEnv, tag)
	cmd.Env = append(os.Environ(), JITEnv+"="+garbage)
	b, _ := cmd.CombinedOutput()
	t.Logf("with a configuration that is not valid:\n%s", b)
	if notConfigured(string(b)) {
		t.Fatalf("the runner did not read %s", JITEnv)
	}
	// the job's user is not root, and the image has git for actions/checkout
	out, err = eng.Run(ctx, nil, "run", "--rm", "--entrypoint", "sh", tag, "-c", "id -u; git --version")
	if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "1001") || !strings.Contains(out, "git version") {
		t.Fatalf("%v\n%s", err, out)
	}
}
