package localllm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Embedding texts with the embedding server (decision P120): the one use Drydock makes of it is ranking the passages a pack
// search found by how close their meaning is to the query (internal/packs, Options.Embedder). The server runs on 127.0.0.1 with
// a key only this process holds, so nothing of the texts leaves the PC.

// MaxEmbedInputs is the most texts one Embed call sends: a pack search ranks at most a few dozen passages.
const MaxEmbedInputs = 64

// ErrNoEmbeddings is what Embed answers when no embedding server runs.
var ErrNoEmbeddings = errors.New("no embedding model is served")

// Embed asks the embedding server for one vector per text (POST /v1/embeddings, OpenAI's shape, which llama-server answers with
// --embeddings), in the order given. It answers ErrNoEmbeddings when the server is not an embedding server that runs.
func (s *Server) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(texts) > MaxEmbedInputs {
		return nil, fmt.Errorf("%d texts at once is more than %d", len(texts), MaxEmbedInputs)
	}
	s.mu.Lock()
	st, key := s.st, s.cur.APIKey
	s.mu.Unlock()
	if st.State != "running" || !st.Embeddings {
		return nil, ErrNoEmbeddings
	}
	body, _ := json.Marshal(map[string]any{"input": texts, "model": st.ModelID})
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(st.Port)+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	hc := s.HTTP
	if hc == nil {
		tr := &http.Transport{Proxy: nil}
		defer tr.CloseIdleConnections()
		hc = &http.Client{Transport: tr}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the embedding server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the embedding server answered %d: %s", resp.StatusCode, errorMessage(resp.Body))
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("the embedding server's answer: %w", err)
	}
	vecs := make([][]float64, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(texts) || vecs[d.Index] != nil || len(d.Embedding) == 0 {
			return nil, errors.New("the embedding server's answer does not match the texts sent")
		}
		vecs[d.Index] = d.Embedding
	}
	for _, v := range vecs {
		if v == nil {
			return nil, errors.New("the embedding server left a text without a vector")
		}
	}
	return vecs, nil
}
