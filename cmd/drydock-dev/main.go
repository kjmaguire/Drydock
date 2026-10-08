// Command drydock-dev runs the Drydock core with the development HTTP transport so the UI can be driven in a browser. It is
// a development and test tool, not the shipped application (the Windows app is the Wails shell).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/transport/httpapi"
)

func main() {
	data := flag.String("data", "", "data directory (default: a new temp directory)")
	ui := flag.String("ui", "ui/dist", "built UI to serve")
	port := flag.Int("port", 0, "port on 127.0.0.1 (0 = any)")
	user := flag.String("user", "", "human display name")
	origin := flag.String("allow-origin", "", "extra allowed Origin (for the Vite dev server)")
	flag.Parse()
	if *data == "" {
		d, err := os.MkdirTemp("", "drydock-dev-")
		if err != nil {
			log.Fatal(err)
		}
		*data = d
	}
	c, err := core.Open(context.Background(), core.Config{DataDir: *data, User: *user})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	s := &httpapi.Server{Core: c, Token: hex.EncodeToString(b), StaticDir: *ui}
	if *origin != "" {
		s.AllowedOrigins = []string{*origin}
	}
	addr, shutdown, err := s.Listen(*port)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("DRYDOCK_DEV http://%s/?token=%s data=%s\n", addr, s.Token, *data)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	_ = shutdown(context.Background())
}
