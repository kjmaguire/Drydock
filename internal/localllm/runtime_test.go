package localllm_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
)

// Install downloads the build and its CUDA runtime, checks both, unpacks them into one folder with the runtime's DLLs beside
// llama-server, records the build, and removes the build it replaces. A second install of the same build downloads nothing.
func TestInstallPutsTheCheckedBuildInPlace(t *testing.T) {
	src := official(t)
	fx := llamatest.NewReleases(t, src.Repo)
	server := map[string][]byte{"llama-server.exe": []byte("MZ stand-in server"), "ggml.dll": []byte("ggml"), "LICENSE": []byte("MIT")}
	fx.Add(llamatest.ReleaseSpec{Tag: "b11600", Prerelease: true, Assets: map[string][]byte{
		"llama-b11600-bin-win-cuda-13.4-x64.zip": llamatest.ZipOf(server), "cudart-llama-bin-win-cuda-13.4-x64.zip": llamatest.ZipOf(map[string][]byte{"cudart64_13.dll": {1, 2, 3}}),
		"llama-b11600-bin-win-vulkan-x64.zip": llamatest.ZipOf(map[string][]byte{"build/bin/llama-server.exe": []byte("older layout")}),
	}})
	api := host.HTTPAPI{BaseURL: fx.Server.URL}
	root := t.TempDir()
	rt := &localllm.Runtime{Root: root, HTTP: &http.Client{Transport: fx.Transport}}
	if _, ok := rt.Installed(); ok {
		t.Fatal("installed before any install")
	}
	ctx := context.Background()
	rel, err := localllm.LatestRelease(ctx, api, src, localllm.CUDA)
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	in, err := rt.Install(ctx, rel, func(p localllm.Progress) {
		if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
			phases = append(phases, p.Phase)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.Tag != "b11600" || in.Backend != localllm.CUDA || filepath.Base(in.Server) != "llama-server.exe" || !strings.HasPrefix(in.Server, root) {
		t.Fatalf("%+v", in)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(in.Server), "cudart64_13.dll")); err != nil {
		t.Fatal("the CUDA runtime's DLL is not beside the server:", err)
	}
	if strings.Join(phases, ",") != "downloading,unpacking,done" {
		t.Fatalf("phases %v", phases)
	}
	got, ok := rt.Installed()
	if !ok || got.Server != in.Server {
		t.Fatalf("installed: %+v %v", got, ok)
	}
	// the same build again: the checked archives are reused
	n := fx.Downloads.Load()
	if _, err := rt.Install(ctx, rel, nil); err != nil {
		t.Fatal(err)
	}
	if fx.Downloads.Load() != n {
		t.Fatal("the archives were downloaded again")
	}
	// another backend: the older layout (build/bin) is found too, and the CUDA build is removed
	rel2, err := localllm.LatestRelease(ctx, api, src, localllm.Vulkan)
	if err != nil {
		t.Fatal(err)
	}
	in2, err := rt.Install(ctx, rel2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(in2.Server), "/build/bin/llama-server.exe") {
		t.Fatalf("%s", in2.Server)
	}
	if _, err := os.Stat(in.Dir); !os.IsNotExist(err) {
		t.Fatal("the replaced build is still there")
	}
	if err := rt.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.Installed(); ok {
		t.Fatal("installed after Remove")
	}
}

// An archive whose bytes do not match the digest GitHub recorded is deleted and refused; one without llama-server in it too.
func TestABuildThatDoesNotMatchItsDigestIsRefused(t *testing.T) {
	src := official(t)
	fx := llamatest.NewReleases(t, src.Repo)
	fx.Add(llamatest.ReleaseSpec{Tag: "b1", Prerelease: true, Assets: map[string][]byte{
		"llama-b1-bin-win-cpu-x64.zip": llamatest.ZipOf(map[string][]byte{"README": []byte("no server here")}),
	}})
	api := host.HTTPAPI{BaseURL: fx.Server.URL}
	ctx := context.Background()
	rel, err := localllm.LatestRelease(ctx, api, src, localllm.CPU)
	if err != nil {
		t.Fatal(err)
	}
	rt := &localllm.Runtime{Root: t.TempDir(), HTTP: &http.Client{Transport: fx.Transport}}
	if _, err := rt.Install(ctx, rel, nil); err == nil || !strings.Contains(err.Error(), "no llama-server") {
		t.Fatal(err)
	}
	rel.Archive.SHA256 = strings.Repeat("0", 64)
	if _, err := rt.Install(ctx, rel, nil); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatal(err)
	}
	if _, ok := rt.Installed(); ok {
		t.Fatal("a refused build is installed")
	}
	rel.Archive.URL = "https://example.com/evil.zip"
	rel.Archive.SHA256 = strings.Repeat("a", 64)
	if _, err := rt.Install(ctx, rel, nil); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatal(err)
	}
}
