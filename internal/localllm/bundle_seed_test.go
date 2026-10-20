package localllm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kjmaguire/drydock/internal/bundle"
	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/runners"
)

type offline struct{ t *testing.T }

func (o offline) RoundTrip(r *http.Request) (*http.Response, error) {
	o.t.Errorf("a bundled install went to the network: %s", r.URL)
	return nil, http.ErrUseLastResponse
}

func bundledBuild(t *testing.T, dir, tag, body string) bundle.LlamaBuild {
	t.Helper()
	src, _ := localllm.SourceByID(localllm.SourceTurboQuant)
	zip := llamatest.ZipOf(map[string][]byte{"bin/" + localllm.ServerName(): []byte(body), "bin/ggml-vulkan.dll": []byte("dll")})
	name := "llama-turboquant-windows-x64-vulkan.zip"
	if err := os.WriteFile(filepath.Join(dir, name), zip, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(zip)
	return bundle.LlamaBuild{Source: src.ID, Tag: tag, Backend: string(localllm.Vulkan),
		Archive: runners.Asset{Name: name, URL: src.DownloadPrefix() + tag + "/" + name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(zip))}}
}

// Decision P77: llama.cpp with the TurboQuant KV cache types comes installed. The bundled build is unpacked at the first
// start without the network; a later start changes nothing; an app update with a newer build replaces the earlier bundled
// one but not a build the person installed; a build the person removed stays removed.
func TestTheBundledLlamaBuildIsPutInPlaceOnce(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	rt := &localllm.Runtime{Root: root, HTTP: &http.Client{Transport: offline{t}}}
	dl := t.TempDir()
	b1 := bundledBuild(t, dl, "b10298-2.0.0", "server one")
	in, did, err := rt.SeedFromBundle(ctx, dl, b1)
	if err != nil || !did || in.Source != localllm.SourceTurboQuant || in.Backend != localllm.Vulkan || in.Tag != b1.Tag {
		t.Fatalf("%v %v %+v", err, did, in)
	}
	if body, _ := os.ReadFile(in.Server); string(body) != "server one" {
		t.Fatalf("server %q", body)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(in.Server), "ggml-vulkan.dll")); err != nil {
		t.Fatal("the backend's DLL is not beside the server")
	}
	if _, did, _ = rt.SeedFromBundle(ctx, dl, b1); did {
		t.Fatal("seeded twice")
	}
	dl2 := t.TempDir()
	b2 := bundledBuild(t, dl2, "b10350-2.1.0", "server two")
	if in, did, err = rt.SeedFromBundle(ctx, dl2, b2); err != nil || !did || in.Tag != b2.Tag {
		t.Fatalf("a newer bundle: %v %v %+v", err, did, in)
	}
	// the person removes it: not put back
	if err := rt.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, did, err = rt.SeedFromBundle(ctx, dl2, b2); err != nil || did {
		t.Fatalf("a removed build came back: %v %v", err, did)
	}
	if _, ok := rt.Installed(); ok {
		t.Fatal("installed after removal")
	}
	// a person's own install afterwards is kept when an older bundle comes along
	if _, err := bundle.Stage(dl2, b2.Archive, "https://github.com/AtomicBot-ai/atomic-llama-cpp-turboquant/releases/download/", filepath.Join(root, "cache")); err != nil {
		t.Fatal(err)
	}
	mine, err := rt.Install(ctx, localllm.Release{Source: b2.Source, Tag: b2.Tag, Backend: localllm.Vulkan, Archive: b2.Archive}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in, did, err = rt.SeedFromBundle(ctx, dl, b1); err != nil || did || in.Tag != mine.Tag {
		t.Fatalf("the person's own build was replaced: %v %v %+v", err, did, in)
	}
}
