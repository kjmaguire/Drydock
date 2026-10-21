// Package uiassets serves the built UI. The UI routes by path (Remix's SPA router: /tasks, /task/<id>), so a reload or a
// deep link asks the server for a path that is not a file; those GET requests get index.html and the router in the page
// resolves the path. Paths with an extension (scripts, styles, fonts) and the Wails runtime routes are never rewritten, so a
// missing asset is still a 404 and not a page.
package uiassets

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Fallback wraps next, the handler that serves files from fsys, so extensionless GET and HEAD requests for paths that are
// not in fsys are served as "/" (index.html).
func Fallback(fsys fs.FS, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pageRequest(fsys, r) {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			r2.URL.RawPath = ""
			next.ServeHTTP(w, r2)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Handler serves fsys over plain net/http with the fallback.
func Handler(fsys fs.FS) http.Handler {
	return Fallback(fsys, http.FileServer(http.FS(fsys)))
}

func pageRequest(fsys fs.FS, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	if p == "" || p == "/" || strings.HasPrefix(p, "/wails/") || path.Ext(p) != "" {
		return false
	}
	name := strings.TrimPrefix(path.Clean(p), "/")
	if !fs.ValidPath(name) {
		return false
	}
	_, err := fs.Stat(fsys, name)
	return errors.Is(err, fs.ErrNotExist)
}
