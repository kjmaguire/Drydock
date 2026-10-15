package localllm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
)

func official(t *testing.T) localllm.Source {
	t.Helper()
	s, ok := localllm.SourceByID(localllm.SourceOfficial)
	if !ok {
		t.Fatal("no official source")
	}
	return s
}

// The newest release that carries the backend's archive with a digest is picked: a draft is skipped, an archive without a
// digest is never offered, and a CUDA build is offered only together with its runtime archive.
func TestTheNewestCheckedBuildIsPicked(t *testing.T) {
	src := official(t)
	fx := llamatest.NewReleases(t, src.Repo)
	server := map[string][]byte{"llama-server.exe": []byte("MZ stand-in"), "LICENSE": []byte("MIT")}
	fx.Add(llamatest.ReleaseSpec{Tag: "b11600", Prerelease: true, Body: "## Older build", Assets: map[string][]byte{
		"llama-b11600-bin-win-vulkan-x64.zip":    llamatest.ZipOf(server),
		"llama-b11600-bin-win-cuda-13.4-x64.zip": llamatest.ZipOf(server), "cudart-llama-bin-win-cuda-13.4-x64.zip": llamatest.ZipOf(map[string][]byte{"cudart64_13.dll": {1}}),
	}})
	fx.Add(llamatest.ReleaseSpec{Tag: "b11650", Prerelease: true, Body: "no digest on vulkan, no cudart", Assets: map[string][]byte{
		"llama-b11650-bin-win-vulkan-x64.zip": llamatest.ZipOf(server), "llama-b11650-bin-win-cuda-13.4-x64.zip": llamatest.ZipOf(server),
	}, NoDigest: []string{"llama-b11650-bin-win-vulkan-x64.zip"}})
	fx.Add(llamatest.ReleaseSpec{Tag: "b11669", Draft: true, Assets: map[string][]byte{"llama-b11669-bin-win-vulkan-x64.zip": llamatest.ZipOf(server)}})
	fx.Add(llamatest.ReleaseSpec{Tag: "v0.7.0", Body: "llama.cpp v0.7.0 adds LiliCorr", Assets: map[string][]byte{"nightly-tag.txt": []byte("b11669")}})
	api := host.HTTPAPI{BaseURL: fx.Server.URL}
	ctx := context.Background()

	rel, err := localllm.LatestRelease(ctx, api, src, localllm.Vulkan)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "b11600" || rel.Archive.Name != "llama-b11600-bin-win-vulkan-x64.zip" || len(rel.Archive.SHA256) != 64 || rel.Cudart != nil || rel.Notes != "Older build" {
		t.Fatalf("vulkan: %+v", rel)
	}
	rel, err = localllm.LatestRelease(ctx, api, src, localllm.CUDA)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "b11600" || rel.Cudart == nil || rel.Cudart.Name != "cudart-llama-bin-win-cuda-13.4-x64.zip" {
		t.Fatalf("cuda: %+v", rel)
	}
	if _, err := localllm.LatestRelease(ctx, api, src, localllm.ROCm); err == nil || !strings.Contains(err.Error(), "rocm") {
		t.Fatalf("rocm: %v", err)
	}
}

// The fork's release that is marked pre-release ("Dev latest") is skipped: only its tagged releases count.
func TestTheForksDevBuildsAreSkipped(t *testing.T) {
	src, _ := localllm.SourceByID(localllm.SourceTurboQuant)
	fx := llamatest.NewReleases(t, src.Repo)
	server := llamatest.ZipOf(map[string][]byte{"llama-server.exe": []byte("MZ")})
	fx.Add(llamatest.ReleaseSpec{Tag: "b10298-2.0.0", Assets: map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": server}})
	fx.Add(llamatest.ReleaseSpec{Tag: "dev-latest", Prerelease: true, Assets: map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": server}})
	rel, err := localllm.LatestRelease(context.Background(), host.HTTPAPI{BaseURL: fx.Server.URL}, src, localllm.Vulkan)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "b10298-2.0.0" || rel.Cudart != nil {
		t.Fatalf("%+v", rel)
	}
	if !src.CacheType("turbo4") || official(t).CacheType("turbo4") || !official(t).CacheType("q4_0") {
		t.Fatal("cache types")
	}
}
