package uiassets

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestFallbackServesIndexForAppPaths(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":             {Data: []byte("<html>app</html>")},
		"assets/app-1.js":        {Data: []byte("console.log(1)")},
		"assets/style.css":       {Data: []byte("body{}")},
		"docs/readme/index.html": {Data: []byte("docs")},
	}
	h := Handler(fsys)
	cases := []struct {
		method, path string
		code         int
		body         string
	}{
		{"GET", "/", 200, "<html>app</html>"},
		{"GET", "/tasks", 200, "<html>app</html>"},
		{"GET", "/task/abc/run/r1", 200, "<html>app</html>"},
		{"HEAD", "/room/x", 200, ""},
		{"GET", "/assets/app-1.js", 200, "console.log(1)"},
		{"GET", "/assets/missing.js", 404, ""},
		{"GET", "/favicon.ico", 404, ""},
		{"POST", "/tasks", 200, ""}, // not rewritten: FileServer answers a POST to a missing path itself
		{"GET", "/wails/runtime", 404, ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		body, _ := io.ReadAll(rec.Body)
		if c.method == "POST" {
			if string(body) == "<html>app</html>" {
				t.Errorf("POST %s was served the page", c.path)
			}
			continue
		}
		if rec.Code != c.code {
			t.Errorf("%s %s: code %d, want %d", c.method, c.path, rec.Code, c.code)
		}
		if c.body != "" && string(body) != c.body {
			t.Errorf("%s %s: body %q, want %q", c.method, c.path, body, c.body)
		}
	}
}

func TestFallbackLeavesExistingDirectoriesAlone(t *testing.T) {
	fsys := fstest.MapFS{"index.html": {Data: []byte("app")}, "docs/index.html": {Data: []byte("docs")}}
	var seen string
	h := Fallback(fsys, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.URL.Path }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/docs", nil))
	if seen != "/docs" {
		t.Fatalf("an existing directory was rewritten to %q", seen)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/../../etc/passwd", nil))
	if seen != "/" && seen != "/../../etc/passwd" {
		t.Fatalf("unexpected path %q", seen)
	}
}
