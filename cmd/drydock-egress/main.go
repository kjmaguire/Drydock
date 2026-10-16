// Command drydock-egress is the network allowlist proxy of a task sandbox (internal/sandbox/egress, docs/sandbox.md). Drydock
// builds it for Linux, puts it alone into a local image (docker import, no registry) and runs it as the only container of the
// task with a route out. It reads its allowlist from DRYDOCK_EGRESS_ALLOW and logs one JSON line per decision.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/kjmaguire/drydock/internal/sandbox/egress"
)

func main() {
	opts, err := egress.FromEnv(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	opts.Log = egress.JSONLog(os.Stdout)
	p, err := egress.New(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	addr := os.Getenv("DRYDOCK_EGRESS_LISTEN")
	if addr == "" {
		addr = ":3128"
	}
	// requests to a proxy are small: the headers are limited, and a connection nobody uses is closed (a CONNECT tunnel is hijacked
	// and has its own idle timeout in the proxy)
	srv := &http.Server{Addr: addr, Handler: p, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	fmt.Fprintf(os.Stderr, "drydock-egress listening on %s, %d allowlist entries\n", addr, len(opts.Allow))
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
