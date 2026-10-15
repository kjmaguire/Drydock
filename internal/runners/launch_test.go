package runners

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/sandbox"
)

const standinArg = "__runner_standin__"

// fakeDockerDir names the folder where the TEST DOUBLE engine client records its calls. It starts with DOCKER_ so the
// launcher's scrubbed environment passes it on, as it does the real client's settings.
const fakeDockerDir = "DOCKER_FAKE_DRYDOCK_DIR"

// TestMain lets this test binary stand in for two programs: the runner (a LABELLED TEST DOUBLE started with standinArg) and
// the container engine's client (a LABELLED TEST DOUBLE when fakeDockerDir is set).
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == standinArg {
		os.Exit(standinRunner(os.Args[2], os.Args[1:]))
	}
	if dir := os.Getenv(fakeDockerDir); dir != "" {
		os.Exit(fakeDocker(dir, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// standinRunner says what it was given and acts out one job, the way the runner prints it.
func standinRunner(mode string, args []string) int {
	jit := os.Getenv(JITEnv)
	if jit == "" {
		fmt.Println("An error occurred: Not configured. Run config.(sh/cmd) to configure the runner.")
		return 1
	}
	fmt.Println("jit-ok")
	for _, a := range append(args, os.Args...) {
		if strings.Contains(a, jit) {
			fmt.Println("jit-in-argv")
		}
	}
	if os.Getenv("GH_TOKEN") != "" {
		fmt.Println("secret-leak")
	}
	_ = os.WriteFile("job-marker", []byte("left by the job"), 0o644)
	fmt.Println("2026-10-15 02:00:00Z: Listening for Jobs")
	if mode == "job" {
		fmt.Println("2026-10-15 02:00:01Z: Running job: build")
		fmt.Println("2026-10-15 02:00:02Z: Job build completed with result: Succeeded")
	}
	return 0
}

type dockerCall struct {
	Args   []string `json:"args"`
	HasJIT bool     `json:"hasJit"`
	Tar    []string `json:"tar,omitempty"`
}

func fakeDocker(dir string, args []string) int {
	call := dockerCall{Args: args, HasJIT: os.Getenv(JITEnv) != ""}
	code := 0
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "version":
		fmt.Println(`{"Version":"29.0.0","Os":"linux","Arch":"amd64"}`)
	case "image":
		if len(args) > 1 && args[1] == "inspect" {
			if _, err := os.Stat(filepath.Join(dir, "built")); err != nil {
				fmt.Fprintln(os.Stderr, "Error: No such image")
				code = 1
			} else {
				fmt.Println("sha256:abc")
			}
		} else if len(args) > 1 && args[1] == "ls" {
			fmt.Println("drydock-runner:2.329.0")
			fmt.Println("drydock-runner:2.330.0")
		}
	case "build":
		tr := tar.NewReader(os.Stdin)
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(tr)
			call.Tar = append(call.Tar, fmt.Sprintf("%s:%d:%s", h.Name, len(b), strings.SplitN(string(b), "\n", 2)[0]))
		}
		_ = os.WriteFile(filepath.Join(dir, "built"), nil, 0o644)
		fmt.Println("sha256:abc")
	case "run":
		jit := os.Getenv(JITEnv)
		for _, a := range args {
			if jit != "" && strings.Contains(a, jit) {
				fmt.Println("jit-in-argv")
			}
		}
		if jit == "" {
			fmt.Println("An error occurred: Not configured.")
			code = 1
		} else {
			fmt.Println("2026-10-15 02:00:00Z: Listening for Jobs")
			fmt.Println("2026-10-15 02:00:01Z: Running job: test")
			fmt.Println("2026-10-15 02:00:02Z: Job test completed with result: Failed")
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, "calls.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_ = json.NewEncoder(f).Encode(call)
		f.Close()
	}
	return code
}

func readCalls(t *testing.T, dir string) []dockerCall {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []dockerCall
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c dockerCall
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func drain(t *testing.T, r Running) ([]string, int) {
	t.Helper()
	var lines []string
	done := time.After(30 * time.Second)
	ch := r.Lines()
	exit := r.Wait()
	code, exited := 0, false
	for ch != nil || !exited {
		select {
		case l, ok := <-ch:
			if !ok {
				ch = nil
				continue
			}
			lines = append(lines, l)
		case code = <-exit:
			exited = true
		case <-done:
			t.Fatal("the runner did not finish")
		}
	}
	return lines, code
}

// A Windows runner: the checked archive is unpacked once, each job runs in a fresh copy that is deleted afterwards, the
// one-job configuration reaches the runner only through its environment, and the app's other variables (a GitHub token
// here) do not reach it at all.
func TestHostLauncherRunsEachJobInAFreshCopyAndPassesTheConfigurationInTheEnvironment(t *testing.T) {
	t.Setenv("GH_TOKEN", "should-not-reach-the-runner")
	zipBody := makeZip(t, map[string]string{"run.cmd": "@echo off", "run.sh": "#!/bin/sh", "bin/Runner.Listener.exe": "MZ"})
	url := DownloadPrefix + "v2.330.0/actions-runner-win-x64-2.330.0.zip"
	tr := &githubTransport{files: map[string][]byte{url: zipBody}}
	rel := Release{Version: "2.330.0", Assets: map[Platform]Asset{WinX64: {Name: "actions-runner-win-x64-2.330.0.zip", URL: url, SHA256: sum(zipBody)}}}
	root, cache := t.TempDir(), t.TempDir()
	h := &HostLauncher{Root: root, Cache: cache, Platform: WinX64, HTTP: &http.Client{Transport: tr},
		Program: func(dir string) (string, []string) { return os.Args[0], []string{standinArg, "job"} }}
	ctx := context.Background()
	if err := h.Prepare(ctx, rel); err != nil {
		t.Fatal(err)
	}
	if err := h.Prepare(ctx, rel); err != nil || tr.hits.Load() != 1 {
		t.Fatalf("prepared twice: %v, %d downloads", err, tr.hits.Load())
	}
	job := Job{Slot: "kjmaguire/shop:windows", Name: "drydock-pc-windows-abc123", JIT: JIT{RunnerID: 7, Encoded: "jit-secret-7"}}
	run, err := h.Start(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	lines, code := drain(t, run)
	out := strings.Join(lines, "\n")
	if code != 0 || !strings.Contains(out, "jit-ok") || strings.Contains(out, "jit-in-argv") || strings.Contains(out, "secret-leak") ||
		!strings.Contains(out, "Job build completed with result: Succeeded") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	jobDir := filepath.Join(root, "jobs", job.Name)
	for _, f := range []string{"job-marker", "run.cmd", filepath.Join("bin", "Runner.Listener.exe")} {
		if _, err := os.Stat(filepath.Join(jobDir, f)); err != nil {
			t.Fatalf("the job's copy lacks %s: %v", f, err)
		}
	}
	if err := h.Cleanup(job); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatal("the job's folder is still there")
	}
	// the unpacked runner itself is untouched by the job
	if _, err := os.Stat(filepath.Join(cache, "dist", "2.330.0-win-x64", "job-marker")); err == nil {
		t.Fatal("the job wrote into the shared runner")
	}
	// a folder left by an earlier Drydock is swept
	os.MkdirAll(filepath.Join(root, "jobs", "drydock-pc-windows-old"), 0o755)
	h.Sweep(ctx)
	if ents, _ := os.ReadDir(filepath.Join(root, "jobs")); len(ents) != 0 {
		t.Fatalf("left: %v", ents)
	}
	if free, ok := h.Free(); !ok || free == 0 {
		t.Fatal("the free space where jobs run is not measured")
	}
}

// A Linux runner: the image is built once from a context of exactly the Dockerfile and the checked archive, older runner
// images are removed, and each job's container is removed when it ends, mounts nothing from the PC and not the engine's
// socket, and gets the configuration by name only.
func TestContainerLauncherBuildsTheImageOnceAndRunsJobsInIsolatedContainers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(fakeDockerDir, dir)
	tgz := []byte("pretend tar.gz")
	url := DownloadPrefix + "v2.330.0/actions-runner-linux-x64-2.330.0.tar.gz"
	tr := &githubTransport{files: map[string][]byte{url: tgz}}
	rel := Release{Version: "2.330.0", Assets: map[Platform]Asset{LinuxX64: {Name: "actions-runner-linux-x64-2.330.0.tar.gz", URL: url, SHA256: sum(tgz)}}}
	c := &ContainerLauncher{Engine: &sandbox.Engine{Bin: os.Args[0]}, Cache: t.TempDir(), HTTP: &http.Client{Transport: tr}}
	ctx := context.Background()
	if err := c.Prepare(ctx, rel); err != nil {
		t.Fatal(err)
	}
	if c.Arch() != "X64" {
		t.Fatal(c.Arch())
	}
	if err := c.Prepare(ctx, rel); err != nil {
		t.Fatal(err)
	}
	var builds, rmis []dockerCall
	for _, call := range readCalls(t, dir) {
		switch call.Args[0] {
		case "build":
			builds = append(builds, call)
		case "rmi":
			rmis = append(rmis, call)
		}
	}
	if len(builds) != 1 {
		t.Fatalf("built %d times", len(builds))
	}
	want := []string{"Dockerfile:" + fmt.Sprint(len(Dockerfile)) + ":FROM ubuntu:24.04", fmt.Sprintf("runner.tar.gz:%d:pretend tar.gz", len(tgz))}
	if !slices.Equal(builds[0].Tar, want) || !slices.Contains(builds[0].Args, ImageTag("2.330.0")) {
		t.Fatalf("%+v", builds[0])
	}
	if len(rmis) == 0 || rmis[0].Args[1] != "drydock-runner:2.329.0" {
		t.Fatalf("the old image was not removed: %+v", rmis)
	}

	job := Job{Slot: "kjmaguire/shop:linux", Name: "drydock-pc-linux-def456", JIT: JIT{RunnerID: 8, Encoded: "jit-secret-8"}}
	run, err := c.Start(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	lines, code := drain(t, run)
	if out := strings.Join(lines, "\n"); code != 0 || strings.Contains(out, "jit-in-argv") || !strings.Contains(out, "Job test completed with result: Failed") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var runCall *dockerCall
	for _, call := range readCalls(t, dir) {
		if call.Args[0] == "run" {
			cc := call
			runCall = &cc
		}
	}
	if runCall == nil || !runCall.HasJIT {
		t.Fatalf("the engine client did not get the configuration: %+v", runCall)
	}
	args := strings.Join(runCall.Args, " ")
	for _, w := range []string{"--rm", "--name " + job.Name, "-e " + JITEnv + " ", "--pids-limit 4096", "--memory 8g", "drydock.runner=" + job.Slot, ImageTag("2.330.0")} {
		if !strings.Contains(args+" ", w) {
			t.Fatalf("%q lacks %q", args, w)
		}
	}
	for _, bad := range []string{"-v", "--volume", "--mount", "--privileged", "docker.sock", "--network=host", "jit-secret-8"} {
		if slices.Contains(runCall.Args, bad) || strings.Contains(args, "docker.sock") || strings.Contains(args, "jit-secret") {
			t.Fatalf("%q has %q", args, bad)
		}
	}
	if err := c.Cleanup(job); err != nil {
		t.Fatal(err)
	}
}
