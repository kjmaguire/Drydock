//go:build windows

// conpty probes aymanbagabas/go-pty on Windows: spawn a command under ConPTY, capture raw VT output, strip with x/ansi.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aymanbagabas/go-pty"
	"github.com/charmbracelet/x/ansi"
)

func main() {
	p, err := pty.New()
	if err != nil {
		fmt.Println("pty.New:", err)
		os.Exit(2)
	}
	// p is closed once, below: go-pty's ConPTY Close is not idempotent, and a second Close (this probe had a deferred one
	// too) corrupted the heap on exit (0xc0000374, windows-probes run 2026-10-10)
	if err := p.Resize(120, 30); err != nil {
		fmt.Println("resize:", err)
	}
	c := p.Command("powershell.exe", "-NoProfile", "-Command", "Write-Host 'hello-from-conpty'; Write-Host (Get-Location).Path; exit 7")
	if err := c.Start(); err != nil {
		fmt.Println("start:", err)
		os.Exit(2)
	}
	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(p)
		done <- b
	}()
	waitErr := make(chan error, 1)
	go func() { waitErr <- c.Wait() }()
	var raw []byte
	select {
	case err := <-waitErr:
		fmt.Println("wait err (expect exit status 7):", err)
	case <-time.After(30 * time.Second):
		fmt.Println("TIMEOUT waiting for child")
		_ = c.Process.Kill()
	}
	p.Close()
	select {
	case raw = <-done:
	case <-time.After(5 * time.Second):
	}
	fmt.Printf("raw bytes: %d\n", len(raw))
	fmt.Printf("raw (quoted, first 400): %q\n", string(raw[:min(len(raw), 400)]))
	stripped := ansi.Strip(string(raw))
	fmt.Printf("stripped: %q\n", stripped)
	if !strings.Contains(stripped, "hello-from-conpty") {
		fmt.Println("FAIL: marker missing from stripped output")
		os.Exit(1)
	}
	fmt.Println("RESULT conpty ok")
}
