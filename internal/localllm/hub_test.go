package localllm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
)

func bytesOf(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7) ^ seed
	}
	return b
}

// The Hub's GGUF repositories are listed (ggml-org's first, gated ones never), and a repository's files are grouped into
// variants by quantization, a split model into one variant of several files, projector files left out.
func TestTheHubIsReadIntoVariants(t *testing.T) {
	fx := llamatest.NewHub(t)
	fx.AddModel("ggml-org/gpt-oss-20b-GGUF", 900000, []string{"reasoning"}, map[string][]byte{"gpt-oss-20b-mxfp4.gguf": bytesOf(3000, 1), "README.md": []byte("x")})
	fx.AddModel("unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF", 500000, nil, map[string][]byte{
		"Qwen3-Coder-30B-A3B-Instruct-Q4_K_M.gguf":                   bytesOf(4000, 2),
		"Qwen3-Coder-30B-A3B-Instruct-Q8_0.gguf":                     bytesOf(8000, 3),
		"BF16/Qwen3-Coder-30B-A3B-Instruct-BF16-00001-of-00002.gguf": bytesOf(5000, 4),
		"BF16/Qwen3-Coder-30B-A3B-Instruct-BF16-00002-of-00002.gguf": bytesOf(5000, 5),
		"mmproj-F16.gguf": bytesOf(100, 6),
	})
	fx.AddGated("meta-llama/Llama-3.3-70B-Instruct-GGUF")
	hub := &localllm.Hub{BaseURL: fx.URL}
	ctx := context.Background()
	sug, err := hub.Suggested(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sug) != 1 || sug[0].ID != "ggml-org/gpt-oss-20b-GGUF" || sug[0].Downloads != 900000 || strings.Join(sug[0].Tags, ",") != "reasoning" {
		t.Fatalf("%+v", sug)
	}
	found, err := hub.Search(ctx, "gguf")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ID != "ggml-org/gpt-oss-20b-GGUF" || found[1].ID != "unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF" {
		t.Fatalf("gated offered or order wrong: %+v", found)
	}
	vs, err := hub.Variants(ctx, "unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 || vs[0].Name != "Q4_K_M" || vs[0].Size != 4000 || vs[1].Name != "Q8_0" || vs[2].Name != "BF16" || len(vs[2].Files) != 2 || vs[2].Size != 10000 {
		t.Fatalf("%+v", vs)
	}
	if vs[0].Files[0].SHA256 == "" || vs[2].Notes != nil {
		t.Fatalf("%+v", vs)
	}
	vs, err = hub.Variants(ctx, "ggml-org/gpt-oss-20b-GGUF")
	if err != nil || len(vs) != 1 || vs[0].Name != "MXFP4" || vs[0].Quant != "MXFP4" {
		t.Fatalf("%+v %v", vs, err)
	}
	if _, err := hub.Variants(ctx, "../etc"); err == nil {
		t.Fatal("a bad repository id was accepted")
	}
}

// A download is checked against the Hub's SHA-256 (a wrong file is deleted), resumes a part that was cut off, and skips a
// file that is already there and right.
func TestModelFilesAreDownloadedCheckedAndResumed(t *testing.T) {
	fx := llamatest.NewHub(t)
	a, b := bytesOf(20000, 9), bytesOf(12000, 8)
	fx.AddModel("acme/tiny-GGUF", 10, nil, map[string][]byte{"tiny-Q4_0-00001-of-00002.gguf": a, "tiny-Q4_0-00002-of-00002.gguf": b})
	hub := &localllm.Hub{BaseURL: fx.URL}
	ctx := context.Background()
	vs, err := hub.Variants(ctx, "acme/tiny-GGUF")
	if err != nil || len(vs) != 1 || len(vs[0].Files) != 2 {
		t.Fatalf("%+v %v", vs, err)
	}
	dir := t.TempDir()
	// a part of the first file is already there: the rest is fetched with a Range request; the second file's part is whole
	// (Drydock closed between the last byte and the check): it is checked and kept without asking the Hub for more
	_ = os.WriteFile(filepath.Join(dir, "tiny-Q4_0-00001-of-00002.gguf.part"), a[:5000], 0o644)
	_ = os.WriteFile(filepath.Join(dir, "tiny-Q4_0-00002-of-00002.gguf.part"), b, 0o644)
	var last localllm.DownloadProgress
	if err := hub.Download(ctx, "acme/tiny-GGUF", vs[0], dir, func(p localllm.DownloadProgress) { last = p }); err != nil {
		t.Fatal(err)
	}
	if last.Done != 32000 || last.Total != 32000 {
		t.Fatalf("%+v", last)
	}
	for name, want := range map[string][]byte{"tiny-Q4_0-00001-of-00002.gguf": a, "tiny-Q4_0-00002-of-00002.gguf": b} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if fx.Requests.Load() != 1 {
		t.Fatalf("%d requests", fx.Requests.Load())
	}
	// again: nothing is fetched
	if err := hub.Download(ctx, "acme/tiny-GGUF", vs[0], dir, nil); err != nil || fx.Requests.Load() != 1 {
		t.Fatal(err, fx.Requests.Load())
	}
	// a whole-sized part with the wrong bytes is thrown away and the file fetched again, not asked for from its end
	dir3 := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir3, "tiny-Q4_0-00001-of-00002.gguf.part"), bytesOf(20000, 1), 0o644)
	if err := hub.Download(ctx, "acme/tiny-GGUF", vs[0], dir3, nil); err != nil || fx.Requests.Load() != 3 {
		t.Fatal(err, fx.Requests.Load())
	}
	if got, _ := os.ReadFile(filepath.Join(dir3, "tiny-Q4_0-00001-of-00002.gguf")); string(got) != string(a) {
		t.Fatal("the wrong part was kept")
	}
	// a file whose bytes changed on the Hub side does not match: deleted and refused
	bad := vs[0]
	sum := sha256.Sum256([]byte("other"))
	bad.Files = []localllm.File{{Path: "tiny-Q4_0-00002-of-00002.gguf", Size: uint64(len(b)), SHA256: hex.EncodeToString(sum[:])}}
	dir2 := t.TempDir()
	if err := hub.Download(ctx, "acme/tiny-GGUF", bad, dir2, nil); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir2); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

// Two files of the same quantization in different folders (a repository that keeps its split quants in folders) are two
// variants with two names, since the name is half the model id and the folder a download goes to.
func TestVariantsOfTheSameQuantizationInFoldersStayApart(t *testing.T) {
	fx := llamatest.NewHub(t)
	fx.AddModel("acme/folders-GGUF", 10, nil, map[string][]byte{
		"Model-Q8_0.gguf":                              bytesOf(300, 1),
		"Q8_0/Model-Q8_0-00001-of-00002.gguf":          bytesOf(200, 2),
		"Q8_0/Model-Q8_0-00002-of-00002.gguf":          bytesOf(200, 3),
		"Model-Q4_K_M.gguf":                            bytesOf(100, 4),
		"BF16/Model-BF16.gguf":                         bytesOf(600, 5),
		"other/Model-BF16.gguf":                        bytesOf(650, 6),
		"nested/deeper/Model-BF16-00001-of-00001.gguf": bytesOf(660, 7),
	})
	hub := &localllm.Hub{BaseURL: fx.URL}
	vs, err := hub.Variants(context.Background(), "acme/folders-GGUF")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range vs {
		names = append(names, v.Name)
		if _, _, ok := localllm.SplitModelID(localllm.ModelID("acme/folders-GGUF", v.Name)); !ok {
			t.Fatalf("%q is not usable in a model id", v.Name)
		}
	}
	if strings.Join(names, " ") != "Q4_K_M Model-Q8_0 Q8_0-Model-Q8_0 BF16-Model-BF16 other-Model-BF16 nested-deeper-Model-BF16" {
		t.Fatalf("%v", names)
	}
}

func TestTheStoreKeepsDownloadedModels(t *testing.T) {
	st := &localllm.Store{Dir: t.TempDir()}
	v := localllm.Variant{Name: "Q4_0", Size: 3, Files: []localllm.File{{Path: "sub/tiny-Q4_0.gguf", Size: 3}}}
	dir := st.VariantDir("acme/tiny-GGUF", "Q4_0")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "tiny-Q4_0.gguf"), []byte("abc"), 0o644)
	m, err := st.Put("acme/tiny-GGUF", v)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "acme/tiny-GGUF:Q4_0" || len(m.Files) != 1 || filepath.Base(m.Files[0]) != "tiny-Q4_0.gguf" {
		t.Fatalf("%+v", m)
	}
	if l := st.List(); len(l) != 1 || l[0].ID != m.ID || st.Used() != 3 {
		t.Fatalf("%+v", l)
	}
	if _, err := st.Get("acme/tiny-GGUF:Q8_0"); err == nil {
		t.Fatal("a model that is not there was found")
	}
	if err := st.Remove(m.ID); err != nil {
		t.Fatal(err)
	}
	if l := st.List(); len(l) != 0 {
		t.Fatalf("%+v", l)
	}
	if _, err := os.Stat(filepath.Join(st.Dir, "acme")); !os.IsNotExist(err) {
		t.Fatal("the empty repository folder stayed")
	}
	if r, va, ok := localllm.SplitModelID("acme/tiny-GGUF:Q4_0"); !ok || r != "acme/tiny-GGUF" || va != "Q4_0" {
		t.Fatal("split")
	}
	if _, _, ok := localllm.SplitModelID("../x:y"); ok {
		t.Fatal("bad id accepted")
	}
}
