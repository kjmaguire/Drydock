package localllm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Start calls the server ready only when /health answers and the server accepts the key it was started with (audit
// 2026-10-16, L32): a different program that took the port between the check and the start answers /health too.
func TestOnlyAServerThatAcceptsTheKeyIsOurs(t *testing.T) {
	const key = "k-for-this-start"
	serve := func(h http.HandlerFunc) string {
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		return srv.URL
	}
	ours := serve(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" && r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"model_alias":"m","total_slots":1}`))
	})
	notJSON := serve(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>a dev server</html>`)) })
	refuses := serve(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })

	s := &Server{}
	hc := &http.Client{Timeout: 2 * time.Second}
	ctx := context.Background()
	if !s.ours(ctx, hc, ours, key) {
		t.Error("a server that accepts the key was not taken for ours")
	}
	if s.ours(ctx, hc, ours, "another key") {
		t.Error("a key the server does not accept passed")
	}
	// a program that answers something else than JSON, or refuses, is not ours (one that answers JSON to anything cannot be told
	// from the server by this check, which is why the port is checked free first and the key travels in the environment)
	if s.ours(ctx, hc, notJSON, key) || s.ours(ctx, hc, refuses, key) {
		t.Error("a program that is not llama-server was taken for it")
	}
	if !s.ours(ctx, hc, notJSON, "") {
		t.Error("without a key nothing can be told apart and /health is the whole check")
	}
}

// The server's environment keeps what a GPU driver needs to be found (a Vulkan build saw no GPU without them) and still none
// of the app's tokens.
func TestTheServerEnvironmentKeepsTheGPUDriversSettings(t *testing.T) {
	for k, v := range map[string]string{
		"DRIVERDATA": `C:\Windows\System32\Drivers\DriverData`, "SYSTEMDRIVE": "C:", "VK_ICD_FILENAMES": "x.json", "GGML_VK_VISIBLE_DEVICES": "0",
		"CUDA_PATH_V12_8": `C:\cuda`, "GITHUB_TOKEN": "ghp_secret", "OPENAI_API_KEY": "sk-secret",
	} {
		t.Setenv(k, v)
	}
	got := map[string]bool{}
	for _, kv := range childEnv() {
		k, _, _ := strings.Cut(kv, "=")
		got[strings.ToUpper(k)] = true
	}
	for _, want := range []string{"DRIVERDATA", "SYSTEMDRIVE", "VK_ICD_FILENAMES", "GGML_VK_VISIBLE_DEVICES", "CUDA_PATH_V12_8"} {
		if !got[want] {
			t.Errorf("%s is not passed on", want)
		}
	}
	for _, secret := range []string{"GITHUB_TOKEN", "OPENAI_API_KEY"} {
		if got[secret] {
			t.Errorf("%s reached the server", secret)
		}
	}
}
