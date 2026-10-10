// Package sandboxtest builds the container images the sandbox tests use, offline. The development sandbox cannot pull images
// (registries refuse it), so the images are made from this machine's own programs: each listed program, the shared libraries
// it loads (found with ldd), /etc/passwd and /tmp, and optionally the Go toolchain, imported with `docker import`. The product
// itself pulls the images a project names. A test helper, also used by the trial harness (cmd/drydock-trial) to make the Go
// image of its Drydock arm.
package sandboxtest

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Programs every test image carries.
var Programs = []string{"sh", "cat", "ls", "mkdir", "rm", "cp", "mv", "env", "timeout", "setsid", "sleep", "tail", "head",
	"id", "wc", "grep", "sed", "tr", "chown", "touch", "date", "uname", "ps", "curl", "find", "dirname", "basename", "sort", "diff"}

var lddLine = regexp.MustCompile(`(?:=>\s*)?(/\S+)\s+\(0x`)

// Engine skips the test when no container engine answers (DRYDOCK_REQUIRE_DOCKER=1 makes it fail instead) and returns the
// client's name.
func Engine(t testing.TB) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the sandbox Docker tests build their images from Linux programs")
	}
	bin := os.Getenv("DRYDOCK_CONTAINER_CLI")
	if bin == "" {
		bin = "docker"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version", "--format", "{{.Server.Os}}").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "linux" {
		if os.Getenv("DRYDOCK_REQUIRE_DOCKER") == "1" {
			t.Fatalf("a container engine is required: %v %s", err, out)
		}
		t.Skipf("no container engine here: %v %s", err, strings.TrimSpace(string(out)))
	}
	return bin
}

// rootfs maps container paths to host files for the programs and their libraries.
func rootfs(t testing.TB) map[string]string {
	t.Helper()
	files, err := programFiles()
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func programFiles() (map[string]string, error) {
	files := map[string]string{}
	for _, p := range Programs {
		host, err := exec.LookPath(p)
		if err != nil {
			return nil, fmt.Errorf("test image: %s is not on this machine: %w", p, err)
		}
		real, err := filepath.EvalSymlinks(host)
		if err != nil {
			return nil, err
		}
		files["/usr/bin/"+p] = real
		out, err := exec.Command("ldd", real).Output()
		if err != nil {
			continue // a static program
		}
		for _, m := range lddLine.FindAllStringSubmatch(string(out), -1) {
			if r, err := filepath.EvalSymlinks(m[1]); err == nil {
				files[m[1]] = r
			}
		}
	}
	return files, nil
}

// write streams a tar of the base files, files (container path -> host file) and trees (container dir -> host dir).
func write(w io.Writer, files map[string]string, trees map[string]string) error {
	tw := tar.NewWriter(w)
	dirs := map[string]bool{}
	var mkdir func(d string, mode int64) error
	mkdir = func(d string, mode int64) error {
		if d == "/" || d == "." || dirs[d] {
			return nil
		}
		if err := mkdir(filepath.Dir(d), 0o755); err != nil {
			return err
		}
		dirs[d] = true
		return tw.WriteHeader(&tar.Header{Name: strings.TrimPrefix(d, "/") + "/", Typeflag: tar.TypeDir, Mode: mode, ModTime: time.Unix(0, 0)})
	}
	add := func(name string, r io.Reader, size, mode int64) error {
		if err := mkdir(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: strings.TrimPrefix(name, "/"), Typeflag: tar.TypeReg, Mode: mode, Size: size, ModTime: time.Unix(0, 0)}); err != nil {
			return err
		}
		_, err := io.Copy(tw, r)
		return err
	}
	addFile := func(name, host string, mode int64) error {
		f, err := os.Open(host)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if mode == 0 {
			mode = int64(st.Mode().Perm())
		}
		return add(name, f, st.Size(), mode)
	}
	if err := mkdir("/root", 0o755); err != nil {
		return err
	}
	dirs["/tmp"] = true
	if err := tw.WriteHeader(&tar.Header{Name: "tmp/", Typeflag: tar.TypeDir, Mode: 0o1777, ModTime: time.Unix(0, 0)}); err != nil {
		return err
	}
	passwd := "root:x:0:0:root:/root:/bin/sh\nagent:x:1000:1000:agent:/tmp:/bin/sh\nnobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin\n"
	group := "root:x:0:\nagent:x:1000:\nnogroup:x:65534:\n"
	for name, body := range map[string]string{"/etc/passwd": passwd, "/etc/group": group} {
		if err := add(name, strings.NewReader(body), int64(len(body)), 0o644); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := addFile(n, files[n], 0o755); err != nil {
			return err
		}
	}
	for dst, src := range trees {
		err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			name := filepath.ToSlash(filepath.Join(dst, rel))
			switch {
			case d.IsDir():
				return mkdir(name, 0o755)
			case d.Type()&fs.ModeSymlink != 0:
				target, err := os.Readlink(p)
				if err != nil {
					return err
				}
				if err := mkdir(filepath.Dir(name), 0o755); err != nil {
					return err
				}
				return tw.WriteHeader(&tar.Header{Name: strings.TrimPrefix(name, "/"), Typeflag: tar.TypeSymlink, Linkname: target, ModTime: time.Unix(0, 0)})
			case d.Type().IsRegular():
				return addFile(name, p, 0)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return tw.Close()
}

func importImage(t testing.TB, bin, tag string, files, trees map[string]string, changes ...string) {
	t.Helper()
	if err := importTar(bin, tag, files, trees, changes...); err != nil {
		t.Fatal(err)
	}
}

func importTar(bin, tag string, files, trees map[string]string, changes ...string) error {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(write(pw, files, trees)) }()
	args := []string{"import", "--change", "ENV PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	for _, c := range changes {
		args = append(args, "--change", c)
	}
	cmd := exec.Command(bin, append(args, "-", tag)...)
	cmd.Stdin = pr
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("test image %s: %w: %s", tag, err, errb.String())
	}
	return nil
}

var (
	mu    sync.Mutex
	built = map[string]string{}
)

func once(t testing.TB, key string, f func() string) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if v, ok := built[key]; ok {
		return v
	}
	v := f()
	built[key] = v
	return v
}

// Base is an image with the Programs.
func Base(t testing.TB, bin string) string {
	return once(t, "base", func() string {
		tag := "drydock-sandbox-test/base:2"
		importImage(t, bin, tag, rootfs(t), nil)
		return tag
	})
}

// Go is Base plus this machine's Go toolchain at /usr/local/go.
func Go(t testing.TB, bin string) string {
	return once(t, "go", func() string {
		tag, err := GoImage(bin)
		if err != nil {
			t.Fatal(err)
		}
		return tag
	})
}

// GoImage builds the image Go returns, without a test.
func GoImage(bin string) (string, error) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOROOT: %w", err)
	}
	files, err := programFiles()
	if err != nil {
		return "", err
	}
	tag := "drydock-sandbox-test/go:2"
	return tag, importTar(bin, tag, files, map[string]string{"/usr/local/go": strings.TrimSpace(string(out))})
}

// GoBuild builds a Linux binary of a package (a path relative to the module root's package dir, or an import path).
func GoBuild(t testing.TB, out, pkg string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, b)
	}
}

// EgressBinary builds cmd/drydock-egress for this machine's architecture once and returns its path.
func EgressBinary(t testing.TB) string {
	return once(t, "egress", func() string {
		dir, err := os.MkdirTemp("", "drydock-egress-*")
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "drydock-egress-linux-"+runtime.GOARCH)
		GoBuild(t, p, "github.com/kjmaguire/drydock/cmd/drydock-egress")
		return p
	})
}

// Containers counts the containers carrying a label (key=value).
func Containers(t testing.TB, bin, label string) int {
	t.Helper()
	out, err := exec.Command(bin, "ps", "-aq", "--filter", "label="+label).Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	return len(strings.Fields(string(out)))
}

// Custom is Base plus extra files (container path -> host file), tagged tag, built once per test run.
func Custom(t testing.TB, bin, tag string, extra map[string]string, changes ...string) string {
	return once(t, "custom:"+tag, func() string {
		files := rootfs(t)
		for k, v := range extra {
			files[k] = v
		}
		importImage(t, bin, tag, files, nil, changes...)
		return tag
	})
}
