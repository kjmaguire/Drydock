// Package httpapi is the development transport for the UI: the same core.Methods registry the Wails bridge exposes, over
// HTTP and Server-Sent Events, so the real React UI can be driven against the real Go core in a browser (Playwright in CI
// and in the cloud sandbox). It listens on loopback only and every request must carry the per-process token; the Host and
// Origin headers are checked so a web page open in the user's browser cannot reach it (DNS rebinding, cross-site POST).
// It is a development and test transport and is not part of the shipped Windows app, which uses Wails services.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/uiassets"
)

// Server serves the API and, if StaticDir is set, the built UI.
type Server struct {
	Core      *core.Core
	Token     string
	StaticDir string
	// AllowedOrigins are extra origins (for example the Vite dev server) allowed to call the API.
	AllowedOrigins []string
}

type envelope struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rpc/{method}", s.guard(s.rpc))
	mux.HandleFunc("GET /events", s.guard(s.sse))
	if s.StaticDir != "" {
		mux.Handle("/", uiassets.Handler(os.DirFS(s.StaticDir)))
	}
	return mux
}

func hostOK(h string) bool {
	host, _, err := net.SplitHostPort(h)
	if err != nil {
		host = h
	}
	return host == "127.0.0.1" || host == "localhost" || host == "[::1]" || host == "::1"
}

func (s *Server) originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true // not a browser cross-origin request
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if hostOK(u.Host) && u.Host == r.Host {
		return true
	}
	for _, a := range s.AllowedOrigins {
		if a == o {
			return true
		}
	}
	return false
}

func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !hostOK(r.Host) || !s.originOK(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		tok := r.Header.Get("X-Drydock-Token")
		if tok == "" {
			tok = r.URL.Query().Get("token") // EventSource cannot set headers
		}
		if s.Token == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Headers", "X-Drydock-Token, Content-Type")
		}
		h(w, r)
	}
}

func (s *Server) rpc(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil && err.Error() != "EOF" {
		writeJSON(w, http.StatusBadRequest, envelope{Error: "invalid JSON body"})
		return
	}
	res, err := s.Core.Call(r.Context(), r.PathValue("method"), raw)
	if err != nil {
		code := http.StatusBadRequest
		if strings.HasPrefix(err.Error(), "unknown method") {
			code = http.StatusNotFound
		}
		writeJSON(w, code, envelope{Error: err.Error()})
		return
	}
	if res == nil {
		res = json.RawMessage("null")
	}
	writeJSON(w, http.StatusOK, envelope{Result: res})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// sse streams events after ?after=<seq>: it subscribes first, replays the persisted backlog, then forwards live events,
// skipping any it already sent. If the subscription falls behind and is closed, it replays again from the last sent
// sequence and resubscribes (brief 24: the UI is a projection of the persisted log).
func (s *Server) sse(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	if id := r.Header.Get("Last-Event-ID"); id != "" { // EventSource reconnects with the last id it saw
		if n, err := strconv.ParseUint(id, 10, 64); err == nil && n > after {
			after = n
		}
	}
	var f events.Filter
	if t := r.URL.Query().Get("types"); t != "" {
		f.Types = strings.Split(t, ",")
	}
	f.ProjectID = r.URL.Query().Get("projectId")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	ctx := r.Context()
	send := func(e events.Event) bool {
		b, _ := json.Marshal(e)
		_, err := fmt.Fprintf(w, "id: %d\nevent: drydock\ndata: %s\n\n", e.Seq, b)
		return err == nil
	}
	s.Core.Follow(ctx, after, f, send, fl.Flush)
}

// Listen serves on 127.0.0.1:port (0 picks a free port) and returns the address and a shutdown function.
func (s *Server) Listen(port int) (addr string, shutdown func(context.Context) error, err error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), srv.Shutdown, nil
}
