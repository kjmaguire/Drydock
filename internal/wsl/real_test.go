package wsl_test

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// realDistro is the distro CI's wsl job installed (DRYDOCK_WSL_TEST_DISTRO); the test is skipped elsewhere.
func realDistro(t *testing.T) (*wsl.Host, string) {
	t.Helper()
	d := os.Getenv("DRYDOCK_WSL_TEST_DISTRO")
	if runtime.GOOS != "windows" || d == "" {
		t.Skip("needs Windows with a WSL distro named in DRYDOCK_WSL_TEST_DISTRO (CI's wsl job)")
	}
	return &wsl.Host{}, d
}

// The real wsl.exe: its UTF-16 list names the distro, the probe finds the distro's facts through a login shell, and a
// program started the way agents are started (a session of its own, the run marker) is found and killed by its marker,
// grandchildren included, after the Windows side (wsl.exe in its Job Object) was killed.
func TestRealWSLListsProbesAndKillsByMarker(t *testing.T) {
	h, distro := realDistro(t)
	ctx := context.Background()
	exe, err := h.Path()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := exec.Command(exe, "-l", "-q").Output()
	t.Logf("wsl.exe -l -q printed %d bytes, UTF-16LE: %v", len(raw), len(raw) > 1 && raw[1] == 0)
	ds, msg, err := h.Distros(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(ds))
	for _, d := range ds {
		names = append(names, d.Name)
	}
	if !slices.Contains(names, distro) {
		t.Fatalf("distros %v (%s) lack %s", names, msg, distro)
	}
	in, err := h.Probe(ctx, distro, "git", []string{"--version"}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("probe: %+v", in)
	if in.Home == "" || in.Path == "" || in.Paths.MountRoot == "" || in.Tool == "" || !strings.Contains(in.Version, "git version") {
		t.Fatalf("probe: %+v", in)
	}
	// the mount root maps this test's own folder into the distro
	wd, _ := os.Getwd()
	lin, err := in.Paths.ToLinux(wd)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe, "-d", distro, "--cd", lin, "--exec", "/bin/sh", "-c", "ls real_test.go").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "real_test.go") {
		t.Fatalf("the distro does not see %s at %s: %v %s", wd, lin, err, out)
	}

	marker := wsl.NewMarker()
	l := wsl.Launch{Distro: distro, Dir: lin, Marker: marker, Env: []string{"HOME=" + in.Home, "PATH=" + in.Path},
		Argv: []string{"/bin/sh", "-c", "setsid sleep 600 >/dev/null 2>&1 </dev/null & sleep 601 >/dev/null 2>&1 </dev/null & echo started; exec sleep 602"}}
	p, err := proc.New().Start(ctx, proc.Spec{Path: exe, Args: l.Args(), Env: wsl.BaseEnv(os.Environ()), Label: "test:wsl"})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if n, _ := p.Stdout().Read(buf); !strings.Contains(string(buf[:n]), "started") {
		t.Fatalf("no output over wsl.exe's pipe: %q", buf[:n])
	}
	waitFor(t, func() bool { pids, _ := h.Marked(ctx, distro, marker); return len(pids) >= 3 })
	_ = p.Kill(true) // the Job Object ends wsl.exe ...
	<-p.Wait()
	time.Sleep(2 * time.Second)
	left, _ := h.Marked(ctx, distro, marker)
	t.Logf("after the Job Object killed wsl.exe, %d process(es) of the run were still alive inside the distro: %v", len(left), left)
	n, err := h.Kill(ctx, distro, marker) // ... and the marker sweep ends what is left
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("the sweep killed %d", n)
	waitFor(t, func() bool { pids, _ := h.Marked(ctx, distro, marker); return len(pids) == 0 })
}
