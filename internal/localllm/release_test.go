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
// digest is never offered, and a CUDA build is offered only together with its runtime archive. The toolkit version in the
// name is not fixed: of a release's CUDA 13 archives the newest toolkit is taken, with the runtime of the same version, and
// CUDA 12 archives belong to the legacy backend.
func TestTheNewestCheckedBuildIsPicked(t *testing.T) {
	src := official(t)
	fx := llamatest.NewReleases(t, src.Repo)
	server := map[string][]byte{"llama-server.exe": []byte("MZ stand-in"), "LICENSE": []byte("MIT")}
	cudart := func(v string) []byte { return llamatest.ZipOf(map[string][]byte{"cudart64_" + v + ".dll": {1}}) }
	fx.Add(llamatest.ReleaseSpec{Tag: "b11600", Prerelease: true, Body: "## Older build", Assets: map[string][]byte{
		"llama-b11600-bin-win-vulkan-x64.zip":    llamatest.ZipOf(server),
		"llama-b11600-bin-win-cuda-13.1-x64.zip": llamatest.ZipOf(server), "cudart-llama-bin-win-cuda-13.1-x64.zip": cudart("13.1"),
		"llama-b11600-bin-win-cuda-13.4-x64.zip": llamatest.ZipOf(server), "cudart-llama-bin-win-cuda-13.4-x64.zip": cudart("13.4"),
		"llama-b11600-bin-win-cuda-12.4-x64.zip": llamatest.ZipOf(server), "cudart-llama-bin-win-cuda-12.4-x64.zip": cudart("12.4"),
	}})
	fx.Add(llamatest.ReleaseSpec{Tag: "b11650", Prerelease: true, Body: "no digest on vulkan, no cudart for the newer toolkit", Assets: map[string][]byte{
		"llama-b11650-bin-win-vulkan-x64.zip": llamatest.ZipOf(server), "llama-b11650-bin-win-cuda-13.5-x64.zip": llamatest.ZipOf(server),
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
	if rel.Tag != "b11600" || rel.Archive.Name != "llama-b11600-bin-win-cuda-13.4-x64.zip" || rel.Cudart == nil || rel.Cudart.Name != "cudart-llama-bin-win-cuda-13.4-x64.zip" {
		t.Fatalf("cuda: %+v", rel)
	}
	rel, err = localllm.LatestRelease(ctx, api, src, localllm.CUDALegacy)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Archive.Name != "llama-b11600-bin-win-cuda-12.4-x64.zip" || rel.Cudart == nil || rel.Cudart.Name != "cudart-llama-bin-win-cuda-12.4-x64.zip" {
		t.Fatalf("cuda 12: %+v", rel)
	}
	// the error names the file looked for, with the tag's place marked, not a name with the tag left out
	if _, err := localllm.LatestRelease(ctx, api, src, localllm.ROCm); err == nil || !strings.Contains(err.Error(), "llama-<tag>-bin-win-rocm-*-x64.zip") {
		t.Fatalf("rocm: %v", err)
	}
	// a toolkit bump upstream: the next release's CUDA archive is found without a Drydock release
	fx.Add(llamatest.ReleaseSpec{Tag: "b11700", Prerelease: true, Assets: map[string][]byte{
		"llama-b11700-bin-win-cuda-13.6-x64.zip": llamatest.ZipOf(server), "cudart-llama-bin-win-cuda-13.6-x64.zip": cudart("13.6"),
		"llama-b11700-bin-win-rocm-10.2-x64.zip": llamatest.ZipOf(server),
	}})
	rel, err = localllm.LatestRelease(ctx, api, src, localllm.CUDA)
	if err != nil || rel.Tag != "b11700" || rel.Cudart == nil || rel.Cudart.Name != "cudart-llama-bin-win-cuda-13.6-x64.zip" {
		t.Fatalf("cuda after a toolkit bump: %+v %v", rel, err)
	}
	if rel, err = localllm.LatestRelease(ctx, api, src, localllm.ROCm); err != nil || rel.Archive.Name != "llama-b11700-bin-win-rocm-10.2-x64.zip" {
		t.Fatalf("rocm: %+v %v", rel, err)
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
	// a pinned tag that names a dev build (the bundle's first pin did) is refused, and the error says it is a pre-release
	fx.Add(llamatest.ReleaseSpec{Tag: "turboquant-windows-x64-vulkan-519f0c5", Prerelease: true, Assets: map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": server}})
	_, err = localllm.ReleaseByTag(context.Background(), host.HTTPAPI{BaseURL: fx.Server.URL}, src, localllm.Vulkan, "turboquant-windows-x64-vulkan-519f0c5")
	if err == nil || !strings.Contains(err.Error(), "only 1 pre-release build(s), which are not installed") {
		t.Fatalf("err = %v", err)
	}
	// the release tag itself is found by its tag
	if rel, err := localllm.ReleaseByTag(context.Background(), host.HTTPAPI{BaseURL: fx.Server.URL}, src, localllm.Vulkan, "b10298-2.0.0"); err != nil || rel.Tag != "b10298-2.0.0" {
		t.Fatalf("%+v %v", rel, err)
	}
}
