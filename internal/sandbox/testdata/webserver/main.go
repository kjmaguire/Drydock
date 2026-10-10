// Command webserver is a test service for the sandbox tests: it answers every request with a greeting and its host name.
package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("PORT")
	if addr == "" {
		addr = "80"
	}
	host, _ := os.Hostname()
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "hello from %s", host) })
	if err := http.ListenAndServe(":"+addr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
