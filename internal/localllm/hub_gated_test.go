package localllm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
)

// A gated repository's files are downloaded with the person's Hugging Face token (decision P111): without one the download says
// the model is gated and where to accept its licence; with a token the account has no access for, it says that; with the right
// one the file comes. The token is the Authorization header of the download from the Hub's own address only: not of the
// listings, and not of the CDN the Hub redirects the download to.
func TestAGatedModelIsDownloadedWithTheTokenAndTheTokenGoesNowhereElse(t *testing.T) {
	fx := llamatest.NewHub(t)
	fx.StartCDN()
	file := bytesOf(5000, 3)
	fx.AddGated("meta-llama/Llama-3.2-1B-Instruct-GGUF", "hf_right", map[string][]byte{"Llama-3.2-1B-Instruct-Q4_K_M.gguf": file})
	token := ""
	hub := &localllm.Hub{BaseURL: fx.URL, Token: func() string { return token }}
	ctx := context.Background()
	vs, err := hub.Variants(ctx, "meta-llama/Llama-3.2-1B-Instruct-GGUF")
	if err != nil || len(vs) != 1 {
		t.Fatalf("a gated repository's files are listed: %+v %v", vs, err)
	}
	dir := t.TempDir()
	var gated *localllm.GatedError
	err = hub.Download(ctx, "meta-llama/Llama-3.2-1B-Instruct-GGUF", vs[0], dir, nil)
	if !errors.As(err, &gated) || gated.HasToken || !strings.Contains(err.Error(), "gated model") ||
		!strings.Contains(err.Error(), "https://huggingface.co/meta-llama/Llama-3.2-1B-Instruct-GGUF") || !strings.Contains(err.Error(), "add a Hugging Face token") {
		t.Fatalf("without a token: %v", err)
	}
	token = "hf_wrong"
	err = hub.Download(ctx, "meta-llama/Llama-3.2-1B-Instruct-GGUF", vs[0], dir, nil)
	if !errors.As(err, &gated) || !gated.HasToken || gated.Status != 403 || !strings.Contains(err.Error(), "no access") || !strings.Contains(err.Error(), "accept its licence at https://huggingface.co/meta-llama/") {
		t.Fatalf("with a token without access: %v", err)
	}
	if strings.Contains(err.Error(), "hf_wrong") {
		t.Fatal("the error carries the token")
	}
	token = "hf_right"
	if err := hub.Download(ctx, "meta-llama/Llama-3.2-1B-Instruct-GGUF", vs[0], dir, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "Llama-3.2-1B-Instruct-Q4_K_M.gguf")); string(got) != string(file) {
		t.Fatal("the gated file was not downloaded")
	}
	resolve := "/meta-llama/Llama-3.2-1B-Instruct-GGUF/resolve/main/Llama-3.2-1B-Instruct-Q4_K_M.gguf"
	if seen := fx.AuthSeen(resolve); strings.Join(seen, ",") != ",Bearer hf_wrong,Bearer hf_right" {
		t.Fatalf("the Hub's download saw %q", seen)
	}
	if seen := fx.AuthSeen("cdn:/cdn" + resolve); len(seen) != 1 || seen[0] != "" {
		t.Fatalf("the token followed the redirect to the CDN: %q", seen)
	}
	for _, p := range []string{"/api/models/meta-llama/Llama-3.2-1B-Instruct-GGUF/tree/main"} {
		for _, a := range fx.AuthSeen(p) {
			if a != "" {
				t.Fatalf("%s was sent the token", p)
			}
		}
	}
	// a token Hugging Face does not accept at all is said so (401 with a token)
	if msg := (&localllm.GatedError{Repo: "a/b", Status: 401, HasToken: true}).Error(); !strings.Contains(msg, "did not accept the token") {
		t.Fatal(msg)
	}
}

// The projector a variant downloads is the one in its own folder before one elsewhere, and of those F16, then BF16, then Q8_0; it
// is downloaded and checked with the model, recorded in the store, and a model whose projector went missing is not listed.
func TestTheProjectorIsPickedDownloadedAndKept(t *testing.T) {
	fx := llamatest.NewHub(t)
	fx.AddModel("ggml-org/gemma-3-4b-it-GGUF", 10, nil, map[string][]byte{
		"gemma-3-4b-it-Q4_K_M.gguf": bytesOf(3000, 1), "mmproj-model-bf16.gguf": bytesOf(300, 2), "mmproj-model-f16.gguf": bytesOf(310, 3),
		"mmproj-Q8_0.gguf": bytesOf(200, 4), "Q8/gemma-3-4b-it-Q8_0.gguf": bytesOf(5000, 5), "Q8/mmproj-Q8_0.gguf": bytesOf(210, 6),
	})
	hub := &localllm.Hub{BaseURL: fx.URL}
	vs, err := hub.Variants(context.Background(), "ggml-org/gemma-3-4b-it-GGUF")
	if err != nil || len(vs) != 2 {
		t.Fatalf("%+v %v", vs, err)
	}
	if vs[0].Name != "Q4_K_M" || vs[0].Projector == nil || vs[0].Projector.Path != "mmproj-model-f16.gguf" || vs[0].Size != 3310 {
		t.Fatalf("the root variant takes the F16 projector beside it: %+v", vs[0])
	}
	if vs[1].Name != "Q8_0" || vs[1].Projector == nil || vs[1].Projector.Path != "Q8/mmproj-Q8_0.gguf" || vs[1].Size != 5210 {
		t.Fatalf("the folder's variant takes its folder's projector: %+v", vs[1])
	}
	st := &localllm.Store{Dir: t.TempDir()}
	dir := st.VariantDir("ggml-org/gemma-3-4b-it-GGUF", vs[0].Name)
	var last localllm.DownloadProgress
	if err := hub.Download(context.Background(), "ggml-org/gemma-3-4b-it-GGUF", vs[0], dir, func(p localllm.DownloadProgress) { last = p }); err != nil {
		t.Fatal(err)
	}
	if last.Done != 3310 || last.Total != 3310 {
		t.Fatalf("%+v", last)
	}
	m, err := st.Put("ggml-org/gemma-3-4b-it-GGUF", vs[0])
	if err != nil || m.Projector != filepath.Join(dir, "mmproj-model-f16.gguf") || m.Size != 3310 {
		t.Fatalf("%+v %v", m, err)
	}
	if l := st.List(); len(l) != 1 || l[0].Projector != m.Projector {
		t.Fatalf("%+v", l)
	}
	// the person's mark that it is (or is not) an embedding model is kept with it
	yes := true
	if err := st.Mark(m.ID, &yes); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Get(m.ID); got.Mark == nil || !*got.Mark || got.Projector == "" {
		t.Fatalf("%+v", got)
	}
	if err := st.Mark(m.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Get(m.ID); got.Mark != nil {
		t.Fatalf("%+v", got)
	}
	_ = os.Remove(m.Projector)
	if l := st.List(); len(l) != 0 {
		t.Fatalf("a model without its projector is listed: %+v", l)
	}
}
